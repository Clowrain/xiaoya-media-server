import { request } from './client';

export async function fetchEnhancements(): Promise<any> {
  return request('/admin/enhancements');
}

export async function updateEnhancements(data: any): Promise<void> {
  await request('/admin/enhancements', 'PUT', data);
}

export async function fetchMediaInfoSettings(): Promise<any> {
  return request('/admin/media-info');
}

export async function updateMediaInfoSettings(data: any): Promise<void> {
  await request('/admin/media-info', 'PUT', data);
}
