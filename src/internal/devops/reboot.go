package devops

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/store"
)

type Ops interface {
	Reboot(ctx context.Context, dongleID string) (*domain.Operation, error)
	SetNetMode(ctx context.Context, dongleID string, m device.NetMode) (*domain.Operation, error)
	SetLanIP(ctx context.Context, dongleID string, gw netip.Addr) (*domain.Operation, error)
	SMSList(ctx context.Context, dongleID string, box device.SMSBox, page, size int) ([]device.SMS, int, error)
	SMSSend(ctx context.Context, dongleID string, to []string, body string) error
	SMSDelete(ctx context.Context, dongleID string, idx int64) error
	SMSMarkRead(ctx context.Context, dongleID string, idx int64) error
}

const (
	StepPrecheck       = "precheck"
	StepReboot         = "reboot"
	StepWaitDown       = "wait_down"
	StepWaitUp         = "wait_up"
	StepDataOff        = "data_off"
	StepWaitDisconnect = "wait_disconnect"
	StepSetMode        = "set_mode"
	StepDataOn         = "data_on"
	StepWaitConnect    = "wait_connect"
	StepWriteNetcfg    = "write_netcfg"
	StepPostDHCP       = "post_dhcp"
	StepRediscovering  = "re_discovering"
	StepVerify         = "verify"
	StepDone           = "done"
)

func RebootSteps() []string {
	return []string{StepPrecheck, StepReboot, StepWaitDown, StepWaitUp, StepDataOn, StepVerify, StepDone}
}

func NetModeSteps() []string {
	return []string{StepPrecheck, StepDataOff, StepWaitDisconnect, StepSetMode, StepDataOn, StepWaitConnect, StepVerify, StepDone}
}

func LanIPSteps() []string {
	return []string{StepPrecheck, StepWriteNetcfg, StepPostDHCP, StepRediscovering, StepVerify, StepDone}
}

const (
	ReasonDeviceUnreachable = "device_unreachable"
	ReasonSimLocked         = "sim_locked"
	ReasonNoDataSession     = "no_data_session"
	ReasonRebootBudget      = "reboot_budget_spent"
	ReasonSetModeRejected   = "set_net_mode_rejected"
	ReasonLanIPUnsupported  = "lan_ip_change_unsupported"
	ReasonLanIPNotFound     = "lan_ip_not_reachable"
	ReasonDeadline          = "deadline_exceeded"
	ReasonSlotMissing       = "dongle_has_no_slot"
	ReasonNetcfgFailed      = "netcfg_apply_failed"
	ReasonVerifyFailed      = "verify_failed"
)

var (
	ErrNoRepos          = errors.New("devops: a store.Repos is required")
	ErrNoDevices        = errors.New("devops: a device.Registry is required")
	ErrClosed           = errors.New("devops: service is shutting down")
	ErrRebootBudget     = errors.New("devops: the reboot budget for this dongle is spent")
	ErrLanIPUnsupported = errors.New("devops: the dongle refused the dhcp settings change; this slot needs a manual netns")
	ErrLanIPNotPlanned  = errors.New("devops: the requested lan gateway is not the address this slot is planned for")
	ErrNoNetcfg         = errors.New("devops: a netcfg.Manager is required to move a dongle lan address")
	errPollTimeout      = errors.New("devops: the dongle did not reach the expected state")
)

type ConflictError struct {
	OperationID string
	SubjectType domain.SubjectType
	SubjectID   string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("devops: operation %s is already live on %s:%s", e.OperationID, e.SubjectType, e.SubjectID)
}

func (e *ConflictError) Unwrap() error { return domain.ErrOpInProgress }

func (e *ConflictError) ActiveOperationID() string { return e.OperationID }

type Actor struct {
	Trigger   domain.Trigger
	Type      domain.ActorType
	ID        string
	RequestID string
}

func AdminActor() Actor {
	return Actor{Trigger: domain.TriggerAdminUI, Type: domain.ActorAdmin}
}

func SystemActor(reason string) Actor {
	return Actor{Trigger: domain.TriggerAutoRecovery, Type: domain.ActorSystem, ID: reason}
}

