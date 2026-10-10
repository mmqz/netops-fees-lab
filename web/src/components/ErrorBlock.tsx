/**
 * 错误提示块：错误信息（可选次行技术原文 detail）+ 可选重试按钮。
 * compact 变体用于行内/抽屉内的紧凑场景（AUD2-13）。
 */

import { BTN_RETRY, LOAD_FAILED } from '../i18n';
import { IconWarning } from './icons';

interface ErrorBlockProps {
  message?: string | null;
  /** 次行技术原文（后端英文消息原文），小号灰色（FX2-4）。 */
  detail?: string | null;
  /** 紧凑变体：更小内边距，用于抽屉/行内。 */
  compact?: boolean;
  onRetry?: () => void;
}

export function ErrorBlock({ message, detail, compact = false, onRetry }: ErrorBlockProps) {
  const text = message === null || message === undefined || message === '' ? LOAD_FAILED : message;
  return (
    <div className={`error-block${compact ? ' error-block--compact' : ''}`} role="alert">
      <IconWarning />
      <span className="error-block-message">
        {text}
        {detail !== null && detail !== undefined && detail !== '' && (
          <span className="error-block-detail">{detail}</span>
        )}
      </span>
      {onRetry !== undefined && (
        <button type="button" className="btn btn--secondary btn--sm" onClick={onRetry}>
          {BTN_RETRY}
        </button>
      )}
    </div>
  );
}
