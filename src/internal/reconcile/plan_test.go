package reconcile

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden action fixtures in testdata")

func goldenPath(name string) string { return filepath.Join("testdata", name+".json") }

func assertGolden(t *testing.T, name string, actions []Action) {
	t.Helper()
	got, err := json.MarshalIndent(actions, "", "  ")
	if err != nil {
		t.Fatalf("marshal actions: %v", err)
	}
	got = append(got, '\n')

	path := goldenPath(name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run go test ./internal/reconcile -update to create it): %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("plan drifted from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

type scenario struct {
	name  string
	build func() World
}

func scenarios() []scenario {
	return []scenario{
		{"healthy_farm", func() World { return newFarm(3).world() }},
		{"cold_start_no_netcfg", func() World {
			f := newFarm(2)
			f.obs.Net.PublicSrcRules = nil
			f.obs.Net.RouteTableNamesOK = false
			for _, s := range sortedSlots(f.slots) {
				delete(f.obs.Net.Links, s.IfaceName())
				delete(f.obs.Fenced, s.IfaceName())
				f.obs.ProxyStatus[s] = proxysup.Status{Unit: s.ProxyUnit()}
				delete(f.obs.Devices, s)
			}
			f.obs.Net.Rules = nil
			f.obs.Net.Routes = map[int][]netcfg.RouteState{}
			return f.world()
		}},
		{"interface_missing_address", func() World {
			f := newFarm(1)
			link := f.obs.Net.Links["dg01"]
			link.Addrs = nil
			f.obs.Net.Links["dg01"] = link
			return f.world()
		}},
		{"routing_rule_missing", func() World {
			f := newFarm(1)
			f.obs.Net.Rules = f.obs.Net.Rules[:1]
			return f.world()
		}},
		{"default_route_missing", func() World {
			f := newFarm(1)
			f.obs.Net.Routes = map[int][]netcfg.RouteState{}
			return f.world()
		}},
		{"firewall_does_not_know_interface", func() World {
			f := newFarm(2)
			delete(f.obs.Fenced, "dg02")
			return f.world()
		}},
		{"max_idle_would_drop_the_session", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) { d.MaxIdleTime = device.MaxIdleTimeDefault })
			return f.world()
		}},
		{"proxy_not_running_cold_starts", func() World {
			f := newFarm(1)
			f.proxyStatus(1, func(st *proxysup.Status) { *st = proxysup.Status{Unit: st.Unit} })
			return f.world()
		}},
		{"proxy_running_but_not_bound", func() World {
			f := newFarm(1)
			f.proxyStatus(1, func(st *proxysup.Status) { st.SocksBound = false })
			return f.world()
		}},
		{"proxy_bound_but_probe_failed", func() World {
			f := newFarm(1)
			f.proxyStatus(1, func(st *proxysup.Status) { st.ProbeOK = false })
			return f.world()
		}},
		{"proxy_disabled_stops", func() World {
			f := newFarm(1)
			f.proxy(1, func(p *domain.Proxy) { p.Enabled = false })
			return f.world()
		}},
		{"proxy_suspended_evicts", func() World {
			f := newFarm(1)
			f.proxy(1, func(p *domain.Proxy) { p.Suspended = true })
			return f.world()
		}},
		{"proxy_expired_marks_and_evicts", func() World {
			f := newFarm(1)
			expired := domain.UnixMillis(baseTime.Add(-time.Hour))
			f.proxy(1, func(p *domain.Proxy) { p.ExpiresAt = &expired })
			return f.world()
		}},
		{"dead_stick_stops_the_proxy", func() World {
			f := newFarm(1)
			f.detach(1)
			delete(f.obs.Devices, 1)
			return f.world()
		}},
		{"data_session_down_recovers", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
			return f.world()
		}},
		{"data_session_down_but_auto_recover_off", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
			f.dongle(1, func(d *domain.Dongle) { d.AutoRecoverEnabled = false })
			return f.world()
		}},
		{"unreachable_dongle_reboots", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) {
				d.Reachable = false
				d.ObservedAt = baseTime.Add(-5 * time.Minute)
				d.Err = "hilink: unreachable"
			})
			return f.world()
		}},
		{"sim_locked_never_recovers", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) {
				d.Conn = device.ConnDisconnected
				d.Sim = device.SimStatePINRequired
			})
			return f.world()
		}},
		{"login_required_never_recovers", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) {
				d.Conn = device.ConnDisconnected
				d.LoginNeeded = true
			})
			return f.world()
		}},
		{"live_operation_fences_the_dongle", func() World {
			f := newFarm(2)
			f.device(1, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
			f.proxyStatus(1, func(st *proxysup.Status) { st.ProbeOK = false })
			f.liveOp(domain.OpRotate, domain.SubjectDongle, dongleID(1))
			f.device(2, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
			return f.world()
		}},
		{"startup_grace_drops_destruction", func() World {
			f := newFarm(2)
			f.started = baseTime.Add(-90 * time.Second)
			for _, s := range sortedSlots(f.slots) {
				f.device(s, func(d *DeviceObservation) {
					d.Reachable = false
					d.ObservedAt = baseTime.Add(-time.Hour)
				})
			}
			return f.world()
		}},
		{"rotate_budget_exhausted", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
			f.budgets.LastRotateAt[proxyID(1)] = baseTime.Add(-10 * time.Second)
			return f.world()
		}},
		{"reboot_budget_exhausted", func() World {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) {
				d.Reachable = false
				d.ObservedAt = baseTime.Add(-5 * time.Minute)
			})
			f.budgets.RebootUsed[dongleID(1)] = 4
			return f.world()
		}},
	}
}

