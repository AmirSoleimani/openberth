package httphandler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
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
		APIKey:          service.NewAPIKey(),
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
	target, _ := h.svc.Store.GetUserByName(name)
	if target == nil {
		jsonErr(w, 404, "User not found.")
		return
	}

	// Refuse if the user still owns resources. Admins must destroy deployments,
	// delete user secrets, and delete/reassign globals they created before
	// removing the user. Ephemeral auth state (sessions, oauth codes/tokens,
	// login codes) is cleaned up automatically on success.
	deployments, _ := h.svc.Store.CountActiveDeployments(target.ID)
	userSecrets, _ := h.svc.Store.CountUserSecrets(target.ID)
	createdGlobals, _ := h.svc.Store.CountGlobalsCreatedBy(target.ID)

	if deployments > 0 || userSecrets > 0 || createdGlobals > 0 {
		jsonResp(w, 409, map[string]interface{}{
			"error":          "User has associated resources. Remove them first.",
			"deployments":    deployments,
			"userSecrets":    userSecrets,
			"createdGlobals": createdGlobals,
		})
		return
	}

	if err := h.svc.Store.DeleteUserAuthState(target.ID); err != nil {
		jsonErr(w, 500, "Failed to clear auth state: "+err.Error())
		return
	}
	if _, err := h.svc.Store.DeleteUser(name); err != nil {
		jsonErr(w, 500, "Failed to delete user: "+err.Error())
		return
	}
	log.Printf("[admin] User '%s' deleted", name)
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

// ── Admin: Rotate User API Key ──────────────────────────────────

func (h *Handlers) AdminRotateUserKey(w http.ResponseWriter, r *http.Request) {
	adminUser := h.requireAdmin(w, r)
	if adminUser == nil {
		return
	}

	name := r.PathValue("name")
	target, _ := h.svc.Store.GetUserByName(name)
	if target == nil {
		jsonErr(w, 404, "User not found.")
		return
	}

	newKey := service.NewAPIKey()
	if err := h.svc.Store.UpdateUserAPIKey(target.ID, newKey); err != nil {
		jsonErr(w, 500, "Failed to rotate API key.")
		return
	}

	log.Printf("[rotate-key] Admin '%s' rotated API key for user '%s'", adminUser.Name, target.Name)
	jsonResp(w, 200, map[string]string{"apiKey": newKey, "name": target.Name})
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

// AdminBackup streams a full instance backup as an encrypted tarball.
// The caller must supply a passphrase in the POST body ({"passphrase":"..."}).
// Argon2id-derived key; AES-256-GCM; see service/backup.go for wire format.
func (h *Handlers) AdminBackup(w http.ResponseWriter, r *http.Request) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}

	var body struct {
		Passphrase string `json:"passphrase"`
	}
	if err := decodeJSONBody(r, &body); err != nil && err != io.EOF {
		jsonErr(w, 400, "Invalid JSON body.")
		return
	}
	if err := service.ValidateBackupPassphrase(body.Passphrase); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}

	// Flush datastore WAL files before backup
	h.svc.DataStore.CloseAll()

	// Checkpoint main DB WAL
	if err := h.svc.Store.Checkpoint(); err != nil {
		log.Printf("[backup] WAL checkpoint warning: %v", err)
	}

	// Set response headers for streaming download
	filename := fmt.Sprintf("openberth-backup-%s.obbk", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	aad := service.BackupAAD{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		AdminUser: user.Name,
		Version:   h.version,
	}
	wrapped, err := service.WrapBackup(w, body.Passphrase, aad)
	if err != nil {
		// Header already sent? Best effort — log and bail.
		log.Printf("[backup] wrap failed: %v", err)
		return
	}
	// Close flushes the final GCM block. Separate defer from the writers
	// above so the tar/gz writers flush first.
	defer wrapped.Close()

	gz := gzip.NewWriter(wrapped)
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

	log.Printf("[backup] Encrypted backup streamed to admin user %s", user.Name)
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

// validateStagedBackup checks that a staged backup directory carries the
// minimum shape AdminRestore assumes: a parseable config.json and a
// SQLite DB file. Any other file is tolerated.
func validateStagedBackup(stagingDir string) error {
	configPath := filepath.Join(stagingDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("backup missing config.json: %w", err)
	}
	var probe map[string]interface{}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("backup config.json is not valid JSON: %w", err)
	}
	dbPath := filepath.Join(stagingDir, "openberth.db")
	if info, err := os.Stat(dbPath); err != nil || info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("backup missing or empty openberth.db")
	}
	return nil
}

