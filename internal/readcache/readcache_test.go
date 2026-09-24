package readcache

import (
	"strings"
	"testing"
	"time"
)

func TestServesWhatItStored(t *testing.T) {
	c := New(1<<20, time.Minute)
	c.Put("a", "store:1", []byte("one"))
	if got, ok := c.Get("a"); !ok || string(got) != "one" {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("a key never stored was answered")
	}
}

// The lifetime is the whole staleness budget, so it has to actually expire.
func TestExpires(t *testing.T) {
	c := New(1<<20, time.Millisecond)
	c.Put("a", "store:1", []byte("one"))
	time.Sleep(3 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("an expired answer was served")
	}
}

// A review changes a shop's page and the count on it. Both are dropped, whatever locale or
// limit they were asked in, because they are about the same shop.
func TestDropTakesEverythingAboutOneShop(t *testing.T) {
	c := New(1<<20, time.Minute)
	c.Put("detail:tr", "store:1", []byte("tr"))
	c.Put("detail:en", "store:1", []byte("en"))
	c.Put("nearby:6", "store:1", []byte("near"))
	c.Put("other", "store:2", []byte("other"))
	c.Drop("store:1")
	for _, key := range []string{"detail:tr", "detail:en", "nearby:6"} {
		if _, ok := c.Get(key); ok {
			t.Fatalf("%s survived the drop", key)
		}
	}
	if _, ok := c.Get("other"); !ok {
		t.Fatal("another shop was dropped too")
	}
}

// The budget is in bytes because entries differ by twenty to one.
func TestBudgetEvictsOldestFirst(t *testing.T) {
	c := New(120, time.Minute)
	big := strings.Repeat("x", 50)
	c.Put("first", "store:1", []byte(big))
	c.Put("second", "store:2", []byte(big))
	if _, ok := c.Get("first"); !ok {
		t.Fatal("the first entry should still fit")
	}
	// Touching "first" above makes "second" the oldest.
	c.Put("third", "store:3", []byte(big))
	if _, ok := c.Get("second"); ok {
		t.Fatal("the least recently used entry should have gone")
	}
	if _, ok := c.Get("first"); !ok {
		t.Fatal("the most recently used entry should have stayed")
	}
	if _, ok := c.Get("third"); !ok {
		t.Fatal("the newest entry should be there")
	}
}

// Switched off is a configuration, not a branch at every call site.
func TestNilCacheIsAPermanentMiss(t *testing.T) {
	var c *Cache
	c.Put("a", "store:1", []byte("one"))
	c.Drop("store:1")
	if _, ok := c.Get("a"); ok {
		t.Fatal("a disabled cache answered")
	}
	if hits, misses, bytes, entries := c.Stats(); hits+misses != 0 || bytes+entries != 0 {
		t.Fatal("a disabled cache reported activity")
	}
}

// An answer larger than the whole budget is not stored, rather than emptying the cache to
// make room for it.
func TestOversizedAnswerIsNotStored(t *testing.T) {
	c := New(50, time.Minute)
	c.Put("keep", "store:1", []byte("small"))
	c.Put("huge", "store:2", []byte(strings.Repeat("x", 200)))
	if _, ok := c.Get("huge"); ok {
		t.Fatal("an answer bigger than the budget was stored")
	}
	if _, ok := c.Get("keep"); !ok {
		t.Fatal("storing an oversized answer evicted a good one")
	}
}
