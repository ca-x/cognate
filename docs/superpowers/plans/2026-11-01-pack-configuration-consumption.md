# Pack配置自动应用与设备通道：完整设计与P1实施授权

本节是主线程在完整F5/F7/F12目标内的技术裁定，不是用户逐条原话或产品验收。
B+D5完整目标保持：持久同意、按设备撤销、三独立批准、订阅自动应用，以及受权设备真实脱敏
diff/冲突/状态/恢复；不能退成临时会话证书或只读元数据来宣称完整完成。

## 本次实施许可

仅批准下述P1 exact10：原冻结写集的九条P1加其中已列产品文档。
实际实现新建固定 `git archive 0a5e414e01f0143266fb6fdaf1c2af1ea2d2110f` 私有树；
原25f9设计和T4/Gemini只读输入保持，不借主线/其它代理活WIP，不分支/worktree/spawn/commit/push。
精确允许集见 `2026-11-01-pack-configuration-consumption-p1-writeset.tsv`。
完整207表仅为后继设计登记，**不是全部207路径实施许可**；协调表与外部LPK三路径也未因此授权。

P1交付为MyGo owner附件元数据完整管理：真实当前owner、现有Pack页面内部子区、typed
packattachments review深拷贝、最多16条完整有序引用、独立双CAS、stale与明确升级、
审阅/最终确认、Unknown单写/显式重读、dirty及取消/换身份/关闭排空。保留原技能订阅，
不改顶层导航/native_shell，不解析终端文本、不读配置正文/密钥或应用本机。
产品文档先仅记录P1已实现事实，其余阶段明确未完成，不因文件名包含consumption暗示已可消费。

实施必须TDD，真实App/TLS/SQLite owner/跨owner/失包/双CAS/排序/取消/实际终结、
MyGo tester与普通binary前门/原生窗口分别取证；六目标CGO0构建不称运行资格。
原model.go/view.go与三类导入的共享hunk由主串行合并，不复制对方实现或重跑全仓/十八构建。
冻结实际patch/fullsource/安全证据，经非作者验收后才合主；第11路径先列准确名字与必要性。

## 其余设计审核状态

完整v2已保存以支持跨机器续接，但P2–P5/GSCHEMA/GUPGRADE尚未在本节获实施许可：
S1精确schema/hash/额度、user_version分配、R3/T4迁移/发布/激活实际接口、LPK维护启动点及
Gemini公共proof共享hunk必须主线程另行锁定。普通schema升级保持账号/密码/密文/序列/
既有授权，不套灾难恢复撤权；真正恢复则S1九DML主动清全部持久device/consent。
S0遇MigrationRequired不能成为升级后永久无法启动的借口，真实升级CLI与包装入口是必要交付。
只有SQL helper/安全拒绝、少数renderer、合成账号或交叉编译均不关闭完整C7/F12/GA。

## 冻结输入

- HANDOFF-v2.md SHA256 `8b1f0869d48c0363ff9fe33419dcac76dc0e6d35ef48d8eac549e39be1346a73`。
- DATA-SHAPES-v2.md SHA256 `3744f0185ba0aad6415a158e8a4af2d5ecc6df9d8bae736a173e3cec1e77560a`。
- SCHEMA-RESTORE-v2.md SHA256 `08a90a9ea4eee1cd97fa4debe11e6542920083d897c7907f7ffd546ccad32efb`。
- UPGRADE-v2.md SHA256 `1b2ca9575dd0581de17ab198b4cf5be5d68b4ebfbc40f0f28b12c11671ce928e`。
- QUALIFICATION-v2.md SHA256 `e0bb57d44d94f200abf5cb9ba82ff2d887450998958f4003ca585181a9d20597`。
- TEST-GATES-v2.md SHA256 `226dadac0ae025edbb40ea8416b9bbde33ec7c7464dfc486418eac142143f811`。
- WRITESET-v2.tsv SHA256 `bf7bc424e8034ddffadf5172a57bab36cbf32eadd186ef7ade4f6bff7eee4951`。
- COORDINATION-v2.tsv SHA256 `6d0a93f761efb3bfbd5b59df8196ff06f4c3a37b05b9f49bdaf5fda03997feac`。
- PACKAGING-REQUEST-v2.tsv SHA256 `e26d6478bc08c0a943798a99203027fe60a53c210820e3c4ed0ce81bb33a4feb`。
- REPORT.md SHA256 `bef303a2e160d91ea83819e52904aee1659112dace03378fcb130631456674a0`。

