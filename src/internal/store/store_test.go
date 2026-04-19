package store

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/secrets"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func (c *fixedClock) Since(t time.Time) time.Duration { return c.t.Sub(t) }

func (c *fixedClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *fixedClock) Sleep(ctx context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return ctx.Err()
}

func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

var baseTime = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

func testSealer(t *testing.T) secrets.Sealer {
	t.Helper()
	kek, err := secrets.GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	s, err := secrets.NewSealer(kek)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func openStore(t *testing.T) (*Store, *fixedClock) {
	t.Helper()
	clock := &fixedClock{t: baseTime}
	path := filepath.Join(t.TempDir(), "state", "dongled.db")
	s, err := Open(path, testSealer(t), WithClock(clock))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s, clock
}

func seedNode(t *testing.T, s *Store) domain.Node {
	t.Helper()
	n := domain.Node{
		ID:         "n1",
		Name:       "local",
		Kind:       domain.NodeKindLocal,
		PublicHost: netip.MustParseAddr("139.99.68.39"),
	}
	if err := s.Nodes().Upsert(context.Background(), n); err != nil {
		t.Fatalf("Upsert node: %v", err)
	}
	return n
}

func seedSlot(t *testing.T, s *Store, slot domain.Slot) domain.SlotRow {
	t.Helper()
	row := domain.SlotRow{
		ID:      "s" + slot.String(),
		NodeID:  "n1",
		Slot:    slot,
		USBPath: "1-13." + slot.String(),
		IDPath:  "pci-0000:00:14.0-usb-0:13." + slot.String() + ":1.0",
		IfName:  slot.IfaceName(),
	}
	if err := s.Slots().Create(context.Background(), row); err != nil {
		t.Fatalf("Create slot %d: %v", int(slot), err)
	}
	return row
}

func seedDongle(t *testing.T, s *Store, id, imei string) domain.Dongle {
	t.Helper()
	d := domain.Dongle{
		ID:                 id,
		NodeID:             "n1",
		IMEI:               imei,
		Carrier:            "viettel",
		AutoRecoverEnabled: true,
	}
	if err := s.Dongles().Create(context.Background(), d); err != nil {
		t.Fatalf("Create dongle %s: %v", id, err)
	}
	return d
}

func seedProxy(t *testing.T, s *Store, id, slotID string, slot domain.Slot) domain.Proxy {
	t.Helper()
	p := domain.Proxy{
		ID:        id,
		SlotID:    slotID,
		Enabled:   true,
		SocksPort: slot.SocksPort(),
		HTTPPort:  slot.HTTPPort(),
		Username:  "cust_" + id,
		Password:  "Kq7mZr2xTn9wLb4V",
		AuthMode:  domain.AuthUserPass,
		Policy:    domain.DefaultProxyPolicy(),
	}
	if err := s.Proxies().Create(context.Background(), p); err != nil {
		t.Fatalf("Create proxy %s: %v", id, err)
	}
	return p
}

func TestOpenAppliesPragmasOnEveryPooledConnection(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	for i := 0; i < s.maxOpen; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		defer c.Close()
		if err := verifyPragmas(ctx, c); err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}
}

