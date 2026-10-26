/**
 * 费用分析页 /fees（#58 阶段 5，前端工作台入口）：
 * - 导入费用文件（multipart：file + feeType + year + month → POST /api/fees/imports，
 *   201 新导入 / 200 幂等重复双形态提示）；
 * - 费用批次列表（最新在前）+ 回滚（ConfirmDialog：删分析行、保快照证据）；
 * - 费用-资源关联（#63 分级关联的前端人类通道）：重新扫描 + 状态/级别筛选 +
 *   模糊提案的确认/驳回（驳回走 ConfirmDialog——审计保留不可撤）；
 * - 风险十场景查询（GET /api/fees/risks/{scenario}）：线索表（站址/账期/触发指标/
 *   显式阈值）+ 行展开证据锚点（sourceFile+sourceRow+importBatch+整数分）+
 *   excluded/dismissed 计数说明 + 人工裁定按钮与决策 chip（#119）；
 *   场景 9 ledger-consistency（#5/#8/#11 台账确定性规则）与场景 10
 *   custom-rules（#2 已启用规则逐条执行）已入枚举；
 *   501 engine_not_built 渲染为引导横幅而非裸错误。
 * - 导入前账期探测（POST /api/fees/period-probe，#15 的导入前确认面板）：
 *   只读探测所选文件的表型与账期，识别与申报不一致时给出覆盖勾选
 *   （periodTrustDeclared：unverified 留痕导入）。
 * - 可配置风险规则（GET/POST /api/fees/rules，#2 的 UI 消费面）：六元组
 *   规则列表 + 创建（默认停用观察态）+ 启用/停用（管理级人类通道，
 *   ConfirmDialog）+ 动作台账 Drawer（阈值变更留痕的可见面）。
 * - 查询预设（GET /api/fees/query/totals|duplicates，#121 浏览器通道补全）：
 *   站址账期合计与跨批次重复候选两表，整数分自动 /100 展示。
 *
 * 教义：线索是待核实差异而非认定结论（账单存在 ≠ 重复付款认定）——页面文案
 * 与 note 字段同源，均由后端契约携带。
 */

import { Fragment, useCallback, useEffect, useRef, useState } from 'react';
import type { FeeDuplicatesResult, FeeLinkItem, FeeLinkScanResult, FeePeriodProbeResult, FeeRiskRule, FeeRuleAuditEntry, FeeTotalsResult, RiskResult } from '../api/types';
import { ApiError } from '../api/client';
import {
  confirmFeeLink,
  createFeeRule,
  dismissFeeLink,
  fetchFeeBatches,
  fetchFeeLinks,
  fetchFeeRuleAudit,
  fetchFeeRules,
  decideFeeRiskClue,
  fetchFeeDuplicates,
  fetchFeeRisks,
  fetchFeeTotals,
  formatApiErrorText,
  probeFeePeriod,
  rollbackFeeBatch,
  scanFeeLinks,
  setFeeRuleEnabled,
  uploadFeeFile,
  uploadFeeSheetsFile,
} from '../api/client';
import { FeesCrossReconcile } from './FeesCrossReconcile';
import { FeesDetails } from './FeesDetails';
import { FeesOverview } from './FeesOverview';
import { useToast } from '../components/Toast';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { Drawer } from '../components/Drawer';
import { ErrorBlock } from '../components/ErrorBlock';
import { Spinner } from '../components/Spinner';
import { Pagination } from '../components/Pagination';
import {
  clueKey,
  centsToYuan,
  formatRiskMetric,
  linkGradeLabel,
  linkStatusLabel,
  selectLinkFilter,
} from './fees-format';
import type { LinkFilterSelection } from './fees-format';
import {
  FEES_501_HINT,
  FEES_BATCHES_COL_FILE,
  FEES_BATCHES_COL_PERIOD,
  FEES_BATCHES_COL_ROWS,
  FEES_BATCHES_COL_STATUS,
  FEES_BATCHES_COL_TYPE,
  FEES_BATCHES_EMPTY,
  FEES_BATCHES_FAILED_ROWS,
  FEES_BATCHES_ROLLED_BACK,
  FEES_BATCHES_ROLLBACK,
  FEES_BATCHES_ROLLBACK_BUSY,
  FEES_BATCHES_ROLLBACK_CONFIRM,
  FEES_BATCHES_TITLE,
  FEES_DESC,
  FEES_IMPORT_DUP,
  FEES_IMPORT_FILE,
  FEES_IMPORT_MODE,
  FEES_IMPORT_MODE_SHEETS,
  FEES_IMPORT_MODE_SINGLE,
  FEES_IMPORT_MONTH,
  FEES_IMPORT_OK,
  FEES_IMPORT_PERIOD_INVALID,
  FEES_IMPORT_SHEETS_DUP,
  FEES_IMPORT_SHEETS_OK,
  FEES_IMPORT_SUBMIT,
  FEES_IMPORT_TITLE,
  FEES_IMPORT_TYPE,
  FEES_IMPORT_YEAR,
  FEES_LINKS_ACTIONS,
  FEES_LINKS_ALL,
  FEES_LINKS_CODE,
  FEES_LINKS_CONFIRM,
  FEES_LINKS_CONFIRMED,
  FEES_LINKS_DESC,
  FEES_LINKS_DISMISS,
  FEES_LINKS_DISMISS_CONFIRM,
  FEES_LINKS_DISMISSED,
  FEES_LINKS_DISMISSING,
  FEES_LINKS_EMPTY,
  FEES_LINKS_GRADE,
  FEES_LINKS_RESOURCE,
  FEES_LINKS_SCAN,
  FEES_LINKS_SCAN_RESULT,
  FEES_LINKS_SIMILARITY,
  FEES_LINKS_SOURCE,
  FEES_LINKS_STATUS,
  FEES_LINKS_TITLE,
  FEES_RISKS_CLUE_COUNT,
  FEES_RISKS_COL_EVIDENCE,
  FEES_RISKS_COL_METRIC,
  FEES_RISKS_COL_PERIOD,
  FEES_RISKS_COL_SITE,
  FEES_RISKS_COL_THRESHOLD,
  FEES_RISKS_EVIDENCE_BATCH,
  FEES_RISKS_EVIDENCE_ROWS,
  FEES_RISKS_EMPTY,
  FEES_RISKS_EVIDENCE,
  FEES_RISKS_EVIDENCE_MORE,
  FEES_RISKS_EXCLUDED,
  FEES_RISKS_MONTH_ALL,
  FEES_RISKS_PERIOD_ALL_YEAR,
  FEES_RISKS_STATUS,
  FEES_RISKS_STATUS_HINT,
  FEES_RISKS_YEAR_INVALID,
  FEES_RISKS_DIFF_CENTS,
  FEES_RISKS_RATIO,
  FEES_RISKS_Z,
  FEES_RISKS_MIN_SITES,
  FEES_RISKS_LIMIT,
  FEES_RISKS_RUN,
  FEES_RISKS_SCENARIO,
  FEES_RISKS_TITLE,
  FEES_TITLE,
  FEE_TYPE_ELECTRICITY,
  FEE_TYPE_MAINTENANCE,
  FEE_TYPE_REPAIR,
  FEE_TYPE_TOWER_LEASE,
  LINKS_GRADE_EXACT,
  LINKS_GRADE_FUZZY,
  LINKS_GRADE_RULE,
  LINKS_SOURCE_AUTO,
  LINKS_SOURCE_HUMAN,
  LINKS_STATUS_CONFIRMED,
  LINKS_STATUS_DISMISSED,
  LINKS_STATUS_PENDING,
  RISK_SCENARIO_DUPLICATE_SETTLEMENT,
  RISK_SCENARIO_LEDGER_MISMATCH,
  RISK_SCENARIO_OUTLIER,
  RISK_SCENARIO_RETIRED_BILLING,
  RISK_SCENARIO_SPIKE,
  RISK_SCENARIO_UNLINKED,
  RISK_SCENARIO_IDLE_BILLING,
  RISK_SCENARIO_MISSING_BILLING,
  RISK_SCENARIO_LEDGER_CONSISTENCY,
  RISK_SCENARIO_CUSTOM_RULES,
  FEES_IMPORT_FILE_REQUIRED,
  FEES_PROBE_TITLE,
  FEES_PROBE_PROBING,
  FEES_PROBE_SUBMIT,
  FEES_PROBE_KIND,
  FEES_PROBE_PERIOD,
  FEES_PROBE_CONFIDENCE,
  FEES_PROBE_BASIS,
  FEES_PROBE_CONF_STRONG,
  FEES_PROBE_CONF_WEAK,
  FEES_PROBE_CONF_NONE,
  FEES_PROBE_MATCH,
  FEES_PROBE_MISMATCH,
  FEES_PROBE_UNKNOWN,
  FEES_PROBE_TRUST,
  FEES_RULES_TITLE,
  FEES_RULES_DESC,
  FEES_RULES_COL_NAME,
  FEES_RULES_COL_TYPE,
  FEES_RULES_COL_TUPLE,
  FEES_RULES_COL_SEVERITY,
  FEES_RULES_COL_STATE,
  FEES_RULES_COL_ACTIONS,
  FEES_RULES_STATE_ENABLED,
  FEES_RULES_STATE_DISABLED,
  FEES_RULES_ENABLE,
  FEES_RULES_DISABLE,
  FEES_RULES_AUDIT,
  FEES_RULES_AUDIT_TITLE,
  FEES_RULES_AUDIT_EMPTY,
  FEES_RULES_AUDIT_CLOSE,
  FEES_RULES_AUDIT_AT,
  FEES_RULES_AUDIT_ACTOR,
  FEES_RULES_AUDIT_ACTION,
  FEES_RULES_AUDIT_CHANGES,
  FEES_RULES_CREATE_TITLE,
  FEES_RULES_CREATE_NAME,
  FEES_RULES_CREATE_NAME_PH,
  FEES_RULES_CREATE_METRIC,
  FEES_RULES_CREATE_AGG,
  FEES_RULES_CREATE_COMPARATOR,
  FEES_RULES_CREATE_THRESHOLD,
  FEES_RULES_CREATE_THRESHOLD_HINT,
  FEES_RULES_CREATE_WINDOW,
  FEES_RULES_CREATE_SEVERITY,
  FEES_RULES_CREATE_DESC,
  FEES_RULES_CREATE_DESC_PH,
  FEES_RULES_CREATE_SUBMIT,
  FEES_RULES_CREATED,
  FEES_RULES_ENABLED_OK,
  FEES_RULES_DISABLED_OK,
  FEES_RULES_CONFIRM_ENABLE,
  FEES_RULES_CONFIRM_DISABLE,
  FEES_RULES_EMPTY,
  FEES_RULES_ENABLE_403,
  FEES_RULES_TOGGLING,
  FEES_RISKS_DISMISSED_COUNT,
  FEES_RISKS_SHOW_DISMISSED,
  FEES_RISKS_DECISION_VERIFIED,
  FEES_RISKS_DECISION_DISMISSED,
  FEES_RISKS_DECIDE_VERIFY,
  FEES_RISKS_DECIDE_DISMISS,
  FEES_RISKS_DECIDE_HINT,
  FEES_QUERY_TITLE,
  FEES_QUERY_DESC,
  FEES_QUERY_PRESET,
  FEES_QUERY_PRESET_TOTALS,
  FEES_QUERY_PRESET_DUPLICATES,
  FEES_QUERY_RUN,
  FEES_QUERY_COL_SITE,
  FEES_QUERY_COL_ROWS,
  FEES_QUERY_COL_TOTAL,
  FEES_QUERY_COL_PERIOD,
  FEES_QUERY_COL_BATCHES,
  FEES_QUERY_EMPTY,
  FEES_QUERY_COUNT,
} from '../i18n';

