// Package admin backs the operator surface. Its SQL lives here rather than in the product
// services on purpose: these queries answer "what happened" across the whole database, and
// mixing them into the services that serve visitors would blur which queries are reachable
// without an administrator.
package admin

import (
	"context"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db} }

// clamp keeps a page size sane whatever the caller asks for. Admin pages are read by a
// person, and an unbounded limit here would be a way to pull the whole database in one
// request.
func clamp(limit int) int {
	if limit < 1 || limit > 200 {
		return 50
	}
	return limit
}

type UserRow struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	ReviewCount int        `json:"review_count"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

func (s *Service) Users(ctx context.Context, query string, limit, offset int) ([]UserRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, e := s.db.Query(ctx, `SELECT u.id,u.primary_email::text,coalesce(p.username::text,''),coalesce(p.display_name,''),u.status,
 (SELECT count(*) FROM posts po WHERE po.user_id=u.id AND po.deleted_at IS NULL),u.created_at,u.deleted_at
 FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id
 WHERE ($1='' OR lower(u.primary_email::text) LIKE '%'||$1||'%' OR lower(coalesce(p.username::text,'')) LIKE '%'||$1||'%')
 ORDER BY u.created_at DESC LIMIT $2 OFFSET $3`, query, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var x UserRow
		if e = rows.Scan(&x.ID, &x.Email, &x.Username, &x.DisplayName, &x.Status, &x.ReviewCount, &x.CreatedAt, &x.DeletedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type StoreRow struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	City        string    `json:"city"`
	IsPremium   bool      `json:"is_premium"`
	ReviewCount int       `json:"review_count"`
	Rating      float64   `json:"average_rating"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Service) Stores(ctx context.Context, query string, premiumOnly bool, limit, offset int) ([]StoreRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, e := s.db.Query(ctx, `SELECT s.id,s.name,s.slug,s.city,s.is_premium,ss.review_count,ss.average_rating,s.created_at
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id
 WHERE s.deleted_at IS NULL AND (NOT $1 OR s.is_premium)
 AND ($2='' OR lower(s.name) LIKE '%'||$2||'%' OR lower(s.city) LIKE '%'||$2||'%')
 ORDER BY s.is_premium DESC, ss.review_count DESC, s.created_at DESC LIMIT $3 OFFSET $4`, premiumOnly, query, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []StoreRow{}
	for rows.Next() {
		var x StoreRow
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.City, &x.IsPremium, &x.ReviewCount, &x.Rating, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type ReviewRow struct {
	ID        uuid.UUID `json:"id"`
	StoreID   uuid.UUID `json:"store_id"`
	StoreName string    `json:"store_name"`
	UserID    uuid.UUID `json:"user_id"`
	Author    string    `json:"author"`
	Rating    int       `json:"rating"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	Deleted   bool      `json:"deleted"`
}

func (s *Service) Reviews(ctx context.Context, query string, limit, offset int) ([]ReviewRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, e := s.db.Query(ctx, `SELECT p.id,p.store_id,st.name,p.user_id,coalesce(up.display_name,''),p.rating,p.body,p.created_at,p.deleted_at IS NOT NULL
 FROM posts p JOIN stores st ON st.id=p.store_id LEFT JOIN user_profiles up ON up.user_id=p.user_id
 WHERE ($1='' OR lower(st.name) LIKE '%'||$1||'%' OR lower(coalesce(up.display_name,'')) LIKE '%'||$1||'%')
 ORDER BY p.created_at DESC LIMIT $2 OFFSET $3`, query, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ReviewRow{}
	for rows.Next() {
		var x ReviewRow
		if e = rows.Scan(&x.ID, &x.StoreID, &x.StoreName, &x.UserID, &x.Author, &x.Rating, &x.Text, &x.CreatedAt, &x.Deleted); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type SearchRow struct {
	ID          uuid.UUID  `json:"id"`
	Query       string     `json:"query"`
	UserID      *uuid.UUID `json:"user_id,omitempty"`
	Locale      string     `json:"query_language"`
	Scope       string     `json:"scope"`
	ResultCount int        `json:"result_count"`
	ClickCount  int        `json:"click_count"`
	DurationMS  *int       `json:"duration_ms,omitempty"`
	Fallback    *string    `json:"fallback_state,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Searches is the raw log the operator asked to be able to read: what was typed, how many
// results came back, and whether anything was opened afterwards.
func (s *Service) Searches(ctx context.Context, query string, limit, offset int) ([]SearchRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, e := s.db.Query(ctx, `SELECT s.id,s.raw_query,s.user_id,coalesce(s.query_language::text,''),coalesce(s.parsed_intent->>'scope',''),
 s.total_result_count,(SELECT count(*) FROM search_interactions i WHERE i.search_id=s.id AND i.event_type IN ('click','store_open')),
 s.duration_ms,s.fallback_state,s.created_at
 FROM searches s WHERE ($1='' OR lower(s.raw_query) LIKE '%'||$1||'%')
 ORDER BY s.created_at DESC LIMIT $2 OFFSET $3`, query, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SearchRow{}
	for rows.Next() {
		var x SearchRow
		if e = rows.Scan(&x.ID, &x.Query, &x.UserID, &x.Locale, &x.Scope, &x.ResultCount, &x.ClickCount, &x.DurationMS, &x.Fallback, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type SearchResultRow struct {
	Rank     int        `json:"rank"`
	StoreID  *uuid.UUID `json:"store_id,omitempty"`
	Name     string     `json:"name"`
	Source   string     `json:"source"`
	Distance *int       `json:"distance_meters,omitempty"`
	Score    *float64   `json:"ranking_score,omitempty"`
}

func (s *Service) SearchResults(ctx context.Context, searchID uuid.UUID) ([]SearchResultRow, error) {
	rows, e := s.db.Query(ctx, `SELECT r.rank,r.store_id,coalesce(st.name,''),r.source,r.distance_meters,r.ranking_score
 FROM search_results r LEFT JOIN stores st ON st.id=r.store_id WHERE r.search_id=$1 ORDER BY r.rank`, searchID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SearchResultRow{}
	for rows.Next() {
		var x SearchResultRow
		if e = rows.Scan(&x.Rank, &x.StoreID, &x.Name, &x.Source, &x.Distance, &x.Score); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type AuditRow struct {
	ID         uuid.UUID      `json:"id"`
	ActorEmail string         `json:"actor_email"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   uuid.UUID      `json:"target_id"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (s *Service) Audit(ctx context.Context, limit, offset int) ([]AuditRow, error) {
	rows, e := s.db.Query(ctx, `SELECT id,actor_email::text,action,target_type,target_id,metadata,created_at
 FROM admin_actions ORDER BY created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var x AuditRow
		if e = rows.Scan(&x.ID, &x.ActorEmail, &x.Action, &x.TargetType, &x.TargetID, &x.Metadata, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// record writes the audit row inside the caller's transaction, so a change and the record
// of it either both land or neither does.
func record(ctx context.Context, tx pgx.Tx, actor uuid.UUID, email, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	_, e := tx.Exec(ctx, `INSERT INTO admin_actions(actor_user_id,actor_email,action,target_type,target_id,metadata) VALUES($1,$2,$3,$4,$5,$6)`,
		actor, email, action, targetType, target, metadata)
	return e
}

func (s *Service) SetStorePremium(ctx context.Context, actor uuid.UUID, email string, store uuid.UUID, premium bool) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE stores SET is_premium=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, store, premium)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	if e = record(ctx, tx, actor, email, "store.premium", "store", store, map[string]any{"is_premium": premium}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// SetUserStatus suspends or reactivates an account. Suspension is the proportionate step
// that deletion is not: the account cannot sign in, and nothing the person wrote is lost.
func (s *Service) SetUserStatus(ctx context.Context, actor uuid.UUID, email string, target uuid.UUID, status string) error {
	if status != "active" && status != "suspended" {
		return httpapi.ErrInvalidInput
	}
	if actor == target {
		return httpapi.E(422, "CANNOT_ACT_ON_SELF", "An administrator cannot change their own access")
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE users SET status=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL AND status<>'deleted'`, target, status)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(404, "USER_NOT_FOUND", "User not found")
	}
	if status == "suspended" {
		// Revoke live sessions, or suspension would not take effect until the access
		// token happened to expire.
		if _, e = tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=coalesce(revoked_at,now()),revoke_reason='admin_suspended' WHERE user_id=$1 AND revoked_at IS NULL`, target); e != nil {
			return e
		}
	}
	if e = record(ctx, tx, actor, email, "user.status", "user", target, map[string]any{"status": status}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// DeleteReview soft-deletes a review and recomputes the store's aggregates, matching what
// happens when an author deletes their own.
func (s *Service) DeleteReview(ctx context.Context, actor uuid.UUID, email string, post uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var store uuid.UUID
	e = tx.QueryRow(ctx, `UPDATE posts SET body='',content_language=NULL,deleted_at=now(),updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING store_id`, post).Scan(&store)
	if e == pgx.ErrNoRows {
		return httpapi.E(404, "POST_NOT_FOUND", "Post not found")
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE store_stats ss SET rating_count=x.n,review_count=x.n,post_count=x.n,average_rating=x.avg,updated_at=now()
 FROM(SELECT count(*)::int n,coalesce(avg(rating),0) avg FROM posts WHERE store_id=$1 AND deleted_at IS NULL) x WHERE ss.store_id=$1`, store); e != nil {
		return e
	}
	if e = record(ctx, tx, actor, email, "post.delete", "post", post, map[string]any{"store_id": store.String()}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// RecordUserDeletion notes an administrator-initiated account deletion. The deletion itself
// runs through the ordinary user service, so administrator and self-service deletion cannot
// drift apart and the published account-deletion page stays accurate for both.
func (s *Service) RecordUserDeletion(ctx context.Context, actor uuid.UUID, email string, target uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = record(ctx, tx, actor, email, "user.delete", "user", target, nil); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
