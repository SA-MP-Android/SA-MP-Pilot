//go:build !release

package webassets

import (
	"embed"
	"io/fs"
)

//go:embed fallback
var fallback embed.FS

var FS = mustSub(fallback, "fallback")

func mustSub(source fs.FS, directory string) fs.FS {
	result, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return result
}
