/**
 * 仿真检查目录（W3，AUD-4 P1-6）：
 * - 引擎检查消息是英文（internal/sim/checks.go 的 fmt.Sprintf 输出），
 *   本文件按 CHK-001..005 的确定性消息格式解析并转成中文（demo 的
 *   「关键时刻」不再跳英文）；未命中回退英文原文；
 * - catalog() 提供编码 → {标题, 解释}，用于 chip 的 title 提示；
 * - 纯函数、零依赖 API 形状。
 */

import type { SimCheckCode } from '../../api/types';
import {
  SIM_CHECK_HINTS,
  SIM_CHECK_LABELS,
  SIM_MSG_DUP_ADDR,
  SIM_MSG_ISOLATED,
  SIM_MSG_OVERLAP,
  SIM_MSG_PARTITION,
  SIM_MSG_UNCONFIGURED,
  SIM_OVERLAP_KIND_LABELS,
  format,
} from '../../i18n';

export interface CheckCatalogEntry {
  code: SimCheckCode;
  /** 中文标题（主行）。 */
  title: string;
  /** 中文解释（tooltip）。 */
  hint: string;
}

/** 编码 → 目录条目（含中文标题与解释）。 */
export const CHECK_CATALOG: Record<SimCheckCode, CheckCatalogEntry> = {
  'CHK-001': { code: 'CHK-001', title: SIM_CHECK_LABELS['CHK-001'], hint: SIM_CHECK_HINTS['CHK-001'] },
  'CHK-002': { code: 'CHK-002', title: SIM_CHECK_LABELS['CHK-002'], hint: SIM_CHECK_HINTS['CHK-002'] },
  'CHK-003': { code: 'CHK-003', title: SIM_CHECK_LABELS['CHK-003'], hint: SIM_CHECK_HINTS['CHK-003'] },
  'CHK-004': { code: 'CHK-004', title: SIM_CHECK_LABELS['CHK-004'], hint: SIM_CHECK_HINTS['CHK-004'] },
  'CHK-005': { code: 'CHK-005', title: SIM_CHECK_LABELS['CHK-005'], hint: SIM_CHECK_HINTS['CHK-005'] },
};

/**
 * 引擎消息 → 中文。消息格式（internal/sim/checks.go）：
 * - CHK-001 "duplicate host address %s used by %d interfaces"
 * - CHK-002 "subnets %s and %s overlap (%s)"  kind ∈ identical/contains/partial
 * - CHK-003 "device %s is isolated (no up links)"
 * - CHK-004 "component of %d devices is disconnected from the main component"
 * - CHK-005 "interface %s/%s has no IPv4 address"
 * 未命中（引擎升级 / 基线 "ok" 行）→ null，调用方回退英文原文。
 */
export function translateCheckMessage(code: SimCheckCode, message: string): string | null {
  let m: RegExpExecArray | null;
  switch (code) {
    case 'CHK-001':
      m = /^duplicate host address (\S+) used by (\d+) interfaces$/.exec(message);
      if (m !== null) return format(SIM_MSG_DUP_ADDR, { addr: m[1], n: m[2] });
      break;
    case 'CHK-002':
      m = /^subnets (\S+) and (\S+) overlap \((\w+)\)$/.exec(message);
      if (m !== null) {
        return format(SIM_MSG_OVERLAP, {
          a: m[1],
          b: m[2],
          kind: SIM_OVERLAP_KIND_LABELS[m[3]] ?? m[3],
        });
      }
      break;
    case 'CHK-003':
      m = /^device (\S+) is isolated \(no up links\)$/.exec(message);
      if (m !== null) return format(SIM_MSG_ISOLATED, { code: m[1] });
      break;
    case 'CHK-004':
      m = /^component of (\d+) devices is disconnected from the main component$/.exec(message);
      if (m !== null) return format(SIM_MSG_PARTITION, { n: m[1] });
      break;
    case 'CHK-005':
      m = /^interface (\S+)\/(\S+) has no IPv4 address$/.exec(message);
      if (m !== null) return format(SIM_MSG_UNCONFIGURED, { device: m[1], name: m[2] });
      break;
  }
  return null;
}
