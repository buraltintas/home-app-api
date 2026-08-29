//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/reporting"
	"github.com/burakaltintas/home-app-api/internal/search"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A place the catalogue does not hold, so the provider always has something of its own to
// offer. The name and type are ordinary home-and-living, which is what makes it eligible
// to be imported and deduplicated the way any other provider result is.
func newPlace(id string, lat, lon float64) search.Place {
	return search.Place{
		PlaceID: id, Name: "Yeni Mobilya " + id, Address: "Cadde No:1, 07070 Konyaaltı/Antalya, Türkiye",
		Latitude: lat, Longitude: lon, Rating: 4.6, RatingCount: 120, Types: []string{"furniture_store"},
	}
}

type gatePlaces struct {
	calls  int
	places []search.Place
}

func (p *gatePlaces) TextSearch(context.Context, string, *float64, *float64, int) ([]search.Place, error) {
	p.calls++
	return p.places, nil
}

func (p *gatePlaces) PlaceDetails(context.Context, string) (search.Place, error) {
	return search.Place{}, errors.New("unexpected place details call")
}

func gateSearchService(t *testing.T, db *pgxpool.Pool, places search.PlacesProvider, policy search.SufficiencyPolicy) *search.Service {
	t.Helper()
	report, err := reporting.NewService(db, "Europe/Istanbul", 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	svc := search.NewService(db, storepkg.NewService(db, report), nil, places, "", 3, report, 72*time.Hour, 24*time.Hour)
	svc.UseSufficiencyPolicy(policy)
	return svc
}

func gatePolicy() search.SufficiencyPolicy {
	p := search.DefaultSufficiencyPolicy()
	p.Enabled = true
	p.ShadowRate = 0
	return p
}

// catalogue plants n carpet shops around a point, close enough together that they all
// fall inside the searcher's radius and inside the coverage count.
func catalogue(t *testing.T, db *pgxpool.Pool, lat, lon float64, n int) {
	t.Helper()
	for i := range n {
		id := uuid.New()
		name := fmt.Sprintf("Halı Dünyası %s", id.String()[:8])
		if _, err := db.Exec(t.Context(), `INSERT INTO stores(id,name,slug,city,district,address,location) VALUES($1,$2,$3,'Antalya','Konyaaltı','Cadde No:2, Konyaaltı/Antalya, Türkiye',ST_SetSRID(ST_MakePoint($5,$4),4326)::geography)`,
			id, name, "gate-"+id.String(), lat+float64(i)*0.0005, lon); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(t.Context(), `INSERT INTO store_stats(store_id) VALUES($1)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(t.Context(), `INSERT INTO store_category_links(store_id,category_id) SELECT $1,id FROM store_categories WHERE slug='carpet' ON CONFLICT DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
	}
}

func gateRecord(t *testing.T, db *pgxpool.Pool, id uuid.UUID) (localOnly bool, reason string, googleUsed bool) {
	t.Helper()
	var gateReason *string
	if err := db.QueryRow(t.Context(), `SELECT local_only,gate_reason,google_places_used FROM searches WHERE id=$1`, id).Scan(&localOnly, &gateReason, &googleUsed); err != nil {
		t.Fatal(err)
	}
	if gateReason != nil {
		reason = *gateReason
	}
	return
}

func at(v float64) *float64 { return &v }

// The saving, end to end: a well-covered city asked an ordinary category question is
// answered out of our own catalogue, and the provider is never called.
func TestGateKeepsADenseCatalogueSearchLocal(t *testing.T) {
	db := database(t)
	lat, lon := 36.85+float64(uuid.New().ID()%100)/1000, 30.60
	catalogue(t, db, lat, lon, 60)
	places := &gatePlaces{places: []search.Place{newPlace(uuid.NewString(), lat, lon)}}
	svc := gateSearchService(t, db, places, gatePolicy())

	response, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, search.Request{Query: "halı mağazası", Latitude: at(lat), Longitude: at(lon), RadiusMeters: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if places.calls != 0 {
		t.Fatalf("provider calls=%d, want none", places.calls)
	}
	if len(response.Results) < 8 {
		t.Fatalf("results=%d, a local-only answer must still fill the screen", len(response.Results))
	}
	localOnly, reason, googleUsed := gateRecord(t, db, response.SearchID)
	if !localOnly || reason != "" || googleUsed {
		t.Fatalf("local_only=%t reason=%q google_used=%t", localOnly, reason, googleUsed)
	}
}

// The same query where we hold almost nothing still reaches the provider, and the reason
// recorded is the one that will tell us later that the fix is catalogue, not threshold.
func TestGateStillCallsTheProviderWhereTheCatalogueIsThin(t *testing.T) {
	db := database(t)
	lat, lon := 39.90+float64(uuid.New().ID()%100)/1000, 32.80
	catalogue(t, db, lat, lon, 2)
	places := &gatePlaces{places: []search.Place{newPlace(uuid.NewString(), lat, lon)}}
	svc := gateSearchService(t, db, places, gatePolicy())

	response, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, search.Request{Query: "halı mağazası", Latitude: at(lat), Longitude: at(lon), RadiusMeters: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if places.calls != 1 {
		t.Fatalf("provider calls=%d, want one", places.calls)
	}
	localOnly, reason, googleUsed := gateRecord(t, db, response.SearchID)
	if localOnly || !googleUsed || reason == "" {
		t.Fatalf("local_only=%t reason=%q google_used=%t", localOnly, reason, googleUsed)
	}
}

// Somebody who names a store gets the provider asked however full the catalogue is. A
// wall of similar shops is not an answer to a name.
func TestGateAlwaysAsksTheProviderForANamedStore(t *testing.T) {
	db := database(t)
	lat, lon := 36.85+float64(uuid.New().ID()%100)/1000, 30.62
	catalogue(t, db, lat, lon, 60)
	places := &gatePlaces{places: []search.Place{newPlace(uuid.NewString(), lat, lon)}}
	svc := gateSearchService(t, db, places, gatePolicy())

	response, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, search.Request{Query: "Yataş Konyaaltı", Latitude: at(lat), Longitude: at(lon), RadiusMeters: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if places.calls != 1 {
		t.Fatalf("provider calls=%d, want one", places.calls)
	}
	if _, _, googleUsed := gateRecord(t, db, response.SearchID); !googleUsed {
		t.Fatal("a named store search must be recorded as having used the provider")
	}
}

// The half of the flow the gate must not touch. When the provider is called, its answer
// is still filtered, imported once, and deduplicated by place id -- so asking twice
// creates one store, not two, and the second search finds the first one's row.
func TestFallbackStillImportsAndDeduplicates(t *testing.T) {
	db := database(t)
	lat, lon := 38.40+float64(uuid.New().ID()%100)/1000, 27.10
	placeID := "gate-place-" + uuid.NewString()
	places := &gatePlaces{places: []search.Place{
		newPlace(placeID, lat, lon),
		// Not a home store. It must be dropped at the door rather than imported.
		{PlaceID: "gate-bakery-" + uuid.NewString(), Name: "Köşe Pastanesi", Latitude: lat, Longitude: lon, Types: []string{"bakery"}},
	}}
	svc := gateSearchService(t, db, places, gatePolicy())
	request := search.Request{Query: "mobilya mağazası", Latitude: at(lat), Longitude: at(lon), RadiusMeters: 10000}

	for range 2 {
		if _, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, request); err != nil {
			t.Fatal(err)
		}
	}
	if places.calls != 2 {
		t.Fatalf("provider calls=%d, want one per search", places.calls)
	}
	var stores int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM store_external_sources WHERE provider='google' AND external_id=$1`, placeID).Scan(&stores); err != nil {
		t.Fatal(err)
	}
	if stores != 1 {
		t.Fatalf("the same place produced %d stores", stores)
	}
	var bakeries int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM stores WHERE name='Köşe Pastanesi'`).Scan(&bakeries); err != nil {
		t.Fatal(err)
	}
	if bakeries != 0 {
		t.Fatalf("a bakery reached the catalogue %d times", bakeries)
	}
}

// The flag is the way back. With it off the search behaves exactly as production does
// today: the provider is asked in parallel, whatever the catalogue holds.
func TestFlagOffRestoresTodaysBehaviour(t *testing.T) {
	db := database(t)
	lat, lon := 36.85+float64(uuid.New().ID()%100)/1000, 30.64
	catalogue(t, db, lat, lon, 60)
	places := &gatePlaces{places: []search.Place{newPlace(uuid.NewString(), lat, lon)}}
	svc := gateSearchService(t, db, places, search.DefaultSufficiencyPolicy())

	response, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, search.Request{Query: "halı mağazası", Latitude: at(lat), Longitude: at(lon), RadiusMeters: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if places.calls != 1 {
		t.Fatalf("provider calls=%d, want the parallel call the product makes today", places.calls)
	}
	localOnly, reason, googleUsed := gateRecord(t, db, response.SearchID)
	if localOnly || !googleUsed || reason != "gate_disabled" {
		t.Fatalf("local_only=%t reason=%q google_used=%t", localOnly, reason, googleUsed)
	}
}

// A shadow call is a measurement, and a measurement that changes what it measures is not
// one. The person's results are the local-only results, and the catalogue learns nothing
// from what the shadow saw.
func TestShadowMeasurementLeavesTheSearchAndTheCatalogueAlone(t *testing.T) {
	db := database(t)
	lat, lon := 36.85+float64(uuid.New().ID()%100)/1000, 30.66
	catalogue(t, db, lat, lon, 60)
	placeID := "shadow-place-" + uuid.NewString()
	places := &gatePlaces{places: []search.Place{newPlace(placeID, lat, lon)}}
	policy := gatePolicy()
	policy.ShadowRate = 1
	svc := gateSearchService(t, db, places, policy)

	response, err := svc.Search(i18n.WithLocale(t.Context(), i18n.LocaleTR), nil, nil, search.Request{Query: "halı mağazası", Latitude: at(lat), Longitude: at(lon), RadiusMeters: 10000})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range response.Results {
		if r.Source != "internal" {
			t.Fatalf("a shadow measurement reached the searcher: %+v", r)
		}
	}
	if localOnly, _, googleUsed := gateRecord(t, db, response.SearchID); !localOnly || googleUsed {
		t.Fatalf("local_only=%t google_used=%t: the shadow call is not a fallback", localOnly, googleUsed)
	}
	// The measurement runs after the answer has gone out, so it is waited for rather
	// than assumed.
	var measured, imported int
	for range 60 {
		time.Sleep(50 * time.Millisecond)
		if err = db.QueryRow(t.Context(), `SELECT count(*) FROM search_shadow_measurements WHERE search_id=$1`, response.SearchID).Scan(&measured); err != nil {
			t.Fatal(err)
		}
		if measured == 1 {
			break
		}
	}
	if measured != 1 {
		t.Fatal("the shadow measurement was never recorded")
	}
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM store_external_sources WHERE provider='google' AND external_id=$1`, placeID).Scan(&imported); err != nil {
		t.Fatal(err)
	}
	if imported != 0 {
		t.Fatal("the catalogue learned from a measurement")
	}
	var providerOnly, highRelevance int
	if err = db.QueryRow(t.Context(), `SELECT provider_only_count,provider_only_high_relevance_count FROM search_shadow_measurements WHERE search_id=$1`, response.SearchID).Scan(&providerOnly, &highRelevance); err != nil {
		t.Fatal(err)
	}
	if providerOnly != 1 {
		t.Fatalf("provider_only_count=%d, want the one store only the provider knows", providerOnly)
	}
	_ = highRelevance
}
