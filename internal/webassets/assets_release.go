//go:build release

package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist
var release embed.FS

var FS = mustSub(release, "dist")

func mustSub(source fs.FS, directory string) fs.FS {
	result, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return result
}
