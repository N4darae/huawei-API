package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
)

type engineRig struct {
	engine *Engine
	repos  *stubRepos
	net    *stubNet
	fw     *stubFW
	proxy  *stubProxy
	dev    *stubRegistry
	ops    *stubOps
	bus    *eventbus.MemBus
	clock  *testClock
	farm   *farm
}

func newEngineRig(t *testing.T, f *farm) *engineRig {
	t.Helper()

	clock := newTestClock(f.now)
	net := &stubNet{obs: f.obs.Net}
	firewall := newStubFW()
	for _, s := range sortedSlots(f.slots) {
		if _, known := f.obs.Fenced[s.IfaceName()]; known {
			firewall.known[s.IfaceName()] = f.obs.Fenced[s.IfaceName()]
		}
	}
	proxy := newStubProxy()
	for _, s := range sortedSlots(f.slots) {
		proxy.status[s] = f.obs.ProxyStatus[s]
	}
	registry := newStubRegistry(len(f.slots))
	for _, s := range sortedSlots(f.slots) {
		obs := f.obs.Devices[s]
		registry.device(s).set(func(d *stubDevice) {
			d.reachable = obs.Reachable
			d.conn = obs.Conn
			d.sim = obs.Sim
			d.maxIdle = obs.MaxIdleTime
			d.loginNeeded = obs.LoginNeeded
		})
	}

	repos := newStubRepos(f)
	ops := &stubOps{}
	bus := eventbus.NewMemBus(eventbus.DefaultSubscriberBuffer)
	t.Cleanup(bus.Close)

	e, err := NewEngine(Deps{
		NodeID: testNodeID,
		Repos:  repos,
		Net:    net,
		FW:     firewall,
		Proxy:  proxy,
		Dev:    registry,
		Bus:    bus,
		Ops:    ops,
		Clock:  clock,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Reconcile: config.Reconcile{
			SweepInterval:       10 * time.Second,
			StartupGrace:        f.grace,
			BackoffMin:          2 * time.Second,
			BackoffMax:          time.Minute,
			RebootBudgetPerDay:  4,
			RebootCooldown:      30 * time.Minute,
			MaxConcurrentRotate: 4,
		},
		MinRotateInterval: 60 * time.Second,
		HostBootedAt:      f.booted,
		Rand:              func() float64 { return 0.5 },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	e.startedAt = f.started

	return &engineRig{
		engine: e, repos: repos, net: net, fw: firewall, proxy: proxy,
		dev: registry, ops: ops, bus: bus, clock: clock, farm: f,
	}
}

func TestNewEngineRejectsMissingDeps(t *testing.T) {
	if _, err := NewEngine(Deps{Repos: newStubRepos(newFarm(1))}); !errors.Is(err, ErrNoNode) {
		t.Errorf("NewEngine without a node id returned %v, want ErrNoNode", err)
	}
	if _, err := NewEngine(Deps{NodeID: testNodeID}); !errors.Is(err, ErrNoRepos) {
		t.Errorf("NewEngine without repos returned %v, want ErrNoRepos", err)
	}
}

func TestEngineConvergesAColdFarm(t *testing.T) {
	f := newFarm(4)
	for _, s := range sortedSlots(f.slots) {
		f.obs.ProxyStatus[s] = proxysup.Status{Unit: s.ProxyUnit()}
		delete(f.obs.Fenced, s.IfaceName())
	}
	rig := newEngineRig(t, f)
	for _, s := range sortedSlots(f.slots) {
		delete(rig.fw.known, s.IfaceName())
		rig.proxy.status[s] = proxysup.Status{Unit: s.ProxyUnit()}
	}
	ctx := context.Background()

	first, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first.Failed != 0 {
		t.Fatalf("first sweep failed %d actions: %+v", first.Failed, first.Outcomes)
	}
	if first.Applied == 0 {
		t.Fatal("a cold farm produced no work")
	}
	if len(rig.fw.addedIfaces()) != 4 {
		t.Fatalf("the firewall learned %d interfaces, want 4", len(rig.fw.addedIfaces()))
	}
	applied, _, _ := rig.proxy.calls()
	if len(applied) != 4 {
		t.Fatalf("%d proxies were started, want 4", len(applied))
	}

	second, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(second.Actions) != 0 {
		t.Fatalf("the engine did not converge; the second sweep still plans %v",
			Actions(second.Actions).Kinds())
	}
}

func TestEngineIsIdempotentAcrossManySweeps(t *testing.T) {
	rig := newEngineRig(t, newFarm(4))
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		res, err := rig.engine.Once(ctx)
		if err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
		if len(res.Actions) != 0 {
			t.Fatalf("sweep %d on a converged farm planned %v", i, Actions(res.Actions).Kinds())
		}
	}
	applied, stopped, evicted := rig.proxy.calls()
	if len(applied)+len(stopped)+len(evicted) != 0 {
		t.Fatalf("a converged farm still touched the supervisor: %d/%d/%d",
			len(applied), len(stopped), len(evicted))
	}
}

func TestEngineHoldsGraceOnTheFirstSweepOfAColdCache(t *testing.T) {
	f := newFarm(48)
	f.booted = baseTime.Add(-72 * time.Hour)
	f.started = baseTime
	rig := newEngineRig(t, f)
	for i := 1; i <= 48; i++ {
		rig.dev.device(domain.Slot(i)).set(func(d *stubDevice) { d.reachable = false })
	}

	res, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if !res.World.InStartupGrace() {
		t.Fatal("the first sweep after a restart must still be inside grace")
	}
	if n := len(Actions(res.Actions).Destructive()); n != 0 {
		t.Fatalf("the first sweep planned %d destructive actions on 48 unreachable dongles", n)
	}
	if len(rig.ops.recorded()) != 0 {
		t.Fatalf("recovery ran during grace: %+v", rig.ops.recorded())
	}
}

func TestEngineRecoversOnceGraceLifts(t *testing.T) {
	f := newFarm(48)
	f.booted = baseTime.Add(-72 * time.Hour)
	f.started = baseTime
	rig := newEngineRig(t, f)
	for i := 1; i <= 48; i++ {
		rig.dev.device(domain.Slot(i)).set(func(d *stubDevice) { d.reachable = false })
	}
	ctx := context.Background()

	if _, err := rig.engine.Once(ctx); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if len(rig.ops.recorded()) != 0 {
		t.Fatal("recovery ran on the first sweep")
	}

	rig.clock.advance(10 * time.Minute)
	res, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if res.World.InStartupGrace() {
		t.Fatal("grace did not lift after a warm cache and an elapsed process grace")
	}
	if got := len(rig.ops.recorded()); got != 48 {
		t.Fatalf("%d dongles were rebooted after grace lifted, want 48", got)
	}
	for _, call := range rig.ops.recorded() {
		if call.kind != domain.OpReboot {
			t.Fatalf("an unreachable dongle produced a %s, want a reboot", call.kind)
		}
	}
}

func TestEngineRecoverFailsOrphanedOperations(t *testing.T) {
	f := newFarm(2)
	f.liveOp(domain.OpRotate, domain.SubjectProxy, proxyID(1))
	f.liveOp(domain.OpReboot, domain.SubjectDongle, dongleID(2))
	rig := newEngineRig(t, f)

	live, err := rig.repos.Operations().ListActive(context.Background())
	if err != nil || len(live) != 2 {
		t.Fatalf("the fixture holds %d live operations, err %v", len(live), err)
	}
	if err := rig.engine.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	live, err = rig.repos.Operations().ListActive(context.Background())
	if err != nil || len(live) != 0 {
		t.Fatalf("%d operations survived the restart, err %v", len(live), err)
	}
	if rig.repos.orphansReconciled != 2 {
		t.Fatalf("Recover reconciled %d orphans", rig.repos.orphansReconciled)
	}
}

func TestEngineRecoverPublishesANotice(t *testing.T) {
	f := newFarm(1)
	f.liveOp(domain.OpRotate, domain.SubjectProxy, proxyID(1))
	rig := newEngineRig(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, unsubscribe, err := rig.bus.Subscribe(ctx, []string{eventbus.TopicSystem})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	if err := rig.engine.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	select {
	case ev := <-events:
		if ev.Type != eventbus.EvSystemNotice {
			t.Fatalf("got event %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notice was published for the reconciled orphans")
	}
}

func TestEngineStopsAProxyWhoseStickDied(t *testing.T) {
	f := newFarm(2)
	f.detach(1)
	rig := newEngineRig(t, f)

	res, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("%d actions failed: %+v", res.Failed, res.Outcomes)
	}
	_, stopped, _ := rig.proxy.calls()
	if len(stopped) != 1 || stopped[0] != 1 {
		t.Fatalf("stopped slots %v, want only slot 1", stopped)
	}
}

func TestEngineEvictsAndDisablesAnExpiredProxy(t *testing.T) {
	f := newFarm(2)
	expired := domain.UnixMillis(baseTime.Add(-time.Hour))
	f.proxy(1, func(p *domain.Proxy) { p.ExpiresAt = &expired })
	rig := newEngineRig(t, f)

	res, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("%d actions failed: %+v", res.Failed, res.Outcomes)
	}
	if got := rig.repos.disabledProxies(); len(got) != 1 || got[0] != proxyID(1) {
		t.Fatalf("disabled proxies %v, want only %s", got, proxyID(1))
	}
	_, _, evicted := rig.proxy.calls()
	if len(evicted) != 1 || evicted[0] != 1 {
		t.Fatalf("evicted slots %v; revoking the 3proxy user cannot end a live session under noforce", evicted)
	}

	second, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(second.Actions) != 0 {
		t.Fatalf("expiry did not converge, the second sweep plans %v", Actions(second.Actions).Kinds())
	}
}

func TestEngineDisablesTheIdleTimer(t *testing.T) {
	f := newFarm(1)
	rig := newEngineRig(t, f)
	rig.dev.device(1).set(func(d *stubDevice) { d.maxIdle = device.MaxIdleTimeDefault })

	first, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if len(Actions(first.Actions).OfKind(ActSetMaxIdle)) != 1 {
		t.Fatalf("the observed MaxIdelTime of 300 produced %v", Actions(first.Actions).Kinds())
	}

	second, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(Actions(second.Actions).OfKind(ActSetMaxIdle)) != 0 {
		t.Fatalf("the idle timer action repeated after it was already applied: %v",
			Actions(second.Actions).Kinds())
	}

	rig.dev.device(1).mu.Lock()
	idle := rig.dev.device(1).maxIdle
	writes := rig.dev.device(1).idleWrites
	rig.dev.device(1).mu.Unlock()
	if idle != device.MaxIdleTimeDisabled || writes != 1 {
		t.Fatalf("the idle timer is %d after %d writes", idle, writes)
	}
}

func TestEngineDoesNotRotateWhenAnOperationAppearsAfterPlanning(t *testing.T) {
	f := newFarm(1)
	f.device(1, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
	rig := newEngineRig(t, f)
	rig.dev.device(1).set(func(d *stubDevice) { d.conn = device.ConnDisconnected })
	ctx := context.Background()

	if _, err := rig.engine.Once(ctx); err != nil {
		t.Fatalf("warm up sweep: %v", err)
	}
	rig.ops.mu.Lock()
	rig.ops.calls = nil
	rig.ops.mu.Unlock()

	rig.repos.setLive(domain.Operation{
		ID: "op-live", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: proxyID(1),
		State: domain.OpRunning, Trigger: domain.TriggerCustomerAPI,
		StartedAt: domain.UnixMillis(baseTime), DeadlineAt: domain.UnixMillis(baseTime.Add(90 * time.Second)),
	})
	rig.clock.advance(5 * time.Minute)

	res, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("a live customer rotate did not fence the plan: %v", Actions(res.Actions).Kinds())
	}
	if len(rig.ops.recorded()) != 0 {
		t.Fatalf("the engine started a second rotate: %+v", rig.ops.recorded())
	}
}

func TestActuatorRefusesADestructiveActionThatRacedAnOperation(t *testing.T) {
	f := newFarm(1)
	rig := newEngineRig(t, f)

	rig.repos.setLive(domain.Operation{
		ID: "op-live", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: proxyID(1),
		State: domain.OpRunning, Trigger: domain.TriggerCustomerAPI,
		StartedAt: domain.UnixMillis(baseTime), DeadlineAt: domain.UnixMillis(baseTime.Add(90 * time.Second)),
	})

	action := act(ActRecoverRotate, domain.SubjectProxy, proxyID(1), 1, ReasonDataSessionDown,
		map[string]string{ParamTrigger: string(domain.TriggerAutoRecovery)})
	out := rig.engine.actuator.Apply(context.Background(), f.world(), action)

	if !out.Skipped {
		t.Fatal("the actuator applied a rotate while another operation was live")
	}
	if out.OperationID != "op-live" {
		t.Fatalf("the skip did not name the live operation: %q", out.OperationID)
	}
	if len(rig.ops.recorded()) != 0 {
		t.Fatalf("the rotate reached the rotator anyway: %+v", rig.ops.recorded())
	}
}

func TestActuatorWithoutOpsRefusesRecovery(t *testing.T) {
	f := newFarm(1)
	a, err := NewActuator(ActuatorDeps{Repos: newStubRepos(f), Clock: newTestClock(baseTime)})
	if err != nil {
		t.Fatalf("NewActuator: %v", err)
	}
	w := f.world()

	rotate := a.Apply(context.Background(), w,
		act(ActRecoverRotate, domain.SubjectProxy, proxyID(1), 1, ReasonDataSessionDown, nil))
	if !errors.Is(rotate.Err, ErrNoOps) {
		t.Errorf("recover_rotate without a rotator returned %v, want ErrNoOps", rotate.Err)
	}
	reboot := a.Apply(context.Background(), w,
		act(ActRebootDongle, domain.SubjectDongle, dongleID(1), 1, ReasonDongleUnreachable, nil))
	if !errors.Is(reboot.Err, ErrNoOps) {
		t.Errorf("reboot_dongle without a rotator returned %v, want ErrNoOps", reboot.Err)
	}
}

func TestActuatorRejectsUnknownActions(t *testing.T) {
	f := newFarm(1)
	a, err := NewActuator(ActuatorDeps{Repos: newStubRepos(f), Clock: newTestClock(baseTime)})
	if err != nil {
		t.Fatalf("NewActuator: %v", err)
	}
	out := a.Apply(context.Background(), f.world(),
		act(ActionKind("teleport"), domain.SubjectSlot, slotID(1), 1, "", nil))
	if !errors.Is(out.Err, ErrUnknownAct) {
		t.Fatalf("an unknown action returned %v, want ErrUnknownAct", out.Err)
	}
}

func TestActuatorAppliesGlobalNetcfgBeforeSlots(t *testing.T) {
	f := newFarm(2)
	f.obs.Net.PublicSrcRules = nil
	f.obs.Net.RouteTableNamesOK = false
	delete(f.obs.Net.Links, "dg01")
	rig := newEngineRig(t, f)
	rig.net.obs = f.obs.Net

	res, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("%d actions failed: %+v", res.Failed, res.Outcomes)
	}
	if rig.net.ensuredGlobal != 1 || rig.net.ensuredTableName != 1 {
		t.Fatalf("global routing was ensured %d/%d times", rig.net.ensuredGlobal, rig.net.ensuredTableName)
	}
	applied := rig.net.applied()
	if len(applied) != 1 || applied[0] != 1 {
		t.Fatalf("netcfg applied slots %v, want only slot 1", applied)
	}
	if res.Outcomes[0].Kind != ActApplyNetcfg || res.Outcomes[0].Slot != 0 {
		t.Fatal("the global apply must run before any slot apply; a slot rule added before rule 900 cuts live sessions")
	}
}

