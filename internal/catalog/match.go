package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The thresholds the whole catalogue's integrity rests on.
//
// They are stated here, once, with what each one is for. Tuning them is a deliberate act;
// a number that drifts by accident turns one shop into two, or two shops into one, and
// neither is visible until somebody complains about the result.
const (
	// Near enough, and named closely enough, to be the same shop. Two different shops at
	// the same street corner do not also share most of their sign.
	mergeSimilarity = 0.55
	mergeMeters     = 150
	// How far a row whose own coordinate nothing vouches for may sit from the brand's.
	// A legacy row was placed by a provider we no longer talk to, and provider geocoding
	// routinely disagrees with a chain's own by a few hundred metres -- the first import
	// found the same five Antalya shops at 0, 32, 57, 228 and 300 m. A brand-placed row is
	// not given this slack: the brand knows where its own shop is.
	legacyMeters = 400
	// A row published with no coordinates has only its name and its district to argue
	// with, so it has to argue much harder.
	placeOnlySimilarity = 0.75
	// Below merge, above this: a resemblance too strong to ignore and too weak to act on.
	// These go to a person rather than being guessed at.
	reviewSimilarity = 0.40
)

// Candidate is a store already in the catalogue that might be the row being imported.
type Candidate struct {
	ID          string
	Name        string
	CompactName string
	Similarity  float64
	Distance    float64
	SourceKind  string
	Verified    bool
	BrandID     *string
	// BrandExternalID is the id this brand's own list already gave this store, when it
	// gave one. Two rows the brand numbers differently are two shops, whatever they are
	// called and however close together they stand.
	BrandExternalID string
}

// Matcher decides, for one published row, which of three things is true: we already have
// this shop under this brand's own id, we probably already have it, or we do not.
type Matcher struct {
	tx pgx.Tx
	// Which existing stores this run has already spoken for.
	claimed map[string]bool
}

func NewMatcher(tx pgx.Tx) *Matcher { return &Matcher{tx: tx, claimed: map[string]bool{}} }

func (m *Matcher) claim(id string) { m.claimed[id] = true }

