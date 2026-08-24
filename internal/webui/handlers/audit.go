package handlers

import (
	"net/http"
	"strconv"

	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

const (
	auditDefaultLimit = 100
	auditMaxLimit     = 1000
)

// AuditHandler serves the audit log query API (ADR 007 §4).
type AuditHandler struct {
	logger *audit.AuditLogger
}

// NewAuditHandler creates an AuditHandler over the durable audit logger.
func NewAuditHandler(logger *audit.AuditLogger) *AuditHandler {
	return &AuditHandler{logger: logger}
}

// Query handles GET /api/v1/admin/audit.
// Parameters: limit (default 100, max 1000), offset (default 0),
// event (optional exact event-name filter). Events are returned newest first
// along with the total number of matching events. A missing audit file
// (fresh install) returns 200 with an empty list.
func (h *AuditHandler) Query(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := auditDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeAPIError(w, http.StatusBadRequest, apierrors.NewAPIError(apierrors.CodeBadRequest, "invalid limit"))
			return
		}
		limit = n
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeAPIError(w, http.StatusBadRequest, apierrors.NewAPIError(apierrors.CodeBadRequest, "invalid offset"))
			return
		}
		offset = n
	}
	event := q.Get("event")

	events, total, err := h.logger.Query(limit, offset, event)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apierrors.NewAPIError(apierrors.CodeInternalServer, "failed to read audit log"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"total":  total,
	})
}
