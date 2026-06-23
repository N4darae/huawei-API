package enroll

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/store"
)

var (
	ErrDuplicateAddr    = errors.New("enroll: more than one interface holds an address in 192.168.8.0/24 — unplug every dongle but the one being enrolled")
	ErrAddressConflict  = errors.New("enroll: two interfaces hold the same address")
	ErrLoginProtected   = errors.New("enroll: the dongle requires login; disable \"Require login\" in the dongle web UI and retry, otherwise every POST fails with 100003")
	ErrSimNotReady      = errors.New("enroll: the SIM is not usable")
	ErrLanIPUnsupported = errors.New("enroll: dhcp/settings is not supported by this dongle; the slot needs a manual namespace, see OPERATIONS.md")
	ErrNoLink           = errors.New("enroll: no new dongle interface appeared before the deadline")
	ErrNoCandidate      = errors.New("enroll: no dongle answers at 192.168.8.1")
	ErrSlotTaken        = errors.New("enroll: slot already holds a dongle")
	ErrMaxIdleNotZero   = errors.New("enroll: MaxIdelTime did not stick at 0; every idle proxy would die after five minutes")
	ErrDeps             = errors.New("enroll: missing dependency")
)

type StepName string

const (
	StepGuardAddresses StepName = "guard_addresses"
	StepDisablePorts   StepName = "disable_other_ports"
	StepAwaitLink      StepName = "await_link"
	StepLoginCheck     StepName = "login_check"
	StepSimCheck       StepName = "sim_check"
	StepIdentify       StepName = "identify"
	StepApplyNetcfg    StepName = "apply_netcfg"
	StepMoveLanIP      StepName = "move_lan_ip"
	StepMaxIdle        StepName = "max_idle"
	StepPublish        StepName = "publish"
	StepEnablePorts    StepName = "enable_other_ports"
)

func Steps() []StepName {
	return []StepName{
		StepGuardAddresses,
		StepDisablePorts,
		StepAwaitLink,
		StepLoginCheck,
		StepSimCheck,
		StepIdentify,
		StepApplyNetcfg,
		StepMoveLanIP,
		StepMaxIdle,
		StepPublish,
		StepEnablePorts,
	}
}

type Event struct {
	Step   StepName `json:"step"`
	Index  int      `json:"index"`
	Total  int      `json:"total"`
	Detail string   `json:"detail"`
	Error  string   `json:"error,omitempty"`
}

type Selftest func(ctx context.Context, proxyID string) error

const (
	DefaultLinkWait     = 90 * time.Second
	DefaultRediscover   = 45 * time.Second
	DefaultProbeEvery   = time.Second
	DHCPPoolStartOctet  = 150
	DHCPPoolEndOctet    = 200
	UsernamePrefix      = "cust_"
	usernameRandomBytes = 4
)

type Deps struct {
	NodeID          string
	PublicHost      netip.Addr
	NServerFallback netip.Addr

	Netcfg     netcfg.Manager
	Firewall   fw.Firewall
	Devices    device.Registry
	Repos      store.Repos
	Supervisor proxysup.Supervisor
	USB        *USBController
	Clock      domain.Clock

	Selftest Selftest
	Progress func(Event)
	NewID    func() string

	LinkWait     time.Duration
	Rediscover   time.Duration
	ProbeEvery   time.Duration
	SkipUSBGuard bool
}

type Enroller struct {
	d Deps
}

func New(d Deps) (*Enroller, error) {
	switch {
	case d.Netcfg == nil:
		return nil, fmt.Errorf("%w: netcfg.Manager", ErrDeps)
	case d.Firewall == nil:
		return nil, fmt.Errorf("%w: fw.Firewall", ErrDeps)
	case d.Devices == nil:
		return nil, fmt.Errorf("%w: device.Registry", ErrDeps)
	case d.Repos == nil:
		return nil, fmt.Errorf("%w: store.Repos", ErrDeps)
	case d.Supervisor == nil:
		return nil, fmt.Errorf("%w: proxysup.Supervisor", ErrDeps)
	case !d.PublicHost.IsValid():
		return nil, fmt.Errorf("%w: public host", ErrDeps)
	case d.NodeID == "":
		return nil, fmt.Errorf("%w: node id", ErrDeps)
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock()
	}
	if d.NewID == nil {
		d.NewID = NewID
	}
	if d.Progress == nil {
		d.Progress = func(Event) {}
	}
	if d.LinkWait <= 0 {
		d.LinkWait = DefaultLinkWait
	}
	if d.Rediscover <= 0 {
		d.Rediscover = DefaultRediscover
	}
	if d.ProbeEvery <= 0 {
		d.ProbeEvery = DefaultProbeEvery
	}
	return &Enroller{d: d}, nil
}

