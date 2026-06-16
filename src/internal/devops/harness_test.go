package devops

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/hilink"
	"github.com/n4darae/huawei-API/src/internal/device/sim"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/secrets"
	"github.com/n4darae/huawei-API/src/internal/store"
)

var (
	testNodeIP  = netip.MustParseAddr("139.99.68.39")
	testBaseNow = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	simTimeout  = 300 * time.Millisecond
)

type simClock struct{ farm *sim.Farm }

func (c *simClock) Now() time.Time { return c.farm.Now() }

func (c *simClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *simClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.farm.Advance(d)
	ch <- c.farm.Now()
	return ch
}

func (c *simClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.farm.Advance(d)
	return nil
}

type hookDev struct {
	device.Device
	hooks *hooks
}

type hooks struct {
	mu            sync.Mutex
	setNetModeErr error
	setDHCPErr    error
	traffic       *device.Traffic
	rebootErr     error
}

func (h *hooks) set(fn func(*hooks)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(h)
}

func (d *hookDev) SetNetMode(ctx context.Context, m device.NetMode) error {
	d.hooks.mu.Lock()
	err := d.hooks.setNetModeErr
	d.hooks.mu.Unlock()
	if err != nil {
		return err
	}
	return d.Device.SetNetMode(ctx, m)
}

func (d *hookDev) SetDHCPSettings(ctx context.Context, s device.DHCPSettings) error {
	d.hooks.mu.Lock()
	err := d.hooks.setDHCPErr
	d.hooks.mu.Unlock()
	if err != nil {
		return err
	}
	return d.Device.SetDHCPSettings(ctx, s)
}

func (d *hookDev) Reboot(ctx context.Context) error {
	d.hooks.mu.Lock()
	err := d.hooks.rebootErr
	d.hooks.mu.Unlock()
	if err != nil {
		return err
	}
	return d.Device.Reboot(ctx)
}

func (d *hookDev) Traffic(ctx context.Context) (device.Traffic, error) {
	d.hooks.mu.Lock()
	tr := d.hooks.traffic
	d.hooks.mu.Unlock()
	if tr != nil {
		return *tr, nil
	}
	return d.Device.Traffic(ctx)
}

type hookRegistry struct {
	farm  *sim.Farm
	inner device.Registry
	hooks *hooks
}

var _ device.Registry = (*hookRegistry)(nil)

func (r *hookRegistry) ForSlot(ctx context.Context, s domain.Slot) (device.Device, error) {
	return r.ForAddr(ctx, s.GatewayIP())
}

func (r *hookRegistry) ForAddr(ctx context.Context, a netip.Addr) (device.Device, error) {
	if r.farm.BaseURLForAddr(a) == "" {
		return nil, domain.Wrap(domain.ErrUnreachable, "devops test: no simulated dongle answers at %s", a)
	}
	d, err := r.inner.ForAddr(ctx, a)
	if err != nil {
		return nil, err
	}
	return &hookDev{Device: d, hooks: r.hooks}, nil
}

func (r *hookRegistry) Close() error { return r.inner.Close() }

type recNet struct {
	mu      sync.Mutex
	applied []domain.Slot
	seen    []bool
	watch   func(domain.Slot) bool
	err     error
}

var _ netcfg.Manager = (*recNet)(nil)

func (m *recNet) EnsureGlobal(context.Context, []netip.Addr) error { return nil }

func (m *recNet) EnsureRouteTableNames(context.Context) error { return nil }

func (m *recNet) ApplySlot(_ context.Context, s domain.Slot, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.applied = append(m.applied, s)
	if m.watch != nil {
		m.seen = append(m.seen, m.watch(s))
	}
	return nil
}

func (m *recNet) RemoveSlot(context.Context, domain.Slot) error { return nil }

func (m *recNet) Observe(context.Context) (netcfg.Observation, error) {
	return netcfg.Observation{}, domain.ErrNotImplemented
}

func (m *recNet) AssertInvariants(context.Context) []netcfg.Violation { return nil }

func (m *recNet) Subscribe(context.Context) (<-chan netcfg.LinkEvent, func(), error) {
	return nil, nil, domain.ErrNotImplemented
}

func (m *recNet) Applied() []domain.Slot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.Slot(nil), m.applied...)
}

func (m *recNet) Observations() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.seen...)
}

type eventSink struct {
	mu     sync.Mutex
	events []eventbus.Event
	done   chan struct{}
}

