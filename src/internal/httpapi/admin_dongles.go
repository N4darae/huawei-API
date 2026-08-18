package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/devops"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/reconcile"
	"github.com/n4darae/huawei-API/src/internal/store"
)

func (a *API) slotRows(ctx context.Context) (map[string]domain.SlotRow, map[string]domain.SlotRow, error) {
	rows, err := a.deps.Repos.Slots().List(ctx, a.deps.NodeID)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]domain.SlotRow, len(rows))
	byDongle := make(map[string]domain.SlotRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
		if r.Occupied() {
			byDongle[*r.DongleID] = r
		}
	}
	return byID, byDongle, nil
}

func (a *API) listSlots(w http.ResponseWriter, r *http.Request) {
	rows, err := a.deps.Repos.Slots().List(r.Context(), a.deps.NodeID)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := SlotList{Items: []Slot{}}
	for _, row := range rows {
		out.Items = append(out.Items, slotDTO(row))
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Slot < out.Items[j].Slot })
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) listDongles(w http.ResponseWriter, r *http.Request) {
	dongles, err := a.deps.Repos.Dongles().List(r.Context(), store.DongleFilter{NodeID: a.deps.NodeID})
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	_, byDongle, err := a.slotRows(r.Context())
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	active, err := reconcile.LoadActiveOps(r.Context(), a.deps.Repos)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	obs := a.snapshot()

	out := DongleList{Items: []Dongle{}}
	for _, d := range dongles {
		row := byDongle[d.ID]
		activeID := ""
		if op, ok := active[reconcile.OpKey(domain.SubjectDongle, d.ID)]; ok {
			activeID = op.ID
		}
		out.Items = append(out.Items, dongleDTO(d, row, obs.Devices[row.Slot], activeID))
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Slot < out.Items[j].Slot })
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) getDongle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "dongle_id")
	d, err := a.deps.Repos.Dongles().Get(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	_, byDongle, err := a.slotRows(r.Context())
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	row := byDongle[id]

	activeID := ""
	if op, err := a.deps.Repos.Operations().FindActive(r.Context(), domain.SubjectDongle, id); err == nil {
		activeID = op.ID
	}
	obs := a.snapshot().Devices[row.Slot]

	out := DongleDetail{Dongle: dongleDTO(d, row, obs, activeID)}
	out.Signal = signalDTO(obs.Signal)
	up, down := int64(0), int64(0)
	if u, dn, err := a.deps.Repos.Usage().SumDongleSince(r.Context(), id, devops.CycleStartDay(a.now(), d.CapResetDay)); err == nil {
		up, down = u, dn
	}
	out.Traffic = trafficDTO(obs.Traffic, up, down)
	if row.ID != "" {
		s := slotDTO(row)
		out.Slot = &s
	}
	if n, err := a.deps.Repos.SMS().CountUnread(r.Context(), id); err == nil {
		out.UnreadSMS = n
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) patchDongle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "dongle_id")
	var req DonglePatchRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}

	d, err := a.deps.Repos.Dongles().Get(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}

	if req.AutoRecoverEnabled != nil {
		if err := a.deps.Repos.Dongles().SetAutoRecover(r.Context(), id, *req.AutoRecoverEnabled); err != nil {
			writeError(w, r, translate(err))
			return
		}
		d.AutoRecoverEnabled = *req.AutoRecoverEnabled
	}
	if req.DataCapBytes != nil || req.CapResetDay != nil {
		capBytes := d.DataCapBytes
		if req.DataCapBytes != nil {
			capBytes = *req.DataCapBytes
		}
		resetDay := d.CapResetDay
		if req.CapResetDay != nil {
			resetDay = *req.CapResetDay
		}
		if resetDay < 1 || resetDay > 28 {
			writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "cap_reset_day must be between 1 and 28"))
			return
		}
		if err := a.deps.Repos.Dongles().SetDataCap(r.Context(), id, capBytes, resetDay); err != nil {
			writeError(w, r, translate(err))
			return
		}
		d.DataCapBytes, d.CapResetDay = capBytes, resetDay
	}
	if req.Carrier != nil {
		d.Carrier = strings.TrimSpace(*req.Carrier)
		if err := a.deps.Repos.Dongles().Update(r.Context(), d); err != nil {
			writeError(w, r, translate(err))
			return
		}
	}

	_, byDongle, err := a.slotRows(r.Context())
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	row := byDongle[id]
	out := dongleDTO(d, row, a.snapshot().Devices[row.Slot], "")
	a.publishDonglePatch(r.Context(), id, map[string]any{
		"auto_recover_enabled": out.AutoRecoverEnabled,
		"data_cap_bytes":       out.DataCapBytes,
		"cap_reset_day":        out.CapResetDay,
		"carrier":              out.Carrier,
	})
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) rebootDongle(w http.ResponseWriter, r *http.Request) {
	if a.deps.DevOps == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "device operations are not wired on this node"))
		return
	}
	id := chi.URLParam(r, "dongle_id")
	op, err := a.deps.DevOps.Reboot(context.WithoutCancel(r.Context()), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusAccepted, acceptedDTO(*op))
}

