package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/devops"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/reconcile"
	"github.com/n4darae/huawei-API/src/internal/rotate"
	"github.com/n4darae/huawei-API/src/internal/store"
)

type proxyView struct {
	proxy  domain.Proxy
	slot   domain.SlotRow
	dongle domain.Dongle
	dto    Proxy
}

func (a *API) buildProxies(ctx context.Context, filter store.ProxyFilter) ([]proxyView, error) {
	proxies, err := a.deps.Repos.Proxies().List(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(proxies) == 0 {
		return nil, nil
	}

	node, err := a.deps.Repos.Nodes().Get(ctx, a.deps.NodeID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	slots, err := a.deps.Repos.Slots().List(ctx, a.deps.NodeID)
	if err != nil {
		return nil, err
	}
	bySlotID := make(map[string]domain.SlotRow, len(slots))
	for _, s := range slots {
		bySlotID[s.ID] = s
	}

	dongles, err := a.deps.Repos.Dongles().List(ctx, store.DongleFilter{NodeID: a.deps.NodeID})
	if err != nil {
		return nil, err
	}
	byDongleID := make(map[string]domain.Dongle, len(dongles))
	for _, d := range dongles {
		byDongleID[d.ID] = d
	}

	active, err := reconcile.LoadActiveOps(ctx, a.deps.Repos)
	if err != nil {
		return nil, err
	}
	obs := a.snapshot()
	now := a.now()

	customers := map[string]string{}
	if list, err := a.deps.Repos.Customers().List(ctx); err == nil {
		for _, c := range list {
			customers[c.ID] = c.Name
		}
	}

	out := make([]proxyView, 0, len(proxies))
	for _, p := range proxies {
		row := bySlotID[p.SlotID]
		var dg domain.Dongle
		if row.Occupied() {
			dg = byDongleID[*row.DongleID]
		}

		v := proxyView{proxy: p, slot: row, dongle: dg}
		activeID := ""
		if op, ok := active[reconcile.OpKey(domain.SubjectProxy, p.ID)]; ok {
			activeID = op.ID
		}

		used := int64(0)
		if dg.ID != "" {
			if up, down, err := a.deps.Repos.Usage().SumDongleSince(ctx, dg.ID, devops.CycleStartDay(now, dg.CapResetDay)); err == nil {
				used = up + down
			}
		}

		v.dto = a.proxyDTO(p, row, dg, node.PublicHost, obs.Devices[row.Slot], obs.ProxyStatus[row.Slot], activeID, customers[deref(p.CustomerID)], used)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dto.Slot < out[j].dto.Slot })
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a *API) proxyDTO(p domain.Proxy, row domain.SlotRow, dg domain.Dongle, host netip.Addr,
	obs reconcile.DeviceObservation, st proxysup.Status, activeOpID, customerName string, usedBytes int64) Proxy {

	state := p.DesiredState(a.nowMS())
	if state == domain.ProxyStateActive && st.Unit != "" && !st.Healthy() {
		state = domain.ProxyStateDegraded
	}

	out := Proxy{
		ID:            p.ID,
		Slot:          row.Slot.Int(),
		State:         string(state),
		Host:          addrText(host),
		SocksPort:     p.SocksPort,
		HTTPPort:      p.HTTPPort,
		Username:      p.Username,
		AuthMode:      string(p.AuthMode),
		AuthIPCount:   len(p.AuthIPs),
		Enabled:       p.Enabled,
		Suspended:     p.Suspended,
		CustomerID:    p.CustomerID,
		CustomerName:  customerName,
		ExpiresAt:     p.ExpiresAt,
		WanIP:         addrText(obs.WanIP),
		SignalBars:    obs.Signal.Bars,
		DataUsedBytes: usedBytes,
		DataCapBytes:  dg.DataCapBytes,
		PortsBound:    portsBoundDTO(st),
		Policy:        policyDTO(p.Policy),
		UpdatedAt:     p.UpdatedAt,
	}
	if p.AuthMode.UsesUserPass() {
		out.Password = p.Password
	}
	if activeOpID != "" {
		out.ActiveOperationID = &activeOpID
	}
	return out
}

func (a *API) listProxies(w http.ResponseWriter, r *http.Request) {
	filter := store.ProxyFilter{CustomerID: r.URL.Query().Get("customer_id")}
	if days, ok := queryInt(r, "expiring_within_days"); ok {
		filter.ExpiringBeforeMS = a.nowMS() + int64(days)*86_400_000
	}

	views, err := a.buildProxies(r.Context(), filter)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}

	want := strings.TrimSpace(r.URL.Query().Get("state"))
	items := make([]Proxy, 0, len(views))
	for _, v := range views {
		if want != "" && v.dto.State != want {
			continue
		}
		items = append(items, v.dto)
	}
	WriteJSON(w, http.StatusOK, ProxyList{Items: items, Total: len(items)})
}

