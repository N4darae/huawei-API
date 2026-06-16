package devops

import (
	"context"
	"errors"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	DayLayout    = "2006-01-02"
	UsageWarnPct = 90
)

type Usage struct {
	DongleID   string `json:"dongle_id"`
	Day        string `json:"day"`
	UpBytes    int64  `json:"up_bytes"`
	DownBytes  int64  `json:"down_bytes"`
	CycleStart string `json:"cycle_start"`
	CycleUp    int64  `json:"cycle_up_bytes"`
	CycleDown  int64  `json:"cycle_down_bytes"`
	CapBytes   int64  `json:"cap_bytes"`
	Pct        int    `json:"pct"`
	Warn       bool   `json:"warn"`
	Over       bool   `json:"over_cap"`
}

func Day(t time.Time) string { return t.UTC().Format(DayLayout) }

func CycleStartDay(now time.Time, resetDay int) string {
	u := now.UTC()
	if resetDay < 1 {
		resetDay = 1
	}
	if resetDay > 28 {
		resetDay = 28
	}
	year, month := u.Year(), u.Month()
	if u.Day() < resetDay {
		year, month = prevMonth(year, month)
	}
	return time.Date(year, month, resetDay, 0, 0, 0, 0, time.UTC).Format(DayLayout)
}

func prevMonth(y int, m time.Month) (int, time.Month) {
	if m == time.January {
		return y - 1, time.December
	}
	return y, m - 1
}

func (s *Service) CollectUsage(ctx context.Context, dongleID string) (Usage, error) {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return Usage{}, err
	}
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return Usage{}, err
	}
	tr, err := dev.Traffic(ctx)
	if err != nil {
		return Usage{}, err
	}

	up, down := s.delta(dongleID, tr.TotalUpload, tr.TotalDownload)
	now := s.deps.Clock.Now()
	if up > 0 || down > 0 {
		if err := s.deps.Repos.Usage().AddDongleDaily(ctx, dongleID, Day(now), up, down, domain.UnixMillis(now)); err != nil {
			return Usage{}, err
		}
	}
	return s.UsageStatus(ctx, dongleID)
}

func (s *Service) delta(dongleID string, up, down int64) (int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.usage[dongleID]
	s.usage[dongleID] = counters{up: up, down: down, set: true}
	if !ok || !prev.set {
		return 0, 0
	}
	du, dd := up-prev.up, down-prev.down
	if du < 0 {
		du = up
	}
	if dd < 0 {
		dd = down
	}
	return du, dd
}

func (s *Service) ResetUsageBaseline(dongleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.usage, dongleID)
}

func (s *Service) UsageStatus(ctx context.Context, dongleID string) (Usage, error) {
	d, err := s.deps.Repos.Dongles().Get(ctx, dongleID)
	if err != nil {
		return Usage{}, err
	}
	now := s.deps.Clock.Now()
	day := Day(now)
	cycle := CycleStartDay(now, d.CapResetDay)

	out := Usage{DongleID: dongleID, Day: day, CycleStart: cycle, CapBytes: d.DataCapBytes}
	today, err := s.deps.Repos.Usage().GetDongleDaily(ctx, dongleID, day)
	if err == nil {
		out.UpBytes, out.DownBytes = today.UpBytes, today.DownBytes
	} else if !errors.Is(err, domain.ErrNotFound) {
		return Usage{}, err
	}

	cu, cd, err := s.deps.Repos.Usage().SumDongleSince(ctx, dongleID, cycle)
	if err != nil {
		return Usage{}, err
	}
	out.CycleUp, out.CycleDown = cu, cd
	if d.DataCapBytes > 0 {
		used := cu + cd
		out.Pct = int(used * 100 / d.DataCapBytes)
		out.Warn = out.Pct >= UsageWarnPct
		out.Over = used >= d.DataCapBytes
	}
	return out, nil
}
