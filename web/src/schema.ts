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
    // W21 可选 ref 列（映射后派生 housed_in / member_of_ring / in_domain / serves；
    // 未映射 → 无 W001 无关系，既有 devices.xlsx 导入逐字节不变 B4）
    { field: 'machine_room_id', required: false, naturalKey: false, kind: 'ref' },
    { field: 'ring_code', required: false, naturalKey: false, kind: 'ref' },
    { field: 'igp_domain', required: false, naturalKey: false, kind: 'ref' },
    { field: 'serve_customer', required: false, naturalKey: false, kind: 'ref' },
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
    // W21 可选 ref 列（carried_by ×2：承载波道 / 承载时隙）
    { field: 'carried_och', required: false, naturalKey: false, kind: 'ref' },
    { field: 'carried_timeslot', required: false, naturalKey: false, kind: 'ref' },
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
    // W21 可选 ref 列（mounted_on / in_vpn）
    { field: 'board_id', required: false, naturalKey: false, kind: 'ref' },
    { field: 'vpn_instance', required: false, naturalKey: false, kind: 'ref' },
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
    // W21 可选 ref 列（serves / uplink_to——归属A 文本列不派生任何关系，B4）
    { field: 'serve_customer', required: false, naturalKey: false, kind: 'ref' },
    { field: 'uplink_device', required: false, naturalKey: false, kind: 'ref' },
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
  // PTP端口（v0.5 W8-D，PTP 配置脚本导入通道）：键 = device_model|port_name（模型级模板
  // 复合键，两分量均为名称类 → W006 改名检测开启）；全局时钟参数按行扁平重复；无 ref
  // 字段不派生关系（脚本不含设备实例身份）；unsymmetrical_offset/utc_trace 为 string
  // （真实值含文字后缀 "0 negative" / "auto holdover 24"）；delay_mechanism 为枚举
  // e2e/p2p。列头 = 各字段中文 Label（同义词首项），虚拟 sheet 经同义词匹配自动建议。
  ptp_port: [
    { field: 'device_model', required: true, naturalKey: true, kind: 'string' },
    { field: 'port_name', required: true, naturalKey: true, kind: 'string' },
    { field: 'annotation', required: false, naturalKey: false, kind: 'string' },
    { field: 'portnum', required: false, naturalKey: false, kind: 'int' },
    { field: 'port_state', required: false, naturalKey: false, kind: 'string' },
    { field: 'free_mode', required: false, naturalKey: false, kind: 'string' },
    { field: 'pdelay_req_interval', required: false, naturalKey: false, kind: 'int' },
    { field: 'unsymmetrical_offset', required: false, naturalKey: false, kind: 'string' },
    { field: 'delay_mechanism', required: false, naturalKey: false, kind: 'enum' },
    { field: 'domain_num', required: false, naturalKey: false, kind: 'int' },
    { field: 'clock_mode', required: false, naturalKey: false, kind: 'string' },
    { field: 'state_algorithm', required: false, naturalKey: false, kind: 'string' },
    { field: 'priority1', required: false, naturalKey: false, kind: 'int' },
    { field: 'priority2', required: false, naturalKey: false, kind: 'int' },
    { field: 'local_priority', required: false, naturalKey: false, kind: 'int' },
    { field: 'clock_class', required: false, naturalKey: false, kind: 'int' },
    { field: 'clock_accuracy', required: false, naturalKey: false, kind: 'int' },
    { field: 'announce_interval', required: false, naturalKey: false, kind: 'int' },
    { field: 'announce_timeout', required: false, naturalKey: false, kind: 'int' },
    { field: 'utc_trace', required: false, naturalKey: false, kind: 'string' },
    { field: 'ssm_mode', required: false, naturalKey: false, kind: 'string' },
    { field: 'ssm_sec_quality', required: false, naturalKey: false, kind: 'int' },
    { field: 'ssm_wtr_time', required: false, naturalKey: false, kind: 'int' },
  ],

  // ---- W21 扩容（25 类，data-model v1.0 §3.2；字段名为后端 mapping Specs 的
  // 精确镜像——名称列 + 自然键 + ref 列；权威字段全集以 GET /api/schema 为准） ----
  // L0 空间层：机架（machine_room_id → located_in）
  rack: [
    { field: 'rack_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'machine_room_id', required: false, naturalKey: false, kind: 'ref' },
  ],
  // 管道（起止局站为纯文本列，W21 无 ref 契约）
  duct: [{ field: 'duct_code', required: true, naturalKey: true, kind: 'string' }],
  manhole: [{ field: 'manhole_code', required: true, naturalKey: true, kind: 'string' }],
  pole: [{ field: 'pole_code', required: true, naturalKey: true, kind: 'string' }],
  // L1 设备层：板卡（device_code → installed_in，必填）
  board: [
    { field: 'board_id', required: true, naturalKey: true, kind: 'string' },
    { field: 'device_code', required: true, naturalKey: false, kind: 'ref' },
  ],
  // L2 光缆层：光缆（a_end_site/z_end_site → cable_a_end/cable_z_end 必填；
  // connect_device → connects_device 可选）
  optical_cable: [
    { field: 'cable_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'cable_name', required: true, naturalKey: false, kind: 'string' },
    { field: 'a_end_site', required: true, naturalKey: false, kind: 'ref' },
    { field: 'z_end_site', required: true, naturalKey: false, kind: 'ref' },
    { field: 'connect_device', required: false, naturalKey: false, kind: 'ref' },
  ],
  // 光缆段（cable_code → segment_of，必填）
  cable_segment: [
    { field: 'segment_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'cable_code', required: true, naturalKey: false, kind: 'ref' },
  ],
  // 纤芯（复合键 cable_code|fiber_no；cable_code 兼键兼 fiber_of ref 必填；
  // terminate_odf → fiber_terminates 可选，值=ODF端子复合键串）
  fiber: [
    { field: 'cable_code', required: true, naturalKey: true, kind: 'ref' },
    { field: 'fiber_no', required: true, naturalKey: true, kind: 'string' },
    { field: 'terminate_odf', required: false, naturalKey: false, kind: 'ref' },
  ],
  odf: [
    { field: 'odf_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'odf_name', required: true, naturalKey: false, kind: 'string' },
  ],
  // ODF端子（复合键 odf_code|terminal_no）
  odf_terminal: [
    { field: 'odf_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'terminal_no', required: true, naturalKey: true, kind: 'string' },
  ],
  splice_box: [{ field: 'splice_box_code', required: true, naturalKey: true, kind: 'string' }],
  // L3 传输层：OTN波道（route_cable → routed_over / use_fiber → uses_fiber 可选）
  otn_och: [
    { field: 'och_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'route_cable', required: false, naturalKey: false, kind: 'ref' },
    { field: 'use_fiber', required: false, naturalKey: false, kind: 'ref' },
  ],
  sdh_timeslot: [{ field: 'timeslot_code', required: true, naturalKey: true, kind: 'string' }],
  // L5 逻辑层：保护组（protected_circuit → protects，必填）
  protection_group: [
    { field: 'pg_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'protected_circuit', required: true, naturalKey: false, kind: 'ref' },
  ],
  ring: [
    { field: 'ring_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'ring_name', required: true, naturalKey: false, kind: 'string' },
  ],
  igp_domain: [
    { field: 'domain_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'domain_name', required: true, naturalKey: false, kind: 'string' },
  ],
  vpn_instance: [
    { field: 'vpn_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'vpn_name', required: true, naturalKey: false, kind: 'string' },
  ],
  // L6 业务层：客户 / 业务实例 / 专线
  customer: [
    { field: 'customer_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'customer_name', required: true, naturalKey: false, kind: 'string' },
  ],
  // 业务实例（customer_code → subscribed_by / provide_circuit → provided_by 必填；
  // depend_device / depend_circuit → depends_on 可选）
  service: [
    { field: 'service_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'customer_code', required: true, naturalKey: false, kind: 'ref' },
    { field: 'provide_circuit', required: true, naturalKey: false, kind: 'ref' },
    { field: 'depend_device', required: false, naturalKey: false, kind: 'ref' },
    { field: 'depend_circuit', required: false, naturalKey: false, kind: 'ref' },
  ],
  leased_line: [
    { field: 'line_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'line_name', required: false, naturalKey: false, kind: 'string' },
    // W25（#16）：真实专线表「客户名称」列 → 客户引用（subscribed_by）
    { field: 'customer_code', required: false, naturalKey: false, kind: 'ref' },
  ],
  // L4 接入层：BBU / RRU / OLT / ONU / PON口
  bbu: [{ field: 'bbu_code', required: true, naturalKey: true, kind: 'string' }],
  // RRU（home_bbu → home_bbu 必填；cover_station → covers 可选）
  rru: [
    { field: 'rru_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'home_bbu', required: true, naturalKey: false, kind: 'ref' },
    { field: 'cover_station', required: false, naturalKey: false, kind: 'ref' },
  ],
  olt: [{ field: 'olt_code', required: true, naturalKey: true, kind: 'string' }],
  // ONU（connect_port → connects_onu 必填，值=PON口复合键串 olt_code|port_no）
  onu: [
    { field: 'onu_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'connect_port', required: true, naturalKey: false, kind: 'ref' },
  ],
  // PON口（复合键 olt_code|port_no）
  pon_port: [
    { field: 'olt_code', required: true, naturalKey: true, kind: 'string' },
    { field: 'port_no', required: true, naturalKey: true, kind: 'string' },
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

/**
 * 类型层次/专业域元数据（#113-B 裁决）：TYPE_META 是前端层名的**单一手抄面**
 * ——与后端无契约耦合的静态镜像（后端 GET /api/schema 暴露同名 layer/layerName/
 * domain 字段可人工对照校验，但两端互不消费对方，漂移风险以视觉评审为界）。
 * W22 traverse 视图（GraphAnalysisTab）自 #113 起经 layerIdName() 消费层名
 * （层号→层名索引从本表机械派生，不再存在第二份手抄——收敛前该页持有
 * 一份已漂移的 L0-L6 层名副本）；W25 域过滤待后续消费。
 * 七层模型 §10.2（§12-B5 修订：接入层 L4、逻辑层 L5、业务层 L6；ip_pool 归逻辑层）。
 */
export interface TypeMeta {
  /** 七层模型层数（0-6）。 */
  layer: number;
  /** 层的中文名（与后端 LayerName 同词形）。 */
  layerName: string;
  /** 专业域（space/device/optical/transport/access/logic/service）。 */
  domain: string;
}

export const TYPE_META: Record<ResourceType, TypeMeta> = {
  site: { layer: 0, layerName: '空间层', domain: 'space' },
  machine_room: { layer: 0, layerName: '空间层', domain: 'space' },
  rack: { layer: 0, layerName: '空间层', domain: 'space' },
  duct: { layer: 0, layerName: '空间层', domain: 'space' },
  manhole: { layer: 0, layerName: '空间层', domain: 'space' },
  pole: { layer: 0, layerName: '空间层', domain: 'space' },
  device: { layer: 1, layerName: '物理层（设备）', domain: 'device' },
  board: { layer: 1, layerName: '物理层（设备）', domain: 'device' },
  port: { layer: 1, layerName: '物理层（设备）', domain: 'device' },
  ptp_port: { layer: 1, layerName: '物理层（设备）', domain: 'device' },
  optical_cable: { layer: 2, layerName: '物理层（光缆）', domain: 'optical' },
  cable_segment: { layer: 2, layerName: '物理层（光缆）', domain: 'optical' },
  fiber: { layer: 2, layerName: '物理层（光缆）', domain: 'optical' },
  odf: { layer: 2, layerName: '物理层（光缆）', domain: 'optical' },
  odf_terminal: { layer: 2, layerName: '物理层（光缆）', domain: 'optical' },
  splice_box: { layer: 2, layerName: '物理层（光缆）', domain: 'optical' },
  circuit: { layer: 3, layerName: '传输层', domain: 'transport' },
  otn_och: { layer: 3, layerName: '传输层', domain: 'transport' },
  sdh_timeslot: { layer: 3, layerName: '传输层', domain: 'transport' },
  base_station: { layer: 4, layerName: '接入层', domain: 'access' },
  bbu: { layer: 4, layerName: '接入层', domain: 'access' },
  rru: { layer: 4, layerName: '接入层', domain: 'access' },
  olt: { layer: 4, layerName: '接入层', domain: 'access' },
  onu: { layer: 4, layerName: '接入层', domain: 'access' },
  pon_port: { layer: 4, layerName: '接入层', domain: 'access' },
  ip_pool: { layer: 5, layerName: '逻辑层', domain: 'logic' },
  protection_group: { layer: 5, layerName: '逻辑层', domain: 'logic' },
  ring: { layer: 5, layerName: '逻辑层', domain: 'logic' },
  igp_domain: { layer: 5, layerName: '逻辑层', domain: 'logic' },
  vpn_instance: { layer: 5, layerName: '逻辑层', domain: 'logic' },
  customer: { layer: 6, layerName: '业务层', domain: 'service' },
  service: { layer: 6, layerName: '业务层', domain: 'service' },
  leased_line: { layer: 6, layerName: '业务层', domain: 'service' },
};

/**
 * 层号 → 层名索引（#113-B：从 TYPE_META 单一事实源机械派生，禁止手抄——
 * GraphAnalysisTab 收敛前持有的第二份 L0-L6 层名副本已词形漂移，此后层名
 * 只有 TYPE_META 一处可改，闭集对齐断言在 schema.test.ts 钉死）。
 * 同层多类型时以源表出现顺序的最后一个为准（层内 layerName 本应同词）。
 */
export const LAYER_ID_NAMES: Record<number, string> = {};
for (const meta of Object.values(TYPE_META)) {
  LAYER_ID_NAMES[meta.layer] = meta.layerName;
}

/**
 * 层号 → 展示名「L{n} {层名}」；未知层回退「L{n}」数字形态——与后端
 * mapping.LayerName 的未知层回退同语义（错误可见而非静默空白）。
 */
export function layerIdName(layer: number): string {
  const name = LAYER_ID_NAMES[layer];
  return name === undefined ? `L${layer}` : `L${layer} ${name}`;
}
