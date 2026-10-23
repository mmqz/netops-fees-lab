/**
 * 应用外壳：路由 + 左侧深色导航栏 + 顶栏（页标题与面包屑）+ 内容区。
 * 费用仓形态：概览 / 导入 / 资源 / 问题 / 费用 / 报告 / API 参考。
 */

import { useEffect, useState } from 'react';
import { BrowserRouter, NavLink, Route, Routes, useLocation } from 'react-router-dom';
import type { ReactNode } from 'react';
import { getHealth, getStoredAuthToken, setStoredAuthToken } from './api/client';
import { useModalA11y } from './hooks/useModalA11y';
import { useSeq } from './hooks/useSeq';
import {
  APP_NAME,
  APP_SUBTITLE,
  APP_VERSION,
  AUTH_TOKEN_BADGE,
  AUTH_TOKEN_BTN,
  AUTH_TOKEN_CLEARED_TOAST,
  AUTH_TOKEN_CLEAR,
  AUTH_TOKEN_HINT,
  AUTH_TOKEN_LABEL,
  AUTH_TOKEN_PLACEHOLDER,
  AUTH_TOKEN_SAVED_TOAST,
  AUTH_TOKEN_SAVE,
  AUTH_TOKEN_TITLE,
  BREADCRUMB_HOME,
  BREADCRUMB_LABEL,
  BTN_BACK_HOME,
  BTN_CLOSE,
  NAV_API,
  NAV_IMPORT,
  NAV_ISSUES,
  NAV_MAIN_LABEL,
  NAV_FEES,
  NAV_OVERVIEW,
  NAV_REPORT,
  NAV_RESOURCES,
  NOT_FOUND_DESC,
  NOT_FOUND_TITLE,
} from './i18n';
import { ToastProvider, useToast } from './components/Toast';
import { EmptyState } from './components/EmptyState';
import {
  IconCoins,
  IconDoc,
  IconHome,
  IconReport,
  IconTable,
  IconUpload,
  IconWarning,
} from './components/icons';
import { OverviewPage } from './pages/Overview';
import { ImportWizardPage } from './pages/ImportWizard';
import { ResourcesPage } from './pages/Resources';
import { IssuesPage } from './pages/Issues';
import { FeesPage } from './pages/Fees';
import { ApiReferencePage } from './pages/ApiReference';
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
  { to: '/fees', label: NAV_FEES, icon: <IconCoins size={20} /> },
  { to: '/report', label: NAV_REPORT, icon: <IconReport /> },
  { to: '/dev', label: NAV_API, icon: <IconDoc size={20} /> },
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
    case '/fees':
      return NAV_FEES;
    case '/report':
      return NAV_REPORT;
    case '/dev':
      return NAV_API;
    default:
      return NOT_FOUND_TITLE;
  }
}

function Sidebar() {
  // 侧栏版本号：挂载时从 /api/health 读服务端 domain.AppVersion（单一事实源，R1-D1 根治），
  // 失败/超时/空值时静默回退本地常量 APP_VERSION；仅请求一次，不轮询。
  const [version, setVersion] = useState<string>(APP_VERSION);
  const healthGuard = useSeq();
  // 访问令牌设置（v0.11-R13）：入口在侧栏页脚，状态本地自持。
  const [tokenOpen, setTokenOpen] = useState(false);

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
      </nav>
      <div className="sidebar-footer">
        <button
          type="button"
          className="sidebar-token-btn"
          onClick={() => setTokenOpen(true)}
          title={AUTH_TOKEN_TITLE}
        >
          <span aria-hidden="true">●</span>
          <span>{AUTH_TOKEN_BTN}</span>
          {getStoredAuthToken() !== '' && <span className="sidebar-token-badge">{AUTH_TOKEN_BADGE}</span>}
        </button>
        <span className="sidebar-version">{version}</span>
      </div>
      {tokenOpen && <TokenDialog onClose={() => setTokenOpen(false)} />}
    </aside>
  );
}

/** 访问令牌设置对话框（v0.11-R13）：保存/清除 localStorage 的 Bearer 令牌。 */
function TokenDialog({ onClose }: { onClose: () => void }) {
  const modalRef = useModalA11y(true);
  const toast = useToast();
  const [token, setToken] = useState(getStoredAuthToken());

  function save(): void {
    setStoredAuthToken(token.trim());
    toast.show(AUTH_TOKEN_SAVED_TOAST, 'success');
    onClose();
  }
  function clear(): void {
    setStoredAuthToken('');
    setToken('');
    toast.show(AUTH_TOKEN_CLEARED_TOAST, 'info');
    onClose();
  }
  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={onClose} />
      <div
        ref={modalRef}
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={AUTH_TOKEN_TITLE}
        tabIndex={-1}
      >
        <h3 className="modal-title">{AUTH_TOKEN_TITLE}</h3>
        <div className="modal-message">{AUTH_TOKEN_HINT}</div>
        <div className="field">
          <label className="field-label" htmlFor="auth-token-input">
            {AUTH_TOKEN_LABEL}
          </label>
          <input
            id="auth-token-input"
            className="input"
            type="password"
            value={token}
            placeholder={AUTH_TOKEN_PLACEHOLDER}
            autoFocus
            onChange={(event) => setToken(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && token.trim() !== '') save();
            }}
          />
        </div>
        <div className="modal-actions">
          <button type="button" className="btn" onClick={onClose}>
            {BTN_CLOSE}
          </button>
          {token !== '' && (
            <button type="button" className="btn btn--secondary" onClick={clear}>
              {AUTH_TOKEN_CLEAR}
            </button>
          )}
          <button type="button" className="btn btn--primary" disabled={token.trim() === ''} onClick={save}>
            {AUTH_TOKEN_SAVE}
          </button>
        </div>
      </div>
    </div>
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

function AppShell() {
  return (
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
            <Route path="/fees" element={<FeesPage />} />
            <Route path="/report" element={<ReportPage />} />
            <Route path="/dev" element={<ApiReferencePage />} />
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
