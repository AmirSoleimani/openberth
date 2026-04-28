package httphandler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
)

// AdminBackupDeployment streams an encrypted single-deployment archive.
// POST /api/admin/deployments/{id}/backup, body {"passphrase":"..."}.
func (h *Handlers) AdminBackupDeployment(w http.ResponseWriter, r *http.Request) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var body struct {
		Passphrase string `json:"passphrase"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		jsonErr(w, 400, "Invalid JSON body.")
		return
	}
	if err := service.ValidateBackupPassphrase(body.Passphrase); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}

	// Resolve the deployment up front so we can name the file. Service
	// layer will revalidate, but a 404 before headers is friendlier.
	deploy, _ := h.svc.Store.GetDeployment(id)
	if deploy == nil {
		jsonErr(w, 404, "Deployment not found.")
		return
	}

	base := deploy.Name
	if base == "" {
		base = deploy.ID
	}
	filename := fmt.Sprintf("ob-deploy-%s-%s.obdp", base, time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	if err := h.svc.DeploymentBackup(id, body.Passphrase, h.version, user.Name, w); err != nil {
		// Headers already sent — best effort log, no further response.
		fmt.Fprintf(w, "\n[ERROR] %s\n", err.Error())
		return
	}
}

// AdminRestoreDeployment ingests an encrypted deployment archive and
// always creates a new deployment with a fresh ID + subdomain.
// POST /api/admin/deployments/restore, multipart form fields:
//   backup (file), passphrase, owner (optional), skipMissingSecrets (optional)
func (h *Handlers) AdminRestoreDeployment(w http.ResponseWriter, r *http.Request) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<30) // 10 GiB
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonErr(w, 400, "Failed to parse upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		jsonErr(w, 400, "No backup file uploaded. Use field name 'backup'.")
		return
	}
	defer file.Close()

	passphrase := r.FormValue("passphrase")
	opts := service.DeploymentRestoreOpts{
		Owner:              r.FormValue("owner"),
		SkipMissingSecrets: r.FormValue("skipMissingSecrets") == "true",
	}

	result, sErr := h.svc.DeploymentRestore(file, passphrase, user, opts)
	if sErr != nil {
		writeErr(w, sErr)
		return
	}
	jsonResp(w, 200, result)
}
