package handlers

import (
	"errors"
	"io"
	"net/http"

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
			writeImportConflict(w, conflict)
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

	writeJSON(w, http.StatusOK, buildImportResponse(result))
}

// importTypeSummary is the per-entity-type import plan count set.
type importTypeSummary struct {
	Total    int `json:"total"`
	New      int `json:"new"`
	Existing int `json:"existing"`
	Blocked  int `json:"blocked"`
}

// importCountsBody carries one count set across the three entity types.
type importCountsBody struct {
	Runtimes  int `json:"runtimes"`
	Models    int `json:"models"`
	Pipelines int `json:"pipelines"`
}

// importSummaryBody is the localized-summary source for the UI.
type importSummaryBody struct {
	Runtimes  importTypeSummary `json:"runtimes"`
	Models    importTypeSummary `json:"models"`
	Pipelines importTypeSummary `json:"pipelines"`
}

// importResponse is the import/import-plan body.
//
// The flat runtimes/models/pipelines fields are created counts, kept so the
// body stays readable without walking the nested objects.
type importResponse struct {
	DryRun    bool                `json:"dry_run"`
	CanImport bool                `json:"can_import"`
	Summary   importSummaryBody   `json:"summary"`
	Created   importCountsBody    `json:"created"`
	Skipped   importCountsBody    `json:"skipped"`
	Blocked   []portable.Conflict `json:"blocked"`
	Runtimes  int                 `json:"runtimes"`
	Models    int                 `json:"models"`
	Pipelines int                 `json:"pipelines"`
}

type importConflictResponse struct {
	importResponse
	Error   string   `json:"error"`
	Code    string   `json:"code"`
	Details []string `json:"details,omitempty"`
}

func buildImportResponse(res *portable.ImportResult) importResponse {
	blockedByType := map[string]int{}
	for _, c := range res.Blocked {
		blockedByType[c.Type]++
	}
	blocked := res.Blocked
	if blocked == nil {
		blocked = []portable.Conflict{}
	}
	return importResponse{
		DryRun:    res.DryRun,
		CanImport: res.CanImport,
		Summary: importSummaryBody{
			Runtimes:  summarize(res.Total.Runtimes, res.Created.Runtimes, res.Skipped.Runtimes, blockedByType["runtime"]),
			Models:    summarize(res.Total.Models, res.Created.Models, res.Skipped.Models, blockedByType["model"]),
			Pipelines: summarize(res.Total.Pipelines, res.Created.Pipelines, res.Skipped.Pipelines, blockedByType["pipeline"]),
		},
		Created: importCountsBody{
			Runtimes:  res.Created.Runtimes,
			Models:    res.Created.Models,
			Pipelines: res.Created.Pipelines,
		},
		Skipped: importCountsBody{
			Runtimes:  res.Skipped.Runtimes,
			Models:    res.Skipped.Models,
			Pipelines: res.Skipped.Pipelines,
		},
		Blocked:   blocked,
		Runtimes:  res.Created.Runtimes,
		Models:    res.Created.Models,
		Pipelines: res.Created.Pipelines,
	}
}

func summarize(total, new, existing, blocked int) importTypeSummary {
	return importTypeSummary{Total: total, New: new, Existing: existing, Blocked: blocked}
}

// writeImportConflict answers a blocked import plan. Nothing was written. The
// body carries the same plan shape as a successful validation so the caller can
// distinguish file validity from repository conflicts, plus the legacy error
// envelope fields.
func writeImportConflict(w http.ResponseWriter, conflict *portable.ErrConflict) {
	resp := importConflictResponse{
		importResponse: buildImportResponse(conflict.Result),
		Error:          conflict.Error(),
		Code:           string(apierrors.CodeConflict),
	}
	details := make([]string, 0, len(conflict.Conflicts))
	for _, c := range conflict.Conflicts {
		detail := c.Type + " " + c.ID + ": " + c.Reason
		if c.Name != "" {
			detail += " (name: " + c.Name + ")"
		}
		if c.RelatedID != "" {
			detail += " (existing: " + c.RelatedID + ")"
		}
		details = append(details, detail)
	}
	resp.Details = details
	writeJSON(w, http.StatusConflict, resp)
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
