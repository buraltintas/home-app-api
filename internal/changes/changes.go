// Package changes tells every instance of the API what any one of them has just written to
// the catalogue, without asking the database.
//
// Each instance answers anonymous catalogue reads from memory: the whole catalogue
// (store.Catalog) and the pages it leaves to the database (readcache). A write drops what it
// made out of date at once -- in the process it landed in. Cloud Run sometimes runs two, and
// the other one went on answering from the copy the write had made wrong until that copy's
// maximum age: six hours, and the web then holds what it rendered for up to a day. The
// owner's requirement is plain: if there is a write, the current data must show; if there is
// not, memory is fine.
//
// The usual ways of telling the other instances -- LISTEN/NOTIFY, a version row asked about
// on a timer -- hold a connection open to the database or ask it every few seconds, and
// either keeps it from ever suspending, which is the reason the copies exist. So the news
// goes through one small object in Cloud Storage instead.
//
// Every write that drops something here also appends one entry to that object -- which
// shops, and whether every list moved -- with a numbered sequence, a timestamp and the
// writing instance. The append is a conditional write on the object's generation, retried
// when somebody else wrote in between, so two writes at once cannot lose either entry, and
// retried after a pause when Cloud Storage says it is busy -- it takes about one write a
// second to one object. It runs inside the write's own request, because Cloud Run throttles
// the CPU once the response is sent, and it can never fail the write: news that cannot be
// written is kept and written with the next write, the next look, or as the process stops.
//
// Every instance looks at the object's generation at most once per interval, inside an
// anonymous catalogue read: one metadata read, never the database. Only when the generation
// has moved does it read the entries, and it drops exactly what they name -- or everything,
// when it cannot tell what it missed. When Cloud Storage does not answer, the look is skipped
// and the copies fall back on their maximum age, as they did before this existed.
package changes

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/google/uuid"
)

// Objects is where the marker is kept. The production one is a Cloud Storage bucket (GCS);
// the tests use one in memory.
type Objects interface {
	// Generation is the object's current generation, zero when it does not exist. One
	// metadata read: a Class B operation.
	Generation(ctx context.Context, name string) (int64, error)
	// Read is the object's contents and the generation they belong to, or nil and zero when
	// it does not exist.
	Read(ctx context.Context, name string) ([]byte, int64, error)
	// Write replaces the object only while its generation is still the one given -- zero
	// meaning it must not exist yet -- and answers ErrConflict when it has moved.
	Write(ctx context.Context, name string, body []byte, generation int64) error
}

// ErrConflict says the object was written by somebody else between the read and the write.
var ErrConflict = errors.New("changes: the marker was written by somebody else in between")

// ErrBusy says the store did not take the write this time and asks for it to be tried again
// after a pause: Cloud Storage answers 429 to a second write to one object within about a
// second, and 408 or 5xx when it could not finish one. Wrapped around the store's own error.
var ErrBusy = errors.New("changes: the store asked for the write to be tried again later")

// Change is what one write made out of date.
type Change struct {
	// The shops whose own page changed.
	Stores []uuid.UUID `json:"stores,omitempty"`
	// Something any catalogue list may show changed -- a review count, a new shop, a merge,
	// a category -- so the whole copy of the catalogue is out of date, not only these pages.
	Full bool `json:"full,omitempty"`
	// A review changed, and the home page's highlights are counted from reviews.
	Reviews bool `json:"reviews,omitempty"`
	// Which shops' pages changed is not known: an account deleted with its reviews, or news
	// this instance missed. Every page held goes, and the whole catalogue with them.
	AllPages bool `json:"all_pages,omitempty"`
}

// everything is what an instance assumes when it cannot tell what it missed.
func everything() *Change { return &Change{Full: true, Reviews: true, AllPages: true} }

// A change naming more shops than this is told as every page: the entry stays small, and
// dropping every held page costs only the reads that fill them again.
const maxStores = 50

func (c *Change) merge(other Change) {
	c.Full = c.Full || other.Full
	c.Reviews = c.Reviews || other.Reviews
	c.AllPages = c.AllPages || other.AllPages
	for _, id := range other.Stores {
		if !slices.Contains(c.Stores, id) {
			c.Stores = append(c.Stores, id)
		}
	}
	if c.AllPages || len(c.Stores) > maxStores {
		c.AllPages, c.Stores = true, nil
	}
}

type entry struct {
	Seq int64     `json:"seq"`
	At  time.Time `json:"at"`
	By  string    `json:"by"`
	Change
}

type marker struct {
	// A name drawn when the object is started -- the first write, or the first after it was
	// found broken -- so an instance can tell a marker started again from the one it was
	// following, whatever the numbers in it say.
	Run string `json:"run"`
	// The sequence number of the latest entry, which is never dropped. Entries older than
	// the last keepEntries are.
	Seq     int64   `json:"seq"`
	Entries []entry `json:"changes"`
}

