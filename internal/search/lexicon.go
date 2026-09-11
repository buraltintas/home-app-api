package search

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The lexicon is what this product knows about its own catalogue: the chains it lists and
// the words people use for the things those chains sell.
//
// It exists because the model was being asked questions we could already answer, and
// answering them badly. Measured against the live parser: "perde" and "yatak" -- the two
// most ordinary searches there are -- came back from the model as a non-home scope with
// search terms still filled in, which fails validation, so every one of those searches paid
// for a call, waited about three seconds for it, and then used the deterministic answer it
// already had. "english home" came back as out_of_scope, because read as English words that
// is what it is.
//
// So the order is inverted. What we know, we answer ourselves; the model is asked only
// about what is genuinely unclear.
type lexicon struct {
	db    *pgxpool.Pool
	ttl   time.Duration
	now   func() time.Time
	mu    sync.RWMutex
	until time.Time
	// brands maps a chain's compacted name to itself, so a query can be recognised as
	// naming one. Both the slug and the display name are held, because people type both.
	brands map[string]string
	// terms maps a product word to the category it belongs to. "gardırop" is furniture
	// even though no shop is called that.
	terms map[string]string
}

func newLexicon(db *pgxpool.Pool) *lexicon {
	return &lexicon{db: db, ttl: 10 * time.Minute, now: time.Now, brands: map[string]string{}, terms: map[string]string{}}
}

// refresh reloads both lists when they are stale. A failure leaves the previous contents in
// place: an out-of-date lexicon is a far smaller problem than an empty one, which would
// silently send every brand search back to the model.
func (l *lexicon) refresh(ctx context.Context) {
	l.mu.RLock()
	fresh := l.now().Before(l.until)
	l.mu.RUnlock()
	if fresh {
		return
	}
	brands := map[string]string{}
	rows, e := l.db.Query(ctx, `SELECT slug,name FROM brands WHERE active`)
	if e != nil {
		return
	}
	for rows.Next() {
		var slug, name string
		if rows.Scan(&slug, &name) != nil {
			continue
		}
		brands[compact(name)] = name
		brands[compact(slug)] = name
	}
	rows.Close()
	if rows.Err() != nil {
		return
	}
	terms := map[string]string{}
	rows, e = l.db.Query(ctx, `SELECT term,category_slug FROM product_terms`)
	if e != nil {
		return
	}
	for rows.Next() {
		var term, category string
		if rows.Scan(&term, &category) != nil {
			continue
		}
		terms[term] = category
	}
	rows.Close()
	if rows.Err() != nil {
		return
	}
	l.mu.Lock()
	l.brands, l.terms, l.until = brands, terms, l.now().Add(l.ttl)
	l.mu.Unlock()
}

// containsSpacedWord tests a compacted brand key against a spaced query, matching on word
// boundaries so that a chain's name is found however the query spaces it: the key for
// "English Home" is "englishhome", and it has to meet "english home" as the user typed it.
func containsSpacedWord(haystack, key string) bool {
	if key == "" {
		return false
	}
	for _, word := range spans(haystack) {
		if compact(word) == key {
			return true
		}
	}
	return false
}

// spans is every run of consecutive words in a query, longest first -- the phrases a brand
// name could be hiding in.
func spans(query string) []string {
	words := strings.Fields(query)
	var out []string
	for length := len(words); length >= 1; length-- {
		for start := 0; start+length <= len(words); start++ {
			out = append(out, strings.Join(words[start:start+length], " "))
		}
	}
	return out
}

func compact(value string) string {
	key := textnorm.Key(value)
	return strings.ReplaceAll(key, " ", "")
}

