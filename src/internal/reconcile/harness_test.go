package reconcile

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/store"
)

var baseTime = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

const testNodeID = "n1"

var testPublicHost = netip.MustParseAddr("139.99.68.39")

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock(t time.Time) *testClock { return &testClock{t: t} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *testClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.Now()
	return ch
}

func (c *testClock) Sleep(ctx context.Context, d time.Duration) error {
	c.advance(d)
	return ctx.Err()
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type farm struct {
	node    domain.Node
	slots   map[domain.Slot]domain.SlotRow
	dongles map[string]domain.Dongle
	proxies map[string]domain.Proxy

	obs     ObservedState
	budgets Budgets
	ops     map[string]domain.Operation

	now     time.Time
	booted  time.Time
	started time.Time
	grace   time.Duration
	warm    bool
}

func slotID(s domain.Slot) string   { return "s" + s.String() }
func dongleID(s domain.Slot) string { return "d" + s.String() }
func proxyID(s domain.Slot) string  { return "p" + s.String() }

func newFarm(n int) *farm {
	f := &farm{
		node: domain.Node{
			ID: testNodeID, Name: "local", Kind: domain.NodeKindLocal, PublicHost: testPublicHost,
		},
		slots:   map[domain.Slot]domain.SlotRow{},
		dongles: map[string]domain.Dongle{},
		proxies: map[string]domain.Proxy{},
		obs:     newObservedState(),
		ops:     map[string]domain.Operation{},
		now:     baseTime,
		booted:  baseTime.Add(-72 * time.Hour),
		started: baseTime.Add(-10 * time.Minute),
		grace:   180 * time.Second,
		warm:    true,
		budgets: Budgets{
			RebootPerDay:        4,
			RebootCooldown:      30 * time.Minute,
			RebootUsed:          map[string]int{},
			LastRebootAt:        map[string]time.Time{},
			MaxConcurrentRotate: 4,
			MinRotateInterval:   60 * time.Second,
			LastRotateAt:        map[string]time.Time{},
		},
	}

	f.obs.Net.RouteTableNamesOK = true
	f.obs.Net.RpFilterAll = netcfg.RequiredRpFilterAll
	f.obs.Net.PublicSrcRules = []netcfg.RuleState{{
		Priority: domain.RulePrioPublic,
		Src:      netip.PrefixFrom(testPublicHost, 32),
		IifName:  "lo",
		Action:   "lookup main",
	}}
	f.obs.NftTablePresent = true
	f.obs.SweepsCompleted = 1
	f.obs.LastSweepAt = baseTime

	for i := 1; i <= n; i++ {
		f.addSlot(domain.Slot(i))
	}
	return f
}

func (f *farm) addSlot(s domain.Slot) {
	dg := dongleID(s)
	f.slots[s] = domain.SlotRow{
		ID:       slotID(s),
		NodeID:   testNodeID,
		Slot:     s,
		USBPath:  "1-13." + s.String(),
		IDPath:   "pci-0000:00:14.0-usb-0:13." + s.String() + ":1.0",
		IfName:   s.IfaceName(),
		DongleID: &dg,
	}
	f.dongles[dg] = domain.Dongle{
		ID: dg, NodeID: testNodeID, IMEI: "8600000000000" + s.String(),
		Classify: domain.ClassifyHiLink, Carrier: "viettel",
		CapResetDay: 1, AutoRecoverEnabled: true,
	}
	f.proxies[proxyID(s)] = domain.Proxy{
		ID: proxyID(s), SlotID: slotID(s), Enabled: true,
		SocksPort: s.SocksPort(), HTTPPort: s.HTTPPort(),
		Username: "cust_" + s.String(), Password: "Kq7mZr2xTn9wLb4V",
		AuthMode: domain.AuthUserPass, Policy: domain.DefaultProxyPolicy(),
	}

	f.obs.Net.Links[s.IfaceName()] = netcfg.LinkState{
		Name: s.IfaceName(), MAC: "00:1e:10:1f:00:" + s.String(),
		OperState: netcfg.OperStateUnknown, Index: 10 + int(s),
		Addrs: []netip.Prefix{s.HostPrefix()},
	}
	f.obs.Net.Rules = append(f.obs.Net.Rules,
		netcfg.RuleState{Priority: s.RulePrioSrc(), Table: s.RouteTable(), Src: s.HostPrefix()},
		netcfg.RuleState{Priority: s.RulePrioUID(), Table: s.RouteTable(), UIDRangeLo: s.UID(), UIDRangeHi: s.UID()},
	)
	f.obs.Net.Routes[s.RouteTable()] = []netcfg.RouteState{{
		Dst: netip.MustParsePrefix("0.0.0.0/0"), Gw: s.GatewayIP(), Dev: s.IfaceName(), Table: s.RouteTable(),
	}}
	f.obs.Fenced[s.IfaceName()] = false
	f.obs.ProxyStatus[s] = proxysup.Status{
		Running: true, SocksBound: true, HTTPBound: true, ProbeOK: true,
		Unit: s.ProxyUnit(), ActiveState: "active", SubState: "running",
	}
	f.obs.Devices[s] = DeviceObservation{
		Reachable: true, Conn: device.ConnConnected, Sim: device.SimStateReady,
		NetMode: device.NetModeLTE, WanIP: netip.MustParseAddr("100.64.1." + itoa(int(s))),
		MaxIdleTime: device.MaxIdleTimeDisabled, ObservedAt: baseTime,
	}
}

func (f *farm) world() World {
	desired := DesiredState{
		Node:    f.node,
		Slots:   map[domain.Slot]domain.SlotRow{},
		Dongles: map[string]domain.Dongle{},
		Proxies: map[string]domain.Proxy{},
		AuthIPs: map[string][]netip.Prefix{},
	}
	for k, v := range f.slots {
		desired.Slots[k] = v
	}
	for k, v := range f.dongles {
		desired.Dongles[k] = v
	}
	for k, v := range f.proxies {
		desired.Proxies[k] = v
		if len(v.AuthIPs) > 0 {
			desired.AuthIPs[k] = v.AuthIPs
		}
	}
	return World{
		Now:              f.now,
		HostBootedAt:     f.booted,
		ProcessStartedAt: f.started,
		StartupGrace:     f.grace,
		CacheWarm:        f.warm,
		Desired:          desired,
		Observed:         f.obs.Clone(),
		Budgets:          f.budgets,
		ActiveOps:        f.ops,
	}
}

func (f *farm) device(s domain.Slot, mutate func(*DeviceObservation)) {
	d := f.obs.Devices[s]
	mutate(&d)
	f.obs.Devices[s] = d
}

func (f *farm) proxyStatus(s domain.Slot, mutate func(*proxysup.Status)) {
	st := f.obs.ProxyStatus[s]
	mutate(&st)
	f.obs.ProxyStatus[s] = st
}

func (f *farm) proxy(s domain.Slot, mutate func(*domain.Proxy)) {
	p := f.proxies[proxyID(s)]
	mutate(&p)
	f.proxies[proxyID(s)] = p
}

func (f *farm) dongle(s domain.Slot, mutate func(*domain.Dongle)) {
	d := f.dongles[dongleID(s)]
	mutate(&d)
	f.dongles[dongleID(s)] = d
}

func (f *farm) detach(s domain.Slot) {
	row := f.slots[s]
	row.DongleID = nil
	f.slots[s] = row
}

func (f *farm) liveOp(kind domain.OpKind, subject domain.SubjectType, id string) domain.Operation {
	op := domain.Operation{
		ID: "op-" + id, Kind: kind, SubjectType: subject, SubjectID: id,
		State: domain.OpRunning, Trigger: domain.TriggerAdminUI,
		StartedAt: domain.UnixMillis(f.now), DeadlineAt: domain.UnixMillis(f.now.Add(90 * time.Second)),
	}
	f.ops[OpKey(subject, id)] = op
	return op
}

type farmOptions struct {
	slots   int
	healthy bool
}

func farmWorld(t *testing.T, opt farmOptions) World {
	t.Helper()
	f := newFarm(opt.slots)
	if !opt.healthy {
		for s := range f.slots {
			f.device(s, func(d *DeviceObservation) { d.Reachable = false })
		}
	}
	return f.world()
}

type stubNet struct {
	mu               sync.Mutex
	obs              netcfg.Observation
	observeErr       error
	appliedSlots     []domain.Slot
	ensuredGlobal    int
	ensuredTableName int
}

func (n *stubNet) EnsureGlobal(context.Context, []netip.Addr) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensuredGlobal++
	return nil
}

