package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/zupit-it/zupit-automations/examples"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"github.com/zupit-it/zupit-automations/migrations"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPresetDraftsCannotActivateWithPlaceholders(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConnectionState(ctx, source, "ready", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"srt", "cup", "kit", "support"} {
		t.Run(name, func(t *testing.T) {
			f, err := examples.Preset(name)
			if err != nil {
				t.Fatal(err)
			}
			f.SourceID = source
			id, err := s.SaveFlow(ctx, f)
			if err != nil {
				t.Fatalf("incomplete preset must be saveable as a draft: %v", err)
			}
			f.ID, f.Enabled = id, true
			if _, err = s.SaveFlow(ctx, f); err == nil || !strings.Contains(err.Error(), "DA_CONFIGURARE") {
				t.Fatalf("expected placeholder validation, got %v", err)
			}
			stored, err := s.Flow(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Enabled || !stored.ActivatedAt.IsZero() {
				t.Fatal("failed activation changed the saved draft")
			}
		})
	}
}

func TestDurableIndependentFlowsDedupAndRecovery(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{"email": "team@example.test"}}, []byte(`{"token":"testsecret"}`))
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.SaveConnection(ctx, model.Connection{Name: "Jira", Provider: "jira", Config: map[string]string{}}, []byte(`{"token":"testsecret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConnectionState(ctx, source, "ready", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ConnectionState(ctx, target, "ready", ""); err != nil {
		t.Fatal(err)
	}
	a := model.Action{Kind: "jira", ConnectionID: target, Project: "TEST", IssueType: "10001", BoardID: 1, Subject: "{{ .subject }}"}
	d := model.Definition{Version: 1, Filter: model.Group{Mode: "all"}, Branches: []model.Branch{{Name: "main", When: model.Group{Mode: "all"}, Actions: []model.Action{a}}}}
	for _, name := range []string{"Flow A", "Flow B"} {
		if _, err = s.SaveFlow(ctx, model.Flow{Name: name, SourceID: source, Enabled: true, Definition: d}); err != nil {
			t.Fatal(err)
		}
	}
	m := model.Message{ID: "m1", Subject: `Errore "è"`, ReceivedAt: time.Now().Add(time.Second)}
	if err = s.Ingest(ctx, source, []model.Message{m}, "120"); err != nil {
		t.Fatal(err)
	}
	if err = s.Ingest(ctx, source, []model.Message{m}, "121"); err != nil {
		t.Fatal(err)
	}
	runs, err := s.Runs(ctx)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs %d: %v", len(runs), err)
	}
	c, err := s.Connection(ctx, source)
	if err != nil || c.HistoryID != "121" {
		t.Fatal("checkpoint not persisted")
	}
	var claimed []model.Step
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			step, err := s.Claim(ctx)
			if err == nil {
				mu.Lock()
				claimed = append(claimed, step)
				mu.Unlock()
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(claimed) != 2 || claimed[0].ID == claimed[1].ID {
		t.Fatalf("claimed duplicates: %+v", claimed)
	}
	if err = s.Finish(ctx, claimed[0].ID, "succeeded", "https://example.test/ticket/1", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.RefreshRuns(ctx); err != nil {
		t.Fatal(err)
	}
	row, err := s.Client.Step.Get(ctx, claimed[1].ID)
	if err != nil || row.Status != "uncertain" {
		t.Fatalf("recovery: %v %+v", err, row)
	}
	if _, err = s.Claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("uncertain action automatically retried")
	}
	if err = s.Retry(ctx, claimed[0].ID, false, ""); err == nil {
		t.Fatal("successful step retried")
	}
	if err = s.Retry(ctx, claimed[1].ID, true, "https://example.test/already-created"); err != nil {
		t.Fatal(err)
	}
	if err = s.Ingest(ctx, source, []model.Message{m}, "122"); err != nil {
		t.Fatal(err)
	}
	runs, _ = s.Runs(ctx)
	if len(runs) != 2 {
		t.Fatal("recovery created duplicate run")
	}
	var stored []byte
	if err = s.DB.QueryRow(`SELECT secret FROM connections WHERE id=$1`, source).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("testsecret")) {
		t.Fatal("plaintext secret stored")
	}
	if plain, err := s.Secret(ctx, source); err != nil || !json.Valid(plain) {
		t.Fatal("secret not readable")
	}
	if err = migrations.Apply(ctx, s.DB); err != nil {
		t.Fatal("migrations are not idempotent", err)
	}
}
func TestTransactionDoesNotAdvanceCheckpointOnFailure(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	id, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`ALTER TABLE inboxes ADD CONSTRAINT fail_fixture CHECK(message_id <> 'bad')`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Ingest(ctx, id, []model.Message{{ID: "good", ReceivedAt: time.Now()}, {ID: "bad", ReceivedAt: time.Now()}}, "999")
	if err == nil {
		t.Fatal("expected database failure")
	}
	c, _ := s.Connection(ctx, id)
	count, _ := s.Client.Inbox.Query().Count(ctx)
	if c.HistoryID != "" || count != 0 {
		t.Fatal("partial transaction committed")
	}
}
func TestSnapshotAndSequentialActions(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	source, _ := s.SaveConnection(ctx, model.Connection{Name: "Source", Provider: "gmail", Config: map[string]string{}}, nil)
	target, _ := s.SaveConnection(ctx, model.Connection{Name: "Target", Provider: "slack", Config: map[string]string{}}, nil)
	_ = s.ConnectionState(ctx, source, "ready", "")
	_ = s.ConnectionState(ctx, target, "ready", "")
	a := model.Action{Kind: "slack", ConnectionID: target, ListID: "FLIST", Fields: []model.Mapping{{Field: "ColTitle", Type: "text", Value: "{{ .subject }}"}}}
	f := model.Flow{Name: "Snapshot", SourceID: source, Enabled: true, Definition: model.Definition{Version: 1, Filter: model.Group{Mode: "all"}, Branches: []model.Branch{{Name: "main", When: model.Group{Mode: "all"}, Actions: []model.Action{a, a}}}}}
	id, err := s.SaveFlow(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Ingest(ctx, source, []model.Message{{ID: "m", Subject: "original", ReceivedAt: time.Now().Add(time.Second)}}, "2"); err != nil {
		t.Fatal(err)
	}
	f.ID = id
	f.Definition.Branches[0].Actions[0].Fields[0].Value = "changed"
	if _, err = s.SaveFlow(ctx, f); err != nil {
		t.Fatal(err)
	}
	first, err := s.Claim(ctx)
	if err != nil || first.Action.Fields[0].Value != "original" {
		t.Fatalf("snapshot changed: %+v %v", first, err)
	}
	if _, err = s.Claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("later action ran before first completed")
	}
	_ = s.Finish(ctx, first.ID, "succeeded", "created", "", time.Now())
	second, err := s.Claim(ctx)
	if err != nil || second.Position != 1 {
		t.Fatalf("second step: %+v %v", second, err)
	}
}
