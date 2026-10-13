/**
 * 资源类型徽章：33 类（8 个既有 + 25 个 W21 扩容类型）。
 *
 * W21 起按「层族」复用既有调色板（每层一个可辨识色族，AA 对比度沿用
 * 各 chip--* 既有的实测值）：
 *   L0 空间（site/machine_room/rack/duct/manhole/pole）→ 深绿 site 族
 *   L1 设备（device/board/port/ptp_port）              → 蓝 device 族
 *   L2 光缆（optical_cable/…/splice_box）              → 深青 port 族
 *   L3 传输（circuit/otn_och/sdh_timeslot）            → 紫 circuit 族
 *   L4 接入（base_station/bbu/rru/olt/onu/pon_port）   → 琥珀 base-station 族
 *   L5 逻辑（ip_pool/protection_group/ring/…）         → 靛蓝 ptp-port 族
 *   L6 业务（customer/service/leased_line）            → 玫瑰 machine-room 族
 */

import type { ResourceType } from '../api/types';
import { RESOURCE_TYPE_LABELS } from '../i18n';

const TYPE_VARIANTS: Record<ResourceType, string> = {
  device: 'chip--device',
  ip_pool: 'chip--pool',
  circuit: 'chip--circuit',
  port: 'chip--port',
  base_station: 'chip--base-station',
  site: 'chip--site',
  machine_room: 'chip--machine-room',
  ptp_port: 'chip--ptp-port',
  // W21 扩容：层族配色
  rack: 'chip--site',
  duct: 'chip--site',
  manhole: 'chip--site',
  pole: 'chip--site',
  board: 'chip--device',
  optical_cable: 'chip--port',
  cable_segment: 'chip--port',
  fiber: 'chip--port',
  odf: 'chip--port',
  odf_terminal: 'chip--port',
  splice_box: 'chip--port',
  otn_och: 'chip--circuit',
  sdh_timeslot: 'chip--circuit',
  protection_group: 'chip--ptp-port',
  ring: 'chip--ptp-port',
  igp_domain: 'chip--ptp-port',
  vpn_instance: 'chip--ptp-port',
  customer: 'chip--machine-room',
  service: 'chip--machine-room',
  leased_line: 'chip--machine-room',
  bbu: 'chip--base-station',
  rru: 'chip--base-station',
  olt: 'chip--base-station',
  onu: 'chip--base-station',
  pon_port: 'chip--base-station',
};

export function TypeChip({ type }: { type: ResourceType }) {
  return <span className={`chip ${TYPE_VARIANTS[type]}`}>{RESOURCE_TYPE_LABELS[type]}</span>;
}
