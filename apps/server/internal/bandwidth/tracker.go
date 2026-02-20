package bandwidth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/openberth/openberth/apps/server/internal/service"
)

// Tracker tails Caddy's structured access log and aggregates
// response bytes per deployment. Periodically flushes totals to SQLite
// and enforces bandwidth quotas by rewriting Caddy site configs.
type Tracker struct {
	svc     *service.Service
	logPath string

	mu      sync.Mutex
	accum   map[string]int64 // subdomain → bytes since last flush
	blocked map[string]bool  // subdomain → currently blocked
}

type caddyLogEntry struct {
	Request struct {
		Host string `json:"host"`
		URI  string `json:"uri"`
	} `json:"request"`
	Size int64 `json:"size"`
}

// NewTracker creates a new bandwidth tracker.
func NewTracker(svc *service.Service, logPath string) *Tracker {
	return &Tracker{
		svc:     svc,
		logPath: logPath,
		accum:   make(map[string]int64),
		blocked: make(map[string]bool),
	}
}

// Run is the main goroutine: tail the log, accumulate bytes, flush periodically.
func (bt *Tracker) Run() {
	if bt.logPath == "" {
		log.Printf("[bandwidth] No access log path configured, tracker disabled")
		return
	}

	// On startup, enforce quotas from existing SQLite data before tailing new entries.
	bt.enforceExisting()

	flushTicker := time.NewTicker(30 * time.Second)
	defer flushTicker.Stop()

	for {
		bt.tailLog(flushTicker)
		// tailLog returns when the file disappears or can't be opened — retry after a pause
		time.Sleep(5 * time.Second)
	}
}

// enforceExisting checks SQLite bandwidth data on startup and blocks any deployments
// that are already over quota.
func (bt *Tracker) enforceExisting() {
	period := service.CurrentPeriodStart(bt.svc.QuotaResetInterval())
	usage, err := bt.svc.Store.GetAllBandwidthForPeriod(period)
	if err != nil {
		return
	}

	needReload := false
	for deployID, used := range usage {
		deploy, _ := bt.svc.Store.GetDeployment(deployID)
		if deploy == nil || deploy.Status != "running" || deploy.NetworkQuota == "" {
			continue
		}
		quota := bt.svc.ResolveNetworkQuota(deploy.NetworkQuota)
		quotaBytes, err := service.ParseSize(quota)
		if err != nil || quotaBytes <= 0 {
			continue
		}
		if used >= quotaBytes {
			bt.mu.Lock()
			bt.blocked[deploy.Subdomain] = true
			bt.mu.Unlock()
			bt.svc.Proxy.BlockRouteNoReload(deploy.Subdomain)
			needReload = true
			log.Printf("[bandwidth] Startup: blocking %s (used %d >= quota %d)", deploy.Subdomain, used, quotaBytes)
		}
	}
	if needReload {
		bt.svc.Proxy.Reload()
	}
}

// tailLog opens the log file and reads lines until the file is rotated or removed.
func (bt *Tracker) tailLog(flushTicker *time.Ticker) {
	f, err := os.Open(bt.logPath)
	if err != nil {
		return
	}
	defer f.Close()

	// Seek to end — we don't want to replay the whole log on startup
	f.Seek(0, 2)

	// Remember inode to detect rotation
	origStat, err := f.Stat()
	if err != nil {
		return
	}

	buf := make([]byte, 32*1024)
	var partial []byte

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			data := buf[:n]
			if len(partial) > 0 {
				data = append(partial, data...)
				partial = nil
			}

			for {
				idx := bytes.IndexByte(data, '\n')
				if idx < 0 {
					partial = append([]byte(nil), data...)
					break
				}
				if idx > 0 {
					bt.processLine(data[:idx])
				}
				data = data[idx+1:]
			}
		}

		if readErr != nil && readErr != io.EOF {
			bt.flush()
			return
		}

		if n == 0 {
			select {
			case <-flushTicker.C:
				bt.flush()
			default:
			}

			currentStat, err := os.Stat(bt.logPath)
			if err != nil || !os.SameFile(origStat, currentStat) {
				bt.flush()
				return
			}

			time.Sleep(1 * time.Second)
		}
	}
}

