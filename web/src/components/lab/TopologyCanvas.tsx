/**
 * 拓扑画布（Cytoscape 3.32，无插件）：
 * - 节点=设备（编码 + 第二行名称），up 蓝 #2F5FE0 / down 红 #D64545；
 * - 边=链路（aDeviceId→zDeviceId），up 实线 #5C6B85 / down 虚线 #B9C2D4，边标签=链路标签；
 * - 默认 cose 力导向布局，工具栏可切换 力导/网格/环形；
 * - 点击节点/边 → onSelect（右侧面板切换标签并定位行）；
 * - ResizeObserver 跟随容器尺寸 cy.resize()；卸载时 cy.destroy()；
 * - 拓扑数据更新时做增量同步（保留既有节点位置，仅新增节点参与重排）。
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import cytoscape from 'cytoscape';
import type { Core, ElementDefinition, LayoutOptions, StylesheetJson } from 'cytoscape';
import type { SimTopology } from '../../api/types';
import type { SelectedEntity } from './selection';
import {
  LAB_CANVAS_EMPTY,
  LAB_CANVAS_HINT,
  LAB_CANVAS_LABEL,
  LAB_LAYOUT,
  LAB_LAYOUT_CIRCLE,
  LAB_LAYOUT_FORCE,
  LAB_LAYOUT_GRID,
  LOADING_TEXT,
} from '../../i18n';
import { IconLayoutCircle, IconLayoutForce, IconLayoutGrid } from '../icons';

type LayoutKind = 'force' | 'grid' | 'circle';

const LAYOUT_NAMES: Record<LayoutKind, string> = {
  force: 'cose',
  grid: 'grid',
  circle: 'circle',
};

const NODE_UP_FILL = '#2F5FE0';
const NODE_DOWN_FILL = '#D64545';
const EDGE_UP_COLOR = '#5C6B85';
const EDGE_DOWN_COLOR = '#B9C2D4';

const CY_STYLE: Array<{ selector: string; style: Record<string, string | number> }> = [
  {
    selector: 'node',
    style: {
      shape: 'round-rectangle',
      'background-color': NODE_UP_FILL,
      'border-width': 0,
      label: 'data(label)',
      color: '#FFFFFF',
      'font-size': 10,
      'font-family': '"Segoe UI", system-ui, "PingFang SC", "Microsoft YaHei", sans-serif',
      'text-valign': 'center',
      'text-halign': 'center',
      'text-wrap': 'wrap',
      'text-max-width': 90,
      width: 56,
      height: 40,
      'min-zoomed-font-size': 6,
    },
  },
  {
    selector: 'node[status = "down"]',
    style: {
      'background-color': NODE_DOWN_FILL,
    },
  },
  {
    selector: 'node:selected',
    style: {
      'border-width': 3,
      'border-color': '#C77D06',
    },
  },
  {
    selector: 'edge',
    style: {
      width: 2,
      'line-color': EDGE_UP_COLOR,
      'target-arrow-shape': 'none',
      'curve-style': 'haystack',
      'haystack-radius': 0.4,
    },
  },
  {
    selector: 'edge[status = "down"]',
    style: {
      'line-color': EDGE_DOWN_COLOR,
      'line-style': 'dashed',
    },
  },
  {
    selector: 'edge[label != ""]',
    style: {
      label: 'data(label)',
      color: '#5C6B85',
      'font-size': 7,
      'text-background-color': '#FFFFFF',
      'text-background-opacity': 0.85,
      'text-background-padding': 2,
      'text-rotation': 'autorotate',
      'min-zoomed-font-size': 8,
    },
  },
  {
    selector: 'edge:selected',
    style: {
      width: 4,
    },
  },
];

interface TopologyCanvasProps {
  topology: SimTopology | null;
  selected: SelectedEntity | null;
  onSelect: (entity: SelectedEntity) => void;
}

function nodeLabel(code: string, name: string): string {
  return name === '' ? code : `${code}\n${name}`;
}

function buildElements(topology: SimTopology): ElementDefinition[] {
  const nodes: ElementDefinition[] = topology.nodes.map((node) => ({
    group: 'nodes',
    data: {
      id: node.id,
      kind: 'device',
      label: nodeLabel(node.code, node.name),
      status: node.status,
      code: node.code,
    },
  }));
  const edges: ElementDefinition[] = topology.edges.map((edge) => ({
    group: 'edges',
    data: {
      id: edge.id,
      kind: 'link',
      source: edge.aDeviceId,
      target: edge.zDeviceId,
      status: edge.status,
      label: edge.label,
    },
  }));
  return [...nodes, ...edges];
}

export function TopologyCanvas({ topology, selected, onSelect }: TopologyCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const cyRef = useRef<Core | null>(null);
  const onSelectRef = useRef(onSelect);
  const [layout, setLayout] = useState<LayoutKind>('force');

  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

  // —— 实例生命周期：只创建一次 ——
  useEffect(() => {
    if (containerRef.current === null) return undefined;
    const cy = cytoscape({
      container: containerRef.current,
      elements: [],
      style: CY_STYLE as unknown as StylesheetJson,
      layout: { name: LAYOUT_NAMES.force },
      wheelSensitivity: 0.2,
      autounselectify: true,
    });
    cy.on('tap', 'node', (event) => {
      const node = event.target;
      onSelectRef.current({ kind: 'device', id: node.id() });
    });
    cy.on('tap', 'edge', (event) => {
      const edge = event.target;
      onSelectRef.current({ kind: 'link', id: edge.id() });
    });
    cyRef.current = cy;

    const observer =
      typeof ResizeObserver === 'undefined'
        ? null
        : new ResizeObserver(() => {
            cy.resize();
          });
    if (observer !== null && containerRef.current !== null) {
      observer.observe(containerRef.current);
    }
    const onWindowResize = (): void => {
      cy.resize();
    };
    window.addEventListener('resize', onWindowResize);

    return () => {
      window.removeEventListener('resize', onWindowResize);
      observer?.disconnect();
      cy.destroy();
      cyRef.current = null;
    };
  }, []);

  // —— 数据增量同步：删除消失元素、更新既有元素、新增元素参与重排 ——
  useEffect(() => {
    const cy = cyRef.current;
    if (cy === null) return;
    const elements = topology === null ? [] : buildElements(topology);
    const wanted = new Set(elements.map((el) => el.data.id as string));

    let added = 0;
    cy.batch(() => {
      cy.elements().forEach((ele) => {
        if (!wanted.has(ele.id())) {
          ele.remove();
        }
      });
      for (const spec of elements) {
        const existing = cy.getElementById(spec.data.id as string);
        if (existing.nonempty()) {
          existing.data(spec.data);
        } else {
          cy.add(spec);
          added += 1;
        }
      }
    });
    if (added > 0) {
      cy.layout({ name: LAYOUT_NAMES[layout], animate: true, animationDuration: 300 } as unknown as LayoutOptions).run();
      cy.fit(undefined, 40);
    }
  }, [topology, layout]);

  // —— 选中联动（右侧表格 → 画布） ——
  useEffect(() => {
    const cy = cyRef.current;
    if (cy === null) return;
    cy.elements().unselect();
    if (selected === null) return;
    const ele = cy.getElementById(selected.id);
    if (ele.nonempty()) {
      ele.select();
    }
  }, [selected, topology]);

  // —— 工具栏：重跑布局 ——
  function runLayout(kind: LayoutKind): void {
    const cy = cyRef.current;
    setLayout(kind);
    if (cy === null) return;
    cy.layout({ name: LAYOUT_NAMES[kind], animate: true, animationDuration: 300 } as unknown as LayoutOptions).run();
    cy.fit(undefined, 40);
  }

  const empty = useMemo(
    () => topology === null || (topology.nodes.length === 0 && topology.edges.length === 0),
    [topology],
  );

  return (
    <div className="lab-canvas-card card" aria-label={LAB_CANVAS_LABEL}>
      <div className="lab-canvas-toolbar">
        <span className="lab-canvas-toolbar-label">{LAB_LAYOUT}</span>
        <button
          type="button"
          className={`btn btn--sm ${layout === 'force' ? 'btn--primary' : 'btn--secondary'}`}
          onClick={() => runLayout('force')}
        >
          <IconLayoutForce />
          {LAB_LAYOUT_FORCE}
        </button>
        <button
          type="button"
          className={`btn btn--sm ${layout === 'grid' ? 'btn--primary' : 'btn--secondary'}`}
          onClick={() => runLayout('grid')}
        >
          <IconLayoutGrid />
          {LAB_LAYOUT_GRID}
        </button>
        <button
          type="button"
          className={`btn btn--sm ${layout === 'circle' ? 'btn--primary' : 'btn--secondary'}`}
          onClick={() => runLayout('circle')}
        >
          <IconLayoutCircle />
          {LAB_LAYOUT_CIRCLE}
        </button>
        <span className="lab-canvas-toolbar-hint">{LAB_CANVAS_HINT}</span>
      </div>
      <div className="lab-canvas-wrap">
        <div ref={containerRef} className="lab-canvas" />
        {empty && (
          <div className="lab-canvas-empty">{topology === null ? LOADING_TEXT : LAB_CANVAS_EMPTY}</div>
        )}
      </div>
    </div>
  );
}
