package email

import (
	"strings"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/burakaltintas/home-app-api/internal/i18n"
)

func TestWelcomeTemplatesCoverSupportedLocales(t *testing.T) {
	for _, locale := range i18n.Supported() {
		copy, ok := welcomeTemplates[locale]
		if !ok || copy.Subject == "" || copy.Preheader == "" || copy.Eyebrow == "" || copy.Title == "" || copy.Intro == "" || copy.CTALabel == "" || copy.Closing == "" || copy.Footer == "" || len(copy.Steps) == 0 {
			t.Fatalf("incomplete welcome template for %s", locale)
		}
		for _, step := range copy.Steps {
			if step.Title == "" || step.Body == "" {
				t.Fatalf("incomplete welcome step for %s: %+v", locale, step)
			}
		}
		message, err := RenderWelcome(locale)
		if err != nil || strings.Contains(message.HTML, "{{.") || strings.Contains(message.Text, "{{.") {
			t.Fatalf("render %s: err=%v", locale, err)
		}
		if !strings.Contains(message.Subject, brand.ProductName) || !strings.Contains(message.HTML, brand.WebsiteURL) || !strings.Contains(message.Text, brand.WebsiteURL) {
			t.Fatalf("welcome message for %s lost its identity: %+v", locale, message)
		}
		for _, step := range copy.Steps {
			if !strings.Contains(message.Text, step.Body) {
				t.Fatalf("welcome text for %s dropped a step: %q", locale, step.Title)
			}
		}
	}
}

// A welcome mail is worth sending in the wrong language, never worth dropping.
func TestWelcomeFallsBackToDefaultLocale(t *testing.T) {
	message, err := RenderWelcome(i18n.Locale("xx"))
	if err != nil {
		t.Fatal(err)
	}
	if message.Subject != welcomeTemplates[i18n.DefaultLocale].Subject {
		t.Fatalf("unexpected fallback subject %q", message.Subject)
	}
}

func TestWorkerRendersBothTemplatesAndRejectsUnknownOnes(t *testing.T) {
	worker := &Worker{from: brand.DefaultEmailFrom}
	message, err := worker.render(job{Template: "welcome", Recipient: "new@test", Locale: i18n.LocaleEN})
	if err != nil || message.From != brand.DefaultEmailFrom || message.To != "new@test" {
		t.Fatalf("welcome job: message=%+v err=%v", message, err)
	}
	if _, err = worker.render(job{Template: "password_reset", Recipient: "new@test", Locale: i18n.LocaleEN}); err == nil {
		t.Fatal("unknown templates must fail permanently rather than send")
	}
}
