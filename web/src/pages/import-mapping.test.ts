/**
 * import-mapping.test.ts — 数据导入映射纯函数表测（#111-A 验收标准）。
 *
 * 修复前 buildMapping / buildDrafts / collectErrors / severityForRule 困在
 * ImportWizard.tsx 里零测试。本文件钉死三件事：
 *
 * 1. keyColumns 身份键硬契约（buildMapping 的核心）：对全部 ResourceType，
 *    请求体里 import 项的 keyColumns 恒等于 schema.ts naturalKeyFields(type)
 *    的派生结果（后端按 keyColumns 拼接行身份——前端手抄或漏派生即静默
 *    串键）；port 复合键（device_name|port_name，migration v8）另以字面
 *    值钉死。
 * 2. POST /api/imports/{id}/mapping 请求体形状：import 项五字段 / skip 项
 *    仅两字段（无 resourceType/keyColumns/columnMapping 泄漏），以
 *    JSON.stringify 字面串钉死（字段改名/增删即红）。
 * 3. buildDrafts 的后端建议优先级与 prefillFromColumns / collectErrors 的
 *    边界行为（置信度、必填校验文案）。
 */
import { describe, expect, it } from 'vitest';
import type {
  ColumnAnalysis,
  ImportAnalysisResponse,
  ResourceType,
  SheetAnalysis,
} from '../api/types';
import { RESOURCE_TYPE_LABELS } from '../i18n';
import { naturalKeyFields, requiredFields } from '../schema';
import {
  buildDrafts,
  buildMapping,
  collectErrors,
  prefillFromColumns,
  severityForRule,
} from './import-mapping';
import type { SheetDraft } from './import-mapping';

// ---------------------------------------------------------------------------
// 夹具构造（最小完备，只填被测函数实际读取的字段）
// ---------------------------------------------------------------------------

function column(header: string, suggestedField: string, confidence: number): ColumnAnalysis {
  return { header, index: 1, samples: [], suggestedField, confidence };
}

function sheet(name: string, columns: ColumnAnalysis[], suggestedType?: ResourceType): SheetAnalysis {
  return { sheet: name, rowCount: columns.length, skippedEmptyRows: 0, columns, suggestedType };
}

function analysis(sheets: SheetAnalysis[], suggestedSheets: ImportAnalysisResponse['suggestedMapping']['sheets']): ImportAnalysisResponse {
  return {
    batchId: 'batch-1',
    workbookName: 'wb.xlsx',
    sha256: 'deadbeef',
    sheets,
    suggestedMapping: { sheets: suggestedSheets },
  };
}

function draft(sheet: string, type: ResourceType, columnMapping: Record<string, string>, action: 'import' | 'skip' = 'import'): SheetDraft {
  return { sheet, action, resourceType: type, columnMapping };
}

// ---------------------------------------------------------------------------
// severityForRule — 规则代码前缀 → 严重级别（internal/domain/rules.go 镜像）
// ---------------------------------------------------------------------------

describe('severityForRule（规则码前缀 → 严重级别）', () => {
  it.each([
    ['E001', 'error'],
    ['E006', 'error'],
    ['W001', 'warning'],
    ['W006', 'warning'],
    ['I001', 'info'],
    ['X001', 'info'],
    ['', 'info'],
  ] as const)('%s → %s', (code, expected) => {
    expect(severityForRule(code)).toBe(expected);
  });

  it('小写前缀不算错误/警告（startsWith 区分大小写，回退 info）', () => {
    expect(severityForRule('e001')).toBe('info');
    expect(severityForRule('w001')).toBe('info');
  });
});

// ---------------------------------------------------------------------------
// prefillFromColumns — 列建议预填
// ---------------------------------------------------------------------------

describe('prefillFromColumns（列建议预填）', () => {
  it('命中列（confidence>0 且字段属于该类型）→ 目标字段=源表头', () => {
    const result = prefillFromColumns(
      sheet('S1', [
        column('设备编码列', 'device_code', 0.9),
        column('设备名称列', 'device_name', 0.5),
      ]),
      'device',
    );
    expect(result).toEqual({ device_code: '设备编码列', device_name: '设备名称列' });
  });

  it.each([
    ['置信度 0 的建议不预填', column('列A', 'device_code', 0)],
    ['不属于该类型的字段不预填', column('列B', 'cidr', 0.9)],
    ['空 suggestedField 不预填', column('列C', '', 0.9)],
  ])('%s', (_name, col) => {
    expect(prefillFromColumns(sheet('S1', [col]), 'device')).toEqual({});
  });

  it('多列建议同一目标字段：后列覆盖前列（循环序即最后写入者）', () => {
    const result = prefillFromColumns(
      sheet('S1', [column('列一', 'device_code', 0.9), column('列二', 'device_code', 0.8)]),
      'device',
    );
    expect(result).toEqual({ device_code: '列二' });
  });

  it('零列工作表 → 空映射（不抛错）', () => {
    expect(prefillFromColumns(sheet('S1', []), 'device')).toEqual({});
  });
});

