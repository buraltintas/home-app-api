// Package feedback carries what people tell us about the product itself. It is deliberately
// separate from posts and comments: those are about a store and are public, this is a
// private message to us and is never shown back to anybody but an administrator.
package feedback

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db     *pgxpool.Pool
	notify string
}

// notify is where a new message is sent. Empty disables the mail and keeps the panel as
// the only place feedback lands, which is what a local run wants.
func NewService(db *pgxpool.Pool, notify string) *Service {
	return &Service{db: db, notify: strings.TrimSpace(notify)}
}

var kinds = map[string]bool{"suggestion": true, "problem": true, "praise": true, "other": true}

type Input struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	ContactEmail string `json:"contact_email"`
}

type Message struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"`
	Message   string     `json:"message"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	Reply     string     `json:"reply,omitempty"`
	RepliedAt *time.Time `json:"replied_at,omitempty"`
}

// Messages returns only feedback written while this account was signed in. Matching by
// contact address would expose anonymous messages to a later account that happens to use
// the same address; user_id is the ownership boundary.
func (s *Service) Messages(ctx context.Context, user uuid.UUID, limit, offset int) ([]Message, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, e := s.db.Query(ctx, `SELECT id,kind,message,status,created_at,coalesce(reply,''),replied_at
 FROM feedback WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, user, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []Message{}
	for rows.Next() {
		var item Message
		if e = rows.Scan(&item.ID, &item.Kind, &item.Message, &item.Status, &item.CreatedAt, &item.Reply, &item.RepliedAt); e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Create records one message. Anonymous visitors may send feedback: browsing does not need
// an account, and neither should telling us the product is wrong. The address is optional
// and exists only so we can answer.
func (s *Service) Create(ctx context.Context, in Input, user *uuid.UUID, visitor *uuid.UUID) error {
	kind, message, email, e := validate(in)
	if e != nil {
		return e
	}
	locale := string(i18n.FromContext(ctx))
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if e = tx.QueryRow(ctx, `INSERT INTO feedback(user_id,visitor_session_id,kind,message,contact_email,locale)
 VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, user, visitor, kind, message, email, locale).Scan(&id); e != nil {
		return e
	}
	// The notice is queued in the same transaction as the message it announces: either both
	// exist or neither does. A feedback row nobody is told about is the failure this avoids,
	// and it is the failure a separate send after the commit would produce on any error.
	if s.notify != "" {
		author := ""
		if user != nil {
			_ = tx.QueryRow(ctx, `SELECT coalesce(display_name,username,'') FROM user_profiles WHERE user_id=$1`, *user).Scan(&author)
		}
		contact := ""
		if email != nil {
			contact = *email
		}
		payload, _ := json.Marshal(map[string]string{
			"kind": kind, "message": message, "contact_email": contact, "author": author, "locale": locale,
		})
		if _, e = tx.Exec(ctx, `INSERT INTO email_outbox(idempotency_key,template,recipient,payload,locale)
 VALUES($1,'feedback',$2,$3,'tr') ON CONFLICT(idempotency_key) DO NOTHING`, "feedback:"+id.String(), s.notify, payload); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
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
