package enroll

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/secrets"
	"github.com/n4darae/huawei-API/src/internal/store"
)

type trace struct {
	mu    sync.Mutex
	steps []string
}

func (t *trace) add(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.steps = append(t.steps, s)
}

func (t *trace) all() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.steps...)
}

func (t *trace) indexOf(s string) int {
	for i, v := range t.all() {
		if v == s {
			return i
		}
	}
	return -1
}

type stubNetcfg struct {
	tr      *trace
	obs     netcfg.Observation
	applyFn func(domain.Slot, string, string) error
	events  chan netcfg.LinkEvent
	applied []domain.Slot
	removed []domain.Slot
}

func (m *stubNetcfg) EnsureGlobal(context.Context, []netip.Addr) error { return nil }

func (m *stubNetcfg) EnsureRouteTableNames(context.Context) error { return nil }

func (m *stubNetcfg) ApplySlot(_ context.Context, s domain.Slot, idPath, mac string) error {
	m.tr.add("netcfg.ApplySlot")
	if m.applyFn != nil {
		if err := m.applyFn(s, idPath, mac); err != nil {
			return err
		}
	}
	m.applied = append(m.applied, s)
	return nil
}

func (m *stubNetcfg) RemoveSlot(_ context.Context, s domain.Slot) error {
	m.tr.add("netcfg.RemoveSlot")
	m.removed = append(m.removed, s)
	return nil
}

func (m *stubNetcfg) Observe(context.Context) (netcfg.Observation, error) { return m.obs, nil }

func (m *stubNetcfg) AssertInvariants(context.Context) []netcfg.Violation { return nil }

func (m *stubNetcfg) Subscribe(context.Context) (<-chan netcfg.LinkEvent, func(), error) {
	if m.events == nil {
		m.events = make(chan netcfg.LinkEvent)
	}
	return m.events, func() {}, nil
}

type stubFirewall struct {
	fw.Fake
	tr      *trace
	added   []string
	removed []string
	addErr  error
}

func (f *stubFirewall) AddDongle(_ context.Context, iface string, _ netip.Addr) error {
	f.tr.add("fw.AddDongle")
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, iface)
	return nil
}

func (f *stubFirewall) RemoveDongle(_ context.Context, iface string) error {
	f.tr.add("fw.RemoveDongle")
	f.removed = append(f.removed, iface)
	return nil
}

type stubSupervisor struct {
	tr       *trace
	applyErr error
	healthy  bool
	stopped  []domain.Slot
}

func (s *stubSupervisor) Apply(_ context.Context, sp proxysup.Spec) (proxysup.Applied, error) {
	s.tr.add("proxysup.Apply")
	if s.applyErr != nil {
		return proxysup.Applied{}, s.applyErr
	}
	return proxysup.Applied{
		Slot:   sp.Slot,
		Status: proxysup.Status{Running: true, SocksBound: s.healthy, HTTPBound: s.healthy, ProbeOK: s.healthy},
	}, nil
}

func (s *stubSupervisor) Stop(_ context.Context, slot domain.Slot, _ bool) error {
	s.tr.add("proxysup.Stop")
	s.stopped = append(s.stopped, slot)
	return nil
}

func (s *stubSupervisor) Status(context.Context, domain.Slot) (proxysup.Status, error) {
	return proxysup.Status{}, nil
}

type nopDevice struct{}