下方HANDOFF及同目录data/schema/upgrade/qualification/test/writeset/coordination/packaging
为完整冻结设计正文；其“未授权”保留当时状态，P1许可仅由上文精确覆盖，不隐含扩大。

---

# C7 B + D5：持久同意与受权设备通道，设计v2

**DESIGN / NOT IMPLEMENTED / NOT RELEASE-READY。主已裁定方向B+D5；本文的数据形状、迁移和逐路径实现范围仍待审阅批准。**

固定源码私有archive：`25f9dbb7b0f4225c5467a738476f9e9c1a2e49bc`。不跟随main，不借Harvey/T4/R3未合WIP。原设计目录 `/tmp/cognate-c7-design-25f9-rAzT13C5` 全部保持；v2只在本目录新增设计，不写任何产品文件，不branch/worktree/commit/push/spawn agent。未来Go1.27.3/CGO_ENABLED=0/no-race，改Go前Modern完整list；没有新增语言、运行时、依赖、OS服务或本地listener。

## 1. 已决定，不再重问

- B：一张所有target共用的持久consent/policy表；owner正常Logout/原24h交互session结束不撤销它。绝不持久owner bearer、制造假session/phase或延长5分钟prep/1分钟grant。
- D2：owner能撤销单条同意、撤销给某设备的自己全部同意，以及整Pack epoch撤销；recipient能撤销自己整台设备。Prepare/Authorize/Consume各自原事务重验。
- D3：Consume提交是材料授权线性化点。已交付材料不可追回，不宣称SQL与远端文件系统原子，不发明durable业务fence。
- D4：owner配置授权、recipient真实diff/逐槽批准、持久opt-in是三步独立动作；默认off。版本/target/tuple/设备/本机基线准确绑定，新版本新同意；Unknown绝不自动重放。
- D5：纳入受权设备交接、真实脱敏diff、冲突、reported状态和恢复入口。设备主动Connect outbound轮询，无Web直连HOME、插件执行或本地host权限。真正秘密批准在设备可信UI，或此前认证持久化的准确opt-in。跨owner的issuer只能撤grant，不能读取recipient本机diff/恢复；设备状态/控制属于recipient。
- Gemini target3/slot4、41及Prepare响应6、Codex40保持。Pack60起独立字段/域；普通无Pack六RPC和各renderer字节不变。

## 2. 推荐实现骨架及必要的扩大

**唯一新表 `pack_configuration_policies`，一个Ent entity。** 明确定义两种真正持久的业务记录，而非借短期表fake phase：

1. `device`：recipient拥有的设备身份/撤销锚点，独立root key派生的公钥、认证revision，以及有界设备命令/报告mailbox。
2. `consent`：该device下面的一条owner→recipient、固定Pack/附件/资产/target/槽范围的配置同意；保存owner grant和recipient批准策略，两个revision分离。

二者用同表self-FK构成**仅一层**父子关系；一个device可有多个Pack/issuer同意，绝不是每target一套授权系统。必须有device锚点：否则“撤销当前所有consent行”与此前发出的未到期申请ticket并发时，owner可能又给同一失窃device签出新行。锚点终态撤销和enrollment revision在Issue事务复验，阻断此反例。不会用pack_id=0、虚假owner、负ID或未知phase表示device记录；用明确row_kind与nullable字段、CHECK/原事务关系校验。

