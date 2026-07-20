package httpapi

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/auth"
)

func TestLoginIssuesAStrictHostCookieAndASession(t *testing.T) {
	h := newHarness(t)

	res := h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: testPassword})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("login returned %d: %s", res.StatusCode, res.text())
	}

	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("no %s cookie was set", auth.CookieName)
	}
	if cookie.SameSite != http.SameSiteStrictMode || !cookie.HttpOnly {
		t.Fatalf("cookie is %+v, want SameSite=Strict and HttpOnly", cookie)
	}

	h.cookie = cookie.Value
	var body SessionBody
	h.getJSON(APIBase+"/auth/session", &body)
	if body.Username != testUser || body.CSRFToken == "" || body.ExpiresAt == 0 {
		t.Fatalf("session body is %+v", body)
	}
}

func TestLoginWithABadPasswordIsUnauthorized(t *testing.T) {
	h := newHarness(t)

	res := h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: "wrong"})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password returned %d", res.StatusCode)
	}
	if code := res.errorBody(t).Error; code != CodeInvalidCreds {
		t.Fatalf("error code is %q, want %q", code, CodeInvalidCreds)
	}
	if len(res.Cookies()) != 0 {
		t.Fatal("a failed login must not set a cookie")
	}
}

func TestRepeatedBadPasswordsLockTheAccountOutWithARetryAfter(t *testing.T) {
	h := newHarness(t)

	var last *response
	for i := 0; i < auth.DefaultLockoutThreshold; i++ {
		last = h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: "wrong"})
	}
	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("attempt %d returned %d, want 429", auth.DefaultLockoutThreshold, last.StatusCode)
	}

	header := last.Header.Get("Retry-After")
	if header == "" {
		t.Fatal("a lockout must send Retry-After")
	}
	secs, err := strconv.Atoi(header)
	if err != nil || secs <= 0 {
		t.Fatalf("Retry-After is %q", header)
	}
	if body := last.errorBody(t); body.RetryAfter != secs {
		t.Fatalf("body retry_after is %d, header says %d; the SPA reads either", body.RetryAfter, secs)
	}

	blocked := h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: testPassword})
	if blocked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the correct password during a lockout returned %d, want 429", blocked.StatusCode)
	}
}

func TestLockoutClearsAfterASuccessfulLogin(t *testing.T) {
	h := newHarness(t)

	for i := 0; i < auth.DefaultLockoutThreshold-1; i++ {
		h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: "wrong"})
	}
	if res := h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: testPassword}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("login returned %d", res.StatusCode)
	}
	for i := 0; i < auth.DefaultLockoutThreshold-1; i++ {
		res := h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: "wrong"})
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d returned %d, the counter was not reset by the success", i, res.StatusCode)
		}
	}
}

func TestEveryAdminRouteNeedsASession(t *testing.T) {
	h := newHarness(t)

	paths := []string{
		APIBase + "/proxies",
		APIBase + "/proxies/px01",
		APIBase + "/proxies/export",
		APIBase + "/slots",
		APIBase + "/dongles",
		APIBase + "/operations",
		APIBase + "/rotations",
		APIBase + "/customers",
		APIBase + "/keys",
		EventPath,
	}
	for _, p := range paths {
		res := h.do(http.MethodGet, p, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session returned %d, want 401", p, res.StatusCode)
		}
	}
}

func TestMutationsWithoutTheCSRFHeaderAreRefused(t *testing.T) {
	h := newHarness(t)
	h.login()

	good := h.csrf
	h.csrf = ""
	res := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a mutation without %s returned %d, want 403", auth.CSRFHeader, res.StatusCode)
	}
	if code := res.errorBody(t).Error; code != CodeCSRFInvalid {
		t.Fatalf("error code is %q", code)
	}

	h.csrf = "not-the-session-token"
	if res := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("a stale csrf token returned %d, want 403", res.StatusCode)
	}

	h.csrf = good
	if res := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil); res.StatusCode != http.StatusAccepted {
		t.Fatalf("the correct csrf token returned %d, want 202", res.StatusCode)
	}
}

func TestTheSPAHeaderNameIsExactlyXCSRFToken(t *testing.T) {
	h := newHarness(t)
	h.login()

	req := h.request(http.MethodPost, APIBase+"/proxies/px01/enable", EnableRequest{Enabled: true})
	req.Header.Del(auth.CSRFHeader)
	req.Header.Set("X-Csrf-Token", h.csrf)

	if res := h.send(req); res.StatusCode != http.StatusOK {
		t.Fatalf("the canonical header spelling returned %d: %s", res.StatusCode, res.text())
	}
}

func TestGetRequestsNeedNoCSRFHeader(t *testing.T) {
	h := newHarness(t)
	h.login()

	req := h.request(http.MethodGet, APIBase+"/proxies", nil)
	req.Header.Del(auth.CSRFHeader)
	if res := h.send(req); res.StatusCode != http.StatusOK {
		t.Fatalf("GET without csrf returned %d", res.StatusCode)
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	h := newHarness(t)
	h.login()

	if res := h.do(http.MethodPost, APIBase+"/auth/logout", nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout returned %d", res.StatusCode)
	}
	if res := h.do(http.MethodGet, APIBase+"/auth/session", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the session survived logout: %d", res.StatusCode)
	}
}

func TestLogoutWorksWithoutTheCSRFHeader(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.csrf = ""

	if res := h.do(http.MethodPost, APIBase+"/auth/logout", nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout without csrf returned %d; the panel calls it before the session query resolves", res.StatusCode)
	}
}

func TestAnExpiredSessionIsRejected(t *testing.T) {
	h := newHarness(t)
	h.login()

	h.clock.Advance(2 * 60 * 60 * 1e9)
	if res := h.do(http.MethodGet, APIBase+"/proxies", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an expired session returned %d", res.StatusCode)
	}
}
