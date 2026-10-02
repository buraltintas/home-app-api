package readcache

import (
	"strings"
	"testing"
	"time"
)

func TestServesWhatItStored(t *testing.T) {
	c := New(1<<20, time.Minute)
	c.Put("a", "store:1", []byte("one"), c.Mark())
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
	c.Put("a", "store:1", []byte("one"), c.Mark())
	time.Sleep(3 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("an expired answer was served")
	}
}

// A review changes a shop's page and the count on it. Both are dropped, whatever locale or
// limit they were asked in, because they are about the same shop.
func TestDropTakesEverythingAboutOneShop(t *testing.T) {
	c := New(1<<20, time.Minute)
	c.Put("detail:tr", "store:1", []byte("tr"), c.Mark())
	c.Put("detail:en", "store:1", []byte("en"), c.Mark())
	c.Put("nearby:6", "store:1", []byte("near"), c.Mark())
	c.Put("other", "store:2", []byte("other"), c.Mark())
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

// When which shops changed is not known, everything goes, and the cache fills again as
// before: the budget it counts against starts from nothing.
func TestClearTakesEverything(t *testing.T) {
	c := New(1<<20, time.Minute)
	c.Put("detail:tr", "store:1", []byte("tr"), c.Mark())
	c.Put("highlights", "home:highlights", []byte("h"), c.Mark())
	c.Clear()
	for _, key := range []string{"detail:tr", "highlights"} {
		if _, ok := c.Get(key); ok {
			t.Fatalf("%s survived the clear", key)
		}
	}
	c.Put("again", "store:1", []byte("again"), c.Mark())
	if _, _, bytes, entries := c.Stats(); entries != 1 || bytes != len("again")+len("again") {
		t.Fatalf("after a clear the cache holds %d entries, %d bytes", entries, bytes)
	}
	c.Drop("store:1")
	if _, ok := c.Get("again"); ok {
		t.Fatal("an answer stored after a clear could not be dropped by its group")
	}
	var off *Cache
	off.Clear()
}

// The budget is in bytes because entries differ by twenty to one.
func TestBudgetEvictsOldestFirst(t *testing.T) {
	c := New(120, time.Minute)
	big := strings.Repeat("x", 50)
	c.Put("first", "store:1", []byte(big), c.Mark())
	c.Put("second", "store:2", []byte(big), c.Mark())
	if _, ok := c.Get("first"); !ok {
		t.Fatal("the first entry should still fit")
	}
	// Touching "first" above makes "second" the oldest.
	c.Put("third", "store:3", []byte(big), c.Mark())
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
	c.Put("a", "store:1", []byte("one"), c.Mark())
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
	c.Put("keep", "store:1", []byte("small"), c.Mark())
	c.Put("huge", "store:2", []byte(strings.Repeat("x", 200)), c.Mark())
	if _, ok := c.Get("huge"); ok {
		t.Fatal("an answer bigger than the budget was stored")
	}
	if _, ok := c.Get("keep"); !ok {
		t.Fatal("storing an oversized answer evicted a good one")
	}
}

// An answer whose build began before a drop may have been read before the write that caused
// it, and is not stored; one begun after it is.
func TestAnAnswerBuiltBeforeADropIsNotStored(t *testing.T) {
	c := New(1<<20, time.Minute)
	for name, drop := range map[string]func(){
		"drop":      func() { c.Drop("store:9") },
		"drop keys": func() { c.DropKeys("nearby|") },
		"clear":     c.Clear,
	} {
		c.Drop("store:1")
		mark := c.Mark()
		drop()
		c.Put("detail", "store:1", []byte("old"), mark)
		if _, ok := c.Get("detail"); ok {
			t.Fatalf("an answer built before a %s was stored", name)
		}
		c.Put("detail", "store:1", []byte("new"), c.Mark())
		if got, ok := c.Get("detail"); !ok || string(got) != "new" {
			t.Fatalf("an answer built after a %s was not stored", name)
		}
	}
}

// A neighbours' list is held under its own shop and shows others, so it goes when any shop
// changes; other kinds of answer stay.
func TestDropKeysTakesOneKindOfAnswer(t *testing.T) {
	c := New(1<<20, time.Minute)
	c.Put("nearby|a|tr|6", "store:1", []byte("a"), c.Mark())
	c.Put("nearby|b|en|6", "store:2", []byte("b"), c.Mark())
	c.Put("store|a|tr", "store:1", []byte("page"), c.Mark())
	c.DropKeys("nearby|")
	for _, key := range []string{"nearby|a|tr|6", "nearby|b|en|6"} {
		if _, ok := c.Get(key); ok {
			t.Fatalf("%s survived", key)
		}
	}
	if _, ok := c.Get("store|a|tr"); !ok {
		t.Fatal("a shop's page went with the neighbour lists")
	}
	if _, _, bytes, entries := c.Stats(); entries != 1 || bytes != len("page")+len("store|a|tr") {
		t.Fatalf("after dropping the lists the cache counts %d entries, %d bytes", entries, bytes)
	}
	c.Drop("store:1")
	if _, _, _, entries := c.Stats(); entries != 0 {
		t.Fatal("the group lost track of what was left in it")
	}
	var off *Cache
	off.DropKeys("nearby|")
}
