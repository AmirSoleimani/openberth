export interface GalleryItem {
  id: string;
  name: string;
  title: string;
  description: string;
  subdomain: string;
  framework: string;
  url: string;
  createdAt: string;
  expiresAt: string;
  ttlHours: number;
  ownerId: string;
  ownerName: string;
  accessMode: string;
  accessUser?: string;
  accessUsers?: string;
  mode: string;
  networkQuota?: string;
  locked: boolean;
  status: string;
}

export interface DeploymentsResponse {
  deployments: GalleryItem[];
  count: number;
}

export interface MeResponse {
  id: string;
  name: string;
  displayName: string;
  role: "admin" | "user";
  hasPassword: boolean;
  serverVersion: string;
}

export interface DeploymentStats {
  id: string;
  status: string;
  live: {
    cpuPercent: number;
    memoryBytes: number;
    memoryLimitBytes: number;
    pids: number;
    buildVolumeBytes: number;
  };
  storage: {
    sourceBytes: number;
    persistBytes: number;
    buildVolumeBytes: number;
    totalBytes: number;
  };
  network: {
    usedBytes: number;
    quotaBytes: number;
    remainingBytes: number;
    periodStart: string;
    periodResetIntervalH: number;
    recentPeriods?: { periodStart: string; bytesOut: number }[];
  };
}

export interface HostStats {
  cpuPercent: number;
  cpuCores: number;
  memoryTotal: number;
  memoryUsed: number;
  memoryFree: number;
  diskTotal: number;
  diskUsed: number;
  diskFree: number;
  loadAvg1: number;
  loadAvg5: number;
  loadAvg15: number;
  uptimeSeconds: number;
  sampledAt: string;
}
