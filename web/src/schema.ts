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

export type FieldKind = 'string' | 'ipv4' | 'cidr' | 'int' | 'enum' | 'ref';

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
};

/** 字段中文名（未知字段回退为字段名本身）。 */
export function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

/** 指定类型的全部字段规格。 */
export function fieldsForType(type: ResourceType): FieldSpec[] {
  return TYPE_SPECS[type];
}

/** 指定类型的自然键字段名（keyColumns 唯一成员）。 */
export function naturalKeyField(type: ResourceType): string {
  const spec: FieldSpec | undefined = TYPE_SPECS[type].find((f) => f.naturalKey);
  if (!spec) throw new Error(`schema: type ${type} has no natural key field`);
  return spec.field;
}

/** 指定类型的必填字段（含自然键）规格列表。 */
export function requiredFields(type: ResourceType): FieldSpec[] {
  return TYPE_SPECS[type].filter((f) => f.required);
}