**两张现有native短期表也必须修改schema**：challenge/session的parent_session_id现为Required且Positive判定；改成准确parent union：普通native有真实session parent且policy parent缺席；Pack apply/control有真实policy parent且session parent为SQL NULL。引入明确Pack purpose和对应FK/版本，不生成一条假Web session来凑父项。旧记录保持原purpose/parent与TTL，不就地升级成Pack能力。

这是原A83范围外的必要schema和身份变化。`DATA-SHAPES-v2.md` 给出字段、RPC/tag、canonical域、payload和状态。`SCHEMA-RESTORE-v2.md` 给出S0/S1精确catalog、旧记录、T4新增撤销和R3迁移门；不再宣称无schema或仍八DML。

## 3. 端到端流与信任域

```text
recipient当前普通账号 + 设备key双PoP -> enrollment device锚点
             |              （只注册受限Pack设备，不取得Web会话）
owner Web/MyGo metadata editor -> Pack ordered refs / stale / dual CAS
             |
recipient申请ticket -> owner完整范围/逐槽审阅 -> 持久consent grant
             |                 （不依赖issuer session继续存在）
recipient设备可信UI -> 原native真实diff -> 秘密批准 + 最终确认
             |                 -> 单独durable opt-in（默认off）
skills同步/Manager.Close -> 单活配置coordinator / 本机shared lock
             | fresh Pack-purpose native child
             | Prepare -> Authorize -> Consume（各自原tx复验）
             | 原engine baseline CAS / backup / publish / journal
             v
  typed脱敏reported diff / conflict / result -> device outbound Connect
             ^                                  |
recipient Web普通账号 -> request review/apply/restore/stop -> 有界mailbox
             |                                  |
             +--- 只可读本recipient设备 ----------+
issuer Web只能管理自己grant/epoch，不进入recipient设备控制域
```

普通share manifest/download、匿名PublicCatalog、F10 token、普通CRUD、默认skills-only sync/watch没有配置权限或agent secret；不扩大旧wire。Pack native请求的recipient/native subject与asset owner明确分离，可信asset subject仅由原事务consent gate产生。设备key不成为账号token，不向浏览器返回native bearer/grant/materialization，Web不导入native proto。

## 4. 授权生命周期

### 4.1 enrollment与远程撤销

- recipient在当前普通账号会话内显式登记设备；已有普通native PoP/当前账号证明加设备key对fresh challenge的签名，绑定规范origin、recipient、device ID/key及请求代。不能只凭随机DeviceID登记，不能替别人登记。
- 准确bootstrap见DATA：原Purpose1 Begin真正随机server challenge→原Bind单次消费成fresh child→Enroll同事务验证当前同一parent及child/device双PoP，再在device行原子写唯一enrollment_child_binding_hash消费该child。无BeginEnrollment/新挑战表，不重复消费used challenge，也不要求未登记设备假已有Pack parent。
- key从原client root key以独立HKDF域派生，绑定origin/recipient/device；不改root生成权限/LoadExisting/失钥拒绝。只有公钥入服务端；本地无owner bearer、无长期native bearer。
- recipient撤销device锚点为终态并递增auth_revision；所有child consent/短会话/待发ticket在原事务检查它。重新登记必须当前recipient账号新审批及新设备key/新ID，不复活旧锚点或旧ticket。
- issuer的“撤该device的grant”仅更新其拥有的consent，不撤recipient设备身份或其它issuer的权限；recipient撤自己整device则撤所有issuer范围。

### 4.2 owner grant与recipient policy

