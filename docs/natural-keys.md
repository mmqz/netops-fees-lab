# NetOps Lab — 各资源类型自然键规范（natural keys）

> 本文档回答一个核心问题：**「IP 可能会变，分配的基站名称应该是不变的？究竟什么做主键？」**（R4 专题审查的正式落地）。
> 结论先行：方向一半对——「用稳定标识做身份、绝不用 IP」完全正确；但「名称」只是**次优**选择，**系统编号/编码类标识才是最佳**。分层裁决见 §1–§2，逐类型策略见 §4。

## 1. 分层身份模型（现状即为此架构）

资源身份分**三层**，各司其职、互不替代：

| 层 | 载体 | 作用 | 稳定性 |
| --- | --- | --- | --- |
| **存储主键** | UUID 代理键 `res_x`（`resources.id`，`domain.NewID` 生成） | 数据库物理主键；`relations`、`source_refs`、`issues`、`virtual_devices` **全部锚定它** | 永不变（代理键无业务含义，改业务键/改名不破坏任何关系） |
| **业务自然键** | `UNIQUE (resource_type, natural_key)`（`resources` 表约束） | **导入幂等判重键**：同键再导 → 行 skipped，零变更；批内同键 → E001 行 failed | 理论应稳定——键的选型即本文档主题 |
| **展示名** | `*_name` 属性字段（schema `NameField`） | 人读名称（资源详情 / 画布标签 / 报告） | 可变（改名不影响身份与关系） |

**裁决**：这是「代理键 PK + 自然键 UNIQUE」的正确两层底座，**保持不变**。任何自然键策略调整都不需要动主键与关系锚——这正是该架构的价值：port 换键（§4，migration v8）已实际验证了这一点，relations/SourceRef/代理键零改动。

## 2. 稳定性排序与变更语义

真实电信表格中身份字段稳定性（R4 勘察结论）：

**编号 / 编码 ＞ 名称 ＞ IP**

- **IP 最差**：重划 / 迁移 / 割接即变；四类真实表里 IP 全部以可变属性列出现。**IP 一律降级为可变属性，永不作身份。**
- **名称次优**：人定但资产生命周期内通常稳定（运维实践靠名称定位：PTP 配置模板用接口名、专线表 A 端名称跨表引用）；风险是重命名运动、跨地市同名、昵称截断。
- **编号最佳**：系统签发、全局唯一、不变——机房ID / 局站ID / 专线编号 / 基站编码列在真实表中确实存在。

**两类变更事件的语义（目标态）**：

| 事件 | 语义 | 处置原则 |
| --- | --- | --- |
| **IP 变**（同一资源的属性变化） | **属性变更事件** | 命中同一资源（靠稳定自然键），产生 diff + 变更历史记录。**已落地（v0.4 W4-D）**：diff 逐字段记入 `resource_attribute_changes` 待确认记录（W005 issue + 行仍 skip），人工经 HTTP API/CLI apply 后生效，dismiss 可忽略（详见 [import-and-validation.md](import-and-validation.md)） |
| **名称变**（疑似改名/新资源） | **身份变更事件** | **人工确认**，绝不自动合并：确认改名 → 更新 natural_key 并保留代理键；确认是另一资源 → 各自独立。**已落地（v0.5 W6-D）**：名称类键类型的导入行命中新键且属性与某「旧键已从源消失」的资源全等时，行照常按新资源导入并发 W006 + pending 改名提案；人工经 HTTP API / CLI（`renames` 子命令）apply 合并身份（关系改挂旧资源、新资源删除、旧资源换键），reject 维持独立（详见 [import-and-validation.md](import-and-validation.md) §16） |

## 3. 唯一性作用域规则

- **编号类自然键全局唯一**（专线编号、机房ID、局站ID、设备编码）：无需附加作用域。
- **名称类自然键必须复合作用域 `(地市, 名称)`**：真实表普遍携带地市/区县列，数据按地市维护，「动力机房2」「源1」这类通用名称全局必重名。名称键类型若未映射地市列，应在导入时提示作用域风险（v0.3.2 起导入向导已提示）。

