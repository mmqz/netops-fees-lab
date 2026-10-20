/**
 * 拓扑工作台共享类型与小工具：
 * - 画布 ↔ 右侧表格的双向选中联动（SelectedEntity）；
 * - 从拓扑数据派生设备/接口/链路视图行（链路数、IPv4 数等聚合）。
 */

import type { SimEntityStatus, SimTopology, TopologyEdge, TopologyNode } from '../../api/types';
import { LAB_I_ERR_ADDR, LAB_I_ERR_BOTH, LAB_I_ERR_PREFIX } from '../../i18n';

/**
 * 选中实体：画布点击或表格行点击时互相定位。
 * #51 扩展：layerNode / layerLink 是图层覆盖层的只读实体（基站簇、OLT、
 * 客户、逻辑电路边……）——详情呈现于 LabWorkspace 的图层信息面板，
 * 不进入演练操作（操作对话框仍只面向 device/interface/link）。
 */
export interface SelectedEntity {
  kind: 'device' | 'interface' | 'link' | 'layerNode' | 'layerLink';
  id: string;
}

/** 接口视图行（接口表格 + 操作对话框的选项上下文）。 */
export interface InterfaceRow {
  id: string;
  deviceId: string;
  deviceCode: string;
  name: string;
  status: SimEntityStatus;
  ipv4Addr: string | null;
  ipv4Prefix: number | null;
  linkCount: number;
}

/**
 * 判断接口是否未配置 IPv4。线上的未配置表示是空串 + 前缀 -1
 * （engine 存储约定直出），同时容忍 null/undefined 以兼容旧约定。
 */
export function isUnconfiguredIPv4(addr: string | null | undefined, prefix: number | null | undefined): boolean {
  return addr === null || addr === undefined || addr === '' || prefix === null || prefix === undefined || prefix < 0;
}

/** 链路视图行。 */
export interface LinkRow {
  id: string;
  label: string;
  status: SimEntityStatus;
  aInterfaceId: string;
  aDeviceCode: string;
  aInterfaceName: string;
  zInterfaceId: string;
  zDeviceCode: string;
  zInterfaceName: string;
}

/** 拓扑派生视图：供右侧各标签页与执行操作对话框复用。 */
export interface TopologyView {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  interfaces: InterfaceRow[];
  links: LinkRow[];
  linkCountByInterface: Map<string, number>;
}

export function buildTopologyView(topology: SimTopology | null): TopologyView {
  const nodes = topology?.nodes ?? [];
  const edges = topology?.edges ?? [];

  const linkCountByInterface = new Map<string, number>();
  for (const edge of edges) {
    linkCountByInterface.set(edge.aInterfaceId, (linkCountByInterface.get(edge.aInterfaceId) ?? 0) + 1);
    linkCountByInterface.set(edge.zInterfaceId, (linkCountByInterface.get(edge.zInterfaceId) ?? 0) + 1);
  }

  const interfaces: InterfaceRow[] = [];
  for (const node of nodes) {
    for (const iface of node.interfaces) {
      interfaces.push({
        id: iface.id,
        deviceId: node.id,
        deviceCode: node.code,
        name: iface.name,
        status: iface.status,
        ipv4Addr: isUnconfiguredIPv4(iface.ipv4Addr, iface.ipv4Prefix) ? null : iface.ipv4Addr,
        ipv4Prefix: isUnconfiguredIPv4(iface.ipv4Addr, iface.ipv4Prefix) ? null : iface.ipv4Prefix,
        linkCount: linkCountByInterface.get(iface.id) ?? 0,
      });
    }
  }

  const interfaceById = new Map(interfaces.map((iface) => [iface.id, iface]));

  const links: LinkRow[] = edges.map((edge) => {
    const aIface = interfaceById.get(edge.aInterfaceId);
    const zIface = interfaceById.get(edge.zInterfaceId);
    return {
      id: edge.id,
      label: edge.label,
      status: edge.status,
      aInterfaceId: edge.aInterfaceId,
      aDeviceCode: edge.aDeviceCode,
      aInterfaceName: aIface?.name ?? '',
      zInterfaceId: edge.zInterfaceId,
      zDeviceCode: edge.zDeviceCode,
      zInterfaceName: zIface?.name ?? '',
    };
  });

  return { nodes, edges, interfaces, links, linkCountByInterface };
}

/**
 * 点分十进制 IPv4 校验（四段 0–255）。
 * 每段拒绝前导零（"0" 合法，"01"/"010" 非法）——与 Go netip.ParseAddr
 * 的严格规则对齐，否则客户端放过、服务端 400（AUD2-2）。
 */
export function isValidIpv4(value: string): boolean {
  const parts = value.split('.');
  if (parts.length !== 4) return false;
  return parts.every((part) => {
    if (!/^\d{1,3}$/.test(part)) return false;
    if (part.length > 1 && part.startsWith('0')) return false;
    const num = Number(part);
    return num >= 0 && num <= 255;
  });
}

/**
 * 可选 IPv4 地址+前缀对的统一校验（AUD2-1/FX2-1）：
 * - 两者均空 → 合法（表示不配置/清除）；
 * - 只填其一 → LAB_I_ERR_BOTH；地址非 dotted-quad → LAB_I_ERR_ADDR；
 * - 前缀非 0–32 十进制整数 → LAB_I_ERR_PREFIX。
 * 纯函数：OpsDialog 与 InterfaceConfigDialog 共用，保证两处行为一致。
 * （#96 起本模块随仓库其余纯函数一起由 vitest 覆盖——见 selection.test.ts。）
 */
export function validateIpv4Pair(addr: string, prefix: string): string | null {
  if (addr.trim() === '' && prefix.trim() === '') return null;
  if (addr.trim() === '' || prefix.trim() === '') return LAB_I_ERR_BOTH;
  if (!isValidIpv4(addr.trim())) return LAB_I_ERR_ADDR;
  // #96: 十进制字面量校验——Number() 会把 "0x1"(→1)、"1e1"(→10)、" 5"(→5)
  // 放行，与 isValidIpv4 对齐的 Go netip.ParsePrefix 语义（strconv.Atoi 的
  // 纯十进制）把它们全部拒绝；前缀解析漂移会让客户端放过、服务端 400。
  const p = prefix.trim();
  if (!/^\d{1,3}$/.test(p)) return LAB_I_ERR_PREFIX;
  const prefixNum = Number(p);
  if (prefixNum < 0 || prefixNum > 32) return LAB_I_ERR_PREFIX;
  return null;
}
