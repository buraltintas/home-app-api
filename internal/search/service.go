package search

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/observability"
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
	model             string
	locationDecimals  int
	report            *reporting.Service
	attributionWindow time.Duration
	visitorTTL        time.Duration
	now               func() time.Time
	// What we know about our own catalogue, so the model is not asked about it.
	lex *lexicon
}

// Turkish letters are folded to their Latin base rather than dropped. Without this,
// "GÜMÜŞHAN PERDE" became "g-m-han-perde": the store's own name was unreadable in its
// URL, which matters now that the slug is the address a store is shared and indexed by.
func storeSlug(name string, id uuid.UUID) string {
	n := foldLatin(normalizeText(name))
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

// A Turkish address ends the way the post office writes it: "... 07260 Kepez/Antalya,
// Türkiye". The last component before the country is therefore a postcode, a district and
// a province, and it was being stored whole as the city. That is why a store's page was
// titled "BAMBİ YATAK ŞANLIURFA — 63320 Karaköprü/Şanlıurfa": nobody asked for a postcode,
// it simply came along with the province and nothing ever separated them.
//
// This is the country's own address format rather than anything about a particular shop,
// so it holds for a store nobody has looked at, in a town nobody has visited.
func cityFromAddress(address string) string {
	city, _ := CityAndDistrict(address)
	return city
}

func districtFromAddress(address string) string {
	_, district := CityAndDistrict(address)
	return district
}

// CityAndDistrict is exported because the catalogue predates it. Every store imported
// before this parser existed had the whole component stored as its city, and putting them
// right is a local pass over addresses we already hold -- no provider call, no guessing.
func CityAndDistrict(address string) (string, string) {
	parts := strings.Split(address, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		v := strings.TrimSpace(parts[i])
		low := strings.ToLower(v)
		if v == "" || strings.ToUpper(v) == "TR" || low == "türkiye" || low == "turkey" {
			continue
		}
		v = strings.TrimSpace(trimLeadingPostcode(v))
		// "Kepez/Antalya" -- the district comes first, the province last. A component with
		// no slash is the province on its own, which is the normal shape for a province
		// that is also its own centre.
		//
		// Some addresses carry a third level: "Bahtılı Köyü/Kepez/Antalya" is a village
		// inside a district inside a province. Taking everything before the last slash as
		// the district stored "Bahtılı Köyü/Kepez", which is not a district and matches
		// nothing. Only the segment next to the province is the district; anything finer
		// than that belongs to the address, not to a column two stores can be grouped by.
		city, district := v, ""
		if parts := strings.Split(v, "/"); len(parts) > 1 {
			city = strings.TrimSpace(parts[len(parts)-1])
			district = strings.TrimSpace(parts[len(parts)-2])
		}
		if city == "" {
			city = v
			district = ""
		}
		return clamp(city), clamp(district)
	}
	return "Bilinmiyor", ""
}

// Leading digits are a postcode, and a postcode is not part of a place's name.
func trimLeadingPostcode(v string) string {
	i := 0
	for i < len(v) && v[i] >= '0' && v[i] <= '9' {
		i++
	}
	if i == 0 || i == len(v) {
		return v
	}
	return strings.TrimSpace(v[i:])
}

func clamp(v string) string {
	if utf8.RuneCountInString(v) > 100 {
		return string([]rune(v)[:100])
	}
	return v
}

func NewService(db *pgxpool.Pool, stores *storepkg.Service, ai IntentParser, model string, decimals int, report *reporting.Service, attribution, visitorTTL time.Duration) *Service {
	return &Service{db: db, stores: stores, ai: ai, model: model, locationDecimals: decimals, report: report, attributionWindow: attribution, visitorTTL: visitorTTL, now: time.Now, lex: newLexicon(db)}
}
func (s *Service) Search(ctx context.Context, user, visitor *uuid.UUID, in Request) (Response, error) {
	started := time.Now()
	out, err := s.search(ctx, user, visitor, in)
	observability.Search("hybrid", observability.Outcome(err), time.Since(started), len(out.Results))
	return out, err
}

// maxResults is how many stores one search answers with.
//
// It was thirty, chosen when the catalogue was small enough that thirty was most of what
// there was. It is not any more: a chain search in İstanbul has more than thirty branches
// before it leaves the district, and cutting at thirty threw away shops that were nearer
// than ones that survived in another query.
//
// They are returned in one response rather than paged over several. Paging would mean
// re-running the search for each page -- a second classification, a second query, a second
// row in the searches log for one person's one question -- to save a payload that is tens of
// kilobytes. The page reveals them a screenful at a time; nobody waits for the second
// screenful.
const maxResults = 90

func (s *Service) search(ctx context.Context, user, visitor *uuid.UUID, in Request) (Response, error) {
	start := s.now()
	in.Query = strings.TrimSpace(in.Query)
	queryLength := utf8.RuneCountInString(in.Query)
	if queryLength < 2 || queryLength > 500 || (in.Latitude == nil) != (in.Longitude == nil) {
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
		_, e := s.db.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at,locale) VALUES($1,now()+$2::interval,$3)`, id, s.visitorTTL.String(), i18n.FromContext(ctx))
		if e != nil {
			return Response{}, e
		}
		visitor = &id
	} else if visitor != nil {
		_, _ = s.db.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at,locale) VALUES($1,now()+$2::interval,$3) ON CONFLICT(id) DO UPDATE SET last_seen_at=now(),expires_at=greatest(visitor_sessions.expires_at,excluded.expires_at),locale=excluded.locale`, *visitor, s.visitorTTL.String(), i18n.FromContext(ctx))
	}
	requestLocale := i18n.FromContext(ctx)
	intent := Deterministic(in.Query)
	// Whether *we* refused this request, as opposed to the model failing to place it. The
	// two look identical downstream and must not be treated alike: an explicit veto is a
	// decision about a trade we do not carry, while "out of scope" from the model is often
	// just an address it could not read as a request.
	vetoed := intent.Scope == ScopeOutOfScope
	// Our own chains and product words, before anybody's model. "english home" is two
	// English words to a language model and a shop to us; "gardırop" is a wardrobe whether
	// or not a sign says so.
	intent = s.lex.enrich(ctx, intent, in.Query)
	aiUsed := false
	fallback := ""
	// Deterministic out-of-scope matches are deliberate vetoes (for example warehouse,
	// tire shop or a service business). Asking the model to reinterpret them both costs a
	// request and used to let a broad home-living answer put the excluded trade back.
	// The model is asked only about queries we cannot place ourselves. It used to be asked
	// about all of them, including "perde" and "yatak", which it answered with a non-home
	// scope and search terms still filled in -- a shape that fails validation, so those
	// searches paid about three seconds for an answer that was then thrown away in favour
	// of the deterministic one they already had.
	if s.ai != nil && !confident(intent) {
		enriched, e := s.ai.ParseSearchIntent(ctx, in.Query, Context{in.Latitude, in.Longitude, requestLocale})
		invalid := false
		if e == nil {
			if e = Validate(enriched); e != nil {
				invalid = true
			}
		}
		if e == nil {
			intent = merge(intent, enriched)
			intent.StoreName = stripEdgeLocation(intent.StoreName, intent.LocationText)
			// A product is not a store. Left in place it turns an ordinary category search
			// into a name-led one, which abandons the radius filter and ranks by whose sign
			// carries the word rather than by what is nearby.
			if genericStoreName(intent.StoreName) {
				intent.StoreName = ""
			}
			aiUsed = true
			// Asked once, known thereafter. This is the only way the product vocabulary can
			// keep up with what people actually type: a maintained list understood 68 of
			// 108 ordinary Turkish words for things these shops sell, and whatever nobody
			// thinks to add fails quietly. Writing the model's answer back means the first
			// person to search "ankastre" waits for it and nobody after them does.
			s.lex.learn(ctx, intent)
		} else {
			// Every silent degradation here reaches the user as "we did not understand
			// you", so the reason has to survive in the logs. The query is the user's
			// own words and is already stored in searches.
			slog.Default().Warn("search intent parsing failed", "error", e, "model", s.model, "reason", aiFallbackReason(e, invalid), "query", in.Query)
			// The three ways this fails need different fixes -- a missing key, a slow
			// provider, and a model answering off-schema are not the same incident -- and
			// they were previously indistinguishable without log access. Naming them in
			// the response makes a single request enough to tell them apart.
			fallback = aiFallbackReason(e, invalid)
		}
	}
	if !i18n.IsSupported(intent.QueryLanguage) {
		intent.QueryLanguage = requestLocale
	}
	var guidance *Guidance
	var internal []storepkg.Item
	var localElapsed time.Duration
	if intent.Scope == ScopeHomeLiving {
		// A named store is worth finding wherever it is, so the radius filter is dropped
		// for name-led intents. Generic queries keep the near-to-far filter.
		searchRadius := in.RadiusMeters
		if intent.StoreName != "" {
			searchRadius = 0
		}
		localStarted := time.Now()
		var e error
		if internal, e = s.stores.Search(ctx, internalQuery(intent), intent.Categories, intent.LocationText, in.Latitude, in.Longitude, searchRadius, maxResults, user); e != nil {
			return Response{}, e
		}
		localElapsed = time.Since(localStarted)
	} else {
		// Before telling somebody we did not understand them, check whether they named a
		// store we actually carry. "güney antalya" reads as a place rather than a request
		// and was classified out of scope, while GÜNEY ANTALYA HALI ve YATAK SATIŞ
		// MAĞAZASI sat in our own catalogue the whole time. Nobody should have to type a
		// store's full registered name to find it.
		//
		// The rescue is for text we could not place, not for text we refused. "Halı saha"
		// is different: we refused it ourselves, and searching the catalogue for it anyway
		// found every carpet shop whose sign carries "halı".
		var named []storepkg.Item
		if !vetoed {
			localStarted := time.Now()
			var e error
			if named, e = s.stores.SearchByName(ctx, in.Query, in.Latitude, in.Longitude, maxResults, user); e != nil {
				return Response{}, e
			}
			localElapsed = time.Since(localStarted)
		}
		// A catalogue row is not permission to turn an explicit exclusion back into retail.
		// Old imports can retain a stale category until their data migration runs; the
		// trade-wide wording remains authoritative on the request path as well.
		named = slices.DeleteFunc(named, func(item storepkg.Item) bool {
			normalized := normalizeText(item.Name)
			return namesAnExcludedTrade(normalized, foldLatin(normalized))
		})
		if len(named) > 0 {
			internal = named
			intent.Scope = ScopeHomeLiving
			// Recording what this turned out to be keeps the ordering honest downstream:
			// this is a search for a store by name, and is ranked as one.
			intent.StoreName = strings.TrimSpace(in.Query)
		} else {
			guidance = guidanceFor(requestLocale, intent.Scope)
		}
	}
	// Promoted stores are added to the candidate list, not merely sorted within it. Google
	// decides what it returns, so a paid-for store it did not include could never be lifted
	// to the top -- it was not there to lift. Merged before the results are built, and
	// deduplicated, so a promoted store that Google did return is not listed twice.
	if intent.Scope == ScopeHomeLiving && in.Latitude != nil {
		promoted, e := s.stores.PremiumNearby(ctx, in.Latitude, in.Longitude, localHorizonMeters, 5, user)
		if e != nil {
			return Response{}, e
		}
		seen := make(map[uuid.UUID]bool, len(internal))
		for _, x := range internal {
			seen[x.ID] = true
		}
		for _, x := range promoted {
			if !seen[x.ID] {
				internal = append(internal, x)
			}
		}
	}

	results := make([]Result, 0, len(internal))
	for rank, x := range internal {
		results = append(results, fromStore(x, rank))
	}
	// A result that belongs to no category of ours has no business in a list of home and
	// living stores. A store already in the catalogue may have been classified by hand, so
	// its own categories are trusted rather than re-derived.
	kept := results[:0]
	for _, r := range results {
		if len(r.Categories) > 0 && (intent.StoreName != "" || r.Premium || len(intent.Categories) == 0 || categoriesIntersect(r.Categories, intent.Categories)) {
			kept = append(kept, r)
		}
	}
	results = kept

	for i := range results {
		if in.Latitude != nil {
			d := haversine(*in.Latitude, *in.Longitude, results[i].Latitude, results[i].Longitude)
			results[i].DistanceMeters = &d
			penalty := d / 10000
			// An explicit name match should not be buried by distance alone.
			if intent.StoreName != "" && nameMatches(results[i].Name, intent.StoreName) {
				penalty = math.Min(penalty, 3)
				results[i].nameHit = true
			}
			results[i].score -= penalty
		}
	}
	// A named store is worth finding wherever it is -- but only when it is not here.
	//
	// Dropping the horizon for every name search was too broad. Somebody in Antalya
	// searching a chain got its branches in other provinces listed beside the local ones,
	// because each of those branches had been learned from a search made in that province
	// on another day. The name was matched; the distance stopped mattering at all.
	//
	// The rule now: if the name they typed exists inside the horizon, that is the answer
	// and a branch four provinces away is not part of it. Only when nothing nearby carries
	// the name is the nearest one anywhere worth showing -- which is the case the loosened
	// rule was written for in the first place.
	if in.Latitude != nil {
		near := withinLocalHorizon(results)
		if intent.StoreName == "" || containsNameHit(near) {
			results = near
		}
	}
	rankResults(results, in.Latitude != nil, intent.StoreName != "")
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	searchID := uuid.New()
	searchCity, searchDistrict := searchPlace(results)
	intentJSON, _ := json.Marshal(intent)
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return Response{}, e
	}
	defer tx.Rollback(ctx)
	lat, lon := rounded(in.Latitude, s.locationDecimals), rounded(in.Longitude, s.locationDecimals)
	// The provider columns are written as what they now always are. They are kept rather
	// than dropped because they are history: a search recorded last month did use Google,
	// and rewriting that would make the record lie about itself.
	_, e = tx.Exec(ctx, `INSERT INTO searches(id,user_id,visitor_session_id,raw_query,normalized_query,parsed_intent,search_mode,ai_used,ai_provider,ai_model,request_latitude,request_longitude,requested_radius_meters,duration_ms,internal_result_count,external_result_count,total_result_count,fallback_state,location_text,google_places_used,status,query_language,local_only,local_duration_ms,search_city,search_district) VALUES($1,$2,$3,$4,$5,$6,'catalogue',$7,$8,$9,$10,$11,$12,$13,$14,0,$15,$16,$17,false,'completed',$18,true,$19,$20,$21)`, searchID, user, visitor, in.Query, intent.NormalizedQuery, intentJSON, aiUsed, nilIf(!aiUsed, "openai"), nilIf(!aiUsed, s.model), lat, lon, in.RadiusMeters, time.Since(start).Milliseconds(), len(internal), len(results), nilIf(fallback == "", fallback), nilIf(intent.LocationText == "", intent.LocationText), intent.QueryLanguage,
		localElapsed.Milliseconds(), nilIf(searchCity == "", searchCity), nilIf(searchDistrict == "", searchDistrict))
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
		var distance *int
		if r.DistanceMeters != nil {
			v := int(math.Round(*r.DistanceMeters))
			distance = &v
		}
		_, e = tx.Exec(ctx, `INSERT INTO search_results(id,search_id,rank,store_id,source,external_provider,external_place_id,platform_rating_at_time,platform_review_count_at_time,favorite_count_at_time,platform_post_count_at_time,distance_meters,ranking_score,ranking_reason) VALUES($1,$2,$3,$4,$5,NULL,NULL,$6,$7,$8,$9,$10,$11,$12)`, impressionID, searchID, i+1, r.ID, r.Source, rating, reviews, favorites, posts, distance, r.score, "source="+r.Source)
		if e != nil {
			return Response{}, e
		}
	}
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.SearchPerformed, IdempotencyKey: "search:" + searchID.String(), UserID: user, VisitorSessionID: visitor, SearchID: &searchID, Metadata: map[string]any{"ai_used": aiUsed, "scope": intent.Scope, "zero_results": len(results) == 0}}); e != nil {
		return Response{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Response{}, e
	}
	observability.SearchStage("local", localElapsed)
	observability.SearchStage("total", time.Since(start))
	return Response{SearchID: searchID, VisitorSessionID: visitor, Intent: intent, Results: results, Guidance: guidance, FallbackState: fallback}, nil
}

