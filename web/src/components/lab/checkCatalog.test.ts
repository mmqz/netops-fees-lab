/**
 * checkCatalog.test.ts — 仿真检查消息正则的 Go 锚测（#111-B 验收标准）。
 *
 * 缺陷场景：translateCheckMessage 的 5 条正则逐字镜像 internal/sim/checks.go
 * 的 fmt.Sprintf 消息格式。后端措辞一改，前端正则静默失配 → 消息「降级设计」
 * 回退英文原文，用户看到的 demo「关键时刻」重新跳英文——漂移被降级掩盖，
 * 无任何测试报警。
 *
 * 钉法（同 #109 issuesRules 闭集断言的手法）：本文件以字面镜像形式保存 Go
 * 消息模板作为第三方事实源，正/负两排断言——
 * - 正排：Go 模板镜像产出的消息必须命中正则并回填到正确槽位（措辞、空格、
 *   括号逐字对齐；后端改措辞 → 同步本镜像 → 正排立即提醒同步正则）；
 * - 负排：锚定 ^…$ 之外的变体（尾句号/复数变形/夹空格）必须失配回退
 *   （null），证明正则是逐字锚定而非宽松兜底。
 */
import { describe, expect, it } from 'vitest';
import type { SimCheckCode } from '../../api/types';
import { CHECK_CATALOG, translateCheckMessage } from './checkCatalog';

/**
 * internal/sim/checks.go 消息模板的字面镜像（第三方事实源；后端改措辞时
 * 需人工同步此处 + checkCatalog.ts 正则——正排测试会先红来提醒）：
 * - CHK-001 fmt.Sprintf("duplicate host address %s used by %d interfaces", …)
 * - CHK-002 fmt.Sprintf("subnets %s and %s overlap (%s)", …)  kind ∈ identical/contains/partial
 * - CHK-003 fmt.Sprintf("device %s is isolated (no up links)", …)
 * - CHK-004 fmt.Sprintf("component of %d devices is disconnected from the main component", …)
 * - CHK-005 fmt.Sprintf("interface %s/%s has no IPv4 address", …)
 */
const GO_MESSAGE_MIRROR = {
  'CHK-001': (addr: string, n: number) => `duplicate host address ${addr} used by ${n} interfaces`,
  'CHK-002': (a: string, b: string, kind: string) => `subnets ${a} and ${b} overlap (${kind})`,
  'CHK-003': (code: string) => `device ${code} is isolated (no up links)`,
  'CHK-004': (n: number) => `component of ${n} devices is disconnected from the main component`,
  'CHK-005': (device: string, name: string) => `interface ${device}/${name} has no IPv4 address`,
} as const;

// ---------------------------------------------------------------------------
// 正排：Go 模板镜像 → 正则命中 → 中文回填槽位（逐字锚测）
// ---------------------------------------------------------------------------

describe('translateCheckMessage 正排：Go 消息格式锚测（镜像命中即中文）', () => {
  it('CHK-001：duplicate host address %s used by %d interfaces', () => {
    const message = GO_MESSAGE_MIRROR['CHK-001']('10.0.0.7', 3);
    expect(translateCheckMessage('CHK-001', message)).toBe('IPv4 地址 10.0.0.7 被 3 个接口同时配置');
  });

  it('CHK-002：subnets %s and %s overlap (%s)——kind ∈ identical/contains/partial 各归各的中文', () => {
    expect(translateCheckMessage('CHK-002', GO_MESSAGE_MIRROR['CHK-002']('10.0.0.0/24', '10.0.0.9/24', 'identical'))).toBe(
      '网段 10.0.0.0/24 与 10.0.0.9/24 重叠（完全相同）',
    );
    expect(translateCheckMessage('CHK-002', GO_MESSAGE_MIRROR['CHK-002']('10.0.0.0/16', '10.0.0.0/24', 'contains'))).toBe(
      '网段 10.0.0.0/16 与 10.0.0.0/24 重叠（包含）',
    );
    expect(translateCheckMessage('CHK-002', GO_MESSAGE_MIRROR['CHK-002']('10.0.0.0/24', '10.0.0.128/25', 'partial'))).toBe(
      '网段 10.0.0.0/24 与 10.0.0.128/25 重叠（部分重叠）',
    );
  });

  it('CHK-002：未知 kind（引擎扩枚举）原样透传不吞词', () => {
    expect(translateCheckMessage('CHK-002', GO_MESSAGE_MIRROR['CHK-002']('a/8', 'b/8', 'nested'))).toBe(
      '网段 a/8 与 b/8 重叠（nested）',
    );
  });

  it('CHK-003：device %s is isolated (no up links)', () => {
    expect(translateCheckMessage('CHK-003', GO_MESSAGE_MIRROR['CHK-003']('PE-SH-01'))).toBe(
      '设备 PE-SH-01 为孤岛（没有任何可用链路）',
    );
  });

  it('CHK-004：component of %d devices is disconnected from the main component', () => {
    expect(translateCheckMessage('CHK-004', GO_MESSAGE_MIRROR['CHK-004'](4))).toBe('4 台设备组成的分区与主分区失联');
  });

  it('CHK-005：interface %s/%s has no IPv4 address（设备名与接口名分槽）', () => {
    expect(translateCheckMessage('CHK-005', GO_MESSAGE_MIRROR['CHK-005']('CE-BJ-02', 'eth0/1'))).toBe(
      '接口 CE-BJ-02/eth0/1 尚未配置 IPv4 地址',
    );
  });
});

