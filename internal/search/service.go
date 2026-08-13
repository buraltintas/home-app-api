package search

import (
	"context"
	"encoding/json"
	"math"
	"sort"
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
	db                *pgxpool.Pool
	stores            *storepkg.Service
	ai                IntentParser
	places            PlacesProvider
	model             string
	locationDecimals  int
	report            *reporting.Service
	attributionWindow time.Duration
	now               func() time.Time
}

func (s *Service) MaterializeGoogleStore(ctx context.Context, placeID string) (uuid.UUID, error) {
	placeID = strings.TrimSpace(placeID)
	if placeID == "" || len(placeID) > 300 {
		return uuid.Nil, httpapi.ErrInvalidInput
	}
	if s.places == nil {
		return uuid.Nil, httpapi.E(503, "PLACES_NOT_CONFIGURED", "Store provider is not configured")
	}
	p, err := s.places.PlaceDetails(ctx, placeID)
	if err != nil {
		return uuid.Nil, httpapi.E(502, "PLACES_UNAVAILABLE", "Store provider is temporarily unavailable")
	}
	if p.PlaceID != placeID || strings.TrimSpace(p.Name) == "" || !storepkg.ValidCoordinates(p.Latitude, p.Longitude) {
		return uuid.Nil, httpapi.E(422, "INVALID_EXTERNAL_STORE", "The external store could not be verified")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('google:'||$1,0))`, placeID); err != nil {
		return uuid.Nil, err
	}
	var existing uuid.UUID
	err = tx.QueryRow(ctx, `SELECT store_id FROM store_external_sources WHERE provider='google' AND external_id=$1`, placeID).Scan(&existing)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}
	id := uuid.New()
	slug := storeSlug(p.Name, id)
	_, err = tx.Exec(ctx, `INSERT INTO stores(id,name,slug,address,city,district,location) VALUES($1,$2,$3,$4,$5,'',ST_SetSRID(ST_MakePoint($7,$6),4326)::geography)`, id, p.Name, slug, p.Address, cityFromAddress(p.Address), p.Latitude, p.Longitude)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO store_stats(store_id) VALUES($1)`, id); err != nil {
		return uuid.Nil, err
	}
	attr, _ := json.Marshal(map[string]any{"provider": "Google", "attributions": p.Attributions})
	if _, err = tx.Exec(ctx, `INSERT INTO store_external_sources(store_id,provider,external_id,attribution,refreshed_at) VALUES($1,'google',$2,$3,now())`, id, placeID, attr); err != nil {
		return uuid.Nil, err
	}
	if _, err = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.StoreImportedGoogle, IdempotencyKey: "google-store:" + placeID, StoreID: &id}); err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}

