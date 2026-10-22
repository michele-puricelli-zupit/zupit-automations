package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	dbent "github.com/zupit-it/zupit-automations/ent"
	"github.com/zupit-it/zupit-automations/ent/connection"
	"github.com/zupit-it/zupit-automations/ent/flow"
	"github.com/zupit-it/zupit-automations/ent/run"
	"github.com/zupit-it/zupit-automations/ent/step"
	"github.com/zupit-it/zupit-automations/internal/engine"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/secure"
)

type Store struct {
	DB     *sql.DB
	Client *dbent.Client
	Vault  *secure.Vault
}

func Open(ctx context.Context, dsn string, vault *secure.Vault) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(12)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db, dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db))), vault}, nil
}
func (s *Store) Close() error { return s.Client.Close() }
func conn(c *dbent.Connection) model.Connection {
	r := model.Connection{ID: c.ID, Name: c.Name, Provider: string(c.Provider), Config: c.Config, Status: c.Status, Error: c.Error, HistoryID: c.HistoryID}
	if c.WatchExpires != nil {
		r.WatchExpires = *c.WatchExpires
	}
	if c.Cutover != nil {
		r.Cutover = *c.Cutover
	}
	return r
}
func (s *Store) Connections(ctx context.Context) ([]model.Connection, error) {
	rows, err := s.Client.Connection.Query().Order(dbent.Asc(connection.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]model.Connection, 0, len(rows))
	for _, c := range rows {
		result = append(result, conn(c))
	}
	return result, nil
}
func (s *Store) Connection(ctx context.Context, id int) (model.Connection, error) {
	c, err := s.Client.Connection.Get(ctx, id)
	if err != nil {
		return model.Connection{}, err
	}
	return conn(c), nil
}
func (s *Store) SaveConnection(ctx context.Context, c model.Connection, secret []byte) (int, error) {
	if c.ID == 0 {
		row, err := s.Client.Connection.Create().SetName(c.Name).SetProvider(connection.Provider(c.Provider)).SetConfig(c.Config).Save(ctx)
		if err != nil {
			return 0, err
		}
		c.ID = row.ID
	} else {
		old, err := s.Connection(ctx, c.ID)
		if err != nil {
			return 0, err
		}
		if old.Provider != c.Provider {
			return 0, errors.New("il tipo di connessione non può essere cambiato")
		}
		u := s.Client.Connection.UpdateOneID(c.ID).SetName(c.Name).SetConfig(c.Config).SetStatus("pending").SetError("")
		if old.Config["email"] != c.Config["email"] && c.Provider == "gmail" {
			u.ClearSecret().SetHistoryID("").ClearCutover().ClearWatchExpires()
		}
		if _, err = u.Save(ctx); err != nil {
			return 0, err
		}
	}
	if len(secret) > 0 {
		if err := s.SaveSecret(ctx, c.ID, secret); err != nil {
			return c.ID, err
		}
	}
	return c.ID, nil
}
func (s *Store) SaveSecret(ctx context.Context, id int, plain []byte) error {
	b, err := s.Vault.Encrypt(plain, fmt.Sprintf("connection:%d", id))
	if err != nil {
		return err
	}
	return s.Client.Connection.UpdateOneID(id).SetSecret(b).Exec(ctx)
}
func (s *Store) Secret(ctx context.Context, id int) ([]byte, error) {
	c, err := s.Client.Connection.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(c.Secret) == 0 {
		return nil, errors.New("collegamento da autorizzare")
	}
	return s.Vault.Decrypt(c.Secret, fmt.Sprintf("connection:%d", id))
}
func (s *Store) ConnectionState(ctx context.Context, id int, status, message string) error {
	_, err := s.Client.Connection.UpdateOneID(id).SetStatus(status).SetError(message).Save(ctx)
	if err == nil && status == "paused" {
		err = s.Notify(ctx, fmt.Sprintf("connection:%d:%s", id, message), fmt.Sprintf("Connessione #%d sospesa: %s. Ricollegarla dall’interfaccia.", id, message))
	}
	return err
}
func (s *Store) Watch(ctx context.Context, id int, history string, expires time.Time, initial bool) error {
	u := s.Client.Connection.UpdateOneID(id).SetWatchExpires(expires).SetStatus("ready").SetError("")
	if initial {
		u.SetHistoryID(history).SetCutover(time.Now().UTC())
	}
	return u.Exec(ctx)
}
func asFlow(f *dbent.Flow) model.Flow {
	r := model.Flow{ID: f.ID, Name: f.Name, SourceID: f.SourceID, Enabled: f.Enabled, Definition: f.Definition}
	if f.ActivatedAt != nil {
		r.ActivatedAt = *f.ActivatedAt
	}
	return r
}
func (s *Store) Flows(ctx context.Context) ([]model.Flow, error) {
	rows, err := s.Client.Flow.Query().Order(dbent.Asc(flow.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	r := make([]model.Flow, 0, len(rows))
	for _, f := range rows {
		r = append(r, asFlow(f))
	}
	return r, nil
}
func (s *Store) Flow(ctx context.Context, id int) (model.Flow, error) {
	f, e := s.Client.Flow.Get(ctx, id)
	if e != nil {
		return model.Flow{}, e
	}
	return asFlow(f), nil
}
func (s *Store) CheckFlow(ctx context.Context, f model.Flow, activating bool) error {
	if strings.TrimSpace(f.Name) == "" {
		return errors.New("nome flusso obbligatorio")
	}
	source, err := s.Connection(ctx, f.SourceID)
	if err != nil || source.Provider != "gmail" {
		return errors.New("scegliere una sorgente Gmail valida")
	}
	if !activating {
		return nil
	}
	if strings.Contains(string(model.JSON(f.Definition)), "DA_CONFIGURARE") {
		return errors.New("completare tutti i segnaposto DA_CONFIGURARE prima dell’attivazione")
	}
	if err := engine.Validate(f.Definition); err != nil {
		return err
	}
	if cfg := f.Definition.Comparison; cfg != nil {
		if err := cfg.Validate(); err != nil {
			return err
		}
		target, err := s.Connection(ctx, cfg.SlackConnectionID)
		if err != nil || target.Provider != "slack" || target.Status != "ready" {
			return errors.New("il confronto B richiede una connessione Slack pronta")
		}
	}
	if activating && source.Status != "ready" {
		return errors.New("la sorgente Gmail non è pronta")
	}
	for _, b := range f.Definition.Branches {
		for _, a := range b.Actions {
			if a.Kind == "ignore" {
				continue
			}
			c, err := s.Connection(ctx, a.ConnectionID)
			if err != nil {
				return errors.New("connessione azione inesistente")
			}
			want := a.Kind
			if want == "jira_request" {
				want = "jira"
			}
			if want == "forward" {
				want = "gmail"
			}
			if c.Provider != want {
				return errors.New("tipo di connessione incompatibile con l’azione")
			}
			if activating && c.Status != "ready" {
				return fmt.Errorf("connessione %s non pronta", c.Name)
			}
		}
	}
	return nil
}
func (s *Store) SaveFlow(ctx context.Context, f model.Flow) (int, error) {
	if err := s.CheckFlow(ctx, f, f.Enabled); err != nil {
		return 0, err
	}
	if f.ID == 0 {
		u := s.Client.Flow.Create().SetName(f.Name).SetSourceID(f.SourceID).SetDefinition(f.Definition).SetEnabled(f.Enabled)
		if f.Enabled {
			u.SetActivatedAt(time.Now())
		}
		row, err := u.Save(ctx)
		if err != nil {
			return 0, err
		}
		return row.ID, nil
	}
	old, err := s.Flow(ctx, f.ID)
	if err != nil {
		return 0, err
	}
	u := s.Client.Flow.UpdateOneID(f.ID).SetName(f.Name).SetSourceID(f.SourceID).SetDefinition(f.Definition).SetEnabled(f.Enabled)
	if f.Enabled && (!old.Enabled || old.SourceID != f.SourceID) {
		u.SetActivatedAt(time.Now())
	}
	return f.ID, u.Exec(ctx)
}

// Ingest persists both the mailbox checkpoint and all independent flow runs atomically.
func (s *Store) Ingest(ctx context.Context, source int, messages []model.Message, history string) error {
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	flows, err := tx.Flow.Query().Where(flow.SourceID(source), flow.Enabled(true)).All(ctx)
	if err != nil {
		return err
	}
	for _, m := range messages {
		var exists bool
		exists, err = tx.Inbox.Query().Where(func(sel *entsql.Selector) {
			sel.Where(entsql.And(entsql.EQ("source_id", source), entsql.EQ("message_id", m.ID)))
		}).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		in, err := tx.Inbox.Create().SetSourceID(source).SetMessageID(m.ID).SetMessage(m).SetReceivedAt(m.ReceivedAt).Save(ctx)
		if err != nil {
			return err
		}
		for _, f := range flows {
			if f.ActivatedAt != nil && m.ReceivedAt.Before(*f.ActivatedAt) {
				continue
			}
			actions, reason, parseErr := engine.Plan(f.Definition, m)
			status := "pending"
			if parseErr != nil {
				status = "failed"
			} else if len(actions) == 0 {
				status = "ignored"
			}
			r, err := tx.Run.Create().SetFlowID(f.ID).SetInboxID(in.ID).SetSourceID(source).SetFlowName(f.Name).SetMessageID(m.ID).SetSubject(m.Subject).SetSnapshot(model.Snapshot{FlowName: f.Name, Definition: f.Definition, Message: m}).SetStatus(status).Save(ctx)
			if err != nil {
				return err
			}
			if f.Definition.Comparison != nil {
				if err = tx.Comparison.Create().SetRunID(r.ID).SetResult(model.ComparisonResult{}).Exec(ctx); err != nil {
					return err
				}
			}
			if parseErr != nil {
				if _, err = tx.Step.Create().SetRunID(r.ID).SetPosition(0).SetAction(model.Action{Kind: "ignore", Reason: "Errore di parsing"}).SetStatus("failed").SetError(parseErr.Error()).Save(ctx); err != nil {
					return err
				}
				if _, err = tx.Notification.Create().SetKey(fmt.Sprintf("run:%d:parse", r.ID)).SetBody(fmt.Sprintf("Flusso %s: parsing fallito. Aprire l’esecuzione #%d.", f.Name, r.ID)).Save(ctx); err != nil {
					return err
				}
			} else if len(actions) == 0 {
				if _, err = tx.Step.Create().SetRunID(r.ID).SetPosition(0).SetAction(model.Action{Kind: "ignore", Reason: reason}).SetStatus("ignored").SetReference(reason).Save(ctx); err != nil {
					return err
				}
			} else {
				for i, a := range actions {
					state := "pending"
					ref := ""
					if a.Kind == "ignore" {
						state = "ignored"
						ref = a.Reason
					}
					if _, err = tx.Step.Create().SetRunID(r.ID).SetPosition(i).SetAction(a).SetStatus(state).SetReference(ref).Save(ctx); err != nil {
						return err
					}
				}
			}
		}
	}
	if err = tx.Connection.UpdateOneID(source).SetHistoryID(history).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Notify(ctx context.Context, key, body string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notifications(key,body,created_at,next_at) VALUES($1,$2,now(),now()) ON CONFLICT(key) DO NOTHING`, key, body)
	return err
}
func (s *Store) Recover(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE steps SET status='uncertain',error='Processo interrotto durante la richiesta: verificare la destinazione prima di ripetere' WHERE status='running'`)
	if err != nil {
		return err
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE comparisons SET
 status=CASE WHEN status='evaluating' THEN 'pending' ELSE 'uncertain' END,
 error=CASE WHEN status='evaluating' THEN '' ELSE 'Invio DM interrotto: verificare Slack prima di ripetere' END
 WHERE status IN ('evaluating','sending')`); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,run_id FROM steps WHERE status='uncertain'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, runID int
		if err = rows.Scan(&id, &runID); err != nil {
			return err
		}
		if err = s.Notify(ctx, fmt.Sprintf("step:%d:uncertain", id), fmt.Sprintf("Esecuzione #%d: esito incerto dopo un riavvio. Verificare la destinazione.", runID)); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (s *Store) Claim(ctx context.Context) (model.Step, error) {
	var id int
	err := s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT s.id FROM steps s JOIN runs r ON r.id=s.run_id JOIN flows f ON f.id=r.flow_id
 LEFT JOIN connections c ON c.id=(s.action->>'connection_id')::bigint
 WHERE s.status='pending' AND s.next_at<=now() AND f.enabled=TRUE AND c.status='ready'
 AND NOT EXISTS(SELECT 1 FROM steps p WHERE p.run_id=s.run_id AND p.position<s.position AND p.status NOT IN('succeeded','warning','ignored'))
 ORDER BY s.next_at,s.id FOR UPDATE OF s SKIP LOCKED LIMIT 1)
 UPDATE steps SET status='running',attempts=attempts+1,started_at=now() WHERE id=(SELECT id FROM candidate) RETURNING id`).Scan(&id)
	if err != nil {
		return model.Step{}, err
	}
	row, err := s.Client.Step.Get(ctx, id)
	if err != nil {
		return model.Step{}, err
	}
	return asStep(row), nil
}
func asStep(v *dbent.Step) model.Step {
	r := model.Step{ID: v.ID, RunID: v.RunID, Position: v.Position, Action: v.Action, Status: v.Status, Attempts: v.Attempts, NextAt: v.NextAt, Reference: v.Reference, Error: v.Error}
	if v.StartedAt != nil {
		r.StartedAt = *v.StartedAt
	}
	return r
}
func (s *Store) Finish(ctx context.Context, id int, status, reference, message string, next time.Time) error {
	return s.Client.Step.UpdateOneID(id).SetStatus(status).SetReference(reference).SetError(message).SetNextAt(next).Exec(ctx)
}
func (s *Store) RefreshRuns(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs r SET status=CASE
 WHEN EXISTS(SELECT 1 FROM steps s WHERE s.run_id=r.id AND s.status='uncertain') THEN 'uncertain'
 WHEN EXISTS(SELECT 1 FROM steps s WHERE s.run_id=r.id AND s.status='failed') THEN 'failed'
 WHEN EXISTS(SELECT 1 FROM steps s WHERE s.run_id=r.id AND s.status IN('running','pending')) THEN 'pending'
 WHEN EXISTS(SELECT 1 FROM steps s WHERE s.run_id=r.id AND s.status='warning') THEN 'warning'
 WHEN EXISTS(SELECT 1 FROM steps s WHERE s.run_id=r.id AND s.status='succeeded') THEN 'succeeded' ELSE 'ignored' END,
 completed_at=CASE WHEN EXISTS(SELECT 1 FROM steps s WHERE s.run_id=r.id AND s.status IN('running','pending')) THEN NULL ELSE COALESCE(completed_at,now()) END`)
	return err
}
func (s *Store) Runs(ctx context.Context) ([]model.Run, error) {
	rows, err := s.Client.Run.Query().Order(dbent.Desc(run.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]model.Run, 0, len(rows))
	for _, r := range rows {
		result = append(result, asRun(r))
	}
	return result, nil
}
func asRun(r *dbent.Run) model.Run {
	m := model.Run{ID: r.ID, FlowID: r.FlowID, FlowName: r.FlowName, SourceID: r.SourceID, MessageID: r.MessageID, Subject: r.Subject, Status: r.Status, CreatedAt: r.CreatedAt}
	if r.CompletedAt != nil {
		m.CompletedAt = *r.CompletedAt
	}
	return m
}
func (s *Store) Run(ctx context.Context, id int) (model.Run, error) {
	r, err := s.Client.Run.Get(ctx, id)
	if err != nil {
		return model.Run{}, err
	}
	m := asRun(r)
	steps, err := s.Client.Step.Query().Where(step.RunID(id)).Order(dbent.Asc(step.FieldPosition)).All(ctx)
	if err != nil {
		return m, err
	}
	for _, row := range steps {
		m.Steps = append(m.Steps, asStep(row))
	}
	comparison, err := s.ComparisonForRun(ctx, id)
	if err != nil {
		return m, err
	}
	m.Comparison = comparison
	return m, nil
}
func (s *Store) Retry(ctx context.Context, id int, resolve bool, reference string) error {
	row, err := s.Client.Step.Get(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != "failed" && row.Status != "uncertain" && row.Status != "warning" {
		return errors.New("azione non riprendibile")
	}
	if row.Action.Kind == "ignore" {
		return errors.New("errore di parsing: correggere e simulare il flusso; non è un’azione esterna ritentabile")
	}
	u := s.Client.Step.UpdateOneID(id).Where(step.StatusEQ(row.Status), step.ReferenceEQ(row.Reference))
	if resolve {
		if strings.TrimSpace(reference) == "" {
			return errors.New("riferimento esterno obbligatorio")
		}
		return u.SetStatus("succeeded").SetReference(reference).SetError("Riconciliato manualmente").SetNextAt(time.Now()).Exec(ctx)
	}
	if row.Reference != "" || row.Status == "warning" {
		return errors.New("la destinazione è già stata creata: verificare e riconciliare senza ripetere")
	}
	return u.SetStatus("pending").SetAttempts(0).SetNextAt(time.Now()).SetError("").Exec(ctx)
}

func (s *Store) Lock(ctx context.Context, key int64) (func(), error) {
	c, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err = c.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		c.Close()
		return nil, err
	}
	if !acquired {
		c.Close()
		return nil, errors.New("operazione già in corso")
	}
	return func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.ExecContext(release, `SELECT pg_advisory_unlock($1)`, key)
		_ = c.Close()
	}, nil
}

func DecodeDefinition(data []byte) (model.Definition, error) {
	var d model.Definition
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&d)
	if err != nil {
		return d, err
	}
	return d, engine.Validate(d)
}
