package main

import (
	"context"
	"errors"
	"github.com/zupit-it/zupit-automations/internal/auth"
	"github.com/zupit-it/zupit-automations/internal/config"
	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/logging"
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
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("Servizio terminato con errore", "event", "server.failed", logging.Error(err))
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	logger.Info("Avvio applicazione", "event", "server.starting")
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Configurazione non valida: correggere ambiente e secret prima di riavviare", "event", "config.failed", "reason", err.Error())
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
		logger.Error("Connessione PostgreSQL fallita", "event", "database.connect_failed", logging.Error(err))
		return errors.New("database non raggiungibile")
	}
	defer func() {
		if err := s.Close(); err != nil {
			logger.Error("Chiusura PostgreSQL fallita", "event", "database.close_failed", logging.Error(err))
		}
	}()
	logger.Info("Connessione PostgreSQL disponibile", "event", "database.connected")
	if err = migrations.Check(startup, s.DB); err != nil {
		logger.Error("Schema non pronto: eseguire il servizio migrate della stessa immagine", "event", "database.schema_failed", logging.Error(err))
		return err
	}
	authManager, err := auth.New(startup, cfg, s)
	if err != nil {
		logger.Error("Autenticazione Google non disponibile: verificare accesso HTTPS a accounts.google.com", "event", "auth.init_failed", logging.Error(err))
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
	go func() {
		logger.Info("Server HTTP in avvio", "event", "server.listening", "address", cfg.Addr)
		serverResult <- server.ListenAndServe()
	}()
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
	logger.Info("Arresto applicazione: attesa delle operazioni in corso", "event", "server.stopping")
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
	logger.Info("Arresto applicazione completato", "event", "server.stopped")
	return result
}
