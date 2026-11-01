# C7 B的schema、T4与R3边界

## 2026-11-01：最小 schema 实施授权

批准固定 `bd39df7fbd07841181417f35c054c9419a777460` 私有副本上的
[76 路径实现范围](2026-11-01-pack-configuration-schema-writeset.tsv)：45 个预计 Ent
生成路径与 31 个手写路径。范围只含 schema、nullable-parent 机械闭包、精确准入、
T1/T4 伴随证明；不是全部 P2、207 路径或可用 Pack 自动配置功能的完成声明。
实际生成出现额外路径或目录差异，须先核查再扩大范围。

采用已实证 SQL 候选的单表 device/consent、准确 parent union，以及 256 当前 consent /
1024 历史的计费建议。S0 保留 user_version=0，S1 保留 user_version=1；真实生成必须
重新证明完整目录与受保护投影，候选 SQL hash 不能直接当生产 Ent 目录资格。
正常升级保留账号、密码、密文、序列和已有授权；仅灾难恢复执行 S1 九 DML 撤权，
不改写 S0 八 DML 的旧收据。不用虚构 session 或零值 parent 模拟 policy 身份。

17 条 GUPGRADE、R3 发布/激活接点、CLI、proto、Web 和 LPK 不在本次写入许可中。
R3 S0 原实现继续独立推进，不能复制其活 WIP 或伪造接口。该片仅在私有副本实现，
主线合入前必须完成真实 Ent/SQLite 验证、非作者审查及与用户可执行升级链的串行整合；
不得因 schema 已生成就自动迁移生产库或宣布 S1 已可升级，Pack 设备能力仍关闭。

批准输入：`SCHEMA-MINIMUM-REQUEST.tsv` SHA256
`87ba4b42e8d7ae8e702c97583f847d7af19781b264142f59dad822ddaa76b09c`；
`SCHEMA-S1-CANDIDATE.md` SHA256
`a7f01d8199d69ad0b9dfa14e4b5671d666cc3effa466774d0e5e7684a9e978dd`。
下面保留原设计边界；本授权不把历史“未执行”记录改成通过。

### 实际生成后的单路径追加

真实 Ent 生成还修改 `internal/server/ent/ent.go`：仅新增 policy 元数据包 import
及 `ValidColumn` 表注册，共 +2/-0。核对实际差异后批准该路径，当前实施范围为
77 路径（46 个生成路径、31 个手写路径）。随附 TSV 追加该路径，保留上面的
原始 76 路径申请及 SHA256，不追改原申请身份。实际差异证据
`05-extra-ent-go.diff` SHA256 为
`7e758dec52404f16e7f2ae36174f4b99103c1b14b6b33b8b9f5b4ffa072822f2`。
本次追加不扩大其它许可，也不表示实现、迁移或验收已完成。

本文件是需主协调的必要设计，不修改T4/R3 freeze，不宣称现有sanitize已覆盖C7，也不借新WIP符号。

## 1. 明确版本，不以“有新表就行”替代exact catalog

| family | 数据库/record合同 | 准入 |
| --- | --- | --- |
| S0 archive25f9 | 27个Ent entity；旧native challenge/session只有Required session parent；已有strict catalog原hash `34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235` / normalized `7e56e095b6943f47835a6a019ad99655b601224afa02726f2270312a5a404cfc`，92 total/90 application entries；SQLite默认user_version=0 | 作为固定旧family保留只读inspect与受权迁移/恢复；不是新设备authority |
| S1 c7b-policy-v1 | 新增一entity/一table；两旧native表准确parent union/Pack purpose；policy record_version=1，C7 wire=2；拟议user_version=1 | 新完整exact SQL+normalized hash pair、条目计数、CHECK/index/FK/types必须由批准后的真实Ent/SQLite输出计算冻结；**现在没有该hash，不能填伪值或开放泛化准入** |

主需在R3版本登记中确认user_version=1没有被其另用。若当前R3已占用，先统一分配再冻结schema，不边实现边覆盖；不是因冲突放弃B。S1版本号本身不是授权，必须同时匹配精确schema/hash、原key/policy proof、全部FK/域/密文/预算。S0当前snapshot函数没有user_version证明，增加版本检查需用真实旧库证明0；不把未核任意旧user_version当可迁移。

新schema没有per-target表，没有session/data复用存policy。新表CHECK区分device/consent；两短期表CHECK区分session/Pack-policy parent，NULL不等于0。Ent0.14.6模板和entsql.Checks支持为设计事实，不是已生成/已验证DDL。

