package store

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestNodeUpsertIsIdempotent(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	n := seedNode(t, s)
	n.Name = "renamed"
	if err := s.Nodes().Upsert(ctx, n); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got, err := s.Nodes().Get(ctx, "n1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "renamed" {
		t.Fatalf("name is %q after upsert, want renamed", got.Name)
	}
	if got.PublicHost != netip.MustParseAddr("139.99.68.39") {
		t.Fatalf("public host round tripped as %v", got.PublicHost)
	}
	if got.CreatedAt != domain.UnixMillis(baseTime) {
		t.Fatalf("created_at was rewritten to %d", got.CreatedAt)
	}
	list, err := s.Nodes().List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List returned %d nodes, err %v", len(list), err)
	}
}

func TestGetMissingRowsAreNotFound(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	checks := map[string]error{}
	_, err := s.Nodes().Get(ctx, "nope")
	checks["node"] = err
	_, err = s.Dongles().Get(ctx, "nope")
	checks["dongle"] = err
	_, err = s.Dongles().GetByIMEI(ctx, "nope")
	checks["dongle by imei"] = err
	_, err = s.Slots().Get(ctx, "nope")
	checks["slot"] = err
	_, err = s.Slots().GetBySlot(ctx, "n1", 1)
	checks["slot by number"] = err
	_, err = s.Slots().GetByDongle(ctx, "nope")
	checks["slot by dongle"] = err
	_, err = s.Proxies().Get(ctx, "nope")
	checks["proxy"] = err
	_, err = s.Proxies().GetBySlot(ctx, "nope")
	checks["proxy by slot"] = err
	_, err = s.Proxies().GetByUsername(ctx, "nope")
	checks["proxy by username"] = err
	_, err = s.Operations().Get(ctx, "nope")
	checks["operation"] = err
	_, err = s.Operations().FindActive(ctx, domain.SubjectProxy, "nope")
	checks["active operation"] = err
	_, err = s.Rotations().LastFor(ctx, "nope")
	checks["rotation"] = err
	_, err = s.Customers().Get(ctx, "nope")
	checks["customer"] = err
	_, err = s.Usage().GetDongleDaily(ctx, "nope", "2026-08-08")
	checks["usage"] = err
	_, err = s.Settings().Get(ctx, "nope")
	checks["setting"] = err

	for what, err := range checks {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: got %v, want ErrNotFound", what, err)
		}
	}
}

func TestUpdateMissingRowIsNotFound(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	errs := map[string]error{
		"dongle":         s.Dongles().Update(ctx, domain.Dongle{ID: "nope", NodeID: "n1", IMEI: "x"}),
		"auto recover":   s.Dongles().SetAutoRecover(ctx, "nope", true),
		"capabilities":   s.Dongles().SetCapabilities(ctx, "nope", true, false),
		"data cap":       s.Dongles().SetDataCap(ctx, "nope", 1, 1),
		"slot":           s.Slots().Update(ctx, domain.SlotRow{ID: "nope", NodeID: "n1", Slot: 1, IfName: "dg01"}),
		"detach":         s.Slots().Detach(ctx, "nope"),
		"proxy enabled":  s.Proxies().SetEnabled(ctx, "nope", false),
		"proxy policy":   s.Proxies().SetPolicy(ctx, "nope", domain.DefaultProxyPolicy()),
		"proxy customer": s.Proxies().SetCustomer(ctx, "nope", nil, nil),
		"customer":       s.Customers().Update(ctx, domain.Customer{ID: "nope", Name: "x"}),
		"delete dongle":  s.Dongles().Delete(ctx, "nope"),
	}
	for what, err := range errs {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: got %v, want ErrNotFound", what, err)
		}
	}
}