func (a *API) proxyView(ctx context.Context, id string) (proxyView, error) {
	if _, err := a.deps.Repos.Proxies().Get(ctx, id); err != nil {
		return proxyView{}, err
	}
	views, err := a.buildProxies(ctx, store.ProxyFilter{})
	if err != nil {
		return proxyView{}, err
	}
	for _, v := range views {
		if v.proxy.ID == id {
			return v, nil
		}
	}
	return proxyView{}, domain.Wrap(domain.ErrNotFound, "httpapi: proxy %q", id)
}

func (a *API) getProxy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	v, err := a.proxyView(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}

	out := ProxyDetail{Proxy: v.dto, AuthIPs: []AuthIP{}}
	if ips, err := a.deps.Repos.Proxies().ListAuthIPs(r.Context(), id); err == nil {
		for _, ip := range ips {
			out.AuthIPs = append(out.AuthIPs, authIPDTO(ip))
		}
	}
	if v.slot.ID != "" {
		s := slotDTO(v.slot)
		out.Slot = &s
	}
	if last, err := a.deps.Repos.Rotations().LastFor(r.Context(), id); err == nil {
		rot := rotationDTO(last)
		out.LastRotation = &rot
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) rotateProxyAdmin(w http.ResponseWriter, r *http.Request) {
	if a.deps.Rotator == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "rotation is not wired on this node"))
		return
	}
	id := chi.URLParam(r, "proxy_id")
	actor := ""
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		actor = sess.Username
	}

	op, err := a.deps.Rotator.Rotate(context.WithoutCancel(r.Context()), rotate.Request{
		ProxyID:   id,
		Trigger:   domain.TriggerAdminUI,
		ActorType: domain.ActorAdmin,
		ActorID:   actor,
		RequestID: requestID(r),
	})
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusAccepted, acceptedDTO(*op))
}

func (a *API) setProxyAuth(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	var req SetAuthRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}

	mode := domain.AuthMode(strings.TrimSpace(req.AuthMode))
	if !mode.Valid() {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "auth_mode must be userpass, iplist or both"))
		return
	}

	px, err := a.deps.Repos.Proxies().Get(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}

	if mode.UsesIPList() {
		ips, err := a.deps.Repos.Proxies().ListAuthIPs(r.Context(), id)
		if err != nil {
			writeError(w, r, translate(err))
			return
		}
		if len(ips) == 0 {
			writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest,
				"add at least one allowed network before switching this proxy to an IP whitelist, otherwise every customer is denied"))
			return
		}
	}

	username := px.Username
	if req.Username != nil && strings.TrimSpace(*req.Username) != "" {
		username = strings.TrimSpace(*req.Username)
	}
	password := px.Password
	switch {
	case req.Password != nil && *req.Password != "":
		password = *req.Password
	case req.RotatePassword:
		fresh, perr := proxysup.NewPassword()
		if perr != nil {
			writeError(w, r, translate(perr))
			return
		}
		password = fresh
	}

	if username != px.Username || password != px.Password {
		if err := a.deps.Repos.Proxies().SetCredentials(r.Context(), id, username, password); err != nil {
			writeError(w, r, translate(err))
			return
		}
	}
	if mode != px.AuthMode {
		if err := a.deps.Repos.Proxies().SetAuthMode(r.Context(), id, mode); err != nil {
			writeError(w, r, translate(err))
			return
		}
	}

	patch := map[string]any{
		"auth_mode": string(mode),
		"username":  username,
	}
	if mode.UsesUserPass() {
		patch["password"] = password
	}
	a.publishProxyPatch(r.Context(), id, patch)
	a.acceptSync(w, r, domain.OpSetAuth, domain.SubjectProxy, id)
}

func (a *API) listProxyAuthIPs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	if _, err := a.deps.Repos.Proxies().Get(r.Context(), id); err != nil {
		writeError(w, r, translate(err))
		return
	}
	a.writeAuthIPs(w, r, id)
}

func (a *API) addProxyAuthIP(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	var req AuthIPRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	prefix, err := parseCIDR(req.CIDR)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	if _, err := a.deps.Repos.Proxies().Get(r.Context(), id); err != nil {
		writeError(w, r, translate(err))
		return
	}

	entry := domain.ProxyAuthIP{
		ID:        newID("aip"),
		ProxyID:   id,
		CIDR:      prefix,
		Note:      strings.TrimSpace(req.Note),
		CreatedAt: a.nowMS(),
	}
	if err := a.deps.Repos.Proxies().AddAuthIP(r.Context(), entry); err != nil && !errors.Is(err, domain.ErrConflict) {
		writeError(w, r, translate(err))
		return
	}
	a.writeAuthIPs(w, r, id)
}

