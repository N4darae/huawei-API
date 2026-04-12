package reconcile

import (
	"testing"
	"time"
)

func TestBackoffGrowsExponentiallyAndCaps(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second, Factor: 2, Jitter: 0}

	want := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second}
	for failures, w := range want {
		if got := b.Delay(failures, 0.5); got != w {
			t.Errorf("Delay(%d) = %s, want %s", failures, got, w)
		}
	}
}

func TestBackoffNeverOverflows(t *testing.T) {
	b := Backoff{Min: time.Second, Max: time.Minute, Factor: 2, Jitter: 0}
	for _, failures := range []int{50, 1000, 1 << 20} {
		if got := b.Delay(failures, 0.5); got != time.Minute {
			t.Errorf("Delay(%d) = %s, want the cap", failures, got)
		}
	}
}

func nearly(t *testing.T, what string, got, want time.Duration) {
	t.Helper()
	if d := got - want; d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("%s is %s, want %s", what, got, want)
	}
}

func TestBackoffJitterStaysInBand(t *testing.T) {
	b := Backoff{Min: 10 * time.Second, Max: time.Minute, Factor: 2, Jitter: 0.3}

	lo := b.Delay(1, 0)
	mid := b.Delay(1, 0.5)
	hi := b.Delay(1, 1)

	nearly(t, "lowest jittered delay", lo, 7*time.Second)
	nearly(t, "mid jittered delay", mid, 10*time.Second)
	nearly(t, "highest jittered delay", hi, 13*time.Second)
	if lo >= mid || mid >= hi {
		t.Error("jitter must be monotonic in its random input so tests can pin it")
	}
}

func TestBackoffClampsOutOfRangeRandomness(t *testing.T) {
	b := Backoff{Min: 10 * time.Second, Max: time.Minute, Factor: 2, Jitter: 0.3}
	nearly(t, "Delay with rnd -5", b.Delay(1, -5), 7*time.Second)
	nearly(t, "Delay with rnd 5", b.Delay(1, 5), 13*time.Second)
}

func TestEngineBackoffCarriesJitter(t *testing.T) {
	f := newFarm(1)
	e := newEngineRig(t, f).engine
	if e.poller.deps.Backoff.Jitter <= 0 {
		t.Fatal("the engine wired a poller with no jitter; 48 dead dongles would retry in lockstep")
	}
	if e.poller.deps.Backoff.Factor < 1 {
		t.Fatal("the engine wired a poller with no exponential factor")
	}
}

func TestBackoffZeroValueIsUsable(t *testing.T) {
	var b Backoff
	if got := b.Delay(1, 0.5); got != DefaultBackoffMin {
		t.Fatalf("the zero Backoff produced %s for the first failure, want %s", got, DefaultBackoffMin)
	}
	if got := b.Delay(30, 0.5); got != DefaultBackoffMax {
		t.Fatalf("the zero Backoff produced %s at the cap, want %s", got, DefaultBackoffMax)
	}
}

func TestBackoffStateGatesRetries(t *testing.T) {
	b := Backoff{Min: 10 * time.Second, Max: time.Minute, Factor: 2, Jitter: 0}
	now := baseTime
	st := &backoffState{}

	if !st.ready(now) {
		t.Fatal("a fresh state must be ready")
	}
	st.fail(b, now, 0.5)
	if st.ready(now.Add(9 * time.Second)) {
		t.Fatal("state is ready before its delay elapsed")
	}
	if !st.ready(now.Add(10 * time.Second)) {
		t.Fatal("state is not ready after its delay elapsed")
	}

	st.fail(b, now.Add(10*time.Second), 0.5)
	if st.failures != 2 {
		t.Fatalf("failures is %d after two failures", st.failures)
	}
	if st.ready(now.Add(29 * time.Second)) {
		t.Fatal("the second delay must be longer than the first")
	}

	st.succeed()
	if !st.ready(now) || st.failures != 0 {
		t.Fatal("a success must clear the backoff")
	}
}
