package search

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// The enum tags are the contract handed to the model as a structured-output schema.
// Without them the model answers with display copy such as "yemek takımı", Validate
// rejects the whole intent, and every search silently degrades to the deterministic
// parser. Keep these in sync with Validate below.
type Intent struct {
	Scope           string      `json:"scope" jsonschema:"enum=home_living,enum=out_of_scope,enum=unclear"`
	QueryLanguage   i18n.Locale `json:"query_language" jsonschema:"enum=tr,enum=en,enum=de,enum=ru"`
	NormalizedQuery string      `json:"normalized_query"`
	StoreName       string      `json:"store_name"`
	LocationText    string      `json:"location_text"`
	Categories      []string    `json:"categories" jsonschema:"enum=furniture,enum=home_textile,enum=lighting,enum=decoration,enum=kitchenware,enum=bathroom,enum=carpet,enum=curtain,enum=bedding,enum=tableware,enum=storage,enum=home_accessories,enum=household"`
	ProductTerms    []string    `json:"product_terms"`
	StyleTerms      []string    `json:"style_terms"`
	PriceIntent     string      `json:"price_intent" jsonschema:"enum=,enum=budget,enum=midrange,enum=premium"`
	Attributes      []string    `json:"attributes"`
	SortPreference  string      `json:"sort_preference" jsonschema:"enum=,enum=relevance,enum=distance,enum=rating,enum=popularity"`
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
	PhotoName              string
	PhotoAttributions      []string
	// The number Google publishes in national format. It is asked for in the same field
	// mask that already carries the rating, so it is billed at the tier we were already
	// paying for -- no separate lookup and no extra request per store.
	Phone string
}
type PlacesProvider interface {
	TextSearch(context.Context, string, *float64, *float64, int) ([]Place, error)
	PlaceDetails(context.Context, string) (Place, error)
}

// photoNamePattern constrains a Google photo resource name. The value is
// interpolated into a provider URL, so anything outside this shape is rejected.
// The character class is what prevents escaping that URL; the length only has to
// admit a real reference, which Google issues at well over four hundred characters.
var photoNamePattern = regexp.MustCompile(`^places/[A-Za-z0-9_-]{1,300}/photos/[A-Za-z0-9_-]{1,1000}$`)

func ValidPhotoName(name string) bool { return photoNamePattern.MatchString(name) }

type LocationResult struct {
	Provider     string   `json:"provider"`
	PlaceID      string   `json:"place_id"`
	Name         string   `json:"name"`
	Address      string   `json:"address"`
	Latitude     float64  `json:"latitude"`
	Longitude    float64  `json:"longitude"`
	Types        []string `json:"types"`
	Attributions []string `json:"attributions"`
}

// A provider that can answer partial input. Kept separate from PlacesProvider so the
// test doubles for store search do not have to grow a method they never exercise.
type AutocompleteProvider interface {
	Autocomplete(ctx context.Context, input string, locale i18n.Locale, lat, lon *float64) ([]Place, error)
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
	Provider          string   `json:"provider"`
	PlaceID           string   `json:"place_id"`
	Rating            float64  `json:"rating"`
	RatingCount       int      `json:"rating_count"`
	PhotoName         string   `json:"photo_name,omitempty"`
	PhotoAttributions []string `json:"photo_attributions,omitempty"`
}

