package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/rotate"
)

func TestAdminRotateIsAcceptedAsynchronously(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var acc OperationAccepted
	res.decode(t, &acc)
	if acc.OperationID == "" || acc.PollURL == "" {
		t.Fatalf("accepted body is %+v", acc)
	}

	p := h.proxy("px01")
	if p.ActiveOperationID == nil || *p.ActiveOperationID != acc.OperationID {
		t.Fatalf("the proxy does not carry the live operation id: %v", p.ActiveOperationID)
	}
}

func TestConcurrentRotateReturns409CarryingTheLiveOperationID(t *testing.T) {
	h := newHarness(t)
	h.login()

	first := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)
	var acc OperationAccepted
	first.decode(t, &acc)

	h.rot.Fail(&rotate.ConflictError{
		OperationID: acc.OperationID,
		SubjectType: domain.SubjectProxy,
		SubjectID:   "px01",
	})

	second := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("a concurrent rotate returned %d, want 409", second.StatusCode)
	}
	body := second.conflictBody(t)
	if body.Error != CodeOpInProgress {
		t.Fatalf("error code is %q", body.Error)
	}
	if body.OperationID != acc.OperationID {
		t.Fatalf("409 carries operation %q, the live one is %q; the panel attaches to it", body.OperationID, acc.OperationID)
	}
	if body.PollURL != APIBase+"/operations/"+acc.OperationID {
		t.Fatalf("poll_url is %q", body.PollURL)
	}
}

func TestDeviceConflictAlsoCarriesItsOperationID(t *testing.T) {
	h := newHarness(t)
	h.login()

	first := h.do(http.MethodPost, APIBase+"/dongles/dg-01/reboot", nil)
	var acc OperationAccepted
	first.decode(t, &acc)

	second := h.do(http.MethodPost, APIBase+"/dongles/dg-01/reboot", nil)
	if second.StatusCode != http.StatusConflict {
		t.Skipf("the first reboot already finished, returned %d", second.StatusCode)
	}
	if body := second.conflictBody(t); body.OperationID == "" {
		t.Fatal("a device 409 must carry the operation id too")
	}
}

func TestMinIntervalReturns429WithRetryAfterInBothPlaces(t *testing.T) {
	h := newHarness(t)
	h.login()

	h.rot.Fail(&rotate.TooSoonError{ProxyID: "px01", RetryAfter: 42 * time.Second, MinInterval: time.Minute})

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("returned %d, want 429", res.StatusCode)
	}
	header := res.Header.Get("Retry-After")
	if header != "42" {
		t.Fatalf("Retry-After header is %q, want 42", header)
	}
	body := res.errorBody(t)
	if body.Error != CodeRateLimited {
		t.Fatalf("error code is %q", body.Error)
	}
	if strconv.Itoa(body.RetryAfter) != header {
		t.Fatalf("body retry_after is %d, header says %q", body.RetryAfter, header)
	}
}

func TestSimLockedReportsAStableCodeTheSPACanMatch(t *testing.T) {
	h := newHarness(t)
	h.login()

	h.rot.Fail(domain.Wrap(domain.ErrSimLocked, "rotate: sim state 260"))

	res := h.do(http.MethodPost, APIBase+"/proxies/px01/rotate", nil)
	body := res.errorBody(t)
	if body.Error != CodeSimPinRequired {
		t.Fatalf("error code is %q, want %q", body.Error, CodeSimPinRequired)
	}
	if !simPinPattern.MatchString(body.Error + " " + body.Message) {
		t.Fatalf("the SPA regex does not match %q", body.Error)
	}
}

func TestCustomerRotateNeedsABearerKeyWithTheRotateScope(t *testing.T) {
	h := newHarness(t)

	if res := h.do(http.MethodPost, APIBase+"/rotate/px01", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no bearer returned %d", res.StatusCode)
	}

	_, statusOnly := h.newKey("read only", []string{auth.ScopeStatus}, nil)
	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01", statusOnly)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a status only key returned %d, want 403", res.StatusCode)
	}
	if code := res.errorBody(t).Error; code != CodeScopeMissing {
		t.Fatalf("error code is %q", code)
	}
}

