package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/burakaltintas/home-app-api/internal/auth"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/media"
	"github.com/burakaltintas/home-app-api/internal/observability"
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
	api := server.NewServer(db, authSvc, stores, socialSvc, searchSvc, users, mediaSvc, []byte(cfg.OTPHashSecret))
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(log, cfg.BFFSecrets, tokens, cfg.MetricsToken, cfg.DefaultLocale), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr, "environment", cfg.Environment)
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Error("server failed", "error", e)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := server.Shutdown(shutdown); e != nil {
		log.Error("shutdown failed", "error", e)
	}
}