func (n *stubNet) EnsureRouteTableNames(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensuredTableName++
	return nil
}

func (n *stubNet) ApplySlot(_ context.Context, s domain.Slot, _, _ string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.appliedSlots = append(n.appliedSlots, s)
	return nil
}

func (n *stubNet) RemoveSlot(context.Context, domain.Slot) error { return nil }

func (n *stubNet) Observe(context.Context) (netcfg.Observation, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.obs, n.observeErr
}

func (n *stubNet) AssertInvariants(context.Context) []netcfg.Violation { return nil }

func (n *stubNet) Subscribe(context.Context) (<-chan netcfg.LinkEvent, func(), error) {
	ch := make(chan netcfg.LinkEvent)
	return ch, func() { close(ch) }, nil
}

func (n *stubNet) applied() []domain.Slot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]domain.Slot(nil), n.appliedSlots...)
}

type stubFW struct {
	mu        sync.Mutex
	tableOK   bool
	known     map[string]bool
	added     []string
	verifyErr error
}

func newStubFW() *stubFW { return &stubFW{tableOK: true, known: map[string]bool{}} }

func (f *stubFW) EnsureTable(context.Context) error { return nil }

func (f *stubFW) Verify(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tableOK {
		return errors.New("table inet dongled is missing")
	}
	return f.verifyErr
}