func (nopDevice) Information(context.Context) (device.Info, error) {
	return device.Info{}, domain.ErrNotImplemented
}
func (nopDevice) Signal(context.Context) (device.Signal, error) {
	return device.Signal{}, domain.ErrNotImplemented
}
func (nopDevice) Status(context.Context) (device.Status, error) {
	return device.Status{}, domain.ErrNotImplemented
}
func (nopDevice) DataSwitch(context.Context, bool) error      { return domain.ErrNotImplemented }
func (nopDevice) SetMaxIdleTime(context.Context, int) error   { return domain.ErrNotImplemented }
func (nopDevice) GetMaxIdleTime(context.Context) (int, error) { return 0, domain.ErrNotImplemented }
func (nopDevice) NetMode(context.Context) (device.NetMode, error) {
	return "", domain.ErrNotImplemented
}
func (nopDevice) SetNetMode(context.Context, device.NetMode) error { return domain.ErrNotImplemented }
func (nopDevice) Reboot(context.Context) error                     { return domain.ErrNotImplemented }
func (nopDevice) DHCPSettings(context.Context) (device.DHCPSettings, error) {
	return device.DHCPSettings{}, domain.ErrNotImplemented
}
func (nopDevice) SetDHCPSettings(context.Context, device.DHCPSettings) error {
	return domain.ErrNotImplemented
}
func (nopDevice) Traffic(context.Context) (device.Traffic, error) {
	return device.Traffic{}, domain.ErrNotImplemented
}
func (nopDevice) MonthStats(context.Context) (device.MonthStats, error) {
	return device.MonthStats{}, domain.ErrNotImplemented
}
func (nopDevice) SMSList(context.Context, device.SMSBox, int, int) ([]device.SMS, int, error) {
	return nil, 0, domain.ErrNotImplemented
}
func (nopDevice) SMSSend(context.Context, []string, string) error { return domain.ErrNotImplemented }
func (nopDevice) SMSDelete(context.Context, int64) error          { return domain.ErrNotImplemented }
func (nopDevice) SMSSetRead(context.Context, int64) error         { return domain.ErrNotImplemented }
func (nopDevice) PinStatus(context.Context) (device.SimState, error) {
	return 0, domain.ErrNotImplemented
}
func (nopDevice) LoginRequired(context.Context) (bool, error) { return false, domain.ErrNotImplemented }
func (nopDevice) Reachable(context.Context) bool              { return false }

type stubDevice struct {
	nopDevice
	tr *trace

	login     bool
	loginErr  error
	sim       device.SimState
	info      device.Info
	dhcp      device.DHCPSettings
	dhcpErr   error
	setDHCP   func(device.DHCPSettings) error
	maxIdle   int
	setIdle   func(int) error
	reachable bool
}

func (d *stubDevice) LoginRequired(context.Context) (bool, error) {
	d.tr.add("device.LoginRequired")
	return d.login, d.loginErr
}

func (d *stubDevice) PinStatus(context.Context) (device.SimState, error) {
	d.tr.add("device.PinStatus")
	return d.sim, nil
}

func (d *stubDevice) Information(context.Context) (device.Info, error) {
	d.tr.add("device.Information")
	return d.info, nil
}

func (d *stubDevice) DHCPSettings(context.Context) (device.DHCPSettings, error) {
	d.tr.add("device.DHCPSettings")
	return d.dhcp, d.dhcpErr
}

func (d *stubDevice) SetDHCPSettings(_ context.Context, s device.DHCPSettings) error {
	d.tr.add("device.SetDHCPSettings")
	if d.setDHCP != nil {
		return d.setDHCP(s)
	}
	d.dhcp = s
	return nil
}

func (d *stubDevice) SetMaxIdleTime(_ context.Context, n int) error {
	d.tr.add("device.SetMaxIdleTime")
	if d.setIdle != nil {
		return d.setIdle(n)
	}
	d.maxIdle = n
	return nil
}

func (d *stubDevice) GetMaxIdleTime(context.Context) (int, error) {
	d.tr.add("device.GetMaxIdleTime")
	return d.maxIdle, nil
}

func (d *stubDevice) Reachable(context.Context) bool { return d.reachable }

type stubRegistry struct {
	factory *stubDevice
	slot    *stubDevice
}

func (r *stubRegistry) ForSlot(context.Context, domain.Slot) (device.Device, error) {
	if r.slot == nil {
		return nil, domain.ErrUnreachable
	}
	return r.slot, nil
}

