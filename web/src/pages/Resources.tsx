/**
 * 资源浏览页 /resources：
 * - 筛选（类型 / 状态 / 搜索防抖 300ms）+ 分页表格；
 * - 行点击 → 右侧抽屉（属性 / 来源 / 关联，关联支持展开二跳）；
 * - 支持 ?focus=<id> 深链直接打开抽屉（问题清单实体链接跳转入口）。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type {
  AlertItem,
  AttributeChangeItem,
  GraphEdge,
  GraphNode,
  Paged,
  RelationGraph,
  RenameProposalItem,
  ResourceDetail,
  RunStateHistoryItem,
  ResourceItem,
  ResourceMetricsResponse,
  ResourceType,
} from '../api/types';
import {
  applyAttributeChange,
  applyRename,
  debounce,
  dismissAttributeChange,
  formatApiError,
  formatApiErrorText,
  getRunStateHistory,
  getResource,
  getResourceMetrics,
  getResourceRelations,
  listAlerts,
  listAttributeChanges,
  listMetricKeys,
  listRenames,
  listResources,
  rejectRename,
} from '../api/client';
import {
  BTN_EXPAND_2HOP,
  BTN_COLLAPSE_2HOP,
  BTN_IMPORT_DATA,
  BTN_EXPORT_RESOURCES,
  FILTER_ALL,
  format,
  formatDateTime,
  NO_VALUE,
  RELATION_LABELS,
  RES_ATTR_FIRST_BATCH,
  RES_ATTR_UPDATED_AT,
  RES_CHANGE_APPLY,
  RES_CHANGE_APPLYING,
  RES_CHANGE_DISMISS,
  RES_CHANGES_ACTION_FAILED,
  RES_CHANGES_APPLIED_RETARGETED_TOAST,
  RES_CHANGES_APPLIED_TOAST,
  RES_CHANGES_BATCH_ROW,
  RES_CHANGES_DISMISSED_TOAST,
  RES_CHANGES_HINT,
  RES_CHANGES_REF_FIELD_HINT,
  RES_CHANGES_TITLE,
  RES_COL_KEY,
  RES_COL_NAME,
  RES_COL_STATUS,
  RES_COL_TYPE,
  RES_COL_UPDATED,
  RES_DRAWER_TAB_ATTRS,
  RES_DRAWER_TAB_ALERTS,
  RES_DRAWER_TAB_GRAPH,
  RES_RUNSTATE_HISTORY_TITLE,
  RES_RUNSTATE_LABEL,
  RES_DRAWER_TAB_METRICS,
  RES_DRAWER_TAB_RELATIONS,
  RES_DRAWER_TAB_SOURCE,
  METRIC_CHART_POINTS,
  METRIC_EMPTY,
  METRICS_EXPORT_BTN,
  METRICS_EXPORT_EMPTY,
  METRICS_EXPORT_FILENAME,
  METRICS_EXPORT_TOAST,
  METRICS_EXPORT_WORKING,
  METRIC_LOAD_FAILED,
  RES_ALERTS_EMPTY,
  RES_ALERTS_LINK,
  RES_ALERTS_LOAD_FAILED,
  RES_ALERTS_MORE,
  RES_ALERTS_TAB_HINT,
  ALR_VALUE_TEXT,
  ALERT_COMPARATOR_LABELS,
  formatRelativeTime,
  RES_EMPTY_DESC,
  RES_EMPTY_TITLE,
  RES_EXPORT_HINT,
  RES_FILTER_SEARCH,
  RES_FILTER_STATUS,
  RES_FILTER_TYPE,
  RES_HOP2_EMPTY,
  RES_HOP2_TITLE,
  RES_NO_RELATIONS,
  RES_NO_SOURCE,
  RES_RELATION_COUNT,
  RES_SEARCH_PLACEHOLDER,
  RES_SOURCE_ITEM,
  RES_TABLE_LABEL,
  RESOURCE_STATUS_VALUES,
  RESOURCE_TYPE_LABELS,
  RES_RENAMES_ACTION_FAILED,
  RES_RENAMES_APPLIED_TOAST,
  RES_RENAMES_HINT,
  RES_RENAMES_MATCHED,
  RES_RENAMES_REJECTED_TOAST,
  RES_RENAMES_TITLE,
  RES_RENAME_CONFIRM,
  RES_RENAME_KEYS,
  RES_RENAME_REJECT,
  RES_RENAME_WORKING,
} from '../i18n';
import { fieldsForType, fieldLabel } from '../schema';
import { Drawer } from '../components/Drawer';
import { MetricChart } from '../components/MetricChart';
import { RunStateChip } from '../components/RunStateChip';
import { AlertStateChip } from '../components/AlertStateChip';
import { SeverityBadge } from '../components/SeverityBadge';
import { GraphAnalysisTab } from '../components/GraphAnalysisTab';
import { EmptyState } from '../components/EmptyState';
import { ErrorBlock } from '../components/ErrorBlock';
import { Pagination } from '../components/Pagination';
import { Spinner } from '../components/Spinner';
import { StatusChip } from '../components/StatusChip';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { TypeChip } from '../components/TypeChip';
import { IconSearch, IconUpload, IconDownload } from '../components/icons';
import { useToast } from '../components/Toast';
import { useSeq } from '../hooks/useSeq';
import { TOAST_COPIED } from '../i18n';

const PAGE_SIZE = 20;
const SEARCH_DEBOUNCE_MS = 300;

type DrawerTab = 'attrs' | 'source' | 'relations' | 'metrics' | 'alerts' | 'graph';

/** 指标 CSV 列（v0.11-R12：与告警导出对称的序列+点平面化）。 */
const METRICS_CSV_COLUMNS = ['seriesId', 'metricKey', 'ts', 'value', 'quality'] as const;

