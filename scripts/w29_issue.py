#!/usr/bin/env python3
"""w29_issue.py — v0.8.0 里程碑追踪 issue 创建 + 验收证据回帖 + 关闭。

PAT 运行时从 ~/.git-credentials 读取（不入库）。
"""
import json
import urllib.request

TOKEN = open('/home/z/.git-credentials').read().strip().split(':')[2].split('@')[0]
AUTH = {'Authorization': 'token ' + TOKEN, 'Accept': 'application/vnd.github+json'}
BASE = 'https://api.github.com/repos/mmqz/netops-lab'

TITLE = 'v0.8.0 真实公开数据里程碑：演示数据集以互联网公开数据填充（OSM + OpenCelliD）'
BODY = """用户指令（2026-10-13）：「继续做下个阶段直到完成，数据用互联网公开数据填充，完全找不到的用模拟数据」。

**范围**
- 数据源实测与裁决：OSM（ODbL）/ OpenCelliD（CC BY-SA 4.0）内嵌；高德 POI 不内嵌（ToS 再分发限制，ADR-0015）；RIPE Atlas 无 HLJ 探针（性能维持模拟）
- `scripts/realdata/` 确定性管线（extract / workbooks / metrics）
- `testdata/realdata/`：31 工作簿 1,965 行全 33 类型，真实/模拟逐字段披露（data-sources.md + manifest.json）
- 地理视图两个新离线图层（光缆干线 / 无线基站，双坐标变体）
- `scripts/demo-real.sh` 一键全链路 + 浏览器级回归

**验收标准**
1. 提取管线确定性：双跑 md5 一致
2. 31 工作簿导入零失败、计数钉死（e2e 门禁）
3. 浏览器级图层回归全绿（14 断言）
4. 全量门禁全绿（429 顶层测试）
5. 版本四处事实源 v0.8.0 + 发布（tag + Release 双资产）
"""

EVIDENCE = """**验收证据（2026-10-13，linux/amd64，W26-W29 四轮完成）**

1. ✅ **确定性**：`extract.py` 连续两次运行三产物 md5 一致（hlj-cables/hlj-basestations/realdata-core，gzip mtime=0 + 坐标量化 + 全序）。
2. ✅ **导入零失败**：31 工作簿 1,965 行 imported / 0 failed；`internal/app/e2e_realdata_test.go` 门禁钉死逐簿计数（base_station 1198/site 83/...）+ 491 关系 + OC-HDQ 影响链（customer/leased_line/service/circuit 全可达）。
3. ✅ **浏览器回归 14/14**：`scripts/w28_browser_regression.sh`——图层存在（cable-line/cable-line-rail/cable-label/basestation-pts）/源加载/画布渲染要素 14 条/面板新分组/开关初始态/切换 visibility=none/3D 保持/console 零错误。
4. ✅ **全量门禁**：gofmt 空 / vet 0 / `go test` 23 包 429 顶层全绿（427+2）/ -race app+webui 0 race / tsc 0 错 / build 5.2s / health 实测 v0.8.0。
5. ✅ **发布**：tag v0.8.0 + Release（source 21.8MB + linux binary 37.0MB 双资产已上传）；演示四脚本零回归（demo.sh E13W3I1 / demo-lab.sh 哈希 aea27501fa59 / demo-real.sh 全链路 / demo-geo-lab.sh）。

**数据构成速览**
- 真实：83 局站（OSM node id 身份）/ 1,198 基站（OpenCelliD 主小区 CGI 身份，844 NR）/ 7 条干线走廊（绥满高速 G10·滨洲线·嫩林线·京漠线 G111 真实几何）/ 光缆锚点链测地里程
- 模拟（披露）：机房/纤芯占用/波道/电路/IP/客户/业务——`docs/data-sources.md` 与 manifest.json 双处披露；高德 POI 不内嵌（ADR-0015）

CI：run 38373827971 failure = 账户 Actions-minutes 配额耗尽（job 3s/0 步骤，基础设施问题非代码，与 v0.6.0/v0.7.0 同况）；本地门禁集与 CI 同集全绿，已在 verified-capabilities.md 如实记录。"""


def call(method: str, path: str, payload: dict | None = None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=AUTH, method=method)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())


issue = call('POST', '/issues', {'title': TITLE, 'body': BODY})
num = issue['number']
print('created issue #%d: %s' % (num, issue['html_url']))
call('POST', f'/issues/{num}/comments', {'body': EVIDENCE})
print('evidence comment posted')
call('PATCH', f'/issues/{num}', {'state': 'closed'})
print('closed #%d' % num)
