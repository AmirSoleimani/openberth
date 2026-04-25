package service

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/runtime"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// ── Result types ────────────────────────────────────────────────────

// DeployStats is the snapshot returned by /api/deployments/{id}/stats.
type DeployStats struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Live    LiveStatsView   `json:"live"`
	Storage StorageBreakdown `json:"storage"`
	Network NetworkUsage    `json:"network"`
}

// LiveStatsView is the JSON-friendly projection of runtime.LiveStats.
type LiveStatsView struct {
	CPUPercent       float64 `json:"cpuPercent"`
	CPULimitCores    float64 `json:"cpuLimitCores"` // 0 when no limit; e.g. 0.5 = half a core
	MemoryBytes      int64   `json:"memoryBytes"`
	MemoryLimitBytes int64   `json:"memoryLimitBytes"`
	PIDs             int     `json:"pids"`
	BuildVolumeBytes int64   `json:"buildVolumeBytes"`
}

// StorageBreakdown sums the three on-disk consumers for a deployment.
type StorageBreakdown struct {
	SourceBytes      int64 `json:"sourceBytes"`      // DeploysDir/<id>
	PersistBytes     int64 `json:"persistBytes"`     // PersistDir/<id> (includes /data + /_data store.db)
	BuildVolumeBytes int64 `json:"buildVolumeBytes"` // sc-ws-<id> volume mounted at /app
	TotalBytes       int64 `json:"totalBytes"`
}

// NetworkUsage describes egress bytes vs the resolved quota plus a
// short period history. Free history because bandwidth_usage already
// stores per-period totals; everything else stays snapshot-only.
type NetworkUsage struct {
	UsedBytes            int64                   `json:"usedBytes"`
	QuotaBytes           int64                   `json:"quotaBytes"`           // 0 when no quota is configured
	RemainingBytes       int64                   `json:"remainingBytes"`       // 0 when no quota
	PeriodStart          string                  `json:"periodStart"`
	PeriodResetIntervalH int                     `json:"periodResetIntervalH"` // hours between resets
	RecentPeriods        []store.BandwidthPeriod `json:"recentPeriods,omitempty"`
}

// ── Storage cache ───────────────────────────────────────────────────

// storageCache memoises the slow part of DeploymentStats — walking the
// source and persist dirs — so a 5s gallery poll cadence doesn't pin
// the I/O subsystem on a deployment with 10k files.
type storageCache struct {
	mu      sync.Mutex
	entries map[string]storageSnapshot
	ttl     time.Duration
}

type storageSnapshot struct {
	Source, Persist int64
	At              time.Time
}

func newStorageCache(ttl time.Duration) *storageCache {
	return &storageCache{entries: make(map[string]storageSnapshot), ttl: ttl}
}

// get returns (snapshot, true) when a fresh entry exists.
func (c *storageCache) get(deployID string) (storageSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[deployID]
	if !ok || time.Since(s.At) > c.ttl {
		return storageSnapshot{}, false
	}
	return s, true
}

func (c *storageCache) put(deployID string, source, persist int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[deployID] = storageSnapshot{Source: source, Persist: persist, At: time.Now()}
}

// dirSize sums every regular file under root via filepath.Walk. Returns
// 0 if the directory doesn't exist (e.g. deployment record exists but
// the source tree was cleaned up).
func dirSize(root string) int64 {
	var total int64
	filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// ── Service entry point ─────────────────────────────────────────────

// DeploymentStats produces a full resource snapshot for one deployment.
// The owner gate matches GetSource: owner, sharer (access_users), or
// admin can read.
func (svc *Service) DeploymentStats(user *store.User, id string) (*DeployStats, error) {
	deploy, _ := svc.Store.GetDeployment(id)
	if deploy == nil {
		return nil, ErrNotFound("Not found.")
	}
	if !CanMutateDeploy(deploy, user) {
		return nil, ErrForbidden("Not your deployment.")
	}

	// Live container metrics — cheap, never cached. Driver returns zeros
	// when the container isn't running rather than erroring; the UI can
	// render that as "0% / 0 B" without special-casing build/stopped.
	var live runtime.LiveStats
	if v, err := svc.Runtime.Stats(deploy.ID); err == nil {
		live = v
	}

	// Storage: cache the slow part (filesystem walks), reuse the live
	// build-volume figure for the third pillar.
	source, persist := svc.resolveStorage(deploy.ID)
	storage := StorageBreakdown{
		SourceBytes:      source,
		PersistBytes:     persist,
		BuildVolumeBytes: live.BuildVolumeBytes,
	}
	storage.TotalBytes = storage.SourceBytes + storage.PersistBytes + storage.BuildVolumeBytes

	// Network: reuse the existing quota math, plus a bonus history slice.
	period := CurrentPeriodStart(svc.QuotaResetInterval())
	used, _ := svc.Store.GetBandwidth(deploy.ID, period)
	network := NetworkUsage{
		UsedBytes:            used,
		PeriodStart:          period,
		PeriodResetIntervalH: int(svc.QuotaResetInterval().Hours()),
	}
	if deploy.NetworkQuota != "" {
		if quotaBytes, err := ParseSize(deploy.NetworkQuota); err == nil {
			network.QuotaBytes = quotaBytes
			remaining := quotaBytes - used
			if remaining < 0 {
				remaining = 0
			}
			network.RemainingBytes = remaining
		}
	}
	if hist, err := svc.Store.GetBandwidthHistory(deploy.ID, 6); err == nil {
		network.RecentPeriods = hist
	}

	// CPU limit: resolve through the same chain as deploy time
	// (per-deploy → admin default → compiled default), then parse to cores.
	cpuLimitCores := 0.0
	if v, err := strconv.ParseFloat(svc.ResolveCPUs(deploy.CPUs), 64); err == nil && v > 0 {
		cpuLimitCores = v
	}

	return &DeployStats{
		ID:     deploy.ID,
		Status: deploy.Status,
		Live: LiveStatsView{
			CPUPercent:       live.CPUPercent,
			CPULimitCores:    cpuLimitCores,
			MemoryBytes:      live.MemoryBytes,
			MemoryLimitBytes: live.MemoryLimitBytes,
			PIDs:             live.PIDs,
			BuildVolumeBytes: live.BuildVolumeBytes,
		},
		Storage: storage,
		Network: network,
	}, nil
}

// resolveStorage returns (source, persist) bytes, hitting the cache
// first. The cache is lazy-initialised on first read so callers don't
// need to wire it through Service construction.
func (svc *Service) resolveStorage(deployID string) (int64, int64) {
	cache := svc.storageCacheRef()
	if snap, ok := cache.get(deployID); ok {
		return snap.Source, snap.Persist
	}
	source := dirSize(filepath.Join(svc.Cfg.DeploysDir, deployID))
	persist := dirSize(filepath.Join(svc.Cfg.PersistDir, deployID))
	cache.put(deployID, source, persist)
	return source, persist
}

// storageCacheRef returns the package-singleton cache, initialising it
// on first access. Service is never reconstructed at runtime, so a
// lazy singleton is fine.
var (
	storageCacheOnce sync.Once
	storageCacheVar  *storageCache
)

func (svc *Service) storageCacheRef() *storageCache {
	storageCacheOnce.Do(func() {
		storageCacheVar = newStorageCache(30 * time.Second)
	})
	return storageCacheVar
}
