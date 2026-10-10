/**
 * 模态可达性（AUD2-12 / FX2）：
 * 打开时锁定 body 滚动 + 焦点陷阱（Tab/Shift-Tab 在对话框内循环）+ 关闭后焦点归还。
 *
 * - 与 Drawer 的滚动锁定语义一致（记录并恢复 body.style.overflow 原值）；
 * - 打开时聚焦对话框容器（调用方需给容器 tabIndex={-1}）；
 * - 关闭/卸载时把焦点还给打开前的焦点元素（若仍在文档中）；
 * - ESC 关闭由各对话框自身的 keydown 处理保留，本 hook 不重复处理。
 *
 * 返回值：绑定到对话框根元素（role="dialog" 容器）的 ref。
 */

import { useEffect, useRef } from 'react';
import type { RefObject } from 'react';

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function useModalA11y(open: boolean): RefObject<HTMLDivElement> {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return undefined;
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    containerRef.current?.focus();

    function focusablesIn(container: HTMLElement | null): HTMLElement[] {
      if (container === null) return [];
      return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
        (el) => el.offsetParent !== null || el === document.activeElement,
      );
    }

    const onKeydown = (event: KeyboardEvent): void => {
      if (event.key !== 'Tab') return;
      const container = containerRef.current;
      if (container === null) return;
      const focusables = focusablesIn(container);
      if (focusables.length === 0) {
        event.preventDefault();
        container.focus();
        return;
      }
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const active = document.activeElement;
      const inside = active instanceof Node && container.contains(active);
      if (event.shiftKey) {
        if (!inside || active === first) {
          event.preventDefault();
          last.focus();
        }
      } else {
        if (!inside || active === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };

    document.addEventListener('keydown', onKeydown);
    return () => {
      document.removeEventListener('keydown', onKeydown);
      document.body.style.overflow = previousOverflow;
      // 焦点归还：仅当归还目标仍在文档中且不在对话框内部
      if (previouslyFocused !== null && document.contains(previouslyFocused)) {
        if (containerRef.current === null || !containerRef.current.contains(previouslyFocused)) {
          previouslyFocused.focus();
        }
      }
    };
  }, [open]);

  return containerRef;
}
