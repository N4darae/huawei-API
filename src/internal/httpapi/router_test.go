package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRouterMountsInArgumentOrder(t *testing.T) {
	var order []string
	first := MounterFunc(func(r chi.Router) {
		order = append(order, "first")
		r.Get(APIBase+"/first", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})
	second := MounterFunc(func(r chi.Router) {
		order = append(order, "second")
		r.Get(LinkBase+"/{link_token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})

	h := NewRouter(nil, first, nil, second)
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("mount order = %v", order)
	}

	for _, path := range []string{APIBase + "/first", LinkBase + "/tok"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s = %d, want 204", path, rec.Code)
		}
	}
}

func TestHealthzIsAlwaysMounted(t *testing.T) {
	h := NewRouter(func(*http.Request) (int, any) {
		return http.StatusServiceUnavailable, map[string]any{"status": "degraded"}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, APIBase+"/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("api responses must not be cached, got %q", got)
	}
}

func TestUnknownPathFallsBackToSPA(t *testing.T) {
	h := NewRouter(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/proxies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("spa fallback = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("spa fallback content type = %q", ct)
	}
}

func TestUnknownAPIPathIsJSON404NotTheSPA(t *testing.T) {
	h := NewRouter(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, APIBase+"/proxies", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmounted api route = %d, want 404; serving the SPA here hides a typo behind a 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("unmounted api route content type = %q, want json", ct)
	}
}

func TestWrongMethodOnAPIIsJSON405(t *testing.T) {
	h := NewRouter(nil, MounterFunc(func(r chi.Router) {
		r.Post(APIBase+"/things", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, APIBase+"/things", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d, want 405", rec.Code)
	}
}
