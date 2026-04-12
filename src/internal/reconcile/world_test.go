package reconcile

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/secrets"
	"github.com/n4darae/huawei-API/src/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	kek, err := secrets.GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	sealer, err := secrets.NewSealer(kek)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "dongled.db"), sealer,
		store.WithClock(newTestClock(baseTime)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func seedRealFarm(t *testing.T, s *store.Store, slots int) {
	t.Helper()
	ctx := context.Background()

	if err := s.Nodes().Upsert(ctx, domain.Node{
		ID: testNodeID, Name: "local", Kind: domain.NodeKindLocal, PublicHost: testPublicHost,
	}); err != nil {
		t.Fatalf("Upsert node: %v", err)
	}
	for i := 1; i <= slots; i++ {
		sl := domain.Slot(i)
		if err := s.Dongles().Create(ctx, domain.Dongle{
			ID: dongleID(sl), NodeID: testNodeID, IMEI: "8600000000000" + sl.String(),
			AutoRecoverEnabled: true,
		}); err != nil {
			t.Fatalf("Create dongle %d: %v", i, err)
		}
		dg := dongleID(sl)
		if err := s.Slots().Create(ctx, domain.SlotRow{
			ID: slotID(sl), NodeID: testNodeID, Slot: sl,
			USBPath: "1-13." + sl.String(), IfName: sl.IfaceName(), DongleID: &dg,
		}); err != nil {
			t.Fatalf("Create slot %d: %v", i, err)
		}
		if err := s.Proxies().Create(ctx, domain.Proxy{
			ID: proxyID(sl), SlotID: slotID(sl), Enabled: true,
			SocksPort: sl.SocksPort(), HTTPPort: sl.HTTPPort(),
			Username: "cust_" + sl.String(), Password: "Kq7mZr2xTn9wLb4V",
			AuthMode: domain.AuthUserPass, Policy: domain.DefaultProxyPolicy(),
		}); err != nil {
			t.Fatalf("Create proxy %d: %v", i, err)
		}
	}
}

func TestLoadDesiredReadsTheWholeNode(t *testing.T) {
	s := openStore(t)
	seedRealFarm(t, s, 3)
	ctx := context.Background()

	if err := s.Proxies().AddAuthIP(ctx, domain.ProxyAuthIP{
		ID: "a1", ProxyID: proxyID(1), CIDR: netip.MustParsePrefix("203.0.113.5/32"),
	}); err != nil {
		t.Fatalf("AddAuthIP: %v", err)
	}

	desired, err := LoadDesired(ctx, s, testNodeID)
	if err != nil {
		t.Fatalf("LoadDesired: %v", err)
	}
	if desired.Node.PublicHost != testPublicHost {
		t.Errorf("node public host is %s", desired.Node.PublicHost)
	}
	if len(desired.Slots) != 3 || len(desired.Dongles) != 3 || len(desired.Proxies) != 3 {
		t.Fatalf("loaded %d slots, %d dongles, %d proxies",
			len(desired.Slots), len(desired.Dongles), len(desired.Proxies))
	}
	if got := desired.AuthIPs[proxyID(1)]; len(got) != 1 || got[0].String() != "203.0.113.5/32" {
		t.Errorf("auth ips for %s are %v", proxyID(1), got)
	}
	if desired.Proxies[proxyID(1)].Password != "Kq7mZr2xTn9wLb4V" {
		t.Error("the proxy password was not decrypted on the way out of the store")
	}

	rows := desired.SlotRows()
	for i, row := range rows {
		if row.Slot != domain.Slot(i+1) {
			t.Fatalf("SlotRows is not ordered: index %d holds slot %d", i, int(row.Slot))
		}
	}
}

func TestLoadDesiredOnAMissingNodeIsNotFound(t *testing.T) {
	s := openStore(t)
	if _, err := LoadDesired(context.Background(), s, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LoadDesired on a missing node returned %v", err)
	}
}

func TestLoadDesiredResolvesADeadStick(t *testing.T) {
	s := openStore(t)
	seedRealFarm(t, s, 2)
	ctx := context.Background()

	if err := s.Slots().Detach(ctx, slotID(1)); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	desired, err := LoadDesired(ctx, s, testNodeID)
	if err != nil {
		t.Fatalf("LoadDesired: %v", err)
	}
	row := desired.Slots[1]
	if row.Occupied() {
		t.Fatal("the detached slot still reports a dongle")
	}
	if _, ok := desired.DongleFor(row); ok {
		t.Fatal("DongleFor resolved a dongle for an empty slot")
	}
	px, ok := desired.ProxiesBySlot()[slotID(1)]
	if !ok || px.ID != proxyID(1) {
		t.Fatal("the proxy could not resolve its own slot after the stick died")
	}
}

func TestLoadActiveOpsKeysBySubject(t *testing.T) {
	s := openStore(t)
	seedRealFarm(t, s, 2)
	ctx := context.Background()

	if err := s.Operations().Create(ctx, domain.Operation{
		ID: "op1", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: proxyID(1),
		State: domain.OpRunning, Trigger: domain.TriggerCustomerAPI,
		StartedAt: domain.UnixMillis(baseTime), DeadlineAt: domain.UnixMillis(baseTime.Add(90 * time.Second)),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	active, err := LoadActiveOps(ctx, s)
	if err != nil {
		t.Fatalf("LoadActiveOps: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("loaded %d active operations", len(active))
	}
	if _, ok := active[OpKey(domain.SubjectProxy, proxyID(1))]; !ok {
		t.Fatalf("active operations are keyed as %v", active)
	}
}

func TestLoadBudgetsFromTheRealStore(t *testing.T) {
	s := openStore(t)
	seedRealFarm(t, s, 2)
	ctx := context.Background()

	dayStart := baseTime.UTC().Truncate(24 * time.Hour)
	rebootAt := dayStart.Add(time.Hour)
	finished := domain.UnixMillis(rebootAt.Add(time.Minute))
	if err := s.Operations().Create(ctx, domain.Operation{
		ID: "op-reboot", Kind: domain.OpReboot, SubjectType: domain.SubjectDongle, SubjectID: dongleID(1),
		State: domain.OpSucceeded, Trigger: domain.TriggerAutoRecovery,
		StartedAt: domain.UnixMillis(rebootAt), DeadlineAt: domain.UnixMillis(rebootAt.Add(2 * time.Minute)),
		FinishedAt: &finished,
	}); err != nil {
		t.Fatalf("Create reboot operation: %v", err)
	}
	if err := s.Operations().Create(ctx, domain.Operation{
		ID: "op-rotate", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: proxyID(2),
		State: domain.OpRunning, Trigger: domain.TriggerAutoRecovery,
		StartedAt: domain.UnixMillis(baseTime), DeadlineAt: domain.UnixMillis(baseTime.Add(90 * time.Second)),
	}); err != nil {
		t.Fatalf("Create rotate operation: %v", err)
	}
	if err := s.Rotations().Create(ctx, domain.Rotation{
		ID: "r1", OperationID: "op-rotate", ProxyID: proxyID(2),
		RequestedAt: domain.UnixMillis(baseTime.Add(-30 * time.Second)),
		Result:      domain.RotationChanged, Trigger: domain.TriggerAutoRecovery,
	}); err != nil {
		t.Fatalf("Create rotation: %v", err)
	}

	active, err := LoadActiveOps(ctx, s)
	if err != nil {
		t.Fatalf("LoadActiveOps: %v", err)
	}
	b, err := LoadBudgets(ctx, s, BudgetPolicy{
		RebootPerDay: 4, RebootCooldown: 30 * time.Minute,
		MaxConcurrentRotate: 4, MinRotateInterval: 60 * time.Second,
	}, baseTime, active)
	if err != nil {
		t.Fatalf("LoadBudgets: %v", err)
	}

	if b.RebootUsed[dongleID(1)] != 1 {
		t.Errorf("reboot budget counted %d", b.RebootUsed[dongleID(1)])
	}
	if !b.LastRebootAt[dongleID(1)].Equal(rebootAt) {
		t.Errorf("last reboot is %s, want %s", b.LastRebootAt[dongleID(1)], rebootAt)
	}
	if b.RotateInFlight != 1 {
		t.Errorf("RotateInFlight is %d", b.RotateInFlight)
	}
	if b.RotateAllowed(proxyID(2), baseTime) {
		t.Error("a rotate 30s after the previous one was allowed under a 60s minimum")
	}
	if !b.RotateAllowed(proxyID(1), baseTime) {
		t.Error("a proxy that never rotated was refused")
	}
	if !b.RebootAllowed(dongleID(1), rebootAt.Add(31*time.Minute)) {
		t.Error("a reboot outside the cooldown was refused")
	}
}

func TestProxiesBySlotPicksDeterministically(t *testing.T) {
	d := DesiredState{Proxies: map[string]domain.Proxy{
		"pz": {ID: "pz", SlotID: "s01"},
		"pa": {ID: "pa", SlotID: "s01"},
		"pm": {ID: "pm", SlotID: "s01"},
	}}
	for i := 0; i < 50; i++ {
		if got := d.ProxiesBySlot()["s01"].ID; got != "pa" {
			t.Fatalf("run %d picked %q; a malformed world must still plan deterministically", i, got)
		}
	}
}

func TestEngineAgainstTheRealStore(t *testing.T) {
	s := openStore(t)
	seedRealFarm(t, s, 4)

	f := newFarm(4)
	rig := newEngineRig(t, f)
	rig.engine.deps.Repos = s
	actuator, err := NewActuator(ActuatorDeps{
		Net: rig.net, FW: rig.fw, Proxy: rig.proxy, Dev: rig.dev,
		Repos: s, Ops: rig.ops, Clock: rig.clock,
	})
	if err != nil {
		t.Fatalf("NewActuator: %v", err)
	}
	rig.engine.actuator = actuator
	ctx := context.Background()

	if err := rig.engine.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	res, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("%d actions failed against the real store: %+v", res.Failed, res.Outcomes)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("a converged farm planned %v", Actions(res.Actions).Kinds())
	}
}

func TestEngineMarksExpiryInTheRealStore(t *testing.T) {
	s := openStore(t)
	seedRealFarm(t, s, 2)
	ctx := context.Background()

	expired := domain.UnixMillis(baseTime.Add(-time.Hour))
	if err := s.Proxies().SetCustomer(ctx, proxyID(1), nil, &expired); err != nil {
		t.Fatalf("SetCustomer: %v", err)
	}

	f := newFarm(2)
	rig := newEngineRig(t, f)
	rig.engine.deps.Repos = s
	actuator, err := NewActuator(ActuatorDeps{
		Net: rig.net, FW: rig.fw, Proxy: rig.proxy, Dev: rig.dev,
		Repos: s, Ops: rig.ops, Clock: rig.clock,
	})
	if err != nil {
		t.Fatalf("NewActuator: %v", err)
	}
	rig.engine.actuator = actuator

	res, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("%d actions failed: %+v", res.Failed, res.Outcomes)
	}

	px, err := s.Proxies().Get(ctx, proxyID(1))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if px.Enabled {
		t.Fatal("the expired proxy is still enabled in the database")
	}
	_, _, evicted := rig.proxy.calls()
	if len(evicted) != 1 {
		t.Fatalf("evicted %v", evicted)
	}
}
