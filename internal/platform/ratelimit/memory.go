package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
)

// DefaultMaxKeys bounds the in-process limiter's memory.
const DefaultMaxKeys = 100_000

// Memory is an in-process GCRA limiter with the same semantics as the Valkey script. On its own it
// limits per replica; Distributed uses it as the fallback when Valkey is unavailable.
type Memory struct {
	mu      sync.Mutex
	clk     clock.Clock
	tats    map[string]int64 // policy\x00hash → theoretical arrival time (ns since epoch)
	maxKeys int
}

// NewMemory returns an in-process limiter. clk nil means clock.System; maxKeys <= 0 means DefaultMaxKeys.
func NewMemory(clk clock.Clock, maxKeys int) *Memory {
	if clk == nil {
		clk = clock.System
	}
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}
	return &Memory{clk: clk, tats: map[string]int64{}, maxKeys: maxKeys}
}

// Allow implements Limiter. Decisions from Memory are never Degraded on their own.
func (m *Memory) Allow(_ context.Context, p Policy, key string) (Decision, error) {
	if err := p.Validate(); err != nil {
		return Decision{}, err
	}
	interval, burst := params(p, time.Nanosecond)
	k := p.Name + "\x00" + hashKey(key)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clk.Now().UnixNano()
	stored, ok := m.tats[k]
	if !ok {
		if len(m.tats) >= m.maxKeys {
			m.evict(now)
		}
		stored = now
	}
	allowed, tat, remaining, retry := gcra(now, stored, interval, burst)
	if allowed {
		m.tats[k] = tat
	}
	return Decision{Allowed: allowed, Remaining: remaining, RetryAfter: time.Duration(retry)}, nil
}

// evict drops fully decayed keys (TAT in the past = full capacity, so dropping them changes nothing).
// If the table is still full (a flood of distinct keys), it drops an arbitrary tenth: bounded memory
// wins over perfect accounting, and the fallback is only used while Valkey is down.
func (m *Memory) evict(now int64) {
	for k, t := range m.tats {
		if t <= now {
			delete(m.tats, k)
		}
	}
	if len(m.tats) < m.maxKeys {
		return
	}
	n := m.maxKeys / 10
	for k := range m.tats {
		if n <= 0 {
			break
		}
		delete(m.tats, k)
		n--
	}
}

// Len reports the number of tracked keys (tests and diagnostics).
func (m *Memory) Len() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.tats) }
