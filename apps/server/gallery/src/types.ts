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