func (a Actor) normalize() Actor {
	if a.Trigger == "" {
		a.Trigger = domain.TriggerAdminUI
	}
	if a.Type == "" {
		switch a.Trigger {
		case domain.TriggerAutoRecovery:
			a.Type = domain.ActorSystem
		case domain.TriggerCustomerAPI:
			a.Type = domain.ActorAPIKey
		default:
			a.Type = domain.ActorAdmin
		}
	}
	return a
}

type Timeouts struct {
	PollInterval       time.Duration
	RebootDeadline     time.Duration
	RebootWaitDown     time.Duration
	RebootWaitUp       time.Duration
	ConnectTimeout     time.Duration
	NetModeDeadline    time.Duration
	LanIPDeadline      time.Duration
	RediscoverWindow   time.Duration
	RebootBudgetPerDay int
	RebootCooldown     time.Duration
}

func DefaultTimeouts() Timeouts {
	return Timeouts{
		PollInterval:       time.Second,
		RebootDeadline:     5 * time.Minute,
		RebootWaitDown:     30 * time.Second,
		RebootWaitUp:       3 * time.Minute,
		ConnectTimeout:     45 * time.Second,
		NetModeDeadline:    2 * time.Minute,
		LanIPDeadline:      90 * time.Second,
		RediscoverWindow:   15 * time.Second,
		RebootBudgetPerDay: 4,
		RebootCooldown:     30 * time.Minute,
	}
}

type Deps struct {
	Repos    store.Repos
	Dev      device.Registry
	Net      netcfg.Manager
	Bus      eventbus.Bus
	Clock    domain.Clock
	Timeouts Timeouts
	NodeID   string
	NewID    func(prefix string) string
}

type counters struct {
	up   int64
	down int64
	set  bool
}

type Service struct {
	deps    Deps
	to      Timeouts
	mu      sync.Mutex
	waiters map[string]chan struct{}
	wg      sync.WaitGroup
	closed  bool
	usage   map[string]counters
}

var _ Ops = (*Service)(nil)

func New(d Deps) (*Service, error) {
	if d.Repos == nil {
		return nil, ErrNoRepos
	}
	if d.Dev == nil {
		return nil, ErrNoDevices
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock()
	}
	if d.NewID == nil {
		d.NewID = NewID
	}
	to := d.Timeouts
	def := DefaultTimeouts()
	if to.PollInterval <= 0 {
		to.PollInterval = def.PollInterval
	}
	if to.RebootDeadline <= 0 {
		to.RebootDeadline = def.RebootDeadline
	}
	if to.RebootWaitDown <= 0 {
		to.RebootWaitDown = def.RebootWaitDown
	}
	if to.RebootWaitUp <= 0 {
		to.RebootWaitUp = def.RebootWaitUp
	}
	if to.ConnectTimeout <= 0 {
		to.ConnectTimeout = def.ConnectTimeout
	}
	if to.NetModeDeadline <= 0 {
		to.NetModeDeadline = def.NetModeDeadline
	}
	if to.LanIPDeadline <= 0 {
		to.LanIPDeadline = def.LanIPDeadline
	}
	if to.RediscoverWindow <= 0 {
		to.RediscoverWindow = def.RediscoverWindow
	}
	if to.RebootBudgetPerDay == 0 {
		to.RebootBudgetPerDay = def.RebootBudgetPerDay
	}
	if to.RebootCooldown <= 0 {
		to.RebootCooldown = def.RebootCooldown
	}
	return &Service{deps: d, to: to, waiters: map[string]chan struct{}{}, usage: map[string]counters{}}, nil
}

func (s *Service) Timeouts() Timeouts { return s.to }

func NewID(prefix string) string {
	var b [10]byte
	if _, err := crand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixNano()))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

type dongleTarget struct {
	dongle domain.Dongle
	slot   domain.SlotRow
	node   domain.Node
	proxy  domain.Proxy
}

