// Package landash holds assets embedded into the binary.
//
// build.sh copies frontend/dist into backend/dist before `go build`, so the
// compiled SPA ships inside the single static binary. A placeholder file
// keeps the embed valid when the frontend has not been built yet.
package landash

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var staticFiles embed.FS

// StaticFS returns the built frontend rooted at dist/.
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFiles, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