func (f *stubFW) AddPublic(context.Context, string, netip.Addr) error { return nil }

func (f *stubFW) RemovePublic(context.Context, string, netip.Addr) error { return nil }

func (f *stubFW) AddDongle(_ context.Context, iface string, _ netip.Addr) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known[iface] = false
	f.added = append(f.added, iface)
	return nil
}

func (f *stubFW) RemoveDongle(_ context.Context, iface string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.known, iface)
	return nil
}

func (f *stubFW) Fence(_ context.Context, iface string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known[iface] = true
	return nil
}

func (f *stubFW) Unfence(_ context.Context, iface string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known[iface] = false
	return nil
}

func (f *stubFW) IsFenced(_ context.Context, iface string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fenced, ok := f.known[iface]
	if !ok {
		return false, errors.New("interface " + iface + " is not in the dongle set")
	}
	return fenced, nil
}

func (f *stubFW) KillSockets(context.Context, netip.Addr) (int, error) { return 0, nil }

func (f *stubFW) FlushConntrack(context.Context, netip.Addr) (int, error) { return 0, nil }

func (f *stubFW) CustomerAcceptHits(context.Context) (uint64, error) { return 1, nil }

func (f *stubFW) addedIfaces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.added...)
}

type stubProxy struct {
	mu       sync.Mutex
	status   map[domain.Slot]proxysup.Status
	applied  []domain.Slot
	stopped  []domain.Slot
	evicted  []domain.Slot
	applyErr error
}

func newStubProxy() *stubProxy {
	return &stubProxy{status: map[domain.Slot]proxysup.Status{}}
}

