# Configuration native apply：Claude preview / apply / restore

2026-10-28。状态：供主线程审阅的候选合同，§10 的协议/产品接缝尚待锁定，**不是已经实现/验证通过的能力声明，也不是完整 native apply 的开工授权**。主线程只将 §§2–3 的 server-only patch 能力拆入 `../plans/2026-10-28-claude-provider-patch.md` 独立审阅实施；文件发布与敏感传输仍受本节待定门禁约束。本文来自只读设计任务，不代表代码、协议生成物或 index 已变更。

2026-10-29 checkpoint：§§2–3 的七键声明式 capability 已独立交付于 `059bf928`，不代表 native apply 交付。受用户委托的独立验收代表已接受 §10.2 的可恢复 absent 窗口及 §5 的一次性秘密消费产品取舍，具体条件见 §10.2 补充；协议、平台原语与完整应用仍待冻结和验证，本文仍不是整体开工授权。

## 1. 目标、依据与交付边界

以 `claude / provider / schema 1 / user settings` 为首个端到端切片：已授权选择 → 私密差异预览 → 明确确认或已授权自动应用 → 本机安全发布 → 状态查询 → 备份恢复。CLI 与 MyGo 使用同一个 Go 内核，不各自实现写文件。Web 管理可以编辑/选择远端资产，但浏览器不能直接写原生文件或接收本切片的原生秘密 materialization。

首发目标仍是 REQUIREMENTS F7/F12 及能力矩阵的十四类 agent、provider/MCP/prompt/settings/workspace、导入/编辑/切换/Pack 附件/恢复闭环。本切片决定实施顺序，**不将首发改成仅 Claude，不取消矩阵中尚未交付的能力**。保存成功、下载成功、renderer 成功、磁盘应用成功、agent 已加载，是不同状态。

依据及核对入口：

- `docs/REQUIREMENTS.md` F7/F12：只自动应用明确选择的配置，保留无关字段、冲突拒绝、先备份、可恢复、卸载只处理自身变更。
- `2026-10-28-configuration-contract-roadmap.md`、`2026-10-28-typed-configuration-domain.md`、`2026-10-28-configuration-assets.md`：typed replacement、secret 三态、同 revision 原子读取、owner/CAS、1 byte–256 KiB 存储合同。
- `docs/AGENT-ADAPTERS.md`、`internal/agentconfig`、`internal/server/agentadapter`：逻辑 target、服务端 moejs、版本/definition digest、宿主校验、客户端无 JS。
- `internal/install/{session,lock,backup,restore,publish,projection}.go`、各平台 publish/trust 文件及 `internal/state/{journal,backup,restore}.go`：持锁、intent/阶段身份、pending recovery、no-replace 发布与恢复模式。本文不把目录操作 API 当作单文件覆盖 API。
- `docs/CCSWITCH-CONFIGURATION-MATRIX.md`；本地 cc-switch 源码 `6d6b7162f122ac0ce1356a14688c8c4f712db3c8` 的 `src-tauri/src/live/patch/json.rs` 和 `live/project/claude.rs`，只作来源格式事实，不直接移植其权限/并发/秘密策略。

本次只读源码和文档；没有检测、打开、上传或修改任何真实用户配置。所有未来验收必须使用合成 HOME / settings / credential。

## 2. 当前事实与不可跨越的 capability gate

现有 `builtin.claude@1.0.0` 的 `claude.provider-env.v1` 只允许显式 additive 更新四键：`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MODEL`。不能删除旧 auth，不能清理 omitted 模型，不能表达 haiku/sonnet/opus，不能恢复 native-login。当前 Render 返回可能含秘密的整文件内容，不是可安全直接暴露的 preview DTO。

**禁止**通过忽略 typed 字段、把 native-login 当空 env、保留另一 auth、混用四键 additive merge 来声称 provider switching。连“可预览”也必须验证完整映射能力，不能先画出会误导用户的成功预览，再应用时报 unsupported。

冻结新 capability 名 `claude.provider-switch.v1`，新 plugin version 为 `builtin.claude@1.1.0`；精确 definition digest 由最终 manifest+module 生成，不能预填或仅比较版本字符串。该 capability 必须先完成真实 moejs/宿主/fixture 验证才可公布。旧 capability 与旧客户端继续 fail closed，不能回退执行。

### 2.1 完整映射及删除语义

新 capability 声明完整管理以下七个 env slot，取值来自同一 selected typed asset revision：

