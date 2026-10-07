// Package web embeds the single-page console.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// FS returns the static files rooted at the console's index.html.
func FS() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
