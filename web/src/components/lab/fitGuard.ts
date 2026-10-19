/**
 * #62（#54 遗留）：letterbox 护栏的「可达性」判定，纯函数抽取。
 *
 * 旧缺陷：护栏分支用 `Math.max(FIT_PAD_V_MIN, padNeeded)` 静默钳掉负数——
 * 当内容比 `(vh/vw) / UTILIZATION_TARGET` 更高瘦时，72% 横向利用率在数学上
 * 不可达（达成它所需的 zoom 会让内容高度超出画布），但代码不区分「可达而
 * 收窄留白」与「不可达而尽力」，退化成普通 fit 而不自知。
 *
 * 本模块把判定拆成显式三态：
 *
 * - plain          内容宽扁（或视口比内容更瘦长）：统一留白即达标，无护栏动作；
 * - utilization    高瘦但可达：纵向留白收窄到精确值，72% 横向利用率成立；
 * - vertical-bound 高瘦且不可达：横向目标物理不可达，按最小纵向留白尽力
 *                  （纵向铺满画布、内容完整可见——高瘦内容的正确验收主指标
 *                  是纵向利用率，不是横向）。
 *
 * 数学口径（#62 修复时校准）：留白是**模型单位**——fitToView 把加垫后的
 * 包围盒交给 cy.fit，其 zoom = vh / (bb.h + 2·padY)。达成横向利用率 u
 * 需要 zoom ≥ u·vw/bb.w，解出留白上界：
 *
 *   vh / (bb.h + 2·padY) ≥ u·vw / bb.w
 *   ⇔ padY ≤ (vh·bb.w / (u·vw) − bb.h) / 2   （记 padMax）
 *   padMax ≥ FIT_PAD_V_MIN ⇔ u 可达（padY 取 min(FIT_PADDING, padMax)）
 *   padMax  < FIT_PAD_V_MIN ⇔ u 不可达（纵向约束主导，留白取下限尽力）
 *
 * 旧实现的 padNeeded = (vh − zoomNeeded·bb.h)/2 是**屏幕像素**模型——
 * 与「留白按模型单位进 fit」的实现不符，可达分支会欠冲（横向利用率达不到
 * 目标还自以为达标）；#62 的不可达判定结论一致（两种模型都判负数），
 * 本模块统一到模型单位口径。
 *
 * mode 同时是埋点面：TopologyCanvas 把它写到容器的 data-fit-mode 属性上，
 * 浏览器级验证（CDP）与人工排查都能直接读出 fit 走了哪个分支。
 */

/** fit 的横向留白（px）。 */
export const FIT_PADDING = 40;

/** 纵向留白可收窄到的下限（px）——内容永不溢出画布。 */
export const FIT_PAD_V_MIN = 12;

/** 护栏的横向利用率目标（高于 0.70 验收线留测量余量）。 */
export const FIT_UTILIZATION_TARGET = 0.72;

/** fit 三态（见模块注释）。 */
export type FitMode = 'plain' | 'utilization' | 'vertical-bound';

/** computeFitPaddings 的输入：结构包围盒 + 视口。 */
export interface FitBox {
  w: number;
  h: number;
}

export interface FitPaddings {
  padX: number;
  padY: number;
  mode: FitMode;
}

/**
 * 判定 fit 留白与可达性。纯函数：同输入同输出，无 DOM 依赖。
 * 数学口径见模块注释（模型单位 padMax）。
 */
export function computeFitPaddings(bb: FitBox, vw: number, vh: number): FitPaddings {
  const valid =
    Number.isFinite(vw) && vw > 0 && Number.isFinite(vh) && vh > 0 && Number.isFinite(bb.w) && bb.w > 0 && bb.h > 0;
  if (!valid) return { padX: FIT_PADDING, padY: FIT_PADDING, mode: 'plain' };

  // 宽扁内容（或视口更瘦长）：统一留白，横向利用率天然不低，无护栏动作。
  if (bb.w / bb.h >= vw / vh) return { padX: FIT_PADDING, padY: FIT_PADDING, mode: 'plain' };

  // 高瘦内容：横向利用率护栏（模型单位口径）。
  const zoomTarget = (FIT_UTILIZATION_TARGET * vw) / bb.w;
  const padMax = (vh / zoomTarget - bb.h) / 2;
  if (padMax >= FIT_PAD_V_MIN) {
    // 目标可达：纵向留白收窄到精确上界（统一留白已够时保持统一）。
    return { padX: FIT_PADDING, padY: Math.min(FIT_PADDING, padMax), mode: 'utilization' };
  }
  // #62：目标不可达——显式分支，不再让 Math.max 吞负数。按最小纵向留白
  // 尽力：zoom 由纵向约束主导（内容完整铺满画布高），横向利用率按内容
  // 宽高比自然下降，右侧留白是结构形状的诚实呈现而非 fit 失误。
  return { padX: FIT_PADDING, padY: FIT_PAD_V_MIN, mode: 'vertical-bound' };
}

/**
 * 埋点：把 fit 走的分支写到 cy 容器的 data-fit-mode 属性。浏览器级验证
 * （CDP querySelector('.lab-canvas').dataset.fitMode）与人工排查直接可读；
 * 容器缺失（理论上的离屏/测试桩）为静默 no-op。
 */
export function recordFitMode(mode: FitMode, container: HTMLElement | null): void {
  container?.setAttribute('data-fit-mode', mode);
}

/**
 * fit 之后的实际横向/纵向利用率（验证面：测试与 CDP 埋点共用同一公式，
 * 验收主指标按 mode 选择——vertical-bound 时看纵向）。
 */
export function fitUtilization(bb: FitBox, vw: number, vh: number, zoom: number): { horizontal: number; vertical: number } {
  return {
    horizontal: (zoom * bb.w) / vw,
    vertical: (zoom * bb.h) / vh,
  };
}
