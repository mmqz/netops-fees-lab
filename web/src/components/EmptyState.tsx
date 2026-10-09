/**
 * 空状态占位：图标 + 标题 + 描述 + 可选操作按钮。
 */

import type { ReactNode } from 'react';

interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  icon?: ReactNode;
}

export function EmptyState({ title, description, action, icon }: EmptyStateProps) {
  return (
    <div className="empty">
      {icon !== undefined && <div className="empty-icon">{icon}</div>}
      <div className="empty-title">{title}</div>
      {description !== undefined && <div className="empty-desc">{description}</div>}
      {action !== undefined && <div className="empty-action">{action}</div>}
    </div>
  );
}
