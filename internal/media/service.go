package media

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db       *pgxpool.Pool
	storage  ObjectStorage
	maxBytes int64
	report   *reporting.Service
}

func NewService(db *pgxpool.Pool, storage ObjectStorage, max int64, report *reporting.Service) *Service {
	return &Service{db, storage, max, report}
}

type CreateRequest struct {
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
}
type CreateResponse struct {
	ID     uuid.UUID `json:"id"`
	Upload Upload    `json:"upload"`
}

func (s *Service) Create(ctx context.Context, user uuid.UUID, in CreateRequest) (CreateResponse, error) {
	exts := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
	ext, ok := exts[in.MimeType]
	if !ok || in.SizeBytes < 1 || in.SizeBytes > s.maxBytes {
		return CreateResponse{}, httpapi.ErrInvalidInput
	}
	id := uuid.New()
	key := filepath.ToSlash("users/" + user.String() + "/" + id.String() + ext)
	upload, e := s.storage.CreateUpload(ctx, key, in.MimeType, in.SizeBytes)
	if e != nil {
		return CreateResponse{}, e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO media(id,owner_user_id,storage_key,mime_type,size_bytes) VALUES($1,$2,$3,$4,$5)`, id, user, key, in.MimeType, in.SizeBytes)
	return CreateResponse{id, upload}, e
}
func (s *Service) Complete(ctx context.Context, user, id uuid.UUID, width, height int) error {
	if width < 1 || height < 1 || width > 20000 || height > 20000 {
		return httpapi.ErrInvalidInput
	}
	var key, mime string
	var size int64
	e := s.db.QueryRow(ctx, `SELECT storage_key,mime_type,size_bytes FROM media WHERE id=$1 AND owner_user_id=$2 AND status='pending'`, id, user).Scan(&key, &mime, &size)
	if errors.Is(e, pgx.ErrNoRows) {
		return httpapi.E(404, "MEDIA_NOT_FOUND", "Media not found")
	}
	if e != nil {
		return e
	}
	info, e := s.storage.Stat(ctx, key)
	if e != nil {
		return httpapi.E(422, "MEDIA_UPLOAD_INCOMPLETE", "Media upload is not complete")
	}
	if info.Size != size || !strings.EqualFold(info.ContentType, mime) {
		_ = s.storage.Delete(ctx, key)
		return httpapi.E(422, "MEDIA_UPLOAD_MISMATCH", "Uploaded media does not match its declaration")
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE media SET status='ready',width=$3,height=$4 WHERE id=$1 AND owner_user_id=$2 AND status='pending'`, id, user, width, height)
	if e != nil || tag.RowsAffected() != 1 {
		return httpapi.E(409, "MEDIA_STATE_CONFLICT", "Media state changed")
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.MediaCreated, IdempotencyKey: "media-ready:" + id.String(), UserID: &user, Metadata: map[string]any{"media_id": id.String()}}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
