package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/secrets"
)

type proxyRepo struct {
	base
	sealer secrets.Sealer
}

const proxyCols = `id, slot_id, customer_id, enabled, suspended, socks_port, http_port, username,
	password_enc, auth_mode, allow_all_ports, allowed_ports, max_conn, conn_limit, expires_at,
	created_at, updated_at`

const authIPCols = `id, proxy_id, cidr, note, created_at`

func (r *proxyRepo) Get(ctx context.Context, id string) (domain.Proxy, error) {
	return r.one(ctx, "proxy", id, `SELECT `+proxyCols+` FROM proxies WHERE id = ?`, id)
}

func (r *proxyRepo) GetBySlot(ctx context.Context, slotID string) (domain.Proxy, error) {
	return r.one(ctx, "proxy for slot", slotID, `SELECT `+proxyCols+` FROM proxies WHERE slot_id = ?`, slotID)
}

func (r *proxyRepo) GetByUsername(ctx context.Context, username string) (domain.Proxy, error) {
	return r.one(ctx, "proxy username", username, `SELECT `+proxyCols+` FROM proxies WHERE username = ?`, username)
}

func (r *proxyRepo) one(ctx context.Context, what, key, query string, args ...any) (domain.Proxy, error) {
	row := r.q.QueryRowContext(ctx, query, args...)
	p, err := r.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Proxy{}, notFound(what, key)
	}
	if err != nil {
		return domain.Proxy{}, mapErr(err, "get "+what)
	}
	return r.withAuthIPs(ctx, p)
}

func (r *proxyRepo) List(ctx context.Context, f ProxyFilter) ([]domain.Proxy, error) {
	where := []string{}
	args := []any{}
	if f.CustomerID != "" {
		where = append(where, "customer_id = ?")
		args = append(args, f.CustomerID)
	}
	if f.SlotID != "" {
		where = append(where, "slot_id = ?")
		args = append(args, f.SlotID)
	}
	if f.Enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, boolInt(*f.Enabled))
	}
	if f.Suspended != nil {
		where = append(where, "suspended = ?")
		args = append(args, boolInt(*f.Suspended))
	}
	if f.ExpiringBeforeMS > 0 {
		where = append(where, "expires_at IS NOT NULL AND expires_at <= ?")
		args = append(args, f.ExpiringBeforeMS)
	}

	q := `SELECT ` + proxyCols + ` FROM proxies`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY socks_port" + limitClause(f.Limit)

	return r.many(ctx, "list proxies", q, withLimit(args, f.Limit)...)
}

func (r *proxyRepo) ListExpired(ctx context.Context, nowMS int64) ([]domain.Proxy, error) {
	return r.many(ctx, "list expired proxies",
		`SELECT `+proxyCols+` FROM proxies
		 WHERE expires_at IS NOT NULL AND expires_at > 0 AND expires_at <= ?
		 ORDER BY socks_port`, nowMS)
}

