package reconcile

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

type pollRig struct {
	poller *Poller
	net    *stubNet
	fw     *stubFW
	proxy  *stubProxy
	dev    *stubRegistry
	clock  *testClock
	rows   []domain.SlotRow
}

func newPollRig(t *testing.T, slots int) *pollRig {
	t.Helper()
	f := newFarm(slots)
	clock := newTestClock(baseTime)
	net := &stubNet{obs: f.obs.Net}
	firewall := newStubFW()
	for _, s := range sortedSlots(f.slots) {
		firewall.known[s.IfaceName()] = false
	}
	proxy := newStubProxy()
	for _, s := range sortedSlots(f.slots) {
		proxy.status[s] = f.obs.ProxyStatus[s]
	}
	registry := newStubRegistry(slots)

	return &pollRig{
		poller: NewPoller(PollDeps{
			Net: net, FW: firewall, Proxy: proxy, Dev: registry, Clock: clock,
			Backoff: Backoff{Min: 10 * time.Second, Max: time.Minute, Factor: 2},
			Rand:    func() float64 { return 0.5 },
			Workers: 4,
		}),
		net: net, fw: firewall, proxy: proxy, dev: registry, clock: clock,
		rows: f.world().Desired.SlotRows(),
	}
}

func TestSweepIncrementsSweepsCompleted(t *testing.T) {
	rig := newPollRig(t, 4)
	ctx := context.Background()

	if rig.poller.SweepsCompleted() != 0 {
		t.Fatal("a fresh poller must report a cold cache")
	}
	if rig.poller.CacheWarm() {
		t.Fatal("a fresh poller must not claim a warm cache; that is what holds startup grace open")
	}

	for i := 1; i <= 3; i++ {
		obs := rig.poller.Sweep(ctx, rig.rows)
		if obs.SweepsCompleted != i {
			t.Fatalf("sweep %d reported SweepsCompleted %d", i, obs.SweepsCompleted)
		}
		if rig.poller.SweepsCompleted() != i {
			t.Fatalf("the cache reports %d sweeps after sweep %d", rig.poller.SweepsCompleted(), i)
		}
	}
	if !rig.poller.CacheWarm() {
		t.Fatal("the cache is still cold after three complete sweeps")
	}
	if !rig.poller.Snapshot().LastSweepAt.Equal(baseTime) {
		t.Fatal("LastSweepAt was not stamped from the injected clock")
	}
}