/** 线索行键与金额展示逻辑见 fees-format.ts（#68 抽出纯函数，表测钉死） */

const FEE_TYPES = [
  { value: 'electricity', label: FEE_TYPE_ELECTRICITY },
  { value: 'tower_lease', label: FEE_TYPE_TOWER_LEASE },
  { value: 'repair', label: FEE_TYPE_REPAIR },
  { value: 'maintenance', label: FEE_TYPE_MAINTENANCE },
];

const SCENARIOS = [
  { value: 'retired-billing', label: RISK_SCENARIO_RETIRED_BILLING },
  { value: 'duplicate-settlement', label: RISK_SCENARIO_DUPLICATE_SETTLEMENT },
  { value: 'ledger-mismatch', label: RISK_SCENARIO_LEDGER_MISMATCH },
  { value: 'spike', label: RISK_SCENARIO_SPIKE },
  { value: 'outlier', label: RISK_SCENARIO_OUTLIER },
  { value: 'unlinked', label: RISK_SCENARIO_UNLINKED },
  { value: 'idle-billing', label: RISK_SCENARIO_IDLE_BILLING },
  { value: 'missing-billing', label: RISK_SCENARIO_MISSING_BILLING },
  { value: 'ledger-consistency', label: RISK_SCENARIO_LEDGER_CONSISTENCY },
  { value: 'custom-rules', label: RISK_SCENARIO_CUSTOM_RULES },
];

interface FeeBatchRow {
  id: string;
  workbookName: string;
  feeTypeLabel: string;
  period: string;
  status: string;
  rowsTotal: number;
  rowsImported: number;
  rowsFailed: number;
}

