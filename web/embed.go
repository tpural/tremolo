// Package web embeds the built web app (npm run build writes web/dist).
// public/.gitkeep is copied into dist so the tracked placeholder survives builds.
package web

import "embed"

// Dist holds dist/, the built app.
//
//go:embed all:dist
var Dist embed.FS
