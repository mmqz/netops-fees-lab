/**
 * 拓扑画布（Cytoscape 3.32，无插件）：
 * - 深色工程面板（W5）：画布底色近黑深蓝 + 点阵网格，节点=浅色填充/停机红、
 *   描边带状态色；边=链路 up 实线灰蓝 / down 亮红 3px 虚线（故障必须看得见，
 *   AUD-4 P1-2）、选中 = 强调蓝；边标签=链路标签；
 * - 节点=徽章式代码牌（FX-4）：64×44 圆角矩形，内部渲染 DEV-001 等短代码
 *   （等宽 12px 深字浅底 / 停机红底白字）——完整中文名不再作为节点下方长标签
 *   （27 节点聚簇时的标签噪音，VLM 砖块感批评），改为 hover 时左上角悬浮信息条
 *   「DEV-001 · 杭州-核心路由器-001 · 运行中」；点击节点仍由右侧面板出详情；
 * - 节点带停机接口时描边变琥珀（接口故障图例项对应，W2）；
 * - 边（FX-4）：bezier 曲线 + 控制点步长 60，up 2.5px 提亮灰蓝，
 *   down 3px [8,6] 虚线亮红，选中 3.5px 强调蓝；
 * - 右上角悬浮视图控制（放大/缩小/适配/重置 1:1）+ 左下角图例（W2，FX-4 加呼吸感），
 *   overlay 容器 pointer-events:none，仅控件自身可点——平移/框选不受遮挡；
 * - 默认 cose 力导向布局，工具栏可切换 力导/网格/环形；布局选择按工作台
 *   持久化到 sessionStorage（P2）；prefers-reduced-motion 时关闭布局动画（P2）；
 * - 点击节点/边 → onSelect（右侧面板切换标签并定位行）；Cytoscape 无逐元素
 *   cursor 样式，改用容器光标在节点/边上切 pointer（可点提示，FX-4）；
 * - ResizeObserver 跟随容器尺寸 cy.resize()；卸载时 cy.destroy()；
 * - 拓扑数据更新时做增量同步（保留既有节点位置，仅新增节点及其邻域参与重排）。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import cytoscape from 'cytoscape';
import type { CollectionReturnValue, Core, ElementDefinition, Layouts, LayoutOptions, StylesheetJson } from 'cytoscape';
import type { SimEntityStatus, SimTopology } from '../../api/types';
import type { SelectedEntity } from './selection';
import {
  LAB_CANVAS_EMPTY,
  LAB_CANVAS_FIT,
  LAB_CANVAS_HINT,
  LAB_CANVAS_LABEL,
  LAB_CANVAS_LEGEND_LABEL,
  LAB_CANVAS_RESET,
  LAB_CANVAS_ZOOM_IN,
  LAB_CANVAS_ZOOM_OUT,
  LAB_CANVAS_ZOOM_HINT,
  LAB_LAYOUT,
  LAB_LAYOUT_CIRCLE,
  LAB_LAYOUT_FORCE,
  LAB_LAYOUT_GRID,
  LAB_LEGEND_DEVICE_DOWN,
  LAB_LEGEND_DEVICE_UP,
  LAB_LEGEND_IFACE_FAULT,
  LAB_LEGEND_LINK_DOWN,
  LAB_LEGEND_LINK_UP,
  LOADING_TEXT,
  SIM_STATUS_LABELS,
} from '../../i18n';
import {
  IconFit,
  IconLayoutCircle,
  IconLayoutForce,
  IconLayoutGrid,
  IconResetView,
  IconZoomIn,
  IconZoomOut,
} from '../icons';

type LayoutKind = 'force' | 'grid' | 'circle';

const LAYOUT_NAMES: Record<LayoutKind, string> = {
  force: 'cose',
  grid: 'grid',
  circle: 'circle',
};

// —— 深色画布调色板（W5；仅画布区域深色，页面其余保持浅色主题）——
// CANVAS_BG 必须与 global.css 中 .lab-canvas-wrap 的底色一致（边标签底衬用）。

/** 画布底色（边标签文字底衬用，与 CSS 深色面板一致）。 */
const CANVAS_BG = '#0e1626';