| Typed 字段 | Claude user settings 位置 | 规则 |
| --- | --- | --- |
| api-key / auth-token | `env.ANTHROPIC_API_KEY` / `env.ANTHROPIC_AUTH_TOKEN` | api-key 只写 API_KEY 并删除 AUTH_TOKEN；auth-token 反向；native-login 删除两者 |
| Endpoint | `env.ANTHROPIC_BASE_URL` | absent 删除受管键；native-login 不允许 endpoint |
| Models.default | `env.ANTHROPIC_MODEL` | present 设置；absent 删除受管键 |
| Models.haiku | `env.ANTHROPIC_DEFAULT_HAIKU_MODEL` | present 设置；absent 删除受管键 |
| Models.sonnet | `env.ANTHROPIC_DEFAULT_SONNET_MODEL` | present 设置；absent 删除受管键 |
| Models.opus | `env.ANTHROPIC_DEFAULT_OPUS_MODEL` | present 设置；absent 删除受管键 |

provider 是对这七个受管键的完整 desired-state，不是新一轮 patch KEEP。domain 的 omitted/KEEP 只决定**资产内** secret 合并，不能变成本地旧 auth 的隐式 KEEP。删除现有未由 Cognate 写入的受管键属于显式切换的一部分，必须列入预览并得到确认；其它 env/顶层字段不得删除。native-login 仅解除这些受管 override，不读写 OAuth/session 文件、不执行登录，不保证外部进程环境变量、项目配置或其它配置优先级没有 override。

凭据缺失的 needs-credential 草稿不能 materialize/apply；native-login 无需秘密。未知 typed 字段、variant、schema、auth mode、必需 capability、无法完整映射的字段均在 preview 前返回 `UnsupportedCapability`。新增角色/模型等不能默默退化为 default。

### 2.2 格式合同

本 variant 为严格 UTF-8 JSON object：重复成员、尾随数据、歧义路径、env 为 null/非 object、受管键为非 string 均拒绝。没有 env 可在确认后创建；未知 JSON 字段/大整数保留，不经 float64。JSONC 不猜测为 JSON；需要独立 capability 才能接受。

客户端采用经过测试的 Go token/span 编辑器，只替换/增删受管叶节点，保留其余 token 字节、成员顺序、转义、换行和未选值；有必要改变的逗号/邻接空白除外。删除最后一个受管键不删除原有 env object；原本缺失 env 且结果仍空则不创建 env。所有格式变化均进入本地预览摘要。不能只因现有整文件 JSON stringify 保留语义就宣称字节保留验收通过。

settings 原文/结果各不超过 256 KiB；继承现有 512 顶层成员、128 env 成员、env 编码 64 KiB、单 env value 8 KiB 限制，按最终结果检查。未知非受管 env value 允许合法 JSON 并原样保留，不把它上传或传给插件；新 capability 的测试必须覆盖这一点。解析资源超限固定分类拒绝，不裁剪。storage payload 的 1 byte–256 KiB 合同不变，不能拿 asset 上限替代本地格式校验。

## 3. 架构：服务端声明式转换，客户端受控本地合并

1. configuration domain 以 `ReadConfigurationAssetVersion` 取得同一行的 metadata/payload，校验 owner、expected revision、typed readiness；不使用分别 Get/Read 拼接。
2. 服务端 agent-adapter 用 moejs 把**选中的 typed 非秘密 desired-state + 所需 secret slot 的 present/reference**转换为受限操作。插件不读取机器文件，不接收旧 settings、未选 env、旧 auth 或其它资产秘密；新秘密也由 Go 宿主保管，不通过 JS。
3. 新内部接口返回 `ClaudeProviderPatchV1`，不是整文件 RenderResult。操作只允许七个枚举 `ClaudeEnvSlot` 的 `set-literal / set-selected-secret / remove`；secret 引用必须属于选中资产及该 revision。禁止任意 JSON pointer、路径、原文文件、URL fetch、命令、脚本和任意 env 字段。
4. 宿主逐项独立验证操作与 typed desired-state 等价、每 slot 恰好一个最终动作、无遗漏/重复/额外字段、auth 互斥、所有能力精确匹配。不能因为 module 属于 builtin 就跳过验证。未知输出一律空结果失败。
5. 原生 Go 内核把已验证操作投影到本地合成/授权读取的文件；本地旧值只留原生内存。插件只声明变更，绝不授权文件路径；客户端仍验证操作白名单并自行计算结果与摘要。

这一新接口与新 capability 是**待实现的前置依赖**，不能把现有四键 Render 加一个名称便接通。CLI/MyGo 的 dependency graph 不得出现 server/agentadapter、server/plugin 或 moejs；服务端调用真实 moejs，不在客户端偷偷做另一套 provider 映射。

## 4. 选择、预览与计划身份

### 4.1 选择是独立授权

本机每 `server origin + owner + profile + agent + target` 最多一个 active provider selection，持有单调 selection revision；选择记录绑定 asset ID/revision 和已授权 secret slot、是否允许后续自动应用。它不存进 provider payload。首次选择、改 target、切 provider、扩大 secret slot 或自动应用范围必须明确确认。

