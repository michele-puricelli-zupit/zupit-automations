package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestMIMEAndHTML(t *testing.T) {
	raw := "From: Test <SUPPORT@example.test>\r\nTo: team@example.test\r\nSubject: =?UTF-8?Q?Errore_=22citt=C3=A0=22?=\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=sample\r\n\r\n--sample\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<div>Testo &amp; accenti è <a href=\"https://help.example.test/42\">Apri ticket</a><script>bad()</script></div>\r\n--sample--\r\n"
	m, err := ParseEmail(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "support@example.test" || m.Subject != `Errore "città"` || !strings.Contains(m.Body, "Testo & accenti è") || strings.Contains(m.Body, "bad()") {
		t.Fatalf("bad decode: %+v", m)
	}
	if !strings.Contains(m.Body, "https://help.example.test/42") {
		t.Fatal("HTML ticket link was lost")
	}
	if _, err := ParseEmail("not an email"); err == nil {
		t.Fatal("invalid RFC822 accepted")
	}
}

func TestReplyToIsConsistentInSimulationAndGmail(t *testing.T) {
	for _, tt := range []struct{ header, want string }{
		{"", ""},
		{"Jira <JIRA@zupit-pro.atlassian.net>", "jira@zupit-pro.atlassian.net"},
		{"Team <TEAM@example.test>, User <USER@example.test>", "team@example.test, user@example.test"},
	} {
		t.Run(tt.header, func(t *testing.T) {
			raw := "From: Support <support@example.test>\r\nSubject: Example\r\n"
			if tt.header != "" {
				raw += "Reply-To: " + tt.header + "\r\n"
			}
			simulated, err := ParseEmail(raw + "\r\nBody")
			if err != nil || simulated.ReplyTo != tt.want {
				t.Fatalf("simulation Reply-To=%q, want %q: %v", simulated.ReplyTo, tt.want, err)
			}
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				payload := gmailMessage{ID: "test", InternalDate: "1792742400000", Payload: gmailPart{MimeType: "text/plain"}}
				payload.Payload.Headers = []gmailHeader{{Name: "From", Value: "support@example.test"}, {Name: "Reply-To", Value: tt.header}}
				payload.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("Body"))
				b, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				return response(200, string(b)), nil
			})}
			c := &Clients{}
			message, accepted, err := c.message(context.Background(), client, "test")
			if err != nil || !accepted || message.ReplyTo != simulated.ReplyTo {
				t.Fatalf("Gmail and simulation differ: %+v %v %v", message, accepted, err)
			}
		})
	}
}

func TestHTTPFailureClassification(t *testing.T) {
	for _, tt := range []struct {
		code                   int
		method                 string
		retry, uncertain, auth bool
	}{{429, "POST", true, false, false}, {503, "POST", false, true, false}, {503, "GET", true, false, false}, {401, "POST", false, false, true}, {400, "POST", false, false, false}} {
		client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			r := response(tt.code, `{"secret":"must-not-leak"}`)
			r.Header.Set("Retry-After", "120")
			return r, nil
		})}
		c := &Clients{}
		err := c.do(context.Background(), client, tt.method, "https://example.test", map[string]string{}, nil, &map[string]any{})
		f := Classify(err)
		if f.HTTPStatus != tt.code || f.Retryable != tt.retry || f.Uncertain != tt.uncertain || f.Auth != tt.auth || strings.Contains(f.Message, "must-not-leak") {
			t.Fatalf("%d %s: %+v", tt.code, tt.method, f)
		}
		if tt.code == 429 && f.After != 2*time.Minute {
			t.Fatal("Retry-After ignored")
		}
	}
}

func TestGmailConfigurationFailures(t *testing.T) {
	for _, tt := range []struct {
		name, endpoint, body, message string
		auth                          bool
	}{
		{"API disabled", gmailBase + "/profile", `{"error":{"message":"private detail","errors":[{"reason":"accessNotConfigured"}]}}`, "Abilitare Gmail API nel progetto Google Cloud del client OAuth, poi verificare nuovamente la connessione", false},
		{"publisher missing", gmailBase + "/watch", `{"error":{"message":"Error sending test message to Cloud PubSub projects/test/topics/gmail : User not authorized to perform this action.","errors":[{"reason":"forbidden"}]}}`, "Gmail non può pubblicare sul topic Pub/Sub: assegnare Pub/Sub Publisher a gmail-api-push@system.gserviceaccount.com sul topic configurato", false},
		{"other forbidden", gmailBase + "/watch", `{"error":{"message":"private detail","errors":[{"reason":"forbidden"}]}}`, "Il servizio ha risposto HTTP 403", true},
		{"other provider", "https://example.test/profile", `{"error":{"message":"private detail","errors":[{"reason":"accessNotConfigured"}]}}`, "Il servizio ha risposto HTTP 403", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return response(http.StatusForbidden, tt.body), nil
			})}
			c := &Clients{}
			f := Classify(c.do(context.Background(), client, "GET", tt.endpoint, nil, nil, nil))
			if f.Message != tt.message || f.Auth != tt.auth || f.Retryable || f.Uncertain {
				t.Fatalf("configuration failure: %+v", f)
			}
		})
	}
}

