package webui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // register hooks in default server
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/version"
	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/handlers"
	"github.com/dsdred/goal/internal/webui/health"
	"github.com/dsdred/goal/internal/webui/security"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// App holds server dependencies and state.
type App struct {
	cfg           *config.Config
	configPath    string
	supervisor    *process.Supervisor
	instanceSvc   *application.InstanceService
	runtimeSvc    *application.RuntimeService
	modelSvc      *application.ModelService
	pipelineSvc   *application.PipelineService
	repo          storage.Repository
	csrf          *security.CSRF
	passwordStore *security.PasswordStore
	sessionStore  *security.SessionStore
	hc            *health.HealthChecker
	reg           *handlers.RouteRegistry
	authEnabled   bool
	auditLog      *audit.AuditLogger
}

// SetConfigPath sets the config file path for settings save.
func (a *App) SetConfigPath(path string) {
	a.configPath = path
}

// NewApp creates server dependencies.
func NewApp(cfg *config.Config, repo storage.Repository, supervisor *process.Supervisor) (*App, error) {
	instanceSvc := application.NewInstanceService(supervisor, repo)
	runtimeSvc := application.NewRuntimeService(repo)
	modelSvc := application.NewModelService(repo)
	pipelineSvc := application.NewPipelineService(supervisor, repo)

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}

	a := &App{
		cfg:           cfg,
		supervisor:    supervisor,
		instanceSvc:   instanceSvc,
		runtimeSvc:    runtimeSvc,
		modelSvc:      modelSvc,
		pipelineSvc:   pipelineSvc,
		repo:          repo,
		csrf:          security.NewCSRF(),
		passwordStore: security.NewPasswordStore(),
		sessionStore:  security.NewSessionStore(),
		authEnabled:   cfg.AuthEnabled,
		auditLog:      audit.New(filepath.Join(dataDir, audit.FileName)),
	}

	// Set admin password hash from config.
	if cfg.AdminPasswordHash != "" {
		if err := a.passwordStore.SetHash(cfg.AdminUser, cfg.AdminPasswordHash); err != nil {
			return nil, fmt.Errorf("set admin password hash: %w", err)
		}
	}

	// Init health checker.
	a.hc = health.NewHealthChecker()
	a.hc.UpdateRuntimes(a.buildRuntimeDefs())

	return a, nil
}

// Router returns the HTTP handler using the new RouteRegistry architecture.
func (a *App) Router() http.Handler {
	if a.reg != nil {
		return a.reg.Build()
	}
	return http.NotFoundHandler()
}

// InitRegistry creates the route registry.
func (a *App) InitRegistry() {
	a.reg = handlers.NewRouteRegistry(
		a.instanceSvc,
		a.runtimeSvc,
		a.modelSvc,
		a.pipelineSvc,
		a.supervisor,
		a.repo,
		a.csrf,
		a.sessionStore,
		a.passwordStore,
		handlers.WithAuthEnabled(a.authEnabled),
		handlers.WithWebAssets(templateFS, staticFS),
		handlers.WithServerInfo(a.cfg.ListenAddress, a.cfg.WebPort, a.authEnabled),
		handlers.WithConfigPath(a.configPath),
		handlers.WithLiveConfig(a.cfg),
		handlers.WithAuditLogger(a.auditLog),
	)
}

// AuditLogger returns the app's durable audit logger (ADR 007).
func (a *App) AuditLogger() *audit.AuditLogger {
	return a.auditLog
}

// CloseAudit closes the audit log file. It is idempotent.
func (a *App) CloseAudit() error {
	if a.auditLog == nil {
		return nil
	}
	return a.auditLog.Close()
}

// StartHealthChecker starts background health checks.
func (a *App) StartHealthChecker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	a.refreshHealthChecks()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshHealthChecks()
		}
	}
}

