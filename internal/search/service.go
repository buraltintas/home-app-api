package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
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
	"golang.org/x/sync/errgroup"
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
	visitorTTL        time.Duration
	now               func() time.Time
	policy            SufficiencyPolicy
	// sample draws the shadow measurement lottery. A field rather than a direct call to
	// the random number generator so a test can decide the outcome.
	sample func() float64
}

// UseSufficiencyPolicy installs the local-first search policy. Nothing calls the gate
// until this is given a policy with Enabled set, so a service built without it behaves
// exactly as it did before the gate existed.
func (s *Service) UseSufficiencyPolicy(p SufficiencyPolicy) { s.policy = p }

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
	ids, err := s.materializePlaces(ctx, []Place{p})
	if err != nil {
		return uuid.Nil, err
	}
	id, ok := ids[placeID]
	if !ok {
		return uuid.Nil, httpapi.E(422, "INVALID_EXTERNAL_STORE", "The external store could not be verified")
	}
	return id, nil
}

// materializePlaces imports every valid place into stores and returns the store id
// for each place id. Import is idempotent: a place that already has a
// store_external_sources row resolves to the existing store.
func (s *Service) materializePlaces(ctx context.Context, places []Place) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	pending := make([]Place, 0, len(places))
	seen := map[string]bool{}
	for _, p := range places {
		id := strings.TrimSpace(p.PlaceID)
		if id == "" || len(id) > 300 || seen[id] || strings.TrimSpace(p.Name) == "" || !storepkg.ValidCoordinates(p.Latitude, p.Longitude) {
			continue
		}
		p.PlaceID = id
		seen[id] = true
		pending = append(pending, p)
	}
	if len(pending) == 0 {
		return out, nil
	}
	// Deterministic lock order keeps concurrent searches over overlapping result
	// sets from deadlocking on pg_advisory_xact_lock.
	sort.Slice(pending, func(i, j int) bool { return pending[i].PlaceID < pending[j].PlaceID })
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, p := range pending {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('google:'||$1,0))`, p.PlaceID); err != nil {
			return nil, err
		}
		// The store detail page reads its Google block straight out of this jsonb, so
		// the provider figures are kept here rather than mixed into store_stats, which
		// holds community data only.
		attribution := map[string]any{"provider": "Google", "attributions": p.Attributions}
		if p.RatingCount > 0 {
			attribution["rating"] = p.Rating
			attribution["rating_count"] = p.RatingCount
		}
		if p.PhotoName != "" {
			attribution["photo_name"] = p.PhotoName
			attribution["photo_attributions"] = p.PhotoAttributions
		}
		if p.Phone != "" {
			attribution["phone"] = p.Phone
		}
		if p.BusinessStatus != "" && p.BusinessStatus != "BUSINESS_STATUS_UNSPECIFIED" {
			attribution["business_status"] = p.BusinessStatus
		}
		if p.Hours != nil {
			attribution["opening_hours"] = p.Hours
		}
		// Kept because we could not get them back. A store's categories are worked out
		// once, at import, from these types and its name -- and when the classifier later
		// learned to read something it could not read before, there was no way to apply
		// that to the stores already here without asking Google about every one of them
		// again. Storing what the provider already told us costs nothing and makes
		// reclassification a local operation.
		if len(p.Types) > 0 {
			attribution["types"] = p.Types
		}
		attr, _ := json.Marshal(attribution)
		var existing uuid.UUID
		err = tx.QueryRow(ctx, `SELECT store_id FROM store_external_sources WHERE provider='google' AND external_id=$1`, p.PlaceID).Scan(&existing)
		if err == nil {
			// Ratings and photos move, and this place was just fetched, so the stored
			// copy is refreshed rather than left to age indefinitely.
			if _, err = tx.Exec(ctx, `UPDATE store_external_sources SET attribution=$2,refreshed_at=now() WHERE store_id=$1 AND provider='google'`, existing, attr); err != nil {
				return nil, err
			}
			// A number we have never held is filled in; one we already hold is left alone,
			// because a store that told us its own number directly knows it better than
			// the directory does.
			if p.Phone != "" {
				if _, err = tx.Exec(ctx, `UPDATE stores SET phone=$2 WHERE id=$1 AND coalesce(phone,'')=''`, existing, p.Phone); err != nil {
					return nil, err
				}
			}
			if p.Website != "" {
				if _, err = tx.Exec(ctx, `UPDATE stores SET website=$2 WHERE id=$1 AND coalesce(website,'')=''`, existing, p.Website); err != nil {
					return nil, err
				}
			}
			out[p.PlaceID] = existing
			continue
		}
		if err != pgx.ErrNoRows {
			return nil, err
		}
		// A place that classifies into none of our categories is not a store for this
		// product. The classifier already says so -- it returns nothing for a service, a
		// workshop, a bakery -- and until now that verdict was ignored at exactly the
		// moment it mattered: the row was written anyway, with no categories, and stayed
		// in the catalogue. That is how a search for a bed brand came back with a bakery
		// whose name merely resembled it. Provider matching is fuzzy by design; deciding
		// what belongs here is ours to do, and this is where we do it.
		categories := StoreCategories(p.Name, p.Types)
		if len(categories) == 0 {
			continue
		}
		id := uuid.New()
		slug := storeSlug(p.Name, id)
		if _, err = tx.Exec(ctx, `INSERT INTO stores(id,name,slug,address,city,district,location,phone,website) VALUES($1,$2,$3,$4,$5,$10,ST_SetSRID(ST_MakePoint($7,$6),4326)::geography,nullif($8,''),nullif($9,''))`, id, p.Name, slug, p.Address, cityFromAddress(p.Address), p.Latitude, p.Longitude, p.Phone, p.Website, districtFromAddress(p.Address)); err != nil {
			return nil, err
		}
		// A store now carries the categories it actually sells, worked out from its own
		// Google types and its own name. Before this an imported store had none at all, and
		// the categories shown beside a result came from the search that found it.
		{
			if _, err = tx.Exec(ctx, `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug=ANY($2) AND active ON CONFLICT DO NOTHING`, id, categories); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO store_stats(store_id) VALUES($1)`, id); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO store_external_sources(store_id,provider,external_id,attribution,refreshed_at) VALUES($1,'google',$2,$3,now())`, id, p.PlaceID, attr); err != nil {
			return nil, err
		}
		if _, err = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.StoreImportedGoogle, IdempotencyKey: "google-store:" + p.PlaceID, StoreID: &id}); err != nil {
			return nil, err
		}
		out[p.PlaceID] = id
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// PhotoProvider is implemented by places providers that can stream place photos.
type PhotoProvider interface {
	PhotoMedia(ctx context.Context, name string, maxWidth int) (io.ReadCloser, string, error)
}

// PlacePhoto streams a provider photo by resource name. The caller must close the reader.
func (s *Service) PlacePhoto(ctx context.Context, name string, maxWidth int) (io.ReadCloser, string, error) {
	if !ValidPhotoName(name) {
		return nil, "", httpapi.ErrInvalidInput
	}
	provider, ok := s.places.(PhotoProvider)
	if !ok || s.places == nil {
		return nil, "", httpapi.E(503, "PLACES_NOT_CONFIGURED", "Store provider is not configured")
	}
	body, contentType, err := provider.PhotoMedia(ctx, name, maxWidth)
	if err != nil {
		return nil, "", httpapi.E(502, "PLACES_UNAVAILABLE", "Store provider is temporarily unavailable")
	}
	return body, contentType, nil
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

func NewService(db *pgxpool.Pool, stores *storepkg.Service, ai IntentParser, places PlacesProvider, model string, decimals int, report *reporting.Service, attribution, visitorTTL time.Duration) *Service {
	return &Service{db: db, stores: stores, ai: ai, places: places, model: model, locationDecimals: decimals, report: report, attributionWindow: attribution, visitorTTL: visitorTTL, now: time.Now, sample: rand.Float64}
}
func (s *Service) Search(ctx context.Context, user, visitor *uuid.UUID, in Request) (Response, error) {
	started := time.Now()
	out, err := s.search(ctx, user, visitor, in)
	observability.Search("hybrid", observability.Outcome(err), time.Since(started), len(out.Results))
	return out, err
}

func (s *Service) ResolveLocations(ctx context.Context, query string, limit int, lat, lon *float64) ([]LocationResult, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 120 || limit < 1 || limit > 10 {
		return nil, httpapi.ErrInvalidInput
	}
	if s.places == nil {
		return nil, httpapi.E(503, "PLACES_NOT_CONFIGURED", "Location provider is not configured")
	}
	// Autocomplete where the provider offers it. Text search was the wrong tool for this
	// field: it matches whole tokens, so "unca" returned nothing while "Uncalı" worked,
	// and with no country restriction "Bos" answered with a village in Belgium.
	var places []Place
	var err error
	if auto, ok := s.places.(AutocompleteProvider); ok {
		places, err = auto.Autocomplete(ctx, query, i18n.FromContext(ctx), lat, lon)
		// Google can interpret “Uluç Mahallesi” as the establishment named “Uluç
		// Mahallesi Muhtarlığı” while the shorter “Uluç” returns the actual
		// administrative area. Retry a conservative Turkish neighbourhood suffix only
		// when the first response has no usable area or public landmark at all.
		if err == nil && !containsLocationAnchor(places) {
			if fallback := locationAutocompleteFallback(query); fallback != query {
				places, err = auto.Autocomplete(ctx, fallback, i18n.FromContext(ctx), lat, lon)
			}
		}
	} else if localized, ok := s.places.(LocalizedPlacesProvider); ok {
		places, err = localized.TextSearchLocalized(ctx, query, nil, nil, 50000, i18n.FromContext(ctx))
	} else {
		places, err = s.places.TextSearch(ctx, query, nil, nil, 50000)
	}
	if err != nil {
		return nil, httpapi.E(502, "PLACES_UNAVAILABLE", "Location provider is temporarily unavailable")
	}
	results := make([]LocationResult, 0, limit)
	for _, place := range places {
		// Coordinates are deliberately not required here. A prediction does not carry
		// any, and the one the person picks is resolved through ResolveLocationPlace --
		// so the coordinates a search is run against are always fetched by us, never
		// taken from the client.
		if !isLocationAnchor(place.Types) || strings.TrimSpace(place.PlaceID) == "" || strings.TrimSpace(place.Name) == "" {
			continue
		}
		results = append(results, LocationResult{Provider: "google", PlaceID: place.PlaceID, Name: place.Name, Address: place.Address, Latitude: place.Latitude, Longitude: place.Longitude, Types: append([]string{}, place.Types...), Attributions: append([]string{}, place.Attributions...)})
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

// ResolveLocationPlace re-fetches a manually selected location so clients never
// need to submit or trust user-entered coordinates.
func (s *Service) ResolveLocationPlace(ctx context.Context, placeID string) (LocationResult, error) {
	placeID = strings.TrimSpace(placeID)
	if placeID == "" || utf8.RuneCountInString(placeID) > 300 {
		return LocationResult{}, httpapi.ErrInvalidInput
	}
	if s.places == nil {
		return LocationResult{}, httpapi.E(503, "PLACES_NOT_CONFIGURED", "Location provider is not configured")
	}
	place, err := s.places.PlaceDetails(ctx, placeID)
	if err != nil {
		return LocationResult{}, httpapi.E(502, "PLACES_UNAVAILABLE", "Location provider is temporarily unavailable")
	}
	if place.PlaceID != placeID || strings.TrimSpace(place.Name) == "" || !isLocationAnchor(place.Types) || !storepkg.ValidCoordinates(place.Latitude, place.Longitude) {
		return LocationResult{}, httpapi.E(422, "INVALID_LOCATION", "The selected location could not be verified")
	}
	return LocationResult{Provider: "google", PlaceID: place.PlaceID, Name: strings.TrimSpace(place.Name), Address: strings.TrimSpace(place.Address), Latitude: place.Latitude, Longitude: place.Longitude, Types: append([]string{}, place.Types...), Attributions: append([]string{}, place.Attributions...)}, nil
}

func isLocationAnchor(types []string) bool {
	// A manually entered search origin is not a profile address and it is not visit proof.
	// It only needs to be an unambiguous point people use to describe where they are. Google
	// classifies ferry, rail and bus terminals as establishments/POIs, so rejecting every
	// POI made valid landmark choices appear in autocomplete and then fail on selection.
	// Accept provider-typed public location anchors, while ordinary shops and businesses
	// still have no accepted type and therefore remain outside this location picker.
	providerPOI := false
	for _, placeType := range types {
		switch placeType {
		case "airport", "bus_station", "ferry_terminal", "light_rail_station", "subway_station", "train_station", "transit_station":
			return true
		case "establishment", "point_of_interest":
			providerPOI = true
		}
	}
	if providerPOI {
		return false
	}
	for _, placeType := range types {
		switch placeType {
		case "administrative_area_level_1", "administrative_area_level_2", "administrative_area_level_3", "administrative_area_level_4", "locality", "neighborhood", "postal_town", "sublocality", "sublocality_level_1", "sublocality_level_2":
			return true
		// A street name is where somebody is standing. Refusing it meant "İsmet Gökşen"
		// found nothing while the neighbourhood around it found plenty.
		case "route", "street_address", "premise":
			return true
		}
	}
	return false
}

func containsLocationAnchor(places []Place) bool {
	for _, place := range places {
		if isLocationAnchor(place.Types) {
			return true
		}
	}
	return false
}

func locationAutocompleteFallback(query string) string {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), "."))
	lower := strings.ToLower(trimmed)
	for _, suffix := range []string{" mahallesi", " mahalle", " mah"} {
		if strings.HasSuffix(lower, suffix) {
			fallback := strings.TrimSpace(trimmed[:len(trimmed)-len(suffix)])
			if utf8.RuneCountInString(fallback) >= 2 {
				return fallback
			}
		}
	}
	return query
}

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
	aiUsed := false
	fallback := ""
	if s.ai != nil {
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
	var external []Place
	googleUsed := false
	// What the gate saw and what it decided, recorded on the search either way. While the
	// flag is off this says "gate_disabled", which is itself worth being able to count.
	decision := gateDecision{Reasons: []string{reasonGateDisabled}}
	observed := sufficiency{Coverage: coverageUnknown}
	var localElapsed, placesElapsed time.Duration
	if intent.Scope == ScopeHomeLiving {
		group, providerContext := errgroup.WithContext(ctx)
		// A named store is worth finding wherever it is, so the radius filter is
		// dropped for name-led intents. Generic queries keep the near-to-far filter.
		searchRadius := in.RadiusMeters
		internalLimit := 20
		if intent.StoreName != "" {
			searchRadius = 0
			internalLimit = 30
		}
		localStarted := time.Now()
		group.Go(func() error {
			var providerErr error
			internal, providerErr = s.stores.Search(providerContext, internalQuery(intent), intent.Categories, intent.LocationText, in.Latitude, in.Longitude, searchRadius, internalLimit, user)
			return providerErr
		})
		coverage := coverageUnknown
		if s.policy.Enabled {
			// Counted alongside the catalogue search rather than after it, so knowing how
			// much of the neighbourhood we hold costs no wall clock time of its own.
			group.Go(func() error {
				coverage = s.catalogueCoverage(providerContext, in.Latitude, in.Longitude, s.policy.CoverageRadiusMeters)
				return nil
			})
		} else if s.places != nil {
			// The behaviour this product has always had, kept whole behind the flag: the
			// provider is asked in parallel, before anyone has looked at what we hold.
			googleUsed = true
			group.Go(func() error {
				places, reason := s.providerSearch(providerContext, intent, in, requestLocale)
				external = places
				if reason != "" {
					fallback = joinFallback(fallback, reason)
				}
				return nil
			})
		}
		if e := group.Wait(); e != nil {
			return Response{}, e
		}
		localElapsed = time.Since(localStarted)
		observed = sufficiency{
			ResultCount:   len(internal),
			Relevance:     localRelevance(internal, intent, s.policy.relevanceSample()),
			Coverage:      coverage,
			ExplicitStore: intent.StoreName != "",
		}
		decision = s.policy.decide(observed)
		// The single change to the flow: when what we already hold answers the question,
		// the provider is not asked. Everything downstream of a provider call -- the
		// home-and-living filter, place_id deduplication, the catalogue import, the
		// ranking -- runs exactly as before whenever the call does happen.
		if s.policy.Enabled && !decision.LocalOnly && s.places != nil {
			googleUsed = true
			placesStarted := time.Now()
			places, reason := s.providerSearch(ctx, intent, in, requestLocale)
			external = places
			if reason != "" {
				fallback = joinFallback(fallback, reason)
			}
			placesElapsed = time.Since(placesStarted)
		}
	} else {
		// Before telling somebody we did not understand them, check whether they named a
		// store we actually carry. "güney antalya" reads as a place rather than a request
		// and was classified out of scope, while GÜNEY ANTALYA HALI ve YATAK SATIŞ
		// MAĞAZASI sat in our own catalogue the whole time. Nobody should have to type a
		// store's full registered name to find it.
		named, e := s.stores.SearchByName(ctx, in.Query, in.Latitude, in.Longitude, 30, user)
		if e != nil {
			return Response{}, e
		}
		// A partial or previously unseen store name cannot be recognized by a finite
		// brand list. Ask the provider for unclear text and let the same generic store
		// classifier used for every city decide whether the matches sell home goods.
		// Definite out-of-scope requests still never reach the provider unless they match
		// a store already in our catalogue.
		if s.places != nil && (len(named) > 0 || intent.Scope == ScopeUnclear) {
			googleUsed = true
			var providerErr error
			if localized, ok := s.places.(LocalizedPlacesProvider); ok {
				external, providerErr = localized.TextSearchLocalized(ctx, in.Query, in.Latitude, in.Longitude, localHorizonMeters, requestLocale)
			} else {
				external, providerErr = s.places.TextSearch(ctx, in.Query, in.Latitude, in.Longitude, localHorizonMeters)
			}
			if providerErr != nil {
				fallback = joinFallback(fallback, "places_unavailable")
				external = nil
			} else {
				external = homeLivingOnly(external)
			}
		}
		if len(named) > 0 || len(external) > 0 {
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

	results := make([]Result, 0, len(internal)+20)
	localIDs := map[uuid.UUID]bool{}
	for rank, x := range internal {
		r := fromStore(x, rank)
		results = append(results, r)
		localIDs[x.ID] = true
	}
	mapped, e := s.lookupExternal(ctx, external)
	if e != nil {
		return Response{}, e
	}
	// Every Google result must resolve to a store id: without one the client cannot
	// open a detail page, verify a visit, or write a review, and search_results.store_id
	// stays NULL so history cannot be replayed without calling Google again.
	//
	// Materialize mapped places too. The result row below uses the live provider payload,
	// while the detail page reads the persisted source. Updating only new places made an
	// existing store show a current phone/photo in search and stale blanks in its detail.
	// materializePlaces is idempotent and preserves a store-managed phone number.
	imported, e := s.materializePlaces(ctx, external)
	if e != nil {
		return Response{}, e
	}
	for rank, p := range external {
		if m, ok := mapped[p.PlaceID]; ok {
			if localIDs[m.Platform.StoreID] {
				for i := range results {
					if results[i].ID != nil && *results[i].ID == m.Platform.StoreID {
						results[i].Google = &External{Provider: "google", PlaceID: p.PlaceID, Rating: p.Rating, RatingCount: p.RatingCount, PhotoName: p.PhotoName, PhotoAttributions: p.PhotoAttributions, BusinessStatus: p.BusinessStatus}
						// Our own record wins: it is the number the store gave us, or one an
						// admin corrected. The provider only fills a gap.
						if results[i].Phone == "" {
							results[i].Phone = p.Phone
						}
						results[i].Source = "google+platform"
						results[i].externalPlaceID = p.PlaceID
					}
				}
				continue
			}
			id := m.Platform.StoreID
			results = append(results, Result{ID: &id, Source: "google+platform", Name: p.Name, Address: p.Address, City: cityFromAddress(p.Address), Latitude: p.Latitude, Longitude: p.Longitude, Categories: append([]string{}, m.Categories...), CategoryLabels: append([]string{}, m.CategoryLabels...), Platform: &m.Platform, Photo: m.Photo, Phone: p.Phone, Google: &External{Provider: "google", PlaceID: p.PlaceID, Rating: p.Rating, RatingCount: p.RatingCount, PhotoName: p.PhotoName, PhotoAttributions: p.PhotoAttributions, BusinessStatus: p.BusinessStatus}, Premium: m.Premium, CatalogStore: m.CatalogStore, score: mergedScore(m.Platform, p, rank), externalPlaceID: p.PlaceID})
			localIDs[id] = true
		} else {
			// Platform stays nil: a freshly imported store has no community data, and
			// an empty Platform block would render a fabricated 0.0 community rating.
			r := Result{Source: "google", Name: p.Name, Address: p.Address, City: cityFromAddress(p.Address), Latitude: p.Latitude, Longitude: p.Longitude, Categories: StoreCategories(p.Name, p.Types), Phone: p.Phone, Google: &External{Provider: "google", PlaceID: p.PlaceID, Rating: p.Rating, RatingCount: p.RatingCount, PhotoName: p.PhotoName, PhotoAttributions: p.PhotoAttributions, BusinessStatus: p.BusinessStatus}, score: googleScore(p, rank), externalPlaceID: p.PlaceID}
			if id, ok := imported[p.PlaceID]; ok {
				storeID := id
				r.ID = &storeID
			}
			results = append(results, r)
		}
	}
	// The same verdict, applied to what is shown. A store already in the catalogue may have
	// been classified by hand, so its own categories are trusted rather than re-derived --
	// but a result that belongs to no category of ours has no business in a list of home
	// and living stores, wherever it came from.
	kept := results[:0]
	for _, r := range results {
		if len(r.Categories) > 0 {
			kept = append(kept, r)
		}
	}
	results = kept

	if e = s.attachStoredGoogle(ctx, results); e != nil {
		return Response{}, e
	}
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
	if len(results) > 30 {
		results = results[:30]
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
	_, e = tx.Exec(ctx, `INSERT INTO searches(id,user_id,visitor_session_id,raw_query,normalized_query,parsed_intent,search_mode,ai_used,ai_provider,ai_model,request_latitude,request_longitude,requested_radius_meters,duration_ms,internal_result_count,external_result_count,total_result_count,fallback_state,location_text,google_places_used,status,query_language,local_only,gate_reason,local_relevance,catalogue_coverage,local_duration_ms,places_duration_ms,search_city,search_district) VALUES($1,$2,$3,$4,$5,$6,'hybrid',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'completed',$20,$21,$22,$23,$24,$25,$26,$27,$28)`, searchID, user, visitor, in.Query, intent.NormalizedQuery, intentJSON, aiUsed, nilIf(!aiUsed, "openai"), nilIf(!aiUsed, s.model), lat, lon, in.RadiusMeters, time.Since(start).Milliseconds(), len(internal), len(external), len(results), nilIf(fallback == "", fallback), nilIf(intent.LocationText == "", intent.LocationText), googleUsed, intent.QueryLanguage,
		decision.LocalOnly, nilIf(decision.reason() == "", decision.reason()), observed.Relevance, nilIf(observed.Coverage == coverageUnknown, observed.Coverage),
		localElapsed.Milliseconds(), nilIf(placesElapsed == 0, placesElapsed.Milliseconds()), nilIf(searchCity == "", searchCity), nilIf(searchDistrict == "", searchDistrict))
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
	if _, e = s.report.RecordTx(ctx, tx, reporting.Event{Type: reporting.SearchPerformed, IdempotencyKey: "search:" + searchID.String(), UserID: user, VisitorSessionID: visitor, SearchID: &searchID, Metadata: map[string]any{"ai_used": aiUsed, "google_places_used": googleUsed, "scope": intent.Scope, "zero_results": len(results) == 0}}); e != nil {
		return Response{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Response{}, e
	}
	observability.SearchGate(decision.LocalOnly, decision.Reasons)
	observability.SearchStage("local", localElapsed)
	if placesElapsed > 0 {
		observability.SearchStage("places", placesElapsed)
	}
	observability.SearchStage(map[bool]string{true: "total_local_only", false: "total_fallback"}[decision.LocalOnly], time.Since(start))
	// A small sample of the searches we decided not to pay for is asked anyway, after the
	// answer has already gone out, purely to find out what the decision cost. Detached
	// from the request so it can never delay or alter it.
	if decision.LocalOnly && s.places != nil && s.shadowSampled() {
		go s.measureShadow(context.WithoutCancel(ctx), shadowMeasurement{
			SearchID: searchID, Intent: intent, Request: in, Locale: requestLocale,
			LocalResultCount: len(internal), LocalTopScore: topScore(results),
			Coverage: observed.Coverage, City: searchCity, District: searchDistrict,
		})
	}
	return Response{SearchID: searchID, VisitorSessionID: visitor, Intent: intent, Results: results, Guidance: guidance, FallbackState: fallback}, nil
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

func (s *Service) lookupExternal(ctx context.Context, places []Place) (map[string]mappedStore, error) {
	ids := make([]string, 0, len(places))
	for _, p := range places {
		ids = append(ids, p.PlaceID)
	}
	out := map[string]mappedStore{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, e := s.db.Query(ctx, `SELECT x.external_id,s.id,ss.average_rating,ss.review_count,ss.favorite_count,ss.post_count,s.is_premium,s.is_catalog_store,coalesce(s.cover_media_id::text,''),coalesce((SELECT array_agg(c.slug ORDER BY c.slug) FROM store_category_links l JOIN store_categories c ON c.id=l.category_id WHERE l.store_id=s.id),'{}'),coalesce((SELECT array_agg(t.name ORDER BY c.slug) FROM store_category_links l JOIN store_categories c ON c.id=l.category_id JOIN store_category_translations t ON t.category_id=c.id AND t.locale=$2 WHERE l.store_id=s.id),'{}') FROM store_external_sources x JOIN stores s ON s.id=x.store_id AND s.deleted_at IS NULL JOIN store_stats ss ON ss.store_id=s.id WHERE x.provider='google' AND x.external_id=ANY($1)`, ids, i18n.FromContext(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var coverMediaID string
		var m mappedStore
		if e = rows.Scan(&id, &m.Platform.StoreID, &m.Platform.AverageRating, &m.Platform.ReviewCount, &m.Platform.FavoriteCount, &m.Platform.PostCount, &m.Premium, &m.CatalogStore, &coverMediaID, &m.Categories, &m.CategoryLabels); e != nil {
			return nil, e
		}
		if coverMediaID != "" {
			m.Photo = &Photo{Source: "admin", MediaID: coverMediaID}
		}
		out[id] = m
	}
	return out, rows.Err()
}

// Internal results must carry the same stored Google score as their detail page even
// when Google's live text search did not return that store in this particular request.
// Live data already attached by the merge above wins; this fills only missing values.
func (s *Service) attachStoredGoogle(ctx context.Context, results []Result) error {
	ids := make([]uuid.UUID, 0, len(results))
	for i := range results {
		if results[i].ID != nil && results[i].Google == nil {
			ids = append(ids, *results[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT store_id,external_id,
 CASE WHEN jsonb_typeof(attribution->'rating')='number' THEN (attribution->>'rating')::float8 ELSE 0 END,
 CASE WHEN jsonb_typeof(attribution->'rating_count')='number' THEN (attribution->>'rating_count')::int ELSE 0 END,
 coalesce(attribution->>'photo_name',''),
	CASE WHEN jsonb_typeof(attribution->'photo_attributions')='array' THEN array(SELECT jsonb_array_elements_text(attribution->'photo_attributions')) ELSE '{}'::text[] END,
	coalesce(attribution->>'business_status',''),
	 CASE WHEN jsonb_typeof(attribution->'opening_hours')='object' THEN attribution->>'opening_hours' ELSE '' END
	 FROM store_external_sources WHERE provider='google' AND store_id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	stored := map[uuid.UUID]*External{}
	for rows.Next() {
		var id uuid.UUID
		var item External
		var hoursJSON string
		if err = rows.Scan(&id, &item.PlaceID, &item.Rating, &item.RatingCount, &item.PhotoName, &item.PhotoAttributions, &item.BusinessStatus, &hoursJSON); err != nil {
			return err
		}
		item.Provider = "google"
		if hoursJSON != "" {
			var hours OpeningHours
			if json.Unmarshal([]byte(hoursJSON), &hours) == nil {
				// Answered now, never stored. "Open" is true for as long as it is true.
				hours.OpenNow = hours.OpenAt(s.now())
				item.Hours = &hours
			}
		}
		stored[id] = &item
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range results {
		if results[i].ID == nil || results[i].Google != nil {
			continue
		}
		results[i].Google = stored[*results[i].ID]
		if results[i].Google != nil && results[i].Source == "internal" {
			results[i].Source = "google+platform"
		}
	}
	return nil
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

func placesQuery(i Intent, raw string) string {
	// A named store is a precision lookup. Repeating the raw sentence, parsed store name,
	// products and categories made the provider query less exact and returned no result
	// for "Yeğenler Elektrik Antalya" even though that exact place exists. Coordinates
	// still provide the geographic bias; explicit location text is kept when supplied.
	if strings.TrimSpace(i.StoreName) != "" {
		query := strings.TrimSpace(strings.Join([]string{i.StoreName, i.LocationText}, " "))
		if runes := []rune(query); len(runes) > 500 {
			query = strings.TrimSpace(string(runes[:500]))
		}
		return query
	}
	// The person's own words, and where they are. Nothing else.
	//
	// This used to append the parsed intent -- product terms, semantic terms and category
	// slugs -- on the theory that more terms meant a better match. They are internal keys
	// in English, and handing them to a provider that does literal text matching wrecks
	// the query: searching "yatak" near Bostanlı returned two chain branches in six, and
	// "yatak bedding" returned six in six, because "Bedding" is half the name of a chain
	// with a branch on every corner. The slug was competing with the question.
	//
	// This is not about that chain. Every category key we have is an ordinary English word
	// that some Turkish business has put on its sign, so any of them can do the same thing.
	// The categories still do their work where they belong -- filtering our own catalogue,
	// where they are keys rather than search terms.
	terms := make([]string, 0, 2)
	terms = append(terms, strings.TrimSpace(raw))
	if location := strings.TrimSpace(i.LocationText); location != "" {
		terms = appendUnique(terms, location)
	}
	query := strings.TrimSpace(strings.Join(terms, " "))
	runes := []rune(query)
	if len(runes) > 500 {
		query = strings.TrimSpace(string(runes[:500]))
	}
	return query
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

// homeLivingOnly drops places Google is explicit about being something else. Silence keeps
// a place: most shops carry nothing but "store" and "establishment", and refusing those
// would empty the catalogue.
func homeLivingOnly(places []Place) []Place {
	if len(places) == 0 {
		return places
	}
	kept := places[:0]
	for _, p := range places {
		if IsHomeLivingStore(p.Name, p.Types) {
			kept = append(kept, p)
		}
	}
	return kept
}

// How long a provider answer stands. Short enough that a shop opening today is found
// today, long enough that a query typed twice in an afternoon is paid for once.
const placesCacheTTL = 6 * time.Hour

// The question, not the asker. Coordinates are rounded to about a kilometre: two people
// standing a street apart are asking the same thing, and keeping full precision would give
// every one of them a private copy of the same answer.
func placesCacheKey(query string, lat, lon *float64, radius int, locale string) string {
	coarse := func(v *float64) string {
		if v == nil {
			return "-"
		}
		return strconv.FormatFloat(math.Round(*v*100)/100, 'f', 2, 64)
	}
	return strings.Join([]string{
		foldLatin(normalizeText(query)), coarse(lat), coarse(lon),
		strconv.Itoa(radius), locale,
	}, "|")
}

// providerSearch is the Google branch exactly as it has always been: the same query, the
// same shared cache, the same home-and-living filter applied at the door. What the
// sufficiency gate changed is when this runs -- never what it does once it does.
//
// The returned string is a fallback reason, empty when the call went through.
func (s *Service) providerSearch(ctx context.Context, intent Intent, in Request, locale i18n.Locale) ([]Place, string) {
	if s.places == nil {
		return nil, ""
	}
	query := placesQuery(intent, in.Query)
	providerRadius := in.RadiusMeters
	if intent.StoreName != "" {
		providerRadius = localHorizonMeters
	}
	// The same question, asked again from the same place, gets the answer we already
	// bought. Two thirds of the searches on record repeat a query already made nearby,
	// and each repeat was paid for separately.
	//
	// It is the question that is cached, not a store. Skipping the provider on the
	// strength of a full local catalogue is now the gate's job, and is decided before
	// this is ever reached.
	key := placesCacheKey(query, in.Latitude, in.Longitude, providerRadius, string(locale))
	if cached, ok := s.cachedPlaces(ctx, key); ok {
		return homeLivingOnly(cached), ""
	}
	var places []Place
	var providerErr error
	if localized, ok := s.places.(LocalizedPlacesProvider); ok {
		places, providerErr = localized.TextSearchLocalized(ctx, query, in.Latitude, in.Longitude, providerRadius, locale)
	} else {
		places, providerErr = s.places.TextSearch(ctx, query, in.Latitude, in.Longitude, providerRadius)
	}
	if providerErr != nil {
		return nil, "places_unavailable"
	}
	s.storePlaces(ctx, key, places)
	// A search finding a bakery does not make the bakery a home store, and anything a
	// search turned up used to be kept and imported. Dropped here, at the door, rather
	// than filtered out of the results later -- otherwise it still lands in the
	// catalogue and in the sitemap.
	return homeLivingOnly(places), ""
}

func (s *Service) cachedPlaces(ctx context.Context, key string) ([]Place, bool) {
	if s.db == nil {
		return nil, false
	}
	var raw []byte
	e := s.db.QueryRow(ctx, `SELECT places FROM places_search_cache WHERE cache_key=$1 AND created_at > now()-$2::interval`, key, placesCacheTTL.String()).Scan(&raw)
	if e != nil {
		return nil, false
	}
	var places []Place
	if json.Unmarshal(raw, &places) != nil {
		return nil, false
	}
	return places, true
}

// A cache write must never fail a search. The worst case of losing one is paying for the
// same question twice, which is what happened before this existed.
func (s *Service) storePlaces(ctx context.Context, key string, places []Place) {
	if s.db == nil {
		return
	}
	raw, e := json.Marshal(places)
	if e != nil {
		return
	}
	_, _ = s.db.Exec(ctx, `INSERT INTO places_search_cache(cache_key,places,created_at) VALUES($1,$2,now())
 ON CONFLICT(cache_key) DO UPDATE SET places=EXCLUDED.places,created_at=now()`, key, raw)
}
