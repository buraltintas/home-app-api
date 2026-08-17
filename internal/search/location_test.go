package search

import (
	"context"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type locationPlacesStub struct {
	places []Place
	locale i18n.Locale
}

func (s *locationPlacesStub) TextSearch(context.Context, string, *float64, *float64, int) ([]Place, error) {
	return s.places, nil
}

func (s *locationPlacesStub) TextSearchLocalized(_ context.Context, _ string, _, _ *float64, _ int, locale i18n.Locale) ([]Place, error) {
	s.locale = locale
	return s.places, nil
}

func (s *locationPlacesStub) PlaceDetails(context.Context, string) (Place, error) {
	return Place{}, nil
}

func TestResolveLocationsFiltersBusinessesAndPreservesProviderOrder(t *testing.T) {
	provider := &locationPlacesStub{places: []Place{
		{PlaceID: "business", Name: "Kadıköy Mobilya", Latitude: 40.99, Longitude: 29.03, Types: []string{"furniture_store"}},
		{PlaceID: "district", Name: "Kadıköy", Address: "İstanbul, Türkiye", Latitude: 40.99, Longitude: 29.03, Types: []string{"administrative_area_level_2", "political"}, Attributions: []string{"Google"}},
		{PlaceID: "neighborhood", Name: "Moda", Address: "Kadıköy, İstanbul", Latitude: 40.98, Longitude: 29.02, Types: []string{"neighborhood", "political"}},
	}}
	service := &Service{places: provider}
	items, err := service.ResolveLocations(i18n.WithLocale(context.Background(), i18n.LocaleTR), "Kadıköy", 1)
	if err != nil || len(items) != 1 || items[0].PlaceID != "district" || items[0].Provider != "google" || provider.locale != i18n.LocaleTR || len(items[0].Attributions) != 1 {
		t.Fatalf("items=%+v locale=%q err=%v", items, provider.locale, err)
	}
}

func TestResolveLocationsValidatesInputBeforeProviderCall(t *testing.T) {
	service := &Service{}
	if _, err := service.ResolveLocations(context.Background(), "x", 5); err == nil {
		t.Fatal("short location accepted")
	}
	if _, err := service.ResolveLocations(context.Background(), "Kadıköy", 11); err == nil {
		t.Fatal("oversized limit accepted")
	}
}
