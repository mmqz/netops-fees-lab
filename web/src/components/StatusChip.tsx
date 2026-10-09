/**
 * 资源状态徽章。
 * 取值为后端存储的状态文本（在网/停用/在建/退网），空值显示「未知」。
 */

import { RESOURCE_STATUS_LABELS, STATUS_UNKNOWN } from '../i18n';

const STATUS_VARIANTS: Record<string, string> = {
  在网: 'chip--success',
  停用: 'chip--muted',
  在建: 'chip--info',
  退网: 'chip--error',
};

export function StatusChip({ status }: { status: string }) {
  const label = RESOURCE_STATUS_LABELS[status] ?? status;
  const variant = STATUS_VARIANTS[status] ?? 'chip--muted';
  const text = status === '' ? STATUS_UNKNOWN : label;
  return (
    <span className={`chip ${status === '' ? 'chip--muted' : variant}`}>
      <span className="chip-dot" aria-hidden="true" />
      {text}
    </span>
  );
}
