package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlaceholderIsCommittedSoTheModuleCompiles(t *testing.T) {
	if _, err := fs.Stat(FS(), IndexFile); err != nil {
		t.Fatalf("dist/%s must be committed: go:embed is compile-time and a missing directory breaks the whole module: %v", IndexFile, err)
	}
}

func TestHandlerServesIndexForUnknownRoutes(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/proxies", "/dongles/d1"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s cache-control = %q, want no-store", path, got)
		}
	}
}

func TestHandlerRejectsWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the ui = %d, want 405", rec.Code)
	}
}
