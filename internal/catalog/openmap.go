package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// The public map, for the shops no chain publishes.
//
// Nine tenths of this catalogue is branches of chains, because chains are the ones who
// publish lists. That leaves the independents -- the carpet shop on the corner, the
// furniture showroom nobody has heard of outside its own district -- and there is no list of
// those to read. Measured before this was written: İstanbul held 70 independent shops,
// Ankara 3, Bursa 4. The map holds around two thousand in İstanbul alone.
//
// OpenStreetMap is not a chain speaking about itself, so what comes from it is not verified
// the way a brand's own list is. It arrives as its own provenance and search ranks it behind
// what a chain has confirmed, which is the honest order.
//
// Licence: OpenStreetMap data is ODbL. Attribution is required wherever it is shown, and a
// derived database carries share-alike obligations. That is recorded in
// docs/LEGAL_REVIEW_REQUIRED.md; it is a condition of using this source, not a footnote.
const OpenMapProvider = "osm"

// Where to ask, in order of preference.
//
// It is an API rather than somebody's website, but it is still somebody's server: the
// fetcher's one-request-a-second and its robots check apply here as they do everywhere else,
// and the queries below ask for one province at a time rather than the country in one breath.
//
// The main instance is not in this list, and that is not an oversight. overpass-api.de's
// robots.txt disallows /api/ outright, so the fetcher refuses it -- correctly. The answer to
// a host that says no is a host that does not, not a special case in the fetcher: these two
// are mirrors run for exactly this purpose and state no rules at all. If one of them ever
// publishes a robots.txt that disallows this path, the fetcher will refuse it too and the
// next one is tried. Geofabrik's bulk extracts were checked as well and are also disallowed.
var overpassEndpoints = []string{
	"https://overpass.kumi.systems/api/interpreter",
	"https://overpass.private.coffee/api/interpreter",
}

// What counts as a home and living shop on the map, and which of our categories it is.
//
// Deliberately narrower than it could be. `doityourself` and `hardware` bring in plumbers,
// builders' merchants and locksmiths -- measured on İstanbul, 111 of them, with names like
// "Güven Sıhhi Tesisat" -- and a directory of home and living stores that lists a plumber is
// wrong about itself. The nine below are the ones whose whole trade is the home.
var openMapShops = map[string][]string{
	"furniture":           {"furniture"},
	"houseware":           {"household", "kitchenware"},
	"interior_decoration": {"decoration", "home_accessories"},
	"curtain":             {"curtain", "home_textile"},
	"bed":                 {"bedding"},
	"carpet":              {"carpet"},
	"kitchen":             {"kitchenware"},
	"lighting":            {"lighting"},
	"bathroom_furnishing": {"bathroom"},
}

// OpenMapSource reads one province's home and living shops.
type OpenMapSource struct {
	province string
	fetcher  *Fetcher
}

func NewOpenMapSource(province string, fetcher *Fetcher) *OpenMapSource {
	return &OpenMapSource{province: province, fetcher: fetcher}
}

func (s *OpenMapSource) Province() string { return s.province }

type overpassAnswer struct {
	// Overpass answers a query it could not finish with 200 and an empty element list, with
	// the reason in this field. Read as JSON and nothing more, that is indistinguishable
	// from "this province has no shops" -- and it is how İstanbul, which has about two
	// thousand, was recorded as having none.
	Remark   string `json:"remark"`
	Elements []struct {
		Type   string                      `json:"type"`
		ID     int64                       `json:"id"`
		Lat    *float64                    `json:"lat"`
		Lon    *float64                    `json:"lon"`
		Center *struct{ Lat, Lon float64 } `json:"center"`
		Tags   map[string]string           `json:"tags"`
	} `json:"elements"`
}

// Fetch asks for one province and maps what comes back.
//
// A shop with no name is skipped rather than listed as "Mobilyacı": an unnamed point on a map
// is a note to a mapper, not a shop somebody can be sent to.
func (s *OpenMapSource) Fetch(ctx context.Context) ([]RawStore, error) {
	kinds := make([]string, 0, len(openMapShops))
	for kind := range openMapShops {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	filter := "^(" + strings.Join(kinds, "|") + ")$"
	query := fmt.Sprintf(`[out:json][timeout:180];
area["name"=%q]["admin_level"="4"]->.a;
(
  node["shop"~%q](area.a);
  way["shop"~%q](area.a);
);
out tags center;`, s.province, filter, filter)

	// Asked in turn until one of them answers with something. An answer is not merely a 200:
	// a loaded instance hands back a complete, well-formed document with an empty element
	// list and, sometimes, no remark at all. Read as JSON and nothing more, that is
	// indistinguishable from "this province has no shops" -- and it is how İstanbul, which
	// has nineteen hundred of them, was twice recorded as having none. A province we have
	// asked about has shops in it; nothing is the server saying it could not, so the next
	// mirror is asked.
	var answer overpassAnswer
	var failures []string
	for _, endpoint := range overpassEndpoints {
		body, e := s.fetcher.Send(ctx, "POST", endpoint, "data="+url.QueryEscape(query))
		if e != nil {
			failures = append(failures, endpoint+": "+e.Error())
			continue
		}
		var got overpassAnswer
		if e := json.Unmarshal(body, &got); e != nil {
			failures = append(failures, endpoint+": "+e.Error())
			continue
		}
		if len(got.Elements) == 0 {
			why := got.Remark
			if why == "" {
				why = "answered with no shops at all"
			}
			failures = append(failures, endpoint+": "+why)
			continue
		}
		answer = got
		break
	}
	if len(answer.Elements) == 0 {
		return nil, fmt.Errorf("overpass %s: %s", s.province, strings.Join(failures, "; "))
	}

	out := make([]RawStore, 0, len(answer.Elements))
	for _, el := range answer.Elements {
		name := strings.TrimSpace(el.Tags["name"])
		if name == "" {
			continue
		}
		lat, lon := el.Lat, el.Lon
		if lat == nil && el.Center != nil {
			lat, lon = &el.Center.Lat, &el.Center.Lon
		}
		if lat == nil || lon == nil {
			continue
		}
		raw := RawStore{
			// Stable for the life of the object on the map, which is what an identifier has
			// to be: the same shop is the same row on every later read.
			ExternalID: el.Type + "/" + strconv.FormatInt(el.ID, 10),
			Name:       name,
			Address:    openMapAddress(el.Tags),
			City:       el.Tags["addr:province"],
			District:   el.Tags["addr:district"],
			Phone:      firstTag(el.Tags, "phone", "contact:phone"),
			Website:    firstTag(el.Tags, "website", "contact:website"),
			Latitude:   lat,
			Longitude:  lon,
			Categories: openMapShops[el.Tags["shop"]],
		}
		if raw.City == "" {
			raw.City = el.Tags["addr:city"]
		}
		out = append(out, raw)
	}
	return out, nil
}

// openMapAddress rebuilds a readable address out of the parts a mapper filled in. Most
// entries have none of them, which is fine: the point is what places the shop, and the
// district comes from the point.
func openMapAddress(tags map[string]string) string {
	parts := make([]string, 0, 4)
	for _, key := range []string{"addr:neighbourhood", "addr:street", "addr:housenumber"} {
		if v := strings.TrimSpace(tags[key]); v != "" {
			parts = append(parts, v)
		}
	}
	return Tidy(strings.Join(parts, " "))
}

func firstTag(tags map[string]string, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(tags[key]); v != "" {
			return v
		}
	}
	return ""
}
