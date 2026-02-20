package httphandler

import (
	"log"
	"net/http"
)

// Cleanup removes expired deployments, expired OAuth data, and expired sessions.
func (h *Handlers) Cleanup(w http.ResponseWriter, r *http.Request) {
	count := h.svc.RunCleanup()
	jsonResp(w, 200, map[string]int{"cleaned": count})
}

// DestroyAllDeployments removes all deployments belonging to the authenticated user.
func (h *Handlers) DestroyAllDeployments(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	deploys, _ := h.svc.Store.ListDeployments(user.ID)
	count := 0
	for _, d := range deploys {
		h.svc.DestroyFull(&d)
		count++
	}

	log.Printf("[destroy-all] %d deployments | user=%s", count, user.Name)
	jsonResp(w, 200, map[string]int{"destroyed": count})
}
