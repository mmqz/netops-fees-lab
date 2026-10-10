/**
 * 拓扑画布（Cytoscape 3.32，无插件）—— v0.2.2 多布局 / 多交互模式升级：
 * - 深色工程面板（W5）：画布底色近黑深蓝 + 点阵网格，节点=浅色填充/停机红、
 *   描边带状态色；边=链路 up 实线灰蓝 / down 亮红 3px 虚线（故障必须看得见）、
 *   选中 = 强调蓝；边标签=链路标签（可经工具带开关关闭，聚簇时降噪）；
 * - 节点=徽章式代码牌（FX-4）：64×44 圆角矩形，内部渲染 DEV-001 等短代码；
 *   完整中文名改为 hover 时左上角悬浮信息条「DEV-001 · 杭州-核心路由器-001 · 运行中」；
 * - 六种布局模式（v0.2.2，用户要求「起码能切换各种模式」）：
 *   力导 cose / 层级 breadthfirst（每个连通分量取最高度节点为根，自上而下树，
 *   对应核心-汇聚-接入分层拓扑）/ 同心圆 concentric（按度数分环，枢纽居中）/
 *   网格 grid / 环形 circle / 锁定 freeze（停用自动布局，新节点螺旋落位，
 *   用户拖拽排布后保持）；
 * - 三种交互模式（v0.2.2）：选择（点击联动右侧表格）/ 路径追踪（依次点击
 *   两台设备，aStar 最短路径高亮 + 其余元素压暗，顶部横幅显示跳数与途经；
 *   不可达时红色横幅提示）/ 邻域聚焦（点击设备，一跳邻域之外全部压暗）；
 *   Esc 或点击画布空白处清除路径/聚焦；
 * - 迷你导航图（v0.2.2，右下角）：节点位置缩略 + 当前视口矩形，点击/拖动
 *   直接平移主视图（工业控制台标配，防大图迷航）；
 * - hover 微交互：节点/边描边与线宽过渡动画（transition-property 120ms）；
 * - 悬浮视图控制（放大/缩小/适配/重置 1:1）+ 左下角图例 + 右下角缩略图；
 *   overlay 容器 pointer-events:none，仅控件自身可点——平移/框选不受遮挡；
 * - 布局 + 交互模式 + 标签开关按工作台持久化到 sessionStorage（v2 JSON；
 *   兼容读取 v1 纯布局字符串）；prefers-reduced-motion 时关闭布局动画；
 * - 点击节点/边 → onSelect（右侧面板切换标签并定位行）；数据增量同步
 *   （保留既有节点位置，仅新增节点及其邻域参与重排）；卸载时 cy.destroy()。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import cytoscape from 'cytoscape';
import type { CollectionReturnValue, Core, ElementDefinition, Layouts, LayoutOptions, NodeSingular, StylesheetJson } from 'cytoscape';
import type { SimEntityStatus, SimTopology } from '../../api/types';
import type { SelectedEntity } from './selection';
import {
  LAB_CANVAS_EMPTY,
  LAB_CANVAS_FIT,
  LAB_CANVAS_HINT,
  LAB_CANVAS_LABEL,
  LAB_CANVAS_LABELS,
  LAB_CANVAS_LEGEND_LABEL,
  LAB_CANVAS_RESET,
  LAB_CANVAS_ZOOM_IN,
  LAB_CANVAS_ZOOM_OUT,
  LAB_CANVAS_ZOOM_HINT,
  LAB_LAYOUT,
  LAB_LAYOUT_CIRCLE,
  LAB_LAYOUT_CONCENTRIC,
  LAB_LAYOUT_FORCE,
  LAB_LAYOUT_FREEZE,
  LAB_LAYOUT_GRID,
  LAB_LAYOUT_HIER,
  LAB_LEGEND_DEVICE_DOWN,
  LAB_LEGEND_DEVICE_UP,
  LAB_LEGEND_IFACE_FAULT,
  LAB_LEGEND_LINK_DOWN,
  LAB_LEGEND_LINK_UP,
  LAB_MINIMAP_LABEL,
  LAB_MODE,
  LAB_MODE_FOCUS,
  LAB_MODE_FOCUS_HINT,
  LAB_MODE_SELECT,
  LAB_MODE_TRACE,
  LAB_MODE_TRACE_HINT,
  LAB_TRACE_CLEAR,
  LAB_TRACE_DONE,
  LAB_TRACE_PICK_A,
  LAB_TRACE_PICK_B,
  LAB_TRACE_UNREACHABLE,
  LOADING_TEXT,
  SIM_STATUS_LABELS,
  format,
} from '../../i18n';
import {
  IconClose,
  IconFit,
  IconLabels,
  IconLayoutCircle,
  IconLayoutConcentric,
  IconLayoutForce,
  IconLayoutFreeze,
  IconLayoutGrid,
  IconLayoutHier,
  IconModeFocus,
  IconModeSelect,
  IconModeTrace,
  IconResetView,
  IconZoomIn,
  IconZoomOut,
} from '../icons';

type LayoutKind = 'force' | 'hier' | 'concentric' | 'grid' | 'circle' | 'freeze';
type ModeKind = 'select' | 'trace' | 'focus';

/** 按工作台持久化的画布偏好（v2：布局 + 交互模式 + 标签开关）。 */
interface CanvasPrefs {
  layout: LayoutKind;
  mode: ModeKind;
  labels: boolean;
}