登录、保存/复制资产、导入预览、公开目录浏览、匿名 skill 安装、未选 Pack 附件都不得创建 selection 或触发文件写。无 selection 返回 `NotSelected`，不是“选择最近编辑项”。自动订阅更新只可消费既有明确 auto-apply policy；新资产/target/权限/新必需 capability 必须重新预览确认。失败不得悄悄换 provider。

### 4.2 Preview 合同

原生用户明确选择逻辑 target 后，内核才读取该 target。本切片唯一 target 为 `claude.settings`、user scope，映射到经信任的本机 HOME 下 `.claude/settings.json`。`.claude` 缺失返回 `TargetAbsent`，不新建 agent 目录；目录存在而 settings 缺失可以预览为 absent → create。忽略 project/workspace 设置是未支持 scope，不能当成全优先级切换成功；检测到本次进程配置的非默认 config-root override 时明确 unsupported，不猜路径。

普通预览/日志/status DTO 只含：opaque operation/plan ID、owner 内 asset ID/revision、agent/target、adapter version/digest、slot ID、动作、secret present/replace/remove 状态、冲突分类和 readiness。可在专属私有界面展示用户已授权的 typed 非秘密字段；不得展示旧 secret、新 secret、前后缀、长度、secret hash 或整文件 diff。未知字段值也不进入日志。

本地 `IntentPlan` 放在原生内存或加密 journal，不通过浏览器 view/model。它绑定：

- server origin、owner、asset ID/schema/revision、adapter API/version/digest、完整 required capabilities；
- selection revision，若来自 Pack 则另含 pack ID/revision 与 attachment selection revision；
- target/scope、已打开父目录的文件身份、目标 absent 或 identity/type/mode/link-count/size、原始 bytes SHA-256；
- 受管 slot 操作、必要 secret references、confirm/auto-policy revision、随机 operation ID、有效期。

此时未消费新秘密，不能声称已计算精确 new file hash，也不能拿 placeholder 渲染的 hash 冒充最终 hash。用户确认的是完整受管动作、秘密 slot 和固定资产 revision 的 `IntentPlan`。消费秘密后原生内核形成 `MaterializedPlan`，绑定原 IntentPlan commitment、完整 new bytes SHA-256、实际操作和 stage identity；若操作偏离已确认 intent，必须中止而不是静默扩大授权。只有 MaterializedPlan 能进入 Prepared/PublishIntent。native-login 无新秘密时可直接完成本地 materialization。

含秘密的整文件 hash 也仅在本地加密记录内使用，不出普通响应/遥测。对服务端的绑定采用本机随机密钥 HMAC 的 opaque **IntentPlan** commitment，不上传原文、路径或 raw file hash；服务端只绑定该 commitment，不能假装验证了本机文件状态。MaterializedPlan 的新 hash 由本机复核，不另开上传秘密内容的“校验”接口。

preview TTL 为 5 分钟，过期或任一绑定变化都重新预览。确认只确认指定 plan，不把“点击过 Apply”变成长期广泛授权。

## 5. 秘密 materialization：独立授权消费边界

普通 List/Get/Edit/Preview/OperationStatus/Pack/公开下载响应绝不返回秘密。无通用 Reveal/ExportRaw RPC。仅以下 native-purpose 通道可以在明确授权下返回**选中资产所需的**原文，并直接交 Go secret wrapper，不经过 Web UI/剪贴板/JSON debug 日志：

| 动作 | 输入/结果与约束 |
| --- | --- |
| PrepareNativeApply | 有效且绑定父 owner session 的 native-purpose 子会话 + expected asset revision + 精确 adapter/capability + operation ID；固定并返回无秘密 patch、preparation 身份与 challenge，不收尚无法构造的完整 plan commitment |
| AuthorizeNativeMaterialization | 同 preparation/owner/session，客户端依据唯一 server patch 构造完整 IntentPlan 后提交 opaque commitment、expiry、用户确认或匹配的本机 auto-policy、仅必要 slot 与持钥证明；事务内不可变绑定最终 ApplyBinding，生成至多 60 秒的一次性 grant；native-login 同样完成绑定但不产生 grant |
| ConsumeNativeMaterialization | 同 session + consumer 持有证明 + grant + operation ID；事务内重核当前 session 有效性/撤销、owner、asset revision/readiness、adapter digest 和 grant 状态，原子标记消费并固定该 revision 的内容；只输出受限敏感 wire envelope |

这些为逻辑操作合同，不在本 spec 分配 proto 包名/field number。native-login 无 secret 时不调用秘密消费，但同样验证 owner/revision/plan。owner session 是新的独立配置消费授权，不复用 Pack token、匿名资格或 MCP token。consumer proof 绑定 owner session 的 challenge、origin、purpose、operation、commitment 和过期时间；不得用 User-Agent/CORS/“是原生程序”的自报标志替代授权。该 proof 证明持钥，不是二进制远程证明。

