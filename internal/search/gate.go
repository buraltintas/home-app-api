package search

import (
	"context"
	"math"
	"strings"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"
)

// Why there is a gate at all.
//
// Every search asked the provider, in parallel with our own catalogue, whether or not the
// catalogue could already answer it. That is a bill that grows with traffic rather than
// with the product, and it does not have to. This decides -- before any provider call is
// made -- whether what we already hold is an answer.
//
// The one thing it must never do is trade a person's result for a saving. So the decision
// is deliberately not "did we find enough rows": it also asks whether those rows are
// actually about what was asked, and whether we know the searcher's surroundings well
// enough for their absence to mean anything.

// SufficiencyPolicy holds every number the gate turns on. They live here, in one place,
// because the right values are not knowable in advance -- they come out of the shadow
// measurements below and will be moved more than once. A threshold scattered through the
// search body as a literal cannot be moved by anyone but the person who wrote it.
type SufficiencyPolicy struct {
	// Enabled is the feature flag. While it is false the search behaves exactly as it
	// does in production today: the provider is asked in parallel, every time.
	Enabled bool
	// MinResults: how many local results count as a filled first screen.
	MinResults int
	// MinRelevance: how well, from 0 to 1, the top local results must match the request.
	MinRelevance float64
	// RelevanceSample: how many of the top results relevance is measured over. Measuring
	// the whole list would let a long tail of weak matches drown a strong opening.
	RelevanceSample int
	// MinCoverage: how many stores we must already hold near the searcher before the
	// absence of a result is evidence of anything.
	MinCoverage int
	// CoverageRadiusMeters: the radius coverage is counted over.
	CoverageRadiusMeters int
	// ShadowRate: the fraction of local-only searches that are measured against the
	// provider afterwards. Kept small on purpose -- a shadow call costs exactly what a
	// real one costs, so a large sample eats the saving it is there to verify.
	ShadowRate float64
}

// DefaultSufficiencyPolicy is deliberately conservative. The first version of this is not
// trying to reach the theoretical ceiling; it is trying not to make search worse. The
// numbers are meant to be raised once shadow measurement says what they can afford to be.
func DefaultSufficiencyPolicy() SufficiencyPolicy {
	return SufficiencyPolicy{
		Enabled:              false,
		MinResults:           8,
		MinRelevance:         0.6,
		RelevanceSample:      5,
		MinCoverage:          40,
		CoverageRadiusMeters: 15000,
		ShadowRate:           0.05,
	}
}

// relevanceSample keeps a policy that was never configured from silently measuring
// relevance over nothing.
func (p SufficiencyPolicy) relevanceSample() int {
	if p.RelevanceSample < 1 {
		return DefaultSufficiencyPolicy().RelevanceSample
	}
	return p.RelevanceSample
}

// Why a provider call was made. Each one is counted separately, because "we call the
// provider too often" and "we call the provider too often for thin catalogues" lead to
// completely different work.
const (
	reasonGateDisabled          = "gate_disabled"
	reasonExplicitStore         = "explicit_store"
	reasonInsufficientResults   = "insufficient_results"
	reasonInsufficientRelevance = "insufficient_relevance"
	reasonInsufficientCoverage  = "insufficient_coverage"
)

// coverageUnknown is what a search with no location reports. It is treated as too little
// coverage rather than enough, so an unknown always falls back.
const coverageUnknown = -1

// sufficiency is what the gate looks at. Nothing in here comes from the provider.
type sufficiency struct {
	ResultCount   int
	Relevance     float64
	Coverage      int
	ExplicitStore bool
}

type gateDecision struct {
	LocalOnly bool
	Reasons   []string
}

func (d gateDecision) reason() string { return strings.Join(d.Reasons, ",") }

// decide is the whole rule: enoughResults && goodRelevance && knownCoverage &&
// !explicitStoreSearch. Every failing condition is reported, not just the first, so the
// counters say which of them is actually driving the bill.
func (p SufficiencyPolicy) decide(in sufficiency) gateDecision {
	if !p.Enabled {
		return gateDecision{Reasons: []string{reasonGateDisabled}}
	}
	var reasons []string
	// Somebody who typed a store's name wants that store, not something like it. A
	// catalogue full of near neighbours is not an answer to "Yataş Ataşehir", and a
	// gate that counted them would be the one change here that people would notice.
	if in.ExplicitStore {
		reasons = append(reasons, reasonExplicitStore)
	}
	if in.ResultCount < p.MinResults {
		reasons = append(reasons, reasonInsufficientResults)
	}
	if in.Relevance < p.MinRelevance {
		reasons = append(reasons, reasonInsufficientRelevance)
	}
	if in.Coverage < p.MinCoverage {
		reasons = append(reasons, reasonInsufficientCoverage)
	}
	return gateDecision{LocalOnly: len(reasons) == 0, Reasons: reasons}
}

// localRelevance answers the question a result count cannot: are these rows about what
// was asked? Twenty stores that merely happen to be nearby are not a reason to stop
// looking, and the provider's own numbers say why this matters -- the stores only it
// knows about score higher on our own ranking than the ones we already hold.
//
// The measure is the mean over the top few results, each scored from 0 to 1.
func localRelevance(items []storepkg.Item, intent Intent, sample int) float64 {
	if len(items) == 0 || sample < 1 {
		return 0
	}
	terms := relevanceTerms(intent)
	if sample > len(items) {
		sample = len(items)
	}
	total := 0.0
	for _, x := range items[:sample] {
		total += itemRelevance(x, intent, terms)
	}
	return total / float64(sample)
}