## 4. 每类型自然键策略表

### 已实现类型（封闭集 = device / ip_pool / circuit / port / base_station / site / machine_room / ptp_port，schema v10/v13）

| 资源类型 | 自然键（现状） | 状态与裁决 |
| --- | --- | --- |
| device 设备 | `device_code`（设备编码） | ✅ 正确——真实表设备编码是系统签发标识；`mgmt_ip` 只是普通属性列。设备层已做对 |
| ip_pool 地址池 | `pool_name` | ✅ 可用；地址池为系统自建实体，名称即编号 |
| circuit 专线/电路 | `circuit_code`（电路编号） | ✅ 正确——对应真实专线表的**专线编号**列（如 `MSTHL1060715`）、集团STN编号等系统编号 |
| **port 端口** | 复合键 `device_name + port_name`（`|` 拼接落库，如 `B2\|bvi2.1008`） | ✅ **已换键落地（migration v8）**：键的设备侧锚定**设备名称**（真实端口表无设备编码列，设备名称是唯一现成稳定列）——设备重划 IP 后同一物理端口不再分裂。`device_ip` 保持 ipv4 **值列**（必填、非 device ref、非自然键）的既有决策不变：端口工作簿自含导出，避免 E003 拒整表；且自 v8 起作为普通属性落入 attributes。存量迁移语义：旧库（v7 及以前）port 行无 device_name 属性可取（旧 schema 无该字段），fallback = 旧键的 device_ip 分量 → **旧键原样保留**（诚实保数据，不发明设备名）；重新导入同一工作簿会以新键入库（若需清理旧行，回滚其批次即可）。冲突策略：目标键被多行认领或被保留行占用时，涉事行一律保留原键（测试钉死） |
| **base_station 基站** | **条件键**：主键 `base_station_code`（基站编码，全局唯一）；**任一主键值空 → 兜底 `(city, base_station_name)` 按序拼接、跳过空值**（无地市列则退化为纯名称） | ✅ **已落地（v0.4 W2-D，schema v9）**：真实无线基站表（样例 `samples/telecom-base-station.xlsx`）的基站编码列存在但值为空——编码是非必填主键，缺失时键退化为地市+名称兜底，**行照常导入并发 W004 治理 issue**（evidence 含所用键与缺失字段）。编码行与名称兜底行天然异键（E001/幂等判重都在解析后的最终键上）。基站IP/网关IP/归属A IP 全部为可变属性（基站IP 真实值带 /30 掩码，故为 string 值列，非身份）。机制通用：`TypeSpec.KeyFallbackFields` 按 spec 声明驱动，base_station 是首个使用者 |
| **site 局站** | `site_id`（局站ID，12 位数字编码，全局唯一） | ✅ **已落地（v0.4 W3-D，schema v10）**：真实机房局站表（样例 `samples/telecom-site-machine-room.xlsx`）同一张 sheet 的局站读法。**键为 KindString 而非数字类型**：数字编码是系统签发的数字串，前导零是身份的一部分（数字解析会丢失前导零导致身份分裂），且编码无任何算术语义。局站名称/设备名称为昵称（展示名/属性），IP地址为可变属性。局站是纯容器：无 ref 字段、不派生关系 |
| **machine_room 机房** | `machine_room_id`（机房ID，17 位数字编码，全局唯一）；同表可选 ref 字段 `site_id`（局站ID，非键） | ✅ **已落地（v0.4 W3-D，schema v10）**：同一张 sheet 的机房读法（双类型导入故事：先局站后机房）。键同为 KindString（前导零同理）。`site_id` 为**可选 ref**：映射后按 E003 既有语义解析（局站须先入库，否则行失败），解析成功自动建 `machine_room --belongs_to--> site` 关系；不映射则导入成功零关系（每行 W001 孤儿警告，同 ip_pool.assigned_device 语义）。机房名称为展示名，设备名称为机房内设备昵称（属性非 ref——设备键是 device_code，本表自由文本无法解析），IP地址为可变属性 |
| **ptp_port PTP 端口** | 复合键 `device_model + port_name`（`|` 拼接，如 `6150\|gei-1/1/0/7`、`6130XG-S\|gei_1/7`） | ✅ **已落地（v0.5 W8-D，schema v13）**：真实中兴 VRP 配置脚本（样例 `samples/ptp-config-script.txt`）经脚本导入通道解析为虚拟 sheet 后走既有映射/导入管线。设备型号取自脚本标题行（「6150配置脚本」→ 6150）——**模型级模板身份**（脚本不携带设备实例身份，实例级关联需脚本未提供的数据，诚实声明）；接口名两种真实形态（`gei-1/1/0/7` 连字符 / `gei_1/7` 下划线）均按原文入库。名称类复合键 → 开启 `RenameDetection`（W006 改名检测适用）。无 ref 字段、不派生关系。`device_model`/`port_name` 同为名称类键分量，教义同 port：IP 一律不出现（脚本无 IP），改名走人工确认 |

