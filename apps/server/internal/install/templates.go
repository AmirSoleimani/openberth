package install

// Config templates embedded as Go string constants.
// Each uses fmt.Sprintf for variable substitution.

const configJSONTemplate = `{
    "domain": "%s",
    "port": 3456,
    "dataDir": "/var/lib/openberth",
    "defaultTTLHours": %d,
    "defaultMaxDeploys": %d,
    "containerDefaults": {
        "memory": "512m",
        "cpus": "0.5",
        "pidsLimit": 256
    }
}`

const caddyfileTemplate = `{
    admin localhost:2019
    acme_ca https://acme-v02.api.letsencrypt.org/directory
    email admin@%s
}

%s {
    reverse_proxy localhost:3456
}

import /etc/caddy/sites/*.caddy`

const configJSONCloudflareTemplate = `{
    "domain": "%s",
    "port": 3456,
    "dataDir": "/var/lib/openberth",
    "cloudflareProxy": true,
    "defaultTTLHours": %d,
    "defaultMaxDeploys": %d,
    "containerDefaults": {
        "memory": "512m",
        "cpus": "0.5",
        "pidsLimit": 256
    }
}`

const caddyfileCloudflareTemplate = `{
    admin localhost:2019
}

%s {
    tls internal
    reverse_proxy localhost:3456
}

import /etc/caddy/sites/*.caddy`

const configJSONInsecureTemplate = `{
    "domain": "%s",
    "port": 3456,
    "dataDir": "/var/lib/openberth",
    "insecure": true,
    "defaultTTLHours": %d,
    "defaultMaxDeploys": %d,
    "containerDefaults": {
        "memory": "512m",
        "cpus": "0.5",
        "pidsLimit": 256
    }
}`

const caddyfileInsecureTemplate = `{
    admin localhost:2019
    auto_https off
}

http://%s {
    reverse_proxy localhost:3456
}

import /etc/caddy/sites/*.caddy`

const systemdServiceTemplate = `[Unit]
Description=OpenBerth Deployment Daemon
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/berth-server
Restart=always
RestartSec=5
Environment=DATA_DIR=%s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target`

const daemonJSONTemplate = `{
    "runtimes": {
        "runsc": {
            "path": "/usr/local/bin/runsc",
            "runtimeArgs": ["--network=sandbox", "--platform=systrap"]
        }
    }
}`

const dbInitSQLTemplate = `CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    api_key TEXT UNIQUE NOT NULL,
    password_hash TEXT DEFAULT '',
    role TEXT DEFAULT 'user',
    max_deployments INTEGER DEFAULT %d,
    default_ttl_hours INTEGER DEFAULT %d,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS deployments (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    subdomain TEXT UNIQUE NOT NULL,
    framework TEXT,
    container_id TEXT,
    port INTEGER,
    status TEXT DEFAULT 'building',
    ttl_hours INTEGER,
    env_json TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id)
);
INSERT INTO users (id, name, api_key, password_hash, role, max_deployments, default_ttl_hours)
VALUES ('%s', 'admin', '%s', '%s', 'admin', 100, %d)
ON CONFLICT(name) DO UPDATE SET api_key=excluded.api_key, password_hash=excluded.password_hash;`

