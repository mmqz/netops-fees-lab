/**
 * 资源类型字段规格（§6 契约的前端镜像）。
 *
 * 用途：
 *  - 数据导入第②步「映射确认」的目标字段下拉选项、必填校验、自然键推导；
 *  - 资源抽屉「属性信息」的字段顺序与中文标签。
 * 仅作为 UI 展示与本地校验依据，真正的校验权威在后端 mapping 包。
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
    { field: 'device_ip', required: true, naturalKey: true, kind: 'ipv4' },
    { field: 'port_name', required: true, naturalKey: true, kind: 'string' },
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
};

/** 字段中文名（未知字段回退为字段名本身）。 */
export function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

/** 指定类型的全部字段规格。 */
export function fieldsForType(type: ResourceType): FieldSpec[] {
  return TYPE_SPECS[type];
}

/** 指定类型的自然键字段名列表（port 为双键：device_ip + port_name，
 *  keyColumns 必须全量携带；单键类型列表长度为 1，行为与旧版一致）。 */
export function naturalKeyFields(type: ResourceType): string[] {
  const keys = TYPE_SPECS[type].filter((f) => f.naturalKey).map((f) => f.field);
  if (keys.length === 0) throw new Error(`schema: type ${type} has no natural key field`);
  return keys;
}

/** 指定类型的必填字段（含自然键）规格列表。 */
export function requiredFields(type: ResourceType): FieldSpec[] {
  return TYPE_SPECS[type].filter((f) => f.required);
}
