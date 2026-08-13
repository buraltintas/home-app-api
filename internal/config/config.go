package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type Config struct {
	Environment, HTTPAddr, DatabaseURL                                     string
	BFFSecrets                                                             []string
	AccessTokenSecret, OTPHashSecret                                       string
	AccessTokenTTL, RefreshTokenTTL                                        time.Duration
	OTPTTL                                                                 time.Duration
	OTPMaxAttempts                                                         int
	OTPEmailRequestLimit, OTPIPRequestLimit                                int
	OTPVisitorRequestLimit                                                 int
	GoogleClientID, GooglePlacesAPIKey                                     string
	OpenAIAPIKey, OpenAIModel                                              string
	OpenAITimeout                                                          time.Duration
	EmailProvider, EmailFrom                                               string
	EmailDevelopmentDir                                                    string
	EmailAPIURL, EmailAPIKey                                               string
	ObjectStorageProvider, Bucket                                          string
	ObjectStorageRegion, ObjectStorageEndpoint                             string
	GCSSigningServiceAccount                                               string
	ObjectStorageLocalDir, ObjectStoragePublicURL                          string
	ObjectStorageAccessKey, ObjectStorageSecretKey                         string
	ObjectStoragePathStyle                                                 bool
	ObjectStorageUploadTTL                                                 time.Duration
	MediaMaxBytes                                                          int64
	StoreReviewRadiusMeters                                                float64
	SearchLocationDecimals                                                 int
	ReportingTimezone                                                      string
	SearchAttributionWindow                                                time.Duration
	SearchRetentionDays, SearchLocationRetentionDays, VisitorRetentionDays int
	MetricsToken                                                           string
	OTELEnabled                                                            bool
	OTLPEndpoint                                                           string
	DefaultLocale                                                          i18n.Locale
}

