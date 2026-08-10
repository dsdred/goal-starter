package handlers

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/webui/security"
)

func TestSystemHandler_ServeIndex_RendersTemplate(t *testing.T) {
	// Build a minimal template FS that mirrors the real template structure.
	templateContent := `{{define "index.html"}}<!DOCTYPE html>
<html><head><title>GoAl</title></head>
<body><div id="login-modal"></div><div class="dashboard"></div></body></html>{{end}}`

	fsys := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte(templateContent)},
	}

	sup := process.NewSupervisor(nil)
	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)

	h := NewSystemHandler(sup, sess, csrf, insSvc)
	h.WithTemplateFS(fsys)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeIndex(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "login-modal") {
		t.Fatalf("expected rendered HTML to contain login-modal; got: %s", body)
	}
	if !strings.Contains(body, "dashboard") {
		t.Fatalf("expected rendered HTML to contain dashboard; got: %s", body)
	}
}

func TestSystemHandler_ServeIndex_ErrorOnMissingTemplate(t *testing.T) {
	sup := process.NewSupervisor(nil)
	sess := security.NewSessionStore()
	csrf := security.NewCSRF()
	insSvc := application.NewInstanceService(sup, nil)

	h := NewSystemHandler(sup, sess, csrf, insSvc)
	h.WithTemplateFS(fstest.MapFS{}) // empty FS

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeIndex(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestStaticServing_FromEmbeddedFS(t *testing.T) {
	// Verify the static subtree works with FileServerFS.
	fsys := fstest.MapFS{
		"static/app.js":    &fstest.MapFile{Data: []byte("console.log('app')")},
		"static/style.css": &fstest.MapFile{Data: []byte("body{color:#000}")},
	}

	sub, err := fs.Sub(fsys, "static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	ts := httptest.NewServer(http.StripPrefix("/static/", http.FileServerFS(sub)))
	defer ts.Close()

	for _, tc := range []struct{ path, want string }{
		{"/static/app.js", "console.log('app')"},
		{"/static/style.css", "body{color:#000}"},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d", tc.path, resp.StatusCode)
		}
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		if got := string(buf[:n]); got != tc.want {
			t.Fatalf("GET %s: expected %q, got %q", tc.path, tc.want, got)
		}
	}
}