type Request struct {
	Slot    domain.Slot
	Carrier string
}

type Result struct {
	OperationID          string      `json:"operation_id"`
	Slot                 domain.Slot `json:"slot"`
	IfName               string      `json:"ifname"`
	IDPath               string      `json:"id_path"`
	USBPath              string      `json:"usb_path"`
	MAC                  string      `json:"mac"`
	DongleID             string      `json:"dongle_id"`
	ProxyID              string      `json:"proxy_id"`
	IMEI                 string      `json:"imei"`
	ICCID                string      `json:"iccid"`
	IMSI                 string      `json:"imsi"`
	Firmware             string      `json:"firmware"`
	DeviceName           string      `json:"device_name"`
	LanIP                netip.Addr  `json:"lan_ip"`
	LanIPChangeSupported bool        `json:"lan_ip_change_supported"`
	SocksPort            int         `json:"socks_port"`
	HTTPPort             int         `json:"http_port"`
	Username             string      `json:"username"`
	Password             string      `json:"password"`
	SelftestRan          bool        `json:"selftest_ran"`
	SelftestNote         string      `json:"selftest_note"`
	Events               []Event     `json:"events"`
}

type session struct {
	e        *Enroller
	res      *Result
	index    int
	rollback []func(context.Context)
	disabled []string
	opID     string
}

func (s *session) emit(ctx context.Context, step StepName, format string, args ...any) {
	s.index++
	total := len(Steps())
	ev := Event{Step: step, Index: s.index, Total: total, Detail: fmt.Sprintf(format, args...)}
	s.res.Events = append(s.res.Events, ev)
	s.e.d.Progress(ev)
	if s.opID != "" {
		pct := s.index * 100 / total
		if pct > 99 {
			pct = 99
		}
		_ = s.e.d.Repos.Operations().Progress(ctx, s.opID, domain.OpRunning, string(step), pct)
	}
}

func (s *session) fail(step StepName, err error) error {
	ev := Event{Step: step, Index: s.index, Total: len(Steps()), Detail: "failed", Error: err.Error()}
	s.res.Events = append(s.res.Events, ev)
	s.e.d.Progress(ev)
	return err
}

func (s *session) undo(f func(context.Context)) { s.rollback = append(s.rollback, f) }

func (s *session) unwind(ctx context.Context) {
	for i := len(s.rollback) - 1; i >= 0; i-- {
		s.rollback[i](ctx)
	}
	s.rollback = nil
}

func (e *Enroller) Enroll(ctx context.Context, req Request) (*Result, error) {
	res := &Result{}
	s := &session{e: e, res: res}

	op := domain.Operation{
		ID:          e.d.NewID(),
		Kind:        domain.OpEnroll,
		SubjectType: domain.SubjectNode,
		SubjectID:   e.d.NodeID,
		State:       domain.OpRunning,
		Step:        string(StepGuardAddresses),
		StartedAt:   e.now(),
		DeadlineAt:  e.now() + (e.d.LinkWait + e.d.Rediscover + 2*time.Minute).Milliseconds(),
		Trigger:     domain.TriggerAdminUI,
		ActorType:   domain.ActorAdmin,
		CreatedAt:   e.now(),
		UpdatedAt:   e.now(),
	}
	if err := e.d.Repos.Operations().Create(ctx, op); err != nil {
		return res, err
	}
	res.OperationID = op.ID
	s.opID = op.ID

	err := e.run(ctx, s, req)
	if err != nil {
		clean := context.WithoutCancel(ctx)
		s.unwind(clean)
		_ = e.d.Repos.Operations().Finish(clean, op.ID, domain.OpFailed, err.Error(), "", e.now())
		return res, err
	}
	payload, _ := json.Marshal(res)
	_ = e.d.Repos.Operations().Finish(ctx, op.ID, domain.OpSucceeded, "", string(payload), e.now())
	return res, nil
}

