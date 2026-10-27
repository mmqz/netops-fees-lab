// @vitest-environment jsdom
/**
 * useFeesDataYear — 数据优先年份默认（v0.20.3）。
 *
 * 旧观感缺陷：费用域四个年份输入（总览/查询预设/风险/对账）一律
 * `useState(String(new Date().getFullYear()))`——日历替数据做了主。2024
 * 数据环境（本仓实测形态）打开总览是 2026 的空白仪表盘：「最新有数据月」
 * 解析为 0，卡片横幅条件（resolvedMonth > 0）不成立整页无空态，风险地图
 * 还把 month=0 发给后端浮出「month 必须是 1–12 的整数」校验错。
 *
 * 本 hook 的套用纪律（三条，缺一不可）：
 * 1. **一次性**：发现落地时若用户尚未碰过年份框，切到最新有数据年；之后
 *    绝不再抢——年份框是用户的，数据不第二次做主。
 * 2. **不抢在先输入**：用户先于发现落地改过年份（快端点+慢手，罕见但
 *    必须处理）→ touched 标记挡住自动套用。
 * 3. **失败静默回退**：发现失败（网络/500）→ dataYears 保持 null、日历年
 *    默认继续有效——年份发现是观感增强，绝不变成页面的新错误面（错误
 *    面板归数据查询本身；null 与 [] 语义分家：null=未知，[]=确证无数据）。
 *
 * 数据面契约见 GET /api/fees/years（docs/api.md）：路径级发现、两种构建
 * 都作答、回滚后的空分区目录不算在场。
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { fetchFeeYears } from '../api/client';

export interface FeesDataYear {
  /** 年份字符串（与既有输入框一致的 string 态；初始=当前年） */
  year: string;
  /** 用户通道的 setter：任何调用都标记 touched（挡掉后续自动套用） */
  setYear: Dispatch<SetStateAction<string>>;
  /** 数据年份升序；null=发现未完成或失败（未知≠无数据，横幅面据此分家） */
  dataYears: number[] | null;
}

export function useFeesDataYear(): FeesDataYear {
  const [year, setYearRaw] = useState(String(new Date().getFullYear()));
  const [dataYears, setDataYears] = useState<number[] | null>(null);
  const applied = useRef(false);
  const touched = useRef(false);

  const setYear: Dispatch<SetStateAction<string>> = useCallback((v) => {
    touched.current = true;
    setYearRaw(v);
  }, []);

  useEffect(() => {
    let cancelled = false;
    fetchFeeYears()
      .then((r) => {
        if (cancelled) return;
        setDataYears(r.years);
        if (!applied.current && !touched.current && r.years.length > 0) {
          applied.current = true;
          const latest = r.years[r.years.length - 1];
          // 函数式更新：当前默认恰好已是最新数据年则不动（避免无谓重渲染）；
          // 否则一次性切过去——用户此刻还没碰过年份框，切换无感。
          setYearRaw((prev) => {
            const n = Number(prev);
            return Number.isInteger(n) && n === latest ? prev : String(latest);
          });
        }
      })
      .catch(() => {
        // 失败静默回退（纪律 3）：日历年默认继续有效，dataYears=null 表未知。
        if (!cancelled) setDataYears(null);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { year, setYear, dataYears };
}
