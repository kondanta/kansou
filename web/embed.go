// Package web embeds the compiled Vue UI and exposes it as an fs.FS.
// The dist directory is populated by `just build-ui` (Docker + Vite).
// When the dist has not been built, DistDirFS contains only the placeholder
// .gitkeep and the server will fall back to the legacy UI automatically.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:tribbie/dist
var dist embed.FS

// DistDirFS is the embedded Vue UI rooted at the dist directory.
// Import this from internal/server to serve the frontend.
var DistDirFS = mustSubFS(dist, "tribbie/dist")

// mustSubFS returns the sub-filesystem rooted at dir.
// Panics if dir does not exist — this would indicate a broken embed path
// which is a programmer error caught at startup, not a runtime condition.
func mustSubFS(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic("web: failed to sub embedded FS at " + dir + ": " + err.Error())
	}
	return sub
}
