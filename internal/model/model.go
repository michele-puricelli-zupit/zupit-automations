package model

import (
	"encoding/json"
	"time"
)

type Message struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body"`
	ReceivedAt time.Time `json:"received_at"`
}

type Condition struct {
	Field      string `json:"field"`
	Operator   string `json:"operator"`
	Value      string `json:"value"`
	Not        bool   `json:"not,omitempty"`
	IgnoreCase bool   `json:"ignore_case,omitempty"`
}
type Group struct {
	Mode       string      `json:"mode"`
	Conditions []Condition `json:"conditions"`
}
type Extractor struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Pattern  string `json:"pattern"`
	Required bool   `json:"required"`
}
type Mapping struct {
	Field string `json:"field"`
	Type  string `json:"type"`
	Value string `json:"value"`
}
type Action struct {
	Kind          string    `json:"kind"`
	ConnectionID  int       `json:"connection_id"`
	Project       string    `json:"project,omitempty"`
	BoardID       int       `json:"board_id,omitempty"`
	IssueType     string    `json:"issue_type,omitempty"`
	Reporter      string    `json:"reporter,omitempty"`
	ServiceDeskID string    `json:"service_desk_id,omitempty"`
	RequestTypeID string    `json:"request_type_id,omitempty"`
	ListID        string    `json:"list_id,omitempty"`
	To            string    `json:"to,omitempty"`
	Subject       string    `json:"subject,omitempty"`
	Body          string    `json:"body,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	Fields        []Mapping `json:"fields,omitempty"`
}
type Branch struct {
	Name    string   `json:"name"`
	When    Group    `json:"when"`
	Actions []Action `json:"actions"`
}
type Definition struct {
	Version    int                 `json:"version"`
	Filter     Group               `json:"filter"`
	Extractors []Extractor         `json:"extractors"`
	Branches   []Branch            `json:"branches"`
	Comparison *ComparisonSettings `json:"comparison,omitempty"`
}
type Flow struct {
	ID          int
	Name        string
	SourceID    int
	Enabled     bool
	Definition  Definition
	ActivatedAt time.Time
}
type Connection struct {
	ID           int
	Name         string
	Provider     string
	Config       map[string]string
	Status       string
	Error        string
	HistoryID    string
	WatchExpires time.Time
	Cutover      time.Time
}
type User struct {
	ID     int
	Email  string
	Name   string
	Admin  bool
	Active bool
}
type Run struct {
	ID          int
	FlowID      int
	FlowName    string
	SourceID    int
	MessageID   string
	Subject     string
	Status      string
	CreatedAt   time.Time
	CompletedAt time.Time
	Steps       []Step
	Comparison  *Comparison
}
type Step struct {
	ID        int
	RunID     int
	Position  int
	Action    Action
	Status    string
	Attempts  int
	NextAt    time.Time
	Reference string
	Error     string
	StartedAt time.Time
}
type Result struct {
	Reference string
	Warning   string
}
type Snapshot struct {
	FlowName   string     `json:"flow_name"`
	Definition Definition `json:"definition"`
	Message    Message    `json:"message"`
}

func JSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
