package webui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/example/goal/internal/process"
	"github.com/example/goal/internal/webui/store"
)

// profileStart handles POST /api/v1/profiles/{id}/start
func (a *App) profileStart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "profile id required")
		return
	}

	profile, err := a.pdb.GetProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}

	// Resolve runtime.
	runtime, err := a.pdb.GetRuntime(profile.RuntimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found for profile")
		return
	}

	// Build CommandSpec from Runtime + Profile.
	spec, err := a.buildCommandSpec(runtime, profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to build command spec: "+err.Error())
		return
	}

	// Start the process.
	if err := a.mgr.Start(r.Context(), spec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start profile: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// profileStop handles POST /api/v1/profiles/{id}/stop
func (a *App) profileStop(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "profile id required")
		return
	}

	ctx := r.Context()
	if err := a.mgr.Stop(ctx); err != nil {
		// It's ok if there's no process running.
	}

	w.WriteHeader(http.StatusOK)
}

// profileRestart handles POST /api/v1/profiles/{id}/restart
func (a *App) profileRestart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "profile id required")
		return
	}

	// Stop current process.
	ctxStop := r.Context()
	if err := a.mgr.Stop(ctxStop); err != nil {
		// It's ok if there's no process running.
	}

	// Resolve runtime.
	profile, err := a.pdb.GetProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "profile or runtime not found")
		return
	}
	runtime, err := a.pdb.GetRuntime(profile.RuntimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found for profile")
		return
	}

	// Re-start with same profile.
	spec, err := a.buildCommandSpec(runtime, profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to build command spec: "+err.Error())
		return
	}
	if err := a.mgr.Start(r.Context(), spec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restart profile: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "restarted",
	})
}

// profileStatus handles GET /api/v1/profiles/{id}/status
func (a *App) profileStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "profile id required")
		return
	}

	// Get profile.
	profile, err := a.pdb.GetProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}

	// Get process status.
	status := a.mgr.Status()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"profile": profile,
		"process": status,
	})
}

// profileActivate handles POST /api/v1/profiles/{id}/activate
func (a *App) profileActivate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "profile id required")
		return
	}

	profile, err := a.pdb.ActivateProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "activated",
		"profile": profile,
	})
}

// profileDeactivate handles POST /api/v1/profiles/{id}/deactivate
func (a *App) profileDeactivate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/")
	if id == r.URL.Path {
		writeError(w, http.StatusBadRequest, "profile id required")
		return
	}

	profile, err := a.pdb.DeactivateProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "deactivated",
		"profile": profile,
	})
}

// buildCommandSpec constructs a CommandSpec from Runtime and Profile.
func (a *App) buildCommandSpec(runtime *store.RuntimeEntry, profile *store.Profile) (process.CommandSpec, error) {
	if runtime == nil {
		return process.CommandSpec{}, errors.New("runtime is required")
	}
	if profile == nil {
		return process.CommandSpec{}, errors.New("profile is required")
	}

	// Build args: runtime default args + profile args + model arg.
	args := append([]string{}, runtime.DefaultArgs...)
	args = append(args, profile.Args...)

	// Add model if specified in profile.
	if profile.ModelID != "" {
		model, err := a.mdb.GetModel(profile.ModelID)
		if err == nil {
			args = append(args, "--model", model.Path)
			if model.MMProj != "" {
				args = append(args, "--mmproj", model.MMProj)
			}
			if model.Format != "" {
				args = append(args, "--format", model.Format)
			}
		}
	}

	// Build environment: runtime env + profile env.
	env := make([]string, 0)
	for k, v := range runtime.Environment {
		env = append(env, k+"="+v)
	}
	for k, v := range profile.Environment {
		env = append(env, k+"="+v)
	}

	spec := process.CommandSpec{
		Executable:       runtime.Executable,
		Args:             args,
		WorkingDirectory: runtime.WorkingDirectory,
		Environment:      env,
	}

	return spec, nil
}
