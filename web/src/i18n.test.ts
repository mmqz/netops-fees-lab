import { describe, expect, it } from 'vitest';
import {
  format,
  formatDateTime,
  formatFileSize,
  formatRelativeTime,
  LAB_GEO_CLUSTER_HOVER,
  LAB_TRACE_VIA_MORE,
  NO_VALUE,
} from './i18n';

// i18n.test.ts — #96: the formatter layer is consumed by EVERY page; before
// this file the 40-assertion suite covered fees-format + fitGuard only and
// these were exercised solely through browser regression scripts.
// #111-D 追加：死常量探测器——i18n.ts 是全站文案唯一事实源，导出常量必须
// 被 src 内至少一处引用（页面 / 组件 / 本文件内部工具函数 / 测试），
// 否则就是「常量写好但页面硬编码」或「功能下线遗留」的漂移债。

describe('format（模板填充）', () => {
  it('填充提供的占位符', () => {
    expect(format('{n} 条', { n: 42 })).toBe('42 条');
    expect(format('{a}/{b}', { a: 1, b: 2 })).toBe('1/2');
  });
  it('未提供的占位符原样保留（不静默丢信息）', () => {
    expect(format('{n} 条 / {total} 条', { n: 3 })).toBe('3 条 / {total} 条');
    expect(format('无占位符', {})).toBe('无占位符');
  });
});

describe('formatDateTime（RFC3339 → 本地显示）', () => {
  it('本地时间的「YYYY-MM-DD HH:mm」形态', () => {
    // 无时区后缀的 ISO 串按本地时间解释 → 断言值与时区无关
    expect(formatDateTime('2026-10-20T12:34')).toBe('2026-10-20 12:34');
  });
  it('空值/缺省 → 占位符', () => {
    expect(formatDateTime(undefined)).toBe(NO_VALUE);
    expect(formatDateTime(null)).toBe(NO_VALUE);
    expect(formatDateTime('')).toBe(NO_VALUE);
  });
  it('非法串原样返回（不吞错）', () => {
    expect(formatDateTime('not-a-date')).toBe('not-a-date');
  });
  it('月/日/时/分补零', () => {
    expect(formatDateTime('2026-01-02T03:04')).toBe('2026-01-02 03:04');
  });
});

describe('formatFileSize', () => {
  it('字节/KB/MB 分档', () => {
    expect(formatFileSize(512)).toBe('512 B');
    expect(formatFileSize(2048)).toBe('2.0 KB');
    expect(formatFileSize(3 * 1024 * 1024)).toBe('3.0 MB');
  });
  it('分档边界（1023B / 1MB）', () => {
    expect(formatFileSize(1023)).toBe('1023 B');
    expect(formatFileSize(1024)).toBe('1.0 KB');
    expect(formatFileSize(1024 * 1024)).toBe('1.0 MB');
  });
});

describe('formatRelativeTime', () => {
  it('空值 → 占位符；非法串原样返回', () => {
    expect(formatRelativeTime(null)).toBe(NO_VALUE);
    expect(formatRelativeTime(undefined)).toBe(NO_VALUE);
    expect(formatRelativeTime('junk')).toBe('junk');
  });
  it('分/时/天梯度与 7 天回退绝对时间', () => {
    const now = Date.now();
    expect(formatRelativeTime(new Date(now - 30_000).toISOString())).toBe('刚刚');
    expect(formatRelativeTime(new Date(now - 5 * 60_000).toISOString())).toBe('5 分钟前');
    expect(formatRelativeTime(new Date(now - 3 * 3600_000).toISOString())).toBe('3 小时前');
    expect(formatRelativeTime(new Date(now - 5 * 24 * 3600_000).toISOString())).toBe('5 天前');
    // 超过 7 天 → 回退 formatDateTime 的本地绝对形态（无时区后缀构造 + 本地 ISO 解析闭环）
    const old = new Date(now - 8 * 24 * 3600_000);
    expect(formatRelativeTime(old.toISOString())).toBe(formatDateTime(old.toISOString()));
  });
});

// ---------------------------------------------------------------------------
// #111-D：死常量探测器（静态扫描——i18n 导出常量的引用闭包）
// ---------------------------------------------------------------------------

/** 以 ?raw 形式内联全部源码（import.meta.glob 由 vite-node 转换，无需 node:fs，
 *  类型域保持在浏览器侧 tsconfig 内）。键形如 '/src/pages/Fees.tsx'。 */
const RAW_SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** i18n.ts 本体的扫描键（#113-A 起探测器与收编回归钉共用）。 */
const i18nKey = Object.keys(RAW_SOURCES).find((key) => key.endsWith('/i18n.ts')) ?? '';

