# v0.4.0–v0.6.2 发布断档：用户仍在 v0.3.0，修复从未送达

## 现象（事实核查，2026-10-12）
- 远端：tag 已至 v0.6.0，main 已至 v0.6.2（多轮地图/运维修复）。
- GitHub Releases：最新只有 **v0.3.0**（2026-10-12 之前发布）+ mapdata 资产包。
- 结果：用户拿到并运行的一直是 **v0.3.0 二进制**——其对「聚簇/地形/
  比例」的抱怨在 v0.3.0 上全部字面成立（无聚簇、无 DEM、fit 粗）。
  v0.6.1/v0.6.2 的地图修复**从未送达用户**。

## 根因
里程碑收口只做了 tag 推送，没有执行 Release 发布步骤（也无 CI 发布
workflow——账户 Actions 配额受限，见 v0.6.0 CI 记录）。「代码已合并」
被误当成「修复已交付」。

**连带发现（v0.6.3 打包时拦下）**：v0.6.2 给 Makefile `build`/`build-testdatagen`
加 `BIN_EXT`（issue #3 修复）时配方行被写成 8 空格而非 TAB——`make build`
自 v0.6.2 起 `missing separator` 失败；因门禁直跑 go test/tsc 不经过 make，
`bin/` 一直是 v0.6.1 旧二进制。即便有人手工打 release，产物也会是旧货。
已修复（配方转 TAB），并把发布验证固化为解包实测 `/api/health` 版本与资产字节数。

## 修复（v0.6.3）
- 一站式发布：tag v0.6.3 + GitHub Release（`netops-lab-v0.6.3-linux-amd64.tar.gz`
  二进制 + `netops-lab-v0.6.3-source.tar.gz` 源码包，内嵌 DEM/等高线全量
  dist）；发布后核对 `/api/health` 实报 v0.6.3 与资产清单。
- 流程纪律（ADR-0012 D4）：里程碑收口 = tag + Release + health 版本核对，
  三步缺一不算交付；**截屏仅存 repo `tmp/` 作回归证据，永不进入
  download/ 与 release 资产**（用户明确要求）。

## 教训
用户报告「仍然没有 X」时，第一步先核对**用户实际运行的版本**——
发布断档会让多个真实修复同时「看起来失效」。