func TestDongleUniqueIMEIIsConflict(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")

	err := s.Dongles().Create(ctx, domain.Dongle{ID: "d2", NodeID: "n1", IMEI: "860000000000001"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate imei returned %v, want ErrConflict", err)
	}
}

func TestDongleDefaultsAndSetters(t *testing.T) {
	s, clock := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	if err := s.Dongles().Create(ctx, domain.Dongle{ID: "d1", NodeID: "n1", IMEI: "860000000000001"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	d, err := s.Dongles().Get(ctx, "d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Classify != domain.ClassifyHiLink {
		t.Errorf("classify defaulted to %q, want hilink", d.Classify)
	}
	if d.CapResetDay != 1 {
		t.Errorf("cap_reset_day defaulted to %d, want 1", d.CapResetDay)
	}
	if d.AutoRecoverEnabled {
		t.Error("auto recover follows the value passed in, not the column default")
	}

	clock.advance(time.Second)
	if err := s.Dongles().SetAutoRecover(ctx, "d1", true); err != nil {
		t.Fatalf("SetAutoRecover: %v", err)
	}
	if err := s.Dongles().SetCapabilities(ctx, "d1", true, true); err != nil {
		t.Fatalf("SetCapabilities: %v", err)
	}
	if err := s.Dongles().SetDataCap(ctx, "d1", 50<<30, 15); err != nil {
		t.Fatalf("SetDataCap: %v", err)
	}
	d, err = s.Dongles().Get(ctx, "d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !d.AutoRecoverEnabled || !d.LanIPChangeSupported || !d.HilinkLoginRequired {
		t.Errorf("setters did not persist: %+v", d)
	}
	if d.DataCapBytes != 50<<30 || d.CapResetDay != 15 {
		t.Errorf("data cap did not persist: %d %d", d.DataCapBytes, d.CapResetDay)
	}
	if d.UpdatedAt <= d.CreatedAt {
		t.Errorf("updated_at %d did not move past created_at %d", d.UpdatedAt, d.CreatedAt)
	}

	if err := s.Dongles().SetDataCap(ctx, "d1", 1, 29); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("cap reset day 29 returned %v, want ErrInvalid", err)
	}
}

func TestDongleListFilters(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")
	seedDongle(t, s, "d2", "860000000000002")

	all, err := s.Dongles().List(ctx, DongleFilter{NodeID: "n1"})
	if err != nil || len(all) != 2 {
		t.Fatalf("List by node returned %d, err %v", len(all), err)
	}
	one, err := s.Dongles().List(ctx, DongleFilter{IMEI: "860000000000002"})
	if err != nil || len(one) != 1 || one[0].ID != "d2" {
		t.Fatalf("List by imei returned %+v, err %v", one, err)
	}
	limited, err := s.Dongles().List(ctx, DongleFilter{Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Fatalf("List with limit returned %d, err %v", len(limited), err)
	}
	byCarrier, err := s.Dongles().List(ctx, DongleFilter{Carrier: "viettel"})
	if err != nil || len(byCarrier) != 2 {
		t.Fatalf("List by carrier returned %d, err %v", len(byCarrier), err)
	}
	none, err := s.Dongles().List(ctx, DongleFilter{Carrier: "nobody"})
	if err != nil || len(none) != 0 {
		t.Fatalf("List by unknown carrier returned %d, err %v", len(none), err)
	}
}

func TestSlotAttachDetachSwapsADeadStick(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 3)
	seedDongle(t, s, "d1", "860000000000001")
	seedDongle(t, s, "d2", "860000000000002")
	proxy := seedProxy(t, s, "p1", row.ID, 3)

	if err := s.Slots().Attach(ctx, row.ID, "d1"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := s.Slots().Attach(ctx, row.ID, "d1"); err != nil {
		t.Fatalf("Attach is not idempotent: %v", err)
	}
	if err := s.Slots().Attach(ctx, row.ID, "d2"); !errors.Is(err, domain.ErrSlotOccupied) {
		t.Fatalf("attaching a second dongle returned %v, want ErrSlotOccupied", err)
	}

	if err := s.Slots().Detach(ctx, row.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	got, err := s.Slots().Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Occupied() {
		t.Fatal("slot still reports a dongle after Detach")
	}
	if _, err := s.Proxies().Get(ctx, proxy.ID); err != nil {
		t.Fatalf("the proxy must survive a dead stick: %v", err)
	}
	if err := s.Slots().Attach(ctx, row.ID, "d2"); err != nil {
		t.Fatalf("Attach after Detach: %v", err)
	}
	found, err := s.Slots().GetByDongle(ctx, "d2")
	if err != nil || found.ID != row.ID {
		t.Fatalf("GetByDongle returned %+v, err %v", found, err)
	}
}

func TestSlotAttachRejectsADongleHeldElsewhere(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	a := seedSlot(t, s, 1)
	b := seedSlot(t, s, 2)
	seedDongle(t, s, "d1", "860000000000001")

	if err := s.Slots().Attach(ctx, a.ID, "d1"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := s.Slots().Attach(ctx, b.ID, "d1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("attaching one dongle to two slots returned %v, want ErrConflict", err)
	}
}

func TestDeletingADongleNullsTheSlot(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedDongle(t, s, "d1", "860000000000001")
	if err := s.Slots().Attach(ctx, row.ID, "d1"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := s.Dongles().Delete(ctx, "d1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := s.Slots().Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get slot: %v", err)
	}
	if got.Occupied() {
		t.Fatal("ON DELETE SET NULL did not fire on slots.dongle_id")
	}
}

func TestSlotNextFree(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	got, err := s.Slots().NextFree(ctx, "n1")
	if err != nil || got != 1 {
		t.Fatalf("NextFree on an empty node returned %d, err %v", int(got), err)
	}
	seedSlot(t, s, 1)
	seedSlot(t, s, 2)
	seedSlot(t, s, 4)
	got, err = s.Slots().NextFree(ctx, "n1")
	if err != nil || got != 3 {
		t.Fatalf("NextFree returned %d, err %v", int(got), err)
	}

	for i := 3; i <= domain.MaxSlots; i++ {
		if i == 4 {
			continue
		}
		seedSlot(t, s, domain.Slot(i))
	}
	if _, err := s.Slots().NextFree(ctx, "n1"); !errors.Is(err, domain.ErrNoFreeSlot) {
		t.Fatalf("NextFree on a full node returned %v, want ErrNoFreeSlot", err)
	}
}

func TestSlotRejectsOutOfRange(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	beyond := domain.Slot(domain.MaxSlots + 1)
	err := s.Slots().Create(ctx, domain.SlotRow{
		ID: "x", NodeID: "n1", Slot: beyond, USBPath: "1-1", IfName: beyond.IfaceName(),
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("slot %d returned %v, want ErrInvalid", int(beyond), err)
	}
}

func TestProxyPasswordIsEncryptedAtRest(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	var enc []byte
	if err := s.db.QueryRowContext(ctx, `SELECT password_enc FROM proxies WHERE id='p1'`).Scan(&enc); err != nil {
		t.Fatalf("read raw column: %v", err)
	}
	if string(enc) == "Kq7mZr2xTn9wLb4V" {
		t.Fatal("the password is stored in plaintext")
	}
	if len(enc) <= 24 {
		t.Fatalf("ciphertext is %d bytes, too short to carry a 24 byte nonce and a tag", len(enc))
	}

	p, err := s.Proxies().Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Password != "Kq7mZr2xTn9wLb4V" {
		t.Fatalf("decrypted password is %q", p.Password)
	}
}

func TestProxyWrongKekFailsLoudly(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	other, err := Open(s.path, testSealer(t))
	if err != nil {
		t.Fatalf("reopen with another kek: %v", err)
	}
	defer other.Close()
	if _, err := other.Proxies().Get(ctx, "p1"); err == nil {
		t.Fatal("reading a proxy under the wrong kek silently succeeded")
	}
}

func TestProxySlotIsUnique(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	err := s.Proxies().Create(ctx, domain.Proxy{
		ID: "p2", SlotID: row.ID, SocksPort: 21099, HTTPPort: 22099,
		Username: "cust_other", Password: "x", AuthMode: domain.AuthUserPass,
		Policy: domain.DefaultProxyPolicy(),
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a second proxy on the same slot returned %v, want ErrConflict", err)
	}
}

func TestProxyCascadesWithTheSlot(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	if err := s.Slots().Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete slot: %v", err)
	}
	if _, err := s.Proxies().Get(ctx, "p1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the proxy survived its slot: %v", err)
	}
}

func TestProxySettersAndPolicy(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	if err := s.Proxies().SetEnabled(ctx, "p1", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := s.Proxies().SetSuspended(ctx, "p1", true); err != nil {
		t.Fatalf("SetSuspended: %v", err)
	}
	if err := s.Proxies().SetCredentials(ctx, "p1", "cust_new", "Ab12Cd34Ef56Gh78"); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	if err := s.Proxies().SetAuthMode(ctx, "p1", domain.AuthBoth); err != nil {
		t.Fatalf("SetAuthMode: %v", err)
	}
	policy := domain.ProxyPolicy{AllowedPorts: []domain.PortRange{{Lo: 80, Hi: 80}, {Lo: 8000, Hi: 8999}}, MaxConn: 10, ConnLimit: 5}
	if err := s.Proxies().SetPolicy(ctx, "p1", policy); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	p, err := s.Proxies().Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Enabled || !p.Suspended {
		t.Errorf("enabled/suspended did not persist: %+v", p)
	}
	if p.Username != "cust_new" || p.Password != "Ab12Cd34Ef56Gh78" {
		t.Errorf("credentials did not persist: %q %q", p.Username, p.Password)
	}
	if p.AuthMode != domain.AuthBoth {
		t.Errorf("auth mode is %q", p.AuthMode)
	}
	if p.Policy.AllowAllPorts {
		t.Error("allow_all_ports did not clear")
	}
	if got := domain.FormatPortRanges(p.Policy.AllowedPorts); got != "80,8000-8999" {
		t.Errorf("allowed ports round tripped as %q", got)
	}
	if p.Policy.MaxConn != 10 || p.Policy.ConnLimit != 5 {
		t.Errorf("limits did not persist: %+v", p.Policy)
	}

	if err := s.Proxies().SetAuthMode(ctx, "p1", domain.AuthMode("nope")); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("an unknown auth mode returned %v, want ErrInvalid", err)
	}
	closed := domain.ProxyPolicy{MaxConn: 1}
	if err := s.Proxies().SetPolicy(ctx, "p1", closed); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("a policy that denies every port returned %v, want ErrInvalid", err)
	}
}

func TestProxyCustomerAndExpiry(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	if err := s.Customers().Create(ctx, domain.Customer{ID: "c1", Name: "Acme"}); err != nil {
		t.Fatalf("Create customer: %v", err)
	}
	id := "c1"
	expires := domain.UnixMillis(baseTime.Add(-time.Second))
	if err := s.Proxies().SetCustomer(ctx, "p1", &id, &expires); err != nil {
		t.Fatalf("SetCustomer: %v", err)
	}

	n, err := s.Customers().CountProxies(ctx, "c1")
	if err != nil || n != 1 {
		t.Fatalf("CountProxies returned %d, err %v", n, err)
	}

	expired, err := s.Proxies().ListExpired(ctx, domain.UnixMillis(baseTime))
	if err != nil || len(expired) != 1 || expired[0].ID != "p1" {
		t.Fatalf("ListExpired returned %+v, err %v", expired, err)
	}
	if !expired[0].Expired(domain.UnixMillis(baseTime)) {
		t.Error("domain.Proxy.Expired disagrees with ListExpired")
	}

	if err := s.Customers().Delete(ctx, "c1"); err != nil {
		t.Fatalf("Delete customer: %v", err)
	}
	p, err := s.Proxies().Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.CustomerID != nil {
		t.Fatal("deleting a customer must null proxies.customer_id, not delete the proxy")
	}
}

func TestProxyListFilters(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	one := seedSlot(t, s, 1)
	two := seedSlot(t, s, 2)
	seedProxy(t, s, "p1", one.ID, 1)
	seedProxy(t, s, "p2", two.ID, 2)
	if err := s.Proxies().SetEnabled(ctx, "p2", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	yes := true
	on, err := s.Proxies().List(ctx, ProxyFilter{Enabled: &yes})
	if err != nil || len(on) != 1 || on[0].ID != "p1" {
		t.Fatalf("List enabled returned %+v, err %v", on, err)
	}
	bySlot, err := s.Proxies().List(ctx, ProxyFilter{SlotID: two.ID})
	if err != nil || len(bySlot) != 1 || bySlot[0].ID != "p2" {
		t.Fatalf("List by slot returned %+v, err %v", bySlot, err)
	}
	all, err := s.Proxies().List(ctx, ProxyFilter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("List all returned %d, err %v", len(all), err)
	}
	if all[0].SocksPort > all[1].SocksPort {
		t.Error("List is not ordered by socks port")
	}
}

func TestProxyAuthIPs(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	add := func(id, cidr string) error {
		return s.Proxies().AddAuthIP(ctx, domain.ProxyAuthIP{
			ID: id, ProxyID: "p1", CIDR: netip.MustParsePrefix(cidr), Note: "office",
		})
	}
	if err := add("a1", "203.0.113.5/32"); err != nil {
		t.Fatalf("AddAuthIP: %v", err)
	}
	if err := add("a2", "198.51.100.0/24"); err != nil {
		t.Fatalf("AddAuthIP: %v", err)
	}
	if err := add("a3", "203.0.113.5/32"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate cidr returned %v, want ErrConflict", err)
	}

	ips, err := s.Proxies().ListAuthIPs(ctx, "p1")
	if err != nil || len(ips) != 2 {
		t.Fatalf("ListAuthIPs returned %d, err %v", len(ips), err)
	}

	p, err := s.Proxies().Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(p.AuthIPs) != 2 {
		t.Fatalf("Get did not hydrate AuthIPs, got %d", len(p.AuthIPs))
	}
	if p.AuthIPs[0].String() != "198.51.100.0/24" {
		t.Errorf("AuthIPs are not sorted, first is %s", p.AuthIPs[0])
	}

	if err := s.Proxies().DeleteAuthIP(ctx, "p1", netip.MustParsePrefix("203.0.113.5/32")); err != nil {
		t.Fatalf("DeleteAuthIP: %v", err)
	}
	if err := s.Proxies().DeleteAuthIP(ctx, "p1", netip.MustParsePrefix("203.0.113.5/32")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleting a missing auth ip returned %v, want ErrNotFound", err)
	}

	if err := s.Proxies().Delete(ctx, "p1"); err != nil {
		t.Fatalf("Delete proxy: %v", err)
	}
	left, err := s.Proxies().ListAuthIPs(ctx, "p1")
	if err != nil || len(left) != 0 {
		t.Fatalf("auth ips did not cascade, %d left, err %v", len(left), err)
	}
}

func TestProxyAuthIPCidrIsMasked(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	if err := s.Proxies().AddAuthIP(ctx, domain.ProxyAuthIP{
		ID: "a1", ProxyID: "p1", CIDR: netip.MustParsePrefix("198.51.100.77/24"),
	}); err != nil {
		t.Fatalf("AddAuthIP: %v", err)
	}
	ips, err := s.Proxies().ListAuthIPs(ctx, "p1")
	if err != nil || len(ips) != 1 {
		t.Fatalf("ListAuthIPs: %d %v", len(ips), err)
	}
	if ips[0].CIDR.String() != "198.51.100.0/24" {
		t.Fatalf("host bits survived masking: %s", ips[0].CIDR)
	}
	if err := s.Proxies().DeleteAuthIP(ctx, "p1", netip.MustParsePrefix("198.51.100.99/24")); err != nil {
		t.Fatalf("DeleteAuthIP must mask too: %v", err)
	}
}

func TestOperationOnlyOneLivePerSubject(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	op := domain.Operation{
		ID: "op1", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: "p1",
		State: domain.OpRunning, Trigger: domain.TriggerAdminUI,
		StartedAt: domain.UnixMillis(baseTime), DeadlineAt: domain.UnixMillis(baseTime.Add(90 * time.Second)),
	}
	if err := s.Operations().Create(ctx, op); err != nil {
		t.Fatalf("Create: %v", err)
	}

	second := op
	second.ID = "op2"
	if err := s.Operations().Create(ctx, second); !errors.Is(err, domain.ErrOpInProgress) {
		t.Fatalf("a second live operation returned %v, want ErrOpInProgress", err)
	}

	if err := s.Operations().Finish(ctx, "op1", domain.OpSucceeded, "", `{"ip_changed":true}`, domain.UnixMillis(baseTime.Add(time.Second))); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := s.Operations().Create(ctx, second); err != nil {
		t.Fatalf("Create after Finish: %v", err)
	}
}

func TestOperationLifecycle(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	op := domain.Operation{
		ID: "op1", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: "p1",
		Trigger: domain.TriggerAutoRecovery, ActorType: domain.ActorSystem,
		StartedAt: domain.UnixMillis(baseTime), DeadlineAt: domain.UnixMillis(baseTime.Add(90 * time.Second)),
	}
	if err := s.Operations().Create(ctx, op); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Operations().Get(ctx, "op1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != domain.OpPending {
		t.Errorf("state defaulted to %q, want pending", got.State)
	}
	if !got.Active() {
		t.Error("a fresh operation must be Active")
	}

	for i, step := range domain.RotateSteps() {
		if step == domain.StepDone {
			continue
		}
		if err := s.Operations().Progress(ctx, "op1", domain.OpRunning, string(step), i*10); err != nil {
			t.Fatalf("Progress %s: %v", step, err)
		}
	}
	got, err = s.Operations().Get(ctx, "op1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Step != string(domain.StepVerify) {
		t.Errorf("step is %q", got.Step)
	}

	live, err := s.Operations().FindActive(ctx, domain.SubjectProxy, "p1")
	if err != nil || live.ID != "op1" {
		t.Fatalf("FindActive returned %+v, err %v", live, err)
	}
	actives, err := s.Operations().ListActive(ctx)
	if err != nil || len(actives) != 1 {
		t.Fatalf("ListActive returned %d, err %v", len(actives), err)
	}

	if err := s.Operations().Finish(ctx, "op1", domain.OpSucceeded, "", "", domain.UnixMillis(baseTime.Add(time.Minute))); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := s.Operations().Finish(ctx, "op1", domain.OpFailed, "late", "", 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("finishing twice returned %v, want ErrNotFound", err)
	}
	if err := s.Operations().Progress(ctx, "op1", domain.OpRunning, "x", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("progressing a finished operation returned %v, want ErrNotFound", err)
	}

	got, err = s.Operations().Get(ctx, "op1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Active() || got.Pct != 100 || got.FinishedAt == nil {
		t.Fatalf("finished operation looks wrong: %+v", got)
	}
}

func TestOperationRejectsInvalidEnums(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	base := domain.Operation{
		ID: "op1", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: "p1",
		Trigger: domain.TriggerAdminUI, StartedAt: 1, DeadlineAt: 2,
	}

	bad := base
	bad.Trigger = domain.Trigger("schedule")
	if err := s.Operations().Create(ctx, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("trigger 'schedule' returned %v, want ErrInvalid", err)
	}
	bad = base
	bad.Kind = domain.OpKind("teleport")
	if err := s.Operations().Create(ctx, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown kind returned %v, want ErrInvalid", err)
	}
	if err := s.Operations().Progress(ctx, "op1", domain.OpSucceeded, "", 0); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("Progress to a terminal state returned %v, want ErrInvalid", err)
	}
	if err := s.Operations().Finish(ctx, "op1", domain.OpRunning, "", "", 0); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("Finish with a live state returned %v, want ErrInvalid", err)
	}
}

func TestOperationMarkStalledAndReconcileOrphans(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	for i := 1; i <= 3; i++ {
		row := seedSlot(t, s, domain.Slot(i))
		seedProxy(t, s, "p"+row.Slot.String(), row.ID, row.Slot)
	}

	start := domain.UnixMillis(baseTime)
	for i := 1; i <= 3; i++ {
		id := domain.Slot(i).String()
		if err := s.Operations().Create(ctx, domain.Operation{
			ID: "op" + id, Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: "p" + id,
			State: domain.OpRunning, Trigger: domain.TriggerCustomerAPI,
			StartedAt: start, DeadlineAt: start + 90_000,
		}); err != nil {
			t.Fatalf("Create op%s: %v", id, err)
		}
	}

	n, err := s.Operations().MarkStalled(ctx, start+1000)
	if err != nil || n != 0 {
		t.Fatalf("MarkStalled before the deadline marked %d, err %v", n, err)
	}
	n, err = s.Operations().MarkStalled(ctx, start+120_000)
	if err != nil || n != 3 {
		t.Fatalf("MarkStalled after the deadline marked %d, err %v", n, err)
	}
	got, err := s.Operations().Get(ctx, "op01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != domain.OpStalled || got.Active() {
		t.Fatalf("stalled operation looks wrong: %+v", got)
	}
	if got.Error != StalledPastDeadline {
		t.Fatalf("stall was not recorded as evidence: %+v", got)
	}

	actives, err := s.Operations().ListActive(ctx)
	if err != nil || len(actives) != 0 {
		t.Fatalf("%d operations are still live after stalling, err %v", len(actives), err)
	}

	n, err = s.Operations().MarkStalled(ctx, start+130_000)
	if err != nil || n != 0 {
		t.Fatalf("MarkStalled is not idempotent: %d, err %v", n, err)
	}

	n, err = s.Operations().ReconcileOrphans(ctx, start+130_000)
	if err != nil || n != 0 {
		t.Fatalf("ReconcileOrphans reconciled %d finished operations, err %v", n, err)
	}
}

func TestOperationReconcileOrphansFailsLiveRows(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedSlot(t, s, 1)
	start := int64(1786190400000)
	if err := s.Operations().Create(ctx, domain.Operation{
		ID: "op01", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: "p01",
		State: domain.OpRunning, Trigger: domain.TriggerCustomerAPI,
		StartedAt: start, DeadlineAt: start + 90_000,
	}); err != nil {
		t.Fatalf("Create op01: %v", err)
	}

	n, err := s.Operations().ReconcileOrphans(ctx, start+130_000)
	if err != nil || n != 1 {
		t.Fatalf("ReconcileOrphans reconciled %d, err %v", n, err)
	}
	got, err := s.Operations().Get(ctx, "op01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != domain.OpFailed || got.Error != OrphanedByRestart {
		t.Fatalf("orphan was not recorded as evidence: %+v", got)
	}

	n, err = s.Operations().ReconcileOrphans(ctx, start+140_000)
	if err != nil || n != 0 {
		t.Fatalf("ReconcileOrphans is not idempotent: %d, err %v", n, err)
	}
}

func TestOperationListFilters(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)
	start := domain.UnixMillis(baseTime)

	mk := func(id string, kind domain.OpKind, trig domain.Trigger, at int64, finished bool) {
		t.Helper()
		op := domain.Operation{
			ID: id, Kind: kind, SubjectType: domain.SubjectProxy, SubjectID: "p1",
			State: domain.OpRunning, Trigger: trig, StartedAt: at, DeadlineAt: at + 1000,
		}
		if finished {
			f := at + 500
			op.FinishedAt = &f
			op.State = domain.OpSucceeded
		}
		if err := s.Operations().Create(ctx, op); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	mk("a", domain.OpRotate, domain.TriggerAdminUI, start, true)
	mk("b", domain.OpRotate, domain.TriggerAutoRecovery, start+1000, true)
	mk("c", domain.OpReboot, domain.TriggerAutoRecovery, start+2000, false)

	byKind, err := s.Operations().List(ctx, OperationFilter{Kind: domain.OpRotate})
	if err != nil || len(byKind) != 2 {
		t.Fatalf("List by kind returned %d, err %v", len(byKind), err)
	}
	byTrigger, err := s.Operations().List(ctx, OperationFilter{Trigger: domain.TriggerAutoRecovery})
	if err != nil || len(byTrigger) != 2 {
		t.Fatalf("List by trigger returned %d, err %v", len(byTrigger), err)
	}
	since, err := s.Operations().List(ctx, OperationFilter{SinceMS: start + 1500})
	if err != nil || len(since) != 1 || since[0].ID != "c" {
		t.Fatalf("List since returned %+v, err %v", since, err)
	}
	limited, err := s.Operations().List(ctx, OperationFilter{Limit: 1})
	if err != nil || len(limited) != 1 || limited[0].ID != "c" {
		t.Fatalf("List with limit returned %+v, err %v", limited, err)
	}
}

func TestRotationEvidence(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)
	start := domain.UnixMillis(baseTime)

	if err := s.Operations().Create(ctx, domain.Operation{
		ID: "op1", Kind: domain.OpRotate, SubjectType: domain.SubjectProxy, SubjectID: "p1",
		State: domain.OpSucceeded, Trigger: domain.TriggerAutoRecovery,
		StartedAt: start, DeadlineAt: start + 90_000, FinishedAt: &start,
	}); err != nil {
		t.Fatalf("Create operation: %v", err)
	}

	rot := domain.Rotation{
		ID: "r1", OperationID: "op1", ProxyID: "p1", RequestedAt: start, DurationMS: 12_000,
		OldPublicIP: netip.MustParseAddr("100.64.1.2"),
		NewPublicIP: netip.MustParseAddr("100.64.9.9"),
		IPChanged:   true, Result: domain.RotationChanged, Trigger: domain.TriggerAutoRecovery,
		HoldMS: 6000, Attempts: 1,
	}
	if err := s.Rotations().Create(ctx, rot); err != nil {
		t.Fatalf("Create rotation: %v", err)
	}
	if err := s.Rotations().Create(ctx, rot); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a second rotation for the same operation returned %v, want ErrConflict", err)
	}

	last, err := s.Rotations().LastFor(ctx, "p1")
	if err != nil {
		t.Fatalf("LastFor: %v", err)
	}
	if last.NewPublicIP != netip.MustParseAddr("100.64.9.9") || !last.IPChanged {
		t.Fatalf("rotation round tripped as %+v", last)
	}
	if last.Result != domain.RotationChanged || last.Trigger != domain.TriggerAutoRecovery {
		t.Fatalf("rotation enums round tripped as %q %q", last.Result, last.Trigger)
	}

	list, err := s.Rotations().List(ctx, RotationFilter{ProxyID: "p1", SinceMS: start})
	if err != nil || len(list) != 1 {
		t.Fatalf("List returned %d, err %v", len(list), err)
	}

	bad := rot
	bad.ID = "r2"
	bad.Result = domain.RotationResult("maybe")
	if err := s.Rotations().Create(ctx, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an unknown rotation result returned %v, want ErrInvalid", err)
	}
}

func TestRotationCascadesWithTheOperation(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)
	start := domain.UnixMillis(baseTime)

	if err := s.Rotations().Create(ctx, domain.Rotation{
		ID: "r1", OperationID: "missing", ProxyID: "p1", RequestedAt: start,
		Result: domain.RotationFailed, Trigger: domain.TriggerAdminUI,
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a rotation without an operation returned %v, want ErrInvalid", err)
	}
}

func TestUsageAccumulates(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")
	now := domain.UnixMillis(baseTime)

	if err := s.Usage().AddDongleDaily(ctx, "d1", "2026-08-08", 100, 200, now); err != nil {
		t.Fatalf("AddDongleDaily: %v", err)
	}
	if err := s.Usage().AddDongleDaily(ctx, "d1", "2026-08-08", 50, 25, now+1000); err != nil {
		t.Fatalf("AddDongleDaily: %v", err)
	}
	if err := s.Usage().AddDongleDaily(ctx, "d1", "2026-08-09", 7, 8, now+2000); err != nil {
		t.Fatalf("AddDongleDaily: %v", err)
	}

	day, err := s.Usage().GetDongleDaily(ctx, "d1", "2026-08-08")
	if err != nil {
		t.Fatalf("GetDongleDaily: %v", err)
	}
	if day.UpBytes != 150 || day.DownBytes != 225 {
		t.Fatalf("usage did not accumulate: %+v", day)
	}

	days, err := s.Usage().ListDongleDaily(ctx, "d1", "2026-08-01", "2026-08-31")
	if err != nil || len(days) != 2 {
		t.Fatalf("ListDongleDaily returned %d, err %v", len(days), err)
	}
	up, down, err := s.Usage().SumDongleSince(ctx, "d1", "2026-08-01")
	if err != nil || up != 157 || down != 233 {
		t.Fatalf("SumDongleSince returned %d/%d, err %v", up, down, err)
	}
	up, down, err = s.Usage().SumDongleSince(ctx, "d1", "2027-01-01")
	if err != nil || up != 0 || down != 0 {
		t.Fatalf("SumDongleSince with no rows returned %d/%d, err %v", up, down, err)
	}

	if err := s.Usage().AddDongleDaily(ctx, "d1", "2026-08-08", -1, 0, now); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("a negative delta returned %v, want ErrInvalid", err)
	}
}

