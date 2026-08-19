package user

import "testing"

// The thresholds are a product decision (1/5/15/40/100), so the boundaries are pinned
// rather than left to be re-derived from the constant.
func TestLevelBoundaries(t *testing.T) {
	for reviews, want := range map[int]int{0: 0, 1: 1, 4: 1, 5: 2, 14: 2, 15: 3, 39: 3, 40: 4, 99: 4, 100: 5, 5000: 5} {
		if got := Level(reviews); got != want {
			t.Fatalf("Level(%d)=%d want %d", reviews, got, want)
		}
	}
}
