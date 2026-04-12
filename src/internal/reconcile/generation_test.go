package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestGenerationsBumpWhenAnOperationAppearsAndClears(t *testing.T) {
	g := NewGenerations()
	target := OpKey(domain.SubjectProxy, "p01")

	if g.Get(target) != 0 {
		t.Fatal("a fresh target must start at generation zero")
	}
	if n := g.Sync(map[string]domain.Operation{}); n != 0 {
		t.Fatalf("an empty sync changed %d generations", n)
	}

	op := domain.Operation{ID: "op1", SubjectType: domain.SubjectProxy, SubjectID: "p01"}
	if n := g.Sync(map[string]domain.Operation{target: op}); n != 1 {
		t.Fatalf("an appearing operation changed %d generations", n)
	}
	afterStart := g.Get(target)
	if afterStart == 0 {
		t.Fatal("the generation did not move when the operation appeared")
	}
	if n := g.Sync(map[string]domain.Operation{target: op}); n != 0 {
		t.Fatalf("resyncing the same operation changed %d generations", n)
	}

	if n := g.Sync(map[string]domain.Operation{}); n != 1 {
		t.Fatalf("a finishing operation changed %d generations", n)
	}
	if g.Get(target) == afterStart {
		t.Fatal("the generation did not move when the operation finished")
	}
}

func TestGenerationsFenceStaleActions(t *testing.T) {
	g := NewGenerations()
	action := act(ActRecoverRotate, domain.SubjectProxy, "p01", 1, ReasonDataSessionDown, nil)

	snapshot := g.Snapshot([]Action{action})
	if g.Fenced(action, snapshot) {
		t.Fatal("an action planned against the current generation must not be fenced")
	}

	g.Bump(action.Target())
	if !g.Fenced(action, snapshot) {
		t.Fatal("an action planned before an operation started must be fenced")
	}
}

func TestGenerationsFenceActionsMissingFromTheSnapshot(t *testing.T) {
	g := NewGenerations()
	action := act(ActRebootDongle, domain.SubjectDongle, "d01", 1, ReasonDongleUnreachable, nil)
	if !g.Fenced(action, map[string]uint64{}) {
		t.Fatal("an action that was never snapshotted must be treated as stale")
	}
}

func TestGenerationsAreIndependentPerTarget(t *testing.T) {
	g := NewGenerations()
	a := act(ActRecoverRotate, domain.SubjectProxy, "p01", 1, "", nil)
	b := act(ActRecoverRotate, domain.SubjectProxy, "p02", 2, "", nil)

	snapshot := g.Snapshot([]Action{a, b})
	g.Bump(a.Target())

	if !g.Fenced(a, snapshot) {
		t.Error("the bumped target is not fenced")
	}
	if g.Fenced(b, snapshot) {
		t.Error("an unrelated target was fenced; one busy dongle must not stall the other 47")
	}
}

func TestGenerationsAreSafeUnderConcurrency(t *testing.T) {
	g := NewGenerations()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				g.Bump("proxy:p01")
				g.Get("proxy:p01")
				g.Sync(map[string]domain.Operation{
					"proxy:p01": {ID: "op1", SubjectType: domain.SubjectProxy, SubjectID: "p01"},
				})
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if g.Get("proxy:p01") == 0 {
		t.Fatal("concurrent bumps were lost")
	}
}

func TestEngineHoldsGraceWhenASweepCouldNotComplete(t *testing.T) {
	f := newFarm(4)
	f.started = baseTime.Add(-2 * time.Hour)
	rig := newEngineRig(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := rig.engine.Once(ctx)
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if res.World.CacheWarm {
		t.Fatal("a cancelled sweep must leave CacheWarm false")
	}
	if !res.World.InStartupGrace() {
		t.Fatal("a cold cache must hold grace open even when the process grace has elapsed")
	}
}

func TestSortedTargetsIsStable(t *testing.T) {
	in := map[string]uint64{"proxy:p03": 1, "dongle:d01": 2, "proxy:p01": 3}
	got := sortedTargets(in)
	want := []string{"dongle:d01", "proxy:p01", "proxy:p03"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedTargets returned %v, want %v", got, want)
		}
	}
}