func (a *App) buildRuntimeDefs() []health.RuntimeDef {
	runtimes, err := a.repo.ListRuntimes()
	if err != nil {
		return nil
	}
	rtNames := make(map[string]string)
	for _, rt := range runtimes {
		rtNames[rt.ID] = rt.Name
	}
	models, _ := a.repo.ListModels()
	defsMap := make(map[string]health.RuntimeDef)
	for _, m := range models {
		host, port := extractHostPort(m.Args)
		if port == 0 {
			continue
		}
		name, ok := rtNames[m.RuntimeID]
		if !ok {
			name = m.RuntimeID
		}
		defsMap[m.RuntimeID] = health.RuntimeDef{
			ID:   m.RuntimeID,
			Name: name,
			Host: host,
			Port: port,
		}
	}
	defs := make([]health.RuntimeDef, 0, len(defsMap))
	for _, d := range defsMap {
		defs = append(defs, d)
	}
	return defs
}

func extractHostPort(args []string) (string, int) {
	host := "127.0.0.1"
	port := 0
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--host":
			host = args[i+1]
		case "--port":
			fmt.Sscanf(args[i+1], "%d", &port)
		}
	}
	return host, port
}

func (a *App) refreshHealthChecks() {
	defs := a.buildRuntimeDefs()
	if len(defs) > 0 {
		a.hc.UpdateRuntimes(defs)
	}
}

// listenerShutdownDeadline is the ONE stop deadline shared by every listener in
// the set (ADR 019 §D15.5), so total stop latency does not grow with the number
// of listeners — the Windows SCM stop deadline (ADR 011) is the reason. It is a
// var only so tests can exercise the deadline instead of waiting 10 seconds;
// nothing in production changes it.
var listenerShutdownDeadline = 10 * time.Second

// managedListener is one bound listener plus the http.Server that serves it.
type managedListener struct {
	scheme       string // "HTTP" or "HTTPS", the name every log line uses
	addr         string
	server       *http.Server
	ln           net.Listener
	startupAttrs []any // listener-naming diagnostics (§D19), empty for HTTP
}

// listenerSet is the configured listener set as one application capability
// (ADR 019 §D15): either every intended listener is bound and serving, or
// startup failed with none of them left bound.
type listenerSet struct {
	listeners []*managedListener
}

// intendedListener describes one listener the configuration asks for.
type intendedListener struct {
	scheme string
	addr   string
}

// tlsEnabled reports whether the optional native HTTPS block opens a listener.
// Only an explicit tls.enabled true does (§D1); a disabled or absent block never
// reads the other fields.
func tlsEnabled(cfg *config.Config) bool {
	return cfg.TLS != nil && cfg.TLS.Enabled
}

// intendedListeners resolves the listener set from config. HTTP keeps its
// existing ownership (listenAddress + webPort, §D2) and HTTPS reuses the same
// bind address on tls.port (§D2).
func intendedListeners(cfg *config.Config) ([]intendedListener, error) {
	httpAddr := net.JoinHostPort(cfg.ListenAddress, strconv.Itoa(cfg.WebPort))
	want := []intendedListener{{scheme: "HTTP", addr: httpAddr}}
	if tlsEnabled(cfg) {
		if cfg.TLS.Port == nil {
			return nil, errors.New("bind HTTPS: tls.port is required when tls.enabled is true")
		}
		httpsAddr := net.JoinHostPort(cfg.ListenAddress, strconv.Itoa(*cfg.TLS.Port))
		want = append(want, intendedListener{scheme: "HTTPS", addr: httpsAddr})
	}
	return want, nil
}

// bindListeners binds every intended listener before anything is served
// (§D15.2). On the first failure it closes the already-bound listeners and
// returns, so no port is published while another intended port failed — the
// partial state §D9–§D13 forbids.
func bindListeners(cfg *config.Config, handler http.Handler) (*listenerSet, error) {
	want, err := intendedListeners(cfg)
	if err != nil {
		slog.Error("listener configuration invalid", "error", err)
		return nil, err
	}

	set := &listenerSet{}
	for _, w := range want {
		l, err := newManagedListener(w, cfg, handler)
		if err != nil {
			slog.Error("listener setup failed", "scheme", w.scheme, "addr", w.addr, "error", err)
			set.closeAll()
			return nil, err
		}
		set.listeners = append(set.listeners, l)
	}
	return set, nil
}

