import { useState, useRef, useEffect } from "react";
import { ArrowLeft, ExternalLink, Trash2, Pencil, Lock, LockOpen, ShieldCheck, MoreVertical } from "lucide-react";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { AppIcon } from "./AppIcon";
import { StatusDot } from "./StatusDot";
import { formatAge, formatExpiry } from "../lib/format";
import type { GalleryItem } from "../types";

interface AppDetailViewProps {
  item: GalleryItem | undefined;
  isOwned: boolean;
  onBack: () => void;
  onNavigateUser: (userId: string) => void;
  onToggleLock: (item: GalleryItem) => void;
  onEdit: (item: GalleryItem) => void;
  onDestroy: (item: GalleryItem) => void;
}

function ActionsDropdown({ item, notRunning, onToggleLock, onEdit, onDestroy }: {
  item: GalleryItem;
  notRunning: boolean;
  onToggleLock: (item: GalleryItem) => void;
  onEdit: (item: GalleryItem) => void;
  onDestroy: (item: GalleryItem) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <Button variant="outline" size="sm" className="h-9 w-9 p-0" onClick={() => setOpen(!open)}>
        <MoreVertical className="h-4 w-4" />
      </Button>
      {open && (
        <div className="absolute left-0 top-full mt-1 z-50 min-w-[140px] rounded-md border bg-popover p-1 shadow-md">
          <button
            className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent disabled:opacity-50 disabled:pointer-events-none"
            onClick={() => { onToggleLock(item); setOpen(false); }}
            disabled={notRunning}
          >
            {item.locked ? <LockOpen className="h-3.5 w-3.5" /> : <ShieldCheck className="h-3.5 w-3.5" />}
            {item.locked ? "Unlock" : "Lock"}
          </button>
          <button
            className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent disabled:opacity-50 disabled:pointer-events-none"
            onClick={() => { onEdit(item); setOpen(false); }}
            disabled={item.locked || notRunning}
          >
            <Pencil className="h-3.5 w-3.5" />
            Edit
          </button>
          <button
            className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-sm text-destructive hover:bg-accent disabled:opacity-50 disabled:pointer-events-none"
            onClick={() => { onDestroy(item); setOpen(false); }}
            disabled={item.locked || notRunning}
          >
            <Trash2 className="h-3.5 w-3.5" />
            Destroy
          </button>
        </div>
      )}
    </div>
  );
}

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[11px] text-muted-foreground uppercase tracking-wider">{label}</span>
      <span className="text-sm">{children}</span>
    </div>
  );
}

export function AppDetailView({ item, isOwned, onBack, onNavigateUser, onToggleLock, onEdit, onDestroy }: AppDetailViewProps) {
  if (!item) {
    return (
      <div>
        <button onClick={onBack} className="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors">
          <ArrowLeft className="h-4 w-4" />Back to gallery
        </button>
        <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
          <p className="text-sm">Deployment not found.</p>
        </div>
      </div>
    );
  }

  const notRunning = item.status !== "running";
  const expiry = formatExpiry(item.expiresAt);

  return (
    <div>
      <button onClick={onBack} className="mb-6 inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors">
        <ArrowLeft className="h-4 w-4" />Back to gallery
      </button>

      {/* Header */}
      <div className="flex items-start gap-4 mb-6">
        <AppIcon title={item.title || item.name} framework={item.framework} size="lg" />
        <div className="min-w-0 flex-1">
          <h1 className="text-xl font-bold break-all">{item.title || item.name}</h1>
          <div className="flex items-center gap-2 mt-1">
            <StatusDot status={item.status} />
            <button onClick={() => onNavigateUser(item.ownerId)} className="text-xs text-muted-foreground hover:text-foreground hover:underline transition-colors">
              {item.ownerName}
            </button>
          </div>
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <Badge variant="secondary" className="text-[10px]">{item.framework}</Badge>
            {item.mode === "sandbox" && (
              <Badge variant="outline" className="text-[10px] border-amber-500 text-amber-600">Sandbox</Badge>
            )}
            {item.locked && (
              <Badge variant="outline" className="text-[10px] border-orange-500 text-orange-600">Locked</Badge>
            )}
            {item.accessMode && item.accessMode !== "public" && (
              <Badge variant="outline" className="text-[10px]">
                <Lock className="h-3 w-3 mr-1" />
                {item.accessMode.replace("_", " ")}
              </Badge>
            )}
            {item.networkQuota && (
              <Badge variant="outline" className="text-[10px]">{item.networkQuota} quota</Badge>
            )}
          </div>
        </div>
      </div>

      {/* Description */}
      {item.description && (
        <p className="text-sm text-muted-foreground mb-6">{item.description}</p>
      )}

      {/* Info grid */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-4 mb-6 p-4 rounded-lg border bg-muted/30">
        <InfoRow label="Owner">
          <button onClick={() => onNavigateUser(item.ownerId)} className="text-primary hover:underline">
            {item.ownerName}
          </button>
        </InfoRow>
        <InfoRow label="Created">{formatAge(item.createdAt)}</InfoRow>
        <InfoRow label="Status">{item.status}</InfoRow>
        <InfoRow label="Subdomain">{item.subdomain}</InfoRow>
        {item.ttlHours > 0 && (
          <InfoRow label="TTL">{item.ttlHours}h</InfoRow>
        )}
        {expiry && (
          <InfoRow label="Expires">{expiry}</InfoRow>
        )}
        {item.accessUsers && (
          <InfoRow label="Allowed Users">{item.accessUsers}</InfoRow>
        )}
      </div>

      {/* Actions */}
      <div className="flex items-center gap-2">
        <a
          href={item.url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center justify-center h-9 rounded-md px-3 text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
        >
          <ExternalLink className="h-3.5 w-3.5 mr-1.5" />Visit
        </a>
        {isOwned && (
          <ActionsDropdown
            item={item}
            notRunning={notRunning}
            onToggleLock={onToggleLock}
            onEdit={onEdit}
            onDestroy={onDestroy}
          />
        )}
      </div>
    </div>
  );
}
