import { useCallback, useEffect, useState } from "react";
import { UserPlus, Pencil, Trash2, ChevronDown } from "lucide-react";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "./ui/tabs";
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "./ui/table";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { CreateUserDialog } from "./dialogs/CreateUserDialog";
import { EditUserDialog, type UserInfo } from "./dialogs/EditUserDialog";
import { ApiKeyDialog } from "./dialogs/ApiKeyDialog";
import { authHeaders } from "../hooks/useAuth";
import { formatAge } from "../lib/format";

interface SettingsViewProps {
  apiKey: string;
}

export function SettingsView({ apiKey }: SettingsViewProps) {
  const [users, setUsers] = useState<UserInfo[]>([]);
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [oidcIssuer, setOidcIssuer] = useState("");
  const [oidcClientId, setOidcClientId] = useState("");
  const [oidcClientSecret, setOidcClientSecret] = useState("");
  const [oidcMode, setOidcMode] = useState("");
  const [oidcAllowedDomains, setOidcAllowedDomains] = useState("");
  const [oidcInstructionsOpen, setOidcInstructionsOpen] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [networkQuotaEnabled, setNetworkQuotaEnabled] = useState(false);
  const [networkDefaultQuota, setNetworkDefaultQuota] = useState("");
  const [networkResetInterval, setNetworkResetInterval] = useState("");
  const [containerDefaultMemory, setContainerDefaultMemory] = useState("");
  const [containerDefaultCpus, setContainerDefaultCpus] = useState("");

  const [showCreateUser, setShowCreateUser] = useState(false);
  const [editUserTarget, setEditUserTarget] = useState<UserInfo | null>(null);
  const [createdUserResult, setCreatedUserResult] = useState<{ name: string; key: string } | null>(null);
  const [rotatedKeyResult, setRotatedKeyResult] = useState<{ name: string; key: string } | null>(null);

  const fetchAdminData = useCallback(() => {
    const headers = authHeaders(apiKey);
    fetch("/api/admin/users", { headers, credentials: "same-origin" })
      .then((r) => r.json())
      .then((data) => setUsers(data.users || []))
      .catch(() => {});
    fetch("/api/admin/settings", { headers, credentials: "same-origin" })
      .then((r) => r.json())
      .then((data) => {
        setSettings(data);
        setOidcIssuer(data["oidc.issuer"] || "");
        setOidcClientId(data["oidc.client_id"] || "");
        setOidcClientSecret(data["oidc.client_secret"] === "***" ? "" : (data["oidc.client_secret"] || ""));
        setOidcMode(data["oidc.mode"] || "");
        setOidcAllowedDomains(data["oidc.allowed_domains"] || "");
        setNetworkQuotaEnabled(data["network.quota_enabled"] === "true");
        setNetworkDefaultQuota(data["network.default_quota"] || "");
        setNetworkResetInterval(data["network.quota_reset_interval"] || "");
        setContainerDefaultMemory(data["container.default_memory"] || "");
        setContainerDefaultCpus(data["container.default_cpus"] || "");
      })
      .catch(() => {});
  }, [apiKey]);

  useEffect(() => {
    fetchAdminData();
  }, [fetchAdminData]);

  const handleSaveSettings = async () => {
    setSettingsSaving(true);
    try {
      const body: Record<string, string> = {
        "oidc.issuer": oidcIssuer,
        "oidc.client_id": oidcClientId,
        "oidc.mode": oidcMode,
        "oidc.allowed_domains": oidcAllowedDomains,
      };
      if (oidcClientSecret) body["oidc.client_secret"] = oidcClientSecret;
      await fetch("/api/admin/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify(body),
      });
      fetchAdminData();
    } finally {
      setSettingsSaving(false);
    }
  };

  const handleSaveNetworkSettings = async () => {
    setSettingsSaving(true);
    try {
      await fetch("/api/admin/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify({
          "network.quota_enabled": networkQuotaEnabled ? "true" : "false",
          "network.default_quota": networkDefaultQuota,
          "network.quota_reset_interval": networkResetInterval,
        }),
      });
      fetchAdminData();
    } finally {
      setSettingsSaving(false);
    }
  };

  const handleSaveContainerSettings = async () => {
    setSettingsSaving(true);
    try {
      await fetch("/api/admin/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify({
          "container.default_memory": containerDefaultMemory,
          "container.default_cpus": containerDefaultCpus,
        }),
      });
      fetchAdminData();
    } finally {
      setSettingsSaving(false);
    }
  };

  const handleCreateUser = async (name: string, password: string, maxDeploys: string) => {
    const body: Record<string, unknown> = { name };
    if (password) body.password = password;
    if (maxDeploys) body.maxDeployments = parseInt(maxDeploys, 10);
    const res = await fetch("/api/admin/users", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
      credentials: "same-origin",
      body: JSON.stringify(body),
    });
    if (res.ok) {
      const data = await res.json();
      setCreatedUserResult({ name: data.name, key: data.apiKey });
      fetchAdminData();
    }
  };

  const handleDeleteUser = async (name: string) => {
    if (!window.confirm(`Delete user "${name}"? This cannot be undone.`)) return;
    const res = await fetch(`/api/admin/users/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: authHeaders(apiKey),
      credentials: "same-origin",
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      if (res.status === 409) {
        const parts: string[] = [];
        if (body.deployments) parts.push(`${body.deployments} deployment(s)`);
        if (body.userSecrets) parts.push(`${body.userSecrets} user secret(s)`);
        if (body.createdGlobals) parts.push(`${body.createdGlobals} global secret(s) they created`);
        window.alert(`Cannot delete "${name}": user still owns ${parts.join(", ")}. Remove them first, then try again.`);
      } else {
        window.alert(body.error || `Failed to delete user (HTTP ${res.status}).`);
      }
      return;
    }
    fetchAdminData();
  };

  const handleSaveUser = async (name: string, data: { displayName?: string; password?: string; maxDeployments?: number }) => {
    if (Object.keys(data).length > 0) {
      await fetch(`/api/admin/users/${encodeURIComponent(name)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify(data),
      });
    }
    fetchAdminData();
  };

  const handleRotateUserKey = async (name: string) => {
    const res = await fetch(`/api/admin/users/${encodeURIComponent(name)}/rotate-key`, {
      method: "POST",
      headers: authHeaders(apiKey),
      credentials: "same-origin",
    });
    if (res.ok) {
      const data = await res.json();
      setRotatedKeyResult({ name: data.name, key: data.apiKey });
    }
  };

  const selectClass = "flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm";

  return (
    <div className="space-y-8">
      <Tabs defaultValue="oidc">
        <TabsList>
          <TabsTrigger value="oidc">OIDC / SSO</TabsTrigger>
          <TabsTrigger value="users">Users</TabsTrigger>
          <TabsTrigger value="network">Network</TabsTrigger>
          <TabsTrigger value="containers">Containers</TabsTrigger>
        </TabsList>
        <TabsContent value="oidc" className="pt-4">
          <div className="grid gap-8 lg:grid-cols-[1fr,1fr]">
            <div className="space-y-6 order-2 lg:order-1">
              <div className="grid gap-2">
                <Label htmlFor="oidc-issuer">Issuer URL</Label>
                <Input id="oidc-issuer" value={oidcIssuer} onChange={(e) => setOidcIssuer(e.target.value)} placeholder="https://accounts.google.com" />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="oidc-client-id">Client ID</Label>
                <Input id="oidc-client-id" value={oidcClientId} onChange={(e) => setOidcClientId(e.target.value)} />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="oidc-secret">Client Secret</Label>
                <Input id="oidc-secret" type="password" value={oidcClientSecret} onChange={(e) => setOidcClientSecret(e.target.value)} placeholder={settings["oidc.client_secret"] === "***" ? "... (configured)" : ""} />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="oidc-mode">Login Mode</Label>
                <select id="oidc-mode" value={oidcMode} onChange={(e) => setOidcMode(e.target.value)} className={selectClass}>
                  <option value="">Optional — show SSO button alongside password form</option>
                  <option value="sso_only">SSO Only — auto-redirect to identity provider</option>
                </select>
                <p className="text-xs text-muted-foreground">SSO Only hides the password form and auto-redirects to your identity provider.</p>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="oidc-domains">Allowed Email Domains</Label>
                <Input id="oidc-domains" value={oidcAllowedDomains} onChange={(e) => setOidcAllowedDomains(e.target.value)} placeholder="company.com, subsidiary.com" />
                <p className="text-xs text-muted-foreground">Comma-separated. Leave empty to allow all domains.</p>
              </div>
              <Button onClick={handleSaveSettings} disabled={settingsSaving}>{settingsSaving ? "Saving..." : "Save OIDC Settings"}</Button>
            </div>

            <div className="order-1 lg:order-2">
              {/* Mobile: accordion */}
              <div className="lg:hidden">
                <button type="button" onClick={() => setOidcInstructionsOpen(!oidcInstructionsOpen)} className="flex w-full items-center justify-between rounded-md border bg-muted/50 px-4 py-3 text-sm font-medium transition-colors hover:bg-muted">
                  Setup Instructions
                  <ChevronDown className={`h-4 w-4 text-muted-foreground transition-transform ${oidcInstructionsOpen ? "rotate-180" : ""}`} />
                </button>
                {oidcInstructionsOpen && <OidcInstructions className="rounded-b-md border border-t-0 bg-muted/50 px-4 pb-4 pt-2" />}
              </div>
              {/* Desktop: always visible */}
              <div className="hidden lg:block rounded-md border bg-muted/50 p-4 text-sm space-y-3 sticky top-8">
                <p className="font-medium">Setup Instructions</p>
                <OidcInstructions />
              </div>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="users" className="pt-4">
          <div className="mb-4">
            <Button size="sm" onClick={() => setShowCreateUser(true)}>
              <UserPlus className="mr-1.5 h-3.5 w-3.5" />Create User
            </Button>
          </div>
          <div className="overflow-x-auto -mx-4 sm:mx-0">
            <div className="min-w-[500px] px-4 sm:px-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Role</TableHead>
                    <TableHead className="hidden sm:table-cell">Display Name</TableHead>
                    <TableHead className="hidden sm:table-cell">Max Deploys</TableHead>
                    <TableHead className="hidden md:table-cell">Created</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((u) => (
                    <TableRow key={u.name}>
                      <TableCell className="font-medium">{u.name}</TableCell>
                      <TableCell><Badge variant={u.role === "admin" ? "default" : "secondary"}>{u.role}</Badge></TableCell>
                      <TableCell className="hidden sm:table-cell">{u.displayName || "—"}</TableCell>
                      <TableCell className="hidden sm:table-cell">{u.maxDeployments}</TableCell>
                      <TableCell className="hidden md:table-cell text-muted-foreground">{u.createdAt ? formatAge(u.createdAt) : "—"}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          <Button variant="ghost" size="icon" className="h-7 w-7" title="Edit user" onClick={() => setEditUserTarget(u)}>
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" title="Delete user" onClick={() => handleDeleteUser(u.name)}>
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                  {users.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground">No users found.</TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="network" className="space-y-4 pt-4">
          <div className="max-w-lg space-y-6">
            <div className="rounded-md border bg-muted/50 p-4 text-sm space-y-2">
              <p className="font-medium">Network Quota</p>
              <p className="text-muted-foreground">
                Limit how much data each deployment can transfer over the network.
                When the quota is exhausted, the container loses external network access.
                Requires the <code className="bg-background px-1 rounded">xt_quota</code> kernel module.
              </p>
            </div>
            <div className="flex items-center gap-3">
              <button
                type="button"
                role="switch"
                aria-checked={networkQuotaEnabled}
                onClick={() => setNetworkQuotaEnabled(!networkQuotaEnabled)}
                className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${networkQuotaEnabled ? "bg-primary" : "bg-muted"}`}
              >
                <span className={`pointer-events-none block h-5 w-5 rounded-full bg-background shadow-lg ring-0 transition-transform ${networkQuotaEnabled ? "translate-x-5" : "translate-x-0"}`} />
              </button>
              <Label>Enable network quota</Label>
            </div>
            {networkQuotaEnabled && (
              <>
                <div className="grid gap-2">
                  <Label htmlFor="network-quota">Default quota per deployment</Label>
                  <Input id="network-quota" value={networkDefaultQuota} onChange={(e) => setNetworkDefaultQuota(e.target.value)} placeholder="e.g. 5g, 500m, 10g" />
                  <p className="text-xs text-muted-foreground">
                    Use <code className="bg-muted px-1 rounded">m</code> for megabytes, <code className="bg-muted px-1 rounded">g</code> for gigabytes.
                    This applies to all new deployments. Per-deployment overrides are available via the API.
                  </p>
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="network-reset-interval">Quota reset interval</Label>
                  <select id="network-reset-interval" value={networkResetInterval} onChange={(e) => setNetworkResetInterval(e.target.value)} className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
                    <option value="">Default (30 days)</option>
                    <option value="7d">7 days</option>
                    <option value="30d">30 days</option>
                    <option value="90d">90 days</option>
                  </select>
                  <p className="text-xs text-muted-foreground">How often to reset the byte counter for all deployments. After a reset, each deployment gets its full quota back.</p>
                </div>
              </>
            )}
            <Button onClick={handleSaveNetworkSettings} disabled={settingsSaving}>{settingsSaving ? "Saving..." : "Save Network Settings"}</Button>
          </div>
        </TabsContent>

        <TabsContent value="containers" className="space-y-4 pt-4">
          <div className="max-w-lg space-y-6">
            <div className="rounded-md border bg-muted/50 p-4 text-sm space-y-2">
              <p className="font-medium">Default Container Resources</p>
              <p className="text-muted-foreground">
                Applied to every new deployment that doesn't specify its own memory or CPU limits.
                Per-deployment values (passed at deploy time) always win. Leaving a field empty
                falls back to the server's compiled-in default.
              </p>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="container-memory">Default memory</Label>
              <Input
                id="container-memory"
                value={containerDefaultMemory}
                onChange={(e) => setContainerDefaultMemory(e.target.value)}
                placeholder="e.g. 512m, 1g, 2g"
              />
              <p className="text-xs text-muted-foreground">
                Docker memory format. Use <code className="bg-muted px-1 rounded">m</code> for megabytes, <code className="bg-muted px-1 rounded">g</code> for gigabytes.
              </p>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="container-cpus">Default CPUs</Label>
              <Input
                id="container-cpus"
                value={containerDefaultCpus}
                onChange={(e) => setContainerDefaultCpus(e.target.value)}
                placeholder="e.g. 0.5, 1.0, 2.0"
              />
              <p className="text-xs text-muted-foreground">
                Fractional CPU allocation per container. <code className="bg-muted px-1 rounded">1.0</code> = one full core.
              </p>
            </div>
            <Button onClick={handleSaveContainerSettings} disabled={settingsSaving}>{settingsSaving ? "Saving..." : "Save Container Defaults"}</Button>
          </div>
        </TabsContent>
      </Tabs>

      <CreateUserDialog open={showCreateUser} onClose={() => setShowCreateUser(false)} onCreate={handleCreateUser} />
      <EditUserDialog target={editUserTarget} onClose={() => setEditUserTarget(null)} onSave={handleSaveUser} onRotateKey={handleRotateUserKey} />
      <ApiKeyDialog data={createdUserResult} variant="user" onClose={() => setCreatedUserResult(null)} />
      <ApiKeyDialog data={rotatedKeyResult} variant="user-rotated" onClose={() => setRotatedKeyResult(null)} />
    </div>
  );
}

function OidcInstructions({ className }: { className?: string }) {
  return (
    <div className={`text-sm space-y-3 ${className || ""}`}>
      <ol className="list-decimal list-inside space-y-1.5 text-muted-foreground">
        <li>Create an OAuth/OIDC application in your identity provider</li>
        <li>Set the <strong>Authorized redirect URI</strong> to:</li>
      </ol>
      <code className="block rounded bg-background px-3 py-2 text-xs font-mono break-all border">
        {window.location.origin}/auth/oidc/callback
      </code>
      <ol className="list-decimal list-inside space-y-1.5 text-muted-foreground" start={3}>
        <li>Copy the Client ID and Client Secret below</li>
      </ol>
      <div className="pt-1">
        <p className="text-xs text-muted-foreground"><strong>Common issuer URLs:</strong></p>
        <ul className="text-xs text-muted-foreground mt-1 space-y-0.5">
          <li>Google: <code className="bg-background px-1 rounded">https://accounts.google.com</code></li>
          <li>Microsoft: <code className="bg-background px-1 rounded">https://login.microsoftonline.com/&#123;tenant&#125;/v2.0</code></li>
          <li>Okta: <code className="bg-background px-1 rounded">https://&#123;domain&#125;.okta.com</code></li>
          <li>Auth0: <code className="bg-background px-1 rounded">https://&#123;domain&#125;.auth0.com</code></li>
          <li>Keycloak: <code className="bg-background px-1 rounded">https://&#123;host&#125;/realms/&#123;realm&#125;</code></li>
        </ul>
      </div>
    </div>
  );
}
