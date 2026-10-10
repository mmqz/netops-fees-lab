/**
 * 概览页 /：统计卡片、类型分布条形图、最近批次表、快捷操作、批次回滚。
 */

import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { ReactNode } from 'react';
import type { BatchListItem, IssueCounts, Paged, StatsResponse } from '../api/types';
import { ApiError, deleteBatch, filledIssueCounts, formatApiError, getStats, listBatches, runValidate } from '../api/client';
import { useSeq } from '../hooks/useSeq';
import {
  BTN_IMPORT_DATA,
  BTN_ROLLBACK,
  BTN_VIEW_ISSUES,
  BTN_VIEW_REPORT,
  format,
  formatDateTime,
  NO_VALUE,
  OV_BY_TYPE_TITLE,
  OV_COL_ACTIONS,
  OV_COL_FAILED,
  OV_COL_IMPORTED,
  OV_COL_ROWS,
  OV_COL_SKIPPED,
  OV_COL_STARTED_AT,
  OV_COL_STATUS,
  OV_COL_WORKBOOK,
  OV_EMPTY_DESC,
  OV_EMPTY_TITLE,
  OV_ISSUE_ERR,
  OV_ISSUE_INFO,
  OV_ISSUE_WARN,
  OV_NO_BATCHES,
  OV_QUICK_ACTIONS,
  OV_RECENT_BATCHES,
  OV_RECENT_BATCHES_MORE,
  OV_ROLLBACK_MESSAGE,
  OV_ROLLBACK_TITLE,
  OV_STAT_LATEST_BATCH,
  OV_STAT_QUALITY_SCORE,
  OV_STAT_SCORE_FULL,
  OV_STAT_TOTAL_ISSUES,
  OV_STAT_TOTAL_RESOURCES,
  resourceTypeLabel,
  TOAST_BATCH_IN_USE,
  TOAST_ROLLBACK_DONE,
} from '../i18n';
import { Table } from '../components/Table';
import type { TableColumn } from '../components/Table';
import { BatchStatusChip } from '../components/BatchStatusChip';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { EmptyState } from '../components/EmptyState';
import { ErrorBlock } from '../components/ErrorBlock';
import { LoadingPanel } from '../components/Spinner';
import { useToast } from '../components/Toast';
import { IconDoc, IconUpload } from '../components/icons';

const RECENT_BATCH_LIMIT = 5;

interface OverviewData {
  stats: StatsResponse;
  counts: IssueCounts;
  score: number | null;
  batches: Paged<BatchListItem>;
}

function StatCard({ label, value, sub }: { label: string; value: ReactNode; sub?: ReactNode }) {
  return (
    <div className="card stat-card">
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      {sub !== undefined && <div className="stat-sub">{sub}</div>}
    </div>
  );
}

function TypeBar({ label, count, max }: { label: string; count: number; max: number }) {
  const width = max > 0 ? Math.round((count / max) * 100) : 0;
  return (
    <div className="bar-row">
      <span className="bar-label">{label}</span>
      <span className="bar-track" aria-hidden="true">
        <span className="bar-fill" style={{ width: `${width}%` }} />
      </span>
      <span className="bar-value">{count}</span>
    </div>
  );
}

/** 类型分布行（按 count 降序；键集来自后端 stats.byType 动态 map，含 port）。 */
interface TypeStatRow {
  key: string;
  label: string;
  count: number;
}

function typeStatRows(byType: Record<string, number>): TypeStatRow[] {
  return Object.entries(byType)
    .map(([key, count]) => ({ key, label: resourceTypeLabel(key), count }))
    .sort((a, b) => b.count - a.count);
}