// newManagedListener prepares and binds one listener. TLS material is loaded
// before the bind so that a pair which became unreadable after startup
// validation (§D8) still fails without leaving any listener bound.
func newManagedListener(w intendedListener, cfg *config.Config, handler http.Handler) (*managedListener, error) {
	// §D15.3: both listeners carry the timeouts the single HTTP listener has
	// always used.
	server := &http.Server{
		Addr:              w.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	var attrs []any
	if w.scheme == "HTTPS" {
		pair, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS certificate pair (cert=%s, key=%s): %w", cfg.TLS.CertFile, cfg.TLS.KeyFile, err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("tls.certFile %s has no parseable leaf certificate: %w", cfg.TLS.CertFile, err)
		}
		// §D22: the minimum version and the certificate chain are constants of this
		// contract, not configuration. Go serves the chain exactly as the file
		// carries it; no assembly, lookup or reordering happens here (§D5).
		server.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{pair},
		}
		// §D19: the HTTPS startup line names the certificate path, its expiry and
		// its SANs. Key material and the key file contents never appear.
		attrs = []any{
			"tls", "enabled",
			"cert", cfg.TLS.CertFile,
			"expires", leaf.NotAfter.Format(time.RFC3339),
			"san", certificateSANs(leaf),
		}
	}

	ln, err := net.Listen("tcp", w.addr)
	if err != nil {
		return nil, fmt.Errorf("bind %s listener on %s: %w", w.scheme, w.addr, err)
	}
	return &managedListener{scheme: w.scheme, addr: w.addr, server: server, ln: ln, startupAttrs: attrs}, nil
}

