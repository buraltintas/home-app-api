package search

import (
	"context"
	"fmt"
	"strings"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
)

type Intent struct {
	NormalizedQuery string   `json:"normalized_query"`
	LocationText    string   `json:"location_text"`
	Categories      []string `json:"categories"`
	ProductTerms    []string `json:"product_terms"`
	StyleTerms      []string `json:"style_terms"`
	PriceIntent     string   `json:"price_intent"`
	Attributes      []string `json:"attributes"`
	SortPreference  string   `json:"sort_preference"`
	SemanticTerms   []string `json:"semantic_terms"`
}
type Context struct {
	Latitude, Longitude *float64
	Locale              string
}
type IntentParser interface {
	ParseSearchIntent(context.Context, string, Context) (Intent, error)
}
type Place struct {
	PlaceID, Name, Address string
	Latitude, Longitude    float64
	Rating                 float64
	RatingCount            int
	Types                  []string
	Attributions           []string
}
type PlacesProvider interface {
	TextSearch(context.Context, string, *float64, *float64, int) ([]Place, error)
	PlaceDetails(context.Context, string) (Place, error)
}
type Platform struct {
	StoreID       uuid.UUID `json:"store_id"`
	AverageRating float64   `json:"average_rating"`
	ReviewCount   int       `json:"review_count"`
	FavoriteCount int       `json:"favorite_count"`
}
type External struct {
	Provider    string  `json:"provider"`
	PlaceID     string  `json:"place_id"`
	Rating      float64 `json:"rating"`
	RatingCount int     `json:"rating_count"`
}
type Result struct {
	ID              *uuid.UUID `json:"id,omitempty"`
	Source          string     `json:"source"`
	Name            string     `json:"name"`
	Address         string     `json:"address"`
	City            string     `json:"city,omitempty"`
	District        string     `json:"district,omitempty"`
	Latitude        float64    `json:"latitude"`
	Longitude       float64    `json:"longitude"`
	DistanceMeters  *float64   `json:"distance_meters,omitempty"`
	Categories      []string   `json:"categories"`
	Platform        *Platform  `json:"platform,omitempty"`
	Google          *External  `json:"google,omitempty"`
	score           float64
	externalPlaceID string
}
type Request struct {
	Query        string   `json:"query"`
	Latitude     *float64 `json:"latitude"`
	Longitude    *float64 `json:"longitude"`
	RadiusMeters int      `json:"radius_meters"`
}
type Response struct {
	SearchID         uuid.UUID  `json:"search_id"`
	VisitorSessionID *uuid.UUID `json:"visitor_session_id,omitempty"`
	Intent           Intent     `json:"intent"`
	Results          []Result   `json:"results"`
	FallbackState    string     `json:"fallback_state,omitempty"`
}

func Deterministic(raw string) Intent {
	n := strings.ToLower(strings.TrimSpace(raw))
	i := Intent{NormalizedQuery: n, SortPreference: "relevance"}
	dict := map[string][]string{"lighting": {"avize", "aydınlatma", "lamba"}, "curtain": {"perde"}, "furniture": {"mobilya", "koltuk", "masa", "sandalye", "çocuk odası"}, "home_textile": {"tekstil", "nevresim"}, "carpet": {"halı", "kilim"}, "decoration": {"dekorasyon", "dekor"}, "kitchenware": {"mutfak", "tencere"}, "bathroom": {"banyo"}, "bedding": {"yatak"}, "tableware": {"sofra", "tabak"}, "storage": {"depolama", "dolap"}}
	for category, terms := range dict {
		for _, term := range terms {
			if strings.Contains(n, term) {
				i.Categories = appendUnique(i.Categories, category)
				i.ProductTerms = appendUnique(i.ProductTerms, term)
			}
		}
	}
	if containsAny(n, "uygun fiyat", "ucuz", "ekonomik", "çok pahalı olmayan") {
		i.PriceIntent = "budget"
	}
	for _, style := range []string{"modern", "minimalist", "klasik", "rustik", "iskandinav"} {
		if strings.Contains(n, style) {
			i.StyleTerms = append(i.StyleTerms, style)
		}
	}
	if containsAny(n, "büyük", "geniş seçenek", "çok çeşitli") {
		i.Attributes = append(i.Attributes, "large_selection")
	}
	for _, city := range []string{"istanbul", "ankara", "antalya", "izmir", "bursa", "adana", "konya", "kadıköy", "çankaya", "lara"} {
		if strings.Contains(n, city) {
			i.LocationText = city
			break
		}
	}
	return i
}
func Validate(i Intent) error {
	allowedCat := map[string]bool{"furniture": true, "home_textile": true, "lighting": true, "decoration": true, "kitchenware": true, "bathroom": true, "carpet": true, "curtain": true, "bedding": true, "tableware": true, "storage": true, "home_accessories": true, "household": true}
	if len(i.Categories) > 8 || len(i.ProductTerms) > 12 || len(i.StyleTerms) > 8 || len(i.Attributes) > 8 {
		return fmt.Errorf("too many intent values")
	}
	for _, c := range i.Categories {
		if !allowedCat[c] {
			return fmt.Errorf("unknown category")
		}
		if len(c) > 40 {
			return fmt.Errorf("category too long")
		}
	}
	if i.PriceIntent != "" && i.PriceIntent != "budget" && i.PriceIntent != "midrange" && i.PriceIntent != "premium" {
		return fmt.Errorf("invalid price")
	}
	if i.SortPreference != "" && i.SortPreference != "relevance" && i.SortPreference != "distance" && i.SortPreference != "rating" && i.SortPreference != "popularity" {
		return fmt.Errorf("invalid sort")
	}
	return nil
}
func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
func containsAny(s string, terms ...string) bool {
	for _, x := range terms {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
func fromStore(x storepkg.Item) Result {
	p := &Platform{x.ID, x.Platform.AverageRating, x.Platform.ReviewCount, x.Platform.FavoriteCount}
	return Result{ID: &x.ID, Source: "platform", Name: x.Name, Address: x.Address, City: x.City, District: x.District, Latitude: x.Latitude, Longitude: x.Longitude, DistanceMeters: x.DistanceMeters, Categories: x.Categories, Platform: p, score: 100 + float64(x.Platform.ReviewCount)}
}
