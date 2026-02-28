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

	"github.com/AmirSoleimani/openberth/apps/server/internal/container"
	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// ExtractTarball extracts a gzipped tar archive into destDir.
func ExtractTarball(file io.Reader, destDir string) error {
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
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
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			// Limit file size to 100MB to prevent decompression bombs
			if _, err := io.Copy(f, io.LimitReader(tr, 100<<20)); err != nil {
				f.Close()
				return err
			}
			f.Close()
		default:
			// Reject symlinks, hard links, and device files
			log.Printf("[tar] Skipping non-regular entry type=%d: %s", hdr.Typeflag, hdr.Name)
			continue
		}
	}
	return nil
}

// ExtractBackup extracts a tar.gz backup into the data directory.
// Validates paths to prevent path traversal.
func ExtractBackup(file io.Reader, dataDir string) error {
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	cleanBase := filepath.Clean(dataDir)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
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
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", clean, err)
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 1<<30)); err != nil {
				f.Close()
				return fmt.Errorf("write file %s: %w", clean, err)
			}
			f.Close()
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

	if err := ExtractTarball(p.File, codeDir); err != nil {
		os.RemoveAll(codeDir)
		return nil, ErrBadRequest("Failed to extract tarball: " + err.Error())
	}

	fw, err := detectFrameworkOrFail(codeDir)
	if err != nil {
		return nil, err
	}

	ttlHours := ParseTTL(p.TTL, user.DefaultTTLHours)
	envVars := ensureEnv(p.EnvVars)
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
		EnvJSON:      marshalEnv(envVars),
		ExpiresAt:    expiresAt,
		NetworkQuota: resolvedQuota,
		Memory:       p.Memory,
		CPUs:         p.CPUs,
	})

	aci, err := svc.setupAccessControl(id, codeDir, subdomain, p.ProtectMode, p.ProtectUsername, p.ProtectPassword, p.ProtectApiKey, p.ProtectUsers)
	if err != nil {
		return nil, err
	}

	svc.buildAndStart(buildStartParams{
		ID: id, UserID: user.ID, UserName: user.Name,
		CodeDir: codeDir, Subdomain: subdomain,
		Memory: p.Memory, CPUs: p.CPUs, NetworkQuota: resolvedQuota,
		LogPrefix: "deploy", FW: fwInfo(fw), Port: port,
		EnvVars: envVars, AC: aci,
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
	if err := ExtractTarball(p.File, codeDir); err != nil {
		return nil, ErrBadRequest("Failed to extract: " + err.Error())
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

		svc.Container.Destroy(d.ID)
		svc.Store.UpdateDeploymentStatus(d.ID, "building")
		rebuilding++

		go func(deploy store.Deployment, fw *framework.FrameworkInfo, env map[string]string) {
			ac := AccessControlFromDeployment(&deploy)
			result, err := svc.Container.Create(container.CreateOpts{
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
			svc.Store.UpdateDeploymentRunning(deploy.ID, result.ContainerID, result.HostPort)
			svc.Proxy.AddRoute(deploy.Subdomain, result.HostPort, ac)
			log.Printf("[restore] Rebuilt %s on port %d", deploy.Subdomain, result.HostPort)
		}(d, fw, userEnv)
	}

	return rebuilding
}
