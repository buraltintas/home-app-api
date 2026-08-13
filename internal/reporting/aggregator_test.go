package reporting

import (
	"testing"
	"time"
)

func TestIstanbulDayBoundsUseConfiguredTimezone(t *testing.T) {
	loc, e := time.LoadLocation("Europe/Istanbul")
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{location: loc}
	day, start, end := s.dayBounds(time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC))
	if day.Format("2006-01-02") != "2026-08-13" {
		t.Fatalf("day=%s", day)
	}
	want := time.Date(2026, 8, 12, 21, 0, 0, 0, time.UTC)
	if !start.Equal(want) || end.Sub(start) != 24*time.Hour {
		t.Fatalf("bounds %s %s", start, end)
	}
}
func TestIstanbulHistoricalDSTBoundary(t *testing.T) {
	loc, e := time.LoadLocation("Europe/Istanbul")
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{location: loc}
	_, start, end := s.dayBounds(time.Date(2015, 3, 29, 12, 0, 0, 0, time.UTC))
	if end.Sub(start) != 23*time.Hour {
		t.Fatalf("expected 23-hour DST day, got %s (%s..%s)", end.Sub(start), start, end)
	}
}
func TestSnapshotDeltasBalanceReversibleActions(t *testing.T) {
	pairs := [][2]string{{FavoriteCreated, FavoriteRemoved}, {LikeCreated, LikeRemoved}, {FollowCreated, FollowRemoved}, {CommentCreated, CommentDeleted}}
	for _, p := range pairs {
		a, b := snapshotDelta(p[0]), snapshotDelta(p[1])
		if a == nil || b == nil {
			t.Fatalf("missing delta for %v", p)
		}
		for i := range a {
			if a[i].(int)+b[i].(int) != 0 {
				t.Fatalf("unbalanced %v at %d", p, i)
			}
		}
	}
}
func TestLifetimePostDelta(t *testing.T) {
	created := snapshotDelta(PostCreated)
	deleted := snapshotDelta(PostDeleted)
	if created[3].(int) != 1 || created[4].(int) != 1 || deleted[3].(int) != -1 || deleted[5].(int) != 1 {
		t.Fatalf("unexpected post deltas: %v %v", created, deleted)
	}
}
