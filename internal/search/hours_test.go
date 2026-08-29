package search

import (
	"testing"
	"time"
)

// Turkey runs three hours ahead, and the question is always about the shop's clock rather
// than the reader's: someone in Berlin asking at 08:00 their time is asking about 09:00 in
// Antalya. The awkward case is a shop that closes after midnight, where the closing time
// belongs to the following day and a naive comparison says it was never open at all.
func TestOpeningHoursOpenAt(t *testing.T) {
	const istanbul = 180
	shop := &OpeningHours{UTCOffsetMinutes: istanbul, Periods: []OpeningPeriod{
		{OpenDay: 1, OpenMinute: 9 * 60, CloseDay: 1, CloseMinute: 19 * 60}, // Monday 09:00-19:00
		{OpenDay: 6, OpenMinute: 20 * 60, CloseDay: 0, CloseMinute: 2 * 60}, // Saturday 20:00 - Sunday 02:00
	}}

	// Times are UTC; add three hours to read them as the shop reads them.
	cases := []struct {
		name string
		utc  time.Time
		want bool
	}{
		{"Monday mid-morning", time.Date(2026, 8, 31, 7, 0, 0, 0, time.UTC), true}, // 10:00 local
		{"Monday just before opening", time.Date(2026, 8, 31, 5, 59, 0, 0, time.UTC), false},
		{"Monday at closing time", time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC), false}, // 19:00 local
		{"Tuesday, no period at all", time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC), false},
		{"Saturday night", time.Date(2026, 9, 5, 19, 0, 0, 0, time.UTC), true}, // 22:00 local
		{"Sunday 01:00, still the Saturday shift", time.Date(2026, 9, 5, 22, 0, 0, 0, time.UTC), true},
		{"Sunday 03:00, shut", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		got := shop.OpenAt(c.utc)
		if got == nil {
			t.Fatalf("%s: got no answer", c.name)
		}
		if *got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, *got, c.want)
		}
	}

	// No hours at all is not the same as closed, and must never be shown as closed.
	if (&OpeningHours{}).OpenAt(time.Now()) != nil {
		t.Error("a store with no published hours was given an answer")
	}
	var missing *OpeningHours
	if missing.OpenAt(time.Now()) != nil {
		t.Error("nil hours were given an answer")
	}
}
