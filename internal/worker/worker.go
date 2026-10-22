package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/store"
)

type Worker struct {
	Store   *store.Store
	Clients *connectors.Clients
	Log     *slog.Logger
	BaseURL string
}

func (w *Worker) Run(ctx context.Context) error {
	unlock, err := w.Store.Lock(ctx, 936422019)
	if err != nil {
		return errors.New("un’altra istanza del worker è già attiva: il deploy deve usare una singola replica")
	}
	defer unlock()
	if err = w.Store.Recover(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); w.actions(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); w.sources(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); w.comparisons(ctx) }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-ticker.C:
			if err = w.Store.RefreshRuns(ctx); err != nil {
				w.Log.Error("aggiornamento esecuzioni fallito")
			}
			w.notifications(ctx)
			if err = w.Store.CleanupAuth(ctx); err != nil {
				w.Log.Error("pulizia sessioni fallita")
			}
		}
	}
}
func (w *Worker) actions(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			step, err := w.Store.Claim(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				w.Log.Error("lettura coda fallita")
				continue
			}
			attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			result, err := w.Clients.Execute(attemptCtx, step.Action)
			cancel()
			// Persist completion even after shutdown; otherwise recovery must treat it as uncertain.
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
			status, message, next := Outcome(step.Attempts, err, time.Now())
			if result.Warning != "" {
				status = "warning"
				message = result.Warning
			}
			if err != nil && connectors.Classify(err).Auth {
				if e := w.Store.ConnectionState(saveCtx, step.Action.ConnectionID, "paused", message); e != nil {
					w.Log.Error("sospensione connessione fallita")
				}
			}
			if e := w.Store.Finish(saveCtx, step.ID, status, result.Reference, message, next); e != nil {
				w.Log.Error("salvataggio esito fallito", "step_id", step.ID)
			}
			if status == "failed" || status == "uncertain" || status == "warning" {
				if e := w.Store.Notify(saveCtx, fmt.Sprintf("step:%d:%s:%d", step.ID, status, step.Attempts), fmt.Sprintf("Esecuzione #%d: %s. Aprire il dettaglio nell’applicazione.", step.RunID, message)); e != nil {
					w.Log.Error("registrazione avviso fallita")
				}
			}
			saveCancel()
		}
	}
}
func Outcome(attempts int, err error, now time.Time) (string, string, time.Time) {
	if err == nil {
		return "succeeded", "", now
	}
	f := connectors.Classify(err)
	if f.Uncertain {
		return "uncertain", f.Message, now
	}
	if f.Auth {
		return "pending", f.Message, now.Add(time.Minute)
	}
	if f.Retryable && attempts < 3 {
		delay := 10 * time.Second
		if attempts >= 2 {
			delay = time.Minute
		}
		if f.After > delay {
			delay = f.After
		}
		return "pending", f.Message, now.Add(delay)
	}
	return "failed", f.Message, now
}
func (w *Worker) sources(ctx context.Context) {
	type receiver struct {
		signature string
		cancel    context.CancelFunc
	}
	active := map[int]receiver{}
	var wg sync.WaitGroup
	defer func() {
		for _, r := range active {
			r.cancel()
		}
		wg.Wait()
	}()
	control := time.NewTicker(10 * time.Second)
	defer control.Stop()
	recovery := time.NewTicker(5 * time.Minute)
	defer recovery.Stop()
	refresh := func(recoverNow bool) {
		connections, err := w.Store.Connections(ctx)
		if err != nil {
			w.Log.Error("lettura sorgenti fallita")
			return
		}
		present := map[int]bool{}
		for _, conn := range connections {
			if conn.Provider != "gmail" || conn.Status != "ready" {
				continue
			}
			present[conn.ID] = true
			signature := conn.Config["email"] + conn.Config["topic"] + conn.Config["subscription"]
			r, found := active[conn.ID]
			if found && r.signature != signature {
				r.cancel()
				delete(active, conn.ID)
				found = false
			}
			if !found {
				child, cancel := context.WithCancel(ctx)
				active[conn.ID] = receiver{signature, cancel}
				wg.Add(1)
				go func(conn model.Connection) { defer wg.Done(); w.receive(child, conn) }(conn)
			}
			if recoverNow || !found || conn.WatchExpires.Before(time.Now().Add(6*24*time.Hour)) {
				wg.Add(1)
				go func(conn model.Connection) {
					defer wg.Done()
					job, cancel := context.WithTimeout(ctx, 4*time.Minute)
					defer cancel()
					if conn.WatchExpires.Before(time.Now().Add(6 * 24 * time.Hour)) {
						if err := w.Clients.Renew(job, conn); err != nil {
							w.sourceError(job, conn, err)
							return
						}
					}
					if err := w.Clients.Sync(job, conn); err != nil {
						w.sourceError(job, conn, err)
					}
				}(conn)
			}
		}
		for id, r := range active {
			if !present[id] {
				r.cancel()
				delete(active, id)
			}
		}
	}
	refresh(true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-control.C:
			refresh(false)
		case <-recovery.C:
			refresh(true)
		}
	}
}
func (w *Worker) sourceError(ctx context.Context, conn model.Connection, err error) {
	f := connectors.Classify(err)
	if f.Auth {
		if e := w.Store.ConnectionState(ctx, conn.ID, "paused", f.Message); e != nil {
			w.Log.Error("salvataggio stato casella fallito")
		}
		return
	}
	if e := w.Store.Notify(ctx, fmt.Sprintf("source:%d:%s:%s", conn.ID, time.Now().Format("2006-01-02"), f.Message), fmt.Sprintf("Casella %s: sincronizzazione non riuscita. Il recupero automatico resta attivo.", conn.Name)); e != nil {
		w.Log.Error("salvataggio avviso sorgente fallito")
	}
}
func (w *Worker) receive(ctx context.Context, conn model.Connection) {
	parts := strings.Split(conn.Config["subscription"], "/")
	if len(parts) != 4 {
		return
	}
	for ctx.Err() == nil {
		client, err := pubsub.NewClient(ctx, parts[1])
		if err == nil {
			subscriber := client.Subscriber(conn.Config["subscription"])
			subscriber.ReceiveSettings.NumGoroutines = 1
			subscriber.ReceiveSettings.MaxOutstandingMessages = 1
			err = subscriber.Receive(ctx, func(deliveryCtx context.Context, message *pubsub.Message) {
				var event struct {
					Email     string `json:"emailAddress"`
					HistoryID string `json:"historyId"`
				}
				if json.Unmarshal(message.Data, &event) != nil || !strings.EqualFold(event.Email, conn.Config["email"]) || event.HistoryID == "" {
					message.Ack()
					return
				}
				if err := w.Clients.Sync(deliveryCtx, conn); err != nil {
					w.sourceError(deliveryCtx, conn, err)
					message.Nack()
					return
				}
				message.Ack()
			})
			_ = client.Close()
		}
		if ctx.Err() != nil {
			return
		}
		w.Log.Warn("listener Pub/Sub non disponibile", "connection_id", conn.ID)
		if e := w.Store.Notify(ctx, fmt.Sprintf("pubsub:%d:%s", conn.ID, time.Now().Format("2006-01-02")), fmt.Sprintf("Casella %s: listener Pub/Sub non disponibile. Verificare credenziali ADC e permessi; il controllo di recupero resta attivo.", conn.Name)); e != nil {
			w.Log.Error("salvataggio avviso Pub/Sub fallito")
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (w *Worker) notifications(ctx context.Context) {
	connections, err := w.Store.Connections(ctx)
	if err != nil {
		return
	}
	var target model.Connection
	for _, conn := range connections {
		if conn.Provider == "slack" && conn.Status == "ready" && conn.Config["alert_channel"] != "" {
			target = conn
			break
		}
	}
	if target.ID == 0 {
		return
	}
	rows, err := w.Store.DB.QueryContext(ctx, `SELECT id,body,attempts FROM notifications WHERE delivered=FALSE AND next_at<=now() ORDER BY id LIMIT 5`)
	if err != nil {
		return
	}
	type notice struct {
		id, attempts int
		body         string
	}
	var notices []notice
	for rows.Next() {
		var n notice
		if rows.Scan(&n.id, &n.body, &n.attempts) != nil {
			rows.Close()
			return
		}
		notices = append(notices, n)
	}
	rows.Close()
	for _, n := range notices {
		err = w.Clients.Alert(ctx, target, n.body)
		if err == nil {
			_, err = w.Store.DB.ExecContext(ctx, `UPDATE notifications SET delivered=TRUE WHERE id=$1`, n.id)
		} else {
			delay := time.Duration(min(n.attempts+1, 30)) * time.Minute
			_, err = w.Store.DB.ExecContext(ctx, `UPDATE notifications SET attempts=attempts+1,next_at=$2 WHERE id=$1`, n.id, time.Now().Add(delay))
		}
		if err != nil {
			w.Log.Error("aggiornamento avviso fallito")
		}
	}
}
