package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

type Intent struct {
	QueryLanguage   i18n.Locale `json:"query_language"`
	NormalizedQuery string      `json:"normalized_query"`
	LocationText    string      `json:"location_text"`
	Categories      []string    `json:"categories"`
	ProductTerms    []string    `json:"product_terms"`
	StyleTerms      []string    `json:"style_terms"`
	PriceIntent     string      `json:"price_intent"`
	Attributes      []string    `json:"attributes"`
	SortPreference  string      `json:"sort_preference"`
	SemanticTerms   []string    `json:"semantic_terms"`
}
type Context struct {
	Latitude, Longitude *float64
	Locale              i18n.Locale
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
type LocalizedPlacesProvider interface {
	TextSearchLocalized(context.Context, string, *float64, *float64, int, i18n.Locale) ([]Place, error)
}
type Platform struct {
	StoreID       uuid.UUID `json:"store_id"`
	AverageRating float64   `json:"average_rating"`
	ReviewCount   int       `json:"review_count"`
	FavoriteCount int       `json:"favorite_count"`
	PostCount     int       `json:"post_count"`
}
type External struct {
	Provider    string  `json:"provider"`
	PlaceID     string  `json:"place_id"`
	Rating      float64 `json:"rating"`
	RatingCount int     `json:"rating_count"`
}
type Result struct {
	ID              *uuid.UUID `json:"id,omitempty"`
	ImpressionID    uuid.UUID  `json:"search_result_impression_id"`
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
	n := normalizeText(raw)
	folded := foldLatin(n)
	i := Intent{QueryLanguage: DetectLanguage(raw), NormalizedQuery: n, SortPreference: "relevance"}
	type concept struct {
		category string
		product  string
		terms    []string
		extra    []string
	}
	concepts := []concept{
		{"lighting", "chandelier", []string{"avize", "aydınlatma", "lamba", "lighting", "chandelier", "lamp", "beleuchtung", "leuchter", "lampe", "освещение", "люстра", "лампа"}, nil},
		{"curtain", "curtain", []string{"perde", "curtain", "gardine", "vorhang", "штор", "занавес"}, []string{"home_textile"}},
		{"furniture", "furniture", []string{"mobilya", "koltuk", "masa", "sandalye", "furniture", "sofa", "table", "chair", "möbel", "sofa", "tisch", "stuhl", "мебел", "диван", "стол", "стул"}, nil},
		{"home_textile", "home_textile", []string{"tekstil", "nevresim", "home textile", "bedding textile", "heimtextil", "домашний текстиль"}, nil},
		{"carpet", "carpet", []string{"halı", "kilim", "carpet", "rug", "teppich", "ковер", "ковёр"}, nil},
		{"decoration", "decoration", []string{"dekorasyon", "dekor", "decoration", "decor", "dekoration", "декор"}, nil},
		{"kitchenware", "kitchenware", []string{"mutfak", "tencere", "kitchenware", "cookware", "küchenbedarf", "кухонные товары", "посуда"}, nil},
		{"bathroom", "bathroom", []string{"banyo", "bathroom", "badezimmer", "ванная"}, nil},
		{"bedding", "bedding", []string{"yatak", "bedding", "bettwaren", "постель"}, nil},
		{"tableware", "tableware", []string{"sofra", "tabak", "tableware", "geschirr", "посуда"}, nil},
		{"storage", "storage", []string{"depolama", "dolap", "storage", "aufbewahrung", "хранение", "шкаф"}, nil},
	}
	for _, concept := range concepts {
		for _, term := range concept.terms {
			if containsNormalized(n, folded, term) {
				i.Categories = appendUnique(i.Categories, concept.category)
				for _, category := range concept.extra {
					i.Categories = appendUnique(i.Categories, category)
				}
				i.ProductTerms = appendUnique(i.ProductTerms, concept.product)
				break
			}
		}
	}
	if containsAnyFolded(n, folded, "uygun fiyat", "ucuz", "ekonomik", "çok pahalı olmayan", "affordable", "cheap", "budget", "günstig", "preiswert", "nicht teuer", "недорог", "дешев", "бюджет") {
		i.PriceIntent = "budget"
	}
	styles := map[string][]string{"modern": {"modern", "современн"}, "minimal": {"minimal", "минимал"}, "classic": {"klasik", "classic", "klassisch", "классическ"}, "rustic": {"rustik", "rustic", "rustikal", "рустик"}, "scandinavian": {"iskandinav", "scandinavian", "skandinavisch", "скандинав"}}
	for key, terms := range styles {
		for _, term := range terms {
			if containsNormalized(n, folded, term) {
				i.StyleTerms = appendUnique(i.StyleTerms, key)
				break
			}
		}
	}
	if containsAnyFolded(n, folded, "büyük", "geniş seçenek", "çok çeşitli", "large selection", "wide range", "große auswahl", "широкий выбор") {
		i.Attributes = append(i.Attributes, "large_selection")
	}
	locations := map[string][]string{"Istanbul": {"istanbul", "стамбул"}, "Ankara": {"ankara", "анкара"}, "Antalya": {"antalya", "антал"}, "Izmir": {"izmir", "измир"}, "Bursa": {"bursa", "бурса"}, "Berlin": {"berlin", "берлин"}, "Kadikoy": {"kadıköy", "kadikoy", "кадыкёй", "кадыкей"}, "Cankaya": {"çankaya", "cankaya", "чанкая"}, "Lara": {"lara", "лара"}}
	for canonical, terms := range locations {
		for _, term := range terms {
			if containsNormalized(n, folded, term) {
				i.LocationText = canonical
				break
			}
		}
		if i.LocationText != "" {
			break
		}
	}
	return i
}

func DetectLanguage(raw string) i18n.Locale {
	for _, r := range raw {
		if unicode.In(r, unicode.Cyrillic) {
			return i18n.LocaleRU
		}
	}
	n := strings.ToLower(raw)
	if strings.ContainsAny(n, "ıİşŞğĞçÇ") || containsAny(n, " mağaza", " perde", " mobilya", " uygun fiyat") {
		return i18n.LocaleTR
	}
	if strings.ContainsAny(n, "äß") || containsAny(n, "geschäft", "günstig", "möbel", "gardine") {
		return i18n.LocaleDE
	}
	return i18n.LocaleEN
}

func normalizeText(raw string) string {
	return norm.NFC.String(strings.ToLower(strings.TrimSpace(raw)))
}

func foldLatin(raw string) string {
	raw = strings.NewReplacer("ı", "i", "ß", "ss").Replace(raw)
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(raw))
}

