package webui

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

//go:embed static/brand/*
var brandFS embed.FS

// The repository-root assets/brand directory is the canonical Owner-approved
// brand source; the embedded copies served by the binary must stay
// byte-identical to it.
func TestEmbeddedBrandAssetsMatchCanonical(t *testing.T) {
	canonical, err := filepath.Abs(filepath.Join("..", "..", "assets", "brand"))
	if err != nil {
		t.Fatalf("resolve canonical brand dir: %v", err)
	}
	if _, err := os.ReadDir(canonical); err != nil {
		t.Fatalf("read canonical brand dir: %v", err)
	}
	embeddedNames, err := fs.Glob(brandFS, "static/brand/*")
	if err != nil {
		t.Fatalf("glob embedded brand assets: %v", err)
	}
	if len(embeddedNames) == 0 {
		t.Fatal("no embedded brand assets found under static/brand/")
	}
	for _, name := range embeddedNames {
		file := filepath.Base(name)
		want, err := os.ReadFile(filepath.Join(canonical, file))
		if err != nil {
			t.Errorf("embedded static/brand/%s has no canonical counterpart in assets/brand/: %v", file, err)
			continue
		}
		got, err := brandFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("embedded static/brand/%s differs from canonical assets/brand/%s", file, file)
		}
	}
}
