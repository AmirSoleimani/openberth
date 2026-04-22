import { useCallback, useEffect, useState } from "react";
import type { GalleryItem, DeploymentsResponse, MeResponse } from "../types";

interface UseGalleryOptions {
  apiKey: string;
  onAuthData: (data: MeResponse) => void;
}

export function useGallery({ apiKey, onAuthData }: UseGalleryOptions) {
  const [items, setItems] = useState<GalleryItem[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchGallery = useCallback(async () => {
    const headers: Record<string, string> = {};
    if (apiKey) headers["X-API-Key"] = apiKey;
    try {
      // /api/gallery is gone — we now fetch the bootstrap user context
      // separately from the deployments list. Run in parallel.
      const [meRes, depsRes] = await Promise.all([
        fetch("/api/me", { headers, credentials: "same-origin" }),
        fetch("/api/deployments", { headers, credentials: "same-origin" }),
      ]);
      if (meRes.status === 401 || depsRes.status === 401) {
        if (!import.meta.env.DEV) {
          window.location.href = "/login?redirect=/gallery/";
        }
        throw new Error("unauthorized");
      }
      const me: MeResponse = await meRes.json();
      const deps: DeploymentsResponse = await depsRes.json();
      setItems(deps.deployments || []);
      onAuthData(me);
    } catch {
      if (import.meta.env.DEV) {
        const { mockGalleryResponse } = await import("../lib/mockData");
        setItems(mockGalleryResponse.deployments);
        onAuthData({
          id: mockGalleryResponse.userId || "",
          name: mockGalleryResponse.userName || "",
          displayName: mockGalleryResponse.userName || "",
          role: (mockGalleryResponse.userRole as "admin" | "user") || "user",
          hasPassword: mockGalleryResponse.hasPassword || false,
          serverVersion: mockGalleryResponse.serverVersion || "",
        });
      }
    } finally {
      setLoading(false);
    }
  }, [apiKey, onAuthData]);

  useEffect(() => {
    fetchGallery();
  }, [fetchGallery]);

  return { items, loading, fetchGallery };
}
