package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompactNameMakesOneStringOfTheSameSign(t *testing.T) {
	// The whole of deduplication rests on these two being one value. A provider shouts,
	// a brand styles, and the shop is the same shop.
	if CompactName("ENGLISH HOME KADIKÖY AVM") != CompactName("English Home Kadıköy AVM") {
		t.Fatalf("%q vs %q", CompactName("ENGLISH HOME KADIKÖY AVM"), CompactName("English Home Kadıköy AVM"))
	}
	if got := CompactName("Yataş Bedding - Konyaaltı Şb."); got != "yatasbeddingkonyaaltisb" {
		t.Errorf("CompactName=%q", got)
	}
	if CompactName("...") != "" {
		t.Error("punctuation alone should compact to nothing")
	}
}

func TestTidyNameLeavesAStyledSignAloneAndCalmsAShoutedOne(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"BALIKESİR MAĞAZASI", "Balıkesir Mağazası"},
		// A name carrying no Turkish letter is read as English, so an English branch code
		// does not acquire a dotless ı; one carrying any Turkish letter is read as Turkish
		// throughout, which is what keeps "ADIYAMAN" from becoming "Adiyaman".
		// A short shouted token is an abbreviation the writer meant. Dropping "ANK" is
		// StripPlaceCode's job, not the caser's; see the test below.
		{"ANK ACITY AVM", "ANK Acity AVM"},
		{"ADIYAMAN MAĞAZASI", "Adıyaman Mağazası"},
		// Already styled by the brand: not ours to restyle.
		{"English Home", "English Home"},
		{"Koçtaş Yapı Marketleri", "Koçtaş Yapı Marketleri"},
		// Short all-capitals is an acronym, not shouting.
		{"IKEA", "IKEA"},
		{"  Taç   Home  ,", "Taç Home"},
	} {
		if got := TidyName(c.in); got != c.want {
			t.Errorf("TidyName(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestRobotsHonoursTheFileRatherThanGuessing(t *testing.T) {
	// This is English Home's own file, trimmed: the list API this catalogue reads is
	// allowed and the per-store pages beside it are not. Both facts have to come out of
	// the parser, because reading them by hand for twenty brands is how a rule becomes a
	// promise nobody keeps.
	rules := parseRobots(strings.NewReader(`
User-agent: *
Disallow: *Arama?*
Disallow: /Sepetim
Disallow: */retail_store/*
Disallow: */list/*
Disallow: /*?page
Allow: /api/
Sitemap: https://www.englishhome.com/sitemap.xml
`))
	for _, c := range []struct {
		path  string
		allow bool
	}{
		{"/api/Store/GetStoriesLite", true},
		{"/Magazalarimiz", true},
		{"/tr/retail_store/kadikoy", false},
		{"/Sepetim", false},
		{"/x/list/y", false},
	} {
		if got := rules.allows(c.path); got != c.allow {
			t.Errorf("allows(%q)=%v want %v", c.path, got, c.allow)
		}
	}
}

func TestRobotsPrefixIsAPrefix(t *testing.T) {
	rules := parseRobots(strings.NewReader("User-agent: *\nDisallow: /list\n"))
	if rules.allows("/list/anything") {
		t.Error("/list should cover everything under it")
	}
	// "/list" is a prefix of the path, not a substring search: a page whose path merely
	// contains the word is a different page.
	if !rules.allows("/store/list") {
		t.Error("/store/list is not under /list")
	}
}

func TestRobotsEndAnchor(t *testing.T) {
	rules := parseRobots(strings.NewReader("User-agent: *\nDisallow: /*.pdf$\n"))
	if rules.allows("/files/report.pdf") {
		t.Error("the anchored pattern should match")
	}
	if !rules.allows("/files/report.pdf.html") {
		t.Error("the anchor means end of path")
	}
}

func TestFetcherRefusesWhatRobotsForbids(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /private/\n"))
			return
		}
		if r.Header.Get("User-Agent") != UserAgent {
			t.Errorf("fetcher did not identify itself: %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	fetcher := NewFetcher()
	if _, e := fetcher.Get(context.Background(), server.URL+"/public/list"); e != nil {
		t.Fatalf("allowed path failed: %v", e)
	}
	_, e := fetcher.Get(context.Background(), server.URL+"/private/list")
	var disallowed ErrDisallowed
	if e == nil || !errorsAs(e, &disallowed) {
		t.Fatalf("forbidden path was fetched anyway: %v", e)
	}
	// The forbidden path must never have reached the server.
	for _, path := range asked {
		if strings.HasPrefix(path, "/private/") {
			t.Fatalf("requested a disallowed path: %s", path)
		}
	}
}

func errorsAs(err error, target *ErrDisallowed) bool {
	value, ok := err.(ErrDisallowed)
	if ok {
		*target = value
	}
	return ok
}

func TestJSONLocatorMapsAPublishedListToRows(t *testing.T) {
	// The shape English Home publishes, which most chain locators resemble: a wrapper
	// object, Turkish field names, and coordinates as strings.
	body := `{"magazalar":[
      {"id":3771,"tanim":"ANK 365 1 AVM","adres":"BİRLİK Mah. 428 Cad.","il":"Ankara","ilce":"Çankaya","telefon":"","latitude":"39.87542","longitude":"32.87025"},
      {"id":3772,"tanim":"ANK ACITY AVM","adres":"Macun Mahallesi","il":"Ankara","ilce":"Yenimahalle","telefon":"0312 000 00 00","latitude":"0","longitude":"0"}
    ]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	config, _ := json.Marshal(map[string]any{
		"url": server.URL + "/api/stores", "list": "magazalar",
		"id": "id", "name": "tanim", "address": "adres", "city": "il", "district": "ilce",
		"phone": "telefon", "latitude": "latitude", "longitude": "longitude",
		"name_prefix": "English Home",
	})
	locator, e := NewJSONLocator(BrandSpec{Slug: "english-home", Name: "English Home", LocatorConfig: config}, NewFetcher())
	if e != nil {
		t.Fatal(e)
	}
	rows, e := locator.Fetch(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	// An id published as a number is still an id.
	if rows[0].ExternalID != "3771" {
		t.Errorf("external id=%q", rows[0].ExternalID)
	}
	// A branch code is not a shop sign: the chain's own city code comes off the front. What
	// goes back on -- the town, the chain's name -- is DisplayName's job, once the town has
	// been resolved to a real one.
	if rows[0].Name != "365 1 AVM" {
		t.Errorf("name=%q", rows[0].Name)
	}
	if rows[0].Latitude == nil || *rows[0].Latitude < 39.8 || *rows[0].Latitude > 39.9 {
		t.Errorf("latitude=%v", rows[0].Latitude)
	}
	// 0,0 is not a coordinate anywhere near Turkey; it is a locator saying "I do not know".
	if rows[1].Latitude != nil || rows[1].Longitude != nil {
		t.Errorf("row published at 0,0 was taken as a point: %v %v", rows[1].Latitude, rows[1].Longitude)
	}
}

func TestStripPlaceCodeDropsTheChainsOwnCityCode(t *testing.T) {
	// Every one of these is a real English Home branch name. The codes are the province in
	// the chain's shorthand, which is useful in their warehouse and meaningless to somebody
	// looking for a shop.
	for _, c := range []struct{ name, province, want string }{
		{"ANK ACITY AVM", "Ankara", "ACITY AVM"},
		{"DYR 75.CAD", "Diyarbakır", "75.CAD"},
		{"GTP AKKENT CAD", "Gaziantep", "AKKENT CAD"},
		{"BLK AYVALIK1 CAD", "Balıkesir", "AYVALIK1 CAD"},
		{"IST AND LARA CAD", "İstanbul", "AND LARA CAD"},
		{"MUG BODRUM MIDTOWN1 AVM", "Muğla", "BODRUM MIDTOWN1 AVM"},
		// Not a code: a real first word, even a short shouted one, stays.
		{"LARA CAD", "Antalya", "LARA CAD"},
		{"AVM MERKEZ", "Ankara", "AVM MERKEZ"},
		// Nothing to strip against, or nothing left after stripping.
		{"ANK ACITY AVM", "", "ANK ACITY AVM"},
		{"ANK", "Ankara", "ANK"},
		// A styled name is not a code, whatever its letters.
		{"Ank Acity", "Ankara", "Ank Acity"},
	} {
		if got := StripPlaceCode(c.name, c.province); got != c.want {
			t.Errorf("StripPlaceCode(%q,%q)=%q want %q", c.name, c.province, got, c.want)
		}
	}
}
