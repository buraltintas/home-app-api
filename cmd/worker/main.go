package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/burakaltintas/home-app-api/internal/reporting"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, e := config.Load()
	if e != nil {
		log.Error("invalid configuration", "error", e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTracing, e := observability.SetupTracing(ctx, cfg.OTELEnabled, cfg.OTLPEndpoint, cfg.Environment)
	if e != nil {
		log.Error("tracing unavailable", "error", e)
		os.Exit(1)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutdown)
	}()
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Error("database unavailable", "error", e)
		os.Exit(1)
	}
	defer db.Close()
	sender, e := email.NewSender(ctx, cfg.EmailSenderOptions())
	if e != nil {
		log.Error("email sender unavailable", "error", e, "email_provider", cfg.EmailProvider)
		os.Exit(1)
	}
	w := email.NewWorker(db, sender, cfg.EmailFrom, []byte(cfg.OTPHashSecret), log)
	reportSvc, e := reporting.NewService(db, cfg.ReportingTimezone, cfg.SearchAttributionWindow)
	if e != nil {
		log.Error("reporting unavailable", "error", e)
		os.Exit(1)
	}
	log.Info("worker started", "email_provider", cfg.EmailProvider)
	errCh := make(chan error, 2)
	go func() { errCh <- w.Run(ctx) }()
	go func() { errCh <- reportSvc.Run(ctx) }()
	e = <-errCh
	if e != nil && e != context.Canceled {
		log.Error("worker stopped", "error", e)
		os.Exit(1)
	}
}