func TestPlanGoldenFixtures(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			assertGolden(t, sc.name, Plan(sc.build()))
		})
	}
}

func TestHealthyFarmPlansNothing(t *testing.T) {
	got := Plan(newFarm(48).world())
	if len(got) != 0 {
		t.Fatalf("a converged farm produced %d actions: %v", len(got), Actions(got).Kinds())
	}
}

func TestStartupGraceAfterAPanelRestartEmitsNoDestruction(t *testing.T) {
	f := newFarm(48)
	f.booted = baseTime.Add(-72 * time.Hour)
	f.started = baseTime.Add(-90 * time.Second)
	for _, s := range sortedSlots(f.slots) {
		f.device(s, func(d *DeviceObservation) {
			d.Reachable = false
			d.Conn = device.ConnDisconnected
			d.ObservedAt = baseTime.Add(-2 * time.Hour)
			d.Err = "hilink: unreachable"
		})
	}
	w := f.world()

	if !w.InStartupGrace() {
		t.Fatal("grace must be measured from max(host boot, process start); a day three restart is still in grace")
	}
	got := Actions(Plan(w))
	if n := len(got.Destructive()); n != 0 {
		t.Fatalf("48 unreachable dongles 90s after a panel restart produced %d destructive actions: %v",
			n, got.Destructive().Kinds())
	}
}

func TestStartupGraceHoldsUntilTheCacheIsWarm(t *testing.T) {
	f := newFarm(48)
	f.booted = baseTime.Add(-72 * time.Hour)
	f.started = baseTime.Add(-2 * time.Hour)
	f.warm = false
	f.obs.SweepsCompleted = 0
	for _, s := range sortedSlots(f.slots) {
		f.device(s, func(d *DeviceObservation) {
			d.Reachable = false
			d.ObservedAt = baseTime.Add(-2 * time.Hour)
		})
	}
	w := f.world()

	if !w.InStartupGrace() {
		t.Fatal("an empty observation cache must hold grace open even long after the process started")
	}
	if n := len(Actions(Plan(w)).Destructive()); n != 0 {
		t.Fatalf("a cold cache produced %d destructive actions", n)
	}

	f.warm = true
	f.obs.SweepsCompleted = 1
	w = f.world()
	if w.InStartupGrace() {
		t.Fatal("grace must lift once one full sweep completed and the process grace elapsed")
	}
	if n := len(Actions(Plan(w)).Destructive()); n != 48 {
		t.Fatalf("after grace the same farm produced %d destructive actions, want 48", n)
	}
}

func TestHostRebootGraceStillApplies(t *testing.T) {
	f := newFarm(48)
	f.booted = baseTime.Add(-30 * time.Second)
	f.started = baseTime.Add(-30 * time.Second)
	for _, s := range sortedSlots(f.slots) {
		f.device(s, func(d *DeviceObservation) {
			d.Reachable = false
			d.ObservedAt = baseTime.Add(-2 * time.Hour)
		})
	}
	if n := len(Actions(Plan(f.world())).Destructive()); n != 0 {
		t.Fatalf("30s after a host reboot the farm produced %d destructive actions", n)
	}
}

