package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/n4darae/huawei-API/src/internal/webui"
)

const (
	APIBase   = "/api/v1"
	LinkBase  = "/r"
	EventPath = APIBase + "/events"
)

type Mounter interface {
	Mount(r chi.Router)
}

type MounterFunc func(r chi.Router)

func (f MounterFunc) Mount(r chi.Router) { f(r) }

type HealthFunc func(r *http.Request) (status int, body any)

func NewRouter(health HealthFunc, mods ...Mounter) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
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

	r.NotFound(webui.Handler().ServeHTTP)
	return r
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
		if len(r.URL.Path) >= len(APIBase) && r.URL.Path[:len(APIBase)] == APIBase {
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
