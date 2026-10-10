/**
 * 前后对比对话框：
 * - 两个运行下拉（默认：最近两次运行，旧→新）；
 * - W8：默认两次运行就绪后自动生成对比（免掉演示路径上的一次点击，
 *   手动改选后仍可点「生成对比」重跑）；
 * - GET /checks/{runId}/diff?compareTo= → 评分变化徽章、新增问题（红底）、
 *   消失问题（绿底）、连通性丢失/恢复 pair chips（默认收起为「共 N 对」
 *   chip，点击展开，W8），各分区独立空态；
 * - W3：检查项/消息列中文主行 + 英文原文次行。
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { CheckRunItem, DiffResult } from '../../api/types';
import { ApiError, diffRuns, formatApiErrorText, listChecks } from '../../api/client';
import {
  LAB_CAPPED_HINT,
  LAB_DIFF_BTN_RUN,
  LAB_DIFF_ADDED,
  LAB_DIFF_COL_CHECK,
  LAB_DIFF_COL_MESSAGE,
  LAB_DIFF_COL_SEV,
  LAB_DIFF_CONN_GAINED,
  LAB_DIFF_CONN_LOST,
  LAB_DIFF_EMPTY_ADDED,
  LAB_DIFF_EMPTY_GAINED,
  LAB_DIFF_EMPTY_LOST,
  LAB_DIFF_EMPTY_REMOVED,
  LAB_DIFF_ERR_REQUIRED,
  LAB_DIFF_FROM,
  LAB_DIFF_NOT_ENOUGH,
  LAB_DIFF_PAIR,
  LAB_DIFF_PAIRS_COLLAPSE,
  LAB_DIFF_PAIRS_EXPAND,
  LAB_DIFF_REMOVED,
  LAB_DIFF_RUN_OPTION,
  LAB_DIFF_SAME_RUN,
  LAB_DIFF_SCORE,
  LAB_DIFF_TITLE,
  LAB_DIFF_TO,
  LOADING_TEXT,
  format,
  formatDateTime,
} from '../../i18n';
import { LabDialog } from './LabDialog';
import { useSeq } from '../../hooks/useSeq';
import { SeverityBadge } from '../SeverityBadge';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import { CheckCodeChip, CheckMessage, SEVERITY_RANK } from './ChecksTab';
import type { DiffFinding, DiffPair } from '../../api/types';

const RUNS_PAGE_SIZE = 50;

interface DiffDialogProps {
  open: boolean;
  workspaceId: string;
  onClose: () => void;
  onError: (err: unknown) => void;
}

function runOptionLabel(run: CheckRunItem): string {
  return format(LAB_DIFF_RUN_OPTION, {
    runId: run.runId,
    time: formatDateTime(run.startedAt),
    score: run.score,
  });
}

interface KeyedFinding extends DiffFinding {
  key: string;
}

function withKeys(findings: DiffFinding[]): KeyedFinding[] {
  return findings.map((finding, index) => ({ ...finding, key: `${finding.checkCode}-${index}` }));
}

const findingColumns: TableColumn<DiffFinding>[] = [
  {
    key: 'checkCode',
    header: LAB_DIFF_COL_CHECK,
    render: (row) => <CheckCodeChip code={row.checkCode} />,
    sortValue: (row) => row.checkCode,
  },
  {
    key: 'severity',
    header: LAB_DIFF_COL_SEV,
    render: (row) => <SeverityBadge severity={row.severity} />,
    sortValue: (row) => SEVERITY_RANK[row.severity] ?? 9,
  },
  { key: 'message', header: LAB_DIFF_COL_MESSAGE, render: (row) => <CheckMessage row={row} /> },
];

/** 连通性对列表（W8）：默认收起为「共 N 对」chip，点击展开完整列表。 */
function PairChips({ pairs, variant }: { pairs: DiffPair[]; variant: 'lost' | 'gained' }) {
  const [expanded, setExpanded] = useState(false);
  const chipClass = variant === 'lost' ? 'chip--error' : 'chip--success';
  if (!expanded) {
    return (
      <button
        type="button"
        className={`chip ${chipClass} lab-pair-toggle`}
        aria-expanded={false}
        onClick={() => setExpanded(true)}
      >
        {format(LAB_DIFF_PAIRS_EXPAND, { n: pairs.length })}
      </button>
    );
  }
  return (
    <div>
      <div className="lab-pair-chips">
        {pairs.map((pair, index) => (
          <span className={`chip ${chipClass}`} key={index}>
            {format(LAB_DIFF_PAIR, { from: pair.fromDevice, to: pair.toDevice })}
          </span>
        ))}
      </div>
      <button
        type="button"
        className="lab-pair-collapse"
        aria-expanded={true}
        onClick={() => setExpanded(false)}
      >
        {LAB_DIFF_PAIRS_COLLAPSE}
      </button>
    </div>
  );
}

