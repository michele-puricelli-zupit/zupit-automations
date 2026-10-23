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
	"github.com/zupit-it/zupit-automations/internal/logging"
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
		w.Log.Error("Worker non avviato: verificare replica unica e disponibilità PostgreSQL", "event", "worker.lock_failed", logging.Error(err))
		return errors.New("un’altra istanza del worker è già attiva: il deploy deve usare una singola replica")
	}
	defer unlock()
	if err = w.Store.Recover(ctx); err != nil {
		w.Log.Error("Recupero delle operazioni interrotte fallito", "event", "worker.recovery_failed", logging.Error(err))
		return err
	}
	w.Log.Info("Worker attivo: coda azioni, ricezione Gmail e confronti disponibili", "event", "worker.started", "action_workers", 4, "gmail_recovery_interval", "5m")
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
			w.Log.Info("Worker arrestato", "event", "worker.stopped")
			return nil
		case <-ticker.C:
			if err = w.Store.RefreshRuns(ctx); err != nil {
				w.Log.Error("Aggiornamento stati esecuzioni fallito", "event", "runs.refresh_failed", logging.Error(err))
			}
			w.notifications(ctx)
			if err = w.Store.CleanupAuth(ctx); err != nil {
				w.Log.Error("Pulizia sessioni scadute fallita", "event", "sessions.cleanup_failed", logging.Error(err))
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
				w.Log.Error("Lettura coda azioni fallita", "event", "action.claim_failed", logging.Error(err))
				continue
			}
			w.executeStep(ctx, step)
		}
	}
}