func (r *stubRegistry) ForAddr(_ context.Context, a netip.Addr) (device.Device, error) {
	if a == device.FactoryDefaultAddr {
		return r.factory, nil
	}
	if r.slot == nil {
		return nil, domain.ErrUnreachable
	}
	return r.slot, nil
}

func (r *stubRegistry) Close() error { return nil }

func factoryObservation(names ...string) netcfg.Observation {
	links := map[string]netcfg.LinkState{
		"lo":       {Name: "lo", OperState: netcfg.OperStateUp},
		"enp1s0f0": {Name: "enp1s0f0", OperState: netcfg.OperStateUp, Addrs: []netip.Prefix{netip.MustParsePrefix("203.0.113.7/24")}},
	}
	for i, n := range names {
		links[n] = netcfg.LinkState{
			Name:      n,
			MAC:       "0c:5b:8f:27:9a:64",
			OperState: netcfg.OperStateUnknown,
			Addrs:     []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("192.168.8.%d/24", 100+i))},
		}
	}
	return netcfg.Observation{Links: links, RpFilterAll: 2}
}

func openRepos(t *testing.T) store.Repos {
	t.Helper()
	kek, err := secrets.GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	sealer, err := secrets.NewSealer(kek)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "state", "dongled.db"), sealer)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.Nodes().Upsert(context.Background(), domain.Node{
		ID: "local", Name: "local", Kind: domain.NodeKindLocal, PublicHost: testPublic,
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return s
}

type harness struct {
	t     *testing.T
	tr    *trace
	nc    *stubNetcfg
	fwall *stubFirewall
	sup   *stubSupervisor
	reg   *stubRegistry
	repos store.Repos
	usb   *USBController
	deps  Deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	tr := &trace{}
	factory := &stubDevice{
		tr:  tr,
		sim: device.SimStateReady,
		info: device.Info{
			DeviceName:      "E3372h-320",
			IMEI:            "867857031234567",
			ICCID:           "89014103211118510720",
			IMSI:            "452040000000001",
			SoftwareVersion: "10.0.1.1(H192SP1C983)",
			HardwareVersion: "CL2E3372HM",
		},
		dhcp: device.DHCPSettings{
			DHCPIPAddress:      device.FactoryDefaultAddr,
			DHCPLanNetmask:     netip.MustParseAddr("255.255.255.0"),
			DHCPStatus:         true,
			DHCPStartIPAddress: netip.MustParseAddr("192.168.8.100"),
			DHCPEndIPAddress:   netip.MustParseAddr("192.168.8.200"),
			DHCPLeaseTime:      86400,
			DNSStatus:          true,
			PrimaryDNS:         device.FactoryDefaultAddr,
			SecondaryDNS:       device.FactoryDefaultAddr,
		},
		maxIdle: device.MaxIdleTimeDefault,
	}
	slotDev := &stubDevice{tr: tr, sim: device.SimStateReady, info: factory.info, reachable: true, maxIdle: device.MaxIdleTimeDefault}

	h := &harness{
		t:     t,
		tr:    tr,
		nc:    &stubNetcfg{tr: tr, obs: factoryObservation("usb0")},
		fwall: &stubFirewall{tr: tr},
		sup:   &stubSupervisor{tr: tr, healthy: true},
		reg:   &stubRegistry{factory: factory, slot: slotDev},
		repos: openRepos(t),
		usb: NewUSBController(USBOptions{
			SysfsRoot: fixtureSysfs,
			Exec: func(context.Context, string, ...string) ([]byte, error) {
				return []byte("ID_PATH=pci-0000:00:14.0-usb-0:13.1:1.0\n"), nil
			},
		}),
	}
	h.deps = Deps{
		NodeID:          "local",
		PublicHost:      testPublic,
		NServerFallback: netip.MustParseAddr("1.1.1.1"),
		Netcfg:          h.nc,
		Firewall:        h.fwall,
		Devices:         h.reg,
		Repos:           h.repos,
		Supervisor:      h.sup,
		USB:             h.usb,
		SkipUSBGuard:    true,
		Rediscover:      2 * time.Second,
		ProbeEvery:      time.Millisecond,
	}
	return h
}

func (h *harness) enroll(slot domain.Slot) (*Result, error) {
	h.t.Helper()
	e, err := New(h.deps)
	if err != nil {
		h.t.Fatalf("New: %v", err)
	}
	return e.Enroll(context.Background(), Request{Slot: slot, Carrier: "viettel"})
}

func TestEnrollProvisionsASlotEndToEnd(t *testing.T) {
	h := newHarness(t)
	res, err := h.enroll(3)
	if err != nil {
		t.Fatalf("Enroll: %v\n%+v", err, res.Events)
	}
	if res.Slot != 3 {
		t.Fatalf("slot = %s", res.Slot)
	}
	if res.IDPath != "pci-0000:00:14.0-usb-0:13.1:1.0" {
		t.Fatalf("id path = %q, must come from udevadm", res.IDPath)
	}
	if res.SocksPort != 21003 || res.HTTPPort != 22003 {
		t.Fatalf("ports = %d/%d", res.SocksPort, res.HTTPPort)
	}
	if res.LanIP != netip.MustParseAddr("192.168.103.1") {
		t.Fatalf("lan ip = %s", res.LanIP)
	}
	if !res.LanIPChangeSupported {
		t.Fatalf("lan ip change must be recorded as supported")
	}

	ctx := context.Background()
	d, err := h.repos.Dongles().GetByIMEI(ctx, res.IMEI)
	if err != nil {
		t.Fatalf("dongle row: %v", err)
	}
	if !d.LanIPChangeSupported || d.Carrier != "viettel" {
		t.Fatalf("dongle row = %+v", d)
	}
	row, err := h.repos.Slots().GetBySlot(ctx, "local", 3)
	if err != nil {
		t.Fatalf("slot row: %v", err)
	}
	if !row.Occupied() || row.IDPath != res.IDPath || row.IfName != "dg03" {
		t.Fatalf("slot row = %+v", row)
	}
	p, err := h.repos.Proxies().GetBySlot(ctx, row.ID)
	if err != nil {
		t.Fatalf("proxy row: %v", err)
	}
	if p.Username != res.Username || p.SocksPort != 21003 {
		t.Fatalf("proxy row = %+v", p)
	}
	op, err := h.repos.Operations().Get(ctx, res.OperationID)
	if err != nil {
		t.Fatalf("operation row: %v", err)
	}
	if op.Kind != domain.OpEnroll || op.State != domain.OpSucceeded {
		t.Fatalf("operation = %s/%s", op.Kind, op.State)
	}
	if len(res.Events) < len(Steps()) {
		t.Fatalf("only %d progress events for %d steps", len(res.Events), len(Steps()))
	}
}

func TestEnrollRefusesTwoFactoryDefaultDongles(t *testing.T) {
	h := newHarness(t)
	h.nc.obs = factoryObservation("usb0", "usb1")

	_, err := h.enroll(1)
	if !errors.Is(err, ErrDuplicateAddr) {
		t.Fatalf("two sticks on 192.168.8.0/24 must be refused, got %v", err)
	}
	if h.tr.indexOf("netcfg.ApplySlot") >= 0 {
		t.Fatalf("nothing may be written once the guard trips")
	}
}

func TestEnrollRefusesADuplicateAddress(t *testing.T) {
	h := newHarness(t)
	obs := factoryObservation("usb0")
	obs.DuplicateAddrs = []netip.Prefix{netip.MustParsePrefix("192.168.103.1/24")}
	h.nc.obs = obs

	_, err := h.enroll(1)
	if !errors.Is(err, ErrAddressConflict) {
		t.Fatalf("a duplicate address must be refused, got %v", err)
	}
}

func TestAddressWatchdogCatchesAFactoryReset(t *testing.T) {
	obs := netcfg.Observation{Links: map[string]netcfg.LinkState{
		"dg07": {Name: "dg07", Addrs: []netip.Prefix{netip.MustParsePrefix("192.168.8.100/24")}},
	}}
	got := AddressConflicts(obs)
	if len(got) != 1 || got[0].Kind != ConflictFactoryBack {
		t.Fatalf("a provisioned interface back on the factory subnet must be reported: %+v", got)
	}
	if err := CheckAddresses(obs); err == nil {
		t.Fatalf("CheckAddresses must refuse it too")
	}
	if len(AddressConflicts(factoryObservation("usb0"))) != 0 {
		t.Fatalf("one un-provisioned stick at the factory default is the normal enrol case")
	}
}

func TestEnrollRefusesALoginProtectedDongle(t *testing.T) {
	h := newHarness(t)
	h.reg.factory.login = true

	_, err := h.enroll(1)
	if !errors.Is(err, ErrLoginProtected) {
		t.Fatalf("a password protected dongle must be refused, got %v", err)
	}
	if !contains(err.Error(), "Require login") {
		t.Fatalf("the message must tell the operator what to switch off: %v", err)
	}
	if h.tr.indexOf("device.PinStatus") >= 0 {
		t.Fatalf("the sequence must stop at the login check")
	}
}

func TestEnrollRefusesEveryUnusableSimState(t *testing.T) {
	unusable := []device.SimState{
		device.SimStateNoSIM,
		device.SimStateCPINError,
		device.SimStatePINChecking,
		device.SimStatePINRequired,
		device.SimStatePUKRequired,
	}
	for _, st := range unusable {
		h := newHarness(t)
		h.reg.factory.sim = st
		if _, err := h.enroll(1); !errors.Is(err, ErrSimNotReady) {
			t.Fatalf("sim state %d must be refused, got %v", int(st), err)
		}
	}
	for _, st := range []device.SimState{device.SimStateReady, device.SimStatePINDisabled} {
		h := newHarness(t)
		h.reg.factory.sim = st
		if _, err := h.enroll(1); err != nil {
			t.Fatalf("sim state %d must be accepted, got %v", int(st), err)
		}
	}
}

func TestEnrollWritesNetcfgBeforeTouchingDhcp(t *testing.T) {
	h := newHarness(t)
	if _, err := h.enroll(4); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	apply := h.tr.indexOf("netcfg.ApplySlot")
	dhcp := h.tr.indexOf("device.SetDHCPSettings")
	if apply < 0 || dhcp < 0 {
		t.Fatalf("both steps must run: %v", h.tr.all())
	}
	if apply > dhcp {
		t.Fatalf("the .link and .network files must exist before the dhcp write, otherwise a re-enumeration renames the interface mid-flight: %v", h.tr.all())
	}
}

func TestEnrollSendsAFullDhcpObjectInsideTheNewSubnet(t *testing.T) {
	h := newHarness(t)
	var sent device.DHCPSettings
	h.reg.factory.setDHCP = func(s device.DHCPSettings) error {
		sent = s
		return nil
	}
	if _, err := h.enroll(5); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if sent.DHCPIPAddress != netip.MustParseAddr("192.168.105.1") {
		t.Fatalf("gateway = %s", sent.DHCPIPAddress)
	}
	for name, a := range map[string]netip.Addr{
		"start": sent.DHCPStartIPAddress,
		"end":   sent.DHCPEndIPAddress,
	} {
		if !domain.Slot(5).Subnet().Contains(a) {
			t.Fatalf("%s address %s is outside the new subnet; the device answers 100005", name, a)
		}
		if a == domain.Slot(5).HostIP() {
			t.Fatalf("the dhcp pool must not contain the host address %s", a)
		}
	}
	if sent.DHCPLeaseTime == 0 || !sent.DHCPStatus {
		t.Fatalf("the full object must be sent, got %+v", sent)
	}
}

func TestEnrollTreatsADhcpTimeoutAsProbableSuccess(t *testing.T) {
	for _, cause := range []error{
		context.DeadlineExceeded,
		errors.New("Post \"http://192.168.8.1/api/dhcp/settings\": context deadline exceeded (Client.Timeout exceeded)"),
		domain.ErrUnreachable,
		errors.New("read tcp 192.168.8.100:41234->192.168.8.1:80: connection reset by peer"),
	} {
		h := newHarness(t)
		h.reg.factory.setDHCP = func(device.DHCPSettings) error { return cause }

		res, err := h.enroll(6)
		if err != nil {
			t.Fatalf("a timeout on the dhcp write means the device already moved, it is not a failure: %v", err)
		}
		if !res.LanIPChangeSupported {
			t.Fatalf("the slot must still be recorded as capable")
		}
		if !IsProbablySuccess(cause) {
			t.Fatalf("IsProbablySuccess(%v) = false", cause)
		}
	}
}

func TestEnrollRecordsLanIPChangeUnsupportedOn100002(t *testing.T) {
	h := newHarness(t)
	h.reg.factory.setDHCP = func(device.DHCPSettings) error {
		return domain.NewAPIError(domain.CodeSystemNoSupport, "no support", "/api/dhcp/settings")
	}
	h.reg.slot = nil

	res, err := h.enroll(7)
	if err != nil {
		t.Fatalf("an unsupported dongle must still enrol: %v", err)
	}
	if res.LanIPChangeSupported {
		t.Fatalf("lan_ip_change_supported must be false")
	}
	d, err := h.repos.Dongles().GetByIMEI(context.Background(), res.IMEI)
	if err != nil {
		t.Fatalf("dongle row: %v", err)
	}
	if d.LanIPChangeSupported {
		t.Fatalf("the false capability must reach the database, it is what points the operator at the manual namespace")
	}
	var pointed bool
	for _, ev := range res.Events {
		if contains(ev.Detail, "OPERATIONS.md") {
			pointed = true
		}
	}
	if !pointed {
		t.Fatalf("the operator must be pointed at the manual procedure: %+v", res.Events)
	}
}

func TestEnrollVerifiesMaxIdleTimeIsActuallyZero(t *testing.T) {
	h := newHarness(t)
	if _, err := h.enroll(8); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if h.reg.slot.maxIdle != device.MaxIdleTimeDisabled {
		t.Fatalf("MaxIdelTime = %d", h.reg.slot.maxIdle)
	}
	if h.tr.indexOf("device.GetMaxIdleTime") < 0 {
		t.Fatalf("the set must be followed by a read back")
	}

	lying := newHarness(t)
	lying.reg.slot.setIdle = func(int) error { return nil }
	lying.reg.slot.maxIdle = device.MaxIdleTimeDefault
	if _, err := lying.enroll(8); !errors.Is(err, ErrMaxIdleNotZero) {
		t.Fatalf("a device that silently keeps 300s must fail enrolment, got %v", err)
	}
}

func TestEnrollRollsBackEverythingWhenPublishFails(t *testing.T) {
	h := newHarness(t)
	h.sup.applyErr = errors.New("3proxy bound no listener")

	res, err := h.enroll(9)
	if err == nil {
		t.Fatalf("Enroll must fail")
	}
	if len(h.nc.removed) != 1 || h.nc.removed[0] != 9 {
		t.Fatalf("the netcfg files must be removed again: %v", h.nc.removed)
	}
	if len(h.fwall.removed) != 1 || h.fwall.removed[0] != "dg09" {
		t.Fatalf("the firewall entry must be removed again: %v", h.fwall.removed)
	}

	ctx := context.Background()
	if _, err := h.repos.Dongles().GetByIMEI(ctx, res.IMEI); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the dongle row must be gone, got %v", err)
	}
	row, err := h.repos.Slots().GetBySlot(ctx, "local", 9)
	if err == nil && row.Occupied() {
		t.Fatalf("slot 9 must not be left half provisioned: %+v", row)
	}
	free, err := h.repos.Slots().NextFree(ctx, "local")
	if err != nil {
		t.Fatalf("NextFree: %v", err)
	}
	if free != 1 {
		t.Fatalf("the allocator must still offer slot 1, got %s", free)
	}
	op, err := h.repos.Operations().Get(ctx, res.OperationID)
	if err != nil {
		t.Fatalf("operation row: %v", err)
	}
	if op.State != domain.OpFailed || op.Error == "" {
		t.Fatalf("the operation must record the failure: %+v", op)
	}
}

