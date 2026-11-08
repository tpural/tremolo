// Package web embeds the built web app (npm run build writes web/dist).
package web

import "embed"

// Dist holds dist/, the built app.
//
//go:embed all:dist
var Dist embed.FS
