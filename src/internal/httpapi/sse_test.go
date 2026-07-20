package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
)

type frame struct {
	name string
	data string
}

func (h *harness) openStream(t *testing.T, query string) (chan frame, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+EventPath+query, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: h.cookie})

	res, err := h.srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		cancel()
		t.Fatalf("stream returned %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !contains(ct, "text/event-stream") {
		t.Fatalf("content type is %q", ct)
	}
	if res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("nginx buffers SSE to death without X-Accel-Buffering: no")
	}

	out := make(chan frame, 64)
	go func() {
		defer close(out)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				select {
				case out <- frame{name: name, data: strings.TrimPrefix(line, "data: ")}:
				default:
				}
			}
		}
	}()

	return out, func() { cancel(); res.Body.Close() }
}

func nextFrame(t *testing.T, stream chan frame, within time.Duration) frame {
	t.Helper()
	select {
	case f, ok := <-stream:
		if !ok {
			t.Fatal("the stream closed before a frame arrived")
		}
		return f
	case <-time.After(within):
		t.Fatalf("no frame within %s", within)
	}
	return frame{}
}

func TestStreamOpensWithHelloThenPings(t *testing.T) {
	h := newHarness(t)
	h.login()

	stream, closeStream := h.openStream(t, "")
	defer closeStream()

	hello := nextFrame(t, stream, 2*time.Second)
	if hello.name != string(eventbus.EvHello) {
		t.Fatalf("the first frame is %q, want hello", hello.name)
	}
	var env struct {
		Type string             `json:"type"`
		Data eventbus.HelloData `json:"data"`
	}
	if err := json.Unmarshal([]byte(hello.data), &env); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if env.Data.NodeID != "node-a" || env.Data.Product == "" || len(env.Data.Topics) == 0 {
		t.Fatalf("hello payload is %+v", env.Data)
	}

	ping := nextFrame(t, stream, 2*time.Second)
	if ping.name != EventPing {
		t.Fatalf("the second frame is %q, want a ping; without it a half open connection looks like a quiet farm", ping.name)
	}
	var pingEnv struct {
		Data PingData `json:"data"`
	}
	if err := json.Unmarshal([]byte(ping.data), &pingEnv); err != nil {
		t.Fatalf("decode ping: %v", err)
	}
	if pingEnv.Data.ServerTime == 0 {
		t.Fatal("the ping carries no server time")
	}
}

func TestStreamDeliversBusEvents(t *testing.T) {
	h := newHarness(t)
	h.login()

	stream, closeStream := h.openStream(t, "?topics=proxies")
	defer closeStream()

	if f := nextFrame(t, stream, 2*time.Second); f.name != string(eventbus.EvHello) {
		t.Fatalf("first frame is %q", f.name)
	}

	ev, err := eventbus.NewEvent("node-a", eventbus.EvProxyPatch, "px01", eventbus.PatchData{
		ID: "px01", Fields: map[string]any{"wan_ip": "100.71.8.8"},
	})
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	if err := h.bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f := nextFrame(t, stream, 2*time.Second)
		if f.name == string(eventbus.EvProxyPatch) {
			if !contains(f.data, "100.71.8.8") {
				t.Fatalf("patch payload is %q", f.data)
			}
			return
		}
	}
	t.Fatal("the proxy patch never arrived")
}

func TestStreamFiltersByTopic(t *testing.T) {
	h := newHarness(t)
	h.login()

	stream, closeStream := h.openStream(t, "?topics=dongles")
	defer closeStream()
	nextFrame(t, stream, 2*time.Second)

	ev, _ := eventbus.NewEvent("node-a", eventbus.EvProxyPatch, "px01", eventbus.PatchData{ID: "px01"})
	h.bus.Publish(context.Background(), ev)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case f := <-stream:
			if f.name == string(eventbus.EvProxyPatch) {
				t.Fatal("a proxies event reached a dongles only subscriber")
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestStreamNeedsASession(t *testing.T) {
	h := newHarness(t)

	res := h.do(http.MethodGet, EventPath, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("returned %d", res.StatusCode)
	}
}

func TestStreamClosesWhenTheSessionExpiresMidFlight(t *testing.T) {
	h := newHarness(t)
	h.login()

	stream, closeStream := h.openStream(t, "")
	defer closeStream()
	nextFrame(t, stream, 2*time.Second)

	if err := h.session.Revoke(context.Background(), h.cookie); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case _, open := <-stream:
			if !open {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("the stream stayed open after the session was revoked")
}