// containment reports whether either compact name holds the other whole. Very short names
// are excluded: almost everything contains "as".
func containment(a, b string) bool {
	if len(a) < 6 || len(b) < 6 {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// Match returns the decision for one row. It never writes.
func (m *Matcher) Match(ctx context.Context, brand BrandSpec, in RawStore) (Decision, error) {
	decision := Decision{Raw: in}
	if in.Name == "" || in.ExternalID == "" {
		decision.Action, decision.Reason = ActionSkipped, "published row has no name or id"
		return decision, nil
	}

	// 1. The brand's own id. Exact, cheap, and the only identity here that cannot be
	// wrong: a chain knows which of its shops is which.
	var existing string
	e := m.tx.QueryRow(ctx, `SELECT store_id::text FROM store_external_sources WHERE provider=$1 AND external_id=$2`, brand.Provider(), in.ExternalID).Scan(&existing)
	if e == nil {
		decision.Action, decision.StoreID = ActionUpdated, existing
		decision.Reason = "same store, matched on the brand's own id"
		m.claim(existing)
		return decision, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return decision, e
	}

	// 2. Everything else. A row we have never seen under this brand may still be a row we
	// already hold from somewhere else -- a legacy import, an admin entry, another brand's
	// list naming the same shop. This is the step that stops the catalogue growing twins.
	candidate, found, e := m.closest(ctx, in, brand.Provider())
	if e != nil {
		return decision, e
	}
	if !found {
		decision.Action, decision.Reason = ActionInserted, "no comparable store nearby"
		return decision, nil
	}
	decision.Similarity, decision.Distance, decision.StoreID = candidate.Similarity, candidate.Distance, candidate.ID

	// Two chains are two chains. A store already confirmed as one brand's cannot be
	// another brand's, however alike the signs read and however close they stand -- an
	// English Home and a Madame Coco in the same shopping centre are 89 m apart and share
	// the mall's name, which is most of what a name-similarity score can see.
	if candidate.BrandID != nil && *candidate.BrandID != brand.ID {
		decision.Action = ActionInserted
		decision.Reason = fmt.Sprintf("nearest comparable store belongs to another brand (%q)", candidate.Name)
		decision.StoreID = ""
		return decision, nil
	}

	// The brand is the authority on which of its own shops are distinct. If the nearest
	// comparable store already carries a different id from this same list, the chain has
	// told us they are two shops -- "Ayvalık 1" and "Ayvalık 2" stand 229 m apart and share
	// four fifths of their name, and no similarity threshold will ever separate them.
	if candidate.BrandExternalID != "" && candidate.BrandExternalID != in.ExternalID {
		decision.Action = ActionInserted
		decision.Reason = fmt.Sprintf("the brand lists this separately from %q", candidate.Name)
		decision.StoreID = ""
		return decision, nil
	}

	// A store already claimed by an earlier row of this same run must not be claimed
	// twice. Without this a chain with two branches near one another, and one vague old
	// row between them, collapses into a single shop -- and the second row would quietly
	// steal the first one's source id on its way.
	if m.claimed[candidate.ID] {
		decision.Action = ActionReview
		decision.Reason = fmt.Sprintf("nearest match %q was already matched by another store in this list", candidate.Name)
		decision.StoreID = ""
		return decision, nil
	}

	hasPoint := in.Latitude != nil && in.Longitude != nil
	// One name containing the other is identity evidence that similarity scores badly:
	// "englishhome" inside "englishhomeantlaracad" is 0.42 by trigram and obviously the
	// same shop on the ground. It counts only alongside proximity, never on its own.
	contained := containment(CompactName(in.Name), candidate.CompactName)
	radius := float64(mergeMeters)
	if !candidate.Verified {
		radius = legacyMeters
	}
	switch {
	case hasPoint && (candidate.Similarity >= mergeSimilarity || contained) && candidate.Distance <= radius:
		decision.Action = ActionUpdated
		why := fmt.Sprintf("%.2f name similarity", candidate.Similarity)
		if contained {
			why = "one name contains the other"
		}
		decision.Reason = fmt.Sprintf("same place: %.0f m away, %s, matched %q", candidate.Distance, why, candidate.Name)
		m.claim(candidate.ID)
	case !hasPoint && candidate.Similarity >= placeOnlySimilarity:
		decision.Action = ActionUpdated
		decision.Reason = fmt.Sprintf("no published coordinates; %.2f name similarity to %q in the same district", candidate.Similarity, candidate.Name)
		m.claim(candidate.ID)
	case candidate.Similarity >= reviewSimilarity:
		decision.Action = ActionReview
		decision.Reason = fmt.Sprintf("resembles %q (%.2f, %.0f m) but not closely enough to merge", candidate.Name, candidate.Similarity, candidate.Distance)
	default:
		decision.Action, decision.Reason = ActionInserted, "nearest comparable store is not the same shop"
		decision.StoreID = ""
	}
	return decision, nil
}

// closest finds the single best comparable store. Two ways in, because a published row
// either has a point or it does not, and the two cases cannot be compared the same way.
func (m *Matcher) closest(ctx context.Context, in RawStore, provider string) (Candidate, bool, error) {
	compact := CompactName(in.Name)
	if compact == "" {
		return Candidate{}, false, nil
	}
	var row pgx.Row
	if in.Latitude != nil && in.Longitude != nil {
		// Ranked by name, not by distance: the nearest shop is frequently not the one with
		// the same sign, and it is the sign that decides identity. The radius here is wider
		// than the merge radius on purpose, so that a near-miss can still be reported for
		// review instead of silently inserted.
		row = m.tx.QueryRow(ctx, `
SELECT id::text, name, compact_name, similarity(compact_name,$1) AS sim,
       ST_Distance(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) AS metres,
       source_kind, data_verified_at IS NOT NULL, brand_id::text,
       coalesce((SELECT external_id FROM store_external_sources x WHERE x.store_id=stores.id AND x.provider=$5),'')
FROM stores
WHERE deleted_at IS NULL
  AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography, $4)
  AND compact_name <> ''
ORDER BY similarity(compact_name,$1) DESC, metres
LIMIT 1`, compact, *in.Latitude, *in.Longitude, mergeMeters*6, provider)
	} else {
		if in.City == "" || in.District == "" {
			return Candidate{}, false, nil
		}
		row = m.tx.QueryRow(ctx, `
SELECT id::text, name, compact_name, similarity(compact_name,$1) AS sim, 0::float8 AS metres, source_kind, data_verified_at IS NOT NULL, brand_id::text,
       coalesce((SELECT external_id FROM store_external_sources x WHERE x.store_id=stores.id AND x.provider=$4),'')
FROM stores
WHERE deleted_at IS NULL AND city=$2 AND district=$3 AND compact_name <> ''
ORDER BY similarity(compact_name,$1) DESC
LIMIT 1`, compact, in.City, in.District, provider)
	}
	var c Candidate
	e := row.Scan(&c.ID, &c.Name, &c.CompactName, &c.Similarity, &c.Distance, &c.SourceKind, &c.Verified, &c.BrandID, &c.BrandExternalID)
	if errors.Is(e, pgx.ErrNoRows) {
		return Candidate{}, false, nil
	}
	if e != nil {
		return Candidate{}, false, e
	}
	return c, true, nil
}
