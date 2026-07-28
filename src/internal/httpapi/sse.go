package httpapi

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/devops"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
)

const (
	EventPing        = "ping"
	SessionCheckStep = 4
)

type PingData struct {
	ServerTime int64 `json:"server_time"`
}

func newID(prefix string) string {
	var b [10]byte
	if _, err := crand.Read(b[:]); err != nil {
		return prefix + "_" + hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func (a *API) publish(ctx context.Context, kind eventbus.EventType, subject string, data any) {
	if a.deps.Bus == nil {
		return
	}
	ev, err := eventbus.NewEvent(a.deps.NodeID, kind, subject, data)
	if err != nil {
		return
	}
	a.deps.Bus.Publish(context.WithoutCancel(ctx), ev)
}

func (a *API) publishProxyPatch(ctx context.Context, id string, fields map[string]any) {
	a.publish(ctx, eventbus.EvProxyPatch, id, eventbus.PatchData{ID: id, Fields: fields})
}

func (a *API) publishDonglePatch(ctx context.Context, id string, fields map[string]any) {
	a.publish(ctx, eventbus.EvDonglePatch, id, eventbus.PatchData{ID: id, Fields: fields})
}

func (a *API) publishOp(ctx context.Context, op domain.Operation, kind eventbus.EventType) {
	a.publish(ctx, kind, op.ID, operationDTO(op))
}

func allEmittedSteps() []string {
	out := []string{}
	for _, s := range domain.RotateSteps() {
		out = append(out, string(s))
	}
	seen := map[string]bool{}
	for _, s := range out {
		seen[s] = true
	}
	for _, group := range [][]string{devops.RebootSteps(), devops.NetModeSteps(), devops.LanIPSteps()} {
		for _, s := range group {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func parseTopics(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return eventbus.AllTopics()
	}
	known := map[string]bool{}
	for _, t := range eventbus.AllTopics() {
		known[t] = true
	}
	out := make([]string, 0, len(known))
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if known[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return eventbus.AllTopics()
	}
	return out
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	if a.deps.Bus == nil {
		writeError(w, r, fail(http.StatusNotImplemented, CodeNotImplemented, "the event bus is not wired on this node"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, fail(http.StatusInternalServerError, CodeInternal, "this server cannot stream events"))
		return
	}

	topics := parseTopics(r.URL.Query().Get("topics"))
	stream, cancel, err := a.deps.Bus.Subscribe(r.Context(), topics)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-store, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sessionSecret := auth.CookieValue(r)
	hello, err := eventbus.NewEvent(a.deps.NodeID, eventbus.EvHello, a.deps.NodeID, eventbus.HelloData{
		NodeID:     a.deps.NodeID,
		ServerTime: a.nowMS(),
		Topics:     topics,
		Product:    config.Product,
	})
	if err != nil {
		return
	}
	if !writeEvent(w, string(eventbus.EvHello), hello) {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(a.deps.PingInterval)
	defer ticker.Stop()
	ticks := 0

	for {
		select {
		case <-r.Context().Done():
			return

		case ev, open := <-stream:
			if !open {
				return
			}
			if !writeEvent(w, string(ev.Type), ev) {
				return
			}
			flusher.Flush()

		case <-ticker.C:
			ticks++
			if ticks%SessionCheckStep == 0 && !a.sessionAlive(r.Context(), sessionSecret) {
				writeComment(w, "session expired")
				flusher.Flush()
				return
			}
			ping, err := eventbus.NewEvent(a.deps.NodeID, eventbus.EvSystemNotice, EventPing, PingData{ServerTime: a.nowMS()})
			if err != nil {
				return
			}
			ping.Type = EventPing
			if !writeEvent(w, EventPing, ping) {
				return
			}
			flusher.Flush()
		}
	}
}

func (a *API) sessionAlive(ctx context.Context, secret string) bool {
	if secret == "" {
		return true
	}
	_, err := a.deps.Sessions.Lookup(ctx, secret)
	return err == nil
}

func writeEvent(w io.Writer, name string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body); err != nil {
		return false
	}
	return true
}

func writeComment(w io.Writer, text string) {
	fmt.Fprintf(w, ": %s\n\n", text)
}