func (p *stubProxy) Apply(_ context.Context, sp proxysup.Spec) (proxysup.Applied, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.applyErr != nil {
		return proxysup.Applied{}, p.applyErr
	}
	p.applied = append(p.applied, sp.Slot)
	p.status[sp.Slot] = proxysup.Status{
		Running: true, SocksBound: true, HTTPBound: true, ProbeOK: true, Unit: sp.Slot.ProxyUnit(),
	}
	return proxysup.Applied{Slot: sp.Slot, Changed: true, Status: p.status[sp.Slot]}, nil
}

func (p *stubProxy) Stop(_ context.Context, slot domain.Slot, evict bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if evict {
		p.evicted = append(p.evicted, slot)
	} else {
		p.stopped = append(p.stopped, slot)
	}
	p.status[slot] = proxysup.Status{Unit: slot.ProxyUnit()}
	return nil
}

func (p *stubProxy) Status(_ context.Context, slot domain.Slot) (proxysup.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.status[slot]
	if !ok {
		return proxysup.Status{Unit: slot.ProxyUnit()}, nil
	}
	return st, nil
}

func (p *stubProxy) calls() (applied, stopped, evicted []domain.Slot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.Slot(nil), p.applied...),
		append([]domain.Slot(nil), p.stopped...),
		append([]domain.Slot(nil), p.evicted...)
}

type stubDevice struct {
	mu          sync.Mutex
	reachable   bool
	conn        device.ConnStatus
	sim         device.SimState
	maxIdle     int
	loginNeeded bool
	statusCalls int
	idleWrites  int
	reboots     int
	delay       time.Duration
}

func (d *stubDevice) Status(ctx context.Context) (device.Status, error) {
	d.mu.Lock()
	delay := d.delay
	d.statusCalls++
	reachable := d.reachable
	conn := d.conn
	d.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return device.Status{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	if !reachable {
		return device.Status{}, domain.ErrUnreachable
	}
	return device.Status{ConnectionStatus: conn}, nil
}

func (d *stubDevice) Information(context.Context) (device.Info, error) {
	return device.Info{}, d.reach()
}

func (d *stubDevice) Signal(context.Context) (device.Signal, error) {
	return device.Signal{Bars: 4}, d.reach()
}

func (d *stubDevice) DataSwitch(context.Context, bool) error { return d.reach() }

func (d *stubDevice) SetMaxIdleTime(_ context.Context, seconds int) error {
	if err := d.reach(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxIdle = seconds
	d.idleWrites++
	return nil
}

func (d *stubDevice) GetMaxIdleTime(context.Context) (int, error) {
	if err := d.reach(); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxIdle, nil
}

func (d *stubDevice) NetMode(context.Context) (device.NetMode, error) {
	return device.NetModeLTE, d.reach()
}

func (d *stubDevice) SetNetMode(context.Context, device.NetMode) error { return d.reach() }

func (d *stubDevice) Reboot(context.Context) error {
	if err := d.reach(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reboots++
	return nil
}

func (d *stubDevice) DHCPSettings(context.Context) (device.DHCPSettings, error) {
	return device.DHCPSettings{}, d.reach()
}

func (d *stubDevice) SetDHCPSettings(context.Context, device.DHCPSettings) error { return d.reach() }

func (d *stubDevice) Traffic(context.Context) (device.Traffic, error) {
	return device.Traffic{}, d.reach()
}

func (d *stubDevice) MonthStats(context.Context) (device.MonthStats, error) {
	return device.MonthStats{}, d.reach()
}

func (d *stubDevice) SMSList(context.Context, device.SMSBox, int, int) ([]device.SMS, int, error) {
	return nil, 0, d.reach()
}

func (d *stubDevice) SMSSend(context.Context, []string, string) error { return d.reach() }

func (d *stubDevice) SMSDelete(context.Context, int64) error { return d.reach() }

func (d *stubDevice) SMSSetRead(context.Context, int64) error { return d.reach() }

func (d *stubDevice) PinStatus(context.Context) (device.SimState, error) {
	if err := d.reach(); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sim, nil
}

func (d *stubDevice) LoginRequired(context.Context) (bool, error) {
	if err := d.reach(); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loginNeeded, nil
}

func (d *stubDevice) Reachable(context.Context) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reachable
}

func (d *stubDevice) reach() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.reachable {
		return domain.ErrUnreachable
	}
	return nil
}

func (d *stubDevice) set(mutate func(*stubDevice)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	mutate(d)
}

func (d *stubDevice) polls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.statusCalls
}

