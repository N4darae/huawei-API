package reconcile

import (
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestActionHelpersDescribeThePlan(t *testing.T) {
	as := Actions{
		act(ActApplyNetcfg, domain.SubjectSlot, "s01", 1, ReasonLinkMissing, nil),
		act(ActApplyProxy, domain.SubjectProxy, "p02", 2, ReasonProxyNotRunning, nil),
		act(ActRecoverRotate, domain.SubjectProxy, "p03", 3, ReasonDataSessionDown, nil),
		act(ActRebootDongle, domain.SubjectDongle, "d04", 4, ReasonDongleUnreachable, nil),
	}

	kinds := as.Kinds()
	if len(kinds) != 4 || kinds[0] != ActApplyNetcfg || kinds[3] != ActRebootDongle {
		t.Fatalf("Kinds returned %v", kinds)
	}
	targets := as.Targets()
	if targets[0] != "slot:s01" || targets[3] != "dongle:d04" {
		t.Fatalf("Targets returned %v", targets)
	}
	counts := as.CountByKind()
	if counts[ActApplyNetcfg] != 1 || counts[ActRecoverRotate] != 1 {
		t.Fatalf("CountByKind returned %v", counts)
	}
	if got := len(as.Destructive()); got != 2 {
		t.Fatalf("Destructive returned %d actions, want the rotate and the reboot", got)
	}
	if got := len(Destructive(as)); got != 2 {
		t.Fatalf("the package level Destructive returned %d actions", got)
	}
	if got := len(as.OfKind(ActApplyNetcfg, ActApplyProxy)); got != 2 {
		t.Fatalf("OfKind returned %d actions", got)
	}
}

func TestEveryActionKindIsPlannableAndClassified(t *testing.T) {
	destructive := map[ActionKind]bool{
		ActRecoverRotate: true,
		ActRebootDongle:  true,
		ActEvictProxy:    true,
	}
	for _, k := range AllActionKinds() {
		if k.Destructive() != destructive[k] {
			t.Errorf("%s reports Destructive()=%v", k, k.Destructive())
		}
	}
	if len(AllActionKinds()) != 9 {
		t.Fatalf("the contract publishes %d action kinds, the planner was written against 9", len(AllActionKinds()))
	}
}

func TestPlanCoversEveryActionKind(t *testing.T) {
	seen := map[ActionKind]bool{}
	for _, sc := range scenarios() {
		for _, a := range Plan(sc.build()) {
			seen[a.Kind()] = true
		}
	}
	for _, k := range AllActionKinds() {
		if !seen[k] {
			t.Errorf("no golden scenario produces %s; it would never be exercised", k)
		}
	}
}

func TestEngineExposesItsGraceAnchors(t *testing.T) {
	f := newFarm(1)
	rig := newEngineRig(t, f)
	if !rig.engine.ProcessStartedAt().Equal(f.started) {
		t.Errorf("ProcessStartedAt is %s", rig.engine.ProcessStartedAt())
	}
	if !rig.engine.HostBootedAt().Equal(f.booted) {
		t.Errorf("HostBootedAt is %s", rig.engine.HostBootedAt())
	}
}

func TestHostBootedAtFallsBackToNow(t *testing.T) {
	now := baseTime
	got := hostBootedAt(now)
	if got.After(now) {
		t.Fatalf("host boot time %s is in the future", got)
	}
	if now.Sub(got) > 365*24*time.Hour {
		t.Fatalf("host boot time %s is implausibly old", got)
	}
}