func storeSlug(name string, id uuid.UUID) string {
	n := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	dash := false
	for _, r := range n {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
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
func cityFromAddress(address string) string {
	parts := strings.Split(address, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		v := strings.TrimSpace(parts[i])
		low := strings.ToLower(v)
		if v != "" && strings.ToUpper(v) != "TR" && low != "türkiye" && low != "turkey" {
			if len(v) > 100 {
				v = v[:100]
			}
			return v
		}
	}
	return "Bilinmiyor"
}

func NewService(db *pgxpool.Pool, stores *storepkg.Service, ai IntentParser, places PlacesProvider, model string, decimals int, report *reporting.Service, attribution time.Duration) *Service {
	return &Service{db, stores, ai, places, model, decimals, report, attribution, time.Now}
}
func (s *Service) Search(ctx context.Context, user, visitor *uuid.UUID, in Request) (Response, error) {
	start := s.now()
	in.Query = strings.TrimSpace(in.Query)
	if len(in.Query) < 2 || len(in.Query) > 500 || (in.Latitude == nil) != (in.Longitude == nil) {
		return Response{}, httpapi.ErrInvalidInput
	}
	if in.Latitude != nil && !storepkg.ValidCoordinates(*in.Latitude, *in.Longitude) {
		return Response{}, httpapi.ErrInvalidInput
	}
	if in.RadiusMeters == 0 {
		in.RadiusMeters = 10000
	}
	if in.RadiusMeters < 100 || in.RadiusMeters > 50000 {
		return Response{}, httpapi.ErrInvalidInput
	}
	if user == nil && visitor == nil {
		id := uuid.New()
		_, e := s.db.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at) VALUES($1,now()+interval '180 days')`, id)
		if e != nil {
			return Response{}, e
		}
		visitor = &id
	} else if visitor != nil {
		_, _ = s.db.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at) VALUES($1,now()+interval '180 days') ON CONFLICT(id) DO UPDATE SET last_seen_at=now()`, *visitor)
	}
	intent := Deterministic(in.Query)
	aiUsed := false
	fallback := ""
	if s.ai != nil {
		enriched, e := s.ai.ParseSearchIntent(ctx, in.Query, Context{in.Latitude, in.Longitude, "tr-TR"})
		if e == nil && Validate(enriched) == nil {
			intent = merge(intent, enriched)
			aiUsed = true
		} else {
			fallback = "ai_unavailable_or_invalid"
		}
	}
	internal, e := s.stores.Search(ctx, intent.NormalizedQuery, in.Latitude, in.Longitude, in.RadiusMeters, 20, user)
	if e != nil {
		return Response{}, e
	}
	results := make([]Result, 0, len(internal)+20)
	localIDs := map[uuid.UUID]bool{}
	for _, x := range internal {
		r := fromStore(x)
		results = append(results, r)
		localIDs[x.ID] = true
	}
	var external []Place
	googleUsed := false
	if s.places != nil {
		googleUsed = true
		external, e = s.places.TextSearch(ctx, in.Query, in.Latitude, in.Longitude, in.RadiusMeters)
		if e != nil {
			fallback = joinFallback(fallback, "places_unavailable")
			external = nil
		}
	}
	mapped, e := s.lookupExternal(ctx, external)
	if e != nil {
		return Response{}, e
	}
	for _, p := range external {
		if m, ok := mapped[p.PlaceID]; ok {
			if localIDs[m.StoreID] {
				for i := range results {
					if results[i].ID != nil && *results[i].ID == m.StoreID {
						results[i].Google = &External{Provider: "google", PlaceID: p.PlaceID, Rating: p.Rating, RatingCount: p.RatingCount}
						results[i].Source = "google+platform"
						results[i].externalPlaceID = p.PlaceID
					}
				}
				continue
			}
			id := m.StoreID
			results = append(results, Result{ID: &id, Source: "google+platform", Name: p.Name, Address: p.Address, Latitude: p.Latitude, Longitude: p.Longitude, Platform: &m, Google: &External{Provider: "google", PlaceID: p.PlaceID, Rating: p.Rating, RatingCount: p.RatingCount}, score: 80 + float64(m.ReviewCount), externalPlaceID: p.PlaceID})
			localIDs[id] = true
		} else {
			results = append(results, Result{Source: "google", Name: p.Name, Address: p.Address, Latitude: p.Latitude, Longitude: p.Longitude, Google: &External{Provider: "google", PlaceID: p.PlaceID, Rating: p.Rating, RatingCount: p.RatingCount}, score: 50 + p.Rating, externalPlaceID: p.PlaceID})
		}
	}
	for i := range results {
		if in.Latitude != nil {
			d := haversine(*in.Latitude, *in.Longitude, results[i].Latitude, results[i].Longitude)
			results[i].DistanceMeters = &d
			results[i].score -= d / 10000
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].score > results[j].score })
	if len(results) > 30 {
		results = results[:30]
	}
	searchID := uuid.New()
	intentJSON, _ := json.Marshal(intent)
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return Response{}, e
	}
	defer tx.Rollback(ctx)
	lat, lon := rounded(in.Latitude, s.locationDecimals), rounded(in.Longitude, s.locationDecimals)
	_, e = tx.Exec(ctx, `INSERT INTO searches(id,user_id,visitor_session_id,raw_query,normalized_query,parsed_intent,search_mode,ai_used,ai_provider,ai_model,request_latitude,request_longitude,requested_radius_meters,duration_ms,internal_result_count,external_result_count,total_result_count,fallback_state,location_text,google_places_used,status) VALUES($1,$2,$3,$4,$5,$6,'hybrid',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'completed')`, searchID, user, visitor, in.Query, intent.NormalizedQuery, intentJSON, aiUsed, nilIf(!aiUsed, "openai"), nilIf(!aiUsed, s.model), lat, lon, in.RadiusMeters, time.Since(start).Milliseconds(), len(internal), len(external), len(results), nilIf(fallback == "", fallback), nilIf(intent.LocationText == "", intent.LocationText), googleUsed)
	if e != nil {
		return Response{}, e
	}
	for i := range results {
		r := results[i]
		impressionID := uuid.New()
		results[i].ImpressionID = impressionID
		var rating *float64
		var reviews, favorites, posts *int
		if r.Platform != nil {
			rating = &r.Platform.AverageRating
			reviews = &r.Platform.ReviewCount
			favorites = &r.Platform.FavoriteCount
			posts = &r.Platform.PostCount
		}
		var provider, place any
		if r.Google != nil {
			provider = "google"
			place = r.Google.PlaceID
		}
		var distance *int
		if r.DistanceMeters != nil {
			v := int(math.Round(*r.DistanceMeters))
			distance = &v
		}
		_, e = tx.Exec(ctx, `INSERT INTO search_results(id,search_id,rank,store_id,source,external_provider,external_place_id,platform_rating_at_time,platform_review_count_at_time,favorite_count_at_time,platform_post_count_at_time,distance_meters,ranking_score,ranking_reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, impressionID, searchID, i+1, r.ID, r.Source, provider, place, rating, reviews, favorites, posts, distance, r.score, "source="+r.Source)
		if e != nil {
			return Response{}, e
		}
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.SearchPerformed, IdempotencyKey: "search:" + searchID.String(), UserID: user, VisitorSessionID: visitor, SearchID: &searchID, Metadata: map[string]any{"ai_used": aiUsed, "google_places_used": googleUsed, "zero_results": len(results) == 0}}); e != nil {
		return Response{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Response{}, e
	}
	return Response{searchID, visitor, intent, results, fallback}, nil
}
func (s *Service) lookupExternal(ctx context.Context, places []Place) (map[string]Platform, error) {
	ids := make([]string, 0, len(places))
	for _, p := range places {
		ids = append(ids, p.PlaceID)
	}
	out := map[string]Platform{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, e := s.db.Query(ctx, `SELECT x.external_id,s.id,ss.average_rating,ss.review_count,ss.favorite_count,ss.post_count FROM store_external_sources x JOIN stores s ON s.id=x.store_id AND s.deleted_at IS NULL JOIN store_stats ss ON ss.store_id=s.id WHERE x.provider='google' AND x.external_id=ANY($1)`, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p Platform
		if e = rows.Scan(&id, &p.StoreID, &p.AverageRating, &p.ReviewCount, &p.FavoriteCount, &p.PostCount); e != nil {
			return nil, e
		}
		out[id] = p
	}
	return out, rows.Err()
}
func (s *Service) Interaction(ctx context.Context, searchID uuid.UUID, user, visitor *uuid.UUID, resultID *uuid.UUID, event, key string) error {
	allowed := map[string]bool{"result_impression": true, "result_click": true, "store_open": true, "favorite": true, "unfavorite": true, "review_started": true, "review_created": true, "share": true}
	if !allowed[event] {
		return httpapi.ErrInvalidInput
	}
	var owned bool
	e := s.db.QueryRow(ctx, `WITH owned AS (SELECT s.id search_id,r.id result_id,r.store_id FROM searches s LEFT JOIN search_results r ON r.id=$4 AND r.search_id=s.id WHERE s.id=$1 AND (($2::uuid IS NOT NULL AND s.user_id=$2) OR ($2::uuid IS NULL AND $3::uuid IS NOT NULL AND s.visitor_session_id=$3)) AND ($4::uuid IS NULL OR r.id IS NOT NULL)),ins AS (INSERT INTO search_interactions(search_id,search_result_id,user_id,visitor_session_id,store_id,event_type,idempotency_key) SELECT search_id,result_id,$2,$3,store_id,$5,nullif($6,'') FROM owned ON CONFLICT DO NOTHING) SELECT EXISTS(SELECT 1 FROM owned)`, searchID, user, visitor, resultID, event, key).Scan(&owned)
	if e != nil {
		return e
	}
	if !owned {
		return httpapi.E(404, "SEARCH_NOT_FOUND", "Search not found")
	}
	return nil
}

func (s *Service) Attribute(ctx context.Context, searchID, resultID, user, store uuid.UUID, event, key string) error {
	if event != "favorite" && event != "unfavorite" && event != "review_created" && event != "review_started" {
		return httpapi.ErrInvalidInput
	}
	var valid bool
	e := s.db.QueryRow(ctx, `WITH valid AS (SELECT s.id search_id,r.id result_id FROM searches s JOIN search_results r ON r.id=$2 AND r.search_id=s.id WHERE s.id=$1 AND s.user_id=$3 AND (r.store_id=$4 OR EXISTS(SELECT 1 FROM store_external_sources x WHERE x.store_id=$4 AND x.provider=r.external_provider AND x.external_id=r.external_place_id)) AND s.created_at>=now()-$7::interval),ins AS (INSERT INTO search_interactions(search_id,search_result_id,user_id,store_id,event_type,idempotency_key) SELECT search_id,result_id,$3,$4,$5,$6 FROM valid ON CONFLICT DO NOTHING) SELECT EXISTS(SELECT 1 FROM valid)`, searchID, resultID, user, store, event, key, s.attributionWindow.String()).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return httpapi.E(422, "SEARCH_ATTRIBUTION_INVALID", "Search attribution is invalid or expired")
	}
	return nil
}

func (s *Service) RecordInternalSearch(ctx context.Context, user, visitor *uuid.UUID, in Request, items []storepkg.Item, elapsed time.Duration) (uuid.UUID, *uuid.UUID, error) {
	if user == nil && visitor == nil {
		id := uuid.New()
		visitor = &id
	}
	if visitor != nil {
		if _, e := s.db.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at) VALUES($1,now()+interval '180 days') ON CONFLICT(id) DO UPDATE SET last_seen_at=now()`, *visitor); e != nil {
			return uuid.Nil, visitor, e
		}
	}
	intent := Deterministic(in.Query)
	if intent.NormalizedQuery == "" {
		intent.NormalizedQuery = "nearby"
	}
	intentJSON, _ := json.Marshal(intent)
	id := uuid.New()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return uuid.Nil, visitor, e
	}
	defer tx.Rollback(ctx)
	lat, lon := rounded(in.Latitude, s.locationDecimals), rounded(in.Longitude, s.locationDecimals)
	_, e = tx.Exec(ctx, `INSERT INTO searches(id,user_id,visitor_session_id,raw_query,normalized_query,parsed_intent,search_mode,request_latitude,request_longitude,requested_radius_meters,duration_ms,internal_result_count,total_result_count,status) VALUES($1,$2,$3,$4,$5,$6,'classic',$7,$8,$9,$10,$11,$11,'completed')`, id, user, visitor, in.Query, intent.NormalizedQuery, intentJSON, lat, lon, in.RadiusMeters, elapsed.Milliseconds(), len(items))
	if e != nil {
		return uuid.Nil, visitor, e
	}
	for i, item := range items {
		_, e = tx.Exec(ctx, `INSERT INTO search_results(search_id,rank,store_id,source,platform_rating_at_time,platform_review_count_at_time,favorite_count_at_time,platform_post_count_at_time,distance_meters,ranking_reason) VALUES($1,$2,$3,'internal',$4,$5,$6,$7,$8,'classic_internal')`, id, i+1, item.ID, item.Platform.AverageRating, item.Platform.ReviewCount, item.Platform.FavoriteCount, item.Platform.PostCount, roundedDistance(item.DistanceMeters))
		if e != nil {
			return uuid.Nil, visitor, e
		}
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.SearchPerformed, IdempotencyKey: "search:" + id.String(), UserID: user, VisitorSessionID: visitor, SearchID: &id, Metadata: map[string]any{"zero_results": len(items) == 0}}); e != nil {
		return uuid.Nil, visitor, e
	}
	return id, visitor, tx.Commit(ctx)
}

