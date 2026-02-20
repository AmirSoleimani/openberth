import { useCallback, useEffect, useState } from "react";
import { Search, ExternalLink, Clock, Trash2, Pencil, Lock, LockOpen, KeyRound, LogIn, LogOut, Settings, UserPlus, ArrowLeft, ChevronDown, ShieldCheck } from "lucide-react";
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from "./components/ui/card";
import { Badge } from "./components/ui/badge";
import { Input } from "./components/ui/input";
import { Button } from "./components/ui/button";
import { Label } from "./components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "./components/ui/tabs";
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "./components/ui/table";
import type { GalleryItem, GalleryResponse } from "./types";

const API_KEY_STORAGE = "openberth_api_key";

function formatAge(createdAt: string): string {
  if (!createdAt) return "?";
  let t: Date;
  if (createdAt.includes("T")) {
    t = new Date(createdAt);
  } else {
    t = new Date(createdAt.replace(" ", "T") + "Z");
  }
  if (isNaN(t.getTime())) return "?";
  const seconds = Math.floor((Date.now() - t.getTime()) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function formatExpiry(expiresAt: string): string {
  if (!expiresAt) return "";
  let t: Date;
  if (expiresAt.includes("T")) {
    t = new Date(expiresAt);
  } else {
    t = new Date(expiresAt.replace(" ", "T") + "Z");
  }
  if (isNaN(t.getTime())) return "";
  const diff = t.getTime() - Date.now();
  if (diff <= 0) return "expired";
  const hours = Math.floor(diff / 3600000);
  if (hours < 1) return `in ${Math.floor(diff / 60000)}m`;
  if (hours < 24) return `in ${hours}h`;
  return `in ${Math.floor(hours / 24)}d`;
}

type Route =
  | { view: "gallery" }
  | { view: "user"; userId: string }
  | { view: "settings" };

function parseRoute(pathname: string): Route {
  const m = pathname.match(/^\/gallery\/user\/([^/]+)/);
  if (m) return { view: "user", userId: m[1] };
  if (pathname.startsWith("/gallery/settings")) return { view: "settings" };
  return { view: "gallery" };
}

function authHeaders(apiKey: string): Record<string, string> {
  if (!apiKey) return {};
  return { "X-API-Key": apiKey };
}

function statusBadge(status: string) {
  switch (status) {
    case "running":
      return <Badge variant="outline" className="text-[10px] border-green-500 text-green-600">Running</Badge>;
    case "building":
      return <Badge variant="outline" className="text-[10px] border-blue-500 text-blue-600 animate-pulse">Building</Badge>;
    case "updating":
      return <Badge variant="outline" className="text-[10px] border-blue-500 text-blue-600 animate-pulse">Updating</Badge>;
    case "failed":
      return <Badge variant="outline" className="text-[10px] border-red-500 text-red-600">Failed</Badge>;
    case "stopped":
      return <Badge variant="outline" className="text-[10px] border-gray-500 text-gray-500">Stopped</Badge>;
    default:
      return <Badge variant="outline" className="text-[10px]">{status}</Badge>;
  }
}

function isNotRunning(item: GalleryItem): boolean {
  return item.status !== "running";
}

interface UserInfo {
  name: string;
  displayName: string;
  role: string;
  maxDeployments: number;
  createdAt: string;
}

export default function App() {
  const [items, setItems] = useState<GalleryItem[]>([]);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [apiKey] = useState(() => localStorage.getItem(API_KEY_STORAGE) || "");
  const [currentUserId, setCurrentUserId] = useState<string | null>(null);
  const [userRole, setUserRole] = useState<string | null>(null);
  const [userName, setUserName] = useState<string | null>(null);

  // Password dialog
  const [showPasswordDialog, setShowPasswordDialog] = useState(false);
  const [hasPassword, setHasPassword] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [savingPassword, setSavingPassword] = useState(false);
  const [passwordError, setPasswordError] = useState("");

  // Destroy dialog
  const [destroyTarget, setDestroyTarget] = useState<GalleryItem | null>(null);
  const [destroying, setDestroying] = useState(false);

  // Edit dialog
  const [editTarget, setEditTarget] = useState<GalleryItem | null>(null);
  const [editTitle, setEditTitle] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editTTL, setEditTTL] = useState("0");
  const [editAccessMode, setEditAccessMode] = useState("public");
  const [editUsername, setEditUsername] = useState("");
  const [editPassword, setEditPassword] = useState("");
  const [editApiKey, setEditApiKey] = useState("");
  const [editNetworkQuota, setEditNetworkQuota] = useState("");
  const [editAccessUsers, setEditAccessUsers] = useState("");
  const [saving, setSaving] = useState(false);
  const [generatedApiKey, setGeneratedApiKey] = useState<{ name: string; key: string } | null>(null);

  // Routing
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname));

  const navigate = useCallback((path: string) => {
    const full = "/gallery/" + path;
    window.history.pushState(null, "", full);
    setRoute(parseRoute(full));
  }, []);

  useEffect(() => {
    const onPopState = () => setRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  // Admin state
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

  // Create user dialog
  const [showCreateUser, setShowCreateUser] = useState(false);
  const [newUserName, setNewUserName] = useState("");
  const [newUserPassword, setNewUserPassword] = useState("");
  const [newUserMaxDeploys, setNewUserMaxDeploys] = useState("");
  const [creatingUser, setCreatingUser] = useState(false);

  // Created user result (to show API key)
  const [createdUserResult, setCreatedUserResult] = useState<{ name: string; apiKey: string } | null>(null);

  // Edit user dialog
  const [editUserTarget, setEditUserTarget] = useState<UserInfo | null>(null);
  const [editUserDisplayName, setEditUserDisplayName] = useState("");
  const [editUserPassword, setEditUserPassword] = useState("");
  const [editUserMaxDeploys, setEditUserMaxDeploys] = useState("");
  const [savingUser, setSavingUser] = useState(false);

  const fetchGallery = useCallback(() => {
    const headers: Record<string, string> = {};
    if (apiKey) headers["X-API-Key"] = apiKey;
    fetch("/api/gallery", { headers, credentials: "same-origin" })
      .then((r) => {
        if (r.status === 401) {
          // Not logged in — redirect to login
          window.location.href = "/login?redirect=/gallery/";
          return null;
        }
        return r.json();
      })
      .then((data: GalleryResponse | null) => {
        if (!data) return;
        setItems(data.deployments || []);
        setCurrentUserId(data.userId || null);
        setUserRole(data.userRole || null);
        setUserName(data.userName || null);
        setHasPassword(data.hasPassword || false);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, [apiKey]);

  useEffect(() => {
    fetchGallery();
  }, [fetchGallery]);

  const fetchAdminData = useCallback(() => {
    if (userRole !== "admin") return;
    const headers: Record<string, string> = { ...authHeaders(apiKey) };
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
      })
      .catch(() => {});
  }, [userRole, apiKey]);

  useEffect(() => {
    if (route.view === "settings") fetchAdminData();
  }, [route, fetchAdminData]);

  const handleLogin = () => {
    window.location.href = "/login?redirect=/gallery/";
  };

  const handleLogout = async () => {
    localStorage.removeItem(API_KEY_STORAGE);
    await fetch("/logout", { method: "POST", credentials: "same-origin" });
    window.location.reload();
  };

  const handleChangePassword = async () => {
    if (newPassword !== confirmPassword) {
      setPasswordError("Passwords do not match");
      return;
    }
    if (newPassword.length < 8) {
      setPasswordError("Password must be at least 8 characters");
      return;
    }
    setSavingPassword(true);
    setPasswordError("");
    try {
      const body: Record<string, string> = { newPassword };
      if (hasPassword && currentPassword) {
        body.currentPassword = currentPassword;
      }
      const res = await fetch("/api/me/password", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({ error: "Failed to change password" }));
        setPasswordError(data.error || "Failed to change password");
        return;
      }
      setHasPassword(true);
      setShowPasswordDialog(false);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } finally {
      setSavingPassword(false);
    }
  };

  const handleDestroy = async () => {
    if (!destroyTarget) return;
    setDestroying(true);
    try {
      await fetch(`/api/deployments/${destroyTarget.id}`, {
        method: "DELETE",
        headers: authHeaders(apiKey),
        credentials: "same-origin",
      });
      setDestroyTarget(null);
      fetchGallery();
    } finally {
      setDestroying(false);
    }
  };

  const openEdit = (item: GalleryItem) => {
    setEditTarget(item);
    setEditTitle(item.title);
    setEditDesc(item.description);
    // Map ttlHours back to select value
    const ttlMap: Record<number, string> = { 0: "0", 24: "24h", 72: "72h", 168: "7d", 720: "30d" };
    setEditTTL(ttlMap[item.ttlHours] ?? "0");
    setEditAccessMode(item.accessMode || "public");
    setEditUsername(item.accessUser || "");
    setEditPassword("");
    setEditApiKey("");
    setEditAccessUsers(item.accessUsers || "");
    setEditNetworkQuota(item.networkQuota || "");
  };

  const toggleLock = async (item: GalleryItem) => {
    await fetch(`/api/deployments/${item.id}/lock`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
      credentials: "same-origin",
      body: JSON.stringify({ locked: !item.locked }),
    });
    fetchGallery();
  };

  const handleSaveEdit = async () => {
    if (!editTarget) return;
    setSaving(true);
    try {
      await fetch(`/api/deployments/${editTarget.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify({
          title: editTitle,
          description: editDesc,
          ttl: editTTL,
          network_quota: editNetworkQuota,
        }),
      });

      const currentMode = editTarget.accessMode || "public";
      const modeChanged = editAccessMode !== currentMode;
      // Also update protection if credentials or user list changed (even if mode unchanged)
      const hasNewCredentials =
        (editAccessMode === "basic_auth" && editPassword) ||
        (editAccessMode === "api_key" && (editApiKey || modeChanged));
      const usersChanged = editAccessMode === "user" && editAccessUsers !== (editTarget.accessUsers || "");

      if (modeChanged || hasNewCredentials || usersChanged) {
        const protectBody: Record<string, unknown> = { mode: editAccessMode };
        if (editAccessMode === "basic_auth") {
          protectBody.username = editUsername;
          protectBody.password = editPassword;
        } else if (editAccessMode === "api_key") {
          if (editApiKey) protectBody.apiKey = editApiKey;
        } else if (editAccessMode === "user") {
          protectBody.users = editAccessUsers
            ? editAccessUsers.split(",").map((u) => u.trim()).filter(Boolean)
            : [];
        }
        const protectRes = await fetch(`/api/deployments/${editTarget.id}/protect`, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
          credentials: "same-origin",
          body: JSON.stringify(protectBody),
        });
        if (protectRes.ok && editAccessMode === "api_key") {
          const protectData = await protectRes.json();
          if (protectData.apiKey) {
            setGeneratedApiKey({
              name: editTarget.title || editTarget.name,
              key: protectData.apiKey,
            });
          }
        }
      }

      setEditTarget(null);
      fetchGallery();
    } finally {
      setSaving(false);
    }
  };

  const handleSaveSettings = async () => {
    setSettingsSaving(true);
    try {
      const body: Record<string, string> = {
        "oidc.issuer": oidcIssuer,
        "oidc.client_id": oidcClientId,
        "oidc.mode": oidcMode,
        "oidc.allowed_domains": oidcAllowedDomains,
      };
      if (oidcClientSecret) {
        body["oidc.client_secret"] = oidcClientSecret;
      }
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

  const handleCreateUser = async () => {
    if (!newUserName) return;
    setCreatingUser(true);
    try {
      const body: Record<string, unknown> = { name: newUserName };
      if (newUserPassword) body.password = newUserPassword;
      if (newUserMaxDeploys) body.maxDeployments = parseInt(newUserMaxDeploys, 10);
      const res = await fetch("/api/admin/users", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
        credentials: "same-origin",
        body: JSON.stringify(body),
      });
      if (res.ok) {
        const data = await res.json();
        setShowCreateUser(false);
        setNewUserName("");
        setNewUserPassword("");
        setNewUserMaxDeploys("");
        setCreatedUserResult({ name: data.name, apiKey: data.apiKey });
        fetchAdminData();
      }
    } finally {
      setCreatingUser(false);
    }
  };

  const handleDeleteUser = async (name: string) => {
    await fetch(`/api/admin/users/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: authHeaders(apiKey),
      credentials: "same-origin",
    });
    fetchAdminData();
  };

  const openEditUser = (u: UserInfo) => {
    setEditUserTarget(u);
    setEditUserDisplayName(u.displayName || "");
    setEditUserPassword("");
    setEditUserMaxDeploys(String(u.maxDeployments));
  };

  const handleSaveUser = async () => {
    if (!editUserTarget) return;
    setSavingUser(true);
    try {
      const body: Record<string, unknown> = {};
      if (editUserDisplayName !== (editUserTarget.displayName || "")) {
        body.displayName = editUserDisplayName;
      }
      if (editUserPassword) {
        body.password = editUserPassword;
      }
      const maxVal = parseInt(editUserMaxDeploys, 10);
      if (!isNaN(maxVal) && maxVal !== editUserTarget.maxDeployments) {
        body.maxDeployments = maxVal;
      }
      if (Object.keys(body).length > 0) {
        await fetch(`/api/admin/users/${encodeURIComponent(editUserTarget.name)}`, {
          method: "PATCH",
          headers: { "Content-Type": "application/json", ...authHeaders(apiKey) },
          credentials: "same-origin",
          body: JSON.stringify(body),
        });
      }
      setEditUserTarget(null);
      fetchAdminData();
    } finally {
      setSavingUser(false);
    }
  };

  const isOwned = (item: GalleryItem) => currentUserId != null && item.userId === currentUserId;

  const baseItems = route.view === "user" ? items.filter((i) => i.userId === route.userId) : items;

  const filtered = baseItems.filter((item) => {
    const q = search.toLowerCase();
    return (
      item.name.toLowerCase().includes(q) ||
      item.title.toLowerCase().includes(q) ||
      item.framework.toLowerCase().includes(q) ||
      item.ownerName.toLowerCase().includes(q)
    );
  });

  const isAdmin = userRole === "admin";

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b">
        <div className="mx-auto max-w-6xl px-4 py-4 sm:py-6">
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <h1 className="text-lg sm:text-xl font-bold tracking-tight truncate">
                <button onClick={() => navigate("")} className="hover:text-primary transition-colors">
                  OpenBerth Gallery
                </button>
              </h1>
              <p className="mt-1 text-sm text-muted-foreground hidden sm:block">
                Browse deployed apps
              </p>
            </div>
            <div className="flex items-center gap-1.5 sm:gap-2 shrink-0">
              {currentUserId ? (
                <>
                  <span className="text-sm text-muted-foreground hidden sm:inline">{userName}</span>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 w-8 p-0 sm:h-auto sm:w-auto sm:px-3 sm:py-1.5"
                    title="Password"
                    onClick={() => {
                      setPasswordError("");
                      setCurrentPassword("");
                      setNewPassword("");
                      setConfirmPassword("");
                      setShowPasswordDialog(true);
                    }}
                  >
                    <KeyRound className="h-3.5 w-3.5 sm:mr-1.5" />
                    <span className="hidden sm:inline">Password</span>
                  </Button>
                  {isAdmin && (
                    <Button
                      variant={route.view === "settings" ? "default" : "outline"}
                      size="sm"
                      className="h-8 w-8 p-0 sm:h-auto sm:w-auto sm:px-3 sm:py-1.5"
                      title="Settings"
                      onClick={() => navigate(route.view === "settings" ? "" : "settings")}
                    >
                      <Settings className="h-3.5 w-3.5 sm:mr-1.5" />
                      <span className="hidden sm:inline">Settings</span>
                    </Button>
                  )}
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 w-8 p-0 sm:h-auto sm:w-auto sm:px-3 sm:py-1.5"
                    title="Logout"
                    onClick={handleLogout}
                  >
                    <LogOut className="h-3.5 w-3.5 sm:mr-1.5" />
                    <span className="hidden sm:inline">Logout</span>
                  </Button>
                </>
              ) : (
                <Button variant="outline" size="sm" onClick={handleLogin}>
                  <LogIn className="mr-1.5 h-3.5 w-3.5" />
                  Login
                </Button>
              )}
            </div>
          </div>
          {route.view !== "settings" && (
            <div className="relative mt-4 max-w-md">
              <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Filter by name, framework, or owner..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-9"
              />
            </div>
          )}
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-8">
        {route.view === "settings" && isAdmin ? (
          <div className="space-y-8">
            <Tabs defaultValue="oidc">
              <TabsList>
                <TabsTrigger value="oidc">OIDC / SSO</TabsTrigger>
                <TabsTrigger value="users">Users</TabsTrigger>
                <TabsTrigger value="network">Network</TabsTrigger>
              </TabsList>
              <TabsContent value="oidc" className="pt-4">
                <div className="grid gap-8 lg:grid-cols-[1fr,1fr]">
                  {/* Form — left on desktop, below instructions on mobile */}
                  <div className="space-y-6 order-2 lg:order-1">
                    <div className="grid gap-2">
                      <Label htmlFor="oidc-issuer">Issuer URL</Label>
                      <Input
                        id="oidc-issuer"
                        value={oidcIssuer}
                        onChange={(e) => setOidcIssuer(e.target.value)}
                        placeholder="https://accounts.google.com"
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="oidc-client-id">Client ID</Label>
                      <Input
                        id="oidc-client-id"
                        value={oidcClientId}
                        onChange={(e) => setOidcClientId(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="oidc-secret">Client Secret</Label>
                      <Input
                        id="oidc-secret"
                        type="password"
                        value={oidcClientSecret}
                        onChange={(e) => setOidcClientSecret(e.target.value)}
                        placeholder={settings["oidc.client_secret"] === "***" ? "••• (configured)" : ""}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="oidc-mode">Login Mode</Label>
                      <select
                        id="oidc-mode"
                        value={oidcMode}
                        onChange={(e) => setOidcMode(e.target.value)}
                        className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                      >
                        <option value="">Optional — show SSO button alongside password form</option>
                        <option value="sso_only">SSO Only — auto-redirect to identity provider</option>
                      </select>
                      <p className="text-xs text-muted-foreground">
                        SSO Only hides the password form and auto-redirects to your identity provider.
                      </p>
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="oidc-domains">Allowed Email Domains</Label>
                      <Input
                        id="oidc-domains"
                        value={oidcAllowedDomains}
                        onChange={(e) => setOidcAllowedDomains(e.target.value)}
                        placeholder="company.com, subsidiary.com"
                      />
                      <p className="text-xs text-muted-foreground">
                        Comma-separated. Leave empty to allow all domains.
                      </p>
                    </div>
                    <Button onClick={handleSaveSettings} disabled={settingsSaving}>
                      {settingsSaving ? "Saving..." : "Save OIDC Settings"}
                    </Button>
                  </div>

                  {/* Instructions — right on desktop, accordion on mobile */}
                  <div className="order-1 lg:order-2">
                    {/* Mobile: accordion */}
                    <div className="lg:hidden">
                      <button
                        type="button"
                        onClick={() => setOidcInstructionsOpen(!oidcInstructionsOpen)}
                        className="flex w-full items-center justify-between rounded-md border bg-muted/50 px-4 py-3 text-sm font-medium transition-colors hover:bg-muted"
                      >
                        Setup Instructions
                        <ChevronDown className={`h-4 w-4 text-muted-foreground transition-transform ${oidcInstructionsOpen ? "rotate-180" : ""}`} />
                      </button>
                      {oidcInstructionsOpen && (
                        <div className="rounded-b-md border border-t-0 bg-muted/50 px-4 pb-4 pt-2 text-sm space-y-3">
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
                            <p className="text-xs text-muted-foreground">
                              <strong>Common issuer URLs:</strong>
                            </p>
                            <ul className="text-xs text-muted-foreground mt-1 space-y-0.5">
                              <li>Google: <code className="bg-background px-1 rounded">https://accounts.google.com</code></li>
                              <li>Microsoft: <code className="bg-background px-1 rounded">https://login.microsoftonline.com/&#123;tenant&#125;/v2.0</code></li>
                              <li>Okta: <code className="bg-background px-1 rounded">https://&#123;domain&#125;.okta.com</code></li>
                              <li>Auth0: <code className="bg-background px-1 rounded">https://&#123;domain&#125;.auth0.com</code></li>
                              <li>Keycloak: <code className="bg-background px-1 rounded">https://&#123;host&#125;/realms/&#123;realm&#125;</code></li>
                            </ul>
                          </div>
                        </div>
                      )}
                    </div>

                    {/* Desktop: always visible */}
                    <div className="hidden lg:block rounded-md border bg-muted/50 p-4 text-sm space-y-3 sticky top-8">
                      <p className="font-medium">Setup Instructions</p>
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
                        <p className="text-xs text-muted-foreground">
                          <strong>Common issuer URLs:</strong>
                        </p>
                        <ul className="text-xs text-muted-foreground mt-1 space-y-0.5">
                          <li>Google: <code className="bg-background px-1 rounded">https://accounts.google.com</code></li>
                          <li>Microsoft: <code className="bg-background px-1 rounded">https://login.microsoftonline.com/&#123;tenant&#125;/v2.0</code></li>
                          <li>Okta: <code className="bg-background px-1 rounded">https://&#123;domain&#125;.okta.com</code></li>
                          <li>Auth0: <code className="bg-background px-1 rounded">https://&#123;domain&#125;.auth0.com</code></li>
                          <li>Keycloak: <code className="bg-background px-1 rounded">https://&#123;host&#125;/realms/&#123;realm&#125;</code></li>
                        </ul>
                      </div>
                    </div>
                  </div>
                </div>
              </TabsContent>
              <TabsContent value="users" className="pt-4">
                <div className="mb-4">
                  <Button size="sm" onClick={() => setShowCreateUser(true)}>
                    <UserPlus className="mr-1.5 h-3.5 w-3.5" />
                    Create User
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
                        <TableCell>
                          <Badge variant={u.role === "admin" ? "default" : "secondary"}>
                            {u.role}
                          </Badge>
                        </TableCell>
                        <TableCell className="hidden sm:table-cell">{u.displayName || "—"}</TableCell>
                        <TableCell className="hidden sm:table-cell">{u.maxDeployments}</TableCell>
                        <TableCell className="hidden md:table-cell text-muted-foreground">
                          {u.createdAt ? formatAge(u.createdAt) : "—"}
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1">
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              title="Edit user"
                              onClick={() => openEditUser(u)}
                            >
                              <Pencil className="h-3.5 w-3.5" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-destructive hover:text-destructive"
                              title="Delete user"
                              onClick={() => handleDeleteUser(u.name)}
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                    {users.length === 0 && (
                      <TableRow>
                        <TableCell colSpan={6} className="text-center text-muted-foreground">
                          No users found.
                        </TableCell>
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
                      className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${
                        networkQuotaEnabled ? "bg-primary" : "bg-muted"
                      }`}
                    >
                      <span
                        className={`pointer-events-none block h-5 w-5 rounded-full bg-background shadow-lg ring-0 transition-transform ${
                          networkQuotaEnabled ? "translate-x-5" : "translate-x-0"
                        }`}
                      />
                    </button>
                    <Label>Enable network quota</Label>
                  </div>
                  {networkQuotaEnabled && (
                    <>
                    <div className="grid gap-2">
                      <Label htmlFor="network-quota">Default quota per deployment</Label>
                      <Input
                        id="network-quota"
                        value={networkDefaultQuota}
                        onChange={(e) => setNetworkDefaultQuota(e.target.value)}
                        placeholder="e.g. 5g, 500m, 10g"
                      />
                      <p className="text-xs text-muted-foreground">
                        Use <code className="bg-muted px-1 rounded">m</code> for megabytes, <code className="bg-muted px-1 rounded">g</code> for gigabytes.
                        This applies to all new deployments. Per-deployment overrides are available via the API.
                      </p>
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="network-reset-interval">Quota reset interval</Label>
                      <select
                        id="network-reset-interval"
                        value={networkResetInterval}
                        onChange={(e) => setNetworkResetInterval(e.target.value)}
                        className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      >
                        <option value="">Default (30 days)</option>
                        <option value="7d">7 days</option>
                        <option value="30d">30 days</option>
                        <option value="90d">90 days</option>
                      </select>
                      <p className="text-xs text-muted-foreground">
                        How often to reset the byte counter for all deployments.
                        After a reset, each deployment gets its full quota back.
                      </p>
                    </div>
                    </>
                  )}
                  <Button onClick={handleSaveNetworkSettings} disabled={settingsSaving}>
                    {settingsSaving ? "Saving..." : "Save Network Settings"}
                  </Button>
                </div>
              </TabsContent>
            </Tabs>
          </div>
        ) : route.view === "user" ? (
          <div>
            <button
              onClick={() => navigate("")}
              className="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              <ArrowLeft className="h-4 w-4" />
              Back to gallery
            </button>
            <div className="mb-6">
              <h2 className="text-lg font-semibold">
                {baseItems[0]?.ownerName || "User"}
              </h2>
              <p className="text-sm text-muted-foreground">
                {baseItems.length} {baseItems.length === 1 ? "deployment" : "deployments"}
              </p>
            </div>
            {filtered.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {baseItems.length === 0 ? "No deployments from this user." : "No matches found."}
              </p>
            ) : (
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {filtered.map((item) => (
                  <Card key={item.id} className={`flex flex-col${isNotRunning(item) ? " opacity-75" : ""}`}>
                    <CardHeader>
                      <div className="flex items-start justify-between gap-2">
                        <CardTitle className="break-all">
                          {item.title || item.name}
                        </CardTitle>
                        <div className="flex shrink-0 items-center gap-1.5">
                          {item.status && item.status !== "running" && statusBadge(item.status)}
                          {item.mode === "sandbox" && (
                            <Badge variant="outline" className="text-[10px] border-amber-500 text-amber-600">
                              Sandbox
                            </Badge>
                          )}
                          {item.locked && (
                            <Badge variant="outline" className="text-[10px] border-orange-500 text-orange-600">
                              Locked
                            </Badge>
                          )}
                          {item.accessMode && item.accessMode !== "public" && (
                            <span title={item.accessMode}>
                              <Lock className="h-3.5 w-3.5 text-muted-foreground" />
                            </span>
                          )}
                          {item.networkQuota && (
                            <Badge variant="outline" className="text-[10px]">
                              {item.networkQuota}
                            </Badge>
                          )}
                          <Badge variant="secondary" className="text-[10px]">
                            {item.framework}
                          </Badge>
                        </div>
                      </div>
                      {item.description && (
                        <CardDescription className="mt-1.5 line-clamp-2">
                          {item.description}
                        </CardDescription>
                      )}
                    </CardHeader>
                    <CardContent className="flex-1">
                      <div className="flex items-center gap-4 text-xs text-muted-foreground">
                        <span className="flex items-center gap-1">
                          <Clock className="h-3 w-3" />
                          {formatAge(item.createdAt)}
                        </span>
                        {item.expiresAt && (
                          <span className="text-xs" title={`Expires: ${item.expiresAt}`}>
                            {formatExpiry(item.expiresAt)}
                          </span>
                        )}
                      </div>
                    </CardContent>
                    <CardFooter className="justify-between">
                      <a
                        href={item.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline"
                      >
                        Visit
                        <ExternalLink className="h-3 w-3" />
                      </a>
                      {isOwned(item) && (
                        <div className="flex items-center gap-1">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7"
                            onClick={() => toggleLock(item)}
                            title={item.locked ? "Unlock" : "Lock"}
                            disabled={isNotRunning(item)}
                          >
                            {item.locked ? <LockOpen className="h-3.5 w-3.5" /> : <ShieldCheck className="h-3.5 w-3.5" />}
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7"
                            onClick={() => openEdit(item)}
                            title="Edit"
                            disabled={item.locked || isNotRunning(item)}
                          >
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-destructive hover:text-destructive"
                            onClick={() => setDestroyTarget(item)}
                            title="Destroy"
                            disabled={item.locked || isNotRunning(item)}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      )}
                    </CardFooter>
                  </Card>
                ))}
              </div>
            )}
          </div>
        ) : loading ? (
          <p className="text-sm text-muted-foreground">Loading...</p>
        ) : filtered.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {items.length === 0
              ? "No deployments yet."
              : "No matches found."}
          </p>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {filtered.map((item) => (
              <Card key={item.id} className={`flex flex-col${isNotRunning(item) ? " opacity-75" : ""}`}>
                <CardHeader>
                  <div className="flex items-start justify-between gap-2">
                    <CardTitle className="break-all">
                      {item.title || item.name}
                    </CardTitle>
                    <div className="flex shrink-0 items-center gap-1.5">
                      {item.status && item.status !== "running" && statusBadge(item.status)}
                      {item.mode === "sandbox" && (
                        <Badge variant="outline" className="text-[10px] border-amber-500 text-amber-600">
                          Sandbox
                        </Badge>
                      )}
                      {item.locked && (
                        <Badge variant="outline" className="text-[10px] border-orange-500 text-orange-600">
                          Locked
                        </Badge>
                      )}
                      {item.accessMode && item.accessMode !== "public" && (
                        <span title={item.accessMode}>
                          <Lock className="h-3.5 w-3.5 text-muted-foreground" />
                        </span>
                      )}
                      {item.networkQuota && (
                        <Badge variant="outline" className="text-[10px]">
                          {item.networkQuota}
                        </Badge>
                      )}
                      <Badge variant="secondary" className="text-[10px]">
                        {item.framework}
                      </Badge>
                    </div>
                  </div>
                  {item.description && (
                    <CardDescription className="mt-1.5 line-clamp-2">
                      {item.description}
                    </CardDescription>
                  )}
                </CardHeader>
                <CardContent className="flex-1">
                  <div className="flex items-center gap-4 text-xs text-muted-foreground">
                    <button
                      onClick={() => navigate("user/" + item.userId)}
                      className="hover:underline hover:text-foreground transition-colors"
                    >
                      {item.ownerName}
                    </button>
                    <span className="flex items-center gap-1">
                      <Clock className="h-3 w-3" />
                      {formatAge(item.createdAt)}
                    </span>
                    {item.expiresAt && (
                      <span className="text-xs" title={`Expires: ${item.expiresAt}`}>
                        {formatExpiry(item.expiresAt)}
                      </span>
                    )}
                  </div>
                </CardContent>
                <CardFooter className="justify-between">
                  <a
                    href={item.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline"
                  >
                    Visit
                    <ExternalLink className="h-3 w-3" />
                  </a>
                  {isOwned(item) && (
                    <div className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => toggleLock(item)}
                        title={item.locked ? "Unlock" : "Lock"}
                        disabled={isNotRunning(item)}
                      >
                        {item.locked ? <LockOpen className="h-3.5 w-3.5" /> : <ShieldCheck className="h-3.5 w-3.5" />}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => openEdit(item)}
                        title="Edit"
                        disabled={item.locked || isNotRunning(item)}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-destructive hover:text-destructive"
                        onClick={() => setDestroyTarget(item)}
                        title="Destroy"
                        disabled={item.locked || isNotRunning(item)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  )}
                </CardFooter>
              </Card>
            ))}
          </div>
        )}
      </main>

      {/* Destroy confirmation dialog */}
      <Dialog open={!!destroyTarget} onOpenChange={(open) => !open && setDestroyTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Destroy deployment</DialogTitle>
            <DialogDescription>
              Are you sure you want to destroy <strong>{destroyTarget?.title || destroyTarget?.name}</strong>? This action cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDestroyTarget(null)} disabled={destroying}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={handleDestroy} disabled={destroying}>
              {destroying ? "Destroying..." : "Destroy"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit dialog */}
      <Dialog open={!!editTarget} onOpenChange={(open) => !open && setEditTarget(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Edit deployment</DialogTitle>
            <DialogDescription>
              Update metadata for <strong>{editTarget?.title || editTarget?.name}</strong>.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="edit-title">Title</Label>
              <Input
                id="edit-title"
                value={editTitle}
                onChange={(e) => setEditTitle(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="edit-desc">Description</Label>
              <Input
                id="edit-desc"
                value={editDesc}
                onChange={(e) => setEditDesc(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="edit-ttl">TTL</Label>
              <select
                id="edit-ttl"
                value={editTTL}
                onChange={(e) => setEditTTL(e.target.value)}
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              >
                <option value="0">Never expires</option>
                <option value="24h">24 hours</option>
                <option value="72h">3 days</option>
                <option value="7d">7 days</option>
                <option value="30d">30 days</option>
              </select>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="edit-quota">Network quota</Label>
              <Input
                id="edit-quota"
                value={editNetworkQuota}
                onChange={(e) => setEditNetworkQuota(e.target.value)}
                placeholder="e.g. 1g, 5g (empty = no quota)"
              />
              <p className="text-xs text-muted-foreground">
                Leave empty to use server default or disable
              </p>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="edit-access">Protection</Label>
              <select
                id="edit-access"
                value={editAccessMode}
                onChange={(e) => setEditAccessMode(e.target.value)}
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              >
                <option value="public">Public</option>
                <option value="basic_auth">Basic Auth</option>
                <option value="api_key">API Key</option>
                <option value="user">User (Login Required)</option>
              </select>
            </div>
            {editAccessMode === "basic_auth" && (
              <div className="grid gap-2">
                <Label htmlFor="edit-user">Username</Label>
                <Input
                  id="edit-user"
                  value={editUsername}
                  onChange={(e) => setEditUsername(e.target.value)}
                />
                <Label htmlFor="edit-pass">Password</Label>
                <Input
                  id="edit-pass"
                  type="password"
                  value={editPassword}
                  onChange={(e) => setEditPassword(e.target.value)}
                  placeholder={editTarget?.accessMode === "basic_auth" ? "Leave empty to keep current" : ""}
                />
                {editTarget?.accessMode === "basic_auth" && !editPassword && (
                  <p className="text-xs text-muted-foreground">Currently configured. Enter a new password to update.</p>
                )}
              </div>
            )}
            {editAccessMode === "api_key" && (
              <div className="grid gap-2">
                <Label htmlFor="edit-ak">API Key</Label>
                <div className="flex items-center gap-2">
                  <KeyRound className="h-4 w-4 text-muted-foreground shrink-0" />
                  <Input
                    id="edit-ak"
                    value={editApiKey}
                    onChange={(e) => setEditApiKey(e.target.value)}
                    placeholder={editTarget?.accessMode === "api_key" ? "Leave empty to keep current" : "Leave empty to auto-generate"}
                  />
                </div>
                {editTarget?.accessMode === "api_key" && !editApiKey && (
                  <p className="text-xs text-muted-foreground">Currently configured. Enter a new key to update.</p>
                )}
              </div>
            )}
            {editAccessMode === "user" && (
              <div className="grid gap-2">
                <Label htmlFor="edit-access-users">Allowed Users</Label>
                <Input
                  id="edit-access-users"
                  value={editAccessUsers}
                  onChange={(e) => setEditAccessUsers(e.target.value)}
                  placeholder="All authenticated users"
                />
                <p className="text-xs text-muted-foreground">
                  Comma-separated usernames. Leave empty to allow all authenticated users.
                  The deployment owner and admins always have access.
                </p>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditTarget(null)} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={handleSaveEdit} disabled={saving}>
              {saving ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Create user dialog */}
      <Dialog open={showCreateUser} onOpenChange={(open) => !open && setShowCreateUser(false)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Create user</DialogTitle>
            <DialogDescription>Add a new user to OpenBerth.</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="new-user-name">Username</Label>
              <Input
                id="new-user-name"
                value={newUserName}
                onChange={(e) => setNewUserName(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="new-user-pass">Password (optional)</Label>
              <Input
                id="new-user-pass"
                type="password"
                value={newUserPassword}
                onChange={(e) => setNewUserPassword(e.target.value)}
                placeholder="Leave empty for API key only"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="new-user-max">Max Deployments</Label>
              <Input
                id="new-user-max"
                type="number"
                min="0"
                value={newUserMaxDeploys}
                onChange={(e) => setNewUserMaxDeploys(e.target.value)}
                placeholder="Default from server config"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setShowCreateUser(false)} disabled={creatingUser}>
              Cancel
            </Button>
            <Button onClick={handleCreateUser} disabled={creatingUser || !newUserName}>
              {creatingUser ? "Creating..." : "Create"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit user dialog */}
      <Dialog open={!!editUserTarget} onOpenChange={(open) => !open && setEditUserTarget(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Edit user</DialogTitle>
            <DialogDescription>
              Update settings for <strong>{editUserTarget?.name}</strong>.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="eu-display">Display Name</Label>
              <Input
                id="eu-display"
                value={editUserDisplayName}
                onChange={(e) => setEditUserDisplayName(e.target.value)}
                placeholder="Optional display name"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="eu-max">Max Deployments</Label>
              <Input
                id="eu-max"
                type="number"
                min="0"
                value={editUserMaxDeploys}
                onChange={(e) => setEditUserMaxDeploys(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="eu-pass">New Password</Label>
              <Input
                id="eu-pass"
                type="password"
                value={editUserPassword}
                onChange={(e) => setEditUserPassword(e.target.value)}
                placeholder="Leave empty to keep current"
              />
              {editUserPassword && editUserPassword.length < 8 && (
                <p className="text-xs text-destructive">Minimum 8 characters</p>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditUserTarget(null)} disabled={savingUser}>
              Cancel
            </Button>
            <Button
              onClick={handleSaveUser}
              disabled={savingUser || (editUserPassword.length > 0 && editUserPassword.length < 8)}
            >
              {savingUser ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Deployment API key dialog */}
      <Dialog open={!!generatedApiKey} onOpenChange={(open) => !open && setGeneratedApiKey(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>API key protection enabled</DialogTitle>
            <DialogDescription>
              Save this API key now — it won't be shown again.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 py-2">
            <div className="grid gap-1">
              <Label className="text-xs text-muted-foreground">Deployment</Label>
              <p className="font-medium">{generatedApiKey?.name}</p>
            </div>
            <div className="grid gap-1">
              <Label className="text-xs text-muted-foreground">API Key</Label>
              <div className="flex items-center gap-2">
                <code className="flex-1 rounded bg-muted px-3 py-2 text-sm font-mono break-all">
                  {generatedApiKey?.key}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    if (generatedApiKey?.key) {
                      navigator.clipboard.writeText(generatedApiKey.key);
                    }
                  }}
                >
                  Copy
                </Button>
              </div>
            </div>
            <div className="rounded-md border bg-muted/50 p-3 text-xs text-muted-foreground space-y-1">
              <p>Use this key to access the deployment via header:</p>
              <p><code className="bg-background px-1 rounded">X-Api-Key: {generatedApiKey?.key}</code></p>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => setGeneratedApiKey(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Created user API key dialog */}
      <Dialog open={!!createdUserResult} onOpenChange={(open) => !open && setCreatedUserResult(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>User created</DialogTitle>
            <DialogDescription>
              Save this API key now — it won't be shown again.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 py-2">
            <div className="grid gap-1">
              <Label className="text-xs text-muted-foreground">Username</Label>
              <p className="font-medium">{createdUserResult?.name}</p>
            </div>
            <div className="grid gap-1">
              <Label className="text-xs text-muted-foreground">API Key</Label>
              <div className="flex items-center gap-2">
                <code className="flex-1 rounded bg-muted px-3 py-2 text-sm font-mono break-all">
                  {createdUserResult?.apiKey}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    if (createdUserResult?.apiKey) {
                      navigator.clipboard.writeText(createdUserResult.apiKey);
                    }
                  }}
                >
                  Copy
                </Button>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => setCreatedUserResult(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Password change dialog */}
      <Dialog open={showPasswordDialog} onOpenChange={(open) => !open && setShowPasswordDialog(false)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{hasPassword ? "Change password" : "Set password"}</DialogTitle>
            <DialogDescription>
              {hasPassword
                ? "Enter your current password and choose a new one."
                : "Set a password to log in via the web dashboard."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            {hasPassword && (
              <div className="grid gap-2">
                <Label htmlFor="pw-current">Current password</Label>
                <Input
                  id="pw-current"
                  type="password"
                  value={currentPassword}
                  onChange={(e) => setCurrentPassword(e.target.value)}
                />
              </div>
            )}
            <div className="grid gap-2">
              <Label htmlFor="pw-new">New password</Label>
              <Input
                id="pw-new"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
              />
              {newPassword && newPassword.length < 8 && (
                <p className="text-xs text-destructive">Minimum 8 characters</p>
              )}
            </div>
            <div className="grid gap-2">
              <Label htmlFor="pw-confirm">Confirm new password</Label>
              <Input
                id="pw-confirm"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
              />
              {confirmPassword && newPassword !== confirmPassword && (
                <p className="text-xs text-destructive">Passwords do not match</p>
              )}
            </div>
            {passwordError && (
              <p className="text-sm text-destructive">{passwordError}</p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setShowPasswordDialog(false)} disabled={savingPassword}>
              Cancel
            </Button>
            <Button
              onClick={handleChangePassword}
              disabled={savingPassword || newPassword.length < 8 || newPassword !== confirmPassword}
            >
              {savingPassword ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
