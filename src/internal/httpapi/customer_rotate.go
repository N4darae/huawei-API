package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/rotate"
)

func (a *API) rotateProxyCustomer(w http.ResponseWriter, r *http.Request) {
	key, ok := auth.KeyFrom(r.Context())
	if !ok {
		writeError(w, r, fail(http.StatusUnauthorized, CodeUnauthorized, "api key is not valid"))
		return
	}
	proxyID := chi.URLParam(r, "proxy_id")
	if err := keyMayUse(key, proxyID); err != nil {
		writeError(w, r, translate(err))
		return
	}
	a.customerRotate(w, r, proxyID, key.ID, queryBool(r, "wait"))
}

func (a *API) customerRotate(w http.ResponseWriter, r *http.Request, proxyID, actorID string, wait bool) {
	if a.deps.Rotator == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "rotation is not wired on this node"))
		return
	}
	if err := a.customerProxyUsable(r.Context(), proxyID); err != nil {
		writeError(w, r, translate(err))
		return
	}

	op, err := a.deps.Rotator.Rotate(context.WithoutCancel(r.Context()), rotate.Request{
		ProxyID:   proxyID,
		Trigger:   domain.TriggerCustomerAPI,
		ActorType: domain.ActorAPIKey,
		ActorID:   actorID,
		RequestID: requestID(r),
	})
	if err != nil {
		writeError(w, r, translate(err))
		return
	}

	if !wait || a.deps.Waiter == nil {
		WriteJSON(w, http.StatusAccepted, acceptedDTO(*op))
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), WaitCap)
	defer cancel()

	done, err := a.deps.Waiter.Wait(ctx, op.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			WriteJSON(w, http.StatusAccepted, acceptedDTO(*op))
			return
		}
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusOK, rotateResultDTO(done))
}

func rotateResultDTO(op domain.Operation) RotateResult {
	out := RotateResult{OperationID: op.ID, Result: string(domain.RotationFailed), Error: op.Error}

	bag := decodeResult(op.ResultJSON)
	if v, ok := bag["result"].(string); ok && v != "" {
		out.Result = v
	}
	if v, ok := bag["ip_changed"].(bool); ok {
		out.IPChanged = v
	}
	out.OldIP = stringField(bag, "old_public_ip", "old_ip")
	out.NewIP = stringField(bag, "new_public_ip", "new_ip")
	if v, ok := bag["duration_ms"].(float64); ok {
		out.DurationMS = int(v)
	}
	if out.Result == string(domain.RotationUnchanged) {
		out.Error = ""
	}
	if op.State == domain.OpCanceled && out.Result == "" {
		out.Result = string(domain.RotationFailed)
	}
	return out
}

func stringField(bag map[string]any, names ...string) string {
	for _, n := range names {
		if v, ok := bag[n].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (a *API) customerProxyUsable(ctx context.Context, proxyID string) error {
	px, err := a.deps.Repos.Proxies().Get(ctx, proxyID)
	if err != nil {
		return err
	}
	switch px.DesiredState(a.nowMS()) {
	case domain.ProxyStateExpired:
		return domain.Wrap(domain.ErrExpired, "httpapi: proxy %q has expired", proxyID)
	case domain.ProxyStateDisabled, domain.ProxyStateSuspended:
		return domain.Wrap(domain.ErrForbidden, "httpapi: proxy %q is not active", proxyID)
	}
	return nil
}

func (a *API) customerStatus(w http.ResponseWriter, r *http.Request) {
	key, ok := auth.KeyFrom(r.Context())
	if !ok {
		writeError(w, r, fail(http.StatusUnauthorized, CodeUnauthorized, "api key is not valid"))
		return
	}
	proxyID := chi.URLParam(r, "proxy_id")
	if err := keyMayUse(key, proxyID); err != nil {
		writeError(w, r, translate(err))
		return
	}

	px, err := a.deps.Repos.Proxies().Get(r.Context(), proxyID)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	node, err := a.deps.Repos.Nodes().Get(r.Context(), a.deps.NodeID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(w, r, translate(err))
		return
	}

	out := CustomerStatus{
		ProxyID:            px.ID,
		State:              string(px.DesiredState(a.nowMS())),
		Host:               addrText(node.PublicHost),
		SocksPort:          px.SocksPort,
		HTTPPort:           px.HTTPPort,
		ExpiresAt:          px.ExpiresAt,
		MinRotateIntervalS: int(a.deps.MinRotateInterval / time.Second),
	}

	if row, err := a.deps.Repos.Slots().Get(r.Context(), px.SlotID); err == nil {
		out.WanIP = addrText(a.snapshot().Devices[row.Slot].WanIP)
	}
	if last, err := a.deps.Repos.Rotations().LastFor(r.Context(), proxyID); err == nil {
		out.LastRotatedAt = last.RequestedAt
		out.RotateAvailableAt = last.RequestedAt + a.deps.MinRotateInterval.Milliseconds()
	}
	WriteJSON(w, http.StatusOK, out)
}
