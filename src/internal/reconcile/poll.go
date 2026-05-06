package reconcile

import (
	"context"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
)

const (
	DefaultPollWorkers    = 8
	DefaultDeviceTimeout  = 4 * time.Second
	DefaultSlowPollEvery  = 6
	DefaultProxyTimeout   = 5 * time.Second
	DefaultNetcfgTimeout  = 5 * time.Second
	FirstSweepIsColdCache = 0
)

type PollDeps struct {
	Net           netcfg.Manager
	FW            fw.Firewall
	Proxy         proxysup.Supervisor
	Dev           device.Registry
	Clock         Clock
	Backoff       Backoff
	Workers       int
	SlowPollEvery int
	DeviceTimeout time.Duration
	Rand          func() float64
}

type Poller struct {
	deps PollDeps

	mu      sync.RWMutex
	cache   ObservedState
	backoff map[domain.Slot]*backoffState
}

func NewPoller(d PollDeps) *Poller {
	if d.Clock == nil {
		d.Clock = domain.SystemClock()
	}
	if d.Workers <= 0 {
		d.Workers = DefaultPollWorkers
	}
	if d.SlowPollEvery <= 0 {
		d.SlowPollEvery = DefaultSlowPollEvery
	}
	if d.DeviceTimeout <= 0 {
		d.DeviceTimeout = DefaultDeviceTimeout
	}
	if d.Rand == nil {
		d.Rand = func() float64 { return 0.5 }
	}
	if d.Backoff == (Backoff{}) {
		d.Backoff = DefaultBackoff()
	}
	return &Poller{
		deps:    d,
		cache:   newObservedState(),
		backoff: map[domain.Slot]*backoffState{},
	}
}

func (p *Poller) Snapshot() ObservedState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cache.Clone()
}

func (p *Poller) SweepsCompleted() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cache.SweepsCompleted
}

func (p *Poller) CacheWarm() bool {
	return p.SweepsCompleted() > FirstSweepIsColdCache
}

func (p *Poller) Sweep(ctx context.Context, rows []domain.SlotRow) ObservedState {
	prev := p.Snapshot()
	next := newObservedState()
	next.SweepsCompleted = prev.SweepsCompleted
	next.LastSweepAt = prev.LastSweepAt
	next.Net = prev.Net
	next.NftTablePresent = prev.NftTablePresent

	if p.deps.Net != nil {
		obsCtx, cancel := context.WithTimeout(ctx, DefaultNetcfgTimeout)
		obs, err := p.deps.Net.Observe(obsCtx)
		cancel()
		if err == nil {
			next.Net = obs
		}
	}
	if p.deps.FW != nil {
		fwCtx, cancel := context.WithTimeout(ctx, DefaultNetcfgTimeout)
		next.NftTablePresent = p.deps.FW.Verify(fwCtx) == nil
		cancel()
	}
	if next.Net.Links == nil {
		next.Net.Links = map[string]netcfg.LinkState{}
	}
	if next.Net.Routes == nil {
		next.Net.Routes = map[int][]netcfg.RouteState{}
	}

	slow := (prev.SweepsCompleted % p.deps.SlowPollEvery) == 0

	type result struct {
		slot   domain.Slot
		iface  string
		fenced bool
		known  bool
		status proxysup.Status
		device DeviceObservation
		seen   bool
	}

	results := make([]result, len(rows))
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.deps.Workers)

	for i, row := range rows {
		if !row.Slot.Valid() {
			continue
		}
		wg.Add(1)
		go func(i int, row domain.SlotRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = result{
				slot:  row.Slot,
				iface: row.IfName,
			}
			results[i].fenced, results[i].known = p.pollFenced(ctx, row)
			results[i].status = p.pollProxy(ctx, row, prev)
			results[i].device, results[i].seen = p.pollDevice(ctx, row, prev, slow)
		}(i, row)
	}
	wg.Wait()

	for _, r := range results {
		if r.slot == 0 {
			continue
		}
		if r.known {
			next.Fenced[r.iface] = r.fenced
		}
		next.ProxyStatus[r.slot] = r.status
		if r.seen {
			next.Devices[r.slot] = r.device
		}
	}

	now := p.deps.Clock.Now()
	if ctx.Err() == nil {
		next.SweepsCompleted = prev.SweepsCompleted + 1
		next.LastSweepAt = now
	}

	p.mu.Lock()
	p.cache = next
	p.mu.Unlock()
	return next.Clone()
}