const DEFAULT_PREFS: CanvasPrefs = { layout: 'force', mode: 'select', labels: true };

const LAYOUT_KINDS: LayoutKind[] = ['force', 'hier', 'concentric', 'grid', 'circle', 'freeze'];
const MODE_KINDS: ModeKind[] = ['select', 'trace', 'focus'];

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
/** hover 描边（v0.2.2 微交互）：比常态提亮一档，120ms 过渡。 */
const NODE_HOVER_BORDER = '#b9cdf0';

/** 节点内部代码字色（FX-4）：up=深藏青（浅底 ≈9:1）/ down=纯白（红底）。 */
const NODE_CODE_COLOR = '#25395c';
const NODE_DOWN_CODE_COLOR = '#ffffff';

/** FX-4：up 边提亮（#5d7093 → #7387ab，深底 4.97:1），消除「细且平淡」。 */
const EDGE_UP_COLOR = '#7387ab';
/** 亮红 3px 虚线：故障链路必须醒目（对比旧 #B9C2D4 1.79:1）。 */
const EDGE_DOWN_COLOR = '#f2554a';
const EDGE_SELECTED_COLOR = '#4f8ef7';

const EDGE_LABEL_COLOR = '#9fb2ce';

/** 路径追踪高亮（v0.2.2）：青绿色系，与故障红/接口琥珀/选中蓝全部错开。 */
const TRACE_ROUTE_COLOR = '#3fd8c7';
const TRACE_END_BORDER = '#3fd8c7';

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

/** 迷你导航图尺寸（CSS px，内部按 devicePixelRatio 放大）。 */
const MINIMAP_W = 160;
const MINIMAP_H = 104;

/** 交互模式的类名常量（clearModeArtifacts 统一摘除）。 */
const TRACE_CLASSES = 'trace-dim trace-route trace-end';
const FOCUS_CLASSES = 'focus-dim';

/** 路径追踪状态机（横幅渲染）：待选起点 → 待选终点 → 完成/不可达。 */
type TraceState =
  | { stage: 'pick-a' }
  | { stage: 'pick-b'; aCode: string }
  | { stage: 'done'; aCode: string; bCode: string; hops: number; via: string[] }
  | { stage: 'unreachable'; aCode: string; bCode: string };

function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

const isLayoutKind = (v: unknown): v is LayoutKind =>
  typeof v === 'string' && (LAYOUT_KINDS as string[]).includes(v);
const isModeKind = (v: unknown): v is ModeKind =>
  typeof v === 'string' && (MODE_KINDS as string[]).includes(v);

/** 画布偏好持久化（v2 JSON；兼容 v1 纯布局字符串；异常静默回退默认）。 */
function readPrefs(persistKey: string | undefined): CanvasPrefs {
  if (persistKey === undefined || typeof sessionStorage === 'undefined') return DEFAULT_PREFS;
  try {
    const raw = sessionStorage.getItem(`netops-lab.canvas.v2.${persistKey}`);
    if (raw !== null) {
      const parsed: unknown = JSON.parse(raw);
      if (typeof parsed === 'object' && parsed !== null) {
        const rec = parsed as Record<string, unknown>;
        return {
          layout: isLayoutKind(rec.layout) ? rec.layout : DEFAULT_PREFS.layout,
          mode: isModeKind(rec.mode) ? rec.mode : DEFAULT_PREFS.mode,
          labels: typeof rec.labels === 'boolean' ? rec.labels : DEFAULT_PREFS.labels,
        };
      }
    }
    // v1（v0.2.1 及之前）只持久化布局字符串
    const legacy = sessionStorage.getItem(`netops-lab.canvas.${persistKey}`);
    if (isLayoutKind(legacy)) return { ...DEFAULT_PREFS, layout: legacy };
  } catch {
    // 隐私模式 / 配额异常 / JSON 损坏：静默回退默认
  }
  return DEFAULT_PREFS;
}

function writePrefs(persistKey: string | undefined, prefs: CanvasPrefs): void {
  if (persistKey === undefined || typeof sessionStorage === 'undefined') return;
  try {
    sessionStorage.setItem(`netops-lab.canvas.v2.${persistKey}`, JSON.stringify(prefs));
  } catch {
    // 写失败不影响功能
  }
}

/**
 * 层级布局的根节点：每个连通分量取度数最高的节点（并列取编码字典序，保证确定性），
 * 让核心/枢纽设备位于树顶，形成核心-汇聚-接入的自上而下分层形态。
 * 不用闭包变量装 NodeSingular（TS 收窄陷阱），全部用基元类型记录。
 */
