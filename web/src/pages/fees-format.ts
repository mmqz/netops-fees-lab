/**
 * fees-format.ts — /fees 工作台的纯逻辑（#68 从 Fees.tsx 抽出；
 * #109-A 起除展示逻辑外还托管关联筛选的纯状态迁移）。
 *
 * 这些函数是风险线索表「金额 vs 比率」双分支展示的唯一裁决点：
 * 一条 metricValue=123456 的 totalCents 线索读作 ¥1,234.56，而同数值的
 * ratio 线索读作 123456（比率原值）——判断分支反了就是财务展示事故。
 * 后端契约金额恒为整数分（#65 P1-a），展示层 /100 在此唯一发生。
 */

/** 整数分 → 元字符串（zh-CN 千分位 + 恒两位小数；契约：金额全程 int64 分） */
export function centsToYuan(cents: number): string {
  return (cents / 100).toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

/** 后端契约里以「分」为单位的指标标签闭集（其余一律按比率/原值展示） */
export function isCentsMetric(metricLabel: string): boolean {
  return metricLabel === 'totalCents' || metricLabel === 'deltaCents';
}

/** 比率/原值展示：toFixed(4) 后清尾零（0.5000→0.5、10.0000→10、0→0） */
export function trimMetricTail(v: number): string {
  return v.toFixed(4).replace(/\.?0+$/, '');
}

/** 线索「触发指标」单元格文案：金额分支带 ¥ 前缀 + /100，其余分支原值 + 尾零清理 */
export function formatRiskMetric(metricValue: number, metricLabel: string): string {
  if (isCentsMetric(metricLabel)) {
    return `¥${centsToYuan(metricValue)}`;
  }
  return trimMetricTail(metricValue);
}

/** 线索行键（同 (站址, 年, 月) 唯一——行展开锚点） */
export function clueKey(c: { siteCode: string; year: number; month: number }): string {
  return `${c.siteCode}|${c.year}|${c.month}`;
}

/** 关联状态 → 中文（三值闭集：pending/confirmed/dismissed） */
export function linkStatusLabel(status: string): string {
  switch (status) {
    case 'pending': return '待确认';
    case 'confirmed': return '已确认';
    default: return '已驳回';
  }
}

/** 关联级别 → 中文（三值闭集：exact/rule/fuzzy） */
export function linkGradeLabel(grade: string): string {
  switch (grade) {
    case 'exact': return '精确';
    case 'rule': return '规则';
    default: return '模糊';
  }
}

/** 关联筛选的完整状态（#109-A）：筛选值与页码必须同拍变更。 */
export interface LinkFilterSelection {
  status: string;
  grade: string;
  page: number;
}

/**
 * 关联筛选（状态/级别）select onChange 的唯一裁决点（#109-A）：
 * 筛选重塑结果集形状（如 pending 12 条 vs 全量 400 条），保留旧页码会在
 * 小结果集上越过总页数——第 5 页 × total=12 → 后端返回空 items，前端渲染
 * 「暂无关联」假空态（分页器仍指第 5 页）。因此筛选变化一律回第 1 页，
 * 与 doScan 扫描后 setLinkPage(1)（#90）同款策略。
 */
export function selectLinkFilter(
  current: LinkFilterSelection,
  patch: Partial<Pick<LinkFilterSelection, 'status' | 'grade'>>,
): LinkFilterSelection {
  return { ...current, ...patch, page: 1 };
}

/**
 * 风险线索空态的四态裁决（#21 三态 + #27 第四态）：「无法分析」与「分析后
 * 无发现」必须可区分。
 *
 * - 'no-data'        该账期无导入数据——引导去导入，不是分析结论；
 * - 'excluded-zero'  0 条线索但有排除数——可能「数据被排除后无法分析」，
 *                    告警色渲染，绝不与「本月正常」混同；
 * - 'not-analyzed'   0 条线索且有告警但无排除数（#27 的「未分析」形态：
 *                    空资源库/无关联/空指标面/空费用面的前置检查拦下了
 *                    场景）——告警色渲染，绝不说「无异常」或「好信号」；
 * - 'clean'          有数据、无排除、确实 0 条——中性的「未发现」。
 */
export type RiskEmptyKind = 'no-data' | 'excluded-zero' | 'not-analyzed' | 'clean';

export function riskEmptyState(
  clueCount: number,
  excluded: number,
  monthHasData: boolean,
  hasWarnings = false,
): RiskEmptyKind {
  if (!monthHasData) return 'no-data';
  if (clueCount === 0 && excluded > 0) return 'excluded-zero';
  if (clueCount === 0 && hasWarnings) return 'not-analyzed';
  return 'clean';
}
