package search

import (
	"testing"

	storepkg "github.com/burakaltintas/home-app-api/internal/store"
)

func policy() SufficiencyPolicy {
	p := DefaultSufficiencyPolicy()
	p.Enabled = true
	return p
}

// The whole point of the gate: a city we know well, asked an ordinary question, is
// answered out of our own catalogue and the provider is never troubled.
func TestDenseCatalogueAnswersAGenericQueryOnItsOwn(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 14, Relevance: 0.9, Coverage: 300})
	if !d.LocalOnly {
		t.Fatalf("expected a local-only search, got reasons %v", d.Reasons)
	}
}

// The same question in a town where we hold nine stores is not the same question. Nine
// results there are not a full answer, they are the whole catalogue -- and the provider
// is the only thing that knows what else is on that street.
func TestThinCatalogueStillAsksTheProvider(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 9, Relevance: 0.9, Coverage: 9})
	if d.LocalOnly {
		t.Fatal("expected a provider call where the catalogue is thin")
	}
	if !hasReason(d, reasonInsufficientCoverage) {
		t.Fatalf("reasons=%v, want %s", d.Reasons, reasonInsufficientCoverage)
	}
}

// Somebody who types a store's name wants that store. A catalogue full of near
// neighbours must never stand in for it -- this is the one condition whose absence
// people would notice immediately.
func TestANamedStoreAlwaysReachesTheProvider(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 30, Relevance: 1, Coverage: 900, ExplicitStore: true})
	if d.LocalOnly {
		t.Fatal("expected a provider call for an explicit store search")
	}
	if !hasReason(d, reasonExplicitStore) {
		t.Fatalf("reasons=%v, want %s", d.Reasons, reasonExplicitStore)
	}
}

// A full page of results that are not about what was asked is not an answer. Counting
// rows alone would have stopped here, which is exactly the failure the relevance
// condition exists to prevent.
func TestManyResultsAreNotEnoughWhenNoneOfThemMatch(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 20, Relevance: 0.2, Coverage: 500})
	if d.LocalOnly {
		t.Fatal("expected a provider call when the local results do not match the request")
	}
	if !hasReason(d, reasonInsufficientRelevance) {
		t.Fatalf("reasons=%v, want %s", d.Reasons, reasonInsufficientRelevance)
	}
}

// Half a screen of results is not a screen of results, however good they are.
func TestTooFewResultsReachTheProvider(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 3, Relevance: 1, Coverage: 500})
	if !hasReason(d, reasonInsufficientResults) {
		t.Fatalf("reasons=%v, want %s", d.Reasons, reasonInsufficientResults)
	}
}

// Every failing condition is reported, not only the first. "We call the provider too
// often" and "we call the provider too often because the catalogue is thin" lead to
// completely different work, and the counters have to be able to tell them apart.
func TestEveryFailingConditionIsCounted(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 1, Relevance: 0, Coverage: 0, ExplicitStore: true})
	if len(d.Reasons) != 4 {
		t.Fatalf("reasons=%v, want all four", d.Reasons)
	}
}

// A search with no location cannot say how much of the searcher's surroundings we hold,
// so it is treated as knowing nothing rather than as knowing enough.
func TestUnknownCoverageFallsBack(t *testing.T) {
	d := policy().decide(sufficiency{ResultCount: 20, Relevance: 1, Coverage: coverageUnknown})
	if d.LocalOnly {
		t.Fatal("expected unknown coverage to fall back")
	}
}

// With the flag off nothing here decides anything: the search behaves as it does in
// production today, and the recorded reason says so.
func TestTheFlagOffLeavesTodaysBehaviourAlone(t *testing.T) {
	d := DefaultSufficiencyPolicy().decide(sufficiency{ResultCount: 30, Relevance: 1, Coverage: 900})
	if d.LocalOnly || d.reason() != reasonGateDisabled {
		t.Fatalf("decision=%+v, want a disabled gate", d)
	}
}