func TestSMSUpsertAndFragments(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")
	now := domain.UnixMillis(baseTime)

	m := device.SMS{
		Index: 40001, Phone: "+84900000000", Content: "half a long message",
		Date: now, Box: device.SMSBoxInbox, SmsType: device.SMSTypeFragment,
	}
	if err := s.SMS().Upsert(ctx, "d1", m, now); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	m.Content = "half a long message, corrected"
	if err := s.SMS().Upsert(ctx, "d1", m, now); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	list, total, err := s.SMS().List(ctx, SMSFilter{DongleID: "d1", Box: device.SMSBoxInbox})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("Upsert duplicated the message: total %d, rows %d", total, len(list))
	}
	if !list[0].IsFragment {
		t.Error("SmsType 2 must set is_fragment so the UI can mark it")
	}
	if list[0].Content != "half a long message, corrected" {
		t.Errorf("upsert did not update content: %q", list[0].Content)
	}

	unread, err := s.SMS().CountUnread(ctx, "d1")
	if err != nil || unread != 1 {
		t.Fatalf("CountUnread returned %d, err %v", unread, err)
	}
	if err := s.SMS().MarkRead(ctx, "d1", device.SMSBoxInbox, 40001); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	unread, err = s.SMS().CountUnread(ctx, "d1")
	if err != nil || unread != 0 {
		t.Fatalf("CountUnread after MarkRead returned %d, err %v", unread, err)
	}

	if err := s.SMS().Delete(ctx, "d1", device.SMSBoxInbox, 40001); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.SMS().Delete(ctx, "d1", device.SMSBoxInbox, 40001); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleting twice returned %v, want ErrNotFound", err)
	}
	if err := s.SMS().Upsert(ctx, "d1", device.SMS{Index: 1, Box: device.SMSBox(9)}, now); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an unknown box returned %v, want ErrInvalid", err)
	}
}

