package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/n4darae/huawei-API/src/internal/webui"
)

const (
	APIBase   = "/api/v1"
	LinkBase  = "/r"
	EventPath = APIBase + "/events"
)

type peerAddrKey struct{}

func withPeerAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), peerAddrKey{}, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type Mounter interface {
	Mount(r chi.Router)
}

type MounterFunc func(r chi.Router)

func (f MounterFunc) Mount(r chi.Router) { f(r) }

type HealthFunc func(r *http.Request) (status int, body any)

func NewRouter(health HealthFunc, mods ...Mounter) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(withPeerAddr)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(noStoreAPI)

	r.Get(APIBase+"/healthz", healthHandler(health))

	for _, m := range mods {
		if m == nil {
			continue
		}
		m.Mount(r)
	}

	ui := webui.Handler()
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			WriteJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "unknown endpoint"})
			return
		}
		ui.ServeHTTP(w, req)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed", "message": req.Method + " is not allowed here"})
	})
	return r
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, APIBase+"/") || p == APIBase
}

func healthHandler(health HealthFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		var body any = map[string]string{"status": "ok"}
		if health != nil {
			status, body = health(r)
		}
		WriteJSON(w, status, body)
	}
}

func noStoreAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		next.ServeHTTP(w, r)
	})
}

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(body)
}