- 保存附件不授权。owner Issue用完整双revision、snapshot和当前asset行/owner/target/profile/槽全域，在同一SQL事务创建consent；signature/ticket只辅助审阅，**数据库持久行是权威**，旧证书原文不能重建缺失行。
- grant可明确expiry，也可选择“直到撤销/版本失效”；不暗设24h session上限。到期不续命，签发者正常Logout不影响。owner/recipient被删除、device撤销、share失效（跨owner必须绑定有效share）、Pack/附件/asset失权或任一pin改变都拒绝后续材料化。
- recipient本机看真实计划、逐槽批准与最终确认；durable opt-in单独启用，签名绑定同意ID/revision、全部pins、批准槽、真实renderer/tuple、本机scope/root identity与expected head。服务端只保存审批摘要/版本和设备签名，不保存设备秘密、旧本机正文或秘密hash。
- server mirror的enabled=true不是充分证据：原engine持shared lock验证本机密文policy、generation、真实baseline、原观察期限和tuple；服务端原事务验证owner grant/device/policy revision。任何一侧false/unknown都不执行。
- issuer正常Logout/24h结束不暂停；recipient普通session自然到期也不伪造session续期，已启用设备可用Pack受限PoP创建新的短child。**设备上显式Logout/换账号/origin/关窗停止当前coordinator，取消并join；恢复执行必须重新选择对应本机身份/策略，绝不跨当前选中身份继续。** 远端grant不因此自动撤销。

### 4.3 新版本与撤销

- 新Pack revision（包括skills-only）、configuration revision、asset revision/target/profile/批准槽变化需要新consent与新本机审批；不用原row换pins、复用原operation或重标历史journal。原consent可保留为Superseded只读历史。
- **Superseded是pins/epoch失效计算出的派生只读状态，不是数据库state新值。** 持久state只含active/revoked；DTO由当前Pack/asset/share/tuple检查派生Superseded/Expired/Unavailable，绝不把派生状态写回、重seal或自动续授权。
- 单consent撤销递增grant_revision并终态revoked；owner按device撤销只涉及自己当前记录并受同一事务/预算限制。全Pack撤销沿用Pack+configuration双revision作为epoch，原事务双CAS bump，允许stale refs，不调用假Replace、不默默清空附件。
- recipient本机disable先durable CAS local policy代并cancel/join；Web disable先原事务policy_enabled=false/policy_revision++阻止新远端材料，再发高优先级stop。UI在真正设备ack前只能说“远端已禁用/停止请求已送达或待送达”，不能说本机已排空。
- owner grant已撤但recipient device仍活：只能读自己reported状态和请求本机显式恢复/停用；不能新Pack Apply/Consume。owner不能阻止recipient恢复自己的受管备份，也不能读取它。

## 5. 三阶段事务、基线和Unknown

唯一`validatePackConsumptionTx`接受原*ent.Tx：Prepare新建及幂等已有分支、Authorize、Consume的每一个renderer调用，包含BUSY安全重试每轮。检查device锚点、consent row/version/auth/grant/policy revisions、recipient/asset owner分离、Pack双rev/完整有序snapshot、精确selected附件成员/ordinal、真实asset current版本/owner/schema/target、share当前撤销/期限、批准槽和真实renderer资格。不能仅靠Get或snapshot hash；后者不证明资产没有stale。

零秘密/native-login Pack仍必须最终Consume，新增consumption_required与materialization_required分离；空材料ack仅Pack新合同允许。原无Pack零槽不发grant/拒空材料等断言不放宽。

自动路径先Manager.Close并检查结果，释放client.db.install.lock后才开配置engine；不在Manager.Sync内嵌套。引擎持锁重验本机policy代、expected head与文件/目录身份、真实observation expiry，publish各步都保留原late-conflict/部分结果。已证实After且pins不变是no-op；本地偏离是Conflict，绝不“自动修复”。多个Pack占同一独占target首写前ConflictingSelections；skills与各配置结果分开，非全Pack原子。

所有网络业务写前本机durable Attempted/fsync；远端Consume成功失包、本地publish失败、Commit/Close/Wait/Sync不明都Unknown/NeedsAttention。自动循环不重放Authorize/Consume、控制命令或恢复。Status只读，不重吐secret。新尝试必须新operation/新计划/新批准，不用old intent补完。已消耗材料不冒称未消耗。