function OverviewBody() {
  const navigate = useNavigate();
  const toast = useToast();
  const loadGuard = useSeq();
  const [data, setData] = useState<OverviewData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [rollbackTarget, setRollbackTarget] = useState<BatchListItem | null>(null);
  const [rollbackBusy, setRollbackBusy] = useState(false);

  const load = useCallback((): void => {
    const isCurrent = loadGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    Promise.all([getStats(), runValidate().catch(() => null), listBatches(1, RECENT_BATCH_LIMIT)])
      .then((results) => {
        if (!isCurrent()) return;
        const [stats, validateResult, batches] = results;
        setData({
          stats,
          counts: filledIssueCounts(stats.issuesBySeverity),
          score: validateResult === null ? null : validateResult.score,
          batches,
        });
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [loadGuard]);

  useEffect(() => {
    load();
  }, [load]);

  function confirmRollback(): void {
    const target = rollbackTarget;
    if (target === null) return;
    setRollbackBusy(true);
    deleteBatch(target.id)
      .then(() => {
        setRollbackBusy(false);
        setRollbackTarget(null);
        toast.show(TOAST_ROLLBACK_DONE, 'success');
        load();
      })
      .catch((err: unknown) => {
        setRollbackBusy(false);
        setRollbackTarget(null);
        if (err instanceof ApiError && err.code === 'batch_in_use') {
          toast.show(TOAST_BATCH_IN_USE, 'error');
        } else {
          const display = formatApiError(err);
          toast.show(display.title, 'error', { detail: display.detail });
        }
      });
  }

  if (loading) {
    return <LoadingPanel />;
  }

  if (error !== null || data === null) {
    return <ErrorBlock message={error} detail={errorDetail} onRetry={load} />;
  }

  const { stats, counts, score, batches } = data;
  const isEmpty = stats.totalResources === 0 && batches.total === 0;

  if (isEmpty) {
    return (
      <div className="card">
        <EmptyState
          title={OV_EMPTY_TITLE}
          description={OV_EMPTY_DESC}
          icon={<IconDoc size={40} />}
          action={
            <button type="button" className="btn btn--primary" onClick={() => navigate('/import')}>
              <IconUpload />
              {BTN_IMPORT_DATA}
            </button>
          }
        />
      </div>
    );
  }

  // 类型分布动态化（R1-D4）：按后端 stats.byType 实际键渲染（含 port），不硬编码类型清单
  const typeStats = typeStatRows(stats.byType);
  const maxTypeCount = typeStats.reduce((max, row) => Math.max(max, row.count), 1);
  const typeSummary = typeStats.map((row) => `${row.count} ${row.label}`).join(' · ');
  const totalIssues = counts.error + counts.warning + counts.info;
  const latestBatch: BatchListItem | undefined = batches.items[0];

  const columns: TableColumn<BatchListItem>[] = [
    { key: 'workbook', header: OV_COL_WORKBOOK, render: (row) => <span className="cell-strong">{row.workbookName}</span> },
    { key: 'status', header: OV_COL_STATUS, render: (row) => <BatchStatusChip status={row.status} /> },
    { key: 'rowsTotal', header: OV_COL_ROWS, render: (row) => row.rowsTotal, align: 'right' },
    { key: 'rowsImported', header: OV_COL_IMPORTED, render: (row) => row.rowsImported, align: 'right' },
    { key: 'rowsSkipped', header: OV_COL_SKIPPED, render: (row) => row.rowsSkipped, align: 'right' },
    { key: 'rowsFailed', header: OV_COL_FAILED, render: (row) => row.rowsFailed, align: 'right' },
    { key: 'startedAt', header: OV_COL_STARTED_AT, render: (row) => formatDateTime(row.startedAt) },
    {
      key: 'actions',
      header: OV_COL_ACTIONS,
      render: (row) =>
        row.status === 'completed' ? (
          <button
            type="button"
            className="btn btn--danger-ghost btn--sm"
            onClick={() => setRollbackTarget(row)}
          >
            {BTN_ROLLBACK}
          </button>
        ) : (
          NO_VALUE
        ),
    },
  ];

  return (
    <>
      <section className="stat-grid">
        <StatCard
          label={OV_STAT_TOTAL_RESOURCES}
          value={stats.totalResources}
          sub={typeSummary === '' ? undefined : typeSummary}
        />
        <StatCard
          label={OV_STAT_TOTAL_ISSUES}
          value={totalIssues}
          sub={`${OV_ISSUE_ERR} ${counts.error} · ${OV_ISSUE_WARN} ${counts.warning} · ${OV_ISSUE_INFO} ${counts.info}`}
        />
        <StatCard label={OV_STAT_QUALITY_SCORE} value={score ?? NO_VALUE} sub={OV_STAT_SCORE_FULL} />
        <StatCard
          label={OV_STAT_LATEST_BATCH}
          value={latestBatch === undefined ? NO_VALUE : latestBatch.workbookName}
          sub={
            latestBatch === undefined ? undefined : (
              <>
                <BatchStatusChip status={latestBatch.status} />
                <span className="stat-sub-text">{formatDateTime(latestBatch.startedAt)}</span>
              </>
            )
          }
        />
      </section>

      <div className="ov-grid">
        <div className="card">
          <h2 className="card-title">{OV_BY_TYPE_TITLE}</h2>
          {typeStats.map((row) => (
            <TypeBar key={row.key} label={row.label} count={row.count} max={maxTypeCount} />
          ))}
        </div>
        <div className="card">
          <h2 className="card-title">{OV_QUICK_ACTIONS}</h2>
          <div className="quick-actions">
            <button type="button" className="btn btn--primary" onClick={() => navigate('/import')}>
              <IconUpload />
              {BTN_IMPORT_DATA}
            </button>
            <button type="button" className="btn btn--secondary" onClick={() => navigate('/issues')}>
              {BTN_VIEW_ISSUES}
            </button>
            <button type="button" className="btn btn--secondary" onClick={() => navigate('/report')}>
              {BTN_VIEW_REPORT}
            </button>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <h2 className="card-title">{OV_RECENT_BATCHES}</h2>
          <span className="card-head-note">{OV_RECENT_BATCHES_MORE}</span>
        </div>
        {batches.items.length === 0 ? (
          <div className="muted-note">{OV_NO_BATCHES}</div>
        ) : (
          <Table
            columns={columns}
            rows={batches.items}
            rowKey={(row) => row.id}
            ariaLabel={OV_RECENT_BATCHES}
          />
        )}
      </div>

      <ConfirmDialog
        open={rollbackTarget !== null}
        title={OV_ROLLBACK_TITLE}
        message={format(OV_ROLLBACK_MESSAGE, {
          workbook: rollbackTarget === null ? '' : rollbackTarget.workbookName,
        })}
        confirmLabel={BTN_ROLLBACK}
        danger
        busy={rollbackBusy}
        onConfirm={confirmRollback}
        onCancel={() => setRollbackTarget(null)}
      />
    </>
  );
}

export function OverviewPage() {
  return <OverviewBody />;
}
