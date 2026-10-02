package server

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/changes"
	"github.com/burakaltintas/home-app-api/internal/changes/changestest"
	"github.com/burakaltintas/home-app-api/internal/readcache"
	"github.com/google/uuid"
)

// twoInstances is two API processes with a read cache each and one bucket between them. The
// markers look on every read, so a test decides when a look happens by when it reads.
func twoInstances(objects *changestest.Objects) (a, b *Server) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, b = &Server{}, &Server{}
	for _, s := range []*Server{a, b} {
		s.SetReadCache(readcache.New(1<<20, time.Hour))
		s.SetChanges(changes.New(objects, changes.ObjectName("test"), time.Nanosecond, quiet))
	}
	return a, b
}

// read is an anonymous read of a held answer, built fresh when it is not held.
func read(s *Server, key, group string) string {
	rec := httptest.NewRecorder()
	s.answer(rec, httptest.NewRequest("GET", "/v1/stores/x", nil), key, func() (any, string, error) {
		return map[string]string{"built": "now"}, group, nil
	})
	return rec.Header().Get("X-Cache")
}

// The read cache holds the pages of shops with reviews, which the catalogue copy leaves to
// the database, and highlights for the home page. A write on one instance drops what it
// changed on the other at its next look: the shop's page, and the highlights for a review.
func TestAWriteOnOneInstanceDropsWhatTheOtherHolds(t *testing.T) {
	objects := &changestest.Objects{}
	a, b := twoInstances(objects)
	shop, other := uuid.New(), uuid.New()
	for _, held := range [][2]string{{"store|shop|tr", storeGroup(shop)}, {"store|other|tr", storeGroup(other)}, {"highlights", highlightsGroup}} {
		read(b, held[0], held[1])
		if got := read(b, held[0], held[1]); got != "hit" {
			t.Fatalf("%s was not held to begin with: %q", held[0], got)
		}
	}
	a.reviewsChanged(shop)
	if got := read(b, "store|shop|tr", storeGroup(shop)); got != "miss" {
		t.Fatalf("the reviewed shop's page outlived the review on the other instance: %q", got)
	}
	if got := read(b, "highlights", highlightsGroup); got != "miss" {
		t.Fatalf("the highlights outlived the review on the other instance: %q", got)
	}
	if got := read(b, "store|other|tr", storeGroup(other)); got != "hit" {
		t.Fatalf("another shop's page was dropped by the review: %q", got)
	}

	// An account deleted with its reviews says no shop, so every page held goes.
	a.reviewsChanged()
	if got := read(b, "store|other|tr", storeGroup(other)); got != "miss" {
		t.Fatalf("a page outlived a deleted account's reviews on the other instance: %q", got)
	}
	if got := read(a, "store|other|tr", storeGroup(other)); got != "miss" {
		t.Fatalf("a page outlived a deleted account's reviews where it was deleted: %q", got)
	}
}

// Telling the other instances can fail; the write it follows has already committed, and
// this instance drops what it changed whatever happens to the telling.
func TestAMarkerThatCannotBeWrittenDoesNotStopTheWrite(t *testing.T) {
	objects := &changestest.Objects{}
	a, b := twoInstances(objects)
	shop := uuid.New()
	read(a, "store|shop|tr", storeGroup(shop))
	read(b, "store|shop|tr", storeGroup(shop))
	objects.FailWrites.Store(true)
	start := time.Now()
	a.storePageChanged(shop)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the write waited %s on a marker it could not write", elapsed)
	}
	if got := read(a, "store|shop|tr", storeGroup(shop)); got != "miss" {
		t.Fatalf("the write was not visible where it was made: %q", got)
	}
	if got := read(b, "store|shop|tr", storeGroup(shop)); got != "hit" {
		t.Fatalf("news that was never written arrived: %q", got)
	}
	// Told with this instance's next look, once the bucket can be written again.
	objects.FailWrites.Store(false)
	read(a, "store|shop|tr", storeGroup(shop))
	if got := read(b, "store|shop|tr", storeGroup(shop)); got != "miss" {
		t.Fatalf("the change that could not be told at first was never told: %q", got)
	}
}

// A list of a shop's neighbours is held under that shop and shows others: their review
// counts, ratings, names and photos. So a review of any shop, or an administrator's edit,
// drops every such list held, here and on the other instance, and the highlights with them;
// a favourite, which no list shows, drops only its own shop's answers.
func TestAChangeToOneShopDropsEveryNeighbourList(t *testing.T) {
	objects := &changestest.Objects{}
	a, b := twoInstances(objects)
	reviewed, neighbour := uuid.New(), uuid.New()
	nearby := nearbyKeys + "neighbour|tr|6"
	hold := func() {
		for _, s := range []*Server{a, b} {
			read(s, nearby, storeGroup(neighbour))
			read(s, "highlights", highlightsGroup)
			read(s, "store|neighbour|tr", storeGroup(neighbour))
		}
	}
	for _, change := range []struct {
		name  string
		write func()
	}{
		{"a review", func() { a.reviewsChanged(reviewed) }},
		{"an administrator's edit", func() { a.catalogueChanged(reviewed) }},
	} {
		hold()
		change.write()
		for _, s := range []*Server{a, b} {
			if got := read(s, nearby, storeGroup(neighbour)); got != "miss" {
				t.Fatalf("after %s a neighbour list held under another shop was served: %q", change.name, got)
			}
			if got := read(s, "highlights", highlightsGroup); got != "miss" {
				t.Fatalf("after %s the highlights were served: %q", change.name, got)
			}
			if got := read(s, "store|neighbour|tr", storeGroup(neighbour)); got != "hit" {
				t.Fatalf("after %s another shop's own page was dropped: %q", change.name, got)
			}
		}
	}
	hold()
	a.storePageChanged(reviewed)
	for _, s := range []*Server{a, b} {
		if got := read(s, nearby, storeGroup(neighbour)); got != "hit" {
			t.Fatalf("a favourite dropped a neighbour list: %q", got)
		}
	}
}

// An answer whose database read began before a write, and that is stored after the write's
// news has been dropped here, would be served for the whole lifetime as if the write had
// never happened. It is not stored.
func TestAnAnswerReadBeforeAWriteIsNotStoredAfterItsNews(t *testing.T) {
	objects := &changestest.Objects{}
	a, b := twoInstances(objects)
	shop := uuid.New()
	key := "store|shop|tr"
	rec := httptest.NewRecorder()
	b.answer(rec, httptest.NewRequest("GET", "/v1/stores/x", nil), key, func() (any, string, error) {
		// Read from the database before the review committed on the other instance; the
		// review is written, and this instance looks at the marker while the answer is
		// still on its way.
		a.reviewsChanged(shop)
		read(b, "store|other|tr", storeGroup(uuid.New()))
		return map[string]string{"built": "before the review"}, storeGroup(shop), nil
	})
	if got := read(b, key, storeGroup(shop)); got != "miss" {
		t.Fatalf("an answer read before the review was stored after it: %q", got)
	}
	if got := read(b, key, storeGroup(shop)); got != "hit" {
		t.Fatalf("an answer read after the review was not stored: %q", got)
	}
}
