/**
 * 运行状态徽章（v0.9-R5，W22 run_state 快照）：
 * 运行（绿）/ 劣化（橙）/ 停机（红）/ 维护（蓝）/ 未知（灰）；空值显示「无观测」。
 * 与台账状态（StatusChip，导入列自由文本）是两套状态——
 * 台账状态是登记事实，运行状态是观测事实（§12-B1 双态模型）。
 */

import { RES_RUNSTATE_NO_DATA, RUN_STATE_LABELS } from '../i18n';

const RUN_STATE_VARIANTS: Record<string, string> = {
  up: 'chip--success',
  degraded: 'chip--warning',
  down: 'chip--error',
  maintenance: 'chip--info',
  unknown: 'chip--muted',
};

export function RunStateChip({ state }: { state: string }) {
  if (state === '') {
    return <span className="chip chip--muted">{RES_RUNSTATE_NO_DATA}</span>;
  }
  const variant = RUN_STATE_VARIANTS[state] ?? 'chip--muted';
  return (
    <span className={`chip ${variant}`}>
      <span className="chip-dot" aria-hidden="true" />
      {RUN_STATE_LABELS[state] ?? state}
    </span>
  );
}
