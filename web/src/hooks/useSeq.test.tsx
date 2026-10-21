// @vitest-environment jsdom
/**
 * useSeq.test.tsx — 请求序列守卫的契约测试（#109-D 修复的机制回归钉）。
 *
 * ResourceDrawer 的 5 路 Promise.all 取数此前是全仓 useSeq 纪律（App/
 * Overview/LabWorkspace/ChecksTab 等 12 处）的唯一漏网点：A→B 快速切换时
 * A 的慢响应后到会把旧数据盖到新资源上。修复 = load 内 begin()/isCurrent()
 * 门 + <ResourceDrawer key={drawerId}>。本文件在真实 React（createRoot）上
 * 验证守卫的三条契约与缺陷主场景：
 * 1. latest-wins：新请求发起后，旧请求的守卫一律返回 false（整包丢弃）；
 * 2. 卸载作废：组件卸载后在途守卫全部失效（fetch-then-unmount 不落态）；
 * 3. 身份稳定：守卫对象跨渲染不变（作为 useEffect/useCallback 依赖不会
 *    引发无限重取——useSeq 文档注释声明的前提）。
 * 另含无守卫对照组：证明「A 慢响应盖 B」这个 harness 真能捕捉缺陷。
 */
import { act, useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useSeq } from './useSeq';
import type { SeqGuard } from './useSeq';

// React 18 act 同步语义要求的环境标记（否则 act 会打警告）
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

/** 渲染期记录守卫对象（测试体直接对其调用 begin() 验证语义）。 */
const seen: SeqGuard[] = [];
function GuardProbe(): null {
  const guard = useSeq();
  seen.push(guard);
  return null;
}

beforeEach(() => {
  seen.length = 0;
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  try {
    act(() => {
      root.unmount();
    });
  } catch {
    // 该测试内已显式卸载：忽略二次卸载
  }
  container.remove();
});

/** 可控延迟：按 id 挂起，测试体手动决定谁先返回。 */
interface Deferred {
  promise: Promise<string>;
  resolve: (value: string) => void;
}

function makeDeferred(): Deferred {
  let resolve!: (value: string) => void;
  const promise = new Promise<string>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

/** 复刻 ResourceDrawer.load 的守卫模式（useSeq + isCurrent 门）。 */
function GuardedDrawerLike({ id, load }: { id: string; load: (id: string) => Promise<string> }): ReactElement {
  const guard = useSeq();
  const [shown, setShown] = useState('(loading)');
  useEffect(() => {
    const isCurrent = guard.begin();
    setShown('(loading)');
    load(id).then((value) => {
      if (isCurrent()) setShown(value);
    });
  }, [id, guard, load]);
  return <>{shown}</>;
}

/** 对照组：同一 harness 去掉守卫——缺陷可复现，证明守卫测试有鉴别力。 */
function UnguardedDrawerLike({ id, load }: { id: string; load: (id: string) => Promise<string> }): ReactElement {
  const [shown, setShown] = useState('(loading)');
  useEffect(() => {
    setShown('(loading)');
    load(id).then((value) => {
      setShown(value);
    });
  }, [id, load]);
  return <>{shown}</>;
}

describe('useSeq — 序列守卫契约', () => {
  it('begin() 后守卫为 true；再次 begin() 后旧守卫失效（旧响应整包丢弃）', () => {
    act(() => {
      root.render(<GuardProbe />);
    });
    const guard = seen[seen.length - 1];
    const first = guard.begin();
    expect(first()).toBe(true);
    const second = guard.begin();
    expect(first()).toBe(false); // 后发请求已发起 → 在途旧响应必须丢弃
    expect(second()).toBe(true);
  });

  it('守卫对象跨渲染身份稳定（作 useEffect/useCallback 依赖不引发无限重取）', () => {
    act(() => {
      root.render(<GuardProbe />);
    });
    act(() => {
      root.render(<GuardProbe />); // 同 root 同类型 → 重渲染而非重挂载
    });
    expect(seen.length).toBeGreaterThanOrEqual(2);
    expect(seen[seen.length - 1]).toBe(seen[0]); // useMemo 同对象
  });

  it('卸载后一切守卫失效（fetch-then-unmount 不再落态）', () => {
    act(() => {
      root.render(<GuardProbe />);
    });
    const guard = seen[seen.length - 1];
    const inFlight = guard.begin();
    expect(inFlight()).toBe(true);
    act(() => {
      root.unmount();
    });
    expect(inFlight()).toBe(false); // 卸载递增序列号 → 在途响应全部作废
    expect(guard.begin()()).toBe(false); // 卸载后新发的守卫也一律 false
  });
});

describe('抽屉式取数竞态（#109-D 主场景 harness 复刻）', () => {
  it('A→B 快速切换：A 的慢响应后到，被守卫拦截不得覆盖 B 的新数据', async () => {
    const slowA = makeDeferred();
    const fastB = makeDeferred();
    const loads: Record<string, Promise<string>> = { A: slowA.promise, B: fastB.promise };
    const load = (id: string): Promise<string> => loads[id];

    act(() => {
      root.render(<GuardedDrawerLike id="A" load={load} />);
    });
    expect(container.textContent).toBe('(loading)');
    act(() => {
      root.render(<GuardedDrawerLike id="B" load={load} />); // A 未返回就切到 B（同实例换 id）
    });
    await act(async () => {
      fastB.resolve('resource-B');
    });
    expect(container.textContent).toBe('resource-B');
    await act(async () => {
      slowA.resolve('resource-A'); // A 的 5 路并发里最慢的一路后到
    });
    expect(container.textContent).toBe('resource-B'); // 串台被 isCurrent() 拦截
  });

  it('对照组：同一 harness 无守卫时 A 慢响应会盖掉 B（证明上一断言有鉴别力）', async () => {
    const slowA = makeDeferred();
    const fastB = makeDeferred();
    const loads: Record<string, Promise<string>> = { A: slowA.promise, B: fastB.promise };
    const load = (id: string): Promise<string> => loads[id];

    act(() => {
      root.render(<UnguardedDrawerLike id="A" load={load} />);
    });
    act(() => {
      root.render(<UnguardedDrawerLike id="B" load={load} />);
    });
    await act(async () => {
      fastB.resolve('resource-B');
    });
    expect(container.textContent).toBe('resource-B');
    await act(async () => {
      slowA.resolve('resource-A');
    });
    expect(container.textContent).toBe('resource-A'); // 无守卫 → 旧数据盖新（缺陷原样复现）
  });
});
