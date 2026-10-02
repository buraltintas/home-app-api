package changes

import "time"

// SetClock gives a test the marker's clock. The start of the process is read from it too, so
// it is set before anything is published or looked at.
func (m *Marker) SetClock(now func() time.Time) {
	m.now = now
	m.started = now()
}

// Self is the name this instance writes its entries under.
func (m *Marker) Self() string { return m.self }
