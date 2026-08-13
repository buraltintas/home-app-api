package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string                  { return e.Code }
func E(status int, code, message string) *Error { return &Error{status, code, message} }

var (
	ErrAuthRequired  = E(http.StatusUnauthorized, "AUTH_REQUIRED", "Authentication is required")
	ErrInvalidToken  = E(http.StatusUnauthorized, "INVALID_TOKEN", "Invalid or expired access token")
	ErrInvalidClient = E(http.StatusUnauthorized, "INVALID_CLIENT", "Invalid client credentials")
	ErrInvalidInput  = E(http.StatusBadRequest, "INVALID_INPUT", "Request validation failed")
)

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, err error, contexts ...context.Context) {
	var app *Error
	if !errors.As(err, &app) {
		app = E(500, "INTERNAL_ERROR", "An unexpected error occurred")
	}
	ctx := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	message := i18n.Translate(i18n.FromContext(ctx), app.Code)
	JSON(w, app.Status, map[string]any{"error": map[string]string{"code": app.Code, "message": message}, "request_id": w.Header().Get("X-Request-ID")})
}

func Decode(w http.ResponseWriter, r *http.Request, dst any, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrInvalidInput
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidInput
	}
	return nil
}
