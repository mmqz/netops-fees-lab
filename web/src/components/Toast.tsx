/**
 * 全局提示（Toast）：右上角弹出，自动消失，始终带关闭按钮 + 状态图标（W9）。
 * 时长：提示 4 秒 / 成功 6 秒 / 错误及带详情 8 秒；带动作链接的提示 8 秒
 * （评委/观众来得及读完变更集 id 并点击）。error → role="alert"；
 * info/success → role="status"。detail 为次行技术原文；action 为可选操作链接。
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { TOAST_DISMISS } from '../i18n';
import { IconAlert, IconCheck, IconInfo } from './icons';

export type ToastVariant = 'error' | 'info' | 'success';

/** 可选动作链接（渲染为按钮；点击后不自动关闭，由调用方决定）。 */
export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface ToastOptions {
  /** 次行技术原文（如后端英文错误消息），小号灰色展示。 */
  detail?: string | null;
  /** 可选动作链接（如「查看变更集」）。 */
  action?: ToastAction;
}

export interface ToastMessage {
  id: number;
  message: string;
  variant: ToastVariant;
  detail: string | null;
  action: ToastAction | null;
}

interface ToastContextValue {
  show: (message: string, variant?: ToastVariant, options?: ToastOptions) => void;
}

const TOAST_DURATION_INFO_MS = 4000;
const TOAST_DURATION_SUCCESS_MS = 6000;
const TOAST_DURATION_ERROR_MS = 8000;
/** 带动作链接的提示统一 8 秒：留出点击时间（W9 演示场景）。 */
const TOAST_DURATION_ACTION_MS = 8000;

const TOAST_CONTEXT_ERROR = 'useToast 必须在 ToastProvider 内部使用';

const ToastContext = createContext<ToastContextValue | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const counterRef = useRef(0);

  const show = useCallback(
    (message: string, variant: ToastVariant = 'info', options?: ToastOptions) => {
      counterRef.current += 1;
      const id = counterRef.current;
      setToasts((prev) => [
        ...prev,
        {
          id,
          message,
          variant,
          detail: options?.detail ?? null,
          action: options?.action ?? null,
        },
      ]);
    },
    [],
  );

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((toast) => toast.id !== id));
  }, []);

  const contextValue = useMemo<ToastContextValue>(() => ({ show }), [show]);

  return (
    <ToastContext.Provider value={contextValue}>
      {children}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext);
  if (context === null) {
    throw new Error(TOAST_CONTEXT_ERROR);
  }
  return context;
}

function ToastViewport({
  toasts,
  onDismiss,
}: {
  toasts: ToastMessage[];
  onDismiss: (id: number) => void;
}) {
  return (
    <div className="toast-viewport" aria-live="polite">
      {toasts.map((toast) => (
        <ToastCard key={toast.id} toast={toast} onDismiss={onDismiss} />
      ))}
    </div>
  );
}

function durationFor(toast: ToastMessage): number {
  // 错误及带详情的提示需要更长的阅读时间（AUD2-18）；
  // 成功提示 6 秒（AUD-4 P2-8：4 秒在读变更集 id 时太快）；带动作链接 8 秒。
  if (toast.action !== null) return TOAST_DURATION_ACTION_MS;
  if (toast.variant === 'error' || toast.detail !== null) return TOAST_DURATION_ERROR_MS;
  if (toast.variant === 'success') return TOAST_DURATION_SUCCESS_MS;
  return TOAST_DURATION_INFO_MS;
}

function ToastCard({
  toast,
  onDismiss,
}: {
  toast: ToastMessage;
  onDismiss: (id: number) => void;
}) {
  const { id, message, variant, detail, action } = toast;

  useEffect(() => {
    const timer = setTimeout(() => onDismiss(id), durationFor(toast));
    return () => clearTimeout(timer);
  }, [id, onDismiss, toast]);

  const icon =
    variant === 'success' ? <IconCheck /> : variant === 'error' ? <IconAlert /> : <IconInfo />;

  return (
    <div className={`toast toast--${variant}`} role={variant === 'error' ? 'alert' : 'status'}>
      <span className={`toast-icon toast-icon--${variant}`} aria-hidden="true">
        {icon}
      </span>
      <span className="toast-message">
        {message}
        {detail !== null && <span className="toast-detail">{detail}</span>}
      </span>
      {action !== null && (
        <button type="button" className="toast-action" onClick={action.onClick}>
          {action.label}
        </button>
      )}
      <button
        type="button"
        className="toast-close"
        aria-label={TOAST_DISMISS}
        onClick={() => onDismiss(id)}
      >
        ×
      </button>
    </div>
  );
}
