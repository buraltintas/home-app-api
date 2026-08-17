package social

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db     *pgxpool.Pool
	cfg    Config
	report *reporting.Service
}

type Config struct {
	ReviewRadiusMeters        float64
	VisitProofTTL             time.Duration
	MaxLocationAccuracyMeters float64
}

func NewService(db *pgxpool.Pool, cfg Config, report *reporting.Service) *Service {
	return &Service{db: db, cfg: cfg, report: report}
}

type CreatePost struct {
	StoreID              uuid.UUID   `json:"store_id"`
	Text                 string      `json:"text"`
	Rating               int         `json:"rating"`
	Latitude             float64     `json:"latitude"`
	Longitude            float64     `json:"longitude"`
	AccuracyMeters       *float64    `json:"accuracy_meters"`
	VisitVerificationID  *uuid.UUID  `json:"visit_verification_id"`
	MediaIDs             []uuid.UUID `json:"media_ids"`
	OriginSearchID       *uuid.UUID  `json:"origin_search_id"`
	OriginSearchResultID *uuid.UUID  `json:"origin_search_result_id"`
	ContentLanguage      *string     `json:"content_language"`
}

type VisitVerification struct {
	ID             uuid.UUID `json:"id"`
	StoreID        uuid.UUID `json:"store_id"`
	DistanceMeters float64   `json:"distance_meters"`
	VerifiedAt     time.Time `json:"verified_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type Post struct {
	ID                  uuid.UUID    `json:"id"`
	UserID              uuid.UUID    `json:"user_id"`
	StoreID             uuid.UUID    `json:"store_id"`
	Text                string       `json:"text"`
	ContentLanguage     string       `json:"content_language,omitempty"`
	Rating              int          `json:"rating"`
	VisitVerified       bool         `json:"visit_verified"`
	DistanceMeters      float64      `json:"distance_meters"`
	StoreDistanceMeters *float64     `json:"store_distance_meters,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	Username            string       `json:"username"`
	DisplayName         string       `json:"display_name"`
	AvatarURL           string       `json:"avatar_url"`
	StoreName           string       `json:"store_name"`
	StoreCity           string       `json:"store_city"`
	StoreDistrict       string       `json:"store_district"`
	Media               []MediaAsset `json:"media"`
	LikeCount           int          `json:"like_count"`
	CommentCount        int          `json:"comment_count"`
	ViewerLiked         bool         `json:"viewer_has_liked"`
	ViewerFollows       bool         `json:"viewer_follows_author"`
	ViewerFavorited     bool         `json:"viewer_has_favorited_store"`
}

