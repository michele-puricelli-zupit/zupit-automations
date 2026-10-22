package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/store"
	"golang.org/x/oauth2"
)

type Failure struct {
	Message                    string
	Retryable, Auth, Uncertain bool
	After                      time.Duration
}

func (e *Failure) Error() string { return e.Message }
func Classify(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return &Failure{Message: "Errore interno del connettore"}
}

type Clients struct {
	Store                         *store.Store
	HTTP                          *http.Client
	GmailOAuth                    oauth2.Config
	TypeSafeAPIKey, TypeSafeModel string
}

func New(s *store.Store, oauth oauth2.Config) *Clients {
	return &Clients{Store: s, GmailOAuth: oauth, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Clients) do(ctx context.Context, client *http.Client, method, endpoint string, body any, headers map[string]string, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(b))
	if err != nil {
		return &Failure{Message: "Indirizzo API non valido"}
	}
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	response, err := client.Do(r)
	mutating := method != "GET"
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			return failure
		}
		return &Failure{Message: "Connessione al servizio interrotta", Retryable: !mutating, Uncertain: mutating}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		f := &Failure{Message: fmt.Sprintf("Il servizio ha risposto HTTP %d", response.StatusCode)}
		if response.StatusCode == 401 || response.StatusCode == 403 {
			f.Auth = true
		}
		if response.StatusCode == 429 {
			f.Retryable = true
			f.After = retryAfter(response.Header.Get("Retry-After"))
		}
		if response.StatusCode >= 500 {
			f.Retryable = !mutating
			f.Uncertain = mutating
		}
		if response.StatusCode == 404 {
			f.Message = "Risorsa non trovata (HTTP 404)"
		}
		return f
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return err
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 25<<20)).Decode(out); err != nil {
		return &Failure{Message: "Risposta del servizio non valida", Retryable: !mutating, Uncertain: mutating}
	}
	return nil
}
func retryAfter(value string) time.Duration {
	if n, err := strconv.Atoi(value); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil && time.Until(t) > 0 {
		return time.Until(t)
	}
	return time.Minute
}
func ValidateConnection(c model.Connection) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 {
		return errors.New("nome connessione obbligatorio, massimo 200 caratteri")
	}
	switch c.Provider {
	case "jira":
		u, err := url.Parse(c.Config["url"])
		if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".atlassian.net") || u.User != nil || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("Jira richiede un URL HTTPS *.atlassian.net senza percorso")
		}
		if c.Config["email"] == "" {
			return errors.New("email dell’account tecnico Jira obbligatoria")
		}
	case "slack":
	case "gmail":
		if !strings.Contains(c.Config["email"], "@") || !resource(c.Config["topic"], "topics") || !resource(c.Config["subscription"], "subscriptions") {
			return errors.New("Gmail richiede email e risorse Pub/Sub complete: projects/PROGETTO/topics/NOME e projects/PROGETTO/subscriptions/NOME")
		}
	default:
		return errors.New("tipo di connessione non supportato")
	}
	return nil
}
func resource(v, kind string) bool {
	p := strings.Split(v, "/")
	return len(p) == 4 && p[0] == "projects" && p[1] != "" && p[2] == kind && p[3] != ""
}
func (c *Clients) token(ctx context.Context, id int) (string, error) {
	b, err := c.Store.Secret(ctx, id)
	if err != nil {
		return "", &Failure{Message: "Credenziali mancanti: ricollegare la connessione", Auth: true}
	}
	var v struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(b, &v); err != nil || v.Token == "" {
		return "", &Failure{Message: "Credenziali non valide: ricollegare la connessione", Auth: true}
	}
	return v.Token, nil
}
func (c *Clients) Verify(ctx context.Context, conn model.Connection) error {
	if err := ValidateConnection(conn); err != nil {
		return err
	}
	switch conn.Provider {
	case "jira":
		var out struct {
			AccountID string `json:"accountId"`
		}
		return c.jira(ctx, conn, "GET", "/rest/api/3/myself", nil, &out)
	case "slack":
		var out map[string]any
		return c.slack(ctx, conn.ID, "auth.test", map[string]any{}, &out)
	case "gmail":
		client, err := c.gmailClient(ctx, conn.ID)
		if err != nil {
			return err
		}
		var profile struct {
			Email string `json:"emailAddress"`
		}
		if err = c.do(ctx, client, "GET", gmailBase+"/profile", nil, nil, &profile); err != nil {
			return err
		}
		if !strings.EqualFold(profile.Email, conn.Config["email"]) {
			return &Failure{Message: "La casella autorizzata non corrisponde alla configurazione", Auth: true}
		}
		return c.Renew(ctx, conn)
	}
	return errors.New("connessione non supportata")
}
func (c *Clients) Execute(ctx context.Context, a model.Action) (model.Result, error) {
	conn, err := c.Store.Connection(ctx, a.ConnectionID)
	if err != nil {
		return model.Result{}, err
	}
	switch a.Kind {
	case "jira", "jira_request":
		return c.createIssue(ctx, conn, a)
	case "slack":
		return c.createListItem(ctx, conn, a)
	case "forward":
		return c.forward(ctx, conn, a)
	default:
		return model.Result{}, errors.New("azione non supportata")
	}
}
