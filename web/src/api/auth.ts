import { api } from './client';

export interface Profile {
  id: number;
  name: string;
  fitness_level: string;
}

export interface MeResponse {
  user_id: number;
  username: string;
  profiles: Profile[];
}

export interface WebAuthResponse {
  user_id: number;
}

export const authApi = {
  me: () => api.get<MeResponse>('/auth/me'),

  webLogin: (username: string, password: string) =>
    api.post<WebAuthResponse>('/auth/web/login', { username, password }),

  webSignup: (username: string, password: string) =>
    api.post<WebAuthResponse>('/auth/web/signup', { username, password }),

  webLogout: () => api.post<void>('/auth/web/logout'),
};