func TestGenerationFencingSkipsEveryActionOnTheSubject(t *testing.T) {
	subjects := []struct {
		name  string
		apply func(*farm)
	}{
		{"dongle", func(f *farm) { f.liveOp(domain.OpRotate, domain.SubjectDongle, dongleID(1)) }},
		{"proxy", func(f *farm) { f.liveOp(domain.OpSetAuth, domain.SubjectProxy, proxyID(1)) }},
		{"slot", func(f *farm) { f.liveOp(domain.OpSetLanIP, domain.SubjectSlot, slotID(1)) }},
	}
	for _, sub := range subjects {
		t.Run(sub.name, func(t *testing.T) {
			f := newFarm(1)
			delete(f.obs.Net.Links, "dg01")
			f.obs.Net.Rules = nil
			f.proxyStatus(1, func(st *proxysup.Status) { *st = proxysup.Status{Unit: st.Unit} })
			f.device(1, func(d *DeviceObservation) {
				d.Conn = device.ConnDisconnected
				d.MaxIdleTime = device.MaxIdleTimeDefault
			})

			if n := len(Plan(f.world())); n == 0 {
				t.Fatal("the unfenced world must produce work, otherwise the fence proves nothing")
			}
			sub.apply(f)
			if got := Plan(f.world()); len(got) != 0 {
				t.Fatalf("a live operation on the %s left %d actions: %v",
					sub.name, len(got), Actions(got).Kinds())
			}
		})
	}
}

func TestGenerationFencingIsPerSubjectNotGlobal(t *testing.T) {
	f := newFarm(2)
	for _, s := range []domain.Slot{1, 2} {
		f.device(s, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
	}
	f.liveOp(domain.OpRotate, domain.SubjectDongle, dongleID(1))

	got := Actions(Plan(f.world()))
	rotates := got.OfKind(ActRecoverRotate)
	if len(rotates) != 1 {
		t.Fatalf("want exactly one recovery rotate, got %v", got.Kinds())
	}
	if rotates[0].Target() != OpKey(domain.SubjectProxy, proxyID(2)) {
		t.Fatalf("the wrong slot was rotated: %s", rotates[0].Target())
	}
}

func TestNodeActionIsFencedByANodeOperation(t *testing.T) {
	f := newFarm(1)
	f.obs.Net.PublicSrcRules = nil
	if len(Actions(Plan(f.world())).OfKind(ActApplyNetcfg)) == 0 {
		t.Fatal("a missing rule 900 must schedule the global netcfg apply")
	}
	f.liveOp(domain.OpEnroll, domain.SubjectNode, testNodeID)
	for _, a := range Plan(f.world()) {
		if param(a, ParamScope) == ScopeGlobal {
			t.Fatal("a live node operation must fence the global netcfg apply")
		}
	}
}

func TestExpiryMarksAndEvicts(t *testing.T) {
	f := newFarm(1)
	expired := domain.UnixMillis(baseTime.Add(-time.Second))
	f.proxy(1, func(p *domain.Proxy) { p.ExpiresAt = &expired })

	got := Actions(Plan(f.world()))
	if len(got) != 2 {
		t.Fatalf("expiry produced %v, want mark_expired then evict_proxy", got.Kinds())
	}
	if got[0].Kind() != ActMarkExpired || got[1].Kind() != ActEvictProxy {
		t.Fatalf("expiry produced %v in the wrong order", got.Kinds())
	}
	if param(got[0], ParamExpiresAt) != i64toa(expired) {
		t.Errorf("mark_expired lost the expiry timestamp: %q", param(got[0], ParamExpiresAt))
	}
	if param(got[1], ParamEvict) != "true" {
		t.Error("evict_proxy must ask the supervisor to kick live sessions; noforce keeps them otherwise")
	}
}

func TestExpiryConvergesOnceMarked(t *testing.T) {
	f := newFarm(1)
	expired := domain.UnixMillis(baseTime.Add(-time.Second))
	f.proxy(1, func(p *domain.Proxy) { p.ExpiresAt = &expired })

	f.proxy(1, func(p *domain.Proxy) { p.Enabled = false })
	f.proxyStatus(1, func(st *proxysup.Status) { *st = proxysup.Status{Unit: st.Unit} })

	if got := Plan(f.world()); len(got) != 0 {
		t.Fatalf("a marked and stopped expired proxy still produces %v", Actions(got).Kinds())
	}
}

func TestExpiryInGraceStillMarksButDoesNotEvict(t *testing.T) {
	f := newFarm(1)
	f.started = baseTime.Add(-10 * time.Second)
	expired := domain.UnixMillis(baseTime.Add(-time.Second))
	f.proxy(1, func(p *domain.Proxy) { p.ExpiresAt = &expired })

	got := Actions(Plan(f.world()))
	if len(got) != 1 || got[0].Kind() != ActMarkExpired {
		t.Fatalf("in grace expiry produced %v, want only mark_expired", got.Kinds())
	}
}

func TestFutureExpiryIsLeftAlone(t *testing.T) {
	f := newFarm(1)
	future := domain.UnixMillis(baseTime.Add(time.Hour))
	f.proxy(1, func(p *domain.Proxy) { p.ExpiresAt = &future })
	if got := Plan(f.world()); len(got) != 0 {
		t.Fatalf("a proxy that expires in an hour produced %v", Actions(got).Kinds())
	}
}

func TestRecoverRotateIsGatedOnAutoRecoverBudgetAndProxyState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*farm)
		want  int
	}{
		{"disconnected", func(*farm) {}, 1},
		{"auto recover disabled", func(f *farm) {
			f.dongle(1, func(d *domain.Dongle) { d.AutoRecoverEnabled = false })
		}, 0},
		{"inside the minimum rotate interval", func(f *farm) {
			f.budgets.LastRotateAt[proxyID(1)] = baseTime.Add(-30 * time.Second)
		}, 0},
		{"outside the minimum rotate interval", func(f *farm) {
			f.budgets.LastRotateAt[proxyID(1)] = baseTime.Add(-10 * time.Minute)
		}, 1},
		{"farm rotate concurrency reached", func(f *farm) {
			f.budgets.RotateInFlight = 4
		}, 0},
		{"proxy suspended", func(f *farm) {
			f.proxy(1, func(p *domain.Proxy) { p.Suspended = true })
		}, 0},
		{"proxy disabled", func(f *farm) {
			f.proxy(1, func(p *domain.Proxy) { p.Enabled = false })
		}, 0},
		{"slot has no proxy to rotate", func(f *farm) {
			delete(f.proxies, proxyID(1))
		}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) { d.Conn = device.ConnDisconnected })
			tc.setup(f)
			got := Actions(Plan(f.world())).OfKind(ActRecoverRotate)
			if len(got) != tc.want {
				t.Fatalf("got %d recovery rotates, want %d", len(got), tc.want)
			}
			if tc.want == 1 && param(got[0], ParamTrigger) != string(domain.TriggerAutoRecovery) {
				t.Errorf("recovery rotate carries trigger %q, want auto_recovery", param(got[0], ParamTrigger))
			}
		})
	}
}