// ---------------------------------------------------------------------------
// buildDrafts — 分析响应 → 初始草稿（后端建议优先）
// ---------------------------------------------------------------------------

describe('buildDrafts（初始草稿）', () => {
  it('后端建议（import + 类型 + 列映射）原样采纳，且列为副本（改草稿不串分析响应）', () => {
    const suggested = { sheet: 'S1', action: 'import' as const, resourceType: 'device' as const, keyColumns: ['device_code'], columnMapping: { device_code: '编码' } };
    const [d] = buildDrafts(analysis([sheet('S1', [column('编码', 'device_code', 0.9)])], [suggested]));
    expect(d).toEqual({ sheet: 'S1', action: 'import', resourceType: 'device', columnMapping: { device_code: '编码' } });
    expect(d.columnMapping).not.toBe(suggested.columnMapping);
  });

  it('建议为 import 但缺列映射 → 按列建议预填', () => {
    const [d] = buildDrafts(
      analysis([sheet('S1', [column('编码', 'device_code', 0.9), column('未知', 'cidr', 0.9)])], [
        { sheet: 'S1', action: 'import', resourceType: 'device' },
      ]),
    );
    expect(d.columnMapping).toEqual({ device_code: '编码' });
  });

  it('建议为 import 但缺 resourceType → 回退 suggestedType 且列映射走预填（键型不确定不采纳）', () => {
    const [d] = buildDrafts(
      analysis([sheet('S1', [column('编码', 'device_code', 0.9)], 'device')], [
        { sheet: 'S1', action: 'import', columnMapping: { device_code: '编码' } },
      ]),
    );
    expect(d.resourceType).toBe('device');
    expect(d.columnMapping).toEqual({ device_code: '编码' }); // 预填结果恰同建议，但路径是 prefill
  });

  it('建议为 skip → 草稿 skip（类型取建议值，列映射仍预填备用）', () => {
    const [d] = buildDrafts(
      analysis([sheet('S1', [column('编码', 'device_code', 0.9)])], [
        { sheet: 'S1', action: 'skip', resourceType: 'device' },
      ]),
    );
    expect(d.action).toBe('skip');
    expect(d.resourceType).toBe('device');
    expect(d.columnMapping).toEqual({ device_code: '编码' });
  });

  it('无建议但有 suggestedType → import + 该类型', () => {
    const [d] = buildDrafts(analysis([sheet('S1', [column('编码', 'device_code', 0.9)], 'device')], []));
    expect(d).toEqual({ sheet: 'S1', action: 'import', resourceType: 'device', columnMapping: { device_code: '编码' } });
  });

  it('无建议且无 suggestedType → skip + 默认 device（无法识别类型的工作表）', () => {
    const [d] = buildDrafts(analysis([sheet('S1', [column('杂', 'unknown', 0.9)])], []));
    expect(d).toEqual({ sheet: 'S1', action: 'skip', resourceType: 'device', columnMapping: {} });
  });

  it('多工作表顺序保持（分析响应序即草稿序）', () => {
    const drafts = buildDrafts(
      analysis(
        [sheet('A', [column('编码', 'device_code', 0.9)], 'device'), sheet('B', [])],
        [{ sheet: 'B', action: 'skip' }],
      ),
    );
    expect(drafts.map((d) => d.sheet)).toEqual(['A', 'B']);
  });
});

// ---------------------------------------------------------------------------
// buildMapping — POST 请求体（keyColumns 身份键硬契约 = 本文件最重的一组）
// ---------------------------------------------------------------------------

