package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zupit-it/zupit-automations/internal/auth"
	"github.com/zupit-it/zupit-automations/internal/config"
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
)

func TestSecurityPolicyAllowsGoogleAuthorizationRedirect(t *testing.T) {
	server := &Server{Auth: &auth.Manager{}}
	w := httptest.NewRecorder()
	server.security(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth", http.StatusSeeOther)
	})).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/connections/1/authorize", nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("authorization redirect status=%d", w.Code)
	}
	target, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var formSources []string
	for _, directive := range strings.Split(w.Header().Get("Content-Security-Policy"), ";") {
		fields := strings.Fields(directive)
		if len(fields) > 0 && fields[0] == "form-action" {
			formSources = fields[1:]
		}
	}
	if len(formSources) != 2 || formSources[0] != "'self'" || formSources[1] != target.Scheme+"://"+target.Host {
		t.Fatalf("form-action must allow local forms and the Google redirect only: %v", formSources)
	}
}

func TestEditorDraftAndSimulationThroughProtectedRoutes(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	admins := map[string]bool{"admin@example.test": true}
	manager := &auth.Manager{Store: s, Cookie: "test_session", Config: config.Config{AdminEmails: admins, CSRFSecret: strings.Repeat("c", 32)}}
	server := &Server{Store: s, Auth: manager, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.Handler()
	for _, email := range []string{"admin@example.test", "viewer@example.test"} {
		u, err := s.ResolveUser(ctx, email, email, email, admins)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(email))
		if err = s.CreateSession(ctx, u.ID, hex.EncodeToString(digest[:]), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	mac := hmac.New(sha256.New, []byte(manager.Config.CSRFSecret))
	mac.Write([]byte("admin@example.test"))
	csrf := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	request := func(method, path, email string, form url.Values, hx bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		if email != "" {
			r.AddCookie(&http.Cookie{Name: manager.Cookie, Value: email})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tt := range []struct {
		path, email string
		status      int
	}{
		{"/flows/new?template=srt", "admin@example.test", 200},
		{"/flows/new?template=unknown", "admin@example.test", 400},
		{"/flows/new?template=srt", "viewer@example.test", 403},
		{"/flows/new?template=srt", "", 303},
	} {
		if w := request("GET", tt.path, tt.email, nil, false); w.Code != tt.status {
			t.Fatalf("%s as %s: status %d", tt.path, tt.email, w.Code)
		}
	}
	source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"_csrf": {csrf}, "name": {"Draft <script>unsafe()</script>"}, "source_id": {fmt.Sprint(source)},
		"filter.mode": {"all"}, "branch.0.name": {"Excluded"}, "branch.0.when.mode": {"all"},
		"branch.0.action.0.kind": {"ignore"}, "branch.0.action.0.reason": {"Test exclusion"},
	}
	for _, hx := range []bool{false, true} {
		form.Set("operation", "filter")
		w := request("POST", "/flows/editor", "admin@example.test", form, hx)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `name="filter.0.field"`) || strings.Contains(body, "<script>unsafe()</script>") {
			t.Fatalf("editor response invalid: status %d", w.Code)
		}
		if strings.Contains(strings.ToLower(body), "<!doctype html>") == hx || w.Header().Get("Vary") != "HX-Request" {
			t.Fatal("editor did not distinguish fragment and full-page requests")
		}
	}
	form.Del("operation")
	form.Set("_csrf", "forged")
	if w := request("POST", "/flows/0", "admin@example.test", form, false); w.Code != 403 {
		t.Fatal("forged CSRF token accepted")
	}
	form.Set("_csrf", csrf)
	w := request("POST", "/flows/0", "admin@example.test", form, false)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/flows/") {
		t.Fatalf("saving draft failed: status %d", w.Code)
	}
	path := w.Header().Get("Location")
	w = request("POST", path+"/simulate", "admin@example.test", url.Values{
		"_csrf": {csrf}, "raw": {"From: support@example.test\r\nTo: team@example.test\r\nSubject: Errore è\r\n\r\nDettagli"},
	}, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test exclusion") {
		t.Fatalf("simulation failed: status %d", w.Code)
	}
	if count, err := s.Client.Run.Query().Count(ctx); err != nil || count != 0 {
		t.Fatalf("simulation queued real actions: count %d, error %v", count, err)
	}
	jira, err := s.SaveConnection(ctx, model.Connection{Name: "Jira", Provider: "jira", Config: map[string]string{"url": "https://example.atlassian.net", "email": "technical@example.test"}}, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	server.Clients = connectors.New(s, oauth2.Config{})
	lookupStatus := 200
	server.Clients.HTTP.Transport = jiraTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("editing must never create a Jira ticket")
		}
		catalog := map[string]string{
			"/rest/api/3/project/search":                      `{"values":[{"id":"100","name":"Supporto","key":"SUP"}],"isLast":true}`,
			"/rest/agile/1.0/board":                           `{"values":[{"id":42,"name":"Lavoro del team"}],"isLast":true}`,
			"/rest/api/3/issue/createmeta/SUP/issuetypes":     `{"issueTypes":[{"id":"10001","name":"Bug"}],"total":1}`,
			"/rest/servicedeskapi/servicedesk":                `{"values":[{"id":"10","projectKey":"HELP","projectName":"Assistenza clienti"}],"isLastPage":true}`,
			"/rest/servicedeskapi/servicedesk/10/requesttype": `{"values":[{"id":"20","name":"Segnala un problema"}],"isLastPage":true}`,
			"/rest/servicedeskapi/servicedesk/10/queue":       `{"values":[{"id":"971","name":"Da lavorare"}],"isLastPage":true}`,
		}
		body, ok := catalog[r.URL.Path]
		if !ok {
			t.Fatalf("unexpected Jira API: %s", r.URL)
		}
		return &http.Response{StatusCode: lookupStatus, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	jiraForm := url.Values{
		"_csrf": {csrf}, "name": {"Jira modes"}, "source_id": {fmt.Sprint(source)}, "branch.0.name": {"Main"},
		"branch.0.action.0.connection_id": {fmt.Sprint(jira)}, "branch.0.action.0.subject": {"Test Jira"},
		"branch.0.action.0.service_desk_id": {"10"}, "branch.0.action.0.request_type_id": {"20"}, "branch.0.action.0.queue_id": {"971"},
		"branch.0.action.0.project": {"SUP"}, "branch.0.action.0.issue_type": {"10001"}, "branch.0.action.0.board_id": {"42"},
	}
	for _, hx := range []bool{false, true} {
		for _, kind := range []string{"jira", "jira_request"} {
			jiraForm.Set("branch.0.action.0.kind", kind)
			w := request("POST", "/flows/editor", "admin@example.test", jiraForm, hx)
			body := w.Body.String()
			if w.Code != 200 || !strings.Contains(body, `value="Test Jira"`) || strings.Contains(body, `name="branch.0.action.0.queue_id"`) != (kind == "jira_request") || strings.Contains(body, `name="branch.0.action.0.board_id"`) != (kind == "jira") {
				t.Fatalf("switching Jira mode %s (HTMX=%v) failed: status %d", kind, hx, w.Code)
			}
			labels := []string{"Supporto · SUP", "Lavoro del team", "Bug"}
			if kind == "jira_request" {
				labels = []string{"Assistenza clienti · HELP", "Segnala un problema", "Da lavorare"}
			}
			for _, label := range labels {
				if !strings.Contains(body, label) {
					t.Errorf("missing readable option %q", label)
				}
			}
		}
	}
	// A failed reload must keep both the selected identifiers and unrelated form values.
	lookupStatus = 403
	w = request("POST", "/flows/editor", "admin@example.test", jiraForm, true)
	if body := w.Body.String(); !strings.Contains(body, "HTTP 403") || !strings.Contains(body, `option value="20" selected`) || !strings.Contains(body, `value="Test Jira"`) {
		t.Fatal("Jira failure lost the draft or its selected request type")
	}
	lookupStatus = 200
	jiraForm.Del("branch.0.action.0.board_id")
	w = request("POST", "/flows/0", "admin@example.test", jiraForm, false)
	if w.Code != 303 {
		t.Fatalf("saving Service Management without board failed: status %d", w.Code)
	}
	saved, err := s.Flow(ctx, intValue(strings.TrimPrefix(w.Header().Get("Location"), "/flows/")))
	if err != nil {
		t.Fatal(err)
	}
	a := saved.Definition.Branches[0].Actions[0]
	if a.Kind != "jira_request" || a.QueueID != 971 || a.BoardID != 0 || a.ServiceDeskID != "10" || a.RequestTypeID != "20" {
		t.Fatalf("saved Jira destination differs: %+v", a)
	}
	w = request("GET", w.Header().Get("Location"), "admin@example.test", nil, false)
	if !strings.Contains(w.Body.String(), "Segnala un problema") {
		t.Fatal("reopening a saved flow did not load readable Jira choices")
	}
}
