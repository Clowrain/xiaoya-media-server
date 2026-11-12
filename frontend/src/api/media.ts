import { MediaItem, LibraryView } from '../types';
import { request, requestFile } from './client';

export function getPrimaryImageUrl(itemId: string, query: string): string {
  return `/Items/${itemId}/Images/Primary?${query}`;
}

export function getBackdropImageUrl(itemId: string, query: string, index?: number): string {
  const imagePath = index === undefined ? 'Backdrop' : `Backdrop/${index}`;
  return `/Items/${itemId}/Images/${imagePath}?${query}`;
}

export interface MediaItemsResponse {
  Items: MediaItem[];
  TotalRecordCount: number;
}

export async function fetchLibraryViews(userId: string, signal?: AbortSignal): Promise<LibraryView[]> {
  const data = await request<{ Items?: LibraryView[] }>(`/emby/Users/${userId}/Views`, 'GET', undefined, signal);
  return data.Items || [];
}

export function fetchMediaItems(params: URLSearchParams, signal?: AbortSignal): Promise<MediaItemsResponse> {
  return request(`/emby/Items?${params.toString()}`, 'GET', undefined, signal);
}

export function fetchItemDetail(userId: string, itemId: string): Promise<MediaItem> {
  return request(`/emby/Users/${userId}/Items/${itemId}`);
}

export async function fetchResumeItems(userId: string): Promise<MediaItem[]> {
  try {
    const data = await request<{ Items?: MediaItem[] }>(`/emby/Users/${userId}/Items/Resume?Limit=12&Recursive=true&Fields=Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources`);
    return data.Items || [];
  } catch {
    return [];
  }
}

export async function fetchNextUpItems(userId: string, signal?: AbortSignal): Promise<MediaItem[]> {
  try {
    const data = await request<{ Items?: MediaItem[] }>(`/emby/Shows/NextUp?UserId=${userId}&Limit=12&Fields=Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources`, 'GET', undefined, signal);
    return data.Items || [];
  } catch {
    return [];
  }
}

export async function fetchLatestItems(userId: string, parentId?: string, signal?: AbortSignal): Promise<MediaItem[]> {
  try {
    const url = `/emby/Users/${userId}/Items/Latest?Limit=16&Fields=Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources,ChildCount,RecursiveItemCount,EpisodeCount${parentId ? `&ParentId=${encodeURIComponent(parentId)}` : ''}`;
    const data = await request<MediaItem[] | { Items?: MediaItem[] }>(url, 'GET', undefined, signal);
    if (Array.isArray(data)) return data;
    return data.Items || [];
  } catch {
    return [];
  }
}

export async function fetchSeasons(userId: string, seriesId: string): Promise<MediaItem[]> {
  const data = await request<{ Items?: MediaItem[] }>(`/emby/Users/${userId}/Shows/${encodeURIComponent(seriesId)}/Seasons`);
  return data.Items || [];
}

export async function fetchEpisodes(userId: string, seriesId: string, seasonId?: string): Promise<MediaItem[]> {
  const qs = new URLSearchParams({
    Fields: 'Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources,MediaStreams',
  });
  if (seasonId) qs.set('SeasonId', seasonId);
  const data = await request<{ Items?: MediaItem[] }>(`/emby/Users/${userId}/Shows/${encodeURIComponent(seriesId)}/Episodes?${qs.toString()}`);
  return data.Items || [];
}

export async function refreshItemMediaInfo(itemId: string): Promise<void> {
  await request(`/emby/Items/${itemId}/RefreshMediaInfo`, 'POST', {});
}

export async function renameMediaItem(id: string, name: string): Promise<void> {
  await request('/admin/media-item', 'PUT', { ID: id, Name: name });
}

export async function deleteMediaItem(id: string): Promise<void> {
  await request('/admin/media-item', 'DELETE', { ID: id, DeleteDirectory: false });
}

export async function uploadItemCover(id: string, file: File): Promise<void> {
  await requestFile(`/Items/${encodeURIComponent(id)}/Images/Primary`, 'POST', file, file.type || 'image/jpeg', '上传封面失败');
}

export async function deleteItemCover(id: string): Promise<void> {
  await requestFile(`/Items/${encodeURIComponent(id)}/Images/Primary`, 'DELETE', undefined, undefined, '清除自定义封面失败');
}