### 规划类型

（已空——机房/局站两行已随 v0.4 W3-D 落地并移入上表；后续新类型的键策略在引入时仍须先在此文档登记裁决。）

**改名检测适用面（v0.5 W6-D 起）**：`mapping.TypeSpec.RenameDetection` 仅对**名称类键**类型开启——ip_pool（pool_name）、base_station（兜底键含名称）、port（复合键的名称分量）三类。编号类键类型（device/circuit/site/machine_room）刻意不开启：编号是系统签发的身份，编号变了按教义就是另一资源（属性变化走 W005 属性变更通道）。检测五条件与确认流语义见 [import-and-validation.md](import-and-validation.md) §16。

存储主键对**所有类型**维持 UUID 代理键 `res_x` 不变。

## 5. 复合键的拼接规则（实现口径）

自然键为多字段复合时，按 `mapping.TypeSpec` 字段序收集键值并以 `|` 连接（`importer/execute.go`）；单键类型 join 恒等原值（device / ip_pool / circuit 行为与哈希不受影响）。示例值：port 的 `B2|bvi2.1008`（真实样例：设备名称 B2 + 端口名 bvi2.1008）。

**条件键（v0.4，base_station）**：主自然键字段值**全非空** → 键 = 主键字段拼接（现行机制，逐字节不变）；**任一为空** → 键 = `KeyFallbackFields` 兜底字段值按序拼接、**跳过空值**（无地市列的表退化为纯名称）。E001 批内去重与 `GetByTypeKey` 库内判重都在**解析后的最终键**上判定，因此编码行与名称兜底行是两个身份。兜底键的空值跳过与 port 复合键的 `|` 拼接共享同一理论碰撞面（值含 `|`），接受同一裁决（治理型表格不构造此类值）。

## 6. 现状差距（诚实声明）与路线图

~~现状导入路径是 Insert-only（`ResourceRepo` 无 upsert），由此产生的差距~~ 前四项已全部闭环（#1/#2 随 v0.4 W4-D、#3 随 v0.5 W6-D、#4 随 v0.5 W7-D）：

1. ~~**同键再导 → 整行 skip，属性变更静默丢弃**~~（如设备重划 IP 后同 device_code 再导，新 IP 永远写不进库）→ **已闭环**：同键再导的属性 diff 逐字段记为待确认变更（W005），行仍 skip，人工 apply 后属性才真正生效；同值再导仍纯 skip（零 issue，幂等不变；source_refs 谱系版本除外，见 #4）；
2. ~~**无属性变更历史表**——「IP 变 = 属性变更事件」没有落点~~ → **已闭环**（migration v11 的 `resource_attribute_changes`，status 生命周期 pending→applied/dismissed）；
3. ~~**无改名人工流**（「同属性不同键」的疑似改名检测与确认）~~ → **已闭环**（v0.5 W6-D，migration v12 的 `rename_proposals` + W006 检测 + apply/reject 人工流，键与关系语义见 §2 名称变行）；
4. ~~**SourceRef 无版本化**：同资源再导入被 skip，来源引用停留在首个批次~~ → **已闭环**（v0.5 W7-D，零迁移）：skip 即版本——再导入（纯 skip 与 W005 diff-skip 两形态）对每批每【resource】实体追加一条 source_refs（**仅资源**：关系行的 refs 恒为创建批——E003 解析只发生在插入路径；UNIQUE(entity_kind, entity_id, batch_id) 天生每批一行；E001 批内重复到不了 skip 检查点不追加），资源详情 API 的 sourceRefs[] 按 rowid 版本序展示全史（创建批次在前），回滚随批消失（batch_id ON DELETE CASCADE）；详见 [import-and-validation.md](import-and-validation.md) §17。

