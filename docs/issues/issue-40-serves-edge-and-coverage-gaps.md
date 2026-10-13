# [P3] 演示数据覆盖缺口：serves 边缺失，2 类型 / 5 关系无数据

来源：[docs/audit-v0.8.0.md](../audit-v0.8.0.md) §2.3（本地库直查）

## 现象

独立导入 31 工作簿后直查数据库：

```
资源：1,965 行覆盖 31 类（域定义 33 类）→ 2 类无数据
关系：491 条覆盖 24 种（域定义 29 种）→ 5 种无数据
```

### 无数据的 2 类资源

```
ptp_port          需 PTP 配置脚本文本；realdata 未纳入
protection_group  保护组
```

### 无数据的 5 种关系

```
mounted_on    端口→板卡
protects      保护组→电路      （依赖 protection_group，连带缺失）
in_vpn        端口→VPN实例
serves        设备→客户        ← 关键缺口
uplink_to     基站→设备        ← B4 裁决的正确后果
```

## 影响分析

**`uplink_to` 缺失无需修**——这是设计决策（`design/data-model-v1.md` §12-B4）的正确后果：
从「归属A IP / 名称」自动派生关系违反项目既有红线
（「绝不从名称相似度推断关系」），故不派生。**属可接受状态。**

**`serves` 缺失值得关注**——它使「设备 → 直接服务的客户」这条边在演示数据中不通。
当前业务影响分析只能走：

```
device → circuit → service → customer（经 subscribed_by）
```

而设计 §4.2 中 `serves`（device|base_station → customer）本意是提供**直接**边。
缺它意味着：
- 「某设备故障影响哪些客户」需多跳推导，而非直连
- 「基站直接服务某客户」这类场景无法表达

实测影响面仍可达（我验证 OC-HDQ down 后 impacted=80，含 customer 4 / service 4），
故**不阻塞演示**，但业务链的完整性弱于设计预期。

## 建议

1. **补 `serves` 演示数据**（可为模拟值——真实客户关系本就涉密）——
   让「设备 / 基站 → 客户」直连边可演示
2. `protection_group` + `protects` 同批补（保护结构是故障分析的要点）
3. `ptp_port` 可复用既有 `samples/ptp-config-script.txt` 的解析通道补入
4. `mounted_on` / `in_vpn` 视需要，优先级低

## 验收

- [ ] `serves` 在演示数据中有数据（或明确记录「不提供」的理由）
- [ ] `manifest.json` 与 `docs/data-sources.md` 覆盖缺口如实披露
- [ ] 若补齐，跨层影响链出现直连「设备→客户」路径