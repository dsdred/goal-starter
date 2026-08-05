package handlers

import (
	"net/http"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/middleware"
	"github.com/dsdred/goal/internal/webui/security"
)

// RouteRegistry registers all HTTP routes.
type RouteRegistry struct {
	authHandler       *AuthHandler
	profileHandler    *ProfilesHandler
	runtimeHandler    *RuntimesHandler
	modelHandler      *ModelsHandler
	instanceHandler   *InstancesHandler
	systemHandler     *SystemHandler
	csrf              *security.CSRF
	sessionStore      *security.SessionStore
	passwordStore     *security.PasswordStore
	rateLimiter       any // *RateLimiter placeholder
	loggingMiddleware func(http.Handler) http.Handler
}

// NewRouteRegistry creates a new route registry.
func NewRouteRegistry(
	profileSvc *application.ProfileService,
	instanceSvc *application.InstanceService,
	runtimeSvc *application.RuntimeService,
	modelSvc *application.ModelService,
	supervisor *process.Supervisor,
	repo storage.Repository,
	csrf *security.CSRF,
	sessionStore *security.SessionStore,
	passwordStore *security.PasswordStore,
) *RouteRegistry {
	return &RouteRegistry{
		authHandler:     NewAuthHandler(sessionStore, passwordStore, csrf),
		profileHandler:  NewProfilesHandler(profileSvc, instanceSvc, supervisor, csrf),
		runtimeHandler:  NewRuntimesHandler(runtimeSvc, instanceSvc, supervisor, csrf),
		modelHandler:    NewModelsHandler(modelSvc, csrf),
		instanceHandler: NewInstancesHandler(instanceSvc, csrf),
		systemHandler:   NewSystemHandler(supervisor, sessionStore, csrf, instanceSvc),
		csrf:            csrf,
		sessionStore:    sessionStore,
		passwordStore:   passwordStore,
	}
}

// WithLoggingMiddleware sets the logging middleware.
func (r *RouteRegistry) WithLoggingMiddleware(m func(http.Handler) http.Handler) *RouteRegistry {
	r.loggingMiddleware = m
	return r
}

