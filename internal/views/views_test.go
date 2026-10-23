package views

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/zupit-it/zupit-automations/internal/model"
)

func renderHTML(t *testing.T, component templ.Component) string {
	t.Helper()
	var html strings.Builder
	if err := component.Render(context.Background(), &html); err != nil {
		t.Fatal(err)
	}
	return html.String()
}

func TestPageContent(t *testing.T) {
	for _, tt := range []struct {
		name string
		data Data
		want string
	}{
		{"flows", Data{}, "Nessun flusso"},
		{"connections", Data{Active: "connections"}, "Nessuna connessione configurata"},
		{"connection form", Data{Active: "connections", Connection: model.Connection{Provider: "gmail"}}, `name="subscription"`},
		{"editor", Data{Active: "editor"}, `id="flow-editor"`},
		{"simulation", Data{Active: "simulate"}, `name="raw"`},
		{"runs", Data{Active: "runs"}, `id="runs-table"`},
		{"run detail", Data{Active: "runs", Run: model.Run{ID: 7, Subject: "Email elaborata"}}, "Email elaborata"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.data
			d.User = model.User{ID: 1, Name: "Mario", Admin: true}
			d.Notice = "Operazione completata"
			body := renderHTML(t, Page(d))
			if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, tt.want) {
				t.Fatalf("page %s did not render its content", tt.name)
			}
			if strings.Count(body, "Operazione completata") != 1 {
				t.Fatal("feedback must appear once, including in the editor")
			}
		})
	}
	body := renderHTML(t, Page(Data{Active: "editor"}))
	if !strings.Contains(body, `href="/auth/login"`) || strings.Contains(body, `id="flow-editor"`) {
		t.Fatal("anonymous page must render the login")
	}
}

func TestFlowEditorFormContract(t *testing.T) {
	d := Data{
		CSRF: "csrf-test",
		Flow: model.Flow{ID: 42, Name: "<script>unsafe()</script>", SourceID: 1, Definition: model.Definition{
			Filter:     model.Group{Conditions: []model.Condition{{Field: "subject", Operator: "contains"}}},
			Extractors: []model.Extractor{{Name: "ticket", Source: "subject", Pattern: "(.*)", Required: true}},
			Comparison: &model.ComparisonSettings{Instructions: "Regole Jev", SlackConnectionID: 2, SlackRecipient: "U0123456789"},
			Branches: []model.Branch{{Name: "Assistenza", When: model.Group{Conditions: []model.Condition{{Field: "ticket", Operator: "equals"}}}, Actions: []model.Action{
				{Kind: "jira", Project: "TEST", Fields: []model.Mapping{{Field: "customfield_1", Type: "text", Value: "{{ .ticket }}"}}},
				{Kind: "forward", To: "team@example.test"},
			}}},
		}},
		Connections: []model.Connection{{ID: 1, Name: "Casella", Provider: "gmail"}, {ID: 2, Name: "Slack", Provider: "slack"}},
	}
	body := renderHTML(t, FlowEditor(d))
	for _, want := range []string{
		`id="flow-editor" method="post" action="/flows/42"`,
		`name="_csrf" value="csrf-test"`,
		`name="source_id"`,
		`name="comparison.slack_recipient" value="U0123456789"`,
		`name="filter.0.field"`,
		`name="extract.0.pattern" value="(.*)"`,
		`name="branch.0.when.0.field"`,
		`name="branch.0.action.0.project"`,
		`option value="TEST" selected`,
		`name="branch.0.action.0.field.0.value" value="{{ .ticket }}"`,
		`name="branch.0.action.1.to" value="team@example.test"`,
		`value="remove.action.0.1"`,
		`hx-post="/flows/editor"`,
		`hx-include="closest form" hx-target="#flow-editor" hx-swap="outerHTML"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("editor lost %s", want)
		}
	}
	if strings.Contains(body, "<script>unsafe()</script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("flow name must remain escaped")
	}
	if strings.Count(body, "<form ") != 1 {
		t.Fatal("editor components must share one form")
	}
}

func TestRunRecoveryControls(t *testing.T) {
	for _, tt := range []struct {
		name, status, reference string
		admin                   bool
		resolve, retry, confirm bool
	}{
		{"admin failed", "failed", "", true, true, true, false},
		{"admin uncertain", "uncertain", "", true, true, true, true},
		{"admin warning", "warning", "", true, true, false, false},
		{"existing reference", "failed", "https://example.test/ticket/1", true, true, false, false},
		{"viewer", "failed", "", false, false, false, false},
		{"completed", "succeeded", "", true, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := Data{CSRF: "csrf-test", User: model.User{Admin: tt.admin}, Run: model.Run{ID: 7, Steps: []model.Step{{ID: 9, Status: tt.status, Reference: tt.reference, Action: model.Action{Kind: "jira"}}}}}
			body := renderHTML(t, RunDetail(d))
			for _, control := range []struct {
				marker string
				want   bool
			}{{`action="/steps/9/resolve"`, tt.resolve}, {`action="/steps/9/retry"`, tt.retry}, {`name="confirmed"`, tt.confirm}} {
				if strings.Contains(body, control.marker) != control.want {
					t.Errorf("unexpected recovery control %s", control.marker)
				}
			}
			if tt.resolve && !strings.Contains(body, `name="_csrf" value="csrf-test"`) {
				t.Fatal("recovery form lost CSRF protection")
			}
		})
	}
}
