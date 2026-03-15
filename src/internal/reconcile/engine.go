package reconcile

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/store"
)

const (
	ProcUptime       = "/proc/uptime"
	KickBuffer       = 1
	MinSweepInterval = time.Second
)

var ErrNoNode = errors.New("reconcile: node id is required")

type Deps struct {
	NodeID            string
	Repos             store.Repos
	Net               netcfg.Manager
	FW                fw.Firewall
	Proxy             proxysup.Supervisor
	Dev               device.Registry
	Bus               eventbus.Bus
	Ops               Ops
	Clock             Clock
	Log               *slog.Logger
	Reconcile         config.Reconcile
	MinRotateInterval time.Duration
	NServerFallback   netip.Addr
	HostBootedAt      time.Time
	Rand              func() float64
}

type Result struct {
	World    World
	Actions  []Action
	Outcomes []Outcome
	Applied  int
	Skipped  int
	Failed   int
}

type Engine struct {
	deps      Deps
	poller    *Poller
	actuator  *Actuator
	gens      *Generations
	log       *slog.Logger
	startedAt time.Time
	bootedAt  time.Time

	kick chan string

	mu       sync.Mutex
	lastKick string
	sweeps   int
}

func NewEngine(d Deps) (*Engine, error) {
	if d.NodeID == "" {
		return nil, ErrNoNode
	}
	if d.Repos == nil {
		return nil, ErrNoRepos
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock()
	}
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	if d.Rand == nil {
		d.Rand = rand.Float64
	}
	if d.Reconcile.SweepInterval < MinSweepInterval {
		d.Reconcile.SweepInterval = config.Default().Reconcile.SweepInterval
	}
	if d.Reconcile.StartupGrace <= 0 {
		d.Reconcile.StartupGrace = config.Default().Reconcile.StartupGrace
	}
	if d.MinRotateInterval <= 0 {
		d.MinRotateInterval = config.Default().Carrier.MinRotateInterval
	}

	actuator, err := NewActuator(ActuatorDeps{
		Net:             d.Net,
		FW:              d.FW,
		Proxy:           d.Proxy,
		Dev:             d.Dev,
		Repos:           d.Repos,
		Ops:             d.Ops,
		Clock:           d.Clock,
		NServerFallback: d.NServerFallback,
	})
	if err != nil {
		return nil, err
	}

	now := d.Clock.Now()
	booted := d.HostBootedAt
	if booted.IsZero() {
		booted = hostBootedAt(now)
	}

	return &Engine{
		deps: d,
		poller: NewPoller(PollDeps{
			Net:     d.Net,
			FW:      d.FW,
			Proxy:   d.Proxy,
			Dev:     d.Dev,
			Clock:   d.Clock,
			Backoff: Backoff{Min: d.Reconcile.BackoffMin, Max: d.Reconcile.BackoffMax},
			Rand:    d.Rand,
		}),
		actuator:  actuator,
		gens:      NewGenerations(),
		log:       d.Log.With("component", "reconcile"),
		startedAt: now,
		bootedAt:  booted,
		kick:      make(chan string, KickBuffer),
	}, nil
}

func (e *Engine) Snapshot() ObservedState { return e.poller.Snapshot() }

func (e *Engine) ProcessStartedAt() time.Time { return e.startedAt }

func (e *Engine) HostBootedAt() time.Time { return e.bootedAt }

func (e *Engine) Sweeps() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sweeps
}

func (e *Engine) Kick(reason string) {
	select {
	case e.kick <- reason:
	default:
	}
}

func (e *Engine) Run(ctx context.Context) error {
	if err := e.Recover(ctx); err != nil {
		return err
	}
	for {
		if _, err := e.Once(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reason := <-e.kick:
			e.mu.Lock()
			e.lastKick = reason
			e.mu.Unlock()
		case <-e.deps.Clock.After(e.deps.Reconcile.SweepInterval):
		}
	}
}