func (a *API) deleteProxyAuthIP(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	var req AuthIPRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	prefix, err := parseCIDR(req.CIDR)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	if err := a.deps.Repos.Proxies().DeleteAuthIP(r.Context(), id, prefix); err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(w, r, translate(err))
		return
	}
	a.writeAuthIPs(w, r, id)
}

func (a *API) writeAuthIPs(w http.ResponseWriter, r *http.Request, proxyID string) {
	ips, err := a.deps.Repos.Proxies().ListAuthIPs(r.Context(), proxyID)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := AuthIPList{Items: []AuthIP{}}
	for _, ip := range ips {
		out.Items = append(out.Items, authIPDTO(ip))
	}
	a.publishProxyPatch(r.Context(), proxyID, map[string]any{"auth_ip_count": len(out.Items)})
	WriteJSON(w, http.StatusOK, out)
}

func parseCIDR(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Prefix{}, domain.Wrap(domain.ErrInvalid, "httpapi: cidr is required")
	}
	if p, err := netip.ParsePrefix(raw); err == nil {
		return p.Masked(), nil
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Prefix{}, domain.Wrap(domain.ErrInvalid, "httpapi: %q is not an address or a cidr", raw)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func (a *API) setProxyPorts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	var req ProxyPolicy
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	policy := policyFrom(req)
	if err := policy.Validate(); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if _, err := a.deps.Repos.Proxies().Get(r.Context(), id); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if err := a.deps.Repos.Proxies().SetPolicy(r.Context(), id, policy); err != nil {
		writeError(w, r, translate(err))
		return
	}
	a.publishProxyPatch(r.Context(), id, map[string]any{"policy": policyDTO(policy)})
	a.acceptSync(w, r, domain.OpSetPorts, domain.SubjectProxy, id)
}

func (a *API) setProxyEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	var req EnableRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if err := a.deps.Repos.Proxies().SetEnabled(r.Context(), id, req.Enabled); err != nil {
		writeError(w, r, translate(err))
		return
	}
	a.writeProxy(w, r, id)
}

func (a *API) assignProxyCustomer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "proxy_id")
	var req AssignCustomerRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if req.CustomerID != nil && strings.TrimSpace(*req.CustomerID) == "" {
		req.CustomerID = nil
	}
	if err := a.deps.Repos.Proxies().SetCustomer(r.Context(), id, req.CustomerID, req.ExpiresAt); err != nil {
		writeError(w, r, translate(err))
		return
	}
	a.writeProxy(w, r, id)
}

func (a *API) writeProxy(w http.ResponseWriter, r *http.Request, id string) {
	v, err := a.proxyView(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	a.publishProxyPatch(r.Context(), id, map[string]any{
		"state":       v.dto.State,
		"enabled":     v.dto.Enabled,
		"suspended":   v.dto.Suspended,
		"customer_id": v.dto.CustomerID,
		"expires_at":  v.dto.ExpiresAt,
	})
	WriteJSON(w, http.StatusOK, v.dto)
}

func (a *API) selftestProxy(w http.ResponseWriter, r *http.Request) {
	if a.deps.Rotator == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "the egress prober is not wired on this node"))
		return
	}
	id := chi.URLParam(r, "proxy_id")

	res, err := a.deps.Rotator.Selftest(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}

	out := SelftestResult{
		SocksOK:   res.SocksOK,
		HTTPOK:    res.HTTPOK,
		EgressIP:  addrText(res.EgressIP),
		LatencyMS: res.LatencyMS,
		Error:     res.Error,
	}
	node, err := a.deps.Repos.Nodes().Get(r.Context(), a.deps.NodeID)
	if err == nil {
		out.EgressOK = res.EgressIP.IsValid() && res.EgressIP != node.PublicHost
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) acceptSync(w http.ResponseWriter, r *http.Request, kind domain.OpKind, subject domain.SubjectType, id string) {
	now := a.nowMS()
	op := domain.Operation{
		ID:          newID("op"),
		Kind:        kind,
		SubjectType: subject,
		SubjectID:   id,
		State:       domain.OpRunning,
		Step:        devops.StepDone,
		Pct:         100,
		StartedAt:   now,
		DeadlineAt:  now,
		Trigger:     domain.TriggerAdminUI,
		ActorType:   domain.ActorAdmin,
		RequestID:   requestID(r),
	}
	if err := a.deps.Repos.Operations().Create(r.Context(), op); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if err := a.deps.Repos.Operations().Finish(r.Context(), op.ID, domain.OpSucceeded, "", "{}", now); err != nil {
		writeError(w, r, translate(err))
		return
	}
	op.State = domain.OpSucceeded
	op.ResultJSON = "{}"
	op.FinishedAt = &now
	a.publishOp(r.Context(), op, eventbus.EvOpDone)
	WriteJSON(w, http.StatusAccepted, acceptedDTO(op))
}
