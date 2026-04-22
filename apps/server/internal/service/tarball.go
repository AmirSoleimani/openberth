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

	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// Extraction caps used when the caller does not specify its own.
// Individually bounded sizes protect against decompression bombs; the
// cumulative byte cap protects against many-file disk-fill attacks;
// the entry-count cap protects against inode exhaustion.
const (
	DefaultMaxTarFileBytes int64 = 100 << 20 // per single entry
	DefaultMaxTarBytes     int64 = 2 << 30   // cumulative across all entries
	DefaultMaxTarEntries   int   = 50_000    // total regular+directory entries
)

// ExtractTarball extracts a gzipped tar archive into destDir.
// maxBytes and maxEntries set cumulative caps; 0 for either means use
// DefaultMaxTarBytes / DefaultMaxTarEntries. Individual files are always
// capped to DefaultMaxTarFileBytes.
func ExtractTarball(file io.Reader, destDir string, maxBytes int64, maxEntries int) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTarBytes
	}
	if maxEntries <= 0 {
		maxEntries = DefaultMaxTarEntries
	}

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var totalBytes int64
	var entries int
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		entries++
		if entries > maxEntries {
			return fmt.Errorf("tar: archive exceeds entry cap (%d > %d)", entries, maxEntries)
		}

		// Security: prevent path traversal
		target := filepath.Join(destDir, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)) {
			log.Printf("[tar] Blocked path traversal attempt: %s", hdr.Name)
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			// Refuse before opening the file so a crafted archive can't fill disk
			// one entry at a time by summing just-under-per-file-cap entries.
			if totalBytes+hdr.Size > maxBytes {
				return fmt.Errorf("tar: archive exceeds cumulative byte cap (%d > %d)", totalBytes+hdr.Size, maxBytes)
			}

			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, DefaultMaxTarFileBytes))
			if err != nil {
				f.Close()
				return err
			}
			f.Close()
			totalBytes += n
			if totalBytes > maxBytes {
				return fmt.Errorf("tar: archive exceeds cumulative byte cap (%d > %d)", totalBytes, maxBytes)
			}
		default:
			// Reject symlinks, hard links, and device files
			log.Printf("[tar] Skipping non-regular entry type=%d: %s", hdr.Typeflag, hdr.Name)
			continue
		}
	}
	return nil
}

// Per-file cap for backup entries — backups may legitimately contain a
// multi-GB SQLite DB, so the per-entry cap is larger than for deploy tarballs.
const DefaultMaxBackupFileBytes int64 = 10 << 30 // 10 GiB per entry

// ExtractBackup extracts a tar.gz backup into the data directory.
// Validates paths to prevent path traversal. Applies the same
// cumulative/entry caps as ExtractTarball unless the caller overrides.
func ExtractBackup(file io.Reader, dataDir string, maxBytes int64, maxEntries int) error {
	if maxBytes <= 0 {
		// Backups are larger than deploy tarballs; use a roomier default.
		maxBytes = 50 << 30 // 50 GiB
	}
	if maxEntries <= 0 {
		maxEntries = 1_000_000
	}

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	cleanBase := filepath.Clean(dataDir)
	var totalBytes int64
	var entries int

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		entries++
		if entries > maxEntries {
			return fmt.Errorf("tar: backup exceeds entry cap (%d > %d)", entries, maxEntries)
		}

		// Security: validate path
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue // skip path traversal attempts
		}

		target := filepath.Join(dataDir, clean)
		if !strings.HasPrefix(filepath.Clean(target), cleanBase) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			if totalBytes+hdr.Size > maxBytes {
				return fmt.Errorf("tar: backup exceeds cumulative byte cap (%d > %d)", totalBytes+hdr.Size, maxBytes)
			}
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", clean, err)
			}
			n, err := io.Copy(f, io.LimitReader(tr, DefaultMaxBackupFileBytes))
			if err != nil {
				f.Close()
				return fmt.Errorf("write file %s: %w", clean, err)
			}
			f.Close()
			totalBytes += n
			if totalBytes > maxBytes {
				return fmt.Errorf("tar: backup exceeds cumulative byte cap (%d > %d)", totalBytes, maxBytes)
			}
		}
	}
	return nil
}

