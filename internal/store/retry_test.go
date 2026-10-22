package store_test

import (
	"context"
	"testing"
	"time"

	dbent "github.com/zupit-it/zupit-automations/ent"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
)

func TestRetryDoesNotOverwriteConcurrentExecution(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "resolve"}[resolve], func(t *testing.T) {
			s := testutil.Database(t)
			ctx := context.Background()
			source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			f, err := s.Client.Flow.Create().SetName("Test").SetSourceID(source).SetDefinition(model.Definition{}).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			in, err := s.Client.Inbox.Create().SetSourceID(source).SetMessageID("test").SetMessage(model.Message{}).SetReceivedAt(time.Now()).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.Client.Run.Create().SetFlowID(f.ID).SetInboxID(in.ID).SetSourceID(source).SetFlowName(f.Name).SetMessageID("test").SetSubject("Test").SetSnapshot(model.Snapshot{}).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			row, err := s.Client.Step.Create().SetRunID(run.ID).SetPosition(0).SetAction(model.Action{Kind: "jira"}).SetStatus("uncertain").Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			s.Client.Step.Use(func(next dbent.Mutator) dbent.Mutator {
				return dbent.MutateFunc(func(ctx context.Context, m dbent.Mutation) (dbent.Value, error) {
					// Simulate another operator retrying and the worker claiming after the initial read.
					if _, err := s.DB.ExecContext(ctx, `UPDATE steps SET status='running' WHERE id=$1`, row.ID); err != nil {
						return nil, err
					}
					return next.Mutate(ctx, m)
				})
			})
			if err := s.Retry(ctx, row.ID, resolve, "https://example.test/issue/1"); !dbent.IsNotFound(err) {
				t.Fatalf("expected concurrent change to reject the stale request, got %v", err)
			}
			row, err = s.Client.Step.Get(ctx, row.ID)
			if err != nil || row.Status != "running" || row.Reference != "" {
				t.Fatalf("concurrent execution was overwritten: %+v, %v", row, err)
			}
		})
	}
}
