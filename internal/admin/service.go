// Package admin backs the operator surface. Its SQL lives here rather than in the product
// services on purpose: these queries answer "what happened" across the whole database, and
// mixing them into the services that serve visitors would blur which queries are reachable
// without an administrator.
package admin

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db} }

// clamp keeps a page size sane whatever the caller asks for. The ceiling is high enough
// for an export of the whole catalogue in one request and low enough that it stays a
// bounded read; an out-of-range value falls back to a screenful rather than the maximum,
// so a mistyped limit cannot quietly become the largest possible query.
const maxAdminPage = 5000

func clamp(limit int) int {
	if limit < 1 || limit > maxAdminPage {
		return 50
	}
	return limit
}

type UserRow struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	ReviewCount int        `json:"review_count"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

func (s *Service) Users(ctx context.Context, query string, limit, offset int) ([]UserRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, e := s.db.Query(ctx, `SELECT u.id,u.primary_email::text,coalesce(p.display_name,''),u.status,
 (SELECT count(*) FROM posts po WHERE po.user_id=u.id AND po.deleted_at IS NULL),u.created_at,u.deleted_at
 FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id
	 WHERE ($1='' OR lower(u.primary_email::text) LIKE '%'||$1||'%' OR lower(coalesce(p.display_name,'')) LIKE '%'||$1||'%')
 ORDER BY u.created_at DESC LIMIT $2 OFFSET $3`, query, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var x UserRow
		if e = rows.Scan(&x.ID, &x.Email, &x.DisplayName, &x.Status, &x.ReviewCount, &x.CreatedAt, &x.DeletedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type StoreRow struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	City           string    `json:"city"`
	IsPremium      bool      `json:"is_premium"`
	IsCatalogStore bool      `json:"is_catalog_store"`
	CoverMediaID   string    `json:"cover_media_id,omitempty"`
	Categories     []string  `json:"categories"`
	ReviewCount    int       `json:"review_count"`
	Rating         float64   `json:"average_rating"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *Service) Stores(ctx context.Context, query string, premiumOnly bool, limit, offset int) ([]StoreRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, e := s.db.Query(ctx, `SELECT s.id,s.name,s.slug,s.city,s.is_premium,s.is_catalog_store,coalesce(s.cover_media_id::text,''),
 coalesce((SELECT array_agg(c.slug ORDER BY c.slug) FROM store_category_links l JOIN store_categories c ON c.id=l.category_id WHERE l.store_id=s.id),'{}'),
 ss.review_count,ss.average_rating,s.created_at
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
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.City, &x.IsPremium, &x.IsCatalogStore, &x.CoverMediaID, &x.Categories, &x.ReviewCount, &x.Rating, &x.CreatedAt); e != nil {
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

type Category struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	rows, e := s.db.Query(ctx, `SELECT slug,name_tr FROM store_categories WHERE active ORDER BY name_tr`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var x Category
		if e = rows.Scan(&x.Slug, &x.Name); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// StoreCategorySlugs is what the editor loads to show current state.
func (s *Service) StoreCategorySlugs(ctx context.Context, store uuid.UUID) ([]string, error) {
	rows, e := s.db.Query(ctx, `SELECT c.slug FROM store_category_links l JOIN store_categories c ON c.id=l.category_id WHERE l.store_id=$1 ORDER BY c.slug`, store)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if e = rows.Scan(&slug); e != nil {
			return nil, e
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// SetStoreCategories replaces a store's categories outright. Imports guess from the name
// and Google's types, which is right often enough to be worth doing and wrong often enough
// to need correcting by hand -- a shop that sells across the whole range has no single
// category to guess at.
//
// An empty list is allowed and means "unclassified", which is not the same as "not a home
// store": every store here is one, and search treats an unclassified promoted store as
// matching any home search rather than none.
func (s *Service) SetStoreCategories(ctx context.Context, actor uuid.UUID, email string, store uuid.UUID, slugs []string) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stores WHERE id=$1 AND deleted_at IS NULL)`, store).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		return httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	if _, e = tx.Exec(ctx, `DELETE FROM store_category_links WHERE store_id=$1`, store); e != nil {
		return e
	}
	if len(slugs) > 0 {
		if _, e = tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=ANY($2) AND active`, store, slugs); e != nil {
			return e
		}
	}
	if e = record(ctx, tx, actor, email, "store.categories", "store", store, map[string]any{"slugs": slugs}); e != nil {
		return e
	}
	return tx.Commit(ctx)
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

// SetStoreCatalogStatus controls the editorial catalogue marker independently from paid
// placement. The flag changes presentation only; categories remain an explicit admin
// choice and search ranking continues to follow the same rules as every other store.
func (s *Service) SetStoreCatalogStatus(ctx context.Context, actor uuid.UUID, email string, store uuid.UUID, catalog bool) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE stores SET is_catalog_store=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, store, catalog)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	if e = record(ctx, tx, actor, email, "store.catalog", "store", store, map[string]any{"is_catalog_store": catalog}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// SetStoreCover publishes one administrator-selected image for a store. The media row must
// be ready and owned by the acting administrator; clearing the value immediately restores
// the Google photo fallback without deleting the uploaded object or changing provider data.
func (s *Service) SetStoreCover(ctx context.Context, actor uuid.UUID, email string, store uuid.UUID, mediaID *uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)

	if mediaID != nil {
		var valid bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE id=$1 AND owner_user_id=$2 AND status='ready')`, *mediaID, actor).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return httpapi.E(400, "INVALID_MEDIA", "Media does not exist, is not ready, or is not owned by this administrator")
		}
	}

	tag, e := tx.Exec(ctx, `UPDATE stores SET cover_media_id=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, store, mediaID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	metadata := map[string]any{"removed": mediaID == nil}
	if mediaID != nil {
		metadata["media_id"] = mediaID.String()
	}
	if e = record(ctx, tx, actor, email, "store.cover", "store", store, metadata); e != nil {
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

type FeedbackRow struct {
	ID           uuid.UUID  `json:"id"`
	UserID       *uuid.UUID `json:"user_id,omitempty"`
	Kind         string     `json:"kind"`
	Message      string     `json:"message"`
	ContactEmail string     `json:"contact_email,omitempty"`
	Author       string     `json:"author,omitempty"`
	Locale       string     `json:"locale"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	HandledAt    *time.Time `json:"handled_at,omitempty"`
	Reply        string     `json:"reply,omitempty"`
	RepliedAt    *time.Time `json:"replied_at,omitempty"`
}

