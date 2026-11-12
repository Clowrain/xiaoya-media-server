import { AListBrowseResult, AdminJob, MonitorRoot, RecognitionPending, SystemOverview } from '../types';
import { request } from './client';

export async function fetchSystemOverview(): Promise<SystemOverview> {
  return request('/api/system/overview');
}

export async function fetchMonitorRoots(): Promise<MonitorRoot[]> {
  const data = await request('/api/monitor/roots');
  if (Array.isArray(data)) return data;
  if (data && Array.isArray(data.items)) return data.items;
  if (data && Array.isArray(data.roots)) return data.roots;
  return [];
}

export async function addMonitorRoot(rootPath: string, intervalSec: number, enabled: boolean): Promise<void> {
  await request('/api/monitor/roots', 'POST', {
    root_path: rootPath,
    interval_sec: intervalSec,
    enabled,
  });
}

export async function updateMonitorRoot(id: string, enabled: boolean, intervalSec: number): Promise<void> {
  await request(`/api/monitor/roots/${encodeURIComponent(id)}`, 'PUT', {
    enabled,
    interval_sec: intervalSec,
  });
}

export async function syncSingleMonitorRoot(id: string): Promise<any> {
  return request(`/api/monitor/roots/${encodeURIComponent(id)}/sync`, 'POST');
}

export async function deleteMonitorRoot(id: string): Promise<void> {
  await request(`/api/monitor/roots/${encodeURIComponent(id)}`, 'DELETE');
}

export async function triggerSync(id: string, full: boolean) {
  return request<AdminJob>(full ? '/api/sync/full?async=true' : '/api/sync/incremental?async=true', 'POST', { root_id: id });
}

export async function importResource(path: string, isDir: boolean, trackUpdates: boolean, libraryID: string, outputDir: string, source: string = 'manifest') {
  return request<AdminJob>('/api/resources/import?async=true', 'POST', {
    path,
    is_dir: isDir,
    track_updates: trackUpdates,
    library_id: libraryID,
    output_dir: outputDir,
    source,
  });
}

export async function browseAList(path: string, page: number = 1, perPage: number = 25, source: 'sqlite' | 'alist' = 'sqlite'): Promise<AListBrowseResult> {
  const qs = new URLSearchParams({ path, page: String(page), per_page: String(perPage), source });
  return request(`/api/alist/browse?${qs.toString()}`);
}

export async function searchAList(keyword: string, path: string = '/', page: number = 1, perPage: number = 25): Promise<AListBrowseResult> {
  const qs = new URLSearchParams({
    keywords: keyword,
    keyword,
    q: keyword,
    parent: path,
    path,
    page: String(page),
    per_page: String(perPage),
  });
  return request(`/api/alist/search?${qs.toString()}`);
}

export async function fetchRecognitionPending(limit: number = 100): Promise<RecognitionPending[]> {
  const data = await request<{ items?: RecognitionPending[] }>(`/api/recognition/pending?limit=${limit}`);
  return data.items || [];
}

export async function confirmRecognition(resourceID: string, externalID: number | string): Promise<void> {
  await request(`/api/recognition/${encodeURIComponent(resourceID)}/confirm`, 'POST', {
    external_id: String(externalID),
  });
}

export async function fetchAdminJobs() {
  return (await request<{items: AdminJob[]}>('/api/jobs')).items;
}
