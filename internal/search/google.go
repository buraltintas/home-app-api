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

	"github.com/burakaltintas/home-app-api/internal/observability"
)

type GooglePlaces struct {
	key    string
	client *http.Client
}

func NewGooglePlaces(key string) *GooglePlaces {
	return &GooglePlaces{key, &http.Client{Timeout: 4 * time.Second}}
}
func (g *GooglePlaces) TextSearch(ctx context.Context, q string, lat, lon *float64, radius int) ([]Place, error) {
	started := time.Now()
	out, err := g.textSearch(ctx, q, lat, lon, radius)
	observability.Provider("google_places", observability.Outcome(err), time.Since(started))
	return out, err
}

func (g *GooglePlaces) textSearch(ctx context.Context, q string, lat, lon *float64, radius int) ([]Place, error) {
	if g.key == "" {
		return nil, nil
	}
	body := map[string]any{"textQuery": q, "languageCode": "tr", "regionCode": "TR", "maxResultCount": 20}
	if lat != nil && lon != nil {
		body["locationBias"] = map[string]any{"circle": map[string]any{"center": map[string]float64{"latitude": *lat, "longitude": *lon}, "radius": radius}}
	}
	b, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://places.googleapis.com/v1/places:searchText", bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.key)
	req.Header.Set("X-Goog-FieldMask", "places.id,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.types,places.attributions")
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
		out = append(out, p)
	}
	return out, nil
}
func (g *GooglePlaces) PlaceDetails(ctx context.Context, id string) (Place, error) {
	started := time.Now()
	out, err := g.placeDetails(ctx, id)
	observability.Provider("google_places", observability.Outcome(err), time.Since(started))
	return out, err
}

func (g *GooglePlaces) placeDetails(ctx context.Context, id string) (Place, error) {
	if g.key == "" {
		return Place{}, fmt.Errorf("places not configured")
	}
	u := "https://places.googleapis.com/v1/places/" + url.PathEscape(id)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-Goog-Api-Key", g.key)
	req.Header.Set("X-Goog-FieldMask", "id,displayName,formattedAddress,location,rating,userRatingCount,types,attributions")
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
	}
	if e = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&x); e != nil {
		return Place{}, e
	}
	return Place{x.ID, x.DisplayName.Text, x.FormattedAddress, x.Location.Latitude, x.Location.Longitude, x.Rating, x.UserRatingCount, x.Types, nil}, nil
}
