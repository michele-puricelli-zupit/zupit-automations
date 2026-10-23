package store_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
)

func TestIngestLogsCommittedDecisionsWithoutEmailContents(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConnectionState(ctx, source, "ready", ""); err != nil {
		t.Fatal(err)
	}
	flow, err := s.SaveFlow(ctx, model.Flow{Name: "Flow", SourceID: source, Enabled: true, Definition: model.Definition{
		Version: 1, Filter: model.Group{Mode: "all", Conditions: []model.Condition{{Field: "subject", Operator: "equals", Value: "PRIVATE"}}},
		Branches: []model.Branch{{Name: "Main", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "ignore", Reason: "PRIVATE"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	message := model.Message{ID: "email-1", From: "PRIVATE@example.test", Subject: "different PRIVATE", Body: "PRIVATE", ReceivedAt: time.Now().Add(time.Second)}
	if err := s.Ingest(ctx, source, []model.Message{message}, "10"); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "PRIVATE") || !strings.Contains(text, `"decision":"filter_mismatch"`) || !strings.Contains(text, `"run_id":`) || !strings.Contains(text, `"flow_id":`) || flow == 0 {
		t.Fatalf("unsafe or missing decision log: %s", text)
	}
	out.Reset()
	if err := s.Ingest(ctx, source, []model.Message{message}, "11"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `flow.evaluated`) || !strings.Contains(out.String(), `"duplicates":1`) {
		t.Fatal("deduplicated message was logged as a new run")
	}
	if _, err := s.DB.Exec(`ALTER TABLE inboxes ADD CONSTRAINT fail_logging CHECK(message_id <> 'bad')`); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	message.ID = "uncommitted"
	bad := message
	bad.ID = "bad"
	if err := s.Ingest(ctx, source, []model.Message{message, bad}, "12"); err == nil {
		t.Fatal("expected rollback")
	}
	if out.Len() != 0 {
		t.Fatalf("rolled back ingestion was logged as committed: %s", out.String())
	}
	f, err := s.Flow(ctx, flow)
	if err != nil {
		t.Fatal(err)
	}
	f.Definition.Extractors = []model.Extractor{{Name: "reference", Source: "body", Pattern: `ID: (\d+)`, Required: true}}
	if _, err := s.SaveFlow(ctx, f); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	message.ID, message.Subject = "missing-capture", "PRIVATE"
	if err := s.Ingest(ctx, source, []model.Message{message}, "13"); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !strings.Contains(text, `"level":"ERROR"`) || !strings.Contains(text, `"decision":"planning_failed"`) || strings.Contains(text, "PRIVATE") {
		t.Fatalf("planning error was hidden or exposed contents: %s", text)
	}
}