func TestActuatorBuildsAProxySpecFromTheDesiredState(t *testing.T) {
	f := newFarm(1)
	f.proxy(1, func(p *domain.Proxy) {
		p.AuthMode = domain.AuthBoth
		p.AuthIPs = []netip.Prefix{
			netip.MustParsePrefix("198.51.100.0/24"),
			netip.MustParsePrefix("203.0.113.5/32"),
		}
	})
	spec := SpecFor(f.node, f.slots[1], f.proxies[proxyID(1)], netip.MustParseAddr("1.1.1.1"))

	if spec.InternalIP != testPublicHost {
		t.Errorf("internal binds %s, must be the node public host", spec.InternalIP)
	}
	if spec.ExternalIP != domain.Slot(1).HostIP() {
		t.Errorf("external binds %s, must be the slot host address", spec.ExternalIP)
	}
	if spec.UID != domain.Slot(1).UID() || spec.GID != domain.Slot(1).GID() {
		t.Errorf("setuid/setgid are %d/%d, want %d/%d",
			spec.UID, spec.GID, domain.Slot(1).UID(), domain.Slot(1).GID())
	}
	if len(spec.NServers) != 2 || spec.NServers[0] != domain.Slot(1).GatewayIP() {
		t.Errorf("nservers are %v, the dongle gateway must come first", spec.NServers)
	}
	if len(spec.Users) != 1 || spec.Users[0].Name != "cust_01" {
		t.Errorf("users are %+v", spec.Users)
	}
	if spec.AuthMode != domain.AuthBoth || len(spec.AuthIPs) != 2 {
		t.Errorf("auth mode %q with %d whitelisted prefixes", spec.AuthMode, len(spec.AuthIPs))
	}
	if spec.ConfigPath != domain.Slot(1).ProxyConfigPath() || spec.LogPath != domain.Slot(1).ProxyLogPath() {
		t.Errorf("paths are %q and %q", spec.ConfigPath, spec.LogPath)
	}
}

