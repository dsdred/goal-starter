package webui

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func loadI18nDict(t *testing.T, lang string) map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(staticFS, "static/i18n/"+lang+".json")
	if err != nil {
		t.Fatalf("read embedded i18n %s.json: %v", lang, err)
	}
	var dict map[string]string
	if err := json.Unmarshal(raw, &dict); err != nil {
		t.Fatalf("parse embedded i18n %s.json: %v", lang, err)
	}
	return dict
}

// TestI18nDictionariesParity enforces the hard i18n rule at build time: a raw
// key reaching the UI means a missing translation, so every key referenced by
// the template (data-i18n / data-i18n-placeholder / data-i18n-tooltip) or by a
// literal t('...') call in app.js must exist in BOTH dictionaries, and the
// RU/EN key sets must be identical.
func TestI18nDictionariesParity(t *testing.T) {
	ru := loadI18nDict(t, "ru")
	en := loadI18nDict(t, "en")

	ruKeys := make([]string, 0, len(ru))
	for k := range ru {
		ruKeys = append(ruKeys, k)
	}
	enKeys := make([]string, 0, len(en))
	for k := range en {
		enKeys = append(enKeys, k)
	}
	sort.Strings(ruKeys)
	sort.Strings(enKeys)

	for _, k := range ruKeys {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q exists in ru.json but not in en.json", k)
		}
	}
	for _, k := range enKeys {
		if _, ok := ru[k]; !ok {
			t.Errorf("key %q exists in en.json but not in ru.json", k)
		}
	}

	htmlSrc, err := fs.ReadFile(templateFS, "templates/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	jsSrc, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}

	used := map[string]bool{}
	attrRe := regexp.MustCompile(`data-i18n(?:-placeholder|-tooltip)?="([a-z0-9_.]+)"`)
	for _, m := range attrRe.FindAllStringSubmatch(string(htmlSrc), -1) {
		used[m[1]] = true
	}
	tCallRe := regexp.MustCompile(`(^|[^A-Za-z0-9_$])t\('([a-z0-9_.]+)'\)`)
	for _, m := range tCallRe.FindAllStringSubmatch(string(jsSrc), -1) {
		used[m[2]] = true
	}
	for _, k := range func() []string {
		out := make([]string, 0, len(used))
		for k := range used {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}() {
		if ru[k] == "" {
			t.Errorf("referenced i18n key %q is missing in ru.json", k)
		}
		if en[k] == "" {
			t.Errorf("referenced i18n key %q is missing in en.json", k)
		}
	}
}

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

func TestEmbeddedJavaScriptMonitorsServerConnection(t *testing.T) {
	source, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	js := string(source)

	required := []string{
		"getElementById('server-dot')",
		"getElementById('conn-banner')",
		"fetch('/api/v1/health'",
		"window.addEventListener('offline'",
		"window.addEventListener('online'",
		"if (serverOnline) probeServer();",
	}
	for _, fragment := range required {
		if !strings.Contains(js, fragment) {
			t.Fatalf("embedded app.js does not contain required connection-monitor behavior %q", fragment)
		}
	}
	if strings.Contains(js, "logEs.onerror = function () {};") {
		t.Fatal("embedded app.js still has the empty logEs.onerror handler (no connection feedback)")
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

// TestEmbeddedAssetsNoRejectedUIMarkers guards the Owner-accepted contract at
// the embed level: the rejected old UI markers (old primary-action labels with
// "+", the old wizard runtime keys, the pipeline "↯" marker, horizontal table
// scrolling) must not exist anywhere in the packaged assets.
func TestEmbeddedAssetsNoRejectedUIMarkers(t *testing.T) {
	jsSrc, err := fs.ReadFile(staticFS, "static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	cssSrc, err := fs.ReadFile(staticFS, "static/style.css")
	if err != nil {
		t.Fatalf("read embedded style.css: %v", err)
	}
	htmlSrc, err := fs.ReadFile(templateFS, "templates/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	assets := map[string]string{
		"app.js":     string(jsSrc),
		"style.css":  string(cssSrc),
		"index.html": string(htmlSrc),
	}
	rejected := []string{
		"+ Добавить модель",
		"+ Создать пайплайн",
		"+ Добавить Runtime",
		"+ Add model",
		"+ Create pipeline",
		"+ Add Runtime",
		"wizard.rt.existing",
		"wizard.rt.new'",
		"wizard.rt.select'",
		"wizard.rt.select\"",
		"Использовать существующий",
		"Создать новый Runtime",
		"↯",
	}
	for file, src := range assets {
		for _, marker := range rejected {
			if strings.Contains(src, marker) {
				t.Errorf("%s still contains rejected UI marker %q", file, marker)
			}
		}
	}
	if strings.Contains(string(cssSrc), "overflow-x: auto") {
		t.Error("style.css still uses overflow-x:auto (horizontal table scrolling is a forbidden state)")
	}

	required := map[string][]string{
		"app.js": {
			`data-tooltip="' + esc(label) + '"`,
			"getElementById('goaltip')",
			"scrollWidth <= wrap.clientWidth + 1",
			"bindFitObservers()",
			"ResizeObserver",
			"if (isPipelineModalOpen()) renderPlBuilder()",
			"if (isWizardOpen()) {",
		},
		"index.html": {"id=\"goaltip\"", "data-i18n=\"models.add\">Добавить<", "data-i18n=\"pipelines.add\">Добавить<", "data-i18n=\"runtimes.add\">Добавить<", "filter-bar-end", `class="table-fit"`},
		"style.css":  {".goal-tooltip", ".table-fit", ".filter-bar-end"},
	}
	for file, fragments := range required {
		for _, frag := range fragments {
			if !strings.Contains(assets[file], frag) {
				t.Errorf("%s is missing required fragment %q", file, frag)
			}
		}
	}
	if strings.Contains(string(jsSrc), `icon-btn-`+`' + color + '" title=`) {
		t.Error("iconBtn still sets a native title attribute (double tooltip with the custom tooltip)")
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
