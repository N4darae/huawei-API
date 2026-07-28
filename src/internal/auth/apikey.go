package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	KeyPrefix     = config.APIKeyPrefix
	KeySecretLen  = 32
	KeyLabelRunes = 8
	LinkTokenLen  = 24
)

const (
	ScopeRotate = "rotate"
	ScopeStatus = "status"
)

func AllScopes() []string { return []string{ScopeRotate, ScopeStatus} }

func ValidScope(s string) bool {
	for _, v := range AllScopes() {
		if v == s {
			return true
		}
	}
	return false
}

type Key struct {
	ID         string
	Name       string
	Prefix     string
	CustomerID *string
	Scopes     []string
	ProxyIDs   []string
	LastUsedAt *int64
	RevokedAt  *int64
	CreatedAt  int64
	LinkTokens []LinkToken
}

func (k Key) Revoked() bool { return k.RevokedAt != nil }

func (k Key) HasScope(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func (k Key) CoversProxy(proxyID string) bool {
	if len(k.ProxyIDs) == 0 {
		return true
	}
	for _, p := range k.ProxyIDs {
		if p == proxyID {
			return true
		}
	}
	return false
}

type LinkToken struct {
	ID         string
	APIKeyID   string
	LastUsedAt *int64
	RevokedAt  *int64
	CreatedAt  int64
}

func (t LinkToken) Revoked() bool { return t.RevokedAt != nil }

type NewKey struct {
	Name       string
	CustomerID *string
	Scopes     []string
	ProxyIDs   []string
}

type Keys struct {
	db     DB
	now    func() time.Time
	params Params
}

func NewKeys(db DB, now func() time.Time) *Keys {
	if now == nil {
		now = time.Now
	}
	return &Keys{db: db, now: now, params: KeyParams()}
}

func (k *Keys) nowMS() int64 { return domain.UnixMillis(k.now()) }

func (k *Keys) Create(ctx context.Context, req NewKey) (Key, string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Key{}, "", fmt.Errorf("%w: an api key needs a name", domain.ErrInvalid)
	}
	scopes := make([]string, 0, len(req.Scopes))
	for _, s := range req.Scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !ValidScope(s) {
			return Key{}, "", fmt.Errorf("%w: scope %q, known scopes are %s", domain.ErrInvalid, s, strings.Join(AllScopes(), ", "))
		}
		if !containsString(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	if len(scopes) == 0 {
		return Key{}, "", fmt.Errorf("%w: an api key needs at least one scope", domain.ErrInvalid)
	}

	body, err := randomToken(KeySecretLen)
	if err != nil {
		return Key{}, "", err
	}
	secret := KeyPrefix + body
	label := body
	if len(label) > KeyLabelRunes {
		label = label[:KeyLabelRunes]
	}

	hash, err := Hash(secret, k.params)
	if err != nil {
		return Key{}, "", err
	}
	id, err := randomID("key")
	if err != nil {
		return Key{}, "", err
	}

	out := Key{
		ID:         id,
		Name:       name,
		Prefix:     KeyPrefix + label,
		CustomerID: req.CustomerID,
		Scopes:     scopes,
		ProxyIDs:   req.ProxyIDs,
		CreatedAt:  k.nowMS(),
	}
	_, err = k.db.ExecContext(ctx,
		`INSERT INTO api_keys(id, name, prefix, secret_hash, customer_id, scopes, proxy_ids, last_used_at, revoked_at, created_at)
		 VALUES(?,?,?,?,?,?,?,NULL,NULL,?)`,
		out.ID, out.Name, out.Prefix, hash, nullText(out.CustomerID),
		joinList(out.Scopes), joinList(out.ProxyIDs), out.CreatedAt)
	if err != nil {
		return Key{}, "", fmt.Errorf("auth: store api key: %w", err)
	}
	return out, secret, nil
}

const keyColumns = `id, name, prefix, customer_id, scopes, proxy_ids, last_used_at, revoked_at, created_at`

func scanKey(sc interface{ Scan(...any) error }) (Key, error) {
	var (
		out      Key
		customer sql.NullString
		scopes   string
		proxies  string
		lastUsed sql.NullInt64
		revoked  sql.NullInt64
	)
	if err := sc.Scan(&out.ID, &out.Name, &out.Prefix, &customer, &scopes, &proxies, &lastUsed, &revoked, &out.CreatedAt); err != nil {
		return Key{}, err
	}
	out.CustomerID = textPtr(customer)
	out.Scopes = splitList(scopes)
	out.ProxyIDs = splitList(proxies)
	out.LastUsedAt = intPtr(lastUsed)
	out.RevokedAt = intPtr(revoked)
	return out, nil
}

func (k *Keys) List(ctx context.Context) ([]Key, error) {
	out, err := k.readKeys(ctx)
	if err != nil {
		return nil, err
	}
	tokens, err := k.listTokens(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].LinkTokens = tokens[out[i].ID]
	}
	return out, nil
}