## 2. 旧记录、启动与升级

- 旧普通native请求/响应、Claude/Codex/Gemini既有record/hash/AAD/profile tuple保持原义。迁移只给旧native行新增NULL Pack列，旧parent_session_id数值、purpose、TTL、token_hash、公钥、operation、ciphertext、charge及所有id/sequence原样；不创建device/consent、不把交互session变永久授权。
- 新程序读旧本机journal/备份按原版本；新Pack envelope有明确version2，未知version拒绝，不当空policy、不自动降级。A草稿证书不存在产品兼容义务，也不能作为B的导入权限。
- 现Store.Open在`client.Schema.Create`前没有exact schema分支。v2必须在它前面有schema preflight：fresh空库按S1创建；已知S1只验证；非空S0返回MigrationRequired；未知/mixed/tampered catalog拒绝。不能普通live SH进程悄悄DDL、修复索引或自动把source迁移。
- S0→S1在主协调的**私有candidate、EX隔离、无live writer**内进行，不改受认证原backup/source。需先真空/旧key验证、原exact S0、schema lock/FD身份，再使用明确migration事务；两旧表nullable parent改变可能触发SQLite table rebuild，必须验证精确DDL/索引/FK和逐行受保护投影，而非相信Ent AutoMigrate。
- DDL、新表/索引/CHECK、两旧表重建、user_version最后更新须一个经审阅的事务/迁移步骤；保留public/internal IDs、所有旧ciphertext/AAD身份、sqlite_sequence及其已存在合法高水位。不让外键off窗口泄漏为可发布库；commit、Rollback、pool/FD Close、Wait、Sync不明无迁移后能力。
- 迁移后fresh worker只读独立inspect S1、旧数据canonical投影相等、新policy表空、FK/integrity、原proof和实际hash均通过后，才可进入后继sanitize/发布阶段。不可把migration helper(*sql.Tx)当R3发布capability。
- 为S1增加的普通Store preflight只关乎DB schema，不改T3S key初始化/懒猫权限/kernel；不能引入名字allowlist或放宽root权限。

新二进制可按版本读取旧业务数据，**不保证旧二进制能安全写S1**：旧Store.Open可能自动Schema.Create，不能虚称旧binary已实现unknown-schema拒绝。发布需主R3/安装版本栅门阻止旧程序打开S1；回退走已验证S0备份/独立恢复，不是删policy表、改user_version或原地降列。

## 3. snapshot / T1 / MaxRecords的新责任

新schema family注册涉及local_snapshot_inventory、worker catalog/read、verify manifest和restore父/worker比较；所有入口必须选择同一S0/S1具体版本，不把常量替成“>=92/包含这些表即可”。原S0 fixture/原hash不覆盖，S1新建与迁移后分别留exact hash证据并比对。

S1相关域：新表全部id/revision/nullable字段/row_kind、三种native purpose的真实parent union、device→consent一层关系、recipient/owner/Pack/share外键和分区、public/device-key唯一性、ciphertext长度/charge/quota、各种revision/state/time组合。先完整域SQL证明再after=0、128分页，不能遗漏非正ID或只验当前页。全表证明不是把无关category合法域收紧；沿用原既有category/ent_types规则，不误拒。

policy/command/report全部使用已核原key独立AAD认证；report先长度再解密/typed校验，未知record version不默默清空。所有新增扫描先扣完整MaxRecords剩余预算，相关domain合计也计入上限；128是page size，不是total cap。公开有效范围(0,MaxInt64]保持，remaining+1检查不得溢出，也不得悄设1m cap。不能为了新表重新扫描一遍canonical技能引用，复用已有完整inventory证据。

旧manifest的TotalRecords仍是原canonical reference计数（含原Items恒等式），不是偷偷改成全部SQL行数。S1 worker内部另维护validation预算，将已核canonical与新增policy域按明确规则计费；不改旧公开计数字段含义，也不扩大为所有无关表全面审计。外层比较继续绑定真实schema/DB hash和原limits，不能只给新表另一个无限预算。

catalog/payload工作之前先worker CPU/内存/FD/时间资源控制。rows.Close/连接/worker Wait/父FD/identity/hash及cleanup不明优先普通取消；失败zero evidence。不能仅更新schema hash就让新表成为未经验证的旁路。

## 4. T4：S1明确九DML，不改写原八DML收据

S0保持原八DML版本。S1分支在旧四个native DELETE后、sessions DELETE前加入：

