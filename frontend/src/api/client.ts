import { 
  User, LibraryView, MediaItem, AdminLibrary, SystemOverview, 
  MonitorRoot, AListBrowseResult, AdminJob, TmdbConfig, TmdbSearchResult, RecognitionPending, LogEntry
} from '../types';

const TOKEN_KEY = 'xiaoya_token';
const USER_KEY = 'xiaoya_user';
const DEVICE_KEY = 'xiaoya_device_id';

function getDeviceId(): string {
  let id = localStorage.getItem(DEVICE_KEY);
  if (!id) {
    id = 'dev_' + Math.random().toString(36).slice(2, 10);
    localStorage.setItem(DEVICE_KEY, id);
  }
  return id;
}

export function getStoredUser(): User | null {
  const u = localStorage.getItem(USER_KEY);
  if (!u) return null;
  try {
    return JSON.parse(u);
  } catch {
    return null;
  }
}

export function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function fetchCurrentUser(userId: string): Promise<User> {
  return request<User>(`/emby/Users/${encodeURIComponent(userId)}`);
}

export function setAuthSession(user: User, token: string) {
  localStorage.setItem(USER_KEY, JSON.stringify(user));
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearAuthSession() {
  localStorage.removeItem(USER_KEY);
  localStorage.removeItem(TOKEN_KEY);
}

// Toast emitter
type ToastListener = (msg: string, type?: 'info' | 'success' | 'error' | 'warning') => void;
const toastListeners: Set<ToastListener> = new Set();

export function onToast(fn: ToastListener) {
  toastListeners.add(fn);
  return () => toastListeners.delete(fn);
}

export function notifyToast(msg: string, type: 'info' | 'success' | 'error' | 'warning' = 'info') {
  toastListeners.forEach(fn => fn(msg, type));
}

// Global API fetcher
export async function request<T = any>(
  path: string, 
  method: string = 'GET', 
  body?: any
): Promise<T> {
  const token = getStoredToken() || '';
  const device = getDeviceId();

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-Emby-Token': token,
    'X-Emby-Authorization': `Emby Client="Xiaoya Web React", Device="Browser", DeviceId="${device}", Version="2.0"`,
  };

  const url = path.startsWith('http') ? path : (path.startsWith('/') ? path : `/${path}`);

  const res = await fetch(url, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 401) {
    // If unauthorized, notify and redirect to login if needed
    clearAuthSession();
    window.dispatchEvent(new CustomEvent('auth:expired'));
    throw new Error('登录会话已过期，请重新登录');
  }

  const text = await res.text();
  let json: any = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = text;
  }

  if (!res.ok) {
    const errMsg = (json && (json.message || json.error || json.Message)) || text || `请求失败 (${res.status})`;
    throw new Error(errMsg);
  }

  return json as T;
}

// Auth API
export async function loginApi(username: string, password: string):Promise<{ User: User; AccessToken: string }> {
  const res = await request('/emby/Users/AuthenticateByName', 'POST', {
    Username: username,
    Pw: password,
  });
  setAuthSession(res.User, res.AccessToken);
  return res;
}

export async function logoutApi(): Promise<void> {
  try {
    await request('/Sessions/Logout', 'POST', {});
  } finally {
    clearAuthSession();
  }
}

export async function changePasswordApi(userId: string, oldPw: string, newPw: string): Promise<void> {
  await request(`/Users/${userId}/Password`, 'POST', {
    CurrentPw: oldPw,
    NewPw: newPw,
  });
}

// Media Wall & SenPlayer API
export async function fetchLibraryViews(userId: string): Promise<LibraryView[]> {
  const data = await request(`/emby/Users/${userId}/Views`);
  return data.Items || [];
}

export async function fetchMediaItems(params: URLSearchParams): Promise<{ Items: MediaItem[]; TotalRecordCount: number }> {
  return request(`/emby/Items?${params.toString()}`);
}

export async function fetchItemDetail(userId: string, itemId: string): Promise<MediaItem> {
  return request(`/emby/Users/${userId}/Items/${itemId}`);
}

export async function fetchResumeItems(userId: string): Promise<MediaItem[]> {
  try {
    const data = await request(`/emby/Users/${userId}/Items/Resume?Limit=12&Recursive=true&Fields=Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources`);
    return data.Items || [];
  } catch {
    return [];
  }
}

export async function fetchNextUpItems(userId: string): Promise<MediaItem[]> {
  try {
    const data = await request(`/emby/Shows/NextUp?UserId=${userId}&Limit=12&Fields=Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources`);
    return data.Items || [];
  } catch {
    return [];
  }
}

export async function fetchLatestItems(userId: string, parentId?: string): Promise<MediaItem[]> {
  try {
    const url = `/emby/Users/${userId}/Items/Latest?Limit=16&Fields=Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources${parentId ? `&ParentId=${encodeURIComponent(parentId)}` : ''}`;
    const data = await request(url);
    if (Array.isArray(data)) return data;
    return data.Items || [];
  } catch {
    return [];
  }
}

export async function fetchSeasons(userId: string, seriesId: string): Promise<MediaItem[]> {
  const data = await request(`/emby/Users/${userId}/Shows/${encodeURIComponent(seriesId)}/Seasons`);
  return data.Items || [];
}