func TestEnrollRollsBackWhenTheProxyComesUpWithNoListener(t *testing.T) {
	h := newHarness(t)
	h.sup.healthy = false

	if _, err := h.enroll(10); !errors.Is(err, proxysup.ErrNotBound) {
		t.Fatalf("a running process with no bound listener is a failure, got %v", err)
	}
	if len(h.sup.stopped) != 1 {
		t.Fatalf("the half started instance must be stopped: %v", h.sup.stopped)
	}
}

func TestEnrollLeavesASeamForTheRotateSelftest(t *testing.T) {
	h := newHarness(t)
	res, err := h.enroll(11)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if res.SelftestRan {
		t.Fatalf("no hook is wired, so nothing may claim a selftest ran")
	}
	if res.SelftestNote == "" {
		t.Fatalf("the skipped selftest must be visible in the result")
	}

	wired := newHarness(t)
	var got string
	wired.deps.Selftest = func(_ context.Context, proxyID string) error {
		got = proxyID
		return nil
	}
	res, err = wired.enroll(12)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if got != res.ProxyID || !res.SelftestRan {
		t.Fatalf("the hook must be called with the new proxy id: %q vs %q", got, res.ProxyID)
	}

	failing := newHarness(t)
	failing.deps.Selftest = func(context.Context, string) error { return errors.New("egress leaked to the host uplink") }
	if _, err := failing.enroll(13); err == nil {
		t.Fatalf("a failing selftest must fail the enrolment")
	}
	if len(failing.sup.stopped) != 1 {
		t.Fatalf("and roll the proxy back")
	}
}