func Load() (Config, error) {
	emailAPIKey := os.Getenv("EMAIL_API_KEY")
	if emailAPIKey == "" {
		emailAPIKey = os.Getenv("RESEND_API_KEY")
	}
	c := Config{
		Environment: env("APP_ENV", "development"), HTTPAddr: env("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"), BFFSecrets: split(os.Getenv("BFF_SECRETS")),
		AccessTokenSecret: os.Getenv("ACCESS_TOKEN_SECRET"), OTPHashSecret: os.Getenv("OTP_HASH_SECRET"),
		GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), GooglePlacesAPIKey: os.Getenv("GOOGLE_PLACES_API_KEY"),
		OpenAIAPIKey: os.Getenv("OPENAI_API_KEY"), OpenAIModel: env("OPENAI_MODEL", "gpt-5-mini"),
		EmailProvider: env("EMAIL_PROVIDER", "development"), EmailFrom: env("EMAIL_FROM", "no-reply@example.test"),
		EmailDevelopmentDir: env("EMAIL_DEVELOPMENT_DIR", ".data/mailbox"),
		EmailAPIURL:         os.Getenv("EMAIL_API_URL"), EmailAPIKey: emailAPIKey,
		ObjectStorageProvider: env("OBJECT_STORAGE_PROVIDER", "development"), Bucket: os.Getenv("OBJECT_STORAGE_BUCKET"),
		ObjectStorageRegion: env("OBJECT_STORAGE_REGION", "auto"), ObjectStorageEndpoint: os.Getenv("OBJECT_STORAGE_ENDPOINT"), ObjectStorageAccessKey: os.Getenv("OBJECT_STORAGE_ACCESS_KEY"), ObjectStorageSecretKey: os.Getenv("OBJECT_STORAGE_SECRET_KEY"),
		GCSSigningServiceAccount: os.Getenv("GCS_SIGNING_SERVICE_ACCOUNT"),
		ObjectStorageLocalDir:    env("OBJECT_STORAGE_LOCAL_DIR", ".data/uploads"), ObjectStoragePublicURL: env("OBJECT_STORAGE_PUBLIC_URL", "http://localhost:8080/uploads"),
		ReportingTimezone: env("REPORTING_TIMEZONE", "Europe/Istanbul"),
		MetricsToken:      os.Getenv("METRICS_TOKEN"),
		OTLPEndpoint:      os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	}
	defaultLocale, ok := i18n.Normalize(env("DEFAULT_LOCALE", string(i18n.DefaultLocale)))
	if !ok {
		return c, errors.New("DEFAULT_LOCALE must be tr, en, de or ru")
	}
	c.DefaultLocale = defaultLocale
	var err error
	if c.AccessTokenTTL, err = duration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return c, err
	}
	if c.RefreshTokenTTL, err = duration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return c, err
	}
	if c.OTPTTL, err = duration("OTP_TTL", 10*time.Minute); err != nil {
		return c, err
	}
	if c.OpenAITimeout, err = duration("OPENAI_TIMEOUT", 3*time.Second); err != nil {
		return c, err
	}
	if c.OTPMaxAttempts, err = integer("OTP_MAX_ATTEMPTS", 5); err != nil {
		return c, err
	}
	if c.OTPEmailRequestLimit, err = integer("OTP_EMAIL_REQUEST_LIMIT", 3); err != nil {
		return c, err
	}
	if c.OTPIPRequestLimit, err = integer("OTP_IP_REQUEST_LIMIT", 10); err != nil {
		return c, err
	}
	if c.OTPVisitorRequestLimit, err = integer("OTP_VISITOR_REQUEST_LIMIT", 5); err != nil {
		return c, err
	}
	if c.SearchLocationDecimals, err = integer("SEARCH_LOCATION_DECIMALS", 3); err != nil {
		return c, err
	}
	if c.StoreReviewRadiusMeters, err = number("STORE_REVIEW_RADIUS_METERS", 500); err != nil {
		return c, err
	}
	if c.ObjectStorageUploadTTL, err = duration("OBJECT_STORAGE_UPLOAD_TTL", 15*time.Minute); err != nil {
		return c, err
	}
	mediaMax, err := integer("MEDIA_MAX_BYTES", 10<<20)
	if err != nil {
		return c, err
	}
	c.MediaMaxBytes = int64(mediaMax)
	pathStyle := env("OBJECT_STORAGE_PATH_STYLE", "true")
	c.ObjectStoragePathStyle = pathStyle == "true"
	if c.OTELEnabled, err = boolean("OTEL_ENABLED", false); err != nil {
		return c, err
	}
	attributionHours, err := integer("SEARCH_ATTRIBUTION_WINDOW_HOURS", 72)
	if err != nil {
		return c, err
	}
	if attributionHours < 1 || attributionHours > 24*30 {
		return c, errors.New("SEARCH_ATTRIBUTION_WINDOW_HOURS must be between 1 and 720")
	}
	c.SearchAttributionWindow = time.Duration(attributionHours) * time.Hour
	if c.SearchRetentionDays, err = integer("SEARCH_RETENTION_DAYS", 365); err != nil {
		return c, err
	}
	if c.SearchLocationRetentionDays, err = integer("SEARCH_LOCATION_RETENTION_DAYS", 30); err != nil {
		return c, err
	}
	if c.VisitorRetentionDays, err = integer("VISITOR_RETENTION_DAYS", 180); err != nil {
		return c, err
	}
	if c.SearchRetentionDays < 1 || c.SearchLocationRetentionDays < 1 || c.VisitorRetentionDays < 1 {
		return c, errors.New("retention days must be positive")
	}
	if _, err = time.LoadLocation(c.ReportingTimezone); err != nil {
		return c, fmt.Errorf("REPORTING_TIMEZONE: %w", err)
	}
	if len(c.BFFSecrets) == 0 {
		if legacy := os.Getenv("BFF_SECRET"); legacy != "" {
			c.BFFSecrets = []string{legacy}
		}
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if len(c.BFFSecrets) == 0 {
		return c, errors.New("BFF_SECRETS or BFF_SECRET is required")
	}
	if len(c.AccessTokenSecret) < 32 {
		return c, errors.New("ACCESS_TOKEN_SECRET must contain at least 32 bytes")
	}
	if len(c.OTPHashSecret) < 32 {
		return c, errors.New("OTP_HASH_SECRET must contain at least 32 bytes")
	}
	if c.OTPMaxAttempts < 1 || c.OTPMaxAttempts > 20 {
		return c, errors.New("OTP_MAX_ATTEMPTS must be between 1 and 20")
	}
	if c.OTPEmailRequestLimit < 1 || c.OTPIPRequestLimit < 1 || c.OTPVisitorRequestLimit < 1 {
		return c, errors.New("OTP request limits must be positive")
	}
	if c.SearchLocationDecimals < 0 || c.SearchLocationDecimals > 5 {
		return c, errors.New("SEARCH_LOCATION_DECIMALS must be 0..5")
	}
	if c.ObjectStorageProvider == "s3" || c.ObjectStorageProvider == "r2" {
		if c.ObjectStorageAccessKey == "" || c.ObjectStorageSecretKey == "" || c.Bucket == "" {
			return c, errors.New("object storage credentials and bucket are required")
		}
	}
	if c.ObjectStorageProvider == "gcs" && c.Bucket == "" {
		return c, errors.New("OBJECT_STORAGE_BUCKET is required for gcs")
	}
	if c.ObjectStorageProvider != "development" && c.ObjectStorageProvider != "s3" && c.ObjectStorageProvider != "r2" && c.ObjectStorageProvider != "gcs" {
		return c, errors.New("OBJECT_STORAGE_PROVIDER must be development, gcs, s3 or r2")
	}
	if c.EmailProvider == "resend" && (c.EmailAPIKey == "" || strings.TrimSpace(c.EmailFrom) == "") {
		return c, errors.New("RESEND_API_KEY (or EMAIL_API_KEY) and EMAIL_FROM are required for resend")
	}
	if c.EmailProvider != "development" && c.EmailProvider != "resend" {
		return c, errors.New("EMAIL_PROVIDER must be development or resend")
	}
	return c, nil
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func split(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func duration(k string, d time.Duration) (time.Duration, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, e := time.ParseDuration(v)
	if e != nil {
		return 0, fmt.Errorf("%s: %w", k, e)
	}
	return n, nil
}
func integer(k string, d int) (int, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, e := strconv.Atoi(v)
	if e != nil {
		return 0, fmt.Errorf("%s: %w", k, e)
	}
	return n, nil
}
func number(k string, d float64) (float64, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, e := strconv.ParseFloat(v, 64)
	if e != nil {
		return 0, fmt.Errorf("%s: %w", k, e)
	}
	return n, nil
}

func boolean(k string, d bool) (bool, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}
