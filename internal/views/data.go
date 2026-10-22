package views

import (
	"encoding/json"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/model"
	"strconv"
	"strings"
	"time"
)

type Data struct {
	Title, Active, CSRF, Error, Notice string
	User                               model.User
	Connections                        []model.Connection
	Flows                              []model.Flow
	Runs                               []model.Run
	Connection                         model.Connection
	Flow                               model.Flow
	Run                                model.Run
	Raw, Simulation, Branch            string
	JevReady                           bool
}

func comparisonConfig(d model.Definition) model.ComparisonSettings {
	if d.Comparison != nil {
		return *d.Comparison
	}
	return model.ComparisonSettings{}
}

func comparisonInfo(r model.ComparisonResult) string {
	if r.Choice == "" {
		return r.Model + " · analisi non completata"
	}
	return fmt.Sprintf("%s · confidenza %.1f%% · %d ms · token %d/%d", r.Model, r.Confidence*100, r.DurationMS, r.InputTokens, r.OutputTokens)
}

func ID(v int) string                   { return strconv.Itoa(v) }
func path(prefix string, id int) string { return prefix + ID(id) }
func key(parts ...any) string {
	r := make([]string, len(parts))
	for i, p := range parts {
		r[i] = fmt.Sprint(p)
	}
	return strings.Join(r, ".")
}
func pretty(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func date(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("02/01/2006 15:04:05")
}
func status(s string) string {
	switch s {
	case "ready":
		return "Collegata"
	case "pending":
		return "In attesa"
	case "paused":
		return "Da ricollegare"
	case "succeeded":
		return "Completata"
	case "failed":
		return "Errore"
	case "uncertain":
		return "Da verificare"
	case "warning":
		return "Con avviso"
	case "ignored":
		return "Esclusa"
	case "running":
		return "In corso"
	case "evaluating":
		return "Analisi Jev"
	case "evaluated":
		return "Report da inviare"
	case "sending":
		return "Invio DM"
	}
	return s
}
func provider(s string) string {
	switch s {
	case "gmail":
		return "Gmail"
	case "jira":
		return "Jira Cloud"
	case "slack":
		return "Slack"
	}
	return s
}
func action(s string) string {
	switch s {
	case "jira":
		return "Crea ticket Jira"
	case "jira_request":
		return "Crea richiesta Service Management"
	case "slack":
		return "Aggiungi elemento Slack List"
	case "forward":
		return "Inoltra email"
	case "ignore":
		return "Escludi con motivo"
	}
	return s
}
func sourceName(id int, connections []model.Connection) string {
	for _, c := range connections {
		if c.ID == id {
			return c.Name
		}
	}
	return "Connessione da configurare"
}
func providerFor(kind string) string {
	switch kind {
	case "jira_request":
		return "jira"
	case "forward":
		return "gmail"
	}
	return kind
}
func latency(r model.Run) string {
	if r.CompletedAt.IsZero() {
		return "—"
	}
	return r.CompletedAt.Sub(r.CreatedAt).Round(time.Millisecond).String()
}
func safeReference(v string) bool {
	return strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "slack://")
}

func choices(v ...string) []string { return v }
