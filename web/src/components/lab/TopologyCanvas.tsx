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
 *   （保留既有节点位置，仅新增节点及其邻域参与重排）；卸载时 cy.destroy()；
 * - FX-6 画布能力增强：
 *   · 右键上下文菜单（cy.on('cxttap') + 自渲染绝对定位 div，无 cxtmenu 插件）：
 *     节点=邻域聚焦/路径追踪起点/表格定位/复制设备 ID；边=表格定位/复制链路 ID；
 *     空白=适配视图/重置视图。菜单出现在鼠标右下、越界向对侧翻转；Esc/点击
 *     别处/Tab 关闭；role=menu + menuitem + ↑↓/Home/End 导航，打开后焦点入菜单，
 *     Esc 关闭还焦点；快捷键提示列（F 等）；
 *   · PNG 快照导出：cy.png({ full, scale:2, bg: CANVAS_BG })（bg 保证不透明）
 *     → a[download] 触发下载 netops-topology-<persistKey>.png；
 *   · 缩放百分比 chip：cy.on('zoom') rAF 节流实时刷新（1:1 → 100%）；
 *   · 节点位置持久化：拖拽结束（drag 标记 + free 事件 + 160ms 合并）把全部
 *     节点 {id,x,y} 写 localStorage netops-lab.canvas.pos.<persistKey>（隐私
 *     模式 try/catch 静默降级）；加载时恢复存档并跳过自动布局（等价 freeze
 *     语义）；重置视图/切换自动布局时清除存档并重排；
 *   · 键盘快捷键：F=适配视图、L=链路标签开关、Esc=清除高亮并关菜单；
 *     输入框/textarea/contenteditable/对话框（焦点陷阱）内不抢键。
 * - FX-5 画布全屏工作模式（zen）：
 *   · 新增可选 props（向后兼容）：zen / onZenChange / resizeSuspended；
 *   · 工具带最右「全屏」按钮 → 画布卡片 position:fixed inset:0（盖住侧栏/顶栏/
 *     页头/右侧面板），工具带深色化（与画布底色一致），退出按钮显著（带文字）；
 *   · zen 时左上浮动选中详情卡（设备：编码/名称/状态/接口 up-down/IPv4；
 *     链路：两端设备+接口）——右侧表格不可见时信息不断档；
 *   · 原生 Fullscreen API 两级增强：requestFullscreen 成功 = 真全屏，失败/不
 *     支持 = 应用内 CSS zen（try/catch 静默降级）；fullscreenchange 同步：浏览
 *     器 Esc 退原生全屏时 CSS zen 一并退（避免「盖着但非全屏」错乱态）；
 *   · Esc 逐级链路（与 FX-6 协调）：右键菜单 → trace/focus → zen，不一键全清；
 *   · resizeSuspended：分栏拖拽期间挂起 cy.resize（不逐帧重设画布缓冲），
 *     拖完/zen 切换后补一次 resize + minimap（容器尺寸变化 Cytoscape 不自动
 *     感知；平时 ResizeObserver 自动，拖拽期间手动挂起防卡顿）。
 * - GEO-2 顶层视图维度：拓扑图 | 2D 地图 | 3D 地图（prefs v2 新增 view 字段，
 *   兼容旧档案默认 topo）。视图分段复用 canvas-seg/canvas-tool 视觉语言；
 *   地图视图由 <GeoCanvas>（maplibre-gl 懒加载）铺满画布区域，覆盖在 cy 容器
 *   之上——cy 实例与全部拓扑能力原样保留（回到 topo 视图零重建、位置/选中/
 *   布局状态不变，且不产生实例泄漏）。布局/模式/PNG 等拓扑专属工具在地图
 *   视图禁用（带说明 tooltip）；地图专属控件（适配视图 F / 指北复位）接管；
 *   minimap/缩放 chip/右键菜单/trace 横幅仅拓扑视图渲染。Esc 链在地图视图
 *   自然收窄为：菜单（不会开）→ trace/focus 痕迹（切视图时清理）→ zen 退出。
 */

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import cytoscape from 'cytoscape';
import type { CollectionReturnValue, Core, EdgeSingular, ElementDefinition, Layouts, LayoutOptions, NodeSingular, StylesheetJson } from 'cytoscape';
import type { SimEntityStatus, SimTopology } from '../../api/types';
import type { SelectedEntity } from './selection';
import { isUnconfiguredIPv4 } from './selection';
import {
  LAB_CANVAS_CTX_COPY_ID,
  LAB_CANVAS_CTX_COPY_ID_TITLE,
  LAB_CANVAS_CTX_COPY_LINK_ID,
  LAB_CANVAS_CTX_COPY_LINK_ID_TITLE,
  LAB_CANVAS_CTX_FIT,
  LAB_CANVAS_CTX_FIT_TITLE,
  LAB_CANVAS_CTX_FOCUS,
  LAB_CANVAS_CTX_FOCUS_TITLE,
  LAB_CANVAS_CTX_LOCATE,
  LAB_CANVAS_CTX_LOCATE_LINK,
  LAB_CANVAS_CTX_LOCATE_LINK_TITLE,
  LAB_CANVAS_CTX_LOCATE_TITLE,
  LAB_CANVAS_CTX_RESET,
  LAB_CANVAS_CTX_RESET_TITLE,
  LAB_CANVAS_CTX_TRACE,
  LAB_CANVAS_CTX_TRACE_TITLE,
  LAB_CANVAS_EMPTY,
  LAB_CANVAS_EXPORT,
  LAB_CANVAS_FIT,
  LAB_CANVAS_HINT,
  LAB_CANVAS_LABEL,
  LAB_CANVAS_LABELS,
  LAB_CANVAS_LEGEND_LABEL,
  LAB_CANVAS_MENU_CANVAS,
  LAB_CANVAS_MENU_EDGE,
  LAB_CANVAS_MENU_NODE,
  LAB_CANVAS_RESET,
  LAB_CANVAS_ZOOM_CURRENT,
  LAB_CANVAS_ZOOM_IN,
  LAB_CANVAS_ZOOM_OUT,
  LAB_CANVAS_ZOOM_HINT,
  LAB_GEO_DISABLED_TITLE,
  LAB_GEO_EXPORT_DISABLED,
  LAB_GEO_FIT,
  LAB_GEO_HINT,
  LAB_GEO_LABELS,
  LAB_GEO_NORTH,
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
  LAB_TRACE_EXIT_MODE,
  LAB_TRACE_PICK_A,
  LAB_TRACE_PICK_B,
  LAB_TRACE_UNREACHABLE,
  LAB_VIEW,
  LAB_VIEW_GEO2D,
  LAB_VIEW_GEO2D_TITLE,
  LAB_VIEW_GEO3D,
  LAB_VIEW_GEO3D_TITLE,
  LAB_VIEW_TOPO,
  LAB_VIEW_TOPO_TITLE,
  LABWS_ZEN_ENTER,
  LABWS_ZEN_EXIT,
  LABWS_ZEN_EXIT_LABEL,
  LABWS_ZEN_INFO_IFACES,
  LABWS_ZEN_INFO_IPV4,
  LABWS_ZEN_INFO_IPV4_MORE,
  LABWS_ZEN_INFO_IPV4_NONE,
  LABWS_ZEN_INFO_LABEL,
  LABWS_ZEN_INFO_LINK_ENDS,
  LOADING_TEXT,
  SIM_STATUS_LABELS,
  format,
} from '../../i18n';
import {
  IconCamera,
  IconClose,
  IconCompass,
  IconExpand,
  IconFit,
  IconLabels,
  IconLayoutCircle,
  IconLayoutConcentric,
  IconLayoutForce,
  IconLayoutFreeze,
  IconLayoutGrid,
  IconLayoutHier,
  IconMap2d,
  IconMap3d,
  IconMinimize,
  IconModeFocus,
  IconModeSelect,
  IconModeTrace,
  IconResetView,
  IconTopology,
  IconZoomIn,
  IconZoomOut,
} from '../icons';
import { GeoCanvas } from './GeoCanvas';
import type { GeoCanvasHandle, GeoHoverInfo } from './GeoCanvas';