func newEventSink(t *testing.T, bus *eventbus.MemBus) *eventSink {
	t.Helper()
	ch, cancel, err := bus.Subscribe(context.Background(), []string{eventbus.TopicAll})
	if err != nil {
		t.Fatalf("bus subscribe: %v", err)
	}
	s := &eventSink{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for e := range ch {
			s.mu.Lock()
			s.events = append(s.events, e)
			s.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-s.done
	})
	return s
}

func (s *eventSink) Events() []eventbus.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]eventbus.Event(nil), s.events...)
}

type harness struct {
	t     *testing.T
	farm  *sim.Farm
	clock *simClock
	db    *store.Store
	net   *recNet
	bus   *eventbus.MemBus
	sink  *eventSink
	hooks *hooks
	svc   *Service
	node  domain.Node
}

type harnessOptions struct {
	Slots       int
	FactoryLAN  bool
	Timeouts    *Timeouts
	Watch       func(domain.Slot) bool
	NoNetcfg    bool
	HoldToNewIP time.Duration
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	if o.Slots <= 0 {
		o.Slots = 1
	}
	base := testBaseNow
	farm := sim.NewFarm(o.Slots, sim.FarmOptions{
		Clock:             func() time.Time { return base },
		FactoryDefaultLAN: o.FactoryLAN,
		HoldToNewIP:       o.HoldToNewIP,
	})
	t.Cleanup(func() { farm.Close() })

	clock := &simClock{farm: farm}
	kek, err := secrets.GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	sealer, err := secrets.NewSealer(kek)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "state", "dongled.db"), sealer, store.WithClock(clock))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()
	node := domain.Node{ID: "n1", Name: "local", Kind: domain.NodeKindLocal, PublicHost: testNodeIP}
	if err := db.Nodes().Upsert(ctx, node); err != nil {
		t.Fatalf("Upsert node: %v", err)
	}
	for i := 1; i <= o.Slots; i++ {
		slot := domain.Slot(i)
		row := domain.SlotRow{
			ID:      slotID(slot),
			NodeID:  node.ID,
			Slot:    slot,
			USBPath: "1-13." + slot.String(),
			IDPath:  "pci-0000:00:14.0-usb-0:13." + slot.String() + ":1.0",
			IfName:  slot.IfaceName(),
		}
		if err := db.Slots().Create(ctx, row); err != nil {
			t.Fatalf("Create slot %d: %v", i, err)
		}
		d := domain.Dongle{
			ID:                 dongleID(slot),
			NodeID:             node.ID,
			IMEI:               "86182103247950" + slot.String(),
			Carrier:            "beeline",
			AutoRecoverEnabled: true,
			CapResetDay:        1,
		}
		if err := db.Dongles().Create(ctx, d); err != nil {
			t.Fatalf("Create dongle %d: %v", i, err)
		}
		if err := db.Slots().Attach(ctx, row.ID, d.ID); err != nil {
			t.Fatalf("Attach dongle %d: %v", i, err)
		}
		px := domain.Proxy{
			ID:        proxyID(slot),
			SlotID:    row.ID,
			Enabled:   true,
			SocksPort: slot.SocksPort(),
			HTTPPort:  slot.HTTPPort(),
			Username:  "cust_" + slot.String(),
			Password:  "Kq7mZr2xTn9wLb4V",
			AuthMode:  domain.AuthUserPass,
			Policy:    domain.DefaultProxyPolicy(),
		}
		if err := db.Proxies().Create(ctx, px); err != nil {
			t.Fatalf("Create proxy %d: %v", i, err)
		}
	}

	h := &harness{
		t:     t,
		farm:  farm,
		clock: clock,
		db:    db,
		net:   &recNet{watch: o.Watch},
		bus:   eventbus.NewMemBus(256),
		hooks: &hooks{},
		node:  node,
	}
	h.sink = newEventSink(t, h.bus)

	inner := hilink.NewRegistry(hilink.RegistryOptions{
		Options:    hilink.Options{Timeout: simTimeout},
		BaseURLFor: farm.BaseURLForAddr,
	})
	reg := &hookRegistry{farm: farm, inner: inner, hooks: h.hooks}

	to := DefaultTimeouts()
	to.PollInterval = time.Second
	to.RediscoverWindow = 15 * time.Second
	if o.Timeouts != nil {
		to = *o.Timeouts
	}
	deps := Deps{
		Repos:    db,
		Dev:      reg,
		Bus:      h.bus,
		Clock:    clock,
		Timeouts: to,
		NodeID:   node.ID,
	}
	if !o.NoNetcfg {
		deps.Net = h.net
	}
	svc, err := New(deps)
	if err != nil {
		t.Fatalf("devops.New: %v", err)
	}
	h.svc = svc
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = svc.Shutdown(c)
	})

	if !o.FactoryLAN {
		for i := 1; i <= o.Slots; i++ {
			dev, err := reg.ForSlot(ctx, domain.Slot(i))
			if err != nil {
				t.Fatalf("registry slot %d: %v", i, err)
			}
			if err := dev.DataSwitch(ctx, true); err != nil {
				t.Fatalf("initial data on for slot %d: %v", i, err)
			}
		}
	}
	return h
}

