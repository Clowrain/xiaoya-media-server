import { User } from '../types';

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
function getAuthHeaders(): Record<string, string> {
  return {
    'X-Emby-Token': getStoredToken() || '',
    'X-Emby-Authorization': `Emby Client="Xiaoya Web React", Device="Browser", DeviceId="${getDeviceId()}", Version="2.0"`,
  };
}

function normalizeUrl(path: string): string {
  return path.startsWith('http') ? path : (path.startsWith('/') ? path : `/${path}`);
}

export async function request<T = any>(
  path: string,
  method: string = 'GET',
  body?: any,
  signal?: AbortSignal
): Promise<T> {
  const headers: Record<string, string> = {
    ...getAuthHeaders(),
    'Content-Type': 'application/json',
  };

  const res = await fetch(normalizeUrl(path), {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
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

export async function requestFile(
  path: string,
  method: 'POST' | 'DELETE',
  file?: File,
  contentType?: string,
  fallbackMessage = '请求失败'
): Promise<void> {
  const headers = getAuthHeaders();
  if (file) headers['Content-Type'] = contentType || file.type || 'application/octet-stream';
  const res = await fetch(normalizeUrl(path), {
    method,
    headers,
    body: file,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || fallbackMessage);
  }
}
