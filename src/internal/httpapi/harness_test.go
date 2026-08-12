package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/device/sim"
	"github.com/n4darae/huawei-API/src/internal/devops"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/fw"
	netcfgfake "github.com/n4darae/huawei-API/src/internal/netcfg/fake"
	"github.com/n4darae/huawei-API/src/internal/ratelimit"
	"github.com/n4darae/huawei-API/src/internal/rotate"
	"github.com/n4darae/huawei-API/src/internal/secrets"
	"github.com/n4darae/huawei-API/src/internal/store"
)

const (
	testUser     = "operator"
	testPassword = "correct-horse"
	testSlots    = 3
	publicHost   = "203.0.113.10"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock {
	return &testClock{t: time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *testClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (c *testClock) Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Millisecond):
		c.Advance(d)
		return nil
	}
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type stubRotator struct {
	mu       sync.Mutex
	ops      map[string]*domain.Operation
	err      error
	started  int
	release  chan struct{}
	observed context.Context
	selftest rotate.SelftestResult
	selfErr  error
	repos    store.Repos
	clock    *testClock
	result   map[string]any
	opState  domain.OpState
}

func newStubRotator(repos store.Repos, clock *testClock) *stubRotator {
	return &stubRotator{
		ops:     map[string]*domain.Operation{},
		release: make(chan struct{}),
		repos:   repos,
		clock:   clock,
		opState: domain.OpSucceeded,
		result: map[string]any{
			"result":        string(domain.RotationChanged),
			"ip_changed":    true,
			"old_public_ip": "100.71.4.1",
			"new_public_ip": "100.71.8.8",
			"duration_ms":   41000,
		},
	}
}

func (s *stubRotator) Rotate(ctx context.Context, req rotate.Request) (*domain.Operation, error) {
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.started++
	s.observed = ctx
	s.mu.Unlock()

	now := domain.UnixMillis(s.clock.Now())
	op := domain.Operation{
		ID:          newID("op"),
		Kind:        domain.OpRotate,
		SubjectType: domain.SubjectProxy,
		SubjectID:   req.ProxyID,
		State:       domain.OpRunning,
		Step:        string(domain.StepPrecheck),
		StartedAt:   now,
		DeadlineAt:  now + 90_000,
		Trigger:     req.Trigger,
		ActorType:   req.ActorType,
		ActorID:     req.ActorID,
		RequestID:   req.RequestID,
	}
	if err := s.repos.Operations().Create(ctx, op); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.ops[op.ID] = &op
	s.mu.Unlock()

	go func() {
		<-s.release
		body, _ := json.Marshal(s.result)
		finished := domain.UnixMillis(s.clock.Now())
		s.repos.Operations().Finish(context.Background(), op.ID, s.opState, "", string(body), finished)
	}()
	return &op, nil
}

func (s *stubRotator) Selftest(context.Context, string) (rotate.SelftestResult, error) {
	return s.selftest, s.selfErr
}

func (s *stubRotator) Wait(ctx context.Context, id string) (domain.Operation, error) {
	select {
	case <-ctx.Done():
		return domain.Operation{}, ctx.Err()
	case <-s.release:
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		op, err := s.repos.Operations().Get(ctx, id)
		if err != nil {
			return domain.Operation{}, err
		}
		if op.State.Terminal() {
			return op, nil
		}
		if time.Now().After(deadline) {
			return op, nil
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (s *stubRotator) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.release:
	default:
		close(s.release)
	}
}

func (s *stubRotator) Starts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func (s *stubRotator) LiveContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observed
}

func (s *stubRotator) Fail(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

type harness struct {
	t       *testing.T
	srv     *httptest.Server
	api     *API
	store   *store.Store
	clock   *testClock
	bus     *eventbus.MemBus
	rot     *stubRotator
	ops     *devops.Service
	keys    *auth.Keys
	session *auth.Sessions
	farm    *sim.Farm
	limiter *ratelimit.Limiter

	cookie string
	csrf   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	clock := newClock()

	kek, err := secrets.GenerateKEK()
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	sealer, err := secrets.NewSealer(kek)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "state", "dongled.db"), sealer, store.WithClock(clock))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := auth.EnsureSchema(ctx, db.DB()); err != nil {
		t.Fatalf("auth schema: %v", err)
	}

	farm := sim.NewFarm(testSlots, sim.FarmOptions{Clock: clock.Now})
	t.Cleanup(func() { farm.Close() })

	bus := eventbus.NewMemBus(64)
	t.Cleanup(bus.Close)

	seedFarm(t, db, clock)

	ops, err := devops.New(devops.Deps{
		Repos:  db,
		Dev:    farm.Registry(),
		Net:    netcfgfake.New(),
		Bus:    bus,
		Clock:  clock,
		NodeID: "node-a",
	})
	if err != nil {
		t.Fatalf("devops: %v", err)
	}
	t.Cleanup(func() { ops.Shutdown(context.Background()) })

	sessions := auth.NewSessions(db.DB(), time.Hour, clock.Now)
	if err := sessions.SetPassword(ctx, testUser, testPassword); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	keys := auth.NewKeys(db.DB(), clock.Now)
	limiter := ratelimit.New(ratelimit.Limit{Burst: 5, Interval: time.Second}, clock.Now)
	rot := newStubRotator(db, clock)

	api, err := New(Deps{
		NodeID:            "node-a",
		Repos:             db,
		Rotator:           rot,
		Waiter:            rot,
		DevOps:            ops,
		Bus:               bus,
		Sessions:          sessions,
		Keys:              keys,
		Lockout:           auth.NewLockout(db.DB(), auth.DefaultLockoutPolicy(), clock.Now),
		Limiter:           limiter,
		Clock:             clock,
		MinRotateInterval: time.Minute,
		PingInterval:      20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("api: %v", err)
	}

	srv := httptest.NewServer(NewRouter(nil, api))
	t.Cleanup(srv.Close)

	h := &harness{
		t: t, srv: srv, api: api, store: db, clock: clock, bus: bus,
		rot: rot, ops: ops, keys: keys, session: sessions, farm: farm, limiter: limiter,
	}
	_ = fw.NewFake()
	return h
}

func seedFarm(t *testing.T, db *store.Store, clock *testClock) {
	t.Helper()
	ctx := context.Background()
	now := domain.UnixMillis(clock.Now())

	if err := db.Nodes().Upsert(ctx, domain.Node{
		ID: "node-a", Name: "node-a", Kind: domain.NodeKindLocal,
		PublicHost: netip.MustParseAddr(publicHost), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("node: %v", err)
	}

	if err := db.Customers().Create(ctx, domain.Customer{
		ID: "cus-1", Name: "Acme", Contact: "ops@acme.test", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("customer: %v", err)
	}

	modes := []domain.AuthMode{domain.AuthUserPass, domain.AuthIPList, domain.AuthBoth}
	for i := 1; i <= testSlots; i++ {
		slot := domain.Slot(i)
		dongleID := "dg-" + slot.String()
		if err := db.Dongles().Create(ctx, domain.Dongle{
			ID: dongleID, NodeID: "node-a", IMEI: "86123456789012" + slot.String(),
			Classify: domain.ClassifyHiLink, Carrier: "Viettel", LanIPChangeSupported: true,
			DataCapBytes: 30 << 30, CapResetDay: 1, AutoRecoverEnabled: true,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("dongle: %v", err)
		}

		slotID := "slot-" + slot.String()
		if err := db.Slots().Create(ctx, domain.SlotRow{
			ID: slotID, NodeID: "node-a", Slot: slot,
			USBPath: "1-" + slot.String(), IDPath: "platform-xhci-usb-0:" + slot.String() + ":1.0",
			IfName: slot.IfaceName(), CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("slot: %v", err)
		}
		if err := db.Slots().Attach(ctx, slotID, dongleID); err != nil {
			t.Fatalf("attach: %v", err)
		}

		mode := modes[(i-1)%len(modes)]
		px := domain.Proxy{
			ID: "px" + slot.String(), SlotID: slotID, Enabled: true,
			SocksPort: slot.SocksPort(), HTTPPort: slot.HTTPPort(),
			Username: "cust_px" + slot.String(), Password: "Kq7mZr2xTn9wLb4V",
			AuthMode: mode, Policy: domain.DefaultProxyPolicy(),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Proxies().Create(ctx, px); err != nil {
			t.Fatalf("proxy: %v", err)
		}
		if mode.UsesIPList() {
			if err := db.Proxies().AddAuthIP(ctx, domain.ProxyAuthIP{
				ID: "aip-" + slot.String(), ProxyID: px.ID,
				CIDR: netip.MustParsePrefix("203.0.113.5/32"), CreatedAt: now,
			}); err != nil {
				t.Fatalf("auth ip: %v", err)
			}
		}
	}
}

func (h *harness) login() {
	h.t.Helper()
	res := h.do(http.MethodPost, APIBase+"/auth/login", LoginRequest{Username: testUser, Password: testPassword})
	if res.StatusCode != http.StatusNoContent {
		h.t.Fatalf("login returned %d: %s", res.StatusCode, res.text())
	}
	for _, c := range res.Cookies() {
		if c.Name == auth.CookieName {
			h.cookie = c.Value
		}
	}
	if h.cookie == "" {
		h.t.Fatal("login issued no session cookie")
	}

	var body SessionBody
	h.getJSON(APIBase+"/auth/session", &body)
	h.csrf = body.CSRFToken
	if h.csrf == "" {
		h.t.Fatal("the session carries no csrf token")
	}
}

type response struct {
	*http.Response
	body []byte
}

func (r *response) text() string { return string(r.body) }

func (r *response) decode(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.body, dst); err != nil {
		t.Fatalf("decode %q: %v", string(r.body), err)
	}
}

func (r *response) errorBody(t *testing.T) ErrorBody {
	t.Helper()
	var out ErrorBody
	r.decode(t, &out)
	return out
}

func (r *response) conflictBody(t *testing.T) OpInProgressBody {
	t.Helper()
	var out OpInProgressBody
	r.decode(t, &out)
	return out
}

func (h *harness) request(method, path string, body any) *http.Request {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reader)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: h.cookie})
	}
	if h.csrf != "" && method != http.MethodGet {
		req.Header.Set(auth.CSRFHeader, h.csrf)
	}
	return req
}

func (h *harness) send(req *http.Request) *response {
	h.t.Helper()
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	return &response{Response: res, body: raw}
}

func (h *harness) do(method, path string, body any) *response {
	h.t.Helper()
	return h.send(h.request(method, path, body))
}

func (h *harness) getJSON(path string, dst any) *response {
	h.t.Helper()
	res := h.do(http.MethodGet, path, nil)
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("GET %s returned %d: %s", path, res.StatusCode, res.text())
	}
	if dst != nil {
		res.decode(h.t, dst)
	}
	return res
}

