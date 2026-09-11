// Package catalog builds this product's own store catalogue out of what chains publish
// about themselves.
//
// The shape of the job is fixed and small: a Source hands back the rows a brand publishes,
// normalisation makes them comparable with what we already hold, and the matcher decides
// per row whether it is the same shop, a new one, or too close to call. Each of those is
// separately testable, and the decision for every single row is written down -- which is
// the difference between a catalogue you can trust and a pile that quietly grew twins.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
)

// BrandSpec is a chain as the registry describes it. It comes from the brands table; a
// Source is handed one rather than knowing its own identity, so the same adapter code can
// serve twenty chains with twenty different configurations.
type BrandSpec struct {
	ID              string
	Slug            string
	Name            string
	Website         string
	CategoryProfile []string
	Tier            int
	LocatorKind     string
	LocatorConfig   json.RawMessage
}

// Provider is the value stored in store_external_sources.provider for a brand's own list.
// Namespacing it keeps a chain's internal store number from ever colliding with another
// chain's, which is what makes that table's unique key a usable identity.
func (b BrandSpec) Provider() string { return "brand:" + b.Slug }

// RawStore is one row exactly as a brand published it, before we decide anything about it.
// Coordinates are optional because plenty of locators do not publish them; the matcher has
// a different rule for those rows rather than pretending it has a point.
type RawStore struct {
	ExternalID string
	Name       string
	Address    string
	City       string
	District   string
	Phone      string
	Website    string
	ImageURL   string
	Latitude   *float64
	Longitude  *float64
	// PointFrom records where the coordinate finally used came from, so a store standing
	// at its district's centre is distinguishable from one the brand actually placed.
	PointFrom string
	// Outside marks a branch the brand publishes abroad. Several Turkish chains do, and
	// their foreign cities sometimes carry names a Turkish district also has.
	Outside bool
	// Raw is kept whole so a later change of mind about a field costs a re-match rather
	// than another fetch of somebody else's website.
	Raw json.RawMessage
}

// Source is a brand's own published store list. Implementations do the fetching and the
// field mapping and nothing else: no database, no matching, no writing.
type Source interface {
	Brand() BrandSpec
	Fetch(ctx context.Context) ([]RawStore, error)
}

// Action is what the matcher decided for one published row.
type Action string

const (
	ActionInserted  Action = "inserted"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionReview    Action = "needs_review"
	ActionSkipped   Action = "skipped"
)

// Decision is one row's verdict, with the evidence that produced it. Every decision is
// persisted, including the ones that changed nothing, because "why is this shop listed
// twice" is a question that has to be answerable a month later.
type Decision struct {
	Raw        RawStore
	Action     Action
	Reason     string
	StoreID    string
	Similarity float64
	Distance   float64
}

// CompactName is the form two names are compared in: folded to plain letters and digits
// with the spaces taken out, so "ENGLISH HOME KADIKÖY AVM" and "English Home Kadıköy AVM"
// are one string and a trigram index can tell how close two shops' signs are.
func CompactName(name string) string {
	key := textnorm.Key(name)
	out := make([]rune, 0, len(key))
	for _, r := range key {
		if r != ' ' {
			out = append(out, r)
		}
	}
	return string(out)
}

// DerivedID stands in for the identifier a chain did not publish.
//
// It is built from the two things about a shop that do not drift between runs: its name,
// folded so that a change of case or of diacritics is not a change of shop, and its point
// rounded to about ten metres, which is finer than any of these locators is accurate and
// coarser than the noise they publish. A shop that moves across the street gets a new id
// and is matched on name and distance like any other row, which is the right outcome: it
// is, for our purposes, a different shop until somebody says otherwise.
func DerivedID(row RawStore) string {
	var point string
	if row.Latitude != nil && row.Longitude != nil {
		point = fmt.Sprintf("%.4f,%.4f", *row.Latitude, *row.Longitude)
	}
	sum := sha256.Sum256([]byte(textnorm.Key(row.Name) + "|" + point))
	return "derived:" + hex.EncodeToString(sum[:8])
}