type FeedContext struct {
	Latitude  *float64
	Longitude *float64
}
type MediaAsset struct {
	ID       uuid.UUID `json:"id"`
	URL      string    `json:"url"`
	MimeType string    `json:"mime_type"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
}
type Comment struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	Body            string    `json:"body"`
	ContentLanguage string    `json:"content_language,omitempty"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"display_name"`
	AvatarURL       string    `json:"avatar_url"`
	CreatedAt       time.Time `json:"created_at"`
}

func (s *Service) CreatePost(ctx context.Context, user uuid.UUID, in CreatePost) (uuid.UUID, error) {
	in.Text = strings.TrimSpace(in.Text)
	textLength := utf8.RuneCountInString(in.Text)
	hasProof := in.VisitVerificationID != nil
	if in.StoreID == uuid.Nil || textLength < 3 || textLength > 5000 || in.Rating < 1 || in.Rating > 5 || (!hasProof && !storepkg.ValidCoordinates(in.Latitude, in.Longitude)) || len(in.MediaIDs) > 10 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	if hasProof && (in.Latitude != 0 || in.Longitude != 0 || in.AccuracyMeters != nil) {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	if hasProof && *in.VisitVerificationID == uuid.Nil {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	if !hasProof && in.AccuracyMeters != nil && (*in.AccuracyMeters <= 0 || *in.AccuracyMeters > s.cfg.MaxLocationAccuracyMeters) {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	seenMedia := make(map[uuid.UUID]struct{}, len(in.MediaIDs))
	if in.ContentLanguage != nil {
		locale, ok := i18n.Normalize(*in.ContentLanguage)
		if !ok {
			return uuid.Nil, httpapi.ErrInvalidInput
		}
		value := string(locale)
		in.ContentLanguage = &value
	}
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
	verifiedAt := time.Now()
	verificationSource := "current_location"
	if hasProof {
		verificationSource = "stored_visit"
		e = tx.QueryRow(ctx, `SELECT v.verification_distance_meters,v.verified_at FROM store_visit_verifications v JOIN stores s ON s.id=v.store_id AND s.deleted_at IS NULL WHERE v.id=$1 AND v.user_id=$2 AND v.store_id=$3 AND v.consumed_at IS NULL AND v.expires_at>now() FOR UPDATE OF v`, *in.VisitVerificationID, user, in.StoreID).Scan(&distance, &verifiedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, httpapi.E(422, "VISIT_VERIFICATION_INVALID", "Visit verification is invalid, expired, or already used")
		}
	} else {
		e = tx.QueryRow(ctx, `SELECT ST_Distance(location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography),now() FROM stores WHERE id=$3 AND deleted_at IS NULL FOR UPDATE`, in.Latitude, in.Longitude, in.StoreID).Scan(&distance, &verifiedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
		}
	}
	if e != nil {
		return uuid.Nil, e
	}
	effectiveDistance := distance
	if !hasProof && in.AccuracyMeters != nil {
		effectiveDistance += *in.AccuracyMeters
	}
	if effectiveDistance > s.cfg.ReviewRadiusMeters {
		// Release the store row lock before recording the rejected attempt. The
		// reporting event uses a separate transaction whose foreign-key check
		// otherwise waits on this transaction's FOR UPDATE lock indefinitely.
		if e = tx.Rollback(ctx); e != nil {
			return uuid.Nil, e
		}
		_, _ = s.report.Record(ctx, reporting.Event{Type: reporting.PostLocationRejected, IdempotencyKey: "post-location-rejected:" + uuid.NewString(), UserID: &user, StoreID: &in.StoreID, Metadata: map[string]any{"distance_meters": distance, "effective_distance_meters": effectiveDistance, "allowed_radius_meters": s.cfg.ReviewRadiusMeters}})
		return uuid.Nil, httpapi.E(422, "STORE_VISIT_NOT_VERIFIED", "You need to be near this store to review it.")
	}
	id := uuid.New()
	_, e = tx.Exec(ctx, `INSERT INTO posts(id,user_id,store_id,body,rating,verification_distance_meters,verified_at,content_language) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, user, in.StoreID, in.Text, in.Rating, distance, verifiedAt, in.ContentLanguage)
	if e != nil {
		return uuid.Nil, e
	}
	if hasProof {
		tag, updateErr := tx.Exec(ctx, `UPDATE store_visit_verifications SET consumed_at=now(),consumed_post_id=$2 WHERE id=$1 AND consumed_at IS NULL`, *in.VisitVerificationID, id)
		if updateErr != nil {
			return uuid.Nil, updateErr
		}
		if tag.RowsAffected() != 1 {
			return uuid.Nil, httpapi.E(422, "VISIT_VERIFICATION_INVALID", "Visit verification is invalid, expired, or already used")
		}
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
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.PostVisitVerified, IdempotencyKey: "post-verified:" + id.String(), UserID: &user, StoreID: &in.StoreID, PostID: &id, Metadata: map[string]any{"distance_meters": distance, "verification_source": verificationSource, "verified_at": verifiedAt}}); e != nil {
		return uuid.Nil, e
	}
	return id, tx.Commit(ctx)
}

