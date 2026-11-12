import type { AdminJob } from '../types';
import { request } from './client';

export async function fetchAdminJob(id: string): Promise<AdminJob> {
  return request<AdminJob>(`/api/jobs/${encodeURIComponent(id)}`);
}

export async function waitForAdminJob(id: string, signal?: AbortSignal): Promise<AdminJob> {
  while (true) {
    if (signal?.aborted) throw new DOMException('任务视图已关闭', 'AbortError');
    const job = await fetchAdminJob(id);
    if (job.status !== 'RUNNING') return job;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
}