func (k *Keys) readKeys(ctx context.Context) ([]Key, error) {
	rows, err := k.db.QueryContext(ctx, `SELECT `+keyColumns+` FROM api_keys ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("auth: list api keys: %w", err)
	}
	defer rows.Close()

	out := []Key{}
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: scan api key: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list api keys: %w", err)
	}
	return out, nil
}

func (k *Keys) Get(ctx context.Context, id string) (Key, error) {
	row := k.db.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE id = ?`, id)
	key, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, notFound("api key", id)
	}
	if err != nil {
		return Key{}, fmt.Errorf("auth: read api key %q: %w", id, err)
	}
	tokens, err := k.tokensFor(ctx, id)
	if err != nil {
		return Key{}, err
	}
	key.LinkTokens = tokens
	return key, nil
}

func (k *Keys) Revoke(ctx context.Context, id string) error {
	res, err := k.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, k.nowMS(), id)
	if err != nil {
		return fmt.Errorf("auth: revoke api key %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		var exists int
		if err := k.db.QueryRowContext(ctx, `SELECT count(*) FROM api_keys WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("auth: revoke api key %q: %w", id, err)
		}
		if exists == 0 {
			return notFound("api key", id)
		}
	}
	return nil
}

func (k *Keys) Authenticate(ctx context.Context, secret string) (Key, error) {
	secret = strings.TrimSpace(secret)
	if !strings.HasPrefix(secret, KeyPrefix) || len(secret) <= len(KeyPrefix) {
		return Key{}, ErrBadKey
	}
	body := strings.TrimPrefix(secret, KeyPrefix)
	label := body
	if len(label) > KeyLabelRunes {
		label = label[:KeyLabelRunes]
	}

	candidates, err := k.candidates(ctx, KeyPrefix+label)
	if err != nil {
		return Key{}, err
	}
	for _, c := range candidates {
		if err := Verify(secret, c.hash); err != nil {
			continue
		}
		if c.key.Revoked() {
			return Key{}, ErrKeyRevoked
		}
		k.touchKey(ctx, c.key.ID)
		return c.key, nil
	}
	return Key{}, ErrBadKey
}

type keyCandidate struct {
	key  Key
	hash string
}

func (k *Keys) candidates(ctx context.Context, prefix string) ([]keyCandidate, error) {
	rows, err := k.db.QueryContext(ctx,
		`SELECT `+keyColumns+`, secret_hash FROM api_keys WHERE prefix = ?`, prefix)
	if err != nil {
		return nil, fmt.Errorf("auth: look up api key: %w", err)
	}
	defer rows.Close()

	out := []keyCandidate{}
	for rows.Next() {
		var (
			key      Key
			customer sql.NullString
			scopes   string
			proxies  string
			lastUsed sql.NullInt64
			revoked  sql.NullInt64
			hash     string
		)
		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &customer, &scopes, &proxies, &lastUsed, &revoked, &key.CreatedAt, &hash); err != nil {
			return nil, fmt.Errorf("auth: scan api key: %w", err)
		}
		key.CustomerID = textPtr(customer)
		key.Scopes = splitList(scopes)
		key.ProxyIDs = splitList(proxies)
		key.LastUsedAt = intPtr(lastUsed)
		key.RevokedAt = intPtr(revoked)
		out = append(out, keyCandidate{key: key, hash: hash})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: look up api key: %w", err)
	}
	return out, nil
}

func (k *Keys) touchKey(ctx context.Context, id string) {
	_, _ = k.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, k.nowMS(), id)
}

func (k *Keys) CreateLinkToken(ctx context.Context, keyID string) (LinkToken, string, error) {
	if _, err := k.Get(ctx, keyID); err != nil {
		return LinkToken{}, "", err
	}
	secret, err := randomToken(LinkTokenLen)
	if err != nil {
		return LinkToken{}, "", err
	}
	id, err := randomID("lt")
	if err != nil {
		return LinkToken{}, "", err
	}

	out := LinkToken{ID: id, APIKeyID: keyID, CreatedAt: k.nowMS()}
	_, err = k.db.ExecContext(ctx,
		`INSERT INTO link_tokens(id, api_key_id, token_hash, last_used_at, revoked_at, created_at) VALUES(?,?,?,NULL,NULL,?)`,
		out.ID, out.APIKeyID, digest(secret), out.CreatedAt)
	if err != nil {
		return LinkToken{}, "", fmt.Errorf("auth: store link token: %w", err)
	}
	return out, secret, nil
}

func (k *Keys) RevokeLinkToken(ctx context.Context, tokenID string) error {
	res, err := k.db.ExecContext(ctx,
		`UPDATE link_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, k.nowMS(), tokenID)
	if err != nil {
		return fmt.Errorf("auth: revoke link token %q: %w", tokenID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		var exists int
		if err := k.db.QueryRowContext(ctx, `SELECT count(*) FROM link_tokens WHERE id = ?`, tokenID).Scan(&exists); err != nil {
			return fmt.Errorf("auth: revoke link token %q: %w", tokenID, err)
		}
		if exists == 0 {
			return notFound("link token", tokenID)
		}
	}
	return nil
}

func (k *Keys) AuthenticateLink(ctx context.Context, secret string) (LinkToken, Key, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return LinkToken{}, Key{}, ErrBadKey
	}

	var (
		tok      LinkToken
		lastUsed sql.NullInt64
		revoked  sql.NullInt64
	)
	err := k.db.QueryRowContext(ctx,
		`SELECT id, api_key_id, last_used_at, revoked_at, created_at FROM link_tokens WHERE token_hash = ?`, digest(secret)).
		Scan(&tok.ID, &tok.APIKeyID, &lastUsed, &revoked, &tok.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkToken{}, Key{}, ErrBadKey
	}
	if err != nil {
		return LinkToken{}, Key{}, fmt.Errorf("auth: look up link token: %w", err)
	}
	tok.LastUsedAt = intPtr(lastUsed)
	tok.RevokedAt = intPtr(revoked)
	if tok.Revoked() {
		return LinkToken{}, Key{}, ErrTokenRevoked
	}

	key, err := k.Get(ctx, tok.APIKeyID)
	if err != nil {
		return LinkToken{}, Key{}, err
	}
	if key.Revoked() {
		return LinkToken{}, Key{}, ErrKeyRevoked
	}
	return tok, key, nil
}

func (k *Keys) TouchLinkToken(ctx context.Context, tokenID string) {
	_, _ = k.db.ExecContext(ctx, `UPDATE link_tokens SET last_used_at = ? WHERE id = ?`, k.nowMS(), tokenID)
}

func (k *Keys) tokensFor(ctx context.Context, keyID string) ([]LinkToken, error) {
	rows, err := k.db.QueryContext(ctx,
		`SELECT id, api_key_id, last_used_at, revoked_at, created_at FROM link_tokens WHERE api_key_id = ? ORDER BY created_at DESC, id`, keyID)
	if err != nil {
		return nil, fmt.Errorf("auth: list link tokens: %w", err)
	}
	defer rows.Close()
	return scanTokens(rows)
}

func (k *Keys) listTokens(ctx context.Context) (map[string][]LinkToken, error) {
	rows, err := k.db.QueryContext(ctx,
		`SELECT id, api_key_id, last_used_at, revoked_at, created_at FROM link_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("auth: list link tokens: %w", err)
	}
	defer rows.Close()

	all, err := scanTokens(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]LinkToken{}
	for _, t := range all {
		out[t.APIKeyID] = append(out[t.APIKeyID], t)
	}
	return out, nil
}

func scanTokens(rows *sql.Rows) ([]LinkToken, error) {
	out := []LinkToken{}
	for rows.Next() {
		var (
			t        LinkToken
			lastUsed sql.NullInt64
			revoked  sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.APIKeyID, &lastUsed, &revoked, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth: scan link token: %w", err)
		}
		t.LastUsedAt = intPtr(lastUsed)
		t.RevokedAt = intPtr(revoked)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list link tokens: %w", err)
	}
	return out, nil
}

func containsString(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}
