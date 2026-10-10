/**
 * 资源类型字段规格（§6 契约的前端镜像）。
 *
 * 用途：
 *  - 数据导入第②步「映射确认」的目标字段下拉选项、必填校验、自然键推导；
 *  - 资源抽屉「属性信息」的字段顺序与中文标签。
 * 仅作为 UI 展示与本地校验依据，真正的校验权威在后端 mapping 包。
 *
 * v0.4.0 起后端提供 GET /api/schema（mapping.Specs 的忠实投影——类型/
 * 字段/键位/枚举/ref/同义词 + 规则目录），本镜像可用其对照校验；
 * 当前 UI 仍直接使用本编译期镜像（稳定性优先，不强制切换）。
 */

import type { ResourceType } from './api/types';
import { FIELD_LABELS } from './i18n';

export type FieldKind = 'string' | 'ipv4' | 'cidr' | 'int' | 'enum' | 'ref' | 'lat' | 'lng' | 'geo';

export interface FieldSpec {
  /** 目标字段名（后端 §6 契约字段， snake_case）。 */
  field: string;
  required: boolean;
  /** 自然键：该类型的唯一键，导入时自动写入 keyColumns。 */
  naturalKey: boolean;
  kind: FieldKind;
}

export const TYPE_SPECS: Record<ResourceType, FieldSpec[]> = {
  device: [
    { field: 'device_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'device_name', required: true, naturalKey: false, kind: 'string' },
    { field: 'city', required: false, naturalKey: false, kind: 'string' },
    { field: 'vendor', required: false, naturalKey: false, kind: 'string' },
    { field: 'model', required: false, naturalKey: false, kind: 'string' },
    { field: 'mgmt_ip', required: false, naturalKey: false, kind: 'ipv4' },
    { field: 'status', required: false, naturalKey: false, kind: 'enum' },
    { field: 'geo_lat', required: false, naturalKey: false, kind: 'lat' },
    { field: 'geo_lng', required: false, naturalKey: false, kind: 'lng' },
    { field: 'geo', required: false, naturalKey: false, kind: 'geo' },
  ],
  ip_pool: [
    { field: 'pool_name', required: true, naturalKey: true, kind: 'string' },
    { field: 'cidr', required: true, naturalKey: false, kind: 'cidr' },
    { field: 'gateway', required: false, naturalKey: false, kind: 'ipv4' },
    { field: 'vlan_id', required: false, naturalKey: false, kind: 'int' },
    { field: 'assigned_device', required: false, naturalKey: false, kind: 'ref' },
    { field: 'status', required: false, naturalKey: false, kind: 'enum' },
  ],
  circuit: [
    { field: 'circuit_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'a_end_device', required: true, naturalKey: false, kind: 'ref' },
    { field: 'z_end_device', required: true, naturalKey: false, kind: 'ref' },
    { field: 'bandwidth_mbps', required: false, naturalKey: false, kind: 'int' },
    { field: 'status', required: false, naturalKey: false, kind: 'enum' },
  ],
  port: [
    { field: 'device_name', required: true, naturalKey: true, kind: 'string' },
    { field: 'port_name', required: true, naturalKey: true, kind: 'string' },
    { field: 'device_ip', required: true, naturalKey: false, kind: 'ipv4' },
    { field: 'port_desc', required: false, naturalKey: false, kind: 'string' },
    { field: 'port_type', required: false, naturalKey: false, kind: 'string' },
    { field: 'bandwidth_mbps', required: false, naturalKey: false, kind: 'int' },
    { field: 'admin_status', required: false, naturalKey: false, kind: 'string' },
    { field: 'oper_status', required: false, naturalKey: false, kind: 'string' },
    { field: 'device_type', required: false, naturalKey: false, kind: 'string' },
    { field: 'city', required: false, naturalKey: false, kind: 'string' },
    { field: 'district', required: false, naturalKey: false, kind: 'string' },
    { field: 'vendor', required: false, naturalKey: false, kind: 'string' },
    { field: 'model', required: false, naturalKey: false, kind: 'string' },
    { field: 'record_date', required: false, naturalKey: false, kind: 'string' },
    { field: 'geo_lat', required: false, naturalKey: false, kind: 'lat' },
    { field: 'geo_lng', required: false, naturalKey: false, kind: 'lng' },
    { field: 'geo', required: false, naturalKey: false, kind: 'geo' },
  ],
  base_station: [
    { field: 'base_station_type', required: false, naturalKey: false, kind: 'string' },
    { field: 'base_station_name', required: true, naturalKey: false, kind: 'string' },
    { field: 'base_station_code', required: false, naturalKey: true, kind: 'string' },
    { field: 'city', required: false, naturalKey: false, kind: 'string' },
    { field: 'sharing', required: false, naturalKey: false, kind: 'string' },
    { field: 'uplink_a_ip', required: false, naturalKey: false, kind: 'ipv4' },
    { field: 'ring', required: false, naturalKey: false, kind: 'string' },
    { field: 'chain', required: false, naturalKey: false, kind: 'string' },
    { field: 'uplink_a_name', required: false, naturalKey: false, kind: 'string' },
    { field: 'uplink_a_port', required: false, naturalKey: false, kind: 'string' },
    { field: 'gateway_ip', required: false, naturalKey: false, kind: 'ipv4' },
    // 基站IP 为 string：真实值带 /30 掩码（如 172.7.10.125/30），且 IP 属可变属性不作身份（避免 E004 误拒）
    { field: 'base_station_ip', required: false, naturalKey: false, kind: 'string' },
    { field: 'description', required: false, naturalKey: false, kind: 'string' },
    { field: 'work_order', required: false, naturalKey: false, kind: 'string' },
    { field: 'remark', required: false, naturalKey: false, kind: 'string' },
  ],
  // 局站（v0.4 W3-D）：键 = site_id（12 位数字编码，全局）；ID 为 string 防前导零丢失；
  // device_name 为局站内设备昵称（属性，非 device ref——设备键是 device_code 无法解析）；
  // ip_address 为可变属性（IPv4 值列，非身份）
  site: [
    { field: 'site_id', required: true, naturalKey: true, kind: 'string' },
    { field: 'site_name', required: true, naturalKey: false, kind: 'string' },
    { field: 'city', required: false, naturalKey: false, kind: 'string' },
    { field: 'district', required: false, naturalKey: false, kind: 'string' },
    { field: 'device_name', required: false, naturalKey: false, kind: 'string' },
    { field: 'ip_address', required: false, naturalKey: false, kind: 'ipv4' },
  ],
  // 机房（v0.4 W3-D）：键 = machine_room_id（15 位数字编码，全局）；site_id 为可选 ref（局站），
  // 映射后按 E003 语义解析——需先导入局站，否则该行失败；解析成功建 belongs_to 关系
  machine_room: [
    { field: 'machine_room_id', required: true, naturalKey: true, kind: 'string' },
    { field: 'machine_room_name', required: true, naturalKey: false, kind: 'string' },
    { field: 'site_id', required: false, naturalKey: false, kind: 'ref' },
    { field: 'city', required: false, naturalKey: false, kind: 'string' },
    { field: 'district', required: false, naturalKey: false, kind: 'string' },
    { field: 'device_name', required: false, naturalKey: false, kind: 'string' },
    { field: 'ip_address', required: false, naturalKey: false, kind: 'ipv4' },
  ],
};

/** 字段中文名（未知字段回退为字段名本身）。 */
export function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

/** 指定类型的全部字段规格。 */
export function fieldsForType(type: ResourceType): FieldSpec[] {
  return TYPE_SPECS[type];
}

/** 指定类型的自然键字段名列表（port 为双键：device_name + port_name（migration v8 起设备侧锚定
 *  设备名称，device_ip 降为必填 ipv4 值列）；单键类型列表长度为 1，行为与旧版一致）。 */
export function naturalKeyFields(type: ResourceType): string[] {
  const keys = TYPE_SPECS[type].filter((f) => f.naturalKey).map((f) => f.field);
  if (keys.length === 0) throw new Error(`schema: type ${type} has no natural key field`);
  return keys;
}

/** 指定类型的必填字段（含自然键）规格列表。 */
export function requiredFields(type: ResourceType): FieldSpec[] {
  return TYPE_SPECS[type].filter((f) => f.required);
}

/**
 * 条件自然键的兜底字段镜像（后端 TypeSpec.KeyFallbackFields，按字段名、序即拼接序）。
 * 主自然键字段值**全非空** → 键=主键字段拼接（现行机制）；**任一为空** → 键=兜底字段值
 * 按序拼接、跳过空值，并发 W004 治理 issue（行照常导入）。base_station 是首个使用者
 * （编码缺失 → 地市|无线基站名称 兜底，缺地市列则退化为纯名称）；空 = 无条件键类型。
 * 仅用于展示键提示（ImportWizard），实际键解析以后端为准。
 */
export const KEY_FALLBACK_FIELDS: Partial<Record<ResourceType, string[]>> = {
  base_station: ['city', 'base_station_name'],
};
