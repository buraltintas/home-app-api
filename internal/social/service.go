package social

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db           *pgxpool.Pool
	reviewRadius float64
	report       *reporting.Service
}

func NewService(db *pgxpool.Pool, r float64, report *reporting.Service) *Service {
	return &Service{db, r, report}
}

type CreatePost struct {
	StoreID              uuid.UUID   `json:"store_id"`
	Text                 string      `json:"text"`
	Rating               int         `json:"rating"`
	Latitude             float64     `json:"latitude"`
	Longitude            float64     `json:"longitude"`
	MediaIDs             []uuid.UUID `json:"media_ids"`
	OriginSearchID       *uuid.UUID  `json:"origin_search_id"`
	OriginSearchResultID *uuid.UUID  `json:"origin_search_result_id"`
}
type Post struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	StoreID         uuid.UUID `json:"store_id"`
	Text            string    `json:"text"`
	Rating          int       `json:"rating"`
	VisitVerified   bool      `json:"visit_verified"`
	DistanceMeters  float64   `json:"distance_meters"`
	CreatedAt       time.Time `json:"created_at"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"display_name"`
	AvatarURL       string    `json:"avatar_url"`
	StoreName       string    `json:"store_name"`
	StoreCity       string    `json:"store_city"`
	StoreDistrict   string    `json:"store_district"`
	Media           []string  `json:"media"`
	LikeCount       int       `json:"like_count"`
	CommentCount    int       `json:"comment_count"`
	ViewerLiked     bool      `json:"viewer_has_liked"`
	ViewerFollows   bool      `json:"viewer_follows_author"`
	ViewerFavorited bool      `json:"viewer_has_favorited_store"`
}
type Comment struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	Body        string    `json:"body"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Service) CreatePost(ctx context.Context, user uuid.UUID, in CreatePost) (uuid.UUID, error) {
	in.Text = strings.TrimSpace(in.Text)
	if in.StoreID == uuid.Nil || len(in.Text) < 3 || len(in.Text) > 5000 || in.Rating < 1 || in.Rating > 5 || !storepkg.ValidCoordinates(in.Latitude, in.Longitude) || len(in.MediaIDs) > 10 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	seenMedia := make(map[uuid.UUID]struct{}, len(in.MediaIDs))
	for _, id := range in.MediaIDs {
		if id == uuid.Nil {
			return uuid.Nil, httpapi.ErrInvalidInput
		}
		if _, exists := seenMedia[id]; exists {
			return uuid.Nil, httpapi.E(400, "DUPLICATE_MEDIA", "Each media item can only be attached once")
		}
		seenMedia[id] = struct{}{}
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return uuid.Nil, e
	}
	defer tx.Rollback(ctx)
	var distance float64
	e = tx.QueryRow(ctx, `SELECT ST_Distance(location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography) FROM stores WHERE id=$3 AND deleted_at IS NULL FOR UPDATE`, in.Latitude, in.Longitude, in.StoreID).Scan(&distance)
	if errors.Is(e, pgx.ErrNoRows) {
		return uuid.Nil, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	if e != nil {
		return uuid.Nil, e
	}
	if distance > s.reviewRadius {
		_, _ = s.report.Record(ctx, reporting.Event{Type: reporting.PostLocationRejected, IdempotencyKey: "post-location-rejected:" + uuid.NewString(), UserID: &user, StoreID: &in.StoreID, Metadata: map[string]any{"distance_meters": distance, "allowed_radius_meters": s.reviewRadius}})
		return uuid.Nil, httpapi.E(422, "STORE_VISIT_NOT_VERIFIED", "You need to be near this store to review it.")
	}
	id := uuid.New()
	_, e = tx.Exec(ctx, `INSERT INTO posts(id,user_id,store_id,body,rating,verification_distance_meters,verified_at) VALUES($1,$2,$3,$4,$5,$6,now())`, id, user, in.StoreID, in.Text, in.Rating, distance)
	if e != nil {
		return uuid.Nil, e
	}
	for i, m := range in.MediaIDs {
		tag, e := tx.Exec(ctx, `INSERT INTO post_media(post_id,media_id,position) SELECT $1,id,$3 FROM media WHERE id=$2 AND owner_user_id=$4 AND status='ready'`, id, m, i, user)
		if e != nil {
			return uuid.Nil, e
		}
		if tag.RowsAffected() != 1 {
			return uuid.Nil, httpapi.E(400, "INVALID_MEDIA", "Media does not exist or is not ready")
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO store_stats(store_id,average_rating,rating_count,review_count,post_count) VALUES($1,$2,1,1,1) ON CONFLICT(store_id) DO UPDATE SET rating_count=store_stats.rating_count+1,review_count=store_stats.review_count+1,post_count=store_stats.post_count+1,average_rating=((store_stats.average_rating*store_stats.rating_count)+$2)/(store_stats.rating_count+1),updated_at=now()`, in.StoreID, in.Rating)
	if e != nil {
		return uuid.Nil, e
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.PostCreated, IdempotencyKey: "post-created:" + id.String(), UserID: &user, StoreID: &in.StoreID, PostID: &id}); e != nil {
		return uuid.Nil, e
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.PostVisitVerified, IdempotencyKey: "post-verified:" + id.String(), UserID: &user, StoreID: &in.StoreID, PostID: &id, Metadata: map[string]any{"distance_meters": distance}}); e != nil {
		return uuid.Nil, e
	}
	return id, tx.Commit(ctx)
}