describe('i18n 导出常量引用闭包（#111-D：死常量回归钉）', () => {
  const i18nLines = (RAW_SOURCES[i18nKey] ?? '').split('\n');

  /** 声明行之外的出现视为真实引用（formatRelativeTime 消费 TIME_* 一类）。 */
  const exportedNames = i18nLines
    .map((line) => /^export const ([A-Z][A-Z0-9_]*)/.exec(line)?.[1])
    .filter((name): name is string => name !== undefined);

  const otherContents = Object.entries(RAW_SOURCES)
    .filter(([key]) => key !== i18nKey)
    .map(([, content]) => content);

  it('i18n.ts 每个导出常量都被引用（页面/组件/文件内工具/测试至少其一）', () => {
    const dead: string[] = [];
    for (const name of exportedNames) {
      const referencedInFile = i18nLines.some(
        (line) => new RegExp(`\\b${name}\\b`).test(line) && !/^export const /.test(line),
      );
      const referencedElsewhere = otherContents.some((content) =>
        new RegExp(`\\b${name}\\b`).test(content),
      );
      if (!referencedInFile && !referencedElsewhere) dead.push(name);
    }
    expect(
      dead,
      '死 i18n 常量（写好但无人引用）：删掉它，或把页面的硬编码文案改用常量',
    ).toEqual([]);
  });

  it('探测器自检：能枚举导出常量与源码文件（扫描器失效时此断言先红）', () => {
    expect(i18nKey, '扫描器应能找到 i18n.ts 本体').not.toBe('');
    expect(exportedNames.length).toBeGreaterThan(300);
    expect(exportedNames).toContain('NO_VALUE');
    expect(exportedNames).toContain('APP_NAME');
    expect(Object.keys(RAW_SOURCES).length).toBeGreaterThan(40);
  });
});

// ---------------------------------------------------------------------------
// #113-A：第二批硬编码收编回归钉（LayerInfoTab / TopologyCanvas / GeoCanvas /
// App 令牌弹窗 / Maintenance——旧内联串零复活 + 迁入完整 + 五触点接线）
// ---------------------------------------------------------------------------

/** 第二批收编前的旧内联串（长句逐字——短词如「关闭」「直达」太通用，由
 *  接线钉与死常量探测器共同覆盖）。出现在 i18n.ts / 测试文件之外 = 抄回组件。 */
const RETIRED_PHRASES = [
  '本工作台物化于 v0.12 之前，无图层覆盖层数据',
  '在画布上点选接入簇 / OLT / 客户 / 业务 / 逻辑电路等覆盖层实体查看详情（需先打开对应图层开关）',
  '逻辑承载/业务关联边为只读投影：右键菜单可发起影响分析高亮。',
  '力导向布局（物理模拟自然聚簇）',
  '层级布局（自上而下树形，核心层在顶）',
  '同心圆布局（按连接度分环，枢纽居中）',
  '网格布局（规整行列，便于对照清单）',
  '环形布局（首尾相接的环）',
  '锁定布局（停用自动排布，保留手动拖拽位置）',
  '选择模式（点击联动右侧表格定位）',
  '路径模式（依次点击两台设备，高亮最短路径）',
  '聚焦模式（点击设备，仅保留其一跳邻域）',
  '未知（尚未执行过检测）',
];

describe('i18n 第二批收编回归（#113-A）', () => {
  /** i18n.ts 与测试文件之外的生产源码。 */
  const productionSources = Object.entries(RAW_SOURCES).filter(
    ([key]) => !key.endsWith('/i18n.ts') && !key.includes('.test.'),
  );

  it('13 条旧串全部迁入 i18n.ts（每条都能在文案事实源找到）', () => {
    const i18nSource = RAW_SOURCES[i18nKey] ?? '';
    for (const phrase of RETIRED_PHRASES) {
      expect(i18nSource.includes(phrase), `「${phrase}」应收编进 i18n.ts`).toBe(true);
    }
  });

  it('旧内联串未在任何组件/页面复活（raw 扫描）', () => {
    const offenders: string[] = [];
    for (const [key, content] of productionSources) {
      for (const phrase of RETIRED_PHRASES) {
        if (content.includes(phrase)) offenders.push(`${key} 内联了「${phrase}」`);
      }
    }
    expect(offenders, '硬编码回潮文件：改回引用 i18n 常量').toEqual([]);
  });

  it('五触点文件的接线（raw 扫描：新常量确实被对应文件消费，防「只被测试引用」的假绿）', () => {
    const WIRINGS: Array<[string, string]> = [
      ['/src/components/lab/LayerInfoTab.tsx', 'LAYER_INFO_EMPTY_DESC'],
      ['/src/components/lab/LayerInfoTab.tsx', 'LAYER_INFO_HINT'],
      ['/src/components/lab/LayerInfoTab.tsx', 'LAYER_INFO_NOTE'],
      ['/src/components/lab/TopologyCanvas.tsx', 'LAB_TRACE_VIA_MORE'],
      ['/src/components/lab/TopologyCanvas.tsx', 'LAB_LAYOUT_FORCE_TITLE'],
      ['/src/components/lab/TopologyCanvas.tsx', 'LAB_MODE_SELECT_TITLE'],
      ['/src/components/lab/TopologyCanvas.tsx', 'LAYER_NODE_FALLBACK'],
      ['/src/components/lab/GeoCanvas.tsx', 'LAB_GEO_CLUSTER_HOVER'],
      ['/src/App.tsx', 'BTN_CLOSE'],
      ['/src/pages/Maintenance.tsx', 'MNT_TIME_UNKNOWN'],
    ];
    for (const [file, name] of WIRINGS) {
      expect(RAW_SOURCES[file], `${file} 应消费 ${name}`).toContain(name);
    }
  });

  it('插值词形钉：路径途经折叠「{via} 等 {n} 台」与聚簇 hover「{n} 台」（行为逐字节等价）', () => {
    expect(
      format(LAB_TRACE_VIA_MORE, { via: '站一、站二、站三、站四、站五、站六', n: 9 }),
    ).toBe('站一、站二、站三、站四、站五、站六 等 9 台');
    expect(format(LAB_GEO_CLUSTER_HOVER, { n: 3 })).toBe('3 台');
  });
});
