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
	"time"
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
	BusinessStatus         string
	// The store's own website, where it has one. Google publishes it; nothing read it.
	Website string

	// When the store is open, as the provider publishes it. Asked for in the same field
	// mask that already carries the rating and the website, so it is billed at the tier we
	// were already paying for -- no separate lookup and no extra request per store.
	Hours *OpeningHours

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

// OpeningHours is what the provider publishes about when a store is open. The descriptions
// are already phrased for a reader; the periods are what answers "is it open right now",
// which the descriptions cannot be parsed back into across four languages. The offset is
// the store's own, not the reader's: whether a shop in Antalya is open does not depend on
// where the person asking happens to be standing.
type OpeningHours struct {
	Periods          []OpeningPeriod `json:"periods,omitempty"`
	Descriptions     []string        `json:"descriptions,omitempty"`
	UTCOffsetMinutes int             `json:"utc_offset_minutes"`
	// Computed when the hours are served, never stored: a stored answer to "open now" is
	// wrong within the hour.
	OpenNow *bool `json:"open_now,omitempty"`
}

// Minutes from midnight, with the day as Google gives it: 0 is Sunday.
type OpeningPeriod struct {
	OpenDay     int `json:"open_day"`
	OpenMinute  int `json:"open_minute"`
	CloseDay    int `json:"close_day"`
	CloseMinute int `json:"close_minute"`
}

// OpenAt answers whether these hours cover the given instant, in the store's own time.
// A period that closes on a later day than it opens wraps past midnight, so it is walked
// as a span of minutes across the week rather than compared day by day.
func (h *OpeningHours) OpenAt(t time.Time) *bool {
	if h == nil || len(h.Periods) == 0 {
		return nil
	}
	local := t.UTC().Add(time.Duration(h.UTCOffsetMinutes) * time.Minute)
	now := int(local.Weekday())*24*60 + local.Hour()*60 + local.Minute()
	week := 7 * 24 * 60
	open := false
	for _, p := range h.Periods {
		start := p.OpenDay*24*60 + p.OpenMinute
		end := p.CloseDay*24*60 + p.CloseMinute
		if end <= start {
			end += week
		}
		for _, m := range []int{now, now + week} {
			if m >= start && m < end {
				open = true
			}
		}
	}
	return &open
}

type External struct {
	Provider          string        `json:"provider"`
	PlaceID           string        `json:"place_id"`
	Rating            float64       `json:"rating"`
	RatingCount       int           `json:"rating_count"`
	PhotoName         string        `json:"photo_name,omitempty"`
	PhotoAttributions []string      `json:"photo_attributions,omitempty"`
	BusinessStatus    string        `json:"business_status,omitempty"`
	Hours             *OpeningHours `json:"opening_hours,omitempty"`
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
	{"bathroom", "bathroom", []string{"banyo", "duşakabin", "dusakabin", "vitrifiye", "armatür", "armatur", "bathroom", "shower cabin", "badezimmer", "ванная"}, nil},
	{"bedding", "bedding", []string{"yatak", "bedding", "bettwaren", "постель"}, nil},
	{"tableware", "tableware", []string{"sofra", "tabak", "tableware", "geschirr", "посуда"}, nil},
	{"storage", "storage", []string{"depolama", "dolap", "storage", "aufbewahrung", "хранение", "шкаф"}, nil},
	{"household", "home_appliance", []string{"beyaz eşya", "beyaz esya", "white goods", "home appliance", "household appliance", "haushaltsgerät", "haushaltsgerat", "бытовая техника"}, nil},
	// Trade words that name the business plainly and were simply missing. "Mefruşat" is
	// how a great many Turkish home textile shops describe themselves, and an "uyku
	// merkezi" sells beds and nothing else.
	{"home_textile", "furnishing", []string{"mefruşat", "mefrusat", "döşemelik", "dosemelik", "kumaş", "kumas"}, nil},
	{"bedding", "sleep_centre", []string{"uyku merkezi", "yatak merkezi"}, nil},
	{"household", "diy", []string{"yapı market", "yapi market", "hırdavat", "hirdavat"}, nil},
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
	// Google names more kinds of shop than we were reading, and every one of these is
	// unambiguous. Koçtaş arrived with no category at all because it is a
	// building_materials_store and nothing here said so.
	"building_materials_store": "household",
	"hardware_store":           "household",
	"appliance_store":          "major_appliances",
	"electronics_store":        "small_appliances",
	"furniture_repair_shop":    "furniture",
	"paint_store":              "household",
	"garden_center":            "garden",
	"plant_nursery":            "garden",
	"linens_store":             "home_textile",
	"cutlery_store":            "tableware",
	"tile_store":               "household",
	"wallpaper_store":          "decoration",
	"bathroom_supply_store":    "bathroom",
}

