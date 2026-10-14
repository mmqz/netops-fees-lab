/**
 * 告警状态徽章（v0.9 告警中心）：
 * 触发中（红）/ 已确认（橙）/ 已清除（灰）。
 * 复用既有 chip 色板，不新增 CSS。
 */

import type { AlertState } from '../api/types';
import { ALERT_STATE_LABELS } from '../i18n';

const ALERT_STATE_VARIANTS: Record<AlertState, string> = {
  firing: 'chip--error',
  acked: 'chip--warning',
  cleared: 'chip--muted',
};

export function AlertStateChip({ state }: { state: AlertState }) {
  return (
    <span className={`chip ${ALERT_STATE_VARIANTS[state]}`}>
      <span className="chip-dot" aria-hidden="true" />
      {ALERT_STATE_LABELS[state]}
    </span>
  );
}
