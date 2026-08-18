package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db     *pgxpool.Pool
	report *reporting.Service
}

func NewService(db *pgxpool.Pool, report *reporting.Service) *Service { return &Service{db, report} }

type Stats struct {
	AverageRating float64 `json:"average_rating"`
	RatingCount   int     `json:"rating_count"`
	ReviewCount   int     `json:"review_count"`
	FavoriteCount int     `json:"favorite_count"`
	PostCount     int     `json:"post_count"`
}
type Item struct {
	ID                   uuid.UUID        `json:"id"`
	Name                 string           `json:"name"`
	Slug                 string           `json:"slug"`
	BrandName            string           `json:"brand_name,omitempty"`
	Address              string           `json:"address"`
	City                 string           `json:"city"`
	District             string           `json:"district"`
	Latitude             float64          `json:"latitude"`
	Longitude            float64          `json:"longitude"`
	DistanceMeters       *float64         `json:"distance_meters,omitempty"`
	Categories           []string         `json:"categories"`
	CategoryLabels       []string         `json:"category_labels"`
	LocalizedDescription string           `json:"localized_description,omitempty"`
	Platform             Stats            `json:"platform"`
	ViewerFavorited      bool             `json:"viewer_has_favorited"`
	ViewerHasReviewed    bool             `json:"viewer_has_reviewed"`
	ExternalSources      []ExternalSource `json:"external_sources,omitempty"`
}

type ExternalSource struct {
	Provider    string         `json:"provider"`
	ExternalID  string         `json:"external_id"`
	Attribution map[string]any `json:"attribution"`
	RefreshedAt *time.Time     `json:"refreshed_at,omitempty"`
}

func (s *Service) Get(ctx context.Context, id uuid.UUID, viewer *uuid.UUID, lat, lon *float64) (Item, error) {
	var x Item
	var distance *float64
	e := s.db.QueryRow(ctx, `SELECT s.id,coalesce((SELECT display_name FROM store_translations WHERE store_id=s.id AND locale=$5),s.name),s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 CASE WHEN $3::float8 IS NULL OR $4::float8 IS NULL THEN NULL ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($4,$3),4326)::geography) END,
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$5 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$5),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,
 EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=s.id AND f.user_id=$2),
 EXISTS(SELECT 1 FROM posts p WHERE p.store_id=s.id AND p.user_id=$2 AND p.deleted_at IS NULL),
 coalesce((SELECT jsonb_agg(jsonb_build_object('provider',x.provider,'external_id',x.external_id,'attribution',x.attribution,'refreshed_at',x.refreshed_at) ORDER BY x.provider) FROM store_external_sources x WHERE x.store_id=s.id),'[]'::jsonb)
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.id=$1 AND s.deleted_at IS NULL GROUP BY s.id,ss.store_id`, id, viewer, lat, lon, i18n.FromContext(ctx)).Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &distance, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.ViewerFavorited, &x.ViewerHasReviewed, &x.ExternalSources)
	if errors.Is(e, pgx.ErrNoRows) {
		return x, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	x.DistanceMeters = distance
	return x, e
}

// IndexEntry is the minimum a search engine needs to crawl a store: where it lives,
// when it last changed, and whether our own community has said anything about it yet.
type IndexEntry struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	City        string    `json:"city"`
	UpdatedAt   time.Time `json:"updated_at"`
	ReviewCount int       `json:"review_count"`
}

