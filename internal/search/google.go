package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/observability"
)

type GooglePlaces struct {
	key, baseURL string
	client       *http.Client
}

func NewGooglePlaces(key string) *GooglePlaces {
	return &GooglePlaces{key: key, baseURL: "https://places.googleapis.com/v1", client: &http.Client{Timeout: 4 * time.Second}}
}
func (g *GooglePlaces) TextSearch(ctx context.Context, q string, lat, lon *float64, radius int) ([]Place, error) {
	return g.TextSearchLocalized(ctx, q, lat, lon, radius, i18n.DefaultLocale)
}

func (g *GooglePlaces) TextSearchLocalized(ctx context.Context, q string, lat, lon *float64, radius int, locale i18n.Locale) ([]Place, error) {
	started := time.Now()
	ctx, finish := observability.StartSpan(ctx, "provider.google_places.text_search")
	out, err := g.textSearch(ctx, q, lat, lon, radius, locale)
	finish(err)
	observability.Provider("google_places", observability.Outcome(err), time.Since(started))
	return out, err
}

func (g *GooglePlaces) textSearch(ctx context.Context, q string, lat, lon *float64, radius int, locale i18n.Locale) ([]Place, error) {
	if g.key == "" {
		return nil, nil
	}
	body := map[string]any{"textQuery": q, "languageCode": string(locale), "maxResultCount": 20}
	if lat != nil && lon != nil {
		body["locationBias"] = map[string]any{"circle": map[string]any{"center": map[string]float64{"latitude": *lat, "longitude": *lon}, "radius": radius}}
	}
	b, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/places:searchText", bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.key)
	req.Header.Set("X-Goog-FieldMask", "places.id,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.types,places.attributions,places.photos")
	r, e := g.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, fmt.Errorf("places status %d", r.StatusCode)
	}
	var payload struct {
		Places []struct {
			ID          string `json:"id"`
			DisplayName struct {
				Text string `json:"text"`
			} `json:"displayName"`
			Address      string `json:"formattedAddress"`
			Location     struct{ Latitude, Longitude float64 }
			Rating       float64
			Count        int `json:"userRatingCount"`
			Types        []string
			Attributions []struct {
				Provider    string `json:"provider"`
				ProviderURI string `json:"providerUri"`
			}
			Photos []googlePhoto `json:"photos"`
		}
	}
	if e = json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&payload); e != nil {
		return nil, e
	}
	out := make([]Place, 0, len(payload.Places))
	for _, x := range payload.Places {
		p := Place{PlaceID: x.ID, Name: x.DisplayName.Text, Address: x.Address, Latitude: x.Location.Latitude, Longitude: x.Location.Longitude, Rating: x.Rating, RatingCount: x.Count, Types: x.Types}
		for _, a := range x.Attributions {
			p.Attributions = append(p.Attributions, a.Provider+" "+a.ProviderURI)
		}
		p.PhotoName, p.PhotoAttributions = firstPhoto(x.Photos)
		out = append(out, p)
	}
	return out, nil
}

type googlePhoto struct {
	Name               string `json:"name"`
	AuthorAttributions []struct {
		DisplayName string `json:"displayName"`
		URI         string `json:"uri"`
	} `json:"authorAttributions"`
}

// firstPhoto returns the leading photo resource name and its required author
// attributions. Google's terms require the attribution to be displayed with the photo.
func firstPhoto(photos []googlePhoto) (string, []string) {
	for _, photo := range photos {
		if !ValidPhotoName(photo.Name) {
			continue
		}
		attributions := make([]string, 0, len(photo.AuthorAttributions))
		for _, a := range photo.AuthorAttributions {
			if a.DisplayName != "" {
				attributions = append(attributions, a.DisplayName)
			}
		}
		return photo.Name, attributions
	}
	return "", nil
}
func (g *GooglePlaces) PlaceDetails(ctx context.Context, id string) (Place, error) {
	started := time.Now()
	ctx, finish := observability.StartSpan(ctx, "provider.google_places.place_details")
	out, err := g.placeDetails(ctx, id)
	finish(err)
	observability.Provider("google_places", observability.Outcome(err), time.Since(started))
	return out, err
}

func (g *GooglePlaces) placeDetails(ctx context.Context, id string) (Place, error) {
	if g.key == "" {
		return Place{}, fmt.Errorf("places not configured")
	}
	u := g.baseURL + "/places/" + url.PathEscape(id)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-Goog-Api-Key", g.key)
	req.Header.Set("X-Goog-FieldMask", "id,displayName,formattedAddress,location,rating,userRatingCount,types,attributions,photos")
	r, e := g.client.Do(req)
	if e != nil {
		return Place{}, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return Place{}, fmt.Errorf("places status %d", r.StatusCode)
	}
	var x struct {
		ID               string
		DisplayName      struct{ Text string }
		FormattedAddress string
		Location         struct{ Latitude, Longitude float64 }
		Rating           float64
		UserRatingCount  int
		Types            []string
		Photos           []googlePhoto `json:"photos"`
	}
	if e = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&x); e != nil {
		return Place{}, e
	}
	p := Place{PlaceID: x.ID, Name: x.DisplayName.Text, Address: x.FormattedAddress, Latitude: x.Location.Latitude, Longitude: x.Location.Longitude, Rating: x.Rating, RatingCount: x.UserRatingCount, Types: x.Types}
	p.PhotoName, p.PhotoAttributions = firstPhoto(x.Photos)
	return p, nil
}

// PhotoMedia streams a Google place photo. The caller must close the reader.
func (g *GooglePlaces) PhotoMedia(ctx context.Context, name string, maxWidth int) (io.ReadCloser, string, error) {
	started := time.Now()
	ctx, finish := observability.StartSpan(ctx, "provider.google_places.photo_media")
	body, contentType, err := g.photoMedia(ctx, name, maxWidth)
	finish(err)
	observability.Provider("google_places", observability.Outcome(err), time.Since(started))
	return body, contentType, err
}

func (g *GooglePlaces) photoMedia(ctx context.Context, name string, maxWidth int) (io.ReadCloser, string, error) {
	if g.key == "" {
		return nil, "", fmt.Errorf("places not configured")
	}
	if !ValidPhotoName(name) {
		return nil, "", fmt.Errorf("invalid photo name")
	}
	if maxWidth < 160 {
		maxWidth = 160
	}
	if maxWidth > 1600 {
		maxWidth = 1600
	}
	u := fmt.Sprintf("%s/%s/media?maxWidthPx=%d&skipHttpRedirect=false", g.baseURL, name, maxWidth)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, "", e
	}
	req.Header.Set("X-Goog-Api-Key", g.key)
	r, e := g.client.Do(req)
	if e != nil {
		return nil, "", e
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, "", fmt.Errorf("places status %d", r.StatusCode)
	}
	return r.Body, r.Header.Get("Content-Type"), nil
}