## 6. D5设备交接：有界outbound，不是Web远程shell

- 仅在现有CLI watch/MyGo进程内启用；无新后台服务/自动启动项。Connect普通轮询默认5s；可选long-poll每次至多20s，一个device最多一个活动poll，服务总数受enrollment quota限制。等待期间不持SQL事务/安装锁；每次返回前重新检查device/child权限。停止/服务Close必须取消所有waiter并join，检查资源错误。
- device锚点仅有一个pending命令及一个reported分页mailbox，不建通用任务队列/新表。command_id+单调sequence+payload digest、expected control revision、exact consent/local policy/review/plan/operation绑定。另有单调stop_generation可抢先停止；不覆盖正在执行命令制造“没发生”。
- recipient Web只可请求Review、RequestApply、RequestEnableOptIn、Disable/Stop、RequestRestore、ReadStatus。Request不是执行许可。无准确durable opt-in的Apply/Enable/Restore转NeedsDeviceApproval；实际秘密与最终确认仍在设备可信UI。Restore始终新本机计划/确认，旧opt-in默认不含恢复权限。
- Review来自实际engine-owned计划，不是服务端逻辑预览。设备先脱敏typed delta：逻辑角色/已知字段路径、旧/新非秘密声明、秘密只显示存在/Keep/Replace/Clear、真实conflict、baseline/tuple/期限和完整digest。未知字段/原文/绝对HOME路径/旧秘密/secret hash均不上报。未知renderer没有通用raw JSON fallback；报具体Unavailable。
- 报告是**device-reported，不是远程attestation**。有签名+TLS只能证明所登记key发过此报告，不能证明设备未被控制或真实磁盘必然如此。Web显示生成时间/原始有效期/在线状态/pins与stale，不能把reported Applied当新的本机CAS授权。
- 完整diff按128条typed变更分页；单项长的合法非秘密值按byte offset分块，缺页/未核完整digest时不可显示“完整审阅”。server只保留一页，recipient逐页确认后device提供下一页；设备本地密文journal固定report快照和hash。掉线/重启可只读重请求同一report页，不重新Apply；原观察过期则NeedsFreshReview，不刷新期限伪装旧报告有效。
- 多tab完整CAS、poll与report revision独立，业务失包只允许显式查询command状态/设备本地job，不自动重发。关窗不会假称撤销已Consume材料；明确取消/stop需ack。身份切换立即销毁当前权限凭据/报告明文和审批状态，再取消join；非秘密草稿可按原origin/generation隔离保留。

## 7. 十四agent及Web/MyGo/平台完整门

权威F5/F7/F12和CCSWITCH矩阵不缩。见`QUALIFICATION-v2.md`逐行保留provider、MCP、prompt/workspace及独占/共存、各自账号/格式差异。共享Pack协议不能制造renderer或host权限；只有真实资格+设备平台+当前格式/tuple+Pack链+GUI/CLI各门满足的cell才Available。Claude/Codex/Gemini少数cell通过不关闭完整C7；Harvey native成功也不自动成为Pack后台资格。

Web owner附件/授权与MyGo同义；recipient Web有真实reported diff/冲突/状态/恢复请求和设备批准反馈，不再删D5。两端都必须处理双CAS/stale、dirty/focus、取消/关窗、身份generation和Unknown；中英/深浅/375px/真正浏览器200% zoom/键盘/reduced-motion，主题语言切换不丢合法草稿。Web永远不拿旧本机秘密/native材料；MyGo/CLI本机审批不被Web按钮代替。

## 8. 独立可审阶段与合并条件

