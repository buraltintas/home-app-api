package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

type placesRoundTripFunc func(*http.Request) (*http.Response, error)

func (f placesRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGooglePlacesContract(t *testing.T) {
	client := &http.Client{Timeout: time.Second, Transport: placesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Goog-Api-Key") != "test-key" || r.Header.Get("X-Goog-FieldMask") == "" {
			t.Error("missing Google Places credentials or field mask")
		}
		body := ""
		switch r.URL.Path {
		case "/places:searchText":
			var request struct {
				LanguageCode string `json:"languageCode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.LanguageCode != "de" {
				t.Errorf("languageCode=%q err=%v", request.LanguageCode, err)
			}
			body = `{"places":[{"id":"place-1","displayName":{"text":"Test Mağaza"},"formattedAddress":"Kadıköy, İstanbul","location":{"latitude":40.99,"longitude":29.03},"rating":4.4,"userRatingCount":12,"types":["furniture_store"],"businessStatus":"CLOSED_PERMANENTLY"}]}`
		case "/places/place-1":
			body = `{"ID":"place-1","DisplayName":{"Text":"Test Mağaza"},"FormattedAddress":"Kadıköy, İstanbul","Location":{"Latitude":40.99,"Longitude":29.03},"Rating":4.4,"UserRatingCount":12,"Types":["furniture_store"],"BusinessStatus":"CLOSED_PERMANENTLY"}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	g := &GooglePlaces{key: "test-key", baseURL: "https://places.test", client: client}
	places, err := g.TextSearchLocalized(context.Background(), "Möbel", nil, nil, 1000, i18n.LocaleDE)
	if err != nil || len(places) != 1 || places[0].PlaceID != "place-1" || places[0].RatingCount != 12 || places[0].BusinessStatus != "CLOSED_PERMANENTLY" {
		t.Fatalf("text search places=%+v err=%v", places, err)
	}
	detail, err := g.PlaceDetails(context.Background(), "place-1")
	if err != nil || detail.PlaceID != "place-1" || detail.Name != "Test Mağaza" || detail.BusinessStatus != "CLOSED_PERMANENTLY" {
		t.Fatalf("details=%+v err=%v", detail, err)
	}
}