`DELETE FROM pack_configuration_policies`

因此**S1九DML**，真实事务只运行一次；先删native grants/preparations/challenges/sessions，再删全部device与consent（含mailbox/reports），再删普通sessions并做原share/MCP/GitHub撤权。即使正常issuer Logout不会撤B授权，恢复sanitize也必须主动删除B，不能依赖删sessions间接失效。

S1前置证明计入policy全部rows/charge/版本/AEAD/FK，撤销后至少证明：policy表0、原native/session全0、Pack shares统一撤销、MCP revision准确+1、GitHub需重授、foreign_key_check空。Protected projection只允许原八DML变更加**完整移除policy表数据**；Pack/资产/排序/引用/历史/key/policy proofs/其它ciphertext/sequence均原样。自增序列不重置，DELETE不是DROP或VACUUM。不能把新表排除后不再证明它确实清空。

外部T4 coordinator还须：新exact S1 inspect、准确projection白名单、新postcondition与实际原事务/worker计数、commit后物理排空/Sync/独立后inspect，最后才返回其真正Sanitized owner。仅改state helper不等于T4已升级；当前T4 freeze/八DML报告继续保留旧边界。

后继已批准输入：主确认T4提交推送a7d164cb48a2f156d395fdf3317e89186b2f2ba4；本轮只用git show读取其中三个已提交文件到approved-inputs，不重建25f9基线。其restoreRevokeProjection实际枚举catalog全部table，只跳过原native/session五表；若只扩大schema并删policy，旧projection会前后不等而保守失败。因此必须精确增加policy移除白名单及清空后验，而非声称T4旧freeze天然可用。

必须真实反例：恢复一份曾有active device+consent的旧快照，原masterkey/Pack pins都未变；sanitize后旧receipt、旧device key、旧短child、旧申请ticket全部不能重新材料化/登记，必须缺失行即拒绝。重新登录也不能重建旧权限；只有新enrollment+owner新授权+device新审批。去掉新增DELETE应出现行为RED，不是编译RED。原样恢复未sanitize不在承诺内。

## 5. R3：必须主协调，不越权写其freeze

主协调顺序建议：S0 authenticated candidate -> 独立EX migration -> 精确S1后验 -> S1九DML sanitize -> 原R3发布/activation/rollback。若主采用先sanitize S0再迁移，也必须证明迁移不生成authority且S1后验policy为空；冻结一种顺序，不能跨阶段把来源hash与迁移后hash混用。

R3原source inventory/DB digest不可覆盖。迁移数据库另有schema family/hash/digest，intent/恢复入口要绑定前后身份与实际完成状态；EX父死/进程退出业务fence仍按R3原门资格化，不从本设计推出已闭合。C7不定义一个绕过R3的Path→Apply或新增公开发布capability。

上段candidate→sanitize次序**仅适用于灾难恢复operation**。普通产品schema升级另走`UPGRADE-v2.md`：不调用T4八/九DML，不丢session/原账号/密码/密文/序列/旧授权；typed EX candidate→migration→独立S1验证→R3发布/实际App读回。`schema-upgrade`Go CLI/包装oneshot/worker/coordinator/publication adapter是明确GUPGRADE申请，不以SQL helper当产品，也不让MigrationRequired成为永久无法升级的终态。

`COORDINATION-v2.tsv`列出已知T4精确入口与R3必须提供的scope契约。R3尚未冻结的具体worker API/路径不能在本设计编造为已存在；主提供其批准freeze后，才补准确交集delta/字节边界再实施。所有schema/生成码/shared native hunk串行消费，没有私自“合起来差不多”。

## 6. 必须保留的测试和迁移证据

- S0→S1真实SQLite，所有旧字段/密文/id/sequence不变；new table空；旧普通native、fresh key/重启/失钥拒绝、双普通SH不回归。
- fresh S1与迁移S1的完整/normalized catalog一致；精确index/CHECK/FK验证，删除/篡改任一条拒绝。
- 故障在DDL/commit/Close/Wait/Sync各边界，无半库可发布、无自动重试；源archive不变。
- S1备份→完整候选→迁移/九DML sanitize→独立后验；去掉新增清空/parent union校验/全域检查分别真实RED。
- 所有合法MaxRecords边界、非正相关ID/错误FK/混parent/循环/跨owner/未知record、完整16附件/512refs/128页跨界；关闭不明/context优先级。
- 禁止全仓/18build代替以上行为；实施先定向CGO0测试/vet，主组合与非作者审在后。当前全部未执行。