func (s *Service) VerifyVisit(ctx context.Context, user, store uuid.UUID, latitude, longitude, accuracy float64) (VisitVerification, error) {
	var out VisitVerification
	if user == uuid.Nil || store == uuid.Nil || !storepkg.ValidCoordinates(latitude, longitude) || accuracy <= 0 || accuracy > s.cfg.MaxLocationAccuracyMeters {
		return out, httpapi.ErrInvalidInput
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var distance float64
	err = tx.QueryRow(ctx, `SELECT ST_Distance(location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography) FROM stores WHERE id=$3 AND deleted_at IS NULL`, latitude, longitude, store).Scan(&distance)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	if err != nil {
		return out, err
	}
	if distance+accuracy > s.cfg.ReviewRadiusMeters {
		if err = tx.Rollback(ctx); err != nil {
			return out, err
		}
		_, _ = s.report.Record(ctx, reporting.Event{Type: reporting.StoreVisitRejected, IdempotencyKey: "visit-location-rejected:" + uuid.NewString(), UserID: &user, StoreID: &store, Metadata: map[string]any{"distance_meters": distance, "accuracy_meters": accuracy, "effective_distance_meters": distance + accuracy, "allowed_radius_meters": s.cfg.ReviewRadiusMeters}})
		return out, httpapi.E(422, "STORE_VISIT_NOT_VERIFIED", "You need to be near this store to verify a visit.")
	}
	id := uuid.New()
	err = tx.QueryRow(ctx, `INSERT INTO store_visit_verifications(id,user_id,store_id,verification_distance_meters,reported_accuracy_meters,verified_at,expires_at) VALUES($1,$2,$3,$4,$5,now(),now()+$6::interval) ON CONFLICT(user_id,store_id) WHERE consumed_at IS NULL DO UPDATE SET verification_distance_meters=excluded.verification_distance_meters,reported_accuracy_meters=excluded.reported_accuracy_meters,verified_at=excluded.verified_at,expires_at=excluded.expires_at RETURNING id,store_id,verification_distance_meters,verified_at,expires_at`, id, user, store, distance, accuracy, s.cfg.VisitProofTTL.String()).Scan(&out.ID, &out.StoreID, &out.DistanceMeters, &out.VerifiedAt, &out.ExpiresAt)
	if err != nil {
		return out, err
	}
	if _, err = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.StoreVisitVerified, IdempotencyKey: "store-visit-verified:" + out.ID.String() + ":" + out.VerifiedAt.UTC().Format(time.RFC3339Nano), UserID: &user, StoreID: &store, Metadata: map[string]any{"distance_meters": distance, "accuracy_meters": accuracy, "expires_at": out.ExpiresAt}}); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *Service) Feed(ctx context.Context, viewer *uuid.UUID, cursor string, limit int, options ...FeedContext) ([]Post, string, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	var feedContext FeedContext
	if len(options) > 0 {
		feedContext = options[0]
	}
	if (feedContext.Latitude == nil) != (feedContext.Longitude == nil) || (feedContext.Latitude != nil && !storepkg.ValidCoordinates(*feedContext.Latitude, *feedContext.Longitude)) {
		return nil, "", httpapi.ErrInvalidInput
	}
	mode := "recent"
	if feedContext.Latitude != nil {
		mode = "nearby"
	}
	var before time.Time
	var beforeID uuid.UUID
	var beforeDistance *float64
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return nil, "", httpapi.ErrInvalidInput
		}
		var c struct {
			Mode     string    `json:"m"`
			Distance *float64  `json:"d,omitempty"`
			Time     time.Time `json:"t"`
			ID       uuid.UUID `json:"id"`
		}
		if json.Unmarshal(b, &c) != nil || c.Time.IsZero() || c.ID == uuid.Nil {
			return nil, "", httpapi.ErrInvalidInput
		}
		if c.Mode == "" {
			c.Mode = "recent"
		}
		if c.Mode != mode || (mode == "nearby" && (c.Distance == nil || *c.Distance < 0)) {
			return nil, "", httpapi.ErrInvalidInput
		}
		before, beforeID = c.Time, c.ID
		beforeDistance = c.Distance
	}
	rows, e := s.db.Query(ctx, `WITH feed AS (SELECT p.id,p.user_id,p.store_id,p.body,coalesce(p.content_language::text,'') content_language,p.rating,p.visit_verified,p.verification_distance_meters,p.created_at,
 coalesce(up.username::text,''),coalesce(up.display_name,''),coalesce(up.avatar_url,''),st.name,st.city,coalesce(st.district,''),
 (SELECT count(*) FROM likes l WHERE l.post_id=p.id),(SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),
 EXISTS(SELECT 1 FROM likes l WHERE l.post_id=p.id AND l.user_id=$1),EXISTS(SELECT 1 FROM follows f WHERE f.following_id=p.user_id AND f.follower_id=$1),EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=p.store_id AND f.user_id=$1),
 coalesce((SELECT jsonb_agg(jsonb_build_object('id',m.id,'url','/media/'||m.id::text,'mime_type',m.mime_type,'width',m.width,'height',m.height) ORDER BY pm.position) FROM post_media pm JOIN media m ON m.id=pm.media_id WHERE pm.post_id=p.id),'[]'::jsonb),
 CASE WHEN $5::float8 IS NULL OR $6::float8 IS NULL THEN NULL ELSE ST_Distance(st.location,ST_SetSRID(ST_MakePoint($6,$5),4326)::geography) END store_distance_meters
 FROM posts p JOIN users u ON u.id=p.user_id AND u.deleted_at IS NULL JOIN user_profiles up ON up.user_id=u.id JOIN stores st ON st.id=p.store_id AND st.deleted_at IS NULL
 WHERE p.deleted_at IS NULL)
 SELECT * FROM feed WHERE
 ($5::float8 IS NULL AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3))) OR
 ($5::float8 IS NOT NULL AND ($7::float8 IS NULL OR store_distance_meters>$7 OR (store_distance_meters=$7 AND (created_at,id)<($2,$3))))
 ORDER BY store_distance_meters ASC NULLS LAST,created_at DESC,id DESC LIMIT $4`, viewer, nilTime(before), nilUUID(beforeID), limit+1, feedContext.Latitude, feedContext.Longitude, beforeDistance)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if e = rows.Scan(&p.ID, &p.UserID, &p.StoreID, &p.Text, &p.ContentLanguage, &p.Rating, &p.VisitVerified, &p.DistanceMeters, &p.CreatedAt, &p.Username, &p.DisplayName, &p.AvatarURL, &p.StoreName, &p.StoreCity, &p.StoreDistrict, &p.LikeCount, &p.CommentCount, &p.ViewerLiked, &p.ViewerFollows, &p.ViewerFavorited, &p.Media, &p.StoreDistanceMeters); e != nil {
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
		cursorValue := map[string]any{"m": mode, "t": last.CreatedAt, "id": last.ID}
		if mode == "nearby" {
			cursorValue["d"] = last.StoreDistanceMeters
		}
		b, _ := json.Marshal(cursorValue)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, next, nil
}

