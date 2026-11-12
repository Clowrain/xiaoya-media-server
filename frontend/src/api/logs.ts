import type { LogEntry } from '../types';
import { request } from './client';

export interface LogsResponse {
  logs: (string | LogEntry)[];
  total?: number;
  [key: string]: unknown;
}

export async function fetchLogs(
  limit: number = 200,
  level?: string,
  source?: string,
  q?: string,
  order: string = 'asc',
  signal?: AbortSignal
): Promise<LogsResponse> {
  const params: Record<string, string> = { limit: String(limit), order };
  if (level) params.level = level;
  if (source) {
    params.source = source;
    params.category = source;
  }
  if (q) params.q = q;
  const data = await request(`/admin/logs?${new URLSearchParams(params)}`, 'GET', undefined, signal);
  const rawList = data?.logs || data?.Items || data?.Entries || (Array.isArray(data) ? data : []);
  return {
    ...data,
    logs: rawList,
  };
}

export async function clearLogs(): Promise<void> {
  await request('/admin/logs', 'DELETE');
}