func TestCancelledSweepDoesNotWarmTheCache(t *testing.T) {
	rig := newPollRig(t, 4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	obs := rig.poller.Sweep(ctx, rig.rows)
	if obs.SweepsCompleted != 0 {
		t.Fatalf("a cancelled sweep reported %d completed sweeps", obs.SweepsCompleted)
	}
	if rig.poller.CacheWarm() {
		t.Fatal("a cancelled sweep must leave the cache cold")
	}
}

func TestSnapshotReadsTheCacheAndPerformsNoIO(t *testing.T) {
	rig := newPollRig(t, 3)
	ctx := context.Background()
	rig.poller.Sweep(ctx, rig.rows)

	before := rig.dev.device(1).polls()
	for i := 0; i < 50; i++ {
		if got := rig.poller.Snapshot(); len(got.Devices) != 3 {
			t.Fatalf("Snapshot returned %d devices", len(got.Devices))
		}
	}
	if after := rig.dev.device(1).polls(); after != before {
		t.Fatalf("Snapshot polled the device %d extra times; poll.go owns the cache alone", after-before)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	rig := newPollRig(t, 2)
	rig.poller.Sweep(context.Background(), rig.rows)

	snap := rig.poller.Snapshot()
	snap.Devices[1] = DeviceObservation{Err: "mutated by a caller"}
	snap.Fenced["dg01"] = true
	delete(snap.ProxyStatus, 2)

	fresh := rig.poller.Snapshot()
	if fresh.Devices[1].Err != "" {
		t.Error("a caller mutated the observation cache through Snapshot")
	}
	if fresh.Fenced["dg01"] {
		t.Error("a caller mutated the fenced map through Snapshot")
	}
	if _, ok := fresh.ProxyStatus[2]; !ok {
		t.Error("a caller deleted from the proxy status map through Snapshot")
	}
}

func TestFencedMapOnlyHoldsInterfacesTheFirewallKnows(t *testing.T) {
	rig := newPollRig(t, 3)
	delete(rig.fw.known, "dg02")

	obs := rig.poller.Sweep(context.Background(), rig.rows)
	if _, ok := obs.Fenced["dg02"]; ok {
		t.Fatal("an interface the firewall does not know must be absent from Fenced, not recorded as false")
	}
	if _, ok := obs.Fenced["dg01"]; !ok {
		t.Fatal("an interface the firewall knows must be recorded")
	}

	if err := rig.fw.AddDongle(context.Background(), "dg02", domain.Slot(2).GatewayIP()); err != nil {
		t.Fatalf("AddDongle: %v", err)
	}
	obs = rig.poller.Sweep(context.Background(), rig.rows)
	if _, ok := obs.Fenced["dg02"]; !ok {
		t.Fatal("the interface is still missing from Fenced after it joined the set")
	}
}

func TestUnreachableDeviceKeepsItsLastSuccessfulObservationTime(t *testing.T) {
	rig := newPollRig(t, 1)
	ctx := context.Background()

	rig.poller.Sweep(ctx, rig.rows)
	first := rig.poller.Snapshot().Devices[1]
	if !first.Reachable || !first.ObservedAt.Equal(baseTime) {
		t.Fatalf("first sweep produced %+v", first)
	}

	rig.dev.device(1).set(func(d *stubDevice) { d.reachable = false })
	rig.clock.advance(2 * time.Minute)
	rig.poller.Sweep(ctx, rig.rows)

	got := rig.poller.Snapshot().Devices[1]
	if got.Reachable {
		t.Fatal("the device is still marked reachable")
	}
	if !got.ObservedAt.Equal(baseTime) {
		t.Fatalf("ObservedAt moved to %s; it must stay at the last successful contact so Plan can measure the dwell", got.ObservedAt)
	}
	if got.Err == "" {
		t.Error("the failure reason was not recorded")
	}
}

func TestBackoffSkipsADeadDongleAndLeavesTheOthersAlone(t *testing.T) {
	rig := newPollRig(t, 4)
	ctx := context.Background()

	rig.dev.device(2).set(func(d *stubDevice) { d.reachable = false })
	rig.poller.Sweep(ctx, rig.rows)
	if rig.poller.BackoffFailures(2) != 1 {
		t.Fatalf("the dead dongle has %d recorded failures", rig.poller.BackoffFailures(2))
	}

	deadBefore := rig.dev.device(2).polls()
	liveBefore := rig.dev.device(3).polls()

	rig.clock.advance(time.Second)
	rig.poller.Sweep(ctx, rig.rows)

	if got := rig.dev.device(2).polls(); got != deadBefore {
		t.Fatalf("the dead dongle was polled again inside its backoff window (%d extra)", got-deadBefore)
	}
	if got := rig.dev.device(3).polls(); got != liveBefore+1 {
		t.Fatalf("a healthy dongle was polled %d times, backoff on one dongle must not slow the rest", got-liveBefore)
	}

	rig.clock.advance(30 * time.Second)
	rig.dev.device(2).set(func(d *stubDevice) { d.reachable = true })
	rig.poller.Sweep(ctx, rig.rows)

	if got := rig.dev.device(2).polls(); got == deadBefore {
		t.Fatal("the dead dongle was never retried after its backoff elapsed")
	}
	if rig.poller.BackoffFailures(2) != 0 {
		t.Fatal("a successful poll must clear the backoff")
	}
	if !rig.poller.Snapshot().Devices[2].Reachable {
		t.Fatal("the recovered dongle is still marked unreachable")
	}
}

func TestASweepOfFortyEightDeadDonglesStillCompletes(t *testing.T) {
	rig := newPollRig(t, 48)
	for i := 1; i <= 48; i++ {
		rig.dev.device(domain.Slot(i)).set(func(d *stubDevice) {
			d.reachable = false
			d.delay = 20 * time.Millisecond
		})
	}
	rig.poller.deps.DeviceTimeout = 50 * time.Millisecond

	start := time.Now()
	obs := rig.poller.Sweep(context.Background(), rig.rows)
	elapsed := time.Since(start)

	if obs.SweepsCompleted != 1 {
		t.Fatalf("the sweep did not complete: %d", obs.SweepsCompleted)
	}
	if len(obs.Devices) != 48 {
		t.Fatalf("the sweep recorded %d devices", len(obs.Devices))
	}
	if elapsed > 5*time.Second {
		t.Fatalf("a farm of dead dongles took %s to sweep; the worker pool is not bounding it", elapsed)
	}
	for i := 1; i <= 48; i++ {
		if rig.poller.BackoffFailures(domain.Slot(i)) != 1 {
			t.Fatalf("slot %d did not record its failure", i)
		}
	}
}

func TestSlowFieldsAreReadOnTheFirstSweep(t *testing.T) {
	rig := newPollRig(t, 1)
	rig.dev.device(1).set(func(d *stubDevice) { d.maxIdle = device.MaxIdleTimeDefault })

	obs := rig.poller.Sweep(context.Background(), rig.rows)
	got := obs.Devices[1]
	if got.MaxIdleTime != device.MaxIdleTimeDefault {
		t.Fatalf("MaxIdleTime is %d after the first sweep; the reconciler would never disable the idle timer",
			got.MaxIdleTime)
	}
	if got.Sim != device.SimStateReady {
		t.Fatalf("SimState is %d after the first sweep", int(got.Sim))
	}
}

func TestSlotWithoutADongleIsNotPolled(t *testing.T) {
	rig := newPollRig(t, 2)
	rig.rows[0].DongleID = nil

	obs := rig.poller.Sweep(context.Background(), rig.rows)
	if _, ok := obs.Devices[1]; ok {
		t.Fatal("an empty slot produced a device observation")
	}
	if _, ok := obs.Devices[2]; !ok {
		t.Fatal("the occupied slot was not observed")
	}
	if rig.dev.device(1).polls() != 0 {
		t.Fatal("an empty slot was polled over the network")
	}
}

func TestNetcfgObservationErrorKeepsTheLastGoodView(t *testing.T) {
	rig := newPollRig(t, 2)
	ctx := context.Background()
	rig.poller.Sweep(ctx, rig.rows)

	rig.net.mu.Lock()
	rig.net.observeErr = domain.ErrDegraded
	rig.net.mu.Unlock()

	obs := rig.poller.Sweep(ctx, rig.rows)
	if len(obs.Net.Links) != 2 {
		t.Fatalf("a failed Observe wiped the link view, leaving %d links; Plan would rewrite every slot",
			len(obs.Net.Links))
	}
}

func TestFirewallVerifyDrivesNftTablePresent(t *testing.T) {
	rig := newPollRig(t, 1)
	ctx := context.Background()

	if obs := rig.poller.Sweep(ctx, rig.rows); !obs.NftTablePresent {
		t.Fatal("a healthy firewall must set NftTablePresent")
	}
	rig.fw.mu.Lock()
	rig.fw.tableOK = false
	rig.fw.mu.Unlock()

	if obs := rig.poller.Sweep(ctx, rig.rows); obs.NftTablePresent {
		t.Fatal("a missing table must clear NftTablePresent")
	}
}

func TestPollerWithoutBackendsStillCompletesSweeps(t *testing.T) {
	p := NewPoller(PollDeps{Clock: newTestClock(baseTime)})
	obs := p.Sweep(context.Background(), []domain.SlotRow{{ID: "s01", Slot: 1, IfName: "dg01"}})
	if obs.SweepsCompleted != 1 {
		t.Fatal("a poller with no backends must still warm the cache, otherwise grace never lifts")
	}
	if obs.Net.Links == nil || obs.Net.Routes == nil {
		t.Fatal("the observation must always carry usable maps")
	}
}

func TestProxyStatusErrorIsRecordedNotDropped(t *testing.T) {
	rig := newPollRig(t, 1)
	obs := rig.poller.Sweep(context.Background(), rig.rows)
	if !obs.ProxyStatus[1].Healthy() {
		t.Fatal("the healthy stub proxy was not recorded as healthy")
	}
	if obs.ProxyStatus[1].Unit != domain.Slot(1).ProxyUnit() {
		t.Fatalf("proxy status carries unit %q", obs.ProxyStatus[1].Unit)
	}
}

func TestObservationCloneDeepCopiesTheNetView(t *testing.T) {
	obs := newObservedState()
	obs.Net.Links["dg01"] = netcfg.LinkState{
		Name:  "dg01",
		Addrs: []netip.Prefix{domain.Slot(1).HostPrefix()},
	}
	obs.Net.Rules = []netcfg.RuleState{{Priority: 1001}}
	obs.Net.Routes[1001] = []netcfg.RouteState{{Dev: "dg01"}}

	clone := obs.Clone()
	clone.Net.Links["dg01"].Addrs[0] = netip.MustParsePrefix("10.0.0.1/32")
	clone.Net.Rules[0].Priority = 9999
	clone.Net.Routes[1001][0].Dev = "eth0"

	if obs.Net.Links["dg01"].Addrs[0] != domain.Slot(1).HostPrefix() {
		t.Error("Clone shares the link address slice")
	}
	if obs.Net.Rules[0].Priority != 1001 {
		t.Error("Clone shares the rule slice")
	}
	if obs.Net.Routes[1001][0].Dev != "dg01" {
		t.Error("Clone shares the route slice")
	}
}
