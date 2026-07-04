import { lazy, Suspense } from 'react';
import type { RouteObject } from 'react-router-dom';
import { Navigate } from 'react-router-dom';
import { AuthGuard, AdminGuard, GuestGuard, AiGuard, getDefaultRoute } from './guards';
import { useServerStore } from '@/stores/server-store';
import UnsupportedFeature from '@/components/unsupported-feature';

// Loading component
function PageLoading() {
  return (
    <div className="flex items-center justify-center py-32">
      <div className="animate-spin rounded-full h-6 w-6 border-2 border-muted border-t-primary"></div>
    </div>
  );
}

// Lazy load wrapper
function lazyPage(
  importFn: () => Promise<{ default: React.ComponentType }>
) {
  const LazyComponent = lazy(importFn);
  return (
    <Suspense fallback={<PageLoading />}>
      <LazyComponent />
    </Suspense>
  );
}

function DefaultRedirect() {
  const { aiEnabled, functionMode } = useServerStore();
  return <Navigate to={getDefaultRoute(aiEnabled, functionMode)} replace />;
}

// Layouts
const AppLayout = lazy(() => import('@/layouts/AppLayout'));

export const routes: RouteObject[] = [
  // Public routes
  {
    element: <GuestGuard />,
    children: [
      {
        path: '/login',
        element: lazyPage(() => import('@/pages/login')),
      },
      {
        path: '/register',
        element: lazyPage(() => import('@/pages/register')),
      },
    ],
  },
  
  // Protected routes with AppLayout
  {
    element: <AuthGuard />,
    children: [
      {
        element: (
          <Suspense fallback={<PageLoading />}>
            <AppLayout />
          </Suspense>
        ),
        children: [
          // Default redirect
          {
            index: true,
            element: <DefaultRedirect />,
          },
          
          // Welcome page
          {
            path: 'welcome',
            element: lazyPage(() => import('@/pages/welcome')),
          },
          
          // Namespace
          {
            path: 'namespace',
            element: lazyPage(() => import('@/pages/namespace')),
          },
          
          // Configuration Management
          {
            path: 'configurationManagement',
            element: lazyPage(() => import('@/pages/configurationManagement')),
          },
          {
            path: 'newconfig',
            element: lazyPage(() => import('@/pages/newconfig')),
          },
          {
            path: 'configdetail',
            element: lazyPage(() => import('@/pages/configdetail')),
          },
          {
            path: 'configeditor',
            element: lazyPage(() => import('@/pages/configeditor')),
          },
          {
            path: 'configsync',
            element: lazyPage(() => import('@/pages/configsync')),
          },
          {
            path: 'configRollback',
            element: lazyPage(() => import('@/pages/configRollback')),
          },
          {
            path: 'historyDetail',
            element: lazyPage(() => import('@/pages/historyDetail')),
          },
          {
            path: 'historyRollback',
            element: lazyPage(() => import('@/pages/historyRollback')),
          },
          {
            path: 'listeningToQuery',
            element: lazyPage(() => import('@/pages/listeningToQuery')),
          },
          
          // Service Management
          {
            path: 'serviceManagement',
            element: lazyPage(() => import('@/pages/serviceManagement')),
          },
          {
            path: 'serviceDetail',
            element: lazyPage(() => import('@/pages/serviceDetail')),
          },
          {
            path: 'subscriberList',
            element: lazyPage(() => import('@/pages/subscriberList')),
          },
          
          // Cluster Management (Admin only)
          {
            element: <AdminGuard />,
            children: [
              {
                path: 'clusterManagement',
                element: <UnsupportedFeature featureName="Cluster Management" />,
              },
            ],
          },
          
          // User Management (Admin only)
          {
            element: <AdminGuard />,
            children: [
              {
                path: 'userManagement',
                element: lazyPage(() => import('@/pages/userManagement')),
              },
              {
                path: 'rolesManagement',
                element: lazyPage(() => import('@/pages/rolesManagement')),
              },
              {
                path: 'permissionsManagement',
                element: lazyPage(() => import('@/pages/permissionsManagement')),
              },
            ],
          },
          
          // AI Registry
          {
            element: <AiGuard />,
            children: [
              {
                path: 'mcpServerManagement',
                element: <UnsupportedFeature featureName="MCP Server Management" />,
              },
              {
                path: 'mcpServerDetail',
                element: <UnsupportedFeature featureName="MCP Server Detail" />,
              },
              {
                path: 'newMcpServer',
                element: <UnsupportedFeature featureName="New MCP Server" />,
              },
              {
                path: 'agentManagement',
                element: <UnsupportedFeature featureName="Agent Management" />,
              },
              {
                path: 'newAgent',
                element: <UnsupportedFeature featureName="New Agent" />,
              },
              {
                path: 'agentDetail',
                element: <UnsupportedFeature featureName="Agent Detail" />,
              },
              {
                path: 'agentspec',
                element: <UnsupportedFeature featureName="Agent Spec Management" />,
              },
              {
                path: 'agentspec/new',
                element: <UnsupportedFeature featureName="New Agent Spec" />,
              },
              {
                path: 'agentspec/:name',
                element: <UnsupportedFeature featureName="Agent Spec Detail" />,
              },
              {
                path: 'skill',
                element: <UnsupportedFeature featureName="Skill Management" />,
              },
              {
                path: 'newSkill',
                element: <UnsupportedFeature featureName="New Skill" />,
              },
              {
                path: 'skill/:name',
                element: <UnsupportedFeature featureName="Skill Detail" />,
              },
              {
                path: 'promptManagement',
                element: <UnsupportedFeature featureName="Prompt Management" />,
              },
              {
                path: 'newPrompt',
                element: <Navigate to="/promptManagement" replace />,
              },
              {
                path: 'promptDetail',
                element: <UnsupportedFeature featureName="Prompt Detail" />,
              },
              {
                path: 'publishPromptVersion',
                element: <Navigate to="/newPrompt" replace />,
              },
            ],
          },
          
          // Plugin Management (Admin only)
          {
            element: <AdminGuard />,
            children: [
              {
                path: 'pluginManagement',
                element: <UnsupportedFeature featureName="Plugin Management" />,
              },
            ],
          },

          // Settings
          {
            path: 'settingCenter',
            element: <UnsupportedFeature featureName="Setting Center" />,
          },
        ],
      },
    ],
  },
  
  // Catch-all redirect
  {
    path: '*',
    element: <Navigate to="/" replace />,
  },
];
