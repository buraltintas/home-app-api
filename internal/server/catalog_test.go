package server

import (
	"net/http/httptest"
	"testing"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"
)

type entry = map[string]any

func items(xs ...entry) map[string]any {
	list := make([]any, len(xs))
	for i, x := range xs {
		list[i] = x
	}
	return map[string]any{"items": list}
}

// What shadow mode calls a difference is what would reach a reader as one. Everything the
// database itself leaves unspecified is allowed to differ; nothing else is.
func TestShadowComparisonOnlyFlagsRealDifferences(t *testing.T) {
	nearby := "/v1/stores/{id}/nearby"
	cases := []struct {
		name, route string
		a, b        any
		same        bool
	}{
		{"identical", nearby, items(entry{"id": "a", "distance_meters": 12.5}), items(entry{"id": "a", "distance_meters": 12.5}), true},
		{"distance below a micrometre", nearby, items(entry{"id": "a", "distance_meters": 946.580814125}), items(entry{"id": "a", "distance_meters": 946.58081412}), true},
		{"distance off by a millimetre", nearby, items(entry{"id": "a", "distance_meters": 946.581}), items(entry{"id": "a", "distance_meters": 946.58}), false},
		{"two shops at one district centre", nearby,
			items(entry{"id": "a", "name": "A", "distance_meters": 2027.37640187}, entry{"id": "b", "name": "B", "distance_meters": 2027.37640187}),
			items(entry{"id": "b", "name": "B", "distance_meters": 2027.37640187}, entry{"id": "a", "name": "A", "distance_meters": 2027.37640187}), true},
		{"a different neighbour", nearby, items(entry{"id": "a", "distance_meters": 10}), items(entry{"id": "b", "distance_meters": 11}), false},
		{"same name, same district", "/v1/discovery/brand-stores",
			map[string]any{"total": 2.0, "items": []any{entry{"id": "a", "district": "Konak", "name": "Yataş"}, entry{"id": "b", "district": "Konak", "name": "Yataş"}}},
			map[string]any{"total": 2.0, "items": []any{entry{"id": "b", "district": "Konak", "name": "Yataş"}, entry{"id": "a", "district": "Konak", "name": "Yataş"}}}, true},
		{"different districts swapped", "/v1/discovery/brand-stores",
			items(entry{"id": "a", "district": "Konak", "name": "Yataş"}, entry{"id": "b", "district": "Bornova", "name": "Yataş"}),
			items(entry{"id": "b", "district": "Bornova", "name": "Yataş"}, entry{"id": "a", "district": "Konak", "name": "Yataş"}), false},
		{"category slugs in another order", "/v1/stores/{id}",
			map[string]any{"store": entry{"categories": []any{"lighting", "furniture"}}},
			map[string]any{"store": entry{"categories": []any{"furniture", "lighting"}}}, true},
		{"another category", "/v1/stores/{id}",
			map[string]any{"store": entry{"categories": []any{"lighting"}}},
			map[string]any{"store": entry{"categories": []any{"carpet"}}}, false},
		{"a renamed shop", "/v1/stores/{id}",
			map[string]any{"store": entry{"name": "Old"}}, map[string]any{"store": entry{"name": "New"}}, false},
		{"ties are not a licence elsewhere", "/v1/discovery/city-categories",
			items(entry{"id": "a"}, entry{"id": "b"}), items(entry{"id": "b"}, entry{"id": "a"}), false},
	}
	for _, c := range cases {
		same, where := sameCatalogAnswer(c.route, c.a, c.b)
		if same != c.same {
			t.Errorf("%s: same=%v (%s), want %v", c.name, same, where, c.same)
		}
	}
	if _, where := sameCatalogAnswer("/v1/stores/{id}", map[string]any{"store": entry{"name": "Old"}}, map[string]any{"store": entry{"name": "New"}}); where != ".store.name" {
		t.Fatalf("the difference is reported at %q", where)
	}
}

// A reader standing somewhere gets a distance measured from them, which the copy does not
// hold; that request goes to the database without the copy being consulted at all.
func TestCoordinatesAndNoCatalogueGoToTheDatabase(t *testing.T) {
	s := &Server{}
	consulted := false
	answer := func(*storepkg.Snapshot) (any, bool) { consulted = true; return nil, true }
	if served, _ := s.fromCatalog(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/stores/x", nil), answer); served || consulted {
		t.Fatal("answered with no catalogue held")
	}
	s.catalog = &storepkg.Catalog{}
	if served, _ := s.fromCatalog(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/stores/x?latitude=41&longitude=29", nil), answer); served || consulted {
		t.Fatal("a reader's own distance was answered from the shared copy")
	}
}
