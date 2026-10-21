import { describe, expect, it } from 'vitest';
import {
  LAYER_ID_NAMES,
  TYPE_META,
  TYPE_SPECS,
  fieldLabel,
  fieldsForType,
  layerIdName,
  naturalKeyFields,
  requiredFields,
} from './schema';
import { RESOURCE_TYPE_LABELS } from './i18n';

// schema.test.ts — #96: this module is the hand-mirrored projection of
// GET /api/schema (the mapping engine's contract). A drift here once shipped
// (issue #66's narrative sweep found the older frozen-count prose lying) —
// these tests pin the projection's own invariants so the mirror's STRUCTURE
// cannot rot even if a field is added.
// #113-B 追加：TYPE_META 裁决为前端层名的单一手抄面（静态镜像，与后端无契约
// 耦合、漂移以视觉评审为界），层号索引与 GraphAnalysisTab 的层 chip 均从其
// 派生——此处钉死派生闭集，禁止第二份手抄再生长。

describe('TYPE_SPECS 注册表自洽', () => {
  it('每个注册类型都有 ≥1 个自然键字段（导入身份键是硬契约）', () => {
    for (const type of Object.keys(TYPE_SPECS)) {
      const keys = naturalKeyFields(type as keyof typeof TYPE_SPECS);
      expect(keys.length, `${type} 必须有自然键`).toBeGreaterThan(0);
    }
  });
  it('每个注册类型都有必填字段', () => {
    for (const type of Object.keys(TYPE_SPECS)) {
      expect(requiredFields(type as keyof typeof TYPE_SPECS).length, `${type} 必填字段`).toBeGreaterThan(0);
    }
  });
  it('port 是双自然键（device_name + port_name，migration v8 契约）', () => {
    expect(naturalKeyFields('port')).toEqual(['device_name', 'port_name']);
  });
});

describe('fieldsForType / requiredFields', () => {
  it('返回该类型的规格子集（自然键字段 ∈ 全字段）', () => {
    const deviceFields = fieldsForType('device').map((f) => f.field);
    expect(deviceFields.length).toBeGreaterThan(3);
    for (const key of naturalKeyFields('device')) {
      expect(deviceFields).toContain(key);
    }
  });
});

describe('fieldLabel', () => {
  it('已知字段 → 中文名（FIELD_LABELS 为 snake_case 键）', () => {
    expect(fieldLabel('device_code')).toBe('设备编码');
  });
  it('未知字段 → 原样回退（不猜、不吞）', () => {
    expect(fieldLabel('definitely_not_a_field')).toBe('definitely_not_a_field');
  });
});

// ---------------------------------------------------------------------------
// #113-B：层名单一手抄面——TYPE_META 派生闭集 + GraphAnalysisTab 对齐钉
// ---------------------------------------------------------------------------

/** 以 ?raw 形式内联全部源码（i18n.test.ts 同款探测器基建）。 */
const RAW_SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

describe('TYPE_META 层序（#113-B：单一手抄面 + 派生闭集）', () => {
  it('33 类型键集与 RESOURCE_TYPE_LABELS 完全一致（闭集总数）', () => {
    expect(Object.keys(TYPE_META).sort()).toEqual(Object.keys(RESOURCE_TYPE_LABELS).sort());
    expect(Object.keys(TYPE_META)).toHaveLength(33);
  });
  it('层号索引从 TYPE_META 派生，恰覆盖 L0-L6 七层（无缺无多）', () => {
    expect(Object.keys(LAYER_ID_NAMES).map(Number).sort((a, b) => a - b)).toEqual([0, 1, 2, 3, 4, 5, 6]);
  });
  it('七层名逐层字面钉死（与后端 mapping.LayerName 同词形）', () => {
    expect(LAYER_ID_NAMES[0]).toBe('空间层');
    expect(LAYER_ID_NAMES[1]).toBe('物理层（设备）');
    expect(LAYER_ID_NAMES[2]).toBe('物理层（光缆）');
    expect(LAYER_ID_NAMES[3]).toBe('传输层');
    expect(LAYER_ID_NAMES[4]).toBe('接入层');
    expect(LAYER_ID_NAMES[5]).toBe('逻辑层');
    expect(LAYER_ID_NAMES[6]).toBe('业务层');
  });
  it('33 类型逐一：layerName ≡ 所属层的派生名（层内不自相矛盾）', () => {
    for (const [type, meta] of Object.entries(TYPE_META)) {
      expect(
        meta.layerName,
        `${type} 的 layerName 应与层 ${meta.layer} 的派生名一致`,
      ).toBe(LAYER_ID_NAMES[meta.layer]);
    }
  });
  it('layerIdName：已知层「L{n} {层名}」、未知层回退「L{n}」（与后端 LayerName 数字回退同语义）', () => {
    expect(layerIdName(0)).toBe('L0 空间层');
    expect(layerIdName(4)).toBe('L4 接入层');
    expect(layerIdName(6)).toBe('L6 业务层');
    expect(layerIdName(99)).toBe('L99');
  });
  it('旧第二份手抄的中点分隔词形已从源码清除（raw 扫描，测试文件除外）', () => {
    // GraphAnalysisTab 收敛前用「·」分隔的漂移词形；规范词形是后端的
    // 全角括号（物理层（设备）/物理层（光缆）），中点形态再出现即第二份手抄回潮。
    const offenders: string[] = [];
    for (const [key, content] of Object.entries(RAW_SOURCES)) {
      if (key.includes('.test.')) continue;
      if (content.includes('物理层·')) offenders.push(key);
    }
    expect(offenders, '含漂移层名词形的文件').toEqual([]);
  });
  it('GraphAnalysisTab 消费 layerIdName（接线钉：层 chip 不再本地下拉）', () => {
    const graphTab = Object.entries(RAW_SOURCES).find(([key]) =>
      key.endsWith('/GraphAnalysisTab.tsx'),
    );
    expect(graphTab, '扫描器应能找到 GraphAnalysisTab.tsx').toBeDefined();
    expect(graphTab?.[1]).toContain("from '../schema'");
    expect(graphTab?.[1]).toContain('layerIdName(');
  });
});
