/**
 * client.test.ts — 全局错误面与响应归一化表测（#111-C 验收标准）。
 *
 * formatApiError / formatApiErrorText 是所有页面错误展示的统一入口
 * （FX2-4），normalizeImportResult 是 POST /imports/{id}/mapping 响应的
 * 唯一归一化点——三者此前全局使用、零测试。本文件表测钉死：
 * - 错误信封路由：错误码 → 中文主题行；英文技术原文保留为 detail 次行；
 *   中文后端消息原样作为主题行（不再套翻译壳）；非 ApiError 兜底。
 * - formatApiErrorText 的拼接形态（主题（detail）全角括号单行）。
 * - normalizeImportResult 的两种形态（§9 契约形态 / importer.Summary 扁平
 *   兼容形态）与缺省回退（batchId=''、status='completed'、计数 0）。
 */
import { describe, expect, it } from 'vitest';
import {
  ERR_API_CHECKPOINT_MISSING_FILE,
  ERR_API_CONFLICT,
  ERR_API_INVALID_REQUEST,
  ERR_API_NOT_LATEST,
  ERR_API_NOT_FOUND,
  ERR_API_OP_FAILED,
  ERR_BAD_RESPONSE,
  ERR_NETWORK,
} from '../i18n';
import { ApiError, formatApiError, formatApiErrorText, normalizeImportResult } from './client';

// ---------------------------------------------------------------------------
// formatApiError — 错误码路由（FX2-4 全局错误面）
// ---------------------------------------------------------------------------

describe('formatApiError（错误信封 → 展示面）', () => {
  it('network_error → 网络失败主题行、无次行（fetch 抛不出更多信息）', () => {
    expect(formatApiError(new ApiError(0, 'network_error', ERR_NETWORK))).toStrictEqual({
      title: ERR_NETWORK,
      detail: null,
    });
  });

  it('bad_response → 格式异常主题行、无次行', () => {
    expect(formatApiError(new ApiError(500, 'bad_response', ERR_BAD_RESPONSE))).toStrictEqual({
      title: ERR_BAD_RESPONSE,
      detail: null,
    });
  });

  it.each([
    ['invalid_request', ERR_API_INVALID_REQUEST],
    ['op_failed', ERR_API_OP_FAILED],
    ['not_found', ERR_API_NOT_FOUND],
    ['not_latest', ERR_API_NOT_LATEST],
    ['conflict', ERR_API_CONFLICT],
    ['checkpoint_missing_file', ERR_API_CHECKPOINT_MISSING_FILE],
  ] as const)('空后端消息 + 已知码 %s → 码表主题行、无次行', (code, title) => {
    const display = formatApiError(new ApiError(400, code, ''));
    expect(display.title).toBe(title);
    expect(display.detail).toBeNull();
  });

  it('英文技术原文 + 已知码 → 码表主题行 + 原文保留为次行（技术信息不丢）', () => {
    const err = new ApiError(400, 'invalid_request', 'bad payload', 'field "year" out of range');
    expect(formatApiError(err)).toStrictEqual({
      title: ERR_API_INVALID_REQUEST,
      detail: 'field "year" out of range',
    });
  });

  it('英文技术原文 + 未知码（http_* 等）→ err.message 兜底主题行 + 原文次行', () => {
    const err = new ApiError(503, 'http_503', 'gateway down', 'upstream timeout');
    expect(formatApiError(err)).toStrictEqual({ title: 'gateway down', detail: 'upstream timeout' });
  });

  it('中文后端消息 → 原样作为主题行（不再套翻译壳）、无次行', () => {
    const err = new ApiError(409, 'conflict', 'conflict', '该编码已被其他资源占用');
    expect(formatApiError(err)).toStrictEqual({ title: '该编码已被其他资源占用', detail: null });
  });

  it('后端消息恰为网络失败文案时视为空 → 回退 err.message 主题行（FX-1 前后兼容）', () => {
    const err = new ApiError(502, 'http_502', ERR_NETWORK, ERR_NETWORK);
    expect(formatApiError(err)).toStrictEqual({ title: ERR_NETWORK, detail: null });
  });

  it('非 ApiError：Error 用 message、其他值 String() 兜底', () => {
    expect(formatApiError(new Error('boom'))).toStrictEqual({ title: 'boom', detail: null });
    expect(formatApiError(42)).toStrictEqual({ title: '42', detail: null });
    expect(formatApiError(null)).toStrictEqual({ title: 'null', detail: null });
  });
});