// certificateSANs renders the identity a client will match against, for the
// startup diagnostic only. GoAl never validates it (§D17).
func certificateSANs(leaf *x509.Certificate) string {
	parts := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	for _, dns := range leaf.DNSNames {
		parts = append(parts, "DNS:"+dns)
	}
	for _, ip := range leaf.IPAddresses {
		parts = append(parts, "IP:"+ip.String())
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// closeAll closes every bound listener that never started serving.
func (s *listenerSet) closeAll() {
	for i := len(s.listeners) - 1; i >= 0; i-- {
		_ = s.listeners[i].ln.Close()
	}
	s.listeners = nil
}

// serveFailure is a Serve loop that returned other than by coordinated
// shutdown.
type serveFailure struct {
	scheme string
	err    error
}

// serve runs the listener set until the context is cancelled or the capability
// is lost, then shuts every listener down under one shared deadline and returns
// the aggregated error to the caller (§D15.4, §D15.5).
func (s *listenerSet) serve(ctx context.Context) error {
	// Buffered for the whole set: after shutdown starts every listener can fail
	// at once, and a goroutine parked on an unbuffered send would never reach
	// serving.Done().
	failures := make(chan serveFailure, len(s.listeners))

	var serving sync.WaitGroup
	for _, l := range s.listeners {
		serving.Add(1)
		go func(l *managedListener) {
			defer serving.Done()
			slog.Info("starting "+l.scheme+" server", l.startupLogArgs()...)
			// An ErrServerClosed return is the coordinated shutdown already in
			// progress, not a failure (§D15) — reporting it would make a normal
			// stop look like a crash.
			if err := l.serveLoop(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error(l.scheme+" server error", "addr", l.addr, "error", err)
				failures <- serveFailure{scheme: l.scheme, err: err}
			}
		}(l)
	}

	// Wait for cancellation or the first lost listener. Any other Serve that
	// returns while this is being handled is drained after the shutdown, so no
	// failure is lost.
	var lost []serveFailure
	select {
	case <-ctx.Done():
	case f := <-failures:
		lost = append(lost, f)
	}

	shutdownErrs := s.shutdownAll()
	serving.Wait()

	for draining := true; draining; {
		select {
		case f := <-failures:
			lost = append(lost, f)
		default:
			draining = false
		}
	}

	// Aggregate in configured order so the reported error is deterministic.
	return s.aggregateFailures(lost, shutdownErrs)
}

// aggregateFailures builds the error returned to the caller: first the listeners
// whose Serve was lost, in configured order (HTTP, then HTTPS) and at most once
// per listener, then the shutdown outcomes in the same order.
//
// A listener that dies while another one is already being shut down reports
// http.ErrServerClosed rather than its own cause — that is documented behavior of
// http.Server.Serve ("After Server.Shutdown or Server.Close, the returned error is
// ErrServerClosed"), and the swallow here is deliberate: the failure that started
// the shutdown is the cause the operator needs.
func (s *listenerSet) aggregateFailures(lost []serveFailure, shutdownErrs []error) error {
	byScheme := make(map[string]error, len(lost))
	for _, f := range lost {
		if _, seen := byScheme[f.scheme]; !seen {
			byScheme[f.scheme] = fmt.Errorf("serve %s: %w", f.scheme, f.err)
		}
	}
	errs := make([]error, 0, len(s.listeners)+len(shutdownErrs))
	for _, l := range s.listeners {
		if err, ok := byScheme[l.scheme]; ok {
			errs = append(errs, err)
		}
	}
	for _, err := range shutdownErrs {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// shutdownAll stops every listener against the same deadline concurrently, so
// adding a listener does not lengthen the stop (§D15.5). One Shutdown path serves
// both a healthy listener and one whose Serve already returned: Serve closes and
// unregisters its own listener before returning, so shutting the dead one down
// drains whatever connections it still holds and reports no close error of its
// own. The failure that started the shutdown stays the reported cause.
func (s *listenerSet) shutdownAll() []error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), listenerShutdownDeadline)
	defer cancel()

	errs := make([]error, len(s.listeners))
	var wg sync.WaitGroup
	for i, l := range s.listeners {
		wg.Add(1)
		go func(i int, l *managedListener) {
			defer wg.Done()
			slog.Info("shutting down "+l.scheme+" server...", "addr", l.addr)
			if err := l.server.Shutdown(shutdownCtx); err != nil {
				slog.Error(l.scheme+" server shutdown error", "addr", l.addr, "error", err)
				errs[i] = fmt.Errorf("shutdown %s: %w", l.scheme, err)
			}
		}(i, l)
	}
	wg.Wait()
	return errs
}

// startupLogArgs is the §D19 startup line for this listener: HTTP names its
// address, HTTPS also names the certificate, its expiry and its SANs.
func (l *managedListener) startupLogArgs() []any {
	args := make([]any, 0, 2+len(l.startupAttrs))
	args = append(args, "addr", l.addr)
	return append(args, l.startupAttrs...)
}

// serveLoop serves the bound listener. The TLS material is already loaded into
// the server's TLSConfig (§D8 fail-closed), so ServeTLS takes no file arguments
// — and Serve must never be used for it: http.Server.Serve ignores TLSConfig
// entirely and would publish plaintext on tls.port.
func (l *managedListener) serveLoop() error {
	if l.server.TLSConfig != nil {
		return l.server.ServeTLS(l.ln, "", "")
	}
	return l.server.Serve(l.ln)
}

// Run starts the configured listeners and blocks until the context is cancelled
// or the listener capability is lost. Callers keep the existing contract: a
// non-nil error is a startup, serve or shutdown failure.
func (a *App) Run(ctx context.Context) error {
	if a.reg == nil {
		return fmt.Errorf("route registry not initialized")
	}

	set, err := bindListeners(a.cfg, a.Router())
	if err != nil {
		return err
	}
	return set.serve(ctx)
}

// Shutdown gracefully shuts down the HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	slog.Info("shutting down HTTP server...")
	return nil
}

// VersionInfo returns version information.
func VersionInfo() map[string]interface{} {
	return map[string]interface{}{
		"version":   version.Version,
		"gitCommit": version.GitCommit,
		"buildTime": version.BuildTime,
		"goVersion": runtime.Version(),
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
	}
}

func staticEmbedded() []string {
	entries, _ := fs.Glob(staticFS, "**")
	return entries
}