2026-10-29 协议细化：主线程接受先 Prepare 获取唯一安全 patch、再构造完整本地 IntentPlan、最后 Authorize 固定 commitment 的两阶段顺序，关闭旧逻辑表中 commitment 先于其 patch 来源的循环依赖。具体候选见 `../plans/2026-10-29-native-consumer-protocol.md`；不能让客户端复制服务端 moejs 映射来填补顺序缺口，也不能用占位 commitment 提前授权。此逻辑细化不等于 endpoint、秘密消费或本机应用已交付；累计配额、GC、proof 与事务实现仍分别受独立门禁约束。

生产传输要求认证 TLS、同 origin、禁止跨 origin redirect、固定 request/response 上限 512 KiB、禁用 body/request/response tracing、固定分类错误；仅合成 loopback 测试可明确开启非 TLS。grant 只经 native transport 保存于 Go 内存，不在 URL、argv、浏览器存储或日志出现；服务端仅存 grant digest 及最少绑定状态。native consumer 私钥/nonce 在该操作结束后丢弃。

敏感 wire DTO 即使由 proto 生成，也不能依赖其 String/JSON 自动脱敏：middleware、拦截器、crash reporter、error details 和测试日志必须禁止记录原对象，只记录单独构造的安全状态。接收后限额解码为 secret wrapper；客户端不持久化明文 materialization envelope。

grant 消费失败、过期、撤销、同 credential/session 轮换或旧 revision 均拒绝，不借幂等请求重放原秘密。消费响应丢失为 `MaterializationUnknown`：查询只返回 consumed/expired 状态，不重发秘密；未获得完整验证内容不能写盘，需重新授权并生成新 grant。

服务端线性化点是 grant 消费事务：授权精确固定版本，不是跨网络锁住资产。之后更新资产/注销无法收回已交付 bytes；不承诺在本地 rename 瞬间仍是服务端最新 revision。未开始消费时撤销必须拒绝；已消费只可按该短期 plan 执行，本机再次检查 selection/TTL/CAS。不存在 server/local 两库原子事务，不伪称 exactly-once 分布式提交。

## 6. 本地文件、权限及 CAS 发布

### 6.1 信任根与复核

- 本机路径映射是客户端代码 allowlist；服务端/plugin 只能给逻辑 target。禁止任意绝对路径、`..`、跨卷、远端 UNC/网络 FS、未支持 scope、跟随符号链接/reparse point。
- HOME 为本机用户明确采用的信任根；根以下逐组件打开/固定目录 handle，检查 owner/权限、类型和身份，拒绝可被其它用户替换的父目录。发布前/后比较目录 entry 与 pinned handle 身份；`os.Root` 不等于自动拒绝所有 symlink，仍需显式 no-follow 检查。
- existing settings 必须为当前用户的常规文件、单硬链接；拒绝 symlink、junction/reparse、目录、FIFO/device/socket、额外 hardlink 和无法确认身份/权限的对象。读前/后 fstat 与路径身份/hash复核，不信任单独 mtime。缺失也必须作为独立 CAS 状态。
- 新 settings 与 stage 文件仅当前用户可读写（POSIX 0600；Windows 等价受限 DACL），private stage directory 为 0700/等价 ACL。已有宽权限必须在预览中明确列出收紧为 private，不能让新 secret 写到 group/world readable 文件；原 mode/ACL 记录进加密备份，恢复时仍不得扩大到不安全权限。
- CLI/MyGo/自动更新共用现有安装 session 的进程锁/单写协调，不建一个互不认识的配置锁。锁只协调 Cognate，不声称能锁住 Claude、编辑器或任意同 UID 程序。

### 6.2 发布决策：捕获旧 entry + no-replace，绝不 blind rename 覆盖

普通“hash 检查后 os.Rename 覆盖”有 TOCTOU，不满足本合同。现有 `publishDirectoryChecked` 验证目录 stage，不能直接拿来发布文件。新 file publisher 需要按相同 handle/identity/no-replace 原则实现并测试；平台不能满足则 `UnsupportedPlatform`，不得退化到覆盖式 rename。

冻结保守的单文件协议：**先将旧 entry 以 no-replace rename 捕获到本事务私有 holding 位置，核验捕获对象确为预览版本，再以 no-replace 发布完整新文件**。它保证不截断/覆盖并发新 entry，代价是旧文件与新文件之间存在可恢复的短暂 absent 窗口；不是零缺失窗口的 exchange 或 kernel hash-CAS。用户预览/文档必须说明此事实。若目标 runtime 必须要求“任何时刻文件都存在”，本协议不能冒充支持，需另行评审更强 publisher；不能暗换为 blind replace。

