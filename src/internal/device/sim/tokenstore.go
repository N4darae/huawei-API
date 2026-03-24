package sim

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device/hilink"
)

const (
	TokenBatch      = hilink.TokenBatchSize
	DefaultTokenTTL = 10 * time.Minute
	SessionIDBytes  = 16
	TokenBytes      = 24
)

type tokenEntry struct {
	issued  time.Time
	used    bool
	expired bool
}

type tokenStore struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	cookie  string
	tokens  map[string]*tokenEntry
	pending []string
}

func newTokenStore(now func() time.Time, ttl time.Duration) *tokenStore {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	return &tokenStore{now: now, ttl: ttl, tokens: map[string]*tokenEntry{}}
}

func (s *tokenStore) handshake() (string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookie = hilink.CookieName + "=" + randomString(SessionIDBytes)
	s.tokens = map[string]*tokenEntry{}
	s.pending = s.mintLocked(TokenBatch)
	out := make([]string, len(s.pending))
	copy(out, s.pending)
	return s.cookie, out
}

func (s *tokenStore) mintLocked(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		t := randomString(TokenBytes)
		s.tokens[t] = &tokenEntry{issued: s.now()}
		out = append(out, t)
	}
	return out
}

func (s *tokenStore) refill(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mintLocked(n)
}

func (s *tokenStore) sessionValid(cookie string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookie != "" && cookie == s.cookie
}

func (s *tokenStore) cookieValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookie
}

func (s *tokenStore) consume(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[tok]
	if !ok || e.used || e.expired {
		return false
	}
	if s.now().Sub(e.issued) > s.ttl {
		e.expired = true
		return false
	}
	e.used = true
	return true
}

func (s *tokenStore) known(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if s.now().Sub(e.issued) > s.ttl {
		e.expired = true
		return false
	}
	return !e.expired
}

func (s *tokenStore) expireAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.tokens {
		e.expired = true
	}
}

func (s *tokenStore) invalidateSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookie = ""
	s.tokens = map[string]*tokenEntry{}
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