// A store matches either because it carries the category that was asked for, or because
// the words in the query appear in what it is called. The better of the two counts: a
// query carries words that describe a preference rather than a shop ("ucuz", "büyük"),
// and those must not drag down a store that is plainly the right kind of shop.
func itemRelevance(x storepkg.Item, intent Intent, terms []string) float64 {
	score := 0.0
	if len(intent.Categories) > 0 && len(x.Categories) > 0 {
		matched := 0
		for _, c := range x.Categories {
			for _, want := range intent.Categories {
				if c == want {
					matched++
					break
				}
			}
		}
		switch {
		case matched == 0:
			// A store that carries none of the asked-for categories contributes nothing.
		case float64(matched)/float64(len(x.Categories)) >= 0.5:
			// Most of what this shop sells is what was asked for.
			score = 1
		default:
			// It carries the category among many others.
			score = 0.7
		}
	}
	if len(terms) > 0 {
		haystack := foldLatin(normalizeText(strings.Join([]string{x.Name, x.BrandName, strings.Join(x.CategoryLabels, " ")}, " ")))
		hits := 0
		for _, t := range terms {
			if strings.Contains(haystack, t) {
				hits++
			}
		}
		if term := float64(hits) / float64(len(terms)); term > score {
			score = term
		}
	}
	return score
}

// The words in a query that describe the thing being looked for. Where the searcher is
// standing is dropped, because a shop's name does not contain the district it stands in;
// so is the vocabulary the whole trade shares, because a word that matches every shop
// distinguishes none of them. Both are general facts about how people write a search, not
// a list of cases somebody noticed.
var genericQueryWords = map[string]bool{
	"magaza": true, "magazasi": true, "magazalari": true, "magazalar": true,
	"dukkan": true, "dukkani": true, "satan": true, "satis": true, "satici": true,
	"store": true, "stores": true, "shop": true, "shops": true, "shopping": true,
	"laden": true, "geschaft": true, "magazin": true, "magazina": true,
	"yakin": true, "yakinimda": true, "civari": true, "nerede": true,
	"near": true, "nearby": true, "around": true,
}

func isCategorySlug(categories []string, term string) bool {
	term = strings.TrimSpace(strings.ToLower(term))
	for _, c := range categories {
		if c == term {
			return true
		}
	}
	return false
}

func relevanceTerms(intent Intent) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	add := func(raw string) {
		for _, word := range strings.Fields(foldLatin(normalizeText(raw))) {
			word = strings.Trim(word, ".,:;!?\"'()")
			if len([]rune(word)) < 3 || genericQueryWords[word] || seen[word] {
				continue
			}
			seen[word] = true
			out = append(out, word)
		}
	}
	for _, t := range intent.ProductTerms {
		// The parser sometimes reports a product as one of our own category slugs --
		// "carpet" for "halı". Those are internal English keys and appear in no Turkish
		// shop sign, so counting them as words to look for would dilute every score by a
		// term that can never match. The category itself is already checked, properly.
		if isCategorySlug(intent.Categories, t) {
			continue
		}
		add(t)
	}
	// The person's own words, minus the place they named -- the location is already a
	// filter on the query and is never part of a store's name.
	query := intent.NormalizedQuery
	if intent.LocationText != "" {
		query = strings.ReplaceAll(foldLatin(normalizeText(query)), foldLatin(normalizeText(intent.LocationText)), " ")
	}
	add(query)
	return out
}

// catalogueCoverage counts the stores we already hold around the searcher. The gate needs
// it because "we found nine results" means one thing in a city where we know six hundred
// stores and something else entirely in a town where we know nine: in the second case the
// nine results are not a full answer, they are the whole catalogue.
func (s *Service) catalogueCoverage(ctx context.Context, lat, lon *float64, radius int) int {
	if lat == nil || lon == nil || s.db == nil {
		return coverageUnknown
	}
	if radius < 1 {
		radius = 15000
	}
	var n int
	if e := s.db.QueryRow(ctx, `SELECT count(*) FROM stores WHERE deleted_at IS NULL AND ST_DWithin(location,ST_SetSRID(ST_MakePoint($2,$1),4326)::geography,$3)`, *lat, *lon, radius).Scan(&n); e != nil {
		return coverageUnknown
	}
	return n
}

// topScore is the score of the best result a person was actually shown. Shadow
// measurement compares the provider's finds against it: a store that would not have
// outranked what we already showed was not a miss worth paying for.
func topScore(results []Result) float64 {
	best := math.Inf(-1)
	for _, r := range results {
		if r.score > best {
			best = r.score
		}
	}
	if math.IsInf(best, -1) {
		return 0
	}
	return best
}

// Where the search happened, as far as the results can say. Taken from the nearest result
// rather than reverse geocoded, which costs nothing and is what lets the gate's numbers be
// read city by city -- a national rate would hide the towns where the catalogue is thin.
func searchPlace(results []Result) (string, string) {
	nearest := math.MaxFloat64
	city, district := "", ""
	for _, r := range results {
		if r.DistanceMeters != nil && *r.DistanceMeters < nearest && strings.TrimSpace(r.City) != "" {
			nearest, city, district = *r.DistanceMeters, strings.TrimSpace(r.City), strings.TrimSpace(r.District)
		}
	}
	return city, district
}
