package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/email"
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
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Error("database unavailable", "error", e)
		os.Exit(1)
	}
	defer db.Close()
	var sender email.Sender = email.DevSender{}
	if cfg.EmailProvider == "resend" {
		url := cfg.EmailAPIURL
		if url == "" {
			url = "https://api.resend.com/emails"
		}
		sender = &email.ResendSender{URL: url, APIKey: cfg.EmailAPIKey, Client: &http.Client{Timeout: 10 * time.Second}}
	}
	w := email.NewWorker(db, sender, cfg.EmailFrom, []byte(cfg.OTPHashSecret), log)
	reportSvc, e := reporting.NewService(db, cfg.ReportingTimezone)
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
