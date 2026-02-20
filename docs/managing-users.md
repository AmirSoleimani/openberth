# Managing Users

## Server-Side Admin CLI

The `berth-admin` script is installed on the server during `berth-server install`:

```bash
# Create a user
berth-admin user add --name alice --max-deployments 10 --ttl 48

# List all users
berth-admin user list

# Remove a user
berth-admin user remove alice

# Show server status (deployments, containers)
berth-admin status

# Show server configuration (domain, TLS mode, limits)
# TLS mode shows: "Direct (Let's Encrypt)", "Cloudflare Proxy (internal TLS)", or "Insecure (HTTP only)"
berth-admin config

# Force TTL-based cleanup
berth-admin cleanup

# Lock/unlock a deployment (prevent all changes)
berth-admin lock <id>
berth-admin unlock <id>
```

## Admin HTTP API

All admin endpoints require an admin API key (`Authorization: Bearer $ADMIN_KEY`).

### List users

```bash
curl https://openberth.example.com/api/admin/users \
  -H "Authorization: Bearer $ADMIN_KEY"
```

Returns:

```json
{
  "users": [
    {
      "id": "usr_abc123",
      "name": "alice",
      "api_key": "sc_...",
      "role": "user",
      "max_deployments": 10,
      "default_ttl_hours": 72,
      "created_at": "2025-01-15T10:00:00Z"
    }
  ]
}
```

### Create a user

```bash
curl -X POST https://openberth.example.com/api/admin/users \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "alice", "maxDeployments": 10}'
```

Returns:

```json
{
  "id": "usr_abc123",
  "name": "alice",
  "apiKey": "sc_...",
  "maxDeployments": 10
}
```

Optional fields: `password` (min 8 chars, for browser login), `ttlHours`.

### Update a user

```bash
curl -X PATCH https://openberth.example.com/api/admin/users/alice \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"maxDeployments": 20}'
```

Updatable fields: `password`, `displayName`, `maxDeployments`.

### Delete a user

```bash
curl -X DELETE https://openberth.example.com/api/admin/users/alice \
  -H "Authorization: Bearer $ADMIN_KEY"
```

### Settings

```bash
# Get all settings
curl https://openberth.example.com/api/admin/settings \
  -H "Authorization: Bearer $ADMIN_KEY"

# Update settings
curl -X POST https://openberth.example.com/api/admin/settings \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"session.ttl_hours": "48"}'
```

Available settings:

| Setting | Description |
|---------|-------------|
| `oidc.issuer` | OIDC provider URL (e.g. `https://accounts.google.com`) |
| `oidc.client_id` | OAuth 2.0 client ID |
| `oidc.client_secret` | OAuth 2.0 client secret |
| `oidc.mode` | Login mode: `""` (optional SSO alongside password form) or `"sso_only"` (auto-redirect to identity provider, hide password form) |
| `oidc.allowed_domains` | Comma-separated email domains allowed to log in via SSO (e.g. `company.com,subsidiary.com`). Empty allows all domains. |
| `session.ttl_hours` | Browser session lifetime in hours |
| `network.quota_enabled` | Enable network transfer quota (`true`/`false`, default: disabled) |
| `network.default_quota` | Default quota per deployment (e.g. `5g`, `500m`) |

OIDC, network quota, and user management settings are also available in the gallery admin UI under Settings.