// Index enumerates published stores for sitemap generation. Search is query driven and
// cannot answer "every store you have", which is exactly what a sitemap is. Ordering is
// stable by id so paging through the whole catalogue cannot skip or repeat a row while
// stores are being written.
func (s *Service) Index(ctx context.Context, offset, limit int) ([]IndexEntry, error) {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	rows, e := s.db.Query(ctx, `SELECT s.id,s.slug,s.name,s.city,s.updated_at,ss.review_count
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id
 WHERE s.deleted_at IS NULL ORDER BY s.id LIMIT $1 OFFSET $2`, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]IndexEntry, 0, limit)
	for rows.Next() {
		var x IndexEntry
		if e = rows.Scan(&x.ID, &x.Slug, &x.Name, &x.City, &x.UpdatedAt, &x.ReviewCount); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ResolveSlug turns a human readable store URL back into an id. Slugs are unique and
// already stored, so readable URLs cost one indexed lookup rather than a schema change.
func (s *Service) ResolveSlug(ctx context.Context, slug string) (uuid.UUID, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" || len(slug) > 200 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	var id uuid.UUID
	e := s.db.QueryRow(ctx, `SELECT id FROM stores WHERE slug=$1 AND deleted_at IS NULL`, slug).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return uuid.Nil, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	return id, e
}

// Search returns matching stores. A radius of zero or less disables the distance
// filter entirely, which is how a store searched for by name is found in another city.
func (s *Service) Search(ctx context.Context, q string, categories []string, location string, lat, lon *float64, radius int, limit int, viewer *uuid.UUID) ([]Item, error) {
	q = strings.TrimSpace(q)
	location = strings.ToLower(strings.TrimSpace(location))
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, e := s.db.Query(ctx, `SELECT s.id,coalesce((SELECT display_name FROM store_translations WHERE store_id=s.id AND locale=$9),s.name),s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 CASE WHEN $2::float8 IS NULL OR $3::float8 IS NULL THEN NULL ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END,
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$9 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$9),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,
 EXISTS(SELECT 1 FROM favorites vf WHERE vf.store_id=s.id AND vf.user_id=$6)
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.deleted_at IS NULL AND ($1='' OR to_tsvector('simple',coalesce(s.name,'')||' '||coalesce(s.brand_name,'')||' '||coalesce(s.description,'')||' '||coalesce(s.city,'')||' '||coalesce(s.district,'')) @@ websearch_to_tsquery('simple',$1) OR ($7::text[] IS NOT NULL AND EXISTS(SELECT 1 FROM store_category_links cl JOIN store_categories sc ON sc.id=cl.category_id WHERE cl.store_id=s.id AND sc.slug=ANY($7))))
 AND ($8='' OR lower(s.city) LIKE '%'||$8||'%' OR lower(coalesce(s.district,'')) LIKE '%'||$8||'%')
 AND ($2::float8 IS NULL OR $3::float8 IS NULL OR $4<=0 OR ST_DWithin(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography,$4))
 GROUP BY s.id,ss.store_id ORDER BY CASE WHEN $1='' THEN 0 ELSE ts_rank(to_tsvector('simple',s.name||' '||coalesce(s.brand_name,'')),websearch_to_tsquery('simple',$1)) END DESC,
 CASE WHEN $2::float8 IS NULL THEN 0 ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END,ss.review_count DESC LIMIT $5`, q, lat, lon, radius, limit, viewer, nilStrings(categories), location, i18n.FromContext(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var x Item
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.DistanceMeters, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.ViewerFavorited); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Favorites lists the stores a user saved, most recently saved first.
func (s *Service) Favorites(ctx context.Context, viewer uuid.UUID, limit int) ([]Item, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, e := s.db.Query(ctx, `SELECT s.id,coalesce((SELECT display_name FROM store_translations WHERE store_id=s.id AND locale=$3),s.name),s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$3 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$3),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,
 coalesce((SELECT jsonb_agg(jsonb_build_object('provider',x.provider,'external_id',x.external_id,'attribution',x.attribution,'refreshed_at',x.refreshed_at) ORDER BY x.provider) FROM store_external_sources x WHERE x.store_id=s.id),'[]'::jsonb),f.created_at
 FROM favorites f JOIN stores s ON s.id=f.store_id AND s.deleted_at IS NULL JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE f.user_id=$1 GROUP BY s.id,ss.store_id,f.created_at ORDER BY f.created_at DESC LIMIT $2`, viewer, limit, i18n.FromContext(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var x Item
		var savedAt time.Time
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.ExternalSources, &savedAt); e != nil {
			return nil, e
		}
		x.ViewerFavorited = true
		out = append(out, x)
	}
	return out, rows.Err()
}

func nilStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func (s *Service) Favorite(ctx context.Context, user, store uuid.UUID, add bool) (bool, error) {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var tag pgconn.CommandTag
	if add {
		tag, e = tx.Exec(ctx, `INSERT INTO favorites(user_id,store_id) SELECT $1,id FROM stores WHERE id=$2 AND deleted_at IS NULL ON CONFLICT DO NOTHING`, user, store)
	} else {
		tag, e = tx.Exec(ctx, `DELETE FROM favorites WHERE user_id=$1 AND store_id=$2`, user, store)
	}
	if e != nil {
		return false, e
	}
	if tag.RowsAffected() == 0 && add {
		var exists bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stores WHERE id=$1 AND deleted_at IS NULL)`, store).Scan(&exists)
		if e != nil {
			return false, e
		}
		if !exists {
			return false, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
		}
		return false, tx.Commit(ctx)
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	delta := -1
	if add {
		delta = 1
	}
	_, e = tx.Exec(ctx, `UPDATE store_stats SET favorite_count=greatest(0,favorite_count+$2),updated_at=now() WHERE store_id=$1`, store, delta)
	if e != nil {
		return false, e
	}
	event := reporting.FavoriteRemoved
	if add {
		event = reporting.FavoriteCreated
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: event, IdempotencyKey: "favorite:" + uuid.NewString(), UserID: &user, StoreID: &store}); e != nil {
		return false, e
	}
	return true, tx.Commit(ctx)
}

func ValidCoordinates(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

var _ = time.Time{}