/** CSV 字段转义：含逗号/引号/换行时包引号并双写内部引号（告警导出同规则）。 */
function csvCell(value: string): string {
  if (/[",\r\n]/.test(value)) {
    return `"${value.replace(/"/g, '""')}"`;
  }
  return value;
}

/** 指标点总数（导出工具条的一行汇总）。 */
function totalMetricPoints(metrics: ResourceMetricsResponse): number {
  return metrics.series.reduce((sum, series) => sum + series.points.length, 0);
}

/** 数字显示：整数无小数位，浮点保留最多 4 位有效（告警页同规则）。 */
function fmtMetricNum(value: number): string {
  if (Number.isInteger(value)) return String(value);
  return String(Number(value.toPrecision(4)));
}

// ---------------------------------------------------------------------------
// 资源抽屉
// ---------------------------------------------------------------------------

interface ResourceDrawerProps {
  resourceId: string;
  onClose: () => void;
}

/** 抽屉里的批次 id（P2/P2-21）：短码展示 + 点击复制完整 id（title 里可见全文）。 */
function BatchIdCell({ batchId }: { batchId: string }): ReactNode {
  const toast = useToast();
  const short = batchId.length > 12 ? `${batchId.slice(0, 12)}…` : batchId;
  function copy(): void {
    void copyText(batchId).then((ok: boolean) => {
      if (ok) {
        toast.show(TOAST_COPIED, 'success');
      }
      // 复制失败（权限被拒）：静默——title 已提供完整 id 可手动选择
    });
  }
  return (
    <button type="button" className="code-chip lab-copy-chip" title={batchId} onClick={copy}>
      {short}
    </button>
  );
}

/** 剪贴板复制：Clipboard API 优先，无头环境/权限被拒时 execCommand 兜底。 */
function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard !== undefined) {
    return navigator.clipboard.writeText(text).then(
      () => true,
      () => legacyCopy(text),
    );
  }
  return Promise.resolve(legacyCopy(text));
}

/** execCommand 兜底复制；成功返回 true，异常/失败返回 false。 */
function legacyCopy(text: string): boolean {
  try {
    const area = document.createElement('textarea');
    area.value = text;
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(area);
    return ok;
  } catch {
    return false;
  }
}

function orderedAttributes(resource: ResourceDetail): Array<[string, string]> {
  const specFields = fieldsForType(resource.type).map((spec) => spec.field);
  const keys = Object.keys(resource.attributes);
  const known = specFields.filter((field) => keys.includes(field));
  const extra = keys.filter((field) => !specFields.includes(field)).sort();
  return [...known, ...extra].map((field) => [field, resource.attributes[field] ?? '']);
}

function RelationItem({
  edge,
  anchorId,
  node,
}: {
  edge: GraphEdge;
  anchorId: string;
  node: GraphNode | undefined;
}) {
  if (node === undefined) return null;
  const outgoing = edge.fromId === anchorId;
  const relLabel = RELATION_LABELS[edge.relationType] ?? edge.relationType;
  return (
    <div className="relation-item">
      <span className="chip chip--primary">{relLabel}</span>
      <span className="relation-arrow" aria-hidden="true">{outgoing ? '→' : '←'}</span>
      <TypeChip type={node.type} />
      <span className="cell-strong">{node.name === '' ? node.naturalKey : node.name}</span>
      <span className="muted-note">{node.naturalKey}</span>
    </div>
  );
}

