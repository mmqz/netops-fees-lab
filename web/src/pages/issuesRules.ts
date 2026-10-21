/**
 * issuesRules.ts — /issues 规则筛选选项的派生常量（#109-B）。
 *
 * RULE_OPTIONS 一律从 i18n RULE_LABELS 派生，禁止维护第二份手抄清单：
 * 修复前此处（当时内联在 Issues.tsx）手抄 10 项，漏了 W004（身份键退化）/
 * W005（属性变更待确认）/ W006（疑似改名待确认）——后端这三条规则真实
 * 产 issue，规则过滤器却无法选中它们。
 *
 * 闭集对齐由两层钉死：
 * - 编译期（tsc -b，本目录在 tsconfig.app include 内）：RULE_LABELS 的
 *   声明类型是 Record<RuleCode, string>——types.ts 增删规则码而 i18n 未
 *   同步时立即编译失败（缺键 TS2739 / 多键 TS2353），因此
 *   Object.keys(RULE_LABELS) 在运行期恒等于 RuleCode 全集；
 * - 运行期（issuesRules.test.ts）：闭集对齐断言——RULE_OPTIONS ==
 *   RULE_LABELS 键集 == types.ts RuleCode 的 13 项字面镜像，另含
 *   W004/W005/W006 的直接回归钉。
 */

import type { RuleCode } from '../api/types';
import { RULE_LABELS } from '../i18n';

/** 规则过滤器选项（RULE_LABELS 书写序，与后端规则目录规范序一致）。 */
export const RULE_OPTIONS: RuleCode[] = Object.keys(RULE_LABELS) as RuleCode[];