func (h *harness) newKey(name string, scopes []string, proxyIDs []string) (auth.Key, string) {
	h.t.Helper()
	key, secret, err := h.keys.Create(context.Background(), auth.NewKey{
		Name: name, Scopes: scopes, ProxyIDs: proxyIDs,
	})
	if err != nil {
		h.t.Fatalf("create key: %v", err)
	}
	return key, secret
}

func (h *harness) newCustomerKey(name, customerID string, scopes []string, proxyIDs []string) (auth.Key, string) {
	h.t.Helper()
	key, secret, err := h.keys.Create(context.Background(), auth.NewKey{
		Name: name, CustomerID: &customerID, Scopes: scopes, ProxyIDs: proxyIDs,
	})
	if err != nil {
		h.t.Fatalf("create key: %v", err)
	}
	return key, secret
}

func (h *harness) bearer(method, path, secret string) *response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, nil)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Accept", "application/json")
	return h.send(req)
}

func (h *harness) proxy(id string) Proxy {
	h.t.Helper()
	var list ProxyList
	h.getJSON(APIBase+"/proxies", &list)
	for _, p := range list.Items {
		if p.ID == id {
			return p
		}
	}
	h.t.Fatalf("proxy %q is not in the list", id)
	return Proxy{}
}

func (h *harness) simDevice(slot domain.Slot) *sim.SimDevice {
	h.t.Helper()
	d := h.farm.Device(slot)
	if d == nil {
		h.t.Fatalf("slot %s has no simulated device", slot)
	}
	return d
}

func contains(hay, needle string) bool { return strings.Contains(hay, needle) }

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

var simPinPattern = regexp.MustCompile(`(?i)sim[_ -]?p(i|u)[nk][_ -]?(required|locked)`)

func jsonMarshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	return string(raw), err
}
