/**
 * 通用数据表格。
 * - 泛型列定义（key / 表头 / 渲染函数 / 列宽 / 对齐）；
 * - 加载骨架行、空态行；
 * - 可选行点击与可展开行（问题清单的证据明细）；
 * - 语义化表头（th scope="col"）。
 */

import { Fragment, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from 'react';
import { EMPTY_TEXT } from '../i18n';

export interface TableColumn<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  width?: string;
  align?: 'left' | 'right' | 'center';
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

  const clickable = onRowClick !== undefined || renderExpanded !== undefined;

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

  if (loading) {
    return (
      <table className="table" aria-busy="true" aria-label={ariaLabel}>
        <thead>
          <tr>
            {columns.map((col) => (
              <th key={col.key} scope="col" style={{ width: col.width, textAlign: col.align }}>
                {col.header}
              </th>
            ))}
          </tr>
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
        <tr>
          {columns.map((col) => (
            <th key={col.key} scope="col" style={{ width: col.width, textAlign: col.align }}>
              {col.header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.length === 0 && (
          <tr>
            <td colSpan={columns.length} className="table-empty">
              {emptyText}
            </td>
          </tr>
        )}
        {rows.map((row) => {
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
