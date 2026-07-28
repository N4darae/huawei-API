package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/ratelimit"

	_ "modernc.org/sqlite"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

type fixture struct {
	db    *sql.DB
	clock *testClock
	sess  *Sessions
	keys  *Keys
	lock  *Lockout
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c := newTestClock()
	db := openDB(t)
	return &fixture{
		db:    db,
		clock: c,
		sess:  NewSessions(db, time.Hour, c.Now),
		keys:  NewKeys(db, c.Now),
		lock:  NewLockout(db, DefaultLockoutPolicy(), c.Now),
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := Hash("correct-horse-battery", KeyParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash is not argon2id: %s", hash)
	}
	if strings.Contains(hash, "correct-horse-battery") {
		t.Fatal("the plaintext survived hashing")
	}
	if err := Verify("correct-horse-battery", hash); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify("wrong", hash); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password returned %v, want ErrBadCredentials", err)
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	a, _ := Hash("same", KeyParams())
	b, _ := Hash("same", KeyParams())
	if a == b {
		t.Fatal("two hashes of the same secret are identical, the salt is not random")
	}
}

func TestVerifyRejectsAMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA"} {
		if err := Verify("x", bad); err == nil {
			t.Fatalf("malformed hash %q was accepted", bad)
		}
	}
}

func TestAuthenticateAcceptsTheStoredPasswordOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.sess.SetPassword(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if err := f.sess.Authenticate(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("good password rejected: %v", err)
	}
	if err := f.sess.Authenticate(ctx, "admin", "bad"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("bad password returned %v", err)
	}
	if err := f.sess.Authenticate(ctx, "nobody", "correct-horse"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown user returned %v, want the same error as a bad password", err)
	}
}

func TestSetPasswordRefusesAShortOne(t *testing.T) {
	f := newFixture(t)
	if err := f.sess.SetPassword(context.Background(), "admin", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password returned %v", err)
	}
}

func TestSessionCookieValueIsNotWhatIsStored(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	sess, secret, err := f.sess.Issue(ctx, "admin")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if secret == "" || secret == sess.ID {
		t.Fatal("the cookie value must not be the stored session id")
	}

	var stored string
	if err := f.db.QueryRow(`SELECT id FROM auth_sessions`).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("the raw session secret is sitting in the database")
	}

	got, err := f.sess.Lookup(ctx, secret)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Username != "admin" || got.CSRFToken != sess.CSRFToken {
		t.Fatalf("looked up %+v, want %+v", got, sess)
	}
}

func TestSessionExpiresAndIsDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, secret, _ := f.sess.Issue(ctx, "admin")
	f.clock.Advance(2 * time.Hour)

	if _, err := f.sess.Lookup(ctx, secret); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired session returned %v", err)
	}
	var n int
	f.db.QueryRow(`SELECT count(*) FROM auth_sessions`).Scan(&n)
	if n != 0 {
		t.Fatal("the expired row was not deleted")
	}
}

func TestRevokeDropsTheSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, secret, _ := f.sess.Issue(ctx, "admin")
	if err := f.sess.Revoke(ctx, secret); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := f.sess.Lookup(ctx, secret); !errors.Is(err, ErrNoSession) {
		t.Fatalf("revoked session returned %v", err)
	}
}

func TestSessionCookieIsHostPrefixedAndStrict(t *testing.T) {
	f := newFixture(t)
	c := f.sess.Cookie("secret", true)

	if c.Name != "__Host-dongled_session" {
		t.Fatalf("cookie name is %q", c.Name)
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie must be SameSite=Strict")
	}
	if !c.HttpOnly || !c.Secure || c.Path != "/" || c.Domain != "" {
		t.Fatalf("__Host- prefix needs Secure, HttpOnly, Path=/ and no Domain, got %+v", c)
	}
}