const (
	// The most entries the object keeps. At the measured rate of catalogue writes -- about
	// thirty a week -- that is a fortnight; an instance would have to miss this many in one
	// interval to lose track, and then it drops everything, which is safe.
	keepEntries = 64
	// How long a look may take. It runs inside somebody's page request, once per interval.
	lookTimeout = time.Second
	// How long telling the others may add to a write. A read and a conditional write are
	// two round trips; the rest is room for one write having to be retried.
	writeTimeout = 2 * time.Second
	// The least a write waits before trying again after the store said it was busy; it waits
	// up to twice this, at random. Cloud Storage takes about one write a second to one
	// object, so a shorter pause would only be refused again.
	busyWait = 500 * time.Millisecond
	// Clocks on two instances agree to far better than this. An entry stamped by another
	// instance this long before this one started is already in what this one read at
	// startup; one stamped after might not be.
	clockSkew = 10 * time.Second
	// Far more than keepEntries can take, so a corrupted object cannot fill the memory.
	maxObjectBytes = 1 << 20
)

// ObjectName is where an environment's marker is kept in its bucket. The environment is in
// the name so a development process pointed at the same bucket can neither drop production's
// copies nor be told about production's writes.
func ObjectName(environment string) string {
	return "_cache/" + environment + "/catalog-changes.json"
}

// Marker tells the other instances about writes made here, and learns about theirs. A nil
// Marker is a working configuration: nothing is told and nothing is learned, and the copies
// fall back on their maximum age, as before this existed.
type Marker struct {
	objects Objects
	name    string
	every   time.Duration
	log     *slog.Logger
	now     func() time.Time
	self    string
	started time.Time

	// The earliest the next look may be taken, in Unix nanoseconds. Whoever moves it on
	// takes the look; everybody else carries on without waiting.
	next    atomic.Int64
	looking atomic.Bool
	// What this instance has already seen. Touched only by the one look under way.
	seen int64
	run  string
	seq  int64

	mu sync.Mutex
	// News written here that could not be told yet.
	pending *Change
}

// New returns a Marker over the object called name, looked at no more than once per every.
func New(objects Objects, name string, every time.Duration, log *slog.Logger) *Marker {
	if log == nil {
		log = slog.Default()
	}
	m := &Marker{objects: objects, name: name, every: every, log: log, now: time.Now, self: instanceID(), seq: -1}
	m.started = m.now()
	return m
}

func instanceID() string {
	b := make([]byte, 8)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

// Publish tells the other instances what a write made here has just made out of date. It is
// called after the write has committed, inside its request, and waits for the object to be
// written: at most writeTimeout, usually two short round trips. It never fails the write.
// News that could not be written is kept and written with the next write or the next look;
// until then the other instances fall back on their copies' maximum age.
func (m *Marker) Publish(c Change) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.pending != nil {
		c.merge(*m.pending)
		m.pending = nil
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if e := m.write(ctx, c); e != nil {
		m.keep(c)
		observability.CatalogChange("publish", "failure")
		m.log.Warn("catalog change could not be told to the other instances; it is kept and told with the next write or look, and until then they show it by their copy's maximum age", "error", e)
		return
	}
	observability.CatalogChange("publish", "success")
}

func (m *Marker) keep(c Change) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = &c
		return
	}
	m.pending.merge(c)
}

