package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	IsPremium            bool             `json:"is_premium"`
	ViewerFavorited      bool             `json:"viewer_has_favorited"`
	ViewerHasReviewed    bool             `json:"viewer_has_reviewed"`
	ExternalSources      []ExternalSource `json:"external_sources,omitempty"`
	Photo                *Photo           `json:"photo,omitempty"`
	// A photograph from a community review of this store, which outranks the provider's own.
	// Somebody who went there and took it describes the place better than a stock frame does.
	OwnPhoto *OwnPhoto `json:"own_photo,omitempty"`
}

// Photo is the provider photograph already held for a store, carried alongside list
// results so a store we have on file is not shown as a blank tile whenever the live
// provider response happens not to include it.
type Photo struct {
	Name         string   `json:"name"`
	Attributions []string `json:"attributions,omitempty"`
}

// OwnPhoto is media uploaded with a review of this store. It carries the media id rather
// than a URL: the client streams it through the API like any other upload.
type OwnPhoto struct {
	MediaID string `json:"media_id"`
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
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$5 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$5),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,s.is_premium,
 EXISTS(SELECT 1 FROM favorites f WHERE f.store_id=s.id AND f.user_id=$2),
 EXISTS(SELECT 1 FROM posts p WHERE p.store_id=s.id AND p.user_id=$2 AND p.deleted_at IS NULL),
 coalesce((SELECT jsonb_agg(jsonb_build_object('provider',x.provider,'external_id',x.external_id,'attribution',x.attribution,'refreshed_at',x.refreshed_at) ORDER BY x.provider) FROM store_external_sources x WHERE x.store_id=s.id),'[]'::jsonb)
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.id=$1 AND s.deleted_at IS NULL GROUP BY s.id,ss.store_id`, id, viewer, lat, lon, i18n.FromContext(ctx)).Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &distance, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.IsPremium, &x.ViewerFavorited, &x.ViewerHasReviewed, &x.ExternalSources)
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

// PremiumNearby returns promoted stores close to the searcher that match the categories
// being searched for.
//
// It exists because promotion that only reorders is not promotion. The candidate list comes
// from Google, and Google decides what it returns: a paid-for store that Google did not
// happen to include could not be lifted to the top, because it was not in the list at all.
// A search for carpets in Antalya missed the promoted carpet shop in Antalya for exactly
// that reason.
//
// Category is deliberately not a condition. Somebody paid to be seen in their own city,
// and a filter that quietly excluded them from most searches would be selling placement
// that does not place. Every store here is a home and living store, so a promoted one is
// never wholly irrelevant to a home and living search.
//
// The trade is real and worth naming: a promoted carpet shop will appear above organic
// results for a lighting search. It is labelled, it is bounded to the searcher's own city,
// and it is capped, so it costs relevance without hiding what it is.
func (s *Service) PremiumNearby(ctx context.Context, lat, lon *float64, radius, limit int, viewer *uuid.UUID) ([]Item, error) {
	if lat == nil || lon == nil {
		return nil, nil
	}
	if limit < 1 || limit > 20 {
		limit = 5
	}
	rows, e := s.db.Query(ctx, `SELECT s.id,coalesce((SELECT display_name FROM store_translations WHERE store_id=s.id AND locale=$5),s.name),s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 ST_Distance(s.location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography),
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$5 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$5),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,s.is_premium,
 EXISTS(SELECT 1 FROM favorites vf WHERE vf.store_id=s.id AND vf.user_id=$4),
 coalesce((SELECT x.attribution->>'photo_name' FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' AND x.attribution ? 'photo_name' AND x.refreshed_at > now()-interval '30 days' LIMIT 1),''),
 coalesce((SELECT array(SELECT jsonb_array_elements_text(x.attribution->'photo_attributions')) FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' AND x.refreshed_at > now()-interval '30 days' LIMIT 1),'{}'),
 coalesce((SELECT m.id::text FROM post_media pm JOIN posts p2 ON p2.id=pm.post_id JOIN media m ON m.id=pm.media_id
  WHERE p2.store_id=s.id AND p2.deleted_at IS NULL AND m.status='ready' ORDER BY p2.created_at DESC, pm.position LIMIT 1),'')
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id
 LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.deleted_at IS NULL AND s.is_premium
 AND ST_DWithin(s.location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography,$3)
 GROUP BY s.id,ss.store_id
 ORDER BY ST_Distance(s.location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography) LIMIT $6`,
		lat, lon, radius, viewer, i18n.FromContext(ctx), limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var x Item
		var photoName, ownMedia string
		var photoAttributions []string
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.DistanceMeters, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.IsPremium, &x.ViewerFavorited, &photoName, &photoAttributions, &ownMedia); e != nil {
			return nil, e
		}
		if photoName != "" {
			x.Photo = &Photo{Name: photoName, Attributions: photoAttributions}
		}
		if ownMedia != "" {
			x.OwnPhoto = &OwnPhoto{MediaID: ownMedia}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SearchByName matches only the store and brand name, deliberately not the city or
// district. The general search indexes those too, which is right for "perde Antalya" but
// useless here: a bare city name would match every store in that city rather than the one
// actually called that.
//
// This exists so that typing part of a store's name finds it. Somebody who remembers
// "güney antalya" should not have to produce "GÜNEY ANTALYA HALI ve YATAK SATIŞ MAĞAZASI"
// word for word, and should certainly not be told the request was not understood.
func (s *Service) SearchByName(ctx context.Context, q string, lat, lon *float64, limit int, viewer *uuid.UUID) ([]Item, error) {
	q = strings.TrimSpace(q)
	if q == "" || limit < 1 {
		return nil, nil
	}
	if limit > 20 {
		limit = 20
	}
	// Three readings of the same words, tried strongest first, because they are not
	// equally good evidence that this is the store somebody meant.
	//
	// All words is the precise reading. Then consecutive phrases, longest first: for
	// "güney antalya home" the phrase "güney antalya" finds GÜNEY ANTALYA HALI ve YATAK
	// SATIŞ MAĞAZASI, which is unmistakably the intended store. Ranking alone could not
	// do this -- "ANTALYA DECOR HOME" also matches two of the three words, so a scattered
	// match scored the same as an exact run of them and won on distance.
	//
	// Any word is the last resort, so a half-remembered name still returns something.
	out, e := s.searchByNameQuery(ctx, "websearch_to_tsquery", q, lat, lon, limit, viewer)
	if e != nil || len(out) > 0 {
		return out, e
	}
	for _, phrase := range PhrasePrefixes(q) {
		out, e = s.searchByNameQuery(ctx, "phraseto_tsquery", phrase, lat, lon, limit, viewer)
		if e != nil || len(out) > 0 {
			return out, e
		}
	}
	if any := anyWordQuery(q); any != "" {
		return s.searchByNameQuery(ctx, "to_tsquery", any, lat, lon, limit, viewer)
	}
	return nil, nil
}

// Consecutive runs of the query words, longest first and at least two words long. A single
// word is left to the any-word pass, where it belongs: one word in common is weak evidence
// and should not outrank a genuine phrase.
func PhrasePrefixes(raw string) []string {
	words := queryWords(raw)
	var out []string
	for length := len(words); length >= 2; length-- {
		for start := 0; start+length <= len(words); start++ {
			out = append(out, strings.Join(words[start:start+length], " "))
		}
	}
	return out
}

func queryWords(raw string) []string {
	words := strings.FieldsFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	kept := make([]string, 0, len(words))
	for _, word := range words {
		if utf8.RuneCountInString(word) > 1 {
			kept = append(kept, word)
		}
	}
	return kept
}

// to_tsquery parses operators, so the words are extracted and rejoined rather than passed
// through. Anything that is not a letter or digit is dropped, which makes injection of
// tsquery syntax impossible by construction.
func anyWordQuery(raw string) string {
	kept := queryWords(raw)
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, " | ")
}

func (s *Service) searchByNameQuery(ctx context.Context, fn, q string, lat, lon *float64, limit int, viewer *uuid.UUID) ([]Item, error) {
	rows, e := s.db.Query(ctx, `SELECT s.id,coalesce((SELECT display_name FROM store_translations WHERE store_id=s.id AND locale=$5),s.name),s.slug,coalesce(s.brand_name,''),coalesce(s.address,''),s.city,coalesce(s.district,''),ST_Y(s.location::geometry),ST_X(s.location::geometry),
 CASE WHEN $2::float8 IS NULL OR $3::float8 IS NULL THEN NULL ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END,
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$5 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$5),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,s.is_premium,
 EXISTS(SELECT 1 FROM favorites vf WHERE vf.store_id=s.id AND vf.user_id=$6),
 coalesce((SELECT x.attribution->>'photo_name' FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' AND x.attribution ? 'photo_name' AND x.refreshed_at > now()-interval '30 days' LIMIT 1),''),
 coalesce((SELECT array(SELECT jsonb_array_elements_text(x.attribution->'photo_attributions')) FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' AND x.refreshed_at > now()-interval '30 days' LIMIT 1),'{}'),
 coalesce((SELECT m.id::text FROM post_media pm JOIN posts p2 ON p2.id=pm.post_id JOIN media m ON m.id=pm.media_id
  WHERE p2.store_id=s.id AND p2.deleted_at IS NULL AND m.status='ready' ORDER BY p2.created_at DESC, pm.position LIMIT 1),'')
 FROM stores s JOIN store_stats ss ON ss.store_id=s.id LEFT JOIN store_category_links l ON l.store_id=s.id LEFT JOIN store_categories c ON c.id=l.category_id
 WHERE s.deleted_at IS NULL AND to_tsvector('simple',coalesce(s.name,'')||' '||coalesce(s.brand_name,'')) @@ `+fn+`('simple',$1)
 GROUP BY s.id,ss.store_id
 ORDER BY ts_rank(to_tsvector('simple',coalesce(s.name,'')||' '||coalesce(s.brand_name,'')),`+fn+`('simple',$1)) DESC,
 CASE WHEN $2::float8 IS NULL THEN 0 ELSE ST_Distance(s.location,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) END LIMIT $4`, q, lat, lon, limit, i18n.FromContext(ctx), viewer)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var x Item
		var photoName, ownMedia string
		var photoAttributions []string
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.DistanceMeters, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.IsPremium, &x.ViewerFavorited, &photoName, &photoAttributions, &ownMedia); e != nil {
			return nil, e
		}
		if photoName != "" {
			x.Photo = &Photo{Name: photoName, Attributions: photoAttributions}
		}
		if ownMedia != "" {
			x.OwnPhoto = &OwnPhoto{MediaID: ownMedia}
		}
		out = append(out, x)
	}
	return out, rows.Err()
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
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$9 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$9),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,s.is_premium,
 EXISTS(SELECT 1 FROM favorites vf WHERE vf.store_id=s.id AND vf.user_id=$6),
 coalesce((SELECT x.attribution->>'photo_name' FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' AND x.attribution ? 'photo_name' AND x.refreshed_at > now()-interval '30 days' LIMIT 1),''),
 coalesce((SELECT array(SELECT jsonb_array_elements_text(x.attribution->'photo_attributions')) FROM store_external_sources x WHERE x.store_id=s.id AND x.provider='google' AND x.refreshed_at > now()-interval '30 days' LIMIT 1),'{}'),
 coalesce((SELECT m.id::text FROM post_media pm JOIN posts p2 ON p2.id=pm.post_id JOIN media m ON m.id=pm.media_id
  WHERE p2.store_id=s.id AND p2.deleted_at IS NULL AND m.status='ready' ORDER BY p2.created_at DESC, pm.position LIMIT 1),'')
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
		var photoName, ownMedia string
		var photoAttributions []string
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.DistanceMeters, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.IsPremium, &x.ViewerFavorited, &photoName, &photoAttributions, &ownMedia); e != nil {
			return nil, e
		}
		if photoName != "" {
			x.Photo = &Photo{Name: photoName, Attributions: photoAttributions}
		}
		if ownMedia != "" {
			x.OwnPhoto = &OwnPhoto{MediaID: ownMedia}
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
 coalesce(array_agg(c.slug) FILTER(WHERE c.slug IS NOT NULL),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c2.slug) FROM store_category_links l2 JOIN store_categories c2 ON c2.id=l2.category_id JOIN store_category_translations t ON t.category_id=c2.id AND t.locale=$3 WHERE l2.store_id=s.id),'{}'),coalesce((SELECT description FROM store_translations WHERE store_id=s.id AND locale=$3),s.description,''),ss.average_rating,ss.rating_count,ss.review_count,ss.favorite_count,ss.post_count,s.is_premium,
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
		if e = rows.Scan(&x.ID, &x.Name, &x.Slug, &x.BrandName, &x.Address, &x.City, &x.District, &x.Latitude, &x.Longitude, &x.Categories, &x.CategoryLabels, &x.LocalizedDescription, &x.Platform.AverageRating, &x.Platform.RatingCount, &x.Platform.ReviewCount, &x.Platform.FavoriteCount, &x.Platform.PostCount, &x.IsPremium, &x.ExternalSources, &savedAt); e != nil {
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
