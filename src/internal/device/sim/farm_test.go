package sim

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/hilink"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	slot1 = domain.Slot(1)
	slot2 = domain.Slot(2)
)

func newFarm(t *testing.T, opt FarmOptions) *Farm {
	t.Helper()
	if opt.MaxHang == 0 {
		opt.MaxHang = 300 * time.Millisecond
	}
	f := NewFarm(2, opt)
	t.Cleanup(func() { f.Close() })
	return f
}

func newClient(t *testing.T, f *Farm, slot domain.Slot) *hilink.Client {
	t.Helper()
	return hilink.New(slot.GatewayIP(), hilink.Options{
		BaseURL: f.BaseURL(slot),
		Timeout: 2 * time.Second,
		Sleep:   func(context.Context, time.Duration) {},
	})
}

func TestSimRoundTripsEveryReadEndpoint(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	info, err := c.Information(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceName != "E3372" || info.HardwareVersion != "CL2E3372HM" {
		t.Fatalf("info = %+v", info)
	}

	sig, err := c.Signal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sig.RSSI != -89 || sig.RSRP != -102 || sig.RSRQ != -6 || sig.SINR != 3 {
		t.Fatalf("signal = %+v", sig)
	}

	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnDisconnected {
		t.Fatalf("fresh device should be disconnected, got %d", st.ConnectionStatus)
	}

	if _, err := c.Traffic(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MonthStats(ctx); err != nil {
		t.Fatal(err)
	}

	idle, err := c.GetMaxIdleTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idle != device.MaxIdleTimeDefault {
		t.Fatalf("MaxIdelTime default = %d, want %d", idle, device.MaxIdleTimeDefault)
	}

	mode, err := c.NetMode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != device.NetModeAuto {
		t.Fatalf("net mode = %q", mode)
	}

	plmn, err := c.CurrentPLMN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plmn.FullName != DefaultCarrier {
		t.Fatalf("plmn = %+v", plmn)
	}
	if _, err := c.Register(ctx); err != nil {
		t.Fatal(err)
	}

	pin, err := c.PinStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pin != device.SimStateReady {
		t.Fatalf("sim state = %d", pin)
	}

	dhcp, err := c.DHCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dhcp.DHCPIPAddress != slot1.GatewayIP() {
		t.Fatalf("dhcp = %+v", dhcp)
	}

	needLogin, err := c.LoginRequired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if needLogin {
		t.Fatal("hilink_login defaults to off")
	}
}

func TestSimDataSwitchAndWanIP(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	on, err := c.DataSwitchState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("dataswitch should read back as 1")
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnConnected {
		t.Fatalf("status = %d", st.ConnectionStatus)
	}
	if !st.WanIP.IsValid() {
		t.Fatal("connected device must report a WAN IP")
	}
}

func TestSimHoldToNewIPBelowThresholdKeepsOldIP(t *testing.T) {
	f := newFarm(t, FarmOptions{HoldToNewIP: 10 * time.Second})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	before := f.Device(slot1).PublicIP()

	if err := c.DataSwitch(ctx, false); err != nil {
		t.Fatal(err)
	}
	f.Advance(6 * time.Second)
	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := f.Device(slot1).PublicIP(); got != before {
		t.Fatalf("hold 6s < 10s must return the OLD ip, got %v want %v", got, before)
	}

	if err := c.DataSwitch(ctx, false); err != nil {
		t.Fatal(err)
	}
	f.Advance(15 * time.Second)
	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := f.Device(slot1).PublicIP(); got == before {
		t.Fatalf("hold 15s > 10s must rotate the ip, still %v", got)
	}
}

func TestSimHoldToNewIPDefaultIsTenSeconds(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	if f.opt.HoldToNewIP != DefaultHoldToNewIP {
		t.Fatalf("default HoldToNewIP = %v", f.opt.HoldToNewIP)
	}
	if DefaultHoldToNewIP != 10*time.Second {
		t.Fatalf("DefaultHoldToNewIP = %v", DefaultHoldToNewIP)
	}
}

func TestSimSetNetModeWhileDialupReturns112001(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	err := c.SetNetMode(ctx, device.NetModeLTE)
	if !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("expected 112001 while dialup active, got %v", err)
	}
	code, ok := domain.HiLinkCodeOf(err)
	if !ok || code != domain.CodeSetNetModeWhenDialup {
		t.Fatalf("code = %d %v", code, ok)
	}

	if err := c.DataSwitch(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetNetMode(ctx, device.NetModeLTE); err != nil {
		t.Fatalf("net mode change with dialup off: %v", err)
	}
	mode, err := c.NetMode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != device.NetModeLTE {
		t.Fatalf("mode = %q", mode)
	}
}

func TestSimMaxIdleTimeDisconnects(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	f.Advance(299 * time.Second)
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnConnected {
		t.Fatalf("still inside the idle window, status = %d", st.ConnectionStatus)
	}

	f.Advance(2 * time.Second)
	st, err = c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnDisconnected {
		t.Fatalf("past MaxIdelTime 300s the sim must drop the connection, status = %d", st.ConnectionStatus)
	}

	f.Device(slot1).MarkActivity()
	st, err = c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnConnected {
		t.Fatalf("activity should restore the connection, status = %d", st.ConnectionStatus)
	}
}

func TestSimSetMaxIdleTimeRoundTrip(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.SetMaxIdleTime(ctx, device.MaxIdleTimeDisabled); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetMaxIdleTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != device.MaxIdleTimeDisabled {
		t.Fatalf("MaxIdelTime = %d", got)
	}

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	f.Advance(3 * time.Hour)
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnConnected {
		t.Fatalf("MaxIdelTime 0 disables the idle timer, status = %d", st.ConnectionStatus)
	}
}

func TestSimDHCPChangeTimesOutInsteadOfAnswering(t *testing.T) {
	f := newFarm(t, FarmOptions{FactoryDefaultLAN: true, MaxHang: 5 * time.Second})
	c := hilink.New(device.FactoryDefaultAddr, hilink.Options{
		BaseURL: f.BaseURL(slot1),
		Timeout: 300 * time.Millisecond,
		Sleep:   func(context.Context, time.Duration) {},
	})
	ctx := context.Background()

	cur, err := c.DHCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur.DHCPIPAddress != device.FactoryDefaultAddr {
		t.Fatalf("factory default lan = %v", cur.DHCPIPAddress)
	}

	target := slot1.GatewayIP()
	cur.DHCPIPAddress = target
	cur.DHCPStartIPAddress = netip.MustParseAddr("192.168.101.100")
	cur.DHCPEndIPAddress = netip.MustParseAddr("192.168.101.200")
	cur.PrimaryDNS = target
	cur.SecondaryDNS = target

	err = c.SetDHCPSettings(ctx, cur)
	if err == nil {
		t.Fatal("moving the LAN address must not produce a reply")
	}
	if !errors.Is(err, domain.ErrUnreachable) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a timeout, got %v", err)
	}
	if got := f.Device(slot1).LANAddr(); got != target {
		t.Fatalf("the change must still have been applied, lan = %v", got)
	}
	if url := f.BaseURLForAddr(target); url == "" {
		t.Fatal("the device must be reachable at its new address")
	}
	if url := f.BaseURLForAddr(device.FactoryDefaultAddr); url == f.BaseURL(slot1) {
		t.Fatal("the device must no longer answer at the old address")
	}
	if url := f.BaseURLForAddr(device.FactoryDefaultAddr); url != f.BaseURL(slot2) {
		t.Fatalf("the slot still parked at the factory address must own it, url = %q", url)
	}
}

func TestSimFactoryDefaultLANRegistersEveryOwningSlot(t *testing.T) {
	f := newFarm(t, FarmOptions{FactoryDefaultLAN: true})
	if url := f.BaseURLForAddr(device.FactoryDefaultAddr); url != f.BaseURL(slot1) {
		t.Fatalf("the lowest slot must own the shared factory address, url = %q", url)
	}
	if got := f.Device(slot2).LANAddr(); got != device.FactoryDefaultAddr {
		t.Fatalf("slot2 must still be parked at the factory address, lan = %v", got)
	}
}

func TestSimDHCPGatewayOutsidePoolSubnetReturns100005(t *testing.T) {
	f := newFarm(t, FarmOptions{FactoryDefaultLAN: true})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	cur, err := c.DHCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cur.DHCPIPAddress = netip.MustParseAddr("192.168.101.1")
	err = c.SetDHCPSettings(ctx, cur)
	if !errors.Is(err, domain.ErrFormat) {
		t.Fatalf("moving the gateway without the pool must be 100005, got %v", err)
	}
	if got := f.Device(slot1).LANAddr(); got != device.FactoryDefaultAddr {
		t.Fatalf("rejected change must not be applied, lan = %v", got)
	}
}

func TestSimDHCPSameSubnetWriteAnswersOK(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	cur, err := c.DHCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cur.DHCPLeaseTime = 3600
	if err := c.SetDHCPSettings(ctx, cur); err != nil {
		t.Fatalf("a write that does not move the gateway must answer: %v", err)
	}
	got, err := c.DHCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.DHCPLeaseTime != 3600 {
		t.Fatalf("lease = %d", got.DHCPLeaseTime)
	}
}

func TestSimRejectsPostWhenHiLinkLogin(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	f.Device(slot1).SetHiLinkLogin(true)

	needLogin, err := c.LoginRequired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !needLogin {
		t.Fatal("hilink_login must read back as 1")
	}

	err = c.DataSwitch(ctx, true)
	if !errors.Is(err, domain.ErrNeedLogin) {
		t.Fatalf("POST under hilink_login must be 100003, got %v", err)
	}
	code, ok := domain.HiLinkCodeOf(err)
	if !ok || code != domain.CodeSystemNoRights {
		t.Fatalf("code = %d %v", code, ok)
	}

	if _, err := c.Status(ctx); err != nil {
		t.Fatalf("GET must still work under hilink_login: %v", err)
	}
}

func TestSimTokenReuseReturns125002(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	d := f.Device(slot1)
	cookie, tokens := d.tok.handshake()
	if len(tokens) != TokenBatch {
		t.Fatalf("handshake issued %d tokens, want %d", len(tokens), TokenBatch)
	}
	if !d.tok.sessionValid(cookie) {
		t.Fatal("cookie must be accepted")
	}
	if !d.tok.consume(tokens[0]) {
		t.Fatal("first use must succeed")
	}
	if d.tok.consume(tokens[0]) {
		t.Fatal("a token is single use")
	}
	if !d.tok.consume(tokens[1]) {
		t.Fatal("a different token must still work")
	}
}

func TestSimTokenExpiryDrivesClientRetry(t *testing.T) {
	f := newFarm(t, FarmOptions{TokenTTL: time.Minute})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	f.Advance(2 * time.Minute)
	if err := c.DataSwitch(ctx, false); err != nil {
		t.Fatalf("client must recover from an expired token in one retry: %v", err)
	}
}

func TestSimWedgeNeverReaches901(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	f.Device(slot1).Wedge(time.Hour)
	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		st, err := c.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if st.ConnectionStatus == device.ConnConnected {
			t.Fatal("a wedged device must never report 901")
		}
		if st.ConnectionStatus != device.ConnConnecting {
			t.Fatalf("wedged status = %d, want 900", st.ConnectionStatus)
		}
		f.Advance(time.Minute)
	}

	f.Advance(2 * time.Hour)
	f.Device(slot1).MarkActivity()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnConnected {
		t.Fatalf("after the wedge expires the device must connect, status = %d", st.ConnectionStatus)
	}
}

