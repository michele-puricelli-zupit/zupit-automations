package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
)

//go:embed *.sql
var files embed.FS

func Check(ctx context.Context, db *sql.DB) error {
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		b, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		var digest string
		if err = db.QueryRowContext(ctx, `SELECT digest FROM schema_versions WHERE name=$1`, entry.Name()).Scan(&digest); err != nil {
			return fmt.Errorf("migrazioni mancanti: eseguire prima make migrate")
		}
		if digest != fmt.Sprintf("%x", sha256.Sum256(b)) {
			return fmt.Errorf("digest migrazione %s non corrispondente", entry.Name())
		}
	}
	return nil
}

func Apply(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(836422019)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_versions(name TEXT PRIMARY KEY,digest TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		body, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		var applied string
		err = tx.QueryRowContext(ctx, `SELECT digest FROM schema_versions WHERE name=$1`, entry.Name()).Scan(&applied)
		if err == nil {
			if applied != digest {
				return fmt.Errorf("migrazione modificata dopo l’applicazione: %s", entry.Name())
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_versions(name,digest) VALUES($1,$2)`, entry.Name(), digest); err != nil {
			return err
		}
	}
	return tx.Commit()
}
