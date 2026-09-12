package catalog

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/jackc/pgx/v5"
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

// Turkey's bounding box. A published point outside it is not a Turkish shop, and this
// catalogue is a Turkish one: Madame Coco publishes branches in Tashkent, whose name the
// administrative table happily matched to Taşkent, a district of Konya. A foreign store is
// dropped rather than relocated -- moving it to a Turkish centre would be inventing a shop
// that is not there.
const (
	minLatitude, maxLatitude   = 35.5, 42.5
	minLongitude, maxLongitude = 25.4, 45.1
)

func insideTurkey(lat, lon float64) bool {
	return lat >= minLatitude && lat <= maxLatitude && lon >= minLongitude && lon <= maxLongitude
}

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

// StripPlaceCode removes a chain's internal city code from the front of a branch name.
//
// English Home publishes "ANK ACITY AVM", "IST AND LARA CAD", "DYR 75.CAD": the first token
// is the province in the chain's own shorthand, useful in their warehouse and meaningless
// to a person looking for a shop. What is wanted is "ACITY AVM".
//
// The test is general and uses the row's own published province rather than a list of
// codes, because a list would cover the chains somebody thought of and quietly fail for
// the rest. A short first token whose letters appear, in order, inside the province's name
// is that province abbreviated -- ANK in Ankara, DYR in Diyarbakır, GTP in Gaziantep, BLK
// in Balıkesir. A real branch word does not do that: "LARA" is not a subsequence of
// "Antalya", and "AVM" is not a subsequence of anywhere.
func StripPlaceCode(name, province string) string {
	name = Tidy(name)
	if province == "" {
		return name
	}
	first, rest, found := strings.Cut(name, " ")
	if !found || strings.TrimSpace(rest) == "" {
		return name
	}
	// Only a shouted, short token is a code. Three letters at least, or two-letter words
	// would match almost any province.
	if first != strings.ToUpper(first) || len([]rune(first)) < 3 || len([]rune(first)) > 4 {
		return name
	}
	if !isSubsequence(textnorm.Key(first), textnorm.Key(province)) {
		return name
	}
	return strings.TrimSpace(rest)
}

// isSubsequence reports whether every letter of short appears in long, in order.
func isSubsequence(short, long string) bool {
	if short == "" {
		return false
	}
	next := 0
	for _, letter := range long {
		if next < len(short) && rune(short[next]) == letter {
			next++
		}
	}
	return next == len(short)
}

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
	// Every neighbourhood in the country with its own centre, for the row that publishes a
	// coordinate and no readable place name. Around seventy thousand of them: a few
	// megabytes held for the length of one import, and accurate to within a kilometre or
	// two, where the nearest district centre would be accurate to within a district.
	neighbourhoods []placed
}

type placed struct {
	at    point
	place Place
}

// How far the nearest neighbourhood may be before a point stops telling us anything. A shop
// standing more than this from every neighbourhood in Turkey is in open country, at sea, or
// across a border, and naming it would be a guess dressed up as a fact.
const maxMetresFromNeighbourhood = 15000

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
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return r, r.loadNeighbourhoods(ctx, db)
}

func (r *Resolver) loadNeighbourhoods(ctx context.Context, db *pgxpool.Pool) error {
	// A fifth of the neighbourhood table -- fifteen thousand rows -- shares its centre with
	// a neighbourhood of another district: where the real centre was not known, something
	// else was put there, and the same coordinate now stands for two places at once. Those
	// rows cannot say where a point is, whichever of them is nearest, and they are left out
	// rather than believed. Fifty thousand remain, which is still one every few hundred
	// metres in a town.
	rows, e := db.Query(ctx, `
WITH shared AS (
  SELECT ST_AsText(location::geometry) AS at FROM tr_locations WHERE kind='mahalle'
  GROUP BY 1 HAVING count(DISTINCT coalesce(district_name,'')) > 1
)
SELECT province_name,coalesce(district_name,''),ST_Y(location::geometry),ST_X(location::geometry)
FROM tr_locations
WHERE kind='mahalle' AND ST_AsText(location::geometry) NOT IN (SELECT at FROM shared)`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var province, district string
		var latitude, longitude float64
		if e = rows.Scan(&province, &district, &latitude, &longitude); e != nil {
			return e
		}
		// The canonical spellings are the ones already held, so the names share their
		// backing strings with the province and district maps rather than being copied
		// seventy thousand times.
		if canonical, ok := r.provinces[textnorm.Key(province)]; ok {
			province = canonical
		}
		if canonical, ok := r.districts[textnorm.Key(province)][textnorm.Key(district)]; ok {
			district = canonical
		}
		r.neighbourhoods = append(r.neighbourhoods, placed{point{latitude, longitude}, Place{City: province, District: district}})
	}
	return rows.Err()
}

