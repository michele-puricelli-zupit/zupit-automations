package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
)

func TestComparisonRejectsPublicAndGroupDestinationsBeforeAnyCall(t *testing.T) {
	c := &Clients{}
	for _, recipient := range []string{"C0123456789", "G0123456789", "U0123456789,U9876543210", "#general"} {
		if _, err := c.ComparisonDM(context.Background(), 1, recipient); err == nil {
			t.Errorf("unsafe destination accepted: %s", recipient)
		}
	}
}

func TestComparisonDMUsesSingleMemberAndPlainText(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	id, err := s.SaveConnection(ctx, model.Connection{Name: "Slack", Provider: "slack", Config: map[string]string{}}, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConnectionState(ctx, id, "ready", ""); err != nil {
		t.Fatal(err)
	}
	c := New(s, oauth2.Config{})
	posted := 0
	group := false
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/api/conversations.open":
			if body["users"] != "U0123456789" || body["return_im"] != true {
				t.Fatal("not a one-person DM request")
			}
			if group {
				return response(200, `{"ok":true,"channel":{"id":"G0123456789","is_im":false}}`), nil
			}
			return response(200, `{"ok":true,"channel":{"id":"D0123456789","is_im":true}}`), nil
		case "/api/chat.postMessage":
			posted++
			if body["channel"] != "D0123456789" || strings.Contains(body["text"].(string), "<@") || body["unfurl_links"] != false {
				t.Fatal("unsafe report delivery")
			}
			block := body["blocks"].([]any)[0].(map[string]any)["text"].(map[string]any)
			if block["type"] != "plain_text" || block["text"] != "Proposta: <@here> <https://example.test|link>" {
				t.Fatal("report content can trigger Slack markup")
			}
			return response(200, `{"ok":true,"channel":"D0123456789","ts":"123.456"}`), nil
		default:
			t.Fatalf("B attempted an unexpected external action: %s", r.URL)
			return nil, nil
		}
	})
	cfg := model.ComparisonSettings{SlackConnectionID: id, SlackRecipient: "U0123456789"}
	if ref, err := c.SendComparison(ctx, cfg, "Proposta: <@here> <https://example.test|link>"); err != nil || ref != "D0123456789:123.456" || posted != 1 {
		t.Fatalf("private report failed: %s %v", ref, err)
	}
	group = true
	if _, err := c.SendComparison(ctx, cfg, "Do not post"); err == nil || posted != 1 {
		t.Fatal("report was sent into a group conversation")
	}
}
