//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/location"
)

// The location picker is now a table we ship, so these run against the real dataset
// loaded into a real PostGIS database. A mocked row would prove nothing: the whole design
// is an ORDER BY, and an ORDER BY is only true where the data is.
func seedLocations(t *testing.T) *location.Service {
	t.Helper()
	db := database(t)
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM tr_locations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		loaded, err := location.Seed(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("loaded %d locations", loaded)
	}
	return location.NewService(db)
}

func TestLocationSearchRanksProvinceBeforeDistrictBeforeNeighbourhood(t *testing.T) {
	service := seedLocations(t)
	// "Antalya" is a province, a set of districts contain the word, and dozens of
	// neighbourhoods are named after it. The province has to come first or the picker is
	// asking people to hunt for the obvious answer.
	items, err := service.Search(t.Context(), "Antalya", 5, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no results for Antalya")
	}
	if items[0].Name != "Antalya" || items[0].Address != "" || items[0].PlaceID != "il:7" {
		t.Fatalf("first result=%+v want the province", items[0])
	}
	if items[0].Latitude < 36 || items[0].Latitude > 37.5 {
		t.Errorf("Antalya is not at latitude %v", items[0].Latitude)
	}
}

func TestLocationSearchFindsTurkishNamesTypedWithoutTheirMarks(t *testing.T) {
	service := seedLocations(t)
	// Nobody reaches for "ı" and "ö" on a hurried phone keyboard. Both spellings have to
	// land on the same district.
	plain, err := service.Search(t.Context(), "kadikoy", 5, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	marked, err := service.Search(t.Context(), "Kadıköy", 5, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == 0 || len(marked) == 0 {
		t.Fatal("Kadıköy is missing from the dataset")
	}
	if plain[0].PlaceID != marked[0].PlaceID {
		t.Fatalf("kadikoy=%q but Kadıköy=%q", plain[0].PlaceID, marked[0].PlaceID)
	}
	if plain[0].Name != "Kadıköy" || plain[0].Address != "İstanbul" {
		t.Fatalf("result=%+v want Kadıköy, İstanbul", plain[0])
	}
}

func TestLocationSearchAnswersAPrefixOfAName(t *testing.T) {
	service := seedLocations(t)
	// This is the case the paid autocomplete was bought for: "unca" is not a whole word,
	// and a text index that matches whole tokens answered nothing.
	items, err := service.Search(t.Context(), "unca", 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if strings.HasPrefix(item.Name, "Uncalı") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Uncalı not among %d results for \"unca\"", len(items))
	}
}

func TestLocationSearchPrefersWhereTheVisitorIsWhenNamesRepeat(t *testing.T) {
	service := seedLocations(t)
	// Hundreds of neighbourhoods are called "Cumhuriyet". The one nearest the person
	// asking is the one they almost certainly mean.
	antalyaLat, antalyaLon := 36.8969, 30.7133
	near, err := service.Search(t.Context(), "Cumhuriyet", 3, &antalyaLat, &antalyaLon)
	if err != nil {
		t.Fatal(err)
	}
	if len(near) == 0 {
		t.Fatal("no Cumhuriyet found")
	}
	if near[0].Latitude < 35 || near[0].Latitude > 38.5 || near[0].Longitude < 28 || near[0].Longitude > 33 {
		t.Fatalf("nearest Cumhuriyet to Antalya is at %v,%v", near[0].Latitude, near[0].Longitude)
	}
}

func TestResolveReadsTheCoordinatesRatherThanTrustingTheClient(t *testing.T) {
	service := seedLocations(t)
	resolved, err := service.Resolve(t.Context(), "ilce:34-445")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name != "Kadıköy" || resolved.Provider != "bosagezme" {
		t.Fatalf("resolved=%+v", resolved)
	}
	if resolved.Latitude < 40.9 || resolved.Latitude > 41.1 || resolved.Longitude < 28.9 || resolved.Longitude > 29.2 {
		t.Fatalf("Kadıköy resolved to %v,%v", resolved.Latitude, resolved.Longitude)
	}
	// An id this table does not have is the client's mistake, not a server fault.
	if _, err = service.Resolve(t.Context(), "ilce:99-99999"); err == nil {
		t.Fatal("resolving an unknown id should fail")
	}
	if _, err = service.Search(t.Context(), "a", 5, nil, nil); err == nil {
		t.Fatal("a single character should be rejected before it reaches the database")
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	db := database(t)
	first, err := location.Seed(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := location.Seed(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first < 60000 {
		t.Fatalf("seed loaded %d then %d rows", first, second)
	}
	var provinces, districts, orphans int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM tr_locations WHERE kind='il'`).Scan(&provinces); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM tr_locations WHERE kind='ilce'`).Scan(&districts); err != nil {
		t.Fatal(err)
	}
	// Every district and neighbourhood must hang off a row that exists. A dangling parent
	// means the picker can show a place whose province it cannot name.
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM tr_locations child WHERE child.parent_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM tr_locations parent WHERE parent.id=child.parent_id)`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if provinces != 81 {
		t.Errorf("provinces=%d want 81", provinces)
	}
	if districts < 900 {
		t.Errorf("districts=%d, fewer than Turkey has", districts)
	}
	if orphans != 0 {
		t.Errorf("%d rows point at a parent that does not exist", orphans)
	}
}
