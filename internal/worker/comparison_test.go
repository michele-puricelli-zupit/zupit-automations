package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
)

type comparisonTransport func(*http.Request) (*http.Response, error)

func (f comparisonTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestComparisonWorkerRetriesReportWithoutReevaluatingOrExecutingB(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	connection := func(provider string) int {
		id, err := s.SaveConnection(ctx, model.Connection{Name: provider, Provider: provider, Config: map[string]string{}}, []byte(`{"token":"test"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ConnectionState(ctx, id, "ready", ""); err != nil {
			t.Fatal(err)
		}
		return id
	}
	source, jira, slack := connection("gmail"), connection("jira"), connection("slack")
	d := model.Definition{Version: 1, Filter: model.Group{Mode: "all"},
		Comparison: &model.ComparisonSettings{Instructions: "Scegli il ramo più appropriato", SlackConnectionID: slack, SlackRecipient: "U0123456789"},
		Branches: []model.Branch{
			{Name: "Bug", When: model.Group{Mode: "all", Conditions: []model.Condition{{Field: "subject", Operator: "contains", Value: "bug"}}}, Actions: []model.Action{{Kind: "jira", ConnectionID: jira, Project: "TEST", BoardID: 1, IssueType: "10001", Subject: "{{ .subject }}"}}},
			{Name: "Altro", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "ignore", Reason: "Test"}}},
		},
	}
	if _, err := s.SaveFlow(ctx, model.Flow{Name: "Test", SourceID: source, Enabled: true, Definition: d}); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(ctx, source, []model.Message{{ID: "m", Subject: "Non si apre", ReceivedAt: time.Now().Add(time.Second)}}, "1"); err != nil {
		t.Fatal(err)
	}
	clients := connectors.New(s, oauth2.Config{})
	clients.TypeSafeAPIKey, clients.TypeSafeModel = "test-key", "jev-test"
	evaluations, posts := 0, 0
	clients.HTTP.Transport = comparisonTransport(func(r *http.Request) (*http.Response, error) {
		body, status := "", 200
		switch r.URL.Host + r.URL.Path {
		case "api.typesafe.ai/v1/systemone":
			evaluations++
			body = `{"model":"jev-test","answers":{"route":{"type":"choice","choice":"branch_0","confidence":1,"probabilities":{"branch_0":1,"branch_1":0,"none":0}}},"usage":{"input_tokens":30,"output_tokens":5}}`
		case "slack.com/api/conversations.open":
			body = `{"ok":true,"channel":{"id":"D0123456789","is_im":true}}`
		case "slack.com/api/chat.postMessage":
			posts++
			if posts == 1 {
				status = 429
			} else {
				body = `{"ok":true,"channel":"D0123456789","ts":"1.2"}`
			}
		default:
			t.Fatalf("B executed a business action: %s", r.URL)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	w := &Worker{Store: s, Clients: clients, BaseURL: "https://app.example.test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	job, err := s.ClaimComparison(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	w.compareOne(ctx, job)
	job, err = s.ClaimComparison(ctx, false)
	if err != nil || job.Result.B.Branch != "Bug" || job.Status != "sending" {
		t.Fatalf("analysis not persisted: %+v %v", job, err)
	}
	w.compareOne(ctx, job)
	row, err := s.Client.Comparison.Get(ctx, job.ID)
	if err != nil || row.Status != "evaluated" || !row.NextAt.After(time.Now()) {
		t.Fatalf("report rate limit lost the saved result: %+v %v", row, err)
	}
	if _, err := s.DB.Exec(`UPDATE comparisons SET next_at=now() WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimComparison(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	w.compareOne(ctx, job)
	row, err = s.Client.Comparison.Get(ctx, job.ID)
	if err != nil || row.Status != "succeeded" || evaluations != 1 || posts != 2 {
		t.Fatalf("wrong retry behavior: %+v %v, evaluations=%d posts=%d", row, err, evaluations, posts)
	}
	steps, err := s.Client.Step.Query().All(ctx)
	if err != nil || len(steps) != 1 || steps[0].Status != "ignored" || steps[0].Attempts != 0 {
		t.Fatal("B changed A's actions")
	}
}