func (s *Service) Feed(ctx context.Context, viewer *uuid.UUID, cursor string, limit int) ([]Post, string, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	var before time.Time
	var beforeID uuid.UUID
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return nil, "", httpapi.ErrInvalidInput
		}
		var c struct {
			Time time.Time `json:"t"`
			ID   uuid.UUID `json:"id"`
		}
		if json.Unmarshal(b, &c) != nil {
			return nil, "", httpapi.ErrInvalidInput
		}
		before, beforeID = c.Time, c.ID
	}
	rows, e := s.db.Query(ctx, `SELECT p.id,p.user_id,p.store_id,p.body,p.rating,p.visit_verified,p.verification_distance_meters,p.created_at,
 coalesce(up.username::text,''),coalesce(up.display_name,''),coalesce(up.avatar_url,''),st.name,st.city,coalesce(st.district,''),
 (SELECT count(*) FROM likes l WHERE l.post_id=p.id),(SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),
 EXISTS(SELECT 1 FROM likes l WHERE l.post_id=p.id AND l.user_id=$1),EXISTS(SELECT 1 FROM follows f WHERE f.following_id=p.user_id AND f.follower_id=$1),EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=p.store_id AND f.user_id=$1),
 coalesce((SELECT array_agg(m.storage_key ORDER BY pm.position) FROM post_media pm JOIN media m ON m.id=pm.media_id WHERE pm.post_id=p.id),'{}')
 FROM posts p JOIN users u ON u.id=p.user_id AND u.deleted_at IS NULL JOIN user_profiles up ON up.user_id=u.id JOIN stores st ON st.id=p.store_id AND st.deleted_at IS NULL
 WHERE p.deleted_at IS NULL AND ($2::timestamptz IS NULL OR (p.created_at,p.id)<($2,$3)) ORDER BY p.created_at DESC,p.id DESC LIMIT $4`, viewer, nilTime(before), nilUUID(beforeID), limit+1)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if e = rows.Scan(&p.ID, &p.UserID, &p.StoreID, &p.Text, &p.Rating, &p.VisitVerified, &p.DistanceMeters, &p.CreatedAt, &p.Username, &p.DisplayName, &p.AvatarURL, &p.StoreName, &p.StoreCity, &p.StoreDistrict, &p.LikeCount, &p.CommentCount, &p.ViewerLiked, &p.ViewerFollows, &p.ViewerFavorited, &p.Media); e != nil {
			return nil, "", e
		}
		out = append(out, p)
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		b, _ := json.Marshal(map[string]any{"t": last.CreatedAt, "id": last.ID})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, next, nil
}

