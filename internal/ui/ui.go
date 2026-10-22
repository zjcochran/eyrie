// Package ui serves the web app built from web/ (Svelte). Built with the
// tag "ui" (as the image is), the files are embedded in the binary; without
// it, a page says the app was not built in, so the Go code builds and tests
// without Node.js.
package ui

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// notBuilt is served in place of the app when the binary has none.
const notBuilt = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Eyrie</title></head>
<body><h1>Eyrie</h1><p>This build of Eyrie has no web interface. Build it with the tag <code>ui</code>,
as the Dockerfile does.</p></body></html>
`

// Handler serves the app in files (nil when there is none):
//
//   - a file that exists is served; files under assets/ have content hashes
//     in their names, so browsers keep them for a year;
//   - any other path without a file extension gets index.html, so the
//     app's own routes work on reload;
//   - index.html is never cached, so an upgrade takes effect at once.
func Handler(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if files == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(notBuilt))
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.ServeFileFS(w, r, files, name) //nolint:gosec // G703: name is cleaned and rooted, and fs.FS refuses ".." anyway
				return
			}
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, fs.ErrNotExist) {
				code = http.StatusNotFound
			}
			http.Error(w, http.StatusText(code), code)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
