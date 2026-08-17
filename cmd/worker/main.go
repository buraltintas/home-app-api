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
	var sender email.Sender = email.FileSender{Dir: cfg.EmailDevelopmentDir}
	switch cfg.EmailProvider {
	case "resend":
		url := cfg.EmailAPIURL
		if url == "" {
			url = "https://api.resend.com/emails"
		}
		sender = &email.ResendSender{URL: url, APIKey: cfg.EmailAPIKey, Client: &http.Client{Timeout: 10 * time.Second}}
	case "gmail":
		credentials := []byte(cfg.GmailServiceAccountJSON)
		if len(credentials) == 0 {
			if cfg.GmailServiceAccountFile == "" {
				log.Error("gmail credentials unavailable", "error", "GMAIL_SERVICE_ACCOUNT_FILE or GMAIL_SERVICE_ACCOUNT_JSON is required by the worker")
				os.Exit(1)
			}
			credentials, e = os.ReadFile(cfg.GmailServiceAccountFile)
			if e != nil {
				log.Error("gmail credentials unavailable", "error", e)
				os.Exit(1)
			}
		}
		gmailSender, gmailErr := email.NewGmailSender(ctx, credentials, cfg.GmailImpersonatedUser, cfg.GmailAPIURL)
		if gmailErr != nil {
			log.Error("gmail sender unavailable", "error", gmailErr)
			os.Exit(1)
		}
		sender = gmailSender
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
