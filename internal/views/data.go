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
		return "Jira Board · crea un ticket di lavoro"
	case "jira_request":
		return "Jira Service Management · apri una richiesta di assistenza"
	case "slack":
		return "Slack · aggiungi un elemento a una lista"
	case "forward":
		return "Email · inoltra il messaggio"
	case "ignore":
		return "Ignora l’email · nessuna azione"
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

func choiceLabel(value string) string {
	labels := map[string]string{
		"from":        "Mittente dell’email",
		"reply_to":    "Indirizzo a cui rispondere",
		"to":          "Destinatari dell’email",
		"subject":     "Oggetto dell’email",
		"body":        "Testo dell’email",
		"id":          "Identificativo dell’email (avanzato)",
		"received_at": "Data e ora di ricezione",
		"equals":      "È esattamente uguale a",
		"prefix":      "Inizia con",
		"contains":    "Contiene il testo",
		"regex":       "Segue un modello di testo (regex, avanzato)",
		"text":        "Testo · parole e frasi",
		"number":      "Numero · es. 10 oppure 2.5",
		"boolean":     "Sì o no · true / false",
		"option":      "Voce di un elenco · identificativo dell’opzione",
		"user":        "Persona · identificativo utente",
		"date":        "Data · formato AAAA-MM-GG",
		"link":        "Collegamento web · URL completo",
		"json":        "Dati strutturati JSON (avanzato)",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}
