package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var assets embed.FS

func Handler() http.Handler {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("embedded web assets unavailable")
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'")
		if strings.HasPrefix(request.URL.Path, "/assets/") {
			files.ServeHTTP(writer, request)
			return
		}
		if !applicationRoute(request.URL.Path) {
			http.NotFound(writer, request)
			return
		}
		http.ServeFileFS(writer, request, root, "index.html")
	})
}

func applicationRoute(path string) bool {
	switch path {
	case "/", "/repositories", "/account", "/packs", "/categories", "/explore", "/mcp-settings", "/configuration", "/server-logging", "/server-backup":
		return true
	}
	id, ok := strings.CutPrefix(path, "/explore/")
	if !ok || len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, letter := range id {
		if !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '-' || letter == '_') {
			return false
		}
	}
	return true
}
