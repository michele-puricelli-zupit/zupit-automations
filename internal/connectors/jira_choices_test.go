package connectors

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
)

func TestJiraDestinationChoices(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	conn := model.Connection{Name: "Jira", Provider: "jira", Config: map[string]string{"url": "https://example.atlassian.net", "email": "technical@example.test"}}
	var err error
	conn.ID, err = s.SaveConnection(ctx, conn, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		action model.Action
		paths  []string
		pages  []string
		want   JiraChoices
	}{
		{
			name:   "board with paginated projects and ticket types",
			action: model.Action{Kind: "jira", Project: "SUP"},
			paths:  []string{"/rest/api/3/project/search?maxResults=100&startAt=0", "/rest/api/3/project/search?maxResults=100&startAt=1", "/rest/agile/1.0/board?maxResults=100&projectKeyOrId=SUP&startAt=0", "/rest/api/3/issue/createmeta/SUP/issuetypes?maxResults=100&startAt=0", "/rest/api/3/issue/createmeta/SUP/issuetypes?maxResults=100&startAt=2"},
			pages:  []string{`{"values":[{"id":"2","key":"SUP","name":"Supporto"}],"isLast":false,"total":2}`, `{"values":[{"id":"1","key":"APP","name":"Applicazioni"}],"isLast":true,"total":2}`, `{"values":[{"id":42,"name":"Assistenza"}],"isLast":true}`, `{"issueTypes":[{"id":"10001","name":"Bug"},{"id":"10002","name":"Sottoattività","subtask":true}],"total":3}`, `{"issueTypes":[{"id":"10003","name":"Attività"}],"total":3}`},
			want:   JiraChoices{Projects: []JiraOption{{"APP", "Applicazioni · APP"}, {"SUP", "Supporto · SUP"}}, Boards: []JiraOption{{"42", "Assistenza"}}, IssueTypes: []JiraOption{{"10003", "Attività"}, {"10001", "Bug"}}},
		},
		{
			name:   "service desk and paginated request types",
			action: model.Action{Kind: "jira_request", ServiceDeskID: "10"},
			paths:  []string{"/rest/servicedeskapi/servicedesk?limit=100&start=0", "/rest/servicedeskapi/servicedesk/10/requesttype?limit=100&start=0", "/rest/servicedeskapi/servicedesk/10/requesttype?limit=100&start=1", "/rest/servicedeskapi/servicedesk/10/queue?limit=100&start=0"},
			pages:  []string{`{"values":[{"id":"10","projectKey":"HELP","projectName":"Assistenza clienti"}],"isLastPage":true}`, `{"values":[{"id":"20","name":"Segnala un problema"}],"isLastPage":false}`, `{"values":[{"id":"21","name":"Richiedi accesso"}],"isLastPage":true}`, `{"values":[{"id":"971","name":"Da lavorare"}],"isLastPage":true}`},
			want:   JiraChoices{ServiceDesks: []JiraOption{{"10", "Assistenza clienti · HELP"}}, RequestTypes: []JiraOption{{"21", "Richiedi accesso"}, {"20", "Segnala un problema"}}, Queues: []JiraOption{{"971", "Da lavorare"}}},
		},
		{
			name:   "no project selected and empty catalog",
			action: model.Action{Kind: "jira"},
			paths:  []string{"/rest/api/3/project/search?maxResults=100&startAt=0"},
			pages:  []string{`{"values":[],"isLast":false}`},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := New(s, oauth2.Config{})
			reads := 0
			c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || reads >= len(tt.paths) || r.URL.RequestURI() != tt.paths[reads] {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
				}
				if user, pass, ok := r.BasicAuth(); !ok || user != conn.Config["email"] || pass != "secret" {
					t.Fatal("missing Jira authentication")
				}
				body := tt.pages[reads]
				reads++
				return response(200, body), nil
			})
			got := c.JiraDestinations(ctx, conn, tt.action)
			if !reflect.DeepEqual(got, tt.want) || reads != len(tt.paths) {
				t.Fatalf("got %+v, want %+v; reads=%d", got, tt.want, reads)
			}
		})
	}
	for _, status := range []int{401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := New(s, oauth2.Config{})
			c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Fatal("catalog lookup must be read-only")
				}
				switch r.URL.Path {
				case "/rest/servicedeskapi/servicedesk":
					return response(200, `{"values":[{"id":"10","projectKey":"HELP","projectName":"Assistenza"}],"isLastPage":true}`), nil
				case "/rest/servicedeskapi/servicedesk/10/requesttype":
					return response(200, `{"values":[{"id":"20","name":"Richiedi assistenza"}],"isLastPage":true}`), nil
				default:
					return response(status, `{}`), nil
				}
			})
			got := c.JiraDestinations(ctx, conn, model.Action{Kind: "jira_request", ServiceDeskID: "10"})
			if got.Error != "" || got.QueueError == "" || len(got.RequestTypes) != 1 {
				t.Fatalf("optional queue failure blocked requests: %+v", got)
			}
			got = c.JiraDestinations(ctx, conn, model.Action{Kind: "jira"})
			if !strings.Contains(got.Error, fmt.Sprint(status)) {
				t.Fatalf("missing API failure: %+v", got)
			}
		})
	}
}