function ScoreDelta({ delta }: { delta: number }) {
  const sign = delta > 0 ? '+' : delta < 0 ? '−' : '±';
  const variant = delta > 0 ? 'up' : delta < 0 ? 'down' : 'flat';
  return (
    <span className={`lab-score-delta lab-score-delta--${variant}`}>
      {sign}
      {Math.abs(delta)}
    </span>
  );
}

export function DiffDialog({ open, workspaceId, onClose, onError }: DiffDialogProps) {
  const [runs, setRuns] = useState<CheckRunItem[]>([]);
  const [runsLoading, setRunsLoading] = useState(true);
  const [runsCapped, setRunsCapped] = useState(false);
  const [fromId, setFromId] = useState('');
  const [toId, setToId] = useState('');
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  /** W8：默认两次运行就绪后自动生成一次（每次打开重置；手动改选后仍可点按钮重跑）。 */
  const autoRanRef = useRef(false);
  /** 取数守卫（AUD2-8）：快速开关对话框时丢弃旧响应。 */
  const runsGuard = useSeq();
  const loadRuns = useCallback((): void => {
    const isCurrent = runsGuard.begin();
    setRunsLoading(true);
    listChecks(workspaceId, 1, RUNS_PAGE_SIZE)
      .then((paged) => {
        if (!isCurrent()) return;
        setRuns(paged.items);
        setRunsCapped(paged.total > paged.items.length);
        setRunsLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        setRunsLoading(false);
        setError(err instanceof ApiError ? formatApiErrorText(err) : String(err));
      });
  }, [workspaceId, runsGuard]);

  // 打开时拉取运行历史，默认选中最近两次（旧 → 新）
  useEffect(() => {
    if (!open) return;
    setDiff(null);
    setError(null);
    setFromId('');
    setToId('');
    autoRanRef.current = false;
    loadRuns();
  }, [open, loadRuns]);

  const runDiff = useCallback(
    (from: string, to: string): void => {
      if (from === '' || to === '') {
        setError(LAB_DIFF_ERR_REQUIRED);
        return;
      }
      if (from === to) {
        setError(LAB_DIFF_SAME_RUN);
        return;
      }
      setBusy(true);
      setError(null);
      diffRuns(workspaceId, from, to)
        .then((result) => {
          setBusy(false);
          setDiff(result);
        })
        .catch((err: unknown) => {
          setBusy(false);
          onError(err);
        });
    },
    [workspaceId, onError],
  );

  // 默认选中最近两次（旧 → 新）；W8：就绪即自动生成一次对比（演示路径少一次点击）
  useEffect(() => {
    if (runs.length < 2) return;
    const defaultFrom = runs[1].runId;
    const defaultTo = runs[0].runId;
    setFromId((prev) => (prev === '' ? defaultFrom : prev));
    setToId((prev) => (prev === '' ? defaultTo : prev));
    if (!autoRanRef.current) {
      autoRanRef.current = true;
      runDiff(defaultFrom, defaultTo);
    }
  }, [runs, runDiff]);

  function submit(): void {
    runDiff(fromId, toId);
  }

  return (
    <LabDialog
      open={open}
      title={LAB_DIFF_TITLE}
      onClose={onClose}
      wide
      busy={busy}
      confirmLabel={LAB_DIFF_BTN_RUN}
      onConfirm={submit}
    >
      {runsLoading ? (
        <div className="muted-note">{LOADING_TEXT}</div>
      ) : runs.length < 2 ? (
        <div className="muted-note">{LAB_DIFF_NOT_ENOUGH}</div>
      ) : (
        <>
          <div className="lab-form-grid">
            <div className="field">
              <label className="field-label" htmlFor="lab-diff-from">
                {LAB_DIFF_FROM}
              </label>
              <select
                id="lab-diff-from"
                className="select select--full"
                value={fromId}
                disabled={busy}
                onChange={(event) => setFromId(event.target.value)}
              >
                {runs.map((run) => (
                  <option key={run.runId} value={run.runId}>
                    {runOptionLabel(run)}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label className="field-label" htmlFor="lab-diff-to">
                {LAB_DIFF_TO}
              </label>
              <select
                id="lab-diff-to"
                className="select select--full"
                value={toId}
                disabled={busy}
                onChange={(event) => setToId(event.target.value)}
              >
                {runs.map((run) => (
                  <option key={run.runId} value={run.runId}>
                    {runOptionLabel(run)}
                  </option>
                ))}
              </select>
            </div>
          </div>
          {runsCapped && (
            <div className="modal-note">{format(LAB_CAPPED_HINT, { n: RUNS_PAGE_SIZE })}</div>
          )}
        </>
      )}

      {error !== null && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}

      {diff !== null && (
        <>
          <div className="lab-diff-section">
            <div className="lab-diff-title">
              {LAB_DIFF_SCORE}
              <ScoreDelta delta={diff.scoreDelta} />
            </div>
          </div>

          <div className="lab-diff-section">
            <div className="lab-diff-title">
              {LAB_DIFF_ADDED}
              <span className="chip chip--error">{diff.findingsAdded.length}</span>
            </div>
            {diff.findingsAdded.length === 0 ? (
              <div className="muted-note">{LAB_DIFF_EMPTY_ADDED}</div>
            ) : (
              <div className="table-wrap table--red-tint">
                <Table
                  columns={findingColumns}
                  rows={withKeys(diff.findingsAdded)}
                  rowKey={(row) => row.key}
                />
              </div>
            )}
          </div>

          <div className="lab-diff-section">
            <div className="lab-diff-title">
              {LAB_DIFF_REMOVED}
              <span className="chip chip--success">{diff.findingsRemoved.length}</span>
            </div>
            {diff.findingsRemoved.length === 0 ? (
              <div className="muted-note">{LAB_DIFF_EMPTY_REMOVED}</div>
            ) : (
              <div className="table-wrap table--green-tint">
                <Table
                  columns={findingColumns}
                  rows={withKeys(diff.findingsRemoved)}
                  rowKey={(row) => row.key}
                />
              </div>
            )}
          </div>

          <div className="lab-diff-section">
            <div className="lab-diff-title">
              {LAB_DIFF_CONN_LOST}
              <span className="chip chip--error">{diff.connectivityLost.length}</span>
            </div>
            {diff.connectivityLost.length === 0 ? (
              <div className="muted-note">{LAB_DIFF_EMPTY_LOST}</div>
            ) : (
              <PairChips pairs={diff.connectivityLost} variant="lost" />
            )}
          </div>

          <div className="lab-diff-section">
            <div className="lab-diff-title">
              {LAB_DIFF_CONN_GAINED}
              <span className="chip chip--success">{diff.connectivityGained.length}</span>
            </div>
            {diff.connectivityGained.length === 0 ? (
              <div className="muted-note">{LAB_DIFF_EMPTY_GAINED}</div>
            ) : (
              <PairChips pairs={diff.connectivityGained} variant="gained" />
            )}
          </div>
        </>
      )}
    </LabDialog>
  );
}
