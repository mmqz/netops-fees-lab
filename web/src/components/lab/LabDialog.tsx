/**
 * 仿真工作台通用对话框骨架：
 * ESC / 遮罩点击关闭（busy 时不可关）、宽窄两档、滚动正文。
 * 语义与 ConfirmDialog / Drawer 保持一致（modal-root / modal-overlay / modal）。
 * AUD2-12：body 滚动锁定 + 焦点陷阱 + 焦点归还由 useModalA11y 提供。
 */

import { useEffect } from 'react';
import type { ReactNode } from 'react';
import { BTN_CANCEL, BTN_PROCESSING } from '../../i18n';
import { useModalA11y } from '../../hooks/useModalA11y';
import { IconClose } from '../icons';

interface LabDialogProps {
  open: boolean;
  title: string;
  onClose: () => void;
  /** 底部确认按钮（不提供则只渲染正文，由调用方自绘按钮）。 */
  confirmLabel?: string;
  /** 表单实时校验未通过时禁用确认按钮（不置 busy，区别于提交中）。 */
  confirmDisabled?: boolean;
  busy?: boolean;
  danger?: boolean;
  onConfirm?: () => void;
  children: ReactNode;
  wide?: boolean;
}

export function LabDialog({
  open,
  title,
  onClose,
  confirmLabel,
  confirmDisabled = false,
  busy = false,
  danger = false,
  onConfirm,
  children,
  wide = false,
}: LabDialogProps) {
  const modalRef = useModalA11y(open);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape' && !busy) {
        onClose();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  return (
    <div className="modal-root" role="presentation">
      <div className="modal-overlay" aria-hidden="true" onClick={() => (busy ? undefined : onClose())} />
      <div
        ref={modalRef}
        className={`modal${wide ? ' modal--wide' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
      >
        <div className="modal-head-row">
          <h3 className="modal-title">{title}</h3>
          <button
            type="button"
            className="btn btn--secondary btn--icon"
            aria-label={BTN_CANCEL}
            disabled={busy}
            onClick={onClose}
          >
            <IconClose />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {confirmLabel !== undefined && onConfirm !== undefined && (
          <div className="modal-form-actions">
            <button type="button" className="btn btn--secondary" disabled={busy} onClick={onClose}>
              {BTN_CANCEL}
            </button>
            <button
              type="button"
              className={danger ? 'btn btn--danger' : 'btn btn--primary'}
              disabled={busy || confirmDisabled}
              onClick={onConfirm}
            >
              {busy ? BTN_PROCESSING : confirmLabel}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