func (s *Service) GetPost(ctx context.Context, id uuid.UUID, viewer *uuid.UUID) (Post, error) {
	var p Post
	e := s.db.QueryRow(ctx, `SELECT p.id,p.user_id,p.store_id,p.body,p.rating,p.visit_verified,p.verification_distance_meters,p.created_at,coalesce(up.username::text,''),coalesce(up.display_name,''),coalesce(up.avatar_url,''),st.name,st.city,coalesce(st.district,''),(SELECT count(*) FROM likes l WHERE l.post_id=p.id),(SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),EXISTS(SELECT 1 FROM likes l WHERE l.post_id=p.id AND l.user_id=$2),EXISTS(SELECT 1 FROM follows f WHERE f.following_id=p.user_id AND f.follower_id=$2),EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=p.store_id AND f.user_id=$2),coalesce((SELECT array_agg(m.storage_key ORDER BY pm.position) FROM post_media pm JOIN media m ON m.id=pm.media_id WHERE pm.post_id=p.id),'{}') FROM posts p JOIN user_profiles up ON up.user_id=p.user_id JOIN stores st ON st.id=p.store_id WHERE p.id=$1 AND p.deleted_at IS NULL`, id, viewer).Scan(&p.ID, &p.UserID, &p.StoreID, &p.Text, &p.Rating, &p.VisitVerified, &p.DistanceMeters, &p.CreatedAt, &p.Username, &p.DisplayName, &p.AvatarURL, &p.StoreName, &p.StoreCity, &p.StoreDistrict, &p.LikeCount, &p.CommentCount, &p.ViewerLiked, &p.ViewerFollows, &p.ViewerFavorited, &p.Media)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, httpapi.E(404, "POST_NOT_FOUND", "Post not found")
	}
	return p, e
}
func (s *Service) Comments(ctx context.Context, post uuid.UUID, limit int) ([]Comment, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, e := s.db.Query(ctx, `SELECT c.id,c.user_id,c.body,c.created_at,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,'') FROM comments c JOIN user_profiles p ON p.user_id=c.user_id WHERE c.post_id=$1 AND c.deleted_at IS NULL ORDER BY c.created_at,c.id LIMIT $2`, post, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if e = rows.Scan(&c.ID, &c.UserID, &c.Body, &c.CreatedAt, &c.Username, &c.DisplayName, &c.AvatarURL); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) PostsBy(ctx context.Context, column string, id uuid.UUID, viewer *uuid.UUID, limit int) ([]Post, error) {
	if column != "user_id" && column != "store_id" {
		return nil, httpapi.ErrInvalidInput
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	q := `SELECT p.id,p.user_id,p.store_id,p.body,p.rating,p.visit_verified,p.verification_distance_meters,p.created_at,coalesce(up.username::text,''),coalesce(up.display_name,''),coalesce(up.avatar_url,''),st.name,st.city,coalesce(st.district,''),(SELECT count(*) FROM likes l WHERE l.post_id=p.id),(SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),EXISTS(SELECT 1 FROM likes l WHERE l.post_id=p.id AND l.user_id=$3),EXISTS(SELECT 1 FROM follows f WHERE f.following_id=p.user_id AND f.follower_id=$3),EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=p.store_id AND f.user_id=$3),coalesce((SELECT array_agg(m.storage_key ORDER BY pm.position) FROM post_media pm JOIN media m ON m.id=pm.media_id WHERE pm.post_id=p.id),'{}') FROM posts p JOIN user_profiles up ON up.user_id=p.user_id JOIN stores st ON st.id=p.store_id WHERE p.` + column + `=$1 AND p.deleted_at IS NULL ORDER BY p.created_at DESC,p.id DESC LIMIT $2`
	rows, e := s.db.Query(ctx, q, id, limit, viewer)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if e = rows.Scan(&p.ID, &p.UserID, &p.StoreID, &p.Text, &p.Rating, &p.VisitVerified, &p.DistanceMeters, &p.CreatedAt, &p.Username, &p.DisplayName, &p.AvatarURL, &p.StoreName, &p.StoreCity, &p.StoreDistrict, &p.LikeCount, &p.CommentCount, &p.ViewerLiked, &p.ViewerFollows, &p.ViewerFavorited, &p.Media); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func nilUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (s *Service) Like(ctx context.Context, user, post uuid.UUID, add bool) error {
	event := reporting.LikeRemoved
	if add {
		event = reporting.LikeCreated
	}
	return s.uniqueMutation(ctx, add, `INSERT INTO likes(user_id,post_id) SELECT $1,id FROM posts WHERE id=$2 AND deleted_at IS NULL ON CONFLICT DO NOTHING`, `DELETE FROM likes WHERE user_id=$1 AND post_id=$2`, `SELECT EXISTS(SELECT 1 FROM posts WHERE id=$1 AND deleted_at IS NULL)`, user, post, event, reporting.Event{UserID: &user, PostID: &post}, httpapi.E(404, "POST_NOT_FOUND", "Post not found"))
}
func (s *Service) Follow(ctx context.Context, user, target uuid.UUID, add bool) error {
	if user == target {
		return httpapi.E(422, "CANNOT_FOLLOW_SELF", "You cannot follow yourself")
	}
	event := reporting.FollowRemoved
	if add {
		event = reporting.FollowCreated
	}
	return s.uniqueMutation(ctx, add, `INSERT INTO follows(follower_id,following_id) SELECT $1,id FROM users WHERE id=$2 AND deleted_at IS NULL ON CONFLICT DO NOTHING`, `DELETE FROM follows WHERE follower_id=$1 AND following_id=$2`, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)`, user, target, event, reporting.Event{UserID: &user, Metadata: map[string]any{"following_user_id": target.String()}}, httpapi.E(404, "USER_NOT_FOUND", "User not found"))
}
func (s *Service) uniqueMutation(ctx context.Context, add bool, insert, del, existsQuery string, a, b uuid.UUID, event string, ev reporting.Event, notFound error) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	q := del
	if add {
		q = insert
	}
	tag, e := tx.Exec(ctx, q, a, b)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		if add {
			var exists bool
			if e = tx.QueryRow(ctx, existsQuery, b).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return notFound
			}
		}
		return tx.Commit(ctx)
	}
	ev.Type = event
	ev.IdempotencyKey = "social:" + uuid.NewString()
	if _, e = s.report.RecordTx(ctx, tx, ev); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Service) AddComment(ctx context.Context, user, post uuid.UUID, body string) (uuid.UUID, error) {
	body = strings.TrimSpace(body)
	if len(body) < 1 || len(body) > 2000 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	id := uuid.New()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return uuid.Nil, e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `INSERT INTO comments(id,post_id,user_id,body) SELECT $1,id,$3,$4 FROM posts WHERE id=$2 AND deleted_at IS NULL`, id, post, user, body)
	if e != nil {
		return uuid.Nil, e
	}
	if tag.RowsAffected() == 0 {
		return uuid.Nil, httpapi.E(404, "POST_NOT_FOUND", "Post not found")
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.CommentCreated, IdempotencyKey: "comment-created:" + id.String(), UserID: &user, PostID: &post}); e != nil {
		return uuid.Nil, e
	}
	return id, tx.Commit(ctx)
}
func (s *Service) DeleteComment(ctx context.Context, user, comment uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var post uuid.UUID
	e = tx.QueryRow(ctx, `UPDATE comments SET deleted_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL RETURNING post_id`, comment, user).Scan(&post)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return httpapi.E(404, "COMMENT_NOT_FOUND", "Comment not found or not owned by you")
		}
		return e
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.CommentDeleted, IdempotencyKey: "comment-deleted:" + comment.String(), UserID: &user, PostID: &post}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) DeletePost(ctx context.Context, user, post uuid.UUID) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var store uuid.UUID
	var rating int
	e = tx.QueryRow(ctx, `UPDATE posts SET deleted_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL RETURNING store_id,rating`, post, user).Scan(&store, &rating)
	if errors.Is(e, pgx.ErrNoRows) {
		return httpapi.E(404, "POST_NOT_FOUND", "Post not found or not owned by you")
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE store_stats ss SET rating_count=x.n,review_count=x.n,post_count=x.n,average_rating=x.avg,updated_at=now() FROM (SELECT count(*)::int n,coalesce(avg(rating),0) avg FROM posts WHERE store_id=$1 AND deleted_at IS NULL) x WHERE ss.store_id=$1`, store)
	if e != nil {
		return e
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.PostDeleted, IdempotencyKey: "post-deleted:" + post.String(), UserID: &user, StoreID: &store, PostID: &post}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

var _ = fmt.Sprintf
