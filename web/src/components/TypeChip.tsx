/**
 * 资源类型徽章：网元设备 / IP地址池 / 传输电路 / 设备端口 / 基站。
 */

import type { ResourceType } from '../api/types';
import { RESOURCE_TYPE_LABELS } from '../i18n';

const TYPE_VARIANTS: Record<ResourceType, string> = {
  device: 'chip--device',
  ip_pool: 'chip--pool',
  circuit: 'chip--circuit',
  port: 'chip--port',
  base_station: 'chip--base-station',
};

export function TypeChip({ type }: { type: ResourceType }) {
  return <span className={`chip ${TYPE_VARIANTS[type]}`}>{RESOURCE_TYPE_LABELS[type]}</span>;
}
