package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/changes"
	. "github.com/burakaltintas/home-app-api/internal/httpapi"
	"github.com/burakaltintas/home-app-api/internal/observability"
	storepkg "github.com/burakaltintas/home-app-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SetCatalog hands the server the catalogue held in memory. Nil is a working configuration:
// every read goes to the database, which is what happened before it existed. In shadow mode
// the copy is consulted and compared with the database's answer, but never served.
func (s *Server) SetCatalog(c *storepkg.Catalog, shadow bool) {
	s.catalog, s.catalogShadow = c, shadow
}

// SetChanges hands the server the marker through which the instances tell each other about
// their writes. Nil is a working configuration: a write here is still visible here at once,
// and reaches the other instances by their copies' maximum age, as before it existed.
func (s *Server) SetChanges(m *changes.Marker) { s.changes = m }

func ignoreComparison(any, error) {}

// fromCatalog answers an anonymous catalogue read from the copy held in memory when it can,
// and reports whether it did.
//
// The same two conditions as the read cache decide who may be answered from here
// (cacheableRead): nobody signed in, and no coordinates. Then the copy answers only what it
// is sure of; anything else falls through to the database, as before.
//
// When it did not answer, the second value is for the database path to hand its own answer
// to. Outside shadow mode it does nothing. In shadow mode it compares the two and says so in
// the log, which is how the copy is checked against the database on real traffic before it
// is trusted to answer.
func (s *Server) fromCatalog(w http.ResponseWriter, r *http.Request, answer func(*storepkg.Snapshot) (any, bool)) (bool, func(any, error)) {
	if !cacheableRead(r) {
		return false, ignoreComparison
	}
	s.catchUp(r)
	if s.catalog == nil {
		return false, ignoreComparison
	}
	snap := s.catalog.Current(r.Context())
	if snap == nil {
		observability.CatalogRead("fallthrough")
		return false, ignoreComparison
	}
	value, ok := answer(snap)
	if !ok {
		observability.CatalogRead("fallthrough")
		return false, ignoreComparison
	}
	if s.catalogShadow {
		return false, func(fromDB any, e error) { compareCatalog(r, snap, value, fromDB, e) }
	}
	body, e := json.Marshal(value)
	if e != nil {
		return false, ignoreComparison
	}
	observability.CatalogRead("served")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Cache", "snapshot")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
	return true, ignoreComparison
}

func compareCatalog(r *http.Request, snap *storepkg.Snapshot, fromSnapshot, fromDB any, dbErr error) {
	where := ""
	if dbErr != nil {
		where = "the database answered an error"
		var app *Error
		if errors.As(dbErr, &app) {
			where += ": " + app.Code
		}
	} else if same, difference := sameCatalogAnswer(chi.RouteContext(r.Context()).RoutePattern(), fromSnapshot, fromDB); same {
		observability.CatalogRead("shadow_match")
		return
	} else {
		where = difference
	}
	observability.CatalogRead("shadow_mismatch")
	// The address is a public page -- a store's slug, a city -- never a person. A difference
	// in a shop changed after the copy was read is staleness, not a fault: the snapshot age
	// says which.
	slog.Warn("catalog snapshot differs from the database", "route", chi.RouteContext(r.Context()).RoutePattern(),
		"path", r.URL.RequestURI(), "difference", where, "snapshot_age_s", int(time.Since(snap.LoadedAt()).Seconds()))
}

// sameCatalogAnswer reports whether two answers would reach a client as the same thing, and
// where they first differ when they would not.
//
// Three allowances, each for something the database itself does not pin down. Distances:
// PostGIS and the copy measure the ellipsoid with different series and agree to a tenth of
// a micrometre, so within one micrometre is the same distance. A shop's category slugs are
// aggregated with no ORDER BY, so they are compared as a set. And two shops that tie on
// everything a list is ordered by -- the same distance from a district centre, the same name
// in the same district -- may come in either order, because the database breaks that tie by
// whatever order it read them in.
func sameCatalogAnswer(route string, a, b any) (bool, string) {
	left, e1 := json.Marshal(a)
	right, e2 := json.Marshal(b)
	if e1 != nil || e2 != nil {
		return false, "unencodable"
	}
	if bytes.Equal(left, right) {
		return true, ""
	}
	var x, y any
	if json.Unmarshal(left, &x) != nil || json.Unmarshal(right, &y) != nil {
		return false, "undecodable"
	}
	where := firstDifference(x, y, "", listOrder[route])
	return where == "", where
}

