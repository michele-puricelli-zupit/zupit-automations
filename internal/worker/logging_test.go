package worker

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestActionLogsExplainOutcomesAndNeverExposePayloads(t *testing.T) {
	for _, tt := range []struct {
		name, status, level string
		httpStatus, queue   int
	}{
		{"success", "succeeded", "INFO", 201, 0},
		{"queue warning", "warning", "WARN", 201, 971},
		{"retry", "pending", "WARN", 429, 0},
		{"uncertain", "uncertain", "ERROR", 503, 0},
		{"invalid fields", "failed", "ERROR", 400, 0},
		{"credentials", "pending", "WARN", 401, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := testutil.Database(t)
			ctx := context.Background()
			connection := func(provider string) int {
				id, err := s.SaveConnection(ctx, model.Connection{Name: provider, Provider: provider, Config: map[string]string{"url": "https://example.atlassian.net", "email": "PRIVATE@example.test"}}, []byte(`{"token":"PRIVATE"}`))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.ConnectionState(ctx, id, "ready", ""); err != nil {
					t.Fatal(err)
				}
				return id
			}
			source, jira := connection("gmail"), connection("jira")
			_, err := s.SaveFlow(ctx, model.Flow{Name: "Flow", SourceID: source, Enabled: true, Definition: model.Definition{
				Version: 1, Filter: model.Group{Mode: "all"}, Branches: []model.Branch{{Name: "Main", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "jira_request", ConnectionID: jira, ServiceDeskID: "PRIVATE", RequestTypeID: "PRIVATE", QueueID: tt.queue, Subject: "{{ .subject }}", Body: "{{ .body }}"}}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Ingest(ctx, source, []model.Message{{ID: "email-1", Subject: "PRIVATE", Body: "PRIVATE", ReceivedAt: time.Now().Add(time.Second)}}, "10"); err != nil {
				t.Fatal(err)
			}
			clients := connectors.New(s, oauth2.Config{})
			creates := 0
			clients.HTTP.Transport = comparisonTransport(func(r *http.Request) (*http.Response, error) {
				code, body := tt.httpStatus, `{"error":"PRIVATE"}`
				if r.Method == "POST" {
					creates++
					if code == 201 {
						body = `{"issueKey":"TEST-1"}`
					}
				} else {
					code = 403
				}
				return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			var out bytes.Buffer
			w := Worker{Store: s, Clients: clients, Log: slog.New(slog.NewJSONHandler(&out, nil))}
			step, err := s.Claim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			w.executeStep(ctx, step)
			if strings.Contains(out.String(), "PRIVATE") || creates != 1 {
				t.Fatalf("unsafe log or duplicate creation: %s", out.String())
			}
			var completed map[string]any
			decoder := json.NewDecoder(&out)
			for decoder.More() {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				if entry["event"] == "action.completed" {
					completed = entry
				}
			}
			if completed["status"] != tt.status || completed["level"] != tt.level || completed["run_id"] != float64(step.RunID) || completed["step_id"] != float64(step.ID) || completed["connection_id"] != float64(jira) {
				t.Fatalf("missing action context: %+v", completed)
			}
			if tt.status == "pending" && completed["next_at"] == nil {
				t.Fatal("retry schedule missing")
			}
			if tt.status == "warning" && completed["reference"] != "https://example.atlassian.net/browse/TEST-1" {
				t.Fatal("created reference lost")
			}
		})
	}
}
