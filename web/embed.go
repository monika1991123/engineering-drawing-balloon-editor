// Package web carries the browser frontend, compiled into the binary.
//
// pdf.js is vendored under vendor/ rather than pulled from a CDN so the whole
// application is one Go binary with no node toolchain, no install step and no
// network dependency at runtime. See vendor/LICENSE-pdfjs.
package web

import "embed"

//go:embed index.html app.js balloon-pdf.mjs styles.css vendor
var FS embed.FS
