package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func (h *harness) issueLink(proxyID string) string {
	h.t.Helper()
	key, _ := h.newKey("link "+proxyID, []string{auth.ScopeRotate}, []string{proxyID})
	token, secret, err := h.keys.CreateLinkToken(h.t.Context(), key.ID)
	if err != nil {
		h.t.Fatalf("create link token: %v", err)
	}
	if token.ID == secret {
		h.t.Fatal("the token id must not be the secret")
	}
	return secret
}

func (h *harness) linkGet(secret string, headers map[string]string) *response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+LinkBase+"/"+secret, nil)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return h.send(req)
}

func TestABareGetOnALinkNeverRotates(t *testing.T) {
	h := newHarness(t)
	secret := h.issueLink("px01")

	scanners := []map[string]string{
		{},
		{"User-Agent": "Mozilla/5.0 (compatible; Proofpoint URL Defense)"},
		{"User-Agent": "Microsoft Office Existence Discovery"},
		{"Sec-Purpose": "prefetch"},
		{"Purpose": "prefetch"},
		{"Accept": "*/*"},
	}
	for _, headers := range scanners {
		res := h.linkGet(secret, headers)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("headers %v returned %d", headers, res.StatusCode)
		}
	}

	if h.rot.Starts() != 0 {
		t.Fatalf("a GET on the link started %d rotations; a link scanner would rotate the customer IP", h.rot.Starts())
	}
}

func TestTheConfirmPageIsHtmlAndCarriesAPostForm(t *testing.T) {
	h := newHarness(t)
	secret := h.issueLink("px01")

	res := h.linkGet(secret, nil)
	if ct := res.Header.Get("Content-Type"); !contains(ct, "text/html") {
		t.Fatalf("content type is %q", ct)
	}
	body := res.text()
	if !contains(body, `<form method="post">`) {
		t.Fatalf("no post form on the confirm page:\n%s", body)
	}
	if !contains(body, "px01") {
		t.Fatal("the page does not name the proxy")
	}
	if contains(body, secret) {
		t.Fatal("the page echoes the secret back into its own body")
	}
	if res.Header.Get("X-Robots-Tag") == "" {
		t.Fatal("the confirm page should ask crawlers to stay away")
	}
}

func TestPostOnALinkRotates(t *testing.T) {
	h := newHarness(t)
	secret := h.issueLink("px01")
	h.rot.Finish()

	req, err := http.NewRequest(http.MethodPost, h.srv.URL+LinkBase+"/"+secret, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Accept", "application/json")

	res := h.send(req)
	if res.StatusCode != http.StatusAccepted && res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	if h.rot.Starts() != 1 {
		t.Fatalf("the POST started %d rotations, want 1", h.rot.Starts())
	}
}

func TestPostFromABrowserRendersAResultPage(t *testing.T) {
	h := newHarness(t)
	secret := h.issueLink("px01")
	h.rot.Finish()

	req, err := http.NewRequest(http.MethodPost, h.srv.URL+LinkBase+"/"+secret, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Accept", "text/html")

	res := h.send(req)
	if ct := res.Header.Get("Content-Type"); !contains(ct, "text/html") {
		t.Fatalf("content type is %q", ct)
	}
	if !contains(res.text(), string(domain.RotationChanged)) {
		t.Fatalf("the result page does not show the outcome:\n%s", res.text())
	}
}

func TestASecondPostInsideTheCooldownIsRefused(t *testing.T) {
	h := newHarness(t)
	secret := h.issueLink("px01")
	h.rot.Finish()

	post := func() *response {
		req, err := http.NewRequest(http.MethodPost, h.srv.URL+LinkBase+"/"+secret, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Accept", "application/json")
		return h.send(req)
	}

	if res := post(); res.StatusCode >= 400 {
		t.Fatalf("the first post returned %d: %s", res.StatusCode, res.text())
	}
	second := post()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the second post inside the cooldown returned %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("the cooldown must send Retry-After")
	}
	if h.rot.Starts() != 1 {
		t.Fatalf("the cooldown let %d rotations through", h.rot.Starts())
	}

	h.clock.Advance(2 * time.Minute)
	if res := post(); res.StatusCode == http.StatusTooManyRequests {
		t.Fatal("the cooldown never expires")
	}
}

func TestAnUnknownLinkIs404(t *testing.T) {
	h := newHarness(t)

	res := h.linkGet("not-a-real-token", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("returned %d", res.StatusCode)
	}
	if !contains(strings.ToLower(res.text()), "does not exist") {
		t.Fatalf("body is:\n%s", res.text())
	}
}

func TestALinkNeverNamesTheDongle(t *testing.T) {
	h := newHarness(t)
	secret := h.issueLink("px01")

	body := h.linkGet(secret, nil).text()
	for _, banned := range []string{"dg-", "imei", "iccid"} {
		if contains(strings.ToLower(body), banned) {
			t.Fatalf("the confirm page leaked %q:\n%s", banned, body)
		}
	}
}
