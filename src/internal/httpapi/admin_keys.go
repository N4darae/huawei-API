package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func (a *API) listApiKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.deps.Keys.List(r.Context())
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := ApiKeyList{Items: []ApiKey{}}
	for _, k := range keys {
		out.Items = append(out.Items, keyDTO(k))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) createApiKey(w http.ResponseWriter, r *http.Request) {
	var req ApiKeyRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if req.CustomerID != nil && strings.TrimSpace(*req.CustomerID) == "" {
		req.CustomerID = nil
	}
	if req.CustomerID != nil {
		if _, err := a.deps.Repos.Customers().Get(r.Context(), *req.CustomerID); err != nil {
			writeError(w, r, translate(err))
			return
		}
	}
	for _, id := range req.ProxyIDs {
		if _, err := a.deps.Repos.Proxies().Get(r.Context(), id); err != nil {
			writeError(w, r, translate(err))
			return
		}
	}

	key, secret, err := a.deps.Keys.Create(r.Context(), auth.NewKey{
		Name:       req.Name,
		CustomerID: req.CustomerID,
		Scopes:     req.Scopes,
		ProxyIDs:   req.ProxyIDs,
	})
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusCreated, ApiKeyCreated{Key: keyDTO(key), Secret: secret})
}

func (a *API) revokeApiKey(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Keys.Revoke(r.Context(), chi.URLParam(r, "key_id")); err != nil {
		writeError(w, r, translate(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) createLinkToken(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "key_id")
	key, err := a.deps.Keys.Get(r.Context(), keyID)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	if key.Revoked() {
		writeError(w, r, fail(http.StatusConflict, CodeKeyRevoked, "this api key was revoked, issue a new key before making a link"))
		return
	}
	if !key.HasScope(auth.ScopeRotate) {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest,
			"a rotate link needs the rotate scope on its api key"))
		return
	}
	if len(key.ProxyIDs) != 1 {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest,
			"a rotate link needs an api key scoped to exactly one proxy"))
		return
	}

	token, secret, err := a.deps.Keys.CreateLinkToken(r.Context(), keyID)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusCreated, LinkTokenCreated{
		Token: linkTokenDTO(token),
		URL:   LinkBase + "/" + secret,
	})
}

func (a *API) revokeLinkToken(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Keys.RevokeLinkToken(r.Context(), chi.URLParam(r, "token_id")); err != nil {
		writeError(w, r, translate(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func keyMayUse(key auth.Key, proxyID string) error {
	if key.CoversProxy(proxyID) {
		return nil
	}
	return domain.Wrap(domain.ErrForbidden, "httpapi: this api key is not allowed on proxy %q", proxyID)
}
