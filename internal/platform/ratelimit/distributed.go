package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
)

// gcraScript is the atomic GCRA step. Time comes from the Valkey server (TIME), so every API replica
// shares one clock. Units are microseconds.
//
//	KEYS[1] = rl:<policy>:<hash>   ARGV[1] = emission interval (µs)   ARGV[2] = burst = interval*limit (µs)
//	returns {allowed (0|1), remaining, retry_after_µs}
var gcraScript = redis.NewScript(`
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local tat = tonumber(redis.call('GET', KEYS[1]) or now) or now
if tat < now then tat = now end
local new_tat = tat + interval
local allow_at = new_tat - burst
if now < allow_at then
  return {0, 0, allow_at - now}
end
local ttl_ms = math.ceil((new_tat - now) / 1000)
if ttl_ms < 1 then ttl_ms = 1 end
redis.call('SET', KEYS[1], string.format('%d', new_tat), 'PX', ttl_ms)
return {1, math.floor((now - allow_at) / interval), 0}
`)

// Options configure a Distributed limiter. Zero values pick the defaults.
type Options struct {
	Clock         clock.Clock   // fallback limiter and circuit-breaker clock; default clock.System
	Metrics       *Metrics      // optional
	Logger        *slog.Logger  // optional; logs Valkey down/up transitions (never keys)
	Timeout       time.Duration // per Valkey call; default 250ms
	RetryInterval time.Duration // how long to stay on the fallback after a Valkey failure; default 2s
	MaxLocalKeys  int           // fallback size bound; default DefaultMaxKeys
}

// Distributed is the Valkey-backed limiter with an in-process fallback. Safe for concurrent use.
type Distributed struct {
	s        redis.Scripter
	fallback *Memory
	o        Options

	downUntil atomic.Int64 // unix ns; 0 = Valkey considered healthy
	probing   atomic.Bool
	mu        sync.Mutex
	down      bool
}

// New returns a limiter on s (cache.Client.Scripter()). A nil s (Valkey not configured) makes every
// decision on the fallback, marked Degraded.
func New(s redis.Scripter, o Options) *Distributed {
	if o.Clock == nil {
		o.Clock = clock.System
	}
	if o.Timeout <= 0 {
		o.Timeout = 250 * time.Millisecond
	}
	if o.RetryInterval <= 0 {
		o.RetryInterval = 2 * time.Second
	}
	return &Distributed{s: s, fallback: NewMemory(o.Clock, o.MaxLocalKeys), o: o}
}

// Allow implements Limiter.
func (l *Distributed) Allow(ctx context.Context, p Policy, key string) (Decision, error) {
	if err := p.Validate(); err != nil {
		return Decision{}, err
	}
	d, err := l.allowValkey(ctx, p, key)
	if err != nil {
		// Never allow-all: protective and non-protective policies both use the local fallback.
		d, err = l.fallback.Allow(ctx, p, key)
		if err != nil {
			return Decision{}, err
		}
		d.Degraded = true
		l.o.Metrics.degradedInc(p.Name)
	}
	l.o.Metrics.decision(p.Name, d.Allowed)
	return d, nil
}

var errBreakerOpen = errors.New("ratelimit: valkey marked unavailable")

func (l *Distributed) allowValkey(ctx context.Context, p Policy, key string) (Decision, error) {
	if l.s == nil {
		return Decision{}, errBreakerOpen
	}
	if until := l.downUntil.Load(); until != 0 {
		// Circuit open: until it expires, go straight to the fallback; afterwards one caller probes.
		if l.o.Clock.Now().UnixNano() < until || !l.probing.CompareAndSwap(false, true) {
			return Decision{}, errBreakerOpen
		}
		defer l.probing.Store(false)
	}
	interval, burst := params(p, time.Microsecond)
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.o.Timeout)
	defer cancel()
	res, err := gcraScript.Run(cctx, l.s, []string{StorageKey(p.Name, key)}, interval, burst).Int64Slice()
	if err == nil && len(res) != 3 {
		err = fmt.Errorf("ratelimit: unexpected script result length %d", len(res))
	}
	if err != nil {
		l.markDown(err)
		return Decision{}, err
	}
	l.markUp()
	return Decision{Allowed: res[0] == 1, Remaining: int(res[1]), RetryAfter: time.Duration(res[2]) * time.Microsecond}, nil
}

func (l *Distributed) markDown(err error) {
	l.downUntil.Store(l.o.Clock.Now().Add(l.o.RetryInterval).UnixNano())
	l.mu.Lock()
	was := l.down
	l.down = true
	l.mu.Unlock()
	if !was && l.o.Logger != nil {
		l.o.Logger.Warn("rate limiter: valkey unavailable, using in-process fallback",
			slog.String("error_category", "dependency"), slog.String("error", err.Error()))
	}
}

func (l *Distributed) markUp() {
	if l.downUntil.Load() == 0 {
		return
	}
	l.downUntil.Store(0)
	l.mu.Lock()
	was := l.down
	l.down = false
	l.mu.Unlock()
	if was && l.o.Logger != nil {
		l.o.Logger.Info("rate limiter: valkey available again")
	}
}
