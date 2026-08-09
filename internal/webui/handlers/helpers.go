package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// profileIDFromActionPath extracts the profile ID from a path of the form
// /api/v1/profiles/{id}{suffix}, where suffix starts with '/' (e.g. "/resolve").
// If the path doesn't match the expected pattern, an empty string is returned.
func profileIDFromActionPath(path, suffix string) string {
	const prefix = "/api/v1/profiles/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if !strings.HasSuffix(rest, suffix) {
		return ""
	}
	return strings.TrimSuffix(rest, suffix)
}
