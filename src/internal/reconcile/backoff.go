package reconcile

import (
	"math"
	"time"
)

const (
	DefaultBackoffMin    = 2 * time.Second
	DefaultBackoffMax    = 5 * time.Minute
	DefaultBackoffFactor = 2.0
	DefaultBackoffJitter = 0.3
	MaxBackoffExponent   = 20
)

type Backoff struct {
	Min    time.Duration
	Max    time.Duration
	Factor float64
	Jitter float64
}

func DefaultBackoff() Backoff {
	return Backoff{
		Min:    DefaultBackoffMin,
		Max:    DefaultBackoffMax,
		Factor: DefaultBackoffFactor,
		Jitter: DefaultBackoffJitter,
	}
}

func (b Backoff) normalised() Backoff {
	if b.Min <= 0 {
		b.Min = DefaultBackoffMin
	}
	if b.Max < b.Min {
		b.Max = b.Min
	}
	if b.Factor < 1 {
		b.Factor = DefaultBackoffFactor
	}
	if b.Jitter < 0 {
		b.Jitter = 0
	}
	if b.Jitter > 1 {
		b.Jitter = 1
	}
	return b
}

func (b Backoff) Delay(failures int, rnd float64) time.Duration {
	b = b.normalised()
	if failures <= 0 {
		return 0
	}
	exp := failures - 1
	if exp > MaxBackoffExponent {
		exp = MaxBackoffExponent
	}
	d := float64(b.Min) * math.Pow(b.Factor, float64(exp))
	if d > float64(b.Max) || math.IsInf(d, 0) {
		d = float64(b.Max)
	}
	if b.Jitter > 0 {
		if rnd < 0 {
			rnd = 0
		}
		if rnd > 1 {
			rnd = 1
		}
		d *= 1 - b.Jitter + 2*b.Jitter*rnd
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

type backoffState struct {
	failures int
	nextAt   time.Time
}

func (s *backoffState) ready(now time.Time) bool {
	return s.failures == 0 || !now.Before(s.nextAt)
}

func (s *backoffState) succeed() {
	s.failures = 0
	s.nextAt = time.Time{}
}

func (s *backoffState) fail(b Backoff, now time.Time, rnd float64) time.Duration {
	s.failures++
	d := b.Delay(s.failures, rnd)
	s.nextAt = now.Add(d)
	return d
}
