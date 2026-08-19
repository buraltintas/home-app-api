// Package feedback carries what people tell us about the product itself. It is deliberately
// separate from posts and comments: those are about a store and are public, this is a
// private message to us and is never shown back to anybody but an administrator.
package feedback

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db} }

var kinds = map[string]bool{"suggestion": true, "problem": true, "praise": true, "other": true}

type Input struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	ContactEmail string `json:"contact_email"`
}

// Create records one message. Anonymous visitors may send feedback: browsing does not need
// an account, and neither should telling us the product is wrong. The address is optional
// and exists only so we can answer.
func (s *Service) Create(ctx context.Context, in Input, user *uuid.UUID, visitor *uuid.UUID) error {
	kind, message, email, e := validate(in)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO feedback(user_id,visitor_session_id,kind,message,contact_email,locale)
 VALUES($1,$2,$3,$4,$5,$6)`, user, visitor, kind, message, email, string(i18n.FromContext(ctx)))
	return e
}

// validate is separated from the write so the rules can be tested without a database. The
// bounds match the table's own constraints; a mismatch there would surface as a 500 rather
// than as the explanation the sender deserves.
func validate(in Input) (kind, message string, email *string, err error) {
	kind = strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "other"
	}
	if !kinds[kind] {
		return "", "", nil, httpapi.ErrInvalidInput
	}
	message = strings.TrimSpace(in.Message)
	if n := utf8.RuneCountInString(message); n < 5 || n > 4000 {
		return "", "", nil, httpapi.E(422, "INVALID_FEEDBACK", "Feedback must be between 5 and 4000 characters")
	}
	if trimmed := strings.TrimSpace(in.ContactEmail); trimmed != "" {
		if !strings.Contains(trimmed, "@") || utf8.RuneCountInString(trimmed) > 320 {
			return "", "", nil, httpapi.E(422, "INVALID_EMAIL", "Contact address is not a valid email")
		}
		email = &trimmed
	}
	return kind, message, email, nil
}
