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
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Error("database unavailable", "error", e)
		os.Exit(1)
	}
	defer db.Close()
	tokens := security.NewTokenManager(cfg.AccessTokenSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	authSvc := auth.NewService(db, auth.Config{OTPTTL: cfg.OTPTTL, OTPMaxAttempts: cfg.OTPMaxAttempts, RefreshTTL: cfg.RefreshTokenTTL, HashKey: []byte(cfg.OTPHashSecret)}, tokens, auth.NewGoogleVerifier(cfg.GoogleClientID))
	stores := storepkg.NewService(db)
	socialSvc := social.NewService(db, cfg.StoreReviewRadiusMeters)
	var ai searchpkg.IntentParser
	if cfg.OpenAIAPIKey != "" {
		ai = searchpkg.NewOpenAIParser(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAITimeout)
	}
	places := searchpkg.NewGooglePlaces(cfg.GooglePlacesAPIKey)
	searchSvc := searchpkg.NewService(db, stores, ai, places, cfg.OpenAIModel, cfg.SearchLocationDecimals)
	users := userpkg.NewService(db)
	api := server.NewServer(db, authSvc, stores, socialSvc, searchSvc, users, []byte(cfg.OTPHashSecret))
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(log, cfg.BFFSecrets, tokens), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
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
