package web

import (
	"net/url"
	"testing"
)

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
