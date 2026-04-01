# Kubernetes Deployment

OpenBerth can run on Kubernetes as an alternative to the default Docker+Caddy mode. In K8s mode, each deployment becomes a Pod with a ClusterIP Service, and the OpenBerth server itself acts as the reverse proxy — no Ingress controller required.

## Prerequisites

- A Kubernetes cluster (Docker Desktop, k3s, EKS, GKE, AKS, etc.)
- `kubectl` configured for your cluster
- `helm` v3+
- The `openberth-server` container image (build from source or pull from registry)

## Quick Start

```bash
# Build the image (from repo root)
docker build -t openberth-server:local -f deploy/k8s/Dockerfile .

# Install with Helm
helm install openberth ./chart/openberth \
  --namespace openberth --create-namespace \
  --set image.repository=openberth-server \
  --set image.tag=local \
  --set image.pullPolicy=Never

# Complete setup
open http://localhost:30456/setup
```

After setup, deploy apps via the CLI, MCP, or API. Each deploy creates a Pod in the `openberth` namespace.

## Architecture

```
                          ┌─────────────────────────────────┐
                          │         K8s Cluster              │
                          │                                  │
 Browser/CLI ──► NodePort ──► ┌──────────────────┐           │
                30456     │   │  OpenBerth Server │           │
                          │   │  (Deployment)     │           │
                          │   │                   │           │
                          │   │  API: /api/*      │           │
                          │   │  Gallery: /gallery│           │
                          │   │  Proxy: *.domain  ├──►  ob-{id} Pod + Svc
                          │   │  (built-in)       ├──►  ob-{id} Pod + Svc
                          │   │                   ├──►  ob-{id} Pod + Svc
                          │   └────────┬──────────┘           │
                          │            │                      │
                          │            ▼                      │
                          │   ┌──────────────────┐           │
                          │   │  PVC (shared)     │           │
                          │   │  SQLite, deploys  │           │
                          │   └──────────────────┘           │
                          └─────────────────────────────────┘
```

**Key difference from Docker mode:** In Docker mode, Caddy handles TLS and subdomain routing externally. In K8s mode, the server handles subdomain routing itself via `httputil.ReverseProxy` — inspecting the `Host` header and forwarding to the correct ClusterIP Service. TLS is handled upstream (cloud LB, ingress controller, or service mesh).

## Helm Chart Configuration

### Minimal (local dev)

```bash
helm install openberth ./chart/openberth \
  --namespace openberth --create-namespace
```

Defaults: `NodePort:30456`, `insecure: true`, `domain: localhost`, `10Gi` storage.

### Production

```bash
helm install openberth ./chart/openberth \
  --namespace openberth --create-namespace \
  --set domain=deploy.company.com \
  --set insecure=false \
  --set service.type=ClusterIP \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set persistence.size=50Gi \
  --set image.repository=ghcr.io/amirsoleimani/openberth \
  --set image.tag=latest
```

### All Values

| Value | Default | Description |
|-------|---------|-------------|
| `domain` | `localhost` | Base domain. Apps are at `{name}.{domain}` |
| `insecure` | `true` | HTTP-only mode. Set `false` when TLS is terminated upstream |
| `replicaCount` | `1` | Server replicas (only 1 supported — SQLite) |
| `image.repository` | `ghcr.io/amirsoleimani/openberth` | Server image |
| `image.tag` | `appVersion` | Image tag |
| `service.type` | `NodePort` | `NodePort`, `ClusterIP`, or `LoadBalancer` |
| `service.port` | `3456` | Server listen port |
| `service.nodePort` | `30456` | Fixed port (only when type=NodePort) |
| `ingress.enabled` | `false` | Create Ingress for server + wildcard |
| `ingress.className` | `""` | Ingress class (nginx, traefik) |
| `persistence.size` | `10Gi` | PVC size for SQLite, deploys, uploads |
| `persistence.existingClaim` | `""` | Use existing PVC |
| `containerDefaults.memory` | `512m` | Default memory for deployed apps |
| `containerDefaults.cpus` | `0.5` | Default CPU for deployed apps |
| `defaultTTLHours` | `72` | Default deployment TTL |
| `defaultMaxDeploys` | `10` | Max deployments per user |

## How It Works

### Deploying an app

1. User sends code via CLI/MCP/API
2. Server writes files to the shared PVC at `/var/lib/openberth/deploys/{id}/`
3. Server creates a Pod + Service in the `openberth` namespace
4. For static sites: Caddy pod serves from `/srv` (shared PVC subpath)
5. For dynamic apps: init container runs build, main container runs the app
6. Server registers a route in its in-memory proxy table
7. Requests to `{name}.{domain}` are reverse-proxied to the Pod's Service

### RBAC

The server needs permissions to manage Pods, Services, and PVCs in its namespace. The Helm chart creates a `ClusterRole` and `ClusterRoleBinding` for this. The permissions are:

- `pods`, `pods/log`, `pods/exec` — create/manage deployed apps
- `services` — expose pods via ClusterIP
- `persistentvolumeclaims` — workspace and data volumes
- `namespaces` — ensure the target namespace exists

### TLS

The server runs HTTP internally. TLS should be terminated upstream:

| Environment | TLS Solution |
|---|---|
| Local dev | None (`insecure: true`) |
| GCP/AWS | Cloud Application Load Balancer with managed certs |
| Bare-metal K8s | cert-manager + Ingress controller |
| Behind Cloudflare | Cloudflare edge TLS (same as Docker `--cloudflare` mode) |

### DNS

Same as Docker mode — point the domain and wildcard to the cluster:

```
A     deploy.company.com       → K8s LoadBalancer/Node IP
A     *.deploy.company.com     → K8s LoadBalancer/Node IP
```

## Accessing Deployed Apps

Deployed apps are accessible at `http(s)://{name}.{domain}` — how you reach that depends on your cluster setup:

| Setup | Example URL | How traffic reaches the server |
|-------|-------------|-------------------------------|
| Local dev (NodePort) | `http://myapp.localhost:30456` | Browser → NodePort → server pod |
| Cloud LB (no chart Ingress) | `https://myapp.deploy.company.com` | Browser → your LB → K8s Service → server pod |
| Chart Ingress enabled | `https://myapp.deploy.company.com` | Browser → Ingress controller → server pod |
| Port-forward | `http://myapp.localhost:3456` | Browser → kubectl tunnel → server pod |

The server handles subdomain routing internally regardless of how traffic arrives. Point your DNS wildcard (`*.{domain}`) at whatever fronts the cluster — a cloud LB, a node IP, an Ingress controller, or your own reverse proxy.

## Upgrading

```bash
# Build new image
docker build -t openberth-server:local -f deploy/k8s/Dockerfile .

# Upgrade the release
helm upgrade openberth ./chart/openberth \
  --namespace openberth \
  --set image.repository=openberth-server \
  --set image.tag=local \
  --set image.pullPolicy=Never
```

## Uninstalling

```bash
helm uninstall openberth --namespace openberth
kubectl delete namespace openberth
```

This removes the server, RBAC, and all deployed app Pods. The PVC is retained by default (Helm does not delete PVCs). Delete it manually if needed:

```bash
kubectl delete pvc -n openberth openberth-data
```
