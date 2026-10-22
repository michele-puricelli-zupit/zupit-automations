package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/zupit-it/zupit-automations/internal/model"
)

var nameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
var funcs = template.FuncMap{"trim": strings.TrimSpace, "lower": strings.ToLower, "upper": strings.ToUpper, "replace": strings.ReplaceAll, "urlquery": url.QueryEscape}

func Variables(m model.Message) map[string]string {
	return map[string]string{"id": m.ID, "from": m.From, "to": m.To, "subject": m.Subject, "body": m.Body, "received_at": m.ReceivedAt.Format("2006-01-02T15:04:05Z07:00")}
}

func Validate(d model.Definition) error {
	if d.Version != 1 {
		return errors.New("versione flusso non supportata")
	}
	if len(d.Branches) == 0 || len(d.Branches) > 50 || len(d.Extractors) > 50 {
		return errors.New("servono da 1 a 50 rami, con al massimo 50 estrazioni")
	}
	vars := Variables(model.Message{})
	if err := validateGroup(d.Filter, vars); err != nil {
		return fmt.Errorf("filtro: %w", err)
	}
	for _, e := range d.Extractors {
		if !nameRE.MatchString(e.Name) {
			return fmt.Errorf("nome estrazione non valido: %s", e.Name)
		}
		if _, exists := vars[e.Name]; exists {
			return fmt.Errorf("variabile duplicata: %s", e.Name)
		}
		if _, ok := Variables(model.Message{})[e.Source]; !ok {
			return fmt.Errorf("sorgente estrazione non valida: %s", e.Source)
		}
		vars[e.Name] = ""
		if e.Pattern != "" {
			r, err := regexp.Compile(e.Pattern)
			if err != nil {
				return fmt.Errorf("estrazione %s: %w", e.Name, err)
			}
			for _, n := range r.SubexpNames() {
				if n != "" {
					if _, ok := vars[n]; ok {
						return fmt.Errorf("variabile duplicata: %s", n)
					}
					vars[n] = ""
				}
			}
		}
	}
	for _, b := range d.Branches {
		if strings.TrimSpace(b.Name) == "" || len(b.Actions) == 0 || len(b.Actions) > 20 {
			return errors.New("ogni ramo richiede nome e da 1 a 20 azioni")
		}
		if err := validateGroup(b.When, vars); err != nil {
			return fmt.Errorf("ramo %s: %w", b.Name, err)
		}
		for _, a := range b.Actions {
			if err := validateAction(a); err != nil {
				return fmt.Errorf("ramo %s: %w", b.Name, err)
			}
			if _, err := renderAction(a, vars, false); err != nil {
				return fmt.Errorf("template ramo %s: %w", b.Name, err)
			}
		}
	}
	return nil
}
func validateGroup(g model.Group, vars map[string]string) error {
	if g.Mode != "all" && g.Mode != "any" {
		return errors.New("condizioni: scegliere all oppure any")
	}
	if len(g.Conditions) > 100 {
		return errors.New("troppe condizioni")
	}
	for _, c := range g.Conditions {
		if _, ok := vars[c.Field]; !ok {
			return fmt.Errorf("campo sconosciuto: %s", c.Field)
		}
		switch c.Operator {
		case "equals", "prefix", "contains":
		case "regex":
			if _, err := regexp.Compile(c.Value); err != nil {
				return err
			}
		default:
			return fmt.Errorf("operatore non valido: %s", c.Operator)
		}
	}
	return nil
}
func validateAction(a model.Action) error {
	if a.Kind != "ignore" && a.ConnectionID <= 0 {
		return errors.New("connessione azione obbligatoria")
	}
	switch a.Kind {
	case "jira":
		if a.Project == "" || a.IssueType == "" || a.BoardID <= 0 || a.Subject == "" {
			return errors.New("Jira richiede progetto, board, tipo e riepilogo")
		}
	case "jira_request":
		if a.ServiceDeskID == "" || a.RequestTypeID == "" || a.BoardID <= 0 || a.Subject == "" {
			return errors.New("richiesta Jira richiede service desk, tipo richiesta, board e riepilogo")
		}
	case "slack":
		if a.ListID == "" || len(a.Fields) == 0 {
			return errors.New("Slack richiede lista e campi")
		}
	case "forward":
		if a.To == "" || a.Subject == "" {
			return errors.New("inoltro richiede destinatario e oggetto")
		}
	case "ignore":
		if strings.TrimSpace(a.Reason) == "" {
			return errors.New("esclusione: motivo obbligatorio")
		}
	default:
		return fmt.Errorf("azione non supportata: %s", a.Kind)
	}
	seen := map[string]bool{}
	for _, f := range a.Fields {
		if f.Field == "" || seen[f.Field] {
			return errors.New("campo di destinazione mancante o duplicato")
		}
		seen[f.Field] = true
		switch f.Type {
		case "text", "number", "boolean", "option", "user", "date", "link", "json":
		default:
			return fmt.Errorf("tipo campo non supportato: %s", f.Type)
		}
		if a.Kind == "jira" && (f.Field == "project" || f.Field == "issuetype" || f.Field == "reporter" || f.Field == "summary" || f.Field == "description") {
			return errors.New("usare i campi Jira dedicati per progetto, tipo, richiedente, riepilogo e descrizione")
		}
	}
	return nil
}

