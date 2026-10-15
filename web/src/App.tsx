/**
 * 应用外壳：路由 + 左侧深色导航栏 + 顶栏（页标题与面包屑）+ 内容区。
 * Phase 2：新增「仿真工作台」导航分组（工作台 /lab + 拓扑工作台 /lab/:id）。
 */

import { useEffect, useState } from 'react';
import { BrowserRouter, NavLink, Route, Routes, useLocation } from 'react-router-dom';
import type { ReactNode } from 'react';
import { getHealth } from './api/client';
import { useSeq } from './hooks/useSeq';
import {
  APP_NAME,
  APP_SUBTITLE,
  APP_VERSION,
  BREADCRUMB_HOME,
  BREADCRUMB_LABEL,
  BTN_BACK_HOME,
  NAV_ALERTS,
  NAV_IMPORT,
  NAV_ISSUES,
  NAV_LAB_GROUP,
  NAV_LAB_TOPOLOGY,
  NAV_LAB_WORKSPACES,
  NAV_MAIN_LABEL,
  NAV_OVERVIEW,
  NAV_REPORT,
  NAV_RESOURCES,
  NAV_RULES,
  NOT_FOUND_DESC,
  NOT_FOUND_TITLE,
} from './i18n';
import { ToastProvider } from './components/Toast';
import { EmptyState } from './components/EmptyState';
import {
  IconAlert,
  IconDoc,
  IconFlask,
  IconHome,
  IconPencil,
  IconReport,
  IconTable,
  IconTopology,
  IconUpload,
  IconWarning,
} from './components/icons';
import { OverviewPage } from './pages/Overview';
import { ImportWizardPage } from './pages/ImportWizard';
import { ResourcesPage } from './pages/Resources';
import { IssuesPage } from './pages/Issues';
import { AlertsPage } from './pages/Alerts';
import { RulesPage } from './pages/Rules';
import { ReportPage } from './pages/Report';
import { LabWorkspacesPage } from './pages/LabWorkspaces';
import { LabWorkspacePage } from './pages/LabWorkspace';
import { readLastWorkspace } from './lastWorkspace';

interface NavItem {
  to: string;
  label: string;
  icon: ReactNode;
  end?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { to: '/', label: NAV_OVERVIEW, icon: <IconHome />, end: true },
  { to: '/import', label: NAV_IMPORT, icon: <IconUpload /> },
  { to: '/resources', label: NAV_RESOURCES, icon: <IconTable /> },
  { to: '/issues', label: NAV_ISSUES, icon: <IconWarning /> },
  { to: '/alerts', label: NAV_ALERTS, icon: <IconAlert /> },
  { to: '/rules', label: NAV_RULES, icon: <IconPencil size={20} /> },
  { to: '/report', label: NAV_REPORT, icon: <IconReport /> },
];

function routeTitle(pathname: string): string {
  if (pathname === '/lab') return NAV_LAB_WORKSPACES;
  if (pathname.startsWith('/lab/')) return NAV_LAB_TOPOLOGY;
  switch (pathname) {
    case '/':
      return NAV_OVERVIEW;
    case '/import':
      return NAV_IMPORT;
    case '/resources':
      return NAV_RESOURCES;
    case '/issues':
      return NAV_ISSUES;
    case '/alerts':
      return NAV_ALERTS;
    case '/rules':
      return NAV_RULES;
    case '/report':
      return NAV_REPORT;
    default:
      return NOT_FOUND_TITLE;
  }
}