1. 获取共享 session 锁，拒绝同 target 的未处理 pending operation；复核 owner/profile/selection revision、plan TTL、源版本及本地 base identity/hash/mode。网络 materialization 在文件变更前完成，锁等待可取消；不得持文件 reservation 等网络。
2. 在同一文件系统的可信 parent 下创建随机、exclusive、private transaction directory，固定 identity；stage 中生成完整新文件。secret 只在 Go 内存和该 private stage 出现，文件 fsync；不把明文塞进 SQLite/WAL、日志或旁路 temp/cache。
3. 将精确预览旧文件的 bytes+mode+identity 加密备份并完成 durable commit；记录 durable `Prepared` intent、stage identity、base/new hash 和 backup ID。备份失败/配额不足必须在动 target 之前退出。
4. existing target：写 durable `ReserveIntent`，以跨目录 descriptor-relative no-replace rename 移入本事务 holding；fsync 两侧目录。立即以 pinned handle 检查捕获对象 identity/hash/mode，必须等于 base。捕获到并发版本则不发布新内容，保全捕获对象为独立加密冲突快照，并仅在目标仍 absent 时 no-replace 放回；若目标已有其它 entry，留下 `NeedsAttention`，不覆盖或删除任何一方。
5. absent target：不移走任何 entry，直接进入 PublishIntent；发布时不再是 absent 则 conflict。
6. 写 durable `PublishIntent`；再次核对 pinned parent、holding/stage identity及本机 selection。以 no-replace 将 stage file 发布为 settings。并发创建/替换导致 destination exists 就拒绝，不删外部文件；holding 仍保留供安全恢复。
7. fsync target 与目录；复核 published identity/type/hash/mode 与 staged new 一致，再 durable `Applied`，记录 Applied 映射、backup、asset/selection/adapter revision，最后清理经验证的 holding/stage。清理失败记录 residue，不删除或误认未知 entry。

各平台必须以实际原生测试证明 descriptor/handle 身份、no-replace、目录持久化及 ACL 合同。现有 Linux `RENAME_NOREPLACE` / Darwin `RENAME_EXCL` / Windows no-replace move 仅是可复用原语线索，不代表文件 publisher 或跨平台断电持久性已经验证；不可跨卷 copy-and-delete。Windows 需要 pinned directory/无 reparse 等价保护，不能只拼路径调用 MoveFile 就宣称 handle 安全。

该协议捕获后发现不匹配时可能已把外部 entry 移至 holding，但不会无痕丢失或覆盖它；这是必须展示并可恢复的 conflict，不得返回“原文件完全未移动”。同 inode 的非合作写入不能靠 hash+rename 实现原子内容 CAS；捕获后和发布前均重验，出现变化保全/报 unknown，不能保证抵抗持有可写 fd 的恶意同 UID 程序。完整同 UID 攻击、读进程自身加载时机和断电后的硬件持久性不是 Go 锁能解决的。原生 agent 是否已重新加载仅显示 `AppliedOnDisk`，不显示 `Activated`。

## 7. 备份、崩溃恢复与取消/幂等

### 7.1 加密与日志

配置备份可能含未选旧 secret，比 skill 目录备份更敏感。不能原样复用现有明文 skill backup。复用其 intent/identity/pending 思路，增加独立 configuration operation/backup 记录，不塞入 skill slug 或假冒 skill 安装。

本机已有 client master key 下使用独立 HKDF-SHA256 域 `cognate/native-configuration/backup/v1`，AES-GCM random nonce；AAD 长度前缀绑定 server origin、owner/profile、device-local identity、operation/backup ID、target/scope、base/new revision 绑定和 format version。journal 中含本地路径、完整 hash/差异的敏感部分同样用独立域 `cognate/native-configuration/journal/v1` 加密；公开状态仅固定分类/opaque ID。无现有 key 而发现备份/journal 时不得新建 key 伪装恢复成功。

加密备份在本机 private stateRoot，未选内容不上传。native settings 和必要 stage/holding 是原生消费格式的受限明文例外，必须由用户明确授权并遵守权限，不将它表述为“所有磁盘上的配置都已加密”。普通状态库/WAL、备份、上传、UI 持久化绝不保留明文。stage/holding 只在核验归属后清理；安全删除不能承诺擦除 SSD/文件系统快照。

至少为一份旧文件、一份新 stage、一个冲突快照预留容量；单项各不超过 256 KiB，加密 journal 单操作上限 1 MiB，完整备份单项上限 512 KiB。未知权限/扩展 ACL 无法安全记录恢复则拒绝，不悄悄丢弃。备份不自动 GC；用户显式删除前列出依赖，pending/当前 Applied 仍依赖的备份不得删除。

### 7.2 状态机与恢复表

