package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

const IndexFile = "index.html"

func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

func Built() bool {
	b, err := fs.ReadFile(embedded, "dist/"+IndexFile)
	if err != nil {
		return false
	}
	return !strings.Contains(string(b), "SPA bundle not built")
}

func Handler() http.Handler {
	root := FS()
	files := http.FileServerFS(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			serveIndex(w, r, root)
			return
		}
		if st, err := fs.Stat(root, name); err != nil || st.IsDir() {
			serveIndex(w, r, root)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	b, err := fs.ReadFile(root, IndexFile)
	if err != nil {
		http.Error(w, "ui not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(b)
}
