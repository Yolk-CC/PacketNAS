// Package web embeds the static frontend assets.
package web

import "embed"

// FS holds the embedded static frontend (index.html, app.js, style.css).
//
//go:embed static
var FS embed.FS