func TestEnrollRefusesAnOccupiedSlot(t *testing.T) {
	h := newHarness(t)
	if _, err := h.enroll(2); err != nil {
		t.Fatalf("first enrol: %v", err)
	}
	h2 := newHarness(t)
	h2.repos = h.repos
	h2.deps.Repos = h.repos
	if _, err := h2.enroll(2); !errors.Is(err, ErrSlotTaken) {
		t.Fatalf("slot 2 is taken, got %v", err)
	}
}

func TestEnrollAllocatesTheLowestFreeSlot(t *testing.T) {
	requireSysfsFixture(t)
	h := newHarness(t)
	if _, err := h.enroll(2); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h2 := newHarness(t)
	h2.repos = h.repos
	h2.deps.Repos = h.repos
	h2.reg.factory.info.IMEI = "867857039999999"
	h2.nc.obs = factoryObservation("usb1")
	res, err := h2.enroll(0)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if res.Slot != 1 {
		t.Fatalf("lowest free slot is 1, got %s", res.Slot)
	}
	if res.USBPath != "1-13.3" {
		t.Fatalf("usb_path must be the sysfs bus path uhubctl and usbreset take, got %q", res.USBPath)
	}
}

func TestEnrollFailsWhenUdevadmHasNoIDPath(t *testing.T) {
	h := newHarness(t)
	h.deps.USB = NewUSBController(USBOptions{
		SysfsRoot: fixtureSysfs,
		Exec: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("DEVPATH=/devices/virtual/net/usb0\n"), nil
		},
	})
	if _, err := h.enroll(1); !errors.Is(err, ErrNoIDPath) {
		t.Fatalf("a missing ID_PATH must stop enrolment, got %v", err)
	}
	if h.tr.indexOf("netcfg.ApplySlot") >= 0 {
		t.Fatalf("a .link with a guessed Path= must never be written")
	}
}

