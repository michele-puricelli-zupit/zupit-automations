package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
)

func TestJiraServiceManagementCreationAndQueue(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	id, err := s.SaveConnection(ctx, model.Connection{Name: "Jira", Provider: "jira", Config: map[string]string{"url": "https://example.atlassian.net", "email": "technical@example.test"}}, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		queue   int
		pages   []string
		status  int
		warning bool
	}{
		{"without queue", 0, nil, 0, false},
		{"in queue", 971, []string{`{"values":[{"key":"TAM-1"}],"isLastPage":true}`}, 200, false},
		{"second page", 971, []string{`{"values":[{"key":"TAM-2"}],"isLastPage":false}`, `{"values":[{"key":"TAM-1"}],"isLastPage":true}`}, 200, false},
		{"missing", 971, []string{`{"values":[{"key":"TAM-2"}],"isLastPage":true}`}, 200, true},
		{"empty", 971, []string{`{"values":[],"isLastPage":false}`}, 200, true},
		{"forbidden", 971, []string{`{}`}, 403, true},
		{"unavailable", 971, []string{`{}`}, 503, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := New(s, oauth2.Config{})
			creates, reads := 0, 0
			c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" && r.URL.Path == "/rest/servicedeskapi/request" {
					creates++
					var payload struct {
						ServiceDeskID string         `json:"serviceDeskId"`
						RequestTypeID string         `json:"requestTypeId"`
						Reporter      string         `json:"raiseOnBehalfOf"`
						Fields        map[string]any `json:"requestFieldValues"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload.ServiceDeskID != "10" || payload.RequestTypeID != "20" || payload.Reporter != "person-id" || payload.Fields["summary"] != "Test" || payload.Fields["description"] != "Body" || payload.Fields["customfield_1"] != "Value" {
						t.Fatalf("wrong request payload: %+v", payload)
					}
					if _, exists := payload.Fields["board_id"]; exists {
						t.Fatal("board leaked into request payload")
					}
					return response(201, `{"issueKey":"TAM-1"}`), nil
				}
				if r.Method != "GET" || r.URL.Path != "/rest/servicedeskapi/servicedesk/10/queue/971/issue" || reads >= len(tt.pages) || r.URL.Query().Get("start") != fmt.Sprint(reads) {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				body := tt.pages[reads]
				reads++
				return response(tt.status, body), nil
			})
			a := model.Action{Kind: "jira_request", ConnectionID: id, ServiceDeskID: "10", RequestTypeID: "20", QueueID: tt.queue, BoardID: 42, Reporter: "person-id", Subject: "Test", Body: "Body", Fields: []model.Mapping{{Field: "customfield_1", Type: "text", Value: "Value"}}}
			result, err := c.Execute(ctx, a)
			if err != nil || creates != 1 || reads != len(tt.pages) || (result.Warning != "") != tt.warning || result.Reference != "https://example.atlassian.net/browse/TAM-1" {
				t.Fatalf("result=%+v error=%v creates=%d reads=%d", result, err, creates, reads)
			}
		})
	}
}

func TestJiraVerifiesSelectedDestination(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	id, err := s.SaveConnection(ctx, model.Connection{Name: "Jira", Provider: "jira", Config: map[string]string{"url": "https://example.atlassian.net", "email": "technical@example.test"}}, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		kind   string
		queue  int
		status int
		paths  []string
	}{
		{"jira", 0, 200, []string{"/rest/agile/1.0/board/42/configuration", "/rest/api/3/issue/createmeta/TEST/issuetypes/10001"}},
		{"jira_request", 0, 200, []string{"/rest/servicedeskapi/servicedesk/10/requesttype/20/field"}},
		{"jira_request", 971, 200, []string{"/rest/servicedeskapi/servicedesk/10/queue/971", "/rest/servicedeskapi/servicedesk/10/requesttype/20/field"}},
		{"jira_request", 971, 403, []string{"/rest/servicedeskapi/servicedesk/10/queue/971"}},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d", tt.kind, tt.queue, tt.status), func(t *testing.T) {
			c := New(s, oauth2.Config{})
			var paths []string
			c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Fatal("destination verification must be read-only")
				}
				paths = append(paths, r.URL.Path)
				return response(tt.status, `{}`), nil
			})
			err := c.VerifyAction(ctx, model.Action{Kind: tt.kind, ConnectionID: id, Project: "TEST", IssueType: "10001", BoardID: 42, ServiceDeskID: "10", RequestTypeID: "20", QueueID: tt.queue})
			if (err != nil) != (tt.status != 200) || !reflect.DeepEqual(paths, tt.paths) {
				t.Fatalf("paths=%v error=%v", paths, err)
			}
		})
	}
}