func (p *Poller) pollFenced(ctx context.Context, row domain.SlotRow) (bool, bool) {
	if p.deps.FW == nil {
		return false, false
	}
	c, cancel := context.WithTimeout(ctx, DefaultNetcfgTimeout)
	defer cancel()
	fenced, err := p.deps.FW.IsFenced(c, row.IfName)
	if err != nil {
		return false, false
	}
	return fenced, true
}

func (p *Poller) pollProxy(ctx context.Context, row domain.SlotRow, prev ObservedState) proxysup.Status {
	if p.deps.Proxy == nil {
		return prev.ProxyStatus[row.Slot]
	}
	c, cancel := context.WithTimeout(ctx, DefaultProxyTimeout)
	defer cancel()
	st, err := p.deps.Proxy.Status(c, row.Slot)
	if err != nil {
		return proxysup.Status{Unit: row.Slot.ProxyUnit(), Error: err.Error()}
	}
	if st.Unit == "" {
		st.Unit = row.Slot.ProxyUnit()
	}
	return st
}

func (p *Poller) pollDevice(ctx context.Context, row domain.SlotRow, prev ObservedState, slow bool) (DeviceObservation, bool) {
	if p.deps.Dev == nil || !row.Occupied() {
		return DeviceObservation{}, false
	}

	last, hadLast := prev.Devices[row.Slot]
	now := p.deps.Clock.Now()

	state := p.backoffFor(row.Slot)
	if !state.ready(now) {
		return last, hadLast
	}

	c, cancel := context.WithTimeout(ctx, p.deps.DeviceTimeout)
	defer cancel()

	obs, err := p.observeDevice(c, row, last, slow)
	if err != nil {
		p.recordFailure(row.Slot, now)
		out := last
		out.Reachable = false
		out.Err = err.Error()
		return out, true
	}
	p.recordSuccess(row.Slot)
	return obs, true
}

func (p *Poller) observeDevice(ctx context.Context, row domain.SlotRow, last DeviceObservation, slow bool) (DeviceObservation, error) {
	dev, err := p.deps.Dev.ForSlot(ctx, row.Slot)
	if err != nil {
		return DeviceObservation{}, err
	}

	out := last
	out.Err = ""

	status, err := dev.Status(ctx)
	if err != nil {
		return DeviceObservation{}, err
	}
	out.Reachable = true
	out.ObservedAt = p.deps.Clock.Now()
	out.Conn = status.ConnectionStatus
	if status.WanIP.IsValid() {
		out.WanIP = status.WanIP
	}

	if idle, err := dev.GetMaxIdleTime(ctx); err == nil {
		out.MaxIdleTime = idle
	}

	if !slow {
		return out, nil
	}

	if sig, err := dev.Signal(ctx); err == nil {
		out.Signal = sig
	}
	if traffic, err := dev.Traffic(ctx); err == nil {
		out.Traffic = traffic
	}
	if sim, err := dev.PinStatus(ctx); err == nil {
		out.Sim = sim
	}
	if needed, err := dev.LoginRequired(ctx); err == nil {
		out.LoginNeeded = needed
	}
	if mode, err := dev.NetMode(ctx); err == nil {
		out.NetMode = mode
	}
	return out, nil
}

func (p *Poller) backoffFor(s domain.Slot) *backoffState {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.backoff[s]
	if !ok {
		st = &backoffState{}
		p.backoff[s] = st
	}
	return st
}

func (p *Poller) recordFailure(s domain.Slot, now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.backoff[s]
	if !ok {
		st = &backoffState{}
		p.backoff[s] = st
	}
	return st.fail(p.deps.Backoff, now, p.deps.Rand())
}

func (p *Poller) recordSuccess(s domain.Slot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.backoff[s]; ok {
		st.succeed()
	}
}

func (p *Poller) BackoffFailures(s domain.Slot) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if st, ok := p.backoff[s]; ok {
		return st.failures
	}
	return 0
}
