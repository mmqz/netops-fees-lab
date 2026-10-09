/**
 * 应用外壳：路由 + 左侧深色导航栏 + 顶栏（页标题与面包屑）+ 内容区。
 */

import { BrowserRouter, NavLink, Route, Routes, useLocation } from 'react-router-dom';
import type { ReactNode } from 'react';
import {
  APP_NAME,
  APP_SUBTITLE,
  APP_VERSION,
  BREADCRUMB_HOME,
  BREADCRUMB_LABEL,
  BTN_BACK_HOME,
  NAV_IMPORT,
  NAV_ISSUES,
  NAV_MAIN_LABEL,
  NAV_OVERVIEW,
  NAV_REPORT,
  NAV_RESOURCES,
  NOT_FOUND_DESC,
  NOT_FOUND_TITLE,
} from './i18n';
import { ToastProvider } from './components/Toast';
import { EmptyState } from './components/EmptyState';
import { IconDoc, IconHome, IconReport, IconTable, IconUpload, IconWarning } from './components/icons';
import { OverviewPage } from './pages/Overview';
import { ImportWizardPage } from './pages/ImportWizard';
import { ResourcesPage } from './pages/Resources';
import { IssuesPage } from './pages/Issues';
import { ReportPage } from './pages/Report';

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
  { to: '/report', label: NAV_REPORT, icon: <IconReport /> },
];

function routeTitle(pathname: string): string {
  switch (pathname) {
    case '/':
      return NAV_OVERVIEW;
    case '/import':
      return NAV_IMPORT;
    case '/resources':
      return NAV_RESOURCES;
    case '/issues':
      return NAV_ISSUES;
    case '/report':
      return NAV_REPORT;
    default:
      return NOT_FOUND_TITLE;
  }
}

function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sidebar-brand">
        <div className="sidebar-logo" aria-hidden="true">N</div>
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
      </nav>
      <div className="sidebar-footer">
        <span className="sidebar-version">{APP_VERSION}</span>
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

export default function App() {
  return (
    <BrowserRouter>
      <ToastProvider>
        <div className="app-shell">
          <Sidebar />
          <div className="app-main">
            <TopBar />
            <main className="app-content">
              <Routes>
                <Route path="/" element={<OverviewPage />} />
                <Route path="/import" element={<ImportWizardPage />} />
                <Route path="/resources" element={<ResourcesPage />} />
                <Route path="/issues" element={<IssuesPage />} />
                <Route path="/report" element={<ReportPage />} />
                <Route path="*" element={<NotFoundPage />} />
              </Routes>
            </main>
          </div>
        </div>
      </ToastProvider>
    </BrowserRouter>
  );
}
