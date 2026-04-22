import { useCallback, useEffect, useState } from "react";
import { Search, KeyRound, LogIn, LogOut, Settings, ArrowLeft } from "lucide-react";
import { Input } from "./components/ui/input";
import { Button } from "./components/ui/button";
import { useTheme } from "./hooks/useTheme";
import { useAuth, authHeaders } from "./hooks/useAuth";
import { useGallery } from "./hooks/useGallery";
import { ThemeToggle } from "./components/ThemeToggle";
import { AppGrid } from "./components/AppGrid";
import { AppDetailView } from "./components/AppDetailView";
import { SettingsView } from "./components/SettingsView";
import { DestroyDialog } from "./components/dialogs/DestroyDialog";
import { EditDialog, type EditData } from "./components/dialogs/EditDialog";
import { PasswordDialog } from "./components/dialogs/PasswordDialog";
import { ApiKeyDialog } from "./components/dialogs/ApiKeyDialog";
import type { GalleryItem, MeResponse } from "./types";

type Route =
  | { view: "gallery" }
  | { view: "user"; userId: string }
  | { view: "app"; appId: string }
  | { view: "settings" };

function parseRoute(pathname: string): Route {
  const userMatch = pathname.match(/^\/gallery\/user\/([^/]+)/);
  if (userMatch) return { view: "user", userId: userMatch[1] };
  const appMatch = pathname.match(/^\/gallery\/app\/([^/]+)/);
  if (appMatch) return { view: "app", appId: appMatch[1] };
  if (pathname.startsWith("/gallery/settings")) return { view: "settings" };
  return { view: "gallery" };
}

type StatusFilter = "all" | "running" | "building" | "failed" | "stopped";

