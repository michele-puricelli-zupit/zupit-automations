package worker

import (
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"testing"
	"time"
)

func TestParseGmailNotification(t *testing.T) {
	for _, tt := range []struct {
		name, payload, history string
		valid                  bool
	}{
		{"string history", `{"emailAddress":"team@example.test","historyId":"9876543210"}`, "9876543210", true},
		{"numeric history", `{"emailAddress":"team@example.test","historyId":9876543210}`, "9876543210", true},
		{"large history", `{"emailAddress":"team@example.test","historyId":18446744073709551615}`, "18446744073709551615", true},
		{"invalid JSON", `not JSON`, "", false},
		{"missing email", `{"historyId":"123"}`, "", false},
		{"missing history", `{"emailAddress":"team@example.test"}`, "", false},
		{"null history", `{"emailAddress":"team@example.test","historyId":null}`, "", false},
		{"empty history", `{"emailAddress":"team@example.test","historyId":""}`, "", false},
		{"invalid history", `{"emailAddress":"team@example.test","historyId":"private"}`, "", false},
		{"fractional history", `{"emailAddress":"team@example.test","historyId":1.5}`, "", false},
		{"negative history", `{"emailAddress":"team@example.test","historyId":-1}`, "", false},
		{"zero history", `{"emailAddress":"team@example.test","historyId":0}`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event, err := parseGmailNotification([]byte(tt.payload))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v: %v", tt.valid, err)
			}
			if tt.valid && (event.Email != "team@example.test" || event.HistoryID.String() != tt.history) {
				t.Fatalf("notification changed: %+v", event)
			}
		})
	}
}

func TestRetryPolicy(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name    string
		attempt int
		failure *connectors.Failure
		status  string
		delay   time.Duration
	}{
		{"first transient", 1, &connectors.Failure{Message: "rate limit", Retryable: true}, "pending", 10 * time.Second},
		{"last retry", 2, &connectors.Failure{Retryable: true}, "pending", time.Minute},
		{"exhausted", 3, &connectors.Failure{Retryable: true}, "failed", 0},
		{"respect service", 1, &connectors.Failure{Retryable: true, After: 5 * time.Minute}, "pending", 5 * time.Minute},
		{"ambiguous write", 1, &connectors.Failure{Uncertain: true}, "uncertain", 0},
		{"revoked token", 3, &connectors.Failure{Auth: true}, "pending", time.Minute},
		{"validation", 1, &connectors.Failure{}, "failed", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status, _, next := Outcome(tt.attempt, tt.failure, now)
			if status != tt.status || next.Sub(now) != tt.delay {
				t.Fatalf("%s %s", status, next.Sub(now))
			}
		})
	}
}
