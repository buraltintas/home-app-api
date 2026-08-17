package config

import (
	"testing"

	"github.com/burakaltintas/home-app-api/internal/brand"
)

func requiredTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://example.invalid/home_app")
	t.Setenv("BFF_SECRETS", "current,previous")
	t.Setenv("ACCESS_TOKEN_SECRET", "test-access-secret-at-least-32-bytes")
	t.Setenv("OTP_HASH_SECRET", "test-otp-secret-at-least-32-bytes")
}

func TestLoadRejectsUnknownObjectStorageProvider(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("OBJECT_STORAGE_PROVIDER", "unknown")
	if _, err := Load(); err == nil {
		t.Fatal("unknown object storage provider accepted")
	}
}

func TestLoadSupportsBFFSecretRotationAndDefaultLocale(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("DEFAULT_LOCALE", "de-DE")
	t.Setenv("EMAIL_FROM", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BFFSecrets) != 2 || cfg.BFFSecrets[0] != "current" || cfg.BFFSecrets[1] != "previous" || cfg.DefaultLocale != "de" || cfg.EmailFrom != brand.DefaultEmailFrom || cfg.StoreVisitProofTTL.Hours() != 720 || cfg.StoreLocationMaxAccuracyMeters != 100 || cfg.StoreReviewRadiusMeters != 500 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadSupportsGCSWithoutStaticStorageCredentials(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("OBJECT_STORAGE_PROVIDER", "gcs")
	t.Setenv("OBJECT_STORAGE_BUCKET", "bosagezme-production")
	t.Setenv("OBJECT_STORAGE_ACCESS_KEY", "")
	t.Setenv("OBJECT_STORAGE_SECRET_KEY", "")
	t.Setenv("GCS_SIGNING_SERVICE_ACCOUNT", "bosagezme@example-project.iam.gserviceaccount.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ObjectStorageProvider != "gcs" || cfg.Bucket != "bosagezme-production" || cfg.GCSSigningServiceAccount == "" {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadRequiresExplicitGCSBucket(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("OBJECT_STORAGE_PROVIDER", "gcs")
	t.Setenv("OBJECT_STORAGE_BUCKET", "")
	if _, err := Load(); err == nil {
		t.Fatal("gcs accepted without an explicit bucket")
	}
}

func TestLoadSupportsGoogleWorkspaceGmailProvider(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("EMAIL_PROVIDER", "gmail")
	t.Setenv("EMAIL_FROM", "Boşa Gezme! <no-reply@bosagezme.com>")
	t.Setenv("GMAIL_IMPERSONATED_USER", "no-reply@bosagezme.com")
	t.Setenv("GMAIL_SERVICE_ACCOUNT_FILE", "/run/secrets/gmail-service-account.json")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmailProvider != "gmail" || cfg.GmailImpersonatedUser != "no-reply@bosagezme.com" {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadDoesNotRequireWorkerGmailSecretForAPIProcess(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("EMAIL_PROVIDER", "gmail")
	t.Setenv("EMAIL_FROM", "no-reply@bosagezme.com")
	t.Setenv("GMAIL_IMPERSONATED_USER", "no-reply@bosagezme.com")
	t.Setenv("GMAIL_SERVICE_ACCOUNT_FILE", "")
	t.Setenv("GMAIL_SERVICE_ACCOUNT_JSON", "")
	if _, err := Load(); err != nil {
		t.Fatalf("shared API config required worker-only Gmail secret: %v", err)
	}
}

func TestLoadRejectsMismatchedGmailSender(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("EMAIL_PROVIDER", "gmail")
	t.Setenv("EMAIL_FROM", "other@bosagezme.com")
	t.Setenv("GMAIL_IMPERSONATED_USER", "no-reply@bosagezme.com")
	t.Setenv("GMAIL_SERVICE_ACCOUNT_JSON", `{}`)
	if _, err := Load(); err == nil {
		t.Fatal("mismatched Gmail sender accepted")
	}
}