func localContainsStoreName(items []storepkg.Item, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, item := range items {
		if nameMatches(item.Name, name) || nameMatches(item.BrandName, name) {
			return true
		}
	}
	return false
}

func categoriesIntersect(storeCategories, requested []string) bool {
	for _, have := range storeCategories {
		for _, want := range requested {
			if have == want {
				return true
			}
		}
	}
	return false
}

// mapped is what we already know about a place that exists in our own catalogue: its
// community stats, and whether its placement is paid for.
type mappedStore struct {
	Platform       Platform
	Premium        bool
	CatalogStore   bool
	Photo          *Photo
	Categories     []string
	CategoryLabels []string
}

func (s *Service) Interaction(ctx context.Context, searchID uuid.UUID, user, visitor *uuid.UUID, resultID *uuid.UUID, event, key string) error {
	allowed := map[string]bool{"result_impression": true, "result_click": true, "store_open": true, "favorite": true, "unfavorite": true, "review_started": true, "review_created": true, "share": true, "call_click": true}
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
	started := time.Now()
	id, visitorID, err := s.recordInternalSearch(ctx, user, visitor, in, items, elapsed)
	observability.Search("classic", observability.Outcome(err), time.Since(started), len(items))
	return id, visitorID, err
}

func (s *Service) recordInternalSearch(ctx context.Context, user, visitor *uuid.UUID, in Request, items []storepkg.Item, elapsed time.Duration) (uuid.UUID, *uuid.UUID, error) {
	in.Query = strings.TrimSpace(in.Query)
	queryLength := utf8.RuneCountInString(in.Query)
	if queryLength > 500 || (queryLength == 1) || (in.Latitude == nil) != (in.Longitude == nil) {
		return uuid.Nil, visitor, httpapi.ErrInvalidInput
	}
	if in.Latitude != nil && !storepkg.ValidCoordinates(*in.Latitude, *in.Longitude) {
		return uuid.Nil, visitor, httpapi.ErrInvalidInput
	}
	if in.RadiusMeters < 0 || in.RadiusMeters > 50000 {
		return uuid.Nil, visitor, httpapi.ErrInvalidInput
	}
	if user == nil && visitor == nil {
		id := uuid.New()
		visitor = &id
	}
	if visitor != nil {
		if _, e := s.db.Exec(ctx, `INSERT INTO visitor_sessions(id,expires_at,locale) VALUES($1,now()+$2::interval,$3) ON CONFLICT(id) DO UPDATE SET last_seen_at=now(),expires_at=greatest(visitor_sessions.expires_at,excluded.expires_at),locale=excluded.locale`, *visitor, s.visitorTTL.String(), i18n.FromContext(ctx)); e != nil {
			return uuid.Nil, visitor, e
		}
	}
	intent := Deterministic(in.Query)
	if !i18n.IsSupported(intent.QueryLanguage) {
		intent.QueryLanguage = i18n.FromContext(ctx)
	}
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
	_, e = tx.Exec(ctx, `INSERT INTO searches(id,user_id,visitor_session_id,raw_query,normalized_query,parsed_intent,search_mode,request_latitude,request_longitude,requested_radius_meters,duration_ms,internal_result_count,total_result_count,status,query_language) VALUES($1,$2,$3,$4,$5,$6,'classic',$7,$8,$9,$10,$11,$11,'completed',$12)`, id, user, visitor, in.Query, intent.NormalizedQuery, intentJSON, lat, lon, in.RadiusMeters, elapsed.Milliseconds(), len(items), intent.QueryLanguage)
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
	// An explicit trade or service exclusion must not be softened by model enrichment.
	// Unclear wording may still be enriched; a known warehouse or repair query may not.
	if a.Scope == ScopeOutOfScope {
		if i18n.IsSupported(b.QueryLanguage) {
			a.QueryLanguage = b.QueryLanguage
		}
		return a
	}
	if b.Scope == ScopeHomeLiving || (a.Scope != ScopeHomeLiving && (b.Scope == ScopeOutOfScope || b.Scope == ScopeUnclear)) {
		a.Scope = b.Scope
	}
	if i18n.IsSupported(b.QueryLanguage) {
		a.QueryLanguage = b.QueryLanguage
	}
	if b.StoreName != "" {
		a.StoreName = b.StoreName
	}
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
	for _, v := range b.SemanticTerms {
		a.SemanticTerms = appendUnique(a.SemanticTerms, v)
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
func internalQuery(i Intent) string {
	terms := make([]string, 0, 1+len(i.ProductTerms)+len(i.SemanticTerms))
	if i.StoreName != "" {
		terms = append(terms, i.StoreName)
	}
	terms = append(terms, i.ProductTerms...)
	terms = append(terms, i.SemanticTerms...)
	if len(terms) == 0 {
		return i.NormalizedQuery
	}
	return strings.Join(terms, " OR ")
}

// aiFallbackReason names why the parser did not answer, without leaking provider detail
// into a public response.
func aiFallbackReason(e error, invalid bool) string {
	switch {
	case invalid:
		return "ai_invalid_response"
	case isTimeout(e):
		return "ai_timeout"
	case isAuthFailure(e):
		return "ai_unauthorized"
	default:
		return "ai_unavailable"
	}
}

// A deadline that arrives wrapped by the provider SDK is still a deadline. Matching only
// the sentinel let a plain timeout fall through and be reported as an unreachable network,
// which sends whoever is debugging it to the wrong setting entirely.
func isTimeout(e error) bool {
	if errors.Is(e, context.DeadlineExceeded) || os.IsTimeout(e) {
		return true
	}
	text := strings.ToLower(e.Error())
	return strings.Contains(text, "deadline exceeded") || strings.Contains(text, "timeout") || strings.Contains(text, "timed out")
}

// The provider returns its status in the error text; 401 and 429 are the two that mean
// "the deployment is misconfigured" rather than "the network wobbled".
func isAuthFailure(e error) bool {
	text := strings.ToLower(e.Error())
	return strings.Contains(text, "401") || strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "invalid_api_key") || strings.Contains(text, "429") ||
		strings.Contains(text, "quota") || strings.Contains(text, "insufficient_quota")
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

// How long a provider answer stands. Short enough that a shop opening today is found
// today, long enough that a query typed twice in an afternoon is paid for once.
const placesCacheTTL = 6 * time.Hour

// searchPlace is where a search effectively happened: the city and district of its nearest
// result. Recorded on the search so that "what do people look for in Konyaaltı" is a
// question the reporting can answer without re-deriving it from coordinates.
func searchPlace(results []Result) (string, string) {
	nearest := math.MaxFloat64
	city, district := "", ""
	for _, r := range results {
		if r.DistanceMeters != nil && *r.DistanceMeters < nearest && strings.TrimSpace(r.City) != "" {
			nearest, city, district = *r.DistanceMeters, strings.TrimSpace(r.City), strings.TrimSpace(r.District)
		}
	}
	return city, district
}
