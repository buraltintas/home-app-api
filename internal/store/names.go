package store

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
)

// NameSuggestion is a name in the catalogue offered while somebody is still typing it:
// "arçel" is answered with "Arçelik" before the word is finished, the way "antal" is
// answered with "Antalya" when a location is being picked.
//
// A chain is offered once, by its own name, however many branches it has -- the branches
// are what the search that follows is for. A shop that belongs to no chain is offered by
// its sign.
type NameSuggestion struct {
	Name          string  `json:"name"`
	BrandSlug     string  `json:"brand_slug,omitempty"`
	Stores        int     `json:"stores"`
	NearestMeters float64 `json:"nearest_meters"`
	District      string  `json:"district,omitempty"`
	City          string  `json:"city,omitempty"`
	key           string
}

// nameHorizonMeters is the search's own horizon. A name is only worth offering if the
// search it starts can answer with that shop: one offered from 300 km away is a name that
// leads to "there is no such shop within 50 km", which is a worse answer than not offering
// it at all.
const nameHorizonMeters = 50000

const maxNameSuggestions = 8

// SuggestNames returns the names within reach of a point that have a word beginning with
// what has been typed so far.
//
// A word, not the name: "home" finds English Home as well as Home Bazaar, because people
// remember the word of a name that is distinctive and that word is not always the first.
// Names that begin with it come first all the same, since that is what typing a name
// usually looks like; after that chains before single shops, the chain with more branches
// nearby first, and the nearer first between equals.
func (s *Service) SuggestNames(ctx context.Context, typed string, lat, lon float64, limit int) ([]NameSuggestion, error) {
	key := textnorm.Key(typed)
	if utf8.RuneCountInString(key) < 2 {
		return []NameSuggestion{}, nil
	}
	if limit < 1 {
		limit = 6
	}
	if limit > maxNameSuggestions {
		limit = maxNameSuggestions
	}
	// The name is folded in SQL the way textnorm.Key folds the query -- Turkish letters and
	// the circumflexes to their plain forms, then everything that is not a letter or a digit
	// to a single space -- so "Arçelik" and "arcel" can meet. The key carries a leading space
	// so that "begins a word" is one LIKE: a space and then the typed letters. Key's output
	// holds only letters, digits and single spaces, so it cannot carry a LIKE wildcard.
	//
	// Grouped by the folded name, which is what merges a chain's linked branches with the
	// unlinked shops that carry the same sign; the chain's own spelling of the name wins.
	rows, e := s.db.Query(ctx, `WITH here AS (SELECT ST_SetSRID(ST_MakePoint($2,$1),4326)::geography AS p),
named AS (
  SELECT coalesce(b.name,s.name) AS label, b.slug AS brand, coalesce(s.district,'') AS district, s.city,
         ' '||btrim(regexp_replace(lower(translate(coalesce(b.name,s.name),'çğıöşüâîûéÇĞİÖŞÜÂÎÛÉ','cgiosuaiuecgiosuaiue')),'[^[:alnum:]]+',' ','g')) AS key,
         ST_Distance(s.location,here.p) AS d
    FROM stores s LEFT JOIN brands b ON b.id=s.brand_id, here
   WHERE s.deleted_at IS NULL AND ST_DWithin(s.location,here.p,$3)
)
SELECT (array_agg(label ORDER BY brand IS NULL, d))[1], coalesce(max(brand),''), count(*), min(d),
       (array_agg(district ORDER BY d))[1], (array_agg(city ORDER BY d))[1], key
  FROM named
 WHERE key LIKE '% '||$4||'%'
 GROUP BY key
 ORDER BY bool_or(key LIKE ' '||$4||'%') DESC, max(brand) IS NOT NULL DESC, count(*) DESC, min(d)
 LIMIT 40`, lat, lon, nameHorizonMeters, key)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var found []NameSuggestion
	for rows.Next() {
		var x NameSuggestion
		if e = rows.Scan(&x.Name, &x.BrandSlug, &x.Stores, &x.NearestMeters, &x.District, &x.City, &x.key); e != nil {
			return nil, e
		}
		x.key = strings.TrimSpace(x.key)
		found = append(found, x)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return collapseNames(found, limit), nil
}

// collapseNames drops a single shop's name that only lengthens another name on the same
// list. "Yatsan Urla" beside "Yatsan" is not a second answer: the search for Yatsan already
// finds the Urla shop, and the line it takes is a line some other name could have used.
//
// A chain's name is never the one dropped. Two chains can share a first word and be two
// companies -- Karaca and Karaca Home -- and a shop whose sign is a chain's name cut short
// ("Yataş" beside Yataş Bedding) gives way to the chain rather than the other way round.
func collapseNames(found []NameSuggestion, limit int) []NameSuggestion {
	out := make([]NameSuggestion, 0, limit)
	for _, x := range found {
		if givesWay(x, found) {
			continue
		}
		out = append(out, x)
		if len(out) == limit {
			break
		}
	}
	return out
}

func givesWay(x NameSuggestion, found []NameSuggestion) bool {
	if x.BrandSlug != "" {
		return false
	}
	for _, y := range found {
		if y.key == x.key {
			continue
		}
		if strings.HasPrefix(x.key, y.key+" ") {
			return true
		}
		if y.BrandSlug != "" && strings.HasPrefix(y.key, x.key+" ") {
			return true
		}
	}
	return false
}