const NODE_UP_FILL = '#dce6f7';
const NODE_UP_BORDER = '#8fa6ce';
/** 停机红底（FX-4 加深一档：内部白字 4.76:1 ≥ AA；图例 legend-dot--down 同步）。 */
const NODE_DOWN_FILL = '#d13c33';
const NODE_DOWN_BORDER = '#ff9d95';
/** 带停机接口的节点：琥珀描边（图例「接口故障」）。 */
const NODE_IFACE_FAULT_BORDER = '#f2a93b';
const NODE_SELECTED_BORDER = '#7da2ff';

/** 节点内部代码字色（FX-4）：up=深藏青（浅底 ≈9:1）/ down=纯白（红底）。 */
const NODE_CODE_COLOR = '#25395c';
const NODE_DOWN_CODE_COLOR = '#ffffff';

/** FX-4：up 边提亮（#5d7093 → #7387ab，深底 4.97:1），消除「细且平淡」。 */
const EDGE_UP_COLOR = '#7387ab';
/** 亮红 3px 虚线：故障链路必须醒目（对比旧 #B9C2D4 1.79:1）。 */
const EDGE_DOWN_COLOR = '#f2554a';
const EDGE_SELECTED_COLOR = '#4f8ef7';

const EDGE_LABEL_COLOR = '#9fb2ce';

/** FX-4：节点 = 代码徽章（64×44 圆角矩形，内部短代码）。 */
const NODE_W = 64;
const NODE_H = 44;
const NODE_CORNER_RADIUS = 6;
const NODE_CODE_FONT =
  'ui-monospace, "SF Mono", "Cascadia Mono", "Roboto Mono", Consolas, monospace';
/** FX-4：bezier 控制点步长（换掉 haystack，曲线更顺、连线更有存在感）。 */
const EDGE_CONTROL_STEP = 60;

/** FX-4：悬浮信息条的节点间切换去抖（ms）——避免 node→node 一帧闪空。 */
const HOVER_CLEAR_DELAY_MS = 60;

/** fit 后的缩放下限：保证默认视图下节点/标签可读（P0 标签可见性修复）。 */
const MIN_ZOOM = 0.55;

/** 布局动画时长（ms）。 */
const LAYOUT_ANIMATION_MS = 300;

/** 布局完成后 fit 的留白（px）。 */
const FIT_PADDING = 40;

/** 视图按钮单步缩放系数。 */
const ZOOM_STEP = 1.25;

function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

/** 布局选择按工作台持久化（sessionStorage，刷新后保留）。 */
function readPersistedLayout(persistKey: string | undefined): LayoutKind {
  if (persistKey === undefined || typeof sessionStorage === 'undefined') return 'force';
  try {
    const value = sessionStorage.getItem(`netops-lab.canvas.${persistKey}`);
    if (value === 'force' || value === 'grid' || value === 'circle') return value;
  } catch {
    // 隐私模式 / 配额异常：静默回退默认布局
  }
  return 'force';
}

function writePersistedLayout(persistKey: string | undefined, layout: LayoutKind): void {
  if (persistKey === undefined || typeof sessionStorage === 'undefined') return;
  try {
    sessionStorage.setItem(`netops-lab.canvas.${persistKey}`, layout);
  } catch {
    // 写失败不影响功能
  }
}