func TestRebootIsGatedOnDwellAndBudget(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*farm)
		want  int
	}{
		{"unreachable long enough", func(*farm) {}, 1},
		{"unreachable only briefly", func(f *farm) {
			f.device(1, func(d *DeviceObservation) { d.ObservedAt = baseTime.Add(-5 * time.Second) })
		}, 0},
		{"daily budget spent", func(f *farm) { f.budgets.RebootUsed[dongleID(1)] = 4 }, 0},
		{"inside the cooldown", func(f *farm) {
			f.budgets.LastRebootAt[dongleID(1)] = baseTime.Add(-5 * time.Minute)
		}, 0},
		{"outside the cooldown", func(f *farm) {
			f.budgets.LastRebootAt[dongleID(1)] = baseTime.Add(-45 * time.Minute)
		}, 1},
		{"auto recover disabled", func(f *farm) {
			f.dongle(1, func(d *domain.Dongle) { d.AutoRecoverEnabled = false })
		}, 0},
		{"sim is pin locked", func(f *farm) {
			f.device(1, func(d *DeviceObservation) { d.Sim = device.SimStatePUKRequired })
		}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFarm(1)
			f.device(1, func(d *DeviceObservation) {
				d.Reachable = false
				d.ObservedAt = baseTime.Add(-5 * time.Minute)
			})
			tc.setup(f)
			if got := len(Actions(Plan(f.world())).OfKind(ActRebootDongle)); got != tc.want {
				t.Fatalf("got %d reboots, want %d", got, tc.want)
			}
		})
	}
}