// Kinds of business that are plainly not home and living. Taken from Google's own types,
// which is what makes this generic: it is one rule for the whole country rather than a list
// of shops somebody has to keep adding to.
//
// The catalogue collected these because anything a search turned up was kept. It ended up
// holding a bakery, a language school, two beauty salons, a ventilation contractor, an
// agricultural R&D company and the state opera's warehouse -- none of which anybody will
// ever visit to buy a curtain, and all of which we were asking Google to index as home and
// living stores.
//
// Only unambiguous types are listed. A "supermarket" sells home goods in Turkey often
// enough that excluding it would cost real shops, and that trade is not worth making.
var nonHomeTypes = map[string]bool{
	"bakery": true, "restaurant": true, "cafe": true, "bar": true, "meal_takeaway": true,
	"food_store": true, "grocery_store": true, "pharmacy": true, "drugstore": true,
	"hospital": true, "medical_clinic": true, "dentist": true, "doctor": true,
	"beauty_salon": true, "hair_salon": true, "hair_care": true, "nail_salon": true, "spa": true,
	"school": true, "primary_school": true, "secondary_school": true, "university": true,
	"educational_institution": true, "child_care_agency": true, "preschool": true,
	"bank": true, "atm": true, "insurance_agency": true, "real_estate_agency": true,
	"apartment_complex": true, "lodging": true, "hotel": true, "travel_agency": true,
	"car_repair": true, "car_dealer": true, "car_wash": true, "gas_station": true,
	"tire_shop": true, "auto_parts_store": true,
	"gym": true, "sporting_goods_store": true, "night_club": true, "casino": true,
	"storage": true, "moving_company": true, "shipping_service": true, "courier_service": true,
	"general_contractor": true, "lawyer": true, "accounting": true, "veterinary_care": true,
	// Google names the building trades one by one and we were reading only the first of
	// them. Every one of these is somebody you hire, which is the distinction the whole
	// catalogue rests on. "interior_designer" is here for the same reason: an interior
	// designer sells labour, and the shop that sells the wallpaper is a different business.
	"painter": true, "roofing_contractor": true, "electrician": true, "plumber": true,
	"interior_designer": true, "carpenter": true, "locksmith": true,
	"pet_store": true, "book_store": true, "clothing_store": true, "shoe_store": true,
	"jewelry_store": true, "cosmetics_store": true, "florist": true, "funeral_home": true,
	"place_of_worship": true, "mosque": true, "church": true, "police": true, "post_office": true,
}