func TestSMSPaging(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")
	now := domain.UnixMillis(baseTime)

	for i := 0; i < 5; i++ {
		if err := s.SMS().Upsert(ctx, "d1", device.SMS{
			Index: int64(i), Box: device.SMSBoxInbox, Date: now + int64(i)*1000, Content: "m",
		}, now); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}
	page, total, err := s.SMS().List(ctx, SMSFilter{DongleID: "d1", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 || len(page) != 2 {
		t.Fatalf("paging returned total %d rows %d", total, len(page))
	}
	if page[0].Index != 3 || page[1].Index != 2 {
		t.Fatalf("paging is not newest first: %d %d", page[0].Index, page[1].Index)
	}
}

func TestSMSCascadesWithTheDongle(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedDongle(t, s, "d1", "860000000000001")
	now := domain.UnixMillis(baseTime)

	if err := s.SMS().Upsert(ctx, "d1", device.SMS{Index: 1, Box: device.SMSBoxInbox, Date: now}, now); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Dongles().Delete(ctx, "d1"); err != nil {
		t.Fatalf("Delete dongle: %v", err)
	}
	_, total, err := s.SMS().List(ctx, SMSFilter{DongleID: "d1"})
	if err != nil || total != 0 {
		t.Fatalf("sms did not cascade: total %d, err %v", total, err)
	}
}

func TestSettings(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	now := domain.UnixMillis(baseTime)

	if err := s.Settings().Set(ctx, SettingSchemaOwner, "p0", now); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Settings().Set(ctx, SettingSchemaOwner, "p4", now+1); err != nil {
		t.Fatalf("Set again: %v", err)
	}
	v, err := s.Settings().Get(ctx, SettingSchemaOwner)
	if err != nil || v != "p4" {
		t.Fatalf("Get returned %q, err %v", v, err)
	}
	if err := s.Settings().Set(ctx, SettingHostBootID, "boot-1", now); err != nil {
		t.Fatalf("Set: %v", err)
	}
	all, err := s.Settings().All(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("All returned %d, err %v", len(all), err)
	}
	if err := s.Settings().Set(ctx, "", "x", now); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an empty key returned %v, want ErrInvalid", err)
	}
}

func TestCustomerCRUD(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	if err := s.Customers().Create(ctx, domain.Customer{ID: "c1", Name: "Acme", Contact: "ops@acme.test"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Customers().Create(ctx, domain.Customer{ID: "c1", Name: "Acme"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a duplicate id returned %v, want ErrConflict", err)
	}
	if err := s.Customers().Update(ctx, domain.Customer{ID: "c1", Name: "Acme Ltd", Note: "prepaid"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	c, err := s.Customers().Get(ctx, "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c.Name != "Acme Ltd" || c.Note != "prepaid" || c.Contact != "" {
		t.Fatalf("Update wrote %+v", c)
	}
	list, err := s.Customers().List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List returned %d, err %v", len(list), err)
	}
	n, err := s.Customers().CountProxies(ctx, "c1")
	if err != nil || n != 0 {
		t.Fatalf("CountProxies returned %d, err %v", n, err)
	}
	if err := s.Customers().Delete(ctx, "c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRequiredFieldsAreRejected(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	errs := map[string]error{
		"node":      s.Nodes().Upsert(ctx, domain.Node{}),
		"dongle":    s.Dongles().Create(ctx, domain.Dongle{NodeID: "n1"}),
		"slot":      s.Slots().Create(ctx, domain.SlotRow{NodeID: "n1", Slot: 1}),
		"proxy":     s.Proxies().Create(ctx, domain.Proxy{}),
		"operation": s.Operations().Create(ctx, domain.Operation{Kind: domain.OpRotate, Trigger: domain.TriggerAdminUI}),
		"rotation":  s.Rotations().Create(ctx, domain.Rotation{}),
		"customer":  s.Customers().Create(ctx, domain.Customer{}),
		"usage":     s.Usage().AddDongleDaily(ctx, "", "", 0, 0, 0),
		"sms":       s.SMS().Upsert(ctx, "", device.SMS{Box: device.SMSBoxInbox}, 0),
		"authip":    s.Proxies().AddAuthIP(ctx, domain.ProxyAuthIP{}),
		"attach":    s.Slots().Attach(ctx, "s1", ""),
	}
	for what, err := range errs {
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", what, err)
		}
	}
}