// write appends c to the object, reading what is there and writing it back only if nobody
// has written in between; when somebody has, it reads theirs and tries again. When the store
// says it is busy -- writes to one object faster than it takes them, or a request it could not
// finish -- it waits half a second to a second and tries again, for as long as ctx leaves room.
// A request that failed after the store had in fact taken it is read back on the next try and
// appended to again: the entry is then there twice, which drops the same pages twice and
// nothing more.
func (m *Marker) write(ctx context.Context, c Change) error {
	for attempt := 0; ; attempt++ {
		body, generation, e := m.objects.Read(ctx, m.name)
		if e != nil {
			return e
		}
		var held marker
		if len(body) == 0 || json.Unmarshal(body, &held) != nil || held.Run == "" {
			// Not there yet, or unreadable: started, under a new name. Every instance that was
			// following the old one takes that as news it cannot follow: everything.
			held = marker{Run: instanceID()}
		}
		held.Seq++
		held.Entries = append(held.Entries, entry{Seq: held.Seq, At: m.now().UTC(), By: m.self, Change: c})
		if len(held.Entries) > keepEntries {
			held.Entries = held.Entries[len(held.Entries)-keepEntries:]
		}
		encoded, e := json.Marshal(held)
		if e != nil {
			return e
		}
		e = m.objects.Write(ctx, m.name, encoded, generation)
		var wait time.Duration
		switch {
		case errors.Is(e, ErrConflict):
			// Somebody else wrote it in between. Waiting a moment, longer each time and never
			// in step with them, lets one of the two finish before the other reads again.
			wait = time.Duration(5+rand.IntN(20)*(attempt+1)) * time.Millisecond
		case errors.Is(e, ErrBusy):
			wait = busyWait + rand.N(busyWait)
		default:
			return e
		}
		// A pause that would outlast the time left only turns the store's answer into a
		// timeout; the news is kept either way.
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Check learns what the other instances have written since this one last looked and hands
// it to forget, which drops what it made out of date. It looks at most once per interval;
// every other call, and every call while a look is under way, returns at once.
//
// It never touches the database. A look is a metadata read of one object, and a second read
// only when its generation has moved. When the object cannot be read the look is skipped --
// logged once, because the next one is an interval away -- and the copies here fall back on
// their maximum age.
func (m *Marker) Check(ctx context.Context, forget func(Change)) {
	if m == nil {
		return
	}
	now := m.now()
	due := m.next.Load()
	if now.UnixNano() < due || !m.next.CompareAndSwap(due, now.Add(m.every).UnixNano()) {
		return
	}
	if !m.looking.CompareAndSwap(false, true) {
		return
	}
	defer m.looking.Store(false)
	// The reader who happened to arrive at the interval does not decide whether it is
	// finished: a crawler hanging up must not leave the look half done.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookTimeout)
	defer cancel()
	news, e := m.look(ctx)
	// The look first, because it is what this reader's answer depends on; news an earlier
	// write here could not tell goes out in whatever time is left, so a busy store cannot
	// spend the look's.
	_ = m.flush(ctx)
	if e != nil {
		observability.CatalogChange("look", "failure")
		m.log.Warn("catalog change marker could not be read; writes on other instances reach this one by its copy's maximum age", "error", e)
		return
	}
	if news == nil {
		observability.CatalogChange("look", "unchanged")
		return
	}
	observability.CatalogChange("look", "changed")
	forget(*news)
}

// Flush writes news that an earlier write here could not, and waits for it: at most until
// ctx is done, and never longer than a write may. Called when the process is told to stop --
// Cloud Run gives a stopping instance its CPU for a few seconds -- so news that a failed write
// left behind is not lost with an instance that would otherwise have had no later write or
// look to tell it with. An error means it is still not told; the other instances then show it
// by their copies' maximum age.
func (m *Marker) Flush(ctx context.Context) error {
	if m == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return m.flush(ctx)
}

// flush writes news that an earlier write here could not.
func (m *Marker) flush(ctx context.Context) error {
	m.mu.Lock()
	pending := m.pending
	m.pending = nil
	m.mu.Unlock()
	if pending == nil {
		return nil
	}
	if e := m.write(ctx, *pending); e != nil {
		m.keep(*pending)
		observability.CatalogChange("publish", "failure")
		return e
	}
	observability.CatalogChange("publish", "success")
	return nil
}

// look reads the object if it has moved and returns what it says changed since the last
// look, leaving out what this instance wrote itself; nil when nothing did.
func (m *Marker) look(ctx context.Context) (*Change, error) {
	generation, e := m.objects.Generation(ctx, m.name)
	if e != nil {
		return nil, e
	}
	if generation == m.seen {
		return nil, nil
	}
	body, generation, e := m.objects.Read(ctx, m.name)
	if e != nil {
		return nil, e
	}
	if generation == 0 {
		// Removed by hand. Whatever was in it is unknown from here.
		news := m.seq > 0
		m.seen, m.seq = 0, 0
		if news {
			return everything(), nil
		}
		return nil, nil
	}
	var held marker
	if e := json.Unmarshal(body, &held); e != nil {
		m.seen = generation
		m.log.Warn("catalog change marker is unreadable; everything held here is dropped", "error", e)
		return everything(), nil
	}
	news := m.since(held)
	m.seen, m.run, m.seq = generation, held.Run, held.Seq
	return news, nil
}

// since is what held says changed after this instance's last look -- or, on the first look,
// after this instance started, since everything before that is already in what it read at
// startup. When it cannot tell, because entries it never saw have been dropped from the
// object or the object was started again, the answer is everything.
func (m *Marker) since(held marker) *Change {
	var news Change
	found := false
	take := func(x entry) {
		if x.By != m.self {
			news.merge(x.Change)
			found = true
		}
	}
	if m.seq >= 0 {
		switch {
		case held.Run != m.run && m.seq > 0:
			return everything()
		case held.Seq == m.seq:
			return nil
		case held.Seq < m.seq, len(held.Entries) == 0, held.Entries[0].Seq > m.seq+1:
			return everything()
		}
		for _, x := range held.Entries {
			if x.Seq > m.seq {
				take(x)
			}
		}
	} else {
		from := m.started.Add(-clockSkew)
		for i, x := range held.Entries {
			if x.At.Before(from) {
				continue
			}
			if i == 0 && x.Seq > 1 {
				// The entries dropped before this one may have been written after this
				// instance started too.
				return everything()
			}
			take(x)
		}
	}
	if !found {
		return nil
	}
	return &news
}
