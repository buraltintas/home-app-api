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
	"github.com/burakaltintas/home-app-api/internal/changes"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
	"github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/feedback"
	locationpkg "github.com/burakaltintas/home-app-api/internal/location"
	"github.com/burakaltintas/home-app-api/internal/media"
	"github.com/burakaltintas/home-app-api/internal/moderation"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/burakaltintas/home-app-api/internal/privacy"
	"github.com/burakaltintas/home-app-api/internal/readcache"
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
	// Told about every round trip, so work that can wait is done while the database is
	// already awake for somebody else rather than waking it on its own.
	activity := &database.Activity{}
	db, e := database.OpenWatched(ctx, cfg.DatabaseURL, activity)
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
		// The same key and model read a review's words before they are published. Without a
		// key a review that has words is held for a person rather than published unread.
		socialSvc.SetModerator(moderation.NewOpenAIChecker(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAITimeout))
	}
	searchSvc := searchpkg.NewService(db, stores, ai, cfg.OpenAIModel, cfg.SearchLocationDecimals, reportSvc, cfg.SearchAttributionWindow, time.Duration(cfg.VisitorRetentionDays)*24*time.Hour)
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
	// Whoever writes a row tells the worker, instead of the worker asking the
	// database every second whether one has appeared. That question, asked all
	// night for a queue that is empty all night, is what kept the database from
	// ever suspending itself.
	authSvc.SetMailNotifier(emailWorker.Notify)
	adminSvc := adminpkg.NewService(db)
	// Where to search comes from our own table now. No key, no provider, nothing to be
	// unavailable -- so nothing here is conditional on configuration.
	placesSvc := locationpkg.NewService(db)
	feedbackSvc := feedback.NewService(db, cfg.FeedbackNotifyEmail)
	feedbackSvc.SetMailNotifier(emailWorker.Notify)
	api := server.NewServer(db, authSvc, stores, socialSvc, searchSvc, placesSvc, users, mediaSvc, adminSvc, reportSvc, feedbackSvc, []byte(cfg.OTPHashSecret))
	// The answers held below for anonymous readers are dropped by a write in the instance it
	// landed in. The other instances learn of it from one small object in the media bucket,
	// looked at once per interval inside an anonymous read -- never by asking the database,
	// which would keep it awake. Set up before the catalogue is read, so a write landing on
	// another instance while this one starts is taken as news rather than assumed to be in
	// what it read.
	var marker *changes.Marker
	switch {
	case cfg.CatalogChangesInterval == 0:
		log.Info("catalog change marker off; writes on another instance reach this one by the catalogue copy's maximum age")
	case cfg.ObjectStorageProvider != "gcs":
		log.Info("catalog change marker needs OBJECT_STORAGE_PROVIDER=gcs; writes on another instance reach this one by the catalogue copy's maximum age")
	default:
		objects, e := changes.NewGCS(ctx, cfg.Bucket)
		if e != nil {
			log.Warn("catalog change marker unavailable; writes on another instance reach this one by the catalogue copy's maximum age", "error", e)
			break
		}
		marker = changes.New(objects, changes.ObjectName(cfg.Environment), cfg.CatalogChangesInterval, log)
		api.SetChanges(marker)
		log.Info("catalog change marker enabled", "object", changes.ObjectName(cfg.Environment), "interval", cfg.CatalogChangesInterval.String())
	}
	// Anonymous catalogue reads are answered from this process. Measured over four daytime
	// hours, it takes 3,817 requests down to 302 and gives the database 107 minutes of the
	// quiet it needs to suspend itself -- 45% of the window -- where today it gets none.
	api.SetReadCache(readcache.New(cfg.ReadCacheBytes, cfg.ReadCacheTTL))
	// And the rest of them from a copy of the whole catalogue: the per-answer cache above hit
	// 4% of store pages, because a crawler asks for each shop about once per language, so the
	// database still never got five quiet minutes. Read here, before the port opens, while
	// Cloud Run gives a starting container its CPU and the database is awake from the
	// connection check above. If it cannot be read, the service starts anyway and the database
	// answers as before until a later read succeeds.
	if cfg.CatalogSnapshot != "off" {
		catalog := storepkg.NewCatalog(db, activity, cfg.CatalogSnapshotMaxAge, log)
		loadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if e := catalog.Load(loadCtx); e != nil {
			log.Warn("catalog snapshot unavailable at startup; catalogue reads go to the database until it can be read", "error", e)
		}
		cancel()
		api.SetCatalog(catalog, cfg.CatalogSnapshot == "shadow")
		log.Info("catalog snapshot enabled", "mode", cfg.CatalogSnapshot, "max_age", cfg.CatalogSnapshotMaxAge.String())
	}
	// Said out loud, because the safe direction here is also the useless one: with nobody
	// named, no caller may state who a request is for, and every rate limit counted against
	// an address falls back to the connection -- which for the website is one address for
	// everybody. A silent revert of that is exactly how it went unnoticed the first time.
	if len(cfg.BFFAddressBearerSecrets) == 0 {
		log.Warn("no BFF_ADDRESS_BEARER_SECRETS configured: the web server cannot state a visitor's address, so per-address limits count the whole site as one caller")
	}
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(log, cfg.BFFSecrets, tokens, cfg.MetricsToken, cfg.DefaultLocale, cfg.AdminEmails, server.AddressBearers(cfg.BFFAddressBearerSecrets), server.RuntimeConfig{StoreReviewRadiusMeters: cfg.StoreReviewRadiusMeters}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
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
	// Daily metrics are rolled up here for the same reason the retention sweep is: the
	// separate worker binary that used to own this is not deployed, so nothing aggregated
	// and every report in the admin panel read empty over data that was all in the tables.
	// Aggregation takes a database lock, so running it in every instance is safe.
	go func() {
		if e := reportSvc.Run(ctx); e != nil && !errors.Is(e, context.Canceled) {
			log.Error("reporting aggregation stopped", "error", e)
		}
	}()
	if cfg.DeliversMail() {
		// A row this process was never told about -- a retry come due, one left by an
		// instance that died -- is looked for whenever the database is awake anyway.
		activity.OnUse(emailWorker.DatabaseInUse)
		go func() {
			log.Info("email worker started", "email_provider", cfg.EmailProvider)
			if e := emailWorker.Run(ctx); e != nil && !errors.Is(e, context.Canceled) {
				log.Error("email worker stopped", "error", e)
			}
		}()
	} else {
		log.Warn("email worker disabled: this is a development process pointed at a remote database, and draining the shared outbox here would deliver other people's sign-in codes to this machine")
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := server.Shutdown(shutdown); e != nil {
		log.Error("shutdown failed", "error", e)
	}
	// A write here whose news Cloud Storage refused keeps it for this instance's next write
	// or look, and a stopping instance has neither. Told now, in the seconds Cloud Run gives
	// it, after the last request has finished.
	if e := marker.Flush(shutdown); e != nil {
		log.Warn("catalog change could not be told to the other instances before stopping; they show it by their copy's maximum age", "error", e)
	}
}