type stubRegistry struct {
	mu      sync.Mutex
	devices map[domain.Slot]*stubDevice
}

func newStubRegistry(slots int) *stubRegistry {
	r := &stubRegistry{devices: map[domain.Slot]*stubDevice{}}
	for i := 1; i <= slots; i++ {
		r.devices[domain.Slot(i)] = &stubDevice{
			reachable: true, conn: device.ConnConnected, sim: device.SimStateReady,
			maxIdle: device.MaxIdleTimeDisabled,
		}
	}
	return r
}

func (r *stubRegistry) ForSlot(_ context.Context, s domain.Slot) (device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[s]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

func (r *stubRegistry) ForAddr(context.Context, netip.Addr) (device.Device, error) {
	return nil, domain.ErrNotImplemented
}

func (r *stubRegistry) Close() error { return nil }

func (r *stubRegistry) device(s domain.Slot) *stubDevice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.devices[s]
}

type recordedOp struct {
	kind   domain.OpKind
	target string
	reason string
}

type stubOps struct {
	mu       sync.Mutex
	calls    []recordedOp
	rotateEr error
	rebootEr error
}

func (o *stubOps) RecoverRotate(_ context.Context, proxyID, reason string) (domain.Operation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, recordedOp{kind: domain.OpRotate, target: proxyID, reason: reason})
	if o.rotateEr != nil {
		return domain.Operation{}, o.rotateEr
	}
	return domain.Operation{
		ID: "op-rotate-" + proxyID, Kind: domain.OpRotate,
		SubjectType: domain.SubjectProxy, SubjectID: proxyID,
		Trigger: domain.TriggerAutoRecovery, ActorType: domain.ActorSystem,
	}, nil
}

func (o *stubOps) RebootDongle(_ context.Context, dongleID, reason string) (domain.Operation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, recordedOp{kind: domain.OpReboot, target: dongleID, reason: reason})
	if o.rebootEr != nil {
		return domain.Operation{}, o.rebootEr
	}
	return domain.Operation{
		ID: "op-reboot-" + dongleID, Kind: domain.OpReboot,
		SubjectType: domain.SubjectDongle, SubjectID: dongleID,
		Trigger: domain.TriggerAutoRecovery, ActorType: domain.ActorSystem,
	}, nil
}

func (o *stubOps) recorded() []recordedOp {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]recordedOp(nil), o.calls...)
}

type stubRepos struct {
	mu sync.Mutex

	node       domain.Node
	slots      []domain.SlotRow
	dongles    []domain.Dongle
	proxies    []domain.Proxy
	operations map[string]domain.Operation
	rotations  []domain.Rotation

	orphansReconciled int
	stalledMarked     int
	disabled          []string
}

func newStubRepos(f *farm) *stubRepos {
	r := &stubRepos{node: f.node, operations: map[string]domain.Operation{}}
	for _, s := range sortedSlots(f.slots) {
		r.slots = append(r.slots, f.slots[s])
		if dg, ok := f.dongles[dongleID(s)]; ok {
			r.dongles = append(r.dongles, dg)
		}
		if px, ok := f.proxies[proxyID(s)]; ok {
			r.proxies = append(r.proxies, px)
		}
	}
	for _, op := range f.ops {
		r.operations[op.ID] = op
	}
	return r
}

