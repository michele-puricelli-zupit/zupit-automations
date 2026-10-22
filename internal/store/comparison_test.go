package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
)

func TestComparisonQueueIsIndependentDurableAndDeduplicated(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	slack, err := s.SaveConnection(ctx, model.Connection{Name: "Report privati", Provider: "slack", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{source, slack} {
		if err := s.ConnectionState(ctx, id, "ready", ""); err != nil {
			t.Fatal(err)
		}
	}
	base := model.Definition{Version: 1, Filter: model.Group{Mode: "all"}, Branches: []model.Branch{{Name: "Ignora", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "ignore", Reason: "Test"}}}}}
	if _, err := s.SaveFlow(ctx, model.Flow{Name: "Solo A", SourceID: source, Enabled: true, Definition: base}); err != nil {
		t.Fatal(err)
	}
	base.Comparison = &model.ComparisonSettings{Instructions: "Regole originali", SlackConnectionID: slack, SlackRecipient: "U0123456789"}
	base.Filter.Conditions = []model.Condition{{Field: "subject", Operator: "contains", Value: "non presente"}}
	ignoredID, err := s.SaveFlow(ctx, model.Flow{Name: "A esclude", SourceID: source, Enabled: true, Definition: base})
	if err != nil {
		t.Fatal(err)
	}
	base.Filter.Conditions = nil
	base.Extractors = []model.Extractor{{Name: "required", Source: "body", Pattern: `(missing)`, Required: true}}
	if _, err := s.SaveFlow(ctx, model.Flow{Name: "A parsing fallito", SourceID: source, Enabled: true, Definition: base}); err != nil {
		t.Fatal(err)
	}
	m := model.Message{ID: "one", Subject: "Test", Body: "Body", ReceivedAt: time.Now().Add(time.Second)}
	for _, history := range []string{"1", "2"} {
		if err := s.Ingest(ctx, source, []model.Message{m}, history); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := s.Client.Run.Query().Count(ctx); err != nil || count != 3 {
		t.Fatalf("wrong A runs: %d %v", count, err)
	}
	if count, err := s.Client.Comparison.Query().Count(ctx); err != nil || count != 2 {
		t.Fatalf("B was duplicated or selected only A matches: %d %v", count, err)
	}
	if _, err := s.ClaimComparison(ctx, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("analysis claimed without API configuration")
	}
	f, err := s.Flow(ctx, ignoredID)
	if err != nil {
		t.Fatal(err)
	}
	f.Definition.Comparison.SlackRecipient = "U9876543210"
	if _, err := s.SaveFlow(ctx, f); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimComparison(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimComparison(ctx, true)
	if err != nil || second.ID == first.ID {
		t.Fatalf("comparison claimed twice: %+v %v", second, err)
	}
	if first.Snapshot.Definition.Comparison.SlackRecipient != "U0123456789" || second.Snapshot.Definition.Comparison.SlackRecipient != "U0123456789" {
		t.Fatal("queued recipient changed with the live flow")
	}
	if err := s.SaveComparisonResult(ctx, first.ID, model.ComparisonResult{Model: "jev-test", B: model.Proposal{Branch: "None"}}); err != nil {
		t.Fatal(err)
	}
	sending, err := s.ClaimComparison(ctx, false)
	if err != nil || sending.ID != first.ID || sending.Status != "sending" || sending.Attempts != 1 {
		t.Fatalf("saved result was not reused for delivery: %+v %v", sending, err)
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	firstRow, err := s.Client.Comparison.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondRow, err := s.Client.Comparison.Get(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRow.Status != "uncertain" || secondRow.Status != "pending" {
		t.Fatal("recovery could duplicate a DM or lose an evaluation")
	}
	if count, err := s.Client.Step.Query().Count(ctx); err != nil || count != 3 {
		t.Fatal("comparison changed A's queued actions")
	}
	if _, err := s.ClaimComparison(ctx, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("uncertain delivery was automatically repeated")
	}
}
