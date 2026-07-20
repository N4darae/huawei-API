package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/sim"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestProxyListCarriesThePasswordSoExportCanWork(t *testing.T) {
	h := newHarness(t)
	h.login()

	var list ProxyList
	h.getJSON(APIBase+"/proxies", &list)
	if len(list.Items) != testSlots || list.Total != testSlots {
		t.Fatalf("got %d proxies, want %d", len(list.Items), testSlots)
	}

	for _, p := range list.Items {
		switch p.AuthMode {
		case string(domain.AuthIPList):
			if p.Password != "" {
				t.Errorf("%s is IP whitelisted and must not carry a password", p.ID)
			}
		default:
			if p.Password == "" {
				t.Errorf("%s uses credentials but GET /proxies returned no password; export is dead without it", p.ID)
			}
		}
		if p.Host != publicHost {
			t.Errorf("%s host is %q, want the node public host", p.ID, p.Host)
		}
	}
}

func TestProxyListReportsSlotPortsAndPolicy(t *testing.T) {
	h := newHarness(t)
	h.login()

	p := h.proxy("px01")
	if p.Slot != 1 || p.SocksPort != 21001 || p.HTTPPort != 22001 {
		t.Fatalf("proxy px01 is %+v", p)
	}
	if !p.Policy.AllowAllPorts || p.Policy.MaxConn != domain.DefaultMaxConn {
		t.Fatalf("policy is %+v", p.Policy)
	}
	if p.ActiveOperationID != nil {
		t.Fatalf("a quiet proxy must report no active operation, got %v", *p.ActiveOperationID)
	}
}

func TestProxyListFiltersByStateAndExpiry(t *testing.T) {
	h := newHarness(t)
	h.login()

	expired := h.nowPlus(-time.Hour)
	soon := h.nowPlus(48 * time.Hour)
	if err := h.store.Proxies().SetCustomer(context.Background(), "px02", nil, &expired); err != nil {
		t.Fatalf("expire px02: %v", err)
	}
	if err := h.store.Proxies().SetCustomer(context.Background(), "px03", nil, &soon); err != nil {
		t.Fatalf("set px03 expiry: %v", err)
	}

	var byState ProxyList
	h.getJSON(APIBase+"/proxies?state=expired", &byState)
	if len(byState.Items) != 1 || byState.Items[0].ID != "px02" {
		t.Fatalf("state filter returned %+v", byState.Items)
	}

	var within ProxyList
	h.getJSON(APIBase+"/proxies?expiring_within_days=3", &within)
	ids := map[string]bool{}
	for _, p := range within.Items {
		ids[p.ID] = true
	}
	if !ids["px02"] {
		t.Fatal("expiring_within_days must include an already expired proxy")
	}
	if !ids["px03"] {
		t.Fatal("expiring_within_days must include a proxy expiring inside the window")
	}
	if ids["px01"] {
		t.Fatal("a proxy with no expiry must not match expiring_within_days")
	}
}

func (h *harness) nowPlus(d time.Duration) int64 {
	return domain.UnixMillis(h.clock.Now().Add(d))
}

func TestProxyDetailCarriesAuthIPsAndSlot(t *testing.T) {
	h := newHarness(t)
	h.login()

	var detail ProxyDetail
	h.getJSON(APIBase+"/proxies/px02", &detail)
	if detail.Proxy.ID != "px02" {
		t.Fatalf("detail is for %q", detail.Proxy.ID)
	}
	if len(detail.AuthIPs) != 1 || detail.AuthIPs[0].CIDR != "203.0.113.5/32" {
		t.Fatalf("auth ips are %+v", detail.AuthIPs)
	}
	if detail.Slot == nil || detail.Slot.USBPath == "" {
		t.Fatal("the slot must be present and carry its usb path for the manual usbreset fallback")
	}
	if detail.Slot.IfName != "dg02" || detail.Slot.RouteTable != 1002 {
		t.Fatalf("slot is %+v", detail.Slot)
	}
}

func TestUnknownProxyIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodGet, APIBase+"/proxies/nope", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("returned %d", res.StatusCode)
	}
	if code := res.errorBody(t).Error; code != CodeNotFound {
		t.Fatalf("error code is %q", code)
	}
}

func TestEnableTogglesTheProxyAndReturnsIt(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/enable", EnableRequest{Enabled: false})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var p Proxy
	res.decode(t, &p)
	if p.Enabled || p.State != string(domain.ProxyStateDisabled) {
		t.Fatalf("proxy is %+v after being disabled", p)
	}
}

func TestAssignCustomerSetsTheNameAndExpiry(t *testing.T) {
	h := newHarness(t)
	h.login()

	id := "cus-1"
	expires := h.nowPlus(72 * time.Hour)
	res := h.do(http.MethodPost, APIBase+"/proxies/px01/customer", AssignCustomerRequest{CustomerID: &id, ExpiresAt: &expires})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}

	p := h.proxy("px01")
	if p.CustomerID == nil || *p.CustomerID != id {
		t.Fatalf("customer_id is %v", p.CustomerID)
	}
	if p.CustomerName != "Acme" {
		t.Fatalf("customer_name is %q, the grid shows it", p.CustomerName)
	}
	if p.ExpiresAt == nil || *p.ExpiresAt != expires {
		t.Fatalf("expires_at is %v", p.ExpiresAt)
	}
}

func TestSetAuthRotatesThePasswordAndKeepsTheUsername(t *testing.T) {
	h := newHarness(t)
	h.login()

	before := h.proxy("px01")
	res := h.do(http.MethodPost, APIBase+"/proxies/px01/auth", SetAuthRequest{
		AuthMode: string(domain.AuthUserPass), RotatePassword: true,
	})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}

	after := h.proxy("px01")
	if after.Password == before.Password {
		t.Fatal("rotate_password did not change the password")
	}
	if len(after.Password) != 16 {
		t.Fatalf("password %q is not the frozen 16 character shape", after.Password)
	}
	if after.Username != before.Username {
		t.Fatalf("username changed to %q", after.Username)
	}
}

func TestSetAuthToIPListWithNoWhitelistIsRefused(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/auth", SetAuthRequest{AuthMode: string(domain.AuthIPList)})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d, an empty whitelist would collapse the config to deny *", res.StatusCode)
	}
}

func TestSetAuthRejectsAnUnknownMode(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/auth", SetAuthRequest{AuthMode: "open"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestAuthIPsAreAddedListedAndDeleted(t *testing.T) {
	h := newHarness(t)
	h.login()

	add := h.do(http.MethodPost, APIBase+"/proxies/px01/auth-ips", AuthIPRequest{CIDR: "198.51.100.0/24", Note: "office"})
	if add.StatusCode != http.StatusOK {
		t.Fatalf("add returned %d: %s", add.StatusCode, add.text())
	}
	var list AuthIPList
	add.decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].CIDR != "198.51.100.0/24" {
		t.Fatalf("list is %+v", list.Items)
	}

	bare := h.do(http.MethodPost, APIBase+"/proxies/px01/auth-ips", AuthIPRequest{CIDR: "203.0.113.9"})
	bare.decode(t, &list)
	found := false
	for _, ip := range list.Items {
		if ip.CIDR == "203.0.113.9/32" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a bare address must be stored as a /32, got %+v", list.Items)
	}

	del := h.do(http.MethodDelete, APIBase+"/proxies/px01/auth-ips", AuthIPRequest{CIDR: "198.51.100.0/24"})
	del.decode(t, &list)
	for _, ip := range list.Items {
		if ip.CIDR == "198.51.100.0/24" {
			t.Fatal("the entry survived the delete")
		}
	}
}

func TestAuthIPRejectsGarbage(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/auth-ips", AuthIPRequest{CIDR: "not-an-address"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestSetPortsStoresThePolicy(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/ports", ProxyPolicy{
		AllowAllPorts: false,
		AllowedPorts:  []PortRange{{Lo: 80, Hi: 80}, {Lo: 443, Hi: 443}},
		MaxConn:       100,
		ConnLimit:     10,
	})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}

	p := h.proxy("px01")
	if p.Policy.AllowAllPorts || len(p.Policy.AllowedPorts) != 2 || p.Policy.MaxConn != 100 {
		t.Fatalf("policy is %+v", p.Policy)
	}
}