func (e *Enroller) run(ctx context.Context, s *session, req Request) error {
	obs, err := e.d.Netcfg.Observe(ctx)
	if err != nil {
		return s.fail(StepGuardAddresses, err)
	}
	if err := CheckAddresses(obs); err != nil {
		return s.fail(StepGuardAddresses, err)
	}
	candidate := FactoryDefaultIface(obs)
	s.emit(ctx, StepGuardAddresses, "no duplicate address, %d link(s) on %s", countFactory(obs), device.FactoryDefaultSubnet)

	if err := e.guardPorts(ctx, s, candidate); err != nil {
		return s.fail(StepDisablePorts, err)
	}

	iface, link, err := e.awaitLink(ctx, s, obs, candidate)
	if err != nil {
		return s.fail(StepAwaitLink, err)
	}
	idPath, err := e.idPath(ctx, iface)
	if err != nil {
		return s.fail(StepAwaitLink, err)
	}
	s.res.IfName = iface
	s.res.IDPath = idPath
	s.res.USBPath = e.usbPath(iface, idPath)
	s.res.MAC = link.MAC
	s.emit(ctx, StepAwaitLink, "%s carries ID_PATH %s", iface, idPath)

	dev, err := e.d.Devices.ForAddr(ctx, device.FactoryDefaultAddr)
	if err != nil {
		return s.fail(StepLoginCheck, err)
	}
	login, err := dev.LoginRequired(ctx)
	if err != nil {
		return s.fail(StepLoginCheck, err)
	}
	if login {
		return s.fail(StepLoginCheck, ErrLoginProtected)
	}
	s.emit(ctx, StepLoginCheck, "hilink_login reports no password is set")

	state, err := dev.PinStatus(ctx)
	if err != nil {
		return s.fail(StepSimCheck, err)
	}
	if !state.Usable() {
		return s.fail(StepSimCheck, fmt.Errorf("%w: %s", ErrSimNotReady, SimStateText(state)))
	}
	s.emit(ctx, StepSimCheck, "sim state %d (%s)", int(state), SimStateText(state))

	info, err := dev.Information(ctx)
	if err != nil {
		return s.fail(StepIdentify, err)
	}
	slot, err := e.allocate(ctx, req.Slot)
	if err != nil {
		return s.fail(StepIdentify, err)
	}
	s.res.Slot = slot
	s.res.IMEI = info.IMEI
	s.res.ICCID = info.ICCID
	s.res.IMSI = info.IMSI
	s.res.Firmware = info.SoftwareVersion
	s.res.DeviceName = info.DeviceName
	s.res.SocksPort = slot.SocksPort()
	s.res.HTTPPort = slot.HTTPPort()
	s.res.LanIP = slot.GatewayIP()
	s.emit(ctx, StepIdentify, "%s imei=%s iccid=%s firmware=%s takes slot %s",
		info.DeviceName, info.IMEI, info.ICCID, info.SoftwareVersion, slot)

	if err := e.d.Netcfg.ApplySlot(ctx, slot, idPath, link.MAC); err != nil {
		return s.fail(StepApplyNetcfg, err)
	}
	s.undo(func(c context.Context) { _ = e.d.Netcfg.RemoveSlot(c, slot) })
	s.emit(ctx, StepApplyNetcfg, "%s and %s written and reloaded before the dhcp write",
		slot.LinkFileName(), slot.NetworkFileName())

	supported, err := e.moveLanIP(ctx, s, dev, slot)
	if err != nil {
		return s.fail(StepMoveLanIP, err)
	}
	s.res.LanIPChangeSupported = supported

	slotDev := dev
	if supported {
		slotDev, err = e.d.Devices.ForSlot(ctx, slot)
		if err != nil {
			return s.fail(StepMoveLanIP, err)
		}
	}

	if err := e.disableMaxIdle(ctx, s, slotDev); err != nil {
		return s.fail(StepMaxIdle, err)
	}

	if err := e.publish(ctx, s, slot, info, req.Carrier, supported); err != nil {
		return s.fail(StepPublish, err)
	}

	s.rollback = nil
	e.releasePorts(ctx, s)
	return nil
}

func (e *Enroller) now() int64 { return e.d.Clock.Now().UnixMilli() }

