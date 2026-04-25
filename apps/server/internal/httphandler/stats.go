package httphandler

import (
	"net/http"

	"github.com/AmirSoleimani/openberth/apps/server/internal/sysstats"
)

// GetDeploymentStats handles GET /api/deployments/{id}/stats.
// Returns CPU%, memory, storage breakdown, and network/quota info for
// the specified deployment. Owner-gated: same auth surface as GetSource.
func (h *Handlers) GetDeploymentStats(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}
	id := r.PathValue("id")
	result, err := h.svc.DeploymentStats(user, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}

// GetHostStats handles GET /api/admin/host-stats. Admin-only — host
// pressure is operational metadata that non-admins shouldn't need.
func (h *Handlers) GetHostStats(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}
	stats, err := sysstats.Sample(h.svc.Cfg.DataDir)
	if err != nil {
		jsonErr(w, 500, "Failed to sample host stats: "+err.Error())
		return
	}
	jsonResp(w, 200, stats)
}