function ResourceDrawer({ resourceId, onClose }: ResourceDrawerProps) {
  const [detail, setDetail] = useState<ResourceDetail | null>(null);
  const [graph, setGraph] = useState<RelationGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [tab, setTab] = useState<DrawerTab>('attrs');
  const [depth2, setDepth2] = useState<RelationGraph | null>(null);
  const [loading2, setLoading2] = useState(false);
  /** 二跳展开失败的内联错误（AUD2-13：不再静默吞掉）。 */
  const [depth2Error, setDepth2Error] = useState<string | null>(null);
  const [depth2ErrorDetail, setDepth2ErrorDetail] = useState<string | null>(null);
  /** 待确认属性变更（W4-D）：抽屉加载时并行拉取，无 pending 时面板不渲染。 */
  const [pendingChanges, setPendingChanges] = useState<AttributeChangeItem[]>([]);
  /** 行内操作错误（应用/忽略失败时按 change id 定位）。 */
  const [changeErrors, setChangeErrors] = useState<Record<string, string>>({});
  /** 正在处理中的 change id（按钮禁用）。 */
  const [changeBusy, setChangeBusy] = useState<string | null>(null);
  /** 运行状态历史（v0.9-R5）：抽屉加载时并行拉取（W22 状态时序）。 */
  const [runStateHistory, setRunStateHistory] = useState<RunStateHistoryItem[] | null>(null);
  /** 疑似改名提案（W6-D）：本资源作为旧资源（身份保留方）的 pending 提案。 */
  const [pendingRenames, setPendingRenames] = useState<RenameProposalItem[]>([]);
  /** 改名操作行内错误（按提案 id 定位）。 */
  const [renameErrors, setRenameErrors] = useState<Record<string, string>>({});
  /** 正在处理中的提案 id（按钮禁用）。 */
  const [renameBusy, setRenameBusy] = useState<string | null>(null);
  /** 性能指标序列（v0.9-R2）：懒加载——首次切到该页签才拉，缓存于组件态；
   *  keys 目录同理（key → 中文标签映射），两者并行、互不阻塞。 */
  const [metrics, setMetrics] = useState<ResourceMetricsResponse | null>(null);
  const [metricsLoading, setMetricsLoading] = useState(false);
  const [metricsError, setMetricsError] = useState<string | null>(null);
  const [metricLabels, setMetricLabels] = useState<Record<string, string>>({});
  const metricsSeqRef = useRef(0);
  /** 本资源告警（v0.11-R12）：懒加载——首次切到「告警」页签才拉，
   *  拉最近 50 条（全部生命周期状态），更多引导去告警中心。 */
  const [alertsData, setAlertsData] = useState<Paged<AlertItem> | null>(null);
  const [alertsLoading, setAlertsLoading] = useState(false);
  const [alertsError, setAlertsError] = useState<string | null>(null);
  const alertsSeqRef = useRef(0);
  /** 指标 CSV 导出中（防重复点击）。 */
  const [metricsExporting, setMetricsExporting] = useState(false);
  const toast = useToast();
  /** 取数竞态守卫（#109-D）：全仓 useSeq 纪律（App/Overview/LabWorkspace 等
   *  12 处）在抽屉主取数点的唯一漏网——A→B 快速切换时 5 路并发慢响应后到
   *  会串台盖新（旧数据覆盖新资源）。守卫语义见 hooks/useSeq.ts。 */
  const loadGuard = useSeq();

  const load = useCallback((): void => {
    const isCurrent = loadGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    Promise.all([
      getResource(resourceId),
      getResourceRelations(resourceId, 1),
      listAttributeChanges({ resourceId, status: 'pending', limit: 100 }),
      listRenames({ resourceId, status: 'pending', limit: 100 }),
      getRunStateHistory(resourceId).catch(() => null),
    ])
      .then((results) => {
        if (!isCurrent()) return; // #109-D：在途期间发起了新取数/卸载 → 整包丢弃
        setDetail(results[0]);
        setGraph(results[1]);
        setPendingChanges(results[2].items);
        setPendingRenames(results[3].items);
        setRunStateHistory(results[4] === null ? [] : results[4].items);
        setChangeErrors({});
        setRenameErrors({});
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [resourceId, loadGuard]);

  /** v0.9-R2：拉取本资源的指标序列 + 指标目录（key → 中文标签）。
   *  懒加载语义：仅首次进入「性能指标」页签触发；失败可经页签内重试。 */
  function loadMetrics(): void {
    const seq = ++metricsSeqRef.current;
    setMetricsLoading(true);
    setMetricsError(null);
    Promise.all([getResourceMetrics(resourceId), listMetricKeys()])
      .then(([res, catalog]) => {
        if (metricsSeqRef.current !== seq) return;
        setMetrics(res);
        const labels: Record<string, string> = {};
        for (const item of catalog.items) {
          labels[item.metricKey] = item.label;
        }
        setMetricLabels(labels);
        setMetricsLoading(false);
      })
      .catch((err: unknown) => {
        if (metricsSeqRef.current !== seq) return;
        const display = formatApiErrorText(err);
        setMetricsError(display);
        setMetricsLoading(false);
      });
  }

  /** v0.11-R12：拉本资源的告警记录（全部状态，最近 50 条）。 */
  function loadAlerts(): void {
    const seq = ++alertsSeqRef.current;
    setAlertsLoading(true);
    setAlertsError(null);
    listAlerts({ resourceId, page: 1, pageSize: 50 })
      .then((paged) => {
        if (alertsSeqRef.current !== seq) return;
        setAlertsData(paged);
        setAlertsLoading(false);
      })
      .catch((err: unknown) => {
        if (alertsSeqRef.current !== seq) return;
        setAlertsError(formatApiErrorText(err));
        setAlertsLoading(false);
      });
  }

  /** v0.11-R12：导出当前资源的指标点为 CSV（与告警导出对称：
   *  BOM 保证 Excel 识别 UTF-8、RFC4180 转义、30s 延迟撤销句柄）。 */
  function exportMetricsCsv(): void {
    if (metricsExporting) return;
    if (metrics === null || metrics.series.length === 0) {
      toast.show(METRICS_EXPORT_EMPTY, 'info');
      return;
    }
    setMetricsExporting(true);
    try {
      const rows: string[] = [METRICS_CSV_COLUMNS.join(',')];
      let points = 0;
      for (const series of metrics.series) {
        for (const p of series.points) {
          rows.push(csvCell(series.seriesId) + ',' + csvCell(series.metricKey) + ',' + csvCell(p.ts) + ',' + String(p.value) + ',' + p.quality);
          points += 1;
        }
      }
      const blob = new Blob([`\ufeff${rows.join('\r\n')}\r\n`], { type: 'text/csv;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${METRICS_EXPORT_FILENAME}-${resourceId}-${new Date().toISOString().slice(0, 10)}.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      // 延迟撤销：立即 revoke 与浏览器抓取下载句柄存在竞态（R7 惯例）
      window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
      toast.show(format(METRICS_EXPORT_TOAST, { n: points, s: metrics.series.length }), 'success');
    } finally {
      setMetricsExporting(false);
    }
  }

  /** 应用一条待确认变更：成功后行内刷新（重拉详情/关联/剩余 pending）；
   *  W13-D：引用字段且已有派生关系的 apply 会同步改挂关系，toast 文案带「关系已改挂」
   *  （relationRetargeted 由 apply 响应回真值）。 */
  function applyChange(change: AttributeChangeItem): void {
    setChangeBusy(change.id);
    setChangeErrors((prev) => ({ ...prev, [change.id]: '' }));
    applyAttributeChange(change.id)
      .then((applied) => {
        const template = applied.relationRetargeted
          ? RES_CHANGES_APPLIED_RETARGETED_TOAST
          : RES_CHANGES_APPLIED_TOAST;
        toast.show(format(template, { field: fieldLabel(change.field) }), 'success');
        load();
      })
      .catch((err: unknown) => {
        // 409 冲突（字段已被其它变更修改/非 pending）等 → 行内错误展示
        const display = formatApiErrorText(err);
        setChangeErrors((prev) => ({ ...prev, [change.id]: display }));
      })
      .finally(() => setChangeBusy(null));
  }

  /** 忽略一条待确认变更：成功后行内刷新。 */
  function dismissChange(change: AttributeChangeItem): void {
    setChangeBusy(change.id);
    setChangeErrors((prev) => ({ ...prev, [change.id]: '' }));
    dismissAttributeChange(change.id)
      .then(() => {
        toast.show(format(RES_CHANGES_DISMISSED_TOAST, { field: fieldLabel(change.field) }), 'info');
        load();
      })
      .catch((err: unknown) => {
        const display = formatApiErrorText(err);
        setChangeErrors((prev) => ({ ...prev, [change.id]: display }));
      })
      .finally(() => setChangeBusy(null));
  }

  /** 确认一条疑似改名：旧资源保留 ID 与关系并改用新键，新资源消失；成功后整抽屉刷新。 */
  function confirmRename(proposal: RenameProposalItem): void {
    setRenameBusy(proposal.id);
    setRenameErrors((prev) => ({ ...prev, [proposal.id]: '' }));
    applyRename(proposal.id)
      .then(() => {
        toast.show(format(RES_RENAMES_APPLIED_TOAST, { old: proposal.oldKey, new: proposal.newKey }), 'success');
        load();
      })
      .catch((err: unknown) => {
        // 409（非 pending / 新资源已不存在而提案仍在——target_missing 防御性守卫）等 → 行内错误展示
        const display = formatApiErrorText(err);
        setRenameErrors((prev) => ({ ...prev, [proposal.id]: display }));
      })
      .finally(() => setRenameBusy(null));
  }

  /** 拒绝一条疑似改名：两个资源维持独立（导入的默认结果）；成功后整抽屉刷新。 */
  function rejectRenameProposal(proposal: RenameProposalItem): void {
    setRenameBusy(proposal.id);
    setRenameErrors((prev) => ({ ...prev, [proposal.id]: '' }));
    rejectRename(proposal.id)
      .then(() => {
        toast.show(format(RES_RENAMES_REJECTED_TOAST, { old: proposal.oldKey }), 'info');
        load();
      })
      .catch((err: unknown) => {
        const display = formatApiErrorText(err);
        setRenameErrors((prev) => ({ ...prev, [proposal.id]: display }));
      })
      .finally(() => setRenameBusy(null));
  }

  useEffect(() => {
    load();
    setDepth2(null);
    setDepth2Error(null);
    setDepth2ErrorDetail(null);
    setTab('attrs');
  }, [load]);

  function expandDepth2(): void {
    setLoading2(true);
    setDepth2Error(null);
    setDepth2ErrorDetail(null);
    getResourceRelations(resourceId, 2)
      .then((result) => {
        setDepth2(result);
        setLoading2(false);
      })
      .catch((err: unknown) => {
        // AUD2-13：二跳展开失败 → 内联错误 + 重试，而不是让转圈无声消失
        setLoading2(false);
        const display = formatApiError(err);
        setDepth2Error(display.title);
        setDepth2ErrorDetail(display.detail);
      });
  }

  const title = detail === null ? '' : detail.name === '' ? detail.naturalKey : detail.name;
  const nodeById = useMemo(() => {
    const source = depth2 ?? graph;
    if (source === null) return new Map<string, GraphNode>();
    return new Map(source.nodes.map((node) => [node.id, node]));
  }, [depth2, graph]);

  const hop1Edges: GraphEdge[] = graph === null ? [] : graph.edges;
  const anchorId = detail?.id ?? '';

  const hop2Groups: Array<{ anchor: GraphNode; edges: GraphEdge[] }> = useMemo(() => {
    if (depth2 === null || graph === null || detail === null) return [];
    const centerId = detail.id;
    const hop1Ids = new Set(
      graph.edges.flatMap((edge) => [edge.fromId, edge.toId]).filter((id) => id !== centerId),
    );
    const groups: Array<{ anchor: GraphNode; edges: GraphEdge[] }> = [];
    for (const hop1Id of hop1Ids) {
      const anchor = nodeById.get(hop1Id);
      if (anchor === undefined) continue;
      const edges = depth2.edges.filter(
        (edge) =>
          (edge.fromId === hop1Id || edge.toId === hop1Id) &&
          edge.fromId !== centerId &&
          edge.toId !== centerId,
      );
      if (edges.length > 0) {
        groups.push({ anchor, edges });
      }
    }
    return groups;
  }, [depth2, graph, detail, nodeById]);

  return (
    <Drawer open title={title} onClose={onClose}>
      {loading ? (
        <div className="loading-panel">
          <Spinner />
        </div>
      ) : error !== null || detail === null ? (
        <ErrorBlock message={error} detail={errorDetail} onRetry={load} />
      ) : (
        <>
          <div className="drawer-summary">
            <TypeChip type={detail.type} />
            <StatusChip status={detail.status} />
            <span className="cell-strong">{detail.naturalKey}</span>
          </div>
          {pendingChanges.length > 0 && (
            <div className="change-panel" data-testid="pending-changes">
              <div className="change-panel-title">{RES_CHANGES_TITLE}</div>
              <div className="muted-note">{RES_CHANGES_HINT}</div>
              {pendingChanges.map((change) => {
                const busy = changeBusy === change.id;
                const changeError = changeErrors[change.id] ?? '';
                return (
                  <div className="change-row" key={change.id}>
                    <div className="change-main">
                      <span className="cell-strong">{fieldLabel(change.field)}</span>
                      <span className="change-values" title={`${change.oldValue} → ${change.newValue}`}>
                        <span className="change-old">{change.oldValue === '' ? NO_VALUE : change.oldValue}</span>
                        <span className="change-arrow" aria-hidden="true">→</span>
                        <span className="change-new">{change.newValue}</span>
                      </span>
                      {change.relationRetargeted && (
                        <span className="muted-note">{RES_CHANGES_REF_FIELD_HINT}</span>
                      )}
                      <span className="muted-note">
                        {format(RES_CHANGES_BATCH_ROW, {
                          batch: change.batchId.length > 12 ? `${change.batchId.slice(0, 12)}…` : change.batchId,
                          row: change.rowNumber,
                        })}
                      </span>
                    </div>
                    <div className="change-actions">
                      <button
                        type="button"
                        className="btn btn--primary btn--sm"
                        disabled={busy}
                        onClick={() => applyChange(change)}
                      >
                        {busy ? RES_CHANGE_APPLYING : RES_CHANGE_APPLY}
                      </button>
                      <button
                        type="button"
                        className="btn btn--secondary btn--sm"
                        disabled={busy}
                        onClick={() => dismissChange(change)}
                      >
                        {RES_CHANGE_DISMISS}
                      </button>
                    </div>
                    {changeError !== '' && (
                      <div className="form-error change-error" role="alert">
                        {format(RES_CHANGES_ACTION_FAILED, { message: changeError })}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
          {pendingRenames.length > 0 && (
            <div className="change-panel" data-testid="pending-renames">
              <div className="change-panel-title">{RES_RENAMES_TITLE}</div>
              <div className="muted-note">{RES_RENAMES_HINT}</div>
              {pendingRenames.map((proposal) => {
                const busy = renameBusy === proposal.id;
                const renameError = renameErrors[proposal.id] ?? '';
                return (
                  <div className="change-row" key={proposal.id}>
                    <div className="change-main">
                      <span className="cell-strong">{format(RES_RENAME_KEYS, { old: proposal.oldKey, new: proposal.newKey })}</span>
                      <span className="muted-note">
                        {format(RES_RENAMES_MATCHED, { count: proposal.matchedFields.length })}
                        {' · '}
                        {format(RES_CHANGES_BATCH_ROW, {
                          batch: proposal.batchId.length > 12 ? `${proposal.batchId.slice(0, 12)}…` : proposal.batchId,
                          row: proposal.rowNumber,
                        })}
                      </span>
                    </div>
                    <div className="change-actions">
                      <button
                        type="button"
                        className="btn btn--primary btn--sm"
                        disabled={busy}
                        onClick={() => confirmRename(proposal)}
                      >
                        {busy ? RES_RENAME_WORKING : RES_RENAME_CONFIRM}
                      </button>
                      <button
                        type="button"
                        className="btn btn--secondary btn--sm"
                        disabled={busy}
                        onClick={() => rejectRenameProposal(proposal)}
                      >
                        {RES_RENAME_REJECT}
                      </button>
                    </div>
                    {renameError !== '' && (
                      <div className="form-error change-error" role="alert">
                        {format(RES_RENAMES_ACTION_FAILED, { message: renameError })}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
          <div className="tabs">
            <button
              type="button"
              className={`tab${tab === 'attrs' ? ' tab--active' : ''}`}
              onClick={() => setTab('attrs')}
            >
              {RES_DRAWER_TAB_ATTRS}
            </button>
            <button
              type="button"
              className={`tab${tab === 'metrics' ? ' tab--active' : ''}`}
              onClick={() => {
                setTab('metrics');
                if (metrics === null && !metricsLoading) {
                  loadMetrics();
                }
              }}
            >
              {RES_DRAWER_TAB_METRICS}
            </button>
            <button
              type="button"
              className={`tab${tab === 'alerts' ? ' tab--active' : ''}`}
              onClick={() => {
                setTab('alerts');
                if (alertsData === null && !alertsLoading) {
                  loadAlerts();
                }
              }}
            >
              {RES_DRAWER_TAB_ALERTS}
            </button>
            <button
              type="button"
              className={`tab${tab === 'graph' ? ' tab--active' : ''}`}
              onClick={() => setTab('graph')}
            >
              {RES_DRAWER_TAB_GRAPH}
            </button>
            <button
              type="button"
              className={`tab${tab === 'source' ? ' tab--active' : ''}`}
              onClick={() => setTab('source')}
            >
              {RES_DRAWER_TAB_SOURCE}
            </button>
            <button
              type="button"
              className={`tab${tab === 'relations' ? ' tab--active' : ''}`}
              onClick={() => setTab('relations')}
            >
              {RES_DRAWER_TAB_RELATIONS}
            </button>
          </div>

          {tab === 'attrs' && (
            <table className="kv-table">
              <tbody>
                <tr>
                  <th scope="row">{RES_COL_KEY}</th>
                  <td>{detail.naturalKey}</td>
                </tr>
                <tr>
                  <th scope="row">{RES_COL_NAME}</th>
                  <td>{detail.name === '' ? NO_VALUE : detail.name}</td>
                </tr>
                <tr>
                  <th scope="row">{RES_COL_TYPE}</th>
                  <td>
                    <TypeChip type={detail.type} />
                  </td>
                </tr>
                <tr>
                  <th scope="row">{RES_COL_STATUS}</th>
                  <td>
                    <StatusChip status={detail.status} />
                  </td>
                </tr>
                <tr>
                  <th scope="row">{RES_RUNSTATE_LABEL}</th>
                  <td>
                    <RunStateChip state={detail.runState ?? ''} />
                    {(runStateHistory ?? []).length > 0 && (
                      <div className="runstate-history">
                        <div className="muted-note">{RES_RUNSTATE_HISTORY_TITLE}</div>
                        {(runStateHistory ?? []).slice(-8).reverse().map((h) => (
                          <div className="runstate-history-row" key={h.ts}>
                            <RunStateChip state={h.runState} />
                            <span className="muted-note" title={h.ts}>
                              {formatDateTime(h.ts)}
                            </span>
                            <span className="muted-note mono">{h.source}</span>
                          </div>
                        ))}
                      </div>
                    )}
                  </td>
                </tr>
                <tr>
                  <th scope="row">{RES_ATTR_FIRST_BATCH}</th>
                  <td>{detail.firstBatchId === '' ? NO_VALUE : <BatchIdCell batchId={detail.firstBatchId} />}</td>
                </tr>
                <tr>
                  <th scope="row">{RES_ATTR_UPDATED_AT}</th>
                  <td>{formatDateTime(detail.updatedAt)}</td>
                </tr>
                {orderedAttributes(detail).map(([field, value]) => (
                  <tr key={field}>
                    <th scope="row">{fieldLabel(field)}</th>
                    <td>{value === '' ? NO_VALUE : value}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {tab === 'source' && (
            <div className="source-list">
              {detail.sourceRefs.length === 0 ? (
                <div className="muted-note">{RES_NO_SOURCE}</div>
              ) : (
                detail.sourceRefs.map((ref, index) => (
                  <div className="source-item" key={`${ref.workbook}-${ref.sheet}-${ref.rowNumber}-${index}`}>
                    {format(RES_SOURCE_ITEM, {
                      workbook: ref.workbook,
                      sheet: ref.sheet,
                      row: ref.rowNumber,
                    })}
                  </div>
                ))
              )}
            </div>
          )}

          {tab === 'relations' && (
            <div className="relations">
              <div className="muted-note">
                {RES_RELATION_COUNT}：{detail.relationCount}
              </div>
              {hop1Edges.length === 0 ? (
                <div className="muted-note">{RES_NO_RELATIONS}</div>
              ) : (
                hop1Edges.map((edge) => (
                  <RelationItem
                    key={edge.id}
                    edge={edge}
                    anchorId={anchorId}
                    node={nodeById.get(edge.fromId === anchorId ? edge.toId : edge.fromId)}
                  />
                ))
              )}
              <div className="hop2">
                {depth2Error !== null && (
                  <ErrorBlock compact message={depth2Error} detail={depth2ErrorDetail} onRetry={expandDepth2} />
                )}
                {depth2 === null ? (
                  hop1Edges.length > 0 && (
                    <button
                      type="button"
                      className="btn btn--secondary btn--sm"
                      disabled={loading2}
                      onClick={expandDepth2}
                    >
                      {BTN_EXPAND_2HOP}
                    </button>
                  )
                ) : (
                  <>
                    <div className="hop2-title">{RES_HOP2_TITLE}</div>
                    {hop2Groups.length === 0 ? (
                      <div className="muted-note">{RES_HOP2_EMPTY}</div>
                    ) : (
                      hop2Groups.map((group) => (
                        <div className="hop2-group" key={group.anchor.id}>
                          <div className="hop2-anchor">
                            <TypeChip type={group.anchor.type} />
                            <span className="cell-strong">
                              {group.anchor.name === '' ? group.anchor.naturalKey : group.anchor.name}
                            </span>
                            <span className="muted-note">{group.anchor.naturalKey}</span>
                          </div>
                          {group.edges.map((edge) => (
                            <RelationItem
                              key={edge.id}
                              edge={edge}
                              anchorId={group.anchor.id}
                              node={nodeById.get(edge.fromId === group.anchor.id ? edge.toId : edge.fromId)}
                            />
                          ))}
                        </div>
                      ))
                    )}
                    <button
                      type="button"
                      className="btn btn--secondary btn--sm"
                      onClick={() => setDepth2(null)}
                    >
                      {BTN_COLLAPSE_2HOP}
                    </button>
                  </>
                )}
                {loading2 && <Spinner size={16} />}
              </div>
            </div>
          )}
          {tab === 'graph' && <GraphAnalysisTab resourceId={resourceId} />}
          {tab === 'metrics' && (
            <div className="metric-list">
              {metricsError !== null && (
                <ErrorBlock message={METRIC_LOAD_FAILED} detail={metricsError} onRetry={loadMetrics} />
              )}
              {metricsLoading && <Spinner size={16} />}
              {!metricsLoading && metricsError === null && metrics !== null && metrics.series.length === 0 && (
                <div className="muted-note">{METRIC_EMPTY}</div>
              )}
              {!metricsLoading &&
                metricsError === null &&
                metrics !== null &&
                metrics.series.length > 0 && (
                  <div className="metric-list-toolbar">
                    <span className="muted-note">
                      {format(METRIC_CHART_POINTS, { n: totalMetricPoints(metrics) })} · {metrics.series.length} 条序列
                    </span>
                    <button
                      type="button"
                      className="btn btn--secondary btn--sm metric-export-btn"
                      disabled={metricsExporting}
                      onClick={exportMetricsCsv}
                      title={METRICS_EXPORT_BTN}
                    >
                      {metricsExporting ? METRICS_EXPORT_WORKING : METRICS_EXPORT_BTN}
                    </button>
                  </div>
                )}
              {!metricsLoading &&
                metricsError === null &&
                metrics !== null &&
                metrics.series.map((series) => (
                  <MetricChart
                    key={series.seriesId}
                    series={series}
                    label={metricLabels[series.metricKey] ?? series.metricKey}
                  />
                ))}
            </div>
          )}
          {tab === 'alerts' && (
            <div className="drawer-alerts">
              <div className="muted-note">{RES_ALERTS_TAB_HINT}</div>
              {alertsError !== null && (
                <ErrorBlock message={RES_ALERTS_LOAD_FAILED} detail={alertsError} onRetry={loadAlerts} />
              )}
              {alertsLoading && <Spinner size={16} />}
              {!alertsLoading && alertsError === null && alertsData !== null && alertsData.items.length === 0 && (
                <div className="muted-note">{RES_ALERTS_EMPTY}</div>
              )}
              {!alertsLoading &&
                alertsError === null &&
                alertsData !== null &&
                alertsData.items.length > 0 && (
                  <>
                    {alertsData.items.map((alert) => (
                      <div className="drawer-alert-row" key={alert.alertId}>
                        <AlertStateChip state={alert.state} />
                        <SeverityBadge severity={alert.severity} />
                        <span className="cell-strong">{alert.ruleLabel}</span>
                        <span className="mono" title={alert.metricKey}>
                          {format(ALR_VALUE_TEXT, {
                            value: fmtMetricNum(alert.lastValue),
                            comparator: ALERT_COMPARATOR_LABELS[alert.comparator],
                            threshold: fmtMetricNum(alert.threshold),
                          })}
                        </span>
                        <span className="muted-note" title={formatDateTime(alert.firedAt)}>
                          {formatRelativeTime(alert.firedAt)}
                        </span>
                      </div>
                    ))}
                    <div className="drawer-alerts-footer">
                      {alertsData.total > alertsData.items.length && (
                        <span className="muted-note">
                          {format(RES_ALERTS_MORE, { n: alertsData.items.length, total: alertsData.total })}
                        </span>
                      )}
                      <Link className="btn btn--secondary btn--sm" to="/alerts">
                        {RES_ALERTS_LINK}
                      </Link>
                    </div>
                  </>
                )}
            </div>
          )}
        </>
      )}
    </Drawer>
  );
}

// ---------------------------------------------------------------------------
// 页面
// ---------------------------------------------------------------------------

export function ResourcesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const focusId = searchParams.get('focus');

  const [type, setType] = useState<ResourceType | ''>('');
  const [status, setStatus] = useState('');
  const [searchText, setSearchText] = useState('');
  const [query, setQuery] = useState('');
  const [page, setPage] = useState(1);
  const [data, setData] = useState<Paged<ResourceItem> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [drawerId, setDrawerId] = useState<string | null>(focusId);

  const requestSeqRef = useRef(0);

  const fetchResources = useCallback((): void => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listResources({
      type: type === '' ? undefined : type,
      q: query === '' ? undefined : query,
      status: status === '' ? undefined : status,
      page,
      pageSize: PAGE_SIZE,
    })
      .then((paged) => {
        if (requestSeqRef.current !== seq) return;
        setData(paged);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (requestSeqRef.current !== seq) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [type, query, status, page]);

  useEffect(() => {
    fetchResources();
  }, [fetchResources]);

  const debouncedSearch = useMemo(
    () =>
      debounce((value: string) => {
        setQuery(value);
        setPage(1);
      }, SEARCH_DEBOUNCE_MS),
    [],
  );

  useEffect(() => {
    if (focusId !== null) {
      setDrawerId(focusId);
    }
  }, [focusId]);

  function closeDrawer(): void {
    setDrawerId(null);
    if (focusId !== null) {
      const next = new URLSearchParams(searchParams);
      next.delete('focus');
      setSearchParams(next, { replace: true });
    }
  }

  const columns: TableColumn<ResourceItem>[] = [
    { key: 'naturalKey', header: RES_COL_KEY, render: (row) => <span className="cell-strong">{row.naturalKey}</span> },
    { key: 'name', header: RES_COL_NAME, render: (row) => (row.name === '' ? NO_VALUE : row.name) },
    { key: 'type', header: RES_COL_TYPE, render: (row) => <TypeChip type={row.type} /> },
    { key: 'status', header: RES_COL_STATUS, render: (row) => <StatusChip status={row.status} /> },
    { key: 'updatedAt', header: RES_COL_UPDATED, render: (row) => formatDateTime(row.updatedAt) },
  ];

  const noFilters = type === '' && status === '' && query === '';
  const showEmptyState = !loading && error === null && data !== null && data.total === 0 && noFilters;

  // 导出（W12-D）：导航式下载——浏览器直接请求导出端点，响应携带
  // Content-Disposition: attachment，页面不跳转、无异步状态（无 busy 态）。
  // 二进制下载不走 requestJson；这里用 window.location.assign 触发浏览器下载。
  // 范围口径：当前类型筛选生效（未选类型 = 全量导出），搜索关键词与状态
  // 筛选不参与（导出的是资源集合，不是检索结果子集——RES_EXPORT_HINT 同口径）。
  function exportResources(): void {
    const params = new URLSearchParams();
    if (type !== '') {
      params.set('types', type);
    }
    const qs = params.toString();
    window.location.assign(`/api/exports/resources${qs === '' ? '' : `?${qs}`}`);
  }

  return (
    <>
      <div className="card">
        <div className="filter-bar" role="search">
          <label className="filter-field">
            <span className="filter-label">{RES_FILTER_TYPE}</span>
            <select
              className="select"
              value={type}
              onChange={(event) => {
                setType(event.target.value === '' ? '' : (event.target.value as ResourceType));
                setPage(1);
              }}
            >
              <option value="">{FILTER_ALL}</option>
              {(Object.keys(RESOURCE_TYPE_LABELS) as ResourceType[]).map((key) => (
                <option key={key} value={key}>
                  {RESOURCE_TYPE_LABELS[key]}
                </option>
              ))}
            </select>
          </label>
          <label className="filter-field">
            <span className="filter-label">{RES_FILTER_STATUS}</span>
            <select
              className="select"
              value={status}
              onChange={(event) => {
                setStatus(event.target.value);
                setPage(1);
              }}
            >
              <option value="">{FILTER_ALL}</option>
              {RESOURCE_STATUS_VALUES.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>
          <div className="filter-field filter-field--grow">
            <span className="filter-label">{RES_FILTER_SEARCH}</span>
            <div className="search-box">
              <IconSearch />
              <input
                type="search"
                className="input"
                placeholder={RES_SEARCH_PLACEHOLDER}
                aria-label={RES_FILTER_SEARCH}
                value={searchText}
                onChange={(event) => {
                  setSearchText(event.target.value);
                  debouncedSearch(event.target.value);
                }}
              />
            </div>
          </div>
          <button
            type="button"
            className="btn btn--secondary"
            onClick={exportResources}
            title={RES_EXPORT_HINT}
          >
            <IconDownload />
            {BTN_EXPORT_RESOURCES}
          </button>
        </div>

        {error !== null ? (
          <ErrorBlock message={error} detail={errorDetail} onRetry={fetchResources} />
        ) : showEmptyState ? (
          <EmptyState
            title={RES_EMPTY_TITLE}
            description={RES_EMPTY_DESC}
            action={
              <Link className="btn btn--primary" to="/import">
                <IconUpload />
                {BTN_IMPORT_DATA}
              </Link>
            }
          />
        ) : (
          <>
            <Table
              columns={columns}
              rows={data?.items ?? []}
              rowKey={(row) => row.id}
              loading={loading}
              onRowClick={(row) => setDrawerId(row.id)}
              ariaLabel={RES_TABLE_LABEL}
            />
            {data !== null && data.total > 0 && (
              <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />
            )}
          </>
        )}
      </div>

      {/* key={drawerId}（#109-D）：切资源时整抽屉重挂载——组件态（页签/二跳
          展开/行内错误）不残留前一资源，与 ResourceDrawer.load 内的 seq 守卫
          双保险（key 保证实例隔离，守卫保证响应不串台）。 */}
      {drawerId !== null && <ResourceDrawer key={drawerId} resourceId={drawerId} onClose={closeDrawer} />}
    </>
  );
}