func TestCSRFComparesConstantTimeAndRejectsAnEmptyToken(t *testing.T) {
	sess := Session{CSRFToken: "abc123"}
	if err := CheckCSRF(sess, "abc123"); err != nil {
		t.Fatalf("matching token rejected: %v", err)
	}
	for _, bad := range []string{"", "abc124", "ABC123"} {
		if err := CheckCSRF(sess, bad); !errors.Is(err, ErrBadCSRF) {
			t.Fatalf("token %q was accepted", bad)
		}
	}
	if err := CheckCSRF(Session{}, ""); !errors.Is(err, ErrBadCSRF) {
		t.Fatal("a session with no csrf token must never match")
	}
}

func TestCSRFIsOnlyRequiredForUnsafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if CSRFRequired(m) {
			t.Errorf("%s must not require csrf", m)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		if !CSRFRequired(m) {
			t.Errorf("%s must require csrf", m)
		}
	}
}

func TestLockoutTripsAfterTheThresholdAndReportsTheWait(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for i := 1; i < DefaultLockoutThreshold; i++ {
		locked, err := f.lock.Fail(ctx, "admin@203.0.113.9")
		if err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
		if locked != 0 {
			t.Fatalf("locked out after only %d attempts", i)
		}
	}

	locked, err := f.lock.Fail(ctx, "admin@203.0.113.9")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if locked != DefaultLockoutPenalty {
		t.Fatalf("penalty is %s, want %s", locked, DefaultLockoutPenalty)
	}

	wait, err := f.lock.Check(ctx, "admin@203.0.113.9")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if wait <= 0 || wait > DefaultLockoutPenalty {
		t.Fatalf("remaining lock is %s", wait)
	}
}

func TestLockoutExpiresWithTime(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for i := 0; i < DefaultLockoutThreshold; i++ {
		f.lock.Fail(ctx, "admin")
	}
	f.clock.Advance(DefaultLockoutPenalty + time.Second)

	if wait, _ := f.lock.Check(ctx, "admin"); wait != 0 {
		t.Fatalf("still locked for %s after the penalty elapsed", wait)
	}
}

func TestLockoutForgetsFailuresOutsideTheWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for i := 0; i < DefaultLockoutThreshold-1; i++ {
		f.lock.Fail(ctx, "admin")
	}
	f.clock.Advance(DefaultLockoutWindow + time.Minute)

	if locked, _ := f.lock.Fail(ctx, "admin"); locked != 0 {
		t.Fatal("failures older than the window must not count towards the lockout")
	}
}

func TestLockoutResetClearsTheCounter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for i := 0; i < DefaultLockoutThreshold; i++ {
		f.lock.Fail(ctx, "admin")
	}
	if err := f.lock.Reset(ctx, "admin"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if wait, _ := f.lock.Check(ctx, "admin"); wait != 0 {
		t.Fatalf("still locked for %s after a reset", wait)
	}
}

func TestApiKeySecretIsShownOnceAndHashedAtRest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key, secret, err := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{ScopeRotate, ScopeStatus}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(secret, "dgl_live_") {
		t.Fatalf("secret %q does not carry the dgl_live_ prefix", secret)
	}
	if len(secret) < 40 {
		t.Fatalf("secret %q is too short to be high entropy", secret)
	}
	if !strings.HasPrefix(key.Prefix, "dgl_live_") || len(key.Prefix) >= len(secret) {
		t.Fatalf("prefix %q must be a short label, not the secret", key.Prefix)
	}

	var hash string
	if err := f.db.QueryRow(`SELECT secret_hash FROM api_keys WHERE id = ?`, key.ID).Scan(&hash); err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(hash, secret) || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("secret is not argon2id hashed at rest: %s", hash)
	}

	listed, err := f.keys.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, k := range listed {
		if strings.Contains(k.Prefix, strings.TrimPrefix(secret, "dgl_live_")) {
			t.Fatal("the full secret leaked through the key list")
		}
	}
}

