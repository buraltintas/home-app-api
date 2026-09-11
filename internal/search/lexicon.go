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

	// A query that is a chain's name is a search for that chain. Whole-query match only:
	// "yataş" inside "yataş yatak aldım" is a product sentence, and the parser handles
	// those better than a substring test would.
	if intent.StoreName == "" {
		if name, ok := brands[compact(query)]; ok {
			intent.Scope = ScopeHomeLiving
			intent.StoreName = name
			return intent
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
