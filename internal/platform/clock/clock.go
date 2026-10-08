// Package clock provides an injectable time source (ADR-011). Business code never calls time.Now
// directly; tests use Fake to control expiry, rate limits and token lifetimes.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time in UTC.
type Clock interface{ Now() time.Time }

type system struct{}

func (system) Now() time.Time { return time.Now().UTC() }

// System is the real clock.
var System Clock = system{}

// Fake is a manually advanced clock for tests. Safe for concurrent use.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake returns a fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

func (f *Fake) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }

// Advance moves the clock forward by d.
func (f *Fake) Advance(d time.Duration) { f.mu.Lock(); f.t = f.t.Add(d); f.mu.Unlock() }

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) { f.mu.Lock(); f.t = t.UTC(); f.mu.Unlock() }