func TestOpenRejectsMissingSealerAndPath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "x.db"), nil); !errors.Is(err, ErrSealerMissing) {
		t.Errorf("Open with a nil sealer returned %v, want ErrSealerMissing", err)
	}
	if _, err := Open("  ", testSealer(t)); !errors.Is(err, ErrPathMissing) {
		t.Errorf("Open with an empty path returned %v, want ErrPathMissing", err)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	err := s.Slots().Create(ctx, domain.SlotRow{
		ID: "orphan", NodeID: "does-not-exist", Slot: 1, USBPath: "1-1", IfName: "dg01",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("creating a slot on a missing node returned %v, want ErrInvalid from the foreign key", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("Migrate pass %d: %v", i, err)
		}
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	ms, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if v != ms[len(ms)-1].Version {
		t.Fatalf("schema version is %d, want %d", v, ms[len(ms)-1].Version)
	}
	n, err := s.repoSet.settings.count(ctx, "count applied", `SELECT count(*) FROM schema_migrations`)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != len(ms) {
		t.Fatalf("schema_migrations holds %d rows after three passes, want %d", n, len(ms))
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	sentinel := errors.New("caller changed its mind")
	err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.Slots().Create(ctx, domain.SlotRow{
			ID: "s01", NodeID: "n1", Slot: 1, USBPath: "1-13.1", IfName: "dg01",
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx returned %v, want the callback error", err)
	}
	if _, err := s.Slots().Get(ctx, "s01"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the rolled back slot is still present: %v", err)
	}
}

func TestTxCommits(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.Dongles().Create(ctx, domain.Dongle{ID: "d1", NodeID: "n1", IMEI: "860000000000001"}); err != nil {
			return err
		}
		return tx.Slots().Create(ctx, domain.SlotRow{
			ID: "s01", NodeID: "n1", Slot: 1, USBPath: "1-13.1", IfName: "dg01",
		})
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if _, err := s.Slots().Get(ctx, "s01"); err != nil {
		t.Fatalf("Get slot after commit: %v", err)
	}
	if _, err := s.Dongles().Get(ctx, "d1"); err != nil {
		t.Fatalf("Get dongle after commit: %v", err)
	}
}

func TestBackupWhileOpenPassesIntegrityCheck(t *testing.T) {
	s, clock := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	row := seedSlot(t, s, 1)
	seedProxy(t, s, "p1", row.ID, 1)

	dir := filepath.Join(t.TempDir(), "backups")
	path, err := s.Backup(ctx, dir)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), BackupPrefix) {
		t.Errorf("backup %s is not named with the product prefix", path)
	}
	if err := VerifyBackup(ctx, path); err != nil {
		t.Fatalf("integrity check on the backup: %v", err)
	}

	if _, err := s.Proxies().Get(ctx, "p1"); err != nil {
		t.Fatalf("the live database is unusable after a backup: %v", err)
	}

	restored, err := Open(path, s.sealer, WithClock(clock))
	if err != nil {
		t.Fatalf("open the backup as a store: %v", err)
	}
	defer restored.Close()
	p, err := restored.Proxies().Get(ctx, "p1")
	if err != nil {
		t.Fatalf("read the proxy back out of the backup: %v", err)
	}
	if p.Password != "Kq7mZr2xTn9wLb4V" {
		t.Fatalf("backup holds password %q", p.Password)
	}

	last, err := s.LastBackupAt(ctx)
	if err != nil {
		t.Fatalf("LastBackupAt: %v", err)
	}
	if !last.Equal(baseTime) {
		t.Fatalf("LastBackupAt is %s, want %s", last, baseTime)
	}
}

func TestBackupTwiceInTheSameSecondDoesNotOverwrite(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "backups")

	first, err := s.Backup(ctx, dir)
	if err != nil {
		t.Fatalf("first Backup: %v", err)
	}
	second, err := s.Backup(ctx, dir)
	if err != nil {
		t.Fatalf("second Backup: %v", err)
	}
	if first == second {
		t.Fatalf("two backups in the same second landed on the same path %s", first)
	}
}

func TestLastBackupAtIsZeroBeforeAnyBackup(t *testing.T) {
	s, _ := openStore(t)
	got, err := s.LastBackupAt(context.Background())
	if err != nil {
		t.Fatalf("LastBackupAt: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("LastBackupAt is %s before any backup ran, want the zero time", got)
	}
}

func TestProxyUpdateRewritesEveryField(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	one := seedSlot(t, s, 1)
	two := seedSlot(t, s, 2)
	p := seedProxy(t, s, "p1", one.ID, 1)

	if err := s.Customers().Create(ctx, domain.Customer{ID: "c1", Name: "Acme"}); err != nil {
		t.Fatalf("Create customer: %v", err)
	}
	customer := "c1"
	expires := domain.UnixMillis(baseTime.Add(24 * time.Hour))

	p.SlotID = two.ID
	p.CustomerID = &customer
	p.ExpiresAt = &expires
	p.Suspended = true
	p.SocksPort = domain.Slot(2).SocksPort()
	p.HTTPPort = domain.Slot(2).HTTPPort()
	p.Username = "cust_moved"
	p.Password = "Zz00Yy11Xx22Ww33"
	p.AuthMode = domain.AuthIPList
	p.Policy = domain.ProxyPolicy{AllowedPorts: []domain.PortRange{{Lo: 443, Hi: 443}}, MaxConn: 7, ConnLimit: 3}
	if err := s.Proxies().Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Proxies().Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SlotID != two.ID || !got.Suspended || got.Username != "cust_moved" {
		t.Fatalf("update did not persist: %+v", got)
	}
	if got.Password != "Zz00Yy11Xx22Ww33" {
		t.Fatalf("password is %q", got.Password)
	}
	if got.AuthMode != domain.AuthIPList || got.Policy.MaxConn != 7 {
		t.Fatalf("auth or policy did not persist: %+v", got)
	}
	if got.CustomerID == nil || *got.CustomerID != "c1" || got.ExpiresAt == nil || *got.ExpiresAt != expires {
		t.Fatalf("customer or expiry did not persist: %+v", got)
	}
}

func TestSlotListIsScopedAndOrdered(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)
	seedSlot(t, s, 3)
	seedSlot(t, s, 1)
	seedSlot(t, s, 2)

	rows, err := s.Slots().List(ctx, "n1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List returned %d rows", len(rows))
	}
	for i, r := range rows {
		if r.Slot != domain.Slot(i+1) {
			t.Fatalf("List is not ordered by slot: index %d holds %d", i, int(r.Slot))
		}
	}
	all, err := s.Slots().List(ctx, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("an empty node filter returned %d rows, err %v", len(all), err)
	}
	none, err := s.Slots().List(ctx, "other")
	if err != nil || len(none) != 0 {
		t.Fatalf("an unknown node returned %d rows, err %v", len(none), err)
	}
}

func TestStoreAccessorsAndIntegrityCheck(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	seedNode(t, s)

	if s.Path() == "" || s.DB() == nil || s.Sealer() == nil {
		t.Fatal("the store does not expose its path, handle and sealer")
	}
	if err := s.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck on a live database: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		if tx.Unwrap() == nil {
			t.Error("Tx does not expose the underlying sql.Tx")
		}
		return nil
	}); err != nil {
		t.Fatalf("Tx: %v", err)
	}
}

func TestCloseIsIdempotentAndTxRefusesAfterwards(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "dongled.db"), testSealer(t),
		WithClock(&fixedClock{t: baseTime}), WithMaxOpenConns(2))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.maxOpen != 2 {
		t.Fatalf("WithMaxOpenConns did not apply, pool holds %d", s.maxOpen)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := s.Tx(context.Background(), func(*Tx) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Tx after Close returned %v, want ErrClosed", err)
	}
}

func TestMigrateRefusesADatabaseFromTheFuture(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version,name,applied_at) VALUES(99,'from_the_future',1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.Migrate(ctx); !errors.Is(err, ErrSchemaAhead) {
		t.Fatalf("Migrate against a newer schema returned %v, want ErrSchemaAhead", err)
	}
}
