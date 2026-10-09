/**
 * 检查结果标签页：
 * - 运行历史下拉（默认最新）+ 最新运行头部（严重级别 chips + 评分 + 触发方式/时间）；
 * - 结果表（findings 在前）：严重级别徽章、检查项 mono 徽章、消息、行点击展开详情 JSON；
 * - 「仅显示问题」过滤；[运行校验] 由父级触发。
 */

import { useCallback, useEffect, useState } from 'react';
import type { CheckResultRow, CheckRunDetail, CheckRunItem } from '../../api/types';
import { ApiError, filledSeverityCounts, getCheckRun, listChecks } from '../../api/client';
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
} from '../../i18n';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import { ErrorBlock } from '../ErrorBlock';
import { SeverityBadge } from '../SeverityBadge';
import { Spinner } from '../Spinner';

const PAGE_SIZE = 20;

interface ChecksTabProps {
  workspaceId: string;
  /** 父级刷新计数：任何变更后 +1。 */
  version: number;
  onRunChecks: () => void;
  checksBusy: boolean;
}

export function ChecksTab({ workspaceId, version, onRunChecks, checksBusy }: ChecksTabProps) {
  const [runs, setRuns] = useState<CheckRunItem[]>([]);
  const [runId, setRunId] = useState('');
  const [detail, setDetail] = useState<CheckRunDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [onlyFindings, setOnlyFindings] = useState(true);

  const loadRuns = useCallback((): void => {
    setLoading(true);
    setError(null);
    listChecks(workspaceId, 1, PAGE_SIZE)
      .then((paged) => {
        setRuns(paged.items);
        setLoading(false);
        setRunId((prev) => (prev === '' || paged.items.every((item) => item.runId !== prev) ? paged.items[0]?.runId ?? '' : prev));
      })
      .catch((err: unknown) => {
        setError(err instanceof ApiError ? err.message : null);
        setLoading(false);
      });
  }, [workspaceId]);

  useEffect(() => {
    loadRuns();
  }, [loadRuns, version]);

  useEffect(() => {
    if (runId === '') {
      setDetail(null);
      return;
    }
    setDetailLoading(true);
    getCheckRun(workspaceId, runId)
      .then((result) => {
        setDetail(result);
        setDetailLoading(false);
      })
      .catch((err: unknown) => {
        setDetail(null);
        setDetailLoading(false);
        setError(err instanceof ApiError ? err.message : null);
      });
  }, [workspaceId, runId]);

  const currentRun: CheckRunItem | undefined = runs.find((item) => item.runId === runId);

  const columns: TableColumn<CheckResultRow>[] = [
    {
      key: 'severity',
      header: LAB_C_COL_SEV,
      render: (row) => (row.passed ? <span className="chip chip--success">{LAB_C_PASSED}</span> : <SeverityBadge severity={row.severity} />),
      width: '90px',
    },
    {
      key: 'checkCode',
      header: LAB_C_COL_CHECK,
      render: (row) => (
        <span className="chip chip--mono">
          {row.checkCode}
          {SIM_CHECK_LABELS[row.checkCode] !== undefined ? ` ${SIM_CHECK_LABELS[row.checkCode]}` : ''}
        </span>
      ),
    },
    { key: 'message', header: LAB_C_COL_MESSAGE, render: (row) => row.message },
    {
      key: 'entity',
      header: LAB_C_COL_ENTITY,
      render: (row) => (row.entityId === null || row.entityId === undefined ? '' : <span className="mono">{row.entityId}</span>),
    },
  ];

  const rows = (detail?.results ?? []).filter((row) => !onlyFindings || !row.passed);

  return (
    <div>
      {error !== null && <ErrorBlock message={error} onRetry={loadRuns} />}
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
            <span className="muted-note">
              {LAB_C_STARTED_AT}：{formatDateTime(currentRun.startedAt)}
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
