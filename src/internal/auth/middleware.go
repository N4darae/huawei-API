package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/ratelimit"
)

type contextKey int

const (
	ctxSession contextKey = iota
	ctxAPIKey
)

func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxSession, s)
}

func SessionFrom(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxSession).(Session)
	return s, ok
}

func WithKey(ctx context.Context, k Key) context.Context {
	return context.WithValue(ctx, ctxAPIKey, k)
}

func KeyFrom(ctx context.Context) (Key, bool) {
	k, ok := ctx.Value(ctxAPIKey).(Key)
	return k, ok
}

type Denial struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Decision   ratelimit.Decision
}

type DenyFunc func(w http.ResponseWriter, r *http.Request, d Denial)

type Middleware struct {
	Sessions   *Sessions
	Keys       *Keys
	Limiter    *ratelimit.Limiter
	Deny       DenyFunc
	CSRFExempt func(r *http.Request) bool
}

func (m Middleware) deny(w http.ResponseWriter, r *http.Request, d Denial) {
	if m.Deny != nil {
		m.Deny(w, r, d)
		return
	}
	http.Error(w, d.Message, d.Status)
}

func (m Middleware) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := m.Sessions.Lookup(r.Context(), CookieValue(r))
		if err != nil {
			m.deny(w, r, Denial{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "sign in to use the panel"})
			return
		}
		if CSRFRequired(r.Method) && !m.exempt(r) {
			if err := CheckCSRF(sess, RequestCSRF(r)); err != nil {
				m.deny(w, r, Denial{Status: http.StatusForbidden, Code: CodeCSRFInvalid, Message: "missing or stale " + CSRFHeader})
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), sess)))
	})
}

func (m Middleware) exempt(r *http.Request) bool {
	return m.CSRFExempt != nil && m.CSRFExempt(r)
}

func BearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return ""
	}
	scheme, token, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func (m Middleware) RequireKey(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := BearerToken(r)
			if token == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="dongled"`)
				m.deny(w, r, Denial{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "send the api key as Authorization: Bearer " + KeyPrefix + "..."})
				return
			}

			key, err := m.Keys.Authenticate(r.Context(), token)
			switch {
			case errors.Is(err, ErrKeyRevoked):
				m.deny(w, r, Denial{Status: http.StatusUnauthorized, Code: CodeKeyRevoked, Message: "this api key was revoked"})
				return
			case err != nil:
				w.Header().Set("WWW-Authenticate", `Bearer realm="dongled"`)
				m.deny(w, r, Denial{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "api key is not valid"})
				return
			}

			if m.Limiter != nil {
				d := m.Limiter.Allow(key.ID)
				WriteRateLimitHeaders(w, d)
				if !d.Allowed {
					m.deny(w, r, Denial{
						Status:     http.StatusTooManyRequests,
						Code:       CodeRateLimited,
						Message:    "api key rate limit exceeded",
						RetryAfter: d.RetryAfter,
						Decision:   d,
					})
					return
				}
			}

			if scope != "" && !key.HasScope(scope) {
				m.deny(w, r, Denial{Status: http.StatusForbidden, Code: CodeScopeMissing, Message: "this api key does not carry the " + scope + " scope"})
				return
			}

			next.ServeHTTP(w, r.WithContext(WithKey(r.Context(), key)))
		})
	}
}

func WriteRateLimitHeaders(w http.ResponseWriter, d ratelimit.Decision) {
	h := w.Header()
	h.Set("X-RateLimit-Limit", strconv.Itoa(max(d.Limit, 0)))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(max(d.Remaining, 0)))
	h.Set("X-RateLimit-Reset", strconv.Itoa(max(d.ResetSeconds(), 0)))
}

const (
	CodeUnauthorized = "unauthorized"
	CodeCSRFInvalid  = "csrf_invalid"
	CodeRateLimited  = "rate_limited"
	CodeKeyRevoked   = "key_revoked"
	CodeScopeMissing = "scope_missing"
)
