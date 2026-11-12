import { AdminLibrary } from '../types';
import { request } from './client';

export interface LibraryDirectoryItem {
  Name?: string;
  name?: string;
  Path?: string;
  path?: string;
}

export interface LibraryDirectoriesResponse {
  Path: string;
  Directories: Array<string | LibraryDirectoryItem>;
}

export interface ScanSchedule {
  Enabled: boolean;
  Frequency: string;
  Time: string;
  Minutes: number;
}

export async function fetchAdminLibraries(): Promise<AdminLibrary[]> {
  const data = await request<AdminLibrary[] | { Libraries?: AdminLibrary[] }>('/admin/libraries');
  return Array.isArray(data) ? data : (data.Libraries || []);
}

export async function createAdminLibrary(name: string, kind: string, paths: string[]): Promise<void> {
  await request('/admin/libraries', 'POST', { Name: name, Kind: kind, Paths: paths });
}

export async function updateAdminLibrary(id: string, name: string, kind: string, paths?: string[]): Promise<void> {
  const payload: { ID: string; Name: string; Kind: string; Paths?: string[] } = { ID: id, Name: name, Kind: kind };
  if (paths && paths.length > 0) payload.Paths = paths;
  await request('/admin/libraries', 'PUT', payload);
}

export async function deleteAdminLibrary(id: string): Promise<void> {
  await request(`/admin/libraries?id=${encodeURIComponent(id)}`, 'DELETE', { ID: id });
}

export async function addLibraryFolder(id: string, path: string): Promise<void> {
  await request('/admin/library-folders', 'POST', { ID: id, Path: path });
}

export async function removeLibraryFolder(id: string, path: string): Promise<void> {
  await request('/admin/library-folders', 'DELETE', { ID: id, Path: path });
}

export async function scanLibrary(id: string, mode: string = 'scan'): Promise<void> {
  await request('/admin/scan', 'POST', { ID: id, Mode: mode });
}

export async function scanAllLibraries(): Promise<void> {
  await request('/admin/scan-all', 'POST', {});
}

export async function generateLibraryCover(id: string): Promise<void> {
  await request(`/admin/cover?id=${encodeURIComponent(id)}&action=generate`, 'POST');
}

export function fetchDirectories(basePath: string = '/media'): Promise<LibraryDirectoriesResponse> {
  return request(`/admin/directories?${new URLSearchParams({ Path: basePath })}`);
}

export function fetchScanSchedule(): Promise<ScanSchedule> {
  return request('/admin/scan-schedule');
}

export async function updateScanSchedule(schedule: ScanSchedule): Promise<void> {
  await request('/admin/scan-schedule', 'PUT', schedule);
}
