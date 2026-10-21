/**
 * import-mapping.ts — 数据导入向导第②步「映射确认」的纯函数（#111-A，
 * 从 ImportWizard.tsx 抽出；先例：#109 的 issuesRules.ts / labTabs.ts）。
 *
 * 四个函数原先困在页面模块里零测试，其中 buildMapping 构造 POST
 * /api/imports/{id}/mapping 的请求体——keyColumns（身份键硬契约：后端按
 * keyColumns 拼接行身份，前端由 schema.ts naturalKeyFields 派生，禁止
 * 手抄）与请求体形状（import 项五字段 / skip 项仅两字段）由
 * import-mapping.test.ts 表测钉死。
 *
 * 依赖方向：api/types（契约形状）+ schema（字段规格唯一事实源）+ i18n
 * （错误文案模板）——零 React、零 DOM，可在 node 环境直接表测。
 */

import type {
  ImportAnalysisResponse,
  MappingAction,
  MappingModel,
  ResourceType,
  Severity,
  SheetAnalysis,
} from '../api/types';
import { IMP_ERR_REQUIRED_UNMAPPED, format } from '../i18n';
import { fieldLabel, fieldsForType, naturalKeyFields, requiredFields } from '../schema';

/** 向导第②步的工作表草稿（页面可编辑状态；buildMapping 的输入）。 */
export interface SheetDraft {
  sheet: string;
  action: MappingAction;
  resourceType: ResourceType;
  /** 目标字段 → 源表头。 */
  columnMapping: Record<string, string>;
}

/** 第②步提交前的必填校验错误（按工作表归组展示）。 */
export interface SheetError {
  sheet: string;
  message: string;
}

/** 规则代码前缀 → 严重级别（与 internal/domain/rules.go 的映射一致）。 */
export function severityForRule(code: string): Severity {
  if (code.startsWith('E')) return 'error';
  if (code.startsWith('W')) return 'warning';
  return 'info';
}

/** 未提供后端建议映射时，按列建议（confidence>0 且字段属于该类型）预填。 */
export function prefillFromColumns(sheet: SheetAnalysis, type: ResourceType): Record<string, string> {
  const valid = new Set<string>(fieldsForType(type).map((f) => f.field));
  const mapping: Record<string, string> = {};
  for (const column of sheet.columns) {
    if (column.suggestedField !== '' && column.confidence > 0 && valid.has(column.suggestedField)) {
      mapping[column.suggestedField] = column.header;
    }
  }
  return mapping;
}

/** 上传分析响应 → 各工作表初始草稿（后端建议优先，缺省按列建议预填）。 */
export function buildDrafts(analysis: ImportAnalysisResponse): SheetDraft[] {
  return analysis.sheets.map((sheet) => {
    const suggested = analysis.suggestedMapping.sheets.find((m) => m.sheet === sheet.sheet);
    const type: ResourceType = suggested?.resourceType ?? sheet.suggestedType ?? 'device';
    const action: MappingAction =
      suggested?.action ?? (sheet.suggestedType !== undefined ? 'import' : 'skip');
    const columnMapping: Record<string, string> =
      suggested !== undefined &&
      suggested.action === 'import' &&
      suggested.resourceType === type &&
      suggested.columnMapping !== undefined
        ? { ...suggested.columnMapping }
        : prefillFromColumns(sheet, type);
    return { sheet: sheet.sheet, action, resourceType: type, columnMapping };
  });
}

/**
 * 草稿 → POST /api/imports/{id}/mapping 请求体。
 * import 项：{sheet, action, resourceType, keyColumns, columnMapping}——
 * keyColumns 恒等于该类型的自然键字段（schema.ts naturalKeyFields 派生，
 * 身份键硬契约）；skip 项：{sheet, action} 仅两字段（后端 omitempty 语义）。
 */
export function buildMapping(drafts: SheetDraft[]): MappingModel {
  return {
    sheets: drafts.map((draft) =>
      draft.action === 'import'
        ? {
            sheet: draft.sheet,
            action: 'import',
            resourceType: draft.resourceType,
            keyColumns: naturalKeyFields(draft.resourceType),
            columnMapping: draft.columnMapping,
          }
        : { sheet: draft.sheet, action: 'skip' },
    ),
  };
}

/** 提交前校验：每个 import 工作表的必填字段必须已映射（skip 项不检查）。 */
export function collectErrors(drafts: SheetDraft[]): SheetError[] {
  const errors: SheetError[] = [];
  for (const draft of drafts) {
    if (draft.action !== 'import') continue;
    for (const spec of requiredFields(draft.resourceType)) {
      if (draft.columnMapping[spec.field] === undefined) {
        errors.push({
          sheet: draft.sheet,
          message: format(IMP_ERR_REQUIRED_UNMAPPED, {
            sheet: draft.sheet,
            field: fieldLabel(spec.field),
          }),
        });
      }
    }
  }
  return errors;
}
