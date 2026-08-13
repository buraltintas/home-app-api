package config

import "testing"

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
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BFFSecrets) != 2 || cfg.BFFSecrets[0] != "current" || cfg.BFFSecrets[1] != "previous" || cfg.DefaultLocale != "de" {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadSupportsGCSWithoutStaticStorageCredentials(t *testing.T) {
	requiredTestEnvironment(t)
	t.Setenv("OBJECT_STORAGE_PROVIDER", "gcs")
	t.Setenv("OBJECT_STORAGE_BUCKET", "home-app-production")
	t.Setenv("OBJECT_STORAGE_ACCESS_KEY", "")
	t.Setenv("OBJECT_STORAGE_SECRET_KEY", "")
	t.Setenv("GCS_SIGNING_SERVICE_ACCOUNT", "home-app@example-project.iam.gserviceaccount.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ObjectStorageProvider != "gcs" || cfg.Bucket != "home-app-production" || cfg.GCSSigningServiceAccount == "" {
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