// Build creates the configured http.Handler with all middleware and routes.
func (r *RouteRegistry) Build() http.Handler {
	mux := http.NewServeMux()

	// Health and version (no auth).
	mux.HandleFunc("GET /api/v1/health", r.systemHandler.Health)

	// Auth endpoints.
	mux.HandleFunc("POST /api/v1/auth/login", r.authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/logout", r.authHandler.Logout)

	// Authenticated system endpoints.
	mux.HandleFunc("GET /api/v1/metrics", r.requireAuth(r.systemHandler.Metrics))
	mux.HandleFunc("GET /api/v1/instances", r.requireAuth(r.instanceHandler.List))
	mux.HandleFunc("GET /api/v1/instances/{id}", r.requireAuth(r.instanceHandler.Get))
	mux.HandleFunc("GET /api/v1/logs", r.requireAuth(r.systemHandler.QueryLogs))
	mux.HandleFunc("GET /api/v1/logs/stream", r.requireAuth(r.systemHandler.LogsStream))
	mux.HandleFunc("GET /api/v1/admin/users", r.requireAuth(r.systemHandler.AdminUsers))
	mux.HandleFunc("GET /api/v1/admin/sessions", r.requireAuth(r.systemHandler.AdminSessions))
	mux.HandleFunc("GET /api/v1/session", r.requireAuth(r.systemHandler.SessionInfo))

	// Authenticated instance CRUD.
	mux.HandleFunc("POST /api/v1/instances/start", r.requireAuthCSRF(r.instanceHandler.StartProfile))
	mux.HandleFunc("POST /api/v1/instances/{id}/stop", r.requireAuthCSRF(r.instanceHandler.StopInstance))
	mux.HandleFunc("POST /api/v1/instances/{id}/restart", r.requireAuthCSRF(r.instanceHandler.RestartInstance))
	mux.HandleFunc("GET /api/v1/instances/{id}/logs", r.requireAuth(r.systemHandler.InstanceLogs))
	mux.HandleFunc("GET /api/v1/instances/{id}/logs/stream", r.requireAuth(r.systemHandler.InstanceLogStream))

	// Profiles CRUD.
	mux.HandleFunc("GET /api/v1/profiles", r.requireAuth(r.profileHandler.List))
	mux.HandleFunc("GET /api/v1/profiles/", r.requireAuth(r.profileHandler.Get))
	mux.HandleFunc("POST /api/v1/profiles", r.requireAuthCSRF(r.profileHandler.Create))
	mux.HandleFunc("PUT /api/v1/profiles/", r.requireAuthCSRF(r.profileHandler.Update))
	mux.HandleFunc("DELETE /api/v1/profiles/", r.requireAuthCSRF(r.profileHandler.Delete))
	mux.HandleFunc("POST /api/v1/profiles/{id}/action/{action}", r.requireAuthCSRF(r.profileHandler.Action))
	mux.HandleFunc("POST /api/v1/profiles/{id}/start", r.requireAuthCSRF(r.profileHandler.Start))
	mux.HandleFunc("POST /api/v1/profiles/{id}/stop", r.requireAuthCSRF(r.profileHandler.Stop))
	mux.HandleFunc("POST /api/v1/profiles/{id}/restart", r.requireAuthCSRF(r.profileHandler.Restart))
	mux.HandleFunc("GET /api/v1/profiles/{id}/status", r.requireAuth(r.profileHandler.Status))
	mux.HandleFunc("POST /api/v1/profiles/{id}/activate", r.requireAuthCSRF(r.profileHandler.Activate))
	mux.HandleFunc("POST /api/v1/profiles/{id}/deactivate", r.requireAuthCSRF(r.profileHandler.Deactivate))
	mux.HandleFunc("POST /api/v1/profiles/{id}/resolve", r.requireAuthCSRF(r.profileHandler.Resolve))

	// Runtimes CRUD.
	mux.HandleFunc("GET /api/v1/runtimes", r.requireAuth(r.runtimeHandler.List))
	mux.HandleFunc("GET /api/v1/runtimes/", r.requireAuth(r.runtimeHandler.Get))
	mux.HandleFunc("POST /api/v1/runtimes", r.requireAuthCSRF(r.runtimeHandler.Create))
	mux.HandleFunc("PUT /api/v1/runtimes/", r.requireAuthCSRF(r.runtimeHandler.Update))
	mux.HandleFunc("DELETE /api/v1/runtimes/", r.requireAuthCSRF(r.runtimeHandler.Delete))
	mux.HandleFunc("POST /api/v1/runtimes/{id}/action/{action}", r.requireAuthCSRF(r.runtimeHandler.Action))
	mux.HandleFunc("GET /api/v1/runtimes/health", r.requireAuth(r.runtimeHandler.HealthCheck))
	mux.HandleFunc("GET /api/v1/runtimes/health/{id}", r.requireAuth(r.runtimeHandler.RuntimeHealth))

	// Models CRUD.
	mux.HandleFunc("GET /api/v1/models", r.requireAuth(r.modelHandler.List))
	mux.HandleFunc("GET /api/v1/models/", r.requireAuth(r.modelHandler.Get))
	mux.HandleFunc("POST /api/v1/models", r.requireAuthCSRF(r.modelHandler.Create))
	mux.HandleFunc("PUT /api/v1/models/", r.requireAuthCSRF(r.modelHandler.Update))
	mux.HandleFunc("DELETE /api/v1/models/", r.requireAuthCSRF(r.modelHandler.Delete))

	// Main UI.
	mux.HandleFunc("/", r.requireAuth(r.systemHandler.ServeIndex))

	// Static files.
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./web/static"))))

	// Apply middleware chain.
	var handler http.Handler = mux
	handler = r.applyCSRF(handler)
	handler = r.applyRateLimit(handler)
	handler = r.applyLogging(handler)

	return handler
}

// requireAuth wraps a handler with authentication check.
func (r *RouteRegistry) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token, err := security.GetSessionToken(req)
		if err != nil || token == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_, err = r.sessionStore.ValidateSession(token)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	}
}

// requireAuthCSRF wraps a handler with authentication and CSRF check.
func (r *RouteRegistry) requireAuthCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token, err := security.GetSessionToken(req)
		if err != nil || token == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		session, err := r.sessionStore.ValidateSession(token)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		// Validate CSRF for unsafe methods.
		if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
			if err := r.csrf.ValidateSessionCSRF(req, session); err != nil {
				http.Error(w, `{"error":"invalid CSRF token"}`, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, req)
	}
}

// applyLogging applies the logging middleware.
func (r *RouteRegistry) applyLogging(next http.Handler) http.Handler {
	if r.loggingMiddleware != nil {
		return r.loggingMiddleware(next)
	}
	return middleware.LoggingMiddleware(next)
}

// applyRateLimit applies rate limiting middleware (placeholder).
func (r *RouteRegistry) applyRateLimit(next http.Handler) http.Handler {
	return next
}

// applyCSRF applies the global CSRF middleware.
func (r *RouteRegistry) applyCSRF(next http.Handler) http.Handler {
	if r.csrf == nil {
		return next
	}
	return r.csrf.Middleware(next)
}
