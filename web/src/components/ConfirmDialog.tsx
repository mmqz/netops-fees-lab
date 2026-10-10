/**
 * 确认对话框（危险操作如批次回滚）：
 * ESC / 遮罩点击取消；确认中（busy）禁用全部按钮。
 * AUD2-12：body 滚动锁定 + 焦点陷阱 + 焦点归还由 useModalA11y 提供。
 */

import { useEffect } from 'react';
import { BTN_CANCEL, BTN_CONFIRM, BTN_PROCESSING } from '../i18n';
import { useModalA11y } from '../hooks/useModalA11y';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = BTN_CONFIRM,
  cancelLabel = BTN_CANCEL,
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const modalRef = useModalA11y(open);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape' && !busy) {
        onCancel();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, busy, onCancel]);

  if (!open) return null;

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={() => (busy ? undefined : onCancel())} />
      <div
        ref={modalRef}
        className="modal"
        role="alertdialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
      >
        <h3 className="modal-title">{title}</h3>
        <p className="modal-message">{message}</p>
        <div className="modal-actions">
          <button
            type="button"
            className="btn btn--secondary"
            disabled={busy}
            onClick={onCancel}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className={danger ? 'btn btn--danger' : 'btn btn--primary'}
            disabled={busy}
            onClick={onConfirm}
          >
            {busy ? BTN_PROCESSING : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