function Sidebar() {
  // 路由切换触发重渲染时读取最近工作台（进入 /lab/:id 时写入）
  const lastWorkspaceId = readLastWorkspace();
  // 侧栏版本号：挂载时从 /api/health 读服务端 domain.AppVersion（单一事实源，R1-D1 根治），
  // 失败/超时/空值时静默回退本地常量 APP_VERSION；仅请求一次，不轮询。
  const [version, setVersion] = useState<string>(APP_VERSION);
  const healthGuard = useSeq();

  useEffect(() => {
    const isCurrent = healthGuard.begin();
    getHealth()
      .then((health) => {
        if (!isCurrent()) return;
        if (health.version !== '') setVersion(health.version);
      })
      .catch(() => {
        /* 静默回退：保留本地常量，不打日志噪音 */
      });
  }, [healthGuard]);

  const labItems: NavItem[] = [
    { to: '/lab', label: NAV_LAB_WORKSPACES, icon: <IconFlask /> },
    ...(lastWorkspaceId === null
      ? []
      : [
          {
            to: `/lab/${lastWorkspaceId}`,
            label: NAV_LAB_TOPOLOGY,
            icon: <IconTopology />,
          },
        ]),
  ];

  return (
    <aside className="sidebar">
      {/* v0.2.2：删除渐变色块 logo（AI 味主要来源）——纯文字字标；折叠态显示单字符标记 */}
      <div className="sidebar-brand">
        <span className="sidebar-brand-mark" aria-hidden="true">N</span>
        <div className="sidebar-brand-text">
          <div className="sidebar-name">{APP_NAME}</div>
          <div className="sidebar-subtitle">{APP_SUBTITLE}</div>
        </div>
      </div>
      <nav className="sidebar-nav" aria-label={NAV_MAIN_LABEL}>
        {NAV_ITEMS.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className={({ isActive }) => `sidebar-link${isActive ? ' sidebar-link--active' : ''}`}
          >
            <span className="sidebar-link-icon">{item.icon}</span>
            <span className="sidebar-link-text">{item.label}</span>
          </NavLink>
        ))}
        <div className="sidebar-group">{NAV_LAB_GROUP}</div>
        {labItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            title={item.to === '/lab' ? undefined : NAV_LAB_TOPOLOGY}
            className={({ isActive }) => `sidebar-link${isActive ? ' sidebar-link--active' : ''}`}
          >
            <span className="sidebar-link-icon">{item.icon}</span>
            <span className="sidebar-link-text">{item.label}</span>
          </NavLink>
        ))}
      </nav>
      <div className="sidebar-footer">
        <span className="sidebar-version">{version}</span>
      </div>
    </aside>
  );
}

function TopBar() {
  const location = useLocation();
  const title = routeTitle(location.pathname);
  const isRoot = location.pathname === '/';
  return (
    <header className="topbar">
      <div className="topbar-title">{title}</div>
      <nav className="topbar-breadcrumb" aria-label={BREADCRUMB_LABEL}>
        <span className="breadcrumb-item">{BREADCRUMB_HOME}</span>
        {!isRoot && (
          <>
            <span className="breadcrumb-sep" aria-hidden="true">/</span>
            <span className="breadcrumb-item breadcrumb-item--current">{title}</span>
          </>
        )}
      </nav>
    </header>
  );
}

function NotFoundPage() {
  return (
    <div className="card">
      <EmptyState
        title={NOT_FOUND_TITLE}
        description={NOT_FOUND_DESC}
        icon={<IconDoc size={36} />}
        action={
          <NavLink to="/" className="btn btn--primary">
            {BTN_BACK_HOME}
          </NavLink>
        }
      />
    </div>
  );
}

/** /lab/:id（拓扑工作台）为全出血宽版；其余页面保持 1200px 阅读宽度（W1）。 */
function isWorkbenchRoute(pathname: string): boolean {
  return /^\/lab\/[^/]+/.test(pathname);
}

function AppShell() {
  const location = useLocation();
  const fullBleed = isWorkbenchRoute(location.pathname);
  return (
    <div className="app-shell">
      <Sidebar />
      <div className="app-main">
        <TopBar />
        <main className={fullBleed ? 'app-content app-content--full' : 'app-content'}>
          <Routes>
            <Route path="/" element={<OverviewPage />} />
            <Route path="/import" element={<ImportWizardPage />} />
            <Route path="/resources" element={<ResourcesPage />} />
            <Route path="/issues" element={<IssuesPage />} />
            <Route path="/alerts" element={<AlertsPage />} />
            <Route path="/rules" element={<RulesPage />} />
            <Route path="/report" element={<ReportPage />} />
            <Route path="/lab" element={<LabWorkspacesPage />} />
            <Route path="/lab/:id" element={<LabWorkspacePage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
        </main>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <ToastProvider>
        <AppShell />
      </ToastProvider>
    </BrowserRouter>
  );
}
