package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/secrets"
)

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type nowFunc func() int64

type base struct {
	q   queryer
	now nowFunc
}

type repoSet struct {
	nodes      *nodeRepo
	dongles    *dongleRepo
	slots      *slotRepo
	proxies    *proxyRepo
	operations *operationRepo
	rotations  *rotationRepo
	customers  *customerRepo
	usage      *usageRepo
	sms        *smsRepo
	settings   *settingsRepo
}

var _ Repos = (*repoSet)(nil)

func newRepoSet(q queryer, sealer secrets.Sealer, now nowFunc) *repoSet {
	b := base{q: q, now: now}
	return &repoSet{
		nodes:      &nodeRepo{b},
		dongles:    &dongleRepo{b},
		slots:      &slotRepo{b},
		proxies:    &proxyRepo{base: b, sealer: sealer},
		operations: &operationRepo{b},
		rotations:  &rotationRepo{b},
		customers:  &customerRepo{b},
		usage:      &usageRepo{b},
		sms:        &smsRepo{b},
		settings:   &settingsRepo{b},
	}
}

func (r *repoSet) Nodes() NodeRepo           { return r.nodes }
func (r *repoSet) Dongles() DongleRepo       { return r.dongles }
func (r *repoSet) Slots() SlotRepo           { return r.slots }
func (r *repoSet) Proxies() ProxyRepo        { return r.proxies }
func (r *repoSet) Operations() OperationRepo { return r.operations }
func (r *repoSet) Rotations() RotationRepo   { return r.rotations }
func (r *repoSet) Customers() CustomerRepo   { return r.customers }
func (r *repoSet) Usage() UsageRepo          { return r.usage }
func (r *repoSet) SMS() SMSRepo              { return r.sms }
func (r *repoSet) Settings() SettingsRepo    { return r.settings }

func (b base) exec(ctx context.Context, what, query string, args ...any) error {
	if _, err := b.q.ExecContext(ctx, query, args...); err != nil {
		return mapErr(err, what)
	}
	return nil
}

func (b base) execAffecting(ctx context.Context, what, id, query string, args ...any) error {
	res, err := b.q.ExecContext(ctx, query, args...)
	if err != nil {
		return mapErr(err, what)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return mapErr(err, what)
	}
	if n == 0 {
		return notFound(what, id)
	}
	return nil
}

func (b base) count(ctx context.Context, what, query string, args ...any) (int, error) {
	var n int
	if err := b.q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, mapErr(err, what)
	}
	return n, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullText(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func textPtr(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	s := v.String
	return &s
}

func intPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func addrText(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

func parseAddr(what, s string) (netip.Addr, error) {
	if strings.TrimSpace(s) == "" {
		return netip.Addr{}, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s %q: %w", what, s, domain.ErrInvalid)
	}
	return a, nil
}

func parsePrefix(what, s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%s %q: %w", what, s, domain.ErrInvalid)
	}
	return p, nil
}

func limitClause(n int) string {
	if n <= 0 {
		return ""
	}
	return " LIMIT ?"
}

func withLimit(args []any, n int) []any {
	if n <= 0 {
		return args
	}
	return append(args, n)
}

func errInvalid(format string, args ...any) error {
	return fmt.Errorf("store: "+format+": %w", append(args, domain.ErrInvalid)...)
}

func (b base) stamps(created int64) (int64, int64) {
	now := b.now()
	if created > 0 {
		return created, now
	}
	return now, now
}