客户端 durable operation ID 是 apply 幂等键；同 ID 绑定不同 plan 返回 `IdempotencyConflict`。同 ID 已 Applied 返回既有安全结果，不再消费 grant或写盘；同 ID pending 先 reconcile，不重新跑一遍 apply。服务端审计与本机状态不是分布式事务，以本机 journal + 对象身份为事实源。

| 中断点/观察 | 恢复动作 |
| --- | --- |
| preview/授权前，或 Planned 且无文件动作 | Cancelled；不产生 agent 目录/新配置 |
| Prepared，target 仍精确 base，stage/backup完整 | 可取消并清理自有 stage；重新 apply 需有效 plan/授权，不复用过期秘密 grant |
| ReserveIntent，target absent，holding 精确 base | 判定已 reservation；默认 no-replace 放回 holding 并标记 RolledBack，不自动完成未确认/过期 apply |
| ReserveIntent/PublishIntent，target 是 exact staged identity + new hash | 发布可能已成功；校验 parent/backup/selection 记录后 reconcile 为 Applied，不能盲重试或删除 target |
| PublishIntent，target absent、holding 精确 base、stage仍在 | 默认 no-replace 恢复旧 entry；若原本 absent 则仅清理自有 stage |
| target 是其它 identity/hash，或 holding/stage/backup被篡改 | NeedsAttention/ConflictUnknown，保留证据；禁止覆盖、删未知对象或根据文件名猜归属 |
| Applied 后仅 cleanup 未完成 | 复核并清理自有 residue，不重发秘密、不重新发布 |

所有恢复遵循 no-follow、身份/内容/权限复验，校验密文/AAD与 schema version；wrong key/tamper fail closed。写 intent → fsync → 文件动作 → fsync → 写结果的每个 crash 点都要注入测试；不能只测正常 Close。SQLite 和文件系统不能原子提交，所以每个文件动作必须在 durable intent 之后，未知结果必须可保全重建。

取消：materialization/备份/Prepared 前可干净取消；一旦 ReserveIntent/PublishIntent 可能落盘，调用方 context 结束只停止等待，返回 opaque operation ID + `OutcomeUnknown`，不得显示 Cancelled=未应用。内核以独立、有界的恢复 context 做最少收尾；进程退出由启动时 reconciliation 接管，不能无限后台重试。状态查询不泄露内容，先查询再决定是否重新操作。

### 7.3 Restore 与卸载

Restore 也是明确预览、确认、独立 operation、完整备份和 CAS 的写流程，不是把 backup 文件直接 copy 覆盖。要求当前 target identity/hash 精确等于所选 Applied 的 postimage；不匹配返回 RestoreConflict，不回滚用户后续编辑、不提供 force 覆盖捷径。

原文件存在则解密原 preimage 并通过同一发布协议恢复；原文件 absent 则只在当前仍为自有 exact postimage 时把它移入私有 holding 并删除该自有 entry，保留可信 `.claude` 目录与其它文件。恢复前先备份当前 postimage；恢复权限不扩大 secret 暴露。恢复完成后记录 Restored、撤销对应 Applied 映射，后续自动应用不得立刻把它覆盖回去，selection auto-policy 需暂停/再次明确启用。

存在不相关外部编辑时，本切片拒绝整文件恢复；UI 给出冲突与重新预览入口，不把未实施的三方字段合并描述为可恢复成功。将来的 selective inverse restore 必须独立验证受管字段归属，不能覆盖无关字段。卸载 skill/退出登录/删除远端资产不自动删除本地 agent 配置；显式“撤销此配置应用”使用上述 restore。

## 8. Pack attachment 隐私与自动应用

- Pack 默认仅引用配置资产，不嵌入 payload、slot secret、可复用 materialization grant 或本地路径。私人 owner 可查看自己的附件 metadata；公开目录/匿名安装/普通 share 持有人不能由附件推断秘密，也不能 resolve 其它 owner 资产。分享 token 不转换为配置消费授权。
- 第一切片只消费当前 owner 本人拥有、明确选中的附件。跨 owner Pack 分享遇到配置引用时 skills 可独立安装，配置明确显示 `ConfigurationAuthorizationRequired`，不得当作已同步/已应用。跨 owner 配置转让和 secret sharing 仍是首发清单上的独立合同依赖，不得以此切片完成而删项。
- 用户需分别选择 skills 与配置/target，并单独启用配置 auto-apply。Pack revision、attachment selection revision、asset revision 与 plan 绑定；Pack 更新替换附件、删除附件、扩大权限不自动套用。引用失效、版本变化、用户本地改动均冲突重预览。
- pack 下载/skill install 与本机配置不是跨领域事务。skill 成功、配置失败要分别展示状态和可恢复操作 ID，不显示整包应用成功；不为了“回滚全部”删掉用户或其它 installer 的文件。