function breadthFirstRoots(eles: CollectionReturnValue): string[] {
  const roots: string[] = [];
  const seen = new Set<string>();
  eles.nodes().forEach((node) => {
    if (seen.has(node.id())) return;
    // component() 返回包含该节点在内的整个连通分量
    const comp = node.component();
    comp.nodes().forEach((n) => {
      seen.add(n.id());
    });
    let bestId = '';
    let bestCode = '';
    let bestDegree = -1;
    comp.nodes().forEach((n) => {
      const d = n.degree(false);
      const code = String(n.data('code') ?? n.id());
      if (d > bestDegree || (d === bestDegree && code < bestCode)) {
        bestId = n.id();
        bestCode = code;
        bestDegree = d;
      }
    });
    if (bestId !== '') roots.push(bestId);
  });
  return roots;
}

/** 按当前布局类型构造 Cytoscape 布局参数；freeze 返回 null（不跑布局）。 */
function layoutOptionsFor(kind: LayoutKind, target: CollectionReturnValue, animate: boolean): LayoutOptions | null {
  const duration = animate ? LAYOUT_ANIMATION_MS : 0;
  switch (kind) {
    case 'force':
      return {
        name: 'cose',
        animate,
        animationDuration: duration,
        padding: FIT_PADDING,
        componentSpacing: 90,
        idealEdgeLength: 90,
      } as unknown as LayoutOptions;
    case 'hier':
      return {
        name: 'breadthfirst',
        roots: breadthFirstRoots(target),
        directed: false,
        circle: false,
        grid: false,
        spacingFactor: 1.35,
        padding: FIT_PADDING,
        animate,
        animationDuration: duration,
      } as unknown as LayoutOptions;
    case 'concentric':
      return {
        name: 'concentric',
        concentric: (node: NodeSingular): number => node.degree(false),
        levelWidth: (): number => 2,
        minNodeSpacing: 36,
        avoidOverlap: true,
        padding: FIT_PADDING,
        animate,
        animationDuration: duration,
      } as unknown as LayoutOptions;
    case 'grid':
      return {
        name: 'grid',
        avoidOverlap: true,
        avoidOverlapPadding: 24,
        condense: false,
        spacingFactor: 1.1,
        padding: FIT_PADDING,
        animate,
        animationDuration: duration,
      } as unknown as LayoutOptions;
    case 'circle':
      return {
        name: 'circle',
        avoidOverlap: true,
        spacingFactor: 1.15,
        startAngle: (3 / 2) * Math.PI,
        padding: FIT_PADDING,
        animate,
        animationDuration: duration,
      } as unknown as LayoutOptions;
    case 'freeze':
      return null;
  }
}

/**
 * 锁定模式下的新节点落位：围绕既有节点质心（或视口中心）螺旋展开，
 * 保证手动排布不被自动布局打乱的同时，新节点仍然可见、可拖。
 */
function placeNewNodesSpiral(cy: Core, addedIds: Set<string>): void {
  const existing: Array<{ x: number; y: number }> = [];
  const added: NodeSingular[] = [];
  cy.nodes().forEach((node) => {
    if (addedIds.has(node.id())) {
      added.push(node);
    } else {
      existing.push({ x: node.position('x'), y: node.position('y') });
    }
  });
  let cx: number;
  let cy0: number;
  if (existing.length > 0) {
    cx = existing.reduce((sum, p) => sum + p.x, 0) / existing.length;
    cy0 = existing.reduce((sum, p) => sum + p.y, 0) / existing.length;
  } else {
    const ext = cy.extent();
    cx = ext.x1 + ext.w / 2;
    cy0 = ext.y1 + ext.h / 2;
  }
  added.forEach((node, i) => {
    const angle = i * 0.7;
    const radius = 80 + 22 * i;
    node.position({
      x: cx + radius * Math.cos(angle),
      y: cy0 + radius * Math.sin(angle),
    });
  });
}

