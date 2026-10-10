/**
 * 通用数据表格。
 * - 泛型列定义（key / 表头 / 渲染函数 / 列宽 / 对齐）；
 * - 加载骨架行、空态行；
 * - 可选行点击与可展开行（问题清单的证据明细）；
 * - 语义化表头（th scope="col"）；
 * - 客户端列排序（W10 / AUD-4 P1-5）：列提供 sortValue 即表头可点，
 *   升 → 降 → 复位三态，aria-sort + 箭头指示；不动分页/展开行为。
 */

import { Fragment, useMemo, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from 'react';
import { EMPTY_TEXT } from '../i18n';

export interface TableColumn<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  width?: string;
  align?: 'left' | 'right' | 'center';
  /** 提供即该列客户端可排序：返回排序键（数字优先，字符串按本地化比较）。 */
  sortValue?: (row: T) => string | number;
}

interface TableProps<T> {
  columns: TableColumn<T>[];
  rows: T[];
  rowKey: (row: T) => string | number;
  loading?: boolean;
  /** 骨架行数量（默认 6）。 */
  skeletonRows?: number;
  emptyText?: string;
  onRowClick?: (row: T) => void;
  /** 提供时行可点击展开（onRowClick 与 renderExpanded 不应同时使用）。 */
  renderExpanded?: (row: T) => ReactNode;
  /** 行级附加 class（如拓扑联动选中高亮）。 */
  rowClassName?: (row: T) => string | undefined;
  ariaLabel?: string;
}

/** 排序状态：dir 1=升序，-1=降序；null=未排序（原始顺序）。 */
interface SortState {
  key: string;
  dir: 1 | -1;
}

/**
 * 实体 id 的数字后缀（vd-22 / if-9 / ln-4 / cs-3 / run-11 …）：
 * 提取结尾数字用于数值排序，无数字返回 0（稳定排序下保持相对顺序）。
 */
export function numericId(id: string): number {
  const match = /(\d+)$/.exec(id);
  return match === null ? 0 : Number(match[1]);
}

export function Table<T>(props: TableProps<T>) {
  const {
    columns,
    rows,
    rowKey,
    loading = false,
    skeletonRows = 6,
    emptyText = EMPTY_TEXT,
    onRowClick,
    renderExpanded,
    rowClassName,
    ariaLabel,
  } = props;

  const [expandedKeys, setExpandedKeys] = useState<Set<string | number>>(new Set());
  const [sort, setSort] = useState<SortState | null>(null);

  const clickable = onRowClick !== undefined || renderExpanded !== undefined;

  /** 点击表头：升 → 降 → 复位。 */
  function toggleSort(key: string): void {
    setSort((prev) => {
      if (prev === null || prev.key !== key) return { key, dir: 1 };
      if (prev.dir === 1) return { key, dir: -1 };
      return null;
    });
  }

  const sortedRows = useMemo(() => {
    if (sort === null) return rows;
    const col = columns.find((candidate) => candidate.key === sort.key);
    if (col === undefined || col.sortValue === undefined) return rows;
    const factor = sort.dir;
    const valueOf = col.sortValue;
    return [...rows].sort((a, b) => {
      const va = valueOf(a);
      const vb = valueOf(b);
      if (typeof va === 'number' && typeof vb === 'number') return (va - vb) * factor;
      return String(va).localeCompare(String(vb), 'zh-Hans-CN') * factor;
    });
  }, [rows, columns, sort]);

  function toggleExpanded(key: string | number): void {
    setExpandedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }

  function handleRowClick(row: T): void {
    if (renderExpanded !== undefined) {
      toggleExpanded(rowKey(row));
      return;
    }
    if (onRowClick !== undefined) {
      onRowClick(row);
    }
  }

  function handleRowKeyDown(event: ReactKeyboardEvent<HTMLTableRowElement>, row: T): void {
    if (!clickable) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      handleRowClick(row);
    }
  }

  /** 表头单元格：可排序列渲染为按钮（aria-sort + 箭头）。 */
  function renderHeadCell(col: TableColumn<T>): ReactNode {
    const style = { width: col.width, textAlign: col.align } as const;
    if (col.sortValue === undefined) {
      return (
        <th key={col.key} scope="col" style={style}>
          {col.header}
        </th>
      );
    }
    const active = sort !== null && sort.key === col.key;
    const ariaSort = active ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none';
    return (
      <th key={col.key} scope="col" style={style} aria-sort={ariaSort}>
        <button type="button" className="th-sort" onClick={() => toggleSort(col.key)}>
          {col.header}
          <span className="th-sort-indicator" aria-hidden="true">
            {active ? (sort.dir === 1 ? '↑' : '↓') : '⇅'}
          </span>
        </button>
      </th>
    );
  }

  if (loading) {
    return (
      <table className="table" aria-busy="true" aria-label={ariaLabel}>
        <thead>
          <tr>{columns.map(renderHeadCell)}</tr>
        </thead>
        <tbody>
          {Array.from({ length: skeletonRows }, (_, index) => (
            <tr key={`skel-${index}`} className="table-skel-row">
              {columns.map((col) => (
                <td key={col.key}>
                  <span className="skel" />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    );
  }

  return (
    <table className="table" aria-label={ariaLabel}>
      <thead>
        <tr>{columns.map(renderHeadCell)}</tr>
      </thead>
      <tbody>
        {sortedRows.length === 0 && (
          <tr>
            <td colSpan={columns.length} className="table-empty">
              {emptyText}
            </td>
          </tr>
        )}
        {sortedRows.map((row) => {
          const key = rowKey(row);
          const expanded = renderExpanded !== undefined && expandedKeys.has(key);
          const extraClass = rowClassName === undefined ? undefined : rowClassName(row);
          return (
            <Fragment key={key}>
              <tr
                data-row-key={key}
                className={
                  clickable
                    ? ['table-row-clickable', extraClass].filter(Boolean).join(' ')
                    : extraClass
                }
                tabIndex={clickable ? 0 : undefined}
                aria-expanded={renderExpanded !== undefined ? expanded : undefined}
                onClick={clickable ? () => handleRowClick(row) : undefined}
                onKeyDown={clickable ? (event) => handleRowKeyDown(event, row) : undefined}
              >
                {columns.map((col) => (
                  <td key={col.key} style={{ textAlign: col.align }}>
                    {col.render(row)}
                  </td>
                ))}
              </tr>
              {expanded && renderExpanded !== undefined && (
                <tr className="table-expanded-row">
                  <td colSpan={columns.length}>{renderExpanded(row)}</td>
                </tr>
              )}
            </Fragment>
          );
        })}
      </tbody>
    </table>
  );
}
