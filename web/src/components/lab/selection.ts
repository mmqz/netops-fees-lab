/**
 * 拓扑工作台共享类型与小工具：
 * - 画布 ↔ 右侧表格的双向选中联动（SelectedEntity）；
 * - 从拓扑数据派生设备/接口/链路视图行（链路数、IPv4 数等聚合）。
 */

import type { SimEntityStatus, SimTopology, TopologyEdge, TopologyNode } from '../../api/types';

/** 选中实体：画布点击或表格行点击时互相定位。 */
export interface SelectedEntity {
  kind: 'device' | 'interface' | 'link';
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
        ipv4Addr: iface.ipv4Addr,
        ipv4Prefix: iface.ipv4Prefix,
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

/** 点分十进制 IPv4 校验（四段 0–255）。 */
export function isValidIpv4(value: string): boolean {
  const parts = value.split('.');
  if (parts.length !== 4) return false;
  return parts.every((part) => {
    if (!/^\d{1,3}$/.test(part)) return false;
    const num = Number(part);
    return num >= 0 && num <= 255;
  });
}
