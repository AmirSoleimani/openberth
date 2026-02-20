package service

import (
	"log"
	"os"
	"path/filepath"

	"github.com/openberth/openberth/apps/server/internal/store"
)

// ── Destroy Full ────────────────────────────────────────────────────

// DestroyFull removes a deployment completely: container, proxy route,
// data store, source code, persistent data, and DB record.
func (svc *Service) DestroyFull(d *store.Deployment) {
	svc.Container.Destroy(d.ID)
	svc.Proxy.RemoveRoute(d.Subdomain)
	svc.DataStore.DeleteDB(d.ID)
	os.RemoveAll(filepath.Join(svc.Cfg.DeploysDir, d.ID))
	os.RemoveAll(filepath.Join(svc.Cfg.PersistDir, d.ID))
	svc.Store.DeleteDeployment(d.ID)
}

// ── Cleanup ─────────────────────────────────────────────────────────

// RunCleanup removes expired deployments, expired OAuth data and sessions,
// and runs a health sweep. Returns the number of expired deployments removed.
func (svc *Service) RunCleanup() int {
	expired, err := svc.Store.GetExpiredDeployments()
	if err != nil {
		return 0
	}
	count := 0
	for _, d := range expired {
		svc.DestroyFull(&d)
		count++
	}
	if count > 0 {
		log.Printf("[cleanup] Removed %d expired deployment(s)", count)
	}
	svc.Store.DeleteExpiredOAuthData()
	svc.Store.DeleteExpiredSessions()

	// Health sweep: detect containers that died during normal operation
	svc.healthSweep()

	return count
}

// healthSweep checks all "running" deployments and restarts or marks failed
// any containers that have crashed (OOM, application panic, etc.).
func (svc *Service) healthSweep() {
	running, err := svc.Store.ListDeploymentsByStatus("running")
	if err != nil {
		return
	}
	for _, d := range running {
		status := svc.Container.Status(d.ID)
		if status == "running" {
			continue
		}
		log.Printf("[cleanup] Container for %s is %s", d.Subdomain, status)
		if svc.Container.Restart(d.ID) {
			log.Printf("[cleanup] Restored %s after restart", d.Subdomain)
		} else {
			log.Printf("[cleanup] Failed to restart %s, marking as failed", d.Subdomain)
			svc.Store.UpdateDeploymentStatus(d.ID, "failed")
		}
	}
}

// ── Network Quota Reset ─────────────────────────────────────────────

// RunQuotaReset unblocks all deployments that were blocked due to bandwidth
// quota, effectively starting a new billing period.
func (svc *Service) RunQuotaReset() int {
	if svc.Bandwidth != nil {
		svc.Bandwidth.UnblockAll()
	}
	log.Printf("[quota-reset] Bandwidth quota period reset")
	return 0
}

// ── Startup Reconciliation ──────────────────────────────────────────

// ReconcileOnStartup restores proxy routes for running containers and
// cleans up stale state left behind by a server or host crash.
func (svc *Service) ReconcileOnStartup() {
	deploys, err := svc.Store.ListDeploymentsByStatus("running", "building", "updating")
	if err != nil {
		log.Printf("[reconcile] Failed to query deployments: %v", err)
		return
	}
	if len(deploys) == 0 {
		log.Printf("[reconcile] No deployments to reconcile")
		return
	}

	log.Printf("[reconcile] Checking %d deployment(s)...", len(deploys))
	restored := 0

	for _, d := range deploys {
		switch d.Status {
		case "building", "updating":
			// The build goroutine is gone after a crash — mark as failed
			log.Printf("[reconcile] Marking stale %s deployment %s as failed", d.Status, d.Subdomain)
			svc.Store.UpdateDeploymentStatus(d.ID, "failed")

		case "running":
			containerStatus := svc.Container.Status(d.ID)
			switch containerStatus {
			case "running":
				// Container is alive — read its actual port and restore proxy route
				port := svc.Container.InspectPort(d.ID)
				if port == 0 {
					log.Printf("[reconcile] Cannot determine port for %s, marking failed", d.Subdomain)
					svc.Store.UpdateDeploymentStatus(d.ID, "failed")
					continue
				}
				ac := AccessControlFromDeployment(&d)
				svc.Proxy.AddRouteNoReload(d.Subdomain, port, ac)
				restored++
				log.Printf("[reconcile] Restored %s on port %d", d.Subdomain, port)

			default:
				// Container is exited/not_found — try to restart
				log.Printf("[reconcile] Container for %s is %s, attempting restart", d.Subdomain, containerStatus)
				if svc.Container.Restart(d.ID) {
					port := svc.Container.InspectPort(d.ID)
					if port > 0 {
						ac := AccessControlFromDeployment(&d)
						svc.Proxy.AddRouteNoReload(d.Subdomain, port, ac)
						restored++
						log.Printf("[reconcile] Restarted and restored %s on port %d", d.Subdomain, port)
					} else {
						log.Printf("[reconcile] Restarted %s but cannot determine port, marking failed", d.Subdomain)
						svc.Store.UpdateDeploymentStatus(d.ID, "failed")
					}
				} else {
					log.Printf("[reconcile] Failed to restart %s, marking as failed", d.Subdomain)
					svc.Store.UpdateDeploymentStatus(d.ID, "failed")
				}
			}
		}
	}

	// Clean up orphaned Caddy files that don't match any running deployment
	svc.cleanOrphanedCaddyFiles()

	// Single Caddy reload at the end
	svc.Proxy.Reload()
	log.Printf("[reconcile] Done — restored %d deployment(s)", restored)
}

// cleanOrphanedCaddyFiles removes .caddy config files that have no matching
// running deployment in the database.
func (svc *Service) cleanOrphanedCaddyFiles() {
	caddySubdomains := svc.Proxy.ListCaddyFiles()
	if len(caddySubdomains) == 0 {
		return
	}

	// Build a set of subdomains that should have Caddy files
	active, _ := svc.Store.ListDeploymentsByStatus("running")
	activeSet := make(map[string]bool, len(active))
	for _, d := range active {
		activeSet[d.Subdomain] = true
	}

	removed := 0
	for _, sub := range caddySubdomains {
		if !activeSet[sub] {
			svc.Proxy.RemoveRouteNoReload(sub)
			removed++
		}
	}
	if removed > 0 {
		log.Printf("[reconcile] Removed %d orphaned Caddy config file(s)", removed)
	}
}
