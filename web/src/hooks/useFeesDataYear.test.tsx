// @vitest-environment jsdom
/**
 * useFeesDataYear.test.tsx — 数据优先年份默认的三条套用纪律（v0.20.3）。
 *
 * 在真实 React（createRoot + act）上钉死 hook 的契约：
 * 1. 一次性套用：发现落地且用户未碰过年份框 → 切到最新有数据年
 *    （2024 数据环境初始 2026 首帧后自动落 2024——旧观感缺陷的正修复）；
 * 2. 不抢在先输入：用户先于发现落地改年份（touched）→ 自动套用被挡；
 * 3. 失败静默回退：发现 reject → dataYears 保持 null（未知≠无数据）、
 *    日历年默认继续有效——发现绝不变成页面的新错误面。
 * 另钉：dataYears=[]（确证无数据）不套用（没有「最新有数据年」可言）；
 * 重挂载语义：tab 切换重置一切是既有行为，重挂载后再次套用（文档注释
 * 声明的取舍——记录在案，防未来「顺手修」把纪律改坏）。
 */
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useFeesDataYear, type FeesDataYear } from './useFeesDataYear';

// React 18 act 同步语义要求的环境标记（useSeq.test.tsx 同款）。
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('../api/client', () => ({
  fetchFeeYears: vi.fn(),
}));

import { fetchFeeYears } from '../api/client';

const mockedFetch = vi.mocked(fetchFeeYears);

/** 延迟 promise：测试体掌控发现何时落地（套用纪律 2 的时序控制）。 */
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** 渲染期记录 hook 返回值（测试体直接断言其三字段）。 */
let latest: FeesDataYear | null = null;
function Probe(): null {
  latest = useFeesDataYear();
  return null;
}

let container: HTMLDivElement;
let root: Root;

function mount(): void {
  root = createRoot(container);
  act(() => {
    root.render(<Probe />);
  });
}

function unmount(): void {
  act(() => {
    root.unmount();
  });
}

beforeEach(() => {
  mockedFetch.mockReset();
  latest = null;
  container = document.createElement('div');
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
});

describe('useFeesDataYear — 数据优先年份默认', () => {
  it('纪律 1：发现落地且未碰过 → 一次性切到最新有数据年', async () => {
    const d = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d.promise);
    mount();

    // 首帧：当前年默认 + dataYears=null（未知≠无数据）。
    expect(latest?.dataYears).toBeNull();

    await act(async () => {
      d.resolve({ years: [2023, 2024] });
    });

    expect(latest?.dataYears).toEqual([2023, 2024]);
    // 切到 2024（最新有数据年）。日历年默认 ≠ 2024 时这就是「2026→2024」
    // 的观感修复本体；恰逢 2024 时则无谓重渲染都没有（下一条专测）。
    expect(latest?.year).toBe('2024');
  });

  it('纪律 1 边界：日历年默认恰好已是最新数据年 → 不动（无谓重渲染都没有）', async () => {
    const current = String(new Date().getFullYear());
    const d = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d.promise);
    mount();
    await act(async () => {
      d.resolve({ years: [2023, Number(current)] });
    });
    expect(latest?.year).toBe(current);
  });

  it('纪律 2：用户先于发现落地改过年份 → 自动套用被挡（年份框是用户的）', async () => {
    const d = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d.promise);
    mount();

    // 用户在发现落地前改到 2025（快端点+快手，罕见但必须处理）。
    act(() => {
      latest?.setYear('2025');
    });
    await act(async () => {
      d.resolve({ years: [2023, 2024] });
    });

    expect(latest?.year).toBe('2025');
    expect(latest?.dataYears).toEqual([2023, 2024]);
  });

  it('纪律 3：发现失败 → 静默回退日历年默认，dataYears=null 表未知', async () => {
    const d = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d.promise);
    mount();
    const before = latest?.year;

    await act(async () => {
      d.reject(new Error('network'));
    });

    expect(latest?.year).toBe(before);
    expect(latest?.dataYears).toBeNull();
  });

  it('空清单（确证无任何导入）→ 不套用：没有「最新有数据年」可言', async () => {
    const d = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d.promise);
    mount();
    const before = latest?.year;
    await act(async () => {
      d.resolve({ years: [] });
    });
    expect(latest?.year).toBe(before);
    expect(latest?.dataYears).toEqual([]);
  });

  it('重挂载语义：tab 切换重置一切（既有行为），重挂载后再按纪律套用', async () => {
    // 第一挂载：2024 套用 → 用户改成 2025。
    const d1 = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d1.promise);
    mount();
    await act(async () => {
      d1.resolve({ years: [2024] });
    });
    act(() => {
      latest?.setYear('2025');
    });
    unmount();

    // 第二挂载（tab 切回来）：状态重置为日历年默认 → 发现 → 再套用 2024。
    const d2 = deferred<{ years: number[] }>();
    mockedFetch.mockReturnValueOnce(d2.promise);
    mount();
    expect(latest?.dataYears).toBeNull();
    await act(async () => {
      d2.resolve({ years: [2024] });
    });
    expect(latest?.year).toBe('2024');
  });
});
