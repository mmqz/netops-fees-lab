/**
 * 内联 SVG 图标集（无图标库依赖）。
 * 统一 24×24 viewBox、stroke 风格，颜色继承 currentColor。
 */

export interface IconProps {
  size?: number;
  className?: string;
}

function svgProps(size: number): { width: number; height: number; viewBox: string; fill: string; stroke: string; strokeWidth: number; strokeLinecap: 'round'; strokeLinejoin: 'round'; 'aria-hidden': true } {
  return {
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 2,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
  };
}

export function IconHome({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M3 9.5 12 3l9 6.5" />
      <path d="M5 9.5V21h14V9.5" />
      <path d="M9.5 21v-6h5v6" />
    </svg>
  );
}

export function IconUpload({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="17 8 12 3 7 8" />
      <line x1="12" y1="3" x2="12" y2="15" />
    </svg>
  );
}

export function IconTable({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <line x1="3" y1="9" x2="21" y2="9" />
      <line x1="9" y1="21" x2="9" y2="9" />
    </svg>
  );
}

export function IconWarning({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
      <line x1="12" y1="9" x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </svg>
  );
}

export function IconReport({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
    </svg>
  );
}

export function IconClose({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}

export function IconSearch({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="11" cy="11" r="7" />
      <line x1="21" y1="21" x2="16.5" y2="16.5" />
    </svg>
  );
}

export function IconRefresh({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <polyline points="23 4 23 10 17 10" />
      <polyline points="1 20 1 14 7 14" />
      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
    </svg>
  );
}

export function IconDownload({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </svg>
  );
}

export function IconTrash({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6M14 11v6" />
      <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
    </svg>
  );
}

export function IconCheck({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

export function IconChevronLeft({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <polyline points="15 18 9 12 15 6" />
    </svg>
  );
}

export function IconChevronRight({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <polyline points="9 18 15 12 9 6" />
    </svg>
  );
}

export function IconInfo({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="9" />
      <line x1="12" y1="16" x2="12" y2="12" />
      <line x1="12" y1="8" x2="12.01" y2="8" />
    </svg>
  );
}

export function IconDoc({ size = 32, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
    </svg>
  );
}

/** 仿真工作台：烧瓶（工作台列表入口）。 */
export function IconFlask({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M9 3h6" />
      <path d="M10 3v6l-5.5 9A2 2 0 0 0 6.2 21h11.6a2 2 0 0 0 1.7-3L14 9V3" />
      <path d="M7.5 15h9" />
    </svg>
  );
}

/** 仿真工作台：拓扑（互联节点，/lab/:id 入口与画布共用）。 */
export function IconTopology({ size = 20, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="5" cy="6" r="2.5" />
      <circle cx="19" cy="6" r="2.5" />
      <circle cx="12" cy="18" r="2.5" />
      <line x1="7.2" y1="7" x2="16.8" y2="7" />
      <line x1="6.3" y1="8.2" x2="10.4" y2="16" />
      <line x1="17.7" y1="8.2" x2="13.6" y2="16" />
    </svg>
  );
}

/** 画布工具栏：力导向布局。 */
export function IconLayoutForce({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="2.5" />
      <circle cx="4" cy="5" r="2" />
      <circle cx="20" cy="5" r="2" />
      <circle cx="4" cy="19" r="2" />
      <circle cx="20" cy="19" r="2" />
      <line x1="5.8" y1="6.2" x2="10" y2="10.4" />
      <line x1="18.2" y1="6.2" x2="14" y2="10.4" />
      <line x1="5.8" y1="17.8" x2="10" y2="13.6" />
      <line x1="18.2" y1="17.8" x2="14" y2="13.6" />
    </svg>
  );
}

/** 画布工具栏：网格布局。 */
export function IconLayoutGrid({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="5" cy="5" r="2" />
      <circle cx="12" cy="5" r="2" />
      <circle cx="19" cy="5" r="2" />
      <circle cx="5" cy="12" r="2" />
      <circle cx="12" cy="12" r="2" />
      <circle cx="19" cy="12" r="2" />
      <circle cx="5" cy="19" r="2" />
      <circle cx="12" cy="19" r="2" />
      <circle cx="19" cy="19" r="2" />
    </svg>
  );
}

/** 画布工具栏：环形布局。 */
export function IconLayoutCircle({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="9" strokeDasharray="2 4" />
      <circle cx="12" cy="3" r="2" />
      <circle cx="18.4" cy="15.5" r="2" />
      <circle cx="5.6" cy="15.5" r="2" />
    </svg>
  );
}

/** 画布工具栏：层级布局（自上而下树形）。 */
export function IconLayoutHier({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <rect x="9" y="3" width="6" height="4" rx="1" />
      <rect x="3" y="17" width="6" height="4" rx="1" />
      <rect x="15" y="17" width="6" height="4" rx="1" />
      <rect x="9" y="17" width="6" height="4" rx="1" />
      <path d="M12 7v4" />
      <path d="M6 13h12v-2" />
      <path d="M6 13v4M12 11v6M18 13v4" />
    </svg>
  );
}

/** 画布工具栏：同心圆布局（中心辐射）。 */
export function IconLayoutConcentric({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="8.5" strokeDasharray="2 3" />
      <circle cx="12" cy="12" r="5" strokeDasharray="2 3" />
      <circle cx="12" cy="12" r="1.8" />
    </svg>
  );
}

/** 画布工具栏：锁定布局（图钉）。 */
export function IconLayoutFreeze({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M9 3h6" />
      <path d="M10 3v5l-3.5 5.5A2 2 0 0 0 8.2 17h7.6a2 2 0 0 0 1.7-3.5L14 8V3" />
      <path d="M12 17v4" />
    </svg>
  );
}

/** 画布交互模式：选择（光标）。 */
export function IconModeSelect({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M5 3l14 8.5-6.2 1.6L10 19z" />
    </svg>
  );
}

/** 画布交互模式：路径追踪（两点间折线路由）。 */
export function IconModeTrace({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="5.5" cy="18.5" r="2" />
      <circle cx="18.5" cy="5.5" r="2" />
      <path d="M7 17L17 7" strokeDasharray="2.5 2.5" />
      <path d="M7 17l4-1.5M17 7l-1.5 4" strokeDasharray="none" />
    </svg>
  );
}

/** 画布交互模式：邻域聚焦（聚光/目标）。 */
export function IconModeFocus({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="8.5" />
      <circle cx="12" cy="12" r="3.5" />
      <path d="M12 1.5v3M12 19.5v3M1.5 12h3M19.5 12h3" />
    </svg>
  );
}

/** 画布视图开关：链路标签。 */
export function IconLabels({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M20.6 13.4 11 3.8a2 2 0 0 0-1.4-.6H4v5.6a2 2 0 0 0 .6 1.4l9.6 9.6a1.5 1.5 0 0 0 2.1 0l4.3-4.3a1.5 1.5 0 0 0 0-2.1z" />
      <circle cx="7.5" cy="7.5" r="0.5" fill="currentColor" />
    </svg>
  );
}

/** 画布视图控制：放大。 */
export function IconZoomIn({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="11" cy="11" r="7" />
      <line x1="11" y1="8" x2="11" y2="14" />
      <line x1="8" y1="11" x2="14" y2="11" />
      <line x1="21" y1="21" x2="16.5" y2="16.5" />
    </svg>
  );
}

/** 画布视图控制：缩小。 */
export function IconZoomOut({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="11" cy="11" r="7" />
      <line x1="8" y1="11" x2="14" y2="11" />
      <line x1="21" y1="21" x2="16.5" y2="16.5" />
    </svg>
  );
}

/** 画布视图控制：适配视图（四角框）。 */
export function IconFit({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M4 9V5.5A1.5 1.5 0 0 1 5.5 4H9" />
      <path d="M15 4h3.5A1.5 1.5 0 0 1 20 5.5V9" />
      <path d="M20 15v3.5a1.5 1.5 0 0 1-1.5 1.5H15" />
      <path d="M9 20H5.5A1.5 1.5 0 0 1 4 18.5V15" />
      <circle cx="12" cy="12" r="2.5" />
    </svg>
  );
}

/** 画布视图控制：重置缩放 1:1（瞄准）。 */
export function IconResetView({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="7" />
      <line x1="12" y1="2.5" x2="12" y2="6" />
      <line x1="12" y1="18" x2="12" y2="21.5" />
      <line x1="2.5" y1="12" x2="6" y2="12" />
      <line x1="18" y1="12" x2="21.5" y2="12" />
      <circle cx="12" cy="12" r="2" />
    </svg>
  );
}

/** FX-6：画布工具带 PNG 快照导出（相机）。 */
export function IconCamera({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z" />
      <circle cx="12" cy="13" r="4" />
    </svg>
  );
}

/** FX-5：zen 全屏工作模式——进入（四角外扩）。 */
export function IconExpand({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M9 3H5.5A2.5 2.5 0 0 0 3 5.5V9" />
      <path d="M15 3h3.5A2.5 2.5 0 0 1 21 5.5V9" />
      <path d="M9 21H5.5A2.5 2.5 0 0 1 3 18.5V15" />
      <path d="M15 21h3.5a2.5 2.5 0 0 0 2.5-2.5V15" />
    </svg>
  );
}

/** FX-5：zen 全屏工作模式——退出（四角内收）。 */
export function IconMinimize({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M3 9h3.5A2.5 2.5 0 0 0 9 6.5V3" />
      <path d="M21 9h-3.5A2.5 2.5 0 0 1 15 6.5V3" />
      <path d="M3 15h3.5A2.5 2.5 0 0 1 9 17.5V21" />
      <path d="M21 15h-3.5a2.5 2.5 0 0 0-2.5 2.5V21" />
    </svg>
  );
}

/** FX-5：折叠右侧面板（面板示意 + 右向箭头，面板收向右侧）。 */
export function IconPanelCollapse({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M15 4v16" />
      <path d="m9 9 3 3-3 3" />
    </svg>
  );
}

/** FX-5：展开右侧面板（面板示意 + 左向箭头，面板自右侧展开）。 */
export function IconPanelExpand({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M15 4v16" />
      <path d="m12 9-3 3 3 3" />
    </svg>
  );
}

/** GEO-2：视图分段——2D 地图（平面网格 + 定位点，俯视）。 */
export function IconMap2d({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M3 9h18M3 15h18M9 3v18M15 3v18" strokeDasharray="0" />
      <circle cx="15" cy="9" r="1.6" fill="currentColor" stroke="none" />
      <circle cx="9" cy="15" r="1.6" fill="currentColor" stroke="none" />
    </svg>
  );
}

/** GEO-2：视图分段——3D 地图（等距立方体，斜视）。 */
export function IconMap3d({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M12 2.5l8.5 4.8v9.4L12 21.5l-8.5-4.8V7.3z" />
      <path d="M3.5 7.3 12 12.1l8.5-4.8" />
      <path d="M12 12.1v9.4" />
    </svg>
  );
}

/** GEO-2：地图视图——指北复位（罗盘针）。 */
export function IconCompass({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="9" />
      <path d="M15.6 8.4l-2 5.2-5.2 2 2-5.2z" />
      <path d="M12 3v1.5M21 12h-1.5M12 21v-1.5M3 12h1.5" />
    </svg>
  );
}

/** Toast 状态图标：错误（实感更重的警示）。 */
export function IconAlert({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
      <line x1="12" y1="9" x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </svg>
  );
}

/* —— #47 工作台动作按钮图标化新增（undo / diff / checkpoint / ops）—— */

/** 撤销（↶ 弧形回退箭头）。 */
export function IconUndo({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M9 14 4 9l5-5" />
      <path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11" />
    </svg>
  );
}

/** 前后对比（左右分栏 + 中缝，diff 语义）。 */
export function IconDiff({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <rect x="3" y="4" width="7" height="16" rx="1" />
      <rect x="14" y="4" width="7" height="16" rx="1" />
      <path d="M12 3v18" strokeDasharray="2 2" />
    </svg>
  );
}

/** 检查点（旗帜——「在当前位置插旗存档」）。 */
export function IconFlag({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <line x1="5" y1="21" x2="5" y2="4" />
      <path d="M5 4h13l-3 4 3 4H5" />
    </svg>
  );
}

/** 执行操作（铅笔——typed ops 编辑语义）。 */
export function IconPencil({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <path d="M17 3a2.8 2.8 0 0 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z" />
    </svg>
  );
}

/** 维护窗口（时钟——计划内时间区间语义）。 */
export function IconClock({ size = 16, className }: IconProps) {
  return (
    <svg {...svgProps(size)} className={className}>
      <circle cx="12" cy="12" r="9" />
      <polyline points="12 7 12 12 16 14" />
    </svg>
  );
}

