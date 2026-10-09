/**
 * 分页控件：总数显示 + 上一页/下一页 + 页码进度。
 */

import { format, PAG_NEXT, PAG_PAGE_OF, PAG_PREV, PAG_TOTAL_PREFIX, PAG_TOTAL_SUFFIX } from '../i18n';
import { IconChevronLeft, IconChevronRight } from './icons';

interface PaginationProps {
  page: number;
  pageSize: number;
  total: number;
  onChange: (page: number) => void;
}

export function Pagination({ page, pageSize, total, onChange }: PaginationProps) {
  const pageCount = Math.max(1, Math.ceil(total / Math.max(1, pageSize)));
  const atFirst = page <= 1;
  const atLast = page >= pageCount;

  return (
    <div className="pagination">
      <span className="pagination-total">
        {PAG_TOTAL_PREFIX} {total} {PAG_TOTAL_SUFFIX} · {format(PAG_PAGE_OF, {
          page,
          pageCount,
        })}
      </span>
      <div className="pagination-controls">
        <button
          type="button"
          className="btn btn--secondary btn--icon"
          aria-label={PAG_PREV}
          disabled={atFirst}
          onClick={() => onChange(page - 1)}
        >
          <IconChevronLeft />
        </button>
        <button
          type="button"
          className="btn btn--secondary btn--icon"
          aria-label={PAG_NEXT}
          disabled={atLast}
          onClick={() => onChange(page + 1)}
        >
          <IconChevronRight />
        </button>
      </div>
    </div>
  );
}
