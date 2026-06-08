// Package web embeds the built Svelte frontend so the single binary serves it.
package web

import "embed"

// Dist is the built frontend (web/dist), produced by `just web-build`.
//
//go:embed all:dist
var Dist embed.FS