// ---------------------------------------------------------------------------
// formatApiErrorText — 纯文本拼接形态
// ---------------------------------------------------------------------------

describe('formatApiErrorText（form-error 单行形态）', () => {
  it('无次行 → 仅主题行', () => {
    expect(formatApiErrorText(new ApiError(0, 'network_error', ERR_NETWORK))).toBe(ERR_NETWORK);
  });

  it('有次行 → 主题行（原文）全角括号拼接', () => {
    const err = new ApiError(400, 'invalid_request', 'bad payload', 'field "year" out of range');
    expect(formatApiErrorText(err)).toBe('请求参数无效，操作被拒绝（field "year" out of range）');
  });

  it('非 ApiError → message 原样', () => {
    expect(formatApiErrorText(new Error('boom'))).toBe('boom');
  });
});

// ---------------------------------------------------------------------------
// normalizeImportResult — POST mapping 响应归一化（契约/兼容双形态）
// ---------------------------------------------------------------------------

describe('normalizeImportResult（导入执行结果归一化）', () => {
  it('非对象（null/字符串/数字/数组）→ null（调用方按坏响应处理）', () => {
    expect(normalizeImportResult(null)).toBeNull();
    expect(normalizeImportResult('ok')).toBeNull();
    expect(normalizeImportResult(42)).toBeNull();
    expect(normalizeImportResult([1, 2])).toBeNull();
  });

  it('契约形态：{batchId,status,summary{rows*},issues} 原样归一', () => {
    const result = normalizeImportResult({
      batchId: 'b-1',
      status: 'completed',
      summary: { rowsTotal: 10, rowsImported: 8, rowsSkipped: 1, rowsFailed: 1 },
      issues: { error: 1, warning: 2, info: 3 },
    });
    expect(result).toStrictEqual({
      batchId: 'b-1',
      status: 'completed',
      summary: { rowsTotal: 10, rowsImported: 8, rowsSkipped: 1, rowsFailed: 1 },
      issues: { error: 1, warning: 2, info: 3 },
    });
  });

  it('兼容形态：importer.Summary 扁平结构 + issuesBySeverity（rows* 平铺于根）', () => {
    const result = normalizeImportResult({
      batchId: 'b-2',
      status: 'completed',
      rowsTotal: 5,
      rowsImported: 5,
      rowsSkipped: 0,
      rowsFailed: 0,
      issuesBySeverity: { error: 0, warning: 1 },
    });
    expect(result).toStrictEqual({
      batchId: 'b-2',
      status: 'completed',
      summary: { rowsTotal: 5, rowsImported: 5, rowsSkipped: 0, rowsFailed: 0 },
      issues: { error: 0, warning: 1, info: 0 },
    });
  });

  it('缺省回退：batchId→空串、status→completed、rows*→0、issues 缺失→空计数对象', () => {
    const result = normalizeImportResult({});
    expect(result).toStrictEqual({
      batchId: '',
      status: 'completed',
      summary: { rowsTotal: 0, rowsImported: 0, rowsSkipped: 0, rowsFailed: 0 },
      issues: {},
    });
  });

  it('Go map 编码省略零值键：issues 只含非零键 → 其余回退 0', () => {
    const result = normalizeImportResult({ summary: {}, issues: { warning: 2 } });
    expect(result?.issues).toStrictEqual({ error: 0, warning: 2, info: 0 });
  });

  it('非有限数字（字符串/NaN）不采信 → 回退 0', () => {
    const result = normalizeImportResult({ summary: { rowsTotal: '10', rowsImported: Number.NaN } });
    expect(result?.summary).toStrictEqual({ rowsTotal: 0, rowsImported: 0, rowsSkipped: 0, rowsFailed: 0 });
  });

  it('issues 键优先于 issuesBySeverity（契约形态赢过兼容形态）', () => {
    const result = normalizeImportResult({
      issues: { error: 7 },
      issuesBySeverity: { error: 1 },
    });
    expect(result?.issues.error).toBe(7);
  });
});
