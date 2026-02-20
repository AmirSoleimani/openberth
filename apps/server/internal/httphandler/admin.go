package httphandler

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/openberth/openberth/apps/server/internal/service"
	"github.com/openberth/openberth/apps/server/internal/store"
)

// allowedSettings is the set of settings keys that can be modified via the admin API.
var allowedSettings = map[string]bool{
	"oidc.issuer":                  true,
	"oidc.client_id":               true,
	"oidc.client_secret":           true,
	"oidc.mode":                    true,
	"oidc.allowed_domains":         true,
	"session.ttl_hours":            true,
	"network.quota_enabled":        true,
	"network.default_quota":        true,
	"network.quota_reset_interval": true,
}

// ── Admin: Users ───────────────────────────────────────────────────

func (h *Handlers) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}

	users, _ := h.svc.Store.ListUsers()
	if users == nil {
		users = []store.User{}
	}
	jsonResp(w, 200, map[string]interface{}{"users": users})
}

func (h *Handlers) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}

	var body struct {
		Name           string `json:"name"`
		Password       string `json:"password"`
		MaxDeployments int    `json:"maxDeployments"`
		TTLHours       int    `json:"ttlHours"`
	}
	if err := decodeJSONBody(r, &body); err != nil || body.Name == "" {
		jsonErr(w, 400, "Name is required.")
		return
	}

	if body.Password != "" && len(body.Password) < 8 {
		jsonErr(w, 400, "Password must be at least 8 characters.")
		return
	}

	if body.MaxDeployments == 0 {
		body.MaxDeployments = h.svc.Cfg.DefaultMaxDeploy
	}
	if body.TTLHours == 0 {
		body.TTLHours = h.svc.Cfg.DefaultTTLHours
	}

	newUser := &store.User{
		ID:              "usr_" + service.RandomHex(8),
		Name:            body.Name,
		APIKey:          "sc_" + service.RandomHex(24),
		Role:            "user",
		MaxDeployments:  body.MaxDeployments,
		DefaultTTLHours: body.TTLHours,
	}

	if body.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonErr(w, 500, "Failed to hash password.")
			return
		}
		newUser.PasswordHash = string(hash)
	}

	if err := h.svc.Store.CreateUser(newUser); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			jsonErr(w, 409, fmt.Sprintf("User '%s' already exists.", body.Name))
			return
		}
		jsonErr(w, 500, "Failed to create user.")
		return
	}

	jsonResp(w, 201, map[string]interface{}{
		"id":             newUser.ID,
		"name":           newUser.Name,
		"apiKey":         newUser.APIKey,
		"maxDeployments": newUser.MaxDeployments,
	})
}

func (h *Handlers) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}

	name := r.PathValue("name")
	deleted, _ := h.svc.Store.DeleteUser(name)
	if !deleted {
		jsonErr(w, 404, "User not found.")
		return
	}
	jsonResp(w, 200, map[string]string{"deleted": name})
}

// ── Admin: Update User ──────────────────────────────────────────

