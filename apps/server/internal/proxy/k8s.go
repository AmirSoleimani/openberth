package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
)

// SubdomainResolver maps a subdomain to a deploy ID so the proxy can
// find the correct K8s service (ob-{deployID}).
type SubdomainResolver func(subdomain string) string

// K8sProxyManager implements Manager using a Caddy sidecar container.
// Routes are pushed to Caddy's admin API (localhost:2019) as JSON config.
// Caddy handles TLS, compression, websockets, and reverse proxying to
// K8s ClusterIP services.
type K8sProxyManager struct {
	cfg       *config.Config
	namespace string
	resolver  SubdomainResolver
	adminURL  string // Caddy admin API, default http://localhost:2019

	mu     sync.RWMutex
	routes map[string]*k8sRoute // subdomain → route
}

type k8sRoute struct {
	subdomain string
	backend   string // e.g. ob-{deployID}.openberth.svc.cluster.local
	ac        *AccessControl
	blocked   bool
}

// NewK8sProxyManager creates a proxy manager that pushes config to a Caddy sidecar.
func NewK8sProxyManager(cfg *config.Config) (*K8sProxyManager, error) {
	ns := os.Getenv("OPENBERTH_K8S_NAMESPACE")
	if ns == "" {
		ns = "openberth"
	}

	adminURL := os.Getenv("CADDY_ADMIN_URL")
	if adminURL == "" {
		adminURL = "http://localhost:2019"
	}

	return &K8sProxyManager{
		cfg:       cfg,
		namespace: ns,
		adminURL:  adminURL,
		routes:    make(map[string]*k8sRoute),
	}, nil
}

// SetResolver sets the function that maps subdomain → deploy ID.
func (kp *K8sProxyManager) SetResolver(fn SubdomainResolver) {
	kp.resolver = fn
}

// backendHost returns the in-cluster service hostname for a subdomain.
func (kp *K8sProxyManager) backendHost(subdomain string) string {
	svcName := "ob-" + subdomain
	if kp.resolver != nil {
		if deployID := kp.resolver(subdomain); deployID != "" {
			svcName = "ob-" + deployID
		}
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", svcName, kp.namespace)
}

// AddRoute registers a subdomain and pushes the updated config to Caddy.
func (kp *K8sProxyManager) AddRoute(subdomain string, hostPort int, ac *AccessControl) string {
	backend := kp.backendHost(subdomain)
	kp.mu.Lock()
	kp.routes[subdomain] = &k8sRoute{
		subdomain: subdomain,
		backend:   backend,
		ac:        ac,
	}
	kp.mu.Unlock()

	kp.pushConfig()
	log.Printf("[k8s-caddy] Added route: %s → %s", subdomain, backend)

	scheme := "https"
	if kp.cfg.Insecure {
		scheme = "http"
	}
	return scheme + "://" + subdomain + "." + kp.cfg.Domain
}

// AddRouteNoReload registers a route without pushing to Caddy (batch mode).
func (kp *K8sProxyManager) AddRouteNoReload(subdomain string, hostPort int, ac *AccessControl) {
	backend := kp.backendHost(subdomain)
	kp.mu.Lock()
	kp.routes[subdomain] = &k8sRoute{
		subdomain: subdomain,
		backend:   backend,
		ac:        ac,
	}
	kp.mu.Unlock()
}

// RemoveRoute removes a subdomain and pushes the updated config to Caddy.
func (kp *K8sProxyManager) RemoveRoute(subdomain string) {
	kp.mu.Lock()
	delete(kp.routes, subdomain)
	kp.mu.Unlock()
	kp.pushConfig()
	log.Printf("[k8s-caddy] Removed route: %s", subdomain)
}

// RemoveRouteNoReload removes a route without pushing to Caddy.
func (kp *K8sProxyManager) RemoveRouteNoReload(subdomain string) {
	kp.mu.Lock()
	delete(kp.routes, subdomain)
	kp.mu.Unlock()
}

// RemoveAllRoutes clears all routes and pushes empty config to Caddy.
func (kp *K8sProxyManager) RemoveAllRoutes() {
	kp.mu.Lock()
	kp.routes = make(map[string]*k8sRoute)
	kp.mu.Unlock()
	kp.pushConfig()
	log.Printf("[k8s-caddy] Removed all routes")
}

// BlockRouteNoReload marks a route as blocked (Caddy will serve 503).
func (kp *K8sProxyManager) BlockRouteNoReload(subdomain string) {
	kp.mu.Lock()
	if r, ok := kp.routes[subdomain]; ok {
		r.blocked = true
	}
	kp.mu.Unlock()
}

// ListCaddyFiles returns all registered subdomain names.
func (kp *K8sProxyManager) ListCaddyFiles() []string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	names := make([]string, 0, len(kp.routes))
	for sub := range kp.routes {
		names = append(names, sub)
	}
	return names
}

// Reload pushes the current route table to Caddy.
func (kp *K8sProxyManager) Reload() {
	kp.pushConfig()
}

// Handler returns nil — Caddy sidecar handles all routing externally.
func (kp *K8sProxyManager) Handler() http.Handler {
	return nil
}