func TestApiKeyAuthenticates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key, secret, _ := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{ScopeRotate}})

	got, err := f.keys.Authenticate(ctx, secret)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.ID != key.ID {
		t.Fatalf("authenticated as %q, want %q", got.ID, key.ID)
	}
	if _, err := f.keys.Authenticate(ctx, secret+"x"); !errors.Is(err, ErrBadKey) {
		t.Fatalf("a tampered secret returned %v", err)
	}
	if _, err := f.keys.Authenticate(ctx, "not-a-key"); !errors.Is(err, ErrBadKey) {
		t.Fatalf("garbage returned %v", err)
	}
}

func TestRevokedApiKeyStopsAuthenticating(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key, secret, _ := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{ScopeRotate}})
	if err := f.keys.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := f.keys.Authenticate(ctx, secret); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("revoked key returned %v", err)
	}
}

func TestApiKeyCreateValidatesScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{"root"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown scope returned %v", err)
	}
	if _, _, err := f.keys.Create(ctx, NewKey{Name: "Acme"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a key with no scope returned %v", err)
	}
	if _, _, err := f.keys.Create(ctx, NewKey{Scopes: []string{ScopeRotate}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a key with no name returned %v", err)
	}
}

func TestKeyScopeAndProxyChecks(t *testing.T) {
	k := Key{Scopes: []string{ScopeRotate}, ProxyIDs: []string{"px01"}}
	if !k.HasScope(ScopeRotate) || k.HasScope(ScopeStatus) {
		t.Fatal("scope check is wrong")
	}
	if !k.CoversProxy("px01") || k.CoversProxy("px02") {
		t.Fatal("proxy allow list is wrong")
	}
	if !(Key{}).CoversProxy("anything") {
		t.Fatal("an empty proxy list must mean every proxy")
	}
}

func TestLinkTokenSecretIsNotTheTokenID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key, _, _ := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{ScopeRotate}})
	tok, secret, err := f.keys.CreateLinkToken(ctx, key.ID)
	if err != nil {
		t.Fatalf("create link token: %v", err)
	}
	if secret == tok.ID {
		t.Fatal("the link token secret is the id, so it leaks through GET /keys")
	}

	listed, err := f.keys.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || len(listed[0].LinkTokens) != 1 {
		t.Fatalf("expected one key carrying one token, got %+v", listed)
	}
	if listed[0].LinkTokens[0].ID == secret {
		t.Fatal("the secret is exposed as the token id in the key listing")
	}

	if _, _, err := f.keys.AuthenticateLink(ctx, secret); err != nil {
		t.Fatalf("the issued token does not authenticate: %v", err)
	}
	if _, _, err := f.keys.AuthenticateLink(ctx, tok.ID); !errors.Is(err, ErrBadKey) {
		t.Fatal("the token id must not work as a secret")
	}
}

func TestLinkTokenIsRevokedIndependentlyOfItsKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key, keySecret, _ := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{ScopeRotate}})
	tok, tokSecret, _ := f.keys.CreateLinkToken(ctx, key.ID)

	if err := f.keys.RevokeLinkToken(ctx, tok.ID); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if _, _, err := f.keys.AuthenticateLink(ctx, tokSecret); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked token returned %v", err)
	}
	if _, err := f.keys.Authenticate(ctx, keySecret); err != nil {
		t.Fatalf("revoking the link token also killed the api key: %v", err)
	}
}

func TestRevokingTheKeyAlsoStopsItsLinkTokens(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key, _, _ := f.keys.Create(ctx, NewKey{Name: "Acme", Scopes: []string{ScopeRotate}})
	_, tokSecret, _ := f.keys.CreateLinkToken(ctx, key.ID)
	f.keys.Revoke(ctx, key.ID)

	if _, _, err := f.keys.AuthenticateLink(ctx, tokSecret); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("token of a revoked key returned %v", err)
	}
}

