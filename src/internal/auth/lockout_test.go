package auth

import (
	"testing"
	"time"
)

func TestPenaltyForEscalatesPerFailure(t *testing.T) {
	l := &Lockout{policy: LockoutPolicy{
		Threshold:  5,
		Penalty:    15 * time.Minute,
		MaxPenalty: 2 * time.Hour,
	}}

	cases := []struct {
		failures int
		want     time.Duration
	}{
		{5, 15 * time.Minute},
		{6, 30 * time.Minute},
		{7, 60 * time.Minute},
		{8, 2 * time.Hour},
		{9, 2 * time.Hour},
		{10, 2 * time.Hour},
		{20, 2 * time.Hour},
	}

	for _, c := range cases {
		got := l.penaltyFor(c.failures)
		if got != c.want {
			t.Errorf("penaltyFor(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}
