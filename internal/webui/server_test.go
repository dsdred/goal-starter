package webui

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		"showLoginError(data.error ? translateServerMessage(data.error) : t('auth.login.failed'));",
		"showLoginError(t('auth.login.network_error'));",
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

func TestEmbeddedJavaScriptWindowExportsResolve(t *testing.T) {
	source, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	js := string(source)

	exportRe := regexp.MustCompile(`window\.(\w+)\s*=\s*(\w+);`)
	exports := exportRe.FindAllStringSubmatch(js, -1)
	if len(exports) == 0 {
		t.Fatal("no window exports found in app.js")
	}

	definedRe := regexp.MustCompile(`(?m)(?:function\s+(\w+)\b|const\s+(\w+)\s*=|let\s+(\w+)\s*=|var\s+(\w+)\s*=)`)
	defined := map[string]bool{}
	for _, m := range definedRe.FindAllStringSubmatch(js, -1) {
		for _, g := range m[1:] {
			if g != "" {
				defined[g] = true
			}
		}
	}

	for _, exp := range exports {
		prop, val := exp[1], exp[2]
		if prop != val {
			continue
		}
		if !defined[val] {
			t.Errorf("window.%s export references undefined symbol %q — would cause ReferenceError at init", prop, val)
		}
	}
}

func TestEmbeddedJavaScriptDOMIDsExistInTemplate(t *testing.T) {
	jsSrc, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	htmlSrc, err := fs.ReadFile(templateFS, "templates/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	js := string(jsSrc)
	html := string(htmlSrc)

	idRe := regexp.MustCompile(`getElementById\('([^']+)'\)`)
	ids := idRe.FindAllStringSubmatch(js, -1)
	if len(ids) == 0 {
		t.Fatal("no getElementById calls found in app.js")
	}

	seen := map[string]bool{}
	for _, m := range ids {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		if !strings.Contains(html, `id="`+id+`"`) && !strings.Contains(html, `id='`+id+`'`) {
			t.Errorf("getElementById('%s') in app.js has no matching id in index.html", id)
		}
	}
}
