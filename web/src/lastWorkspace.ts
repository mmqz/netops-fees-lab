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
