//go:build provider

package provider_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/email"
	"github.com/burakaltintas/home-app-api/internal/search"
)

func TestOpenAIIntentSmoke(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY is not available")
	}
	parser := search.NewOpenAIParser(key, env("OPENAI_MODEL", "gpt-5-mini"), 15*time.Second)
	queries := []string{
		"Kadıköy'de modern avize bakabileceğim mağazalar",
		"Antalya'da uygun fiyatlı perde mağazası arıyorum",
		"Yeni ev kuruyorum, Ankara'da modern ama çok pahalı olmayan mobilya mağazaları",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			intent, err := parser.ParseSearchIntent(context.Background(), query, search.Context{Locale: "tr-TR"})
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

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
