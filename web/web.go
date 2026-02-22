// Package web embeds static web assets and HTML templates into the Go binary.
// It provides a file system (FS) that allows the application to serve these
// assets directly without external file dependencies.
package web

import "embed"

//go:embed static templates
var FS embed.FS