func TestRevokingAnUnknownKeyIsNotFound(t *testing.T) {
	f := newFixture(t)
	if err := f.keys.Revoke(context.Background(), "key-nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if err := f.keys.RevokeLinkToken(context.Background(), "lt-nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	cases := map[string]string{
		"Bearer dgl_live_abc": "dgl_live_abc",
		"bearer dgl_live_abc": "dgl_live_abc",
		"Basic dgl_live_abc":  "",
		"dgl_live_abc":        "",
		"":                    "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := BearerToken(r); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestRequireSessionRejectsAMissingCSRFHeader(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sess, secret, _ := f.sess.Issue(ctx, "admin")

	var denied Denial
	m := Middleware{
		Sessions: f.sess,
		Deny:     func(w http.ResponseWriter, _ *http.Request, d Denial) { denied = d; w.WriteHeader(d.Status) },
	}
	h := m.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	post := func(csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/proxies/px01/rotate", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: secret})
		if csrf != "" {
			r.Header.Set(CSRFHeader, csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := post(""); w.Code != http.StatusForbidden || denied.Code != CodeCSRFInvalid {
		t.Fatalf("missing csrf gave %d %q", w.Code, denied.Code)
	}
	if w := post("wrong"); w.Code != http.StatusForbidden {
		t.Fatalf("wrong csrf gave %d", w.Code)
	}
	if w := post(sess.CSRFToken); w.Code != http.StatusNoContent {
		t.Fatalf("correct csrf gave %d", w.Code)
	}
}

func TestRequireSessionLetsGetThroughWithoutCSRF(t *testing.T) {
	f := newFixture(t)
	_, secret, _ := f.sess.Issue(context.Background(), "admin")

	m := Middleware{Sessions: f.sess}
	h := m.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/proxies", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: secret})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET with a session gave %d", w.Code)
	}
}

func TestRequireKeyEnforcesScopeAndRateLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, statusOnly, _ := f.keys.Create(ctx, NewKey{Name: "read", Scopes: []string{ScopeStatus}})

	var denied Denial
	m := Middleware{
		Keys:    f.keys,
		Limiter: ratelimit.New(ratelimit.Limit{Burst: 2, Interval: time.Minute}, f.clock.Now),
		Deny:    func(w http.ResponseWriter, _ *http.Request, d Denial) { denied = d; w.WriteHeader(d.Status) },
	}
	h := m.RequireKey(ScopeRotate)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))

	call := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/rotate/px01", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := call(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer gave %d", w.Code)
	}
	if w := call(statusOnly); w.Code != http.StatusForbidden || denied.Code != CodeScopeMissing {
		t.Fatalf("wrong scope gave %d %q", w.Code, denied.Code)
	}
	if w := call(statusOnly); w.Code != http.StatusForbidden {
		t.Fatalf("second call gave %d, the burst of two is not spent yet", w.Code)
	}

	w := call(statusOnly)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third call gave %d, want 429 once the two token burst is spent", w.Code)
	}
	if w.Header().Get("X-RateLimit-Limit") != "2" {
		t.Fatalf("X-RateLimit-Limit is %q", w.Header().Get("X-RateLimit-Limit"))
	}
	if denied.RetryAfter <= 0 {
		t.Fatal("a 429 must carry a retry after")
	}
}

func TestSweepDropsExpiredSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.sess.Issue(ctx, "admin")
	f.clock.Advance(2 * time.Hour)
	f.sess.Issue(ctx, "admin")

	n, err := f.sess.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d sessions, want 1", n)
	}
}

func TestHasUsersReportsAnEmptyInstall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if ok, _ := f.sess.HasUsers(ctx); ok {
		t.Fatal("a fresh database must have no users")
	}
	f.sess.SetPassword(ctx, "admin", "correct-horse")
	if ok, _ := f.sess.HasUsers(ctx); !ok {
		t.Fatal("HasUsers missed the seeded account")
	}
}

func TestEnsureSchemaIsIdempotent(t *testing.T) {
	db := openDB(t)
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}
