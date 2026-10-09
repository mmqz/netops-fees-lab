/**
 * 批次状态徽章（导入向导状态机中文标签）。
 */

import type { BatchStatus } from '../api/types';
import { BATCH_STATUS_LABELS } from '../i18n';

const STATUS_VARIANTS: Record<BatchStatus, string> = {
  analyzing: 'chip--info',
  awaiting_mapping: 'chip--warning',
  importing: 'chip--info',
  completed: 'chip--success',
  failed: 'chip--error',
  rolled_back: 'chip--muted',
};

export function BatchStatusChip({ status }: { status: BatchStatus }) {
  const label = BATCH_STATUS_LABELS[status] ?? status;
  const variant = STATUS_VARIANTS[status] ?? 'chip--muted';
  return (
    <span className={`chip ${variant}`}>
      <span className="chip-dot" aria-hidden="true" />
      {label}
    </span>
  );
}