func (r *proxyRepo) many(ctx context.Context, what, query string, args ...any) ([]domain.Proxy, error) {
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err, what)
	}
	defer rows.Close()

	out := []domain.Proxy{}
	for rows.Next() {
		p, err := r.scan(rows)
		if err != nil {
			return nil, mapErr(err, what)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err, what)
	}
	for i := range out {
		p, err := r.withAuthIPs(ctx, out[i])
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

func (r *proxyRepo) Create(ctx context.Context, p domain.Proxy) error {
	if p.ID == "" || p.SlotID == "" || p.Username == "" {
		return errInvalid("proxy id, slot_id and username are required")
	}
	p = withProxyDefaults(p)
	if err := p.Policy.Validate(); err != nil {
		return err
	}
	if !p.AuthMode.Valid() {
		return errInvalid("auth mode %q is not one of userpass, iplist, both", string(p.AuthMode))
	}
	enc, err := r.seal(p.Password)
	if err != nil {
		return err
	}
	created, updated := r.stamps(p.CreatedAt)
	return r.exec(ctx, "create proxy",
		`INSERT INTO proxies(`+proxyCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.SlotID, nullText(p.CustomerID), boolInt(p.Enabled), boolInt(p.Suspended),
		p.SocksPort, p.HTTPPort, p.Username, enc, string(p.AuthMode),
		boolInt(p.Policy.AllowAllPorts), domain.FormatPortRanges(p.Policy.AllowedPorts),
		p.Policy.MaxConn, p.Policy.ConnLimit, nullInt(p.ExpiresAt), created, updated)
}

func (r *proxyRepo) Update(ctx context.Context, p domain.Proxy) error {
	if p.ID == "" {
		return errInvalid("proxy id is required")
	}
	p = withProxyDefaults(p)
	if err := p.Policy.Validate(); err != nil {
		return err
	}
	if !p.AuthMode.Valid() {
		return errInvalid("auth mode %q is not one of userpass, iplist, both", string(p.AuthMode))
	}
	enc, err := r.seal(p.Password)
	if err != nil {
		return err
	}
	return r.execAffecting(ctx, "update proxy", p.ID,
		`UPDATE proxies SET slot_id=?, customer_id=?, enabled=?, suspended=?, socks_port=?, http_port=?,
		 username=?, password_enc=?, auth_mode=?, allow_all_ports=?, allowed_ports=?, max_conn=?,
		 conn_limit=?, expires_at=?, updated_at=? WHERE id=?`,
		p.SlotID, nullText(p.CustomerID), boolInt(p.Enabled), boolInt(p.Suspended),
		p.SocksPort, p.HTTPPort, p.Username, enc, string(p.AuthMode),
		boolInt(p.Policy.AllowAllPorts), domain.FormatPortRanges(p.Policy.AllowedPorts),
		p.Policy.MaxConn, p.Policy.ConnLimit, nullInt(p.ExpiresAt), r.now(), p.ID)
}

func (r *proxyRepo) Delete(ctx context.Context, id string) error {
	return r.execAffecting(ctx, "delete proxy", id, `DELETE FROM proxies WHERE id = ?`, id)
}

func (r *proxyRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return r.execAffecting(ctx, "set proxy enabled", id,
		`UPDATE proxies SET enabled=?, updated_at=? WHERE id=?`, boolInt(enabled), r.now(), id)
}

func (r *proxyRepo) SetSuspended(ctx context.Context, id string, suspended bool) error {
	return r.execAffecting(ctx, "set proxy suspended", id,
		`UPDATE proxies SET suspended=?, updated_at=? WHERE id=?`, boolInt(suspended), r.now(), id)
}

func (r *proxyRepo) SetCredentials(ctx context.Context, id, username, password string) error {
	if username == "" {
		return errInvalid("proxy username is required")
	}
	enc, err := r.seal(password)
	if err != nil {
		return err
	}
	return r.execAffecting(ctx, "set proxy credentials", id,
		`UPDATE proxies SET username=?, password_enc=?, updated_at=? WHERE id=?`,
		username, enc, r.now(), id)
}

func (r *proxyRepo) SetAuthMode(ctx context.Context, id string, mode domain.AuthMode) error {
	if !mode.Valid() {
		return errInvalid("auth mode %q is not one of userpass, iplist, both", string(mode))
	}
	return r.execAffecting(ctx, "set proxy auth mode", id,
		`UPDATE proxies SET auth_mode=?, updated_at=? WHERE id=?`, string(mode), r.now(), id)
}

func (r *proxyRepo) SetPolicy(ctx context.Context, id string, policy domain.ProxyPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	return r.execAffecting(ctx, "set proxy policy", id,
		`UPDATE proxies SET allow_all_ports=?, allowed_ports=?, max_conn=?, conn_limit=?, updated_at=? WHERE id=?`,
		boolInt(policy.AllowAllPorts), domain.FormatPortRanges(policy.AllowedPorts),
		policy.MaxConn, policy.ConnLimit, r.now(), id)
}

func (r *proxyRepo) SetCustomer(ctx context.Context, id string, customerID *string, expiresAt *int64) error {
	return r.execAffecting(ctx, "set proxy customer", id,
		`UPDATE proxies SET customer_id=?, expires_at=?, updated_at=? WHERE id=?`,
		nullText(customerID), nullInt(expiresAt), r.now(), id)
}

func (r *proxyRepo) ListAuthIPs(ctx context.Context, proxyID string) ([]domain.ProxyAuthIP, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+authIPCols+` FROM proxy_auth_ips WHERE proxy_id = ? ORDER BY cidr`, proxyID)
	if err != nil {
		return nil, mapErr(err, "list proxy auth ips")
	}
	defer rows.Close()

	out := []domain.ProxyAuthIP{}
	for rows.Next() {
		var (
			a    domain.ProxyAuthIP
			cidr string
		)
		if err := rows.Scan(&a.ID, &a.ProxyID, &cidr, &a.Note, &a.CreatedAt); err != nil {
			return nil, mapErr(err, "scan proxy auth ip")
		}
		p, err := parsePrefix("proxy auth ip", cidr)
		if err != nil {
			return nil, err
		}
		a.CIDR = p
		out = append(out, a)
	}
	return out, mapErr(rows.Err(), "list proxy auth ips")
}

