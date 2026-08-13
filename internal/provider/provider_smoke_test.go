//go:build provider

package provider_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/media"
	"github.com/burakaltintas/home-app-api/internal/search"
)

func TestOpenAIIntentSmoke(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY is not available")
	}
	parser := search.NewOpenAIParser(key, env("OPENAI_MODEL", "gpt-5-mini"), 15*time.Second)
	queries := []struct {
		query  string
		locale string
	}{
		{"Antalya'da uygun fiyatlı perde mağazası arıyorum", "tr"},
		{"Affordable curtain stores in Antalya", "en"},
		{"Günstige Gardinengeschäfte in Antalya", "de"},
		{"Недорогие магазины штор в Анталии", "ru"},
	}
	for _, test := range queries {
		t.Run(test.locale, func(t *testing.T) {
			intent, err := parser.ParseSearchIntent(context.Background(), test.query, search.Context{Locale: i18n.Locale(test.locale)})
			if err != nil {
				t.Fatal(err)
			}
			if err = search.Validate(intent); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGooglePlacesSmoke(t *testing.T) {
	key := os.Getenv("GOOGLE_PLACES_API_KEY")
	if key == "" {
		t.Skip("GOOGLE_PLACES_API_KEY is not available")
	}
	provider := search.NewGooglePlaces(key)
	lat, lon := 40.99, 29.03
	places, err := provider.TextSearch(context.Background(), "Kadıköy mobilya mağazası", &lat, &lon, 5000)
	if err != nil || len(places) == 0 {
		t.Fatalf("places=%d err=%v", len(places), err)
	}
	detail, err := provider.PlaceDetails(context.Background(), places[0].PlaceID)
	if err != nil || detail.PlaceID == "" || detail.Name == "" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestResendSmoke(t *testing.T) {
	key, recipient := os.Getenv("RESEND_API_KEY"), os.Getenv("RESEND_TEST_RECIPIENT")
	if key == "" || recipient == "" {
		t.Skip("RESEND_API_KEY and explicit RESEND_TEST_RECIPIENT are required")
	}
	sender := &email.ResendSender{URL: env("EMAIL_API_URL", "https://api.resend.com/emails"), APIKey: key, Client: &http.Client{Timeout: 10 * time.Second}}
	id, err := sender.Send(context.Background(), email.Message{From: os.Getenv("EMAIL_FROM"), To: recipient, Subject: "home-app provider smoke test", Text: "This is an explicitly requested home-app provider smoke test.", HTML: "<p>This is an explicitly requested home-app provider smoke test.</p>"})
	if err != nil || id == "" {
		t.Fatalf("provider id=%q err=%v", id, err)
	}
}

func TestGoogleCloudStorageSmoke(t *testing.T) {
	bucket := os.Getenv("GCS_TEST_BUCKET")
	if bucket == "" {
		t.Skip("GCS_TEST_BUCKET and Application Default Credentials are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider, err := media.NewGCSStorage(ctx, media.GCSConfig{Bucket: bucket, SigningServiceAccount: os.Getenv("GCS_SIGNING_SERVICE_ACCOUNT"), UploadTTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	key := "provider-smoke/" + time.Now().UTC().Format("20060102T150405.000000000") + ".png"
	upload, err := provider.CreateUpload(ctx, key, "image/png", 8)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = provider.Delete(cleanupContext, key)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.UploadURL, bytes.NewReader([]byte("gcs-test")))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range upload.Headers {
		req.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("upload status=%d", response.StatusCode)
	}
	info, err := provider.Stat(ctx, key)
	if err != nil || info.Size != 8 || info.ContentType != "image/png" {
		t.Fatalf("object=%+v err=%v", info, err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
