package handlers

import (
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// Login rate limiting: at most loginRateLimit requests per loginRateWindow per client address.
const (
	loginRateLimit  = 100
	loginRateWindow = time.Minute
)

// RouteRegistry registers all HTTP routes.
type RouteRegistry struct {
	authHandler       *AuthHandler
	runtimeHandler    *RuntimesHandler
	modelHandler      *ModelsHandler
	instanceHandler   *InstancesHandler
	systemHandler     *SystemHandler
	csrf              *security.CSRF
	sessionStore      *security.SessionStore
	passwordStore     *security.PasswordStore
	loginLimiter      *security.RateLimiter
	loggingMiddleware func(http.Handler) http.Handler
	authEnabled       bool
	staticFS          fs.FS
}

type RouteRegistryOption func(*RouteRegistry)

func WithAuthEnabled(enabled bool) RouteRegistryOption {
	return func(r *RouteRegistry) {
		r.authEnabled = enabled
	}
}

func WithWebAssets(templateFS, staticFS fs.FS) RouteRegistryOption {
	return func(r *RouteRegistry) {
		r.systemHandler.WithTemplateFS(templateFS)
		r.staticFS = staticFS
	}
}

func WithServerInfo(listenAddr string, webPort int, authEnabled bool) RouteRegistryOption {
	return func(r *RouteRegistry) {
		host, port, err := net.SplitHostPort(listenAddr)
		if err != nil {
			r.systemHandler.listenAddr = listenAddr
			r.systemHandler.webPort = webPort
		} else {
			r.systemHandler.listenAddr = host
			if p, convErr := strconv.Atoi(port); convErr == nil {
				r.systemHandler.webPort = p
			} else {
				r.systemHandler.webPort = webPort
			}
		}
		r.systemHandler.authEnabled = authEnabled
	}
}

func WithConfigPath(path string) RouteRegistryOption {
	return func(r *RouteRegistry) {
		r.systemHandler.configPath = path
	}
}

func NewRouteRegistry(
	instanceSvc *application.InstanceService,
	runtimeSvc *application.RuntimeService,
	modelSvc *application.ModelService,
	supervisor *process.Supervisor,
	repo storage.Repository,
	csrf *security.CSRF,
	sessionStore *security.SessionStore,
	passwordStore *security.PasswordStore,
	opts ...RouteRegistryOption,
) *RouteRegistry {
	r := &RouteRegistry{
		authHandler:     NewAuthHandler(sessionStore, passwordStore, csrf),
		runtimeHandler:  NewRuntimesHandler(runtimeSvc, instanceSvc, supervisor, csrf),
		modelHandler:    NewModelsHandler(modelSvc, instanceSvc, supervisor, repo, csrf),
		instanceHandler: NewInstancesHandler(instanceSvc, csrf),
		systemHandler:   NewSystemHandler(supervisor, sessionStore, csrf, instanceSvc),
		csrf:            csrf,
		sessionStore:    sessionStore,
		passwordStore:   passwordStore,
		loginLimiter:    security.NewRateLimiter(loginRateLimit, loginRateWindow),
		authEnabled:     true,
	}
	r.systemHandler.passStore = passwordStore
	for _, opt := range opts {
		opt(r)
	}
	r.authHandler.WithAuthEnabled(r.authEnabled)
	return r
}

func (r *RouteRegistry) WithLoggingMiddleware(m func(http.Handler) http.Handler) *RouteRegistry {
	r.loggingMiddleware = m
	return r
}

func (r *RouteRegistry) Build() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", r.systemHandler.Health)
	mux.HandleFunc("GET /api/v1/version", r.systemHandler.Version)

	mux.HandleFunc("POST /api/v1/auth/login", r.rateLimited(r.authHandler.Login))
	mux.HandleFunc("POST /api/v1/auth/logout", r.requireAuthCSRF(r.authHandler.Logout))
	mux.HandleFunc("GET /api/v1/auth/session", r.authHandler.CheckSession)

	mux.HandleFunc("GET /api/v1/metrics", r.requireAuth(r.systemHandler.Metrics))
	mux.HandleFunc("PUT /api/v1/settings", r.requireAuthCSRF(r.systemHandler.SaveSettings))
	mux.HandleFunc("GET /api/v1/instances", r.requireAuth(r.instanceHandler.List))
	mux.HandleFunc("GET /api/v1/instances/{id}", r.requireAuth(r.instanceHandler.Get))
	mux.HandleFunc("GET /api/v1/history", r.requireAuth(r.instanceHandler.History))
	mux.HandleFunc("GET /api/v1/logs", r.requireAuth(r.systemHandler.QueryLogs))
	mux.HandleFunc("GET /api/v1/logs/stream", r.requireAuth(r.systemHandler.LogsStream))
	mux.HandleFunc("GET /api/v1/admin/users", r.requireAuth(r.systemHandler.AdminUsers))
	mux.HandleFunc("GET /api/v1/admin/sessions", r.requireAuth(r.systemHandler.AdminSessions))
	mux.HandleFunc("GET /api/v1/session", r.requireAuth(r.systemHandler.SessionInfo))

	mux.HandleFunc("POST /api/v1/instances/start", r.requireAuthCSRF(r.instanceHandler.StartModel))
	mux.HandleFunc("POST /api/v1/instances/{id}/stop", r.requireAuthCSRF(r.instanceHandler.StopInstance))
	mux.HandleFunc("POST /api/v1/instances/{id}/restart", r.requireAuthCSRF(r.instanceHandler.RestartInstance))
	mux.HandleFunc("POST /api/v1/instances/{id}/dismiss", r.requireAuthCSRF(r.instanceHandler.Dismiss))
	mux.HandleFunc("POST /api/v1/instances/cleanup", r.requireAuthCSRF(r.instanceHandler.Cleanup))
	mux.HandleFunc("GET /api/v1/instances/{id}/logs", r.requireAuth(r.systemHandler.InstanceLogs))
	mux.HandleFunc("GET /api/v1/instances/{id}/logs/stream", r.requireAuth(r.systemHandler.InstanceLogStream))

	// Models CRUD + lifecycle.
	mux.HandleFunc("GET /api/v1/models", r.requireAuth(r.modelHandler.List))
	mux.HandleFunc("GET /api/v1/models/{id}", r.requireAuth(r.modelHandler.Get))
	mux.HandleFunc("POST /api/v1/models", r.requireAuthCSRF(r.modelHandler.Create))
	mux.HandleFunc("PUT /api/v1/models/{id}", r.requireAuthCSRF(r.modelHandler.Update))
	mux.HandleFunc("DELETE /api/v1/models/{id}", r.requireAuthCSRF(r.modelHandler.Delete))
	mux.HandleFunc("POST /api/v1/models/{id}/start", r.requireAuthCSRF(r.modelHandler.Start))
	mux.HandleFunc("POST /api/v1/models/{id}/stop", r.requireAuthCSRF(r.modelHandler.Stop))
	mux.HandleFunc("POST /api/v1/models/{id}/restart", r.requireAuthCSRF(r.modelHandler.Restart))
	mux.HandleFunc("GET /api/v1/models/{id}/status", r.requireAuth(r.modelHandler.Status))
	mux.HandleFunc("POST /api/v1/models/{id}/activate", r.requireAuthCSRF(r.modelHandler.Activate))
	mux.HandleFunc("POST /api/v1/models/{id}/deactivate", r.requireAuthCSRF(r.modelHandler.Deactivate))
	mux.HandleFunc("POST /api/v1/models/{id}/resolve", r.requireAuthCSRF(r.modelHandler.Resolve))

	// Runtimes CRUD.
	mux.HandleFunc("GET /api/v1/runtimes", r.requireAuth(r.runtimeHandler.List))
	mux.HandleFunc("GET /api/v1/runtimes/health", r.requireAuth(r.runtimeHandler.HealthCheck))
	mux.HandleFunc("GET /api/v1/runtimes/health/{id}", r.requireAuth(r.runtimeHandler.RuntimeHealth))
	mux.HandleFunc("GET /api/v1/runtimes/{id}", r.requireAuth(r.runtimeHandler.Get))
	mux.HandleFunc("POST /api/v1/runtimes", r.requireAuthCSRF(r.runtimeHandler.Create))
	mux.HandleFunc("PUT /api/v1/runtimes/{id}", r.requireAuthCSRF(r.runtimeHandler.Update))
	mux.HandleFunc("DELETE /api/v1/runtimes/{id}", r.requireAuthCSRF(r.runtimeHandler.Delete))
	mux.HandleFunc("POST /api/v1/runtimes/{id}/replace", r.requireAuthCSRF(r.runtimeHandler.Replace))
	mux.HandleFunc("POST /api/v1/runtimes/{id}/cascade-delete", r.requireAuthCSRF(r.runtimeHandler.CascadeDelete))
	mux.HandleFunc("POST /api/v1/runtimes/{id}/action/{action}", r.requireAuthCSRF(r.runtimeHandler.Action))

	mux.HandleFunc("/", r.systemHandler.ServeIndex)

	if r.staticFS != nil {
		sub, err := fs.Sub(r.staticFS, "static")
		if err == nil {
			mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(sub)))
		} else {
			mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(r.staticFS)))
		}
	}

	var handler http.Handler = mux
	handler = r.applyCachePolicy(handler)
	handler = r.applyLogging(handler)
	return handler
}

