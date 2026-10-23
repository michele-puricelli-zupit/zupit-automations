package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	dbent "github.com/zupit-it/zupit-automations/ent"
	"github.com/zupit-it/zupit-automations/examples"
	"github.com/zupit-it/zupit-automations/internal/auth"
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/engine"
	"github.com/zupit-it/zupit-automations/internal/logging"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/store"
	"github.com/zupit-it/zupit-automations/internal/views"
	"github.com/zupit-it/zupit-automations/static"
)

type Server struct {
	Store   *store.Store
	Auth    *auth.Manager
	Clients *connectors.Clients
	Log     *slog.Logger
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, s.security, s.Auth.Middleware)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static.Files))))
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.DB.PingContext(r.Context()); err != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.Write([]byte("ok"))
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.Current(r.Context()); ok {
			http.Redirect(w, r, "/flows", 303)
			return
		}
		s.render(w, r, views.Page(views.Data{Title: "Accedi"}))
	})
	r.Get("/auth/login", func(w http.ResponseWriter, r *http.Request) { s.Auth.Start(w, r, "login", 0) })
	r.Get("/auth/callback", func(w http.ResponseWriter, r *http.Request) { s.Auth.Callback(w, r, "login") })
	r.Get("/auth/gmail/callback", func(w http.ResponseWriter, r *http.Request) { s.Auth.Callback(w, r, "gmail") })
	r.Group(func(r chi.Router) {
		r.Use(s.Auth.Require, s.Auth.CSRF)
		r.Post("/auth/logout", s.Auth.Logout)
		r.Get("/connections", s.connections)
		r.Get("/flows", s.flows)
		r.Get("/runs", s.runs)
		r.Get("/runs/{id}", s.run)
		r.Group(func(r chi.Router) {
			r.Use(s.Auth.Admin)
			r.Get("/connections/new", s.connectionForm)
			r.Get("/connections/{id}", s.connectionForm)
			r.Post("/connections/{id}", s.saveConnection)
			r.Post("/connections/{id}/verify", s.verifyConnection)
			r.Post("/connections/{id}/authorize", func(w http.ResponseWriter, r *http.Request) {
				s.Auth.Start(w, r, "gmail", intValue(chi.URLParam(r, "id")))
			})
			r.Get("/flows/new", s.flowForm)
			r.Get("/flows/{id}", s.flowForm)
			r.Post("/flows/editor", s.editor)
			r.Post("/flows/{id}", s.saveFlow)
			r.Post("/flows/{id}/toggle", s.toggle)
			r.Get("/flows/{id}/simulate", s.simulate)
			r.Post("/flows/{id}/simulate", s.simulate)
			r.Post("/steps/{id}/retry", s.retry)
			r.Post("/steps/{id}/resolve", s.resolve)
		})
	})
	return r
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self' https://accounts.google.com; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		if s.Auth.Config.Production {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		next.ServeHTTP(w, r)
	})
}
func (s *Server) data(r *http.Request, active, title string) (views.Data, error) {
	p, _ := auth.Current(r.Context())
	d := views.Data{Active: active, Title: title, User: p.User, CSRF: p.CSRF}
	d.JevReady = s.Clients != nil && s.Clients.TypeSafeAPIKey != ""
	var err error
	d.Connections, err = s.Store.Connections(r.Context())
	if err != nil {
		return d, err
	}
	return d, nil
}
func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		s.Log.Error("Rendering pagina fallito", "event", "http.render_failed", "method", r.Method, "path", r.URL.Path, logging.Error(err))
	}
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if dbent.IsNotFound(err) {
		http.Error(w, "Risorsa non trovata", 404)
		return
	}
	route := r.Pattern
	if ctx := chi.RouteContext(r.Context()); ctx != nil {
		route = ctx.RoutePattern()
	}
	s.Log.Error("Operazione applicativa fallita", "event", "http.operation_failed", "method", r.Method, "route", route, logging.Error(err))
	http.Error(w, "Operazione non riuscita. Riprovare.", 500)
}
func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "connections", "Connessioni")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, views.Page(d))
}
func (s *Server) connectionForm(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "connections", "Connessione")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id := intValue(chi.URLParam(r, "id")); id > 0 {
		d.Connection, err = s.Store.Connection(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	} else {
		provider := r.URL.Query().Get("provider")
		if provider != "gmail" && provider != "jira" && provider != "slack" {
			http.Error(w, "Tipo di connessione non valido", 400)
			return
		}
		d.Connection = model.Connection{Provider: provider, Config: map[string]string{}}
		if provider == "jira" {
			d.Connection.Config["url"] = "https://zupit-pro.atlassian.net"
		}
	}
	s.render(w, r, views.Page(d))
}
func (s *Server) saveConnection(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "connections", "Connessione")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err = r.ParseForm(); err != nil {
		http.Error(w, "Modulo non valido", 400)
		return
	}
	c := model.Connection{ID: intValue(chi.URLParam(r, "id")), Name: strings.TrimSpace(r.Form.Get("name")), Provider: r.Form.Get("provider"), Config: map[string]string{}}
	for _, k := range []string{"email", "url", "topic", "subscription", "alert_channel"} {
		if value := strings.TrimSpace(r.Form.Get(k)); value != "" {
			c.Config[k] = value
		}
	}
	if c.Config["email"] != "" {
		c.Config["email"] = strings.ToLower(c.Config["email"])
	}
	err = connectors.ValidateConnection(c)
	if err == nil && c.Config["alert_channel"] != "" {
		for _, other := range d.Connections {
			if other.ID != c.ID && other.Provider == "slack" && other.Config["alert_channel"] != "" {
				err = errors.New("il canale avvisi è già configurato su un’altra connessione Slack")
			}
		}
	}
	if err != nil {
		d.Connection = c
		d.Error = err.Error()
		s.render(w, r, views.Page(d))
		return
	}
	var secret []byte
	if token := r.Form.Get("token"); token != "" {
		secret, _ = json.Marshal(map[string]string{"token": token})
	}
	id, err := s.Store.SaveConnection(r.Context(), c, secret)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/connections/%d", id), 303)
}
func (s *Server) verifyConnection(w http.ResponseWriter, r *http.Request) {
	id := intValue(chi.URLParam(r, "id"))
	c, err := s.Store.Connection(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	verifyErr := s.Clients.Verify(r.Context(), c)
	message := ""
	state := "ready"
	if verifyErr != nil {
		state = "pending"
		message = connectors.Classify(verifyErr).Message
		if c.Provider == "gmail" && connectors.Classify(verifyErr).Auth {
			state = "paused"
		}
	}
	if err = s.Store.ConnectionState(r.Context(), id, state, message); err != nil {
		s.fail(w, r, err)
		return
	}
	if verifyErr != nil {
		s.Log.Warn("Verifica connessione fallita: aprire Connessioni per correggere la configurazione", "event", "connection.verify_failed", "connection_id", id, "provider", c.Provider, "status", state, logging.Error(verifyErr))
	} else {
		s.Log.Info("Verifica connessione riuscita", "event", "connection.verified", "connection_id", id, "provider", c.Provider)
	}
	http.Redirect(w, r, "/connections", 303)
}
func (s *Server) flows(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "flows", "Flussi")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Flows, err = s.Store.Flows(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, views.Page(d))
}
func (s *Server) flowForm(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "editor", "Editor del flusso")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id := intValue(chi.URLParam(r, "id")); id > 0 {
		d.Flow, err = s.Store.Flow(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	} else {
		d.Flow = model.Flow{Definition: model.Definition{Version: 1, Filter: model.Group{Mode: "all"}, Branches: []model.Branch{{Name: "Caso principale", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "ignore", Reason: "Da configurare"}}}}}}
		if name := r.URL.Query().Get("template"); name != "" {
			d.Flow, err = examples.Preset(name)
			if err != nil {
				http.Error(w, "Modello non disponibile", 400)
				return
			}
		}
	}
	s.jiraChoices(r, &d)
	s.render(w, r, views.Page(d))
}
func (s *Server) editor(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "HX-Request")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Modulo non valido", 400)
		return
	}
	d, err := s.data(r, "editor", "Editor del flusso")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Flow = parseFlow(r.Form)
	if err = editorOperation(&d.Flow, r.Form.Get("operation")); err != nil {
		d.Error = err.Error()
	}
	s.jiraChoices(r, &d)
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, r, views.FlowEditor(d))
	} else {
		s.render(w, r, views.Page(d))
	}
}
func (s *Server) validateDestinations(r *http.Request, f model.Flow) error {
	if err := s.Store.CheckFlow(r.Context(), f, true); err != nil {
		return err
	}
	if cfg := f.Definition.Comparison; cfg != nil {
		if s.Clients == nil || s.Clients.TypeSafeAPIKey == "" {
			return errors.New("configurare TYPESAFE_API_KEY o TYPESAFE_API_KEY_FILE sul server prima di attivare B")
		}
		if _, err := s.Clients.ComparisonDM(r.Context(), cfg.SlackConnectionID, cfg.SlackRecipient); err != nil {
			return fmt.Errorf("DM del confronto non verificato: %s", connectors.Classify(err).Message)
		}
	}
	for _, b := range f.Definition.Branches {
		for _, a := range b.Actions {
			if a.Kind != "ignore" {
				if err := s.Clients.VerifyAction(r.Context(), a); err != nil {
					s.Log.Warn("Destinazione del flusso non verificata: attivazione rifiutata", "event", "flow.destination_failed", "flow_id", f.ID, "connection_id", a.ConnectionID, "action", a.Kind, logging.Error(err))
					return fmt.Errorf("destinazione del ramo %s non verificata: %s", b.Name, connectors.Classify(err).Message)
				}
			}
		}
	}
	return nil
}
func (s *Server) saveFlow(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Modulo non valido", 400)
		return
	}
	f := parseFlow(r.Form)
	f.ID = intValue(chi.URLParam(r, "id"))
	var err error
	if f.Enabled {
		err = s.validateDestinations(r, f)
	}
	var id int
	if err == nil {
		id, err = s.Store.SaveFlow(r.Context(), f)
	}
	if err != nil {
		d, e := s.data(r, "editor", "Editor del flusso")
		if e != nil {
			s.fail(w, r, e)
			return
		}
		d.Flow = f
		d.Error = err.Error()
		s.jiraChoices(r, &d)
		s.render(w, r, views.Page(d))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/flows/%d", id), 303)
}
func (s *Server) toggle(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.Flow(r.Context(), intValue(chi.URLParam(r, "id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f.Enabled = !f.Enabled
	if f.Enabled {
		err = s.validateDestinations(r, f)
	}
	if err == nil {
		_, err = s.Store.SaveFlow(r.Context(), f)
	}
	if err != nil {
		d, e := s.data(r, "flows", "Flussi")
		if e != nil {
			s.fail(w, r, e)
			return
		}
		d.Flows, _ = s.Store.Flows(r.Context())
		d.Error = err.Error()
		s.render(w, r, views.Page(d))
		return
	}
	http.Redirect(w, r, "/flows", 303)
}
func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "simulate", "Simulazione")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Flow, err = s.Store.Flow(r.Context(), intValue(chi.URLParam(r, "id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Method == "POST" {
		d.Raw = r.FormValue("raw")
		message, e := connectors.ParseEmail(d.Raw)
		if e == nil {
			actions, branch, e := engine.Plan(d.Flow.Definition, message)
			if e == nil {
				b, _ := json.MarshalIndent(actions, "", "  ")
				d.Branch = branch
				d.Simulation = string(b)
			} else {
				d.Error = e.Error()
			}
		} else {
			d.Error = e.Error()
		}
	}
	s.render(w, r, views.Page(d))
}
func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "runs", "Esecuzioni")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Runs, err = s.Store.Runs(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, views.Page(d))
}
func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	d, err := s.data(r, "runs", "Dettaglio esecuzione")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Run, err = s.Store.Run(r.Context(), intValue(chi.URLParam(r, "id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, views.Page(d))
}
func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	id := intValue(chi.URLParam(r, "id"))
	row, err := s.Store.Client.Step.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if row.Status == "uncertain" && r.FormValue("confirmed") != "yes" {
		http.Error(w, "Verificare prima che l’azione non sia stata eseguita", 400)
		return
	}
	if err = s.Store.Retry(r.Context(), id, false, ""); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/runs/%d", row.RunID), 303)
}
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	id := intValue(chi.URLParam(r, "id"))
	row, err := s.Store.Client.Step.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.Store.Retry(r.Context(), id, true, r.FormValue("reference")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/runs/%d", row.RunID), 303)
}
