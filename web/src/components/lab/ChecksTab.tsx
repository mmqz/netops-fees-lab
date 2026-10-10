/**
 * 检查结果标签页：
 * - 运行历史下拉（默认最新）+ 最新运行头部（严重级别 chips + 评分 + 触发方式/时间）；
 * - 结果表（findings 在前）：严重级别徽章、检查项 mono 徽章、消息、行点击展开详情 JSON；
 * - 「仅显示问题」过滤；[运行校验] 由父级触发。
 */

import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import type { CheckResultRow, CheckRunDetail, CheckRunItem, SimCheckCode } from '../../api/types';
import { filledSeverityCounts, formatApiError, getCheckRun, listChecks } from '../../api/client';
import {
  BTN_PROCESSING,
  LAB_BTN_RUN_CHECKS,
  LAB_C_COL_CHECK,
  LAB_C_COL_ENTITY,
  LAB_C_COL_MESSAGE,
  LAB_C_COL_SEV,
  LAB_C_DETAIL,
  LAB_C_EMPTY_DETAIL,
  LAB_C_HISTORY,
  LAB_CAPPED_HINT,
  LAB_C_NO_FINDINGS,
  LAB_C_NO_RUNS,
  LAB_C_ONLY_FINDINGS,
  LAB_C_PASSED,
  LAB_C_RUN_OPTION,
  LAB_C_STARTED_AT,
  LAB_C_TABLE_LABEL,
  LAB_C_TRIGGER,
  LAB_SCORE_VALUE,
  OV_ISSUE_ERR,
  OV_ISSUE_INFO,
  OV_ISSUE_WARN,
  SIM_CHECK_LABELS,
  CHECK_TRIGGER_LABELS,
  format,
  formatDateTime,
  formatRelativeTime,
} from '../../i18n';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import { CHECK_CATALOG, translateCheckMessage } from './checkCatalog';
import { ErrorBlock } from '../ErrorBlock';
import { SeverityBadge } from '../SeverityBadge';
import { Spinner } from '../Spinner';
import { useSeq } from '../../hooks/useSeq';

const PAGE_SIZE = 20;

/** 级别排序序（error 最重 → passed 最轻）；DiffDialog 复用。 */
export const SEVERITY_RANK: Record<string, number> = { error: 0, warning: 1, info: 2 };

/** 检查项列渲染（W3）：mono 编码 chip + title 提示检查项中文解释；DiffDialog 复用。 */
export function CheckCodeChip({ code }: { code: SimCheckCode }): ReactNode {
  const entry = CHECK_CATALOG[code];
  return (
    <span className="chip chip--mono" title={entry === undefined ? undefined : entry.hint}>
      {code}
      {entry === undefined ? (SIM_CHECK_LABELS[code] !== undefined ? ` ${SIM_CHECK_LABELS[code]}` : '') : ` ${entry.title}`}
    </span>
  );
}

/** 消息列（W3）：中文主行 + 英文原文次行（引擎消息确定性解析，未命中回退仅英文）；DiffDialog 复用。 */
export function CheckMessage({ row }: { row: { checkCode: SimCheckCode; message: string } }): ReactNode {
  const zh = translateCheckMessage(row.checkCode, row.message);
  if (zh === null) {
    return <span>{row.message}</span>;
  }
  return (
    <span>
      <span className="cell-strong">{zh}</span>
      <span className="muted-note" style={{ display: 'block', marginTop: 1 }} title={row.message}>
        {row.message}
      </span>
    </span>
  );
}

interface ChecksTabProps {
  workspaceId: string;
  /** 父级刷新计数：任何变更后 +1。 */
  version: number;
  onRunChecks: () => void;
  checksBusy: boolean;
}

