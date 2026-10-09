/**
 * 前后对比对话框：
 * - 两个运行下拉（默认：最近两次运行，旧→新）；
 * - GET /checks/{runId}/diff?compareTo= → 评分变化徽章、新增问题（红底）、
 *   消失问题（绿底）、连通性丢失/恢复 pair chips，各分区独立空态。
 */

import { useCallback, useEffect, useState } from 'react';
import type { CheckRunItem, DiffResult } from '../../api/types';
import { ApiError, diffRuns, listChecks } from '../../api/client';
import {
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
  LAB_DIFF_REMOVED,
  LAB_DIFF_RUN_OPTION,
  LAB_DIFF_SAME_RUN,
  LAB_DIFF_SCORE,
  LAB_DIFF_TITLE,
  LAB_DIFF_TO,
  LOADING_TEXT,
  SIM_CHECK_LABELS,
  format,
  formatDateTime,
} from '../../i18n';
import { LabDialog } from './LabDialog';
import { SeverityBadge } from '../SeverityBadge';
import { Table } from '../Table';
import type { TableColumn } from '../Table';
import type { DiffFinding } from '../../api/types';

const RUNS_PAGE_SIZE = 50;

interface DiffDialogProps {
  open: boolean;
  workspaceId: string;
  onClose: () => void;
  onError: (message: string) => void;
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
    render: (row) => (
      <span className="chip chip--mono">
        {row.checkCode}
        {SIM_CHECK_LABELS[row.checkCode] !== undefined ? ` ${SIM_CHECK_LABELS[row.checkCode]}` : ''}
      </span>
    ),
  },
  { key: 'severity', header: LAB_DIFF_COL_SEV, render: (row) => <SeverityBadge severity={row.severity} /> },
  { key: 'message', header: LAB_DIFF_COL_MESSAGE, render: (row) => row.message },
];

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
  const [fromId, setFromId] = useState('');
  const [toId, setToId] = useState('');
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const loadRuns = useCallback((): void => {
    setRunsLoading(true);
    listChecks(workspaceId, 1, RUNS_PAGE_SIZE)
      .then((paged) => {
        setRuns(paged.items);
        setRunsLoading(false);
      })
      .catch((err: unknown) => {
        setRunsLoading(false);
        setError(err instanceof ApiError ? err.message : String(err));
      });
  }, [workspaceId]);

  // 打开时拉取运行历史，默认选中最近两次（旧 → 新）
  useEffect(() => {
    if (!open) return;
    setDiff(null);
    setError(null);
    setFromId('');
    setToId('');
    loadRuns();
  }, [open, loadRuns]);

  useEffect(() => {
    if (runs.length >= 2) {
      setFromId((prev) => (prev === '' ? runs[1].runId : prev));
      setToId((prev) => (prev === '' ? runs[0].runId : prev));
    }
  }, [runs]);

  function submit(): void {
    if (fromId === '' || toId === '') {
      setError(LAB_DIFF_ERR_REQUIRED);
      return;
    }
    if (fromId === toId) {
      setError(LAB_DIFF_SAME_RUN);
      return;
    }
    setBusy(true);
    setError(null);
    diffRuns(workspaceId, fromId, toId)
      .then((result) => {
        setBusy(false);
        setDiff(result);
      })
      .catch((err: unknown) => {
        setBusy(false);
        if (err instanceof ApiError) {
          onError(err.message);
        } else {
          setError(String(err));
        }
      });
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
              <div className="lab-pair-chips">
                {diff.connectivityLost.map((pair, index) => (
                  <span className="chip chip--error" key={index}>
                    {format(LAB_DIFF_PAIR, { from: pair.fromDevice, to: pair.toDevice })}
                  </span>
                ))}
              </div>
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
              <div className="lab-pair-chips">
                {diff.connectivityGained.map((pair, index) => (
                  <span className="chip chip--success" key={index}>
                    {format(LAB_DIFF_PAIR, { from: pair.fromDevice, to: pair.toDevice })}
                  </span>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </LabDialog>
  );
}
