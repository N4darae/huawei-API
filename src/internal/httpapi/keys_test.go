package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/auth"
)

func TestApiKeySecretIsShownExactlyOnce(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/keys", ApiKeyRequest{
		Name: "Acme production", Scopes: []string{auth.ScopeRotate, auth.ScopeStatus}, ProxyIDs: []string{"px01"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var created ApiKeyCreated
	res.decode(t, &created)
	if !strings.HasPrefix(created.Secret, auth.KeyPrefix) {
		t.Fatalf("secret %q has no dgl_live_ prefix", created.Secret)
	}
	if created.Key.Prefix == created.Secret {
		t.Fatal("the listing prefix must be a short label, not the whole secret")
	}

	var list ApiKeyList
	h.getJSON(APIBase+"/keys", &list)
	if contains(mustText(t, list), created.Secret) {
		t.Fatal("GET /keys returned the secret; shown once is then a lie")
	}
	for _, k := range list.Items {
		if k.ID == created.Key.ID && k.Prefix != created.Key.Prefix {
			t.Fatalf("prefix changed between create and list: %q vs %q", k.Prefix, created.Key.Prefix)
		}
	}
}

func TestApiKeyCreateAcceptsTheScopesTheFormOffers(t *testing.T) {
	h := newHarness(t)
	h.login()

	for _, scopes := range [][]string{{"rotate"}, {"status"}, {"rotate", "status"}} {
		res := h.do(http.MethodPost, APIBase+"/keys", ApiKeyRequest{Name: "k", Scopes: scopes})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("scopes %v returned %d: %s", scopes, res.StatusCode, res.text())
		}
	}
}

