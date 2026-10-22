package model

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

type ComparisonSettings struct {
	Instructions      string `json:"instructions"`
	SlackConnectionID int    `json:"slack_connection_id"`
	SlackRecipient    string `json:"slack_recipient"`
}

var slackRecipientRE = regexp.MustCompile(`^[UWD][A-Z0-9]{8,}$`)

func ValidSlackRecipient(v string) bool { return slackRecipientRE.MatchString(v) }

func (c ComparisonSettings) Validate() error {
	if strings.TrimSpace(c.Instructions) == "" || len(c.Instructions) > 8000 {
		return errors.New("descrivere le regole per Jev, massimo 8000 byte")
	}
	if c.SlackConnectionID <= 0 || !ValidSlackRecipient(c.SlackRecipient) {
		return errors.New("il confronto richiede una connessione Slack e un ID membro U…/W… o DM D…; i canali sono esclusi")
	}
	return nil
}

type Proposal struct {
	Branch  string   `json:"branch"`
	Actions []Action `json:"actions,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type ComparisonResult struct {
	A             Proposal           `json:"a"`
	B             Proposal           `json:"b"`
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Model         string             `json:"model"`
	InputTokens   int                `json:"input_tokens"`
	OutputTokens  int                `json:"output_tokens"`
	DurationMS    int64              `json:"duration_ms"`
}

type Comparison struct {
	ID, RunID, Attempts      int
	Status, Error, Reference string
	Result                   ComparisonResult
	NextAt                   time.Time
	Snapshot                 Snapshot
}
