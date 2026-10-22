package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zupit-it/zupit-automations/internal/model"
)

func comparisonSnapshot() model.Snapshot {
	return model.Snapshot{FlowName: "Supporto", Message: model.Message{ID: "mail-1", Subject: "Non riesco ad accedere", Body: "SR-123827: l'applicazione si blocca"}, Definition: model.Definition{
		Version: 1, Filter: model.Group{Mode: "all"},
		Extractors: []model.Extractor{{Name: "reference", Source: "body", Pattern: `(SR-\d+)`, Required: true}},
		Comparison: &model.ComparisonSettings{Instructions: "Riconosci i bug anche senza la parola bug", SlackConnectionID: 9, SlackRecipient: "U0123456789"},
		Branches: []model.Branch{
			{Name: "Bug", When: model.Group{Mode: "all", Conditions: []model.Condition{{Field: "subject", Operator: "contains", Value: "bug"}}}, Actions: []model.Action{{Kind: "jira", ConnectionID: 3, Project: "TEST", BoardID: 1, IssueType: "10001", Subject: "{{ .reference }} · {{ .subject }}", Body: "{{ .body }}"}}},
			{Name: "Altro", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "ignore", Reason: "Fuori ambito"}}},
		},
	}}
}

const jevAnswer = `{"model":"jev-test-1","answers":{"route":{"type":"choice","choice":"branch_0","confidence":0.8,"probabilities":{"branch_0":0.9,"branch_1":0.05,"none":0.05}}},"usage":{"input_tokens":100,"output_tokens":10}}`

func TestCompareChoosesConfiguredBranchWithoutExecutingIt(t *testing.T) {
	snapshot := comparisonSnapshot()
	before := model.JSON(snapshot)
	requests := 0
	c := &Clients{TypeSafeAPIKey: "test-key", TypeSafeModel: "jev-test", HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != "https://api.typesafe.ai/v1/systemone" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected external operation: %s %s", r.Method, r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Model     string                     `json:"model"`
			State     map[string]json.RawMessage `json:"state"`
			Questions map[string]struct {
				Type     string                     `json:"type"`
				Criteria map[string]json.RawMessage `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "jev-test" || payload.Questions["route"].Type != "choice" || len(payload.Questions["route"].Criteria) != 3 || payload.Questions["route"].Criteria["none"] == nil {
			t.Fatal("missing closed-set options")
		}
		if len(payload.State) != 2 || payload.State["email"] == nil || payload.State["flow"] == nil || bytes.Contains(body, []byte("U0123456789")) || bytes.Contains(body, []byte("test-key")) {
			t.Fatal("A's answer, delivery config or credentials must not be sent to Jev")
		}
		return response(200, jevAnswer), nil
	})}}
	result, err := c.Compare(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || result.A.Branch != "Altro" || result.B.Branch != "Bug" || len(result.B.Actions) != 1 || result.B.Actions[0].Kind != "jira" {
		t.Fatalf("unexpected comparison: %+v, requests=%d", result, requests)
	}
	if result.B.Actions[0].Subject != "SR-123827 · Non riesco ad accedere" || result.B.Actions[0].Body != snapshot.Message.Body {
		t.Fatal("arguments must be rendered from exact source data")
	}
	if result.Model != "jev-test-1" || result.InputTokens != 100 || result.OutputTokens != 10 || result.Confidence != 0.8 {
		t.Fatal("missing provenance or usage")
	}
	if !bytes.Equal(before, model.JSON(snapshot)) {
		t.Fatal("B mutated the snapshot used by A")
	}
}

func TestCompareNoneAndInvalidAnswers(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		wantError  bool
	}{
		{"none", strings.ReplaceAll(strings.ReplaceAll(jevAnswer, `"choice":"branch_0"`, `"choice":"none"`), `"branch_0":0.9,"branch_1":0.05,"none":0.05`, `"branch_0":0.05,"branch_1":0.05,"none":0.9`), false},
		{"unknown choice", strings.Replace(jevAnswer, `"choice":"branch_0"`, `"choice":"create_anything"`, 1), true},
		{"missing confidence", strings.Replace(jevAnswer, `"confidence":0.8,`, "", 1), true},
		{"invalid confidence", strings.Replace(jevAnswer, `"confidence":0.8`, `"confidence":1.8`, 1), true},
		{"missing probability", strings.Replace(jevAnswer, `,"none":0.05`, "", 1), true},
		{"not highest probability", strings.Replace(jevAnswer, `"branch_0":0.9,"branch_1":0.05`, `"branch_0":0.05,"branch_1":0.9`, 1), true},
		{"wrong type", strings.Replace(jevAnswer, `"type":"choice"`, `"type":"noul"`, 1), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Clients{TypeSafeAPIKey: "test-key", TypeSafeModel: "jev-test", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(200, tt.body), nil })}}
			result, err := c.Compare(context.Background(), comparisonSnapshot())
			if (err != nil) != tt.wantError || len(result.B.Actions) != 0 {
				t.Fatalf("unexpected outcome: %+v %v", result, err)
			}
		})
	}
}

func TestCompareErrorsDoNotLeakResponseBodies(t *testing.T) {
	for _, code := range []int{401, 429, 503} {
		c := &Clients{TypeSafeAPIKey: "test-key", TypeSafeModel: "jev-test", HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			return response(code, `{"error":"private-email-and-key"}`), nil
		})}}
		result, err := c.Compare(context.Background(), comparisonSnapshot())
		if err == nil || strings.Contains(err.Error(), "private-email-and-key") || len(result.B.Actions) != 0 || result.A.Branch != "Altro" {
			t.Fatalf("unsafe error: %+v %v", result, err)
		}
		if Classify(err).Retryable != (code != 401) || Classify(err).Uncertain {
			t.Fatalf("wrong inference retry policy: %+v", Classify(err))
		}
	}
}

func TestComparisonReportIsBoundedAndLinksToFullResult(t *testing.T) {
	job := model.Comparison{RunID: 42, Snapshot: comparisonSnapshot(), Result: model.ComparisonResult{A: model.Proposal{Branch: "Altro"}, B: model.Proposal{Branch: "Bug"}, Model: "jev-test"}}
	job.Snapshot.Message.Subject = strings.Repeat("è <@here> ", 1000)
	text := ComparisonReport(job, "https://app.example.test/")
	if !utf8.ValidString(text) || len([]rune(text)) > 3000 || !strings.Contains(text, "https://app.example.test/runs/42") || !strings.Contains(text, "B non esegue azioni") {
		t.Fatal("report is invalid or misleading")
	}
}
