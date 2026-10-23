package web

import (
	"net/http"
	"net/url"
	"testing"
)

type jiraTestTransport func(*http.Request) (*http.Response, error)

func (f jiraTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJiraParentChangeClearsDependentChoices(t *testing.T) {
	const prefix = "branch.0.action.0."
	for _, tt := range []struct {
		kind, previous, value string
		cleared, kept         []string
	}{
		{"jira", "previous_project", "OLD", []string{"issue_type", "board_id"}, []string{"connection_id", "project", "subject"}},
		{"jira_request", "previous_service_desk_id", "OLD", []string{"request_type_id", "queue_id"}, []string{"connection_id", "service_desk_id", "subject"}},
		{"jira", "previous_connection_id", "9", []string{"project", "issue_type", "board_id", "service_desk_id", "request_type_id", "queue_id", "reporter"}, []string{"connection_id", "subject"}},
		{"jira", "previous_project", "SUP", nil, []string{"project", "issue_type", "board_id", "subject"}},
		{"jira_request", "previous_service_desk_id", "10", nil, []string{"service_desk_id", "request_type_id", "queue_id", "subject"}},
	} {
		t.Run(tt.kind+"/"+tt.previous+"/"+tt.value, func(t *testing.T) {
			v := url.Values{"branch.0.name": {"Supporto"}, prefix + "kind": {tt.kind}, prefix + "connection_id": {"2"}, prefix + "project": {"SUP"}, prefix + "issue_type": {"10001"}, prefix + "board_id": {"42"}, prefix + "service_desk_id": {"10"}, prefix + "request_type_id": {"20"}, prefix + "queue_id": {"971"}, prefix + "subject": {"Email da lavorare"}, prefix + "reporter": {"person-id"}, prefix + tt.previous: {tt.value}}
			a := parseFlow(v).Definition.Branches[0].Actions[0]
			got := map[string]any{"connection_id": a.ConnectionID, "project": a.Project, "issue_type": a.IssueType, "board_id": a.BoardID, "service_desk_id": a.ServiceDeskID, "request_type_id": a.RequestTypeID, "queue_id": a.QueueID, "subject": a.Subject, "reporter": a.Reporter}
			for _, field := range tt.cleared {
				if got[field] != "" && got[field] != 0 {
					t.Errorf("stale %s retained: %v", field, got[field])
				}
			}
			for _, field := range tt.kept {
				if got[field] == "" || got[field] == 0 {
					t.Errorf("%s unexpectedly cleared", field)
				}
			}
		})
	}
}