func Match(g model.Group, vars map[string]string) bool {
	if len(g.Conditions) == 0 {
		return true
	}
	for _, c := range g.Conditions {
		value, want := vars[c.Field], c.Value
		if c.IgnoreCase && c.Operator != "regex" {
			value = strings.ToLower(value)
			want = strings.ToLower(want)
		}
		var ok bool
		switch c.Operator {
		case "equals":
			ok = value == want
		case "prefix":
			ok = strings.HasPrefix(value, want)
		case "contains":
			ok = strings.Contains(value, want)
		case "regex":
			if c.IgnoreCase {
				want = "(?i)" + want
			}
			r, err := regexp.Compile(want)
			ok = err == nil && r.MatchString(value)
		}
		if c.Not {
			ok = !ok
		}
		if g.Mode == "any" && ok {
			return true
		}
		if g.Mode == "all" && !ok {
			return false
		}
	}
	return g.Mode == "all"
}

func Plan(d model.Definition, m model.Message) ([]model.Action, string, error) {
	if err := Validate(d); err != nil {
		return nil, "", err
	}
	vars := Variables(m)
	if !Match(d.Filter, vars) {
		return nil, "non corrisponde al filtro", nil
	}
	for _, e := range d.Extractors {
		value := vars[e.Source]
		if e.Pattern != "" {
			r := regexp.MustCompile(e.Pattern)
			matches := r.FindStringSubmatch(value)
			for _, n := range r.SubexpNames() {
				if n != "" {
					vars[n] = ""
				}
			}
			if len(matches) == 0 {
				value = ""
			} else {
				value = matches[0]
				if len(matches) > 1 {
					value = matches[1]
				}
				for i, n := range r.SubexpNames() {
					if n != "" {
						vars[n] = matches[i]
					}
				}
			}
		}
		vars[e.Name] = strings.TrimSpace(value)
		if e.Required && vars[e.Name] == "" {
			return nil, "", fmt.Errorf("estrazione obbligatoria assente: %s", e.Name)
		}
	}
	for _, b := range d.Branches {
		if !Match(b.When, vars) {
			continue
		}
		var actions []model.Action
		for _, a := range b.Actions {
			rendered, err := renderAction(a, vars, true)
			if err != nil {
				return nil, "", err
			}
			actions = append(actions, rendered)
		}
		return actions, b.Name, nil
	}
	return nil, "nessun ramo corrispondente", nil
}

func renderAction(a model.Action, vars map[string]string, checkValues bool) (model.Action, error) {
	// A copy keeps simulation and queued execution snapshots immutable.
	copyAction := a
	copyAction.Fields = append([]model.Mapping(nil), a.Fields...)
	values := []*string{&copyAction.Project, &copyAction.IssueType, &copyAction.Reporter, &copyAction.ServiceDeskID, &copyAction.RequestTypeID, &copyAction.ListID, &copyAction.To, &copyAction.Subject, &copyAction.Body, &copyAction.Reason}
	for i := range copyAction.Fields {
		values = append(values, &copyAction.Fields[i].Value)
	}
	for _, value := range values {
		t, err := template.New("field").Funcs(funcs).Option("missingkey=error").Parse(*value)
		if err != nil {
			return a, err
		}
		var b bytes.Buffer
		if err = t.Execute(&b, vars); err != nil {
			return a, err
		}
		if b.Len() > 1024*1024 {
			return a, errors.New("campo generato troppo grande")
		}
		*value = b.String()
	}
	if checkValues {
		if err := validateAction(copyAction); err != nil {
			return a, err
		}
		if a.Kind == "forward" {
			if _, err := mail.ParseAddress(copyAction.To); err != nil || strings.ContainsAny(copyAction.To+copyAction.Subject, "\r\n") {
				return a, errors.New("destinatario o oggetto inoltro non valido")
			}
		}
		for _, f := range copyAction.Fields {
			if _, err := Value(f); err != nil {
				return a, fmt.Errorf("campo %s: %w", f.Field, err)
			}
		}
	}
	return copyAction, nil
}
func Value(m model.Mapping) (any, error) {
	switch m.Type {
	case "number":
		return strconv.ParseFloat(m.Value, 64)
	case "boolean":
		return strconv.ParseBool(m.Value)
	case "json":
		var v any
		err := json.Unmarshal([]byte(m.Value), &v)
		return v, err
	case "option":
		return map[string]string{"id": m.Value}, nil
	case "user":
		return map[string]string{"accountId": m.Value}, nil
	default:
		return m.Value, nil
	}
}
