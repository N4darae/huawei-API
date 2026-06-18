package devops

import (
	"context"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/rotate"
)

var (
	_ Ops             = (*Service)(nil)
	_ rotate.Rebooter = (*Service)(nil)
)

func setTraffic(h *harness, up, down int64) {
	h.hooks.set(func(x *hooks) { x.traffic = &device.Traffic{TotalUpload: up, TotalDownload: down} })
}

func TestCollectUsageTreatsTheFirstSampleAsABaseline(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	setTraffic(h, 5_000_000, 40_000_000)

	got, err := h.svc.CollectUsage(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("CollectUsage: %v", err)
	}
	if got.UpBytes != 0 || got.DownBytes != 0 {
		t.Fatalf("the first sample booked %d up and %d down, want a baseline of zero", got.UpBytes, got.DownBytes)
	}
}

func TestCollectUsageWritesTheDeltaIntoUsageDaily(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	setTraffic(h, 1_000, 2_000)
	if _, err := h.svc.CollectUsage(context.Background(), dongleID(1)); err != nil {
		t.Fatalf("baseline CollectUsage: %v", err)
	}
	setTraffic(h, 3_000, 9_000)

	got, err := h.svc.CollectUsage(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("CollectUsage: %v", err)
	}
	if got.UpBytes != 2_000 || got.DownBytes != 7_000 {
		t.Fatalf("usage_daily holds %d up and %d down, want 2000 and 7000", got.UpBytes, got.DownBytes)
	}
	day, err := h.db.Usage().GetDongleDaily(context.Background(), dongleID(1), Day(h.clock.Now()))
	if err != nil {
		t.Fatalf("GetDongleDaily: %v", err)
	}
	if day.UpBytes != 2_000 || day.DownBytes != 7_000 {
		t.Fatalf("sqlite holds %+v, want the delta", day)
	}
}

func TestCollectUsageSurvivesACounterResetOnTheDongle(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	setTraffic(h, 10_000, 20_000)
	if _, err := h.svc.CollectUsage(context.Background(), dongleID(1)); err != nil {
		t.Fatalf("baseline CollectUsage: %v", err)
	}
	setTraffic(h, 40_000, 80_000)
	if _, err := h.svc.CollectUsage(context.Background(), dongleID(1)); err != nil {
		t.Fatalf("second CollectUsage: %v", err)
	}
	setTraffic(h, 500, 900)

	got, err := h.svc.CollectUsage(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("CollectUsage after a reset: %v", err)
	}
	if got.UpBytes != 30_000+500 || got.DownBytes != 60_000+900 {
		t.Fatalf("a rebooted counter booked %d up and %d down, want the post reset value added once", got.UpBytes, got.DownBytes)
	}
}

func TestUsageStatusReportsTheCapAndWarnsBeforeItIsSpent(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()
	if err := h.db.Dongles().SetDataCap(ctx, dongleID(1), 10_000, 1); err != nil {
		t.Fatalf("SetDataCap: %v", err)
	}
	setTraffic(h, 0, 0)
	if _, err := h.svc.CollectUsage(ctx, dongleID(1)); err != nil {
		t.Fatalf("baseline CollectUsage: %v", err)
	}

	setTraffic(h, 4_000, 5_000)
	got, err := h.svc.CollectUsage(ctx, dongleID(1))
	if err != nil {
		t.Fatalf("CollectUsage: %v", err)
	}
	if got.CapBytes != 10_000 {
		t.Fatalf("usage reports cap %d, want 10000", got.CapBytes)
	}
	if got.Pct != 90 || !got.Warn || got.Over {
		t.Fatalf("usage is %+v, want 90 percent, warning, not over", got)
	}

	setTraffic(h, 6_000, 6_000)
	got, err = h.svc.CollectUsage(ctx, dongleID(1))
	if err != nil {
		t.Fatalf("CollectUsage: %v", err)
	}
	if !got.Over {
		t.Fatalf("usage is %+v, want the cap reported as spent", got)
	}
}

func TestUsageStatusWithoutACapReportsNoPercentage(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	got, err := h.svc.UsageStatus(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("UsageStatus: %v", err)
	}
	if got.Pct != 0 || got.Warn || got.Over {
		t.Fatalf("an uncapped dongle reports %+v", got)
	}
	if got.CycleStart == "" {
		t.Fatal("usage does not name the billing cycle it is summing")
	}
}

func TestCycleStartDayFollowsTheResetDay(t *testing.T) {
	cases := []struct {
		now      string
		resetDay int
		want     string
	}{
		{"2026-08-08", 1, "2026-08-01"},
		{"2026-08-08", 15, "2026-07-15"},
		{"2026-08-15", 15, "2026-08-15"},
		{"2026-01-03", 20, "2025-12-20"},
		{"2026-08-08", 0, "2026-08-01"},
		{"2026-08-08", 31, "2026-07-28"},
	}
	for _, c := range cases {
		now, err := time.Parse(DayLayout, c.now)
		if err != nil {
			t.Fatalf("parse %q: %v", c.now, err)
		}
		if got := CycleStartDay(now, c.resetDay); got != c.want {
			t.Errorf("CycleStartDay(%s, %d) = %s, want %s", c.now, c.resetDay, got, c.want)
		}
	}
}

func TestResetUsageBaselineForcesTheNextSampleToRebase(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	setTraffic(h, 1_000, 1_000)
	if _, err := h.svc.CollectUsage(context.Background(), dongleID(1)); err != nil {
		t.Fatalf("baseline CollectUsage: %v", err)
	}
	h.svc.ResetUsageBaseline(dongleID(1))
	setTraffic(h, 9_000, 9_000)

	got, err := h.svc.CollectUsage(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("CollectUsage: %v", err)
	}
	if got.UpBytes != 0 || got.DownBytes != 0 {
		t.Fatalf("a rebased counter booked %d up and %d down, want zero", got.UpBytes, got.DownBytes)
	}
}
