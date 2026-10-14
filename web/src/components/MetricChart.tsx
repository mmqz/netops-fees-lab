/**
 * 指标时序 SVG 折线图（v0.9-R2，资源详情抽屉「性能指标」页签）：
 *
 * 设计约束：
 * - **零图表库**：纯 SVG 手绘（项目红线之一是依赖面克制——maplibre/cytoscape
 *   之外不再引第三方可视化库；14 键 × 7 日的小数据量用不上图形引擎）。
 * - **确定性渲染**：同一点集产出的 SVG 逐字节一致（坐标纯函数计算，无随机、
 *   无动画）——可对照测试。
 * - **语义化**：y 轴 min/mid/max 标签 + x 轴首末日期；网格 3 线；点集 ≤ 60
 *   画圆点（title 悬浮「日期 值」），> 60 只画折线；suspect 点橙色描边
 *   （catalog 建议界外的点，ADR-0014 语义），missing 点空心。
 * - 单位与极值兜底：min==max（恒定序列）时人为拉开 ±1，避免零高度路径。
 */

import type { MetricPoint, MetricSeriesResult } from '../api/types';
import {
  METRIC_CHART_LAST,
  METRIC_CHART_POINTS,
  METRIC_CHART_SUSPECT,
  METRIC_CHART_MISSING,
  format,
  formatDateTime,
} from '../i18n';

const VIEW_W = 320;
const VIEW_H = 150;
const PAD_L = 42;
const PAD_R = 10;
const PAD_T = 10;
const PAD_B = 20;
/** 点位绘制上限（超过则只画折线）。 */
const MAX_DOTS = 60;

const C_LINE = '#2f5fe0';
const C_LINE_DOWN = '#ff5252';
const C_GRID = '#e3e8f0';
const C_TEXT = '#8896ab';
const C_SUSPECT = '#e8842c';
const C_MISSING = '#b6c1d1';

function fmtVal(value: number): string {
  if (Number.isInteger(value) && Math.abs(value) < 1e6) return String(value);
  return String(Number(value.toPrecision(3)));
}

function fmtDay(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const pad = (n: number): string => String(n).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

interface ChartGeometry {
  points: Array<{ x: number; y: number; p: MetricPoint }>;
  min: number;
  max: number;
}

function geometry(points: MetricPoint[]): ChartGeometry {
  const xs = points.map((p) => Date.parse(p.ts)).filter((t) => !Number.isNaN(t));
  const tMin = xs.length > 0 ? Math.min(...xs) : 0;
  const tMax = xs.length > 0 ? Math.max(...xs) : tMin + 1;
  let min = Math.min(...points.map((p) => p.value));
  let max = Math.max(...points.map((p) => p.value));
  if (min === max) {
    min -= 1;
    max += 1;
  } else {
    const pad = (max - min) * 0.08;
    min -= pad;
    max += pad;
  }
  const spanX = tMax - tMin || 1;
  const spanY = max - min || 1;
  const width = VIEW_W - PAD_L - PAD_R;
  const height = VIEW_H - PAD_T - PAD_B;
  const mapped = points.map((p) => {
    const t = Date.parse(p.ts);
    const x = PAD_L + (Number.isNaN(t) ? 0 : ((t - tMin) / spanX) * width);
    const y = PAD_T + height - ((p.value - min) / spanY) * height;
    return { x, y, p };
  });
  return { points: mapped, min, max };
}

/** 单条序列的卡片：标题行（标签/单位/点数/最新值）+ SVG 折线。 */
export function MetricChart({ series, label }: { series: MetricSeriesResult; label: string }) {
  const pts = series.points;
  if (pts.length === 0) return null;
  const geom = geometry(pts);
  const path = geom.points.map((pt) => `${pt.x.toFixed(1)},${pt.y.toFixed(1)}`).join(' ');
  const dots = pts.length <= MAX_DOTS;
  const mid = (geom.min + geom.max) / 2;
  const midY = PAD_T + (VIEW_H - PAD_T - PAD_B) / 2;
  const last = pts[pts.length - 1];
  const first = pts[0];
  /** 劣化方向（direction=down 时值越低越差）只影响折线配色提示。 */
  const declining = last.value < first.value;

  return (
    <div className="metric-card">
      <div className="metric-card-head">
        <span className="metric-card-label">{label}</span>
        {series.unit !== '' && <span className="chip chip--mono metric-card-unit">{series.unit}</span>}
        <span className="muted-note">
          {format(METRIC_CHART_POINTS, { n: pts.length })} ·{' '}
          {format(METRIC_CHART_LAST, { v: fmtVal(last.value) })}
        </span>
      </div>
      <svg
        className="metric-chart"
        viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
        role="img"
        aria-label={`${label} ${series.unit}`}
      >
        {/* 网格 + y 轴标签（min/mid/max） */}
        {[geom.min, mid, geom.max].map((v, i) => {
          const y = i === 0 ? VIEW_H - PAD_B : i === 1 ? midY : PAD_T;
          return (
            <g key={i}>
              <line x1={PAD_L} y1={y} x2={VIEW_W - PAD_R} y2={y} stroke={C_GRID} strokeWidth={1} />
              <text x={PAD_L - 6} y={y + 3} textAnchor="end" fontSize={9} fill={C_TEXT}>
                {fmtVal(v)}
              </text>
            </g>
          );
        })}
        {/* x 轴首末日期 */}
        <text x={PAD_L} y={VIEW_H - 6} fontSize={9} fill={C_TEXT}>
          {fmtDay(first.ts)}
        </text>
        <text x={VIEW_W - PAD_R} y={VIEW_H - 6} fontSize={9} fill={C_TEXT} textAnchor="end">
          {fmtDay(last.ts)}
        </text>
        {/* 折线 */}
        <polyline
          points={path}
          fill="none"
          stroke={declining ? C_LINE_DOWN : C_LINE}
          strokeWidth={1.6}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
        {/* 点（≤60 才画；title 悬浮明细） */}
        {dots &&
          geom.points.map((pt, i) => (
            <circle
              key={i}
              cx={pt.x}
              cy={pt.y}
              r={2.6}
              fill={pt.p.quality === 'missing' ? '#ffffff' : pt.p.quality === 'suspect' ? C_SUSPECT : C_LINE}
              stroke={pt.p.quality === 'missing' ? C_MISSING : pt.p.quality === 'suspect' ? C_SUSPECT : 'none'}
              strokeWidth={1}
            >
              <title>
                {`${formatDateTime(pt.p.ts)} ${fmtVal(pt.p.value)}${series.unit}`}
                {pt.p.quality === 'suspect' ? ` · ${METRIC_CHART_SUSPECT}` : ''}
                {pt.p.quality === 'missing' ? ` · ${METRIC_CHART_MISSING}` : ''}
              </title>
            </circle>
          ))}
      </svg>
    </div>
  );
}
