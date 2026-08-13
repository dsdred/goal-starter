package webui

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEmbeddedJavaScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	source, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	path := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	if output, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("app.js syntax check failed: %v\n%s", err, output)
	}
}
