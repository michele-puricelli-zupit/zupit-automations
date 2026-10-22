package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DATABASE_URL_FILE", "GOOGLE_CLIENT_SECRET_FILE", "CSRF_SECRET_FILE", "ENCRYPTION_KEY_FILE", "ADDR", "TYPESAFE_API_KEY", "TYPESAFE_API_KEY_FILE", "TYPESAFE_MODEL"} {
		t.Setenv(key, "")
	}
	for key, value := range map[string]string{
		"APP_ENV": "production", "APP_BASE_URL": "https://automations.example.test",
		"DATABASE_URL": "postgres://localhost/test", "GOOGLE_CLIENT_ID": "test-client",
		"GOOGLE_CLIENT_SECRET": "test-secret", "CSRF_SECRET": strings.Repeat("c", 32),
		"ENCRYPTION_KEY":       base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"AUTH_ALLOWED_DOMAINS": " EXAMPLE.TEST ", "ADMIN_EMAILS": " Admin@Example.Test ",
	} {
		t.Setenv(key, value)
	}
}

func TestLoadValidatesDeploymentConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name, key, value string
	}{
		{"missing database", "DATABASE_URL", ""},
		{"missing client", "GOOGLE_CLIENT_ID", ""},
		{"missing secret", "GOOGLE_CLIENT_SECRET", ""},
		{"weak CSRF secret", "CSRF_SECRET", "short"},
		{"invalid encryption key", "ENCRYPTION_KEY", "invalid"},
		{"HTTP in production", "APP_BASE_URL", "http://localhost:8080"},
		{"URL path", "APP_BASE_URL", "https://app.example.test/path"},
		{"URL credentials", "APP_BASE_URL", "https://user:secret@app.example.test"},
		{"missing domains", "AUTH_ALLOWED_DOMAINS", ""},
		{"missing admins", "ADMIN_EMAILS", ""},
		{"outside admin", "ADMIN_EMAILS", "admin@outside.test"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			validEnvironment(t)
			t.Setenv(tt.key, tt.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Run("valid production and local development", func(t *testing.T) {
		validEnvironment(t)
		c, err := Load()
		if err != nil || !c.Production || c.Addr != ":8080" || !c.AdminEmails["admin@example.test"] || !c.AllowedDomains["example.test"] {
			t.Fatalf("valid production configuration failed: %v", err)
		}
		t.Setenv("APP_ENV", "development")
		t.Setenv("APP_BASE_URL", "http://localhost:8080/")
		c, err = Load()
		if err != nil || c.Production || c.BaseURL != "http://localhost:8080" {
			t.Fatalf("valid development configuration failed: %v", err)
		}
	})
}

func TestLoadMountedSecret(t *testing.T) {
	validEnvironment(t)
	p := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(p, []byte("mounted-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_CLIENT_SECRET_FILE", p)
	c, err := Load()
	if err != nil || c.GoogleClientSecret != "mounted-secret" {
		t.Fatalf("mounted secret did not override environment value: %v", err)
	}
	t.Setenv("GOOGLE_CLIENT_SECRET_FILE", p+"-missing")
	if _, err := Load(); err == nil || strings.Contains(err.Error(), p) {
		t.Fatal("unreadable secret must fail without exposing its path")
	}
}

func TestTypeSafeIsOptionalAndSupportsMountedSecrets(t *testing.T) {
	validEnvironment(t)
	c, err := Load()
	if err != nil || c.TypeSafeAPIKey != "" || c.TypeSafeModel != "jev-latest" {
		t.Fatalf("A-only deployment changed: %v", err)
	}
	p := filepath.Join(t.TempDir(), "typesafe-key")
	if err := os.WriteFile(p, []byte("test-typesafe-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY_FILE", p)
	t.Setenv("TYPESAFE_MODEL", "jev-test")
	c, err = Load()
	if err != nil || c.TypeSafeAPIKey != "test-typesafe-key" || c.TypeSafeModel != "jev-test" {
		t.Fatalf("TypeSafe secret configuration failed: %v", err)
	}
}