func roundedDistance(v *float64) *int {
	if v == nil {
		return nil
	}
	n := int(math.Round(*v))
	return &n
}
func merge(a, b Intent) Intent {
	if b.LocationText != "" {
		a.LocationText = b.LocationText
	}
	for _, v := range b.Categories {
		a.Categories = appendUnique(a.Categories, v)
	}
	for _, v := range b.ProductTerms {
		a.ProductTerms = appendUnique(a.ProductTerms, v)
	}
	for _, v := range b.StyleTerms {
		a.StyleTerms = appendUnique(a.StyleTerms, v)
	}
	for _, v := range b.Attributes {
		a.Attributes = appendUnique(a.Attributes, v)
	}
	if b.PriceIntent != "" {
		a.PriceIntent = b.PriceIntent
	}
	if b.SortPreference != "" {
		a.SortPreference = b.SortPreference
	}
	return a
}
func rounded(p *float64, d int) any {
	if p == nil {
		return nil
	}
	m := math.Pow10(d)
	return math.Round(*p*m) / m
}
func nilIf(cond bool, v any) any {
	if cond {
		return nil
	}
	return v
}
func joinFallback(a, b string) string {
	if a == "" {
		return b
	}
	return a + "," + b
}
func haversine(a, b, c, d float64) float64 {
	const r = 6371000
	la1, la2 := a*math.Pi/180, c*math.Pi/180
	dl := (c - a) * math.Pi / 180
	dn := (d - b) * math.Pi / 180
	x := math.Sin(dl/2)*math.Sin(dl/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dn/2)*math.Sin(dn/2)
	return 2 * r * math.Atan2(math.Sqrt(x), math.Sqrt(1-x))
}

var _ = pgx.ErrNoRows
