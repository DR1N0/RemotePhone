package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var assets embed.FS

// Handler serves the built admin SPA under /admin/. Unknown paths fall back
// to index.html so BrowserRouter deep links work.
func Handler() http.Handler {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(root))
	index, _ := fs.ReadFile(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/admin/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/admin")
		if path == "" || path == "/" {
			writeIndex(w, index)
			return
		}
		clean := strings.TrimPrefix(path, "/")
		if info, statErr := fs.Stat(root, clean); statErr == nil && !info.IsDir() {
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = path
			fileServer.ServeHTTP(w, r2)
			return
		}
		writeIndex(w, index)
	})
}

func writeIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}
