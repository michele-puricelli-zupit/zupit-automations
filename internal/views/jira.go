package views

import "github.com/zupit-it/zupit-automations/internal/connectors"

func hasJiraOption(options []connectors.JiraOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}
