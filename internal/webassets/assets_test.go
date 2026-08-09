package webassets

import (
	"io/fs"
	"testing"
)

func TestAssetsContainIndex(t *testing.T) {
	contents, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) == 0 {
		t.Fatal("embedded index is empty")
	}
}
