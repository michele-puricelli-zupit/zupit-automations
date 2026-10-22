package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zupit-it/zupit-automations/internal/auth"
	"github.com/zupit-it/zupit-automations/internal/config"
	"github.com/zupit-it/zupit-automations/internal/model"
	"github.com/zupit-it/zupit-automations/internal/testutil"
)

func TestEditorDraftAndSimulationThroughProtectedRoutes(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	admins := map[string]bool{"admin@example.test": true}
	manager := &auth.Manager{Store: s, Cookie: "test_session", Config: config.Config{AdminEmails: admins, CSRFSecret: strings.Repeat("c", 32)}}
	server := &Server{Store: s, Auth: manager, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.Handler()
	for _, email := range []string{"admin@example.test", "viewer@example.test"} {
		u, err := s.ResolveUser(ctx, email, email, email, admins)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(email))
		if err = s.CreateSession(ctx, u.ID, hex.EncodeToString(digest[:]), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	mac := hmac.New(sha256.New, []byte(manager.Config.CSRFSecret))
	mac.Write([]byte("admin@example.test"))
	csrf := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	request := func(method, path, email string, form url.Values, hx bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		if email != "" {
			r.AddCookie(&http.Cookie{Name: manager.Cookie, Value: email})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tt := range []struct {
		path, email string
		status      int
	}{
		{"/flows/new?template=srt", "admin@example.test", 200},
		{"/flows/new?template=unknown", "admin@example.test", 400},
		{"/flows/new?template=srt", "viewer@example.test", 403},
		{"/flows/new?template=srt", "", 303},
	} {
		if w := request("GET", tt.path, tt.email, nil, false); w.Code != tt.status {
			t.Fatalf("%s as %s: status %d", tt.path, tt.email, w.Code)
		}
	}
	source, err := s.SaveConnection(ctx, model.Connection{Name: "Mailbox", Provider: "gmail", Config: map[string]string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"_csrf": {csrf}, "name": {"Draft <script>unsafe()</script>"}, "source_id": {fmt.Sprint(source)},
		"filter.mode": {"all"}, "branch.0.name": {"Excluded"}, "branch.0.when.mode": {"all"},
		"branch.0.action.0.kind": {"ignore"}, "branch.0.action.0.reason": {"Test exclusion"},
	}
	for _, hx := range []bool{false, true} {
		form.Set("operation", "filter")
		w := request("POST", "/flows/editor", "admin@example.test", form, hx)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `name="filter.0.field"`) || strings.Contains(body, "<script>unsafe()</script>") {
			t.Fatalf("editor response invalid: status %d", w.Code)
		}
		if strings.Contains(body, "<!DOCTYPE html>") == hx || w.Header().Get("Vary") != "HX-Request" {
			t.Fatal("editor did not distinguish fragment and full-page requests")
		}
	}
	form.Del("operation")
	form.Set("_csrf", "forged")
	if w := request("POST", "/flows/0", "admin@example.test", form, false); w.Code != 403 {
		t.Fatal("forged CSRF token accepted")
	}
	form.Set("_csrf", csrf)
	w := request("POST", "/flows/0", "admin@example.test", form, false)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/flows/") {
		t.Fatalf("saving draft failed: status %d", w.Code)
	}
	path := w.Header().Get("Location")
	w = request("POST", path+"/simulate", "admin@example.test", url.Values{
		"_csrf": {csrf}, "raw": {"From: support@example.test\r\nTo: team@example.test\r\nSubject: Errore è\r\n\r\nDettagli"},
	}, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test exclusion") {
		t.Fatalf("simulation failed: status %d", w.Code)
	}
	if count, err := s.Client.Run.Query().Count(ctx); err != nil || count != 0 {
		t.Fatalf("simulation queued real actions: count %d, error %v", count, err)
	}
}