type LayoutKind = 'force' | 'hier' | 'concentric' | 'grid' | 'circle' | 'freeze';
type ModeKind = 'select' | 'trace' | 'focus';

/** GEO-2：画布顶层视图维度（拓扑图 / 2D 地图 / 3D 地图）。 */
type CanvasView = 'topo' | 'geo2d' | 'geo3d';

const VIEW_KINDS: CanvasView[] = ['topo', 'geo2d', 'geo3d'];

const isViewKind = (v: unknown): v is CanvasView =>
  typeof v === 'string' && (VIEW_KINDS as string[]).includes(v);

/** 按工作台持久化的画布偏好（v2：布局 + 交互模式 + 标签开关 + 顶层视图 GEO-2）。 */
interface CanvasPrefs {
  layout: LayoutKind;
  mode: ModeKind;
  labels: boolean;
  view: CanvasView;
}

/** FX-6：右键菜单项（label=可见文案；title=tooltip；hint=快捷键提示列）。 */
interface CanvasMenuItem {
  key: string;
  label: string;
  title: string;
  hint?: string;
  run: () => void;
}

/** FX-6：右键菜单状态（cx/cy=光标点；x/y=当前左上角，越界翻转后更新）。 */
interface CanvasMenuState {
  cx: number;
  cy: number;
  x: number;
  y: number;
  label: string;
  items: CanvasMenuItem[];
}

const DEFAULT_PREFS: CanvasPrefs = { layout: 'force', mode: 'select', labels: true, view: 'topo' };

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

/** FX-6：右键菜单出现位置相对鼠标的偏移（默认右下方，越界向对侧翻转）。 */
const MENU_CURSOR_OFFSET = 6;

/** FX-6：拖拽结束后位置存档写入的合并窗口（ms；free 后一次性落盘，不在 drag 过程狂写）。 */
const POSITION_SAVE_DELAY_MS = 160;

/** FX-6：PNG 快照导出倍率。 */
const PNG_EXPORT_SCALE = 2;

/** 迷你导航图尺寸（CSS px，内部按 devicePixelRatio 放大）。 */
const MINIMAP_W = 160;
const MINIMAP_H = 104;

/** 交互模式的类名常量（clearModeArtifacts 统一摘除）。 */
const TRACE_CLASSES = 'trace-dim trace-route trace-end';
const FOCUS_CLASSES = 'focus-dim';

/** 路径追踪状态机（横幅渲染）：待选终点 → 完成/不可达。
 *  待选起点（pick-a）不入状态机：由渲染层直接从 mode==='trace' && trace===null 派生——
 *  否则挂载时序（mode effect 先跑、topology 异步到达后数据 effect 再清一次）会把
 *  引导横幅覆盖掉，且 mode 值不变时 effect 不会重跑（v0.2.3 修复）。 */
type TraceState =
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
          // GEO-2：旧 v2 档案无 view 字段 → 默认 topo（向后兼容）
          view: isViewKind(rec.view) ? rec.view : DEFAULT_PREFS.view,
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

// —— FX-6：节点位置持久化（localStorage；位置数据量大且需跨会话保真，
// 与 sessionStorage 里的轻量 prefs 分开存放；隐私模式 try/catch 静默降级） ——

function positionsKey(persistKey: string | undefined): string {
  return `netops-lab.canvas.pos.${persistKey ?? ''}`;
}

/** 读取位置存档；不存在 / JSON 损坏 / 隐私模式 → 返回 null。 */
function readPositions(persistKey: string | undefined): Record<string, { x: number; y: number }> | null {
  if (persistKey === undefined || persistKey === '' || typeof localStorage === 'undefined') return null;
  try {
    const raw = localStorage.getItem(positionsKey(persistKey));
    if (raw === null) return null;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return null;
    const rec = parsed as Record<string, unknown>;
    if (rec.v !== 1 || typeof rec.p !== 'object' || rec.p === null) return null;
    const out: Record<string, { x: number; y: number }> = {};
    for (const [id, value] of Object.entries(rec.p as Record<string, unknown>)) {
      if (typeof value !== 'object' || value === null) continue;
      const v = value as Record<string, unknown>;
      const x = Number(v.x);
      const y = Number(v.y);
      if (Number.isFinite(x) && Number.isFinite(y)) out[id] = { x, y };
    }
    return Object.keys(out).length > 0 ? out : null;
  } catch {
    return null;
  }
}

/** 把当前全部节点 {id,x,y} 写入存档（隐私模式 / 配额写失败静默降级）。 */
function writePositions(persistKey: string | undefined, cy: Core): void {
  if (persistKey === undefined || persistKey === '' || typeof localStorage === 'undefined') return;
  try {
    const p: Record<string, { x: number; y: number }> = {};
    cy.nodes().forEach((node) => {
      p[node.id()] = { x: node.position('x'), y: node.position('y') };
    });
    localStorage.setItem(positionsKey(persistKey), JSON.stringify({ v: 1, p }));
  } catch {
    // 写失败不影响功能
  }
}

/** 清除位置存档（重置视图 / 切换自动布局时）。 */
function clearPositions(persistKey: string | undefined): void {
  if (persistKey === undefined || persistKey === '' || typeof localStorage === 'undefined') return;
  try {
    localStorage.removeItem(positionsKey(persistKey));
  } catch {
    // 忽略
  }
}

/** 是否存在位置存档（决定重置视图是否触发「清除 + 重排」）。 */
function hasPositions(persistKey: string | undefined): boolean {
  if (persistKey === undefined || persistKey === '' || typeof localStorage === 'undefined') return false;
  try {
    return localStorage.getItem(positionsKey(persistKey)) !== null;
  } catch {
    return false;
  }
}

/** FX-6：剪贴板写入（navigator.clipboard 优先，旧环境 execCommand 降级）。 */
async function copyTextToClipboard(text: string): Promise<void> {
  try {
    if (
      typeof navigator !== 'undefined' &&
      navigator.clipboard !== undefined &&
      typeof navigator.clipboard.writeText === 'function'
    ) {
      await navigator.clipboard.writeText(text);
      return;
    }
  } catch {
    // 非安全上下文 / 权限拒绝 → execCommand 降级
  }
  try {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.top = '-1000px';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand('copy');
    document.body.removeChild(textarea);
  } catch {
    // 剪贴板整体不可用：静默（不打断画布交互）
  }
}

/** FX-6：快捷键抢键守卫——事件目标或当前焦点是可编辑控件时不响应。 */
function isEditableTarget(node: EventTarget | null): boolean {
  if (!(node instanceof HTMLElement)) return false;
  const tag = node.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || node.isContentEditable;
}

