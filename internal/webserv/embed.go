package webserv

import (
	"embed"
	"io/fs"
)

// dist holds the built React bundle (web/ compiled by Vite into ./dist).
// Rebuild with: cd web && npm run build. The single-binary promise (§5.4)
// includes the frontend — no separate asset directory to deploy.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the embedded frontend as an fs.FS rooted at the bundle,
// suitable for Config.Static. Returns nil if the bundle wasn't built.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	// Guard: if dist is empty (never built), report nil so the server
	// serves API-only rather than 404ing every page.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