export default function App() {
  const { theme, cycleTheme } = useTheme();
  const auth = useAuth();
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname));
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");

  // Dialogs
  const [destroyTarget, setDestroyTarget] = useState<GalleryItem | null>(null);
  const [destroying, setDestroying] = useState(false);
  const [editTarget, setEditTarget] = useState<GalleryItem | null>(null);
  const [showPasswordDialog, setShowPasswordDialog] = useState(false);
  const [generatedApiKey, setGeneratedApiKey] = useState<{ name: string; key: string } | null>(null);

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

  const onAuthData = useCallback((data: MeResponse) => {
    auth.setCurrentUserId(data.id || null);
    auth.setUserRole(data.role || null);
    auth.setUserName(data.displayName || data.name || null);
    auth.setHasPassword(data.hasPassword || false);
    auth.setServerVersion(data.serverVersion || "");
  }, []);

  const { items, loading, fetchGallery } = useGallery({ apiKey: auth.apiKey, onAuthData });

  // Filter items (only for gallery and user views)
  const baseItems = route.view === "user" ? items.filter((i) => i.ownerId === (route as { userId: string }).userId) : items;

  const filtered = baseItems.filter((item) => {
    if (statusFilter !== "all" && item.status !== statusFilter) return false;
    if (!search) return true;
    const q = search.toLowerCase();
    return (
      item.name.toLowerCase().includes(q) ||
      item.title.toLowerCase().includes(q) ||
      item.framework.toLowerCase().includes(q) ||
      item.ownerName.toLowerCase().includes(q)
    );
  });

  // Status counts for filter buttons
  const statusCounts = baseItems.reduce<Record<string, number>>((acc, item) => {
    acc[item.status] = (acc[item.status] || 0) + 1;
    return acc;
  }, {});

  // Handlers
  const handleDestroy = async () => {
    if (!destroyTarget) return;
    setDestroying(true);
    try {
      await fetch(`/api/deployments/${destroyTarget.id}`, {
        method: "DELETE",
        headers: authHeaders(auth.apiKey),
        credentials: "same-origin",
      });
      setDestroyTarget(null);
      fetchGallery();
      // Navigate back to gallery if we were on the detail page
      if (route.view === "app") navigate("");
    } finally {
      setDestroying(false);
    }
  };

  const toggleLock = async (item: GalleryItem) => {
    await fetch(`/api/deployments/${item.id}/lock`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders(auth.apiKey) },
      credentials: "same-origin",
      body: JSON.stringify({ locked: !item.locked }),
    });
    fetchGallery();
  };

  const handleSaveEdit = async (data: EditData) => {
    if (!editTarget) return;
    await fetch(`/api/deployments/${editTarget.id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json", ...authHeaders(auth.apiKey) },
      credentials: "same-origin",
      body: JSON.stringify({
        title: data.title,
        description: data.description,
        ttl: data.ttl,
        network_quota: data.networkQuota,
      }),
    });

    const currentMode = editTarget.accessMode || "public";
    const modeChanged = data.accessMode !== currentMode;
    const hasNewCredentials =
      (data.accessMode === "basic_auth" && data.password) ||
      (data.accessMode === "api_key" && (data.apiKey || modeChanged));
    const usersChanged = data.accessMode === "user" && data.accessUsers !== (editTarget.accessUsers || "");

    if (modeChanged || hasNewCredentials || usersChanged) {
      const protectBody: Record<string, unknown> = { mode: data.accessMode };
      if (data.accessMode === "basic_auth") {
        protectBody.username = data.username;
        protectBody.password = data.password;
      } else if (data.accessMode === "api_key") {
        if (data.apiKey) protectBody.apiKey = data.apiKey;
      } else if (data.accessMode === "user") {
        protectBody.users = data.accessUsers
          ? data.accessUsers.split(",").map((u) => u.trim()).filter(Boolean)
          : [];
      }
      const protectRes = await fetch(`/api/deployments/${editTarget.id}/protect`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders(auth.apiKey) },
        credentials: "same-origin",
        body: JSON.stringify(protectBody),
      });
      if (protectRes.ok && data.accessMode === "api_key") {
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
  };

  const handleChangePassword = async (currentPassword: string, newPassword: string): Promise<string | null> => {
    const body: Record<string, string> = { newPassword };
    if (auth.hasPassword && currentPassword) body.currentPassword = currentPassword;
    const res = await fetch("/api/me/password", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders(auth.apiKey) },
      credentials: "same-origin",
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({ error: "Failed to change password" }));
      return data.error || "Failed to change password";
    }
    auth.setHasPassword(true);
    return null;
  };

  const showSearchAndFilters = route.view === "gallery" || route.view === "user";
  const showFilters = showSearchAndFilters && baseItems.length > 0;
  const hasMultipleStatuses = Object.keys(statusCounts).length > 1;

  const filterButtons: { value: StatusFilter; label: string }[] = [
    { value: "all", label: `All (${baseItems.length})` },
    ...(statusCounts.running ? [{ value: "running" as StatusFilter, label: `Running (${statusCounts.running})` }] : []),
    ...(statusCounts.building ? [{ value: "building" as StatusFilter, label: `Building (${statusCounts.building})` }] : []),
    ...(statusCounts.failed ? [{ value: "failed" as StatusFilter, label: `Failed (${statusCounts.failed})` }] : []),
    ...(statusCounts.stopped ? [{ value: "stopped" as StatusFilter, label: `Stopped (${statusCounts.stopped})` }] : []),
  ];

  return (
    <div className="min-h-screen bg-background flex flex-col">
      {/* Header */}
      <header className="border-b">
        <div className="mx-auto max-w-6xl px-4 py-3 sm:py-4">
          <div className="flex items-center justify-between gap-3">
            <button onClick={() => navigate("")} className="text-lg font-bold tracking-tight hover:text-primary transition-colors shrink-0">
              OpenBerth
            </button>

            {/* Search — center (hidden on detail/settings views) */}
            {showSearchAndFilters && (
              <div className="relative flex-1 max-w-md hidden sm:block">
                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder={`Search ${baseItems.length} apps...`}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  className="pl-9 h-9"
                />
              </div>
            )}

            {/* Actions — right */}
            <div className="flex items-center gap-1.5 shrink-0">
              <ThemeToggle theme={theme} onCycle={cycleTheme} />
              {auth.currentUserId ? (
                <>
                  <span className="text-sm text-muted-foreground hidden md:inline">{auth.userName}</span>
                  <Button variant="outline" size="sm" className="h-8 w-8 p-0" title="Password" onClick={() => setShowPasswordDialog(true)}>
                    <KeyRound className="h-3.5 w-3.5" />
                  </Button>
                  {auth.isAdmin && (
                    <Button variant={route.view === "settings" ? "default" : "outline"} size="sm" className="h-8 w-8 p-0" title="Settings" onClick={() => navigate(route.view === "settings" ? "" : "settings")}>
                      <Settings className="h-3.5 w-3.5" />
                    </Button>
                  )}
                  <Button variant="outline" size="sm" className="h-8 w-8 p-0" title="Logout" onClick={auth.handleLogout}>
                    <LogOut className="h-3.5 w-3.5" />
                  </Button>
                </>
              ) : (
                <Button variant="outline" size="sm" className="h-8 w-8 p-0" title="Login" onClick={auth.handleLogin}>
                  <LogIn className="h-3.5 w-3.5" />
                </Button>
              )}
            </div>
          </div>

          {/* Mobile search */}
          {showSearchAndFilters && (
            <div className="relative mt-3 sm:hidden">
              <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder={`Search ${baseItems.length} apps...`}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-9 h-9"
              />
            </div>
          )}
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-6 flex-1 w-full">
        {route.view === "settings" && auth.isAdmin ? (
          <SettingsView apiKey={auth.apiKey} />
        ) : route.view === "app" ? (
          <AppDetailView
            item={items.find((i) => i.id === (route as { appId: string }).appId)}
            isOwned={auth.currentUserId != null && items.find((i) => i.id === (route as { appId: string }).appId)?.ownerId === auth.currentUserId}
            onBack={() => navigate("")}
            onNavigateUser={(id) => navigate("user/" + id)}
            onToggleLock={toggleLock}
            onEdit={setEditTarget}
            onDestroy={setDestroyTarget}
          />
        ) : route.view === "user" ? (
          <div>
            <button onClick={() => navigate("")} className="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors">
              <ArrowLeft className="h-4 w-4" />Back to gallery
            </button>
            <div className="mb-6">
              <h2 className="text-lg font-semibold">{baseItems[0]?.ownerName || "User"}</h2>
              <p className="text-sm text-muted-foreground">{baseItems.length} {baseItems.length === 1 ? "deployment" : "deployments"}</p>
            </div>
            <AppGrid
              items={filtered}
              emptyMessage={baseItems.length === 0 ? "No deployments from this user." : "No matches found."}
              onNavigate={(id) => navigate("app/" + id)}
            />
          </div>
        ) : loading ? (
          <div className="flex items-center justify-center py-16 text-muted-foreground">
            <p className="text-sm">Loading...</p>
          </div>
        ) : (
          <>
            {/* Status filter bar */}
            {showFilters && hasMultipleStatuses && (
              <div className="flex flex-wrap items-center gap-1.5 mb-5">
                {filterButtons.map((f) => (
                  <button
                    key={f.value}
                    onClick={() => setStatusFilter(f.value)}
                    className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                      statusFilter === f.value
                        ? "bg-primary text-primary-foreground"
                        : "bg-muted text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
            )}
            <AppGrid
              items={filtered}
              emptyMessage={items.length === 0 ? "No deployments yet." : "No matches found."}
              onNavigate={(id) => navigate("app/" + id)}
            />
          </>
        )}
      </main>

      {/* Footer */}
      <footer className="border-t mt-auto">
        <div className="mx-auto max-w-6xl px-4 py-3 flex items-center justify-between text-xs text-muted-foreground">
          <span>OpenBerth</span>
          {auth.serverVersion && <span>{auth.serverVersion}</span>}
        </div>
      </footer>

      {/* Dialogs */}
      <DestroyDialog target={destroyTarget} destroying={destroying} onClose={() => setDestroyTarget(null)} onConfirm={handleDestroy} />
      <EditDialog target={editTarget} onClose={() => setEditTarget(null)} onSave={handleSaveEdit} />
      <PasswordDialog open={showPasswordDialog} hasPassword={auth.hasPassword} onClose={() => setShowPasswordDialog(false)} onSave={handleChangePassword} />
      <ApiKeyDialog data={generatedApiKey} variant="deployment" onClose={() => setGeneratedApiKey(null)} />
    </div>
  );
}
