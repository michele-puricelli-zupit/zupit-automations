package web

import (
	"context"
	"github.com/zupit-it/zupit-automations/internal/engine"
	"github.com/zupit-it/zupit-automations/internal/views"
	"net/url"
	"strings"
	"testing"
)

func TestJiraModeFormAndFields(t *testing.T) {
	v := url.Values{"name": {"Jira"}, "source_id": {"1"}, "branch.0.name": {"Main"},
		"branch.0.action.0.connection_id": {"2"}, "branch.0.action.0.subject": {"Test"},
		"branch.0.action.0.service_desk_id": {"10"}, "branch.0.action.0.request_type_id": {"20"},
		"branch.0.action.0.project": {"TEST"}, "branch.0.action.0.issue_type": {"10001"}, "branch.0.action.0.board_id": {"42"}}
	for _, kind := range []string{"jira", "jira_request"} {
		v.Set("branch.0.action.0.kind", kind)
		v.Set("branch.0.action.0.queue_id", "971")
		f := parseFlow(v)
		if err := engine.Validate(f.Definition); err != nil {
			t.Fatal(err)
		}
		a := f.Definition.Branches[0].Actions[0]
		if a.QueueID != 971 || a.Subject != "Test" {
			t.Fatalf("form lost Jira configuration: %+v", a)
		}
		var html strings.Builder
		if err := views.ActionEditor(views.Data{}, 0, 0, a).Render(context.Background(), &html); err != nil {
			t.Fatal(err)
		}
		body := html.String()
		for _, field := range []string{"project", "issue_type", "board_id"} {
			if strings.Contains(body, `name="branch.0.action.0.`+field+`"`) != (kind == "jira") {
				t.Fatalf("wrong %s field for %s", field, kind)
			}
		}
		for _, field := range []string{"service_desk_id", "request_type_id", "queue_id"} {
			if strings.Contains(body, `name="branch.0.action.0.`+field+`"`) != (kind == "jira_request") {
				t.Fatalf("wrong %s field for %s", field, kind)
			}
		}
		if !strings.Contains(body, "Jira Board") || !strings.Contains(body, "Jira Service Management") {
			t.Fatal("missing explicit Jira mode choices")
		}
	}
	for _, value := range []string{"invalid", "-1", "999999999999999999999"} {
		v.Set("branch.0.action.0.queue_id", value)
		if err := engine.Validate(parseFlow(v).Definition); err == nil {
			t.Fatalf("invalid queue ID accepted: %q", value)
		}
	}
}

func TestComparisonIsOptInAndSurvivesEditorOperations(t *testing.T) {
	v := url.Values{"name": {"Flow"}, "source_id": {"1"}, "comparison.instructions": {" Regole Jev "}, "comparison.slack_connection_id": {"9"}, "comparison.slack_recipient": {" U0123456789 "}}
	if f := parseFlow(v); f.Definition.Comparison != nil {
		t.Fatal("default A must not opt into B implicitly")
	}
	v.Set("comparison", "on")
	f := parseFlow(v)
	if err := f.Definition.Comparison.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := editorOperation(&f, "filter"); err != nil {
		t.Fatal(err)
	}
	cfg := f.Definition.Comparison
	if cfg.Instructions != "Regole Jev" || cfg.SlackConnectionID != 9 || cfg.SlackRecipient != "U0123456789" {
		t.Fatal("editor lost the comparison settings")
	}
	v.Del("comparison")
	if f := parseFlow(v); f.Definition.Comparison != nil {
		t.Fatal("disabling B did not restore standard mode")
	}
}