func TestUnobservedDongleIsNeverRecovered(t *testing.T) {
	f := newFarm(1)
	delete(f.obs.Devices, 1)
	if n := len(Actions(Plan(f.world())).Destructive()); n != 0 {
		t.Fatalf("a dongle the poller has never reached produced %d destructive actions", n)
	}
}

func TestProxyIsNotAppliedWithoutEgress(t *testing.T) {
	f := newFarm(1)
	delete(f.obs.Net.Links, "dg01")
	f.proxyStatus(1, func(st *proxysup.Status) { *st = proxysup.Status{Unit: st.Unit} })

	got := Actions(Plan(f.world()))
	if len(got.OfKind(ActApplyProxy)) != 0 {
		t.Fatal("3proxy cannot bind external before the interface carries its address; apply_proxy must wait")
	}
	if len(got.OfKind(ActApplyNetcfg)) != 1 {
		t.Fatalf("want the netcfg apply that fixes the interface, got %v", got.Kinds())
	}
}

func TestFirewallActionStopsOnceTheSetKnowsTheInterface(t *testing.T) {
	f := newFarm(1)
	delete(f.obs.Fenced, "dg01")
	got := Actions(Plan(f.world())).OfKind(ActAddFwDongle)
	if len(got) != 1 {
		t.Fatalf("an interface missing from the dongle set must be added, got %v", got.Kinds())
	}
	if param(got[0], ParamGateway) != domain.Slot(1).GatewayIP().String() {
		t.Errorf("add_fw_dongle carries gateway %q", param(got[0], ParamGateway))
	}

	f.obs.Fenced["dg01"] = false
	if n := len(Actions(Plan(f.world())).OfKind(ActAddFwDongle)); n != 0 {
		t.Fatalf("the firewall action repeated %d times after the set knew the interface", n)
	}
}

func TestFirewallActionWaitsForTheTable(t *testing.T) {
	f := newFarm(1)
	delete(f.obs.Fenced, "dg01")
	f.obs.NftTablePresent = false
	if n := len(Actions(Plan(f.world())).OfKind(ActAddFwDongle)); n != 0 {
		t.Fatal("adding an element to a table that is not loaded would succeed silently against nothing")
	}
}

func TestActionsAreOrderedBySlot(t *testing.T) {
	f := newFarm(6)
	for _, s := range sortedSlots(f.slots) {
		f.proxyStatus(s, func(st *proxysup.Status) { *st = proxysup.Status{Unit: st.Unit} })
	}
	got := Plan(f.world())
	if len(got) != 6 {
		t.Fatalf("want one action per slot, got %v", Actions(got).Kinds())
	}
	for i, a := range got {
		if slotOf(a) != domain.Slot(i+1) {
			t.Fatalf("action %d targets slot %d, plan must walk slots in ascending order", i, int(slotOf(a)))
		}
	}
}

func TestInvalidSlotRowsAreIgnored(t *testing.T) {
	f := newFarm(1)
	f.slots[99] = domain.SlotRow{ID: "s99", NodeID: testNodeID, Slot: 99, IfName: "dg99"}
	if got := Plan(f.world()); len(got) != 0 {
		t.Fatalf("an out of range slot row produced %v", Actions(got).Kinds())
	}
}

func BenchmarkPlan(b *testing.B) {
	f := newFarm(48)
	for _, s := range sortedSlots(f.slots) {
		f.proxyStatus(s, func(st *proxysup.Status) { st.ProbeOK = false })
		f.device(s, func(d *DeviceObservation) { d.MaxIdleTime = device.MaxIdleTimeDefault })
	}
	w := f.world()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(Plan(w)) == 0 {
			b.Fatal("benchmark world must produce work")
		}
	}
}

func TestPlanIsUnderOneMillisecondForAFullFarm(t *testing.T) {
	f := newFarm(48)
	for _, s := range sortedSlots(f.slots) {
		f.proxyStatus(s, func(st *proxysup.Status) { st.ProbeOK = false })
		f.device(s, func(d *DeviceObservation) { d.MaxIdleTime = device.MaxIdleTimeDefault })
	}
	w := f.world()

	res := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Plan(w)
		}
	})
	if per := res.T / time.Duration(res.N); per > time.Millisecond {
		t.Fatalf("Plan takes %s for 48 dongles, budget is 1ms", per)
	}
}
