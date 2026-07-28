package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/device"
)

const (
	DefaultSMSPage = 1
	DefaultSMSSize = 20
	MaxSMSSize     = 200
)

func (a *API) listSms(w http.ResponseWriter, r *http.Request) {
	if a.deps.DevOps == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "device operations are not wired on this node"))
		return
	}
	id := chi.URLParam(r, "dongle_id")
	if err := a.dongleExists(r.Context(), id); err != nil {
		writeError(w, r, translate(err))
		return
	}

	box := device.SMSBoxInbox
	if n, ok := queryInt(r, "box"); ok {
		box = device.SMSBox(n)
	}
	if !box.Valid() {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "box must be 1 inbox, 2 outbox or 3 draft"))
		return
	}
	page := DefaultSMSPage
	if n, ok := queryInt(r, "page"); ok && n > 0 {
		page = n
	}
	size := DefaultSMSSize
	if n, ok := queryInt(r, "size"); ok && n > 0 {
		size = n
	}
	if size > MaxSMSSize {
		size = MaxSMSSize
	}

	msgs, total, err := a.deps.DevOps.SMSList(r.Context(), id, box, page, size)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	out := SmsList{Items: []Sms{}, Total: total}
	for _, m := range msgs {
		out.Items = append(out.Items, smsDTO(m))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) sendSms(w http.ResponseWriter, r *http.Request) {
	if a.deps.DevOps == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "device operations are not wired on this node"))
		return
	}
	id := chi.URLParam(r, "dongle_id")
	var req SmsSendRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}

	to := make([]string, 0, len(req.To))
	for _, n := range req.To {
		if n = strings.TrimSpace(n); n != "" {
			to = append(to, n)
		}
	}
	if len(to) == 0 {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "at least one recipient is required"))
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "the message body is empty"))
		return
	}

	if err := a.deps.DevOps.SMSSend(r.Context(), id, to, req.Body); err != nil {
		writeError(w, r, translate(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteSms(w http.ResponseWriter, r *http.Request) {
	a.smsIndexAction(w, r, func(id string, idx int64) error {
		return a.deps.DevOps.SMSDelete(r.Context(), id, idx)
	})
}

func (a *API) markSmsRead(w http.ResponseWriter, r *http.Request) {
	a.smsIndexAction(w, r, func(id string, idx int64) error {
		return a.deps.DevOps.SMSMarkRead(r.Context(), id, idx)
	})
}

func (a *API) smsIndexAction(w http.ResponseWriter, r *http.Request, fn func(string, int64) error) {
	if a.deps.DevOps == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "device operations are not wired on this node"))
		return
	}
	id := chi.URLParam(r, "dongle_id")
	var req SmsIndexRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}
	if req.Index <= 0 {
		writeError(w, r, fail(http.StatusBadRequest, CodeInvalidRequest, "index must be a positive message index"))
		return
	}
	if err := fn(id, req.Index); err != nil {
		writeError(w, r, translate(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
