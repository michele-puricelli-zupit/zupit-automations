package main

import (
	"context"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/store"
	"github.com/zupit-it/zupit-automations/migrations"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Migrazione non riuscita:", err)
		os.Exit(1)
	}
}
func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if p := os.Getenv("DATABASE_URL_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("secret database non leggibile")
		}
		dsn = strings.TrimSpace(string(b))
	}
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL o DATABASE_URL_FILE obbligatoria")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := store.Open(ctx, dsn, nil)
	if err != nil {
		return fmt.Errorf("database non raggiungibile")
	}
	defer s.Close()
	if err = migrations.Apply(ctx, s.DB); err != nil {
		return err
	}
	fmt.Println("Migrazioni applicate e verificate.")
	return nil
}