func TestSetPortsRefusesAPolicyThatDeniesEveryPort(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/ports", ProxyPolicy{MaxConn: 10, ConnLimit: 5})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestSelftestComputesEgressOKOnTheServer(t *testing.T) {
	h := newHarness(t)
	h.login()

	h.rot.selftest.SocksOK = true
	h.rot.selftest.HTTPOK = true
	h.rot.selftest.EgressIP = mustAddr("100.71.4.5")
	h.rot.selftest.LatencyMS = 180

	var out SelftestResult
	res := h.do(http.MethodPost, APIBase+"/proxies/px01/selftest", nil)
	res.decode(t, &out)
	if !out.EgressOK {
		t.Fatal("a probe leaving through the dongle must report egress_ok")
	}

	h.rot.selftest.EgressIP = mustAddr(publicHost)
	res = h.do(http.MethodPost, APIBase+"/proxies/px01/selftest", nil)
	res.decode(t, &out)
	if out.EgressOK {
		t.Fatal("a probe whose egress equals node.PublicHost is the leak the flag exists to catch")
	}
}

func TestSlotListExposesEveryHandleTheOperatorNeeds(t *testing.T) {
	h := newHarness(t)
	h.login()

	var list SlotList
	h.getJSON(APIBase+"/slots", &list)
	if len(list.Items) != testSlots {
		t.Fatalf("got %d slots", len(list.Items))
	}
	for _, s := range list.Items {
		if s.USBPath == "" {
			t.Errorf("slot %d has no usb_path; the manual usbreset instructions are then unusable", s.Slot)
		}
		if s.HostIP == "" || s.GatewayIP == "" || s.RouteTable == 0 {
			t.Errorf("slot %d is missing routing data: %+v", s.Slot, s)
		}
		if !s.Occupied || s.DongleID == nil {
			t.Errorf("slot %d should hold a dongle", s.Slot)
		}
	}
}

func TestDongleListAndDetail(t *testing.T) {
	h := newHarness(t)
	h.login()

	var list DongleList
	h.getJSON(APIBase+"/dongles", &list)
	if len(list.Items) != testSlots {
		t.Fatalf("got %d dongles", len(list.Items))
	}
	if list.Items[0].Slot != 1 {
		t.Fatalf("dongles are not ordered by slot: %+v", list.Items)
	}

	var detail DongleDetail
	h.getJSON(APIBase+"/dongles/dg-01", &detail)
	if detail.Dongle.ID != "dg-01" || detail.Slot == nil {
		t.Fatalf("detail is %+v", detail)
	}
}