func (s *Service) target(ctx context.Context, dongleID string) (dongleTarget, error) {
	var t dongleTarget
	if strings.TrimSpace(dongleID) == "" {
		return t, fmt.Errorf("%w: a dongle id is required", domain.ErrInvalid)
	}
	d, err := s.deps.Repos.Dongles().Get(ctx, dongleID)
	if err != nil {
		return t, err
	}
	t.dongle = d
	row, err := s.deps.Repos.Slots().GetByDongle(ctx, dongleID)
	if err != nil {
		return t, err
	}
	t.slot = row
	node, err := s.deps.Repos.Nodes().Get(ctx, row.NodeID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return t, err
	}
	t.node = node
	px, err := s.deps.Repos.Proxies().GetBySlot(ctx, row.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return t, err
	}
	t.proxy = px
	return t, nil
}

func (s *Service) checkConflict(ctx context.Context, subject domain.SubjectType, id string) error {
	live, err := s.deps.Repos.Operations().FindActive(ctx, subject, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return &ConflictError{OperationID: live.ID, SubjectType: subject, SubjectID: id}
}

func (s *Service) checkRotateFence(ctx context.Context, t dongleTarget) error {
	if t.proxy.ID == "" {
		return nil
	}
	return s.checkConflict(ctx, domain.SubjectProxy, t.proxy.ID)
}

func (s *Service) Wait(ctx context.Context, operationID string) (domain.Operation, error) {
	s.mu.Lock()
	ch, ok := s.waiters[operationID]
	s.mu.Unlock()
	if ok {
		select {
		case <-ch:
		case <-ctx.Done():
			return domain.Operation{}, ctx.Err()
		}
	}
	return s.deps.Repos.Operations().Get(ctx, operationID)
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type runFunc func(ctx context.Context, op *domain.Operation) (any, string, error)

func (s *Service) start(ctx context.Context, kind domain.OpKind, subject domain.SubjectType, id string, a Actor, deadline time.Duration, fn runFunc) (*domain.Operation, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	a = a.normalize()
	now := s.deps.Clock.Now()
	op := domain.Operation{
		ID:          s.deps.NewID("op"),
		Kind:        kind,
		SubjectType: subject,
		SubjectID:   id,
		State:       domain.OpPending,
		StartedAt:   domain.UnixMillis(now),
		DeadlineAt:  domain.UnixMillis(now.Add(deadline)),
		Trigger:     a.Trigger,
		ActorType:   a.Type,
		ActorID:     a.ID,
		RequestID:   a.RequestID,
	}
	if err := s.deps.Repos.Operations().Create(ctx, op); err != nil {
		if errors.Is(err, domain.ErrOpInProgress) {
			if live, lookupErr := s.deps.Repos.Operations().FindActive(ctx, subject, id); lookupErr == nil {
				return nil, &ConflictError{OperationID: live.ID, SubjectType: subject, SubjectID: id}
			}
		}
		return nil, err
	}
	s.publishOp(ctx, op, eventbus.EvOpUpdate)

	done := make(chan struct{})
	s.mu.Lock()
	s.waiters[op.ID] = done
	s.mu.Unlock()

	s.wg.Add(1)
	go func(op domain.Operation) {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.waiters, op.ID)
			s.mu.Unlock()
			close(done)
		}()
		bg, cancel := context.WithTimeout(context.Background(), deadline*2)
		defer cancel()
		payload, reason, err := fn(bg, &op)
		s.finishOp(bg, op, payload, reason, err)
	}(op)

	return &op, nil
}

func (s *Service) step(ctx context.Context, op *domain.Operation, step string, steps []string) {
	op.State = domain.OpRunning
	op.Step = step
	op.Pct = pctOf(step, steps)
	if err := s.deps.Repos.Operations().Progress(ctx, op.ID, domain.OpRunning, op.Step, op.Pct); err != nil {
		return
	}
	s.publishOp(ctx, *op, eventbus.EvOpUpdate)
}

func pctOf(step string, steps []string) int {
	for i, v := range steps {
		if v == step {
			return (i + 1) * 100 / len(steps)
		}
	}
	return 0
}