func hasReason(d gateDecision, want string) bool {
	for _, r := range d.Reasons {
		if r == want {
			return true
		}
	}
	return false
}

// Relevance is what stops the gate from mistaking a list for an answer. A shop that
// sells what was asked for scores; one that merely happens to be nearby does not.
func TestLocalRelevanceReadsWhatTheResultsActuallySell(t *testing.T) {
	carpet := storepkg.Item{Name: "Anadolu Halı", Categories: []string{"carpet"}}
	bedding := storepkg.Item{Name: "Uyku Dünyası", Categories: []string{"bedding"}}
	general := storepkg.Item{Name: "Ev Yaşam", Categories: []string{"carpet", "bedding", "furniture", "lighting"}}
	intent := Intent{Categories: []string{"carpet"}, NormalizedQuery: "halı mağazası", ProductTerms: []string{"halı"}}

	if got := localRelevance([]storepkg.Item{carpet, carpet, carpet}, intent, 3); got < 0.9 {
		t.Errorf("a carpet shop asked for carpets scored %.2f", got)
	}
	if got := localRelevance([]storepkg.Item{bedding, bedding, bedding}, intent, 3); got > 0.3 {
		t.Errorf("bedding shops asked for carpets scored %.2f", got)
	}
	// It carries carpets among much else: worth showing, not worth calling a full answer
	// on its own.
	partial := localRelevance([]storepkg.Item{general, general}, intent, 2)
	if partial < 0.5 || partial > 0.8 {
		t.Errorf("a general home store scored %.2f, expected a middling score", partial)
	}
	if got := localRelevance(nil, intent, 3); got != 0 {
		t.Errorf("no results scored %.2f, want 0", got)
	}
}

// The Turkish "I" is the reason this is folded rather than lowercased: a shop signed
// "HALI" must match a search for "halı", which strings.ToLower alone gets wrong.
func TestRelevanceMatchesAShopSignWrittenInCapitals(t *testing.T) {
	shop := storepkg.Item{Name: "GÜNEY HALI VE YATAK"}
	intent := Intent{NormalizedQuery: "halı", ProductTerms: []string{"halı"}}
	if got := localRelevance([]storepkg.Item{shop}, intent, 1); got < 0.9 {
		t.Fatalf("scored %.2f, want a full match", got)
	}
}

// Words that match every shop distinguish none of them, and the district somebody is
// standing in is not part of a shop's name. Neither may drag a genuine match down.
func TestGenericAndLocationWordsDoNotCountAgainstAMatch(t *testing.T) {
	shop := storepkg.Item{Name: "Bambi Yatak", Categories: []string{"bedding"}}
	intent := Intent{NormalizedQuery: "kadıköy yatak mağazası", LocationText: "Kadıköy", Categories: []string{"bedding"}}
	if terms := relevanceTerms(intent); len(terms) != 1 || terms[0] != "yatak" {
		t.Fatalf("terms=%v, want just the product word", terms)
	}
	if got := localRelevance([]storepkg.Item{shop}, intent, 1); got < 0.9 {
		t.Fatalf("scored %.2f, want a full match", got)
	}
}

// The shadow sample has to stay small: a shadow call costs exactly what a real one
// costs, so measuring everything would spend the saving it exists to verify.
func TestShadowSamplingHonoursTheConfiguredRate(t *testing.T) {
	s := &Service{policy: policy(), sample: func() float64 { return 0.02 }}
	if !s.shadowSampled() {
		t.Error("a draw below the rate should be measured")
	}
	s.sample = func() float64 { return 0.9 }
	if s.shadowSampled() {
		t.Error("a draw above the rate should not be measured")
	}
	off := &Service{policy: DefaultSufficiencyPolicy(), sample: func() float64 { return 0 }}
	if off.shadowSampled() {
		t.Error("no measurement happens while the gate is off")
	}
	none := policy()
	none.ShadowRate = 0
	quiet := &Service{policy: none, sample: func() float64 { return 0 }}
	if quiet.shadowSampled() {
		t.Error("a zero rate measures nothing")
	}
}