// Photo mirrors the stored provider photograph. Attribution travels with it because the
// provider terms require the credit to be displayed wherever the photograph is.
type Photo struct {
	Source       string   `json:"source"`
	MediaID      string   `json:"media_id,omitempty"`
	Name         string   `json:"name,omitempty"`
	Attributions []string `json:"attributions,omitempty"`
}
type Result struct {
	ID             *uuid.UUID `json:"id,omitempty"`
	ImpressionID   uuid.UUID  `json:"search_result_impression_id"`
	Source         string     `json:"source"`
	Name           string     `json:"name"`
	Address        string     `json:"address"`
	City           string     `json:"city,omitempty"`
	District       string     `json:"district,omitempty"`
	Latitude       float64    `json:"latitude"`
	Longitude      float64    `json:"longitude"`
	DistanceMeters *float64   `json:"distance_meters,omitempty"`
	Categories     []string   `json:"categories"`
	Platform       *Platform  `json:"platform,omitempty"`
	Google         *External  `json:"google,omitempty"`
	// Photo is the photograph already on file for this store, used when the live provider
	// response carries none. Without it a store we hold ourselves -- including every
	// promoted one, which reaches the list without going through Google at all -- renders
	// as a blank tile beside imported results that have a picture.
	Photo *Photo `json:"photo,omitempty"`
	// A store's public telephone number. Listing it is what lets someone ask a question
	// without leaving for Google, and it is the one detail a person wants before making a
	// trip that a photograph cannot answer.
	Phone string `json:"phone,omitempty"`
	// Paid placement. The client must label it: promotion that cannot be told apart from
	// an organic result is exactly what consumer rules prohibit, and /about and /terms
	// already promise it is marked wherever it applies.
	Premium bool `json:"premium,omitempty"`
	score   float64
	// Set when the visitor named a store and this result is one of them. Ranking needs to
	// tell "the shop you asked for" from "a shop like it"; without the distinction a chain
	// with several branches came back in no useful order at all.
	nameHit         bool
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
	Guidance         *Guidance  `json:"guidance,omitempty"`
	FallbackState    string     `json:"fallback_state,omitempty"`
}

type Guidance struct {
	Code     string   `json:"code"`
	Reason   string   `json:"reason"`
	Message  string   `json:"message"`
	Examples []string `json:"examples"`
}

const (
	ScopeHomeLiving = "home_living"
	ScopeOutOfScope = "out_of_scope"
	ScopeUnclear    = "unclear"
)

type homeConcept struct {
	category string
	product  string
	terms    []string
	extra    []string
}

var homeConcepts = []homeConcept{
	{"lighting", "chandelier", []string{"avize", "aydınlatma", "lamba", "elektrik aydınlatma", "elektrik ve aydınlatma", "elektrik malzemeleri", "elektrik mağazası", "lighting", "chandelier", "lamp", "electrical lighting", "lighting supplies", "beleuchtung", "leuchter", "lampe", "электротовары", "освещение", "люстра", "лампа"}, nil},
	{"curtain", "curtain", []string{"perde", "curtain", "gardine", "vorhang", "штор", "занавес"}, []string{"home_textile"}},
	{"furniture", "furniture", []string{"mobilya", "koltuk", "masa", "sandalye", "furniture", "sofa", "table", "chair", "möbel", "sofa", "tisch", "stuhl", "мебел", "диван", "стол", "стул"}, nil},
	{"home_textile", "home_textile", []string{"tekstil", "nevresim", "çarşaf", "yorgan", "battaniye", "havlu", "pike", "home textile", "duvet cover", "bed linen", "blanket", "towel", "bedding textile", "heimtextil", "bettwäsche", "handtuch", "домашний текстиль", "постельное белье", "одеяло", "полотенце"}, nil},
	{"carpet", "carpet", []string{"halı", "kilim", "carpet", "rug", "teppich", "ковер", "ковёр"}, nil},
	{"decoration", "decoration", []string{"dekorasyon", "dekor", "decoration", "decor", "dekoration", "декор", "ayna", "mirror", "spiegel", "зеркало"}, nil},
	{"kitchenware", "kitchenware", []string{"mutfak", "tencere", "kitchenware", "cookware", "küchenbedarf", "кухонные товары", "посуда"}, nil},
	{"bathroom", "bathroom", []string{"banyo", "bathroom", "badezimmer", "ванная"}, nil},
	{"bedding", "bedding", []string{"yatak", "bedding", "bettwaren", "постель"}, nil},
	{"tableware", "tableware", []string{"sofra", "tabak", "tableware", "geschirr", "посуда"}, nil},
	{"storage", "storage", []string{"depolama", "dolap", "storage", "aufbewahrung", "хранение", "шкаф"}, nil},
	{"household", "home_appliance", []string{"beyaz eşya", "beyaz esya", "white goods", "home appliance", "household appliance", "haushaltsgerät", "haushaltsgerat", "бытовая техника"}, nil},
}

