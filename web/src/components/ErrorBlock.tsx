/**
 * 错误提示块：错误信息 + 可选重试按钮。
 */

import { BTN_RETRY, LOAD_FAILED } from '../i18n';
import { IconWarning } from './icons';

interface ErrorBlockProps {
  message?: string | null;
  onRetry?: () => void;
}

export function ErrorBlock({ message, onRetry }: ErrorBlockProps) {
  const text = message === null || message === undefined || message === '' ? LOAD_FAILED : message;
  return (
    <div className="error-block" role="alert">
      <IconWarning />
      <span className="error-block-message">{text}</span>
      {onRetry !== undefined && (
        <button type="button" className="btn btn--secondary btn--sm" onClick={onRetry}>
          {BTN_RETRY}
        </button>
      )}
    </div>
  );
}
