import { useEffect, useState } from "react";
import { Cpu, MemoryStick, HardDrive, Activity, Clock } from "lucide-react";
import { authHeaders } from "../hooks/useAuth";
import type { HostStats } from "../types";

interface HostStatsViewProps {
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

function formatUptime(s: number): string {
  if (s <= 0) return "—";
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function Gauge({
  icon,
  label,
  used,
  total,
  detail,
}: {
  icon: React.ReactNode;
  label: string;
  used: number;
  total: number;
  detail?: string;
}) {
  const value = pct(used, total);
  const color = value > 90 ? "bg-red-500" : value > 75 ? "bg-amber-500" : "bg-primary";
  return (
    <div className="rounded-lg border bg-muted/30 p-4 space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground">{icon}</span>
          <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
            {label}
          </span>
        </div>
        <span className="text-xs text-muted-foreground tabular-nums">{value.toFixed(1)}%</span>
      </div>
      <div className="h-2 w-full rounded-full bg-muted overflow-hidden">
        <div className={`h-full ${color} transition-all duration-500`} style={{ width: `${value}%` }} />
      </div>
      {detail && <p className="text-xs text-muted-foreground tabular-nums">{detail}</p>}
    </div>
  );
}

function CpuGauge({ value, cores }: { value: number; cores: number }) {
  const v = Math.min(100, Math.max(0, value));
  const color = v > 90 ? "bg-red-500" : v > 75 ? "bg-amber-500" : "bg-primary";
  return (
    <div className="rounded-lg border bg-muted/30 p-4 space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">CPU</span>
        </div>
        <span className="text-xs text-muted-foreground tabular-nums">{v.toFixed(1)}%</span>
      </div>
      <div className="h-2 w-full rounded-full bg-muted overflow-hidden">
        <div className={`h-full ${color} transition-all duration-500`} style={{ width: `${v}%` }} />
      </div>
      <p className="text-xs text-muted-foreground">{cores} {cores === 1 ? "core" : "cores"}</p>
    </div>
  );
}

export function HostStatsView({ apiKey }: HostStatsViewProps) {
  const [stats, setStats] = useState<HostStats | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const fetchStats = async () => {
      try {
        const res = await fetch("/api/admin/host-stats", {
          headers: authHeaders(apiKey),
          credentials: "same-origin",
        });
        if (!res.ok) {
          if (!cancelled) {
            const body = await res.json().catch(() => ({} as { error?: string }));
            setError(body.error || `Failed to load host stats (HTTP ${res.status})`);
          }
          return;
        }
        const data = (await res.json()) as HostStats;
        if (!cancelled) {
          setStats(data);
          setError(null);
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : "Failed to load host stats");
      }
    };
    fetchStats();
    const id = setInterval(fetchStats, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [apiKey]);

  if (error && !stats) {
    return (
      <div className="rounded-md border bg-muted/50 p-4 text-sm text-muted-foreground">{error}</div>
    );
  }
  if (!stats) {
    return (
      <div className="rounded-md border bg-muted/50 p-4 text-sm text-muted-foreground">
        Loading host stats…
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="rounded-md border bg-muted/50 p-4 text-sm space-y-1">
        <p className="font-medium">Host Resources</p>
        <p className="text-muted-foreground">
          Live snapshot of the server hosting OpenBerth. Refreshes every 5s.
          Linux-only — disk usage is for the partition holding deployment data.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <CpuGauge value={stats.cpuPercent} cores={stats.cpuCores} />
        <Gauge
          icon={<MemoryStick className="h-3.5 w-3.5" />}
          label="Memory"
          used={stats.memoryUsed}
          total={stats.memoryTotal}
          detail={`${formatBytes(stats.memoryUsed)} / ${formatBytes(stats.memoryTotal)} (${formatBytes(stats.memoryFree)} free)`}
        />
        <Gauge
          icon={<HardDrive className="h-3.5 w-3.5" />}
          label="Disk"
          used={stats.diskUsed}
          total={stats.diskTotal}
          detail={`${formatBytes(stats.diskUsed)} / ${formatBytes(stats.diskTotal)} (${formatBytes(stats.diskFree)} free)`}
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="rounded-lg border bg-muted/30 p-4 space-y-2">
          <div className="flex items-center gap-2">
            <Activity className="h-3.5 w-3.5 text-muted-foreground" />
            <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">Load Average</span>
          </div>
          <div className="grid grid-cols-3 gap-3 pt-1">
            <div>
              <div className="text-xs text-muted-foreground">1 min</div>
              <div className="text-lg tabular-nums">{stats.loadAvg1.toFixed(2)}</div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">5 min</div>
              <div className="text-lg tabular-nums">{stats.loadAvg5.toFixed(2)}</div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">15 min</div>
              <div className="text-lg tabular-nums">{stats.loadAvg15.toFixed(2)}</div>
            </div>
          </div>
          <p className="text-[11px] text-muted-foreground pt-1">
            Per-core load: {stats.cpuCores > 0 ? (stats.loadAvg1 / stats.cpuCores).toFixed(2) : "—"} (1m)
          </p>
        </div>

        <div className="rounded-lg border bg-muted/30 p-4 space-y-2">
          <div className="flex items-center gap-2">
            <Clock className="h-3.5 w-3.5 text-muted-foreground" />
            <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">Uptime</span>
          </div>
          <div className="text-lg tabular-nums">{formatUptime(stats.uptimeSeconds)}</div>
          <p className="text-[11px] text-muted-foreground">
            Sampled at {new Date(stats.sampledAt).toLocaleTimeString()}
          </p>
        </div>
      </div>
    </div>
  );
}
