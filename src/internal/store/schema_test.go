package store

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openMemory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func applyAll(t *testing.T, db *sql.DB) {
	t.Helper()
	ms, err := Migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("P0 owns exactly one migration, found %d", len(ms))
	}
	for _, m := range ms {
		if _, err := db.Exec(m.SQL); err != nil {
			t.Fatalf("migration %04d_%s: %v", m.Version, m.Name, err)
		}
	}
}

func TestInitMigrationApplies(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	want := []string{
		"schema_migrations", "nodes", "dongles", "slots", "customers",
		"proxies", "proxy_auth_ips", "operations", "rotations", "sms",
		"usage_daily", "settings",
	}
	for _, table := range want {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatalf("lookup %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s missing from 0001_init.sql", table)
		}
	}
}

func TestEveryTableIsStrict(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	rows, err := db.Query(`SELECT name, sql FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query schema: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if !strings.Contains(strings.ToUpper(ddl), "STRICT") {
			t.Errorf("table %s is not STRICT", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if seen == 0 {
		t.Fatal("no tables inspected")
	}
}

func TestNoPostgresTypesInSchema(t *testing.T) {
	ms, err := Migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	banned := []string{"TIMESTAMPTZ", "TIMESTAMP", "INET", "SMALLINT", "BIGINT", "SERIAL", "BOOLEAN", "JSONB", "UUID", "VARCHAR", "NOW()"}
	for _, m := range ms {
		upper := strings.ToUpper(m.SQL)
		for _, b := range banned {
			if strings.Contains(upper, b) {
				t.Errorf("migration %04d_%s uses PostgreSQL type %s; SQLite STRICT accepts only INT INTEGER REAL TEXT BLOB ANY", m.Version, m.Name, b)
			}
		}
	}
}

func TestTimeColumnsAreIntegerMillis(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	timeCol := regexp.MustCompile(`(_at|_ms)$`)
	tables := []string{}
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables = append(tables, n)
	}
	rows.Close()

	for _, table := range tables {
		cols, err := db.Query(`SELECT name, type FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatalf("table_info %s: %v", table, err)
		}
		for cols.Next() {
			var name, typ string
			if err := cols.Scan(&name, &typ); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if timeCol.MatchString(name) && strings.ToUpper(typ) != "INTEGER" {
				t.Errorf("%s.%s is %s, must be INTEGER unix-millis", table, name, typ)
			}
		}
		cols.Close()
	}
}

func TestSlotAndDongleAreSeparateTables(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	now := int64(1700000000000)
	mustExec(t, db, `INSERT INTO nodes(id,name,kind,public_host,created_at,updated_at) VALUES('n1','local','local','139.99.68.39',?,?)`, now, now)
	mustExec(t, db, `INSERT INTO dongles(id,node_id,imei,created_at,updated_at) VALUES('d1','n1','860000000000001',?,?)`, now, now)
	mustExec(t, db, `INSERT INTO slots(id,node_id,slot,usb_path,if_name,dongle_id,created_at,updated_at) VALUES('s1','n1',1,'1-13.1','dg01','d1',?,?)`, now, now)

	if _, err := db.Exec(`INSERT INTO dongles(id,node_id,imei,created_at,updated_at) VALUES('d2','n1','860000000000001',?,?)`, now, now); err == nil {
		t.Error("UNIQUE(imei) on dongles is missing")
	}
	if _, err := db.Exec(`INSERT INTO slots(id,node_id,slot,usb_path,if_name,created_at,updated_at) VALUES('s2','n1',2,'1-13.1','dg02',?,?)`, now, now); err == nil {
		t.Error("UNIQUE(node_id, usb_path) on slots is missing")
	}

	mustExec(t, db, `UPDATE slots SET dongle_id=NULL WHERE id='s1'`)
	var null bool
	if err := db.QueryRow(`SELECT dongle_id IS NULL FROM slots WHERE id='s1'`).Scan(&null); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !null {
		t.Error("slots.dongle_id must be nullable so a dead stick can be swapped")
	}
}