func TestJiraPreservesPayloadAndNeverRecreatesOnBoardFailure(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	id, err := s.SaveConnection(ctx, model.Connection{Name: "Jira", Provider: "jira", Config: map[string]string{"url": "https://example.atlassian.net", "email": "technical@example.test"}}, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	c := New(s, oauth2.Config{})
	creates := 0
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			creates++
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Fields["summary"] != `Errore "è"` {
				t.Fatal("subject modified")
			}
			if payload.Fields["reporter"].(map[string]any)["accountId"] != "person-id" {
				t.Fatal("reporter missing")
			}
			if payload.Fields["description"].(map[string]any)["type"] != "doc" {
				t.Fatal("ADF missing")
			}
			return response(201, `{"key":"TEST-1"}`), nil
		}
		return response(403, `{}`), nil
	})
	a := model.Action{Kind: "jira", ConnectionID: id, Project: "TEST", IssueType: "10001", BoardID: 42, Reporter: "person-id", Subject: `Errore "è"`, Body: "Prima riga\nSeconda"}
	result, err := c.Execute(ctx, a)
	if err != nil || creates != 1 || result.Warning == "" || result.Reference != "https://example.atlassian.net/browse/TEST-1" {
		t.Fatalf("bad result: %+v %v creates=%d", result, err, creates)
	}
}
func TestSlackSerializationAndAmbiguousErrors(t *testing.T) {
	fields, err := slackFields([]model.Mapping{{Field: "ColTitle", Type: "text", Value: `Titolo "è"`}, {Field: "ColNumber", Type: "number", Value: "12.5"}, {Field: "ColCheck", Type: "boolean", Value: "true"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(fields)
	if !strings.Contains(string(b), `"number":[12.5]`) || !strings.Contains(string(b), `"checkbox":true`) {
		t.Fatalf("bad fields: %s", b)
	}
	s := testutil.Database(t)
	ctx := context.Background()
	id, _ := s.SaveConnection(ctx, model.Connection{Name: "Slack", Provider: "slack", Config: map[string]string{}}, []byte(`{"token":"secret"}`))
	c := New(s, oauth2.Config{})
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"ok":false,"error":"internal_error"}`), nil
	})
	err = c.slack(ctx, id, "slackLists.items.create", nil, &map[string]any{})
	if !Classify(err).Uncertain {
		t.Fatal("ambiguous Slack mutation would be retried")
	}
}
func TestGmailHistoryExpiredResyncAndDuplicateNotifications(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	id, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{"email": "team@example.test"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := json.Marshal(&oauth2.Token{AccessToken: "test-token", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)})
	if err = s.SaveSecret(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	if err = s.Watch(ctx, id, "100", time.Now().Add(7*24*time.Hour), true); err != nil {
		t.Fatal(err)
	}
	target, _ := s.SaveConnection(ctx, model.Connection{Name: "Slack", Provider: "slack", Config: map[string]string{}}, nil)
	_ = s.ConnectionState(ctx, target, "ready", "")
	f := model.Flow{Name: "test", SourceID: id, Enabled: true, Definition: model.Definition{Version: 1, Filter: model.Group{Mode: "all"}, Branches: []model.Branch{{Name: "all", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "slack", ConnectionID: target, ListID: "FTEST", Fields: []model.Mapping{{Field: "ColTitle", Type: "text", Value: "{{ .subject }}"}}}}}}}}
	if _, err = s.SaveFlow(ctx, f); err != nil {
		t.Fatal(err)
	}
	c := New(s, oauth2.Config{})
	expired := true
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing Gmail bearer token")
		}
		switch r.URL.Path {
		case "/gmail/v1/users/me/history":
			if expired {
				expired = false
				return response(404, `{}`), nil
			}
			return response(200, `{"historyId":"201","history":[{"messagesAdded":[{"message":{"id":"m1"}}]}]}`), nil
		case "/gmail/v1/users/me/profile":
			return response(200, `{"historyId":"200"}`), nil
		case "/gmail/v1/users/me/messages":
			if !strings.Contains(r.URL.Query().Get("q"), "after:") {
				t.Fatal("cutover missing")
			}
			return response(200, `{"messages":[{"id":"m1"}]}`), nil
		case "/gmail/v1/users/me/messages/m1":
			data := base64.RawURLEncoding.EncodeToString([]byte("Contenuto è"))
			return response(200, fmt.Sprintf(`{"id":"m1","internalDate":"%d","labelIds":["INBOX"],"payload":{"mimeType":"text/plain","headers":[{"name":"From","value":"Support <support@example.test>"},{"name":"Subject","value":"Nuova segnalazione"}],"body":{"data":"%s"}}}`, time.Now().Add(time.Second).UnixMilli(), data)), nil
		}
		t.Fatalf("unexpected request: %s", r.URL)
		return nil, nil
	})
	conn, _ := s.Connection(ctx, id)
	if err = c.Sync(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err = c.Sync(ctx, conn); err != nil {
		t.Fatal(err)
	}
	count, err := s.Client.Run.Query().Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("duplicate runs: %d %v", count, err)
	}
	conn, _ = s.Connection(ctx, id)
	if conn.HistoryID != "201" {
		t.Fatal("checkpoint not recovered")
	}
}