| 阶段 | 可独立交付内容 | 必须保持的关闭门 |
| --- | --- | --- |
| P1 | MyGo owner附件编辑parity，复用现有typed client/双CAS；原Web成果不重造 | 无grant/应用资格 |
| P2 | S1 schema、持久device/consent管理、明确parent union与PoP隔离；旧native字节兼容 | 未资格target/后台默认off；S0不自动迁移 |
| P3 | 真实原事务Pack手动消费和durable local journal/审批 | 未完成自动与D5不称完整C7 |
| P4 | 独立outbound设备控制、真实reported diff/状态、可信UI审批/恢复；Unknown | 无root shell/秘密回传，未enable不写配置 |
| P5 | 准确pinned版本下显式opt-in订阅coordinator、停止排空和Web/MyGo完整UI | 新版本不续同意；逐cell资格 |
| G-SCHEMA | 与主/T4/R3协调的S0→S1迁移/备份/inspect/sanitize/激活组合 | 未组合签收前不在旧生产DB开放B |

`WRITESET-v2.tsv` 是C7对固定25f9的逐路径设计允许集（区分handwritten/new/generated）；`COORDINATION-v2.tsv`是T4/R3/Gemini所有者须另行冻结的精确共享入口，不能伪造已合符号或混入本片patch。生成码只是按Ent0.14.6模板预计受影响路径，批准后实际生成必须再核最终delta；不因设计清单就声称每个输出已改变。新增/超集必须先报主。

主需确认的不是已裁定D1–D5，而是本版具体落地：单表device/consent两种明确记录、S1迁移与user_version分配/EX路线、T4九DML及受保护projection扩展、共享native hunk串行窗口。这些未消费前不写Go/proto/ent产品。

用户可执行升级不是SQL helper：`UPGRADE-v2.md`明确同binary的offline `schema-upgrade --check/--apply --review-id`、真实EX candidate/migration/S1复验/R3发布/实际App读回，以及LPK/Docker停服oneshot启动点。**SchemaUpgrade与DisasterRestore分开**：升级完整保留账号/密码/密文/序列/旧授权行与session，不跑T4撤权；灾难恢复才九DML清空B。R3未交付这条产品链前，S0 MigrationRequired只是一道安全门，不能发布成“用户升级已可用”。其精确申请路径纳入新写集/协调表。

共享proof门必须在public Go APIs内部实施：`internal/configclient/native_proof.go`拥有旧validators、knownTree、StrictUnmarshalNativeProof/validDecodedProof，新增Pack成为known字段后不能只靠HTTP/native_codec拒绝。所有旧Claude/Codex/Gemini公共builder/hash/validator/strict decoder对混Pack/双parent、显式零值/空message都拒，合法旧canonical不变。原Gemini exact65教训/12RED是后继输入，不在此伪称已复跑；后续与Harvey冻逐hunk串行。

## 9. 验证、回退、交付

全部产品测试未执行。`TEST-GATES-v2.md`列真实RED→GREEN、SQLite三阶段并发barrier、issuer Logout仍可用/撤销即拒、device失窃ticket反例、普通CRUD隔离、TLS实际设备/Web/本机进程、分页隐私、失包/断电/取消/Close/Wait和schema恢复矩阵。编译失败不是安全RED，fixture不能换profile冒Available，shutdown收据不能只unchecked defer。

回退不是删新表跑旧binary：S1对旧程序不保证可执行兼容，既有会话/历史记录按原版读取不等于旧binary可写新catalog。禁用coordinator保留Unknown/journal与权限行，落盘配置显式Restore；数据库降级只能走主协调的已验证旧格式恢复/迁移，不原地丢授权/审计痕迹。T4必须删除全部policy行（包括device锚点/mailbox）及原native/session凭据，防旧证书/设备key借原masterkey复活；原八DML收据仍旧边界，不能改写。

现在仅设计交接，不提供产品patch、PASS或发布Ready。实施后分阶段另冻实际patch/fullsource/exact路径/SHA/真实收据并交非作者Spec/Quality审；不自授完整十四agent、跨平台、全F12或GA。
