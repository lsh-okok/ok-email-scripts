package web

import (
	"io/fs"
	"os"
	"testing"
)

func TestAllProductionAssetsAreEmbedded(t *testing.T) {
	// Vite may generate helper chunks beginning with '_'. A normal directory
	// embed silently omits these, leaving valid builds with broken routes.
	err := fs.WalkDir(os.DirFS("dist"), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if _, err := fs.Stat(Assets, "dist/"+path); err != nil {
			t.Errorf("production asset is missing from the executable: %s: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
