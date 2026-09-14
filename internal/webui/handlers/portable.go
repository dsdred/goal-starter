package handlers

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/dsdred/goal/internal/application/portable"
	"github.com/dsdred/goal/internal/storage"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
)

const importMaxBodyBytes = 10 * 1024 * 1024

// PortableHandler handles portable config export/import (ADR 014 Slice 2B).
type PortableHandler struct {
	repo         storage.Repository
	orchestrator *portable.ImportOrchestrator
}

func NewPortableHandler(repo storage.Repository) *PortableHandler {
	return &PortableHandler{
		repo:         repo,
		orchestrator: portable.NewImportOrchestrator(repo),
	}
}

// Export handles GET /api/v1/export.
func (h *PortableHandler) Export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Check for repeated selectors.
	var present []string
	for _, key := range []string{"runtime_id", "model_id", "pipeline_id"} {
		vals := q[key]
		if len(vals) == 0 {
			continue
		}
		if len(vals) > 1 {
			writeError(w, http.StatusBadRequest, "repeated query parameter: "+key)
			return
		}
		present = append(present, key)
	}

	if len(present) > 1 {
		writeError(w, http.StatusBadRequest, "at most one root selector may be provided")
		return
	}

	var root portable.ExportRoot
	for _, key := range present {
		v := q.Get(key)
		if v == "" {
			writeError(w, http.StatusBadRequest, "empty selector value: "+key)
			return
		}
		switch key {
		case "runtime_id":
			root.RuntimeID = v
		case "model_id":
			root.ModelID = v
		case "pipeline_id":
			root.PipelineID = v
		}
	}

	data, err := portable.ExportBundle(h.repo, root)
	if err != nil {
		var notFound *portable.ErrNotFound
		if errors.As(err, &notFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "export failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="goal-portable-config.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// Import handles POST /api/v1/import.
func (h *PortableHandler) Import(w http.ResponseWriter, r *http.Request) {
	// Parse dry_run query parameter.
	dryRun, err := parseDryRun(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Bound the request body before any parsing.
	r.Body = http.MaxBytesReader(w, r.Body, importMaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 10 MiB limit")
			return
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "empty request body")
			return
		}
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body")
		return
	}

	bundle, err := portable.ParseBundle(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid bundle: "+err.Error())
		return
	}

	result, err := h.orchestrator.Import(bundle, dryRun)
	if err != nil {
		var conflict *portable.ErrConflict
		if errors.As(err, &conflict) {
			writeConflictError(w, conflict)
			return
		}
		var persistence *portable.ErrPersistence
		if errors.As(err, &persistence) {
			writeError(w, http.StatusInternalServerError, "import persistence failure")
			return
		}
		var validation *portable.ErrValidation
		if errors.As(err, &validation) {
			writeError(w, http.StatusBadRequest, "invalid bundle: "+err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "import failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run":   dryRun,
		"runtimes":  result.Runtimes,
		"models":    result.Models,
		"pipelines": result.Pipelines,
	})
}

func parseDryRun(r *http.Request) (bool, error) {
	q := r.URL.Query()
	vals := q["dry_run"]
	if len(vals) == 0 {
		return false, nil
	}
	if len(vals) > 1 {
		return false, errors.New("repeated query parameter: dry_run")
	}
	v := vals[0]
	if v == "" {
		return false, errors.New("dry_run requires a value (true or false)")
	}
	if v == "true" {
		return true, nil
	}
	if v == "false" {
		return false, nil
	}
	return false, errors.New("dry_run must be 'true' or 'false'")
}

func writeConflictError(w http.ResponseWriter, conflict *portable.ErrConflict) {
	details := make([]string, 0, len(conflict.Conflicts))
	for _, c := range conflict.Conflicts {
		detail := c.Type + " " + c.ID + ": " + c.Reason
		if c.Name != "" {
			detail += " (name: " + c.Name + ")"
		}
		details = append(details, detail)
	}
	writeAPIError(w, http.StatusConflict, apierrors.NewAPIError(apierrors.CodeConflict, "import rejected: "+strconv.Itoa(len(conflict.Conflicts))+" collision(s)", details...))
}
