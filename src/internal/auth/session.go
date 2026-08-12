package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	CookieName   = config.SessionCookie
	CookiePath   = "/"
	SessionBytes = 32
	DefaultTTL   = 12 * time.Hour
)

type Session struct {
	ID         string
	Username   string
	CSRFToken  string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
}

func (s Session) Valid(nowMS int64) bool { return s.ID != "" && s.ExpiresAt > nowMS }

type Sessions struct {
	db     DB
	ttl    time.Duration
	now    func() time.Time
	params Params
	decoy  func() string
}

func NewSessions(db DB, ttl time.Duration, now func() time.Time) *Sessions {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if now == nil {
		now = time.Now
	}
	params := PasswordParams()
	return &Sessions{
		db:     db,
		ttl:    ttl,
		now:    now,
		params: params,
		decoy: sync.OnceValue(func() string {
			h, err := Hash("no-such-user", params)
			if err != nil {
				return ""
			}
			return h
		}),
	}
}

func (s *Sessions) TTL() time.Duration { return s.ttl }

func (s *Sessions) nowMS() int64 { return domain.UnixMillis(s.now()) }

func (s *Sessions) SetPassword(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(strings.ToLower(username))
	if username == "" {
		return fmt.Errorf("%w: username is required", domain.ErrInvalid)
	}
	if len(password) < MinPasswordLen {
		return fmt.Errorf("%w: at least %d characters", ErrWeakPassword, MinPasswordLen)
	}
	hash, err := Hash(password, s.params)
	if err != nil {
		return err
	}
	now := s.nowMS()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO auth_users(username, password_hash, created_at, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		username, hash, now, now)
	if err != nil {
		return fmt.Errorf("auth: store user %q: %w", username, err)
	}
	return nil
}

func (s *Sessions) HasUsers(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM auth_users`).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: count users: %w", err)
	}
	return n > 0, nil
}

func (s *Sessions) Authenticate(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(strings.ToLower(username))

	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM auth_users WHERE username = ?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		if decoy := s.decoy(); decoy != "" {
			_ = Verify(password, decoy)
		}
		return ErrBadCredentials
	}
	if err != nil {
		return fmt.Errorf("auth: read user %q: %w", username, err)
	}
	return Verify(password, hash)
}

func (s *Sessions) Issue(ctx context.Context, username string) (Session, string, error) {
	secret, err := randomToken(SessionBytes)
	if err != nil {
		return Session{}, "", err
	}
	csrf, err := NewCSRFToken()
	if err != nil {
		return Session{}, "", err
	}

	now := s.nowMS()
	sess := Session{
		ID:         digest(secret),
		Username:   strings.TrimSpace(strings.ToLower(username)),
		CSRFToken:  csrf,
		CreatedAt:  now,
		ExpiresAt:  now + s.ttl.Milliseconds(),
		LastSeenAt: now,
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO auth_sessions(id, username, csrf_token, created_at, expires_at, last_seen_at) VALUES(?,?,?,?,?,?)`,
		sess.ID, sess.Username, sess.CSRFToken, sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt)
	if err != nil {
		return Session{}, "", fmt.Errorf("auth: store session: %w", err)
	}
	return sess, secret, nil
}

func (s *Sessions) Lookup(ctx context.Context, secret string) (Session, error) {
	if strings.TrimSpace(secret) == "" {
		return Session{}, ErrNoSession
	}
	id := digest(secret)

	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, csrf_token, created_at, expires_at, last_seen_at FROM auth_sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.Username, &sess.CSRFToken, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: read session: %w", err)
	}

	now := s.nowMS()
	if sess.ExpiresAt <= now {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id = ?`, id)
		return Session{}, ErrSessionExpired
	}
	if now-sess.LastSeenAt > time.Minute.Milliseconds() {
		_, _ = s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at = ? WHERE id = ?`, now, id)
		sess.LastSeenAt = now
	}
	return sess, nil
}

func (s *Sessions) Revoke(ctx context.Context, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id = ?`, digest(secret))
	if err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

func (s *Sessions) RevokeUser(ctx context.Context, username string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE username = ?`,
		strings.TrimSpace(strings.ToLower(username)))
	if err != nil {
		return fmt.Errorf("auth: revoke sessions of %q: %w", username, err)
	}
	return nil
}

func (s *Sessions) Sweep(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= ?`, s.nowMS())
	if err != nil {
		return 0, fmt.Errorf("auth: sweep sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}

func (s *Sessions) Cookie(secret string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    secret,
		Path:     CookiePath,
		MaxAge:   int(s.ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (s *Sessions) ClearCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     CookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func CookieValue(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