// ---------------------------------------------------------------------------
// 负排：锚定之外一律失配（null = 调用方回退英文原文），证明逐字锚定
// ---------------------------------------------------------------------------

describe('translateCheckMessage 负排：非 Go 格式失配回退（正则是 ^…$ 逐字锚定）', () => {
  it.each([
    ['CHK-001', 'duplicate host address 10.0.0.7 used by 3 interfaces.', '尾句号'],
    ['CHK-001', 'duplicate host address 10.0.0.7 used by 3 interface', '复数变形'],
    ['CHK-001', ' Duplicate host address 10.0.0.7 used by 3 interfaces', '前导空格/措辞'],
    ['CHK-001', 'warning: duplicate host address 10.0.0.7 used by 3 interfaces', '整句嵌前缀（钉 ^ 锚）'],
    ['CHK-002', 'subnets 10.0.0.0/24 and 10.0.0.9/24 overlap (identical) extra', '尾部多余'],
    ['CHK-003', 'device PE-1 is isolated (no uplinks)', '括号内措辞'],
    ['CHK-003', 'note: device PE-1 is isolated (no up links) — see topology', '整句嵌前后缀（钉 ^ $ 锚）'],
    ['CHK-004', 'component of 4 devices disconnected from the main component', '漏 is'],
    ['CHK-005', 'interface CE-1/eth0 has no IPv6 address', 'IPv6 变体'],
    ['CHK-005', 'interface CE-1/eth0 has no IPv4 address, configure it first', '整句嵌后缀（钉 $ 锚）'],
  ] as [SimCheckCode, string, string][])('%s 措辞漂移（%s）→ null 回退', (code, message) => {
    expect(translateCheckMessage(code, message)).toBeNull();
  });

  it('基线 ok 行与其余自由文本 → null（run 全通过时的占位不进翻译）', () => {
    expect(translateCheckMessage('CHK-001', 'ok')).toBeNull();
    expect(translateCheckMessage('CHK-003', '')).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// CHECK_CATALOG 目录闭集（chip 展示面：5 编码齐全、标题/解释非空）
// ---------------------------------------------------------------------------

describe('CHECK_CATALOG 目录（编码 → 中文标题/解释）', () => {
  const ALL_CODES: SimCheckCode[] = ['CHK-001', 'CHK-002', 'CHK-003', 'CHK-004', 'CHK-005'];

  it('与 SimCheckCode 闭集逐项一致（无缺项无多项）', () => {
    expect(Object.keys(CHECK_CATALOG).sort()).toEqual([...ALL_CODES].sort());
  });

  it.each(ALL_CODES)('%s：title/hint 非空中文且 entry.code 回指自身', (code) => {
    const entry = CHECK_CATALOG[code];
    expect(entry.code).toBe(code);
    expect(entry.title.trim().length).toBeGreaterThan(0);
    expect(entry.hint.trim().length).toBeGreaterThan(0);
  });
});
