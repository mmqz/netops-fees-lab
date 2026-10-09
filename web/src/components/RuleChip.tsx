/**
 * 校验规则徽章：规则代码 + 中文标签，颜色随严重级别。
 */

import type { RuleCode, Severity } from '../api/types';
import { RULE_LABELS } from '../i18n';

const SEVERITY_VARIANTS: Record<Severity, string> = {
  error: 'chip--error',
  warning: 'chip--warning',
  info: 'chip--info',
};

interface RuleChipProps {
  code: string;
  severity: Severity;
}

export function RuleChip({ code, severity }: RuleChipProps) {
  const label: string | undefined = RULE_LABELS[code as RuleCode];
  const variant = SEVERITY_VARIANTS[severity];
  return (
    <span className={`chip chip--mono ${variant}`}>
      {code}
      {label !== undefined ? ` ${label}` : ''}
    </span>
  );
}