func (h *Handlers) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}

	name := r.PathValue("name")

	var body struct {
		Password       string  `json:"password"`
		DisplayName    *string `json:"displayName"`
		MaxDeployments *int    `json:"maxDeployments"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		jsonErr(w, 400, "Invalid JSON.")
		return
	}

	target, _ := h.svc.Store.GetUserByName(name)
	if target == nil {
		jsonErr(w, 404, "User not found.")
		return
	}

	if body.Password != "" {
		if len(body.Password) < 8 {
			jsonErr(w, 400, "Password must be at least 8 characters.")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonErr(w, 500, "Failed to hash password.")
			return
		}
		h.svc.Store.UpdateUserPassword(target.ID, string(hash))
	}

	if body.MaxDeployments != nil {
		h.svc.Store.UpdateUserMaxDeployments(target.ID, *body.MaxDeployments)
	}

	if body.DisplayName != nil {
		h.svc.Store.UpdateUserDisplayName(target.ID, *body.DisplayName)
	}

	jsonResp(w, 200, map[string]string{"message": "User updated.", "name": name})
}

// ── Admin: Settings ─────────────────────────────────────────────

func (h *Handlers) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}

	settings, _ := h.svc.Store.GetSettings("")
	if settings == nil {
		settings = map[string]string{}
	}
	// Mask the client secret
	if _, ok := settings["oidc.client_secret"]; ok {
		settings["oidc.client_secret"] = "***"
	}

	jsonResp(w, 200, settings)
}

func (h *Handlers) AdminSetSettings(w http.ResponseWriter, r *http.Request) {
	if h.requireAdmin(w, r) == nil {
		return
	}

	var body map[string]string
	if err := decodeJSONBody(r, &body); err != nil {
		jsonErr(w, 400, "Invalid JSON.")
		return
	}

	for key, value := range body {
		if !allowedSettings[key] {
			jsonErr(w, 400, fmt.Sprintf("Setting '%s' is not allowed.", key))
			return
		}
		if err := h.svc.Store.SetSetting(key, value); err != nil {
			jsonErr(w, 500, "Failed to save setting.")
			return
		}
	}

	jsonResp(w, 200, map[string]string{"message": "Settings updated."})
}

// ── Admin: Backup ──────────────────────────────────────────────────

func (h *Handlers) AdminBackup(w http.ResponseWriter, r *http.Request) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}

	// Flush datastore WAL files before backup
	h.svc.DataStore.CloseAll()

	// Checkpoint main DB WAL
	if err := h.svc.Store.Checkpoint(); err != nil {
		log.Printf("[backup] WAL checkpoint warning: %v", err)
	}

	// Set response headers for streaming download
	filename := fmt.Sprintf("openberth-backup-%s.tar.gz", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	dataDir := h.svc.Cfg.DataDir

	// Add config.json
	addFileToTar(tw, filepath.Join(dataDir, "config.json"), "config.json")

	// Add openberth.db
	addFileToTar(tw, filepath.Join(dataDir, "openberth.db"), "openberth.db")

	// Add deploys/ tree
	addDirToTar(tw, filepath.Join(dataDir, "deploys"), "deploys")

	// Add persist/ tree
	addDirToTar(tw, filepath.Join(dataDir, "persist"), "persist")

	log.Printf("[backup] Backup streamed to admin user %s", user.Name)
}

// addFileToTar adds a single file to a tar archive.
func addFileToTar(tw *tar.Writer, srcPath, tarPath string) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return
	}
	header.Name = tarPath
	if err := tw.WriteHeader(header); err != nil {
		return
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(tw, f)
}

// addDirToTar recursively adds a directory tree to a tar archive.
func addDirToTar(tw *tar.Writer, srcDir, tarPrefix string) {
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || rel == "." {
			return nil
		}
		tarPath := filepath.Join(tarPrefix, rel)

		if info.IsDir() {
			header := &tar.Header{
				Name:     tarPath + "/",
				Typeflag: tar.TypeDir,
				Mode:     int64(info.Mode()),
				ModTime:  info.ModTime(),
			}
			tw.WriteHeader(header)
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = tarPath
		if err := tw.WriteHeader(header); err != nil {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		io.Copy(tw, f)
		return nil
	})
}

// ── Admin: Restore ─────────────────────────────────────────────────

func (h *Handlers) AdminRestore(w http.ResponseWriter, r *http.Request) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}

	// Limit upload to 10GB
	r.Body = http.MaxBytesReader(w, r.Body, 10<<30)

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

	// 1. Stop all running containers
	deploys, _ := h.svc.Store.ListDeploymentsByStatus("running", "building", "updating")
	for _, d := range deploys {
		h.svc.Container.Destroy(d.ID)
	}

	// 2. Remove all Caddy site configs
	h.svc.Proxy.RemoveAllRoutes()

	// 3. Close datastores and store
	h.svc.DataStore.CloseAll()
	h.svc.Store.Close()

	// 4. Extract archive into dataDir
	dataDir := h.svc.Cfg.DataDir
	if err := service.ExtractBackup(file, dataDir); err != nil {
		// Try to reopen store even on error
		h.svc.Store.Reopen(h.svc.Cfg.DBPath)
		jsonErr(w, 500, "Failed to extract backup: "+err.Error())
		return
	}

	// 5. Reopen store (cleans stale WAL/SHM files + runs migrations)
	if err := h.svc.Store.Reopen(h.svc.Cfg.DBPath); err != nil {
		jsonErr(w, 500, "Failed to reopen database: "+err.Error())
		return
	}

	// 6. Rebuild deployments from source
	rebuilding := h.svc.RebuildAll()

	// 7. Count restored items
	userCount, _ := h.svc.Store.CountUsers()
	allDeploys, _ := h.svc.Store.ListDeployments("")

	log.Printf("[restore] Backup restored by admin user %s: %d users, %d deployments (%d rebuilding)",
		user.Name, userCount, len(allDeploys), rebuilding)

	jsonResp(w, 200, map[string]interface{}{
		"message":     "Backup restored successfully. Deployments are rebuilding in the background — TLS certificates may take a few minutes to provision, so expect brief SSL errors until then.",
		"users":       userCount,
		"deployments": len(allDeploys),
		"rebuilding":  rebuilding,
	})
}