/** FX-6：焦点在打开的对话框内（焦点陷阱）时不响应画布快捷键。 */
function inOpenDialog(node: EventTarget | null): boolean {
  return node instanceof Element && node.closest('[role="dialog"]') !== null;
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

/** 布局完成后再 fit（cose 为异步仿真、grid/circle 有 300ms 动画，立即 fit 抓到的是布局前包围盒）。 */
function fitToView(cy: Core): void {
  cy.fit(undefined, FIT_PADDING);
  // minZoom 在构造时已设为 0.55；fit 受其约束，这里再显式钳一次作保险
  if (cy.zoom() < MIN_ZOOM) {
    cy.zoom(MIN_ZOOM);
    cy.center();
  }
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
  /** FX-5：zen 全屏工作模式（画布卡片 fixed inset:0 盖住整页）。 */
  zen?: boolean;
  /** FX-5：zen 状态变化回调（由 LabWorkspace 持有并持久化 sessionStorage）。 */
  onZenChange?: (zen: boolean) => void;
  /** FX-5：分栏拖拽进行中——挂起 cy.resize，结束后补一次（避免逐帧 resize 卡顿）。 */
  resizeSuspended?: boolean;
}

/** 悬浮信息条内容（FX-4）：节点 code / 完整名 / 状态。 */
interface HoverInfo {
  code: string;
  name: string;
  status: SimEntityStatus;
}

/** FX-5：zen 左上浮动选中详情卡内容（设备 / 接口 / 链路三种选中）。 */
interface ZenInfo {
  kind: 'device' | 'interface' | 'link';
  /** 主编码（设备编码 / 链路标签）。 */
  code: string;
  /** 设备完整名（链路无）。 */
  name: string | null;
  status: SimEntityStatus;
  /** 设备：接口 up/down 计数。 */
  ifacesUp: number;
  ifacesDown: number;
  /** 设备：已配置 IPv4（'addr/prefix'，超出上限折叠计数）。 */
  ipv4: string[];
  ipv4More: number;
  /** 接口选中：'接口名 · 状态' 行 + 该接口 IPv4。 */
  ifaceLine: string | null;
  ifaceIpv4: string | null;
  /** 链路选中：两端 'DEV-xxx · 接口名'。 */
  linkEnds: { a: string; z: string } | null;
}

/** FX-5：zen 详情卡 IPv4 列表展示上限（超过折叠为「等 N 个」）。 */
const ZEN_INFO_IPV4_MAX = 4;

function buildZenInfo(topology: SimTopology, selected: SelectedEntity): ZenInfo | null {
  const emptyCounts = { ifacesUp: 0, ifacesDown: 0, ipv4: [] as string[], ipv4More: 0 };
  if (selected.kind === 'device') {
    const node = topology.nodes.find((n) => n.id === selected.id);
    if (node === undefined) return null;
    const ifacesUp = node.interfaces.filter((iface) => iface.status === 'up').length;
    const addrs = node.interfaces
      .filter((iface) => !isUnconfiguredIPv4(iface.ipv4Addr, iface.ipv4Prefix))
      .map((iface) => `${iface.ipv4Addr}/${iface.ipv4Prefix}`);
    return {
      kind: 'device',
      code: node.code,
      name: node.name,
      status: node.status,
      ifacesUp,
      ifacesDown: node.interfaces.length - ifacesUp,
      ipv4: addrs.slice(0, ZEN_INFO_IPV4_MAX),
      ipv4More: Math.max(addrs.length - ZEN_INFO_IPV4_MAX, 0),
      ifaceLine: null,
      ifaceIpv4: null,
      linkEnds: null,
    };
  }
  if (selected.kind === 'link') {
    const edge = topology.edges.find((e) => e.id === selected.id);
    if (edge === undefined) return null;
    const ifaceName = (ifaceId: string): string => {
      for (const node of topology.nodes) {
        const found = node.interfaces.find((iface) => iface.id === ifaceId);
        if (found !== undefined) return found.name;
      }
      return '';
    };
    return {
      kind: 'link',
      code: edge.label,
      name: null,
      status: edge.status,
      ...emptyCounts,
      ifaceLine: null,
      ifaceIpv4: null,
      linkEnds: {
        a: `${edge.aDeviceCode} · ${ifaceName(edge.aInterfaceId)}`,
        z: `${edge.zDeviceCode} · ${ifaceName(edge.zInterfaceId)}`,
      },
    };
  }
  // 接口选中：定位其所属设备 + 该接口行
  for (const node of topology.nodes) {
    const iface = node.interfaces.find((i) => i.id === selected.id);
    if (iface !== undefined) {
      const configured = !isUnconfiguredIPv4(iface.ipv4Addr, iface.ipv4Prefix);
      return {
        kind: 'interface',
        code: node.code,
        name: node.name,
        status: node.status,
        ...emptyCounts,
        ifaceLine: `${iface.name} · ${SIM_STATUS_LABELS[iface.status]}`,
        ifaceIpv4: configured ? `${iface.ipv4Addr}/${iface.ipv4Prefix}` : null,
        linkEnds: null,
      };
    }
  }
  return null;
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

export function TopologyCanvas({ topology, selected, onSelect, persistKey, zen = false, onZenChange, resizeSuspended = false }: TopologyCanvasProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const cyRef = useRef<Core | null>(null);
  const minimapRef = useRef<HTMLCanvasElement | null>(null);
  const onSelectRef = useRef(onSelect);
  /** 当前布局任务（防并发：新布局启动前先停旧的）。 */
  const layoutRunRef = useRef<Layouts | null>(null);
  /** FX-4：hover 信息条/容器光标的事件清理（实例卸载时调用）。 */
  const hoverCleanupRef = useRef<(() => void) | null>(null);
  const [prefs, setPrefs] = useState<CanvasPrefs>(() => readPrefs(persistKey));
  /** GEO-2：GeoCanvas 命令句柄（工具带适配/指北与 F 键经 ref 转发）。 */
  const geoRef = useRef<GeoCanvasHandle | null>(null);
  /** GEO-2：当前视图镜像（挂载期闭包 / 快捷键路径读最新值）。 */
  const viewRef = useRef<CanvasView>(prefs.view);
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
  // —— FX-6 状态/refs ——
  /** 右键菜单状态（null=关闭）。 */
  const [menu, setMenu] = useState<CanvasMenuState | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  /** 菜单宿主容器（.lab-canvas-wrap；菜单定位与越界翻转的参照系）。 */
  const wrapRef = useRef<HTMLDivElement | null>(null);
  /** 打开菜单前的焦点元素（Esc 关闭时还焦点）。 */
  const menuReturnFocusRef = useRef<HTMLElement | null>(null);
  /** 当前缩放百分比（zoom 事件 → rAF 节流刷新）。 */
  const [zoomPct, setZoomPct] = useState(100);
  /** persistKey 镜像（cxttap / 位置存档 / PNG 文件名在挂载期闭包里读最新值）。 */
  const persistKeyRef = useRef(persistKey);
  /** 位置存档生效（恢复存档或 freeze）→ 新增节点螺旋落位而非局部重排。 */
  const manualLayoutRef = useRef(false);
  /** 当前 persistKey 是否已尝试恢复位置存档（首个数据批次到达时执行一次）。 */
  const posRestoreDoneRef = useRef(false);
  /** 拖拽结束后待落盘的位置存档定时器（合并窗口内去抖）。 */
  const posSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  /** 右键「路径起点」跨模式切换传递（模式切换 effect 消费，避免闭包过期）。 */
  const pendingContextTraceRef = useRef<string | null>(null);
  /** 菜单动作转发（挂载期注册一次的 cxttap 处理器经 ref 读到最新实现）。 */
  const menuActionsRef = useRef<{
    focusTap: (id: string) => void;
    traceStart: (id: string) => void;
  }>({ focusTap: () => undefined, traceStart: () => undefined });
  /** 视图动作转发（全局快捷键 F/L、菜单 fit/reset、外部关闭读取）。 */
  const viewActionsRef = useRef<{
    fitView: () => void;
    resetView: () => void;
    toggleLabels: () => void;
    exportPng: () => void;
    closeMenu: (restoreFocus: boolean) => void;
  }>({
    fitView: () => undefined,
    resetView: () => undefined,
    toggleLabels: () => undefined,
    exportPng: () => undefined,
    closeMenu: () => undefined,
  });
  // —— FX-5 zen / 分栏 resize 状态 ——
  /** zen 镜像（fullscreenchange 监听器读最新值，避免闭包过期）。 */
  const zenRef = useRef(zen);
  /** onZenChange 镜像（挂载期闭包读最新实现）。 */
  const onZenChangeRef = useRef(onZenChange);
  /** resizeSuspended 镜像（挂载期注册一次的 ResizeObserver 闭包里读最新值）。 */
  const resizeSuspendedRef = useRef(false);

  const layout = prefs.layout;
  const mode = prefs.mode;
  const edgeLabels = prefs.labels;
  const view = prefs.view;

  useEffect(() => {
    viewRef.current = view;
  }, [view]);

  useEffect(() => {
    onSelectRef.current = onSelect;
  }, [onSelect]);

  useEffect(() => {
    onZenChangeRef.current = onZenChange;
  }, [onZenChange]);

  useEffect(() => {
    zenRef.current = zen;
  }, [zen]);

  useEffect(() => {
    resizeSuspendedRef.current = resizeSuspended;
  }, [resizeSuspended]);

  useEffect(() => {
    writePrefs(persistKey, prefs);
  }, [persistKey, prefs]);

  /** FX-6：persistKey 变化（路由切换工作台，组件不重挂载）→ 重置位置存档状态。 */
  useEffect(() => {
    persistKeyRef.current = persistKey;
    posRestoreDoneRef.current = false;
    manualLayoutRef.current = false;
    if (posSaveTimerRef.current !== null) {
      clearTimeout(posSaveTimerRef.current);
      posSaveTimerRef.current = null;
    }
  }, [persistKey]);

  useEffect(() => {
    modeRef.current = mode;
  }, [mode]);

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

  // FX-6：菜单 / 视图动作转发（挂载期注册一次的 cxttap、全局快捷键经 ref 读到最新实现）
  useEffect(() => {
    menuActionsRef.current = { focusTap: contextFocusTap, traceStart: contextTraceStart };
    viewActionsRef.current = { fitView, resetView, toggleLabels, exportPng, closeMenu };
  });

  // —— GEO-2：视图切换 ——
  // 离开拓扑图时清理模式痕迹与菜单（cy 实例保留在地图层下方，全部拓扑能力
  // 在回到 topo 视图时原样可用；地图视图自身无菜单/路径/聚焦语义）。
  useEffect(() => {
    if (view === 'topo') return;
    clearModeArtifacts();
    setMenu(null);
  }, [view, clearModeArtifacts]);

  function switchView(kind: CanvasView): void {
    setPrefs((p) => (p.view === kind ? p : { ...p, view: kind }));
  }

  /** 悬停信息条回调（拓扑 cy 事件与地图地标悬停共用同一状态与样式）。 */
  const handleGeoHoverInfo = useCallback((info: GeoHoverInfo | null): void => {
    setHoverInfo(info);
  }, []);

  // 模式切换 → 清掉上一模式的视觉残留（pick-a 引导横幅由渲染层从 mode/trace 派生）
  // （FX-6 例外：右键「路径起点」——先切换到路径模式再注入起点，跳过清除）
  useEffect(() => {
    const pendingTrace = pendingContextTraceRef.current;
    if (pendingTrace !== null) {
      pendingContextTraceRef.current = null;
      // 清上一模式的聚焦残留（traceTap 只清路径类，不清聚焦类）
      cyRef.current?.elements().removeClass(FOCUS_CLASSES);
      actionsRef.current.traceTap(pendingTrace);
      return;
    }
    clearModeArtifacts();
  }, [mode, clearModeArtifacts]);

  /**
   * FX-5：路径追踪/邻域聚焦是否有激活痕迹（Esc 逐级链路第二级判据）。
   * trace 看 traceRef 起点；focus 无 React 状态（直接加类），查 .focus-dim 元素。
   */
  const modeArtifactsActive = useCallback((): boolean => {
    if (traceRef.current.aId !== null) return true;
    const cy = cyRef.current;
    if (cy === null || cy.destroyed()) return false;
    return cy.elements().filter('.focus-dim').nonempty();
  }, []);

  // Esc 逐级链路（FX-5，与 FX-6 协调，不一键全清）：
  //   右键菜单开 → 先关菜单（还焦点）→ trace/focus 激活 → 清痕迹 → zen → 退 zen。
  // 对话框（焦点陷阱）打开时 Esc 归对话框（document 级监听先于 window，这里让路）。
  useEffect(() => {
    function onKey(event: KeyboardEvent): void {
      if (event.key !== 'Escape') return;
      if (inOpenDialog(event.target) || inOpenDialog(document.activeElement)) return;
      if (menu !== null) {
        viewActionsRef.current.closeMenu(true);
        return;
      }
      if (modeArtifactsActive()) {
        clearModeArtifacts();
        return;
      }
      if (zen) {
        onZenChangeRef.current?.(false);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [menu, zen, clearModeArtifacts, modeArtifactsActive]);

  // —— FX-5：原生 Fullscreen API 两级增强 ——
  // 进入 zen：尝试 requestFullscreen（成功 = 真全屏；无权限/不支持/非手势 → 拒绝
  // 或抛错，静默降级为应用内 CSS zen）；监听 fullscreenchange，用户在原生全屏按
  // Esc 退出时同步退 zen（CSS 层一并退，避免「盖着但非全屏」错乱态）。
  // 目标元素选 document.documentElement 而非画布卡片：真全屏时目标进入浏览器
  // 顶层（top layer，高于一切 z-index）——若用卡片会盖住 Toast(z100)；整文档
  // 全屏则 Toast 仍按 z-index 100 浮在 zen 卡片(z60) 之上。
  // 退出 zen（按钮/链路 Esc）：若仍在原生全屏则主动 exitFullscreen。
  useEffect(() => {
    if (zen) {
      const el = document.documentElement;
      if (typeof el.requestFullscreen === 'function') {
        try {
          const request = el.requestFullscreen();
          if (request !== undefined && typeof request.then === 'function') {
            request.then(undefined, () => undefined);
          }
        } catch {
          // 旧浏览器同步抛错 → 应用内 zen 降级
        }
      }
      const onFullscreenChange = (): void => {
        if (document.fullscreenElement === null && zenRef.current) {
          onZenChangeRef.current?.(false);
          // 顺带收起右键菜单：菜单是瞬时 UI，不应在模式退出后仍悬浮在画布上
          // （浏览器 Esc 退原生全屏时 keydown 被浏览器消费，页面链路收不到——
          // 若不在此关闭，菜单会残留到退出 zen 之后）。
          viewActionsRef.current.closeMenu(false);
        }
      };
      document.addEventListener('fullscreenchange', onFullscreenChange);
      return () => document.removeEventListener('fullscreenchange', onFullscreenChange);
    }
    if (typeof document.exitFullscreen === 'function' && document.fullscreenElement !== null) {
      try {
        const exit = document.exitFullscreen();
        if (exit !== undefined && typeof exit.then === 'function') {
          exit.then(undefined, () => undefined);
        }
      } catch {
        // 忽略（非全屏态调用会直接 resolve）
      }
    }
    return undefined;
  }, [zen]);

  // —— FX-5：resize 补偿 ——
  // 分栏拖拽结束（resizeSuspended 翻回 false）或 zen 切换后：补一次 cy.resize
  // + minimap 重绘（容器尺寸变化 Cytoscape 不会自动感知；拖拽期间 ResizeObserver
  // 被挂起，不逐帧 resize，这里一次补齐；视口 zoom/pan 保持不变）。
  useEffect(() => {
    if (resizeSuspended) return undefined;
    const raf = requestAnimationFrame(() => {
      const cy = cyRef.current;
      if (cy === null || cy.destroyed()) return;
      cy.resize();
      scheduleMinimapRef.current();
    });
    return () => cancelAnimationFrame(raf);
  }, [resizeSuspended, zen]);

  // —— FX-6：键盘快捷键（F=适配视图、L=切换链路标签；仅画布可见时生效） ——
  // 抢键守卫：输入框/textarea/contenteditable/对话框（焦点陷阱）内不响应；
  // 带修饰键（Ctrl/Cmd/Alt，如 Ctrl+F 浏览器查找）不响应；按住不放不重复触发。
  useEffect(() => {
    function onKey(event: KeyboardEvent): void {
      if (event.defaultPrevented || event.repeat) return;
      if (event.ctrlKey || event.metaKey || event.altKey) return;
      if (isEditableTarget(event.target) || isEditableTarget(document.activeElement)) return;
      if (inOpenDialog(event.target) || inOpenDialog(document.activeElement)) return;
      const key = event.key.toLowerCase();
      if (key === 'f') {
        viewActionsRef.current.closeMenu(false);
        viewActionsRef.current.fitView();
      } else if (key === 'l') {
        viewActionsRef.current.closeMenu(false);
        viewActionsRef.current.toggleLabels();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // —— FX-6：菜单外按下指针 → 关闭（捕获阶段，先于画布 tap 生效；菜单内点击不关） ——
  useEffect(() => {
    if (menu === null) return undefined;
    function onPointerDown(event: PointerEvent): void {
      const el = menuRef.current;
      if (el !== null && event.target instanceof Node && el.contains(event.target)) return;
      viewActionsRef.current.closeMenu(false);
    }
    window.addEventListener('pointerdown', onPointerDown, true);
    return () => window.removeEventListener('pointerdown', onPointerDown, true);
  }, [menu]);

  // —— FX-6：菜单落位（鼠标右下出现，越界向对侧翻转；稳定后焦点入首项） ————
  useLayoutEffect(() => {
    if (menu === null) return;
    const el = menuRef.current;
    const wrap = wrapRef.current;
    if (el === null || wrap === null) return;
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    const wrapW = wrap.clientWidth;
    const wrapH = wrap.clientHeight;
    let x = menu.cx + MENU_CURSOR_OFFSET;
    if (x + w > wrapW - 4) x = menu.cx - w - MENU_CURSOR_OFFSET;
    let y = menu.cy + MENU_CURSOR_OFFSET;
    if (y + h > wrapH - 4) y = menu.cy - h - MENU_CURSOR_OFFSET;
    x = Math.min(Math.max(x, 4), Math.max(4, wrapW - w - 4));
    y = Math.min(Math.max(y, 4), Math.max(4, wrapH - h - 4));
    if (x !== menu.x || y !== menu.y) {
      setMenu((m) => (m === menu ? { ...m, x, y } : m));
      return;
    }
    // 焦点管理：打开后焦点进入菜单（重排时焦点已在菜单内则不重复抢）
    const active = el.ownerDocument.activeElement;
    if (active === null || !el.contains(active)) {
      el.querySelector<HTMLButtonElement>('button[role="menuitem"]')?.focus();
    }
  }, [menu]);

  // —— 实例生命周期：只创建一次 ——
  useEffect(() => {
    if (containerRef.current === null) return undefined;
    const cy = cytoscape({
      container: containerRef.current,
      elements: [],
      style: CY_STYLE as unknown as StylesheetJson,
      layout: { name: 'cose' },
      autounselectify: true,
      // FX-6：不再设置 wheelSensitivity（v0.2.x 曾用 0.2 调低滚轮灵敏度）——
      // Cytoscape 对任何自定义值都会打印建议性 console warning，工业控制台
      // 要求零警告；默认 1 即主流 Cytoscape 应用（Grafana/Kiali 同源）手感。
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

    // —— FX-6：右键上下文菜单（cxttap=右键松开；event.target=元素或 cy 核心） ——
    // Cytoscape 自身已对容器 contextmenu preventDefault（原生菜单不弹出），
    // 这里按 target 类型组装菜单项；坐标取原生事件相对 wrap 的偏移。
    // 还焦目标：右键 pointerdown 捕获阶段（浏览器把焦点挪到 body 之前）快照
    // 当前焦点元素，Esc 关闭时精确还原（如工具带按钮），比 cxttap 时再取
    // （彼时焦点已被 mousedown 默认行为打到 body）更有意义。
    const wrapEl = wrapRef.current;
    const onWrapRightPointerDown = (e: PointerEvent): void => {
      if (e.button !== 2) return;
      const active = document.activeElement;
      menuReturnFocusRef.current = active instanceof HTMLElement ? active : null;
    };
    if (wrapEl !== null) {
      wrapEl.addEventListener('pointerdown', onWrapRightPointerDown, true);
    }
    cy.on('cxttap', (event) => {
      const wrap = wrapRef.current;
      if (wrap === null) return;
      const native = event.originalEvent;
      const rect = wrap.getBoundingClientRect();
      const cx = native.clientX - rect.left;
      const cyPos = native.clientY - rect.top;
      const openMenu = (label: string, items: CanvasMenuItem[]): void => {
        // 快照缺失（异常路径）时退回当前焦点元素
        if (menuReturnFocusRef.current === null) {
          const active = document.activeElement;
          menuReturnFocusRef.current = active instanceof HTMLElement ? active : null;
        }
        setMenu({ cx, cy: cyPos, x: cx, y: cyPos, label, items });
      };
      const target = event.target as NodeSingular | EdgeSingular | Core | null | undefined;
      if (target === cy) {
        // 空白画布：视图菜单（空拓扑不出菜单）
        if (cy.nodes().length === 0) return;
        openMenu(LAB_CANVAS_MENU_CANVAS, [
          {
            key: 'fit',
            label: LAB_CANVAS_CTX_FIT,
            title: LAB_CANVAS_CTX_FIT_TITLE,
            hint: 'F',
            run: () => viewActionsRef.current.fitView(),
          },
          {
            key: 'reset',
            label: LAB_CANVAS_CTX_RESET,
            title: LAB_CANVAS_CTX_RESET_TITLE,
            run: () => viewActionsRef.current.resetView(),
          },
        ]);
        return;
      }
      // 命中节点或边（二次收窄：Core 分支已返回）
      const ele = target as NodeSingular | EdgeSingular | null | undefined;
      if (ele === null || ele === undefined) return;
      if (ele.isNode()) {
        const id = ele.id();
        const code = String(ele.data('code') ?? id);
        openMenu(LAB_CANVAS_MENU_NODE, [
          {
            key: 'focus',
            label: LAB_CANVAS_CTX_FOCUS,
            title: LAB_CANVAS_CTX_FOCUS_TITLE,
            run: () => menuActionsRef.current.focusTap(id),
          },
          {
            key: 'trace',
            label: LAB_CANVAS_CTX_TRACE,
            title: LAB_CANVAS_CTX_TRACE_TITLE,
            run: () => menuActionsRef.current.traceStart(id),
          },
          {
            key: 'locate',
            label: LAB_CANVAS_CTX_LOCATE,
            title: LAB_CANVAS_CTX_LOCATE_TITLE,
            run: () => onSelectRef.current({ kind: 'device', id }),
          },
          {
            key: 'copy',
            label: LAB_CANVAS_CTX_COPY_ID,
            title: LAB_CANVAS_CTX_COPY_ID_TITLE,
            run: () => void copyTextToClipboard(code),
          },
        ]);
        return;
      }
      if (ele.isEdge()) {
        const id = ele.id();
        const label = String(ele.data('label') ?? '') || id;
        openMenu(LAB_CANVAS_MENU_EDGE, [
          {
            key: 'locate',
            label: LAB_CANVAS_CTX_LOCATE_LINK,
            title: LAB_CANVAS_CTX_LOCATE_LINK_TITLE,
            run: () => onSelectRef.current({ kind: 'link', id }),
          },
          {
            key: 'copy',
            label: LAB_CANVAS_CTX_COPY_LINK_ID,
            title: LAB_CANVAS_CTX_COPY_LINK_ID_TITLE,
            run: () => void copyTextToClipboard(label),
          },
        ]);
      }
    });

    // —— FX-6：缩放百分比（zoom 事件 → rAF 节流合并 setState；fit/1:1 均会触发） ——
    let zoomRaf: number | null = null;
    let zoomPctPending = 100;
    cy.on('zoom', () => {
      zoomPctPending = Math.round(cy.zoom() * 100);
      if (zoomRaf !== null) return;
      zoomRaf = requestAnimationFrame(() => {
        zoomRaf = null;
        setZoomPct(zoomPctPending);
      });
    });

    // —— FX-6：拖拽结束（free）→ 节点位置存档（160ms 合并窗口，不在 drag 过程狂写）；
    // 纯点击（grab→free 无位移，drag 事件未触发）不落盘。 ——
    let nodeDragMoved = false;
    cy.on('drag', 'node', () => {
      nodeDragMoved = true;
    });
    cy.on('free', 'node', () => {
      if (!nodeDragMoved) return;
      nodeDragMoved = false;
      if (posSaveTimerRef.current !== null) clearTimeout(posSaveTimerRef.current);
      posSaveTimerRef.current = setTimeout(() => {
        posSaveTimerRef.current = null;
        if (!cy.destroyed()) writePositions(persistKeyRef.current, cy);
      }, POSITION_SAVE_DELAY_MS);
    });

    cyRef.current = cy;
    const observer =
      typeof ResizeObserver === 'undefined'
        ? null
        : new ResizeObserver(() => {
            // FX-5：分栏拖拽期间挂起（不逐帧重设画布缓冲，拖完由补偿 effect 补一次）
            if (resizeSuspendedRef.current) return;
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
      if (zoomRaf !== null) cancelAnimationFrame(zoomRaf);
      if (posSaveTimerRef.current !== null) {
        // 卸载前冲刷未落盘的位置存档（拖完 160ms 合并窗口内直接 reload/切页不丢位置）
        clearTimeout(posSaveTimerRef.current);
        posSaveTimerRef.current = null;
        if (!cy.destroyed()) writePositions(persistKeyRef.current, cy);
      }
      scheduleMinimapRef.current = () => undefined;
      if (wrapEl !== null) wrapEl.removeEventListener('pointerdown', onWrapRightPointerDown, true);
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
    // 数据变更后旧路径/聚焦/右键菜单必然过期，先清视觉再同步数据
    clearModeArtifacts();
    setMenu(null);
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
      // FX-6：首个数据批次到达时尝试恢复位置存档（跳过自动布局，等价 freeze 语义）
      if (!posRestoreDoneRef.current) {
        posRestoreDoneRef.current = true;
        const stored = readPositions(persistKeyRef.current);
        if (stored !== null) {
          const uncovered = new Set<string>(addedIds);
          for (const [id, pos] of Object.entries(stored)) {
            const node = cy.getElementById(id);
            if (node.nonempty()) {
              node.position(pos);
              uncovered.delete(id);
            }
          }
          // 存档之后新增的节点：围绕既有质心螺旋落位（不打乱已恢复的排布）
          if (uncovered.size > 0) placeNewNodesSpiral(cy, uncovered);
          manualLayoutRef.current = true;
          fitToView(cy);
          scheduleMinimapRef.current();
          return;
        }
        manualLayoutRef.current = layout === 'freeze';
      }
      if (manualLayoutRef.current || layout === 'freeze') {
        // 锁定/手动排布模式：不打乱手动排布，新节点围绕质心螺旋落位
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
    // 取消未落盘的位置存档写入（布局即将重排，旧拖拽位置作废）
    if (posSaveTimerRef.current !== null) {
      clearTimeout(posSaveTimerRef.current);
      posSaveTimerRef.current = null;
    }
    if (kind === 'freeze') {
      // 锁定：停在当前排布，不动任何节点
      layoutRunRef.current?.stop();
      layoutRunRef.current = null;
      manualLayoutRef.current = true;
      scheduleMinimapRef.current();
      return;
    }
    // FX-6：切换到自动布局 → 清除手动位置存档（下次 reload 不再恢复旧排布）
    clearPositions(persistKeyRef.current);
    manualLayoutRef.current = false;
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
    // GEO-2：地图视图 → GeoCanvas 的 fitBounds（与拓扑 fitView 同一 F 键与语义）
    if (viewRef.current !== 'topo') {
      geoRef.current?.fitView();
      return;
    }
    const cy = cyRef.current;
    if (cy === null) return;
    fitToView(cy);
  }

  function resetView(): void {
    const cy = cyRef.current;
    if (cy === null) return;
    // FX-6：存在手动排布存档 → 清除并按当前布局重排（freeze 除外：无自动布局可跑，
    // 仅缩放 1:1 + 居中；重排路径由布局 stop 回调自动 fit）
    if (hasPositions(persistKeyRef.current)) {
      if (posSaveTimerRef.current !== null) {
        clearTimeout(posSaveTimerRef.current);
        posSaveTimerRef.current = null;
      }
      clearPositions(persistKeyRef.current);
      if (layout !== 'freeze') {
        manualLayoutRef.current = false;
        startLayout(cy, layout);
        return;
      }
    }
    cy.zoom(1);
    cy.center();
  }

  // —— FX-6：PNG 快照导出（完整拓扑、2 倍分辨率、bg 填充 CANVAS_BG 保证不透明） ——
  function exportPng(): void {
    const cy = cyRef.current;
    if (cy === null || cy.destroyed() || cy.nodes().length === 0) return;
    let dataUrl = '';
    try {
      dataUrl = cy.png({ full: true, scale: PNG_EXPORT_SCALE, bg: CANVAS_BG, output: 'base64uri' });
    } catch {
      return; // 渲染异常（超大画布等）：静默放弃导出
    }
    if (typeof dataUrl !== 'string' || dataUrl === '') return;
    const key = persistKeyRef.current;
    const filename = key !== undefined && key !== '' ? `netops-topology-${key}.png` : 'netops-topology.png';
    const anchor = document.createElement('a');
    anchor.href = dataUrl;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    document.body.removeChild(anchor);
  }

  /** FX-6：关闭右键菜单（restoreFocus=true 时把焦点还给打开前的元素）。 */
  function closeMenu(restoreFocus: boolean): void {
    if (restoreFocus) {
      const el = menuReturnFocusRef.current;
      if (el !== null && el.isConnected) el.focus();
    }
    menuReturnFocusRef.current = null;
    setMenu(null);
  }

  /**
   * FX-6：右键「设为路径追踪起点」——切换到路径模式并以该设备为起点。
   * 模式切换 effect 会清上一模式残留，用 pending ref 把起点注入到切换之后
   * （已在路径模式则直接开始新路径）。
   */
  function contextTraceStart(id: string): void {
    if (modeRef.current === 'trace') {
      actionsRef.current.traceTap(id);
      return;
    }
    pendingContextTraceRef.current = id;
    switchMode('trace');
  }

  /**
   * FX-6：右键「邻域聚焦」——先清路径/聚焦残留再执行（等价 focus 模式点击
   * 该节点的一跳压暗效果；模式本身不切换，一次性动作，Esc 取消）。
   */
  function contextFocusTap(id: string): void {
    const cy = cyRef.current;
    if (cy === null) return;
    cy.elements().removeClass(TRACE_CLASSES);
    cy.elements().removeClass(FOCUS_CLASSES);
    traceRef.current = { aId: null, bId: null };
    setTrace(null);
    actionsRef.current.focusTap(id);
  }

  /** FX-6：执行菜单项动作并关闭菜单。 */
  function runMenuItem(item: CanvasMenuItem): void {
    closeMenu(false);
    item.run();
  }

  /** FX-6：菜单键盘导航（↑↓/Home/End 循环、Esc 仅关菜单并还焦点（FX-5 逐级链路，
   * 不顺手清 trace/focus——那属于下一级）、Tab 关闭放行）。 */
  function onMenuKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    const el = menuRef.current;
    if (el === null) return;
    if (event.key === 'Escape') {
      event.stopPropagation();
      closeMenu(true);
      return;
    }
    if (event.key === 'Tab') {
      closeMenu(false);
      return;
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Home' || event.key === 'End') {
      event.stopPropagation();
      event.preventDefault();
      const buttons = Array.from(el.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]'));
      if (buttons.length === 0) return;
      let next: HTMLButtonElement | undefined;
      if (event.key === 'Home') {
        next = buttons[0];
      } else if (event.key === 'End') {
        next = buttons[buttons.length - 1];
      } else {
        const current = buttons.findIndex((btn) => btn === document.activeElement);
        const dir = event.key === 'ArrowDown' ? 1 : -1;
        const idx = current === -1 ? (dir === 1 ? 0 : buttons.length - 1) : (current + dir + buttons.length) % buttons.length;
        next = buttons[idx];
      }
      next?.focus();
    }
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

  /** FX-5：zen 左上浮动选中详情卡（无选中/选中实体不在拓扑中 → 不显示）。 */
  const zenInfo = useMemo(() => {
    if (!zen || selected === null || topology === null) return null;
    return buildZenInfo(topology, selected);
  }, [zen, selected, topology]);

  const hintText =
    view !== 'topo'
      ? LAB_GEO_HINT
      : mode === 'trace'
        ? LAB_MODE_TRACE_HINT
        : mode === 'focus'
          ? LAB_MODE_FOCUS_HINT
          : LAB_CANVAS_HINT;

  /** 路径追踪横幅文案（v0.2.2；v0.2.3 pick-a 改为渲染层派生）。 */
  let traceBanner: { className: string; text: string } | null = null;
  if (mode === 'trace' && trace === null) {
    // 路径模式下尚未选定起点：引导横幅（数据刷新/清除路径后自然回到此态）
    traceBanner = { className: 'lab-canvas-trace--pick', text: LAB_TRACE_PICK_A };
  } else if (trace !== null) {
    if (trace.stage === 'pick-b') {
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

  /** GEO-2：顶层视图分段（拓扑图 | 2D 地图 | 3D 地图；后两者互为俯视/斜视切换）。 */
  const VIEW_BUTTONS: Array<{ kind: CanvasView; label: string; title: string; icon: ReactNode }> = [
    { kind: 'topo', label: LAB_VIEW_TOPO, title: LAB_VIEW_TOPO_TITLE, icon: <IconTopology size={16} /> },
    { kind: 'geo2d', label: LAB_VIEW_GEO2D, title: LAB_VIEW_GEO2D_TITLE, icon: <IconMap2d /> },
    { kind: 'geo3d', label: LAB_VIEW_GEO3D, title: LAB_VIEW_GEO3D_TITLE, icon: <IconMap3d /> },
  ];

  const zenCardCls: string = `lab-canvas-card card${zen ? ' lab-canvas-card--zen' : ''}`;

  return (
    <div className={zenCardCls} aria-label={LAB_CANVAS_LABEL}>
      {/* 工具带：GEO-2 视图分段（拓扑图/2D/3D）+ 布局 6 态 + 交互模式 3 态 +
          标签开关（地图视图=设备标签）+ 地图专属控件（适配/指北）+ PNG + zen。
          拓扑专属工具在地图视图禁用（带说明 tooltip，保持工具带结构稳定）。 */}
      <div className="lab-canvas-toolbar" role="toolbar" aria-label={LAB_CANVAS_LABEL}>
        <span className="lab-canvas-toolbar-label">{LAB_VIEW}</span>
        <div className="canvas-seg" role="group" aria-label={LAB_VIEW}>
          {VIEW_BUTTONS.map((btn) => (
            <button
              key={btn.kind}
              type="button"
              className={`canvas-tool${view === btn.kind ? ' is-active' : ''}`}
              aria-pressed={view === btn.kind}
              title={btn.title}
              aria-label={btn.title}
              onClick={() => switchView(btn.kind)}
            >
              {btn.icon}
            </button>
          ))}
        </div>
        <span className="lab-canvas-toolbar-sep" aria-hidden="true" />
        <span className="lab-canvas-toolbar-label">{LAB_LAYOUT}</span>
        <div className="canvas-seg" role="group" aria-label={LAB_LAYOUT}>
          {LAYOUT_BUTTONS.map((btn) => (
            <button
              key={btn.kind}
              type="button"
              className={`canvas-tool${layout === btn.kind ? ' is-active' : ''}`}
              aria-pressed={layout === btn.kind}
              title={view === 'topo' ? btn.title : LAB_GEO_DISABLED_TITLE}
              aria-label={btn.title}
              disabled={view !== 'topo'}
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
              title={view === 'topo' ? btn.title : LAB_GEO_DISABLED_TITLE}
              aria-label={btn.title}
              disabled={view !== 'topo'}
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
          title={view === 'topo' ? LAB_CANVAS_LABELS : LAB_GEO_LABELS}
          aria-label={view === 'topo' ? LAB_CANVAS_LABELS : LAB_GEO_LABELS}
          onClick={toggleLabels}
        >
          <IconLabels />
        </button>
        {view !== 'topo' && (
          <>
            <button
              type="button"
              className="canvas-tool canvas-tool--action"
              title={LAB_GEO_FIT}
              aria-label={LAB_GEO_FIT}
              onClick={() => geoRef.current?.fitView()}
            >
              <IconFit />
            </button>
            <button
              type="button"
              className="canvas-tool canvas-tool--action"
              title={LAB_GEO_NORTH}
              aria-label={LAB_GEO_NORTH}
              onClick={() => geoRef.current?.resetNorth()}
            >
              <IconCompass />
            </button>
          </>
        )}
        <span className="lab-canvas-toolbar-sep" aria-hidden="true" />
        {/* FX-6：PNG 快照导出（空拓扑禁用；地图视图禁用——快照仅拓扑视图） */}
        <button
          type="button"
          className="canvas-tool canvas-tool--action"
          title={view === 'topo' ? LAB_CANVAS_EXPORT : LAB_GEO_EXPORT_DISABLED}
          aria-label={view === 'topo' ? LAB_CANVAS_EXPORT : LAB_GEO_EXPORT_DISABLED}
          disabled={empty || view !== 'topo'}
          onClick={exportPng}
        >
          <IconCamera />
        </button>
        <span className="lab-canvas-toolbar-hint">{hintText}</span>
        {/* FX-5：zen 全屏工作模式（工具带最右；zen 中变为带文字的退出按钮） */}
        {zen ? (
          <button
            type="button"
            className="canvas-zen-exit"
            title={LABWS_ZEN_EXIT}
            aria-label={LABWS_ZEN_EXIT}
            onClick={() => onZenChange?.(false)}
          >
            <IconMinimize size={14} />
            <span>{LABWS_ZEN_EXIT_LABEL}</span>
          </button>
        ) : (
          <button
            type="button"
            className="canvas-tool canvas-tool--action"
            title={LABWS_ZEN_ENTER}
            aria-label={LABWS_ZEN_ENTER}
            onClick={() => onZenChange?.(true)}
          >
            <IconExpand />
          </button>
        )}
      </div>
      <div ref={wrapRef} className="lab-canvas-wrap lab-canvas-wrap--dark">
        {/* cy 容器常驻（地图视图不卸载：GeoCanvas 以不透明层覆盖其上，交互被
            地图层接管；切回拓扑视图零重建，位置/选中/布局状态原样保留） */}
        <div ref={containerRef} className="lab-canvas" />
        {/* GEO-2：地图视图（2D/3D 同一组件，view 驱动相机与塔可见性） */}
        {view !== 'topo' && (
          <GeoCanvas
            ref={geoRef}
            topology={topology}
            selected={selected}
            onSelect={onSelect}
            view={view}
            labels={edgeLabels}
            zen={zen}
            resizeSuspended={resizeSuspended}
            onHoverInfo={handleGeoHoverInfo}
            onBackToTopo={() => switchView('topo')}
          />
        )}
        {/* 视图控制（右上角，仅拓扑视图；地图缩放/旋转用 maplibre 右侧控件） */}
        {view === 'topo' && (
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
        )}
        {/* FX-6：缩放百分比 chip（cy 缩放语义，仅拓扑视图；1:1 → 100%） */}
        {view === 'topo' && !empty && (
          <div
            className="lab-canvas-overlay canvas-zoom-chip"
            role="status"
            aria-live="off"
            title={LAB_CANVAS_ZOOM_CURRENT}
          >
            {zoomPct}%
          </div>
        )}
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
        {/* FX-5：zen 左上浮动选中详情卡（位于 hover 信息条下方；无选中不显示。
            zen 中右侧表格不可见，画布选中后信息在此不断档） */}
        {zenInfo !== null && (
          <div className="lab-canvas-overlay canvas-zen-info" role="status" aria-label={LABWS_ZEN_INFO_LABEL}>
            <div className="zen-info-head">
              <span className={`hoverinfo-dot hoverinfo-dot--${zenInfo.status}`} aria-hidden="true" />
              <span className="zen-info-code">{zenInfo.code}</span>
              <span className="zen-info-status">{SIM_STATUS_LABELS[zenInfo.status]}</span>
            </div>
            {zenInfo.name !== null && (
              <div className="zen-info-name" title={zenInfo.name}>
                {zenInfo.name}
              </div>
            )}
            {zenInfo.kind === 'device' && (
              <div className="zen-info-row">
                {format(LABWS_ZEN_INFO_IFACES, {
                  total: zenInfo.ifacesUp + zenInfo.ifacesDown,
                  up: zenInfo.ifacesUp,
                  down: zenInfo.ifacesDown,
                })}
              </div>
            )}
            {zenInfo.ifaceLine !== null && <div className="zen-info-row">{zenInfo.ifaceLine}</div>}
            {zenInfo.kind === 'device' && (
              <div className="zen-info-ipv4-block">
                <div className="zen-info-ipv4-label">{LABWS_ZEN_INFO_IPV4}</div>
                {zenInfo.ipv4.length === 0 ? (
                  <div className="zen-info-row zen-info-row--muted">{LABWS_ZEN_INFO_IPV4_NONE}</div>
                ) : (
                  <>
                    {zenInfo.ipv4.map((addr) => (
                      <div key={addr} className="zen-info-ipv4">
                        {addr}
                      </div>
                    ))}
                    {zenInfo.ipv4More > 0 && (
                      <div className="zen-info-row zen-info-row--muted">
                        {format(LABWS_ZEN_INFO_IPV4_MORE, { n: zenInfo.ipv4More })}
                      </div>
                    )}
                  </>
                )}
              </div>
            )}
            {zenInfo.kind === 'interface' && zenInfo.ifaceIpv4 !== null && (
              <div className="zen-info-ipv4">{zenInfo.ifaceIpv4}</div>
            )}
            {zenInfo.linkEnds !== null && (
              <div className="zen-info-ipv4-block">
                <div className="zen-info-ipv4-label">{LABWS_ZEN_INFO_LINK_ENDS}</div>
                <div className="zen-info-ends">
                  <span className="zen-info-end">{zenInfo.linkEnds.a}</span>
                  <span className="zen-info-arrow" aria-hidden="true">↔</span>
                  <span className="zen-info-end">{zenInfo.linkEnds.z}</span>
                </div>
              </div>
            )}
          </div>
        )}
        {/* 路径追踪横幅（v0.2.2，顶部居中）：仅拓扑视图（地图视图无路径语义） */}
        {view === 'topo' && traceBanner !== null && (
          <div className={`lab-canvas-overlay lab-canvas-trace ${traceBanner.className}`} role="status" aria-live="polite">
            <span className="trace-text">{traceBanner.text}</span>
            <button
              type="button"
              className="trace-close"
              title={mode === 'trace' && trace === null ? LAB_TRACE_EXIT_MODE : LAB_TRACE_CLEAR}
              aria-label={mode === 'trace' && trace === null ? LAB_TRACE_EXIT_MODE : LAB_TRACE_CLEAR}
              onClick={() => {
                // pick-a 引导态：横幅随路径模式存在，关闭 = 退出路径模式回选择模式；
                // 其余态（pick-b/done/unreachable）：清除当前路径，回到引导态。
                if (mode === 'trace' && trace === null) {
                  switchMode('select');
                } else {
                  clearModeArtifacts();
                }
              }}
            >
              <IconClose size={12} />
            </button>
          </div>
        )}
        {/* 迷你导航图（v0.2.2，右下角；仅拓扑视图——地图视图右下角由 maplibre
            NavigationControl 占位）：点击/拖动定位主视图 */}
        {view === 'topo' && !empty && (
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
        {/* FX-6：右键上下文菜单（节点/边/空白三套；Esc 或点击别处关闭，
            鼠标右下出现、越界翻转；快捷键提示列） */}
        {menu !== null && (
          <div
            ref={menuRef}
            className="canvas-menu"
            role="menu"
            aria-label={menu.label}
            style={{ left: menu.x, top: menu.y }}
            onKeyDown={onMenuKeyDown}
            onContextMenu={(event) => event.preventDefault()}
          >
            {menu.items.map((item) => (
              <button
                key={item.key}
                type="button"
                role="menuitem"
                className="canvas-menu-item"
                title={item.title}
                onClick={() => runMenuItem(item)}
              >
                <span className="canvas-menu-label">{item.label}</span>
                {item.hint !== undefined ? (
                  <span className="canvas-menu-key" aria-hidden="true">
                    {item.hint}
                  </span>
                ) : null}
              </button>
            ))}
          </div>
        )}
        {view === 'topo' && (
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
        )}
        {view === 'topo' && empty && (
          <div className="lab-canvas-empty">{topology === null ? LOADING_TEXT : LAB_CANVAS_EMPTY}</div>
        )}
      </div>
    </div>
  );
}
