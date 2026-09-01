package search

import (
	"context"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type locationPlacesStub struct {
	places  []Place
	detail  Place
	locale  i18n.Locale
	queries []string
	byQuery map[string][]Place
}

func (s *locationPlacesStub) Autocomplete(_ context.Context, query string, locale i18n.Locale, _, _ *float64) ([]Place, error) {
	s.queries = append(s.queries, query)
	s.locale = locale
	if s.byQuery != nil {
		return s.byQuery[query], nil
	}
	return s.places, nil
}

func (s *locationPlacesStub) TextSearch(context.Context, string, *float64, *float64, int) ([]Place, error) {
	return s.places, nil
}

func (s *locationPlacesStub) TextSearchLocalized(_ context.Context, _ string, _, _ *float64, _ int, locale i18n.Locale) ([]Place, error) {
	s.locale = locale
	return s.places, nil
}

func (s *locationPlacesStub) PlaceDetails(context.Context, string) (Place, error) {
	return s.detail, nil
}

func TestResolveLocationPlaceAcceptsOnlyVerifiedLocationAnchors(t *testing.T) {
	provider := &locationPlacesStub{detail: Place{PlaceID: "kadikoy", Name: "Kadıköy", Address: "İstanbul, Türkiye", Latitude: 40.99, Longitude: 29.03, Types: []string{"administrative_area_level_2", "political"}}}
	service := &Service{places: provider}
	item, err := service.ResolveLocationPlace(context.Background(), "kadikoy")
	if err != nil || item.PlaceID != "kadikoy" || item.Name != "Kadıköy" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	provider.detail.Types = []string{"furniture_store"}
	if _, err = service.ResolveLocationPlace(context.Background(), "kadikoy"); err == nil {
		t.Fatal("business accepted as a profile location")
	}
}

func TestResolveLocationPlaceAcceptsVerifiedPublicTransportLandmark(t *testing.T) {
	provider := &locationPlacesStub{detail: Place{PlaceID: "ferry", Name: "Karşıyaka Vapur İskelesi", Address: "Karşıyaka/İzmir", Latitude: 38.455, Longitude: 27.12, Types: []string{"ferry_terminal", "point_of_interest", "establishment"}}}
	service := &Service{places: provider}
	item, err := service.ResolveLocationPlace(context.Background(), "ferry")
	if err != nil || item.PlaceID != "ferry" || item.Latitude == 0 || item.Longitude == 0 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}

func TestResolveLocationsFiltersBusinessesAndPreservesProviderOrder(t *testing.T) {
	provider := &locationPlacesStub{places: []Place{
		{PlaceID: "business", Name: "Kadıköy Mobilya", Latitude: 40.99, Longitude: 29.03, Types: []string{"furniture_store"}},
		{PlaceID: "district", Name: "Kadıköy", Address: "İstanbul, Türkiye", Latitude: 40.99, Longitude: 29.03, Types: []string{"administrative_area_level_2", "political"}, Attributions: []string{"Google"}},
		{PlaceID: "neighborhood", Name: "Moda", Address: "Kadıköy, İstanbul", Latitude: 40.98, Longitude: 29.02, Types: []string{"neighborhood", "political"}},
	}}
	service := &Service{places: provider}
	items, err := service.ResolveLocations(i18n.WithLocale(context.Background(), i18n.LocaleTR), "Kadıköy", 1, nil, nil)
	if err != nil || len(items) != 1 || items[0].PlaceID != "district" || items[0].Provider != "google" || provider.locale != i18n.LocaleTR || len(items[0].Attributions) != 1 {
		t.Fatalf("items=%+v locale=%q err=%v", items, provider.locale, err)
	}
}

func TestResolveLocationsIncludesPublicTransportLandmarkButNotOrdinaryBusiness(t *testing.T) {
	provider := &locationPlacesStub{places: []Place{
		{PlaceID: "ferry", Name: "Karşıyaka Vapur İskelesi", Address: "Karşıyaka/İzmir", Types: []string{"ferry_terminal", "point_of_interest", "establishment", "premise"}},
		{PlaceID: "shop", Name: "Karşıyaka Mobilya", Address: "Karşıyaka/İzmir", Types: []string{"furniture_store", "point_of_interest", "establishment", "premise"}},
		{PlaceID: "district", Name: "Karşıyaka", Address: "İzmir, Türkiye", Types: []string{"administrative_area_level_2", "political"}},
	}}
	service := &Service{places: provider}
	items, err := service.ResolveLocations(context.Background(), "Karşıyaka", 5, nil, nil)
	if err != nil || len(items) != 2 || items[0].PlaceID != "ferry" || items[1].PlaceID != "district" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestResolveLocationsValidatesInputBeforeProviderCall(t *testing.T) {
	service := &Service{}
	if _, err := service.ResolveLocations(context.Background(), "x", 5, nil, nil); err == nil {
		t.Fatal("short location accepted")
	}
	if _, err := service.ResolveLocations(context.Background(), "Kadıköy", 11, nil, nil); err == nil {
		t.Fatal("oversized limit accepted")
	}
}

func TestResolveLocationsRetriesTurkishNeighborhoodSuffixAfterBusinessOnlyPrediction(t *testing.T) {
	provider := &locationPlacesStub{byQuery: map[string][]Place{
		"Uluç Mahallesi": {{PlaceID: "office", Name: "Uluç Mahallesi Muhtarlığı", Types: []string{"establishment", "point_of_interest"}}},
		"Uluç":           {{PlaceID: "uluc", Name: "Uluç", Address: "Konyaaltı/Antalya, Türkiye", Types: []string{"administrative_area_level_4", "political"}}},
	}}
	service := &Service{places: provider}
	items, err := service.ResolveLocations(context.Background(), "Uluç Mahallesi", 5, nil, nil)
	if err != nil || len(items) != 1 || items[0].PlaceID != "uluc" {
		t.Fatalf("items=%+v queries=%v err=%v", items, provider.queries, err)
	}
	if len(provider.queries) != 2 || provider.queries[0] != "Uluç Mahallesi" || provider.queries[1] != "Uluç" {
		t.Fatalf("queries=%v", provider.queries)
	}
}

func TestLocationAutocompleteFallbackIsConservative(t *testing.T) {
	tests := map[string]string{
		"Uluç Mahallesi": "Uluç",
		"Uluç mahalle":   "Uluç",
		"Uluç mah.":      "Uluç",
		"Mahallesi":      "Mahallesi",
		"Kadıköy":        "Kadıköy",
	}
	for input, want := range tests {
		if got := locationAutocompleteFallback(input); got != want {
			t.Errorf("locationAutocompleteFallback(%q)=%q want %q", input, got, want)
		}
	}
}