func (w *Worker) executeStep(ctx context.Context, step model.Step) {
	log := w.Log.With("run_id", step.RunID, "step_id", step.ID, "connection_id", step.Action.ConnectionID, "action", step.Action.Kind, "attempt", step.Attempts)
	if step.Action.Kind == "jira_request" {
		log = log.With("queue_id", step.Action.QueueID)
	} else if step.Action.Kind == "jira" {
		log = log.With("board_id", step.Action.BoardID)
	}
	log.Info("Esecuzione azione avviata", "event", "action.started")
	started := time.Now()
	attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	result, err := w.Clients.Execute(attemptCtx, step.Action)
	cancel()
	// Persist completion even after shutdown; otherwise recovery must treat it as uncertain.
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer saveCancel()
	status, message, next := Outcome(step.Attempts, err, time.Now())
	if result.Warning != "" {
		status, message = "warning", result.Warning
	}
	if err != nil && connectors.Classify(err).Auth {
		if e := w.Store.ConnectionState(saveCtx, step.Action.ConnectionID, "paused", message); e != nil {
			log.Error("Sospensione connessione fallita", "event", "connection.pause_failed", logging.Error(e))
		} else {
			log.Warn("Connessione sospesa: ricollegarla dalla pagina Connessioni per riprendere la coda", "event", "connection.paused", logging.Error(err))
		}
	}
	attrs := []any{"event", "action.completed", "status", status, "duration_ms", time.Since(started).Milliseconds(), "reference", result.Reference}
	level := slog.LevelInfo
	if err != nil {
		attrs = append(attrs, logging.Error(err))
		level = slog.LevelError
	}
	switch status {
	case "pending":
		level = slog.LevelWarn
		attrs = append(attrs, "next_at", next, "hint", "Retry pianificato; una connessione sospesa deve essere ricollegata")
	case "warning":
		level = slog.LevelWarn
		attrs = append(attrs, "reason", result.Warning, "hint", "Azione già eseguita: verificare filtro e permessi della board/coda, senza ripetere la creazione")
	case "uncertain":
		attrs = append(attrs, "hint", "Controllare il servizio esterno prima di riconciliare o autorizzare un nuovo tentativo")
	case "failed":
		attrs = append(attrs, "hint", "Aprire l’esecuzione e correggere configurazione, campi o permessi; nessun retry automatico")
	}
	if e := w.Store.Finish(saveCtx, step.ID, status, result.Reference, message, next); e != nil {
		log.Error("Esito esterno non salvato: verificare il servizio prima di ripetere l’azione", "event", "action.persist_failed", "external_status", status, "reference", result.Reference, logging.Error(e))
	} else {
		log.Log(saveCtx, level, "Esecuzione azione conclusa", attrs...)
	}
	if status == "failed" || status == "uncertain" || status == "warning" {
		if e := w.Store.Notify(saveCtx, fmt.Sprintf("step:%d:%s:%d", step.ID, status, step.Attempts), fmt.Sprintf("Esecuzione #%d: %s. Aprire il dettaglio nell’applicazione.", step.RunID, message)); e != nil {
			log.Error("Registrazione avviso fallita", "event", "notification.enqueue_failed", logging.Error(e))
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
			w.Log.Error("Lettura sorgenti Gmail fallita", "event", "gmail.sources_failed", logging.Error(err))
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
					w.Log.Info("Controllo di recupero Gmail avviato", "event", "gmail.recovery_started", "connection_id", conn.ID)
					if conn.WatchExpires.Before(time.Now().Add(6 * 24 * time.Hour)) {
						if err := w.Clients.Renew(job, conn); err != nil {
							w.sourceError(job, conn, "gmail.watch", err)
							return
						}
					}
					if err := w.Clients.Sync(job, conn); err != nil {
						w.sourceError(job, conn, "gmail.history", err)
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
func (w *Worker) sourceError(ctx context.Context, conn model.Connection, operation string, err error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	f := connectors.Classify(err)
	log := w.Log.With("connection_id", conn.ID, "operation", operation)
	if f.Auth {
		log.Error("Autorizzazione Gmail non disponibile: necessario un nuovo collegamento", "event", "gmail.source_failed", logging.Error(err), "hint", "Aprire Connessioni e autorizzare nuovamente la casella")
		if e := w.Store.ConnectionState(ctx, conn.ID, "paused", f.Message); e != nil {
			log.Error("Salvataggio stato casella fallito", "event", "connection.pause_failed", logging.Error(e))
		}
		return
	}
	log.Warn("Accesso sorgente Gmail fallito: il recupero automatico resta attivo", "event", "gmail.source_failed", logging.Error(err), "hint", "Verificare permessi Gmail, configurazione Pub/Sub e connettività")
	if e := w.Store.Notify(ctx, fmt.Sprintf("source:%d:%s:%s", conn.ID, time.Now().Format("2006-01-02"), f.Message), fmt.Sprintf("Casella %s: sincronizzazione non riuscita. Il recupero automatico resta attivo.", conn.Name)); e != nil {
		log.Error("Salvataggio avviso sorgente fallito", "event", "notification.enqueue_failed", logging.Error(e))
	}
}
func (w *Worker) receive(ctx context.Context, conn model.Connection) {
	log := w.Log.With("connection_id", conn.ID, "subscription", conn.Config["subscription"])
	parts := strings.Split(conn.Config["subscription"], "/")
	if len(parts) != 4 {
		log.Error("Subscription Pub/Sub non valida: usare projects/PROGETTO/subscriptions/NOME", "event", "pubsub.config_failed")
		return
	}
	log.Info("Listener Pub/Sub avviato", "event", "pubsub.started")
	defer log.Info("Listener Pub/Sub arrestato", "event", "pubsub.stopped")
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
				if json.Unmarshal(message.Data, &event) != nil || event.HistoryID == "" {
					log.Warn("Notifica Pub/Sub ignorata: formato Gmail non valido", "event", "pubsub.message_ignored", "pubsub_message_id", message.ID)
					message.Ack()
					return
				}
				if !strings.EqualFold(event.Email, conn.Config["email"]) {
					log.Debug("Notifica destinata a un’altra casella", "event", "pubsub.message_ignored", "pubsub_message_id", message.ID)
					message.Ack()
					return
				}
				log.Info("Notifica Gmail ricevuta da Pub/Sub", "event", "pubsub.received", "pubsub_message_id", message.ID)
				if err := w.Clients.Sync(deliveryCtx, conn); err != nil {
					w.sourceError(deliveryCtx, conn, "gmail.history", err)
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
		log.Warn("Listener Pub/Sub non disponibile: nuovo tentativo fra 30 secondi; recupero Gmail ogni 5 minuti", "event", "pubsub.receive_failed", logging.Error(err), "hint", "Verificare GOOGLE_APPLICATION_CREDENTIALS, subscription e ruolo Pub/Sub Subscriber dell’identità runtime")
		if e := w.Store.Notify(ctx, fmt.Sprintf("pubsub:%d:%s", conn.ID, time.Now().Format("2006-01-02")), fmt.Sprintf("Casella %s: listener Pub/Sub non disponibile. Verificare credenziali ADC e permessi; il controllo di recupero resta attivo.", conn.Name)); e != nil {
			log.Error("Salvataggio avviso Pub/Sub fallito", "event", "notification.enqueue_failed", logging.Error(e))
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
		w.Log.Error("Lettura connessioni per gli avvisi fallita", "event", "notification.connections_failed", logging.Error(err))
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
		w.Log.Error("Lettura coda avvisi fallita", "event", "notification.query_failed", logging.Error(err))
		return
	}
	type notice struct {
		id, attempts int
		body         string
	}
	var notices []notice
	for rows.Next() {
		var n notice
		if err := rows.Scan(&n.id, &n.body, &n.attempts); err != nil {
			w.Log.Error("Lettura avviso fallita", "event", "notification.read_failed", logging.Error(err))
			rows.Close()
			return
		}
		notices = append(notices, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		w.Log.Error("Lettura avvisi interrotta", "event", "notification.read_failed", logging.Error(err))
		return
	}
	rows.Close()
	for _, n := range notices {
		err = w.Clients.Alert(ctx, target, n.body)
		if err == nil {
			w.Log.Info("Avviso inviato a Slack", "event", "notification.sent", "notification_id", n.id, "connection_id", target.ID)
			_, err = w.Store.DB.ExecContext(ctx, `UPDATE notifications SET delivered=TRUE WHERE id=$1`, n.id)
		} else {
			delay := time.Duration(min(n.attempts+1, 30)) * time.Minute
			w.Log.Warn("Invio avviso Slack fallito: nuovo tentativo pianificato", "event", "notification.send_failed", "notification_id", n.id, "connection_id", target.ID, "retry_after", delay, logging.Error(err))
			_, err = w.Store.DB.ExecContext(ctx, `UPDATE notifications SET attempts=attempts+1,next_at=$2 WHERE id=$1`, n.id, time.Now().Add(delay))
		}
		if err != nil {
			w.Log.Error("Aggiornamento avviso fallito", "event", "notification.persist_failed", "notification_id", n.id, logging.Error(err))
		}
	}
}