// Words that name a service rather than a shop. A renovation firm calls itself "tadilat",
// an architecture practice "mimarlık", and both put "dekorasyon" in the name too -- which
// is how "Antalya Tadilat - Dekorasyon Hizmetleri" ended up classified as a decoration
// store. It is not a store. Nobody visits it to buy a lamp.
//
// This is the distinction the product rests on: somewhere you go to buy a thing, not
// somebody you hire. Every renovation firm in the country uses these words, so the rule
// travels; it is not a list of businesses anyone has to maintain.
//
// Deliberately narrow. "İnşaat" is left out because construction companies run real
// showrooms -- "VitrA - Artema - Güvercinler İnşaat" sells bathroom fittings over a
// counter. So are "montaj" and "tesisat": a shop that sells the parts often fits them too,
// and the shop is the part we want.
// Stems, not whole words. Turkish glues suffixes onto everything, so a whole-word rule
// catches "Tadilat" and misses "Tadilatı" -- which is how a renovation firm stayed
// classified as a decoration store after the rule was written to remove it. Listing the
// inflections instead would be the same mistake as listing shop names: there is always one
// more nobody thought of.
var serviceBusinessStems = []string{
	"tadilat", "mimar", "müteahhit", "muteahhit",
	"taahhüt", "taahhut", "restorasyon", "hizmet",
}

// namesAService reports whether a store's own name says it sells labour rather than goods.
func namesAService(normalized, folded string) bool {
	for _, stem := range serviceBusinessStems {
		if startsAWord(normalized, folded, stem) {
			return true
		}
	}
	return false
}

// startsAWord reports whether the stem begins a word in the text. A letter before it
// disqualifies the match; letters after it do not, because those are the suffixes.
func startsAWord(normalized, folded, stem string) bool {
	for _, haystack := range [2]string{normalized, foldLatin(folded)} {
		needle := normalizeText(stem)
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
			if start == 0 || !isWordRune(rune(haystack[start-1])) {
				return true
			}
			offset = start + 1
		}
	}
	return false
}

// IsHomeLivingPlace reports whether a place belongs in a catalogue of home and living
// stores. It answers no only when Google is explicit about the business being something
// else; silence is not evidence, and a place Google has nothing to say about is kept.
func IsHomeLivingPlace(types []string) bool {
	return isHomeLivingPlace("", types)
}

// IsHomeLivingStore is the same question with the name to hand, which is the only way to
// tell a decoration shop from a decorating firm: Google types both as home_improvement.
func IsHomeLivingStore(name string, types []string) bool {
	return isHomeLivingPlace(name, types)
}

// sectorStoreTypes name the trade a place works in, not what it sells over a counter.
// Google hands them out to the whole building trade: every painter, roofer and contractor
// in the catalogue carried "home_improvement_store", and several carried
// "building_materials_store" as well. Read as evidence of a shop they outvoted the
// explicit "general_contractor" sitting in the same list, which is how renovation firms
// kept arriving in a catalogue of shops. They still count -- a shop with nothing but one
// of these is kept -- they simply cannot overrule a trade.
var sectorStoreTypes = map[string]bool{
	"home_improvement_store":   true,
	"home_goods_store":         true,
	"building_materials_store": true,
}

// tradeNameTerms are the concept words that name a trade rather than a thing on a shelf.
// They are fine for working out what a shop sells once it is known to be a shop, and
// useless for deciding whether it is one: "dekorasyon" is on the sign of every renovation
// firm in the country, and "depolama" is warehousing, not a wardrobe.
var tradeNameTerms = map[string]bool{
	"dekorasyon": true, "dekor": true, "decoration": true, "decor": true,
	"dekoration": true, "декор": true,
	"depolama": true, "storage": true, "aufbewahrung": true, "хранение": true,
}

// namesAProduct reports whether a store's own name says plainly what it sells. This is the
// second source in the order the product rests on: the provider's data first, because it
// is complete, then the words the whole trade uses, because every business of that kind
// uses them.
//
// It is here because Google's type list is thin or wrong often enough to lose real shops
// on its own. A curtain maker typed "general_contractor" and a carpet shop typed
// "clothing_store" were both about to be struck off a catalogue their own signs qualify
// them for.
func namesAProduct(normalized, folded string) bool {
	for _, concept := range homeWordConcepts {
		for _, term := range concept.terms {
			if !tradeNameTerms[term] && containsWord(normalized, folded, term) {
				return true
			}
		}
	}
	for _, concept := range homeConcepts {
		for _, term := range concept.terms {
			if !tradeNameTerms[term] && containsNormalized(normalized, folded, term) {
				return true
			}
		}
	}
	return false
}