func TestApiKeyCreateRejectsAnUnknownScope(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/keys", ApiKeyRequest{Name: "k", Scopes: []string{"admin"}})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestApiKeyCreateRejectsAnUnknownProxy(t *testing.T) {
	h := newHarness(t)
	h.login()

	res := h.do(http.MethodPost, APIBase+"/keys", ApiKeyRequest{
		Name: "k", Scopes: []string{auth.ScopeRotate}, ProxyIDs: []string{"px99"},
	})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestRevokingAKeyMarksItAndKeepsItListed(t *testing.T) {
	h := newHarness(t)
	h.login()

	key, _ := h.newKey("acme", []string{auth.ScopeRotate}, nil)
	if res := h.do(http.MethodDelete, APIBase+"/keys/"+key.ID, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("returned %d", res.StatusCode)
	}

	var list ApiKeyList
	h.getJSON(APIBase+"/keys", &list)
	for _, k := range list.Items {
		if k.ID == key.ID {
			if k.RevokedAt == nil {
				t.Fatal("the key is still listed as live after being revoked")
			}
			return
		}
	}
	t.Fatal("the revoked key vanished from the listing")
}

func TestLinkTokenURLCarriesASecretThatIsNotTheTokenID(t *testing.T) {
	h := newHarness(t)
	h.login()

	key, _ := h.newKey("acme", []string{auth.ScopeRotate}, []string{"px01"})
	res := h.do(http.MethodPost, APIBase+"/keys/"+key.ID+"/link-tokens", nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("returned %d: %s", res.StatusCode, res.text())
	}
	var created LinkTokenCreated
	res.decode(t, &created)

	secret := strings.TrimPrefix(created.URL, LinkBase+"/")
	if secret == "" || secret == created.Token.ID {
		t.Fatalf("url %q must carry a secret different from the token id %q", created.URL, created.Token.ID)
	}

	var list ApiKeyList
	h.getJSON(APIBase+"/keys", &list)
	if contains(mustText(t, list), secret) {
		t.Fatal("GET /keys leaked the link token secret")
	}
	for _, k := range list.Items {
		if k.ID != key.ID {
			continue
		}
		if len(k.LinkTokens) != 1 || k.LinkTokens[0].ID != created.Token.ID {
			t.Fatalf("link tokens are %+v", k.LinkTokens)
		}
	}
}

func TestALinkTokenIsRevokedIndependentlyOfItsKey(t *testing.T) {
	h := newHarness(t)
	h.login()

	key, keySecret := h.newKey("acme", []string{auth.ScopeRotate, auth.ScopeStatus}, []string{"px01"})
	create := h.do(http.MethodPost, APIBase+"/keys/"+key.ID+"/link-tokens", nil)
	var created LinkTokenCreated
	create.decode(t, &created)
	linkSecret := strings.TrimPrefix(created.URL, LinkBase+"/")

	if res := h.do(http.MethodGet, LinkBase+"/"+linkSecret, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("the fresh link returned %d", res.StatusCode)
	}

	if res := h.do(http.MethodDelete, APIBase+"/link-tokens/"+created.Token.ID, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke returned %d", res.StatusCode)
	}

	if res := h.do(http.MethodGet, LinkBase+"/"+linkSecret, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("the revoked link returned %d, want 404", res.StatusCode)
	}
	if res := h.bearer(http.MethodGet, APIBase+"/status/px01", keySecret); res.StatusCode != http.StatusOK {
		t.Fatalf("revoking the link killed its api key too: %d", res.StatusCode)
	}
}

func TestALinkTokenNeedsARotateScopedKey(t *testing.T) {
	h := newHarness(t)
	h.login()

	key, _ := h.newKey("read only", []string{auth.ScopeStatus}, []string{"px01"})
	res := h.do(http.MethodPost, APIBase+"/keys/"+key.ID+"/link-tokens", nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

// linkTarget resolves a link to key.ProxyIDs[0], so a link only means anything for a key
// scoped to exactly one proxy. A key's proxy scope is fixed at creation, so minting a token
// for any other key hands out a URL that can never work.
func TestALinkTokenNeedsAKeyScopedToOneProxy(t *testing.T) {
	h := newHarness(t)
	h.login()

	for _, tc := range []struct {
		name     string
		proxyIDs []string
	}{
		{"unrestricted", nil},
		{"two proxies", []string{"px01", "px02"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, _ := h.newKey(tc.name, []string{auth.ScopeRotate}, tc.proxyIDs)
			res := h.do(http.MethodPost, APIBase+"/keys/"+key.ID+"/link-tokens", nil)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("returned %d, want 400: %s", res.StatusCode, res.text())
			}
		})
	}

	key, _ := h.newKey("one proxy", []string{auth.ScopeRotate}, []string{"px01"})
	res := h.do(http.MethodPost, APIBase+"/keys/"+key.ID+"/link-tokens", nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("a single-proxy key returned %d, want 201: %s", res.StatusCode, res.text())
	}
}

func TestRevokingAnUnknownLinkTokenIs404(t *testing.T) {
	h := newHarness(t)
	h.login()

	if res := h.do(http.MethodDelete, APIBase+"/link-tokens/lt-nope", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestKeysAreListedNewestFirstWithNoHashes(t *testing.T) {
	h := newHarness(t)
	h.login()

	h.newKey("first", []string{auth.ScopeStatus}, nil)
	h.clock.Advance(time.Second)
	h.newKey("second", []string{auth.ScopeStatus}, nil)

	res := h.do(http.MethodGet, APIBase+"/keys", nil)
	if contains(res.text(), "argon2") || contains(res.text(), "secret_hash") {
		t.Fatalf("the listing leaked hash material: %s", res.text())
	}
	var list ApiKeyList
	res.decode(t, &list)
	if len(list.Items) != 2 || list.Items[0].Name != "second" {
		t.Fatalf("keys are %+v", list.Items)
	}
}

func TestKeyLastUsedAtIsRecorded(t *testing.T) {
	h := newHarness(t)
	key, secret := h.newKey("acme", []string{auth.ScopeStatus}, nil)
	h.login()

	if res := h.bearer(http.MethodGet, APIBase+"/status/px01", secret); res.StatusCode != http.StatusOK {
		t.Fatalf("returned %d", res.StatusCode)
	}

	stored, err := h.keys.Get(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if stored.LastUsedAt == nil {
		t.Fatal("last_used_at was never written")
	}
}

func mustText(t *testing.T, v any) string {
	t.Helper()
	raw, err := jsonMarshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