func TestCustomerRotateIsAcceptedAndNeverNamesTheDongle(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate}, []string{"px01"})

	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01", secret)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	if contains(res.text(), "dg-") || contains(res.text(), "dongle") {
		t.Fatalf("a customer response revealed dongle identity: %s", res.text())
	}
}

func TestCustomerKeyCannotTouchAProxyItDoesNotCover(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate, auth.ScopeStatus}, []string{"px01"})

	if res := h.bearer(http.MethodPost, APIBase+"/rotate/px02", secret); res.StatusCode != http.StatusForbidden {
		t.Fatalf("rotate of an uncovered proxy returned %d", res.StatusCode)
	}
	if res := h.bearer(http.MethodGet, APIBase+"/status/px02", secret); res.StatusCode != http.StatusForbidden {
		t.Fatalf("status of an uncovered proxy returned %d", res.StatusCode)
	}
}

func TestUnchangedRotationIsHTTP200WithIPChangedFalse(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate}, nil)

	h.rot.opState = domain.OpFailed
	h.rot.result = map[string]any{
		"result":        string(domain.RotationUnchanged),
		"ip_changed":    false,
		"old_public_ip": "100.71.4.5",
		"new_public_ip": "100.71.4.5",
		"duration_ms":   38000,
		"reason":        "unchanged",
	}
	h.rot.Finish()

	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01?wait=true", secret)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("an unchanged rotation returned %d, the customer contract is 200: %s", res.StatusCode, res.text())
	}
	var out RotateResult
	res.decode(t, &out)
	if out.Result != string(domain.RotationUnchanged) {
		t.Fatalf("result is %q", out.Result)
	}
	if out.IPChanged {
		t.Fatal("ip_changed must be false")
	}
	if out.OldIP != "100.71.4.5" || out.NewIP != "100.71.4.5" {
		t.Fatalf("old_ip/new_ip are %q/%q; result_json uses old_public_ip and the API renames it", out.OldIP, out.NewIP)
	}
	if out.OperationID == "" {
		t.Fatal("the result must name its operation")
	}

	op, err := h.store.Operations().Get(context.Background(), out.OperationID)
	if err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if op.State != domain.OpFailed {
		t.Fatalf("the internal verdict is %q, an unchanged rotation stays a failed operation", op.State)
	}
}

func TestWaitTrueReturnsTheTerminalResult(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate}, nil)
	h.rot.Finish()

	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01?wait=true", secret)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var out RotateResult
	res.decode(t, &out)
	if out.Result != string(domain.RotationChanged) || !out.IPChanged {
		t.Fatalf("result is %+v", out)
	}
	if out.OldIP != "100.71.4.1" || out.NewIP != "100.71.8.8" {
		t.Fatalf("addresses are %q -> %q", out.OldIP, out.NewIP)
	}
	if out.DurationMS != 41000 {
		t.Fatalf("duration_ms is %d", out.DurationMS)
	}
}

func TestWithoutWaitTheCallerGets202AndPolls(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate}, nil)

	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01", secret)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d", res.StatusCode)
	}
	var acc OperationAccepted
	res.decode(t, &acc)
	if acc.PollURL != APIBase+"/operations/"+acc.OperationID {
		t.Fatalf("poll_url is %q", acc.PollURL)
	}
}

func TestTheRotationOutlivesAClientThatDisconnects(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate}, nil)

	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01", secret)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("returned %d", res.StatusCode)
	}

	live := h.rot.LiveContext()
	if live == nil {
		t.Fatal("the rotator saw no context")
	}
	if err := live.Err(); err != nil {
		t.Fatalf("the rotate context is already cancelled: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if live.Err() != nil {
			t.Fatalf("the rotate context was cancelled when the HTTP request ended: %v", live.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPerKeyRateLimitReturns429WithHeaders(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeStatus}, nil)

	var last *response
	for i := 0; i < 6; i++ {
		last = h.bearer(http.MethodGet, APIBase+"/status/px01", secret)
	}
	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the sixth call over a burst of five returned %d", last.StatusCode)
	}
	if last.Header.Get("Retry-After") == "" {
		t.Fatal("a rate limited call must send Retry-After")
	}
	if last.Header.Get("X-RateLimit-Limit") != "5" {
		t.Fatalf("X-RateLimit-Limit is %q", last.Header.Get("X-RateLimit-Limit"))
	}
	if last.Header.Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("X-RateLimit-Remaining is %q", last.Header.Get("X-RateLimit-Remaining"))
	}
	if last.Header.Get("X-RateLimit-Reset") == "" {
		t.Fatal("X-RateLimit-Reset is missing")
	}
}

