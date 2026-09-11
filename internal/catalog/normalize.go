package catalog

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/jackc/pgx/v5/pgxpool"
)

// How far a published point may sit from the province it claims to be in before we stop
// believing it. Turkey's largest province is about 400 km across, so 200 km from the middle
// is generous for a real shop and unmistakable for a wrong coordinate.
//
// Deliberately measured against the province and not the district. A district's centre is
// the middle of its neighbourhoods, and a large district -- Alanya, Bodrum -- legitimately
// reaches 50 km beyond that; testing against it moved four correctly placed shops for every
// two wrongly placed ones. Only a point that is certainly wrong is replaced.
//
// It is not a hypothetical check. English Home publishes its Çeşme shop at a coordinate in
// Trabzon, 1,186 km away, and its Ünye shop at one in Bodrum. A shop at the wrong end of the
// country is worse than a shop with no point at all: it answers searches near a city it is
// not in.
const maxMetresFromProvince = 200000

var whitespace = regexp.MustCompile(`\s+`)

// Tidy is what every published field passes through before it is compared or stored.
// Chains publish addresses in shouting capitals, with doubled spaces and a stray comma at
// the end; none of that is information.
func Tidy(raw string) string {
	return strings.Trim(whitespace.ReplaceAllString(strings.TrimSpace(raw), " "), " ,;-/")
}

// TidyName tidies a published name and cases it the way a person writes it. The casing
// rule itself lives in textnorm, because the location dataset is built with the same one.
func TidyName(raw string) string { return textnorm.Title(Tidy(raw)) }

type Place struct {
	City     string
	District string
}

// Resolver turns published place names into canonical ones. It reads tr_locations once and
// keeps it in memory: an import touches a few hundred rows and the table is a thousand
// districts, so a map is both faster and simpler than a query per row.
type point struct{ lat, lon float64 }

type Resolver struct {
	provinces map[string]string            // folded province -> canonical province
	districts map[string]map[string]string // folded province -> folded district -> canonical district
	centres   map[string]point             // canonical "province" or "province|district" -> its centre
	// districtOnly answers the common case of a locator that publishes a district and no
	// province. It only holds districts whose name is unique in Turkey; an ambiguous one
	// must not be guessed at.
	districtOnly map[string]Place
	ambiguous    map[string]bool
}

func NewResolver(ctx context.Context, db *pgxpool.Pool) (*Resolver, error) {
	r := &Resolver{
		provinces:    map[string]string{},
		districts:    map[string]map[string]string{},
		districtOnly: map[string]Place{},
		ambiguous:    map[string]bool{},
		centres:      map[string]point{},
	}
	rows, e := db.Query(ctx, `SELECT kind,name,province_name,coalesce(district_name,''),ST_Y(location::geometry),ST_X(location::geometry) FROM tr_locations WHERE kind IN ('il','ilce')`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name, province, district string
		var latitude, longitude float64
		if e = rows.Scan(&kind, &name, &province, &district, &latitude, &longitude); e != nil {
			return nil, e
		}
		provinceKey := textnorm.Key(province)
		r.provinces[provinceKey] = province
		if kind != "ilce" {
			r.centres[province] = point{latitude, longitude}
			continue
		}
		r.centres[province+"|"+district] = point{latitude, longitude}
		if r.districts[provinceKey] == nil {
			r.districts[provinceKey] = map[string]string{}
		}
		districtKey := textnorm.Key(district)
		r.districts[provinceKey][districtKey] = district
		if _, seen := r.districtOnly[districtKey]; seen {
			r.ambiguous[districtKey] = true
			continue
		}
		r.districtOnly[districtKey] = Place{City: province, District: district}
	}
	return r, rows.Err()
}

// Resolve canonicalises whatever the brand published. It answers with what it can prove and
// leaves the rest empty -- a wrong district is worse than a missing one, because the
// catalogue will be grouped and searched by it.
func (r *Resolver) Resolve(city, district, address string) Place {
	cityKey := textnorm.Key(city)
	districtKey := textnorm.Key(district)

	if province, ok := r.provinces[cityKey]; ok {
		out := Place{City: province}
		if name, ok := r.districts[cityKey][districtKey]; ok {
			out.District = name
		}
		return out
	}
	// Some locators publish only the district, and some swap the two fields round.
	if place, ok := r.districtOnly[districtKey]; ok && !r.ambiguous[districtKey] {
		return place
	}
	if place, ok := r.districtOnly[cityKey]; ok && !r.ambiguous[cityKey] {
		return place
	}
	// Last resort: read the address. Turkish addresses end with the province, so the
	// province is looked for first and the district only inside it.
	return r.fromAddress(address)
}

func (r *Resolver) fromAddress(address string) Place {
	key := textnorm.Key(address)
	if key == "" {
		return Place{}
	}
	words := strings.Fields(key)
	// Walk from the end: "... Muratpaşa/Antalya" puts the province last.
	for i := len(words) - 1; i >= 0; i-- {
		if province, ok := r.provinces[words[i]]; ok {
			out := Place{City: province}
			provinceKey := words[i]
			for j := 0; j < len(words); j++ {
				if j == i {
					continue
				}
				if name, ok := r.districts[provinceKey][words[j]]; ok {
					out.District = name
					break
				}
			}
			return out
		}
	}
	for _, word := range words {
		if place, ok := r.districtOnly[word]; ok && !r.ambiguous[word] {
			return place
		}
	}
	return Place{}
}

// Normalize applies every rule above to one published row, and then checks the row against
// itself: a point that contradicts the place beside it is not usable as either.
func (r *Resolver) Normalize(in RawStore) RawStore {
	in.Name = TidyName(in.Name)
	in.Address = Tidy(in.Address)
	in.Phone = Tidy(in.Phone)
	in.Website = Tidy(in.Website)
	place := r.Resolve(in.City, in.District, in.Address)
	in.City, in.District = place.City, place.District
	return r.placePoint(in)
}

// placePoint decides which of the two things a row publishes about where it is -- a
// coordinate and a place name -- to believe when they disagree.
//
// The place name wins. It comes with a street address a person can read, and it is the
// field a chain is least likely to get wrong; a coordinate is produced by a geocoder and
// fails silently. So a point too far from the named place is replaced by that place's own
// centre, which is right to within a few kilometres instead of wrong by a thousand.
func (r *Resolver) placePoint(in RawStore) RawStore {
	if in.City == "" {
		return in
	}
	province, ok := r.centres[in.City]
	if !ok {
		return in
	}
	// The most precise centre we can stand a store at when its own point cannot be used.
	fallback, where := province, in.City
	if in.District != "" {
		if district, ok := r.centres[in.City+"|"+in.District]; ok {
			fallback, where = district, in.District
		}
	}
	if in.Latitude == nil || in.Longitude == nil {
		in.Latitude, in.Longitude = &fallback.lat, &fallback.lon
		in.PointFrom = "placed at the centre of " + where + "; none published"
		return in
	}
	away := metresBetween(*in.Latitude, *in.Longitude, province.lat, province.lon)
	if away <= maxMetresFromProvince {
		in.PointFrom = "published"
		return in
	}
	in.Latitude, in.Longitude = &fallback.lat, &fallback.lon
	in.PointFrom = "placed at the centre of " + where + "; the published point was " + kilometres(away) + " from " + in.City
	return in
}

func kilometres(metres float64) string {
	return strconv.FormatFloat(metres/1000, 'f', 0, 64) + " km"
}

func metresBetween(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000
	radians := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLon := radians(lat2-lat1), radians(lon2-lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(radians(lat1))*math.Cos(radians(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Min(1, math.Sqrt(a)))
}
