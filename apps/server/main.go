package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openberth/openberth/apps/server/internal/bandwidth"
	"github.com/openberth/openberth/apps/server/internal/config"
	"github.com/openberth/openberth/apps/server/internal/container"
	"github.com/openberth/openberth/apps/server/internal/datastore"
	"github.com/openberth/openberth/apps/server/internal/httphandler"
	mcphandler "github.com/openberth/openberth/apps/server/internal/httphandler/mcp"
	"github.com/openberth/openberth/apps/server/internal/install"
	"github.com/openberth/openberth/apps/server/internal/proxy"
	"github.com/openberth/openberth/apps/server/internal/service"
	"github.com/openberth/openberth/apps/server/internal/store"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		install.Run(os.Args[2:])
		return
	}

	// Load config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize store
	dataStore, err := store.NewStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer dataStore.Close()

	// Initialize services
	cm := container.NewContainerManager(cfg)
	pm := proxy.NewProxyManager(cfg)
	ds := datastore.NewManager(cfg.PersistDir)
	defer ds.Close()

	svc := service.NewService(cfg, dataStore, cm, pm, ds)
	bt := bandwidth.NewTracker(svc, cfg.CaddyAccessLog)
	svc.SetBandwidth(bt)

	h := httphandler.NewHandlers(svc, version)
	oauth := httphandler.NewOAuthHandlers(cfg, dataStore, h.Authenticate)
	mcpH := mcphandler.NewMCPHandler(svc, h.Authenticate, version)

	// ── Routes (Go 1.22+ enhanced patterns) ────────────────────────
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /{$}", h.Index)
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /api/gallery", h.Gallery)

	// Login / Setup / Logout
	mux.HandleFunc("GET /setup", h.SetupPage)
	mux.HandleFunc("POST /setup", h.SetupSubmit)
	mux.HandleFunc("GET /login", h.LoginPage)
	mux.HandleFunc("POST /login", h.LoginSubmit)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("POST /api/login/exchange", h.LoginExchange)
	mux.HandleFunc("POST /api/me/password", h.ChangePassword)

	// OIDC/SSO
	mux.HandleFunc("GET /auth/oidc/start", h.OIDCStart)
	mux.HandleFunc("GET /auth/oidc/callback", h.OIDCCallback)

	// Internal (Caddy forward_auth uses any method)
	mux.HandleFunc("POST /internal/cleanup", h.Cleanup)
	mux.HandleFunc("/internal/auth-check", h.AuthCheck)

	// Deploy (tarball + code)
	mux.HandleFunc("POST /api/deploy", h.Deploy)
	mux.HandleFunc("POST /api/deploy/code", h.DeployCode)
	mux.HandleFunc("POST /api/deploy/{id}/update", h.Update)
	mux.HandleFunc("POST /api/deploy/{id}/update/code", h.UpdateCode)

	// Deployments
	mux.HandleFunc("GET /api/deployments", h.ListDeployments)
	mux.HandleFunc("DELETE /api/deployments", h.DestroyAllDeployments)
	mux.HandleFunc("GET /api/deployments/{id}", h.GetDeployment)
	mux.HandleFunc("PATCH /api/deployments/{id}", h.UpdateMeta)
	mux.HandleFunc("DELETE /api/deployments/{id}", h.DestroyDeployment)
	mux.HandleFunc("GET /api/deployments/{id}/logs", h.GetLogs)
	mux.HandleFunc("GET /api/deployments/{id}/source", h.GetSource)
	mux.HandleFunc("POST /api/deployments/{id}/protect", h.ProtectDeployment)
	mux.HandleFunc("POST /api/deployments/{id}/lock", h.LockDeployment)

	// Admin
	mux.HandleFunc("GET /api/admin/users", h.AdminListUsers)
	mux.HandleFunc("POST /api/admin/users", h.AdminCreateUser)
	mux.HandleFunc("DELETE /api/admin/users/{name}", h.AdminDeleteUser)
	mux.HandleFunc("PATCH /api/admin/users/{name}", h.AdminUpdateUser)
	mux.HandleFunc("GET /api/admin/backup", h.AdminBackup)
	mux.HandleFunc("POST /api/admin/restore", h.AdminRestore)
	mux.HandleFunc("GET /api/admin/settings", h.AdminGetSettings)
	mux.HandleFunc("POST /api/admin/settings", h.AdminSetSettings)

	// OAuth 2.1
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", oauth.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauth.AuthorizationServerMetadata)
	mux.HandleFunc("POST /oauth/register", oauth.Register)
	mux.HandleFunc("/oauth/authorize", oauth.Authorize) // GET+POST
	mux.HandleFunc("POST /oauth/token", oauth.Token)

	// Gallery SPA
	galleryDist, _ := fs.Sub(galleryFS, "gallery/dist")
	galleryFileServer := http.FileServer(http.FS(galleryDist))
	mux.HandleFunc("/gallery/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/gallery/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(galleryDist, path); err == nil {
			http.StripPrefix("/gallery/", galleryFileServer).ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		r.URL.Path = "/gallery/index.html"
		http.StripPrefix("/gallery/", galleryFileServer).ServeHTTP(w, r)
	})

	// Sandbox
	mux.HandleFunc("POST /api/sandbox", h.SandboxCreate)
	mux.HandleFunc("DELETE /api/sandbox/{id}", h.DestroyDeployment)
	mux.HandleFunc("POST /api/sandbox/{id}/push", h.SandboxPush)
	mux.HandleFunc("POST /api/sandbox/{id}/install", h.SandboxInstall)
	mux.HandleFunc("POST /api/sandbox/{id}/exec", h.SandboxExec)
	mux.HandleFunc("GET /api/sandbox/{id}/logs", h.SandboxLogs)
	mux.HandleFunc("POST /api/sandbox/{id}/promote", h.PromoteSandbox)

	// Data API (per-deployment document store, all methods)
	mux.HandleFunc("/_data/", h.DataHandler)
	mux.HandleFunc("/_data", h.DataHandler)

	// MCP Streamable HTTP endpoint (all methods)
	mux.Handle("/mcp", mcpH)

	// ── CORS middleware for browser-facing paths ────────────────────
	handler := corsMiddleware(mux)

	// ── Startup reconciliation ──────────────────────────────────────
	svc.ReconcileOnStartup()

	// ── Bandwidth tracker ───────────────────────────────────────────
	go bt.Run()

	// ── Cleanup scheduler ───────────────────────────────────────────
	go func() {
		svc.RunCleanup()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			svc.RunCleanup()
		}
	}()

	// ── Quota reset scheduler ───────────────────────────────────────
	go func() {
		ticker := time.NewTicker(svc.QuotaResetInterval())
		defer ticker.Stop()
		for range ticker.C {
			svc.RunQuotaReset()
		}
	}()

	// ── Start server ────────────────────────────────────────────────
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)

	log.Println("")
	log.Println("⚓ OpenBerth daemon starting")
	log.Printf("   Version: %s", version)
	log.Printf("   Domain:  %s", cfg.Domain)
	log.Printf("   Listen:  %s", addr)
	log.Printf("   Data:    %s", cfg.DataDir)
	log.Printf("   gVisor:  %v", cm.GVisorAvailable())
	log.Println("")

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Println("[shutdown] Received signal, shutting down...")
		os.Exit(0)
	}()

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // allow large uploads
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// corsMiddleware adds CORS headers to browser-facing API paths and handles
// OPTIONS preflight requests. Paths not in the list pass through unchanged.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		needsCORS := strings.HasPrefix(p, "/api/deployments/") ||
			strings.HasPrefix(p, "/api/sandbox/") ||
			p == "/api/gallery" ||
			p == "/api/login/exchange" ||
			p == "/api/me/password" ||
			strings.HasPrefix(p, "/_data") ||
			p == "/mcp" ||
			strings.HasPrefix(p, "/.well-known/") ||
			strings.HasPrefix(p, "/oauth/")

		if needsCORS {
			httphandler.SetCORSHeaders(w)
			if r.Method == http.MethodOptions {
				w.WriteHeader(204)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
