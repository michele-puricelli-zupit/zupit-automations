package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/zupit-it/zupit-automations/internal/config"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/store"
	"golang.org/x/oauth2"
)

type principalKey struct{}
type Principal struct {
	User  model.User
	CSRF  string
	token string
}
type claims struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Verified bool   `json:"email_verified"`
	Domain   string `json:"hd"`
	Nonce    string `json:"nonce"`
}
type tokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}
type Manager struct {
	Store     *store.Store
	Config    config.Config
	OAuth     oauth2.Config
	verifier  tokenVerifier
	Cookie    string
	limit     *rateLimiter
	Connected func(context.Context, int, *oauth2.Token) error
}

func New(ctx context.Context, c config.Config, s *store.Store) (*Manager, error) {
	p, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, errors.New("discovery Google OIDC non disponibile")
	}
	cookie := "zupit_auto_session"
	if c.Production {
		cookie = "__Host-zupit_auto_session"
	}
	return &Manager{Store: s, Config: c, OAuth: oauth2.Config{ClientID: c.GoogleClientID, ClientSecret: c.GoogleClientSecret, Endpoint: p.Endpoint()}, verifier: p.Verifier(&oidc.Config{ClientID: c.GoogleClientID}), Cookie: cookie, limit: newRateLimiter(20, time.Minute)}, nil
}
func (m *Manager) oauth(purpose string) oauth2.Config {
	c := m.OAuth
	c.Scopes = []string{"openid", "email", "profile"}
	c.RedirectURL = m.Config.BaseURL + "/auth/callback"
	if purpose == "gmail" {
		c.Scopes = append(c.Scopes, "https://www.googleapis.com/auth/gmail.readonly", "https://www.googleapis.com/auth/gmail.send")
		c.RedirectURL = m.Config.BaseURL + "/auth/gmail/callback"
	}
	return c
}
func (m *Manager) GmailOAuth() oauth2.Config { return m.oauth("gmail") }
func (m *Manager) Start(w http.ResponseWriter, r *http.Request, purpose string, connectionID int) {
	if !m.limit.allow(r.RemoteAddr, time.Now()) {
		http.Error(w, "Troppi tentativi: riprovare tra un minuto", 429)
		return
	}
	var userID int
	if purpose == "gmail" {
		p, ok := Current(r.Context())
		if !ok || !p.User.Admin {
			http.Error(w, "Accesso amministratore richiesto", 403)
			return
		}
		userID = p.User.ID
		c, err := m.Store.Connection(r.Context(), connectionID)
		if err != nil || c.Provider != "gmail" {
			http.Error(w, "Connessione Gmail non valida", 400)
			return
		}
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "Errore interno", 500)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "Errore interno", 500)
		return
	}
	binding, err := randomToken()
	if err != nil {
		http.Error(w, "Errore interno", 500)
		return
	}
	verifier := oauth2.GenerateVerifier()
	a := store.Attempt{StateHash: hash(state), Nonce: nonce, Verifier: verifier, Purpose: purpose, ConnectionID: connectionID, UserID: userID, BindingHash: hash(binding), ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err = m.Store.SaveAttempt(r.Context(), a); err != nil {
		http.Error(w, "Impossibile avviare l’accesso", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "zupit_auto_" + purpose, Value: binding, Path: "/auth/", MaxAge: 600, HttpOnly: true, Secure: m.Config.Production, SameSite: http.SameSiteLaxMode})
	options := []oauth2.AuthCodeOption{oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)}
	if len(m.Config.AllowedDomains) == 1 {
		for domain := range m.Config.AllowedDomains {
			options = append(options, oauth2.SetAuthURLParam("hd", domain))
		}
	}
	if purpose == "gmail" {
		c, _ := m.Store.Connection(r.Context(), connectionID)
		options = append(options, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent select_account"), oauth2.SetAuthURLParam("login_hint", c.Config["email"]))
	}
	cfg := m.oauth(purpose)
	http.Redirect(w, r, cfg.AuthCodeURL(state, options...), 303)
}
func (m *Manager) Callback(w http.ResponseWriter, r *http.Request, purpose string) {
	if !m.limit.allow(r.RemoteAddr, time.Now()) {
		http.Error(w, "Troppi tentativi", 429)
		return
	}
	cookie, err := r.Cookie("zupit_auto_" + purpose)
	if err != nil {
		http.Error(w, "Accesso scaduto: riprovare", 400)
		return
	}
	a, err := m.Store.ConsumeAttempt(r.Context(), hash(r.URL.Query().Get("state")), hash(cookie.Value), purpose)
	if err != nil {
		http.Error(w, "Tentativo scaduto o già utilizzato", 400)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "zupit_auto_" + purpose, Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: m.Config.Production, SameSite: http.SameSiteLaxMode})
	if r.URL.Query().Get("code") == "" {
		http.Error(w, "Autorizzazione non concessa", 400)
		return
	}
	cfg := m.oauth(purpose)
	token, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(a.Verifier))
	if err != nil {
		http.Error(w, "Autorizzazione Google non riuscita", 401)
		return
	}
	raw, _ := token.Extra("id_token").(string)
	idToken, err := m.verifier.Verify(r.Context(), raw)
	if err != nil {
		http.Error(w, "Identità Google non valida", 401)
		return
	}
	var identity claims
	if err = idToken.Claims(&identity); err != nil || identity.Subject == "" || !identity.Verified || identity.Nonce != a.Nonce || !Allowed(identity.Email, identity.Domain, m.Config.AllowedDomains) {
		http.Error(w, "Account Workspace non autorizzato", 403)
		return
	}
	if purpose == "gmail" {
		p, ok := Current(r.Context())
		if !ok || !p.User.Admin || p.User.ID != a.UserID {
			http.Error(w, "Sessione amministratore scaduta", 403)
			return
		}
		c, err := m.Store.Connection(r.Context(), a.ConnectionID)
		if err != nil || !strings.EqualFold(c.Config["email"], identity.Email) {
			http.Error(w, "L’account Google selezionato non corrisponde alla casella configurata", 403)
			return
		}
		if token.RefreshToken == "" {
			http.Error(w, "Google non ha concesso l’accesso offline: ripetere il collegamento", 400)
			return
		}
		if m.Connected == nil || m.Connected(r.Context(), a.ConnectionID, token) != nil {
			http.Error(w, "Collegamento incompleto: verificare Pub/Sub e riprovare dalla pagina Connessioni", 502)
			return
		}
		http.Redirect(w, r, "/connections", 303)
		return
	}
	u, err := m.Store.ResolveUser(r.Context(), identity.Subject, identity.Email, identity.Name, m.Config.AdminEmails)
	if err != nil {
		http.Error(w, "Utente non autorizzato", 403)
		return
	}
	value, err := randomToken()
	if err != nil {
		http.Error(w, "Errore interno", 500)
		return
	}
	if err = m.Store.CreateSession(r.Context(), u.ID, hash(value), time.Now()); err != nil {
		http.Error(w, "Impossibile creare la sessione", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: m.Cookie, Value: value, Path: "/", MaxAge: 8 * 3600, Expires: time.Now().Add(8 * time.Hour), HttpOnly: true, Secure: m.Config.Production, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/flows", 303)
}
func Allowed(email, domain string, allowed map[string]bool) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(email)), "@")
	return len(parts) == 2 && parts[0] != "" && allowed[parts[1]] && allowed[strings.ToLower(strings.TrimSpace(domain))]
}
func Current(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(m.Cookie); err == nil {
			if u, err := m.Store.Session(r.Context(), hash(c.Value), m.Config.AdminEmails); err == nil {
				p := Principal{User: u, CSRF: m.csrf(c.Value), token: c.Value}
				r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := Current(r.Context()); !ok {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/")
			}
			http.Redirect(w, r, "/", 303)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (m *Manager) Admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := Current(r.Context())
		if !ok || !p.User.Admin {
			http.Error(w, "Accesso amministratore richiesto", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (m *Manager) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}
		p, ok := Current(r.Context())
		if !ok {
			http.Error(w, "Sessione richiesta", 401)
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		if token == "" {
			token = r.FormValue("_csrf")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(p.CSRF)) != 1 {
			http.Error(w, "Sessione scaduta o token CSRF non valido: ricaricare la pagina", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	p, _ := Current(r.Context())
	if err := m.Store.DeleteSession(r.Context(), hash(p.token)); err != nil {
		http.Error(w, "Impossibile terminare la sessione", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: m.Cookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.Config.Production, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", 303)
}
func (m *Manager) csrf(value string) string {
	mac := hmac.New(sha256.New, []byte(m.Config.CSRFSecret))
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(value string) string { v := sha256.Sum256([]byte(value)); return hex.EncodeToString(v[:]) }

func TokenBytes(t *oauth2.Token) []byte { b, _ := json.Marshal(t); return b }