describe('buildMapping（POST /api/imports/{id}/mapping 请求体）', () => {
  it('keyColumns 契约：全部 ResourceType 的 import 项 keyColumns == naturalKeyFields(type)（派生，非手抄）', () => {
    for (const type of Object.keys(RESOURCE_TYPE_LABELS) as ResourceType[]) {
      const mapping = buildMapping([draft('S1', type, {})]);
      expect(mapping.sheets[0].keyColumns, `type=${type}`).toEqual(naturalKeyFields(type));
      // 身份键恒非空——空键列表会让后端行身份拼接退化为全行
      expect(mapping.sheets[0].keyColumns!.length, `type=${type}`).toBeGreaterThan(0);
    }
  });

  it('port 复合键字面钉死：device_name|port_name（migration v8 设备侧锚定）', () => {
    const mapping = buildMapping([draft('S1', 'port', { device_name: '设备', port_name: '端口' })]);
    expect(mapping.sheets[0].keyColumns).toEqual(['device_name', 'port_name']);
  });

  it('fiber 复合键字面钉死：cable_code|fiber_no（光缆层双键）', () => {
    expect(buildMapping([draft('S1', 'fiber', {})]).sheets[0].keyColumns).toEqual(['cable_code', 'fiber_no']);
  });

  it('import 项五字段 / skip 项仅两字段（omitempty 形状，无键泄漏）', () => {
    const mapping = buildMapping([
      draft('导入表', 'device', { device_code: '编码' }),
      draft('跳过表', 'site', {}, 'skip'),
    ]);
    expect(mapping.sheets[0]).toStrictEqual({
      sheet: '导入表',
      action: 'import',
      resourceType: 'device',
      keyColumns: ['device_code'],
      columnMapping: { device_code: '编码' },
    });
    expect(mapping.sheets[1]).toStrictEqual({ sheet: '跳过表', action: 'skip' });
  });

  it('请求体 JSON 形状字面钉死（字段改名/增删即红）', () => {
    const body = JSON.stringify(
      buildMapping([draft('设备表', 'device', { device_code: '编码', device_name: '名称' }), draft('其他', 'site', {}, 'skip')]),
    );
    expect(body).toBe(
      '{"sheets":[' +
        '{"sheet":"设备表","action":"import","resourceType":"device","keyColumns":["device_code"],"columnMapping":{"device_code":"编码","device_name":"名称"}},' +
        '{"sheet":"其他","action":"skip"}' +
        ']}',
    );
  });

  it('草稿顺序 → 请求体顺序（与后端按序消费一致）', () => {
    const mapping = buildMapping([draft('B', 'device', {}), draft('A', 'device', {}, 'skip')]);
    expect(mapping.sheets.map((s) => s.sheet)).toEqual(['B', 'A']);
  });

  it('keyColumns 每次调用都是新数组（草稿复用不共享引用）', () => {
    const first = buildMapping([draft('S1', 'device', {})]);
    const second = buildMapping([draft('S1', 'device', {})]);
    expect(first.sheets[0].keyColumns).not.toBe(second.sheets[0].keyColumns);
  });
});

// ---------------------------------------------------------------------------
// collectErrors — 提交前必填校验
// ---------------------------------------------------------------------------

describe('collectErrors（必填字段校验）', () => {
  it('skip 工作表不校验（空映射也不报错）', () => {
    expect(collectErrors([draft('S1', 'device', {}, 'skip')])).toEqual([]);
  });

  it('import 工作表缺必填 → 报错，文案为 i18n 模板 + 字段中文名（字面钉死）', () => {
    const errors = collectErrors([draft('设备表', 'device', {})]);
    expect(errors).toEqual([
      { sheet: '设备表', message: '工作表「设备表」：必填字段「设备编码」尚未映射' },
      { sheet: '设备表', message: '工作表「设备表」：必填字段「设备名称」尚未映射' },
    ]);
  });

  it('缺部分必填 → 只报缺失项（requiredFields 序）', () => {
    const errors = collectErrors([draft('S1', 'device', { device_code: '编码' })]);
    expect(errors).toEqual([{ sheet: 'S1', message: '工作表「S1」：必填字段「设备名称」尚未映射' }]);
  });

  it('全部必填已映射 → 无错误', () => {
    const type: ResourceType = 'device';
    const columnMapping: Record<string, string> = {};
    for (const spec of requiredFields(type)) columnMapping[spec.field] = `列:${spec.field}`;
    expect(collectErrors([draft('S1', type, columnMapping)])).toEqual([]);
  });

  it('多工作表：错误按草稿序、字段序归组排列', () => {
    const errors = collectErrors([
      draft('S1', 'device', { device_code: '编码' }),
      draft('S2', 'device', {}),
    ]);
    expect(errors.map((e) => e.sheet)).toEqual(['S1', 'S2', 'S2']);
    expect(errors[0].message).toContain('设备名称');
    expect(errors[1].message).toContain('设备编码');
  });
});