const adminScriptTemplate = `#!/usr/bin/env bash
set -euo pipefail
DB="/var/lib/openberth/openberth.db"

case "${1:-help}" in
    user)
        case "${2:-}" in
            add)
                shift 2
                NAME=""; MAX=10; TTL=72
                while [[ $# -gt 0 ]]; do
                    case "$1" in
                        --name) NAME="$2"; shift 2;;
                        --max-deployments) MAX="$2"; shift 2;;
                        --ttl) TTL="$2"; shift 2;;
                        *) shift;;
                    esac
                done
                [[ -z "$NAME" ]] && { echo "Usage: berth-admin user add --name NAME [--max-deployments N] [--ttl HOURS]"; exit 1; }
                ID="usr_$(openssl rand -hex 8)"
                KEY="sc_$(openssl rand -hex 24)"
                sqlite3 "$DB" "INSERT INTO users (id, name, api_key, role, max_deployments, default_ttl_hours) VALUES ('$ID', '$NAME', '$KEY', 'user', $MAX, $TTL);"
                echo -e "Created user '\033[1m$NAME\033[0m'"
                echo -e "API Key: \033[1;33m$KEY\033[0m"
                ;;
            list)
                sqlite3 -header -column "$DB" "SELECT name, role, max_deployments, default_ttl_hours, created_at FROM users ORDER BY created_at;"
                ;;
            remove)
                NAME="${3:-}"; [[ -z "$NAME" ]] && { echo "Usage: berth-admin user remove NAME"; exit 1; }
                sqlite3 "$DB" "DELETE FROM users WHERE name='$NAME';"
                echo "Removed user '$NAME'"
                ;;
            *) echo "Usage: berth-admin user {add|list|remove}";;
        esac
        ;;
    status)
        echo "=== Deployments ==="
        sqlite3 -header -column "$DB" "SELECT d.subdomain, d.status, d.framework, u.name as owner, d.expires_at FROM deployments d JOIN users u ON d.user_id = u.id WHERE d.status != 'destroyed' ORDER BY d.created_at DESC LIMIT 20;"
        echo ""
        echo "=== Containers ==="
        docker ps --filter "label=openberth" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || true
        ;;
    cleanup)
        curl -s -X POST http://localhost:3456/internal/cleanup | jq .
        ;;
    config)
        CONFIG="/var/lib/openberth/config.json"
        if [[ ! -f "$CONFIG" ]]; then
            echo "Config not found: $CONFIG"
            exit 1
        fi
        DOMAIN=$(jq -r '.domain // "localhost"' "$CONFIG")
        CF=$(jq -r '.cloudflareProxy // false' "$CONFIG")
        INSECURE=$(jq -r '.insecure // false' "$CONFIG")
        TTL=$(jq -r '.defaultTTLHours // 72' "$CONFIG")
        MAX=$(jq -r '.defaultMaxDeploys // 10' "$CONFIG")
        MEM=$(jq -r '.containerDefaults.memory // "512m"' "$CONFIG")
        CPUS=$(jq -r '.containerDefaults.cpus // "0.5"' "$CONFIG")
        if [[ "$INSECURE" == "true" ]]; then
            TLS_MODE="Insecure (HTTP only)"
        elif [[ "$CF" == "true" ]]; then
            TLS_MODE="Cloudflare Proxy (internal TLS)"
        else
            TLS_MODE="Direct (Let's Encrypt)"
        fi
        echo "=== OpenBerth Config ==="
        echo "  Domain:          $DOMAIN"
        echo "  TLS mode:        $TLS_MODE"
        echo "  Default TTL:     ${TTL}h"
        echo "  Max deploys:     $MAX"
        echo "  Container mem:   $MEM"
        echo "  Container CPUs:  $CPUS"
        ;;
    lock)
        ID="${2:-}"; [[ -z "$ID" ]] && { echo "Usage: berth-admin lock ID"; exit 1; }
        sqlite3 "$DB" "UPDATE deployments SET locked = 1 WHERE id='$ID';"
        echo "Deployment '$ID' locked"
        ;;
    unlock)
        ID="${2:-}"; [[ -z "$ID" ]] && { echo "Usage: berth-admin unlock ID"; exit 1; }
        sqlite3 "$DB" "UPDATE deployments SET locked = 0 WHERE id='$ID';"
        echo "Deployment '$ID' unlocked"
        ;;
    *)
        echo "OpenBerth Admin"
        echo ""
        echo "  berth-admin user add --name NAME [--max-deployments N] [--ttl HOURS]"
        echo "  berth-admin user list"
        echo "  berth-admin user remove NAME"
        echo "  berth-admin status"
        echo "  berth-admin config"
        echo "  berth-admin cleanup"
        echo "  berth-admin lock ID"
        echo "  berth-admin unlock ID"
        ;;
esac`
