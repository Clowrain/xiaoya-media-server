import { User } from '../types';
import { clearAuthSession, request, setAuthSession } from './client';

export async function loginApi(username: string, password: string): Promise<{ User: User; AccessToken: string }> {
  const res = await request<{ User: User; AccessToken: string }>('/emby/Users/AuthenticateByName', 'POST', {
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
