package config

import (
	"errors"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/secure"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Addr, DatabaseURL, BaseURL, GoogleClientID, GoogleClientSecret, CSRFSecret, EncryptionKey string
	AllowedDomains, AdminEmails                                                               map[string]bool
	Production                                                                                bool
	TypeSafeAPIKey, TypeSafeModel                                                             string
}

func Load() (Config, error) {
	c := Config{Addr: os.Getenv("ADDR"), DatabaseURL: os.Getenv("DATABASE_URL"), BaseURL: strings.TrimRight(os.Getenv("APP_BASE_URL"), "/"), GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"), CSRFSecret: os.Getenv("CSRF_SECRET"), EncryptionKey: os.Getenv("ENCRYPTION_KEY"), AllowedDomains: CSV(os.Getenv("AUTH_ALLOWED_DOMAINS")), AdminEmails: CSV(os.Getenv("ADMIN_EMAILS")), Production: os.Getenv("APP_ENV") != "development"}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	c.TypeSafeAPIKey = os.Getenv("TYPESAFE_API_KEY")
	c.TypeSafeModel = os.Getenv("TYPESAFE_MODEL")
	if c.TypeSafeModel == "" {
		c.TypeSafeModel = "jev-latest"
	}
	for _, item := range []struct {
		env  *string
		file string
	}{{&c.GoogleClientSecret, "GOOGLE_CLIENT_SECRET_FILE"}, {&c.EncryptionKey, "ENCRYPTION_KEY_FILE"}, {&c.CSRFSecret, "CSRF_SECRET_FILE"}, {&c.DatabaseURL, "DATABASE_URL_FILE"}, {&c.TypeSafeAPIKey, "TYPESAFE_API_KEY_FILE"}} {
		if p := os.Getenv(item.file); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				return c, fmt.Errorf("impossibile leggere %s: verificare mount e permessi del secret per UID 10001", item.file)
			}
			*item.env = strings.TrimSpace(string(b))
		}
	}
	if c.DatabaseURL == "" || c.GoogleClientID == "" || c.GoogleClientSecret == "" || len(c.CSRFSecret) < 32 || len(c.AllowedDomains) == 0 || len(c.AdminEmails) == 0 {
		return c, errors.New("configurazione incompleta: DATABASE_URL, GOOGLE_CLIENT_ID/SECRET, AUTH_ALLOWED_DOMAINS, ADMIN_EMAILS, CSRF_SECRET richiesti")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return c, errors.New("APP_BASE_URL non valida")
	}
	if c.Production && u.Scheme != "https" {
		return c, errors.New("HTTPS obbligatorio in produzione")
	}
	for email := range c.AdminEmails {
		parts := strings.Split(email, "@")
		if len(parts) != 2 || !c.AllowedDomains[parts[1]] {
			return c, errors.New("ADMIN_EMAILS deve contenere indirizzi nei domini autorizzati")
		}
	}
	_, err = secure.New(c.EncryptionKey)
	return c, err
}
func CSV(value string) map[string]bool {
	r := map[string]bool{}
	for _, v := range strings.Split(value, ",") {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			r[v] = true
		}
	}
	return r
}