func TestPatchDongleUpdatesTheCapAndRecoveryFlag(t *testing.T) {
	h := newHarness(t)
	h.login()

	off := false
	capBytes := int64(50 << 30)
	day := 7
	res := h.do(http.MethodPatch, APIBase+"/dongles/dg-01", DonglePatchRequest{
		AutoRecoverEnabled: &off, DataCapBytes: &capBytes, CapResetDay: &day,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var d Dongle
	res.decode(t, &d)
	if d.AutoRecoverEnabled || d.DataCapBytes != capBytes || d.CapResetDay != day {
		t.Fatalf("dongle is %+v", d)
	}
}

func TestPatchDongleRejectsAnImpossibleResetDay(t *testing.T) {
	h := newHarness(t)
	h.login()

	day := 31
	res := h.do(http.MethodPatch, APIBase+"/dongles/dg-01", DonglePatchRequest{CapResetDay: &day})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestRebootAcceptsAndRecordsAnOperation(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/reboot", nil)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var acc OperationAccepted
	res.decode(t, &acc)
	if acc.OperationID == "" || acc.PollURL != APIBase+"/operations/"+acc.OperationID {
		t.Fatalf("accepted body is %+v", acc)
	}

	var op Operation
	h.getJSON(acc.PollURL, &op)
	if op.Kind != string(domain.OpReboot) || op.SubjectID != "dg-01" {
		t.Fatalf("operation is %+v", op)
	}
}

func TestNetModeRejectsAnUnknownMode(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/netmode", NetModeRequest{NetMode: "6g"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestLanIPRejectsAnAddressThatIsNotIPv4(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/lanip", LanIPRequest{Gateway: "not-an-ip"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestSmsInboxIsListedSentAndDeleted(t *testing.T) {
	h := newHarness(t)
	h.login()

	dev := h.simDevice(domain.Slot(1))
	dev.AddMessage(sim.Message{Phone: "+84900000001", Content: "Your balance is 120000 VND", Box: device.SMSBoxInbox})

	var list SmsList
	h.getJSON(APIBase+"/dongles/dg-01/sms?box=1", &list)
	if len(list.Items) == 0 {
		t.Fatal("the inbox came back empty")
	}
	first := list.Items[0]
	if first.Phone == "" || first.Content == "" {
		t.Fatalf("message is %+v", first)
	}

	if res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/sms/read", SmsIndexRequest{Index: first.Index}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("mark read returned %d: %s", res.StatusCode, res.text())
	}
	if res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/sms/delete", SmsIndexRequest{Index: first.Index}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete returned %d: %s", res.StatusCode, res.text())
	}
}

func TestSmsSendNeedsARecipientAndABody(t *testing.T) {
	h := newHarness(t)
	h.login()

	if res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/sms/send", SmsSendRequest{Body: "hi"}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("no recipient returned %d", res.StatusCode)
	}
	if res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/sms/send", SmsSendRequest{To: []string{"+84900000001"}}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty body returned %d", res.StatusCode)
	}
	if res := h.do(http.MethodPost, APIBase+"/dongles/dg-01/sms/send", SmsSendRequest{To: []string{"+84900000001"}, Body: "hi"}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("a valid send returned %d", res.StatusCode)
	}
}

func TestSmsRejectsAnUnknownBox(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodGet, APIBase+"/dongles/dg-01/sms?box=9", nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestCustomersAreCreatedListedAndPatched(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/customers", CustomerRequest{Name: "Globex", Contact: "ops@globex.test"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create returned %d: %s", res.StatusCode, res.text())
	}
	var created Customer
	res.decode(t, &created)
	if created.ID == "" || created.Name != "Globex" {
		t.Fatalf("created is %+v", created)
	}

	patch := h.do(http.MethodPatch, APIBase+"/customers/"+created.ID, CustomerRequest{Name: "Globex Ltd"})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch returned %d", patch.StatusCode)
	}

	var list CustomerList
	h.getJSON(APIBase+"/customers", &list)
	names := map[string]bool{}
	for _, c := range list.Items {
		names[c.Name] = true
	}
	if !names["Globex Ltd"] || !names["Acme"] {
		t.Fatalf("customers are %+v", list.Items)
	}
}

func TestCustomerNeedsAName(t *testing.T) {
	h := newHarness(t)
	h.login()

	if res := h.do(http.MethodPost, APIBase+"/customers", CustomerRequest{}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestOperationListIsFilterable(t *testing.T) {
	h := newHarness(t)
	h.login()

	h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)

	var list OperationList
	h.getJSON(APIBase+"/operations?kind=rotate", &list)
	if len(list.Items) != 1 || list.Items[0].Kind != string(domain.OpRotate) {
		t.Fatalf("operations are %+v", list.Items)
	}

	var none OperationList
	h.getJSON(APIBase+"/operations?kind=enroll", &none)
	if len(none.Items) != 0 {
		t.Fatalf("filter leaked %d rows", len(none.Items))
	}
}

func TestUnknownOperationIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.login()

	if res := h.do(http.MethodGet, APIBase+"/operations/op-nope", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestUnknownApiPathIsAJsonNotFound(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodGet, APIBase+"/nope", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("returned %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !contains(ct, "application/json") {
		t.Fatalf("content type is %q", ct)
	}
}
