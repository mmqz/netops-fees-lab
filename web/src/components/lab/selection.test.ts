import { describe, expect, it } from 'vitest';
import { isValidIpv4, validateIpv4Pair } from './selection';

// selection.test.ts — #96: the module doc said "仓库无 vitest 运行器，不新
// 增测试" long after vitest shipped; the IPv4 pair validator is the input
// gate of TWO dialogs (OpsDialog + InterfaceConfigDialog) and its prefix
// parse drifted from the Go netip.ParsePrefix semantics it claims to mirror.

describe('isValidIpv4（与 Go netip.ParseAddr 对齐）', () => {
  it('合法形态', () => {
    expect(isValidIpv4('10.0.0.1')).toBe(true);
    expect(isValidIpv4('0.0.0.0')).toBe(true);
    expect(isValidIpv4('255.255.255.255')).toBe(true);
  });
  it('本函数不 trim（trim 是 validateIpv4Pair 的前置职责）', () => {
    expect(isValidIpv4('10.0.0.1 ')).toBe(false);
    expect(validateIpv4Pair(' 10.0.0.1 ', ' 24 ')).toBeNull(); // pair 层统一 trim
  });
  it('前导零拒绝（"0" 合法，"01" 非法——AUD2-2）', () => {
    expect(isValidIpv4('01.0.0.1')).toBe(false);
    expect(isValidIpv4('10.010.0.1')).toBe(false);
  });
  it('段界/结构拒绝', () => {
    expect(isValidIpv4('256.0.0.1')).toBe(false);
    expect(isValidIpv4('10.0.0')).toBe(false);
    expect(isValidIpv4('10.0.0.1.2')).toBe(false);
    expect(isValidIpv4('a.b.c.d')).toBe(false);
    expect(isValidIpv4('')).toBe(false);
  });
});

describe('validateIpv4Pair（#96 前缀解析漂移修复）', () => {
  it('均空 → 合法；只填其一 → BOTH', () => {
    expect(validateIpv4Pair('', '')).toBeNull();
    expect(validateIpv4Pair('10.0.0.1', '')).not.toBeNull();
    expect(validateIpv4Pair('', '24')).not.toBeNull();
  });
  it('合法对', () => {
    expect(validateIpv4Pair('10.0.0.1', '24')).toBeNull();
    expect(validateIpv4Pair('10.0.0.1', '0')).toBeNull();
    expect(validateIpv4Pair('10.0.0.1', '32')).toBeNull();
  });
  it('地址非法 → ADDR', () => {
    expect(validateIpv4Pair('999.1.1.1', '24')).not.toBeNull();
  });
  it('前缀域外 → PREFIX（<0、>32）', () => {
    expect(validateIpv4Pair('10.0.0.1', '33')).not.toBeNull();
    expect(validateIpv4Pair('10.0.0.1', '100')).not.toBeNull();
  });
  it('#96 漂移类全部拒绝：Number() 曾放行的十六进制/指数/带符号/小数', () => {
    expect(validateIpv4Pair('10.0.0.1', '0x1')).not.toBeNull(); // Number→1 曾放行
    expect(validateIpv4Pair('10.0.0.1', '1e1')).not.toBeNull(); // Number→10 曾放行
    expect(validateIpv4Pair('10.0.0.1', '+24')).not.toBeNull();
    expect(validateIpv4Pair('10.0.0.1', '-24')).not.toBeNull();
    expect(validateIpv4Pair('10.0.0.1', '2.5')).not.toBeNull();
    expect(validateIpv4Pair('10.0.0.1', 'abc')).not.toBeNull();
  });
  it('十进制字面量含前导零仍按值解析（Go strconv.Atoi 语义）', () => {
    expect(validateIpv4Pair('10.0.0.1', '024')).toBeNull(); // 24
  });
});
