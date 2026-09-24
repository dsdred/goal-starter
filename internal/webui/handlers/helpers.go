package handlers

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeAPIError writes a structured API error response with details.
func writeAPIError(w http.ResponseWriter, status int, err *apierrors.APIError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": err.Message,
		"code":  string(err.Code),
	}
	if len(err.Details) > 0 {
		resp["details"] = err.Details
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// statusForAPICode maps a bounded API error code to its HTTP status.
func statusForAPICode(code apierrors.Code) int {
	switch code {
	case apierrors.CodeBadRequest, apierrors.CodeInvalidPort, apierrors.CodeInvalidHost,
		apierrors.CodeInvalidAddress:
		return http.StatusBadRequest
	case apierrors.CodeUnauthorized:
		return http.StatusUnauthorized
	case apierrors.CodeForbidden:
		return http.StatusForbidden
	case apierrors.CodeNotFound, apierrors.CodeInvalidRuntime, apierrors.CodeInvalidModel:
		return http.StatusNotFound
	case apierrors.CodeGone:
		return http.StatusGone
	case apierrors.CodeConflict:
		return http.StatusConflict
	case apierrors.CodeRateLimited:
		return http.StatusTooManyRequests
	case apierrors.CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// lifecycleTokens are the bounded `error` values of the lifecycle error
// contract. The stable client-visible vocabulary; never raw error text.
const (
	tokenLaunchInFlight         = "launch_in_flight"
	tokenLaunchAborted          = "launch_aborted"
	tokenShuttingDown           = "shutting_down"
	tokenOrphan                 = "orphan"
	tokenNotRestartable         = "not_restartable"
	tokenInstanceNotFound       = "instance_not_found"
	tokenLaunchPersistFailed    = "launch_persist_failed"
	tokenTerminationUnconfirmed = "termination_unconfirmed"
	tokenRollbackFailed         = "rollback_failed"
)

// writeLifecycleError maps a SINGLE-TARGET process-layer lifecycle failure
// (instance, model or runtime start/stop/restart) to the flat
// error/code wire contract (BF-03a). Classification is by sentinel identity
// only — errors.Is / errors.As / process.HasRejectionReason — never by message
// substring. An unrecognized error keeps the endpoint's previous bounded 500.
// Group pipeline requests are classified separately by groupFailureClass, which
// derives the status per phase and never reports a per-instance 404.
func writeLifecycleError(w http.ResponseWriter, err error) {
	apiErr := lifecycleAPIError(err)
	if apiErr == nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAPIError(w, statusForAPICode(apiErr.Code), apiErr)
}

// lifecycleAuditToken returns the same bounded token writeLifecycleError wrote
// into the response, for the audit record (ADR 007: identifiers only). It falls
// back to the sanitized error text for unrecognized classes.
func lifecycleAuditToken(err error) string {
	if apiErr := lifecycleAPIError(err); apiErr != nil {
		return apiErr.Message
	}
	return sanitizeAuditError(err)
}

// lifecycleAPIError classifies one lifecycle error, or returns nil when the
// class is not part of the lifecycle contract.
//
// Canonical precedence inside a multi-cause error is 500 > 503 > 409
// (docs/API.md, "Lifecycle error mapping"), shared with the group classifier in
// pipelines.go: an internal persistence/ownership/termination failure outranks
// a retry-later shutdown refusal, because only the 500 class means GoAl's own
// state may be unresolved and an unconfirmed termination must never be laundered
// into a benign retry answer.
func lifecycleAPIError(err error) *apierrors.APIError {
	switch {
	// Server-side: the platform could not complete or confirm its own work.
	// Evaluated first so a joined shutdown cause cannot hide it.
	case errors.Is(err, process.ErrPersistenceFailure):
		return apierrors.NewAPIError(apierrors.CodeInternalServer, tokenLaunchPersistFailed)
	case errors.Is(err, process.ErrTerminationUnconfirmed):
		return apierrors.NewAPIError(apierrors.CodeInternalServer, tokenTerminationUnconfirmed)
	case errors.Is(err, process.ErrRollbackFailed):
		return apierrors.NewAPIError(apierrors.CodeInternalServer, tokenRollbackFailed)
	// Retry-later: the platform is going down (RB-015b abort = admitted then
	// aborted; RejShuttingDown = rejected before admission).
	case errors.Is(err, process.ErrLaunchAbortedByShutdown):
		return apierrors.NewAPIError(apierrors.CodeServiceUnavailable, tokenLaunchAborted)
	case process.HasRejectionReason(err, process.RejShuttingDown):
		return apierrors.NewAPIError(apierrors.CodeServiceUnavailable, tokenShuttingDown)
	// Bounded caller-visible conditions.
	case errors.Is(err, process.ErrInstanceNotFound):
		return apierrors.NewAPIError(apierrors.CodeNotFound, tokenInstanceNotFound)
	case errors.Is(err, process.ErrNotRestartable):
		return apierrors.NewAPIError(apierrors.CodeConflict, tokenNotRestartable)
	case process.HasRejectionReason(err, process.RejOrphan):
		return apierrors.NewAPIError(apierrors.CodeConflict, tokenOrphan)
	case errors.Is(err, process.ErrLaunchInFlight),
		process.HasRejectionReason(err, process.RejInFlight):
		return apierrors.NewAPIError(apierrors.CodeConflict, tokenLaunchInFlight)
	}
	return nil
}

// clientIP returns the TCP peer address for audit records.
// X-Forwarded-For / X-Real-IP are intentionally not trusted (spoofable).
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// auditUser resolves the authenticated username for the request, or "" when
// the session is unknown (auth disabled, missing or invalid cookie).
func auditUser(sess *security.SessionStore, r *http.Request) string {
	if sess == nil {
		return ""
	}
	token, err := security.GetSessionToken(r)
	if err != nil || token == "" {
		return ""
	}
	session, err := sess.ValidateSession(token)
	if err != nil || session == nil {
		return ""
	}
	return session.User
}

// logAudit emits an audit event fail-open (ADR 007 §6): a persistence failure
// never affects the business operation; the logger emits the structured
// operational diagnostic itself.
func logAudit(logger *audit.AuditLogger, sess *security.SessionStore, r *http.Request, event string, detail map[string]string) {
	if logger == nil {
		return
	}
	_ = logger.Log(audit.AuditEvent{
		Event:    event,
		User:     auditUser(sess, r),
		SourceIP: clientIP(r),
		Detail:   detail,
	})
}
