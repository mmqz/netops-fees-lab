/**
 * 问题严重级别徽章：错误（红）/ 警告（橙）/ 信息（蓝）。
 */

import type { Severity } from '../api/types';
import { SEVERITY_LABELS } from '../i18n';

const SEVERITY_VARIANTS: Record<Severity, string> = {
  error: 'chip--error',
  warning: 'chip--warning',
  info: 'chip--info',
};

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={`chip ${SEVERITY_VARIANTS[severity]}`}>
      <span className="chip-dot" aria-hidden="true" />
      {SEVERITY_LABELS[severity]}
    </span>
  );
}
