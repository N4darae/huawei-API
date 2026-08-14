package hilink

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	HeaderToken    = "__RequestVerificationToken"
	HeaderTokenOne = "__RequestVerificationTokenone"
	HeaderTokenTwo = "__RequestVerificationTokentwo"
	HeaderCookie   = "Cookie"

	CookieName = "SessionID"

	TokenBatchSize = 30

	TokenSeparator = "#"

	PathSesTokInfo = "webserver/SesTokInfo"
)

type sesTokInfo struct {
	XMLName xml.Name `xml:"response"`
	SesInfo string   `xml:"SesInfo"`
	TokInfo string   `xml:"TokInfo"`
}

type session struct {
	mu     sync.Mutex
	cookie string
	tokens []string
}

func (s *session) snapshot() (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookie == "" || len(s.tokens) == 0 {
		return "", "", false
	}
	return s.cookie, s.tokens[0], true
}

func (s *session) take() (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookie == "" || len(s.tokens) == 0 {
		return "", "", false
	}
	token := s.tokens[0]
	s.tokens = s.tokens[1:]
	return s.cookie, token, true
}

func (s *session) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookie = ""
	s.tokens = nil
}

func (s *session) install(cookie string, tokens []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cookie != "" {
		s.cookie = cookie
	}
	if len(tokens) > 0 {
		s.tokens = tokens
	}
	return len(s.tokens)
}

func (s *session) absorb(tokens []string) int {
	if len(tokens) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, tokens...)
	if len(s.tokens) > TokenBatchSize {
		s.tokens = s.tokens[len(s.tokens)-TokenBatchSize:]
	}
	return len(tokens)
}

func splitTokens(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, TokenSeparator)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func tokensFromHeader(h http.Header) []string {
	if v := h.Get(HeaderTokenOne); v != "" {
		out := splitTokens(v)
		if w := h.Get(HeaderTokenTwo); w != "" {
			out = append(out, splitTokens(w)...)
		}
		return out
	}
	return splitTokens(h.Get(HeaderToken))
}

func cookieFromHeader(h http.Header) string {
	for _, raw := range h.Values("Set-Cookie") {
		for _, part := range strings.Split(raw, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, CookieName+"=") {
				return part
			}
		}
	}
	return ""
}

func normalizeCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, CookieName+"=") {
		return raw
	}
	return CookieName + "=" + raw
}

func (c *Client) handshake(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, PathSesTokInfo, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.unreachable(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return c.unreachable(err)
	}
	if apiErr := DecodeError(body, PathSesTokInfo); apiErr != nil {
		return apiErr
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Wrap(domain.ErrUnreachable, "hilink: %s status %d", PathSesTokInfo, resp.StatusCode)
	}
	var info sesTokInfo
	if err := xml.Unmarshal(body, &info); err != nil {
		return domain.Wrap(domain.ErrUnreachable, "hilink: %s malformed session response", PathSesTokInfo)
	}
	cookie := normalizeCookie(info.SesInfo)
	if cookie == "" {
		cookie = cookieFromHeader(resp.Header)
	}
	tokens := splitTokens(info.TokInfo)
	tokens = append(tokens, tokensFromHeader(resp.Header)...)
	n := c.sess.install(cookie, tokens)
	if c.onTokenRefill != nil {
		c.onTokenRefill(n)
	}
	return nil
}
