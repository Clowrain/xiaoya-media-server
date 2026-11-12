import type { User } from '../types';
import { request } from './client';

export interface AdminUser extends User {
  MaxDevices?: number;
}

export interface CreateUserRequest {
  Name: string;
  Password?: string;
  MaxDevices: number;
  AllowPlayback: boolean;
}

export interface UpdateUserRequest {
  MaxDevices: number;
  Admin: boolean;
  AllowPlayback: boolean;
}

export function fetchCurrentUser(userId: string): Promise<User> {
  return request<User>(`/emby/Users/${encodeURIComponent(userId)}`);
}

export async function changePasswordApi(userId: string, oldPw: string, newPw: string): Promise<void> {
  await request(`/Users/${userId}/Password`, 'POST', {
    CurrentPw: oldPw,
    NewPw: newPw,
  });
}

export async function fetchUsers(): Promise<AdminUser[]> {
  return request<AdminUser[]>('/admin/users');
}

export async function createUser(data: CreateUserRequest): Promise<void> {
  await request('/admin/users', 'POST', {
    Name: data.Name,
    Password: data.Password || '',
    MaxDevices: data.MaxDevices,
    AllowPlayback: data.AllowPlayback,
  });
}

export async function updateUser(id: string, data: UpdateUserRequest): Promise<void> {
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