func (a *API) setDongleNetMode(w http.ResponseWriter, r *http.Request) {
	if a.deps.DevOps == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "device operations are not wired on this node"))
		return
	}
	id := chi.URLParam(r, "dongle_id")
	var req NetModeRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	mode := device.NetMode(strings.TrimSpace(strings.ToLower(req.NetMode)))
	if !mode.Valid() {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "net_mode must be auto, 2g, 3g or lte"))
		return
	}

	op, err := a.deps.DevOps.SetNetMode(context.WithoutCancel(r.Context()), id, mode)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusAccepted, acceptedDTO(*op))
}

func (a *API) setDongleLanIP(w http.ResponseWriter, r *http.Request) {
	if a.deps.DevOps == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "device operations are not wired on this node"))
		return
	}
	id := chi.URLParam(r, "dongle_id")
	var req LanIPRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	gw, err := netip.ParseAddr(strings.TrimSpace(req.Gateway))
	if err != nil || !gw.Is4() {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "gateway must be an IPv4 address"))
		return
	}

	op, opErr := a.deps.DevOps.SetLanIP(context.WithoutCancel(r.Context()), id, gw)
	if opErr != nil {
		writeError(w, r, translate(opErr))
		return
	}
	WriteJSON(w, http.StatusAccepted, acceptedDTO(*op))
}

func (a *API) listOperations(w http.ResponseWriter, r *http.Request) {
	filter := store.OperationFilter{
		Kind:      domain.OpKind(r.URL.Query().Get("kind")),
		State:     domain.OpState(r.URL.Query().Get("state")),
		Trigger:   domain.Trigger(r.URL.Query().Get("trigger")),
		SubjectID: r.URL.Query().Get("subject_id"),
		Limit:     50,
	}
	if n, ok := queryInt(r, "limit"); ok && n > 0 {
		filter.Limit = n
	}

	ops, err := a.deps.Repos.Operations().List(r.Context(), filter)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := OperationList{Items: []Operation{}}
	for _, o := range ops {
		out.Items = append(out.Items, operationDTO(o))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) getOperation(w http.ResponseWriter, r *http.Request) {
	op, err := a.deps.Repos.Operations().Get(r.Context(), chi.URLParam(r, "op_id"))
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	if op.Stalled(a.nowMS()) {
		op.State = domain.OpStalled
	}
	WriteJSON(w, http.StatusOK, operationDTO(op))
}

func (a *API) listRotations(w http.ResponseWriter, r *http.Request) {
	filter := store.RotationFilter{ProxyID: r.URL.Query().Get("proxy_id"), Limit: 50}
	if n, ok := queryInt(r, "limit"); ok && n > 0 {
		filter.Limit = n
	}
	rows, err := a.deps.Repos.Rotations().List(r.Context(), filter)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := RotationList{Items: []Rotation{}}
	for _, row := range rows {
		out.Items = append(out.Items, rotationDTO(row))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) listCustomers(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Repos.Customers().List(r.Context())
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := CustomerList{Items: []Customer{}}
	for _, c := range list {
		n, _ := a.deps.Repos.Customers().CountProxies(r.Context(), c.ID)
		out.Items = append(out.Items, customerDTO(c, n))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) createCustomer(w http.ResponseWriter, r *http.Request) {
	var req CustomerRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "a customer needs a name"))
		return
	}

	now := a.nowMS()
	c := domain.Customer{
		ID:        newID("cus"),
		Name:      strings.TrimSpace(req.Name),
		Contact:   strings.TrimSpace(req.Contact),
		Note:      strings.TrimSpace(req.Note),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.deps.Repos.Customers().Create(r.Context(), c); err != nil {
		writeError(w, r, translate(err))
		return
	}
	WriteJSON(w, http.StatusCreated, customerDTO(c, 0))
}

func (a *API) patchCustomer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "customer_id")
	var req CustomerRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	c, err := a.deps.Repos.Customers().Get(r.Context(), id)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		c.Name = strings.TrimSpace(req.Name)
	}
	c.Contact = strings.TrimSpace(req.Contact)
	c.Note = strings.TrimSpace(req.Note)
	if err := a.deps.Repos.Customers().Update(r.Context(), c); err != nil {
		writeError(w, r, translate(err))
		return
	}
	n, _ := a.deps.Repos.Customers().CountProxies(r.Context(), id)
	WriteJSON(w, http.StatusOK, customerDTO(c, n))
}

func (a *API) dongleExists(ctx context.Context, id string) error {
	_, err := a.deps.Repos.Dongles().Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Wrap(domain.ErrNotFound, "httpapi: dongle %q", id)
	}
	return err
}
