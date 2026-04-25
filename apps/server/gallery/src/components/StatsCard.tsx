import { useEffect, useState } from "react";
import { Cpu, MemoryStick, HardDrive, Network } from "lucide-react";
import { authHeaders } from "../hooks/useAuth";
import type { DeploymentStats } from "../types";

interface StatsCardProps {
  deploymentId: string;
  apiKey: string;
}

const POLL_MS = 5000;

function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

function pct(part: number, total: number): number {
  if (total <= 0) return 0;
  return Math.min(100, Math.max(0, (part / total) * 100));
}

function Bar({ value, color = "bg-primary" }: { value: number; color?: string }) {
  return (
    <div className="h-2 w-full rounded-full bg-muted overflow-hidden">
      <div
        className={`h-full ${color} transition-all duration-500`}
        style={{ width: `${value}%` }}
      />
    </div>
  );
}

function StackedBar({ segments }: { segments: { value: number; color: string; label: string }[] }) {
  const total = segments.reduce((s, x) => s + x.value, 0);
  if (total <= 0) {
    return <div className="h-2 w-full rounded-full bg-muted" />;
  }
  return (
    <div className="h-2 w-full rounded-full bg-muted overflow-hidden flex">
      {segments.map((s, i) => (
        <div
          key={i}
          className={`h-full ${s.color} transition-all duration-500`}
          style={{ width: `${(s.value / total) * 100}%` }}
          title={`${s.label}: ${formatBytes(s.value)}`}
        />
      ))}
    </div>
  );
}

function Row({
  icon,
  label,
  detail,
  children,
}: {
  icon: React.ReactNode;
  label: string;
  detail?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground">{icon}</span>
          <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
            {label}
          </span>
        </div>
        {detail && <span className="text-xs text-muted-foreground tabular-nums">{detail}</span>}
      </div>
      {children}
    </div>
  );
}

export function StatsCard({ deploymentId, apiKey }: StatsCardProps) {
  const [stats, setStats] = useState<DeploymentStats | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const fetchStats = async () => {
      try {
        const res = await fetch(`/api/deployments/${deploymentId}/stats`, {
          headers: authHeaders(apiKey),
          credentials: "same-origin",
        });
        if (!res.ok) {
          if (!cancelled) setError(`Failed to load stats (${res.status})`);
          return;
        }
        const data = (await res.json()) as DeploymentStats;
        if (!cancelled) {
          setStats(data);
          setError(null);
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : "Failed to load stats");
      }
    };
    fetchStats();
    const id = setInterval(fetchStats, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [deploymentId, apiKey]);

  if (error && !stats) {
    return (
      <div className="rounded-lg border bg-muted/30 p-4 mb-6">
        <p className="text-sm text-muted-foreground">{error}</p>
      </div>
    );
  }
  if (!stats) {
    return (
      <div className="rounded-lg border bg-muted/30 p-4 mb-6">
        <p className="text-sm text-muted-foreground">Loading stats…</p>
      </div>
    );
  }

  const cpuLimitPct = stats.live.cpuLimitCores > 0 ? stats.live.cpuLimitCores * 100 : 0;
  const cpuRaw = Math.max(0, stats.live.cpuPercent);
  const cpuBarValue = cpuLimitPct > 0
    ? Math.min(100, (cpuRaw / cpuLimitPct) * 100)
    : Math.min(100, cpuRaw);
  const memPct = pct(stats.live.memoryBytes, stats.live.memoryLimitBytes);
  const netPct = stats.network.quotaBytes > 0 ? pct(stats.network.usedBytes, stats.network.quotaBytes) : 0;

  const storageSegments = [
    { value: stats.storage.sourceBytes, color: "bg-blue-500", label: "Source" },
    { value: stats.storage.persistBytes, color: "bg-emerald-500", label: "Persist" },
    { value: stats.storage.buildVolumeBytes, color: "bg-amber-500", label: "Build volume" },
  ];

  const recent = stats.network.recentPeriods ?? [];
  const maxRecent = recent.reduce((m, p) => (p.bytesOut > m ? p.bytesOut : m), 0);

  return (
    <div className="rounded-lg border bg-muted/30 p-4 mb-6 space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
          Resource Usage
        </h2>
        <span className="text-[10px] text-muted-foreground">refreshing every 5s</span>
      </div>

      <Row
        icon={<Cpu className="h-3.5 w-3.5" />}
        label="CPU"
        detail={
          cpuLimitPct > 0
            ? `${cpuRaw.toFixed(1)}% / ${cpuLimitPct.toFixed(0)}% (${stats.live.cpuLimitCores} ${stats.live.cpuLimitCores === 1 ? "core" : "cores"})`
            : `${cpuRaw.toFixed(1)}%`
        }
      >
        <Bar value={cpuBarValue} color={cpuBarValue > 80 ? "bg-red-500" : cpuBarValue > 50 ? "bg-amber-500" : "bg-primary"} />
      </Row>

      <Row
        icon={<MemoryStick className="h-3.5 w-3.5" />}
        label="Memory"
        detail={
          stats.live.memoryLimitBytes > 0
            ? `${formatBytes(stats.live.memoryBytes)} / ${formatBytes(stats.live.memoryLimitBytes)}`
            : formatBytes(stats.live.memoryBytes)
        }
      >
        <Bar value={memPct} color={memPct > 90 ? "bg-red-500" : memPct > 70 ? "bg-amber-500" : "bg-primary"} />
      </Row>

      <Row
        icon={<HardDrive className="h-3.5 w-3.5" />}
        label="Storage"
        detail={formatBytes(stats.storage.totalBytes)}
      >
        <StackedBar segments={storageSegments} />
        <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground mt-1">
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-blue-500" />
            Source {formatBytes(stats.storage.sourceBytes)}
          </span>
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-emerald-500" />
            Persist {formatBytes(stats.storage.persistBytes)}
          </span>
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-amber-500" />
            Build {formatBytes(stats.storage.buildVolumeBytes)}
          </span>
        </div>
      </Row>

      <Row
        icon={<Network className="h-3.5 w-3.5" />}
        label="Network (this period)"
        detail={
          stats.network.quotaBytes > 0
            ? `${formatBytes(stats.network.usedBytes)} / ${formatBytes(stats.network.quotaBytes)}`
            : formatBytes(stats.network.usedBytes)
        }
      >
        {stats.network.quotaBytes > 0 ? (
          <Bar value={netPct} color={netPct > 90 ? "bg-red-500" : netPct > 70 ? "bg-amber-500" : "bg-primary"} />
        ) : (
          <div className="h-2 w-full rounded-full bg-muted" />
        )}
        {recent.length > 0 && (
          <div className="mt-2">
            <div className="text-[11px] text-muted-foreground mb-1">
              Recent periods ({stats.network.periodResetIntervalH}h each)
            </div>
            <div className="flex items-end gap-1 h-8">
              {[...recent].reverse().map((p, i) => {
                const h = maxRecent > 0 ? Math.max(2, (p.bytesOut / maxRecent) * 100) : 2;
                return (
                  <div
                    key={i}
                    className="flex-1 bg-primary/40 rounded-sm hover:bg-primary/70 transition-colors"
                    style={{ height: `${h}%` }}
                    title={`${p.periodStart}: ${formatBytes(p.bytesOut)}`}
                  />
                );
              })}
            </div>
          </div>
        )}
      </Row>

      {stats.live.pids > 0 && (
        <div className="text-[11px] text-muted-foreground pt-1 border-t">
          {stats.live.pids} process{stats.live.pids === 1 ? "" : "es"} running
        </div>
      )}
    </div>
  );
}