func countFactory(obs netcfg.Observation) int {
	n := 0
	for _, l := range obs.Links {
		for _, p := range l.Addrs {
			if device.FactoryDefaultSubnet.Contains(p.Addr()) {
				n++
				break
			}
		}
	}
	return n
}

type Conflict struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

const (
	ConflictDuplicateAddr = "duplicate_address"
	ConflictFactoryMulti  = "factory_default_multi"
	ConflictFactoryBack   = "factory_default_present"
)

func AddressConflicts(obs netcfg.Observation) []Conflict {
	var out []Conflict
	for _, p := range obs.DuplicateAddrs {
		out = append(out, Conflict{Kind: ConflictDuplicateAddr, Detail: p.String() + " is held by more than one interface"})
	}
	var factory []string
	for name, l := range obs.Links {
		for _, p := range l.Addrs {
			if device.FactoryDefaultSubnet.Contains(p.Addr()) {
				factory = append(factory, name+" "+p.String())
				break
			}
		}
	}
	sort.Strings(factory)
	switch {
	case len(factory) > 1:
		out = append(out, Conflict{Kind: ConflictFactoryMulti, Detail: strings.Join(factory, ", ")})
	case len(factory) == 1:
		if _, provisioned := domain.ParseIfaceName(strings.Fields(factory[0])[0]); provisioned {
			out = append(out, Conflict{Kind: ConflictFactoryBack,
				Detail: factory[0] + " fell back to the factory default; the dongle was probably reset"})
		}
	}
	return out
}

func CheckAddresses(obs netcfg.Observation) error {
	for _, c := range AddressConflicts(obs) {
		switch c.Kind {
		case ConflictDuplicateAddr:
			return fmt.Errorf("%w: %s", ErrAddressConflict, c.Detail)
		case ConflictFactoryMulti:
			return fmt.Errorf("%w: %s", ErrDuplicateAddr, c.Detail)
		case ConflictFactoryBack:
			return fmt.Errorf("%w: %s", ErrDuplicateAddr, c.Detail)
		}
	}
	return nil
}

