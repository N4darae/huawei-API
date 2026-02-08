package domain

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Node struct {
	ID         string
	Name       string
	Kind       string
	PublicHost netip.Addr
	CreatedAt  int64
	UpdatedAt  int64
}

const NodeKindLocal = "local"

type Dongle struct {
	ID                   string
	NodeID               string
	IMEI                 string
	ICCID                string
	IMSI                 string
	FirmwareVer          string
	HwVer                string
	Classify             string
	Carrier              string
	LanIPChangeSupported bool
	HilinkLoginRequired  bool
	DataCapBytes         int64
	CapResetDay          int
	AutoRecoverEnabled   bool
	CreatedAt            int64
	UpdatedAt            int64
}

const ClassifyHiLink = "hilink"

type SlotRow struct {
	ID        string
	NodeID    string
	Slot      Slot
	USBPath   string
	IDPath    string
	IfName    string
	DongleID  *string
	CreatedAt int64
	UpdatedAt int64
}

func (r SlotRow) Occupied() bool { return r.DongleID != nil && *r.DongleID != "" }

type Proxy struct {
	ID         string
	SlotID     string
	CustomerID *string
	Enabled    bool
	Suspended  bool
	SocksPort  int
	HTTPPort   int
	Username   string
	Password   string
	AuthMode   AuthMode
	AuthIPs    []netip.Prefix
	Policy     ProxyPolicy
	ExpiresAt  *int64
	CreatedAt  int64
	UpdatedAt  int64
}

func (p Proxy) Expired(nowMS int64) bool {
	return p.ExpiresAt != nil && *p.ExpiresAt > 0 && nowMS >= *p.ExpiresAt
}

func (p Proxy) DesiredState(nowMS int64) ProxyState {
	switch {
	case !p.Enabled:
		return ProxyStateDisabled
	case p.Expired(nowMS):
		return ProxyStateExpired
	case p.Suspended:
		return ProxyStateSuspended
	default:
		return ProxyStateActive
	}
}

type ProxyAuthIP struct {
	ID        string
	ProxyID   string
	CIDR      netip.Prefix
	Note      string
	CreatedAt int64
}

type PortRange struct {
	Lo int
	Hi int
}

func (r PortRange) Valid() bool {
	return r.Lo >= 1 && r.Hi <= 65535 && r.Lo <= r.Hi
}

func (r PortRange) Contains(p int) bool { return p >= r.Lo && p <= r.Hi }

func (r PortRange) String() string {
	if r.Lo == r.Hi {
		return strconv.Itoa(r.Lo)
	}
	return strconv.Itoa(r.Lo) + "-" + strconv.Itoa(r.Hi)
}

func ParsePortRange(s string) (PortRange, error) {
	s = strings.TrimSpace(s)
	lo, hi, found := strings.Cut(s, "-")
	if !found {
		hi = lo
	}
	l, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return PortRange{}, fmt.Errorf("%w: port range %q", ErrInvalid, s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return PortRange{}, fmt.Errorf("%w: port range %q", ErrInvalid, s)
	}
	r := PortRange{Lo: l, Hi: h}
	if !r.Valid() {
		return PortRange{}, fmt.Errorf("%w: port range %q", ErrInvalid, s)
	}
	return r, nil
}

func FormatPortRanges(rs []PortRange) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.String())
	}
	return strings.Join(out, ",")
}

func ParsePortRanges(s string) ([]PortRange, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]PortRange, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		r, err := ParsePortRange(p)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type ProxyPolicy struct {
	AllowAllPorts bool
	AllowedPorts  []PortRange
	MaxConn       int
	ConnLimit     int
}

const (
	DefaultMaxConn   = 200
	DefaultConnLimit = 30
)

func DefaultProxyPolicy() ProxyPolicy {
	return ProxyPolicy{
		AllowAllPorts: true,
		AllowedPorts:  nil,
		MaxConn:       DefaultMaxConn,
		ConnLimit:     DefaultConnLimit,
	}
}

func (p ProxyPolicy) Validate() error {
	if !p.AllowAllPorts && len(p.AllowedPorts) == 0 {
		return fmt.Errorf("%w: port policy denies every port", ErrInvalid)
	}
	for _, r := range p.AllowedPorts {
		if !r.Valid() {
			return fmt.Errorf("%w: port range %s", ErrInvalid, r)
		}
	}
	if p.MaxConn < 1 {
		return fmt.Errorf("%w: max_conn must be positive", ErrInvalid)
	}
	if p.ConnLimit < 0 {
		return fmt.Errorf("%w: conn_limit must not be negative", ErrInvalid)
	}
	return nil
}

var SMTPPorts = []int{25, 465, 587}

type Operation struct {
	ID          string
	Kind        OpKind
	SubjectType SubjectType
	SubjectID   string
	State       OpState
	Step        string
	Pct         int
	StartedAt   int64
	DeadlineAt  int64
	FinishedAt  *int64
	Error       string
	ResultJSON  string
	Trigger     Trigger
	ActorType   ActorType
	ActorID     string
	RequestID   string
	CreatedAt   int64
	UpdatedAt   int64
}

func (o Operation) Active() bool { return o.FinishedAt == nil && !o.State.Terminal() }

func (o Operation) Stalled(nowMS int64) bool {
	return o.Active() && o.DeadlineAt > 0 && nowMS > o.DeadlineAt
}

func (o Operation) Target() string { return string(o.SubjectType) + ":" + o.SubjectID }

type Rotation struct {
	ID          string
	OperationID string
	ProxyID     string
	RequestedAt int64
	DurationMS  int
	OldPublicIP netip.Addr
	NewPublicIP netip.Addr
	IPChanged   bool
	Result      RotationResult
	Trigger     Trigger
	RequestID   string
	HoldMS      int
	Attempts    int
	Error       string
}

type Customer struct {
	ID        string
	Name      string
	Contact   string
	Note      string
	CreatedAt int64
	UpdatedAt int64
}

type CarrierProfile struct {
	Name              string
	HoldEscalate      []time.Duration
	WaitConnect       time.Duration
	VerifyTimeout     time.Duration
	HardDeadline      time.Duration
	PollInterval      time.Duration
	MaxAttempts       int
	MinRotateInterval time.Duration
}

func (p CarrierProfile) Validate() error {
	if len(p.HoldEscalate) == 0 {
		return fmt.Errorf("%w: carrier profile has an empty hold ladder", ErrInvalid)
	}
	if p.HardDeadline <= 0 || p.WaitConnect <= 0 {
		return fmt.Errorf("%w: carrier profile timeouts must be positive", ErrInvalid)
	}
	return nil
}

type UsageDay struct {
	DongleID  string
	Day       string
	UpBytes   int64
	DownBytes int64
	UpdatedAt int64
}

type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

func SystemClock() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Since(t time.Time) time.Duration { return time.Since(t) }

func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func UnixMillis(t time.Time) int64 { return t.UnixMilli() }

func FromUnixMillis(ms int64) time.Time { return time.UnixMilli(ms) }

func SortPrefixes(ps []netip.Prefix) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Addr() == ps[j].Addr() {
			return ps[i].Bits() < ps[j].Bits()
		}
		return ps[i].Addr().Less(ps[j].Addr())
	})
}
