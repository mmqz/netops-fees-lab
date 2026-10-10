/**
 * 变更集标签页：
 * - 倒序列表（id mono / 描述 / 触发中文 / 状态 chips / 操作数 / 创建·撤销时间）；
 * - [撤销] 仅最新已应用变更集可用（其余禁用 + tooltip 提示）；
 * - 撤销确认 → 父级 onUndo（toast 含新变更集 id + 刷新）。
 */

import { useCallback, useEffect, useState } from 'react';
import type { ChangeSetItem, WorkspaceDetail } from '../../api/types';
import { formatApiError, listChangeSets } from '../../api/client';
import {
  CS_STATUS_LABELS,
  CS_TRIGGER_LABELS,
  LAB_COL_ACTIONS,
  LAB_CS_BTN_UNDO,
  LAB_CS_COL_CREATED,
  LAB_CS_COL_DESC,
  LAB_CS_COL_ID,
  LAB_CS_COL_OPS,
  LAB_CS_COL_REVERTED,
  LAB_CS_COL_STATUS,
  LAB_CS_COL_TRIGGER,
  LAB_CS_EMPTY,
  LAB_CS_ONLY_LATEST_HINT,
  LAB_CS_TABLE_LABEL,
  LAB_CS_UNDO_MESSAGE,
  LAB_CS_UNDO_TITLE,
  NO_VALUE,
  format,
  formatDateTime,
  formatRelativeTime,
} from '../../i18n';
import { Table, numericId } from '../Table';
import type { TableColumn } from '../Table';
import { Pagination } from '../Pagination';
import { ErrorBlock } from '../ErrorBlock';
import { Spinner } from '../Spinner';
import { ConfirmDialog } from '../ConfirmDialog';
import { useSeq } from '../../hooks/useSeq';

const PAGE_SIZE = 20;

const STATUS_CHIP: Record<ChangeSetItem['status'], string> = {
  applied: 'chip--success',
  reverted: 'chip--muted',
  failed: 'chip--error',
};

interface ChangeSetsTabProps {
  workspaceId: string;
  /** 父级刷新计数：任何变更后 +1。 */
  version: number;
  /** 工作台级「全局最新变更集」（来自详情接口，不受分页影响——AUD2-9）。 */
  latestChangeset: WorkspaceDetail['latestChangeset'];
  onUndo: (changesetId?: string) => void;
  undoBusy: boolean;
  /** W9：toast「查看变更集」定位后要闪亮的行 id。 */
  highlightId?: string;
}

