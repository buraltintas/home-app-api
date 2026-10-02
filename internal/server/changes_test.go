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
