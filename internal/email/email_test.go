package email

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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

func TestResendSenderReturnsProviderMessageID(t *testing.T) {
	s := &ResendSender{URL: "https://email.test", APIKey: "secret", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"mail-123"}`)), Header: make(http.Header)}, nil
	})}}
	id, err := s.Send(context.Background(), Message{From: "a@test", To: "b@test"})
	if err != nil || id != "mail-123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}
