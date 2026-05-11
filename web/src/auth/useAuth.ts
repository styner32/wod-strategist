import { useQuery, useQueryClient } from '@tanstack/react-query';
import { authApi, type MeResponse } from '../api/auth';

const ME_QUERY_KEY = ['auth', 'me'] as const;

export function useAuth() {
  const queryClient = useQueryClient();

  const {
    data: user,
    isLoading,
    isError,
    error,
  } = useQuery<MeResponse>({
    queryKey: ME_QUERY_KEY,
    queryFn: authApi.me,
    retry: false,
    staleTime: 5 * 60 * 1000, // 5 minutes
  });

  const logout = async () => {
    await authApi.webLogout();
    queryClient.removeQueries({ queryKey: ME_QUERY_KEY });
    window.location.href = '/login';
  };

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ME_QUERY_KEY });
  };

  return {
    user,
    isLoading,
    isAuthenticated: !!user && !isError,
    logout,
    invalidate,
    error,
  };
}