func (bt *Tracker) processLine(line []byte) {
	var entry caddyLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return
	}

	if entry.Size <= 0 {
		return
	}

	host := entry.Request.Host
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}

	domain := bt.svc.Cfg.Domain
	if host == domain || !strings.HasSuffix(host, "."+domain) {
		return
	}

	if strings.HasPrefix(entry.Request.URI, "/_data/") || entry.Request.URI == "/_data" {
		return
	}

	subdomain := strings.TrimSuffix(host, "."+domain)

	bt.mu.Lock()
	bt.accum[subdomain] += entry.Size
	bt.mu.Unlock()
}

// flush writes accumulated bytes to SQLite and enforces quotas.
func (bt *Tracker) flush() {
	bt.mu.Lock()
	if len(bt.accum) == 0 {
		bt.mu.Unlock()
		return
	}
	batch := bt.accum
	bt.accum = make(map[string]int64)
	bt.mu.Unlock()

	period := service.CurrentPeriodStart(bt.svc.QuotaResetInterval())
	needReload := false

	for subdomain, bytes := range batch {
		deploy, _ := bt.svc.Store.GetDeploymentBySubdomain(subdomain)
		if deploy == nil {
			continue
		}

		if err := bt.svc.Store.AddBandwidth(deploy.ID, period, bytes); err != nil {
			log.Printf("[bandwidth] Failed to record %d bytes for %s: %v", bytes, subdomain, err)
			continue
		}

		quota := bt.svc.ResolveNetworkQuota(deploy.NetworkQuota)
		if quota == "" {
			continue
		}
		quotaBytes, err := service.ParseSize(quota)
		if err != nil || quotaBytes <= 0 {
			continue
		}

		used, _ := bt.svc.Store.GetBandwidth(deploy.ID, period)

		bt.mu.Lock()
		alreadyBlocked := bt.blocked[subdomain]
		bt.mu.Unlock()

		if used >= quotaBytes && !alreadyBlocked {
			bt.mu.Lock()
			bt.blocked[subdomain] = true
			bt.mu.Unlock()
			bt.svc.Proxy.BlockRouteNoReload(subdomain)
			needReload = true
			log.Printf("[bandwidth] Quota exceeded for %s (used %d / limit %d), blocking", subdomain, used, quotaBytes)
		}
	}

	if needReload {
		bt.svc.Proxy.Reload()
	}
}

// UnblockAll restores normal Caddy configs for all currently blocked deployments.
// Called on periodic quota reset. Implements service.BandwidthManager.
func (bt *Tracker) UnblockAll() {
	bt.mu.Lock()
	toUnblock := make([]string, 0, len(bt.blocked))
	for sub := range bt.blocked {
		toUnblock = append(toUnblock, sub)
	}
	bt.blocked = make(map[string]bool)
	bt.mu.Unlock()

	if len(toUnblock) == 0 {
		return
	}

	for _, subdomain := range toUnblock {
		deploy, _ := bt.svc.Store.GetDeploymentBySubdomain(subdomain)
		if deploy == nil {
			continue
		}

		port := deploy.Port
		if deploy.Status != "running" {
			containerStatus := bt.svc.Container.Status(deploy.ID)
			if containerStatus == "running" {
				actualPort := bt.svc.Container.InspectPort(deploy.ID)
				if actualPort > 0 {
					port = actualPort
				}
				bt.svc.Store.UpdateDeploymentRunning(deploy.ID, deploy.ContainerID, port)
				log.Printf("[bandwidth] Fixed stale status for %s (%s -> running)", subdomain, deploy.Status)
			} else if bt.svc.Container.Restart(deploy.ID) {
				actualPort := bt.svc.Container.InspectPort(deploy.ID)
				if actualPort > 0 {
					port = actualPort
				}
				bt.svc.Store.UpdateDeploymentRunning(deploy.ID, deploy.ContainerID, port)
				log.Printf("[bandwidth] Restarted container for %s and restored status", subdomain)
			} else {
				log.Printf("[bandwidth] Cannot restore %s: container is %s", subdomain, containerStatus)
				continue
			}
		}

		ac := service.AccessControlFromDeployment(deploy)
		bt.svc.Proxy.AddRouteNoReload(subdomain, port, ac)
		log.Printf("[bandwidth] Unblocked %s after quota reset", subdomain)
	}
	bt.svc.Proxy.Reload()
}

