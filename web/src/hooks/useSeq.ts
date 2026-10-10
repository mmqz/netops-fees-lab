/**
 * 请求序列守卫（AUD2-8 / FX2-3）：
 * P1 页面（Resources/Issues）requestSeqRef 模式的共享封装，用于 /lab 各取数路径。
 *
 * - begin()：发起请求前调用，递增序列号并返回「是否仍为最新请求」的守卫函数；
 * - 守卫返回 false 表示已有更新的请求发起（旧响应应整包丢弃，防止串台覆盖）；
 * - 组件卸载后守卫一律返回 false（fetch-then-unmount 安全）。
 *
 * 用法：
 * ```ts
 * const guard = useSeq();
 * const isCurrent = guard.begin();
 * listChecks(workspaceId).then((paged) => {
 *   if (!isCurrent()) return;
 *   setRuns(paged.items);
 * });
 * ```
 *
 * 注意：同一实例只服务于一个取数点；同一组件内多个并发取数点（如
 * ChecksTab 的运行列表 + 运行详情）应各自持有一个实例，否则后发的
 * 请求会错误地作废先发请求的结果。
 */

import { useCallback, useEffect, useMemo, useRef } from 'react';

export interface SeqGuard {
  /** 递增序列号，返回守卫函数（true = 结果仍有效）。 */
  begin: () => () => boolean;
}

export function useSeq(): SeqGuard {
  const seqRef = useRef(0);
  const aliveRef = useRef(true);

  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
      seqRef.current += 1; // 作废所有在途响应
    };
  }, []);

  const begin = useCallback((): (() => boolean) => {
    const seq = ++seqRef.current;
    return (): boolean => aliveRef.current && seqRef.current === seq;
  }, []);

  // 返回对象必须稳定（useMemo），否则作为 useCallback/useEffect 依赖会造成无限重取
  return useMemo<SeqGuard>(() => ({ begin }), [begin]);
}
