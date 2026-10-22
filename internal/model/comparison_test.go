package model

import "testing"

func TestComparisonOnlyAcceptsPersonalSlackRecipients(t *testing.T) {
	for _, tt := range []struct {
		value string
		valid bool
	}{{"U0123456789", true}, {"W0123456789", true}, {"D0123456789", true}, {"C0123456789", false}, {"G0123456789", false}, {"U0123456789,U9876543210", false}, {"user@example.test", false}, {"D", false}, {"", false}} {
		c := ComparisonSettings{Instructions: "Classifica le segnalazioni", SlackConnectionID: 1, SlackRecipient: tt.value}
		if (c.Validate() == nil) != tt.valid {
			t.Errorf("recipient %q: expected valid=%v", tt.value, tt.valid)
		}
	}
}
