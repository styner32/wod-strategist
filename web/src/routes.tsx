import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from './auth/ProtectedRoute';
import { LoginPage } from './auth/LoginPage';

import { HistoryListPage } from './history/HistoryListPage';
import { SessionDetailPage } from './history/SessionDetailPage';
import { StretchesPage } from './stretches/StretchesPage';
import { StretchCatalogManagePage } from './stretches/StretchCatalogManagePage';
import { StretchFormPage } from './stretches/StretchFormPage';
import { UploadPage } from './upload/UploadPage';
import { AppLayout } from './AppLayout';

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <LoginPage />,
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
            path: '/stretches',
            element: <StretchesPage />,
          },
          {
            path: '/stretches/manage',
            element: <StretchCatalogManagePage />,
          },
          {
            path: '/stretches/manage/new',
            element: <StretchFormPage />,
          },
          {
            path: '/stretches/manage/:id',
            element: <StretchFormPage />,
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
