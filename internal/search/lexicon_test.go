package search

import (
	"context"
	"testing"
	"time"
)

// A lexicon already loaded, so the test exercises the matching rather than the database.
func loaded() *lexicon {
	l := newLexicon(nil)
	l.brands = map[string]string{"englishhome": "English Home", "madamecoco": "Madame Coco", "dogtas": "Doğtaş"}
	l.terms = map[string]string{"gardırop": "furniture", "yemek masası": "furniture", "yemek takımı": "tableware", "tül perde": "curtain", "perde": "curtain"}
	l.until = time.Now().Add(time.Hour)
	return l
}

// A chain named inside a longer query is still that chain. "doğtaş" was understood and
// "doğtaş mobilya" was not, which is the same request typed slightly more naturally.
func TestLexiconFindsAChainNamedInsideALongerQuery(t *testing.T) {
	l := loaded()
	l.terms["mobilya"] = "furniture"
	for _, query := range []string{"doğtaş mobilya", "english home kadıköy", "madame coco bul"} {
		got := l.enrich(context.Background(), Deterministic(query), query)
		if got.StoreName == "" {
			t.Fatalf("%q: no chain recognised (%+v)", query, got)
		}
	}
	// The rest of the query still says what is wanted.
	got := l.enrich(context.Background(), Intent{Scope: ScopeUnclear}, "doğtaş mobilya")
	if got.StoreName != "Doğtaş" || len(got.Categories) != 1 || got.Categories[0] != "furniture" {
		t.Fatalf("%+v", got)
	}
}

// The longest name wins, so a chain whose name begins with another chain's is not mistaken
// for it.
func TestLexiconPrefersTheLongerChainName(t *testing.T) {
	l := loaded()
	l.brands["yatas"] = "Yataş"
	l.brands["yatasbedding"] = "Yataş Bedding"
	if got := l.enrich(context.Background(), Intent{Scope: ScopeUnclear}, "yataş bedding"); got.StoreName != "Yataş Bedding" {
		t.Fatalf("store name %q", got.StoreName)
	}
}

func TestLexiconRecognisesOurOwnChains(t *testing.T) {
	for _, query := range []string{"english home", "English Home", "ENGLİSH HOME", "madame coco"} {
		got := loaded().enrich(context.Background(), Deterministic(query), query)
		if got.Scope != ScopeHomeLiving || got.StoreName == "" {
			t.Fatalf("%q: scope %q name %q", query, got.Scope, got.StoreName)
		}
		if !confident(got) {
			t.Fatalf("%q would still be sent to the model", query)
		}
	}
}

// A product word the deterministic parser does not know still places the query.
func TestLexiconPlacesAProductWordTheParserMisses(t *testing.T) {
	const query = "gardırop"
	if before := Deterministic(query); len(before.Categories) > 0 {
		t.Skip("the parser now knows this word on its own")
	}
	got := loaded().enrich(context.Background(), Deterministic(query), query)
	if got.Scope != ScopeHomeLiving || len(got.Categories) != 1 || got.Categories[0] != "furniture" {
		t.Fatalf("%+v", got)
	}
}

// The longest matching word wins, or "yemek takımı" becomes furniture by way of the
// shorter word it shares with "yemek masası".
func TestLexiconPrefersTheLongerProductWord(t *testing.T) {
	blank := Intent{Scope: ScopeUnclear, Categories: []string{}}
	for query, want := range map[string]string{"tül perde": "curtain", "yemek takımı": "tableware"} {
		got := loaded().enrich(context.Background(), blank, query)
		if len(got.Categories) != 1 || got.Categories[0] != want {
			t.Fatalf("%q: %v, want %s", query, got.Categories, want)
		}
	}
}

// The catalogue fills gaps; it does not argue with an answer already given.
func TestLexiconKeepsWhatTheParserAlreadyDecided(t *testing.T) {
	const query = "tül perde"
	parsed := Deterministic(query)
	if len(parsed.Categories) == 0 {
		t.Skip("the parser no longer places this query")
	}
	got := loaded().enrich(context.Background(), parsed, query)
	if len(got.Categories) != len(parsed.Categories) {
		t.Fatalf("categories rewritten: %v -> %v", parsed.Categories, got.Categories)
	}
}

// A query we cannot place stays unplaced, so the model is still asked about it.
func TestLexiconLeavesGenuinelyUnclearQueriesAlone(t *testing.T) {
	const query = "arkadaşıma taşınma hediyesi"
	got := loaded().enrich(context.Background(), Deterministic(query), query)
	if confident(got) {
		t.Fatalf("%q was answered without the model: %+v", query, got)
	}
}

// A refusal is a decision, not a gap: nothing here may talk one back into scope.
func TestLexiconNeverOverturnsARefusal(t *testing.T) {
	const query = "halı saha"
	got := loaded().enrich(context.Background(), Deterministic(query), query)
	if got.Scope != ScopeOutOfScope {
		t.Fatalf("refusal overturned: %+v", got)
	}
}
