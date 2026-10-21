// @vitest-environment jsdom
/**
 * labTabs.test.ts — /lab 标签白名单派生 + sessionStorage 往返（#109-C）。
 *
 * 缺陷场景：TABS 含 7 项（含 layers 图层标签），白名单手抄只列 6 项——
 * 切到图层标签后刷新页面，sessionStorage 里存着 'layers' 却被白名单拒绝，
 * 静默回退「设备」标签。修复 = 白名单从 TABS 派生；本文件用「全标签
 * 写读往返」钉死：TABS 里每个标签都必须能被 write→read 原样取回。
 */
import { beforeEach, describe, expect, it } from 'vitest';
import { LAB_TAB_VALUES, readPersistedTab, TABS, writePersistedTab } from './labTabs';

/** 持久化键格式（与 labTabs.ts 实现一致，按键隔离到工作台）。 */
const key = (id: string): string => `netops-lab.tab.${id}`;

describe('标签白名单从 TABS 派生（#109-C：消灭手抄）', () => {
  it('白名单 == TABS 值集（单一事实源，非第二份清单）', () => {
    expect(LAB_TAB_VALUES).toEqual(TABS.map((tab) => tab.value));
  });

  it('含 layers 图层标签（本缺陷的直接回归钉）', () => {
    expect(LAB_TAB_VALUES).toContain('layers');
  });

  it('七个标签全在白名单（无缺项）', () => {
    expect(LAB_TAB_VALUES).toHaveLength(7);
    expect(new Set(LAB_TAB_VALUES).size).toBe(7); // 且无重复
  });
});

describe('readPersistedTab / writePersistedTab — sessionStorage 往返（jsdom）', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  it('TABS 中每个标签写入后都能原样读回（刷新保活契约——白名单漏项在这里现形）', () => {
    for (const tab of TABS.map((item) => item.value)) {
      writePersistedTab('ws', tab);
      expect(readPersistedTab('ws')).toBe(tab); // 修复前 'layers' 会回退 'devices'
    }
  });

  it('无记录回退默认「设备」标签', () => {
    expect(readPersistedTab('fresh')).toBe('devices');
  });

  it('白名单外的脏值（旧版本残留/手改）回退默认，不抛错', () => {
    sessionStorage.setItem(key('dirty'), 'bogus');
    expect(readPersistedTab('dirty')).toBe('devices');
    sessionStorage.setItem(key('dirty2'), '');
    expect(readPersistedTab('dirty2')).toBe('devices');
  });

  it('按工作台隔离：各自记录互不串台', () => {
    writePersistedTab('ws-a', 'layers');
    writePersistedTab('ws-b', 'checkpoints');
    expect(readPersistedTab('ws-a')).toBe('layers');
    expect(readPersistedTab('ws-b')).toBe('checkpoints');
  });
});
