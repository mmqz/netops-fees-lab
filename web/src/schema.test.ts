import { describe, expect, it } from 'vitest';
import { TYPE_SPECS, fieldLabel, fieldsForType, naturalKeyFields, requiredFields } from './schema';

// schema.test.ts — #96: this module is the hand-mirrored projection of
// GET /api/schema (the mapping engine's contract). A drift here once shipped
// (issue #66's narrative sweep found the older frozen-count prose lying) —
// these tests pin the projection's own invariants so the mirror's STRUCTURE
// cannot rot even if a field is added.

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