export function FeesPage() {
  const toast = useToast();

  // ---- 批次 ----
  const [batches, setBatches] = useState<FeeBatchRow[] | null>(null);
  const [batchesError, setBatchesError] = useState<string>('');
  const [rollbackTarget, setRollbackTarget] = useState<FeeBatchRow | null>(null);
  const [rollingBack, setRollingBack] = useState(false);

  const loadBatches = useCallback(async () => {
    setBatchesError('');
    try {
      const res = await fetchFeeBatches();
      setBatches(res.items);
    } catch (err) {
      setBatchesError(formatApiErrorText(err));
      setBatches(null);
    }
  }, []);

  useEffect(() => {
    void loadBatches();
  }, [loadBatches]);

  const doRollback = useCallback(async () => {
    if (rollbackTarget === null) return;
    setRollingBack(true);
    try {
      await rollbackFeeBatch(rollbackTarget.id);
      toast.show(FEES_BATCHES_ROLLED_BACK(rollbackTarget.workbookName), 'success');
      setRollbackTarget(null);
      await loadBatches();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setRollingBack(false);
    }
  }, [rollbackTarget, loadBatches, toast]);

  // ---- 导入 ----
  const fileInput = useRef<HTMLInputElement | null>(null);
  const [importMode, setImportMode] = useState<'single' | 'sheets'>('single');
  const [importType, setImportType] = useState('electricity');
  const [importYear, setImportYear] = useState(String(new Date().getFullYear()));
  const [importMonth, setImportMonth] = useState(String(new Date().getMonth() + 1));
  const [importing, setImporting] = useState(false);
  // ---- 导入前账期探测（#15：只读探针 + 不一致覆盖勾选）----
  const [probing, setProbing] = useState(false);
  const [probeResult, setProbeResult] = useState<FeePeriodProbeResult | null>(null);
  const [trustDeclared, setTrustDeclared] = useState(false);

  const doProbe = useCallback(async () => {
    const file = fileInput.current?.files?.[0];
    if (file === undefined) {
      toast.show(FEES_IMPORT_FILE_REQUIRED, 'error');
      return;
    }
    setProbing(true);
    setTrustDeclared(false);
    try {
      const res = await probeFeePeriod(file);
      setProbeResult(res);
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
      setProbeResult(null);
    } finally {
      setProbing(false);
    }
  }, [toast]);

  /** 探测结论与申报账期是否一致（kind 未知或无账期证据时不比对——
   * 那是人工指定治理的形态，不存在「不一致」可言）。 */
  const probeMismatch = probeResult !== null
    && probeResult.year > 0
    && (probeResult.year !== Number(importYear) || probeResult.month !== Number(importMonth));

  const doImport = useCallback(async () => {
    const file = fileInput.current?.files?.[0];
    if (file === undefined) return;
    const year = Number(importYear);
    const month = Number(importMonth);
    if (!Number.isInteger(year) || year < 2000 || year > 2100 || !Number.isInteger(month) || month < 1 || month > 12) {
      toast.show(FEES_IMPORT_PERIOD_INVALID, 'error');
      return;
    }
    setImporting(true);
    try {
      if (importMode === 'sheets') {
        // #125 多 sheet 台账：无 feeType，每 sheet 由表名/表头自动判型。
        const sum = await uploadFeeSheetsFile(file, year, month, trustDeclared);
        const allDup = sum.batches.every((b) => b.skippedDuplicate);
        if (allDup) {
          toast.show(FEES_IMPORT_SHEETS_DUP, 'info');
        } else {
          const detail = sum.batches.map((b) => `${b.sheet} ${b.rowsImported} 行`).join(' / ');
          toast.show(FEES_IMPORT_SHEETS_OK(sum.batches.length, detail), 'success');
          if ((sum.skippedSheets?.length ?? 0) > 0) {
            toast.show(`装饰性 sheet 已按名略过：${sum.skippedSheets?.join('、')}`, 'info');
          }
        }
      } else {
        const sum = await uploadFeeFile(file, importType, year, month, trustDeclared);
        if (sum.skippedDuplicate) {
          toast.show(FEES_IMPORT_DUP(sum.existingBatch ?? sum.batchId), 'info');
        } else {
          toast.show(FEES_IMPORT_OK(sum.batchId, sum.rowsImported), 'success');
          if (sum.rowsFailed > 0 && sum.errors !== undefined && sum.errors.length > 0) {
            toast.show(sum.errors[0], 'info');
          }
        }
      }
      if (fileInput.current !== null) fileInput.current.value = '';
      await loadBatches();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setImporting(false);
    }
  }, [importMode, importType, importYear, importMonth, trustDeclared, loadBatches, toast]);

  // ---- 关联（费用-资源分级关联的人类确认通道）----
  const [links, setLinks] = useState<FeeLinkItem[] | null>(null);
  const [linksTotal, setLinksTotal] = useState(0);
  const [linkPage, setLinkPage] = useState(1);
  const [linksError, setLinksError] = useState('');
  const [linkStatus, setLinkStatus] = useState('pending');
  const [linkGrade, setLinkGrade] = useState('');
  const [scanning, setScanning] = useState(false);
  const [scanResult, setScanResult] = useState<FeeLinkScanResult | null>(null);
  const [dismissTarget, setDismissTarget] = useState<FeeLinkItem | null>(null);
  const [dismissing, setDismissing] = useState(false);

  const loadLinks = useCallback(async () => {
    setLinksError('');
    try {
      const res = await fetchFeeLinks({
        status: linkStatus === '' ? undefined : linkStatus,
        grade: linkGrade === '' ? undefined : linkGrade,
        page: linkPage,
        pageSize: 20,
      });
      setLinks(res.items);
      setLinksTotal(res.total);
    } catch (err) {
      setLinksError(formatApiErrorText(err));
      setLinks(null);
      setLinksTotal(0);
    }
  }, [linkStatus, linkGrade, linkPage]);

  useEffect(() => {
    void loadLinks();
  }, [loadLinks]);

  const doScan = useCallback(async () => {
    setScanning(true);
    try {
      const res = await scanFeeLinks();
      setScanResult(res);
      setLinkPage(1); // #90: a fresh scan re-shapes the list — restart from page 1
      await loadLinks();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setScanning(false);
    }
  }, [loadLinks, toast]);

  const doConfirm = useCallback(async (l: FeeLinkItem) => {
    try {
      await confirmFeeLink(l.id);
      toast.show(FEES_LINKS_CONFIRMED(l.feeSiteCode), 'success');
      await loadLinks();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    }
  }, [loadLinks, toast]);

  const doDismiss = useCallback(async () => {
    if (dismissTarget === null) return;
    setDismissing(true);
    try {
      await dismissFeeLink(dismissTarget.id);
      toast.show(FEES_LINKS_DISMISSED(dismissTarget.feeSiteCode), 'success');
      setDismissTarget(null);
      await loadLinks();
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setDismissing(false);
    }
  }, [dismissTarget, loadLinks, toast]);

  // ---- 可配置风险规则（#2：列表/创建/启用/停用/台账）----
  const [rules, setRules] = useState<FeeRiskRule[] | null>(null);
  const [rulesError, setRulesError] = useState<string>('');
  const [rulesEnabledOnly, setRulesEnabledOnly] = useState(false);
  const [ruleCreating, setRuleCreating] = useState(false);
  const [ruleToggleTarget, setRuleToggleTarget] = useState<{ rule: FeeRiskRule; enable: boolean } | null>(null);
  const [ruleToggling, setRuleToggling] = useState(false);
  const [auditRule, setAuditRule] = useState<FeeRiskRule | null>(null);
  const [auditEntries, setAuditEntries] = useState<FeeRuleAuditEntry[] | null>(null);
  const [auditLoading, setAuditLoading] = useState(false);
  // 创建表单（六元组；观察态默认由服务端强制）
  const [ruleName, setRuleName] = useState('');
  const [ruleType, setRuleType] = useState('electricity');
  const [ruleMetric, setRuleMetric] = useState('amount');
  const [ruleAgg, setRuleAgg] = useState('sum');
  const [ruleComparator, setRuleComparator] = useState('gt');
  const [ruleThreshold, setRuleThreshold] = useState('');
  const [ruleWindow, setRuleWindow] = useState('1');
  const [ruleSeverity, setRuleSeverity] = useState('high');
  const [ruleDescription, setRuleDescription] = useState('');

  const loadRules = useCallback(async () => {
    setRulesError('');
    try {
      const res = await fetchFeeRules(rulesEnabledOnly);
      setRules(res.items);
    } catch (err) {
      setRulesError(formatApiErrorText(err));
      setRules(null);
    }
  }, [rulesEnabledOnly]);

  useEffect(() => {
    void loadRules();
  }, [loadRules]);

  const doCreateRule = useCallback(async () => {
    const threshold = Number(ruleThreshold);
    if (ruleName.trim() === '' || !Number.isFinite(threshold) || threshold < 0) {
      toast.show('规则名必填，阈值必须是 ≥ 0 的数（金额类=分、环比类=涨幅比）', 'error');
      return;
    }
    setRuleCreating(true);
    try {
      const rule = await createFeeRule({
        name: ruleName.trim(),
        feeType: ruleType,
        metric: ruleMetric,
        aggregation: ruleAgg,
        dimension: 'site_month',
        comparator: ruleComparator,
        threshold,
        windowMonths: Number(ruleWindow) > 0 ? Number(ruleWindow) : 1,
        severity: ruleSeverity,
        description: ruleDescription.trim(),
      });
      toast.show(FEES_RULES_CREATED(rule.name), 'success');
      setRuleName('');
      setRuleThreshold('');
      setRuleDescription('');
      await loadRules();
    } catch (err) {
      // 422 rule_refused 携带服务端的枚举/重名文案——原样呈现
      toast.show(formatApiErrorText(err), 'error');
    } finally {
      setRuleCreating(false);
    }
  }, [ruleName, ruleType, ruleMetric, ruleAgg, ruleComparator, ruleThreshold, ruleWindow, ruleSeverity, ruleDescription, loadRules, toast]);

  const doToggleRule = useCallback(async () => {
    if (ruleToggleTarget === null) return;
    setRuleToggling(true);
    try {
      const updated = await setFeeRuleEnabled(ruleToggleTarget.rule.id, ruleToggleTarget.enable);
      toast.show(
        ruleToggleTarget.enable ? FEES_RULES_ENABLED_OK(updated.name) : FEES_RULES_DISABLED_OK(updated.name),
        'success',
      );
      setRuleToggleTarget(null);
      await loadRules();
    } catch (err) {
      // 403 = 申报级令牌够不到管理级人类通道——给出明确指引而非裸报错
      toast.show(formatApiErrorText(err), 'error');
      if (err instanceof ApiError && err.status === 403) {
        toast.show(FEES_RULES_ENABLE_403, 'info');
      }
    } finally {
      setRuleToggling(false);
    }
  }, [ruleToggleTarget, loadRules, toast]);

  const doOpenAudit = useCallback(async (rule: FeeRiskRule) => {
    setAuditRule(rule);
    setAuditEntries(null);
    setAuditLoading(true);
    try {
      const res = await fetchFeeRuleAudit(rule.id);
      setAuditEntries(res.items);
    } catch (err) {
      toast.show(formatApiErrorText(err), 'error');
      setAuditEntries([]);
    } finally {
      setAuditLoading(false);
    }
  }, [toast]);

  /** 六元组的单格摘要：sum(amount) > 500000 / mom_ratio ≥ 0.3（基期 3 月） */
  function ruleTupleSummary(r: FeeRiskRule): string {
    const metric = r.metric === 'rows' ? 'rows' : 'amount';
    const threshold = Number.isInteger(r.threshold) ? String(r.threshold) : String(r.threshold);
    const head = r.metric === 'rows'
      ? 'count(rows)'
      : `${r.aggregation}(${metric})`;
    if (r.comparator === 'mom_ratio') {
      const window = r.windowMonths > 1 ? `（基期回看 ${r.windowMonths} 个月）` : '';
      return `${head} 环比涨幅 ≥ ${threshold}${window}`;
    }
    const comp = r.comparator === 'gt' ? '>' : r.comparator === 'gte' ? '≥' : r.comparator === 'lt' ? '<' : '≤';
    return `${head} ${comp} ${threshold}`;
  }

  // 标签映射函数 linkStatusLabel / linkGradeLabel 见 fees-format.ts（#68）

  /** 状态/级别筛选 onChange 的唯一入口（#109-A）：筛选值与页码同拍迁移
   *  （裁决点在 fees-format.selectLinkFilter）——否则第 5 页切到小数据筛选
   *  会渲染「暂无关联」假空态。与 doScan 的 setLinkPage(1)（#90）同款。 */
  function applyLinkFilter(patch: Partial<Pick<LinkFilterSelection, 'status' | 'grade'>>): void {
    const next = selectLinkFilter({ status: linkStatus, grade: linkGrade, page: linkPage }, patch);
    setLinkStatus(next.status);
    setLinkGrade(next.grade);
    setLinkPage(next.page);
  }

  // ---- 查询预设（#121：浏览器通道补全——汇总/重复候选两表面板） ----
  const [qPreset, setQPreset] = useState<'totals' | 'duplicates'>('totals');
  const [qType, setQType] = useState('electricity');
  const [qYear, setQYear] = useState(String(new Date().getFullYear()));
  const [qMonth, setQMonth] = useState('');
  const [qTotals, setQTotals] = useState<FeeTotalsResult | null>(null);
  const [qDuplicates, setQDuplicates] = useState<FeeDuplicatesResult | null>(null);
  const [qRunning, setQRunning] = useState(false);
  const [qError, setQError] = useState('');
  const [qEngineMissing, setQEngineMissing] = useState(false);

  const runQueryPreset = useCallback(async () => {
    const year = Number(qYear);
    if (!Number.isInteger(year) || year < 2000 || year > 2100) {
      setQError(FEES_RISKS_YEAR_INVALID);
      return;
    }
    setQRunning(true);
    setQError('');
    setQEngineMissing(false);
    setQTotals(null);
    setQDuplicates(null);
    try {
      const scope = {
        feeType: qType,
        year,
        month: qMonth === '' ? undefined : Number(qMonth),
        limit: 500, // the HTTP honest bound — the browser shows what exists
      };
      if (qPreset === 'totals') {
        setQTotals(await fetchFeeTotals(scope));
      } else {
        setQDuplicates(await fetchFeeDuplicates(scope));
      }
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'engine_not_built') {
        setQEngineMissing(true);
      } else {
        setQError(formatApiErrorText(err));
      }
    } finally {
      setQRunning(false);
    }
  }, [qPreset, qType, qYear, qMonth]);

  // ---- 风险查询 ----
  const [scenario, setScenario] = useState('duplicate-settlement');
  const [riskType, setRiskType] = useState('electricity');
  const [riskYear, setRiskYear] = useState(String(new Date().getFullYear()));
  const [riskMonth, setRiskMonth] = useState('');
  const [riskLimit, setRiskLimit] = useState('50');
  // #89: the per-scenario thresholds — visible only for their scenario, so
  // the human channel can tune exactly what the scenario tunes.
  const [riskDiffCents, setRiskDiffCents] = useState('');
  const [riskRatio, setRiskRatio] = useState('');
  const [riskZ, setRiskZ] = useState('');
  const [riskMinSites, setRiskMinSites] = useState('');
  const [riskStatus, setRiskStatus] = useState('');
  const [running, setRunning] = useState(false);
  const [riskResult, setRiskResult] = useState<RiskResult | null>(null);
  const [riskError, setRiskError] = useState('');
  const [engineMissing, setEngineMissing] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  // #119: the human verification loop — the audit toggle and the busy
  // guard of the per-clue decide buttons.
  const [includeDecided, setIncludeDecided] = useState(false);
  const [decidingKey, setDecidingKey] = useState<string | null>(null);

  const runRisks = useCallback(async () => {
    const year = Number(riskYear);
    if (!Number.isInteger(year) || year < 2000 || year > 2100) {
      setRiskError(FEES_RISKS_YEAR_INVALID);
      return;
    }
    setRunning(true);
    setRiskError('');
    setEngineMissing(false);
    setExpanded(null);
    try {
      const res = await fetchFeeRisks(scenario, {
        feeType: riskType,
        year,
        month: riskMonth === '' ? undefined : Number(riskMonth),
        limit: riskLimit === '' ? undefined : Number(riskLimit),
        // #89: per-scenario thresholds — empty input = the governed default.
        status: scenario === 'retired-billing' && riskStatus.trim() !== '' ? riskStatus.trim() : undefined,
        diffCents:
          scenario === 'ledger-mismatch' && riskDiffCents.trim() !== ''
            ? Number(riskDiffCents)
            : undefined,
        ratio: scenario === 'spike' && riskRatio.trim() !== '' ? Number(riskRatio) : undefined,
        z: scenario === 'outlier' && riskZ.trim() !== '' ? Number(riskZ) : undefined,
        minSites:
          scenario === 'outlier' && riskMinSites.trim() !== '' ? Number(riskMinSites) : undefined,
        includeDecided,
      });
      setRiskResult(res);
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      if (code === 'engine_not_built') {
        setEngineMissing(true);
        setRiskResult(null);
      } else {
        setRiskError(formatApiErrorText(err));
      }
    } finally {
      setRunning(false);
    }
  }, [
    scenario,
    riskType,
    riskYear,
    riskMonth,
    riskLimit,
    riskStatus,
    riskDiffCents,
    riskRatio,
    riskZ,
    riskMinSites,
    includeDecided,
  ]);

  // #119: the human decide channel — verify (real, stays visible and
  // marked) or dismiss (ruled out; hidden by default, counted). The
  // decision lands via UPSERT, then the scenario re-runs so the operator
  // sees the suppression take effect immediately.
  const decideClue = useCallback(
    async (clue: { siteCode: string; year: number; month: number }, status: 'verified' | 'dismissed') => {
      const key = clueKey(clue);
      setDecidingKey(key);
      setRiskError('');
      try {
        await decideFeeRiskClue({
          scenario,
          feeType: riskType,
          siteCode: clue.siteCode,
          year: clue.year,
          month: clue.month,
          status,
        });
        await runRisks();
      } catch (err) {
        setRiskError(formatApiErrorText(err));
      } finally {
        setDecidingKey(null);
      }
    },
    [scenario, riskType, runRisks],
  );

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <h1 className="page__title">{FEES_TITLE}</h1>
          <p className="page__desc">{FEES_DESC}</p>
        </div>
      </header>

      {/* #3 二期：费用总览与分析路径（总览卡片 / 月度趋势 / 一键分析 / 跨场景线索列表） */}
      <FeesOverview />

      {/* #16 完成线 6：明细补录佐证层（差额对账 + 录入 + 台账裁定——永不参与系统表金额） */}
      <FeesDetails />

      {/* #12：跨数据集对账（电费台账 vs 塔类详单按主编码归并 + 站址编码映射桥） */}
      <FeesCrossReconcile />

      <section className="panel">
        <h2 className="panel__title">{FEES_RISKS_TITLE}</h2>
        <div className="filters">
          <label className="field">
            <span>{FEES_RISKS_SCENARIO}</span>
            <select value={scenario} onChange={(e) => setScenario(e.target.value)}>
              {SCENARIOS.map((s) => (
                <option key={s.value} value={s.value}>{s.label}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{FEES_IMPORT_TYPE}</span>
            <select value={riskType} onChange={(e) => setRiskType(e.target.value)}>
              {FEE_TYPES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </label>
          <label className="field field--xs">
            <span>{FEES_IMPORT_YEAR}</span>
            <input value={riskYear} onChange={(e) => setRiskYear(e.target.value)} inputMode="numeric" />
          </label>
          <label className="field field--xs">
            <span>{FEES_IMPORT_MONTH}</span>
            <input
              value={riskMonth}
              onChange={(e) => setRiskMonth(e.target.value)}
              inputMode="numeric"
              placeholder={FEES_RISKS_MONTH_ALL}
            />
          </label>
          <label className="field field--xs">
            <span>{FEES_RISKS_LIMIT}</span>
            <input value={riskLimit} onChange={(e) => setRiskLimit(e.target.value)} inputMode="numeric" />
          </label>
          {scenario === 'retired-billing' && (
            <label className="field">
              <span>{FEES_RISKS_STATUS}</span>
              <input
                value={riskStatus}
                onChange={(e) => setRiskStatus(e.target.value)}
                placeholder={FEES_RISKS_STATUS_HINT}
              />
            </label>
          )}
          {scenario === 'ledger-mismatch' && (
            <label className="field field--xs">
              <span>{FEES_RISKS_DIFF_CENTS}</span>
              <input
                value={riskDiffCents}
                onChange={(e) => setRiskDiffCents(e.target.value)}
                inputMode="numeric"
                placeholder="0"
              />
            </label>
          )}
          {scenario === 'spike' && (
            <label className="field field--xs">
              <span>{FEES_RISKS_RATIO}</span>
              <input
                value={riskRatio}
                onChange={(e) => setRiskRatio(e.target.value)}
                inputMode="decimal"
                placeholder="0.5"
              />
            </label>
          )}
          {scenario === 'outlier' && (
            <>
              <label className="field field--xs">
                <span>{FEES_RISKS_Z}</span>
                <input value={riskZ} onChange={(e) => setRiskZ(e.target.value)} inputMode="decimal" placeholder="3" />
              </label>
              <label className="field field--xs">
                <span>{FEES_RISKS_MIN_SITES}</span>
                <input
                  value={riskMinSites}
                  onChange={(e) => setRiskMinSites(e.target.value)}
                  inputMode="numeric"
                  placeholder="8"
                />
              </label>
            </>
          )}
          <label className="switch">
            <input
              type="checkbox"
              checked={includeDecided}
              onChange={(e) => setIncludeDecided(e.target.checked)}
            />
            {FEES_RISKS_SHOW_DISMISSED}
          </label>
          <button className="btn btn--primary" disabled={running} onClick={() => void runRisks()}>
            {running ? <Spinner /> : FEES_RISKS_RUN}
          </button>
        </div>

        {engineMissing && <div className="banner banner--info">{FEES_501_HINT}</div>}
        {riskError !== '' && <ErrorBlock message={riskError} />}
        {riskResult !== null && (
          <div className="panel__body">
            <p className="muted">
              {riskResult.scenarioCn} · {FEES_RISKS_CLUE_COUNT(riskResult.total)}
              {riskResult.excluded > 0 && <> · {FEES_RISKS_EXCLUDED(riskResult.excluded)}</>}
              {riskResult.dismissed > 0 && <> · {FEES_RISKS_DISMISSED_COUNT(riskResult.dismissed)}</>}
            </p>
            <p className="muted muted--small">{riskResult.note}</p>
            {riskResult.clues.length === 0 ? (
              <p className="empty">{FEES_RISKS_EMPTY}</p>
            ) : (
              <table className="table table--interactive">
                <thead>
                  <tr>
                    <th>{FEES_RISKS_COL_SITE}</th>
                    <th>{FEES_RISKS_COL_PERIOD}</th>
                    <th>{FEES_RISKS_COL_METRIC}</th>
                    <th>{FEES_RISKS_COL_THRESHOLD}</th>
                    <th>{FEES_RISKS_COL_EVIDENCE}</th>
                  </tr>
                </thead>
                <tbody>
                  {riskResult.clues.map((c) => {
                    const key = clueKey(c);
                    const open = expanded === key;
                    return (
                      <Fragment key={key}>
                        <tr
                          className="row--expandable"
                          onClick={() => {
                            setExpanded(open ? null : key);
                          }}
                        >
                          <td>
                            {c.siteCode}
                            {c.decision === 'verified' && (
                              <span className="chip chip--ok">{FEES_RISKS_DECISION_VERIFIED}</span>
                            )}
                            {c.decision === 'dismissed' && (
                              <span className="chip chip--muted">{FEES_RISKS_DECISION_DISMISSED}</span>
                            )}
                          </td>
                          <td>{c.month > 0 ? `${c.year}-${String(c.month).padStart(2, '0')}` : FEES_RISKS_PERIOD_ALL_YEAR(c.year)}</td>
                          <td>
                            {formatRiskMetric(c.metricValue, c.metricLabel)}（{c.metricLabel}）
                          </td>
                          <td className="muted">{c.thresholdLabel}</td>
                          <td>{FEES_RISKS_EVIDENCE_ROWS(c.evidenceTotal)}</td>
                        </tr>
                        {open && (
                          <tr className="row--detail">
                            <td colSpan={5}>
                              {c.note !== undefined && c.note !== '' && <p className="muted">{c.note}</p>}
                              <p className="muted muted--small">{FEES_RISKS_DECIDE_HINT}</p>
                              <p>
                                <button
                                  className="btn btn--small"
                                  disabled={decidingKey === key || c.decision === 'verified'}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    void decideClue(c, 'verified');
                                  }}
                                >
                                  {FEES_RISKS_DECIDE_VERIFY}
                                </button>{' '}
                                <button
                                  className="btn btn--small"
                                  disabled={decidingKey === key || c.decision === 'dismissed'}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    void decideClue(c, 'dismissed');
                                  }}
                                >
                                  {FEES_RISKS_DECIDE_DISMISS}
                                </button>
                              </p>
                              <p className="kv__title">{FEES_RISKS_EVIDENCE}</p>
                              <ul className="kv">
                                {(c.evidence ?? []).map((a, i) => (
                                  <li key={i} className="kv__item">
                                    <span className="kv__key">{a.sourceFile}:{a.sourceRow}</span>
                                    <span className="kv__value">
                                      ¥{centsToYuan(a.amountCents)} · {FEES_RISKS_EVIDENCE_BATCH(a.importBatch)}
                                    </span>
                                  </li>
                                ))}
                                {c.evidenceTotal > c.evidence.length && (
                                  <li className="kv__item muted">
                                    {FEES_RISKS_EVIDENCE_MORE(c.evidenceTotal - c.evidence.length)}
                                  </li>
                                )}
                              </ul>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
        )}
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_QUERY_TITLE}</h2>
        <p className="page__desc">{FEES_QUERY_DESC}</p>
        <div className="filters">
          <label className="field">
            <span>{FEES_QUERY_PRESET}</span>
            <select
              value={qPreset}
              onChange={(e) => {
                setQPreset(e.target.value as 'totals' | 'duplicates');
                setQTotals(null);
                setQDuplicates(null);
              }}
            >
              <option value="totals">{FEES_QUERY_PRESET_TOTALS}</option>
              <option value="duplicates">{FEES_QUERY_PRESET_DUPLICATES}</option>
            </select>
          </label>
          <label className="field">
            <span>{FEES_IMPORT_TYPE}</span>
            <select value={qType} onChange={(e) => setQType(e.target.value)}>
              <option value="electricity">{FEE_TYPE_ELECTRICITY}</option>
              <option value="tower_lease">{FEE_TYPE_TOWER_LEASE}</option>
              <option value="repair">{FEE_TYPE_REPAIR}</option>
              <option value="maintenance">{FEE_TYPE_MAINTENANCE}</option>
            </select>
          </label>
          <label className="field field--xs">
            <span>{FEES_IMPORT_YEAR}</span>
            <input value={qYear} onChange={(e) => setQYear(e.target.value)} inputMode="numeric" placeholder="2024" />
          </label>
          <label className="field field--xs">
            <span>{FEES_IMPORT_MONTH}</span>
            <input value={qMonth} onChange={(e) => setQMonth(e.target.value)} inputMode="numeric" placeholder={FEES_RISKS_MONTH_ALL} />
          </label>
          <button className="btn btn--primary" disabled={qRunning} onClick={() => void runQueryPreset()}>
            {qRunning ? <Spinner /> : FEES_QUERY_RUN}
          </button>
        </div>

        {qEngineMissing && <div className="banner banner--info">{FEES_501_HINT}</div>}
        {qError !== '' && <ErrorBlock message={qError} />}
        {qTotals !== null && (
          <div className="panel__body">
            <p className="muted">{FEES_QUERY_COUNT(qTotals.total)}</p>
            {qTotals.items.length === 0 ? (
              <p className="empty">{FEES_QUERY_EMPTY}</p>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>{FEES_QUERY_COL_SITE}</th>
                    <th>{FEES_QUERY_COL_ROWS}</th>
                    <th>{FEES_QUERY_COL_TOTAL}</th>
                  </tr>
                </thead>
                <tbody>
                  {qTotals.items.map((r) => (
                    <tr key={r.siteCode}>
                      <td>{r.siteCode}</td>
                      <td>{r.rows}</td>
                      <td>¥{centsToYuan(r.totalCents)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
        {qDuplicates !== null && (
          <div className="panel__body">
            <p className="muted">{FEES_QUERY_COUNT(qDuplicates.total)}</p>
            {qDuplicates.items.length === 0 ? (
              <p className="empty">{FEES_QUERY_EMPTY}</p>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>{FEES_QUERY_COL_SITE}</th>
                    <th>{FEES_QUERY_COL_PERIOD}</th>
                    <th>{FEES_QUERY_COL_BATCHES}</th>
                    <th>{FEES_QUERY_COL_ROWS}</th>
                    <th>{FEES_QUERY_COL_TOTAL}</th>
                  </tr>
                </thead>
                <tbody>
                  {qDuplicates.items.map((r) => (
                    <tr key={`${r.siteCode}|${r.year}|${r.month}`}>
                      <td>{r.siteCode}</td>
                      <td>{r.month > 0 ? `${r.year}-${String(r.month).padStart(2, '0')}` : FEES_RISKS_PERIOD_ALL_YEAR(r.year)}</td>
                      <td>{r.batches}</td>
                      <td>{r.rows}</td>
                      <td>¥{centsToYuan(r.totalCents)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_LINKS_TITLE}</h2>
        <p className="page__desc">{FEES_LINKS_DESC}</p>
        <div className="filters">
          <label className="field">
            <span>{FEES_LINKS_STATUS}</span>
            <select value={linkStatus} onChange={(e) => applyLinkFilter({ status: e.target.value })}>
              <option value="pending">{LINKS_STATUS_PENDING}</option>
              <option value="confirmed">{LINKS_STATUS_CONFIRMED}</option>
              <option value="dismissed">{LINKS_STATUS_DISMISSED}</option>
              <option value="">{FEES_LINKS_ALL}</option>
            </select>
          </label>
          <label className="field">
            <span>{FEES_LINKS_GRADE}</span>
            <select value={linkGrade} onChange={(e) => applyLinkFilter({ grade: e.target.value })}>
              <option value="exact">{LINKS_GRADE_EXACT}</option>
              <option value="rule">{LINKS_GRADE_RULE}</option>
              <option value="fuzzy">{LINKS_GRADE_FUZZY}</option>
              <option value="">{FEES_LINKS_ALL}</option>
            </select>
          </label>
          <button className="btn btn--primary" disabled={scanning} onClick={() => void doScan()}>
            {scanning ? <Spinner /> : FEES_LINKS_SCAN}
          </button>
        </div>

        {scanResult !== null && (
          <p className="muted">
            {FEES_LINKS_SCAN_RESULT(
              scanResult.codes, scanResult.exactLinks, scanResult.ruleLinks,
              scanResult.fuzzyProposed, scanResult.unlinked, scanResult.humanKept,
            )}
          </p>
        )}
        {linksError !== '' && <ErrorBlock message={linksError} />}
        <div className="panel__body">
          {links !== null && links.length === 0 && <p className="empty">{FEES_LINKS_EMPTY}</p>}
          {links !== null && links.length > 0 && (
            <Pagination page={linkPage} pageSize={20} total={linksTotal} onChange={setLinkPage} />
          )}
          {links !== null && links.length > 0 && (
            <table className="table table--interactive">
              <thead>
                <tr>
                  <th>{FEES_LINKS_CODE}</th>
                  <th>{FEES_LINKS_RESOURCE}</th>
                  <th>{FEES_LINKS_GRADE}</th>
                  <th>{FEES_LINKS_STATUS}</th>
                  <th>{FEES_LINKS_SOURCE}</th>
                  <th>{FEES_LINKS_ACTIONS}</th>
                </tr>
              </thead>
              <tbody>
                {links.map((l) => (
                  <tr key={l.id}>
                    <td>{l.feeSiteCode}</td>
                    <td>
                      {l.resourceName !== undefined ? `${l.resourceName}（` : ''}
                      {l.resourceKey}
                      {l.resourceName !== undefined ? '）' : ''}
                      {l.similarity !== undefined && l.similarity > 0 && (
                        <span className="chip chip--muted">{FEES_LINKS_SIMILARITY((l.similarity * 100).toFixed(0))}</span>
                      )}
                    </td>
                    <td>
                      <span className="chip chip--muted">{linkGradeLabel(l.grade)}</span>
                      {l.rule !== undefined && l.rule !== '' && <span className="muted"> {l.rule}</span>}
                    </td>
                    <td>
                      <span className={`chip chip--${l.status === 'confirmed' ? 'ok' : l.status === 'pending' ? 'warn' : 'muted'}`}>
                        {linkStatusLabel(l.status)}
                      </span>
                    </td>
                    <td className="muted">{l.source === 'human' ? LINKS_SOURCE_HUMAN : LINKS_SOURCE_AUTO}</td>
                    <td>
                      {l.status === 'pending' && (
                        <>
                          <button className="btn btn--ghost" onClick={() => void doConfirm(l)}>
                            {FEES_LINKS_CONFIRM}
                          </button>
                          <button className="btn btn--ghost" onClick={() => setDismissTarget(l)}>
                            {FEES_LINKS_DISMISS}
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_RULES_TITLE}</h2>
        <div className="panel__body">
          <p className="panel__desc">{FEES_RULES_DESC}</p>
          <div className="filters">
            <label className="field field--check">
              <input
                type="checkbox"
                checked={rulesEnabledOnly}
                onChange={(e) => setRulesEnabledOnly(e.target.checked)}
              />
              <span>仅已启用</span>
            </label>
          </div>
          {rulesError !== '' && <ErrorBlock message={rulesError} />}
          {rules !== null && rules.length === 0 && <p className="empty">{FEES_RULES_EMPTY}</p>}
          {rules !== null && rules.length > 0 && (
            <table className="table">
              <thead>
                <tr>
                  <th>{FEES_RULES_COL_NAME}</th>
                  <th>{FEES_RULES_COL_TYPE}</th>
                  <th>{FEES_RULES_COL_TUPLE}</th>
                  <th>{FEES_RULES_COL_SEVERITY}</th>
                  <th>{FEES_RULES_COL_STATE}</th>
                  <th>{FEES_RULES_COL_ACTIONS}</th>
                </tr>
              </thead>
              <tbody>
                {rules.map((r) => (
                  <tr key={r.id}>
                    <td>
                      {r.name}
                      {r.description !== undefined && r.description !== '' && (
                        <span className="chip chip--muted" title={r.description}>{r.description}</span>
                      )}
                    </td>
                    <td>{FEE_TYPES.find((t) => t.value === r.feeType)?.label ?? r.feeType}</td>
                    <td>{ruleTupleSummary(r)}</td>
                    <td>
                      <span className={`chip chip--${r.severity === 'high' ? 'error' : r.severity === 'medium' ? 'warn' : 'muted'}`}>
                        {r.severity}
                      </span>
                    </td>
                    <td>
                      <span className={`chip chip--${r.enabled ? 'ok' : 'muted'}`}>
                        {r.enabled ? FEES_RULES_STATE_ENABLED : FEES_RULES_STATE_DISABLED}
                      </span>
                    </td>
                    <td>
                      <button className="btn btn--ghost" onClick={() => void doOpenAudit(r)}>
                        {FEES_RULES_AUDIT}
                      </button>{' '}
                      {r.enabled ? (
                        <button className="btn btn--ghost" onClick={() => setRuleToggleTarget({ rule: r, enable: false })}>
                          {FEES_RULES_DISABLE}
                        </button>
                      ) : (
                        <button className="btn" onClick={() => setRuleToggleTarget({ rule: r, enable: true })}>
                          {FEES_RULES_ENABLE}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <details className="rule-create">
            <summary>{FEES_RULES_CREATE_TITLE}</summary>
            <div className="filters">
              <label className="field">
                <span>{FEES_RULES_CREATE_NAME}</span>
                <input value={ruleName} onChange={(e) => setRuleName(e.target.value)} placeholder={FEES_RULES_CREATE_NAME_PH} />
              </label>
              <label className="field">
                <span>{FEES_IMPORT_TYPE}</span>
                <select value={ruleType} onChange={(e) => setRuleType(e.target.value)}>
                  {FEE_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>{t.label}</option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>{FEES_RULES_CREATE_METRIC}</span>
                <select value={ruleMetric} onChange={(e) => setRuleMetric(e.target.value)}>
                  <option value="amount">amount（金额）</option>
                  <option value="rows">rows（行数）</option>
                </select>
              </label>
              <label className="field">
                <span>{FEES_RULES_CREATE_AGG}</span>
                <select value={ruleAgg} onChange={(e) => setRuleAgg(e.target.value)} disabled={ruleMetric === 'rows'}>
                  <option value="sum">sum</option>
                  <option value="avg">avg</option>
                  <option value="max">max</option>
                  <option value="count">count</option>
                </select>
              </label>
              <label className="field">
                <span>{FEES_RULES_CREATE_COMPARATOR}</span>
                <select value={ruleComparator} onChange={(e) => setRuleComparator(e.target.value)}>
                  <option value="gt">gt（大于）</option>
                  <option value="gte">gte（大于等于）</option>
                  <option value="lt">lt（小于）</option>
                  <option value="lte">lte（小于等于）</option>
                  <option value="mom_ratio">mom_ratio（环比涨幅比）</option>
                </select>
              </label>
              <label className="field field--xs">
                <span>{FEES_RULES_CREATE_THRESHOLD}</span>
                <input
                  value={ruleThreshold}
                  onChange={(e) => setRuleThreshold(e.target.value)}
                  inputMode="decimal"
                  title={FEES_RULES_CREATE_THRESHOLD_HINT}
                />
              </label>
              {ruleComparator === 'mom_ratio' && (
                <label className="field field--xs">
                  <span>{FEES_RULES_CREATE_WINDOW}</span>
                  <input value={ruleWindow} onChange={(e) => setRuleWindow(e.target.value)} inputMode="numeric" />
                </label>
              )}
              <label className="field">
                <span>{FEES_RULES_CREATE_SEVERITY}</span>
                <select value={ruleSeverity} onChange={(e) => setRuleSeverity(e.target.value)}>
                  <option value="high">high</option>
                  <option value="medium">medium</option>
                  <option value="low">low</option>
                </select>
              </label>
              <label className="field">
                <span>{FEES_RULES_CREATE_DESC}</span>
                <input value={ruleDescription} onChange={(e) => setRuleDescription(e.target.value)} placeholder={FEES_RULES_CREATE_DESC_PH} />
              </label>
              <button className="btn btn--primary" disabled={ruleCreating} onClick={() => void doCreateRule()}>
                {ruleCreating ? <Spinner /> : FEES_RULES_CREATE_SUBMIT}
              </button>
            </div>
            <p className="hint">{FEES_RULES_CREATE_THRESHOLD_HINT}</p>
          </details>
        </div>
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_BATCHES_TITLE}</h2>
        <div className="panel__body">
          {batchesError !== '' && <ErrorBlock message={batchesError} />}
          {batches !== null && batches.length === 0 && (
            <p className="empty">{FEES_BATCHES_EMPTY}</p>
          )}
          {batches !== null && batches.length > 0 && (
            <table className="table table--interactive">
              <thead>
                <tr>
                  <th>{FEES_BATCHES_COL_FILE}</th>
                  <th>{FEES_BATCHES_COL_TYPE}</th>
                  <th>{FEES_BATCHES_COL_PERIOD}</th>
                  <th>{FEES_BATCHES_COL_ROWS}</th>
                  <th>{FEES_BATCHES_COL_STATUS}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {batches.map((b) => (
                  <tr key={b.id}>
                    <td title={b.id}>{b.workbookName}</td>
                    <td>{b.feeTypeLabel}</td>
                    <td>{b.period}</td>
                    <td>
                      {b.rowsImported}/{b.rowsTotal}
                      {b.rowsFailed > 0 && <span className="chip chip--warn">{FEES_BATCHES_FAILED_ROWS(b.rowsFailed)}</span>}
                    </td>
                    <td>
                      <span className={`chip chip--${b.status === 'completed' ? 'ok' : b.status === 'failed' ? 'error' : 'muted'}`}>
                        {b.status}
                      </span>
                    </td>
                    <td>
                      {b.status === 'completed' && (
                        <button className="btn btn--ghost" onClick={() => setRollbackTarget(b)}>
                          {FEES_BATCHES_ROLLBACK}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <section className="panel">
        <h2 className="panel__title">{FEES_IMPORT_TITLE}</h2>
        <div className="panel__body">
          <div className="filters">
            <label className="field">
              <span>{FEES_IMPORT_MODE}</span>
              <select value={importMode} onChange={(e) => setImportMode(e.target.value === 'sheets' ? 'sheets' : 'single')}>
                <option value="single">{FEES_IMPORT_MODE_SINGLE}</option>
                <option value="sheets">{FEES_IMPORT_MODE_SHEETS}</option>
              </select>
            </label>
            {importMode === 'single' && (
              <label className="field">
                <span>{FEES_IMPORT_TYPE}</span>
                <select value={importType} onChange={(e) => setImportType(e.target.value)}>
                  {FEE_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>{t.label}</option>
                  ))}
                </select>
              </label>
            )}
            <label className="field field--xs">
              <span>{FEES_IMPORT_YEAR}</span>
              <input value={importYear} onChange={(e) => setImportYear(e.target.value)} inputMode="numeric" />
            </label>
            <label className="field field--xs">
              <span>{FEES_IMPORT_MONTH}</span>
              <input value={importMonth} onChange={(e) => setImportMonth(e.target.value)} inputMode="numeric" />
            </label>
            <label className="field">
              <span>{FEES_IMPORT_FILE}</span>
              <input
                ref={fileInput}
                type="file"
                accept={importMode === 'sheets' ? '.xlsx' : '.csv,.xlsx'}
              />
            </label>
            <button className="btn" disabled={probing} onClick={() => void doProbe()}>
              {probing ? FEES_PROBE_PROBING : FEES_PROBE_SUBMIT}
            </button>
            <button className="btn btn--primary" disabled={importing} onClick={() => void doImport()}>
              {importing ? <Spinner /> : FEES_IMPORT_SUBMIT}
            </button>
          </div>
          {probeResult !== null && (
            <div className="probe-card">
              <p className="probe-card__title">{FEES_PROBE_TITLE}</p>
              <dl className="probe-card__grid">
                <div><dt>{FEES_PROBE_KIND}</dt><dd>{probeResult.kindLabel !== '' ? probeResult.kindLabel : probeResult.kind}</dd></div>
                <div><dt>{FEES_PROBE_PERIOD}</dt>
                  <dd>{probeResult.year > 0
                    ? `${probeResult.year}-${String(probeResult.month).padStart(2, '0')}`
                    : probeResult.month > 0
                      ? `${String(probeResult.month).padStart(2, '0')} 月（年份无字段——弱识别）`
                      : '—'}</dd></div>
                <div><dt>{FEES_PROBE_CONFIDENCE}</dt>
                  <dd>{probeResult.confidence === 'strong' ? FEES_PROBE_CONF_STRONG
                    : probeResult.confidence === 'weak' ? FEES_PROBE_CONF_WEAK
                    : FEES_PROBE_CONF_NONE}</dd></div>
                <div className="probe-card__basis"><dt>{FEES_PROBE_BASIS}</dt><dd>{probeResult.basis !== '' ? probeResult.basis : '—'}</dd></div>
              </dl>
              {probeMismatch ? (
                <p className="probe-card__verdict probe-card__verdict--mismatch">
                  {FEES_PROBE_MISMATCH(`${probeResult.year}-${String(probeResult.month).padStart(2, '0')}`, `${importYear}-${importMonth.padStart(2, '0')}`)}
                </p>
              ) : probeResult.year > 0 ? (
                <p className="probe-card__verdict probe-card__verdict--ok">
                  {FEES_PROBE_MATCH(`${probeResult.year}-${String(probeResult.month).padStart(2, '0')}`)}
                </p>
              ) : (
                <p className="probe-card__verdict">{FEES_PROBE_UNKNOWN}</p>
              )}
              {probeMismatch && (
                <label className="probe-card__trust">
                  <input
                    type="checkbox"
                    checked={trustDeclared}
                    onChange={(e) => setTrustDeclared(e.target.checked)}
                  />
                  <span>{FEES_PROBE_TRUST}</span>
                </label>
              )}
            </div>
          )}
        </div>
      </section>

      <ConfirmDialog
        open={rollbackTarget !== null}
        title={FEES_BATCHES_ROLLBACK}
        message={rollbackTarget !== null ? FEES_BATCHES_ROLLBACK_CONFIRM(rollbackTarget.workbookName, rollbackTarget.period) : ''}
        confirmLabel={rollingBack ? FEES_BATCHES_ROLLBACK_BUSY : FEES_BATCHES_ROLLBACK}
        busy={rollingBack}
        onConfirm={() => void doRollback()}
        onCancel={() => {
          setRollbackTarget(null);
        }}
      />

      <Drawer
        open={auditRule !== null}
        title={auditRule !== null ? FEES_RULES_AUDIT_TITLE(auditRule.name) : ''}
        onClose={() => setAuditRule(null)}
      >
        {auditLoading && <Spinner />}
        {!auditLoading && auditEntries !== null && auditEntries.length === 0 && (
          <p className="empty">{FEES_RULES_AUDIT_EMPTY}</p>
        )}
        {auditEntries !== null && auditEntries.length > 0 && (
          <table className="table">
            <thead>
              <tr>
                <th>{FEES_RULES_AUDIT_ACTION}</th>
                <th>{FEES_RULES_AUDIT_ACTOR}</th>
                <th>{FEES_RULES_AUDIT_AT}</th>
                <th>{FEES_RULES_AUDIT_CHANGES}</th>
              </tr>
            </thead>
            <tbody>
              {auditEntries.map((a) => (
                <tr key={a.id}>
                  <td><span className={`chip chip--${a.action === 'enabled' ? 'ok' : a.action === 'disabled' ? 'muted' : 'info'}`}>{a.action}</span></td>
                  <td>{a.actor}</td>
                  <td>{a.at}</td>
                  <td>{a.changes !== undefined && a.changes !== '' ? a.changes : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="drawer__actions">
          <button className="btn" onClick={() => setAuditRule(null)}>{FEES_RULES_AUDIT_CLOSE}</button>
        </div>
      </Drawer>

      <ConfirmDialog
        open={ruleToggleTarget !== null}
        title={ruleToggleTarget !== null && ruleToggleTarget.enable ? FEES_RULES_ENABLE : FEES_RULES_DISABLE}
        message={ruleToggleTarget !== null
          ? (ruleToggleTarget.enable
            ? FEES_RULES_CONFIRM_ENABLE(ruleToggleTarget.rule.name)
            : FEES_RULES_CONFIRM_DISABLE(ruleToggleTarget.rule.name))
          : ''}
        confirmLabel={ruleToggling ? FEES_RULES_TOGGLING : (ruleToggleTarget !== null && ruleToggleTarget.enable ? FEES_RULES_ENABLE : FEES_RULES_DISABLE)}
        busy={ruleToggling}
        onConfirm={() => void doToggleRule()}
        onCancel={() => {
          setRuleToggleTarget(null);
        }}
      />

      <ConfirmDialog
        open={dismissTarget !== null}
        title={FEES_LINKS_DISMISS}
        message={dismissTarget !== null
          ? FEES_LINKS_DISMISS_CONFIRM(dismissTarget.feeSiteCode, dismissTarget.resourceKey)
          : ''}
        confirmLabel={dismissing ? FEES_LINKS_DISMISSING : FEES_LINKS_DISMISS}
        busy={dismissing}
        onConfirm={() => void doDismiss()}
        onCancel={() => {
          setDismissTarget(null);
        }}
      />
    </div>
  );
}