func (s *Service) GetPost(ctx context.Context, id uuid.UUID, viewer *uuid.UUID) (Post, error) {
	var p Post
	e := s.db.QueryRow(ctx, `SELECT p.id,p.user_id,p.store_id,p.body,coalesce(p.content_language::text,''),p.rating,p.visit_verified,p.verification_distance_meters,p.created_at,coalesce(up.username::text,''),coalesce(up.display_name,''),coalesce(up.avatar_url,''),st.name,st.city,coalesce(st.district,''),(SELECT count(*) FROM likes l WHERE l.post_id=p.id),(SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),EXISTS(SELECT 1 FROM likes l WHERE l.post_id=p.id AND l.user_id=$2),EXISTS(SELECT 1 FROM follows f WHERE f.following_id=p.user_id AND f.follower_id=$2),EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=p.store_id AND f.user_id=$2),coalesce((SELECT jsonb_agg(jsonb_build_object('id',m.id,'url','/media/'||m.id::text,'mime_type',m.mime_type,'width',m.width,'height',m.height) ORDER BY pm.position) FROM post_media pm JOIN media m ON m.id=pm.media_id WHERE pm.post_id=p.id),'[]'::jsonb) FROM posts p JOIN user_profiles up ON up.user_id=p.user_id JOIN stores st ON st.id=p.store_id WHERE p.id=$1 AND p.deleted_at IS NULL`, id, viewer).Scan(&p.ID, &p.UserID, &p.StoreID, &p.Text, &p.ContentLanguage, &p.Rating, &p.VisitVerified, &p.DistanceMeters, &p.CreatedAt, &p.Username, &p.DisplayName, &p.AvatarURL, &p.StoreName, &p.StoreCity, &p.StoreDistrict, &p.LikeCount, &p.CommentCount, &p.ViewerLiked, &p.ViewerFollows, &p.ViewerFavorited, &p.Media)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, httpapi.E(404, "POST_NOT_FOUND", "Post not found")
	}
	return p, e
}
func (s *Service) Comments(ctx context.Context, post uuid.UUID, limit int) ([]Comment, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, e := s.db.Query(ctx, `SELECT c.id,c.user_id,c.body,coalesce(c.content_language::text,''),c.created_at,coalesce(p.username::text,''),coalesce(p.display_name,''),coalesce(p.avatar_url,'') FROM comments c JOIN user_profiles p ON p.user_id=c.user_id WHERE c.post_id=$1 AND c.deleted_at IS NULL ORDER BY c.created_at,c.id LIMIT $2`, post, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if e = rows.Scan(&c.ID, &c.UserID, &c.Body, &c.ContentLanguage, &c.CreatedAt, &c.Username, &c.DisplayName, &c.AvatarURL); e != nil {
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
	q := `SELECT p.id,p.user_id,p.store_id,p.body,coalesce(p.content_language::text,''),p.rating,p.visit_verified,p.verification_distance_meters,p.created_at,coalesce(up.username::text,''),coalesce(up.display_name,''),coalesce(up.avatar_url,''),st.name,st.city,coalesce(st.district,''),(SELECT count(*) FROM likes l WHERE l.post_id=p.id),(SELECT count(*) FROM comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),EXISTS(SELECT 1 FROM likes l WHERE l.post_id=p.id AND l.user_id=$3),EXISTS(SELECT 1 FROM follows f WHERE f.following_id=p.user_id AND f.follower_id=$3),EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=p.store_id AND f.user_id=$3),coalesce((SELECT jsonb_agg(jsonb_build_object('id',m.id,'url','/media/'||m.id::text,'mime_type',m.mime_type,'width',m.width,'height',m.height) ORDER BY pm.position) FROM post_media pm JOIN media m ON m.id=pm.media_id WHERE pm.post_id=p.id),'[]'::jsonb) FROM posts p JOIN user_profiles up ON up.user_id=p.user_id JOIN stores st ON st.id=p.store_id WHERE p.` + column + `=$1 AND p.deleted_at IS NULL ORDER BY p.created_at DESC,p.id DESC LIMIT $2`
	rows, e := s.db.Query(ctx, q, id, limit, viewer)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if e = rows.Scan(&p.ID, &p.UserID, &p.StoreID, &p.Text, &p.ContentLanguage, &p.Rating, &p.VisitVerified, &p.DistanceMeters, &p.CreatedAt, &p.Username, &p.DisplayName, &p.AvatarURL, &p.StoreName, &p.StoreCity, &p.StoreDistrict, &p.LikeCount, &p.CommentCount, &p.ViewerLiked, &p.ViewerFollows, &p.ViewerFavorited, &p.Media); e != nil {
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
	return s.AddCommentLocalized(ctx, user, post, body, nil)
}

func (s *Service) AddCommentLocalized(ctx context.Context, user, post uuid.UUID, body string, language *string) (uuid.UUID, error) {
	body = strings.TrimSpace(body)
	if utf8.RuneCountInString(body) < 1 || utf8.RuneCountInString(body) > 2000 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	if language != nil {
		locale, ok := i18n.Normalize(*language)
		if !ok {
			return uuid.Nil, httpapi.ErrInvalidInput
		}
		value := string(locale)
		language = &value
	}
	id := uuid.New()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return uuid.Nil, e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `INSERT INTO comments(id,post_id,user_id,body,content_language) SELECT $1,id,$3,$4,$5 FROM posts WHERE id=$2 AND deleted_at IS NULL`, id, post, user, body, language)
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