func TestSimSetPinLocked(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	f.Device(slot1).SetPinLocked(device.SimState(hilink.SimStatePUKRequired))
	got, err := c.PinStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if int(got) != hilink.SimStatePUKRequired {
		t.Fatalf("sim state = %d", got)
	}
	if !hilink.SimStateLocked(got) {
		t.Fatal("261 must be treated as locked")
	}
	if hilink.SimStateUsable(got) {
		t.Fatal("261 is not usable")
	}
}

func TestSimReboot(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Device(slot1).DataOn() {
		t.Fatal("reboot must drop the data connection")
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatalf("client must re-handshake after the reboot invalidated the session: %v", err)
	}
}

func TestSimSMSRoundTrip(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	d := f.Device(slot1)
	c := newClient(t, f, slot1)
	ctx := context.Background()

	first := d.AddMessage(Message{Phone: "+48616673870", Content: "hello", Smstat: 0, SmsType: 1})
	d.AddMessage(Message{Phone: "3350", Content: "part one", Smstat: 0, SmsType: device.SMSTypeFragment})

	list, total, err := c.SMSList(ctx, device.SMSBoxInbox, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("total %d, list %d", total, len(list))
	}
	if list[0].Index != first || list[0].Content != "hello" {
		t.Fatalf("first = %+v", list[0])
	}
	if list[0].Read {
		t.Error("Smstat 0 is unread")
	}
	if list[1].SmsType != device.SMSTypeFragment || !list[1].IsFragment {
		t.Fatalf("SmsType 2 must set IsFragment, got %+v", list[1])
	}
	if list[0].Date == 0 {
		t.Error("date must decode")
	}

	if err := c.SMSSetRead(ctx, first); err != nil {
		t.Fatal(err)
	}
	list, _, err = c.SMSList(ctx, device.SMSBoxInbox, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !list[0].Read {
		t.Fatal("set-read must flip Smstat")
	}

	if err := c.SMSDelete(ctx, first); err != nil {
		t.Fatal(err)
	}
	list, total, err = c.SMSList(ctx, device.SMSBoxInbox, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("after delete: total %d, list %d", total, len(list))
	}

	if err := c.SMSSend(ctx, []string{"+420603052000"}, "outbound"); err != nil {
		t.Fatal(err)
	}
	sent, _, err := c.SMSList(ctx, device.SMSBoxOutbox, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Content != "outbound" {
		t.Fatalf("outbox = %+v", sent)
	}
}

func TestSimSMSEmptyInbox(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	list, total, err := c.SMSList(context.Background(), device.SMSBoxInbox, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("total %d, list %d", total, len(list))
	}
}

func TestSimUnknownEndpointReturns100002(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	var out struct{}
	err := c.Get(context.Background(), "wlan/basic-settings", &out)
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestSimFaultInjectionRate(t *testing.T) {
	f := newFarm(t, FarmOptions{
		FaultRate: 1,
		Faults:    FaultProfile{Err100002: true},
		Seed:      7,
	})
	c := newClient(t, f, slot1)
	err := c.Get(context.Background(), hilink.PathDeviceInformation, &struct{}{})
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("with rate 1 and only Err100002 enabled every call must fault, got %v", err)
	}

	quiet := NewFarm(1, FarmOptions{FaultRate: 0, Faults: FaultProfile{Err100002: true}})
	defer quiet.Close()
	qc := hilink.New(slot1.GatewayIP(), hilink.Options{BaseURL: quiet.BaseURL(slot1), Timeout: time.Second})
	if _, err := qc.Information(context.Background()); err != nil {
		t.Fatalf("rate 0 must inject nothing, got %v", err)
	}
}

func TestSimForcedFaultSequence(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	f.Device(slot1).Faults().Force(FaultErr112001)
	err := c.Get(ctx, hilink.PathDeviceInformation, &struct{}{})
	if !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("forced fault = %v", err)
	}
	if _, err := c.Information(ctx); err != nil {
		t.Fatalf("the forced fault must be consumed once: %v", err)
	}
}

func TestSimTimeoutFault(t *testing.T) {
	f := newFarm(t, FarmOptions{MaxHang: 2 * time.Second})
	c := hilink.New(slot1.GatewayIP(), hilink.Options{
		BaseURL: f.BaseURL(slot1),
		Timeout: 200 * time.Millisecond,
		Sleep:   func(context.Context, time.Duration) {},
	})
	f.Device(slot1).Faults().Force(FaultTimeout)
	_, err := c.Information(context.Background())
	if !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("timeout fault = %v", err)
	}
}

