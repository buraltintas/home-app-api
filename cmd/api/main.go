package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	adminpkg "github.com/burakaltintas/home-app-api/internal/admin"
	"github.com/burakaltintas/home-app-api/internal/auth"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/media"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/burakaltintas/home-app-api/internal/privacy"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	searchpkg "github.com/burakaltintas/home-app-api/internal/search"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/burakaltintas/home-app-api/internal/server"
	"github.com/burakaltintas/home-app-api/internal/social"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	userpkg "github.com/burakaltintas/home-app-api/internal/user"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
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
	tokens := security.NewTokenManager(cfg.AccessTokenSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	reportSvc, e := reporting.NewService(db, cfg.ReportingTimezone, cfg.SearchAttributionWindow)
	if e != nil {
		log.Error("reporting unavailable", "error", e)
		os.Exit(1)
	}
	authSvc := auth.NewService(db, auth.Config{OTPTTL: cfg.OTPTTL, OTPMaxAttempts: cfg.OTPMaxAttempts, OTPEmailLimit: cfg.OTPEmailRequestLimit, OTPIPLimit: cfg.OTPIPRequestLimit, OTPVisitorLimit: cfg.OTPVisitorRequestLimit, VisitorTTL: time.Duration(cfg.VisitorRetentionDays) * 24 * time.Hour, RefreshTTL: cfg.RefreshTokenTTL, HashKey: []byte(cfg.OTPHashSecret), AppReviewEmail: cfg.AppReviewEmail, AppReviewCode: cfg.AppReviewCode}, tokens, auth.NewGoogleVerifier(cfg.GoogleClientID), reportSvc)
	stores := storepkg.NewService(db, reportSvc)
	socialSvc := social.NewService(db, social.Config{ReviewRadiusMeters: cfg.StoreReviewRadiusMeters, VisitProofTTL: cfg.StoreVisitProofTTL, MaxLocationAccuracyMeters: cfg.StoreLocationMaxAccuracyMeters}, reportSvc)
	var ai searchpkg.IntentParser
	// A key that does not look like a key is almost always a paste that carried the variable
	// name with it. Refusing to boot over it would trade a degraded search for a dead site,
	// so this warns as loudly as a log can instead.
	if cfg.OpenAIAPIKey != "" && !strings.HasPrefix(cfg.OpenAIAPIKey, "sk-") {
		log.Warn("OPENAI_API_KEY does not start with sk- and will be rejected by the provider; search will fall back to the deterministic parser")
	}
	if cfg.OpenAIAPIKey != "" {
		ai = searchpkg.NewOpenAIParser(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAITimeout)
	}
	var places searchpkg.PlacesProvider
	if cfg.GooglePlacesAPIKey != "" {
		places = searchpkg.NewGooglePlaces(cfg.GooglePlacesAPIKey)
	}
	searchSvc := searchpkg.NewService(db, stores, ai, places, cfg.OpenAIModel, cfg.SearchLocationDecimals, reportSvc, cfg.SearchAttributionWindow, time.Duration(cfg.VisitorRetentionDays)*24*time.Hour)
	users := userpkg.NewService(db, reportSvc)
	var storage media.ObjectStorage
	switch cfg.ObjectStorageProvider {
	case "s3", "r2":
		storage, e = media.NewS3Storage(ctx, media.S3Config{Region: cfg.ObjectStorageRegion, Endpoint: cfg.ObjectStorageEndpoint, AccessKey: cfg.ObjectStorageAccessKey, SecretKey: cfg.ObjectStorageSecretKey, Bucket: cfg.Bucket, PathStyle: cfg.ObjectStoragePathStyle, UploadTTL: cfg.ObjectStorageUploadTTL})
		if e != nil {
			log.Error("object storage unavailable", "error", e)
			os.Exit(1)
		}
	case "gcs":
		storage, e = media.NewGCSStorage(ctx, media.GCSConfig{Bucket: cfg.Bucket, SigningServiceAccount: cfg.GCSSigningServiceAccount, UploadTTL: cfg.ObjectStorageUploadTTL})
		if e != nil {
			log.Error("Google Cloud Storage unavailable", "error", e)
			os.Exit(1)
		}
	default:
		storage, e = media.NewLocalStorage(cfg.ObjectStorageLocalDir, cfg.ObjectStoragePublicURL, cfg.ObjectStorageUploadTTL, []byte(cfg.OTPHashSecret))
		if e != nil {
			log.Error("local object storage unavailable", "error", e)
			os.Exit(1)
		}
	}
	mediaSvc := media.NewService(db, storage, cfg.MediaMaxBytes, reportSvc)
	// Sign-in is unusable if mail cannot leave the process, and the outbox is drained
	// here now, so a broken provider is a startup failure rather than a queue that grows
	// silently behind a healthy-looking service.
	sender, e := email.NewSender(ctx, cfg.EmailSenderOptions())
	if e != nil {
		log.Error("email sender unavailable", "error", e, "email_provider", cfg.EmailProvider)
		os.Exit(1)
	}
	emailWorker := email.NewWorker(db, sender, cfg.EmailFrom, []byte(cfg.OTPHashSecret), log)
	adminSvc := adminpkg.NewService(db)
	api := server.NewServer(db, authSvc, stores, socialSvc, searchSvc, users, mediaSvc, adminSvc, reportSvc, []byte(cfg.OTPHashSecret))
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(log, cfg.BFFSecrets, tokens, cfg.MetricsToken, cfg.DefaultLocale, cfg.AdminEmails), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr, "environment", cfg.Environment)
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Error("server failed", "error", e)
			stop()
		}
	}()
	// The outbox is drained inside this process so the deployment stays one service.
	// Requesting a code still only enqueues a row; nothing here makes the HTTP handler
	// wait on the mail provider. Run returns when ctx is cancelled, which is the normal
	// shutdown path and not worth reporting as a failure.
	go privacy.Run(ctx, db, privacy.Config{
		SearchRetentionDays:         cfg.SearchRetentionDays,
		SearchLocationRetentionDays: cfg.SearchLocationRetentionDays,
		VisitorRetentionDays:        cfg.VisitorRetentionDays,
	}, log)
	go func() {
		log.Info("email worker started", "email_provider", cfg.EmailProvider)
		if e := emailWorker.Run(ctx); e != nil && !errors.Is(e, context.Canceled) {
			log.Error("email worker stopped", "error", e)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := server.Shutdown(shutdown); e != nil {
		log.Error("shutdown failed", "error", e)
	}
}