// placeFromPoint answers the question the other way round: not where a named place is, but
// what place a coordinate stands in. It is the rule for the row that publishes a point and
// nothing readable beside it -- Merinos publishes hundreds, every one of them a real shop
// that would otherwise belong to no city and answer no search.
//
// The nearest neighbourhood, not the nearest district centre. A district centre is the
// middle of its neighbourhoods and a large district reaches tens of kilometres past it, so
// the nearest centre is regularly in the district next door; the nearest neighbourhood, out
// of seventy thousand, is almost always the one the shop is standing in.
func (r *Resolver) placeFromPoint(lat, lon float64, withinProvince string) Place {
	best, nearest := Place{}, math.MaxFloat64
	for _, candidate := range r.neighbourhoods {
		if withinProvince != "" && candidate.place.City != withinProvince {
			continue
		}
		// A cheap box test first: the full distance for seventy thousand rows per shop is
		// the difference between an import that takes a minute and one that takes an hour.
		if math.Abs(candidate.at.lat-lat) > 0.2 || math.Abs(candidate.at.lon-lon) > 0.25 {
			continue
		}
		away := metresBetween(lat, lon, candidate.at.lat, candidate.at.lon)
		if away < nearest {
			best, nearest = candidate.place, away
		}
	}
	if nearest > maxMetresFromNeighbourhood {
		return Place{}
	}
	return best
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

// ResolveAt is Resolve with the row's own coordinate to fall back on.
//
// What a row says about where it is comes in two forms, and a row missing one of them can
// be read from the other. The name is asked first, because it is the field a chain is least
// likely to get wrong; the point answers only what the name left empty.
func (r *Resolver) ResolveAt(city, district, address string, lat, lon *float64) Place {
	place := r.Resolve(city, district, address)
	if lat == nil || lon == nil || !insideTurkey(*lat, *lon) {
		return place
	}
	if place.City == "" {
		return r.placeFromPoint(*lat, *lon, "")
	}
	if place.District == "" {
		place.District = r.placeFromPoint(*lat, *lon, place.City).District
	}
	return place
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
	// No province named in the address means we do not know where this is. There was a
	// last resort here that took any word matching some district's name, and it placed a
	// shop in İstanbul Maltepe at Hani, in Diyarbakır, because a word in its address
	// happened to be a district somewhere. A wrong district is worse than a missing one:
	// the catalogue is grouped and searched by it, and a missing one is visibly missing.
	return Place{}
}

// Normalize applies every rule above to one published row, and then checks the row against
// itself: a point that contradicts the place beside it is not usable as either.
func (r *Resolver) Normalize(in RawStore) RawStore {
	// The name is deliberately left as published here. Casing it before the importer has
	// repaired its capital I's and taken off the chain's city code would erase the very
	// evidence those two steps read -- a shouted "ÇELIK" is what tells us it is ambiguous.
	in.Name = Tidy(in.Name)
	in.Address = Tidy(in.Address)
	in.Phone = Tidy(in.Phone)
	in.Website = Tidy(in.Website)
	place := r.ResolveAt(in.City, in.District, in.Address, in.Latitude, in.Longitude)
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
	// Checked before anything else, and on the published point rather than the resolved
	// place: a foreign branch often resolves to a Turkish name by coincidence.
	if in.Latitude != nil && in.Longitude != nil && !insideTurkey(*in.Latitude, *in.Longitude) {
		in.Outside = true
		in.PointFrom = "published outside Turkey"
		return in
	}
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

// Derived reports whether a coordinate was put there by us rather than published. The
// wording is the provenance a row carries in the catalogue, so this is the one place that
// reads it.
func Derived(pointFrom string) bool {
	return strings.HasPrefix(pointFrom, "placed at the centre")
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

// Several chains sell clothing as well as homeware and publish both in one store list, so
// a Mudo "Akmerkez Home" arrives beside a Mudo "Akmerkez Giyim" at the same address. The
// second one is a clothes shop, and a directory of home and living stores that lists it is
// wrong about itself.
//
// These are the trade's own words, not a list of branches: any chain that separates its
// departments this way labels them with the same handful of nouns, and a chain nobody has
// added yet is covered by the same test. The words are matched whole -- "erkek" as a word,
// never inside another.
var apparelDepartments = []string{"giyim", "kadin", "erkek", "ayakkabi", "canta", "kozmetik", "parfumeri", "aksesuar giyim"}

// NamesAnApparelDepartment reports whether a published branch name says, in the chain's own
// words, that this shop sells clothes rather than homeware.
func NamesAnApparelDepartment(name string) bool {
	words := strings.Fields(textnorm.Key(name))
	for _, word := range words {
		for _, department := range apparelDepartments {
			if word == department {
				return true
			}
		}
	}
	return false
}

// DisplayName is what a shop is finally called in this catalogue.
//
// Three things have to be true of it at once, and a chain's own branch name is reliably
// none of them. It must say which chain this is, because "Ahsen Mobilya" tells nobody it is
// where you buy an İstikbal bed. It must say where it is, because "Forum AVM" is nine
// different shops in nine cities and "Merkez CAD" is five. And it must say each of those
// once: a chain that already names itself in its branches does not need saying twice, and a
// branch already named for its town does not need its town again.
//
// So each part is added only when it is missing, tested on the folded name so that case and
// diacritics cannot let the same word through twice.
func DisplayName(brand, branch, province, district string) string {
	branch = Tidy(branch)
	missing := missingPlaces(branch, province, district)
	// A chain that has already put its own name at the front of a branch name has it in the
	// right place, and the town then goes after it rather than in front of it: Koçtaş
	// publishes "Koçtaş Ankara Eryaman", and the shop is in Etimesgut, so it is "Koçtaş
	// Etimesgut Ankara Eryaman" and never "Etimesgut Koçtaş Ankara Eryaman".
	if head, rest, ok := cutLeading(branch, brand); ok && len(missing) > 0 {
		return strings.TrimSpace(head + " " + strings.Join(missing, " ") + " " + rest)
	}
	name := strings.TrimSpace(strings.Join(missing, " ") + " " + branch)
	if key, brandKey := textnorm.Key(name), textnorm.Key(brand); brandKey != "" && !strings.Contains(key, brandKey) {
		name = strings.TrimSpace(brand + " - " + name)
	}
	return name
}

// cutLeading splits a name that begins with the chain's own name into that name and the
// rest of it. Compared word by word on the folded forms, because "Koçtaş" and "KOCTAS" are
// the same word and neither is as long as the other in bytes.
func cutLeading(name, brand string) (head, rest string, ok bool) {
	words, brandWords := strings.Fields(name), strings.Fields(brand)
	if len(brandWords) == 0 || len(words) <= len(brandWords) {
		return "", "", false
	}
	head = strings.Join(words[:len(brandWords)], " ")
	if textnorm.Key(head) != textnorm.Key(brand) {
		return "", "", false
	}
	return head, strings.Join(words[len(brandWords):], " "), true
}

// missingPlaces is the part of the shop's address its name does not already carry, city
// before town -- the order a Turkish address is read aloud in, and the order the chains that
// do publish it already use ("Antalya Kepez Kültür").
func missingPlaces(name, province, district string) []string {
	key := textnorm.Key(name)
	var missing []string
	for _, place := range []string{province, district} {
		place = strings.TrimSpace(place)
		if place == "" {
			continue
		}
		if folded := textnorm.Key(place); folded != "" && !strings.Contains(key, folded) {
			missing = append(missing, TidyName(place))
		}
	}
	return missing
}

// HouseWords are the tokens a chain uses in its own branch names that tell a shopper
// nothing: Yataş writes "ANK ORAN YB PRK SHW CAD", where only "Oran" names the shop and the
// rest is the chain's shorthand for which of its brands, whether there is parking, and that
// it is a showroom on a street.
//
// The test is not a list of codes, because a list covers the chains somebody thought of and
// quietly fails for the rest. It is two measurements:
//
//   - the token appears in at least a quarter of this chain's own branch names, so it
//     cannot be the thing that distinguishes one branch from another; and
//   - no other chain in the catalogue uses it, so it is this chain's private vocabulary
//     rather than the trade's.
//
// The second measurement is what keeps "AVM" and "Outlet" -- words every chain in Turkey
// writes, which genuinely tell a shopper what kind of place this is -- while removing "SHW"
// and "PRK", which only Yataş writes. A chain imported into an empty catalogue has nothing
// to compare against and strips nothing, which is the safe direction to fail in.
//
// The comparison is done here rather than in SQL so that both sides are folded by the same
// function. A second folding written in SQL is the kind of near-duplicate that agrees on
// every case anybody tests and disagrees on the one that matters.
func HouseWords(ctx context.Context, db Queryer, names []string, brandID string) (map[string]bool, error) {
	if len(names) < minNamesForHouseWords {
		return nil, nil
	}
	counts := map[string]int{}
	for _, name := range names {
		for word := range uniqueWords(name) {
			counts[word]++
		}
	}
	threshold := len(names) / 4
	frequent := map[string]bool{}
	for word, n := range counts {
		if n >= threshold && len([]rune(word)) <= maxHouseWordLength {
			frequent[word] = true
		}
	}
	if len(frequent) == 0 {
		return nil, nil
	}
	rows, e := db.Query(ctx, `SELECT name FROM stores WHERE deleted_at IS NULL AND brand_id IS DISTINCT FROM $1::uuid`, brandID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return nil, e
		}
		for word := range uniqueWords(name) {
			delete(frequent, word)
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return frequent, nil
}

// Queryer is the part of a pool or a transaction this needs.
type Queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const (
	// Below this there is no frequency to measure and a coincidence looks like a pattern.
	minNamesForHouseWords = 20
	// Internal shorthand is short. A whole word repeated across a chain's branches is
	// usually the chain naming itself, which DisplayName already handles.
	maxHouseWordLength = 4
)

// StripHouseWords removes those tokens from one name, leaving what actually names the shop.
func StripHouseWords(name string, house map[string]bool) string {
	if len(house) == 0 {
		return name
	}
	fields := strings.Fields(name)
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		if !house[textnorm.Key(field)] {
			kept = append(kept, field)
		}
	}
	// Never everything: a name reduced to nothing is worse than one carrying shorthand.
	if len(kept) == 0 {
		return name
	}
	return strings.Join(kept, " ")
}

func uniqueWords(name string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.Fields(textnorm.Key(name)) {
		out[word] = true
	}
	return out
}

// Spellings is how a publisher tells us which of its own capital I's is an İ.
//
// Turkish has two i's and a keyboard that makes it easy to type the wrong capital, so a
// chain's list says "ÇELİK CENTROOM ALTINTAŞ" on one row and "ÇELIK CENTROOM KONYAALTI" on
// the next. Cased by the Turkish rules, the second becomes "Çelık" -- a word that does not
// exist, printed on a shop nobody will recognise.
//
// Vowel harmony was tried and is not good enough: measured across the catalogue it repairs
// about thirty-five words and breaks about twenty-five, because Turkish place and family
// names break harmony freely -- Kırşehir, Iğdır, Yılmaz, Ilgın all become wrong. A rule that
// trades one kind of error for another is not a fix.
//
// This uses evidence instead. Within one brand's own list, a shouted word containing an
// ASCII I is repaired only when that same brand writes the same word with İ somewhere else.
// Nobody writes Kırşehir with an İ, so nothing invents one; Bellona writes MOBİLYA on most
// of its rows, so the handful spelled MOBILYA are corrected to match.
//
// Both halves of that scope are load-bearing, and widening either was measured and rejected:
//
//   - Only shouted words. The ambiguity comes from a capital-I key; a name already written
//     in ordinary case carries the dot or does not, and is not ours to second-guess.
//   - Only this brand's own list. Taking evidence from the whole catalogue turns "halı" into
//     "hali" -- 156 rows somewhere spell it that way -- and "satış" into "satiş" and "aydın"
//     into "aydin". A hundred and seventy-eight such "corrections" were on offer and most of
//     them were wrong.
func Spellings(names []string) map[string]string {
	definite := map[string]bool{}
	ambiguous := map[string]bool{}
	for _, name := range names {
		for _, word := range shoutedWords(name) {
			if strings.ContainsRune(word, 'İ') {
				definite[word] = true
			}
			if strings.ContainsRune(word, 'I') {
				ambiguous[word] = true
			}
		}
	}
	out := map[string]string{}
	for word := range ambiguous {
		if fixed := strings.ReplaceAll(word, "I", "İ"); definite[fixed] {
			out[word] = fixed
		}
	}
	return out
}

// RepairSpelling applies those corrections to one published name.
func RepairSpelling(name string, spellings map[string]string) string {
	if len(spellings) == 0 {
		return name
	}
	return shouted.ReplaceAllStringFunc(name, func(word string) string {
		if fixed, ok := spellings[word]; ok {
			return fixed
		}
		return word
	})
}

// A shouted word is one written entirely in capitals, which is how these lists are kept and
// the only place the ambiguity arises: a name written in ordinary case already carries the
// dot or does not.
var shouted = regexp.MustCompile(`[A-ZÇĞİÖŞÜI]{2,}`)

func shoutedWords(name string) []string { return shouted.FindAllString(name, -1) }
