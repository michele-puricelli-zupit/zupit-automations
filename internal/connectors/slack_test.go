package connectors

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSlackFailureRetainsSafeDiagnosticsAndClassification(t *testing.T) {
	for _, tt := range []struct {
		code, want             string
		auth, retry, uncertain bool
	}{
		{"messages_tab_disabled", "messages_tab_disabled", false, false, false},
		{"channel_not_found", "channel_not_found", false, false, false},
		{"user_not_found", "user_not_found", false, false, false},
		{"invalid_blocks", "invalid_blocks", false, false, false},
		{"missing_scope", "missing_scope", false, false, false},
		{"invalid_auth", "invalid_auth", true, false, false},
		{"rate_limited", "rate_limited", false, true, false},
		{"internal_error", "internal_error", false, false, true},
		{"PRIVATE token or payload", "unrecognized_error", false, false, false},
	} {
		t.Run(tt.code, func(t *testing.T) {
			f := slackFailure("chat.postMessage", tt.code)
			if f.Auth != tt.auth || f.Retryable != tt.retry || f.Uncertain != tt.uncertain || f.HTTPStatus != 200 {
				t.Fatalf("classification changed: %+v", f)
			}
			var out bytes.Buffer
			slog.New(slog.NewJSONHandler(&out, nil)).Warn("Slack failed", "error", f)
			for _, value := range []string{out.String(), f.Error()} {
				if !strings.Contains(value, tt.want) || !strings.Contains(value, "chat.postMessage") || strings.Contains(value, "PRIVATE") {
					t.Fatalf("unsafe or incomplete diagnostics: %s", value)
				}
			}
		})
	}
}
