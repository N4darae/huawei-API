package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS auth_users (
  username      TEXT PRIMARY KEY,
  password_hash TEXT    NOT NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS auth_sessions (
  id           TEXT PRIMARY KEY,
  username     TEXT    NOT NULL,
  csrf_token   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS ix_auth_sessions_expires ON auth_sessions(expires_at);

CREATE TABLE IF NOT EXISTS auth_lockout (
  subject      TEXT PRIMARY KEY,
  failures     INTEGER NOT NULL DEFAULT 0,
  first_at     INTEGER NOT NULL,
  locked_until INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE IF NOT EXISTS api_keys (
  id           TEXT PRIMARY KEY,
  name         TEXT    NOT NULL,
  prefix       TEXT    NOT NULL,
  secret_hash  TEXT    NOT NULL,
  customer_id  TEXT,
  scopes       TEXT    NOT NULL DEFAULT '',
  proxy_ids    TEXT    NOT NULL DEFAULT '',
  last_used_at INTEGER,
  revoked_at   INTEGER,
  created_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS ix_api_keys_prefix ON api_keys(prefix);

CREATE TABLE IF NOT EXISTS link_tokens (
  id           TEXT PRIMARY KEY,
  api_key_id   TEXT    NOT NULL,
  token_hash   TEXT    NOT NULL,
  last_used_at INTEGER,
  revoked_at   INTEGER,
  created_at   INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX IF NOT EXISTS ux_link_tokens_hash ON link_tokens(token_hash);
CREATE INDEX IF NOT EXISTS ix_link_tokens_key ON link_tokens(api_key_id);
`

func EnsureSchema(ctx context.Context, db DB) error {
	if db == nil {
		return ErrNoDB
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("auth: install schema: %w", err)
	}
	return nil
}

var (
	ErrNoDB           = errors.New("auth: a database handle is required")
	ErrBadCredentials = errors.New("auth: username or password is wrong")
	ErrNoSession      = errors.New("auth: no valid session")
	ErrSessionExpired = errors.New("auth: session has expired")
	ErrBadCSRF        = errors.New("auth: csrf token does not match the session")
	ErrLockedOut      = errors.New("auth: too many failed attempts")
	ErrBadKey         = errors.New("auth: api key is not valid")
	ErrKeyRevoked     = errors.New("auth: api key has been revoked")
	ErrScopeMissing   = errors.New("auth: api key does not carry the required scope")
	ErrProxyNotOnKey  = errors.New("auth: api key is not allowed on this proxy")
	ErrTokenRevoked   = errors.New("auth: link token has been revoked")
	ErrWeakPassword   = errors.New("auth: password is too short")
)

const MinPasswordLen = 8

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("auth: crypto/rand is unavailable: %w", err)
	}
	return b, nil
}

func randomToken(n int) (string, error) {
	b, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomID(prefix string) (string, error) {
	b, err := randomBytes(10)
	if err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}

func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func joinList(in []string) string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ",")
}

func splitList(in string) []string {
	if strings.TrimSpace(in) == "" {
		return nil
	}
	parts := strings.Split(in, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullText(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func intPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func textPtr(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	s := v.String
	return &s
}

func notFound(what, id string) error {
	return fmt.Errorf("auth: %s %q: %w", what, id, domain.ErrNotFound)
}
