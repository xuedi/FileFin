// Package web embeds the built Svelte frontend so the whole product ships as one
// binary. Run `just web-build` to (re)generate web/dist.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
