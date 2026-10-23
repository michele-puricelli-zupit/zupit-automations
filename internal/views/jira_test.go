package views

import (
	"strings"
	"testing"

	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/model"
)

func TestJiraSelectNamesAndUnavailableSelection(t *testing.T) {
	body := renderHTML(t, JiraSelect("ticket_type", "Tipo di ticket", "10001", "Scegli il tipo", []connectors.JiraOption{{Value: "10001", Label: "Bug <script>"}}, true))
	if !strings.Contains(body, `option value="10001" selected>Bug &lt;script&gt;`) || strings.Contains(body, "<script>") || !strings.Contains(body, `hx-post="/flows/editor"`) {
		t.Fatal("select must show escaped names, retain IDs internally and reload dependent choices")
	}
	body = renderHTML(t, JiraDestination(0, 0, model.Action{Kind: "jira_request", ConnectionID: 2, ServiceDeskID: "10", RequestTypeID: "20", QueueID: 971}, connectors.JiraChoices{Error: "Jira non raggiungibile"}))
	for _, want := range []string{`role="alert">Jira non raggiungibile`, `option value="10" selected`, `option value="20" selected`, `option value="971" selected`, "Aggiorna scelte Jira"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, label := range []string{"ID Service Desk", "ID tipo richiesta", "ID coda"} {
		if strings.Contains(body, label) {
			t.Errorf("technical label remains: %s", label)
		}
	}
}
