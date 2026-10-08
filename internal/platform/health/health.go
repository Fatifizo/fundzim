// Package health runs bounded dependency checks for readiness.
package health

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Check is one dependency check. Critical checks decide readiness; non-critical ones are reported only.
type Check struct {
	Name     string
	Critical bool
	Timeout  time.Duration
	Run      func(ctx context.Context) error
}

// Result is the outcome of one check. Err is for logs and the internal endpoint only; it is never
// returned to public clients.
type Result struct {
	Name     string        `json:"name"`
	Critical bool          `json:"critical"`
	OK       bool          `json:"ok"`
	Duration time.Duration `json:"-"`
	Err      error         `json:"-"`
}

// Report is the aggregate outcome.
type Report struct {
	Ready   bool
	Results []Result
}

// Checker runs a fixed set of checks.
type Checker struct {
	checks []Check

	// Optional result cache, so unauthenticated readiness probes cannot multiply dependency load.
	mu       sync.Mutex
	ttl      time.Duration
	now      func() time.Time
	cachedAt time.Time
	cached   *Report
}

// NewChecker returns a checker for checks.
func NewChecker(checks ...Check) *Checker { return &Checker{checks: checks, now: time.Now} }

// WithCache makes RunCached reuse a report for ttl. now is injectable for tests (nil = time.Now).
func (c *Checker) WithCache(ttl time.Duration, now func() time.Time) *Checker {
	c.ttl = ttl
	if now != nil {
		c.now = now
	}
	return c
}

// RunCached returns a report no older than the configured TTL, running the checks at most once per TTL
// (concurrent callers wait for the same run).
func (c *Checker) RunCached(ctx context.Context) Report {
	if c.ttl <= 0 {
		return c.Run(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && c.now().Sub(c.cachedAt) < c.ttl {
		return *c.cached
	}
	rep := c.Run(ctx)
	c.cached, c.cachedAt = &rep, c.now()
	return rep
}

// Run executes every check concurrently, each bounded by its own timeout and by ctx.
func (c *Checker) Run(ctx context.Context) Report {
	results := make([]Result, len(c.checks))
	var wg sync.WaitGroup
	for i, chk := range c.checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timeout := chk.Timeout
			if timeout <= 0 {
				timeout = 2 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			start := time.Now()
			err := runSafely(cctx, chk.Run)
			results[i] = Result{Name: chk.Name, Critical: chk.Critical, OK: err == nil, Duration: time.Since(start), Err: err}
		}()
	}
	wg.Wait()
	sort.Slice(results, func(a, b int) bool { return results[a].Name < results[b].Name })
	ready := true
	for _, r := range results {
		if r.Critical && !r.OK {
			ready = false
		}
	}
	return Report{Ready: ready, Results: results}
}

// runSafely returns ctx.Err() if the check does not finish in time, even if it ignores ctx.
func runSafely(ctx context.Context, f func(context.Context) error) error {
	done := make(chan error, 1)
	go func() { done <- f(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
