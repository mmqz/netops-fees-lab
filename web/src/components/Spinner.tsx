/**
 * 加载指示：CSS 旋转 spinner（Spinner）与「加载中」面板（LoadingPanel）。
 */

import { LOADING_TEXT } from '../i18n';

export function Spinner({ size = 20 }: { size?: number }) {
  return (
    <span
      className="spinner"
      style={{ width: size, height: size }}
      role="status"
      aria-label={LOADING_TEXT}
    />
  );
}

export function LoadingPanel() {
  return (
    <div className="loading-panel">
      <Spinner />
      <span className="loading-text">{LOADING_TEXT}</span>
    </div>
  );
}
