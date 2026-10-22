package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/secure"
	"github.com/zupit-it/zupit-automations/internal/store"
	"github.com/zupit-it/zupit-automations/migrations"
	"net/url"
	"os"
	"testing"
)

func Database(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("integration test: TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err = rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%x", buf)
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	vault, err := secure.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, u.String(), vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		if _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	if err = migrations.Apply(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Check(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	return s
}