// RecheckQuota is called when a deployment's quota is changed via UpdateMeta.
// Implements service.BandwidthManager.
func (bt *Tracker) RecheckQuota(deployID, subdomain, newQuota string) {
	if newQuota == "" {
		bt.mu.Lock()
		wasBlocked := bt.blocked[subdomain]
		delete(bt.blocked, subdomain)
		bt.mu.Unlock()

		if wasBlocked {
			bt.restoreRoute(deployID, subdomain, "quota removed")
		}
		return
	}

	quotaBytes, err := service.ParseSize(newQuota)
	if err != nil || quotaBytes <= 0 {
		return
	}

	period := service.CurrentPeriodStart(bt.svc.QuotaResetInterval())
	used, _ := bt.svc.Store.GetBandwidth(deployID, period)

	bt.mu.Lock()
	wasBlocked := bt.blocked[subdomain]
	bt.mu.Unlock()

	if used < quotaBytes && wasBlocked {
		bt.mu.Lock()
		delete(bt.blocked, subdomain)
		bt.mu.Unlock()

		bt.restoreRoute(deployID, subdomain, fmt.Sprintf("quota increased, used %d < limit %d", used, quotaBytes))
	} else if used >= quotaBytes && !wasBlocked {
		bt.mu.Lock()
		bt.blocked[subdomain] = true
		bt.mu.Unlock()

		bt.svc.Proxy.BlockRouteNoReload(subdomain)
		bt.svc.Proxy.Reload()
		log.Printf("[bandwidth] Blocked %s (quota decreased, used %d >= limit %d)", subdomain, used, quotaBytes)
	}
}

func (bt *Tracker) restoreRoute(deployID, subdomain, reason string) {
	deploy, _ := bt.svc.Store.GetDeployment(deployID)
	if deploy == nil {
		return
	}

	port := deploy.Port
	if deploy.Status != "running" {
		containerStatus := bt.svc.Container.Status(deployID)
		if containerStatus == "running" {
			actualPort := bt.svc.Container.InspectPort(deployID)
			if actualPort > 0 {
				port = actualPort
			}
			bt.svc.Store.UpdateDeploymentRunning(deployID, deploy.ContainerID, port)
			log.Printf("[bandwidth] Fixed stale status for %s (%s -> running)", subdomain, deploy.Status)
		} else if bt.svc.Container.Restart(deployID) {
			actualPort := bt.svc.Container.InspectPort(deployID)
			if actualPort > 0 {
				port = actualPort
			}
			bt.svc.Store.UpdateDeploymentRunning(deployID, deploy.ContainerID, port)
			log.Printf("[bandwidth] Restarted container for %s and restored status", subdomain)
		} else {
			log.Printf("[bandwidth] Cannot restore %s: container is %s and restart failed", subdomain, containerStatus)
			return
		}
	}

	ac := service.AccessControlFromDeployment(deploy)
	bt.svc.Proxy.AddRoute(subdomain, port, ac)
	log.Printf("[bandwidth] Unblocked %s (%s)", subdomain, reason)
}
