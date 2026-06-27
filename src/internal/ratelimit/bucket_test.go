package ratelimit

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestBurstIsSpentThenRefusedWithARetryAfter(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 3, Interval: time.Second}, c.Now)

	for i := 1; i <= 3; i++ {
		d := l.Allow("key-1")
		if !d.Allowed {
			t.Fatalf("request %d of the burst was refused", i)
		}
		if want := 3 - i; d.Remaining != want {
			t.Fatalf("request %d left %d tokens, want %d", i, d.Remaining, want)
		}
	}

	d := l.Allow("key-1")
	if d.Allowed {
		t.Fatal("the fourth request must be refused")
	}
	if d.RetryAfterSeconds() != 1 {
		t.Fatalf("retry after is %ds, want 1s", d.RetryAfterSeconds())
	}
	if d.Limit != 3 {
		t.Fatalf("limit is %d, want 3", d.Limit)
	}
}

func TestTokensComeBackAtTheRefillInterval(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 2, Interval: time.Second}, c.Now)

	l.Allow("key-1")
	l.Allow("key-1")
	if l.Allow("key-1").Allowed {
		t.Fatal("bucket should be empty")
	}

	c.Advance(time.Second)
	if !l.Allow("key-1").Allowed {
		t.Fatal("one token should have been refilled after one interval")
	}
	if l.Allow("key-1").Allowed {
		t.Fatal("only one token should have come back")
	}
}

func TestRefillIsCappedAtTheBurst(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 2, Interval: time.Second}, c.Now)

	l.Allow("key-1")
	c.Advance(time.Hour)

	if d := l.Allow("key-1"); d.Remaining != 1 {
		t.Fatalf("remaining is %d, want 1: the bucket must not overflow the burst", d.Remaining)
	}
}

func TestBucketsAreIndependentPerKey(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 1, Interval: time.Minute}, c.Now)

	if !l.Allow("key-1").Allowed {
		t.Fatal("first key must pass")
	}
	if l.Allow("key-1").Allowed {
		t.Fatal("first key must now be empty")
	}
	if !l.Allow("key-2").Allowed {
		t.Fatal("a different key must have its own bucket")
	}
}

func TestResetCountsDownToAFullBucket(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 4, Interval: 2 * time.Second}, c.Now)

	l.Allow("key-1")
	l.Allow("key-1")
	d := l.Allow("key-1")
	if d.ResetSeconds() != 6 {
		t.Fatalf("reset is %ds, want 6s to refill the three spent tokens", d.ResetSeconds())
	}
}

func TestZeroValuesFallBackToTheDefaults(t *testing.T) {
	l := New(Limit{}, nil)
	if l.Limit() != DefaultLimit() {
		t.Fatalf("limit is %+v, want the default %+v", l.Limit(), DefaultLimit())
	}
}

func TestReapDropsIdleBuckets(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 1, Interval: time.Second}, c.Now)

	l.Allow("old")
	c.Advance(2 * time.Hour)
	l.Allow("fresh")

	if dropped := l.Reap(time.Hour); dropped != 1 {
		t.Fatalf("reaped %d buckets, want 1", dropped)
	}
	if l.Tracked() != 1 {
		t.Fatalf("%d buckets survived, want 1", l.Tracked())
	}
}

func TestForgetResetsAKey(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 1, Interval: time.Hour}, c.Now)

	l.Allow("key-1")
	l.Forget("key-1")
	if !l.Allow("key-1").Allowed {
		t.Fatal("a forgotten key must start from a full bucket")
	}
}

func TestConcurrentCallersNeverExceedTheBurst(t *testing.T) {
	c := newClock()
	l := New(Limit{Burst: 50, Interval: time.Hour}, c.Now)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("key-1").Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != 50 {
		t.Fatalf("%d requests passed, want exactly the burst of 50", allowed)
	}
}