func FactoryDefaultIface(obs netcfg.Observation) string {
	names := make([]string, 0, len(obs.Links))
	for name, l := range obs.Links {
		for _, p := range l.Addrs {
			if device.FactoryDefaultSubnet.Contains(p.Addr()) {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func (e *Enroller) guardPorts(ctx context.Context, s *session, keep string) error {
	if e.d.USB == nil || e.d.SkipUSBGuard {
		s.emit(ctx, StepDisablePorts, "usb port control disabled; make sure only one un-provisioned dongle is plugged in")
		return nil
	}
	nets, err := e.d.USB.USBNets()
	if err != nil {
		return err
	}
	var disabled []string
	for _, n := range nets {
		if n.Provisioned || n.Iface == keep {
			continue
		}
		if err := e.d.USB.DisablePort(n.Device); err != nil {
			for _, d := range disabled {
				_ = e.d.USB.EnablePort(d)
			}
			return err
		}
		disabled = append(disabled, n.Device)
	}
	s.disabled = disabled
	if len(disabled) > 0 {
		s.undo(func(context.Context) {
			for _, d := range disabled {
				_ = e.d.USB.EnablePort(d)
			}
		})
	}
	s.emit(ctx, StepDisablePorts, "disabled %d un-provisioned usb port(s): %s", len(disabled), strings.Join(disabled, " "))
	return nil
}

func (e *Enroller) releasePorts(ctx context.Context, s *session) {
	if e.d.USB == nil || len(s.disabled) == 0 {
		s.emit(ctx, StepEnablePorts, "no usb port was disabled; you can plug in the next dongle")
		return
	}
	failed := 0
	for _, d := range s.disabled {
		if err := e.d.USB.EnablePort(d); err != nil {
			failed++
		}
	}
	total := len(s.disabled)
	s.disabled = nil
	s.emit(ctx, StepEnablePorts, "re-enabled %d usb port(s), %d failed; you can plug in the next dongle",
		total-failed, failed)
}

func (e *Enroller) awaitLink(ctx context.Context, s *session, obs netcfg.Observation, candidate string) (string, netcfg.LinkState, error) {
	if candidate != "" {
		return candidate, obs.Links[candidate], nil
	}
	events, cancel, err := e.d.Netcfg.Subscribe(ctx)
	if err != nil {
		return "", netcfg.LinkState{}, err
	}
	defer cancel()

	deadline := time.NewTimer(e.d.LinkWait)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", netcfg.LinkState{}, ctx.Err()
		case <-deadline.C:
			return "", netcfg.LinkState{}, fmt.Errorf("%w: waited %s", ErrNoLink, e.d.LinkWait)
		case ev, ok := <-events:
			if !ok {
				return "", netcfg.LinkState{}, ErrNoLink
			}
			if ev.Kind == netcfg.LinkDeleted {
				continue
			}
			if _, provisioned := domain.ParseIfaceName(ev.Link.Name); provisioned {
				continue
			}
			if _, existed := obs.Links[ev.Link.Name]; existed && ev.Kind == netcfg.LinkAdded {
				continue
			}
			fresh, err := e.d.Netcfg.Observe(ctx)
			if err != nil {
				return "", netcfg.LinkState{}, err
			}
			if err := CheckAddresses(fresh); err != nil {
				return "", netcfg.LinkState{}, err
			}
			name := FactoryDefaultIface(fresh)
			if name == "" {
				continue
			}
			return name, fresh.Links[name], nil
		}
	}
}

func (e *Enroller) usbPath(iface, fallback string) string {
	if e.d.USB == nil {
		return fallback
	}
	dev, err := e.d.USB.DeviceName(iface)
	if err != nil {
		return fallback
	}
	return dev
}

func (e *Enroller) idPath(ctx context.Context, iface string) (string, error) {
	if e.d.USB == nil {
		return "", fmt.Errorf("%w: usb controller is required to read ID_PATH", ErrDeps)
	}
	return e.d.USB.IDPath(ctx, iface)
}

func (e *Enroller) allocate(ctx context.Context, want domain.Slot) (domain.Slot, error) {
	if want == 0 {
		return e.d.Repos.Slots().NextFree(ctx, e.d.NodeID)
	}
	if !want.Valid() {
		return 0, fmt.Errorf("%w: slot %d is outside 1-%d", domain.ErrInvalid, int(want), domain.MaxSlots)
	}
	row, err := e.d.Repos.Slots().GetBySlot(ctx, e.d.NodeID, want)
	if errors.Is(err, domain.ErrNotFound) {
		return want, nil
	}
	if err != nil {
		return 0, err
	}
	if row.Occupied() {
		return 0, fmt.Errorf("%w: slot %s", ErrSlotTaken, want)
	}
	return want, nil
}

func DesiredDHCP(cur device.DHCPSettings, s domain.Slot) device.DHCPSettings {
	out := cur
	out.DHCPIPAddress = s.GatewayIP()
	out.DHCPLanNetmask = netip.AddrFrom4([4]byte{255, 255, 255, 0})
	out.DHCPStatus = true
	out.DHCPStartIPAddress = slotAddr(s, DHCPPoolStartOctet)
	out.DHCPEndIPAddress = slotAddr(s, DHCPPoolEndOctet)
	if out.DHCPLeaseTime <= 0 {
		out.DHCPLeaseTime = 86400
	}
	out.DNSStatus = true
	out.PrimaryDNS = s.GatewayIP()
	if !out.SecondaryDNS.IsValid() || device.FactoryDefaultSubnet.Contains(out.SecondaryDNS) {
		out.SecondaryDNS = s.GatewayIP()
	}
	return out
}

func slotAddr(s domain.Slot, last byte) netip.Addr {
	b := s.Subnet().Addr().As4()
	b[3] = last
	return netip.AddrFrom4(b)
}

func (e *Enroller) moveLanIP(ctx context.Context, s *session, dev device.Device, slot domain.Slot) (bool, error) {
	cur, err := dev.DHCPSettings(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrUnsupported) {
			return e.unsupportedLanIP(ctx, s, slot, err)
		}
		return false, err
	}
	want := DesiredDHCP(cur, slot)
	if cur.DHCPIPAddress == want.DHCPIPAddress {
		s.emit(ctx, StepMoveLanIP, "lan is already %s", want.DHCPIPAddress)
		return true, nil
	}

	err = dev.SetDHCPSettings(ctx, want)
	switch {
	case err == nil:
		s.emit(ctx, StepMoveLanIP, "dhcp/settings accepted, re-probing at %s", want.DHCPIPAddress)
	case errors.Is(err, domain.ErrUnsupported):
		return e.unsupportedLanIP(ctx, s, slot, err)
	case IsProbablySuccess(err):
		s.emit(ctx, StepMoveLanIP, "the device stopped answering at %s, which is the expected success signal; re-probing at %s",
			device.FactoryDefaultAddr, want.DHCPIPAddress)
	default:
		return false, err
	}

	if err := e.rediscover(ctx, slot); err != nil {
		return false, err
	}
	s.emit(ctx, StepMoveLanIP, "dongle answers at %s", slot.GatewayIP())
	return true, nil
}

func (e *Enroller) unsupportedLanIP(ctx context.Context, s *session, slot domain.Slot, cause error) (bool, error) {
	s.emit(ctx, StepMoveLanIP, "dongle answered 100002: %v; recording lan_ip_change_supported=false, slot %s needs the manual namespace in OPERATIONS.md",
		cause, slot)
	return false, nil
}

func IsProbablySuccess(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, domain.ErrUnreachable) {
		return true
	}
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, sig := range []string{"timeout", "deadline exceeded", "connection reset", "eof", "no route to host", "connection refused"} {
		if strings.Contains(text, sig) {
			return true
		}
	}
	return false
}

