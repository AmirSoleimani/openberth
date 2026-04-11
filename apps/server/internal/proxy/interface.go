package proxy

import "net/http"

// Manager defines the interface for reverse proxy route management.
// Implementations exist for Caddy (default) and Kubernetes (built-in reverse proxy).
type Manager interface {
	AddRoute(subdomain string, hostPort int, ac *AccessControl) string
	AddRouteNoReload(subdomain string, hostPort int, ac *AccessControl)
	RemoveRoute(subdomain string)
	RemoveRouteNoReload(subdomain string)
	RemoveAllRoutes()
	BlockRouteNoReload(subdomain string)
	ListCaddyFiles() []string
	Reload()
	// Handler returns an http.Handler that reverse-proxies subdomain traffic.
	// Returns nil if routing is handled externally (e.g. Caddy).
	Handler() http.Handler
}
