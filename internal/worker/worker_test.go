package worker

import (
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"testing"
	"time"
)

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