/** 迷你导航图坐标变换：模型坐标 → 缩略图像素（内容等比缩放并居中）。 */
function minimapTransform(cy: Core): { scale: number; ox: number; oy: number } | null {
  const nodes = cy.nodes();
  if (nodes.length === 0) return null;
  const bb = cy.elements().boundingBox();
  const bw = Math.max(bb.w, 1);
  const bh = Math.max(bb.h, 1);
  const pad = 7;
  const scale = Math.min((MINIMAP_W - pad * 2) / bw, (MINIMAP_H - pad * 2) / bh);
  const ox = pad + ((MINIMAP_W - pad * 2) - bw * scale) / 2 - bb.x1 * scale;
  const oy = pad + ((MINIMAP_H - pad * 2) - bh * scale) / 2 - bb.y1 * scale;
  return { scale, ox, oy };
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
      // v0.2.2 微交互：hover 描边提亮 120ms 过渡（无动画偏好时不影响布局动画）
      'transition-property': 'border-width border-color',
      'transition-duration': 120,
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
  // v0.2.2 微交互：hover 效果用事件 + .hovered 类（Cytoscape 3.32 无 :hover 伪类）
  {
    selector: 'node.hovered',
    style: {
      'border-width': 2.5,
      'border-color': NODE_HOVER_BORDER,
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
      'transition-property': 'width line-color',
      'transition-duration': 120,
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
  // 链路标签开关（v0.2.2）：类规则放在带属性选择器的标签规则之后，
  // 同等特异度下源顺序取胜——加类即隐藏标签。
  {
    selector: 'edge.nolabel',
    style: {
      label: '',
    },
  },
  {
    selector: 'edge.hovered',
    style: {
      width: 3,
    },
  },
  {
    selector: 'edge:selected',
    style: {
      width: 3.5,
      'line-color': EDGE_SELECTED_COLOR,
    },
  },
  // —— 路径追踪（v0.2.2）：途经元素青绿高亮，其余压暗 ——
  {
    selector: 'node.trace-dim, edge.trace-dim',
    style: {
      opacity: 0.14,
    },
  },
  {
    selector: 'edge.trace-route',
    style: {
      'line-color': TRACE_ROUTE_COLOR,
      width: 3.5,
      'z-index': 100,
    },
  },
  {
    selector: 'node.trace-end',
    style: {
      'border-color': TRACE_END_BORDER,
      'border-width': 3,
    },
  },
  // —— 邻域聚焦（v0.2.2）：一跳邻域之外压暗 ——
  {
    selector: 'node.focus-dim, edge.focus-dim',
    style: {
      opacity: 0.14,
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
  const minimapRef = useRef<HTMLCanvasElement | null>(null);
  const onSelectRef = useRef(onSelect);
  /** 当前布局任务（防并发：新布局启动前先停旧的）。 */
  const layoutRunRef = useRef<Layouts | null>(null);
  /** FX-4：hover 信息条/容器光标的事件清理（实例卸载时调用）。 */
  const hoverCleanupRef = useRef<(() => void) | null>(null);
  const [prefs, setPrefs] = useState<CanvasPrefs>(() => readPrefs(persistKey));
  /** 悬浮节点信息（FX-4）：左上角信息条「code · 名称 · 状态」。 */
  const [hoverInfo, setHoverInfo] = useState<HoverInfo | null>(null);
  /** 路径追踪横幅（v0.2.2）。 */
  const [trace, setTrace] = useState<TraceState | null>(null);
  /** 路径追踪的 id 镜像（tap 处理器读取，避免闭包过期）。 */
  const traceRef = useRef<{ aId: string | null; bId: string | null }>({ aId: null, bId: null });
  /** tap 处理器 → 最新模式/追踪/聚焦动作（挂载期注册一次，靠 ref 转发）。 */
  const modeRef = useRef<ModeKind>(prefs.mode);
  const actionsRef = useRef<{
    traceTap: (id: string) => void;
    focusTap: (id: string) => void;
  }>({ traceTap: () => undefined, focusTap: () => undefined });
  /** 迷你导航图重绘调度（挂载期注册监听，rAF 合并 pan/zoom 高频事件）。 */
  const scheduleMinimapRef = useRef<() => void>(() => undefined);

  const layout = prefs.layout;
  const mode = prefs.mode;
  const edgeLabels = prefs.labels;

  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

  useEffect(() => {
    writePrefs(persistKey, prefs);
  }, [persistKey, prefs]);

  useEffect(() => {
    modeRef.current = mode;
  }, [mode]);

  /** 布局完成后再 fit（cose 为异步仿真、grid/circle 有 300ms 动画，立即 fit 抓到的是布局前包围盒）。 */
  function fitToView(cy: Core): void {
    cy.fit(undefined, FIT_PADDING);
    // minZoom 在构造时已设为 0.55；fit 受其约束，这里再显式钳一次作保险
    if (cy.zoom() < MIN_ZOOM) {
      cy.zoom(MIN_ZOOM);
      cy.center();
    }
  }

  /**
   * 启动布局：显式传入布局类型（v0.2.2 修复 v0.2.1 的闭包陷阱——旧实现
   * runLayout 先 setLayout 再调用闭包里的 startLayout，实际跑的仍是旧布局，
   * 网格/环形按钮点击后不变就是这个原因）；先停旧布局（AUD2-7 并发防护），
   * reduced-motion 时关闭动画。
   */
  const startLayout = useCallback(
    (cy: Core, kind: LayoutKind, eles?: CollectionReturnValue): void => {
      layoutRunRef.current?.stop();
      if (kind === 'freeze') return;
      const target = eles ?? cy.elements();
      if (target.length === 0) return;
      const reduceMotion = prefersReducedMotion();
      const options = layoutOptionsFor(kind, target, !reduceMotion);
      if (options === null) return;
      const run = target.layout({
        ...options,
        stop: () => {
          if (layoutRunRef.current === run) {
            layoutRunRef.current = null;
          }
          fitToView(cy);
          scheduleMinimapRef.current();
        },
      } as unknown as LayoutOptions);
      layoutRunRef.current = run;
      run.run();
    },
    [],
  );

  /** 摘除路径追踪 / 邻域聚焦的全部视觉痕迹（模式切换、Esc、空白点击、数据变更）。 */
  const clearModeArtifacts = useCallback((): void => {
    const cy = cyRef.current;
    traceRef.current = { aId: null, bId: null };
    setTrace(null);
    if (cy === null) return;
    cy.elements().removeClass(TRACE_CLASSES);
    cy.elements().removeClass(FOCUS_CLASSES);
  }, []);

  /** 迷你导航图绘制（v0.2.2）：节点缩略点 + 视口矩形；DPR 自适应。 */
  const drawMinimap = useCallback((): void => {
    const cy = cyRef.current;
    const canvas = minimapRef.current;
    if (cy === null || canvas === null) return;
    const ctx = canvas.getContext('2d');
    if (ctx === null) return;
    const dpr = typeof window !== 'undefined' && window.devicePixelRatio > 0 ? window.devicePixelRatio : 1;
    if (canvas.width !== Math.round(MINIMAP_W * dpr)) {
      canvas.width = Math.round(MINIMAP_W * dpr);
      canvas.height = Math.round(MINIMAP_H * dpr);
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, MINIMAP_W, MINIMAP_H);
    const tf = minimapTransform(cy);
    if (tf === null) return;
    const { scale, ox, oy } = tf;
    cy.nodes().forEach((node) => {
      const x = node.position('x') * scale + ox;
      const y = node.position('y') * scale + oy;
      ctx.fillStyle = node.data('status') === 'down' ? '#f2554a' : '#7387ab';
      ctx.fillRect(x - 1.5, y - 1.5, 3, 3);
    });
    const ext = cy.extent();
    const vx = ext.x1 * scale + ox;
    const vy = ext.y1 * scale + oy;
    const vw = Math.max(ext.w * scale, 4);
    const vh = Math.max(ext.h * scale, 4);
    ctx.fillStyle = 'rgba(125, 162, 255, 0.10)';
    ctx.fillRect(vx, vy, vw, vh);
    ctx.strokeStyle = '#7da2ff';
    ctx.lineWidth = 1;
    ctx.strokeRect(vx, vy, vw, vh);
  }, []);

  /** 缩略图点击/拖动 → 主视图以该模型坐标为中心（类型安全：用 zoom/pan 推导，绕开 center(position) 未入类型的问题）。 */
  const centerFromMinimap = useCallback((clientX: number, clientY: number): void => {
    const cy = cyRef.current;
    if (cy === null) return;
    const canvas = minimapRef.current;
    if (canvas === null) return;
    const tf = minimapTransform(cy);
    if (tf === null) return;
    const rect = canvas.getBoundingClientRect();
    const mx = clientX - rect.left;
    const my = clientY - rect.top;
    const x = (mx - tf.ox) / tf.scale;
    const y = (my - tf.oy) / tf.scale;
    if (!Number.isFinite(x) || !Number.isFinite(y)) return;
    // 模型坐标 → 渲染坐标 → 把该点平移到视口中心
    const zoom = cy.zoom();
    const pan = cy.pan();
    const renderedX = x * zoom + pan.x;
    const renderedY = y * zoom + pan.y;
    cy.panBy({
      x: cy.width() / 2 - renderedX,
      y: cy.height() / 2 - renderedY,
    });
  }, []);

  // —— 路径追踪 tap（v0.2.2）：起点 → 终点 → aStar 最短路径高亮 ——
  const handleTraceTap = useCallback((id: string): void => {
    const cy = cyRef.current;
    if (cy === null) return;
    const t = traceRef.current;
    if (t.aId === null || t.bId !== null) {
      // 开始一条新路径（顺手清掉上一条的视觉状态）
      cy.elements().removeClass(TRACE_CLASSES);
      traceRef.current = { aId: id, bId: null };
      const node = cy.getElementById(id);
      node.addClass('trace-end');
      setTrace({ stage: 'pick-b', aCode: String(node.data('code') ?? id) });
      return;
    }
    if (id === t.aId) {
      // 再点一次起点 = 取消本次追踪
      cy.elements().removeClass(TRACE_CLASSES);
      traceRef.current = { aId: null, bId: null };
      setTrace(null);
      return;
    }
    const a = cy.getElementById(t.aId);
    const b = cy.getElementById(id);
    const result = cy.elements().aStar({ root: a, goal: b, directed: false });
    if (result.found) {
      const pathIds = new Set<string>();
      result.path.forEach((ele) => {
        pathIds.add(ele.id());
      });
      cy.elements().forEach((ele) => {
        if (!pathIds.has(ele.id())) ele.addClass('trace-dim');
      });
      result.path.addClass('trace-route');
      a.addClass('trace-end');
      b.addClass('trace-end');
      const codes = result.path.nodes().map((n) => String(n.data('code') ?? n.id()));
      traceRef.current = { aId: t.aId, bId: id };
      setTrace({
        stage: 'done',
        aCode: codes.length > 0 ? codes[0] : String(a.data('code') ?? t.aId),
        bCode: codes.length > 0 ? codes[codes.length - 1] : String(b.data('code') ?? id),
        hops: Math.max(codes.length - 1, 0),
        via: codes.slice(1, -1),
      });
    } else {
      a.addClass('trace-end');
      b.addClass('trace-end');
      traceRef.current = { aId: t.aId, bId: id };
      setTrace({
        stage: 'unreachable',
        aCode: String(a.data('code') ?? t.aId),
        bCode: String(b.data('code') ?? id),
      });
    }
  }, []);

  // —— 邻域聚焦 tap（v0.2.2）：一跳闭邻域之外的元素全部压暗 ——
  const handleFocusTap = useCallback((id: string): void => {
    const cy = cyRef.current;
    if (cy === null) return;
    const node = cy.getElementById(id);
    if (node.empty()) return;
    cy.elements().removeClass(FOCUS_CLASSES);
    const keepIds = new Set<string>();
    node.closedNeighborhood().forEach((ele) => {
      keepIds.add(ele.id());
    });
    cy.elements().forEach((ele) => {
      if (!keepIds.has(ele.id())) ele.addClass('focus-dim');
    });
  }, []);

  useEffect(() => {
    actionsRef.current = { traceTap: handleTraceTap, focusTap: handleFocusTap };
  });

  // 模式切换 → 清掉上一模式的视觉残留
  useEffect(() => {
    clearModeArtifacts();
  }, [mode, clearModeArtifacts]);

  // Esc 清除路径/聚焦（与对话框 Esc 并存：清画布痕迹无副作用）
  useEffect(() => {
    function onKey(event: KeyboardEvent): void {
      if (event.key === 'Escape') clearModeArtifacts();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [clearModeArtifacts]);

  // —— 实例生命周期：只创建一次 ——
  useEffect(() => {
    if (containerRef.current === null) return undefined;
    const cy = cytoscape({
      container: containerRef.current,
      elements: [],
      style: CY_STYLE as unknown as StylesheetJson,
      layout: { name: 'cose' },
      wheelSensitivity: 0.2,
      autounselectify: true,
      // 缩放下限：fit/滚轮都不会低于该值，保证 27 节点拓扑的默认视图下标签可读
      minZoom: MIN_ZOOM,
    });
    cy.on('tap', 'node', (event) => {
      const node = event.target;
      if (modeRef.current === 'trace') {
        actionsRef.current.traceTap(node.id());
        return;
      }
      if (modeRef.current === 'focus') {
        actionsRef.current.focusTap(node.id());
        // 聚焦模式仍联动右侧表格（点击即定位行）
        onSelectRef.current({ kind: 'device', id: node.id() });
        return;
      }
      onSelectRef.current({ kind: 'device', id: node.id() });
    });
    cy.on('tap', 'edge', (event) => {
      const edge = event.target;
      onSelectRef.current({ kind: 'link', id: edge.id() });
    });
    // 点击画布空白：清除路径追踪 / 邻域聚焦（选择模式保持原状）
    cy.on('tap', (event) => {
      if (event.target === cy) {
        clearModeArtifacts();
      }
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
      // v0.2.2：hover 微交互——.hovered 类（描边提亮/线宽加粗，样式表 120ms 过渡）
      cy.on('mouseover', 'node, edge', (event) => {
        event.target.addClass('hovered');
        setPointerCursor();
      });
      cy.on('mouseout', 'node, edge', (event) => {
        event.target.removeClass('hovered');
        resetCursor();
      });
      container.addEventListener('mouseleave', onContainerMouseLeave);
      hoverCleanupRef.current = () => {
        container.removeEventListener('mouseleave', onContainerMouseLeave);
        resetCursor();
      };
    }

    // —— 迷你导航图（v0.2.2）：pan/zoom/position/layoutstop 全部 rAF 合并重绘 ——
    let minimapRaf: number | null = null;
    const scheduleMinimap = (): void => {
      if (minimapRaf !== null) return;
      minimapRaf = requestAnimationFrame(() => {
        minimapRaf = null;
        drawMinimap();
      });
    };
    scheduleMinimapRef.current = scheduleMinimap;
    cy.on('pan zoom resize', scheduleMinimap);
    cy.on('position', '*', scheduleMinimap);
    cy.on('layoutstop', scheduleMinimap);

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
      if (minimapRaf !== null) cancelAnimationFrame(minimapRaf);
      scheduleMinimapRef.current = () => undefined;
      window.removeEventListener('resize', onWindowResize);
      observer?.disconnect();
      layoutRunRef.current?.stop();
      layoutRunRef.current = null;
      cy.destroy();
      cyRef.current = null;
    };
    // drawMinimap 只读 refs，签名稳定；clearModeArtifacts 同理
  }, [drawMinimap, clearModeArtifacts]);

  // —— 数据增量同步：删除消失元素、更新既有元素、仅新增元素及其邻域参与重排（保留既有位置） ——
  useEffect(() => {
    const cy = cyRef.current;
    if (cy === null) return;
    // 数据变更后旧路径/聚焦必然过期，先清视觉再同步数据
    clearModeArtifacts();
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
    // 链路标签开关作用到（可能新加入的）边
    cy.edges().toggleClass('nolabel', !edgeLabels);
    if (addedIds.size > 0) {
      if (layout === 'freeze') {
        // 锁定模式：不打乱手动排布，新节点围绕质心螺旋落位
        placeNewNodesSpiral(cy, addedIds);
        fitToView(cy);
        scheduleMinimapRef.current();
      } else {
        const added = cy.elements().filter((ele) => addedIds.has(ele.id()));
        const scope = added.union(added.neighborhood());
        startLayout(cy, layout, scope);
      }
    } else {
      scheduleMinimapRef.current();
    }
  }, [topology, layout, edgeLabels, startLayout, clearModeArtifacts]);

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

  // —— 工具带：切换布局 / 模式 / 标签 ——
  function runLayout(kind: LayoutKind): void {
    setPrefs((p) => ({ ...p, layout: kind }));
    const cy = cyRef.current;
    if (cy === null) return;
    if (kind === 'freeze') {
      // 锁定：停在当前排布，不动任何节点
      layoutRunRef.current?.stop();
      layoutRunRef.current = null;
      scheduleMinimapRef.current();
      return;
    }
    // 显式传新 kind：状态更新是异步的，闭包里的旧值不可靠（v0.2.1 的回归根源）
    startLayout(cy, kind);
  }

  function switchMode(kind: ModeKind): void {
    setPrefs((p) => ({ ...p, mode: kind }));
  }

  function toggleLabels(): void {
    setPrefs((p) => ({ ...p, labels: !p.labels }));
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

  // —— 缩略图指针交互：按下/拖动持续以指针位置为中心 ——
  const minimapDraggingRef = useRef(false);
  function onMinimapPointerDown(event: ReactPointerEvent<HTMLCanvasElement>): void {
    minimapDraggingRef.current = true;
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // 旧浏览器无 pointer capture：退化为点击定位
    }
    centerFromMinimap(event.clientX, event.clientY);
  }
  function onMinimapPointerMove(event: ReactPointerEvent<HTMLCanvasElement>): void {
    if (!minimapDraggingRef.current) return;
    centerFromMinimap(event.clientX, event.clientY);
  }
  function onMinimapPointerUp(event: ReactPointerEvent<HTMLCanvasElement>): void {
    minimapDraggingRef.current = false;
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // 忽略
    }
  }

  const empty = useMemo(
    () => topology === null || (topology.nodes.length === 0 && topology.edges.length === 0),
    [topology],
  );

  const hintText =
    mode === 'trace' ? LAB_MODE_TRACE_HINT : mode === 'focus' ? LAB_MODE_FOCUS_HINT : LAB_CANVAS_HINT;

  /** 路径追踪横幅文案（v0.2.2）。 */
  let traceBanner: { className: string; text: string } | null = null;
  if (trace !== null) {
    if (trace.stage === 'pick-a') {
      traceBanner = { className: 'lab-canvas-trace--pick', text: LAB_TRACE_PICK_A };
    } else if (trace.stage === 'pick-b') {
      traceBanner = { className: 'lab-canvas-trace--pick', text: format(LAB_TRACE_PICK_B, { a: trace.aCode }) };
    } else if (trace.stage === 'done') {
      // 途经列表过长时折叠为「前 6 台 + 等 N 台」，避免横幅被撑爆（关键信息是跳数）
      const viaText =
        trace.via.length > 6
          ? `${trace.via.slice(0, 6).join('、')} 等 ${trace.via.length} 台`
          : trace.via.length > 0
            ? trace.via.join('、')
            : '直达';
      traceBanner = {
        className: 'lab-canvas-trace--done',
        text: format(LAB_TRACE_DONE, {
          a: trace.aCode,
          b: trace.bCode,
          hops: trace.hops,
          via: viaText,
        }),
      };
    } else {
      traceBanner = {
        className: 'lab-canvas-trace--unreachable',
        text: format(LAB_TRACE_UNREACHABLE, { a: trace.aCode, b: trace.bCode }),
      };
    }
  }

  const LAYOUT_BUTTONS: Array<{ kind: LayoutKind; label: string; title: string; icon: ReactNode }> = [
    { kind: 'force', label: LAB_LAYOUT_FORCE, title: '力导向布局（物理模拟自然聚簇）', icon: <IconLayoutForce /> },
    { kind: 'hier', label: LAB_LAYOUT_HIER, title: '层级布局（自上而下树形，核心层在顶）', icon: <IconLayoutHier /> },
    { kind: 'concentric', label: LAB_LAYOUT_CONCENTRIC, title: '同心圆布局（按连接度分环，枢纽居中）', icon: <IconLayoutConcentric /> },
    { kind: 'grid', label: LAB_LAYOUT_GRID, title: '网格布局（规整行列，便于对照清单）', icon: <IconLayoutGrid /> },
    { kind: 'circle', label: LAB_LAYOUT_CIRCLE, title: '环形布局（首尾相接的环）', icon: <IconLayoutCircle /> },
    { kind: 'freeze', label: LAB_LAYOUT_FREEZE, title: '锁定布局（停用自动排布，保留手动拖拽位置）', icon: <IconLayoutFreeze /> },
  ];
  const MODE_BUTTONS: Array<{ kind: ModeKind; label: string; title: string; icon: ReactNode }> = [
    { kind: 'select', label: LAB_MODE_SELECT, title: '选择模式（点击联动右侧表格定位）', icon: <IconModeSelect /> },
    { kind: 'trace', label: LAB_MODE_TRACE, title: '路径模式（依次点击两台设备，高亮最短路径）', icon: <IconModeTrace /> },
    { kind: 'focus', label: LAB_MODE_FOCUS, title: '聚焦模式（点击设备，仅保留其一跳邻域）', icon: <IconModeFocus /> },
  ];

  return (
    <div className="lab-canvas-card card" aria-label={LAB_CANVAS_LABEL}>
      {/* 工具带（v0.2.2 图标化分段控件）：布局 6 态 + 交互模式 3 态 + 链路标签开关 */}
      <div className="lab-canvas-toolbar" role="toolbar" aria-label={LAB_CANVAS_LABEL}>
        <span className="lab-canvas-toolbar-label">{LAB_LAYOUT}</span>
        <div className="canvas-seg" role="group" aria-label={LAB_LAYOUT}>
          {LAYOUT_BUTTONS.map((btn) => (
            <button
              key={btn.kind}
              type="button"
              className={`canvas-tool${layout === btn.kind ? ' is-active' : ''}`}
              aria-pressed={layout === btn.kind}
              title={btn.title}
              aria-label={btn.title}
              onClick={() => runLayout(btn.kind)}
            >
              {btn.icon}
            </button>
          ))}
        </div>
        <span className="lab-canvas-toolbar-sep" aria-hidden="true" />
        <span className="lab-canvas-toolbar-label">{LAB_MODE}</span>
        <div className="canvas-seg" role="group" aria-label={LAB_MODE}>
          {MODE_BUTTONS.map((btn) => (
            <button
              key={btn.kind}
              type="button"
              className={`canvas-tool${mode === btn.kind ? ' is-active' : ''}`}
              aria-pressed={mode === btn.kind}
              title={btn.title}
              aria-label={btn.title}
              onClick={() => switchMode(btn.kind)}
            >
              {btn.icon}
            </button>
          ))}
        </div>
        <span className="lab-canvas-toolbar-sep" aria-hidden="true" />
        <button
          type="button"
          className={`canvas-tool canvas-tool--toggle${edgeLabels ? ' is-active' : ''}`}
          aria-pressed={edgeLabels}
          title={LAB_CANVAS_LABELS}
          aria-label={LAB_CANVAS_LABELS}
          onClick={toggleLabels}
        >
          <IconLabels />
        </button>
        <span className="lab-canvas-toolbar-hint">{hintText}</span>
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
        {/* 路径追踪横幅（v0.2.2，顶部居中）：待选 → 结果（跳数/途经/不可达） */}
        {traceBanner !== null && (
          <div className={`lab-canvas-overlay lab-canvas-trace ${traceBanner.className}`} role="status" aria-live="polite">
            <span className="trace-text">{traceBanner.text}</span>
            <button
              type="button"
              className="trace-close"
              title={LAB_TRACE_CLEAR}
              aria-label={LAB_TRACE_CLEAR}
              onClick={clearModeArtifacts}
            >
              <IconClose size={12} />
            </button>
          </div>
        )}
        {/* 迷你导航图（v0.2.2，右下角）：点击/拖动定位主视图 */}
        {!empty && (
          <div className="lab-canvas-minimap" aria-label={LAB_MINIMAP_LABEL} title={LAB_MINIMAP_LABEL}>
            <canvas
              ref={minimapRef}
              width={MINIMAP_W}
              height={MINIMAP_H}
              onPointerDown={onMinimapPointerDown}
              onPointerMove={onMinimapPointerMove}
              onPointerUp={onMinimapPointerUp}
              onPointerCancel={onMinimapPointerUp}
            />
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