func TestProxyBindsToSlotNotDongle(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('proxies') WHERE name='dongle_id'`).Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 0 {
		t.Fatal("proxies.dongle_id must not exist; the dongle relationship goes through the slot")
	}
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('proxies') WHERE name IN ('slot_id','customer_id','expires_at')`).Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 3 {
		t.Fatalf("proxies is missing slot_id/customer_id/expires_at, found %d of 3", n)
	}
}

func TestOneActiveOperationPerSubject(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	now := int64(1700000000000)
	ins := `INSERT INTO operations(id,kind,subject_type,subject_id,state,started_at,deadline_at,trigger_kind,created_at,updated_at)
	        VALUES(?,'rotate','proxy','p1','running',?,?,'admin_ui',?,?)`
	mustExec(t, db, ins, "op1", now, now+90000, now, now)
	if _, err := db.Exec(ins, "op2", now, now+90000, now, now); err == nil {
		t.Fatal("a second unfinished operation on the same subject must violate ux_operations_active")
	}
	mustExec(t, db, `UPDATE operations SET state='succeeded', finished_at=? WHERE id='op1'`, now+1000)
	mustExec(t, db, ins, "op3", now, now+90000, now, now)
}

func TestForbiddenColumnsAreAbsent(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	banned := map[string][]string{
		"dongles":  {"fw_mark", "fwmark", "netns_fallback", "agent_url", "agent_token_hash", "drain_timeout_ms", "slot_id"},
		"proxies":  {"dongle_id", "totp_secret_enc"},
		"nodes":    {"agent_url", "agent_token_hash"},
		"settings": {"totp_secret_enc"},
	}
	for table, cols := range banned {
		for _, c := range cols {
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, table, c).Scan(&n); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if n != 0 {
				t.Errorf("%s.%s was cut from the schema", table, c)
			}
		}
	}
}

func TestTriggerEnumRejectsSchedule(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	now := int64(1700000000000)
	_, err := db.Exec(`INSERT INTO operations(id,kind,subject_type,subject_id,state,started_at,deadline_at,trigger_kind,created_at,updated_at)
	                   VALUES('op1','rotate','proxy','p1','running',?,?,'schedule',?,?)`, now, now+1, now, now)
	if err == nil {
		t.Fatal("trigger_kind must reject 'schedule'")
	}
}

func TestProxyAuthIPsCascade(t *testing.T) {
	db := openMemory(t)
	applyAll(t, db)

	now := int64(1700000000000)
	mustExec(t, db, `INSERT INTO nodes(id,name,kind,public_host,created_at,updated_at) VALUES('n1','local','local','139.99.68.39',?,?)`, now, now)
	mustExec(t, db, `INSERT INTO slots(id,node_id,slot,usb_path,if_name,created_at,updated_at) VALUES('s1','n1',1,'1-13.1','dg01',?,?)`, now, now)
	mustExec(t, db, `INSERT INTO proxies(id,slot_id,socks_port,http_port,username,password_enc,created_at,updated_at) VALUES('p1','s1',21001,22001,'cust_ab12cd34',x'00',?,?)`, now, now)
	mustExec(t, db, `INSERT INTO proxy_auth_ips(id,proxy_id,cidr,created_at) VALUES('a1','p1','203.0.113.5/32',?)`, now)

	if _, err := db.Exec(`INSERT INTO proxy_auth_ips(id,proxy_id,cidr,created_at) VALUES('a2','p1','203.0.113.5/32',?)`, now); err == nil {
		t.Error("ux_proxy_auth_ips must reject a duplicate cidr for the same proxy")
	}
	mustExec(t, db, `DELETE FROM proxies WHERE id='p1'`)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM proxy_auth_ips`).Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 0 {
		t.Errorf("proxy_auth_ips did not cascade, %d rows left", n)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