func isHomeLivingPlace(name string, types []string) bool {
	var normalized, folded string
	if name != "" {
		normalized = normalizeText(name)
		folded = foldLatin(normalized)
		if namesAService(normalized, folded) {
			return false
		}
	}
	// Three passes, and the order is the whole point. A type that names what is sold --
	// "carpet_store", "bathroom_supply_store" -- is strong evidence and wins outright: a
	// department store that also has a cafe is still a department store. A type that names
	// a trade is next, because Google is being explicit about the business being labour.
	// Only then the sector labels, which are worth something but never worth more than an
	// explicit trade.
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		if googleTypeCategories[t] != "" && !sectorStoreTypes[t] {
			return true
		}
	}
	// The shop's own sign, before the provider's verdict against it. A name that plainly
	// says "perde" or "halı" is what the trade itself calls the business, and it outranks
	// a type list that has already been seen to be wrong.
	if name != "" && namesAProduct(normalized, folded) {
		return true
	}
	for _, t := range types {
		if nonHomeTypes[strings.ToLower(strings.TrimSpace(t))] {
			return false
		}
	}
	for _, t := range types {
		if sectorStoreTypes[strings.ToLower(strings.TrimSpace(t))] {
			return true
		}
	}
	return true
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
	normalized := normalizeText(name)
	folded := foldLatin(normalized)
	// A renovation firm with "dekorasyon" in its name is still a renovation firm, and this
	// beats the provider too: Google types "New Yapı Tadilat" as a home_improvement_store,
	// which is how a contractor came back under a search for decoration. What a business
	// calls itself is better evidence than a category Google fitted it into.
	if namesAService(normalized, folded) {
		return out
	}
	// Sector types are held back to the end for the same reason they cannot decide whether
	// a place is a shop: they name the trade, not the goods. Google gives
	// "home_goods_store" to carpet shops, curtain shops and lighting shops alike, and
	// reading it as a category put every one of them into Ev Aksesuarları as well as its
	// own. They still answer for a shop nothing else describes -- a plain züccaciye with
	// an uninformative name -- so they are a fallback rather than a verdict.
	var sector []string
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		if sectorStoreTypes[t] {
			sector = append(sector, googleTypeCategories[t])
			continue
		}
		add(googleTypeCategories[t])
	}
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
	if len(out) == 0 {
		for _, slug := range sector {
			add(slug)
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
// How far a community review carries. Chosen with the product owner: near enough that
// two stores are genuinely alternatives to each other.
const reviewLeadMeters = 1000

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
			// A name is a weak signal on its own: searching for İşbir also turns up a
			// bakery called Isbirli. A store we could not classify at all is the one most
			// likely to be something else entirely, so it goes last rather than being
			// thrown away -- we would rather show a wrong shop at the bottom than hide a
			// right one we failed to label.
			if first, second := len(a.Categories) > 0, len(b.Categories) > 0; first != second {
				return first
			}
			// Distance orders both halves, not just the named one. Ordering only the
			// matches left everything under them sorted by score, so among the other bed
			// shops a farther one could still lead a nearer one -- reported from the live
			// site, and there was no reason for it beyond an oversight.
			if a.DistanceMeters != nil && b.DistanceMeters != nil && *a.DistanceMeters != *b.DistanceMeters {
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
	// Reviewed stores used to lead the whole of the searcher's own city, which in Antalya
	// meant a shop ten kilometres away with one review beat a shop five hundred metres
	// away with none. Community reviews are the point of this product and still lead --
	// but only against stores you would weigh against each other, which is what a
	// kilometre is. Reported from the live site, and the ranking really did work that way.
	reviewLead := func(r Result) int {
		if r.DistanceMeters == nil {
			return math.MaxInt32
		}
		return int(*r.DistanceMeters / reviewLeadMeters)
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
		if first, second := reviewLead(a), reviewLead(b); first != second {
			return first < second
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