func TestSimReenumerateFault(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	f.Device(slot1).Faults().Force(FaultReenumerate)
	_, err := c.Information(context.Background())
	if !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("reenumerate fault = %v", err)
	}
}

func TestSimUnreachableDevice(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	f.Device(slot1).SetUnreachable(true)
	if c.Reachable(context.Background()) {
		t.Fatal("an unreachable device must report false")
	}
	f.Device(slot1).SetUnreachable(false)
	if !c.Reachable(context.Background()) {
		t.Fatal("device came back, Reachable must be true")
	}
}

func TestSimRegistryResolvesSlotAndAddr(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	reg := f.Registry()
	defer reg.Close()
	ctx := context.Background()

	d, err := reg.ForSlot(ctx, slot1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Information(ctx); err != nil {
		t.Fatal(err)
	}

	d2, err := reg.ForAddr(ctx, slot1.GatewayIP())
	if err != nil {
		t.Fatal(err)
	}
	if d2 != d {
		t.Fatal("same address must yield the same client")
	}

	if _, err := reg.ForSlot(ctx, domain.Slot(0)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid slot = %v", err)
	}
}

func TestSimOmitWanIPVariant(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	ctx := context.Background()

	f.Device(slot1).SetOmitWanIP(true)
	if err := c.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ConnectionStatus != device.ConnConnected {
		t.Fatalf("status = %d", st.ConnectionStatus)
	}
	if st.WanIP.IsValid() {
		t.Fatal("this firmware variant has no WanIPAddress element at all")
	}
}

func TestSimIsolatedPerSlot(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	ctx := context.Background()
	c1 := newClient(t, f, slot1)
	c2 := newClient(t, f, domain.Slot(2))

	if err := c1.DataSwitch(ctx, true); err != nil {
		t.Fatal(err)
	}
	st2, err := c2.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st2.ConnectionStatus == device.ConnConnected {
		t.Fatal("slots must not share state")
	}
	if f.Device(slot1).PublicIP() == f.Device(domain.Slot(2)).PublicIP() {
		t.Fatal("slots must not share a public ip")
	}
}

// Slot identities must be unique across the whole 1..MaxSlots range and must keep the
// widths the concatenated values imply: 15 digits for IMEI and IMSI, 16 characters for the
// Huawei serial, and two hex characters for the final MAC octet. A formatter that wraps
// (the old n%100) silently gave two slots the same identity, and one that simply widens
// breaks the lengths instead, so both properties are asserted together here.
func TestSlotIdentitiesAreUniqueAndFixedWidth(t *testing.T) {
	seen := map[string]domain.Slot{}
	for i := 1; i <= domain.MaxSlots; i++ {
		slot := domain.Slot(i)
		st := newState(slot, DefaultCarrier)

		for _, tc := range []struct {
			name  string
			value string
			want  int
		}{
			{"imei", st.imei, 15},
			{"imsi", st.imsi, 15},
			{"serialNumber", st.serialNumber, 16},
			{"macAddress1", st.macAddress1, len("BA:AB:BE:34:00:") + 2},
		} {
			if len(tc.value) != tc.want {
				t.Errorf("slot %d: %s = %q, length %d, want %d",
					i, tc.name, tc.value, len(tc.value), tc.want)
			}
			key := tc.name + "=" + tc.value
			if prev, dup := seen[key]; dup {
				t.Errorf("slot %d: %s = %q already used by slot %d",
					i, tc.name, tc.value, prev)
			}
			seen[key] = slot
		}
	}
}
