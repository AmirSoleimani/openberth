package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// route holds the backend URL and access control for a subdomain.
type route struct {
	subdomain string
	backend   *url.URL // e.g. http://ob-{deployID}.openberth.svc.cluster.local
	ac        *AccessControl
	blocked   bool
}

// SubdomainResolver maps a subdomain to a deploy ID so the proxy can
// find the correct K8s service (ob-{deployID}).
type SubdomainResolver func(subdomain string) string

// K8sProxyManager implements Manager using an in-memory route table and
// Go's httputil.ReverseProxy. The OpenBerth server itself proxies subdomain
// traffic to K8s ClusterIP services — no Ingress controller needed.
type K8sProxyManager struct {
	cfg       *config.Config
	namespace string
	resolver  SubdomainResolver

	mu     sync.RWMutex
	routes map[string]*route // subdomain → route
}

// NewK8sProxyManager creates an in-memory proxy manager for K8s mode.
func NewK8sProxyManager(cfg *config.Config) (*K8sProxyManager, error) {
	ns := os.Getenv("OPENBERTH_K8S_NAMESPACE")
	if ns == "" {
		ns = "openberth"
	}

	return &K8sProxyManager{
		cfg:       cfg,
		namespace: ns,
		routes:    make(map[string]*route),
	}, nil
}

// SetResolver sets the function that maps subdomain → deploy ID.
// Must be called before routes are added (typically at startup).
func (kp *K8sProxyManager) SetResolver(fn SubdomainResolver) {
	kp.resolver = fn
}

// backendURL returns the in-cluster service URL for a subdomain.
func (kp *K8sProxyManager) backendURL(subdomain string) *url.URL {
	svcName := "ob-" + subdomain // fallback
	if kp.resolver != nil {
		if deployID := kp.resolver(subdomain); deployID != "" {
			svcName = "ob-" + deployID
		}
	}
	host := fmt.Sprintf("%s.%s.svc.cluster.local", svcName, kp.namespace)
	return &url.URL{Scheme: "http", Host: host}
}

// AddRoute registers a subdomain → K8s service mapping.
func (kp *K8sProxyManager) AddRoute(subdomain string, hostPort int, ac *AccessControl) string {
	backend := kp.backendURL(subdomain)
	kp.mu.Lock()
	kp.routes[subdomain] = &route{
		subdomain: subdomain,
		backend:   backend,
		ac:        ac,
	}
	kp.mu.Unlock()

	log.Printf("[k8s-proxy] Added route: %s → %s", subdomain, backend)

	scheme := "https"
	if kp.cfg.Insecure {
		scheme = "http"
	}
	return scheme + "://" + subdomain + "." + kp.cfg.Domain
}

// AddRouteNoReload is identical to AddRoute (no reload concept for in-memory routes).
func (kp *K8sProxyManager) AddRouteNoReload(subdomain string, hostPort int, ac *AccessControl) {
	backend := kp.backendURL(subdomain)
	kp.mu.Lock()
	kp.routes[subdomain] = &route{
		subdomain: subdomain,
		backend:   backend,
		ac:        ac,
	}
	kp.mu.Unlock()
}

// RemoveRoute removes a subdomain from the route table.
func (kp *K8sProxyManager) RemoveRoute(subdomain string) {
	kp.mu.Lock()
	delete(kp.routes, subdomain)
	kp.mu.Unlock()
	log.Printf("[k8s-proxy] Removed route: %s", subdomain)
}

// RemoveRouteNoReload is identical to RemoveRoute.
func (kp *K8sProxyManager) RemoveRouteNoReload(subdomain string) {
	kp.mu.Lock()
	delete(kp.routes, subdomain)
	kp.mu.Unlock()
}

// RemoveAllRoutes clears the entire route table.
func (kp *K8sProxyManager) RemoveAllRoutes() {
	kp.mu.Lock()
	kp.routes = make(map[string]*route)
	kp.mu.Unlock()
	log.Printf("[k8s-proxy] Removed all routes")
}

// BlockRouteNoReload marks a route as blocked (returns 503).
func (kp *K8sProxyManager) BlockRouteNoReload(subdomain string) {
	kp.mu.Lock()
	if r, ok := kp.routes[subdomain]; ok {
		r.blocked = true
	}
	kp.mu.Unlock()
}

// ListCaddyFiles returns all registered subdomain names.
// Named for interface compat with the Caddy proxy manager.
func (kp *K8sProxyManager) ListCaddyFiles() []string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	names := make([]string, 0, len(kp.routes))
	for sub := range kp.routes {
		names = append(names, sub)
	}
	return names
}

// Reload is a no-op — routes are applied immediately in memory.
func (kp *K8sProxyManager) Reload() {}

// Handler returns an http.Handler that inspects the Host header and
// reverse-proxies matching subdomain requests to K8s services.
func (kp *K8sProxyManager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subdomain := kp.extractSubdomain(r.Host)
		if subdomain == "" {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}

		kp.mu.RLock()
		rt, ok := kp.routes[subdomain]
		kp.mu.RUnlock()

		if !ok {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}

		if rt.blocked {
			http.Error(w, "Bandwidth quota exceeded", http.StatusServiceUnavailable)
			return
		}

		if rt.ac != nil && !kp.checkAccess(rt.ac, r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="OpenBerth"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = rt.backend.Scheme
				req.URL.Host = rt.backend.Host
				req.Host = r.Host
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				log.Printf("[k8s-proxy] Proxy error for %s: %v", subdomain, err)
				http.Error(w, "Service unavailable", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// extractSubdomain pulls the subdomain from a Host header like "myapp.localhost:30456".
// Returns "" if the host is the main domain (no subdomain).
func (kp *K8sProxyManager) extractSubdomain(host string) string {
	if i := strings.LastIndex(host, ":"); i != -1 {
		host = host[:i]
	}
	host = strings.ToLower(host)

	domain := strings.ToLower(kp.cfg.Domain)
	if !strings.HasSuffix(host, "."+domain) {
		return ""
	}

	sub := strings.TrimSuffix(host, "."+domain)
	if strings.Contains(sub, ".") {
		return ""
	}
	return sub
}

// checkAccess validates the request against the route's access control.
func (kp *K8sProxyManager) checkAccess(ac *AccessControl, r *http.Request) bool {
	switch ac.Mode {
	case "public", "":
		return true
	case "api_key":
		return r.Header.Get("X-Api-Key") == ac.Hash
	case "basic_auth":
		user, pass, ok := r.BasicAuth()
		if !ok {
			return false
		}
		if user != ac.Username {
			return false
		}
		return bcrypt.CompareHashAndPassword([]byte(ac.Hash), []byte(pass)) == nil
	case "user":
		// User mode requires a valid session or API key — delegate to the
		// server's auth-check endpoint via an internal HTTP call.
		checkURL := fmt.Sprintf("http://localhost:%d/internal/auth-check?subdomain=%s", kp.cfg.Port, ac.Subdomain)
		req, err := http.NewRequest("GET", checkURL, nil)
		if err != nil {
			return false
		}
		// Forward auth headers from the original request
		req.Header.Set("Cookie", r.Header.Get("Cookie"))
		req.Header.Set("Authorization", r.Header.Get("Authorization"))
		req.Header.Set("X-Api-Key", r.Header.Get("X-Api-Key"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	return true
}
