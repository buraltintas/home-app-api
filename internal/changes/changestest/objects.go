// Package changestest is an object store in memory for tests of anything that tells or
// learns about catalogue changes. It keeps Cloud Storage's one promise the marker relies on
// -- a write conditional on a generation either lands whole or is refused -- and it can be
// told to fail, to show what a reader sees when Cloud Storage does not answer.
package changestest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/burakaltintas/home-app-api/internal/changes"
)

// ErrDown is what a store told to fail answers.
var ErrDown = errors.New("changestest: object store unavailable")

// Objects is a bucket in memory. The zero value is empty and working.
type Objects struct {
	mu         sync.Mutex
	bodies     map[string][]byte
	generation map[string]int64
	next       int64

	// Set to make every operation of that kind fail with ErrDown.
	FailReads, FailWrites atomic.Bool
	// How many of the next writes are refused as busy (changes.ErrBusy), the way Cloud
	// Storage answers a second write to one object within a second.
	Busy atomic.Int64
	// How many operations of each kind were asked for, failed ones included.
	Generations, Reads, Writes, Conflicts atomic.Int64
}

func (o *Objects) Generation(ctx context.Context, name string) (int64, error) {
	o.Generations.Add(1)
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if o.FailReads.Load() {
		return 0, ErrDown
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.generation[name], nil
}

func (o *Objects) Read(ctx context.Context, name string) ([]byte, int64, error) {
	o.Reads.Add(1)
	if e := ctx.Err(); e != nil {
		return nil, 0, e
	}
	if o.FailReads.Load() {
		return nil, 0, ErrDown
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.bodies[name]...), o.generation[name], nil
}

func (o *Objects) Write(ctx context.Context, name string, body []byte, generation int64) error {
	o.Writes.Add(1)
	if e := ctx.Err(); e != nil {
		return e
	}
	if o.FailWrites.Load() {
		return ErrDown
	}
	if o.busy() {
		return fmt.Errorf("%w: 429 rate limit exceeded", changes.ErrBusy)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.generation[name] != generation {
		o.Conflicts.Add(1)
		return changes.ErrConflict
	}
	if o.bodies == nil {
		o.bodies, o.generation = map[string][]byte{}, map[string]int64{}
	}
	o.next++
	o.bodies[name], o.generation[name] = append([]byte(nil), body...), o.next
	return nil
}

func (o *Objects) busy() bool {
	for {
		n := o.Busy.Load()
		if n <= 0 {
			return false
		}
		if o.Busy.CompareAndSwap(n, n-1) {
			return true
		}
	}
}

// Put replaces an object unconditionally, as somebody editing the bucket by hand would.
func (o *Objects) Put(name string, body []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.bodies == nil {
		o.bodies, o.generation = map[string][]byte{}, map[string]int64{}
	}
	o.next++
	o.bodies[name], o.generation[name] = append([]byte(nil), body...), o.next
}

// Body is an object's current contents.
func (o *Objects) Body(name string) []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.bodies[name]...)
}
