import type { AdminJob, SyncBatch } from '../types';
import { request } from './client';

export interface ManagedResource {
  id: string;
  source_path: string;
  status: string;
  origin_type: string;
  local_strm_path?: string;
  last_error?: string;
  is_dir: boolean;
}

export interface ManagedResourceDetail {
  resource: ManagedResource;
  cleanup_preview: { unmanaged: boolean; affected_resources: number };
}

export interface ResourceChange {
  id: string;
  change_type: string;
  old_path?: string;
  new_path?: string;
  detected_at: number;
}

export interface ResourceTask {
  task_key: string;
  name: string;
  status: string;
  last_run_at: number;
  last_error?: string;
  enabled: boolean;
}

export interface TrackingReview {
  resource_id: string;
  path: string;
}

export interface ResourcePage {
  items: ManagedResource[];
  total: number;
}

export type ResourceAction = 'unmanage' | 'restore-management' | 'reprocess' | 'retry' | 'track';

export async function fetchResources(query: {
  q: string;
  status: string;
  limit: number;
  offset: number;
}): Promise<ResourcePage> {
  const params = new URLSearchParams({
    q: query.q,
    status: query.status,
    limit: String(query.limit),
    offset: String(query.offset),
  });
  return request<ResourcePage>(`/api/resources?${params.toString()}`);
}

export async function fetchTrackingReviews(): Promise<TrackingReview[]> {
  const result = await request<{ items: TrackingReview[] }>('/api/alist/tracking-review');
  return result.items;
}

export async function fetchResourceChanges(limit = 100): Promise<ResourceChange[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  const result = await request<{ items: ResourceChange[] }>(`/api/resources/changes?${params.toString()}`);
  return result.items;
}

export async function fetchResourceBatches(limit = 100): Promise<SyncBatch[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  const result = await request<{ items: SyncBatch[] }>(`/api/batches?${params.toString()}`);
  return result.items;
}

export async function fetchResourceTasks(): Promise<ResourceTask[]> {
  const result = await request<{ items: ResourceTask[] }>('/api/tasks');
  return result.items;
}

export async function fetchResourceDetail(id: string): Promise<ManagedResourceDetail> {
  return request<ManagedResourceDetail>(`/api/resources/${encodeURIComponent(id)}`);
}

export async function performResourceAction(id: string, action: ResourceAction): Promise<void> {
  const confirmation = action === 'unmanage' ? '?confirm=true' : '';
  await request(`/api/resources/${encodeURIComponent(id)}/${action}${confirmation}`, 'POST', {});
}

export async function runResourceTask(key: string): Promise<AdminJob> {
  return request<AdminJob>(`/api/tasks/${encodeURIComponent(key)}/run`, 'POST', {});
}