## 9. 可验证验收与 red / green / negative 矩阵

全部合成 fixtures、临时 HOME、临时 Store、loopback native transport；使用真实 moejs、真实 HTTP、真实临时文件/socket 和原生 OS 文件系统测试。不得扫描开发机实际 HOME/Claude 登录态。每行先在缺失/故障实现观察指定 red，再补实现得到 green；关键保护再做移除负例，不能靠 mock renderer 或仅静态检查通过。

| ID | Red/注入 | Green 必须观察到 | Negative 必须失败 |
| --- | --- | --- | --- |
| C01 | 旧四键 renderer遇 haiku/sonnet/opus/native-login | UnsupportedCapability，空 plan/零写入；新 capability 的七键实际 moejs完整映射 | 丢一个模型、遗漏删除旧 auth、旧cap回退不得通过 |
| C02 | api-key↔auth-token↔native-login；其它旧 env含 sentinel | 旧 auth真正删除；mode互斥；所有模型presence；插件输入无旧env/任何未选secret | 把旧settings或旧auth送插件立即失败 |
| C03 | 未知 plugin版本/digest、额外操作、任意path/命令 | host与client双重拒绝；无secret/文件动作 | builtin跳过校验失败 |
| C04 | 重复 JSON、env null、JSONC、大整数、未知对象env、上限边界 | 不支持格式拒绝；合法无关token/大整数保留；prospective上限正确 | float64转换/整文件丢字段/裁剪失败 |
| C05 | 真实 wire replacement/materialization sentinel +成功/失败/超限/取消 | 普通响应、日志、trace、error detail、浏览器状态、SQLite/WAL、加密备份均无sentinel；只在授权native target及受控stage/holding出现明文 | 记录原proto/String/JSON、普通Get泄露必失败 |
| C06 | 跨owner猜ID/Pack token/匿名；过期撤销轮换session；grant重放/错consumer | 消费事务拒绝，零文件动作；日志仅固定分类 | 只在call入口鉴权或忽略revision不能通过 |
| C07 | 同资产读取中更新；materialize时revision变化 | metadata/payload同版；旧revision冲突；不拼R/R+1 | 分别Get/Read或KEEP取新revision失败 |
| C08 | 未选/导入/公开skill/登录；auto policy未勾选 | 零文件动作；已选自动更新限定同target/slot/policy并处理新revision | 默认选全部附件/安装即写配置失败 |
| C09 | 父目录/目标symlink、hardlink、reparse、FIFO、跨卷、不可信ACL | fail closed；其它路径无读写；缺agent目录不创建 | 只清理路径字符串/跟随link失败 |
| C10 | preview后换entry、同inode改bytes、reservation后外部新建target | conflict/unknown且外部对象被保全，无blind覆盖；仅安全no-replace恢复 | 去掉capture后校验/no-replace/parent身份必失败 |
| C11 | 两CLI/MyGo并发、pending operation、取消等锁 | 共用锁；一个commit，其它冲突；取消不无限等待 | 各自独立锁/last-write-wins失败 |
| C12 | 备份失败/满盘、wrong key、AAD/密文swap、路径替换 | 变更前拒绝；加密备份与journal；保存preimage及安全权限 | 明文backup、备份未durable就动target失败 |
| C13 | 每个intent/fsync/rename/DB结果间kill进程，重启 | 表7.2逐态恢复，ID稳定；未知对象不删，结果不误报 | 发布后取消被称“未应用”/自动重试覆盖失败 |
| C14 | grant消费响应丢失、apply响应丢失、同ID异plan/重复调用 | MaterializationUnknown/OutcomeUnknown可查询；已Applied不重写；异plan拒绝；最终hash在消费后计算且绑定已确认intent | 重放秘密/再次执行文件写/placeholder hash冒充最终hash失败 |
| C15 | apply→restart→restore；external edit；原文件absent | 精确恢复或安全移除自有postimage；外部编辑冲突；restore后auto暂停 | restore盲覆盖/卸载删未知配置失败 |
| C16 | 新旧客户端、缺native capability、平台原语不支持 | supported=false且无写；三OS原生通过才开放对应能力 | metadata支持冒充apply支持失败 |
| C17 | Pack分享/附件升级/跨owner/配置失败但skill成功 | 无secret，状态分域，新选择重确认，已授权本人附件才自动apply | share凭据可消费配置/整包假成功失败 |

测试必须验证执行后的 bytes/mode/identity、加密落盘与真实进程恢复，不能只断言工具被 advertise 或某函数被调用。Linux/macOS/Windows 各自具备 native filesystem 测试；交叉编译不是 Windows reparse/ACL/崩溃测试的替代。

