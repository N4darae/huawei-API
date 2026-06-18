package rotate

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestDefaultPolicyMatchesTheFrozenTimings(t *testing.T) {
	p := DefaultPolicy()
	if p.HardDeadline != 90*time.Second {
		t.Errorf("hard deadline is %s, want 90s", p.HardDeadline)
	}
	if p.WaitConnect != 45*time.Second {
		t.Errorf("wait connect is %s, want 45s", p.WaitConnect)
	}
	if p.PollInterval != time.Second {
		t.Errorf("poll interval is %s, want 1s", p.PollInterval)
	}
	if p.VerifyTimeout != 10*time.Second {
		t.Errorf("verify timeout is %s, want 10s", p.VerifyTimeout)
	}
	if p.MaxAttempts != 3 {
		t.Errorf("max attempts is %d, want 3", p.MaxAttempts)
	}
	if p.RebootBudgetPerDay != 4 {
		t.Errorf("reboot budget is %d, want 4", p.RebootBudgetPerDay)
	}
	if p.RebootCooldown != 30*time.Minute {
		t.Errorf("reboot cooldown is %s, want 30m", p.RebootCooldown)
	}
	if p.MinInterval != 60*time.Second {
		t.Errorf("min interval is %s, want 60s", p.MinInterval)
	}
	if p.MaxConcurrent != 4 {
		t.Errorf("farm cap is %d, want 4", p.MaxConcurrent)
	}
	want := []time.Duration{6 * time.Second, 15 * time.Second, 40 * time.Second}
	if !reflect.DeepEqual(p.HoldEscalate, want) {
		t.Errorf("hold ladder is %v, want %v", p.HoldEscalate, want)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("the default policy does not validate: %v", err)
	}
}

func TestTheHoldLadderLivesInConfigAndIsMarkedProvisional(t *testing.T) {
	fromConfig := config.Default().Carrier.HoldEscalate
	provisional := ProvisionalHoldLadderPendingA3()
	if !reflect.DeepEqual(provisional, fromConfig) {
		t.Fatalf("the provisional ladder %v drifted from config %v", provisional, fromConfig)
	}
	provisional[0] = time.Hour
	if config.Default().Carrier.HoldEscalate[0] == time.Hour {
		t.Fatal("mutating the returned ladder reached back into the frozen config")
	}
}

func TestLadderHasNoNetModeRungAndEndsInAReboot(t *testing.T) {
	p := DefaultPolicy()
	rungs := p.Ladder()
	if len(rungs) != p.MaxAttempts+1 {
		t.Fatalf("ladder has %d rungs, want %d holds plus one reboot", len(rungs), p.MaxAttempts)
	}
	for i, r := range rungs[:p.MaxAttempts] {
		if r.Reboot {
			t.Fatalf("rung %d is a reboot, want a hold", i+1)
		}
		if r.Hold != p.HoldEscalate[i] {
			t.Fatalf("rung %d holds %s, want %s", i+1, r.Hold, p.HoldEscalate[i])
		}
	}
	last := rungs[len(rungs)-1]
	if !last.Reboot || last.Hold != 0 {
		t.Fatalf("the last rung is %+v, want the reboot rung", last)
	}
}

func TestHoldForClampsToTheLastRung(t *testing.T) {
	p := DefaultPolicy()
	if got := p.HoldFor(0); got != p.HoldEscalate[0] {
		t.Errorf("HoldFor(0) is %s, want the first rung", got)
	}
	if got := p.HoldFor(99); got != p.HoldEscalate[len(p.HoldEscalate)-1] {
		t.Errorf("HoldFor(99) is %s, want the last rung", got)
	}
}

func TestAttemptFitsRespectsTheHardDeadline(t *testing.T) {
	p := DefaultPolicy()
	if p.AttemptFits(20*time.Second, 3) {
		t.Error("a 40s hold plus verification was allowed inside a 20s remaining budget")
	}
	if !p.AttemptFits(60*time.Second, 3) {
		t.Error("a 40s hold plus verification was refused inside a 60s remaining budget")
	}
}

func TestPolicyValidateRejectsUnusablePolicies(t *testing.T) {
	cases := map[string]func(p *Policy){
		"empty ladder":   func(p *Policy) { p.HoldEscalate = nil },
		"zero rung":      func(p *Policy) { p.HoldEscalate = []time.Duration{0} },
		"no deadline":    func(p *Policy) { p.HardDeadline = 0 },
		"no poll":        func(p *Policy) { p.PollInterval = 0 },
		"no attempts":    func(p *Policy) { p.MaxAttempts = 0 },
		"no concurrency": func(p *Policy) { p.MaxConcurrent = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := DefaultPolicy()
			mutate(&p)
			if err := p.Validate(); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("Validate returned %v, want ErrInvalid", err)
			}
		})
	}
}

func TestPolicyForCarrierOverridesOnlyWhatTheProfileSets(t *testing.T) {
	base := DefaultPolicy()
	p := PolicyForCarrier(domain.CarrierProfile{
		Name:         "beeline",
		HoldEscalate: []time.Duration{2 * time.Second},
		WaitConnect:  20 * time.Second,
	})
	if !reflect.DeepEqual(p.HoldEscalate, []time.Duration{2 * time.Second}) {
		t.Errorf("ladder is %v, want the carrier override", p.HoldEscalate)
	}
	if p.WaitConnect != 20*time.Second {
		t.Errorf("wait connect is %s, want the carrier override", p.WaitConnect)
	}
	if p.HardDeadline != base.HardDeadline || p.MaxConcurrent != base.MaxConcurrent {
		t.Errorf("unset carrier fields overwrote the defaults: %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("a carrier policy does not validate: %v", err)
	}
}

func TestDefaultCarrierProfileValidates(t *testing.T) {
	c := DefaultCarrierProfile()
	if err := c.Validate(); err != nil {
		t.Fatalf("the default carrier profile does not validate: %v", err)
	}
	if c.Name == "" {
		t.Fatal("the default carrier profile has no name")
	}
}

func TestPolicyForCarrierNameFallsBackToTheDefault(t *testing.T) {
	profiles := []domain.CarrierProfile{{Name: "beeline", HoldEscalate: []time.Duration{time.Second}}}
	if got := PolicyForCarrierName("beeline", profiles); got.HoldEscalate[0] != time.Second {
		t.Errorf("named lookup returned %v", got.HoldEscalate)
	}
	if got := PolicyForCarrierName("nope", profiles); !reflect.DeepEqual(got.HoldEscalate, DefaultPolicy().HoldEscalate) {
		t.Errorf("unknown carrier returned %v, want the default ladder", got.HoldEscalate)
	}
}

func TestStepPctIsMonotonicAcrossThePublicSequence(t *testing.T) {
	prev := -1
	for _, s := range domain.RotateSteps() {
		got := StepPct(s)
		if got <= prev {
			t.Fatalf("step %q maps to %d, which does not advance past %d", s, got, prev)
		}
		prev = got
	}
	if prev != 100 {
		t.Fatalf("the last step maps to %d, want 100", prev)
	}
}
