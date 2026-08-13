package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
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
	ID                uuid.UUID        `json:"id"`
	Name              string           `json:"name"`
	Slug              string           `json:"slug"`
	BrandName         string           `json:"brand_name,omitempty"`
	Address           string           `json:"address"`
	City              string           `json:"city"`
	District          string           `json:"district"`
	Latitude          float64          `json:"latitude"`
	Longitude         float64          `json:"longitude"`
	DistanceMeters    *float64         `json:"distance_meters,omitempty"`
	Categories        []string         `json:"categories"`
	Platform          Stats            `json:"platform"`
	ViewerFavorited   bool             `json:"viewer_has_favorited"`
	ViewerHasReviewed bool             `json:"viewer_has_reviewed"`
	ExternalSources   []ExternalSource `json:"external_sources,omitempty"`
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
	e := s.db.QueryRow(ctx, `SELECT s.id,s.name,s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 CASE WHEN $3::float8 IS NULL OR $4::float8 IS NULL THEN NULL ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($4,$3),4326)::geography) END,
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,
 EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=s.id AND f.user_id=$2),
 EXISTS(SELECT 1 FROM posts p WHERE p.store_id=s.id AND p.user_id=$2 AND p.deleted_at IS NULL),
 coalesce((SELECT jsonb_agg(jsonb_build_object('provider',x.provider,'external_id',x.external_id,'attribution',x.attribution,'refreshed_at',x.refreshed_at) ORDER BY x.provider) FROM store_external_sources x WHERE x.store_id=s.id),'[]'::jsonb)
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.id=$1 AND s.deleted_at IS NULL GROUP BY s.id,ss.store_id`, id, viewer, lat, lon).Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &distance, &x.Categories, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.ViewerFavorited, &x.ViewerHasReviewed, &x.ExternalSources)
	if errors.Is(e, pgx.ErrNoRows) {
		return x, httpapi.E(404, "STORE_NOT_FOUND", "Store not found")
	}
	x.DistanceMeters = distance
	return x, e
}

func (s *Service) Search(ctx context.Context, q string, categories []string, location string, lat, lon *float64, radius int, limit int, viewer *uuid.UUID) ([]Item, error) {
	q = strings.TrimSpace(q)
	location = strings.ToLower(strings.TrimSpace(location))
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, e := s.db.Query(ctx, `SELECT s.id,s.name,s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 CASE WHEN $2::float8 IS NULL OR $3::float8 IS NULL THEN NULL ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END,
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,
 EXISTS(SELECT 1 FROM favorites vf WHERE vf.store_id=s.id AND vf.user_id=$6)
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.deleted_at IS NULL AND ($1='' OR to_tsvector('simple',coalesce(s.name,'')||' '||coalesce(s.brand_name,'')||' '||coalesce(s.description,'')||' '||coalesce(s.city,'')||' '||coalesce(s.district,'')) @@ websearch_to_tsquery('simple',$1) OR ($7::text[] IS NOT NULL AND EXISTS(SELECT 1 FROM store_category_links cl JOIN store_categories sc ON sc.id=cl.category_id WHERE cl.store_id=s.id AND sc.slug=ANY($7))))
 AND ($8='' OR lower(s.city) LIKE '%'||$8||'%' OR lower(coalesce(s.district,'')) LIKE '%'||$8||'%')
 AND ($2::float8 IS NULL OR $3::float8 IS NULL OR ST_DWithin(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography,$4))
 GROUP BY s.id,ss.store_id ORDER BY CASE WHEN $1='' THEN 0 ELSE ts_rank(to_tsvector('simple',s.name||' '||coalesce(s.brand_name,'')),websearch_to_tsquery('simple',$1)) END DESC,
 CASE WHEN $2::float8 IS NULL THEN 0 ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END,ss.review_count DESC LIMIT $5`, q, lat, lon, radius, limit, viewer, nilStrings(categories), location)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var x Item
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.DistanceMeters, &x.Categories, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.ViewerFavorited); e != nil {
			return nil, e
		}
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
