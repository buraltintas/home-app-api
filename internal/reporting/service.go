package reporting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db       *pgxpool.Pool
	location *time.Location
	now      func() time.Time
}

func NewService(db *pgxpool.Pool, timezone string) (*Service, error) {
	loc, e := time.LoadLocation(timezone)
	if e != nil {
		return nil, e
	}
	return &Service{db, loc, time.Now}, nil
}

func (s *Service) Record(ctx context.Context, e Event) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	changed, err := s.RecordTx(ctx, tx, e)
	if err != nil {
		return false, err
	}
	return changed, tx.Commit(ctx)
}
func (s *Service) RecordTx(ctx context.Context, tx pgx.Tx, e Event) (bool, error) {
	if e.Type == "" {
		return false, fmt.Errorf("event type is required")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now()
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO platform_events(event_type,idempotency_key,user_id,visitor_session_id,store_id,post_id,search_id,metadata,created_at) VALUES($1,nullif($2,''),$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(idempotency_key) DO NOTHING RETURNING id::text`, e.Type, e.IdempotencyKey, e.UserID, e.VisitorSessionID, e.StoreID, e.PostID, e.SearchID, metadata(e.Metadata), e.CreatedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	d := snapshotDelta(e.Type)
	if d == nil {
		return true, nil
	}
	_, err = tx.Exec(ctx, `UPDATE platform_stats SET registered_users_total=greatest(0,registered_users_total+$1),stores_total=greatest(0,stores_total+$2),google_imported_stores_total=greatest(0,google_imported_stores_total+$3),posts_current_total=greatest(0,posts_current_total+$4),posts_created_lifetime=greatest(0,posts_created_lifetime+$5),posts_deleted_lifetime=greatest(0,posts_deleted_lifetime+$6),comments_current_total=greatest(0,comments_current_total+$7),likes_current_total=greatest(0,likes_current_total+$8),follows_current_total=greatest(0,follows_current_total+$9),favorites_current_total=greatest(0,favorites_current_total+$10),searches_lifetime=greatest(0,searches_lifetime+$11),media_current_total=greatest(0,media_current_total+$12),updated_at=now() WHERE id=1`, d...)
	return true, err
}
func snapshotDelta(t string) []any {
	d := make([]any, 12)
	for i := range d {
		d[i] = 0
	}
	set := func(i, v int) { d[i] = v }
	switch t {
	case UserRegistered:
		set(0, 1)
	case StoreCreated:
		set(1, 1)
	case StoreImportedGoogle:
		set(1, 1)
		set(2, 1)
	case PostCreated:
		set(3, 1)
		set(4, 1)
	case PostDeleted:
		set(3, -1)
		set(5, 1)
	case CommentCreated:
		set(6, 1)
	case CommentDeleted:
		set(6, -1)
	case LikeCreated:
		set(7, 1)
	case LikeRemoved:
		set(7, -1)
	case FollowCreated:
		set(8, 1)
	case FollowRemoved:
		set(8, -1)
	case FavoriteCreated:
		set(9, 1)
	case FavoriteRemoved:
		set(9, -1)
	case SearchPerformed:
		set(10, 1)
	case MediaCreated:
		set(11, 1)
	case MediaDeleted:
		set(11, -1)
	default:
		return nil
	}
	return d
}

func (s *Service) RebuildSnapshot(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE platform_stats SET registered_users_total=(SELECT count(*) FROM users WHERE deleted_at IS NULL),stores_total=(SELECT count(*) FROM stores WHERE deleted_at IS NULL),google_imported_stores_total=(SELECT count(DISTINCT s.store_id) FROM store_external_sources s JOIN stores st ON st.id=s.store_id AND st.deleted_at IS NULL WHERE s.provider='google'),posts_current_total=(SELECT count(*) FROM posts WHERE deleted_at IS NULL),posts_created_lifetime=(SELECT count(*) FROM posts),posts_deleted_lifetime=(SELECT count(*) FROM posts WHERE deleted_at IS NOT NULL),comments_current_total=(SELECT count(*) FROM comments WHERE deleted_at IS NULL),likes_current_total=(SELECT count(*) FROM likes),follows_current_total=(SELECT count(*) FROM follows),favorites_current_total=(SELECT count(*) FROM favorites),searches_lifetime=(SELECT count(*) FROM searches),media_current_total=(SELECT count(*) FROM media WHERE status<>'deleted'),updated_at=now() WHERE id=1`)
	return e
}
