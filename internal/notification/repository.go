package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db  *pgxpool.Pool
	key []byte
}

func NewRepository(db *pgxpool.Pool, key []byte) *Repository {
	return &Repository{db: db, key: append([]byte(nil), key...)}
}

func (r *Repository) RegisterDevice(ctx context.Context, user uuid.UUID, platform, token string) (uuid.UUID, error) {
	return r.RegisterDeviceLocale(ctx, user, platform, token, i18n.FromContext(ctx))
}

func (r *Repository) RegisterDeviceLocale(ctx context.Context, user uuid.UUID, platform, token string, locale i18n.Locale) (uuid.UUID, error) {
	platform, token = strings.TrimSpace(platform), strings.TrimSpace(token)
	if user == uuid.Nil || !i18n.IsSupported(locale) || (platform != "ios" && platform != "android" && platform != "web") || token == "" || len(token) > 4096 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	id := uuid.New()
	err := r.db.QueryRow(ctx, `INSERT INTO push_devices(id,user_id,platform,token_hash,locale) VALUES($1,$2,$3,$4,$5) ON CONFLICT(token_hash) DO UPDATE SET user_id=excluded.user_id,platform=excluded.platform,locale=excluded.locale,disabled_at=NULL RETURNING id`, id, user, platform, security.Hash(r.key, token), locale).Scan(&id)
	return id, err
}

func (r *Repository) DeleteDevice(ctx context.Context, user uuid.UUID, token string) (bool, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM push_devices WHERE user_id=$1 AND token_hash=$2`, user, security.Hash(r.key, strings.TrimSpace(token)))
	return tag.RowsAffected() == 1, err
}

func (r *Repository) SetPreferences(ctx context.Context, user uuid.UUID, preferences map[string]bool) error {
	raw, err := json.Marshal(preferences)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO notification_preferences(user_id,preferences) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET preferences=excluded.preferences,updated_at=now()`, user, raw)
	return err
}

func (r *Repository) Enqueue(ctx context.Context, user uuid.UUID, event, key string, payload map[string]any) (bool, error) {
	return r.EnqueueLocalized(ctx, user, event, event, key, i18n.FromContext(ctx), payload)
}

func (r *Repository) EnqueueLocalized(ctx context.Context, user uuid.UUID, event, templateKey, key string, locale i18n.Locale, payload map[string]any) (bool, error) {
	if user == uuid.Nil || strings.TrimSpace(event) == "" || strings.TrimSpace(templateKey) == "" || !i18n.IsSupported(locale) {
		return false, httpapi.ErrInvalidInput
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO notification_outbox(user_id,event_type,template_key,template_params,idempotency_key,payload,locale) VALUES($1,$2,$3,$4,nullif($5,''),$4,$6) ON CONFLICT(idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`, user, event, templateKey, raw, key, locale)
	return tag.RowsAffected() == 1, err
}

type Job struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Event       string
	Payload     map[string]any
	Attempts    int
	Locale      i18n.Locale
	TemplateKey string
}

func (r *Repository) Claim(ctx context.Context) (Job, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback(ctx)
	var job Job
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT id,user_id,event_type,payload,attempts,locale::text,template_key FROM notification_outbox WHERE ((status IN ('pending','failed') AND available_at<=now()) OR (status='processing' AND locked_at<now()-interval '5 minutes')) ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&job.ID, &job.UserID, &job.Event, &raw, &job.Attempts, &job.Locale, &job.TemplateKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE notification_outbox SET status='processing',locked_at=now(),attempts=attempts+1 WHERE id=$1`, job.ID); err != nil {
		return Job{}, false, err
	}
	if err = json.Unmarshal(raw, &job.Payload); err != nil {
		return Job{}, false, err
	}
	return job, true, tx.Commit(ctx)
}

func (r *Repository) Complete(ctx context.Context, id uuid.UUID, providerID string) error {
	_, err := r.db.Exec(ctx, `UPDATE notification_outbox SET status='sent',provider_message_id=$2,last_error=NULL,locked_at=NULL WHERE id=$1 AND status='processing'`, id, providerID)
	return err
}

func (r *Repository) Fail(ctx context.Context, id uuid.UUID, cause string, retryAfter time.Duration) error {
	if retryAfter < 0 {
		retryAfter = 0
	}
	_, err := r.db.Exec(ctx, `UPDATE notification_outbox SET status='failed',last_error=$2,locked_at=NULL,available_at=now()+$3::interval WHERE id=$1 AND status='processing'`, id, cause, retryAfter.String())
	return err
}
