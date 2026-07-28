package ratelimit

import (
	"math"
	"sync"
	"time"
)

const (
	DefaultBurst    = 30
	DefaultInterval = 2 * time.Second
	DefaultIdleTTL  = time.Hour
)

type Limit struct {
	Burst    int
	Interval time.Duration
}

func DefaultLimit() Limit {
	return Limit{Burst: DefaultBurst, Interval: DefaultInterval}
}

func (l Limit) normalize() Limit {
	if l.Burst < 1 {
		l.Burst = DefaultBurst
	}
	if l.Interval <= 0 {
		l.Interval = DefaultInterval
	}
	return l
}

type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
	Reset      time.Duration
}

func (d Decision) RetryAfterSeconds() int {
	return ceilSeconds(d.RetryAfter)
}

func (d Decision) ResetSeconds() int {
	return ceilSeconds(d.Reset)
}

func ceilSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}

type bucket struct {
	tokens float64
	seenAt time.Time
}

type Limiter struct {
	limit Limit
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

func New(l Limit, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: l.normalize(), now: now, buckets: map[string]*bucket{}}
}

func (l *Limiter) Limit() Limit { return l.limit }

func (l *Limiter) Allow(key string) Decision {
	return l.AllowN(key, 1)
}

func (l *Limiter) AllowN(key string, n float64) Decision {
	burst := float64(l.limit.Burst)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: burst, seenAt: now}
		l.buckets[key] = b
	}
	l.refill(b, now, burst)
	b.seenAt = now

	if b.tokens < n {
		missing := n - b.tokens
		return Decision{
			Allowed:    false,
			Limit:      l.limit.Burst,
			Remaining:  int(b.tokens),
			RetryAfter: time.Duration(missing * float64(l.limit.Interval)),
			Reset:      time.Duration((burst - b.tokens) * float64(l.limit.Interval)),
		}
	}

	b.tokens -= n
	return Decision{
		Allowed:   true,
		Limit:     l.limit.Burst,
		Remaining: int(b.tokens),
		Reset:     time.Duration((burst - b.tokens) * float64(l.limit.Interval)),
	}
}

func (l *Limiter) refill(b *bucket, now time.Time, burst float64) {
	elapsed := now.Sub(b.seenAt)
	if elapsed <= 0 {
		return
	}
	b.tokens += float64(elapsed) / float64(l.limit.Interval)
	if b.tokens > burst {
		b.tokens = burst
	}
}

func (l *Limiter) Forget(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

func (l *Limiter) Reap(idleFor time.Duration) int {
	if idleFor <= 0 {
		idleFor = DefaultIdleTTL
	}
	cutoff := l.now().Add(-idleFor)

	l.mu.Lock()
	defer l.mu.Unlock()

	dropped := 0
	for k, b := range l.buckets {
		if b.seenAt.Before(cutoff) {
			delete(l.buckets, k)
			dropped++
		}
	}
	return dropped
}

func (l *Limiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