func (e *Enroller) rediscover(ctx context.Context, slot domain.Slot) error {
	deadline := e.d.Clock.Now().Add(e.d.Rediscover)
	var last error
	for {
		dev, err := e.d.Devices.ForSlot(ctx, slot)
		if err == nil && dev.Reachable(ctx) {
			return nil
		}
		last = err
		if !e.d.Clock.Now().Before(deadline) {
			break
		}
		if err := e.d.Clock.Sleep(ctx, e.d.ProbeEvery); err != nil {
			return err
		}
	}
	if last == nil {
		last = domain.ErrUnreachable
	}
	return fmt.Errorf("enroll: dongle never answered at %s within %s: %w", slot.GatewayIP(), e.d.Rediscover, last)
}

func (e *Enroller) disableMaxIdle(ctx context.Context, s *session, dev device.Device) error {
	if err := dev.SetMaxIdleTime(ctx, device.MaxIdleTimeDisabled); err != nil {
		return err
	}
	got, err := dev.GetMaxIdleTime(ctx)
	if err != nil {
		return err
	}
	if got != device.MaxIdleTimeDisabled {
		return fmt.Errorf("%w: GET reports %d", ErrMaxIdleNotZero, got)
	}
	s.emit(ctx, StepMaxIdle, "MaxIdelTime verified at 0, the session will not drop after %ds", device.MaxIdleTimeDefault)
	return nil
}

