/**
 * labTabs.ts — /lab/:id 右侧标签页的常量与 sessionStorage 持久化（#109-C）。
 *
 * 标签值白名单 LAB_TAB_VALUES 从 TABS 派生（单一事实源），禁止手抄：
 * 修复前白名单手抄 6 项漏了 'layers'（图层标签），而 TABS 含 7 项且切换
 * 即写 sessionStorage——切到图层后刷新页面，存储里的 'layers' 被白名单
 * 拒绝，静默回退「设备」标签。新增标签只需在 TABS 加一行，白名单与
 * 持久化自动跟进（labTabs.test.ts 的全标签写读往返钉死这一点）。
 */

import {
  LAB_TAB_CHANGESETS,
  LAB_TAB_CHECKPOINTS,
  LAB_TAB_CHECKS,
  LAB_TAB_DEVICES,
  LAB_TAB_INTERFACES,
  LAB_TAB_LAYERS,
  LAB_TAB_LINKS,
} from '../i18n';

export type LabTab = 'layers' | 'devices' | 'interfaces' | 'links' | 'checks' | 'changesets' | 'checkpoints';

export const TABS: Array<{ value: LabTab; label: string }> = [
  { value: 'layers', label: LAB_TAB_LAYERS },
  { value: 'devices', label: LAB_TAB_DEVICES },
  { value: 'interfaces', label: LAB_TAB_INTERFACES },
  { value: 'links', label: LAB_TAB_LINKS },
  { value: 'checks', label: LAB_TAB_CHECKS },
  { value: 'changesets', label: LAB_TAB_CHANGESETS },
  { value: 'checkpoints', label: LAB_TAB_CHECKPOINTS },
];

/** 标签值白名单（派生自 TABS——#109-C 消灭手抄，增删标签自动同步）。
 *  导出供 labTabs.test.ts 做闭集往返断言；运行期仅 readPersistedTab 消费。 */
export const LAB_TAB_VALUES: LabTab[] = TABS.map((tab) => tab.value);

/** 标签页选择按工作台持久化（sessionStorage，刷新后保留，P2）。 */
export function readPersistedTab(workspaceId: string): LabTab {
  if (typeof sessionStorage === 'undefined') return 'devices';
  try {
    const value = sessionStorage.getItem(`netops-lab.tab.${workspaceId}`);
    if (value !== null && (LAB_TAB_VALUES as string[]).includes(value)) return value as LabTab;
  } catch {
    // 隐私模式 / 配额异常：静默回退默认标签
  }
  return 'devices';
}

export function writePersistedTab(workspaceId: string, tab: LabTab): void {
  if (typeof sessionStorage === 'undefined') return;
  try {
    sessionStorage.setItem(`netops-lab.tab.${workspaceId}`, tab);
  } catch {
    // 写失败不影响功能
  }
}