export function ChecksTab({ workspaceId, version, onRunChecks, checksBusy }: ChecksTabProps) {
  const [runs, setRuns] = useState<CheckRunItem[]>([]);
  const [runsCapped, setRunsCapped] = useState(false);
  const [runId, setRunId] = useState('');
  const [detail, setDetail] = useState<CheckRunDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [detailErrorText, setDetailErrorText] = useState<string | null>(null);
  const [detailVersion, setDetailVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [onlyFindings, setOnlyFindings] = useState(true);
  /** 取数守卫（AUD2-8）：运行列表与运行详情各自独立计数，下拉快速切换时丢弃旧详情。 */
  const runsGuard = useSeq();
  const detailGuard = useSeq();

  const loadRuns = useCallback((): void => {
    const isCurrent = runsGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listChecks(workspaceId, 1, PAGE_SIZE)
      .then((paged) => {
        if (!isCurrent()) return;
        setRuns(paged.items);
        setRunsCapped(paged.total > paged.items.length);
        setLoading(false);
        setRunId((prev) => (prev === '' || paged.items.every((item) => item.runId !== prev) ? paged.items[0]?.runId ?? '' : prev));
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [workspaceId, runsGuard]);

  useEffect(() => {
    loadRuns();
  }, [loadRuns, version]);

  useEffect(() => {
    if (runId === '') {
      setDetail(null);
      setDetailError(null);
      setDetailErrorText(null);
      return;
    }
    const isCurrent = detailGuard.begin();
    setDetailLoading(true);
    setDetailError(null);
    setDetailErrorText(null);
    getCheckRun(workspaceId, runId)
      .then((result) => {
        if (!isCurrent()) return;
        setDetail(result);
        setDetailLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        setDetail(null);
        setDetailLoading(false);
        // AUD2-14：详情加载失败时记录错误态，表格不再以「无问题」空态误导
        const display = formatApiError(err);
        setDetailError(display.title);
        setDetailErrorText(display.detail);
      });
  }, [workspaceId, runId, detailVersion, detailGuard]);

  const currentRun: CheckRunItem | undefined = runs.find((item) => item.runId === runId);

  const columns: TableColumn<CheckResultRow>[] = [
    {
      key: 'severity',
      header: LAB_C_COL_SEV,
      render: (row) => (row.passed ? <span className="chip chip--success">{LAB_C_PASSED}</span> : <SeverityBadge severity={row.severity} />),
      width: '90px',
      sortValue: (row) => (row.passed ? 3 : (SEVERITY_RANK[row.severity] ?? 9)),
    },
    {
      key: 'checkCode',
      header: LAB_C_COL_CHECK,
      render: (row) => <CheckCodeChip code={row.checkCode} />,
      sortValue: (row) => row.checkCode,
    },
    { key: 'message', header: LAB_C_COL_MESSAGE, render: (row) => <CheckMessage row={row} /> },
    {
      key: 'entity',
      header: LAB_C_COL_ENTITY,
      render: (row) => (row.entityId === null || row.entityId === undefined ? '' : <span className="code-chip" title={row.entityId}>{row.entityId}</span>),
      sortValue: (row) => row.entityId ?? '',
    },
  ];

  const rows = (detail?.results ?? []).filter((row) => !onlyFindings || !row.passed);

  return (
    <div>
      {error !== null && <ErrorBlock message={error} detail={errorDetail} onRetry={loadRuns} />}
      <div className="lab-checks-head">
        <label className="filter-field">
          <span className="filter-label">{LAB_C_HISTORY}</span>
          <select
            className="select"
            value={runId}
            disabled={loading || runs.length === 0}
            onChange={(event) => setRunId(event.target.value)}
          >
            {runs.map((item) => (
              <option key={item.runId} value={item.runId}>
                {format(LAB_C_RUN_OPTION, {
                  runId: item.runId,
                  time: formatDateTime(item.startedAt),
                  score: item.score,
                })}
              </option>
            ))}
          </select>
          {runsCapped && (
            <span className="muted-note">{format(LAB_CAPPED_HINT, { n: PAGE_SIZE })}</span>
          )}
        </label>
        {currentRun !== undefined && (
          <>
            <span className="chip chip--error">
              {OV_ISSUE_ERR} {filledSeverityCounts(currentRun.counts).error}
            </span>
            <span className="chip chip--warning">
              {OV_ISSUE_WARN} {filledSeverityCounts(currentRun.counts).warning}
            </span>
            <span className="chip chip--info">
              {OV_ISSUE_INFO} {filledSeverityCounts(currentRun.counts).info}
            </span>
            <span className="chip chip--primary">{format(LAB_SCORE_VALUE, { score: currentRun.score })}</span>
            <span className="muted-note">
              {LAB_C_TRIGGER}：{formatTrigger(currentRun.trigger)}
            </span>
            <span className="muted-note" title={formatDateTime(currentRun.startedAt)}>
              {LAB_C_STARTED_AT}：{formatRelativeTime(currentRun.startedAt)}
            </span>
          </>
        )}
        <label className="switch">
          <input
            type="checkbox"
            checked={onlyFindings}
            onChange={(event) => setOnlyFindings(event.target.checked)}
          />
          {LAB_C_ONLY_FINDINGS}
        </label>
        <button type="button" className="btn btn--secondary btn--sm" disabled={checksBusy} onClick={onRunChecks}>
          {checksBusy ? <Spinner size={14} /> : null}
          {checksBusy ? BTN_PROCESSING : LAB_BTN_RUN_CHECKS}
        </button>
      </div>

      {loading ? (
        <div className="loading-panel">
          <Spinner />
        </div>
      ) : runs.length === 0 ? (
        <div className="muted-note" style={{ padding: '24px 12px', textAlign: 'center' }}>
          {LAB_C_NO_RUNS}
        </div>
      ) : detailLoading ? (
        <div className="loading-panel">
          <Spinner />
        </div>
      ) : detailError !== null ? (
        // AUD2-14：详情加载失败 → 错误块（可重试），不渲染「无问题」空态表格
        <ErrorBlock
          message={detailError}
          detail={detailErrorText}
          onRetry={() => setDetailVersion((v) => v + 1)}
        />
      ) : (
        <div className="table-wrap">
          <Table
            columns={columns}
            rows={rows}
            rowKey={(row) => row.seq}
            emptyText={LAB_C_NO_FINDINGS}
            renderExpanded={(row) => (
              <div>
                <div className="evidence-title">{LAB_C_DETAIL}</div>
                <pre className="lab-json">
                  {row.detail === null || row.detail === undefined || Object.keys(row.detail).length === 0
                    ? LAB_C_EMPTY_DETAIL
                    : JSON.stringify(row.detail, null, 2)}
                </pre>
              </div>
            )}
            ariaLabel={LAB_C_TABLE_LABEL}
          />
        </div>
      )}
    </div>
  );
}

function formatTrigger(trigger: string): string {
  const labels: Record<string, string> = {
    manual: CHECK_TRIGGER_LABELS.manual,
    api: CHECK_TRIGGER_LABELS.api,
    mcp: CHECK_TRIGGER_LABELS.mcp,
    auto_after_ops: CHECK_TRIGGER_LABELS.auto_after_ops,
  };
  return labels[trigger] ?? trigger;
}