export async function fetchEpisodes(userId: string, seriesId: string, seasonId?: string): Promise<MediaItem[]> {
  const qs = new URLSearchParams({
    Fields: 'Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,MediaSources,MediaStreams',
  });
  if (seasonId) {
    qs.set('SeasonId', seasonId);
  }
  const data = await request(`/emby/Users/${userId}/Shows/${encodeURIComponent(seriesId)}/Episodes?${qs.toString()}`);
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

// Admin Libraries API
export async function fetchAdminLibraries(): Promise<AdminLibrary[]> {
  const data = await request('/admin/libraries');
  return Array.isArray(data) ? data : (data.Libraries || []);
}

export async function createAdminLibrary(name: string, kind: string, paths: string[]): Promise<void> {
  await request('/admin/libraries', 'POST', { Name: name, Kind: kind, Paths: paths });
}

export async function updateAdminLibrary(id: string, name: string, kind: string, paths?: string[]): Promise<void> {
  const payload: any = { ID: id, Name: name, Kind: kind };
  if (paths && paths.length > 0) {
    payload.Paths = paths;
  }
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

export async function fetchDirectories(basePath: string = '/media'): Promise<{ Path: string; Directories: any[] }> {
  return request(`/admin/directories?${new URLSearchParams({ Path: basePath })}`);
}

export async function fetchScanSchedule(): Promise<any> {
  return request('/admin/scan-schedule');
}

export async function updateScanSchedule(schedule: { Enabled: boolean; Frequency: string; Time: string; Minutes: number }): Promise<void> {
  await request('/admin/scan-schedule', 'PUT', schedule);
}

// Xiaoya Sync & AList API
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

export async function importResource(path: string, isDir: boolean, trackUpdates: boolean, libraryID: string, outputDir: string) {
  return request<AdminJob>('/api/resources/import?async=true', 'POST', {
    path,
    is_dir: isDir,
    track_updates: trackUpdates,
    library_id: libraryID,
    output_dir: outputDir,
  });
}

export async function browseAList(path: string, page: number = 1, perPage: number = 25, source: 'sqlite' | 'alist' = 'sqlite'): Promise<AListBrowseResult> {
  const qs = new URLSearchParams({ path, page: String(page), per_page: String(perPage), source });
  return request(`/api/alist/browse?${qs.toString()}`);
}

export async function searchAList(keyword: string, path: string = '/', page: number = 1, perPage: number = 25): Promise<AListBrowseResult> {
  const qs = new URLSearchParams({
    keywords: keyword,
    keyword: keyword,
    q: keyword,
    parent: path,
    path: path,
    page: String(page),
    per_page: String(perPage),
  });
  return request(`/api/alist/search?${qs.toString()}`);
}

// TMDB API
export async function fetchTmdbConfig(): Promise<TmdbConfig> {
  return request('/admin/tmdb');
}

export async function updateTmdbConfig(cfg: TmdbConfig): Promise<void> {
  await request('/admin/tmdb', 'PUT', cfg);
}

export async function testTmdb(): Promise<any> {
  return request('/admin/tmdb/test', 'POST', {});
}

export async function searchTmdb(q: string, kind: string, year?: string): Promise<TmdbSearchResult[]> {
  const params: Record<string, string> = { q, kind };
  if (year) params.year = year;
  const data = await request(`/admin/tmdb/search?${new URLSearchParams(params)}`);
  return Array.isArray(data) ? data : (data.Items || data.items || data.results || []);
}

export async function matchTmdb(itemId: string, tmdbId: number | string, kind: string): Promise<void> {
  await request('/admin/tmdb/match', 'POST', {
    ItemId: itemId,
    TmdbId: String(tmdbId),
    MediaType: kind,
  });
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

// Logs API
export async function fetchLogs(limit: number = 200, level?: string, source?: string, q?: string): Promise<{ logs: (string | LogEntry)[] }> {
  const params: Record<string, string> = { limit: String(limit) };
  if (level) params.level = level;
  if (source) params.source = source;
  if (q) params.q = q;
  const data = await request(`/admin/logs?${new URLSearchParams(params)}`);
  return data;
}

export async function clearLogs(): Promise<void> {
  await request('/admin/logs', 'DELETE');
}

// Users & System Settings
export async function fetchUsers(): Promise<any[]> {
  return request('/admin/users');
}

export async function createUser(data: { Name: string; Password?: string; MaxDevices: number; AllowPlayback: boolean }): Promise<void> {
  await request('/admin/users', 'POST', {
    Name: data.Name,
    Password: data.Password || '',
    MaxDevices: data.MaxDevices,
    AllowPlayback: data.AllowPlayback,
  });
}

export async function updateUser(id: string, data: { MaxDevices: number; Admin: boolean; AllowPlayback: boolean }): Promise<void> {
  await request('/admin/users', 'PUT', {
    ID: id,
    MaxDevices: data.MaxDevices,
    Admin: data.Admin,
    AllowPlayback: data.AllowPlayback,
  });
}

export async function updateUserIdentity(id: string, name: string, password?: string): Promise<void> {
  await request('/admin/user-identity', 'PUT', {
    ID: id,
    Name: name,
    Password: password || '',
  });
}

export async function deleteUser(id: string): Promise<void> {
  await request('/admin/users', 'DELETE', { ID: id });
}

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


export async function fetchAdminJobs() {
  return (await request<{items: AdminJob[]}>('/api/jobs')).items;
}

export async function waitForAdminJob(id: string, signal?: AbortSignal): Promise<AdminJob> {
  while (true) {
    if (signal?.aborted) throw new DOMException('任务视图已关闭', 'AbortError');
    const job = await request<AdminJob>(`/api/jobs/${encodeURIComponent(id)}`);
    if (job.status !== 'RUNNING') return job;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
}