// DeployTarball handles a tarball-based deployment.
func (svc *Service) DeployTarball(user *store.User, p TarballDeployParams) (*DeployResult, error) {
	if err := svc.checkDeployLimit(user.ID, user.MaxDeployments); err != nil {
		return nil, err
	}

	id, name, subdomain, err := svc.generateDeployIdentity(p.Name, "app-", "")
	if err != nil {
		return nil, err
	}

	codeDir := filepath.Join(svc.Cfg.DeploysDir, id)
	os.MkdirAll(codeDir, 0755)

	if err := ExtractTarball(p.File, codeDir, svc.Cfg.MaxTarballBytes, svc.Cfg.MaxTarballEntries); err != nil {
		os.RemoveAll(codeDir)
		return nil, ErrBadRequest("Failed to extract tarball: " + err.Error())
	}

	fw, err := detectFrameworkOrFail(codeDir)
	if err != nil {
		return nil, err
	}

	ttlHours := ParseTTL(p.TTL, user.DefaultTTLHours)
	userEnv := ensureEnv(p.EnvVars)
	buildEnvVars, envVars, err := svc.mergeEnvAndSecrets(user.ID, userEnv, p.Secrets)
	if err != nil {
		return nil, err
	}
	port := resolvePort(p.Port, fw.Port)
	expiresAt := computeExpiry(ttlHours)
	resolvedQuota := svc.ResolveNetworkQuota(p.NetworkQuota)

	svc.Store.CreateDeployment(&store.Deployment{
		ID:           id,
		UserID:       user.ID,
		Name:         name,
		Title:        p.Title,
		Description:  p.Description,
		Subdomain:    subdomain,
		Framework:    fw.Framework,
		Status:       "building",
		TTLHours:     ttlHours,
		EnvJSON:      marshalEnv(userEnv),
		ExpiresAt:    expiresAt,
		NetworkQuota: resolvedQuota,
		Memory:       p.Memory,
		CPUs:         p.CPUs,
	})
	svc.Store.UpdateDeploymentSecrets(id, p.Secrets)

	aci, err := svc.setupAccessControl(id, codeDir, subdomain, p.ProtectMode, p.ProtectUsername, p.ProtectPassword, p.ProtectApiKey, p.ProtectUsers)
	if err != nil {
		return nil, err
	}

	svc.buildAndStart(buildStartParams{
		ID: id, UserID: user.ID, UserName: user.Name,
		CodeDir: codeDir, Subdomain: subdomain,
		Memory: p.Memory, CPUs: p.CPUs, NetworkQuota: resolvedQuota,
		LogPrefix: "deploy", FW: fwInfo(fw), Port: port,
		BuildEnvVars: buildEnvVars, EnvVars: envVars, AC: aci,
	})

	var accessMode, apiKey string
	if aci != nil {
		accessMode = aci.Mode
		apiKey = aci.ResultKey
	}
	return &DeployResult{
		ID: id, Name: name, Title: p.Title, Description: p.Description,
		Subdomain: subdomain, Framework: fw.Framework, Language: fw.Language,
		Status: "building", URL: svc.deployURL(subdomain),
		ExpiresAt: expiresAt, AccessMode: accessMode, ApiKey: apiKey,
	}, nil
}

// UpdateTarball handles a tarball-based update.
func (svc *Service) UpdateTarball(user *store.User, p TarballUpdateParams) (*UpdateResult, error) {
	deploy, err := svc.updateOwnerGuard(p.DeployID, user)
	if err != nil {
		return nil, err
	}

	codeDir := filepath.Join(svc.Cfg.DeploysDir, deploy.ID)
	if err := ExtractTarball(p.File, codeDir, svc.Cfg.MaxTarballBytes, svc.Cfg.MaxTarballEntries); err != nil {
		return nil, ErrBadRequest("Failed to extract: " + err.Error())
	}

	// Update stored secrets if provided
	if len(p.Secrets) > 0 {
		svc.Store.UpdateDeploymentSecrets(deploy.ID, p.Secrets)
		deploy.SecretsJSON = marshalSecrets(p.Secrets)
	}

	return svc.detectAndRebuild(deploy, user.Name, p.EnvVars, p.Port, p.Memory, p.CPUs, p.NetworkQuota, "update")
}

// RebuildAll rebuilds all non-destroyed deployments from source code on disk.
// Used after backup restore when containers don't survive.
func (svc *Service) RebuildAll() int {
	deploys, err := svc.Store.ListDeployments("")
	if err != nil {
		log.Printf("[restore] Failed to list deployments: %v", err)
		return 0
	}

	rebuilding := 0
	for _, d := range deploys {
		if d.Status == "destroyed" {
			continue
		}

		codeDir := filepath.Join(svc.Cfg.DeploysDir, d.ID)
		if _, err := os.Stat(codeDir); os.IsNotExist(err) {
			log.Printf("[restore] No source for %s, marking as failed", d.Subdomain)
			svc.Store.UpdateDeploymentStatus(d.ID, "failed")
			continue
		}

		fw := framework.DetectWithOverrides(codeDir)
		if fw == nil {
			log.Printf("[restore] Cannot detect framework for %s, marking as failed", d.Subdomain)
			svc.Store.UpdateDeploymentStatus(d.ID, "failed")
			continue
		}

		userEnv := map[string]string{}
		if d.EnvJSON != "" && d.EnvJSON != "{}" {
			json.Unmarshal([]byte(d.EnvJSON), &userEnv)
		}

		svc.Runtime.Destroy(d.ID)
		svc.Store.UpdateDeploymentStatus(d.ID, "building")
		rebuilding++

		go func(deploy store.Deployment, fw *framework.FrameworkInfo, env map[string]string) {
			ac := AccessControlFromDeployment(&deploy)
			result, err := svc.Runtime.Deploy(runtime.DeployOpts{
				ID:           deploy.ID,
				UserID:       deploy.UserID,
				CodeDir:      filepath.Join(svc.Cfg.DeploysDir, deploy.ID),
				Framework:    fw.Framework,
				Language:     fw.Language,
				Port:         fw.Port,
				Image:        fw.Image,
				RunImage:     fw.RunImage,
				BuildCmd:     fw.BuildCmd,
				StartCmd:     fw.StartCmd,
				InstallCmd:   fw.InstallCmd,
				CacheDir:     fw.CacheDir,
				FrameworkEnv: fw.Env,
				UserEnv:      env,
				NetworkQuota: deploy.NetworkQuota,
				Memory:       deploy.Memory,
				CPUs:         deploy.CPUs,
			})
			if err != nil {
				log.Printf("[restore] Rebuild failed for %s: %v", deploy.Subdomain, err)
				svc.Store.UpdateDeploymentStatus(deploy.ID, "failed")
				return
			}
			svc.Store.UpdateDeploymentRunning(deploy.ID, result.InstanceID, result.Endpoint.Port)
			svc.Proxy.AddRoute(deploy.Subdomain, result.Endpoint.Port, ac)
			log.Printf("[restore] Rebuilt %s on port %d", deploy.Subdomain, result.Endpoint.Port)
		}(d, fw, userEnv)
	}

	return rebuilding
}