func (r *stubRepos) Nodes() store.NodeRepo           { return stubNodeRepo{r} }
func (r *stubRepos) Dongles() store.DongleRepo       { return stubDongleRepo{r} }
func (r *stubRepos) Slots() store.SlotRepo           { return stubSlotRepo{r} }
func (r *stubRepos) Proxies() store.ProxyRepo        { return stubProxyRepo{r} }
func (r *stubRepos) Operations() store.OperationRepo { return stubOperationRepo{r} }
func (r *stubRepos) Rotations() store.RotationRepo   { return stubRotationRepo{r} }
func (r *stubRepos) Customers() store.CustomerRepo   { return stubCustomerRepo{r} }
func (r *stubRepos) Usage() store.UsageRepo          { return stubUsageRepo{r} }
func (r *stubRepos) SMS() store.SMSRepo              { return stubSMSRepo{r} }
func (r *stubRepos) Settings() store.SettingsRepo    { return stubSettingsRepo{r} }

func (r *stubRepos) setLive(op domain.Operation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations[op.ID] = op
}

func (r *stubRepos) disabledProxies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.disabled...)
}

type stubNodeRepo struct{ r *stubRepos }

func (s stubNodeRepo) Get(_ context.Context, id string) (domain.Node, error) {
	if s.r.node.ID != id {
		return domain.Node{}, domain.ErrNotFound
	}
	return s.r.node, nil
}

func (s stubNodeRepo) List(context.Context) ([]domain.Node, error) {
	return []domain.Node{s.r.node}, nil
}

func (s stubNodeRepo) Upsert(context.Context, domain.Node) error { return nil }

type stubDongleRepo struct {
	*stubRepos
}

func (s stubDongleRepo) Get(_ context.Context, id string) (domain.Dongle, error) {
	for _, d := range s.dongles {
		if d.ID == id {
			return d, nil
		}
	}
	return domain.Dongle{}, domain.ErrNotFound
}

func (s stubDongleRepo) GetByIMEI(context.Context, string) (domain.Dongle, error) {
	return domain.Dongle{}, domain.ErrNotFound
}

func (s stubDongleRepo) List(context.Context, store.DongleFilter) ([]domain.Dongle, error) {
	return s.dongles, nil
}

func (s stubDongleRepo) Create(context.Context, domain.Dongle) error { return nil }

func (s stubDongleRepo) Update(context.Context, domain.Dongle) error { return nil }

func (s stubDongleRepo) Delete(context.Context, string) error { return nil }

func (s stubDongleRepo) SetAutoRecover(context.Context, string, bool) error { return nil }

func (s stubDongleRepo) SetCapabilities(context.Context, string, bool, bool) error { return nil }

func (s stubDongleRepo) SetDataCap(context.Context, string, int64, int) error { return nil }

type stubSlotRepo struct{ *stubRepos }

func (s stubSlotRepo) Get(_ context.Context, id string) (domain.SlotRow, error) {
	for _, r := range s.slots {
		if r.ID == id {
			return r, nil
		}
	}
	return domain.SlotRow{}, domain.ErrNotFound
}

func (s stubSlotRepo) GetBySlot(context.Context, string, domain.Slot) (domain.SlotRow, error) {
	return domain.SlotRow{}, domain.ErrNotFound
}

func (s stubSlotRepo) GetByDongle(context.Context, string) (domain.SlotRow, error) {
	return domain.SlotRow{}, domain.ErrNotFound
}

func (s stubSlotRepo) List(context.Context, string) ([]domain.SlotRow, error) { return s.slots, nil }

func (s stubSlotRepo) Create(context.Context, domain.SlotRow) error { return nil }

func (s stubSlotRepo) Update(context.Context, domain.SlotRow) error { return nil }

func (s stubSlotRepo) Delete(context.Context, string) error { return nil }

func (s stubSlotRepo) Attach(context.Context, string, string) error { return nil }

