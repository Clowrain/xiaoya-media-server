import { request } from './client';

export interface ManifestSearchEntry {
  title?: string;
  rel_path: string;
  media_type: string;
  episode_count?: number;
  series_root_path?: string;
}

export async function searchManifest(query: string, limit = 100): Promise<ManifestSearchEntry[]> {
  const params = new URLSearchParams({ query: query === '/' ? '' : query, limit: String(limit) });
  const result = await request<{ items?: ManifestSearchEntry[] }>(`/api/xiaoya/manifest/search?${params.toString()}`);
  return result.items ?? [];
}
