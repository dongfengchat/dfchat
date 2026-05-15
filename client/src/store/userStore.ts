import { create } from 'zustand';
import type { User } from '@/types';

interface UserState {
  user: User | null;
  accessToken: string | null;
  setSession: (user: User, accessToken: string, refreshToken?: string) => void;
  clear: () => void;
}

export const useUserStore = create<UserState>((set) => ({
  user: null,
  accessToken: localStorage.getItem('accessToken'),
  setSession: (user, accessToken, refreshToken) => {
    localStorage.setItem('accessToken', accessToken);
    if (refreshToken) localStorage.setItem('refreshToken', refreshToken);
    set({ user, accessToken });
  },
  clear: () => {
    localStorage.removeItem('accessToken');
    localStorage.removeItem('refreshToken');
    set({ user: null, accessToken: null });
  },
}));
