package service

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
	"github.com/rs/xid"
)

// DeploymentBackupVersion is the manifest schema version. Bump when the
// manifest shape changes incompatibly so old archives are rejected with
// a clear error rather than silently mis-restored.
const DeploymentBackupVersion = 1

// DeploymentManifest is the JSON document at the top of every per-deployment
// archive. Captures everything restore needs to recreate the deployment row
// without carrying transient fields (id, subdomain, container_id, port,
// status, created_at, user_id) which restore regenerates.
type DeploymentManifest struct {
	Version           int                `json:"version"`
	CreatedAt         string             `json:"createdAt"`
	CreatedBy         string             `json:"createdBy"`
	OpenberthVersion  string             `json:"openberthVersion"`
	OriginalID        string             `json:"originalId"`
	OriginalSubdomain string             `json:"originalSubdomain"`
	OriginalOwnerName string             `json:"originalOwnerName"`
	Deployment        DeploymentSnapshot `json:"deployment"`
}

// DeploymentSnapshot is the round-trippable subset of store.Deployment.
// AccessHash is shipped opaquely (already a bcrypt hash or API key
// string in storage form). SecretRefs are names only — actual secret
// material lives in the encrypted secrets table and is not exported.
type DeploymentSnapshot struct {
	Name         string   `json:"name"`
	Framework    string   `json:"framework"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	TTLHours     int      `json:"ttlHours"`
	Mode         string   `json:"mode"`
	Memory       string   `json:"memory"`
	CPUs         string   `json:"cpus"`
	NetworkQuota string   `json:"networkQuota"`
	EnvJSON      string   `json:"envJson"`
	AccessMode   string   `json:"accessMode"`
	AccessUser   string   `json:"accessUser"`
	AccessHash   string   `json:"accessHash"`
	AccessUsers  string   `json:"accessUsers"`
	SecretRefs   []string `json:"secretRefs"`
}

// bandwidthHistory is a thin shape so manifest readers don't have to know
// about the store package. Mirrors store.BandwidthPeriod field-for-field.
type bandwidthHistory struct {
	Periods []store.BandwidthPeriod `json:"periods"`
}

// DeploymentRestoreOpts are the knobs the admin can twist on restore.
type DeploymentRestoreOpts struct {
	// Owner is the username that should own the restored deployment.
	// Defaults to the calling admin when empty.
	Owner string
	// SkipMissingSecrets allows restore to proceed when the archive's
	// secret references don't exist on the target. The deployment will
	// build but probably fail at runtime — useful for cross-server
	// migrations where the operator plans to create the secrets after.
	SkipMissingSecrets bool
}

// DeploymentRestoreResult is the JSON shape returned to the caller.
type DeploymentRestoreResult struct {
	ID                string   `json:"id"`
	Subdomain         string   `json:"subdomain"`
	Status            string   `json:"status"`
	OriginalID        string   `json:"originalId"`
	OriginalSubdomain string   `json:"originalSubdomain"`
	SecretsMissing    []string `json:"secretsMissing,omitempty"`
}

// DeploymentBackup writes the encrypted per-deployment archive to w.
// Streams through Argon2id+AES-GCM (via WrapBackup) into a gzipped tar
// containing manifest.json, source/, persist/, and (when the deployment
// has any usage) bandwidth.json.
func (svc *Service) DeploymentBackup(deployID, passphrase, openberthVersion, adminName string, w io.Writer) error {
	if err := ValidateBackupPassphrase(passphrase); err != nil {
		return ErrBadRequest(err.Error())
	}
	deploy, _ := svc.Store.GetDeployment(deployID)
	if deploy == nil {
		return ErrNotFound("Deployment not found.")
	}

	ownerName := ""
	if u, _ := svc.Store.GetUserByID(deploy.UserID); u != nil {
		ownerName = u.Name
	}

	// Flush per-deployment datastore handles so the SQLite WAL is folded
	// into the main DB file before we tar the persist tree. Mirrors the
	// server-wide backup path's CloseAll() call.
	if svc.DataStore != nil {
		svc.DataStore.CloseAll()
	}

	aad := BackupAAD{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		AdminUser: adminName,
		Version:   openberthVersion,
	}
	wrapped, err := WrapBackup(w, passphrase, DeploymentBackupMagic, aad)
	if err != nil {
		return ErrInternal("Failed to wrap backup: " + err.Error())
	}
	defer wrapped.Close()

	gz := gzip.NewWriter(wrapped)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	manifest := DeploymentManifest{
		Version:           DeploymentBackupVersion,
		CreatedAt:         aad.Timestamp,
		CreatedBy:         adminName,
		OpenberthVersion:  openberthVersion,
		OriginalID:        deploy.ID,
		OriginalSubdomain: deploy.Subdomain,
		OriginalOwnerName: ownerName,
		Deployment: DeploymentSnapshot{
			Name:         deploy.Name,
			Framework:    deploy.Framework,
			Title:        deploy.Title,
			Description:  deploy.Description,
			TTLHours:     deploy.TTLHours,
			Mode:         deploy.Mode,
			Memory:       deploy.Memory,
			CPUs:         deploy.CPUs,
			NetworkQuota: deploy.NetworkQuota,
			EnvJSON:      deploy.EnvJSON,
			AccessMode:   deploy.AccessMode,
			AccessUser:   deploy.AccessUser,
			AccessHash:   deploy.AccessHash,
			AccessUsers:  deploy.AccessUsers,
			SecretRefs:   parseSecretsJSON(deploy.SecretsJSON),
		},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ErrInternal("Failed to marshal manifest: " + err.Error())
	}
	if err := writeBytesToTar(tw, "manifest.json", manifestBytes); err != nil {
		return ErrInternal("Failed to write manifest: " + err.Error())
	}

	// source/ — exclude .openberth/ (regenerated at build time).
	sourceDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)
	if _, err := os.Stat(sourceDir); err == nil {
		writeDirToTarFiltered(tw, sourceDir, "source", ".openberth")
	}

	// persist/ — full copy.
	persistDir := filepath.Join(svc.Cfg.PersistDir, deploy.ID)
	if _, err := os.Stat(persistDir); err == nil {
		writeDirToTarFiltered(tw, persistDir, "persist", "")
	}

	// bandwidth.json — optional; only if there's history to carry.
	if hist, err := svc.Store.GetBandwidthHistory(deploy.ID, 200); err == nil && len(hist) > 0 {
		bwBytes, _ := json.MarshalIndent(bandwidthHistory{Periods: hist}, "", "  ")
		_ = writeBytesToTar(tw, "bandwidth.json", bwBytes)
	}

	log.Printf("[deploy-backup] %s | by=%s", deploy.Subdomain, adminName)
	return nil
}

// DeploymentRestore decrypts an archive, allocates a fresh deployment ID
// and subdomain, places source + persist on disk, inserts the row, and
// triggers a rebuild. Always creates a new deployment — the archive's
// originalId/originalSubdomain are kept for diagnostics only.
func (svc *Service) DeploymentRestore(in io.Reader, passphrase string, caller *store.User, opts DeploymentRestoreOpts) (*DeploymentRestoreResult, error) {
	if err := ValidateBackupPassphrase(passphrase); err != nil {
		return nil, ErrBadRequest(err.Error())
	}

	plain, _, err := UnwrapBackup(in, passphrase, DeploymentBackupMagic)
	if err != nil {
		return nil, ErrBadRequest("Failed to read deployment backup: " + err.Error())
	}

	stagingDir := filepath.Join(svc.Cfg.DataDir, fmt.Sprintf(".restore-deploy-%d", time.Now().UnixNano()))
	if err := ExtractBackup(plain, stagingDir, 0, 0); err != nil {
		os.RemoveAll(stagingDir)
		return nil, ErrBadRequest("Failed to extract backup: " + err.Error())
	}

	manifestBytes, err := os.ReadFile(filepath.Join(stagingDir, "manifest.json"))
	if err != nil {
		os.RemoveAll(stagingDir)
		return nil, ErrBadRequest("Backup is missing manifest.json.")
	}
	var manifest DeploymentManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		os.RemoveAll(stagingDir)
		return nil, ErrBadRequest("Invalid manifest.json: " + err.Error())
	}
	if manifest.Version != DeploymentBackupVersion {
		os.RemoveAll(stagingDir)
		return nil, ErrBadRequest(fmt.Sprintf("Unsupported manifest version %d (expected %d).", manifest.Version, DeploymentBackupVersion))
	}

	targetUsername := opts.Owner
	if targetUsername == "" && caller != nil {
		targetUsername = caller.Name
	}
	target, _ := svc.Store.GetUserByName(targetUsername)
	if target == nil {
		os.RemoveAll(stagingDir)
		return nil, ErrBadRequest(fmt.Sprintf("Target owner %q does not exist on this server.", targetUsername))
	}

	var missing []string
	for _, name := range manifest.Deployment.SecretRefs {
		if s, _ := svc.Store.GetSecret(target.ID, name); s == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 && !opts.SkipMissingSecrets {
		os.RemoveAll(stagingDir)
		return nil, ErrConflict(fmt.Sprintf("Target server is missing secrets: %s. Restore with skipMissingSecrets=true to proceed (the deployment will fail at runtime until they are created).", strings.Join(missing, ", ")))
	}

	newID := xid.New().String()
	base := manifest.OriginalSubdomain
	if base == "" {
		base = SanitizeName(manifest.Deployment.Name)
	}
	if base == "" {
		base = "restored"
	}
	subdomain := base
	for suffix := 2; suffix < 1000; suffix++ {
		if existing, _ := svc.Store.GetDeploymentBySubdomain(subdomain); existing == nil {
			break
		}
		subdomain = fmt.Sprintf("%s-%d", base, suffix)
		if suffix == 999 {
			os.RemoveAll(stagingDir)
			return nil, ErrInternal("Could not find a free subdomain after 1000 attempts.")
		}
	}

	targetSource := filepath.Join(svc.Cfg.DeploysDir, newID)
	targetPersist := filepath.Join(svc.Cfg.PersistDir, newID)
	stagedSource := filepath.Join(stagingDir, "source")
	stagedPersist := filepath.Join(stagingDir, "persist")

	if err := os.MkdirAll(filepath.Dir(targetSource), 0755); err != nil {
		os.RemoveAll(stagingDir)
		return nil, ErrInternal("Failed to prepare deploys dir: " + err.Error())
	}
	if _, err := os.Stat(stagedSource); err == nil {
		if err := os.Rename(stagedSource, targetSource); err != nil {
			os.RemoveAll(stagingDir)
			return nil, ErrInternal("Failed to place source: " + err.Error())
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetPersist), 0755); err != nil {
		os.RemoveAll(targetSource)
		os.RemoveAll(stagingDir)
		return nil, ErrInternal("Failed to prepare persist dir: " + err.Error())
	}
	if _, err := os.Stat(stagedPersist); err == nil {
		if err := os.Rename(stagedPersist, targetPersist); err != nil {
			os.RemoveAll(targetSource)
			os.RemoveAll(stagingDir)
			return nil, ErrInternal("Failed to place persist: " + err.Error())
		}
	}

	nameForRow := manifest.Deployment.Name
	if nameForRow == "" {
		nameForRow = subdomain
	}
	deploy := &store.Deployment{
		ID:           newID,
		UserID:       target.ID,
		Name:         nameForRow,
		Subdomain:    subdomain,
		Framework:    manifest.Deployment.Framework,
		Status:       "building",
		TTLHours:     manifest.Deployment.TTLHours,
		ExpiresAt:    computeExpiry(manifest.Deployment.TTLHours),
		EnvJSON:      manifest.Deployment.EnvJSON,
		Title:        manifest.Deployment.Title,
		Description:  manifest.Deployment.Description,
		Mode:         manifest.Deployment.Mode,
		NetworkQuota: manifest.Deployment.NetworkQuota,
		Memory:       manifest.Deployment.Memory,
		CPUs:         manifest.Deployment.CPUs,
	}
	if err := svc.Store.CreateDeployment(deploy); err != nil {
		os.RemoveAll(targetSource)
		os.RemoveAll(targetPersist)
		os.RemoveAll(stagingDir)
		return nil, ErrInternal("Failed to create deployment row: " + err.Error())
	}

	if manifest.Deployment.AccessMode != "" && manifest.Deployment.AccessMode != "public" {
		svc.Store.UpdateDeploymentAccess(newID, manifest.Deployment.AccessMode, manifest.Deployment.AccessUser, manifest.Deployment.AccessHash, manifest.Deployment.AccessUsers)
	}

	if len(manifest.Deployment.SecretRefs) > 0 {
		svc.Store.UpdateDeploymentSecrets(newID, manifest.Deployment.SecretRefs)
	}

	bwPath := filepath.Join(stagingDir, "bandwidth.json")
	if data, err := os.ReadFile(bwPath); err == nil {
		var hist bandwidthHistory
		if err := json.Unmarshal(data, &hist); err == nil {
			for _, p := range hist.Periods {
				svc.Store.AddBandwidth(newID, p.PeriodStart, p.BytesOut)
			}
		}
	}

	os.RemoveAll(stagingDir)

	svc.RebuildOne(newID, "deploy-restore")

	log.Printf("[deploy-restore] %s -> %s | owner=%s | from=%s", manifest.OriginalSubdomain, subdomain, target.Name, manifest.OriginalID)

	return &DeploymentRestoreResult{
		ID:                newID,
		Subdomain:         subdomain,
		Status:            "building",
		OriginalID:        manifest.OriginalID,
		OriginalSubdomain: manifest.OriginalSubdomain,
		SecretsMissing:    missing,
	}, nil
}

// ── tar helpers (local to deploy backup) ────────────────────────────

// writeBytesToTar writes a single in-memory blob as a tar entry.
func writeBytesToTar(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), ModTime: time.Now()}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// writeDirToTarFiltered walks srcDir and writes every entry to tw under
// tarPrefix. When skipPrefix is non-empty, entries whose path-relative-
// to-srcDir starts with it are skipped (and dirs are pruned from the
// walk). Used to exclude .openberth/ from per-deployment archives.
func writeDirToTarFiltered(tw *tar.Writer, srcDir, tarPrefix, skipPrefix string) {
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		if rel == "." {
			return nil
		}
		if skipPrefix != "" && strings.HasPrefix(rel, skipPrefix) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		tarPath := filepath.Join(tarPrefix, rel)
		if info.IsDir() {
			tw.WriteHeader(&tar.Header{Name: tarPath + "/", Typeflag: tar.TypeDir, Mode: int64(info.Mode()), ModTime: info.ModTime()})
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