func (r *proxyRepo) AddAuthIP(ctx context.Context, a domain.ProxyAuthIP) error {
	if a.ID == "" || a.ProxyID == "" {
		return errInvalid("proxy auth ip id and proxy_id are required")
	}
	if !a.CIDR.IsValid() {
		return errInvalid("proxy auth ip cidr is not a valid prefix")
	}
	created, _ := r.stamps(a.CreatedAt)
	return r.exec(ctx, "add proxy auth ip",
		`INSERT INTO proxy_auth_ips(`+authIPCols+`) VALUES(?,?,?,?,?)`,
		a.ID, a.ProxyID, a.CIDR.Masked().String(), a.Note, created)
}

func (r *proxyRepo) DeleteAuthIP(ctx context.Context, proxyID string, cidr netip.Prefix) error {
	return r.execAffecting(ctx, "delete proxy auth ip", proxyID+" "+cidr.String(),
		`DELETE FROM proxy_auth_ips WHERE proxy_id = ? AND cidr = ?`, proxyID, cidr.Masked().String())
}

func (r *proxyRepo) seal(password string) ([]byte, error) {
	if r.sealer == nil {
		return nil, ErrSealerMissing
	}
	enc, err := r.sealer.Seal([]byte(password))
	if err != nil {
		return nil, fmt.Errorf("store: sealing proxy password: %w", err)
	}
	return enc, nil
}

func (r *proxyRepo) withAuthIPs(ctx context.Context, p domain.Proxy) (domain.Proxy, error) {
	ips, err := r.ListAuthIPs(ctx, p.ID)
	if err != nil {
		return domain.Proxy{}, err
	}
	p.AuthIPs = make([]netip.Prefix, 0, len(ips))
	for _, a := range ips {
		p.AuthIPs = append(p.AuthIPs, a.CIDR)
	}
	domain.SortPrefixes(p.AuthIPs)
	return p, nil
}

func (r *proxyRepo) scan(s scanner) (domain.Proxy, error) {
	var (
		p           domain.Proxy
		customer    sql.NullString
		expires     sql.NullInt64
		enc         []byte
		mode        string
		allowAll    int
		allowedText string
		enabled     int
		suspended   int
	)
	err := s.Scan(&p.ID, &p.SlotID, &customer, &enabled, &suspended, &p.SocksPort, &p.HTTPPort,
		&p.Username, &enc, &mode, &allowAll, &allowedText, &p.Policy.MaxConn, &p.Policy.ConnLimit,
		&expires, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return domain.Proxy{}, err
	}
	p.CustomerID = textPtr(customer)
	p.ExpiresAt = intPtr(expires)
	p.Enabled = enabled == 1
	p.Suspended = suspended == 1
	p.AuthMode = domain.AuthMode(mode)
	p.Policy.AllowAllPorts = allowAll == 1

	ranges, err := domain.ParsePortRanges(allowedText)
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy %q allowed_ports: %w", p.ID, err)
	}
	p.Policy.AllowedPorts = ranges

	if r.sealer == nil {
		return domain.Proxy{}, ErrSealerMissing
	}
	plain, err := r.sealer.Open(enc)
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("store: proxy %q password: %w", p.ID, err)
	}
	p.Password = string(plain)
	return p, nil
}

func withProxyDefaults(p domain.Proxy) domain.Proxy {
	if p.AuthMode == "" {
		p.AuthMode = domain.AuthUserPass
	}
	unset := !p.Policy.AllowAllPorts && len(p.Policy.AllowedPorts) == 0 &&
		p.Policy.MaxConn == 0 && p.Policy.ConnLimit == 0
	if unset {
		p.Policy = domain.DefaultProxyPolicy()
	}
	if p.Policy.MaxConn == 0 {
		p.Policy.MaxConn = domain.DefaultMaxConn
	}
	return p
}