// Generic words that name a home store without naming a product. These are matched as
// whole words, not substrings, which the product terms above do not need to be: "halı"
// inside a longer word is still about carpets, but "home" inside "Homeros" and "ev" inside
// "Evren" are about nothing at all.
//
// The gap these close was large. Nearly a fifth of imported stores carried no category,
// and the list included English Home, Nilda Home, Evim Home Goods and Deco Home Ev
// Aksesuar -- shops whose whole business is in their name. A store with no category cannot
// be found by anything that filters on one, and shows a blank where its categories belong.
var homeWordConcepts = []homeConcept{
	{"home_accessories", "home_goods", []string{"home", "homeware", "home goods", "ev aksesuar", "ev aksesuarları", "ev aksesuarlari", "züccaciye", "zuccaciye", "ev gereçleri", "ev gerecleri", "haushaltswaren", "wohnaccessoires", "товары для дома"}, nil},
	{"home_textile", "home_textile", []string{"ev tekstil", "ev tekstili", "home textile"}, nil},
}

// containsWord reports whether the term appears as a whole word. A letter on either side
// disqualifies the match; punctuation, spaces and string edges do not.
func containsWord(normalized, folded, term string) bool {
	for _, haystack := range [2]string{normalized, foldLatin(folded)} {
		needle := normalizeText(term)
		if haystack != normalized {
			needle = foldLatin(needle)
		}
		if needle == "" {
			continue
		}
		for offset := 0; ; {
			index := strings.Index(haystack[offset:], needle)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(needle)
			beforeOK := start == 0 || !isWordRune(rune(haystack[start-1]))
			afterOK := end == len(haystack) || !isWordRune(rune(haystack[end]))
			if beforeOK && afterOK {
				return true
			}
			offset = start + 1
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
}

func Deterministic(raw string) Intent {
	n := normalizeText(raw)
	folded := foldLatin(n)
	i := Intent{Scope: ScopeUnclear, QueryLanguage: DetectLanguage(raw), NormalizedQuery: n, Categories: []string{}, ProductTerms: []string{}, StyleTerms: []string{}, Attributes: []string{}, SortPreference: "relevance", SemanticTerms: []string{}}
	knownHomeStores := []string{"ikea", "koçtaş", "koctas", "madame coco", "english home", "karaca", "paşabahçe", "pasabahce", "vivense", "bellona", "istikbal", "istikbal mobilya", "taç", "tac", "işbir", "isbir", "yataş", "yatas", "lova"}
	for _, storeName := range knownHomeStores {
		if containsNormalized(n, folded, storeName) {
			i.StoreName = storeName
			i.Scope = ScopeHomeLiving
			break
		}
	}
	if containsAnyFolded(n, folded, "çeyiz", "ceyiz", "dowry", "aussteuer", "приданое") {
		i.Categories = appendUnique(i.Categories, "home_textile")
		i.Categories = appendUnique(i.Categories, "bedding")
		i.Categories = appendUnique(i.Categories, "kitchenware")
		i.Categories = appendUnique(i.Categories, "tableware")
		i.ProductTerms = appendUnique(i.ProductTerms, "dowry_set")
		i.SemanticTerms = appendUnique(i.SemanticTerms, "home dowry shopping")
	}
	if containsAnyFolded(n, folded, "nevresim takımı", "nevresim takimi", "duvet cover set", "bettwäsche set", "комплект постельного белья") {
		i.Categories = appendUnique(i.Categories, "home_textile")
		i.Categories = appendUnique(i.Categories, "bedding")
		i.ProductTerms = appendUnique(i.ProductTerms, "bedding_set")
	}
	for _, concept := range homeConcepts {
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
	if i.StoreName != "" || len(i.Categories) > 0 || len(i.ProductTerms) > 0 {
		i.Scope = ScopeHomeLiving
	} else if containsAnyFolded(n, folded, "lastikçi", "lastikci", "lastik", "tire shop", "tyre shop", "reifen", "шиномонтаж", "restoran", "restaurant", "kuaför", "kuafor", "berber", "hairdresser", "eczane", "pharmacy", "oto servis", "car repair", "elektrikçi", "elektrikci", "electrician", "elektrik tesisat", "elektrik arıza", "elektrik ariza", "elektrik faturası", "elektrik faturasi") {
		i.Scope = ScopeOutOfScope
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
	// Electrical and lighting retailers belong in home and living; an electrician or
	// repair request does not. A short query shaped like "Yeğenler Elektrik Antalya"
	// is also a business name plus a location, not a request for every business whose
	// name happens to contain Antalya.
	if i.StoreName == "" {
		if candidate := inferredElectricalStoreName(raw, i.LocationText); candidate != "" {
			i.StoreName = candidate
			i.Categories = appendUnique(i.Categories, "lighting")
			i.ProductTerms = appendUnique(i.ProductTerms, "lighting")
			i.Scope = ScopeHomeLiving
		}
	}
	if i.Scope != ScopeHomeLiving {
		i.StoreName = ""
		i.Categories = i.Categories[:0]
		i.ProductTerms = i.ProductTerms[:0]
		i.StyleTerms = i.StyleTerms[:0]
		i.PriceIntent = ""
		i.Attributes = i.Attributes[:0]
		i.SemanticTerms = i.SemanticTerms[:0]
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

// nameMatches reports whether a result name contains the store name the user asked
// for, ignoring case and Turkish diacritics.
func nameMatches(resultName, storeName string) bool {
	storeName = strings.TrimSpace(storeName)
	if storeName == "" {
		return false
	}
	normalized := normalizeText(resultName)
	return containsNormalized(normalized, foldLatin(normalized), storeName)
}

func containsAnyFolded(normalized, folded string, terms ...string) bool {
	for _, term := range terms {
		if containsNormalized(normalized, folded, term) {
			return true
		}
	}
	return false
}

func nameWords(raw string) []string {
	return strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func sameFoldedWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if foldLatin(normalizeText(a[index])) != foldLatin(normalizeText(b[index])) {
			return false
		}
	}
	return true
}

// stripEdgeLocation removes a location the parser repeated at the beginning or end of a
// store name. Middle words stay untouched because Antalya can legitimately be part of a
// registered name such as "Güney Antalya Halı".
func stripEdgeLocation(storeName, location string) string {
	storeWords := nameWords(storeName)
	locationWords := nameWords(location)
	if len(storeWords) == 0 || len(locationWords) == 0 || len(storeWords) <= len(locationWords) {
		return strings.TrimSpace(storeName)
	}
	if sameFoldedWords(storeWords[:len(locationWords)], locationWords) {
		storeWords = storeWords[len(locationWords):]
	} else if sameFoldedWords(storeWords[len(storeWords)-len(locationWords):], locationWords) {
		storeWords = storeWords[:len(storeWords)-len(locationWords)]
	}
	return strings.TrimSpace(strings.Join(storeWords, " "))
}

func inferredElectricalStoreName(raw, location string) string {
	normalized := normalizeText(raw)
	folded := foldLatin(normalized)
	if !containsAnyFolded(normalized, folded, "elektrik", "electric", "elektro") ||
		containsAnyFolded(normalized, folded, "elektrikçi", "elektrikci", "electrician", "tesisat", "tamir", "arıza", "ariza", "fatura", "repair", "installation") {
		return ""
	}
	candidate := stripEdgeLocation(raw, location)
	words := nameWords(candidate)
	if len(words) < 2 || containsAnyFolded(normalized, folded, "mağaza", "magaza", "store", "malzeme", "aydınlatma", "lighting", "ürün", "urun", "arıyorum", "ariyorum", "lazım", "lazim", "yakınımda", "yakinimda") {
		return ""
	}
	return candidate
}
func Validate(i Intent) error {
	if i.Scope != ScopeHomeLiving && i.Scope != ScopeOutOfScope && i.Scope != ScopeUnclear {
		return fmt.Errorf("invalid search scope")
	}
	if i.QueryLanguage != "" && !i18n.IsSupported(i.QueryLanguage) {
		return fmt.Errorf("unsupported query language")
	}
	allowedCat := map[string]bool{"furniture": true, "home_textile": true, "lighting": true, "decoration": true, "kitchenware": true, "bathroom": true, "carpet": true, "curtain": true, "bedding": true, "tableware": true, "storage": true, "home_accessories": true, "household": true}
	if utf8.RuneCountInString(i.NormalizedQuery) > 500 || utf8.RuneCountInString(i.StoreName) > 160 || utf8.RuneCountInString(i.LocationText) > 120 {
		return fmt.Errorf("intent text too long")
	}
	if i.StoreName != "" && (strings.TrimSpace(i.StoreName) == "" || strings.ContainsAny(i.StoreName, "\x00\r\n")) {
		return fmt.Errorf("invalid store name")
	}
	if i.Scope != ScopeHomeLiving && (i.StoreName != "" || len(i.Categories) > 0 || len(i.ProductTerms) > 0 || len(i.StyleTerms) > 0 || i.PriceIntent != "" || len(i.Attributes) > 0 || len(i.SemanticTerms) > 0) {
		return fmt.Errorf("non-home intent contains search terms")
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
func fromStore(x storepkg.Item, rank int) Result {
	p := &Platform{StoreID: x.ID, AverageRating: x.Platform.AverageRating, ReviewCount: x.Platform.ReviewCount, FavoriteCount: x.Platform.FavoriteCount, PostCount: x.Platform.PostCount}
	var photo *Photo
	if x.Photo != nil {
		switch {
		case x.Photo.Source == "admin" && x.Photo.MediaID != "":
			photo = &Photo{Source: "admin", MediaID: x.Photo.MediaID}
		case x.Photo.Source == "google" && ValidPhotoName(x.Photo.Name):
			photo = &Photo{Source: "google", Name: x.Photo.Name, Attributions: x.Photo.Attributions}
		}
	}
	return Result{ID: &x.ID, Source: "internal", Name: x.Name, Address: x.Address, City: x.City, District: x.District, Latitude: x.Latitude, Longitude: x.Longitude, DistanceMeters: x.DistanceMeters, Categories: append([]string{}, x.Categories...), Platform: p, Photo: photo, Phone: x.Phone, Premium: x.IsPremium, score: platformScore(*p, rank)}
}

func platformScore(p Platform, relevanceRank int) float64 {
	if p.ReviewCount > 0 {
		return 300 + p.AverageRating*10 + math.Log1p(float64(p.ReviewCount))*15 + math.Log1p(float64(p.FavoriteCount))*3 - float64(relevanceRank)
	}
	return 80 - float64(relevanceRank)
}

// googleTypeCategories maps the Places types worth trusting onto our own taxonomy. Google's
// types are coarse -- a carpet shop and a sofa shop are both home_goods_store -- so this
// only carries the ones that are unambiguous.
var googleTypeCategories = map[string]string{
	"furniture_store":        "furniture",
	"home_goods_store":       "home_accessories",
	"lighting_store":         "lighting",
	"rug_store":              "carpet",
	"carpet_store":           "carpet",
	"bed_and_mattress_store": "bedding",
	"kitchen_supply_store":   "kitchenware",
	"curtain_store":          "curtain",
	"home_improvement_store": "household",
	"department_store":       "home_accessories",
}

// StoreCategories works out what a store actually sells, for a store being imported from
// Google. Until now imported stores were given no categories at all, and the categories
// shown next to a result came from the search that happened to find it -- so a shop plainly
// named "... HALI ..." carried no carpet category, and could not be found by anything that
// filtered on one.
//
// Two sources, because neither is enough alone. Google's types are reliable but coarse, and
// Turkish shops very often carry the category in the name ("... HALI", "... PERDE"). Only
// explicit product words count here. A shopper asking for a dowry set can legitimately
// fan out to several categories, but a store merely named "... ÇEYİZ" must not thereby be
// labelled as selling beds, cookware and tableware.
func StoreCategories(name string, types []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(slug string) {
		if slug != "" && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	for _, t := range types {
		add(googleTypeCategories[strings.ToLower(strings.TrimSpace(t))])
	}
	normalized := normalizeText(name)
	folded := foldLatin(normalized)
	for _, concept := range homeWordConcepts {
		for _, term := range concept.terms {
			if containsWord(normalized, folded, term) {
				add(concept.category)
				break
			}
		}
	}
	for _, concept := range homeConcepts {
		for _, term := range concept.terms {
			if containsNormalized(normalized, folded, term) {
				add(concept.category)
				for _, slug := range concept.extra {
					add(slug)
				}
				break
			}
		}
	}
	return out
}

func googleScore(p Place, relevanceRank int) float64 {
	return 100 + p.Rating*4 + math.Log1p(float64(p.RatingCount))*2 - float64(relevanceRank)
}

// A store we already carry must never rank below the same store seen only through
// Google. Before this, a mapped store with no community reviews scored 80 while an
// identical Google-only result scored well past 100, so knowing a place pushed it down.
func mergedScore(p Platform, g Place, relevanceRank int) float64 {
	if p.ReviewCount > 0 {
		return platformScore(p, relevanceRank)
	}
	return googleScore(g, relevanceRank)
}

// cityKey reduces a formatted Turkish address to something two results can be compared
// on. "Kızıltoprak, Aspendos Blv. No:41, 07230 Muratpaşa/Antalya, Türkiye" becomes
// "antalya", which is what lets a searcher's own city be recognised without a reverse
// geocoding round trip.
func cityKey(city, address string) string {
	value := strings.TrimSpace(city)
	if value == "" {
		parts := strings.Split(address, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			part := strings.TrimSpace(parts[i])
			low := strings.ToLower(part)
			if part == "" || low == "türkiye" || low == "turkey" || strings.ToUpper(part) == "TR" {
				continue
			}
			value = part
			break
		}
	}
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	value = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(value), "0123456789"))
	return foldLatin(strings.ToLower(strings.TrimSpace(value)))
}

// The searcher's own city is the city of whatever turned out to be closest to them.
// It costs nothing and is far more reliable than trusting a typed location string.
func homeCity(results []Result) string {
	nearest := math.MaxFloat64
	home := ""
	for _, r := range results {
		if r.DistanceMeters != nil && *r.DistanceMeters < nearest {
			if key := cityKey(r.City, r.Address); key != "" {
				nearest, home = *r.DistanceMeters, key
			}
		}
	}
	return home
}

// Distance is banded rather than subtracted so that relevance can still order places that
// are genuinely equally reachable, while a store in the next province can never overtake
// one down the road no matter how many stars it collected.
//
// The bands have to be fine enough to matter inside a single city. An earlier version
// jumped straight from 7 km to 15 km, which put every store in Antalya into one band and
// left relevance sorting the whole city: the list opened at 11.4 km, went to 13.9, then
// back to 7.4. Below 25 km the granularity is now 500 m, which reads as nearest first
// while still letting two stores on the same street be ordered by how well they match.
const nearBandMeters = 500
const nearBandLimit = 25000

func distanceBand(distance *float64) int {
	if distance == nil {
		return math.MaxInt32
	}
	if *distance <= nearBandLimit {
		return int(*distance / nearBandMeters)
	}
	// Beyond the city the exact figure stops being a decision the searcher acts on, so
	// bands widen to 5 km and relevance is given more room again.
	return int(nearBandLimit/nearBandMeters) + int((*distance-nearBandLimit)/5000) + 1
}

// Google is asked with locationBias, which is a hint rather than a restriction, so a
// search for bed linen in Antalya cheerfully comes back with shops in Denizli and
// İstanbul. Those are not useful while there are still nine shops in the searcher's own
// city: nobody drives 169 km for a duvet cover because a list offered it.
//
// Far results are kept only as a fallback, for the genuinely sparse case where the
// nearby area really has run out. A query that names a particular store is exempt, since
// finding that brand in the next province is exactly what was asked for.
const localHorizonMeters = 50000
const minLocalResults = 5

func withinLocalHorizon(results []Result) []Result {
	local := make([]Result, 0, len(results))
	for _, r := range results {
		if r.DistanceMeters == nil || *r.DistanceMeters <= localHorizonMeters {
			local = append(local, r)
		}
	}
	if len(local) < minLocalResults {
		return results
	}
	return local
}

// Ordering is tiered, not a single weighted score. One score let a five star store 169 km
// away outrank a store 14 km away, because distance only cost distance/10000 points while
// ratings and review counts were worth far more. Tier one is what the product promises
// first: places in the searcher's own city that our own community has already reviewed.
// Everything after that runs nearest to farthest, with relevance deciding the order inside
// each band.
func rankResults(results []Result, located, nameLed bool) {
	// Somebody who typed a store's name is looking for that store, not for whatever is
	// closest. The radius filter already steps aside for name-led intents; the ordering
	// has to as well, or the store they named finishes last because it happens to be
	// across town.
	if nameLed && located {
		// Somebody who typed a chain's name wants the branch they can reach. Every branch
		// carries the same name and much the same score, so without distance they came back
		// in an order that meant nothing. Stores that are not the one named still appear --
		// the nearest bed shop is worth knowing about when the İşbir you asked for is shut
		// -- but never above the ones that are.
		sort.SliceStable(results, func(i, j int) bool {
			a, b := results[i], results[j]
			if a.nameHit != b.nameHit {
				return a.nameHit
			}
			if a.nameHit && a.DistanceMeters != nil && b.DistanceMeters != nil && *a.DistanceMeters != *b.DistanceMeters {
				return *a.DistanceMeters < *b.DistanceMeters
			}
			return a.score > b.score
		})
		return
	}
	if nameLed || !located {
		sort.SliceStable(results, func(i, j int) bool { return results[i].score > results[j].score })
		return
	}
	home := homeCity(results)
	inHomeCity := func(r Result) bool { return home != "" && cityKey(r.City, r.Address) == home }
	// PremiumNearby limits injected placement to a local 50 km candidate set. A mapped
	// Google result may carry the same premium flag too, so keep the distance guard here;
	// unlike an exact city-label match it tolerates “Muratpaşa/Antalya” versus “Antalya”.
	promotedHere := func(r Result) bool {
		return r.Premium && r.DistanceMeters != nil && *r.DistanceMeters <= localHorizonMeters
	}
	reviewedHere := func(r Result) bool {
		return r.Platform != nil && r.Platform.ReviewCount > 0 && inHomeCity(r)
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if first, second := promotedHere(a), promotedHere(b); first != second {
			return first
		} else if first {
			return a.score > b.score
		}
		if first, second := reviewedHere(a), reviewedHere(b); first != second {
			return first
		} else if first {
			return a.score > b.score
		}
		if first, second := distanceBand(a.DistanceMeters), distanceBand(b.DistanceMeters); first != second {
			return first < second
		}
		return a.score > b.score
	})
}

var guidanceRotation atomic.Uint64

type guidanceCopy struct {
	message  string
	examples []string
}

var localizedGuidance = map[i18n.Locale]guidanceCopy{
	i18n.LocaleTR: {"İsteğinizi anlayamadım. Yalnızca ev ürünleri mağazaları bulabilirim.", []string{"Perdelerimi yenilemek istiyorum", "Nevresim takımı almak istiyorum", "Çeyiz alışverişi yapmak istiyorum", "Modern bir avize arıyorum", "Yeni bir yemek takımı lazım", "Salonuma uygun bir halı arıyorum"}},
	i18n.LocaleEN: {"I couldn't understand that request. I can only find stores for home and living products.", []string{"I want to replace my curtains", "I need a new bedding set", "I'm shopping for home essentials", "I'm looking for a modern chandelier", "I need a new dinnerware set", "I'm looking for a rug for my living room"}},
	i18n.LocaleDE: {"Ich konnte diese Anfrage nicht verstehen. Ich kann nur Geschäfte für Wohn- und Haushaltsprodukte finden.", []string{"Ich möchte meine Vorhänge erneuern", "Ich brauche neue Bettwäsche", "Ich suche Haushaltswaren für meine Aussteuer", "Ich suche einen modernen Kronleuchter", "Ich brauche ein neues Geschirrset", "Ich suche einen Teppich für mein Wohnzimmer"}},
	i18n.LocaleRU: {"Не удалось понять запрос. Я могу искать только магазины товаров для дома.", []string{"Я хочу обновить шторы", "Мне нужен новый комплект постельного белья", "Я покупаю товары для дома", "Я ищу современную люстру", "Мне нужен новый столовый сервиз", "Я ищу ковёр для гостиной"}},
}

func guidanceFor(locale i18n.Locale, reason string) *Guidance {
	c, ok := localizedGuidance[locale]
	if !ok {
		c = localizedGuidance[i18n.DefaultLocale]
	}
	start := int(guidanceRotation.Add(1)-1) % len(c.examples)
	return &Guidance{Code: "HOME_LIVING_ONLY", Reason: reason, Message: c.message, Examples: []string{c.examples[start], c.examples[(start+1)%len(c.examples)]}}
}
