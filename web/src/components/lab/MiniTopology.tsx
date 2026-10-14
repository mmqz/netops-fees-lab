/**
 * 工作台拓扑缩略图（#47 诉求 4）：真实拓扑数据的轻量静态 SVG 预览——
 * 环形布局节点 + 弦线链路，up/down 沿用拓扑画布色板（蓝/红）。
 *
 * 设计约束：
 * - **不挂 Cytoscape/MapLibre 实例**：列表页可能同时显示几十张卡片，
 *   每卡一个图形引擎实例的内存/初始化开销不可接受（issue #47 明示
 *   「需评估性能」）；静态 SVG 一次渲染即定格，无交互无监听。
 * - **视口内懒加载**（IntersectionObserver，rootMargin 120px 预取）：
 *   拓扑 JSON 只为实际滚到的卡片拉取，一次成型缓存于组件 state。
 * - **确定性布局**：节点按编码排序后均匀落圆周——同一工作台任何时刻
 *   渲染的缩略图逐字节一致（可对照测试）。
 * - 节点数封顶 80（>80 取排序后前 80，链路只画两端均在显示集内的弦）：
 *   30 设备的真实演示数据全量可见，极端大工作台不退化为噪点团。
 */

import { useEffect, useRef, useState } from 'react';
import { getTopology } from '../../api/client';
import type { SimTopology, TopologyEdge, TopologyNode } from '../../api/types';
import { LAB_TILE_PREVIEW_EMPTY, LAB_TILE_PREVIEW_ERROR, LAB_TILE_PREVIEW_LOADING } from '../../i18n';

const VIEW_W = 320;
const VIEW_H = 200;
const CX = VIEW_W / 2;
const CY = VIEW_H / 2;
const R = 72;
const NODE_R = 4.5;
/** 缩略图节点显示上限（见文件头注释）。 */
const MAX_NODES = 80;

const C_UP = '#5b8fd9';
const C_DOWN = '#ff5252';
const LINK_UP = 'rgba(91, 143, 217, 0.5)';
const LINK_DOWN = 'rgba(255, 82, 82, 0.75)';

interface MiniGeometry {
  nodes: Array<{ x: number; y: number; status: string }>;
  edges: Array<{ x1: number; y1: number; x2: number; y2: number; down: boolean }>;
  totalNodes: number;
  totalEdges: number;
}

/** 环形确定性布局：节点按 id 排序均匀落圆周；链路为两端点弦线。 */
function layoutGeometry(topo: SimTopology): MiniGeometry {
  const sorted: TopologyNode[] = [...topo.nodes].sort((a, b) =>
    a.id < b.id ? -1 : a.id > b.id ? 1 : 0,
  );
  const shown = sorted.slice(0, MAX_NODES);
  const positions = new Map<string, { x: number; y: number; status: string }>();
  const nodes: MiniGeometry['nodes'] = shown.map((node, i) => {
    const angle = (2 * Math.PI * i) / Math.max(shown.length, 1) - Math.PI / 2;
    const pos = { x: CX + R * Math.cos(angle), y: CY + R * Math.sin(angle), status: node.status };
    positions.set(node.id, pos);
    return pos;
  });
  const edges: MiniGeometry['edges'] = [];
  for (const edge of topo.edges as TopologyEdge[]) {
    const a = positions.get(edge.aDeviceId);
    const z = positions.get(edge.zDeviceId);
    if (a === undefined || z === undefined) continue;
    edges.push({ x1: a.x, y1: a.y, x2: z.x, y2: z.y, down: edge.status === 'down' });
  }
  return { nodes, edges, totalNodes: sorted.length, totalEdges: topo.edges.length };
}

export function MiniTopology({ workspaceId }: { workspaceId: string }) {
  const holderRef = useRef<HTMLDivElement | null>(null);
  const startedRef = useRef(false);
  const [topo, setTopo] = useState<SimTopology | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const el = holderRef.current;
    startedRef.current = false;
    setTopo(null);
    setFailed(false);

    function start(): void {
      if (startedRef.current) return;
      startedRef.current = true;
      getTopology(workspaceId)
        .then((t) => setTopo(t))
        .catch(() => setFailed(true));
    }

    if (el === null || typeof IntersectionObserver === 'undefined') {
      start();
      return undefined;
    }
    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            io.disconnect();
            start();
          }
        }
      },
      { rootMargin: '120px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [workspaceId]);

  const geom = topo !== null ? layoutGeometry(topo) : null;

  return (
    <div ref={holderRef} className="lab-tile-preview" aria-hidden="true">
      {failed ? (
        <span className="lab-tile-placeholder">{LAB_TILE_PREVIEW_ERROR}</span>
      ) : geom === null ? (
        <span className="lab-tile-placeholder">{LAB_TILE_PREVIEW_LOADING}</span>
      ) : geom.totalNodes === 0 ? (
        <span className="lab-tile-placeholder">{LAB_TILE_PREVIEW_EMPTY}</span>
      ) : (
        <svg viewBox={`0 0 ${VIEW_W} ${VIEW_H}`} role="img">
          {geom.edges.map((e, i) => (
            <line
              key={`e${i}`}
              x1={e.x1}
              y1={e.y1}
              x2={e.x2}
              y2={e.y2}
              stroke={e.down ? LINK_DOWN : LINK_UP}
              strokeWidth={1}
            />
          ))}
          {geom.nodes.map((n, i) => (
            <circle
              key={`n${i}`}
              cx={n.x}
              cy={n.y}
              r={NODE_R}
              fill={n.status === 'down' ? C_DOWN : C_UP}
            />
          ))}
        </svg>
      )}
    </div>
  );
}