func (s stubSlotRepo) Detach(context.Context, string) error { return nil }

func (s stubSlotRepo) NextFree(context.Context, string) (domain.Slot, error) {
	return 0, domain.ErrNoFreeSlot
}

type stubProxyRepo struct{ *stubRepos }

func (s stubProxyRepo) Get(_ context.Context, id string) (domain.Proxy, error) {
	for _, p := range s.proxies {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.Proxy{}, domain.ErrNotFound
}

func (s stubProxyRepo) GetBySlot(context.Context, string) (domain.Proxy, error) {
	return domain.Proxy{}, domain.ErrNotFound
}

func (s stubProxyRepo) GetByUsername(context.Context, string) (domain.Proxy, error) {
	return domain.Proxy{}, domain.ErrNotFound
}

func (s stubProxyRepo) List(context.Context, store.ProxyFilter) ([]domain.Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Proxy(nil), s.proxies...), nil
}

func (s stubProxyRepo) Create(context.Context, domain.Proxy) error { return nil }

func (s stubProxyRepo) Update(context.Context, domain.Proxy) error { return nil }

func (s stubProxyRepo) Delete(context.Context, string) error { return nil }

func (s stubProxyRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.proxies {
		if s.proxies[i].ID != id {
			continue
		}
		s.proxies[i].Enabled = enabled
		if !enabled {
			s.disabled = append(s.disabled, id)
		}
		return nil
	}
	return domain.ErrNotFound
}

func (s stubProxyRepo) SetSuspended(context.Context, string, bool) error { return nil }

func (s stubProxyRepo) SetCredentials(context.Context, string, string, string) error { return nil }

func (s stubProxyRepo) SetAuthMode(context.Context, string, domain.AuthMode) error { return nil }

func (s stubProxyRepo) SetPolicy(context.Context, string, domain.ProxyPolicy) error { return nil }

func (s stubProxyRepo) SetCustomer(context.Context, string, *string, *int64) error { return nil }

func (s stubProxyRepo) ListAuthIPs(context.Context, string) ([]domain.ProxyAuthIP, error) {
	return nil, nil
}

func (s stubProxyRepo) AddAuthIP(context.Context, domain.ProxyAuthIP) error { return nil }

func (s stubProxyRepo) DeleteAuthIP(context.Context, string, netip.Prefix) error { return nil }

func (s stubProxyRepo) ListExpired(context.Context, int64) ([]domain.Proxy, error) { return nil, nil }

type stubOperationRepo struct{ *stubRepos }

func (s stubOperationRepo) Get(_ context.Context, id string) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[id]
	if !ok {
		return domain.Operation{}, domain.ErrNotFound
	}
	return op, nil
}

func (s stubOperationRepo) List(_ context.Context, f store.OperationFilter) ([]domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Operation{}
	for _, op := range s.operations {
		if f.Kind != "" && op.Kind != f.Kind {
			continue
		}
		if f.SinceMS > 0 && op.StartedAt < f.SinceMS {
			continue
		}
		out = append(out, op)
	}
	return out, nil
}

func (s stubOperationRepo) Create(_ context.Context, o domain.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations[o.ID] = o
	return nil
}

func (s stubOperationRepo) Progress(context.Context, string, domain.OpState, string, int) error {
	return nil
}

func (s stubOperationRepo) Finish(context.Context, string, domain.OpState, string, string, int64) error {
	return nil
}

func (s stubOperationRepo) FindActive(_ context.Context, subject domain.SubjectType, id string) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range s.operations {
		if op.SubjectType == subject && op.SubjectID == id && op.Active() {
			return op, nil
		}
	}
	return domain.Operation{}, domain.ErrNotFound
}

func (s stubOperationRepo) ListActive(context.Context) ([]domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Operation{}
	for _, op := range s.operations {
		if op.Active() {
			out = append(out, op)
		}
	}
	return out, nil
}