// stagedMasterKeyMatches returns true when the master key embedded in a
// staged backup's config.json matches the currently running key.
func stagedMasterKeyMatches(stagingDir, liveKey string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, "config.json"))
	if err != nil {
		return false, err
	}
	var staged struct {
		MasterKey string `json:"masterKey"`
	}
	if err := json.Unmarshal(data, &staged); err != nil {
		return false, err
	}
	// Empty staged key is treated as "matching" — a fresh-install backup
	// never had a key yet, so replacement is harmless.
	if staged.MasterKey == "" {
		return true, nil
	}
	return staged.MasterKey == liveKey, nil
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

	passphrase := r.FormValue("passphrase")
	legacy := r.FormValue("legacyUnencrypted") == "true"
	allowMasterKeyReplace := r.FormValue("allowMasterKeyReplace") == "true"

	// Detect the backup format and unwrap if needed. The unwrapped reader
	// then feeds the normal ExtractBackup gzip/tar pipeline.
	var archive io.Reader
	plain, _, uErr := service.UnwrapBackup(file, passphrase)
	if uErr == nil {
		archive = plain
	} else {
		var legacyErr *service.LegacyUnencryptedBackupError
		if errors.As(uErr, &legacyErr) {
			if !legacy {
				jsonErr(w, 400, "This backup is in the pre-passphrase format. Resubmit with form field legacyUnencrypted=true to accept it (upgrade operators are prompted once during the transition).")
				return
			}
			log.Printf("[restore] Accepting legacy unencrypted backup (admin=%s)", user.Name)
			// Re-prepend the already-consumed prefix bytes.
			archive = io.MultiReader(bytes.NewReader(legacyErr.Prefix()), file)
		} else {
			jsonErr(w, 400, "Failed to unwrap backup: "+uErr.Error())
			return
		}
	}

	// Staged extract: write the archive into a sibling directory first, so
	// validation happens before any destructive change to live data. If
	// anything fails before the atomic swap, the live dataDir is untouched.
	dataDir := h.svc.Cfg.DataDir
	stagingDir := dataDir + fmt.Sprintf(".restore-%d", time.Now().UnixNano())
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		jsonErr(w, 500, "Failed to create staging dir: "+err.Error())
		return
	}
	cleanupStaging := func() { os.RemoveAll(stagingDir) }

	if err := service.ExtractBackup(archive, stagingDir, h.svc.Cfg.MaxBackupBytes, h.svc.Cfg.MaxBackupEntries); err != nil {
		cleanupStaging()
		jsonErr(w, 500, "Failed to extract backup: "+err.Error())
		return
	}

	// Validate the staged backup before touching live data.
	if err := validateStagedBackup(stagingDir); err != nil {
		cleanupStaging()
		jsonErr(w, 400, err.Error())
		return
	}

	// Master-key guard: refuse silent key replacement unless explicitly opted
	// in. Replacing the master key orphans every secret in the live DB.
	sameKey, err := stagedMasterKeyMatches(stagingDir, h.svc.Cfg.MasterKey)
	if err != nil {
		cleanupStaging()
		jsonErr(w, 400, "Failed to read staged config: "+err.Error())
		return
	}
	if !sameKey && !allowMasterKeyReplace {
		cleanupStaging()
		jsonErr(w, 409, "The backup has a different master key than the running instance. Set form field allowMasterKeyReplace=true to proceed (this will replace the current master key; existing secrets encrypted with the old key become undecryptable).")
		return
	}

	// From here on, changes are destructive.

	// 1. Stop all running containers
	deploys, _ := h.svc.Store.ListDeploymentsByStatus("running", "building", "updating")
	for _, d := range deploys {
		h.svc.Runtime.Destroy(d.ID)
	}

	// 2. Remove all Caddy site configs
	h.svc.Proxy.RemoveAllRoutes()

	// 3. Close datastores and store
	h.svc.DataStore.CloseAll()
	h.svc.Store.Close()

	// 4. Atomic swap: rename current dataDir aside, then staging into place.
	// On failure, roll the aside back into place so the operator keeps live
	// state intact. Cleanup of the asideDir only happens on full success.
	asideDir := dataDir + fmt.Sprintf(".old-%d", time.Now().UnixNano())
	if err := os.Rename(dataDir, asideDir); err != nil {
		cleanupStaging()
		h.svc.Store.Reopen(h.svc.Cfg.DBPath) // best-effort recover
		jsonErr(w, 500, "Failed to move live dataDir aside: "+err.Error())
		return
	}
	if err := os.Rename(stagingDir, dataDir); err != nil {
		// Roll back: put the live data back in place.
		os.Rename(asideDir, dataDir)
		cleanupStaging()
		h.svc.Store.Reopen(h.svc.Cfg.DBPath) // best-effort recover
		jsonErr(w, 500, "Failed to swap staging into dataDir: "+err.Error())
		return
	}
	// Post-swap cleanup of the aside dir (best-effort).
	defer os.RemoveAll(asideDir)

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