func (s *Service) finishOp(ctx context.Context, op domain.Operation, payload any, reason string, err error) {
	state := domain.OpSucceeded
	msg := ""
	if err != nil {
		state = domain.OpFailed
		msg = reason
		if msg == "" {
			msg = err.Error()
		}
	}
	body := "{}"
	if payload != nil {
		if b, mErr := json.Marshal(payload); mErr == nil {
			body = string(b)
		}
	}
	now := s.deps.Clock.Now()
	_ = s.deps.Repos.Operations().Finish(ctx, op.ID, state, msg, body, domain.UnixMillis(now))

	op.State = state
	op.Error = msg
	op.ResultJSON = body
	op.Pct = 100
	finished := domain.UnixMillis(now)
	op.FinishedAt = &finished
	s.publishOp(ctx, op, eventbus.EvOpDone)
}

type opPayload struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	State       string `json:"state"`
	Step        string `json:"step"`
	Pct         int    `json:"pct"`
	StartedAt   int64  `json:"started_at"`
	DeadlineAt  int64  `json:"deadline_at"`
	FinishedAt  *int64 `json:"finished_at,omitempty"`
	Error       string `json:"error,omitempty"`
	Trigger     string `json:"trigger"`
	ActorType   string `json:"actor_type,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

func (s *Service) publishOp(ctx context.Context, op domain.Operation, kind eventbus.EventType) {
	if s.deps.Bus == nil {
		return
	}
	ev, err := eventbus.NewEvent(s.deps.NodeID, kind, op.ID, opPayload{
		ID:          op.ID,
		Kind:        string(op.Kind),
		SubjectType: string(op.SubjectType),
		SubjectID:   op.SubjectID,
		State:       string(op.State),
		Step:        op.Step,
		Pct:         op.Pct,
		StartedAt:   op.StartedAt,
		DeadlineAt:  op.DeadlineAt,
		FinishedAt:  op.FinishedAt,
		Error:       op.Error,
		Trigger:     string(op.Trigger),
		ActorType:   string(op.ActorType),
		RequestID:   op.RequestID,
	})
	if err != nil {
		return
	}
	_ = s.deps.Bus.Publish(ctx, ev)
}

func (s *Service) pollConn(ctx context.Context, dev device.Device, want device.ConnStatus, budget time.Duration) error {
	deadline := s.deps.Clock.Now().Add(budget)
	var seen device.ConnStatus
	var last error
	for {
		st, err := dev.Status(ctx)
		if err == nil {
			seen = st.ConnectionStatus
			if st.ConnectionStatus == want {
				return nil
			}
		} else {
			last = err
		}
		if !s.deps.Clock.Now().Before(deadline) {
			if last != nil {
				return last
			}
			return fmt.Errorf("%w: status %d after %s, want %d", errPollTimeout, int(seen), budget, int(want))
		}
		if err := s.deps.Clock.Sleep(ctx, s.to.PollInterval); err != nil {
			return err
		}
	}
}

type RebootResult struct {
	Reachable    bool   `json:"reachable"`
	DownObserved bool   `json:"down_observed"`
	ConnStatus   int    `json:"conn_status"`
	WanIP        string `json:"wan_ip,omitempty"`
	DurationMS   int    `json:"duration_ms"`
	Note         string `json:"note,omitempty"`
}

func (s *Service) Reboot(ctx context.Context, dongleID string) (*domain.Operation, error) {
	return s.RebootAs(ctx, dongleID, AdminActor())
}

func (s *Service) RebootAuto(ctx context.Context, dongleID, reason string) (domain.Operation, error) {
	op, err := s.RebootAs(ctx, dongleID, SystemActor(reason))
	if err != nil {
		return domain.Operation{}, err
	}
	return *op, nil
}

func (s *Service) RebootAs(ctx context.Context, dongleID string, a Actor) (*domain.Operation, error) {
	a = a.normalize()
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return nil, err
	}
	if err := s.checkConflict(ctx, domain.SubjectDongle, dongleID); err != nil {
		return nil, err
	}
	if a.Trigger != domain.TriggerAutoRecovery {
		if err := s.checkRotateFence(ctx, t); err != nil {
			return nil, err
		}
	} else {
		ok, err := s.RebootAllowed(ctx, dongleID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrRebootBudget
		}
	}

	return s.start(ctx, domain.OpReboot, domain.SubjectDongle, dongleID, a, s.to.RebootDeadline,
		func(ctx context.Context, op *domain.Operation) (any, string, error) {
			return s.runReboot(ctx, op, t)
		})
}

func (s *Service) runReboot(ctx context.Context, op *domain.Operation, t dongleTarget) (any, string, error) {
	start := s.deps.Clock.Now()
	res := RebootResult{}
	steps := RebootSteps()

	s.step(ctx, op, StepPrecheck, steps)
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return res, ReasonDeviceUnreachable, err
	}

	s.step(ctx, op, StepReboot, steps)
	if err := dev.Reboot(ctx); err != nil && !errors.Is(err, domain.ErrUnreachable) {
		return res, ReasonDeviceUnreachable, err
	}

	s.step(ctx, op, StepWaitDown, steps)
	res.DownObserved = s.waitDown(ctx, dev, s.to.RebootWaitDown)
	if !res.DownObserved {
		res.Note = "the dongle never stopped answering; a fast reboot can be missed"
	}

	s.step(ctx, op, StepWaitUp, steps)
	if err := s.waitUp(ctx, dev, s.to.RebootWaitUp); err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonDeviceUnreachable, err
	}
	res.Reachable = true

	s.step(ctx, op, StepDataOn, steps)
	if err := dev.DataSwitch(ctx, true); err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonDeviceUnreachable, err
	}
	if err := s.pollConn(ctx, dev, device.ConnConnected, s.to.ConnectTimeout); err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonNoDataSession, err
	}

	s.step(ctx, op, StepVerify, steps)
	if st, err := dev.Status(ctx); err == nil {
		res.ConnStatus = int(st.ConnectionStatus)
		if st.WanIP.IsValid() {
			res.WanIP = st.WanIP.String()
		}
	}
	res.DurationMS = s.elapsedMS(start)

	s.step(ctx, op, StepDone, steps)
	return res, "", nil
}

func (s *Service) waitDown(ctx context.Context, dev device.Device, budget time.Duration) bool {
	deadline := s.deps.Clock.Now().Add(budget)
	for {
		if !dev.Reachable(ctx) {
			return true
		}
		if st, err := dev.Status(ctx); err == nil && st.ConnectionStatus != device.ConnConnected {
			return true
		}
		if !s.deps.Clock.Now().Before(deadline) {
			return false
		}
		if err := s.deps.Clock.Sleep(ctx, s.to.PollInterval); err != nil {
			return false
		}
	}
}

func (s *Service) waitUp(ctx context.Context, dev device.Device, budget time.Duration) error {
	deadline := s.deps.Clock.Now().Add(budget)
	var last error
	for {
		if dev.Reachable(ctx) {
			sim, err := dev.PinStatus(ctx)
			if err == nil && !sim.Locked() {
				return nil
			}
			if err == nil && sim.Locked() {
				return fmt.Errorf("%w: sim state %d", domain.ErrSimLocked, int(sim))
			}
			last = err
		}
		if !s.deps.Clock.Now().Before(deadline) {
			if last != nil {
				return last
			}
			return fmt.Errorf("%w: the dongle did not come back within %s", domain.ErrUnreachable, budget)
		}
		if err := s.deps.Clock.Sleep(ctx, s.to.PollInterval); err != nil {
			return err
		}
	}
}

func (s *Service) RebootAllowed(ctx context.Context, dongleID string) (bool, error) {
	if s.to.RebootBudgetPerDay <= 0 {
		return false, nil
	}
	now := s.deps.Clock.Now()
	ops, err := s.deps.Repos.Operations().List(ctx, store.OperationFilter{
		Kind:        domain.OpReboot,
		SubjectType: domain.SubjectDongle,
		SubjectID:   dongleID,
		SinceMS:     domain.UnixMillis(startOfDay(now)),
	})
	if err != nil {
		return false, err
	}
	if len(ops) >= s.to.RebootBudgetPerDay {
		return false, nil
	}
	for _, o := range ops {
		if now.Sub(domain.FromUnixMillis(o.StartedAt)) < s.to.RebootCooldown {
			return false, nil
		}
	}
	return true, nil
}

func startOfDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Service) elapsedMS(start time.Time) int {
	return int(s.deps.Clock.Now().Sub(start) / time.Millisecond)
}