实施采用 Go 1.27.3、CGO0，不 race；候选包名冻结为 `internal/configapply`（客户端纯Go内核）、`internal/configclient`（敏感native传输），服务端扩 `internal/server/configuration`、`internal/server/agentadapter`，共享typed操作放 `internal/agentconfig`。复用现有install/state原语需保持旧skill语义不变，后续实施写集由主线程单独授权；本spec不授权修改它们。

包创建后最低验证命令如下，包不存在前不能把这些命令写成已通过证据：

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
export CGO_ENABLED=0
go test -mod=readonly -count=1 ./internal/configapply ./internal/configclient ./internal/agentconfig ./internal/server/configuration ./internal/server/agentadapter ./internal/server/plugin ./internal/install ./internal/state ./internal/server/httpapi -timeout=180s
go vet -mod=readonly ./internal/configapply ./internal/configclient ./internal/agentconfig ./internal/server/configuration ./internal/server/agentadapter ./internal/server/plugin ./internal/install ./internal/state ./internal/server/httpapi
go list -mod=readonly -deps ./internal/configapply ./internal/configclient
git diff --check
```

依赖清单需用可失败的测试/脚本验证客户端无server/plugin/moejs，而非人工看一眼；另跑全仓库回归、三OS native suites、CLI/MyGo共同flow验收及独立代表review。失败回滚与安全负例必须进入CI。

## 10. 实施依赖、真正待定项与签收条件

顺序：storage原子读取 → typed domain/secret三态 → 新renderer capability真实映射及宿主验证 → native消费授权协议 → 单文件journal/加密backup/安全publisher → preview/apply/restore共用内核 → CLI/MyGo/选择与Pack接线 → 三OS隔离验收。未实现前在产品能力表标 unsupported，不用设计稿冒充交付。

行为决策由本spec给定，实施者不得自行更改以求green。仍需主线程在进入代码前解决/签认的真实接缝：

1. **协议整合**：native-purpose服务的最终proto包名/方法名/field numbers、session消费事务入口及native consumer challenge字段由主线程与typed RPC作者联合冻结；不得占用既有标签或开通普通Reveal。本文确定授权语义，不虚构这些RPC已存在。
2. **文件发布产品签认**：本spec选择“捕获旧entry后no-replace发布”的可恢复absent窗口，而不是声称OS有原子hash-CAS。主线程需明确接受此可见语义；若要求连续存在，必须先重新评审更强跨平台publisher，本切片不得偷换实现。它是需要产品签认的决定，不是把并发覆盖风险藏成实现细节。
3. **平台与格式证据**：七键/native-login的合成Claude版本fixture、三OS安全publisher/ACL/fsync/key存储能力必须在实施中提供原生证据。现有client key与restore代码部分平台fail closed，不足以签收跨平台apply；失败保留首发缺口，不从矩阵删掉平台/能力。
4. **独立后续合同**：跨owner附件配置转让/secret sharing、项目/自定义config-root、其它agent/配置种类、选择同步与其它配置优先级仍需矩阵对应spec，不阻塞本切片安全地拒绝，但**阻塞完整F12首发签收**。不是本spec默认授权或已交付功能。

本次无代码/测试执行证据，只有文档设计与本地源码核对；不宣称任何待建包、RPC、七键renderer或原生apply已通过。主线程先冻结此spec及上述签认，再发独立实施写集和验收任务。

### 10.2 产品签认补充（2026-10-29）

用户已委托独立代表验收首发前各阶段；代表接受上述第 2 项取舍，不要求真人逐片确认。F7/F12 要求冲突检测、保全无关内容、先备份和可恢复，并未要求目标路径始终存在。产品选择待定项关闭，以下条件成为实现和验收硬门禁，不由此批准其它未冻结部分：

- 预览必须披露目标可能暂时缺失，崩溃后缺失可能持续至恢复完成；不得承诺固定毫秒上限、零中断、原子 hash-CAS 或 agent 已加载。
- 完整 stage、加密备份、durable intent 必须在移动旧 entry 前完成；窗口内不得等待网络授权、秘密消费或用户确认。
- 捕获对象不符即中止。并发新 entry 不覆盖、不删除；无法安全放回时保留双方和恢复证据，进入 `NeedsAttention`，不伪报成功或“文件从未移动”。
- 每个 intent/fsync/rename 边界须有真实崩溃重启恢复证据；覆盖窗口内外部创建、替换和取消。确实要求持续存在的 runtime，在更强方案验证前不得宣称支持，其首发缺口不因此删除。

代表同时接受 §5 的产品取舍：短期、一次性且绑定 owner/session/revision/plan 的秘密消费；响应丢失不重放秘密，需重新授权。消费后不能收回已交付 bytes，不承诺跨网络与本地文件原子提交。精确 proto、consumer proof、事务内 session 复核及敏感日志防护仍需另行冻结并独立验收；平台证据、codec、CLI/MyGo 闭环均未由该产品签认代替。