const CY_STYLE: Array<{ selector: string; style: Record<string, string | number | number[]> }> = [
  {
    selector: 'node',
    style: {
      shape: 'round-rectangle',
      'corner-radius': NODE_CORNER_RADIUS,
      'background-color': NODE_UP_FILL,
      'border-width': 1.5,
      'border-color': NODE_UP_BORDER,
      // FX-4：节点内部只渲染短代码（DEV-001，等宽深字浅底居中）；
      // 完整中文名不再占节点下方（长名聚簇噪音）→ hover 左上角信息条
      label: 'data(code)',
      color: NODE_CODE_COLOR,
      'font-size': 12,
      'font-weight': 600,
      'font-family': NODE_CODE_FONT,
      'text-valign': 'center',
      'text-halign': 'center',
      width: NODE_W,
      height: NODE_H,
      // 0 = 不因缩小而隐藏标签
      'min-zoomed-font-size': 0,
    },
  },
  {
    selector: 'node[status = "down"]',
    style: {
      'background-color': NODE_DOWN_FILL,
      'border-color': NODE_DOWN_BORDER,
      color: NODE_DOWN_CODE_COLOR,
    },
  },
  {
    selector: 'node[ifaceFault = "true"]',
    style: {
      'border-width': 2.5,
      'border-color': NODE_IFACE_FAULT_BORDER,
    },
  },
  {
    selector: 'node:selected',
    style: {
      'border-width': 3,
      'border-color': NODE_SELECTED_BORDER,
    },
  },
  {
    selector: 'edge',
    style: {
      width: 2.5,
      'line-color': EDGE_UP_COLOR,
      'target-arrow-shape': 'none',
      'curve-style': 'bezier',
      'control-point-step-size': EDGE_CONTROL_STEP,
    },
  },
  {
    selector: 'edge[status = "down"]',
    style: {
      'line-color': EDGE_DOWN_COLOR,
      'line-style': 'dashed',
      'line-dash-pattern': [8, 6],
      width: 3,
    },
  },
  {
    selector: 'edge[label != ""]',
    style: {
      label: 'data(label)',
      color: EDGE_LABEL_COLOR,
      'font-size': 8,
      'text-background-color': CANVAS_BG,
      'text-background-opacity': 0.85,
      'text-background-padding': 2,
      'text-rotation': 'autorotate',
      'min-zoomed-font-size': 0,
    },
  },
  {
    selector: 'edge:selected',
    style: {
      width: 3.5,
      'line-color': EDGE_SELECTED_COLOR,
    },
  },
];

interface TopologyCanvasProps {
  topology: SimTopology | null;
  selected: SelectedEntity | null;
  onSelect: (entity: SelectedEntity) => void;
  /** 布局持久化键（工作台 id）；不提供则不持久化。 */
  persistKey?: string;
}

/** 悬浮信息条内容（FX-4）：节点 code / 完整名 / 状态。 */
interface HoverInfo {
  code: string;
  name: string;
  status: SimEntityStatus;
}