func (e *Engine) Recover(ctx context.Context) error {
	now := domain.UnixMillis(e.deps.Clock.Now())
	orphans, err := e.deps.Repos.Operations().ReconcileOrphans(ctx, now)
	if err != nil {
		return err
	}
	if orphans > 0 {
		e.log.Warn("reconciled operations left live by a restart", "count", orphans)
		e.notice(ctx, eventbus.NoticeWarn, "operations reconciled after a restart",
			strconv.Itoa(orphans)+" operations were left live by the previous process and were failed; no process was adopted")
	}
	return nil
}

func (e *Engine) Once(ctx context.Context) (Result, error) {
	desired, err := LoadDesired(ctx, e.deps.Repos, e.deps.NodeID)
	if err != nil {
		return Result{}, err
	}

	now := e.deps.Clock.Now()
	if _, err := e.deps.Repos.Operations().MarkStalled(ctx, domain.UnixMillis(now)); err != nil {
		return Result{}, err
	}

	active, err := LoadActiveOps(ctx, e.deps.Repos)
	if err != nil {
		return Result{}, err
	}
	e.gens.Sync(active)

	budgets, err := LoadBudgets(ctx, e.deps.Repos, BudgetPolicy{
		RebootPerDay:        e.deps.Reconcile.RebootBudgetPerDay,
		RebootCooldown:      e.deps.Reconcile.RebootCooldown,
		MaxConcurrentRotate: e.deps.Reconcile.MaxConcurrentRotate,
		MinRotateInterval:   e.deps.MinRotateInterval,
	}, now, active)
	if err != nil {
		return Result{}, err
	}

	observed := e.poller.Sweep(ctx, desired.SlotRows())

	w := World{
		Now:              e.deps.Clock.Now(),
		HostBootedAt:     e.bootedAt,
		ProcessStartedAt: e.startedAt,
		StartupGrace:     e.deps.Reconcile.StartupGrace,
		CacheWarm:        observed.SweepsCompleted > FirstSweepIsColdCache,
		Desired:          desired,
		Observed:         observed,
		Budgets:          budgets,
		ActiveOps:        active,
	}

	actions := Plan(w)
	snapshot := e.gens.Snapshot(actions)

	res := Result{World: w, Actions: actions, Outcomes: make([]Outcome, 0, len(actions))}
	for _, action := range actions {
		if ctx.Err() != nil {
			break
		}
		if e.gens.Fenced(action, snapshot) {
			res.Skipped++
			res.Outcomes = append(res.Outcomes, Outcome{
				Kind: action.Kind(), Target: action.Target(), Slot: slotOf(action),
				Reason: reasonOf(action), Skipped: true, SkipReason: ErrFenced.Error(),
			})
			continue
		}
		out := e.actuator.Apply(ctx, w, action)
		res.Outcomes = append(res.Outcomes, out)
		switch {
		case out.Skipped:
			res.Skipped++
		case out.Err != nil:
			res.Failed++
			e.log.Error("action failed",
				"kind", string(out.Kind), "target", out.Target, "reason", out.Reason, "err", out.Err)
		default:
			res.Applied++
			e.onApplied(ctx, out)
		}
	}

	e.mu.Lock()
	e.sweeps++
	e.mu.Unlock()
	return res, nil
}

func (e *Engine) onApplied(ctx context.Context, out Outcome) {
	if !out.Kind.Destructive() {
		return
	}
	e.log.Warn("destructive action applied",
		"kind", string(out.Kind), "target", out.Target, "reason", out.Reason, "operation_id", out.OperationID)
	e.notice(ctx, eventbus.NoticeWarn, "the panel started an unrequested recovery",
		string(out.Kind)+" on "+out.Target+": "+out.Reason)
}

func (e *Engine) notice(ctx context.Context, level, title, detail string) {
	if e.deps.Bus == nil {
		return
	}
	ev, err := eventbus.NewEvent(e.deps.NodeID, eventbus.EvSystemNotice, title, eventbus.NoticeData{
		Level: level, Title: title, Detail: detail,
	})
	if err != nil {
		return
	}
	if err := e.deps.Bus.Publish(ctx, ev); err != nil {
		e.log.Debug("event bus rejected a notice", "err", err)
	}
}

func hostBootedAt(now time.Time) time.Time {
	raw, err := os.ReadFile(ProcUptime)
	if err != nil {
		return now
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return now
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return now
	}
	return now.Add(-time.Duration(seconds * float64(time.Second)))
}
