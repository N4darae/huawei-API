CREATE TABLE schema_migrations (
  version    INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  applied_at INTEGER NOT NULL
) STRICT;

CREATE TABLE nodes (
  id          TEXT PRIMARY KEY,
  name        TEXT    NOT NULL,
  kind        TEXT    NOT NULL DEFAULT 'local' CHECK (kind IN ('local')),
  public_host TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_nodes_name ON nodes(name);

CREATE TABLE dongles (
  id                      TEXT PRIMARY KEY,
  node_id                 TEXT    NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  imei                    TEXT    NOT NULL,
  iccid                   TEXT    NOT NULL DEFAULT '',
  imsi                    TEXT    NOT NULL DEFAULT '',
  firmware_ver            TEXT    NOT NULL DEFAULT '',
  hw_ver                  TEXT    NOT NULL DEFAULT '',
  classify                TEXT    NOT NULL DEFAULT 'hilink',
  carrier                 TEXT    NOT NULL DEFAULT '',
  lan_ip_change_supported INTEGER NOT NULL DEFAULT 0 CHECK (lan_ip_change_supported IN (0, 1)),
  hilink_login_required   INTEGER NOT NULL DEFAULT 0 CHECK (hilink_login_required IN (0, 1)),
  data_cap_bytes          INTEGER NOT NULL DEFAULT 0,
  cap_reset_day           INTEGER NOT NULL DEFAULT 1 CHECK (cap_reset_day BETWEEN 1 AND 28),
  auto_recover_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (auto_recover_enabled IN (0, 1)),
  created_at              INTEGER NOT NULL,
  updated_at              INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_dongles_imei ON dongles(imei);
CREATE INDEX ix_dongles_node ON dongles(node_id);

CREATE TABLE slots (
  id         TEXT PRIMARY KEY,
  node_id    TEXT    NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  slot       INTEGER NOT NULL CHECK (slot BETWEEN 1 AND 48),
  usb_path   TEXT    NOT NULL,
  id_path    TEXT    NOT NULL DEFAULT '',
  if_name    TEXT    NOT NULL,
  dongle_id  TEXT    REFERENCES dongles(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_slots_node_usb ON slots(node_id, usb_path);
CREATE UNIQUE INDEX ux_slots_node_slot ON slots(node_id, slot);
CREATE UNIQUE INDEX ux_slots_dongle ON slots(dongle_id) WHERE dongle_id IS NOT NULL;

CREATE TABLE customers (
  id         TEXT PRIMARY KEY,
  name       TEXT    NOT NULL,
  contact    TEXT    NOT NULL DEFAULT '',
  note       TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX ix_customers_name ON customers(name);

CREATE TABLE proxies (
  id              TEXT PRIMARY KEY,
  slot_id         TEXT    NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
  customer_id     TEXT    REFERENCES customers(id) ON DELETE SET NULL,
  enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  suspended       INTEGER NOT NULL DEFAULT 0 CHECK (suspended IN (0, 1)),
  socks_port      INTEGER NOT NULL CHECK (socks_port BETWEEN 1024 AND 32767),
  http_port       INTEGER NOT NULL CHECK (http_port BETWEEN 1024 AND 32767),
  username        TEXT    NOT NULL,
  password_enc    BLOB    NOT NULL,
  auth_mode       TEXT    NOT NULL DEFAULT 'userpass' CHECK (auth_mode IN ('userpass', 'iplist', 'both')),
  allow_all_ports INTEGER NOT NULL DEFAULT 1 CHECK (allow_all_ports IN (0, 1)),
  allowed_ports   TEXT    NOT NULL DEFAULT '',
  max_conn        INTEGER NOT NULL DEFAULT 200,
  conn_limit      INTEGER NOT NULL DEFAULT 30,
  expires_at      INTEGER,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_proxies_slot ON proxies(slot_id);
CREATE UNIQUE INDEX ux_proxies_username ON proxies(username);
CREATE UNIQUE INDEX ux_proxies_socks_port ON proxies(socks_port);
CREATE UNIQUE INDEX ux_proxies_http_port ON proxies(http_port);
CREATE INDEX ix_proxies_customer ON proxies(customer_id);
CREATE INDEX ix_proxies_expires ON proxies(expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE proxy_auth_ips (
  id         TEXT PRIMARY KEY,
  proxy_id   TEXT    NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
  cidr       TEXT    NOT NULL,
  note       TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_proxy_auth_ips ON proxy_auth_ips(proxy_id, cidr);

CREATE TABLE operations (
  id           TEXT PRIMARY KEY,
  kind         TEXT    NOT NULL CHECK (kind IN ('rotate', 'reboot', 'set_auth', 'set_ports', 'set_lan_ip', 'set_net_mode', 'enroll', 'selftest')),
  subject_type TEXT    NOT NULL CHECK (subject_type IN ('proxy', 'dongle', 'slot', 'node')),
  subject_id   TEXT    NOT NULL,
  state        TEXT    NOT NULL CHECK (state IN ('pending', 'running', 'stalled', 'succeeded', 'failed', 'canceled')),
  step         TEXT    NOT NULL DEFAULT '',
  pct          INTEGER NOT NULL DEFAULT 0 CHECK (pct BETWEEN 0 AND 100),
  started_at   INTEGER NOT NULL,
  deadline_at  INTEGER NOT NULL,
  finished_at  INTEGER,
  error        TEXT    NOT NULL DEFAULT '',
  result_json  TEXT    NOT NULL DEFAULT '',
  trigger_kind TEXT    NOT NULL CHECK (trigger_kind IN ('admin_ui', 'customer_api', 'auto_recovery')),
  actor_type   TEXT    NOT NULL DEFAULT '' CHECK (actor_type IN ('', 'admin', 'api_key', 'system')),
  actor_id     TEXT    NOT NULL DEFAULT '',
  request_id   TEXT    NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_operations_active ON operations(subject_type, subject_id) WHERE finished_at IS NULL;
CREATE INDEX ix_operations_subject ON operations(subject_type, subject_id, started_at DESC);
CREATE INDEX ix_operations_started ON operations(started_at DESC);

CREATE TABLE rotations (
  id            TEXT PRIMARY KEY,
  operation_id  TEXT    NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  proxy_id      TEXT    NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
  requested_at  INTEGER NOT NULL,
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  old_public_ip TEXT    NOT NULL DEFAULT '',
  new_public_ip TEXT    NOT NULL DEFAULT '',
  ip_changed    INTEGER NOT NULL DEFAULT 0 CHECK (ip_changed IN (0, 1)),
  result        TEXT    NOT NULL CHECK (result IN ('changed', 'unchanged', 'failed')),
  trigger_kind  TEXT    NOT NULL CHECK (trigger_kind IN ('admin_ui', 'customer_api', 'auto_recovery')),
  request_id    TEXT    NOT NULL DEFAULT '',
  hold_ms       INTEGER NOT NULL DEFAULT 0,
  attempts      INTEGER NOT NULL DEFAULT 0,
  error         TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX ix_rotations_proxy ON rotations(proxy_id, requested_at DESC);
CREATE UNIQUE INDEX ux_rotations_operation ON rotations(operation_id);

CREATE TABLE sms (
  id          TEXT PRIMARY KEY,
  dongle_id   TEXT    NOT NULL REFERENCES dongles(id) ON DELETE CASCADE,
  idx         INTEGER NOT NULL,
  box         INTEGER NOT NULL,
  phone       TEXT    NOT NULL DEFAULT '',
  content     TEXT    NOT NULL DEFAULT '',
  sent_at     INTEGER NOT NULL,
  is_read     INTEGER NOT NULL DEFAULT 0 CHECK (is_read IN (0, 1)),
  sms_type    INTEGER NOT NULL DEFAULT 0,
  is_fragment INTEGER NOT NULL DEFAULT 0 CHECK (is_fragment IN (0, 1)),
  created_at  INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX ux_sms_dongle_idx ON sms(dongle_id, box, idx);
CREATE INDEX ix_sms_dongle_date ON sms(dongle_id, sent_at DESC);

CREATE TABLE usage_daily (
  dongle_id  TEXT    NOT NULL REFERENCES dongles(id) ON DELETE CASCADE,
  day        TEXT    NOT NULL,
  up_bytes   INTEGER NOT NULL DEFAULT 0,
  down_bytes INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (dongle_id, day)
) STRICT;

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