func (e *Enroller) publish(ctx context.Context, s *session, slot domain.Slot, info device.Info, carrier string, lanIPOK bool) error {
	iface := slot.IfaceName()
	if err := e.d.Firewall.AddDongle(ctx, iface, slot.GatewayIP()); err != nil {
		return err
	}
	s.undo(func(c context.Context) { _ = e.d.Firewall.RemoveDongle(c, iface) })

	now := e.now()
	dongle := domain.Dongle{
		ID:                   e.d.NewID(),
		NodeID:               e.d.NodeID,
		IMEI:                 info.IMEI,
		ICCID:                info.ICCID,
		IMSI:                 info.IMSI,
		FirmwareVer:          info.SoftwareVersion,
		HwVer:                info.HardwareVersion,
		Classify:             domain.ClassifyHiLink,
		Carrier:              carrier,
		LanIPChangeSupported: lanIPOK,
		HilinkLoginRequired:  false,
		AutoRecoverEnabled:   true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := e.d.Repos.Dongles().Create(ctx, dongle); err != nil {
		return err
	}
	s.undo(func(c context.Context) { _ = e.d.Repos.Dongles().Delete(c, dongle.ID) })
	s.res.DongleID = dongle.ID

	slotRow, err := e.upsertSlot(ctx, s, slot, dongle.ID)
	if err != nil {
		return err
	}

	username, err := NewUsername()
	if err != nil {
		return err
	}
	password, err := proxysup.NewPassword()
	if err != nil {
		return err
	}
	proxy := domain.Proxy{
		ID:        e.d.NewID(),
		SlotID:    slotRow.ID,
		Enabled:   true,
		SocksPort: slot.SocksPort(),
		HTTPPort:  slot.HTTPPort(),
		Username:  username,
		Password:  password,
		AuthMode:  domain.AuthUserPass,
		Policy:    domain.DefaultProxyPolicy(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.d.Repos.Proxies().Create(ctx, proxy); err != nil {
		return err
	}
	s.undo(func(c context.Context) { _ = e.d.Repos.Proxies().Delete(c, proxy.ID) })
	s.res.ProxyID = proxy.ID
	s.res.Username = username
	s.res.Password = password

	spec := proxysup.NewSpec(slot, e.d.PublicHost, e.d.NServerFallback)
	spec.Users = []proxysup.User{{Name: username, Password: password}}
	spec.AuthMode = domain.AuthUserPass
	spec.Policy = proxy.Policy
	if err := spec.Validate(); err != nil {
		return err
	}
	applied, err := e.d.Supervisor.Apply(ctx, spec)
	if err != nil {
		return err
	}
	s.undo(func(c context.Context) { _ = e.d.Supervisor.Stop(c, slot, true) })
	s.emit(ctx, StepPublish, "%s bound socks %d and http %d as %s",
		spec.Unit(), spec.SocksPort, spec.HTTPPort, username)

	if !applied.Status.Healthy() {
		return fmt.Errorf("%w: %s", proxysup.ErrNotBound, applied.Status.Error)
	}
	return e.selftest(ctx, s, proxy.ID)
}

func (e *Enroller) selftest(ctx context.Context, s *session, proxyID string) error {
	if e.d.Selftest == nil {
		s.res.SelftestNote = "no selftest hook wired; run `" + strings.ToLower(string(domain.OpSelftest)) + "` from the panel once rotate is installed"
		s.emit(ctx, StepPublish, "selftest skipped: %s", s.res.SelftestNote)
		return nil
	}
	if err := e.d.Selftest(ctx, proxyID); err != nil {
		return err
	}
	s.res.SelftestRan = true
	s.res.SelftestNote = "selftest passed"
	s.emit(ctx, StepPublish, "selftest passed")
	return nil
}

func (e *Enroller) upsertSlot(ctx context.Context, s *session, slot domain.Slot, dongleID string) (domain.SlotRow, error) {
	now := e.now()
	row, err := e.d.Repos.Slots().GetBySlot(ctx, e.d.NodeID, slot)
	if errors.Is(err, domain.ErrNotFound) {
		row = domain.SlotRow{
			ID:        e.d.NewID(),
			NodeID:    e.d.NodeID,
			Slot:      slot,
			IDPath:    s.res.IDPath,
			USBPath:   s.res.USBPath,
			IfName:    slot.IfaceName(),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := e.d.Repos.Slots().Create(ctx, row); err != nil {
			return domain.SlotRow{}, err
		}
		s.undo(func(c context.Context) { _ = e.d.Repos.Slots().Delete(c, row.ID) })
	} else if err != nil {
		return domain.SlotRow{}, err
	} else {
		row.IDPath = s.res.IDPath
		row.USBPath = s.res.USBPath
		row.IfName = slot.IfaceName()
		row.UpdatedAt = now
		if err := e.d.Repos.Slots().Update(ctx, row); err != nil {
			return domain.SlotRow{}, err
		}
	}
	if err := e.d.Repos.Slots().Attach(ctx, row.ID, dongleID); err != nil {
		return domain.SlotRow{}, err
	}
	s.undo(func(c context.Context) { _ = e.d.Repos.Slots().Detach(c, row.ID) })
	row.DongleID = &dongleID
	return row, nil
}

func SimStateText(s device.SimState) string {
	switch s {
	case device.SimStateNoSIM:
		return "no SIM inserted"
	case device.SimStateCPINError:
		return "CPIN error"
	case device.SimStateReady:
		return "ready"
	case device.SimStatePINDisabled:
		return "PIN disabled"
	case device.SimStatePINChecking:
		return "PIN checking"
	case device.SimStatePINRequired:
		return "PIN required, unlock the SIM in another phone first"
	case device.SimStatePUKRequired:
		return "PUK required"
	default:
		return "unknown state"
	}
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

func NewUsername() (string, error) {
	var b [usernameRandomBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return UsernamePrefix + hex.EncodeToString(b[:]), nil
}
