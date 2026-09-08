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
	var searchMask, detailMask, locationMask string
	client := &http.Client{Timeout: time.Second, Transport: placesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Goog-Api-Key") != "test-key" || r.Header.Get("X-Goog-FieldMask") == "" {
			t.Error("missing Google Places credentials or field mask")
		}
		body := ""
		switch r.URL.Path {
		case "/places:searchText":
			searchMask = r.Header.Get("X-Goog-FieldMask")
			var request struct {
				LanguageCode string `json:"languageCode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.LanguageCode != "de" {
				t.Errorf("languageCode=%q err=%v", request.LanguageCode, err)
			}
			// Expensive fields are included deliberately: the decoder must ignore them even
			// if a proxy or fixture returns more than the field mask requested.
			body = `{"places":[{"id":"place-1","displayName":{"text":"Test Mağaza"},"formattedAddress":"Kadıköy, İstanbul","location":{"latitude":40.99,"longitude":29.03},"rating":4.4,"userRatingCount":12,"nationalPhoneNumber":"123","websiteUri":"https://example.test","photos":[{"name":"places/place-1/photos/photo"}],"types":["furniture_store"],"businessStatus":"CLOSED_PERMANENTLY"}]}`
		case "/places/place-1":
			if r.Header.Get("X-Goog-FieldMask") == googleLocationFieldMask {
				locationMask = r.Header.Get("X-Goog-FieldMask")
				body = `{"ID":"place-1","FormattedAddress":"Kadıköy, İstanbul","Location":{"Latitude":40.99,"Longitude":29.03},"Types":["administrative_area_level_4"]}`
			} else {
				detailMask = r.Header.Get("X-Goog-FieldMask")
				body = `{"ID":"place-1","DisplayName":{"Text":"Test Mağaza"},"FormattedAddress":"Kadıköy, İstanbul","Location":{"Latitude":40.99,"Longitude":29.03},"Rating":4.4,"UserRatingCount":12,"Photos":[{"Name":"places/place-1/photos/photo"}],"Types":["furniture_store"],"NationalPhoneNumber":"123","WebsiteUri":"https://example.test","BusinessStatus":"CLOSED_PERMANENTLY"}`
			}
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	g := &GooglePlaces{key: "test-key", baseURL: "https://places.test", client: client}
	places, err := g.TextSearchLocalized(context.Background(), "Möbel", nil, nil, 1000, i18n.LocaleDE)
	if err != nil || len(places) != 1 || places[0].PlaceID != "place-1" || places[0].RatingCount != 0 || places[0].Rating != 0 || places[0].Phone != "" || places[0].Website != "" || places[0].PhotoName != "" || places[0].BusinessStatus != "CLOSED_PERMANENTLY" {
		t.Fatalf("text search places=%+v err=%v", places, err)
	}
	for _, forbidden := range []string{"rating", "userRatingCount", "regularOpeningHours", "utcOffsetMinutes", "nationalPhoneNumber", "websiteUri", "photos"} {
		if strings.Contains(searchMask, forbidden) {
			t.Errorf("search mask contains expensive field %q: %s", forbidden, searchMask)
		}
	}
	detail, err := g.PlaceDetails(context.Background(), "place-1")
	if err != nil || detail.PlaceID != "place-1" || detail.Name != "Test Mağaza" || detail.BusinessStatus != "CLOSED_PERMANENTLY" || !detail.DetailsFetched || detail.Phone != "123" || detail.Website != "https://example.test" || detail.PhotoName != "places/place-1/photos/photo" {
		t.Fatalf("details=%+v err=%v", detail, err)
	}
	if detailMask != googleDetailFieldMask {
		t.Errorf("detail mask=%q want=%q", detailMask, googleDetailFieldMask)
	}
	location, err := g.PlaceEssentials(context.Background(), "place-1")
	if err != nil || location.PlaceID != "place-1" || location.Name != "Kadıköy, İstanbul" || len(location.Types) != 1 {
		t.Fatalf("location=%+v err=%v", location, err)
	}
	if locationMask != googleLocationFieldMask {
		t.Errorf("location mask=%q want=%q", locationMask, googleLocationFieldMask)
	}
}
