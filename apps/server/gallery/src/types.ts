export interface GalleryItem {
  id: string;
  name: string;
  title: string;
  description: string;
  subdomain: string;
  framework: string;
  url: string;
  createdAt: string;
  ownerName: string;
  userId: string;
  accessMode: string;
  accessUser: string;
  accessUsers: string;
  ttlHours: number;
  expiresAt: string;
  mode: string;
  networkQuota: string;
  locked: boolean;
  status: string;
}

export interface GalleryResponse {
  deployments: GalleryItem[];
  userId?: string;
  userRole?: string;
  userName?: string;
  hasPassword?: boolean;
}
