/**
 * issuesRules.test.ts — 规则筛选闭集对齐断言（#109-B 验收标准）。
 *
 * 修复前 RULE_OPTIONS 是手抄 10 项清单；types.ts 的 RuleCode 演进到 13 项、
 * i18n RULE_LABELS 同步到 13 项后，筛选器漂移——W004/W005/W006 三条在产
 * 规则无法过滤。本文件把三处事实源钉成一个闭集：
 *
 *   RULE_OPTIONS（筛选器） == RULE_LABELS 键集（i18n） == RuleCode 全集
 *
 * 第三方「RuleCode 全集」在本测试里以字面镜像形式存在——未来在 types.ts
 * 新增规则码时，长度断言先失败（14 ≠ 13），提醒同步这里的镜像与 i18n
 * 文案（i18n 缺键已被 Record<RuleCode, string> 类型在编译期挡住）。
 */
import { describe, expect, it } from 'vitest';
import type { RuleCode } from '../api/types';
import { RULE_LABELS } from '../i18n';
import { RULE_OPTIONS } from './issuesRules';

/** types.ts RuleCode 联合的运行期字面镜像（闭集对齐的第三方事实源）。 */
const ALL_RULE_CODES: RuleCode[] = [
  'E001',
  'E002',
  'E003',
  'E004',
  'E005',
  'E006',
  'W001',
  'W002',
  'W003',
  'W004',
  'W005',
  'W006',
  'I001',
];

describe('RULE_OPTIONS 闭集对齐（#109-B：手抄 10 项漏 W004/W005/W006 的回归钉）', () => {
  it('长度 == RULE_LABELS 键数 == RuleCode 项数（当前 13）', () => {
    expect(RULE_OPTIONS).toHaveLength(ALL_RULE_CODES.length);
    expect(Object.keys(RULE_LABELS)).toHaveLength(ALL_RULE_CODES.length);
    expect(RULE_OPTIONS).toHaveLength(13);
  });

  it('与 RULE_LABELS 键集逐项一致且保序（派生关系成立，非第二份手抄）', () => {
    expect([...RULE_OPTIONS]).toEqual(Object.keys(RULE_LABELS));
  });

  it('与 types.ts RuleCode 闭集一致（E×6 + W×6 + I001，无缺项无多项）', () => {
    expect(new Set(RULE_OPTIONS)).toEqual(new Set(ALL_RULE_CODES));
  });

  it('后端在产规则 W004/W005/W006 均可选（本缺陷的直接回归钉）', () => {
    for (const code of ['W004', 'W005', 'W006'] as RuleCode[]) {
      expect(RULE_OPTIONS).toContain(code);
    }
  });

  it('每个选项都有非空中文标签（过滤器不出现裸码无文案行）', () => {
    for (const code of RULE_OPTIONS) {
      expect(RULE_LABELS[code].trim().length).toBeGreaterThan(0);
    }
  });
});
