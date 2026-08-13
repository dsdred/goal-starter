package webui

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestEmbeddedJavaScriptResetsLoginStateAcrossAuthTransitions(t *testing.T) {
	source, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	js := string(source)

	required := []string{
		"function resetLoginState()",
		"passwordEl.value = '';",
		"errorEl.textContent = '';",
		"errorEl.style.display = 'none';",
		"function showLoginError(message)",
		"function showLogin()",
		"showLoginError(data.error || 'Authentication failed');",
		"showLoginError('Login request failed: ' + err.message);",
	}
	for _, fragment := range required {
		if !strings.Contains(js, fragment) {
			t.Fatalf("embedded app.js does not contain required auth reset behavior %q", fragment)
		}
	}

	if got := strings.Count(js, "showLogin();"); got < 3 {
		t.Fatalf("showLogin() calls = %d, want at least 3 for logout and session-check failure paths", got)
	}
}
