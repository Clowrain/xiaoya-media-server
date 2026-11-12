import { TmdbConfig, TmdbSearchResult } from '../types';
import { request } from './client';

export async function fetchTmdbConfig(): Promise<TmdbConfig> {
  return request('/admin/tmdb');
}

export async function updateTmdbConfig(cfg: TmdbConfig): Promise<void> {
  await request('/admin/tmdb', 'PUT', cfg);
}

export async function testTmdb(): Promise<any> {
  return request('/admin/tmdb/test', 'POST', {});
}

export async function searchTmdb(q: string, kind: string, year?: string, signal?: AbortSignal): Promise<TmdbSearchResult[]> {
  const params: Record<string, string> = { q, kind };
  if (year) params.year = year;
  const data = await request(`/admin/tmdb/search?${new URLSearchParams(params)}`, 'GET', undefined, signal);
  return Array.isArray(data) ? data : (data.Items || data.items || data.results || []);
}

export async function matchTmdb(itemId: string, tmdbId: number | string, kind: string): Promise<void> {
  await request('/admin/tmdb/match', 'POST', {
    ItemId: itemId,
    TmdbId: String(tmdbId),
    MediaType: kind,
  });
}