// enrich answers from the catalogue what the deterministic parser could not.
//
// It never overrides a verdict already reached: a query the parser refused stays refused,
// and one it has already placed in home living keeps its categories. It only fills in what
// was left blank.
func (l *lexicon) enrich(ctx context.Context, intent Intent, query string) Intent {
	if l == nil || intent.Scope == ScopeOutOfScope {
		return intent
	}
	l.refresh(ctx)
	l.mu.RLock()
	brands, terms := l.brands, l.terms
	l.mu.RUnlock()

	// A chain named anywhere in the query is a search for that chain. Whole query or part
	// of it: "doğtaş" and "doğtaş mobilya" are the same request, and only the first of them
	// used to be understood -- the second went looking for a shop whose sign says both
	// words, and there is no such shop.
	//
	// The longest name wins, so "yataş bedding" is that chain rather than Yataş, and the
	// rest of the query still contributes its product words below: "bellona yatak" is
	// Bellona, and beds.
	if intent.StoreName == "" {
		normalized := textnorm.Key(query)
		best, bestLength := "", 0
		for key, name := range brands {
			if len(key) > bestLength && containsSpacedWord(normalized, key) {
				best, bestLength = name, len(key)
			}
		}
		if best != "" {
			intent.Scope = ScopeHomeLiving
			intent.StoreName = best
		}
	}
	if len(intent.Categories) > 0 {
		return intent
	}
	// Otherwise look for product words. The longest match wins, so "yemek takımı" is
	// tableware rather than furniture by way of "yemek". Whole-word matching is the
	// deterministic parser's own, so the two cannot disagree about what counts as a word.
	normalized := normalizeText(query)
	folded := foldLatin(normalized)
	best, bestLength := "", 0
	for term, category := range terms {
		if len(term) > bestLength && containsWord(normalized, folded, term) {
			best, bestLength = category, len(term)
		}
	}
	if best == "" {
		return intent
	}
	intent.Scope = ScopeHomeLiving
	intent.Categories = []string{best}
	return intent
}

// confident reports whether the catalogue has already answered the question well enough
// that asking a language model could only cost time. A named chain or a known category is
// an answer; "merhaba" is not.
func confident(intent Intent) bool {
	if intent.Scope == ScopeOutOfScope {
		return true
	}
	return intent.Scope == ScopeHomeLiving && (intent.StoreName != "" || len(intent.Categories) > 0)
}

// learn records what the model understood, so the catalogue answers it next time.
//
// Only the model's own product words are kept, and only against a single category: a query
// the model placed in two categories at once has not told us which of them the word belongs
// to, and a guess written into a shared table is worse than no entry. Locations, chain
// names and anything the query says about price or style are left alone -- those are not
// product vocabulary and would poison the term list for everybody.
//
// Failures are ignored on purpose. This is a side effect of answering somebody's search;
// it must never be the reason their search fails.
func (l *lexicon) learn(ctx context.Context, intent Intent) {
	if l == nil || l.db == nil || intent.Scope != ScopeHomeLiving || len(intent.Categories) != 1 {
		return
	}
	category := intent.Categories[0]
	l.mu.RLock()
	brands := l.brands
	l.mu.RUnlock()
	for _, raw := range intent.ProductTerms {
		term := textnorm.Key(raw)
		if !learnable(term, brands) {
			continue
		}
		_, _ = l.db.Exec(ctx, `INSERT INTO product_terms(term,locale,category_slug,source) VALUES($1,$2,$3,'learned') ON CONFLICT DO NOTHING`,
			term, string(intent.QueryLanguage), category)
	}
	// The next refresh picks these up; forcing one here would put a query on the path of
	// every search that reaches the model.
}

// learnable decides whether a word the model produced belongs in the shared term list.
//
// The bar is deliberately high: this table is consulted for every future search by every
// visitor, so a wrong entry is a wrong answer repeated indefinitely, while a refused one
// costs only one more model call the next time somebody uses that word.
func learnable(term string, brands map[string]string) bool {
	// One or two characters is not a word, and a sentence is not a term. Both appear in
	// model output often enough to be worth refusing.
	if len([]rune(term)) < 3 || len(strings.Fields(term)) > 3 {
		return false
	}
	// A chain's name is not a product word. "english home" recorded as one would place
	// every future mention of that chain into whatever category one search happened to
	// carry.
	_, isBrand := brands[compact(term)]
	return !isBrand
}
