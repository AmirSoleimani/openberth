# Kubernetes Deployment

OpenBerth runs on Kubernetes as an alternative to the default Docker+Caddy single-server mode. Each deployment becomes a Pod with a ClusterIP Service. A Caddy sidecar handles TLS, compression, websockets, and subdomain routing — no Ingress controller required.

## Prerequisites

- A Kubernetes cluster (Docker Desktop, k3s, EKS, GKE, AKS, etc.)
- `kubectl` configured for your cluster
- `helm` v3+

## DNS Setup

Same as the single-server install — point both the root domain and a wildcard at your cluster:

```
A     deploy.example.com       → K8s LoadBalancer/Node IP
A     *.deploy.example.com     → K8s LoadBalancer/Node IP
```

For local development with Docker Desktop, no DNS is needed — `*.localhost` resolves automatically.

## Install with Helm

**1. Build the server image** (or pull from a registry):

```bash
docker build -t openberth-server:local .
```

**2. Install the chart:**

```bash
helm install openberth ./chart/openberth \
  --namespace openberth --create-namespace \
  --set image.repository=openberth-server \
  --set image.tag=local \
  --set image.pullPolicy=Never
```

**3. Complete initial setup:**

Open `http://localhost:30080/setup` in your browser, create the admin user.

**4. Configure the CLI:**

```bash
berth config set server http://localhost:30080
berth config set key sc_your_admin_key_here
```

## Verify

```bash
berth version
# Should show CLI version, server version, and domain

berth deploy ./examples/jsxapp
# Should return a live URL
```

Deployed apps are available at `http://{name}.localhost:30080`.

## Architecture

```
                        ┌──────────────────────────────────────┐
                        │            K8s Cluster                │
                        │                                       │
Browser/CLI ──► Service ──► ┌─────────────────────────────┐    │
              (port 80) │   │  OpenBerth Pod               │    │
                        │   │                              │    │
                        │   │  ┌────────┐   ┌───────────┐ │    │
                        │   │  │ Caddy  │──►│  Server   │ │    │
                        │   │  │ :80    │   │  :3456    │ │    │
                        │   │  │ :443   │   │  (API,    │ │    │
                        │   │  │ :2019  │◄──│   gallery)│ │    │
                        │   │  │(admin) │   └───────────┘ │    │
                        │   │  └───┬────┘                  │    │
                        │   └──────┼───────────────────────┘    │
                        │          │                             │
                        │          ├──► ob-{id} Pod + Svc        │
                        │          ├──► ob-{id} Pod + Svc        │
                        │          └──► ob-{id} Pod + Svc        │
                        │                                       │
                        │   ┌──────────────────┐                │
                        │   │  PVC (shared)     │                │
                        │   │  SQLite, deploys  │                │
                        │   └──────────────────┘                │
                        └──────────────────────────────────────┘
```

The Caddy sidecar runs in the same pod as the server. It receives all external traffic, routes subdomain requests to deployed app Pods, and proxies API/gallery traffic to the server. The server pushes route updates to Caddy via its admin API (`localhost:2019`).

## Helm Chart Options

### Local dev (defaults)

```bash
helm install openberth ./chart/openberth \
  --namespace openberth --create-namespace
```

Defaults: `NodePort:30080`, `insecure: true`, `domain: localhost`, `10Gi` storage.

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
| `caddy.image` | `caddy:2-alpine` | Caddy sidecar image |
| `service.type` | `NodePort` | `NodePort`, `ClusterIP`, or `LoadBalancer` |
| `service.port` | `3456` | Server API port (internal) |
| `service.nodePort` | `30080` | External port (only when type=NodePort) |
| `ingress.enabled` | `false` | Create Ingress for server + wildcard |
| `ingress.className` | `""` | Ingress class (nginx, traefik) |
| `persistence.size` | `10Gi` | PVC size for SQLite, deploys, uploads |
| `persistence.existingClaim` | `""` | Use existing PVC |
| `pdb.enabled` | `true` | Create PodDisruptionBudget |
| `pdb.maxUnavailable` | `0` | Pods allowed to be unavailable during disruptions |
| `containerDefaults.memory` | `512m` | Default memory for deployed apps |
| `containerDefaults.cpus` | `0.5` | Default CPU for deployed apps |
| `defaultTTLHours` | `72` | Default deployment TTL |
| `defaultMaxDeploys` | `10` | Max deployments per user |

## TLS

The server runs HTTP internally. TLS should be terminated upstream:

| Environment | TLS Solution |
|---|---|
| Local dev | None (`insecure: true`) |
| GCP/AWS | Cloud Application Load Balancer with managed certs |
| Bare-metal K8s | cert-manager + Ingress controller |
| Behind Cloudflare | Cloudflare edge TLS (same as Docker `--cloudflare` mode) |

## Sandbox Isolation (gVisor)

OpenBerth auto-detects gVisor at startup — if a `gvisor` or `runsc` RuntimeClass exists in the cluster, all deployed app pods use it automatically. No configuration needed.

**GKE:** Enable "Sandbox" on the node pool — gVisor is supported natively.

**Other clusters:** Install gVisor with the official DaemonSet installer:

```bash
kubectl apply -f https://raw.githubusercontent.com/google/gvisor/master/tools/installers/containerd/runsc-overlay.yaml
```

This installs `runsc` on every node and creates the RuntimeClass. OpenBerth picks it up on next restart.

**Without gVisor:** Everything works — pods run with the default runtime (runc). gVisor is recommended when running untrusted code from multiple users on shared infrastructure.

## Accessing Deployed Apps

Deployed apps are accessible at `http(s)://{name}.{domain}` — how you reach that depends on your cluster setup:

| Setup | Example URL | How traffic reaches the server |
|-------|-------------|-------------------------------|
| Local dev (NodePort) | `http://myapp.localhost:30080` | Browser → NodePort → Caddy → app pod |
| Cloud LB | `https://myapp.deploy.company.com` | Browser → LB → Caddy → app pod |
| Chart Ingress enabled | `https://myapp.deploy.company.com` | Browser → Ingress → Caddy → app pod |
| Port-forward | `http://myapp.localhost:8080` | Browser → kubectl tunnel → Caddy → app pod |

The Caddy sidecar handles subdomain routing internally. Point your DNS wildcard (`*.{domain}`) at whatever fronts the cluster.

## Upgrading

```bash
# Build new image
docker build -t openberth-server:local .

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