// listOrder names what each list is ordered by, which is what two shops must share to be a
// tie the database may break either way.
var listOrder = map[string][]string{
	"/v1/stores/{id}/nearby":     {"distance_meters"},
	"/v1/discovery/stores":       {"review_count", "average_rating", "name"},
	"/v1/discovery/brand-stores": {"district", "name"},
}

func firstDifference(a, b any, path string, order []string) string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return path
		}
		keys := make([]string, 0, len(x)+len(y))
		for k := range x {
			keys = append(keys, k)
		}
		for k := range y {
			if _, seen := x[k]; !seen {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			if d := firstDifference(x[k], y[k], path+"."+k, order); d != "" {
				return d
			}
		}
		return ""
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return path + " (length)"
		}
		if strings.HasSuffix(path, ".store.categories") {
			x, y = sortedStrings(x), sortedStrings(y)
		}
		for i := range x {
			if path == ".items" && tied(x[i], y[i], order) {
				continue
			}
			if d := firstDifference(x[i], y[i], fmt.Sprintf("%s[%d]", path, i), order); d != "" {
				return d
			}
		}
		return ""
	case float64:
		y, ok := b.(float64)
		if ok && (x == y || strings.HasSuffix(path, ".distance_meters") && math.Abs(x-y) <= 1e-6) {
			return ""
		}
		return path
	default:
		if reflect.DeepEqual(a, b) {
			return ""
		}
		return path
	}
}

// tied reports whether two different shops share every value their list is ordered by.
func tied(a, b any, order []string) bool {
	x, ok1 := a.(map[string]any)
	y, ok2 := b.(map[string]any)
	if !ok1 || !ok2 || len(order) == 0 || reflect.DeepEqual(x["id"], y["id"]) {
		return false
	}
	for _, key := range order {
		if firstDifference(x[key], y[key], "."+key, nil) != "" {
			return false
		}
	}
	return true
}

func sortedStrings(values []any) []any {
	out := append([]any(nil), values...)
	sort.Slice(out, func(a, b int) bool { return fmt.Sprint(out[a]) < fmt.Sprint(out[b]) })
	return out
}

// highlightsGroup is what the home page's highlights are held under, so a change to any
// review can drop them.
const highlightsGroup = "home:highlights"

// storePageChanged is called after this process changes one shop's own page and nothing a
// list or the sitemap index shows: a favourite, a comment's moderation. Both copies of that
// page held here go, the rest stay.
func (s *Server) storePageChanged(id uuid.UUID) {
	s.changed(changes.Change{Stores: []uuid.UUID{id}})
}

// catalogueChanged is called after this process changes something any catalogue list may
// show -- a new shop, a merge, categories, a cover photo, a flag that moves the shop's date
// in the sitemap index. Until the catalogue has been read again every catalogue read here
// goes to the database, so whoever made the change sees it on the next page they open.
func (s *Server) catalogueChanged(stores ...uuid.UUID) {
	s.changed(changes.Change{Stores: stores, Full: true})
}

// reviewsChanged is catalogueChanged for a review: its shop's counts move up or down every
// list it is in, and the home page's highlights are counted from reviews. Called with no shop
// -- an account deleted with all its reviews -- it cannot say which pages carried them, so
// every page held goes.
func (s *Server) reviewsChanged(stores ...uuid.UUID) {
	s.changed(changes.Change{Stores: stores, Full: true, Reviews: true, AllPages: len(stores) == 0})
}

// changed drops here what a write in this process made out of date, then tells the other
// instances, which drop the same on their next look (see internal/changes). Called after the
// write has committed and inside its request: the telling waits for Cloud Storage, briefly,
// because the CPU is throttled once the response is sent, and it never fails the write.
func (s *Server) changed(c changes.Change) {
	s.forget(c)
	s.changes.Publish(c)
}

// forget drops what c made out of date from everything this process holds. It is the same
// whether the write landed here or on another instance, so a reader cannot tell which.
func (s *Server) forget(c changes.Change) {
	if c.AllPages {
		s.reads.Clear()
	}
	for _, id := range c.Stores {
		s.reads.Drop(storeGroup(id))
	}
	if c.Reviews {
		s.reads.Drop(highlightsGroup)
	}
	if c.Full || c.AllPages {
		s.catalog.Invalidate()
		return
	}
	for _, id := range c.Stores {
		s.catalog.InvalidateStore(id)
	}
}

// catchUp learns what the other instances have written since this one last looked, and
// drops it here, before an anonymous catalogue read is answered from memory. It looks at most
// once per CATALOG_CHANGES_INTERVAL, inside the request, at Cloud Storage and never at the
// database; every other call returns at once.
func (s *Server) catchUp(r *http.Request) {
	s.changes.Check(r.Context(), s.forget)
}
