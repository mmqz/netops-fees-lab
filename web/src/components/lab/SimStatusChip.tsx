/**
 * 仿真实体状态徽章：up=运行中（绿）/ down=已停机（红）。
 */

import type { SimEntityStatus } from '../../api/types';
import { SIM_STATUS_LABELS } from '../../i18n';

const VARIANTS: Record<SimEntityStatus, string> = {
  up: 'chip--success',
  down: 'chip--error',
};

export function SimStatusChip({ status }: { status: SimEntityStatus }) {
  return (
    <span className={`chip ${VARIANTS[status]}`}>
      <span className="chip-dot" aria-hidden="true" />
      {SIM_STATUS_LABELS[status]}
    </span>
  );
}
