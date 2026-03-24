package sim

import (
	"math/rand"
	"sync"
	"time"
)

const DefaultSlowResponse = 3 * time.Second

type FaultProfile struct {
	Timeout      bool
	Err100002    bool
	Err112001    bool
	TokenExpire  bool
	SlowResponse bool
	Reenumerate  bool
}

func (p FaultProfile) any() bool {
	return p.Timeout || p.Err100002 || p.Err112001 || p.TokenExpire || p.SlowResponse || p.Reenumerate
}

func (p FaultProfile) kinds() []Fault {
	out := make([]Fault, 0, 6)
	if p.Timeout {
		out = append(out, FaultTimeout)
	}
	if p.Err100002 {
		out = append(out, FaultErr100002)
	}
	if p.Err112001 {
		out = append(out, FaultErr112001)
	}
	if p.TokenExpire {
		out = append(out, FaultTokenExpire)
	}
	if p.SlowResponse {
		out = append(out, FaultSlowResponse)
	}
	if p.Reenumerate {
		out = append(out, FaultReenumerate)
	}
	return out
}

type Fault int

const (
	FaultNone Fault = iota
	FaultTimeout
	FaultErr100002
	FaultErr112001
	FaultTokenExpire
	FaultSlowResponse
	FaultReenumerate
)

type faultInjector struct {
	mu      sync.Mutex
	rng     *rand.Rand
	rate    float64
	profile FaultProfile
	forced  []Fault
	slow    time.Duration
}

func newFaultInjector(seed int64, rate float64, profile FaultProfile, slow time.Duration) *faultInjector {
	if slow <= 0 {
		slow = DefaultSlowResponse
	}
	return &faultInjector{
		rng:     rand.New(rand.NewSource(seed)),
		rate:    rate,
		profile: profile,
		slow:    slow,
	}
}

func (f *faultInjector) Force(kinds ...Fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forced = append(f.forced, kinds...)
}

func (f *faultInjector) SetRate(rate float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rate = rate
}

func (f *faultInjector) SetProfile(p FaultProfile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profile = p
}

func (f *faultInjector) next() Fault {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.forced) > 0 {
		k := f.forced[0]
		f.forced = f.forced[1:]
		return k
	}
	if f.rate <= 0 || !f.profile.any() {
		return FaultNone
	}
	if f.rng.Float64() >= f.rate {
		return FaultNone
	}
	kinds := f.profile.kinds()
	return kinds[f.rng.Intn(len(kinds))]
}

func (f *faultInjector) slowFor() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.slow
}