func slotID(s domain.Slot) string { return "s" + s.String() }

func dongleID(s domain.Slot) string { return "d" + s.String() }

func proxyID(s domain.Slot) string { return "p" + s.String() }

func (h *harness) await(op *domain.Operation, err error) domain.Operation {
	h.t.Helper()
	if err != nil {
		h.t.Fatalf("operation did not start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	final, err := h.svc.Wait(ctx, op.ID)
	if err != nil {
		h.t.Fatalf("Wait %s: %v", op.ID, err)
	}
	return final
}

func (s *eventSink) waitFor(t *testing.T, want func([]eventbus.Event) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if want(s.Events()) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the event bus never carried what the test was waiting for")
}

func (h *harness) steps(subject string) []string {
	h.t.Helper()
	h.sink.waitFor(h.t, func(evs []eventbus.Event) bool {
		for _, e := range evs {
			if e.Type != eventbus.EvOpDone {
				continue
			}
			var p opPayload
			if err := json.Unmarshal(e.Data, &p); err == nil && p.SubjectID == subject {
				return true
			}
		}
		return false
	})
	out := []string{}
	for _, e := range h.sink.Events() {
		if e.Type != eventbus.EvOpUpdate && e.Type != eventbus.EvOpDone {
			continue
		}
		var p opPayload
		if err := json.Unmarshal(e.Data, &p); err != nil {
			continue
		}
		if subject != "" && p.SubjectID != subject {
			continue
		}
		if p.Step == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == p.Step {
			continue
		}
		out = append(out, p.Step)
	}
	return out
}

func decodeResult[T any](t *testing.T, op domain.Operation) T {
	t.Helper()
	var out T
	if op.ResultJSON == "" {
		return out
	}
	if err := json.Unmarshal([]byte(op.ResultJSON), &out); err != nil {
		t.Fatalf("result json %q: %v", op.ResultJSON, err)
	}
	return out
}

func requireErrorIs(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s returned %v, want %v", what, got, want)
	}
}

func (h *harness) startLiveRotate(slot domain.Slot) domain.Operation {
	h.t.Helper()
	now := h.clock.Now()
	op := domain.Operation{
		ID:          "op_live_rotate_" + slot.String(),
		Kind:        domain.OpRotate,
		SubjectType: domain.SubjectProxy,
		SubjectID:   proxyID(slot),
		State:       domain.OpRunning,
		Step:        string(domain.StepHold),
		StartedAt:   domain.UnixMillis(now),
		DeadlineAt:  domain.UnixMillis(now.Add(90 * time.Second)),
		Trigger:     domain.TriggerCustomerAPI,
		ActorType:   domain.ActorAPIKey,
	}
	if err := h.db.Operations().Create(context.Background(), op); err != nil {
		h.t.Fatalf("seed live rotate: %v", err)
	}
	return op
}

func (h *harness) startLiveRotateOnDongle(slot domain.Slot) domain.Operation {
	h.t.Helper()
	now := h.clock.Now()
	op := domain.Operation{
		ID:          "op_live_dongle_" + slot.String(),
		Kind:        domain.OpSetLanIP,
		SubjectType: domain.SubjectDongle,
		SubjectID:   dongleID(slot),
		State:       domain.OpRunning,
		Step:        StepPostDHCP,
		StartedAt:   domain.UnixMillis(now),
		DeadlineAt:  domain.UnixMillis(now.Add(90 * time.Second)),
		Trigger:     domain.TriggerAdminUI,
		ActorType:   domain.ActorAdmin,
	}
	if err := h.db.Operations().Create(context.Background(), op); err != nil {
		h.t.Fatalf("seed live dongle operation: %v", err)
	}
	return op
}