function buildElements(topology: SimTopology): ElementDefinition[] {
  const nodes: ElementDefinition[] = topology.nodes.map((node) => ({
    group: 'nodes',
    data: {
      id: node.id,
      kind: 'device',
      // FX-4：code 进节点内部徽章；name 供 hover 信息条（不再渲染为下方长标签）
      code: node.code,
      name: node.name,
      status: node.status,
      // 接口故障：任一接口停机 → 琥珀描边（图例「接口故障」）
      ifaceFault: node.interfaces.some((iface) => iface.status === 'down') ? 'true' : 'false',
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

export function TopologyCanvas({ topology, selected, onSelect, persistKey }: TopologyCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const cyRef = useRef<Core | null>(null);
  const onSelectRef = useRef(onSelect);
  /** 当前布局任务（防并发：新布局启动前先停旧的）。 */
  const layoutRunRef = useRef<Layouts | null>(null);
  /** FX-4：hover 信息条/容器光标的事件清理（实例卸载时调用）。 */
  const hoverCleanupRef = useRef<(() => void) | null>(null);
  const [layout, setLayout] = useState<LayoutKind>(() => readPersistedLayout(persistKey));
  /** 悬浮节点信息（FX-4）：左上角信息条「code · 名称 · 状态」。 */
  const [hoverInfo, setHoverInfo] = useState<HoverInfo | null>(null);

  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

  useEffect(() => {
    writePersistedLayout(persistKey, layout);
  }, [persistKey, layout]);

  /** 布局完成后再 fit（cose 为异步仿真、grid/circle 有 300ms 动画，立即 fit 抓到的是布局前包围盒）。 */
  function fitToView(cy: Core): void {
    cy.fit(undefined, FIT_PADDING);
    // minZoom 在构造时已设为 0.55；fit 受其约束，这里再显式钳一次作保险
    if (cy.zoom() < MIN_ZOOM) {
      cy.zoom(MIN_ZOOM);
      cy.center();
    }
  }

  /** 启动布局：先停掉可能仍在跑的旧布局（AUD2-7 并发防护）；reduced-motion 时关闭动画。 */
  const startLayout = useCallback(
    (cy: Core, eles?: CollectionReturnValue): void => {
      layoutRunRef.current?.stop();
      const target = eles ?? cy.elements();
      if (target.length === 0) return;
      const reduceMotion = prefersReducedMotion();
      const run = target.layout({
        name: LAYOUT_NAMES[layout],
        animate: !reduceMotion,
        animationDuration: reduceMotion ? 0 : LAYOUT_ANIMATION_MS,
        stop: () => {
          if (layoutRunRef.current === run) {
            layoutRunRef.current = null;
          }
          fitToView(cy);
        },
      } as unknown as LayoutOptions);
      layoutRunRef.current = run;
      run.run();
    },
    [layout],
  );

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
      // 缩放下限：fit/滚轮都不会低于该值，保证 27 节点拓扑的默认视图下标签可读
      minZoom: MIN_ZOOM,
    });
    cy.on('tap', 'node', (event) => {
      const node = event.target;
      onSelectRef.current({ kind: 'device', id: node.id() });
    });
    cy.on('tap', 'edge', (event) => {
      const edge = event.target;
      onSelectRef.current({ kind: 'link', id: edge.id() });
    });

    // —— FX-4：hover 节点 → 左上角信息条；节点/边上光标切 pointer ——
    // Cytoscape 无逐元素 cursor 样式属性，用容器光标切换给出可点提示。
    // mouseout 去抖 60ms：避免 node→node 移动时信息条闪空一帧。
    let hoverClearTimer: ReturnType<typeof setTimeout> | null = null;
    const container = containerRef.current;
    const clearHover = (): void => {
      if (hoverClearTimer !== null) {
        clearTimeout(hoverClearTimer);
        hoverClearTimer = null;
      }
      setHoverInfo(null);
    };
    cy.on('mouseover', 'node', (event) => {
      if (hoverClearTimer !== null) {
        clearTimeout(hoverClearTimer);
        hoverClearTimer = null;
      }
      const node = event.target;
      setHoverInfo({
        code: node.data('code') as string,
        name: node.data('name') as string,
        status: node.data('status') as SimEntityStatus,
      });
    });
    cy.on('mouseout', 'node', () => {
      if (hoverClearTimer !== null) clearTimeout(hoverClearTimer);
      hoverClearTimer = setTimeout(() => {
        hoverClearTimer = null;
        setHoverInfo(null);
      }, HOVER_CLEAR_DELAY_MS);
    });
    if (container !== null) {
      const setPointerCursor = (): void => {
        container.style.cursor = 'pointer';
      };
      const resetCursor = (): void => {
        container.style.cursor = '';
      };
      // 指针整层离开画布：收信息条 + 复位光标（此时 node mouseout 不一定触发）
      const onContainerMouseLeave = (): void => {
        clearHover();
        resetCursor();
      };
      cy.on('mouseover', 'node, edge', setPointerCursor);
      cy.on('mouseout', 'node, edge', resetCursor);
      container.addEventListener('mouseleave', onContainerMouseLeave);
      hoverCleanupRef.current = () => {
        container.removeEventListener('mouseleave', onContainerMouseLeave);
        resetCursor();
      };
    }
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
      hoverCleanupRef.current?.();
      hoverCleanupRef.current = null;
      if (hoverClearTimer !== null) clearTimeout(hoverClearTimer);
      window.removeEventListener('resize', onWindowResize);
      observer?.disconnect();
      layoutRunRef.current?.stop();
      layoutRunRef.current = null;
      cy.destroy();
      cyRef.current = null;
    };
  }, []);

  // —— 数据增量同步：删除消失元素、更新既有元素、仅新增元素及其邻域参与重排（保留既有位置） ——
  useEffect(() => {
    const cy = cyRef.current;
    if (cy === null) return;
    const elements = topology === null ? [] : buildElements(topology);
    const wanted = new Set(elements.map((el) => el.data.id as string));

    const addedIds = new Set<string>();
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
          addedIds.add(spec.data.id as string);
        }
      }
    });
    if (addedIds.size > 0) {
      const added = cy.elements().filter((ele) => addedIds.has(ele.id()));
      const scope = added.union(added.neighborhood());
      startLayout(cy, scope);
    }
  }, [topology, layout, startLayout]);

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

  // —— 工具栏：全量重跑布局 ——
  function runLayout(kind: LayoutKind): void {
    const cy = cyRef.current;
    setLayout(kind);
    if (cy === null) return;
    startLayout(cy);
  }

  // —— 视图控制（W2）——
  function zoomBy(factor: number): void {
    const cy = cyRef.current;
    if (cy === null) return;
    const next = cy.zoom() * factor;
    cy.zoom(Math.min(Math.max(next, cy.minZoom()), cy.maxZoom()));
  }

  function fitView(): void {
    const cy = cyRef.current;
    if (cy === null) return;
    fitToView(cy);
  }

  function resetView(): void {
    const cy = cyRef.current;
    if (cy === null) return;
    cy.zoom(1);
    cy.center();
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
      <div className="lab-canvas-wrap lab-canvas-wrap--dark">
        <div ref={containerRef} className="lab-canvas" />
        {/* 视图控制（右上角）与图例（左下角）：容器不拦截指针，仅控件可点 */}
        <div className="lab-canvas-overlay lab-canvas-controls" role="toolbar" aria-label={LAB_CANVAS_LABEL} title={LAB_CANVAS_ZOOM_HINT}>
          <button
            type="button"
            className="canvas-ctrl"
            title={LAB_CANVAS_ZOOM_IN}
            aria-label={LAB_CANVAS_ZOOM_IN}
            onClick={() => zoomBy(ZOOM_STEP)}
          >
            <IconZoomIn />
          </button>
          <button
            type="button"
            className="canvas-ctrl"
            title={LAB_CANVAS_ZOOM_OUT}
            aria-label={LAB_CANVAS_ZOOM_OUT}
            onClick={() => zoomBy(1 / ZOOM_STEP)}
          >
            <IconZoomOut />
          </button>
          <button
            type="button"
            className="canvas-ctrl"
            title={LAB_CANVAS_FIT}
            aria-label={LAB_CANVAS_FIT}
            onClick={fitView}
          >
            <IconFit />
          </button>
          <button
            type="button"
            className="canvas-ctrl"
            title={LAB_CANVAS_RESET}
            aria-label={LAB_CANVAS_RESET}
            onClick={resetView}
          >
            <IconResetView />
          </button>
        </div>
        {/* FX-4：hover 信息条（左上角，避开右上角视图控件）：code · 完整名 · 状态 */}
        {hoverInfo !== null && (
          <div className="lab-canvas-overlay lab-canvas-hoverinfo" role="status">
            <span className={`hoverinfo-dot hoverinfo-dot--${hoverInfo.status}`} aria-hidden="true" />
            <span className="hoverinfo-code">{hoverInfo.code}</span>
            <span className="hoverinfo-sep" aria-hidden="true">·</span>
            <span className="hoverinfo-name" title={hoverInfo.name}>
              {hoverInfo.name}
            </span>
            <span className="hoverinfo-sep" aria-hidden="true">·</span>
            <span className="hoverinfo-status">{SIM_STATUS_LABELS[hoverInfo.status]}</span>
          </div>
        )}
        <div className="lab-canvas-overlay lab-canvas-legend" aria-label={LAB_CANVAS_LEGEND_LABEL}>
          <span className="legend-item">
            <span className="legend-dot legend-dot--up" aria-hidden="true" />
            {LAB_LEGEND_DEVICE_UP}
          </span>
          <span className="legend-item">
            <span className="legend-dot legend-dot--down" aria-hidden="true" />
            {LAB_LEGEND_DEVICE_DOWN}
          </span>
          <span className="legend-item">
            <span className="legend-dot legend-dot--fault" aria-hidden="true" />
            {LAB_LEGEND_IFACE_FAULT}
          </span>
          <span className="legend-item">
            <span className="legend-line legend-line--up" aria-hidden="true" />
            {LAB_LEGEND_LINK_UP}
          </span>
          <span className="legend-item">
            <span className="legend-line legend-line--down" aria-hidden="true" />
            {LAB_LEGEND_LINK_DOWN}
          </span>
        </div>
        {empty && (
          <div className="lab-canvas-empty">{topology === null ? LOADING_TEXT : LAB_CANVAS_EMPTY}</div>
        )}
      </div>
    </div>
  );
}
