package main

import (
	"context"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/logging"
	"github.com/zupit-it/zupit-automations/internal/store"
	"github.com/zupit-it/zupit-automations/migrations"
	"log/slog"
	"os"
	"strings"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("Migrazione non riuscita: applicazione non avviabile", "event", "migration.failed", logging.Error(err))
		os.Exit(1)
	}
}
func run() error {
	slog.Info("Avvio migrazioni", "event", "migration.starting")
	dsn := os.Getenv("DATABASE_URL")
	if p := os.Getenv("DATABASE_URL_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			slog.Error("Secret DATABASE_URL_FILE non leggibile", "event", "migration.config_failed", logging.Error(err))
			return fmt.Errorf("secret database non leggibile")
		}
		dsn = strings.TrimSpace(string(b))
	}
	if dsn == "" {
		slog.Error("Impostare DATABASE_URL o DATABASE_URL_FILE", "event", "migration.config_failed")
		return fmt.Errorf("DATABASE_URL o DATABASE_URL_FILE obbligatoria")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := store.Open(ctx, dsn, nil)
	if err != nil {
		slog.Error("Connessione PostgreSQL fallita durante le migrazioni", "event", "database.connect_failed", logging.Error(err))
		return fmt.Errorf("database non raggiungibile")
	}
	defer s.Close()
	if err = migrations.Apply(ctx, s.DB); err != nil {
		return err
	}
	slog.Info("Migrazioni applicate e verificate", "event", "migration.completed")
	return nil
}