func (s stubOperationRepo) MarkStalled(context.Context, int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stalledMarked++
	return 0, nil
}

func (s stubOperationRepo) ReconcileOrphans(_ context.Context, nowMS int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, op := range s.operations {
		if !op.Active() {
			continue
		}
		finished := nowMS
		op.State = domain.OpFailed
		op.FinishedAt = &finished
		op.Error = "orphaned"
		s.operations[id] = op
		n++
	}
	s.orphansReconciled += n
	return n, nil
}

type stubRotationRepo struct{ *stubRepos }

func (s stubRotationRepo) Create(_ context.Context, r domain.Rotation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotations = append(s.rotations, r)
	return nil
}

func (s stubRotationRepo) List(_ context.Context, f store.RotationFilter) ([]domain.Rotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Rotation{}
	for _, r := range s.rotations {
		if f.ProxyID != "" && r.ProxyID != f.ProxyID {
			continue
		}
		if f.SinceMS > 0 && r.RequestedAt < f.SinceMS {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s stubRotationRepo) LastFor(context.Context, string) (domain.Rotation, error) {
	return domain.Rotation{}, domain.ErrNotFound
}

type stubCustomerRepo struct{ *stubRepos }

func (s stubCustomerRepo) Get(context.Context, string) (domain.Customer, error) {
	return domain.Customer{}, domain.ErrNotFound
}

func (s stubCustomerRepo) List(context.Context) ([]domain.Customer, error) { return nil, nil }

func (s stubCustomerRepo) Create(context.Context, domain.Customer) error { return nil }

func (s stubCustomerRepo) Update(context.Context, domain.Customer) error { return nil }

func (s stubCustomerRepo) Delete(context.Context, string) error { return nil }

func (s stubCustomerRepo) CountProxies(context.Context, string) (int, error) { return 0, nil }

type stubUsageRepo struct{ *stubRepos }

func (s stubUsageRepo) AddDongleDaily(context.Context, string, string, int64, int64, int64) error {
	return nil
}

func (s stubUsageRepo) GetDongleDaily(context.Context, string, string) (domain.UsageDay, error) {
	return domain.UsageDay{}, domain.ErrNotFound
}

func (s stubUsageRepo) ListDongleDaily(context.Context, string, string, string) ([]domain.UsageDay, error) {
	return nil, nil
}

func (s stubUsageRepo) SumDongleSince(context.Context, string, string) (int64, int64, error) {
	return 0, 0, nil
}

type stubSMSRepo struct{ *stubRepos }

func (s stubSMSRepo) Upsert(context.Context, string, device.SMS, int64) error { return nil }

func (s stubSMSRepo) List(context.Context, store.SMSFilter) ([]device.SMS, int, error) {
	return nil, 0, nil
}

func (s stubSMSRepo) Delete(context.Context, string, device.SMSBox, int64) error { return nil }

func (s stubSMSRepo) MarkRead(context.Context, string, device.SMSBox, int64) error { return nil }

func (s stubSMSRepo) CountUnread(context.Context, string) (int, error) { return 0, nil }

type stubSettingsRepo struct{ *stubRepos }

func (s stubSettingsRepo) Get(context.Context, string) (string, error) {
	return "", domain.ErrNotFound
}

func (s stubSettingsRepo) Set(context.Context, string, string, int64) error { return nil }

func (s stubSettingsRepo) All(context.Context) (map[string]string, error) { return nil, nil }

var (
	_ netcfg.Manager      = (*stubNet)(nil)
	_ fw.Firewall         = (*stubFW)(nil)
	_ proxysup.Supervisor = (*stubProxy)(nil)
	_ device.Registry     = (*stubRegistry)(nil)
	_ device.Device       = (*stubDevice)(nil)
	_ store.Repos         = (*stubRepos)(nil)
	_ Ops                 = (*stubOps)(nil)
	_ Clock               = (*testClock)(nil)
)