func TestEnrollAwaitsANewLinkWhenNothingIsPluggedIn(t *testing.T) {
	h := newHarness(t)
	h.nc.obs = factoryObservation()
	h.nc.events = make(chan netcfg.LinkEvent, 1)
	h.deps.LinkWait = 2 * time.Second

	e, err := New(h.deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		h.nc.obs = factoryObservation("usb0")
		h.nc.events <- netcfg.LinkEvent{Kind: netcfg.LinkAdded, Link: netcfg.LinkState{Name: "usb0"}}
	}()
	res, err := e.Enroll(context.Background(), Request{Slot: 1})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if res.IfName != "usb0" {
		t.Fatalf("ifname = %q", res.IfName)
	}
}

func TestEnrollTimesOutWaitingForALink(t *testing.T) {
	h := newHarness(t)
	h.nc.obs = factoryObservation()
	h.nc.events = make(chan netcfg.LinkEvent)
	h.deps.LinkWait = 30 * time.Millisecond

	e, err := New(h.deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e.Enroll(context.Background(), Request{Slot: 1}); !errors.Is(err, ErrNoLink) {
		t.Fatalf("want ErrNoLink, got %v", err)
	}
}

func TestUSBGuardDisablesEveryOtherUnprovisionedPortForTheSession(t *testing.T) {
	requireSysfsFixture(t)
	root := copyTree(t, fixtureSysfs)
	h := newHarness(t)
	h.deps.SkipUSBGuard = false
	h.deps.USB = NewUSBController(USBOptions{
		SysfsRoot: root,
		Exec: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("ID_PATH=pci-0000:00:14.0-usb-0:13.1:1.0\n"), nil
		},
	})
	h.nc.obs = factoryObservation("usb0")

	port := func(n int) string {
		return filepath.Join(root, usbDevicesRel, "1-13:1.0", fmt.Sprintf("1-13-port%d", n), "disable")
	}
	read := func(p string) string {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(raw)
	}

	var midflight map[int]string
	h.deps.Progress = func(ev Event) {
		if ev.Step != StepAwaitLink || midflight != nil {
			return
		}
		midflight = map[int]string{1: read(port(1)), 2: read(port(2)), 3: read(port(3))}
	}

	if _, err := h.enroll(2); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if midflight == nil {
		t.Fatalf("no progress event was observed")
	}
	if midflight[3] != "1\n" {
		t.Fatalf("usb1 is un-provisioned and not the one being enrolled, its port must be disabled for the session, holds %q", midflight[3])
	}
	if midflight[2] != "0\n" {
		t.Fatalf("the port of the dongle being enrolled must stay enabled, holds %q", midflight[2])
	}
	if midflight[1] != "0\n" {
		t.Fatalf("an already provisioned slot must not be knocked offline, holds %q", midflight[1])
	}
	for n := 1; n <= 3; n++ {
		if got := read(port(n)); got != "0\n" {
			t.Fatalf("port %d must be re-enabled at the end, holds %q", n, got)
		}
	}
}

func TestNewRefusesToRunWithoutItsDependencies(t *testing.T) {
	h := newHarness(t)
	base := h.deps
	for name, mutate := range map[string]func(*Deps){
		"netcfg":      func(d *Deps) { d.Netcfg = nil },
		"firewall":    func(d *Deps) { d.Firewall = nil },
		"devices":     func(d *Deps) { d.Devices = nil },
		"repos":       func(d *Deps) { d.Repos = nil },
		"supervisor":  func(d *Deps) { d.Supervisor = nil },
		"public host": func(d *Deps) { d.PublicHost = netip.Addr{} },
		"node id":     func(d *Deps) { d.NodeID = "" },
	} {
		d := base
		mutate(&d)
		if _, err := New(d); !errors.Is(err, ErrDeps) {
			t.Fatalf("New without %s must fail, got %v", name, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 ||
		indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