均为已识别差距，非隐性缺陷。~~名称改名的人工确认流（#3）与 SourceRef 版本化（#4）待后续排期~~ 四项差距已全部闭环（#1/#2：v0.4 W4-D；#3：v0.5 W6-D；#4：v0.5 W7-D）。~~port 换键迁移（migration v8）~~ 已落地（v8 就地重算 natural_key，冲突保留原键，详见 §4）。

## 7. UI 落地（v0.3.2 起生效；migration v8 后 port 提示升级）

- **导入向导（ImportWizard）映射确认页**：源列被映射到 **IP 类自然键**时显示告警——「IP 属可变属性，重划后同键资源将分裂」（migration v8 后四类均无 IP 自然键，该告警为防御性保留）；所选类型自然键含**名称类字段**（如 `pool_name`）时显示轻提示——名称键需保证唯一作用域，建议 `(地市 + 名称)` 复合作键（R4 §4.5 短期项）。
- **port 专属正向提示（migration v8 起）**：port 类型改为显示「端口键 = 设备名称|端口名称：设备重划 IP 不再影响资源身份（设备IP 为普通值列）」——通用名称作用域文案对 port 复合键不再合适（键已固定，无从追加地市分量），改展示换键后的稳定性语义。
- **基站条件键提示（v0.4 起）**：base_station 显示「基站键 = 基站编码（全局唯一）；编码为空时退化为 地市|无线基站名称 兜底键（缺地市列则为纯名称），并记录 W004 治理问题（行照常导入）」——主键是 code，不命中名称键启发式，提示分支独立判定（spec 声明了 KeyFallbackFields 且兜底含名称字段）。
- **引用字段提示（v0.4 W3-D 起）**：列被映射到 **ref 类字段**（机房的 局站ID→site_id、地址池的 归属设备→assigned_device、电路的 A/Z端 等）时，该列旁显示正向提示「引用列 · 需先导入目标类型」（完整文案说明：按被引用类型的自然键解析、E003 依赖导入顺序先局站后机房、该列可不映射则不建立关系）——展示提示，不改映射逻辑。

- **疑似改名面板（v0.5 W6-D 起）**：资源详情抽屉在「待确认属性变更」面板之下新增「疑似改名待确认」面板——展示旧键 → 新键、一致属性数、批次/行号证据，附「确认改名 / 拒绝」按钮（与 W4 面板同款行内错误与忙态；无 pending 提案不渲染）。**任一侧资源抽屉均渲染**（v0.5 W11-D 起：`resource_id` 过滤匹配提案任一侧——旧资源=身份保留方与新资源两侧都能看到确认入口；W006 issue 的 EntityID 落在新资源，深链打开新资源抽屉时面板同样可见）。确认后旧资源的自然键换为新键而资源 ID 不变。

## 8. 相关文档

- [docs/data-model.md](data-model.md) —— `resources` DDL（`id TEXT PRIMARY KEY` + `UNIQUE (resource_type, natural_key)`）与 v7/v8 迁移；
- [docs/import-and-validation.md](import-and-validation.md) —— 幂等 / skip-duplicate / E001 的行为细节，及 §17 来源引用版本化（本节 #4 的落地语义）；
- [samples/README.md](../samples/README.md) —— 真实样例的授权与披露（本规范的数据依据之一）。