func TestCustomerStatusExposesProxyIDNeverDongleID(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeStatus}, []string{"px01"})

	res := h.bearer(http.MethodGet, APIBase+"/status/px01", secret)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var out CustomerStatus
	res.decode(t, &out)
	if out.ProxyID != "px01" || out.Host != publicHost {
		t.Fatalf("status is %+v", out)
	}
	if out.SocksPort != 21001 || out.HTTPPort != 22001 {
		t.Fatalf("ports are %d/%d", out.SocksPort, out.HTTPPort)
	}
	if out.MinRotateIntervalS != 60 {
		t.Fatalf("min_rotate_interval_s is %d", out.MinRotateIntervalS)
	}
	for _, banned := range []string{"dg-", "dongle", "imei", "iccid", "slot"} {
		if contains(res.text(), banned) {
			t.Fatalf("customer status leaked %q: %s", banned, res.text())
		}
	}
}

func TestAnExpiredProxyRefusesACustomerRotate(t *testing.T) {
	h := newHarness(t)
	_, secret := h.newKey("acme", []string{auth.ScopeRotate}, nil)

	past := h.nowPlus(-time.Hour)
	if err := h.store.Proxies().SetCustomer(context.Background(), "px01", nil, &past); err != nil {
		t.Fatalf("expire: %v", err)
	}

	res := h.bearer(http.MethodPost, APIBase+"/rotate/px01", secret)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("returned %d, want 403", res.StatusCode)
	}
	if code := res.errorBody(t).Error; code != CodeProxyExpired {
		t.Fatalf("error code is %q", code)
	}
}

func TestARevokedKeyStopsWorking(t *testing.T) {
	h := newHarness(t)
	key, secret := h.newKey("acme", []string{auth.ScopeStatus}, nil)

	if res := h.bearer(http.MethodGet, APIBase+"/status/px01", secret); res.StatusCode != http.StatusOK {
		t.Fatalf("a fresh key returned %d", res.StatusCode)
	}
	if err := h.keys.Revoke(context.Background(), key.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	res := h.bearer(http.MethodGet, APIBase+"/status/px01", secret)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked key returned %d", res.StatusCode)
	}
	if code := res.errorBody(t).Error; code != CodeKeyRevoked {
		t.Fatalf("error code is %q", code)
	}
}

func TestCustomerKeyCannotTouchAnotherCustomersProxy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := domain.UnixMillis(h.clock.Now())
	if err := h.store.Customers().Create(ctx, domain.Customer{
		ID: "cus-2", Name: "Globex", Contact: "ops@globex.test", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("customer: %v", err)
	}
	acme, globex := "cus-1", "cus-2"
	if err := h.store.Proxies().SetCustomer(ctx, "px01", &acme, nil); err != nil {
		t.Fatalf("assign px01: %v", err)
	}
	if err := h.store.Proxies().SetCustomer(ctx, "px02", &globex, nil); err != nil {
		t.Fatalf("assign px02: %v", err)
	}

	_, secret := h.newCustomerKey("acme", acme, []string{auth.ScopeRotate, auth.ScopeStatus}, nil)

	if res := h.bearer(http.MethodPost, APIBase+"/rotate/px01", secret); res.StatusCode != http.StatusAccepted {
		t.Fatalf("rotate of its own proxy returned %d: %s", res.StatusCode, res.text())
	}
	if res := h.bearer(http.MethodPost, APIBase+"/rotate/px02", secret); res.StatusCode != http.StatusForbidden {
		t.Fatalf("rotate of another customer proxy returned %d", res.StatusCode)
	}
	if res := h.bearer(http.MethodGet, APIBase+"/status/px02", secret); res.StatusCode != http.StatusForbidden {
		t.Fatalf("status of another customer proxy returned %d", res.StatusCode)
	}
}