func TestEngineKickIsNonBlockingAndCoalesces(t *testing.T) {
	rig := newEngineRig(t, newFarm(1))
	for i := 0; i < 100; i++ {
		rig.engine.Kick("test")
	}
	if got := len(rig.engine.kick); got != KickBuffer {
		t.Fatalf("the kick channel holds %d entries, want %d coalesced", got, KickBuffer)
	}
}

func TestEngineRunStopsOnContextCancel(t *testing.T) {
	rig := newEngineRig(t, newFarm(2))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- rig.engine.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for rig.engine.Sweeps() == 0 {
		select {
		case <-deadline:
			t.Fatal("the engine never completed a sweep")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestEngineSnapshotMatchesThePollerCache(t *testing.T) {
	rig := newEngineRig(t, newFarm(3))
	if len(rig.engine.Snapshot().Devices) != 0 {
		t.Fatal("the engine reported observations before any sweep")
	}
	if _, err := rig.engine.Once(context.Background()); err != nil {
		t.Fatalf("Once: %v", err)
	}
	snap := rig.engine.Snapshot()
	if len(snap.Devices) != 3 || snap.SweepsCompleted != 1 {
		t.Fatalf("engine snapshot holds %d devices after %d sweeps", len(snap.Devices), snap.SweepsCompleted)
	}
}

func TestEngineMarksStalledOperationsEverySweep(t *testing.T) {
	rig := newEngineRig(t, newFarm(1))
	for i := 0; i < 3; i++ {
		if _, err := rig.engine.Once(context.Background()); err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
	}
	if rig.repos.stalledMarked != 3 {
		t.Fatalf("MarkStalled ran %d times over three sweeps", rig.repos.stalledMarked)
	}
}

func TestEngineBudgetsCountRebootsAndRotations(t *testing.T) {
	f := newFarm(2)
	rig := newEngineRig(t, f)
	ctx := context.Background()

	if err := rig.repos.Operations().Create(ctx, domain.Operation{
		ID: "op-old-reboot", Kind: domain.OpReboot,
		SubjectType: domain.SubjectDongle, SubjectID: dongleID(1),
		State: domain.OpSucceeded, Trigger: domain.TriggerAutoRecovery,
		StartedAt:  domain.UnixMillis(baseTime.Add(-time.Hour)),
		FinishedAt: ptr(domain.UnixMillis(baseTime.Add(-time.Hour))),
	}); err != nil {
		t.Fatalf("seed reboot: %v", err)
	}
	if err := rig.repos.Rotations().Create(ctx, domain.Rotation{
		ID: "r1", OperationID: "op-old-reboot", ProxyID: proxyID(2),
		RequestedAt: domain.UnixMillis(baseTime.Add(-30 * time.Second)),
		Result:      domain.RotationChanged, Trigger: domain.TriggerCustomerAPI,
	}); err != nil {
		t.Fatalf("seed rotation: %v", err)
	}

	res, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.World.Budgets.RebootUsed[dongleID(1)] != 1 {
		t.Fatalf("reboot budget counted %d for %s", res.World.Budgets.RebootUsed[dongleID(1)], dongleID(1))
	}
	if res.World.Budgets.LastRotateAt[proxyID(2)].IsZero() {
		t.Fatal("the last rotation time was not loaded, so the minimum rotate interval cannot be enforced")
	}
	if res.World.Budgets.RebootAllowed(dongleID(1), baseTime.Add(-50*time.Minute)) {
		t.Fatal("a reboot inside the cooldown was allowed")
	}
	if res.World.Budgets.RotateAllowed(proxyID(2), baseTime) {
		t.Fatal("a rotate inside the minimum interval was allowed")
	}
}

func TestEngineCountsRotatesInFlight(t *testing.T) {
	f := newFarm(2)
	f.liveOp(domain.OpRotate, domain.SubjectProxy, proxyID(1))
	rig := newEngineRig(t, f)

	res, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.World.Budgets.RotateInFlight != 1 {
		t.Fatalf("RotateInFlight is %d, want 1", res.World.Budgets.RotateInFlight)
	}
}

func TestEngineReportsFailedActionsWithoutAborting(t *testing.T) {
	f := newFarm(3)
	for _, s := range sortedSlots(f.slots) {
		f.obs.ProxyStatus[s] = proxysup.Status{Unit: s.ProxyUnit()}
	}
	rig := newEngineRig(t, f)
	for _, s := range sortedSlots(f.slots) {
		rig.proxy.status[s] = proxysup.Status{Unit: s.ProxyUnit()}
	}
	rig.proxy.applyErr = errors.New("3proxy exited 0 without binding a listener")

	res, err := rig.engine.Once(context.Background())
	if err != nil {
		t.Fatalf("Once must not abort on an actuator failure: %v", err)
	}
	if res.Failed != 3 {
		t.Fatalf("%d of 3 failing actions were reported", res.Failed)
	}
	if len(res.Outcomes) != 3 {
		t.Fatalf("%d outcomes were recorded", len(res.Outcomes))
	}
}

func ptr[T any](v T) *T { return &v }
