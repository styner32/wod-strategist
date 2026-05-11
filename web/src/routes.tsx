import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from './auth/ProtectedRoute';
import { LoginPage } from './auth/LoginPage';
import { SignupPage } from './auth/SignupPage';
import { HistoryListPage } from './history/HistoryListPage';
import { SessionDetailPage } from './history/SessionDetailPage';
import { UploadPage } from './upload/UploadPage';
import { AppLayout } from './AppLayout';

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <LoginPage />,
  },
  {
    path: '/signup',
    element: <SignupPage />,
  },
  {
    element: <ProtectedRoute />,
    children: [
      {
        element: <AppLayout />,
        children: [
          {
            path: '/',
            element: <HistoryListPage />,
          },
          {
            path: '/sessions/:sessionId',
            element: <SessionDetailPage />,
          },
          {
            path: '/upload',
            element: <UploadPage />,
          },
        ],
      },
    ],
  },
  {
    path: '*',
    element: <Navigate to="/" replace />,
  },
]);
