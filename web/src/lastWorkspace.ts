/**
 * 最近访问的仿真工作台（localStorage 持久化）：
 * 侧栏「拓扑工作台」入口指向最近打开的工作台，进入 /lab/:id 时写入。
 */

const STORAGE_KEY = 'netops-lab:last-workspace';

export function saveLastWorkspace(id: string): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // 隐私模式 / 存储不可用：静默降级（仅影响侧栏快捷入口）
  }
}

export function readLastWorkspace(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

/**
 * 清除最近工作台记录（AUD2-11）：工作台被删除 / 加载 404 时调用，
 * 避免侧栏「拓扑工作台」快捷入口指向已不存在的工作台。
 * 传入 id 时仅在存储值与该 id 一致时清除（不影响其他工作台的记录）。
 */
export function clearLastWorkspace(id?: string): void {
  try {
    if (id === undefined || window.localStorage.getItem(STORAGE_KEY) === id) {
      window.localStorage.removeItem(STORAGE_KEY);
    }
  } catch {
    // 隐私模式 / 存储不可用：静默降级
  }
}
