package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/zupit-it/zupit-automations/internal/config"
	"github.com/zupit-it/zupit-automations/internal/testutil"
	"golang.org/x/oauth2"
)

type keySet struct{ key *rsa.PublicKey }

func (k keySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	j, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return nil, err
	}
	return j.Verify(k.key)
}
func TestWorkspaceClaims(t *testing.T) {
	allowed := map[string]bool{"example.test": true}
	for _, tt := range []struct {
		email, domain string
		want          bool
	}{{"user@example.test", "example.test", true}, {"user@example.test", "", false}, {"user@example.test.evil.test", "example.test", false}, {"user@example.test", "evil.test", false}, {"user@gmail.com", "example.test", false}} {
		if got := Allowed(tt.email, tt.domain, allowed); got != tt.want {
			t.Fatalf("%s/%s: %v", tt.email, tt.domain, got)
		}
	}
}
func TestGoogleLoginPKCENonceOneTimeStateAndSignature(t *testing.T) {
	s := testutil.Database(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var idToken, verifier string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("code_verifier") == "" || r.Form.Get("code_verifier") != verifier {
			t.Error("PKCE verifier missing or changed")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
	}))
	defer tokenServer.Close()
	cfg := config.Config{BaseURL: "http://app.test", GoogleClientID: "client", AllowedDomains: map[string]bool{"example.test": true}, AdminEmails: map[string]bool{"admin@example.test": true}, CSRFSecret: strings.Repeat("c", 32)}
	m := &Manager{Store: s, Config: cfg, Cookie: "test_session", limit: newRateLimiter(100, time.Minute), OAuth: oauth2.Config{ClientID: "client", ClientSecret: "secret", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.test/auth", TokenURL: tokenServer.URL}}, verifier: oidc.NewVerifier("https://accounts.google.com", keySet{&key.PublicKey}, &oidc.Config{ClientID: "client"})}
	for _, tt := range []struct {
		name   string
		mutate func(map[string]any)
		want   int
	}{{"valid", func(map[string]any) {}, 303}, {"nonce mismatch", func(c map[string]any) { c["nonce"] = "wrong" }, 403}, {"email unverified", func(c map[string]any) { c["email_verified"] = false }, 403}, {"outside domain", func(c map[string]any) { c["hd"] = "evil.test" }, 403}, {"wrong audience", func(c map[string]any) { c["aud"] = "another-client" }, 401}, {"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, 401}} {
		t.Run(tt.name, func(t *testing.T) {
			start := httptest.NewRecorder()
			m.Start(start, httptest.NewRequest("GET", "http://app.test/auth/login", nil), "login", 0)
			location, err := url.Parse(start.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			query := location.Query()
			state := query.Get("state")
			nonce := query.Get("nonce")
			if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || query.Get("hd") != "example.test" {
				t.Fatal("missing PKCE or Workspace hint")
			}
			if err = s.DB.QueryRow(`SELECT verifier FROM auth_attempts WHERE state_hash=$1`, hash(state)).Scan(&verifier); err != nil {
				t.Fatal(err)
			}
			identity := map[string]any{"iss": "https://accounts.google.com", "sub": "google-user", "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "admin@example.test", "email_verified": true, "hd": "example.test", "name": "Admin", "nonce": nonce}
			tt.mutate(identity)
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
			if err != nil {
				t.Fatal(err)
			}
			idToken, err = jwt.Signed(signer).Claims(identity).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("GET", "http://app.test/auth/callback?code=fake&state="+url.QueryEscape(state), nil)
			r.AddCookie(start.Result().Cookies()[0])
			result := httptest.NewRecorder()
			m.Callback(result, r, "login")
			if result.Code != tt.want {
				b, _ := io.ReadAll(result.Result().Body)
				t.Fatalf("status=%d want=%d body=%s", result.Code, tt.want, b)
			}
			if tt.want == 303 {
				found := false
				for _, cookie := range result.Result().Cookies() {
					if cookie.Name == m.Cookie {
						found = true
						if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
							t.Fatal("unsafe session cookie")
						}
						if _, err = s.Session(context.Background(), hash(cookie.Value), cfg.AdminEmails); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !found {
					t.Fatal("session cookie missing")
				}
			}
			replay := httptest.NewRecorder()
			m.Callback(replay, r, "login")
			if replay.Code != 400 {
				t.Fatal("replayed state accepted")
			}
		})
	}
}
func TestSessionCSRFAndAdminAuthorization(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	admins := map[string]bool{"admin@example.test": true}
	u, err := s.ResolveUser(ctx, "subject", "viewer@example.test", "Viewer", admins)
	if err != nil {
		t.Fatal(err)
	}
	raw := "synthetic-session-token"
	if err = s.CreateSession(ctx, u.ID, hash(raw), time.Now()); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Store: s, Config: config.Config{AdminEmails: admins, CSRFSecret: strings.Repeat("s", 32)}, Cookie: "test_session"}
	protected := m.Middleware(m.Require(m.CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))))
	for _, tt := range []struct {
		name, token string
		want        int
	}{{"valid", m.csrf(raw), 204}, {"missing", "", 403}, {"forged", "wrong", 403}} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/change", strings.NewReader(url.Values{"_csrf": {tt.token}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(&http.Cookie{Name: m.Cookie, Value: raw})
			w := httptest.NewRecorder()
			protected.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
	r := httptest.NewRequest("GET", "/admin", nil)
	r.AddCookie(&http.Cookie{Name: m.Cookie, Value: raw})
	w := httptest.NewRecorder()
	m.Middleware(m.Admin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("viewer reached admin handler") }))).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("viewer authorized")
	}
	if _, err = s.DB.Exec(`UPDATE sessions SET last_seen_at=now()-interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(ctx, hash(raw), admins); err == nil {
		t.Fatal("idle session accepted")
	}
}
