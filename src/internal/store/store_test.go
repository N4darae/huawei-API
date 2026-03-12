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