func containsNormalized(normalized, folded, term string) bool {
	term = normalizeText(term)
	return strings.Contains(normalized, term) || strings.Contains(folded, foldLatin(term))
}

func containsAnyFolded(normalized, folded string, terms ...string) bool {
	for _, term := range terms {
		if containsNormalized(normalized, folded, term) {
			return true
		}
	}
	return false
}
func Validate(i Intent) error {
	if i.QueryLanguage != "" && !i18n.IsSupported(i.QueryLanguage) {
		return fmt.Errorf("unsupported query language")
	}
	allowedCat := map[string]bool{"furniture": true, "home_textile": true, "lighting": true, "decoration": true, "kitchenware": true, "bathroom": true, "carpet": true, "curtain": true, "bedding": true, "tableware": true, "storage": true, "home_accessories": true, "household": true}
	if utf8.RuneCountInString(i.NormalizedQuery) > 500 || utf8.RuneCountInString(i.LocationText) > 120 {
		return fmt.Errorf("intent text too long")
	}
	if len(i.Categories) > 8 || len(i.ProductTerms) > 12 || len(i.StyleTerms) > 8 || len(i.Attributes) > 8 || len(i.SemanticTerms) > 12 {
		return fmt.Errorf("too many intent values")
	}
	for _, c := range i.Categories {
		if !allowedCat[c] {
			return fmt.Errorf("unknown category")
		}
		if utf8.RuneCountInString(c) > 40 {
			return fmt.Errorf("category too long")
		}
	}
	for _, values := range [][]string{i.ProductTerms, i.StyleTerms, i.Attributes, i.SemanticTerms} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 80 || strings.ContainsAny(value, "\x00\r\n") {
				return fmt.Errorf("invalid intent value")
			}
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
	p := &Platform{StoreID: x.ID, AverageRating: x.Platform.AverageRating, ReviewCount: x.Platform.ReviewCount, FavoriteCount: x.Platform.FavoriteCount, PostCount: x.Platform.PostCount}
	return Result{ID: &x.ID, Source: "internal", Name: x.Name, Address: x.Address, City: x.City, District: x.District, Latitude: x.Latitude, Longitude: x.Longitude, DistanceMeters: x.DistanceMeters, Categories: x.Categories, Platform: p, score: 100 + float64(x.Platform.ReviewCount)}
}
