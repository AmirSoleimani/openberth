package httphandler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/openberth/openberth/apps/server/internal/service"
)

// Handlers holds HTTP handler methods. All handlers share the service layer.
type Handlers struct {
	svc     *service.Service
	version string
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(svc *service.Service, version string) *Handlers {
	return &Handlers{svc: svc, version: version}
}

// jsonResp writes a JSON response with the given status code.
func jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// jsonErr writes a JSON error response.
func jsonErr(w http.ResponseWriter, status int, msg string) {
	jsonResp(w, status, map[string]string{"error": msg})
}

// writeErr writes a service.AppError as a JSON error response.
func writeErr(w http.ResponseWriter, err error) {
	if ae, ok := err.(*service.AppError); ok {
		jsonErr(w, ae.Status, ae.Message)
	} else {
		jsonErr(w, 500, err.Error())
	}
}

// decodeJSON decodes a JSON request body into the given type.
// Returns false and writes an error response if decoding fails.
func decodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return v, false
	}
	return v, true
}

// parseTail parses the "tail" query parameter for log requests.
func parseTail(r *http.Request) int {
	tail := 200
	if t, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && t > 0 {
		tail = t
		if tail > 10000 {
			tail = 10000
		}
	}
	return tail
}

// parseEnvVars extracts environment variables from a multipart form.
func parseEnvVars(r *http.Request) map[string]string {
	env := make(map[string]string)
	if err := r.ParseMultipartForm(200 << 20); err == nil {
		for _, v := range r.MultipartForm.Value["env"] {
			if idx := strings.Index(v, "="); idx > 0 {
				env[v[:idx]] = v[idx+1:]
			}
		}
	}
	return env
}