export function ChangeSetsTab({ workspaceId, version, latestChangeset, onUndo, undoBusy, highlightId }: ChangeSetsTabProps) {
  const [items, setItems] = useState<ChangeSetItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [confirmTarget, setConfirmTarget] = useState<ChangeSetItem | null>(null);
  /** 取数守卫（AUD2-8）：翻页/版本刷新重叠时丢弃旧响应。 */
  const loadGuard = useSeq();

  const load = useCallback((): void => {
    const isCurrent = loadGuard.begin();
    setLoading(true);
    setError(null);
    setErrorDetail(null);
    listChangeSets(workspaceId, page, PAGE_SIZE)
      .then((paged) => {
        if (!isCurrent()) return;
        setItems(paged.items);
        setTotal(paged.total);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        const display = formatApiError(err);
        setError(display.title);
        setErrorDetail(display.detail);
        setLoading(false);
      });
  }, [workspaceId, page, loadGuard]);

  useEffect(() => {
    load();
  }, [load, version]);

  /*
   * 全局最新「已应用」变更集（AUD2-9）：
   * - 详情接口的 latestChangeset 为全局最新且状态 applied 时，它就是可撤销目标（任意分页皆准）；
   * - 最新一条是 failed（不影响 LIFO）时，退回第 1 页扫描（倒序首条 applied 即全局最新）；
   * - 其余分页拿不到全局信息 → 不启用行内撤销（页头「撤销最近变更」仍可用）。
   */
  const latestAppliedId: string | undefined =
    latestChangeset !== null && latestChangeset.status === 'applied'
      ? latestChangeset.id
      : page === 1
        ? items.find((item) => item.status === 'applied')?.id
        : undefined;

  function confirmUndo(): void {
    const target = confirmTarget;
    if (target === null) return;
    setConfirmTarget(null);
    onUndo(target.id);
  }

  const columns: TableColumn<ChangeSetItem>[] = [
    {
      key: 'id',
      header: LAB_CS_COL_ID,
      render: (row) => <span className="mono cell-strong">{row.id}</span>,
      sortValue: (row) => numericId(row.id),
    },
    {
      key: 'description',
      header: LAB_CS_COL_DESC,
      render: (row) => (row.description === '' ? <span className="muted-note">{NO_VALUE}</span> : row.description),
      sortValue: (row) => row.description,
    },
    {
      key: 'trigger',
      header: LAB_CS_COL_TRIGGER,
      render: (row) => CS_TRIGGER_LABELS[row.trigger] ?? row.trigger,
      sortValue: (row) => CS_TRIGGER_LABELS[row.trigger] ?? row.trigger,
    },
    {
      key: 'status',
      header: LAB_CS_COL_STATUS,
      render: (row) => <span className={`chip ${STATUS_CHIP[row.status]}`}>{CS_STATUS_LABELS[row.status]}</span>,
      sortValue: (row) => CS_STATUS_LABELS[row.status],
    },
    {
      key: 'opsApplied',
      header: LAB_CS_COL_OPS,
      render: (row) => row.opsApplied,
      align: 'right',
      sortValue: (row) => row.opsApplied,
    },
    {
      key: 'createdAt',
      header: LAB_CS_COL_CREATED,
      render: (row) => (
        <span title={formatDateTime(row.createdAt)}>{formatRelativeTime(row.createdAt)}</span>
      ),
      sortValue: (row) => Date.parse(row.createdAt),
    },
    {
      key: 'revertedAt',
      header: LAB_CS_COL_REVERTED,
      render: (row) =>
        row.revertedAt === null || row.revertedAt === undefined ? (
          <span className="muted-note">{NO_VALUE}</span>
        ) : (
          <span title={formatDateTime(row.revertedAt)}>{formatRelativeTime(row.revertedAt)}</span>
        ),
      sortValue: (row) => Date.parse(row.revertedAt ?? ''),
    },
    {
      key: 'actions',
      header: LAB_COL_ACTIONS,
      render: (row) => (
        <button
          type="button"
          className="btn btn--secondary btn--sm"
          disabled={undoBusy || row.status !== 'applied' || row.id !== latestAppliedId}
          title={row.id === latestAppliedId ? undefined : LAB_CS_ONLY_LATEST_HINT}
          onClick={() => setConfirmTarget(row)}
        >
          {undoBusy && confirmTarget?.id === row.id ? <Spinner size={12} /> : null}
          {LAB_CS_BTN_UNDO}
        </button>
      ),
    },
  ];

  return (
    <div>
      {error !== null && <ErrorBlock message={error} detail={errorDetail} onRetry={load} />}
      <div className="table-wrap">
        <Table
          columns={columns}
          rows={items}
          rowKey={(row) => row.id}
          rowClassName={(row) => (highlightId !== undefined && row.id === highlightId ? 'lab-row-highlight' : undefined)}
          loading={loading}
          emptyText={LAB_CS_EMPTY}
          ariaLabel={LAB_CS_TABLE_LABEL}
        />
      </div>
      {!loading && total > PAGE_SIZE && (
        <Pagination page={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} />
      )}
      <ConfirmDialog
        open={confirmTarget !== null}
        title={LAB_CS_UNDO_TITLE}
        message={format(LAB_CS_UNDO_MESSAGE, { id: confirmTarget?.id ?? '' })}
        confirmLabel={LAB_CS_BTN_UNDO}
        onConfirm={confirmUndo}
        onCancel={() => setConfirmTarget(null)}
      />
    </div>
  );
}
