package main

import (
	"context"
	"errors"
	"github.com/zupit-it/zupit-automations/internal/auth"
	"github.com/zupit-it/zupit-automations/internal/config"
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/secure"
	"github.com/zupit-it/zupit-automations/internal/store"
	"github.com/zupit-it/zupit-automations/internal/web"
	"github.com/zupit-it/zupit-automations/internal/worker"
	"github.com/zupit-it/zupit-automations/migrations"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("avvio o esecuzione fallita", "error", err.Error())
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	vault, err := secure.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := store.Open(startup, cfg.DatabaseURL, vault)
	if err != nil {
		return errors.New("database non raggiungibile")
	}
	defer func(s *store.Store) {
		err := s.Close()
		if err != nil {
			err = errors.New("database non raggiungibile")
		}
	}(s)
	if err = migrations.Check(startup, s.DB); err != nil {
		return err
	}
	authManager, err := auth.New(startup, cfg, s)
	if err != nil {
		return err
	}
	clients := connectors.New(s, authManager.GmailOAuth())
	clients.TypeSafeAPIKey, clients.TypeSafeModel = cfg.TypeSafeAPIKey, cfg.TypeSafeModel
	authManager.Connected = clients.ConnectGmail
	app := &web.Server{Store: s, Auth: authManager, Clients: clients, Log: logger}
	server := &http.Server{Addr: cfg.Addr, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	serviceCtx, cancelService := context.WithCancel(ctx)
	defer cancelService()
	workerResult := make(chan error, 1)
	go func() {
		w := worker.Worker{Store: s, Clients: clients, Log: logger, BaseURL: cfg.BaseURL}
		workerResult <- w.Run(serviceCtx)
	}()
	serverResult := make(chan error, 1)
	go func() { logger.Info("server pronto", "address", cfg.Addr); serverResult <- server.ListenAndServe() }()
	var result error
	workerStopped := false
	select {
	case <-ctx.Done():
	case result = <-workerResult:
		workerStopped = true
	case result = <-serverResult:
		if errors.Is(result, http.ErrServerClosed) {
			result = nil
		}
	}
	cancelService()
	shutdown, done := context.WithTimeout(context.Background(), 35*time.Second)
	defer done()
	if err = server.Shutdown(shutdown); err != nil {
		_ = server.Close()
	}
	if !workerStopped {
		if workerErr := <-workerResult; result == nil {
			result = workerErr
		}
	}
	return result
}
