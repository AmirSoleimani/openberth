package service

import (
	"github.com/openberth/openberth/apps/server/internal/config"
	"github.com/openberth/openberth/apps/server/internal/container"
	"github.com/openberth/openberth/apps/server/internal/datastore"
	"github.com/openberth/openberth/apps/server/internal/proxy"
	"github.com/openberth/openberth/apps/server/internal/store"
)

// BandwidthManager is implemented by the bandwidth tracker in the main package.
// Separated as an interface to avoid circular dependencies.
type BandwidthManager interface {
	RecheckQuota(deployID, subdomain, newQuota string)
	UnblockAll()
}

// Service holds all dependencies needed by business logic operations.
// Fields are exported because this is an internal/ package — only code
// within this module can import it.
type Service struct {
	Cfg       *config.Config
	Store     *store.Store
	Container *container.ContainerManager
	Proxy     *proxy.ProxyManager
	DataStore *datastore.Manager
	Bandwidth BandwidthManager
}

// NewService creates a new Service. The BandwidthManager is set later
// via SetBandwidth to break the circular dependency.
func NewService(cfg *config.Config, s *store.Store, cm *container.ContainerManager, pm *proxy.ProxyManager, ds *datastore.Manager) *Service {
	return &Service{
		Cfg:       cfg,
		Store:     s,
		Container: cm,
		Proxy:     pm,
		DataStore: ds,
	}
}

// SetBandwidth sets the bandwidth manager after construction.
func (svc *Service) SetBandwidth(bm BandwidthManager) {
	svc.Bandwidth = bm
}
