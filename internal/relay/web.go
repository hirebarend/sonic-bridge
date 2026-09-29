package relay

import (
	"embed"
	"io/fs"
	"net/http"
)

// The player is embedded from source, so a deployment is a single artefact and
// there is no asset build step. Each file is listed explicitly: renaming one
// then becomes a compile error rather than a 404 a listener discovers.
//
//go:embed web/index.html
//go:embed web/codec.js
//go:embed web/playback-processor.js
//go:embed web/favicon.svg
var playerFiles embed.FS

// newWebHandler serves the browser player. It is a file server rather than one
// handler so that GET / resolves to index.html, unknown paths return 404
// instead of the page with status 200, and the worklet gets a JavaScript
// content type, which AudioWorklet.addModule requires.
func newWebHandler() http.Handler {
	assets, err := fs.Sub(playerFiles, "web")
	if err != nil {
		panic(err)
	}

	files := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// No filename carries a content hash, so caching would pin a stale
		// player in every browser across a deploy.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, req)
	})
}
