// Package readcache holds answers to catalogue reads in this process, so that reading the
// catalogue does not wake the database.
//
// Why it exists, with the measurement that justifies it. The database suspends itself after
// five quiet minutes and is billed for the hours it stays awake. Over a full day it never
// got five quiet minutes: requests arrive every few seconds, and 69% of them are one shop's
// page being built -- its details and the shops near it. Blocking crawlers did not create a
// single gap either; measured over four daytime hours, removing every rule-ignoring agent
// and all datacentre address space still left a request every six seconds.
//
// So the traffic is not the lever. The lever is that these requests stop reaching Postgres.
// Replaying the same four hours with catalogue reads served from memory leaves 302 requests
// instead of 3,817, and three gaps worth 107 minutes -- 45% of the window asleep.
//
// What makes it affordable is a fact about this catalogue: of 11,252 shops, 11 have a review.
// A shop's page is otherwise the same bytes every time it is asked for, and the answer for
// every shop in the country fits in about thirty megabytes.
//
// Three rules keep it honest, and they are the whole design:
//
//   - Only anonymous reads. An answer that mentions the reader -- whether they saved this
//     shop, whether they reviewed it -- is never stored or served from here. The page is
//     already built that way: what differs per reader is fetched by the browser afterwards.
//   - A reader who is signed in never reads from it. Whoever notices staleness is whoever
//     wrote something, and they are signed in; the crawlers that fill it are not. This is
//     what lets the lifetime be long without anybody meeting a stale page.
//   - A write drops what it changed, here and now. Other instances of this process cannot
//     be told -- the usual way of telling them holds a connection open to the database,
//     which would defeat the entire point -- so they expire by time instead.
package readcache

import (
	"container/list"
	"sync"
	"time"
)

// Entry is a rendered answer, kept as the bytes that were sent.
type entry struct {
	key     string
	group   string
	body    []byte
	expires time.Time
	element *list.Element
}

// Cache is a byte-budgeted LRU with a lifetime on every entry.
//
// The budget is in bytes rather than entries because entries differ by twenty to one: a
// shop with twenty reviews answers in 18 KB and a shop with none in 880 bytes. Counting
// entries would either waste most of the budget or blow through it.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*entry
	groups  map[string]map[string]struct{}
	order   *list.List
	bytes   int
	budget  int
	ttl     time.Duration
	hits    int64
	misses  int64
}

// New returns a cache, or nil when it is switched off. A nil *Cache is usable: every method
// treats it as a permanent miss, so turning this off is a configuration change and not a
// code path.
func New(budgetBytes int, ttl time.Duration) *Cache {
	if budgetBytes <= 0 || ttl <= 0 {
		return nil
	}
	return &Cache{
		entries: make(map[string]*entry),
		groups:  make(map[string]map[string]struct{}),
		order:   list.New(),
		budget:  budgetBytes,
		ttl:     ttl,
	}
}

// Get returns a stored answer. The second value distinguishes a miss from an empty answer.
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	found, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, false
	}
	if time.Now().After(found.expires) {
		c.removeLocked(found)
		c.misses++
		return nil, false
	}
	c.order.MoveToFront(found.element)
	c.hits++
	return found.body, true
}

// Put stores an answer under a key, and under a group that a later write can drop whole.
// The group is the thing the answer is about -- a shop -- so that reviewing a shop drops
// its page and its neighbours' list together, whatever locale or limit they were asked in.
func (c *Cache) Put(key, group string, body []byte) {
	if c == nil || len(body) == 0 || len(body) > c.budget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		c.removeLocked(existing)
	}
	stored := &entry{key: key, group: group, body: body, expires: time.Now().Add(c.ttl)}
	stored.element = c.order.PushFront(stored)
	c.entries[key] = stored
	if group != "" {
		if c.groups[group] == nil {
			c.groups[group] = make(map[string]struct{}, 4)
		}
		c.groups[group][key] = struct{}{}
	}
	c.bytes += len(body) + len(key)
	for c.bytes > c.budget {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.removeLocked(oldest.Value.(*entry))
	}
}

// Drop removes everything stored about one thing. Called when that thing changes.
func (c *Cache) Drop(group string) {
	if c == nil || group == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.groups[group] {
		if found, ok := c.entries[key]; ok {
			c.removeLocked(found)
		}
	}
	delete(c.groups, group)
}

// Stats reports what it is doing, for the metrics endpoint. A cache nobody can see the hit
// rate of is a cache nobody can tell is working.
func (c *Cache) Stats() (hits, misses int64, bytes, entries int) {
	if c == nil {
		return 0, 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, c.bytes, len(c.entries)
}

func (c *Cache) removeLocked(found *entry) {
	c.order.Remove(found.element)
	delete(c.entries, found.key)
	if group, ok := c.groups[found.group]; ok {
		delete(group, found.key)
		if len(group) == 0 {
			delete(c.groups, found.group)
		}
	}
	c.bytes -= len(found.body) + len(found.key)
}