// pushConfig builds a full Caddy JSON config from the route table and
// POSTs it to the Caddy admin API.
func (kp *K8sProxyManager) pushConfig() {
	cfg := kp.buildCaddyConfig()
	body, err := json.Marshal(cfg)
	if err != nil {
		log.Printf("[k8s-caddy] Failed to marshal config: %v", err)
		return
	}

	resp, err := http.Post(kp.adminURL+"/load", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[k8s-caddy] Failed to push config: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		log.Printf("[k8s-caddy] Caddy rejected config (status %d): %s", resp.StatusCode, buf.String())
	}
}

// buildCaddyConfig generates the full Caddy JSON config with:
// - A route for the main domain → upstream server (API, gallery)
// - A route per deployed app → K8s service
func (kp *K8sProxyManager) buildCaddyConfig() map[string]interface{} {
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	serverAddr := fmt.Sprintf("localhost:%d", kp.cfg.Port)

	// Build routes for deployed apps
	var srvRoutes []map[string]interface{}

	for _, rt := range kp.routes {
		fqdn := rt.subdomain + "." + kp.cfg.Domain

		if rt.blocked {
			// Blocked route: respond with 503
			srvRoutes = append(srvRoutes, map[string]interface{}{
				"match": []map[string]interface{}{
					{"host": []string{fqdn}},
				},
				"handle": []map[string]interface{}{
					{
						"handler":     "static_response",
						"status_code": "503",
						"body":        "Bandwidth quota exceeded",
					},
				},
			})
			continue
		}

		handlers := []map[string]interface{}{}

		// Access control
		if rt.ac != nil {
			switch rt.ac.Mode {
			case "basic_auth":
				handlers = append(handlers, map[string]interface{}{
					"handler": "authentication",
					"providers": map[string]interface{}{
						"http_basic": map[string]interface{}{
							"accounts": []map[string]interface{}{
								{
									"username": rt.ac.Username,
									"password": rt.ac.Hash,
								},
							},
						},
					},
				})
			case "api_key":
				if rt.ac.Hash != "" {
					handlers = append(handlers, map[string]interface{}{
						"handler": "reverse_proxy",
						"upstreams": []map[string]interface{}{
							{"dial": serverAddr},
						},
						"rewrite": map[string]interface{}{
							"uri": fmt.Sprintf("/internal/auth-check?subdomain=%s", rt.subdomain),
						},
					})
				}
			case "user":
				handlers = append(handlers, map[string]interface{}{
					"handler": "reverse_proxy",
					"upstreams": []map[string]interface{}{
						{"dial": serverAddr},
					},
					"rewrite": map[string]interface{}{
						"uri": fmt.Sprintf("/internal/auth-check?subdomain=%s", rt.ac.Subdomain),
					},
				})
			}
		}

		// Reverse proxy to the K8s service
		handlers = append(handlers, map[string]interface{}{
			"handler": "reverse_proxy",
			"upstreams": []map[string]interface{}{
				{"dial": rt.backend + ":80"},
			},
		})

		// Encode gzip
		handlers = append(handlers, map[string]interface{}{
			"handler": "encode",
			"encodings": map[string]interface{}{
				"gzip": map[string]interface{}{},
			},
		})

		srvRoutes = append(srvRoutes, map[string]interface{}{
			"match": []map[string]interface{}{
				{"host": []string{fqdn}},
			},
			"handle": handlers,
		})
	}

	// Default route: proxy everything else to the OpenBerth server
	srvRoutes = append(srvRoutes, map[string]interface{}{
		"handle": []map[string]interface{}{
			{
				"handler": "reverse_proxy",
				"upstreams": []map[string]interface{}{
					{"dial": serverAddr},
				},
			},
		},
	})

	// Listen addresses
	listen := []string{":80"}
	if !kp.cfg.Insecure {
		listen = append(listen, ":443")
	}

	// Build server config
	srv := map[string]interface{}{
		"listen": listen,
		"routes": srvRoutes,
	}

	// TLS: auto_https off if insecure
	apps := map[string]interface{}{
		"http": map[string]interface{}{
			"servers": map[string]interface{}{
				"srv0": srv,
			},
		},
	}

	if kp.cfg.Insecure {
		apps["http"].(map[string]interface{})["servers"].(map[string]interface{})["srv0"].(map[string]interface{})["automatic_https"] = map[string]interface{}{
			"disable": true,
		}
	}

	// Build the wildcard domain list for on-demand TLS
	if !kp.cfg.Insecure {
		domains := []string{kp.cfg.Domain}
		for sub := range kp.routes {
			domains = append(domains, sub+"."+kp.cfg.Domain)
		}
		apps["tls"] = map[string]interface{}{
			"automation": map[string]interface{}{
				"policies": []map[string]interface{}{
					{
						"subjects": domains,
					},
				},
			},
		}
	}

	return map[string]interface{}{
		"admin": map[string]interface{}{
			"listen": strings.TrimPrefix(kp.adminURL, "http://"),
		},
		"apps": apps,
	}
}
