package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
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
	// A shop about to be added within this distance of another shop whose name shares a rare
	// word with it is held for a person. It is the width of one frontage: close enough that
	// two names sharing "Çaykar" are very probably one dealer's one door.
	dealerMeters = 30
	// A word found in more shop names than this is the trade's vocabulary or a big city's
	// name, not a particular business's. Measured on the catalogue: "dayanikli" is in 317
	// names and "mobilya" in 2,353; "caykar" and "altinoglu" are in two or three.
	rareWordShops = 20
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
	// BrandExternalIDs are the identifiers this brand's own list has already given this
	// store. Usually one; two when the chain listed one shop twice and the rows have since
	// been merged, which is a fact about the chain's list and not a fault to be scanned as
	// a single value. Two rows the brand numbers differently are two shops, whatever they
	// are called and however close together they stand.
	BrandExternalIDs []string
}

// Matcher decides, for one published row, which of three things is true: we already have
// this shop under this brand's own id, we probably already have it, or we do not.
type Matcher struct {
	tx pgx.Tx
	// Which existing stores this run has already spoken for.
	claimed map[string]bool
	// In how many shop names each word appears, read once per run the first time it is
	// needed. It is what tells a business's own name from the words every business uses.
	shopsWithWord map[string]int
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

// located reports whether this row's coordinate is the publisher's own.
//
// A point we put there ourselves is not evidence of where the shop is. When a chain
// publishes no coordinate the row is stood at the centre of its town, which is the right
// thing to show on a map and the wrong thing to measure with: every shop that chain has in
// that town is then standing on the same spot, nought metres from the others and nought
// metres from whichever old row happens to be nearest the town centre. Boyner publishes no
// coordinates at all, and its whole list arrived competing for one row per town -- two
// hundred and twenty of the review queue's two hundred and ninety-one entries were that.
//
// So a derived point is treated as no point: the row is compared by name inside its own
// district, which is what we actually know about it.
func located(in RawStore) bool {
	return in.Latitude != nil && in.Longitude != nil && !Derived(in.PointFrom)
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
	// The shop may already be here under a name that shares nothing with the one being
	// published. A chain that sells through dealers publishes the dealer's registered name --
	// Vestel's list says "UGS Elektronik Sanayi ve Ticaret Ltd. Şti." -- while the row we
	// already hold was written from the sign over the door: "Vestel Antalya Muratpaşa
	// Şarampol Yetkili Satış Mağazası". The two stand nought metres apart and their name
	// similarity is near zero, so the candidate above, chosen by name, is some other shop,
	// and the rule further down that recognises a chain's own sign never gets to see the row
	// that carries it. Measured on Vestel's first dry run: eight of the twenty-one Vestel
	// shops we already held would have been listed a second time beside themselves.
	//
	// So when the name finds nothing it would merge with, the sign is asked for directly:
	// the nearest unclaimed row within reach whose name carries this chain's name as a word.
	// Only as a fallback, so every verdict the name already settles stays as it was.
	if located(in) && !(found && m.settledByName(candidate, in, brand)) {
		signed, ok, e := m.signedNearby(ctx, in, brand)
		if e != nil {
			return decision, e
		}
		if ok {
			candidate, found = signed, true
		}
	}
	if !found {
		decision.Action, decision.Reason = ActionInserted, "no comparable store nearby"
		return m.holdSameDealer(ctx, in, brand, decision)
	}
	decision.Similarity, decision.Distance, decision.StoreID = candidate.Similarity, candidate.Distance, candidate.ID

	// Two chains are two chains. A store already confirmed as one brand's cannot be
	// another brand's, however alike the signs read and however close they stand -- an
	// English Home and a Madame Coco in the same shopping centre are 89 m apart and share
	// the mall's name, which is most of what a name-similarity score can see.
	if candidate.BrandID != nil && *candidate.BrandID != brand.ID {
		// One dealer can hold two franchises. A shop in Antalya appears in Taç's published
		// list and in Linens's, under nearly the same company name, at the same address --
		// it is one shop that sells both, and listing it twice is telling a visitor to
		// choose between two doors that are the same door.
		//
		// What separates that from two chains sharing a mall floor is the name: the dealer
		// carries its own name into both lists, while English Home and Madame Coco ten
		// metres apart do not resemble each other at all. So a near, same-named shop under
		// another brand is this shop, and the brand being imported is recorded as one it
		// carries rather than as a second shop.
		near := located(in) && candidate.Distance <= mergeMeters
		alike := candidate.Similarity >= mergeSimilarity || containment(CompactName(in.Name), candidate.CompactName)
		if near && alike {
			decision.Action = ActionUpdated
			decision.Carried = true
			decision.Reason = fmt.Sprintf("the same dealer %.0f m away already carries another brand (%q); recorded as carrying this one too", candidate.Distance, candidate.Name)
			m.claim(candidate.ID)
			return decision, nil
		}
		decision.Action = ActionInserted
		decision.Reason = fmt.Sprintf("nearest comparable store belongs to another brand (%q)", candidate.Name)
		decision.StoreID = ""
		return m.holdSameDealer(ctx, in, brand, decision)
	}

	// The brand is the authority on which of its own shops are distinct. If the nearest
	// comparable store already carries a different id from this same list, the chain has
	// told us they are two shops -- "Ayvalık 1" and "Ayvalık 2" stand 229 m apart and share
	// four fifths of their name, and no similarity threshold will ever separate them.
	//
	// Only identifiers the brand actually published carry that authority. One we derived
	// because the brand published none is ours, and says nothing about whether the chain
	// considers two rows distinct -- so two different derived identifiers must never be read
	// as the chain distinguishing them. They differ whenever the derivation changes, and the
	// first time it did, this rule wanted to insert all 652 İstikbal dealers a second time.
	if published(in.ExternalID) && listedSeparately(candidate.BrandExternalIDs, in.ExternalID) {
		decision.Action = ActionInserted
		decision.Reason = fmt.Sprintf("the brand lists this separately from %q", candidate.Name)
		decision.StoreID = ""
		// The chain saying this is not that other branch of its own says nothing about the
		// other chain's shop next door. Vestel's first apply let a dealer through here that a
		// dry run had held: the nearest row was a Vestel branch written earlier in the same
		// run, which a dry run never sees, and Uğur Pazarlama in Şanlıurfa was added four
		// metres from the unbranded row that was already its door.
		return m.holdSameDealer(ctx, in, brand, decision)
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

	hasPoint := located(in)
	// One name containing the other is identity evidence that similarity scores badly:
	// "englishhome" inside "englishhomeantlaracad" is 0.42 by trigram and obviously the
	// same shop on the ground. It counts only alongside proximity, never on its own.
	contained := containment(CompactName(in.Name), candidate.CompactName)
	// A nearby shop whose own sign names this chain is this chain's shop. It is the
	// strongest evidence on offer and the one similarity is worst at: "Çelik Mağazacılık
	// Konyaaltı Bellona" and our "Bellona - Antalya Çelik Centroom Konyaaltı" stand seven
	// metres apart, are plainly the same dealer, and score 0.28 -- below even the review
	// band, so the row inserted silently and the shop appeared twice in the results.
	//
	// Two different dealers of one chain do not share a doorway, so proximity is what makes
	// this safe; the rule is not applied beyond the merge radius, and a candidate the brand
	// itself lists separately has already been settled above.
	named := strings.Contains(candidate.CompactName, CompactName(brand.Name))
	radius := float64(mergeMeters)
	if !candidate.Verified {
		radius = legacyMeters
	}
	switch {
	case hasPoint && (candidate.Similarity >= mergeSimilarity || contained || named) && candidate.Distance <= radius:
		decision.Action = ActionUpdated
		why := fmt.Sprintf("%.2f name similarity", candidate.Similarity)
		if contained {
			why = "one name contains the other"
		}
		if named && !contained && candidate.Similarity < mergeSimilarity {
			why = "the shop already here is signed with this chain's name"
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
		return m.holdSameDealer(ctx, in, brand, decision)
	}
	return decision, nil
}

// holdSameDealer turns an insert into a question when the shop may already be here under
// another chain's name.
//
// A dealer often sells more than one chain from one door. Vestel's list names a shop "Çay
// Kar Day. Tük. Mall." and Merinos's names the same door "Çaykar Day. Tük. Ma." two metres
// away; inserted, it would be the same shop twice under two signs. But two shops of two
// chains two metres apart are just as often neighbours in a shopping centre, and a merge
// would hand one shop's reviews to the other -- and nothing in a name tells those apart
// reliably: the word they share is the dealer's name as often as it is the mall's. Measured
// on Vestel's first run, thirty-six rows shared a rare word with a shop within thirty
// metres; about two thirds were one dealer's door, a third were Armonipark, Akbatı and
// Vialand. So the row is neither merged nor added. It goes to the review queue, with the
// word and the shop that raised it, and a person decides with the two side by side.
func (m *Matcher) holdSameDealer(ctx context.Context, in RawStore, brand BrandSpec, decision Decision) (Decision, error) {
	if !located(in) {
		return m.holdSignedInDistrict(ctx, in, brand, decision)
	}
	rows, e := m.tx.Query(ctx, `
SELECT id::text, name, ST_Distance(location, ST_SetSRID(ST_MakePoint($2,$1),4326)::geography) AS metres
FROM stores
WHERE deleted_at IS NULL
  AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($2,$1),4326)::geography, $3)
ORDER BY metres
LIMIT 10`, *in.Latitude, *in.Longitude, float64(dealerMeters))
	if e != nil {
		return decision, e
	}
	type neighbour struct {
		id, name string
		metres   float64
	}
	var near []neighbour
	for rows.Next() {
		var n neighbour
		if e = rows.Scan(&n.id, &n.name, &n.metres); e != nil {
			rows.Close()
			return decision, e
		}
		near = append(near, n)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return decision, e
	}
	if len(near) == 0 {
		return decision, nil
	}
	frequency, e := m.wordFrequency(ctx)
	if e != nil {
		return decision, e
	}
	// Words that say where, or which chain, are shared by every neighbour and prove nothing.
	ignore := map[string]bool{}
	for _, text := range []string{in.City, in.District, brand.Name} {
		for _, word := range strings.Fields(textnorm.Key(text)) {
			ignore[word] = true
		}
	}
	own := map[string]bool{}
	for _, word := range strings.Fields(textnorm.Key(in.Name)) {
		if len([]rune(word)) >= 4 && !ignore[word] && frequency[word] <= rareWordShops {
			own[word] = true
		}
	}
	for _, n := range near {
		if m.claimed[n.id] {
			continue
		}
		for _, word := range strings.Fields(textnorm.Key(n.name)) {
			if own[word] {
				decision.Action = ActionReview
				decision.StoreID = ""
				decision.Reason = fmt.Sprintf("%.0f m from %q, and both names carry %q -- one dealer's door, or two shops side by side?", n.metres, n.name, word)
				return decision, nil
			}
		}
	}
	return decision, nil
}

// holdSignedInDistrict is the same question for a row with no point of its own.
//
// Banio publishes its five shops by address alone. "Banio Yapı Market - Osmangazi Bursa
// Şubesi" and the "Banio Yapı Market" we already held in Osmangazi score under the name bar
// a pointless row has to clear, so the first dry run would have added the Bursa shop a second
// time. A row in the same district signed with this chain's name is very likely this shop --
// but with no point there is no telling which branch it is when the chain has two in one
// district, and Banio has two in Muratpaşa. So it is not merged either: it is held for a
// person, with the row that raised it named.
func (m *Matcher) holdSignedInDistrict(ctx context.Context, in RawStore, brand BrandSpec, decision Decision) (Decision, error) {
	if in.City == "" {
		return decision, nil
	}
	brandCompact := CompactName(brand.Name)
	if brandCompact == "" {
		return decision, nil
	}
	rows, e := m.tx.Query(ctx, `
SELECT id::text, name FROM stores
WHERE deleted_at IS NULL AND city=$1 AND ($2='' OR district=$2)
  AND compact_name LIKE '%' || $3 || '%'
  AND (brand_id IS NULL OR brand_id::text = $4)
LIMIT 10`, in.City, in.District, brandCompact, brand.ID)
	if e != nil {
		return decision, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if e = rows.Scan(&id, &name); e != nil {
			return decision, e
		}
		if m.claimed[id] || !carriesBrandWord(name, brand.Name) {
			continue
		}
		decision.Action = ActionReview
		decision.StoreID = ""
		decision.Reason = fmt.Sprintf("no published point, and %q in the same district is signed with this chain's name -- this branch, or another one?", name)
		return decision, nil
	}
	return decision, rows.Err()
}

// wordFrequency counts, once per run, how many shop names each word appears in.
func (m *Matcher) wordFrequency(ctx context.Context) (map[string]int, error) {
	if m.shopsWithWord != nil {
		return m.shopsWithWord, nil
	}
	rows, e := m.tx.Query(ctx, `SELECT name FROM stores WHERE deleted_at IS NULL`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return nil, e
		}
		for word := range uniqueWords(name) {
			counts[word]++
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	m.shopsWithWord = counts
	return counts, nil
}

// closest finds the single best comparable store. Two ways in, because a published row
// either has a point or it does not, and the two cases cannot be compared the same way.
func (m *Matcher) closest(ctx context.Context, in RawStore, provider string) (Candidate, bool, error) {
	compact := CompactName(in.Name)
	if compact == "" {
		return Candidate{}, false, nil
	}
	var row pgx.Row
	if located(in) {
		// Ranked by name, not by distance: the nearest shop is frequently not the one with
		// the same sign, and it is the sign that decides identity. The radius here is wider
		// than the merge radius on purpose, so that a near-miss can still be reported for
		// review instead of silently inserted.
		row = m.tx.QueryRow(ctx, `
SELECT id::text, name, compact_name, similarity(compact_name,$1) AS sim,
       ST_Distance(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) AS metres,
       source_kind, data_verified_at IS NOT NULL, brand_id::text,
       -- Every identifier this shop already carries from this chain's list, not one of
       -- them. A shop can carry two once the chain has listed it twice and the two rows
       -- have been merged, and reading "the" identifier then fails outright -- it did, on
       -- Merinos, after the duplicate cleanup gave one dealer both of its entries.
       coalesce((SELECT array_agg(external_id) FROM store_external_sources x WHERE x.store_id=stores.id AND x.provider=$5),'{}')
FROM stores
WHERE deleted_at IS NULL
  AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography, $4)
  AND compact_name <> ''
ORDER BY similarity(compact_name,$1) DESC, metres
LIMIT 1`, compact, *in.Latitude, *in.Longitude, mergeMeters*6, provider)
	} else {
		// A district narrows the search and is not required to make one. A chain that
		// publishes neither a usable coordinate nor a district -- Vivense's Erzurum,
		// Karabük and Kastamonu shops name only their province -- would otherwise find
		// nothing to compare itself with and be added a second time on every run. The city
		// alone is enough to look in, because what decides a merge here is the name, and
		// it has to clear a much higher bar than a merge backed by a coordinate does.
		if in.City == "" {
			return Candidate{}, false, nil
		}
		row = m.tx.QueryRow(ctx, `
SELECT id::text, name, compact_name, similarity(compact_name,$1) AS sim, 0::float8 AS metres, source_kind, data_verified_at IS NOT NULL, brand_id::text,
       coalesce((SELECT array_agg(external_id) FROM store_external_sources x WHERE x.store_id=stores.id AND x.provider=$4),'{}')
FROM stores
WHERE deleted_at IS NULL AND city=$2 AND ($3='' OR district=$3) AND compact_name <> ''
ORDER BY similarity(compact_name,$1) DESC
LIMIT 1`, compact, in.City, in.District, provider)
	}
	var c Candidate
	e := row.Scan(&c.ID, &c.Name, &c.CompactName, &c.Similarity, &c.Distance, &c.SourceKind, &c.Verified, &c.BrandID, &c.BrandExternalIDs)
	if errors.Is(e, pgx.ErrNoRows) {
		return Candidate{}, false, nil
	}
	if e != nil {
		return Candidate{}, false, e
	}
	return c, true, nil
}

// settledByName reports whether the candidate chosen by name is one the rules below would
// actually merge with: close in spelling, contained in the other, or already signed with
// this chain's name -- and near enough, and not already taken by an earlier row of this
// run. A name that matches a shop across town, or one already spoken for, settles nothing.
//
// The second half was learned on the same dry run. Vestel's Antalya dealer "Grand Dayanıklı
// Tüketim" has three shops; the name picked the one already claimed, called that settled,
// and the Grand three metres away was never looked at.
func (m *Matcher) settledByName(candidate Candidate, in RawStore, brand BrandSpec) bool {
	radius := float64(mergeMeters)
	if !candidate.Verified {
		radius = legacyMeters
	}
	if m.claimed[candidate.ID] || candidate.Distance > radius {
		return false
	}
	return candidate.Similarity >= mergeSimilarity ||
		containment(CompactName(in.Name), candidate.CompactName) ||
		carriesBrandWord(candidate.Name, brand.Name)
}

// carriesBrandWord reports whether a shop's name has the chain's name in it as a word, not
// merely as letters. Taç is three letters, and "tac" sits inside Ataç and Tacettin; a
// merge decided by a substring would give a Taç dealer's reviews to a shop called Ataç.
func carriesBrandWord(name, brand string) bool {
	brandKey := textnorm.Key(brand)
	if brandKey == "" {
		return false
	}
	return strings.Contains(" "+textnorm.Key(name)+" ", " "+brandKey+" ")
}

// signedNearby finds the nearest row within the legacy radius that is signed with this
// chain's name as a word, not yet claimed by this run, and not another chain's. The
// radius is the wide one on purpose: the rows this exists for are legacy rows, placed by
// a provider whose geocoding disagrees with a chain's own by a few hundred metres, and the
// merge rule below still holds a verified row to the narrow one.
func (m *Matcher) signedNearby(ctx context.Context, in RawStore, brand BrandSpec) (Candidate, bool, error) {
	brandCompact := CompactName(brand.Name)
	if brandCompact == "" {
		return Candidate{}, false, nil
	}
	rows, e := m.tx.Query(ctx, `
SELECT id::text, name, compact_name, similarity(compact_name,$1) AS sim,
       ST_Distance(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography) AS metres,
       source_kind, data_verified_at IS NOT NULL, brand_id::text,
       coalesce((SELECT array_agg(external_id) FROM store_external_sources x WHERE x.store_id=stores.id AND x.provider=$5),'{}')
FROM stores
WHERE deleted_at IS NULL
  AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($3,$2),4326)::geography, $4)
  AND compact_name LIKE '%' || $6 || '%'
  AND (brand_id IS NULL OR brand_id::text = $7)
ORDER BY metres
LIMIT 8`, CompactName(in.Name), *in.Latitude, *in.Longitude, float64(legacyMeters), brand.Provider(), brandCompact, brand.ID)
	if e != nil {
		return Candidate{}, false, e
	}
	defer rows.Close()
	for rows.Next() {
		var c Candidate
		if e = rows.Scan(&c.ID, &c.Name, &c.CompactName, &c.Similarity, &c.Distance, &c.SourceKind, &c.Verified, &c.BrandID, &c.BrandExternalIDs); e != nil {
			return Candidate{}, false, e
		}
		if m.claimed[c.ID] || !carriesBrandWord(c.Name, brand.Name) {
			continue
		}
		return c, true, nil
	}
	return Candidate{}, false, rows.Err()
}

// listedSeparately reports whether the chain has given this store identifiers of its own
// and none of them is this row's -- which is the chain saying these are two shops.
func listedSeparately(carried []string, incoming string) bool {
	any := false
	for _, id := range carried {
		if !published(id) {
			continue
		}
		if id == incoming {
			return false
		}
		any = true
	}
	return any
}

// published reports whether an identifier came from the chain rather than from us.
func published(id string) bool { return id != "" && !strings.HasPrefix(id, derivedPrefix) }