// applyCachePolicy sets Cache-Control headers based on request path.
// API/auth: no-store (never cache dynamic data).
// Static assets: no-store (never cache; guarantees fresh content after binary
//
//	replacement with no reliance on browser revalidation of old entries).
//
// Index/other: no-cache (always revalidate the HTML document).
func (r *RouteRegistry) applyCachePolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/api/"):
			w.Header().Set("Cache-Control", "no-store, private")
		case strings.HasPrefix(req.URL.Path, "/static/"):
			w.Header().Set("Cache-Control", "no-store")
		default:
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, req)
	})
}

func (r *RouteRegistry) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	if !r.authEnabled {
		return next
	}
	return func(w http.ResponseWriter, req *http.Request) {
		token, err := security.GetSessionToken(req)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if _, err := r.sessionStore.ValidateSession(token); err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, req)
	}
}

func (r *RouteRegistry) requireAuthCSRF(next http.HandlerFunc) http.HandlerFunc {
	if !r.authEnabled {
		return next
	}
	return func(w http.ResponseWriter, req *http.Request) {
		token, err := security.GetSessionToken(req)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		session, err := r.sessionStore.ValidateSession(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := r.csrf.ValidateSessionCSRF(req, session); err != nil {
			writeError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next(w, req)
	}
}

// rateLimited bounds brute-force attempts on the login endpoint.
// The key is the client's TCP peer address; X-Forwarded-For / X-Real-IP are
// intentionally not trusted, because client-supplied headers would allow an
// attacker to bypass the limiter by rotating fake addresses.
func (r *RouteRegistry) rateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !r.loginLimiter.Allow(loginClientKey(req)) {
			writeAPIError(w, http.StatusTooManyRequests, apierrors.NewAPIError(apierrors.CodeRateLimited, "too many login attempts, please try again later"))
			return
		}
		next(w, req)
	}
}

// loginClientKey returns the client address used for login rate limiting.
func loginClientKey(req *http.Request) string {
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}

func (r *RouteRegistry) applyLogging(next http.Handler) http.Handler {
	if r.loggingMiddleware != nil {
		return r.loggingMiddleware(next)
	}
	return next
}
