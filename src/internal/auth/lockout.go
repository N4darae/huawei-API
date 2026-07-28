package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	DefaultLockoutThreshold = 5
	DefaultLockoutWindow    = 15 * time.Minute
	DefaultLockoutPenalty   = 15 * time.Minute
	MaxLockoutPenalty       = 2 * time.Hour
)

type LockoutPolicy struct {
	Threshold  int
	Window     time.Duration
	Penalty    time.Duration
	MaxPenalty time.Duration
}

func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		Threshold:  DefaultLockoutThreshold,
		Window:     DefaultLockoutWindow,
		Penalty:    DefaultLockoutPenalty,
		MaxPenalty: MaxLockoutPenalty,
	}
}

func (p LockoutPolicy) normalize() LockoutPolicy {
	if p.Threshold < 1 {
		p.Threshold = DefaultLockoutThreshold
	}
	if p.Window <= 0 {
		p.Window = DefaultLockoutWindow
	}
	if p.Penalty <= 0 {
		p.Penalty = DefaultLockoutPenalty
	}
	if p.MaxPenalty < p.Penalty {
		p.MaxPenalty = MaxLockoutPenalty
	}
	return p
}

type Lockout struct {
	db     DB
	policy LockoutPolicy
	now    func() time.Time
}

func NewLockout(db DB, p LockoutPolicy, now func() time.Time) *Lockout {
	if now == nil {
		now = time.Now
	}
	return &Lockout{db: db, policy: p.normalize(), now: now}
}

func (l *Lockout) Policy() LockoutPolicy { return l.policy }

func (l *Lockout) nowMS() int64 { return domain.UnixMillis(l.now()) }

type lockoutRow struct {
	failures    int
	firstAt     int64
	lockedUntil int64
}

func (l *Lockout) read(ctx context.Context, subject string) (lockoutRow, bool, error) {
	var row lockoutRow
	err := l.db.QueryRowContext(ctx,
		`SELECT failures, first_at, locked_until FROM auth_lockout WHERE subject = ?`, subject).
		Scan(&row.failures, &row.firstAt, &row.lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return lockoutRow{}, false, nil
	}
	if err != nil {
		return lockoutRow{}, false, fmt.Errorf("auth: read lockout %q: %w", subject, err)
	}
	return row, true, nil
}

func (l *Lockout) Check(ctx context.Context, subject string) (time.Duration, error) {
	row, ok, err := l.read(ctx, subject)
	if err != nil || !ok {
		return 0, err
	}
	now := l.nowMS()
	if row.lockedUntil > now {
		return time.Duration(row.lockedUntil-now) * time.Millisecond, nil
	}
	return 0, nil
}

func (l *Lockout) Fail(ctx context.Context, subject string) (time.Duration, error) {
	now := l.nowMS()
	row, ok, err := l.read(ctx, subject)
	if err != nil {
		return 0, err
	}

	failures := 1
	firstAt := now
	if ok && now-row.firstAt <= l.policy.Window.Milliseconds() {
		failures = row.failures + 1
		firstAt = row.firstAt
	}

	lockedUntil := int64(0)
	var locked time.Duration
	if failures >= l.policy.Threshold {
		locked = l.penaltyFor(failures)
		lockedUntil = now + locked.Milliseconds()
		failures = 0
		firstAt = now
	}

	_, err = l.db.ExecContext(ctx,
		`INSERT INTO auth_lockout(subject, failures, first_at, locked_until) VALUES(?,?,?,?)
		 ON CONFLICT(subject) DO UPDATE SET failures = excluded.failures, first_at = excluded.first_at,
		   locked_until = max(excluded.locked_until, auth_lockout.locked_until)`,
		subject, failures, firstAt, lockedUntil)
	if err != nil {
		return 0, fmt.Errorf("auth: record failed attempt for %q: %w", subject, err)
	}
	return locked, nil
}

func (l *Lockout) penaltyFor(failures int) time.Duration {
	rounds := failures / l.policy.Threshold
	penalty := l.policy.Penalty
	for i := 1; i < rounds && penalty < l.policy.MaxPenalty; i++ {
		penalty *= 2
	}
	if penalty > l.policy.MaxPenalty {
		penalty = l.policy.MaxPenalty
	}
	return penalty
}

func (l *Lockout) Reset(ctx context.Context, subject string) error {
	if _, err := l.db.ExecContext(ctx, `DELETE FROM auth_lockout WHERE subject = ?`, subject); err != nil {
		return fmt.Errorf("auth: clear lockout %q: %w", subject, err)
	}
	return nil
}

func (l *Lockout) Sweep(ctx context.Context) error {
	cutoff := l.nowMS() - l.policy.Window.Milliseconds()
	_, err := l.db.ExecContext(ctx,
		`DELETE FROM auth_lockout WHERE locked_until <= ? AND first_at <= ?`, l.nowMS(), cutoff)
	if err != nil {
		return fmt.Errorf("auth: sweep lockout: %w", err)
	}
	return nil
}