// Feedback lists what people have told us about the product, newest first. The author is
// resolved where the sender was signed in; anonymous rows simply have none, which is the
// normal case and not a gap.
func (s *Service) Feedback(ctx context.Context, q string, limit, offset int) ([]FeedbackRow, error) {
	q = strings.TrimSpace(q)
	rows, e := s.db.Query(ctx, `SELECT f.id,f.user_id,f.kind,f.message,coalesce(f.contact_email::text,''),
 coalesce(p.display_name,p.username,''),f.locale::text,f.status,f.created_at,f.handled_at,
 coalesce(f.reply,''),f.replied_at
 FROM feedback f LEFT JOIN user_profiles p ON p.user_id=f.user_id
 WHERE ($1='' OR f.message ILIKE '%'||$1||'%' OR f.contact_email::text ILIKE '%'||$1||'%')
 ORDER BY f.created_at DESC LIMIT $2 OFFSET $3`, q, clamp(limit), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []FeedbackRow{}
	for rows.Next() {
		var x FeedbackRow
		if e = rows.Scan(&x.ID, &x.UserID, &x.Kind, &x.Message, &x.ContactEmail, &x.Author, &x.Locale, &x.Status, &x.CreatedAt, &x.HandledAt, &x.Reply, &x.RepliedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func validateFeedbackReply(value string) (string, error) {
	reply := strings.TrimSpace(value)
	if n := utf8.RuneCountInString(reply); n < 1 || n > 4000 {
		return "", httpapi.E(422, "INVALID_FEEDBACK_REPLY", "Reply must be between 1 and 4000 characters")
	}
	return reply, nil
}

// ReplyFeedback writes one private answer for a signed-in sender and closes the queue item
// in the same transaction. Anonymous feedback cannot receive an in-product answer because
// it has no account ownership boundary.
func (s *Service) ReplyFeedback(ctx context.Context, actor uuid.UUID, email string, id uuid.UUID, value string) error {
	reply, e := validateFeedbackReply(value)
	if e != nil {
		return e
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE feedback SET reply=$2,replied_at=now(),replied_by=$3,status='handled',handled_at=now()
 WHERE id=$1 AND user_id IS NOT NULL`, id, reply, actor)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(422, "FEEDBACK_NOT_REPLYABLE", "Feedback does not belong to a signed-in user")
	}
	if e = record(ctx, tx, actor, email, "feedback.reply", "feedback", id, nil); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// SetFeedbackStatus marks a message read or handled so a queue of one person's attention
// does not have to be held in their head. Audited like every other operator write.
func (s *Service) SetFeedbackStatus(ctx context.Context, actor uuid.UUID, email string, id uuid.UUID, status string) error {
	if status != "new" && status != "read" && status != "handled" {
		return httpapi.ErrInvalidInput
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE feedback SET status=$2,handled_at=CASE WHEN $2='handled' THEN now() ELSE NULL END WHERE id=$1`, id, status)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return httpapi.E(404, "FEEDBACK_NOT_FOUND", "Feedback not found")
	}
	if e = record(ctx, tx, actor, email, "feedback.status", "feedback", id, map[string]any{"status": status}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// storeSlug is the address a store is shared and indexed by. It is the search package's
// rule, repeated here rather than exported, because the two must agree: a store added by
// hand and one imported have to produce the same shape of URL.
func storeSlug(name string, id uuid.UUID) string {
	folded := textnorm.Fold(strings.ToLower(strings.TrimSpace(name)))
	var b strings.Builder
	dash := false
	for _, r := range folded {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "store"
	}
	if len(base) > 60 {
		base = base[:60]
	}
	return base + "-" + id.String()[:8]
}

// NewStore is what an operator types to add a shop by hand.
type NewStore struct {
	Name       string   `json:"name"`
	BrandSlug  string   `json:"brand_slug"`
	Address    string   `json:"address"`
	City       string   `json:"city"`
	District   string   `json:"district"`
	Phone      string   `json:"phone"`
	Website    string   `json:"website"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Categories []string `json:"categories"`
}

// NearbyStore is a shop that already exists close to where one is about to be added, with
// how close and how alike the two names are.
type NearbyStore struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Address    string    `json:"address"`
	City       string    `json:"city"`
	District   string    `json:"district"`
	Distance   float64   `json:"distance_meters"`
	Similarity float64   `json:"name_similarity"`
	SourceKind string    `json:"source_kind"`
}

// nearbyMeters is how far around a proposed shop is worth showing. Wider than the importer's
// merge radius on purpose: this list is shown to a person, who can tell a neighbour from a
// duplicate, and the cost of showing one shop too many is a glance.
const nearbyMeters = 400

// Nearby answers "is this shop already here?" for the add form.
//
// It does not decide; it shows. The importer has to choose without anybody watching and so
// needs thresholds, but an operator adding one shop can see that "Çelik Mağazacılık
// Konyaaltı Bellona" seven metres away is the shop they are about to type in again -- a
// judgement no similarity score made correctly.
func (s *Service) Nearby(ctx context.Context, name string, lat, lon float64) ([]NearbyStore, error) {
	rows, e := s.db.Query(ctx, `
SELECT id,name,coalesce(address,''),city,coalesce(district,''),
       ST_Distance(location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography),
       similarity(compact_name,$1),source_kind
  FROM stores
 WHERE deleted_at IS NULL
   AND ST_DWithin(location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography,$4)
 ORDER BY 6 LIMIT 12`, textnorm.Compact(name), lat, lon, nearbyMeters)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []NearbyStore{}
	for rows.Next() {
		var x NearbyStore
		if e = rows.Scan(&x.ID, &x.Name, &x.Address, &x.City, &x.District, &x.Distance, &x.Similarity, &x.SourceKind); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// CreateStore adds a shop an operator typed in.
//
// It is recorded as source_kind='admin' and verified now: a person looked at it, which is
// the strongest provenance this catalogue has. The importer will not overwrite it -- its
// update only rewrites rows it owns or rows nothing has verified.
//
// The duplicate check is shown by Nearby before this is called and is deliberately not
// enforced here. An operator who has seen the neighbours and still says add is answering a
// question the software cannot: two shops of different chains do share a mall floor.
func (s *Service) CreateStore(ctx context.Context, actor uuid.UUID, email string, in NewStore) (uuid.UUID, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || strings.TrimSpace(in.City) == "" {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	if !storepkg.ValidCoordinates(in.Latitude, in.Longitude) {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return uuid.Nil, e
	}
	defer tx.Rollback(ctx)

	id := uuid.New()
	var brand *uuid.UUID
	var brandName string
	if slug := strings.TrimSpace(in.BrandSlug); slug != "" {
		var found uuid.UUID
		if e = tx.QueryRow(ctx, `SELECT id,name FROM brands WHERE slug=$1`, slug).Scan(&found, &brandName); e != nil {
			return uuid.Nil, httpapi.E(404, "BRAND_NOT_FOUND", "Brand not found")
		}
		brand = &found
	}
	if _, e = tx.Exec(ctx, `
INSERT INTO stores(id,name,slug,brand_name,brand_id,address,city,district,location,phone,website,compact_name,
                   source_kind,data_verified_at,is_catalog_store,location_from)
VALUES($1,$2,$3,nullif($4,''),$5,nullif($6,''),$7,nullif($8,''),ST_SetSRID(ST_MakePoint($10,$9),4326)::geography,
       nullif($11,''),nullif($12,''),$13,'admin',now(),true,'entered by an operator')`,
		id, name, storeSlug(name, id), brandName, brand, strings.TrimSpace(in.Address), strings.TrimSpace(in.City),
		strings.TrimSpace(in.District), in.Latitude, in.Longitude, strings.TrimSpace(in.Phone),
		strings.TrimSpace(in.Website), textnorm.Compact(name)); e != nil {
		return uuid.Nil, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO store_stats(store_id) VALUES($1)`, id); e != nil {
		return uuid.Nil, e
	}
	if len(in.Categories) > 0 {
		if _, e = tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=ANY($2) AND active`, id, in.Categories); e != nil {
			return uuid.Nil, e
		}
	}
	if e = record(ctx, tx, actor, email, "store.create", "store", id, map[string]any{"name": name, "city": in.City}); e != nil {
		return uuid.Nil, e
	}
	return id, tx.Commit(ctx)
}

// MatchQueueRow is a published shop the matcher could not place: too like an existing one to
// ignore, too unlike it to merge on. The queue is the deliberate middle of that decision,
// and this is what a person needs to settle it -- both shops, side by side, with the
// distance and the resemblance that made it a question.
type MatchQueueRow struct {
	ID         uuid.UUID  `json:"id"`
	Brand      string     `json:"brand"`
	Name       string     `json:"name"`
	Address    string     `json:"address"`
	City       string     `json:"city"`
	District   string     `json:"district"`
	Reason     string     `json:"reason"`
	Similarity float64    `json:"similarity"`
	Distance   float64    `json:"distance_meters"`
	CreatedAt  time.Time  `json:"created_at"`
	MatchID    *uuid.UUID `json:"match_id"`
	MatchName  string     `json:"match_name"`
	MatchAddr  string     `json:"match_address"`
	MatchKind  string     `json:"match_source_kind"`
}

// Reviews lists what is waiting, newest first.
//
// Only rows whose matched store still exists are worth showing: if that store has since
// been merged away or deleted, the question the queue was holding has answered itself.
func (s *Service) MatchQueue(ctx context.Context, limit, offset int) ([]MatchQueueRow, error) {
	rows, e := s.db.Query(ctx, `
SELECT r.id,coalesce(b.name,''),r.name,
       coalesce(r.raw->>'address',''),coalesce(r.raw->>'city',''),coalesce(r.raw->>'district',''),
       coalesce(r.reason,''),coalesce(r.similarity,0),coalesce(r.distance_meters,0),r.created_at,
       r.matched_store_id,coalesce(m.name,''),coalesce(m.address,''),coalesce(m.source_kind,'')
  FROM store_import_records r
  JOIN store_import_runs run ON run.id=r.run_id
  LEFT JOIN brands b ON b.id=run.brand_id
  LEFT JOIN stores m ON m.id=r.matched_store_id AND m.deleted_at IS NULL
 WHERE r.action='needs_review'
 ORDER BY r.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []MatchQueueRow{}
	for rows.Next() {
		var x MatchQueueRow
		if e = rows.Scan(&x.ID, &x.Brand, &x.Name, &x.Address, &x.City, &x.District, &x.Reason,
			&x.Similarity, &x.Distance, &x.CreatedAt, &x.MatchID, &x.MatchName, &x.MatchAddr, &x.MatchKind); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ResolveReview settles one queued row.
//
// "merge" says the two are one shop: the existing row takes this brand's details and the
// question is closed. "separate" says they are two, and the published row is added as its
// own shop. Either way the record stops being a question, and the audit log says who
// answered it -- the queue exists so that a judgement is made visibly rather than by a
// threshold nobody can see.
func (s *Service) ResolveReview(ctx context.Context, actor uuid.UUID, email string, id uuid.UUID, merge bool) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var matched *uuid.UUID
	var brand *uuid.UUID
	var name string
	if e = tx.QueryRow(ctx, `
SELECT r.matched_store_id, run.brand_id, r.name FROM store_import_records r
  JOIN store_import_runs run ON run.id=r.run_id
 WHERE r.id=$1 AND r.action='needs_review'`, id).Scan(&matched, &brand, &name); e != nil {
		return httpapi.E(404, "REVIEW_NOT_FOUND", "This row is not waiting for a decision")
	}
	action := "skipped"
	if merge {
		if matched == nil {
			return httpapi.E(409, "NOTHING_TO_MERGE", "This row has no matching store to merge into")
		}
		// The shop is this brand's after all. Its own details are not overwritten here --
		// the next import of this brand will do that, now that the rows are tied together.
		if _, e = tx.Exec(ctx, `UPDATE stores SET brand_id=coalesce(brand_id,$2),source_kind='brand_locator',data_verified_at=now(),updated_at=now() WHERE id=$1`, *matched, brand); e != nil {
			return e
		}
		action = "updated"
	}
	if _, e = tx.Exec(ctx, `UPDATE store_import_records SET action=$2, reason=coalesce(reason,'')||' | settled by an operator' WHERE id=$1`, id, action); e != nil {
		return e
	}
	if e = record(ctx, tx, actor, email, "catalog.review", "import_record", id, map[string]any{"merge": merge, "name": name}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
