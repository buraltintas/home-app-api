package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResendSenderClassifiesProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		status    int
		retryable bool
	}{{400, false}, {401, false}, {429, true}, {503, true}} {
		s := &ResendSender{URL: "https://email.test", APIKey: "secret", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Fatal("missing provider authorization")
			}
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		})}}
		_, err := s.Send(context.Background(), Message{From: "a@test", To: "b@test"})
		if err == nil || retryable(err) != tc.retryable {
			t.Fatalf("status %d: err=%v retryable=%v", tc.status, err, retryable(err))
		}
	}
}

func TestLoginCodeTemplatesCoverSupportedLocales(t *testing.T) {
	for _, locale := range i18n.Supported() {
		tpl, ok := loginCodeTemplates[locale]
		if !ok || tpl.Subject == "" || tpl.HTML == "" || tpl.Text == "" {
			t.Fatalf("incomplete login_code template for %s", locale)
		}
		message, err := RenderLoginCode(locale, "123456", 10)
		if err != nil || !strings.Contains(message.HTML, "123456") || !strings.Contains(message.Text, "123456") || !strings.Contains(message.Subject, brand.ProductName) || !strings.Contains(message.HTML, brand.ProductName) || !strings.Contains(message.Text, brand.ProductName) {
			t.Fatalf("render %s: message=%+v err=%v", locale, message, err)
		}
	}
}

func TestLoginCodeTemplateFallsBackToDefaultLocale(t *testing.T) {
	want, err := RenderLoginCode(i18n.DefaultLocale, "123456", 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderLoginCode(i18n.Locale("fr"), "123456", 10)
	if err != nil || got != want {
		t.Fatalf("fallback got=%+v want=%+v err=%v", got, want, err)
	}
}

func TestResendSenderReturnsProviderMessageID(t *testing.T) {
	s := &ResendSender{URL: "https://email.test", APIKey: "secret", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"mail-123"}`)), Header: make(http.Header)}, nil
	})}}
	id, err := s.Send(context.Background(), Message{From: "a@test", To: "b@test"})
	if err != nil || id != "mail-123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestGmailSenderBuildsMIMEAndReturnsProviderMessageID(t *testing.T) {
	sender := &GmailSender{URL: "https://gmail.test/gmail/v1/users/me/messages/send", Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/gmail/v1/users/me/messages/send" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %s headers=%v", request.Method, request.URL.Path, request.Header)
		}
		var payload struct {
			Raw string `json:"raw"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(payload.Raw)
		if err != nil {
			t.Fatal(err)
		}
		message := string(raw)
		for _, expected := range []string{"no-reply@bosagezme.com", "user@example.com", "multipart/alternative", "123456"} {
			if !strings.Contains(message, expected) {
				t.Fatalf("MIME message missing %q: %s", expected, message)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"gmail-message-123"}`)), Header: make(http.Header)}, nil
	})}}
	id, err := sender.Send(context.Background(), Message{From: "Boşa Gezme! <no-reply@bosagezme.com>", To: "user@example.com", Subject: "Giriş kodunuz", Text: "Kod: 123456", HTML: "<strong>123456</strong>"})
	if err != nil || id != "gmail-message-123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestGmailSenderClassifiesQuotaAndConfigurationFailures(t *testing.T) {
	for _, test := range []struct {
		status    int
		body      string
		retryable bool
	}{{403, `{"error":{"errors":[{"reason":"userRateLimitExceeded"}]}}`, true}, {403, `{"error":{"errors":[{"reason":"forbidden"}]}}`, false}, {429, `{}`, true}, {500, `{}`, true}, {401, `{}`, false}} {
		sender := &GmailSender{URL: "https://gmail.test/send", Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
		})}}
		_, err := sender.Send(context.Background(), Message{From: "no-reply@bosagezme.com", To: "user@example.com", Subject: "OTP", Text: "123456", HTML: "123456"})
		if err == nil || retryable(err) != test.retryable {
			t.Fatalf("status=%d err=%v retryable=%v", test.status, err, retryable(err))
		}
	}
}

func TestGmailSenderRejectsHeaderInjection(t *testing.T) {
	if _, err := gmailRawMessage(Message{From: "no-reply@bosagezme.com", To: "user@example.com", Subject: "OTP\r\nBcc: attacker@example.com"}); err == nil {
		t.Fatal("subject header injection accepted")
	}
}
