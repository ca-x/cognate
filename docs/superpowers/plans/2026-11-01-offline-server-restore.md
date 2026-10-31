# F4 完整离线恢复与运行代切换 Implementation Plan

> **For agentic workers:** 后继实施按 `executing-plans` 原生逐片执行；本次只设计，不 spawn、不实现、不 commit/push/branch/worktree。每片独立红绿验收后才推进下一片。主线程在 2026-11-01 授权实施 §6 T1，只读验真先行；R2–R6 与 T2–T5 仍待后继设计核对和独立验收，不阻塞正在进行的界面集成。

2026-11-01 续执行注：上段保留初次设计历史；T1、T2a–T2d 已按限定写集独审及主线验证后
逐片实施。它们不是完整 T2/Candidate 或恢复产品入口；R2–R6 与 T3–T5 仍须设计核对、
实现与独立验收，具体冻结与真实状态见 `VERIFICATION.md`，不能以本计划文本宣称已交付。

**Goal:** 从既有 S3 认证密文恢复全实例数据库及归档闭包，经凭据安全处理、隔离候选发布和离线运行代切换，真实重启后通过业务 API 读取；失败保留可核实的旧代与显式回退路径。

**Architecture:** 沿用 `SealEngine → OpenSession → CryptoVerifiedBundle`，增加候选语义验真和运行根目录协调；数据库与归档作为一个不可拆分的运行代。稳定根上的停服锁、发布意图和单一 current 选择记录协调文件系统与启动，任何不明均先拒绝服务，再显式核对。

**Tech Stack:** Go 1.27.3、CGO_ENABLED=0、现有 ent/entsqlite/modernc SQLite、Afero、S3 SDK；不引入新的数据库、备份容器、归档格式或密码算法。

**Spec:** `/home/czyt/code/go/skillhub/docs/REQUIREMENTS.md:98`；`docs/SERVER-BACKUP.md:29`；`docs/LOCAL-SNAPSHOT.md:89`；`docs/FORMAL-RELEASE-AUDIT.md:39`（下文路径均相对该仓库，行号为本次读取位置）。

## Global Constraints

- 权威需求原文：“备份对象：收录的 skill 内容 + 仓库配置 + pack 配置。”“自动/手动触发，本地 SQLite 快照 + 远端对象存储（后端见 §3）。”“支持恢复。” 可选开关不缩减 F4。
- “单二进制交叉编译（GOOS/GOARCH 全覆盖 Windows/macOS/Linux）；一切选型不得引入 cgo。” 首片 Linux 运行证据只是增量，不能取消其它平台义务。
- “已有数据而密钥缺失必须恢复原密钥，不得自动生成替代密钥。” 恢复 key 与原 server master key 分别外置供应，均为 32 raw bytes；不读取开发机真实 credentials。
- 现有 `CGBKSE01`/版本 1/suite 1、bootstrap/root/pages/extents/complete、HKDF 域、inventory v1、archive hash 与路径契约全部保留；新增本地运行选择/操作记录不是另一种可传输备份格式。
- 不重做历史快照、seal、手动备份控制平面或 UI。自动调度、保留与孤儿清理仍属于 F4 的独立剩余义务；C4 完成不自动关闭它们。
- 第一片只新增恢复专用文件及三个 dispatcher 接点（生产 main、state/backup TestMain）；不碰当前 npx、配置 UI、RPC/proto/generated、审计/TODO/VERIFICATION 的并行写集。

## Review Focus

1. 恢复 key 正确而原 master key 错误/缺失：不能因为容器解封成功就发布数据库（T1）。
2. 密文全部合法但数据库、inventory、归档来自不同切点或缺一项：必须闭包拒绝（T2）。
3. 普通模式服务、快照 worker 或旧执行文件仍可写：不能以“管理员说停了”替代跨进程排他与降级门（T3/T5）。
4. rename 已发生而 fsync/close/响应丢失：不得自动重复切换、宣布回滚或自动选择旧代（T4/T5）。
5. 回退旧代使已撤销的 session/share/MCP/native 权限复活：必须与正向恢复执行相同撤销屏障（T4/T5）。

## 0. 观察基线、非目标与依据冲突

本次只读 HEAD：`f0e194baf0836a93889f68ed0e67abe9720de26a`，工作区有主线程未提交 npx/配置 UI 等变更；源码事实以本次文件为准，不将其视为冻结发布候选。执行前重新定位本表符号，不能机械套用旧行号。T1 实施基线后继为已推送的 `79423887903393d01ef17b8738234cc21b535d38`；不把早期工作区状态当作新候选状态。

`SERVER-BACKUP.md:79` 仍有“无管理 Web 页”，`LOCAL-SNAPSHOT.md:5` 仍有“App/手动待接线”；均被 `REQUIREMENTS.md:106`、`:108` 和前者 `:5` 的后继事实覆盖。本计划不重开 C2/C3 已交付内容。C4 仍明确未实现，`remote_committed`、`CryptoVerifiedBundle` 或 SQLite 可打开都不是 `restore_verified`。

已按 Nowledge 路由执行一次有界 CLI thread 检索：query=`Cognate 离线恢复 C4`，limit=3，返回 default Space 的不相关线程；没有可采用的 C4 ruling，不拉取整段历史、不写入推测性记忆。需求与源码是本设计依据。

## 1. API 依赖与实际路径行号

| 已有依赖 | 精确位置 | 可复用能力 / 不能假定的能力 |
|---|---|---|
| `ImportRecoveryKey(id [16]byte, material []byte)`；`NewSealEngine(parent *os.File, key RecoveryKey, limits SealLimits)` | `internal/server/backup/seal.go:142`、`:176`；预算 `:23` | 导入外置恢复 KEK、受控暂存；opaque key Destroy/engine Close 均须明确成功 |
| `NewAttempt(instanceID, backupID, attemptID [16]byte)` | `internal/server/backup/target.go:158` | 固定一个 source attempt，不能混用不同对象列表或根据 latest 自动换备份 |
| `SealEngine.BeginOpen(ctx, bootstrap, complete *os.File, expected Attempt)` | `internal/server/backup/seal_manifest.go:420` | bootstrap + complete 认证；BeginOpen 成功尚未验证全部对象 |
| `OpenSession.NextRef(ctx)` / `AcceptFile(ctx, source *os.File)` / `Finish(ctx)` | 同文件 `:615`、`:629`、`:957` | 严格按返回 ObjectRef 取对象；Finish 只返回密码学校验 bundle |
| `CryptoVerifiedBundle.Metadata(ctx)` / `CopyFile(ctx, kind, id string, output io.Writer)` / `Close()` | 同文件 `:1040`、`:1057`、`:983` | database/database、inventory/inventory、archive/64 位小写 hash；不是 schema/关系/部署验收 |
| `SealMetadata`；`captureMetadata` | 同文件 `:132`；`internal/server/backup/capture.go:160` | 已认证 database/inventory 长度+hash、schema、cut、master_key_id、archives、closure；捕获当前未填写部署外部配置，不能凭 ExternalConfigNames 推断配置齐备 |
| 内容 inventory 和 closure 生产者 | `internal/server/library/content_snapshot.go:79`、`:191`、`:194`、`:225`、`:238` | 首行 `cognate-content-inventory` v1 + 原 canonical references NDJSON；closure 输入规则见 §3；不另造 manifest |
| `Store.WithLocalSnapshot` / `LocalSnapshotView.NextReferences` / `CopyReferences` | `internal/server/state/local_snapshot.go:205`、`:575`、`:552` | 活库切点专用；不能伪造 Store/lease 作为离线准入，也不能从恢复候选再次 Capture 当作验证 |
| `snapshotSchema` / `snapshotReadCatalog` | `internal/server/state/local_snapshot_worker_linux.go:407`、`:417` | 固定可信 92 catalog 元组；应用 90 + 两个严格内部元组，无 sqlite_ 前缀豁免 |
| 固定指纹 | `internal/server/state/local_snapshot_inventory.go:21` | exact `34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235`；normalized `7e56e095b6943f47835a6a019ad99655b601224afa02726f2270312a5a404cfc` |
| `snapshotWriteInventory` / `snapshotValidateReference` / `snapshotValidateCategories` / `snapshotReadRow` | 同文件 `:30`、`:129`、`:182`；`local_snapshot_verify.go:185` | 复用 canonical 编码、全部 owner 引用和分类检查；现有函数为包内能力，需要新离线入口，而非复制 SQL |
| `verifyStoredKeyProof` / `verifyStoredObservationProof` | `internal/server/state/key.go:74`、`:86` | 验证原 key 和既有观察策略；`verifyDatabaseKey:17` 会补元数据，不能用于只读恢复准入 |
| `newConfigurationAEAD` / `configurationAAD` / `decryptCredential` | `internal/server/state/configuration.go:92`、`:105`；`credential.go:119` | 保持原 owner/ID/revision/AAD；逐行验所有保留密文，不能只验一条 GitHub credential（现快照 worker `:753` 是 LIMIT 1） |
| `state.ReadMasterKey` / `ReadPrivateFile` / `OpenPrivateDirectory` | `internal/server/state/masterkey.go:16`、`:108`；`directory.go:67` | 私有文件/祖先校验；InitializeMasterKey `:28` 只适合新实例，恢复禁止调用 |
| Unix 安全目录与普通文件约束 | `internal/server/state/files_unix.go:16`、`:32`、`:54` | owner、权限、无 symlink、单硬链；需继续做 fd 与目录项的物理身份复核 |
| `maintenance.Join` 与 `.instance.lock` | `internal/server/maintenance/instance.go:44`、`:166` | 受管实例能力，不是所有普通 server 的生命周期锁；锁在可替换运行代内也不足以保护稳定选择记录 |
| `filelock.Acquire` | `internal/filelock/lock.go:14` | 目前是排他 TryLockContext，没有共享 server lease API，不能直接假定支持 SH/EX |
| `state.OpenWithOptions` | `internal/server/state/store.go:59`、`:138`、`:160` | schema.lock 只覆盖初始化；`client.Schema.Create`、key/policy 写入、时间兼容/backfill 会变更候选。禁止先 Open 再验证 |
| `OpenArchiveCacheWithOptions` / `Put` / `checkPolicy` | `internal/server/library/cache.go:49`、`:110`、`:262` | 消费 `<hash>.tar.gz`、`.cache-budget`；预算为本地显式部署策略，不在当前密文元数据中；不得默认 512MiB 导致恢复后打不开 |
| `repository.InspectArchive`；`inspectContentSnapshotArchive` | 调用在 `internal/server/library/content_snapshot.go:243`、`:250` | 归档完整 hash、skill 数/路径/hash；复用既有 32MiB 压缩/64MiB 文件/10000 entries/512 skills 安全限制 |
| 真实 startup、路径与关闭 | `cmd/cognate-server/main.go:10`；`internal/servercli/root.go:27`、`:42`、`:46`；`internal/server/app.go:84`、`:168`、`:213`、`:329`、`:393` | root 现在直接开 server.db/archives；只有 BackupEnabled 才 managed；先 Open 再 net.Listen，适合在 Open 前加恢复门；后台 worker 在 Serve 启动 |
| 真业务读取 | `internal/server/library/service.go:190`、`:199`；`library/distribution.go:10`；`state/pack.go:154`、`:170`；`state/configuration.go:243` | 仓库/归档、Pack、配置明文受权读取；最终须再经真实 Connect/HTTP 读取，不能停在这些库调用 |
| 权限依赖 | `internal/server/ent/schema/session.go:21`；`nativeconsumersession.go:31`；`nativeapplypreparation.go:29`；`packshare.go:14`；`mcpcredential.go:14` | session → native challenge/session → preparations/grants 有级联关系；shares/MCP 用 revoked_at；需核对最终迁移表而非猜表名 |

## 2. 必须先记录的技术 ruling 与先决缺口

以下为建议决策，尚非用户批准或发布资格。T1 不依赖 R2–R5 的最终选择，可先合入；T3/T4 写入产品 startup 前必须完成相应 ruling，不能默选并扩大首片。

| ID | 建议 ruling | 生效前缺口 / 判定证据 |
|---|---|---|
| R1 兼容 | 第一版恢复只接受当前可信 schema 的精确结构；AppVersion 仅作诊断，不作为 schema 证明。未来更旧版本通过逐项批准的离线迁移链；新 schema、未知对象/套件/版本、缺证明一律拒绝，不用 ent 自动迁移或“忽略未知”救活 | 复用现有 exact+normalized 检查；另建运行布局/最低 reader 版本门，不能只对备份 AppVersion 做字符串比较 |
| R2 停服 | 稳定根的 shared server lease + exclusive restore lease；普通/backup-enabled/启动中的 server 都参与，lease 从任何数据库/缓存/日志写入前持有至所有依赖明确关闭。保留普通模式允许多进程的现有语义 | 新 SH/EX 安全锁能力、同进程注册表和真实进程测试；旧未参与锁的 binary 必须先停用并由部署者禁止自动重启，flock 不能约束不合作程序 |
| R3 布局/发布 | 同文件系统 private generations + 单个 regular current 选择记录，稳定根永不 rename；不使用 symlink current。只交换这一选择，不分别替换 DB 和 archives | startup/backup/cache 路径拆分；fsync/rename 资格；旧布局离线收编与旧 binary 拒读保护。发布元数据持久化需版本化、严格解析、原 key 派生认证与身份绑定，复用成熟原子发布机制，不复用备份任务日志冒充恢复事务 |
| R4 权限 | 默认全量撤销站内 bearer/native 权限；保留业务配置与原 master key。原 GitHub token 保留密文但 status=needs_reauthorization，刷新/同步状态退役，不主动访问上游；恢复的 provider/API secrets 保留但不自动应用到设备 | §5 的逐表事务、所有读取状态检查及回退同策略；如果产品希望保留 GitHub 登录，须另行 ruling 与权限复活分析，不能首版静默保留 ready |
| R5 回退/启动 | 已激活后的失败一律停服并显式回退；回退只保证恢复旧业务代，不承诺恢复新代期间已接受的写入。启动失败不自动选 previous | 只读验收启动 gate、禁止正式写入直到验收/显式启用、回退重施撤销；新旧 key 不同的覆盖恢复须外部供应两把 key，首版同实例原 key 不变 |
| R6 部署/平台 | 外置 TLS/OIDC/管理员映射/预算/恢复 key ID/S3 目标由部署者重供；密文不包含它们。源 instance/attempt 是恢复 provenance，目的实例备份控制身份不能靠复制数据库猜出来 | 新实例与覆盖恢复分别演练；目的 backup/control 不覆盖、不借用历史 generation 自动对账。Linux 先交增量，其它 OS/文件系统安全锁和持久性后继资格不可取消 |

DDIA 对应取舍：以认证源数据为权威、缓存重新材料化；用单写者和一个选择点避免 DB/归档双写分裂；用 durable intent+精确核对处理部分失败；超时不等于未提交，先保安全再恢复可用性。ub-mode 对应：先交一个可测准入入口，再交闭包，再接启动；每项状态决策只在一处定义。

## 3. 完整恢复协议与闭包不变量（T2/T4 必须实现）

1. 操作员明确选择源 instance/backup/attempt、目标稳定根和请求 UUID；停止 supervisor 自动拉起及所有服务进程。读取两份外置 key 和显式部署配置，先取 exclusive 根锁，再开始候选写入；不能用 PID 文件、端口空闲或 `.schema.lock` 替代停服锁。
2. 可使用预先下载的原始密文对象目录，或在此离线维护进程中只读拉取 S3；“离线”是应用停服，完全断网时走本地密文输入。两条路都只接受同一既有容器；无明文 zip/tar/SQL 导入后门。
3. bootstrap 固定 156 bytes、complete 受既有 16KiB 上限约束；源选择必须显式 pinned，不能自动选“最新”。创建既有 SealEngine，BeginOpen → 逐 NextRef 精确下载/读取并 AcceptFile → Finish。缺对象、wrong attempt、认证/长度/hash/close 错误立即停止；S3 列表不是完整性权威。
4. 当前 `S3Target.VerifyExact` 只验摘要，不导出密文，**缺少恢复下载 API 和 bootstrap/complete 引导 API**。T2 在 `s3_restore.go` 增加 `(*S3Target).WithRestoreHead(ctx context.Context, parent *os.File, expected Attempt, consume func(context.Context, *os.File, *os.File) error) error`（依次传 bootstrap、complete），只按既有 canonical attempt 对象名有界下载；这些未获信任的头必须交 BeginOpen 认证，不能由 Listing/ETag 授权。随后 `(*S3Target).CopyExact(ctx context.Context, ref ObjectRef, output io.Writer) (VerifyResult, error)` 消费 NextRef，取回原始密文字节并验精确长度/hash。头文件借用 scope 的读取、回调及最终关闭共同成功才成功，失败使整个恢复失效；不依赖已丢失的本地 control journal，不 PUT/DELETE，不对写结果做重放。
5. bundle 仅在作用域内使用。将 database/database 写入隔离 `server.db`、inventory/inventory 受限读取；从 inventory 首行严格识别 v1、snapshot_id=metadata.CutID、archives 数及按 hash 严格递增的列表。只根据认证列表复制 archive/<hash>，文件名由 hash 派生为 `<hash>.tar.gz`；绝不从远端文件名拼目标路径。
6. T1 先验候选 DB 的页/长度/hash、完整 catalog、integrity_check、foreign_key_check、所有 owner 引用、分类与密钥证明；metadata.SchemaSHA256、Database 和 MasterKeyID 必须与实际值一致。缺失 key proof 不是空库；不得创建证明。逐行验证所有保留 GitHub/configuration 密文及原 AAD，不只抽样第一条。
7. 对候选 DB 重算现有 `snapshotWriteInventory` 的 canonical reference 字节流与 hash；inventory 的首行之后必须逐字节等价于这条流（等价长度+SHA256并严格语法/排序校验），无遗漏/额外/重复/乱序/尾随字节。`metadata.Inventory.Records = 1 + references`，Owners 是 repository/pack 引用覆盖的 owner 去重数，**不是 users 总数**；空业务实例仍须合法的头和 0 引用。
8. 每个去重归档严格核验长度、整体 SHA256、InspectArchive 安全扫描、每个相关 repository 的技能数与 SKILL.md path/contentHash。数据库引用集合 = inventory archive 集合 = 实际候选归档集合，双向相等；共享 archive 可跨 owner 去重，权限不随去重合并。不联网补缺、不丢私有/公开任一内容。分类、Pack attachments、排序、用户等数据库业务表同切点保留，并验证其引用/隔离约束。
9. 精确重算既有 closure：`SHA256("cognate/f4/content-closure/v1\\x00" 的实际 NUL 域字节 || databaseSHA256[32] || canonicalReferencesSHA256[32] || 按 hash 升序的每个(archiveSHA256[32] || BE64(archiveBytes)))`。不是 inventory 整体 hash；不得用 JSON hash 代替。结果须等于 metadata.ClosureSHA256。
10. 验真与撤销是两个阶段：先证实原始认证闭包，后只在候选事务中施加 §5 策略；记录 source digest 与 sanitized candidate digest 两套身份。原始 inventory 不会匹配撤销后的 DB hash，不能改写认证 root 或声称原闭包仍逐字节相同。事务后重新 integrity/FK/业务引用检查、关闭数据库并确认无未合并 sidecar，再计算发布身份。
11. 配置 candidate archive cache 的显式预算，至少容纳闭包总归档量且在既有 1TiB ceiling 内；沿用 `.cache-budget` 格式。数据库/密文输入/解封暂存/最终归档/旧代/控制记录分别预算；预算不足提前拒绝，不删除旧代或受引用归档腾空间。首版保留 1 个 previous 和最多 1 个候选；不明残留存在则拒绝新任务，清理需显式精确操作。
12. 候选写文件 Sync/Close、目录 Sync，以及原密文读取 scope、bundle/session/engine/恢复 KEK 的清理全部明确成功后，才能持久化 `candidate_verified`。原 server master key 仅在仍需验真/认证发布元数据的受控内存中保留，用完清零。先 StageCandidate 材料化并返回自有 Candidate，再退出全部密文读取/解封 scope，最后 Candidate.WithVerified 验真并在回调里进入 T4；禁止在 WithRestoreHead 的回调内激活。候选根 FD 可作为长生命周期能力保留，其最终关闭错误按操作已经到达的真实阶段记录，不把晚期错误说成“未切换”。scope 失败、取消、清理/close 不明不得签发可激活 capability；跨进程重启的记录只作核对线索，须重新打开既有认证密文并重新验真，不能只信残留明文候选或控制摘要。

## 4. 运行代选择、可中断语义和回退

建议布局：稳定 `data-root/` 下保留 `logs/`、目的实例 `backup/control/` 和恢复控制目录；`generations/<opaque-uuid>/` 内只放 `server.db`、`archives/` 及候选资格记录。`runtime.current` 是私有 regular 选择记录，绑定布局版本、generation、sanitized digest、previous、最低 reader 与操作 UUID；不包含原 key、token 或配置正文。

- T3 startup 在 `PrepareDirectory` 的最小安全根建立后、任何其它初始化/ReadMasterKey 自动生成分支/日志/Store/cache/backup 构造前检查恢复门并取 SH lease；读取外置 key 后认证并解析 current 一次，将 Store 与 cache 指到同一代，整个 App 生命周期持有 lease。不能用 data-root 的 current 在运行中重新解析造成代混用。非参与的直接 Store/ArchiveCache writer 属于受信任内部代码，生产入口必须全部纳入；离线工具不得借它们绕开根锁。
- 新根锁会使目录非空，现有 `InitializeMasterKey` 的空目录判定因此需要 T3 同步调整：只在当前 lease 下、无 DB/代/current/intent/历史数据，且每个协调文件的身份都由本次空实例建立过程证明时才允许初始化；不笼统忽略点文件。实际新实例 `--init-key-if-missing` 正控和非空/不明标记拒绝必须通过，不能为恢复锁破坏空实例首次安装；恢复命令始终不调用该生成分支。
- 旧布局迁移是首次恢复的独立停服子事务：先 durable layout intent，离线将旧 DB/sidecar 与 archives 收编为 previous；根上的旧 `server.db` 位置最终放置非 regular 拒读占位，以让现有旧 binary 的 `inspectServerFile` 拒绝，而非打开陈旧库或自动建空库。中途所有新 binary 见 intent 拒绝；不合作旧 binary 必须由 supervisor 禁止启动，这一点不能靠 advisory lock 解决。不得对活动 WAL 直接复制、独立交换 archives，或承诺转换中的旧程序也受锁保护。
- 发布同文件系统、同稳定父目录内的选择记录临时文件，使用 O_EXCL/NOFOLLOW/0600、精确写入、file Sync/Close，再 rename 为 current，最后父目录 fsync。rename 是可见选择点，fsync 确认后才称持久切换；不是跨文件系统事务。候选与 previous 全部句柄/身份被 pin，发布前再验身份和 sanitized digest。
- durable intent 必须在可能改变运行选择之前落盘；短写、未知版本/部分记录/认证失败均 fail-closed。该 intent/fence 的生命周期与最后根锁 FD 关闭顺序需单独设计并测试：close 报错不证明内核仍锁住；下一进程须被持久标记阻挡，原能力永久隔离，不能重试 close 或继续发布。复用既有 control fence 的安全原则，不直接复用它的状态机/私有格式。

| 持久阶段 / 故障点 | 对外结果 | 重启与允许动作 |
|---|---|---|
| 无 intent，尚未触碰候选/选择 | rejected | 当前代照常；干净拒绝不能伪报 interrupted |
| preparing / validating，取消或已知错误 | interrupted/failed，未切换 | previous/current 不动；精确清理确认后可结束操作；不自动恢复任务 |
| 已验候选，未发 switch intent | candidate_verified，仅可供后续显式操作 | 不启动候选；重验、核对原请求后再决定激活 |
| switch intent 已持久，rename 未知或 directory fsync/close 不明 | switch_unknown / needs_attention | 普通 startup 拒绝；只读 status 核对 current、候选/旧代身份和原 operation，不能重发切换或删现场 |
| current 已确认切换，尚未业务验收 | activated_unverified | 只允许受限验收启动；不开放正式业务写入/外部副作用/普通备份调度 |
| 真实启动和业务验收通过，显式启用确认 | restore_verified | 正常业务开放；首次响应丢失通过原 UUID status 查询，不创建新恢复 |
| 初始化/业务验收失败 | activation_failed | 停止并排空新进程；拿 EX 锁后显式 rollback；不能自动选 previous |
| rollback intent/rename/fsync 结果不明 | rollback_unknown / needs_attention | 同样阻止普通 startup，精确核对；不能把失败写成“已回滚” |

显式 `restore status --operation <uuid>` 只读；`restore activate/rollback/reconcile` 每次绑定原操作、预期 current 和 digest，状态改变需新批准。未知操作先核对，不以时间戳、mtime、最后一个目录或“锁似乎没了”猜成功。相同请求与相同输入返回同一持久结果；输入不同拒绝。网络 GET 的失败可重新只读获取同一对象，切换/撤销事务不因响应丢失自动重放。

回退先取得 EX、确认新服务完全退出，保留失败新代供核查，再在 previous 的隔离副本施加最新撤销策略并验真，通过相同 single-current 流程发布。原始 previous 不就地改写；无可验证旧代、旧 key/部署配置缺失、磁盘不足或关闭不明时保持停服。验收后开放写入再回退须明确确认丢失新代业务写入，不能声称自动数据合并或 RPO=0。

## 5. 会话、token、客户端凭据的明确策略

| 类型 | 默认恢复策略与验收 |
|---|---|
| Web/CLI/OIDC 站内 session | 删除全部 sessions，按真实 FK 顺序/级联清除 native challenges、native consumer sessions、apply preparations、materialization grants；备份后撤销而在备份中仍有效的 token 也必须失效。重启后旧 cookie/Bearer 401，重新登录可用 |
| Pack share 与 MCP 管理 token | 全部 revoked_at 设置为该恢复操作固定的撤销时刻；MCP revision 正确推进，审计保留。旧 token 在 Pack manifest/download/MCP 真实入口拒绝，重新授权生成新 token 后业务读取正常 |
| native 客户端公钥、bind、grant/consume 能力 | 随 session 链彻底退役；不恢复旧设备授权，不复活已消费/结果不明的远端 grant。本机 journal/密钥库不在服务端备份范围；客户端只可查安全状态并重新登录/授权，不能自动重放配置应用 |
| GitHub OAuth/PAT 密文 | 验真全部密文后保留资产，置 needs_reauthorization，清除 refresh_attempt/sync_fetch_id/在途调度状态，提升现有代次栅栏的处理必须与其 AAD 规则一致；不改 generation 后保留旧 ciphertext。最小实现可保留原 generation 并依赖强制非 ready + 进程全退出。现有 PutCredential 只接受 ready，不能直接承诺重新授权可用；后继须补显式重新授权 CAS 及所有者取得预期 generation 的真实入口，在同一事务内生成新代/新密文并恢复 ready，拒绝 stale/refreshing 和旧票据。私有历史、选中 Lists 和业务配置保留，不因恢复变成匿名可取 |
| provider/MCP 配置中的 API secrets | 保留原密文/owner/版本/AAD，所有者重新登录后可以原受权 API 读取/使用；不传给浏览器存储，不自动向 agent 写文件，不声称替用户撤销上游 key |
| 本地密码、OIDC issuer+subject、管理员 | 保留原本地密码 hash 和明确绑定；不按邮箱合并，不自动提升任何账号。管理员 UUID 授权来自外置部署配置；没有可用本地账号的 OIDC-only 实例须由部署者重供对应 OIDC 环境，不能偷偷创建管理员后门 |
| master key / 恢复 KEK / TLS、OIDC secret、S3 credentials | 两把原始 key 外部供应、内存使用后清零；不放 argv/env 原文、诊断或结果 DTO。恢复不轮换 master key 以免损坏站内密文；上游凭据/部署 secret 如需轮换由独立显式程序处理 |

以上处理在候选事务原子提交，失败不发布。先完整验源，再撤销，最后重新验候选。rollback 必须施加相同或更严格策略，不能从 previous 或更早备份恢复已撤销 bearer；客户端自动更新器也不得用旧 share 恢复授权。

实施进度：上述撤销 SQL 已交付为未导出的 `revokeRestoreCredentialsTx` 借事务 helper，
exact2 经非作者限定 ACCEPT。它不拥有事务终结、不证明候选 provenance、不提供激活能力；
固定撤销时刻不是幂等键，未知 commit 后禁止自动再调。完整生产 coordinator、写后再验、
关闭/提交不明隔离与发布仍须后继接通；独审和主组合定向证据见 `VERIFICATION.md`。

## 6. 第一实施片 T1：只读离线候选数据库验真（最小可合入）

后继执行状态：fix1 已独立增量复审限定 ACCEPT，原 P1 CLOSED；累计 exact12 合入主线，
T1 与既有 dispatcher/密文/S3 CopyExact 定向组合及真实生产 worker 通过。复审报告
`/tmp/cognate-t1-independent-7942388-3Wf81L/fix1-review/REPORT.md`，SHA256
`2a0ce86e096b7435c35c029b74a1fd495321dcf6ed38ce288a331d8710c01cfd`。
下段保留首次候选 BLOCK 历史，不能将其改写为首次即通过；详见 `VERIFICATION.md` 首节。

2026-11-01 首次 exact12 候选独审为 **P1 BLOCK**，尚未合入主线。报告
`/tmp/cognate-t1-independent-7942388-3Wf81L/REPORT.md`，SHA256
`fbeac7b4e0d01a9fdb13df510caac86a8be20482008abdfc1067b66d8012c919`。
从 `after=0` 开始的 `id > after` 分页并未证明正 ID 数据域，SQLite 可显式插入 0/负值；
七个损坏密文、非法 Pack/分类反例仍取得 Valid。旧 18 顶层/80 PASS 不覆盖该反例。
后继仅在 exact12 新 worker/test 内补 SQL 资源限制之后、所有正域分页之前的完整准入，
或完整遍历整数域；不得迁移候选、改变旧 helper 或只在已过滤循环中补判断。

**交付边界：** 给后继恢复协调器一个无迁移、无活库快照的真实只读准入 API，并用现有 Seal/Open 解出的真实数据库证明能接上。它不能发布代、启动服务、改变 `restore_available`，不能称 F4/C4 完成。该片针对当前确实缺少的离线检查入口；不重写历史 backup 管线。

**精确 disjoint 写集（相对仓库，除此不写）：**

| 路径 | 动作 / 责任 |
|---|---|
| `internal/server/state/restore_database.go` | 新建：opaque evidence、错误类别、外层 API/脱敏；不依赖 backup 包，避免 backup→library→state 的 import cycle |
| `internal/server/state/restore_database_linux.go` | 新建：借入 FD 的身份校验、有界 worker 编排、取消排空与宿主复核 |
| `internal/server/state/restore_database_other.go` | 新建：!linux 普通受控 ErrRestoreUnsupported，不开放平台资格 |
| `internal/server/state/restore_database_worker.go` | 新建：独立隐藏 worker dispatch 和仅匿名管道上的受限控制消息，不生成持久备份文件或修改 CGBK/CGB2 旧帧 |
| `internal/server/state/restore_database_worker_linux.go` | 新建：复用 snapshotInstallControls/snapshotSchema/snapshotWriteInventory、旧 key/policy/AAD 解密 helper；只读 SQLite 验真 |
| `internal/server/state/restore_database_worker_other.go` | 新建：不支持平台 dispatcher 明确拒绝 |
| `internal/server/state/restore_database_linux_test.go` | 新建：真实数据库、多 owner/分类/Pack/加密资产、FD/资源/错误测试，局部实例故障注入 |
| `internal/server/state/local_snapshot_linux_test.go` | 修改：现有 TestMain 仅增加 RestoreDatabaseWorkerDispatch 分派，不改历史测试逻辑 |
| `internal/server/backup/restore_database_integration_test.go` | 新建：包 backup 内复用既有真实 captureFixture/Seal/Open 测试构造，接新 state API；不重写容器 |
| `internal/server/backup/local_snapshot_crypto_integration_test.go` | 修改：现有 backup TestMain:26 仅增加 state.RestoreDatabaseWorkerDispatch，保证集成测试重执行本 binary 时确实可分派 |
| `cmd/cognate-server/main.go` | 修改：既有 SnapshotWorkerDispatch 旁增加新 dispatcher；正常 servercli.Execute 语义不变 |
| `cmd/cognate-server/restore_worker_integration_test.go` | 新建：一个真实 server binary 的新隐藏入口正控/拒绝/取消，非构建矩阵 |

与当前 npx/configuration UI 所有已知写集不交叉；任何新增写需求须先更新此表并交主线程串行协调，不能顺手修改 store.go/app.go/servercli/root.go/现有 snapshot worker/schema/generated/docs。首片不需新依赖或 schema 更新。

**Interfaces（拟新增，非现有 API）：**

```go
func InspectRestoreDatabase(ctx context.Context, parent, database *os.File,
    originalMasterKey []byte, limits LocalSnapshotLimits) (RestoreDatabaseEvidence, error)
func RestoreDatabaseWorkerDispatch(args []string) (handled bool, exitCode int)
func (evidence RestoreDatabaseEvidence) Database() (int64, [32]byte)
func (evidence RestoreDatabaseEvidence) SchemaFingerprint() [32]byte
func (evidence RestoreDatabaseEvidence) KeyIdentity() string
func (evidence RestoreDatabaseEvidence) References() (int64, [32]byte, uint64)
func (evidence RestoreDatabaseEvidence) ReferenceCounts() (uint64, uint64, uint64, uint64)
func (evidence RestoreDatabaseEvidence) ObservationMaxAge() time.Duration
func (evidence RestoreDatabaseEvidence) Valid() bool
```

`RestoreDatabaseEvidence` 仅含未导出状态、不可由字段构造；zero value 的 Valid=false，任一错误返回 zero evidence。它不是发布 capability，不能跨写入/路径替换复用。错误只用 `ErrRestoreInvalid`、`ErrRestoreKey`、`ErrRestoreBudget`、`ErrRestoreUncertain`、`ErrRestoreUnsupported`（均在新 types 文件定义）和安全的 context 类别，不暴露路径、SQL、正文、token。Format/JSON/slog 与旧 opaque 类型保持脱敏。

**输入/生命周期约定：** 仅可信内部协调器提供隔离候选目录 FD 和该目录固定 `server.db` 的只读 FD，按目录项+fstat 双向绑定；不是 RPC 任意路径入口。非 O_RDONLY、symlink/hardlink、非本用户/公开权限、候选 sidecar 存在、非 regular DB、无效 limits 均拒绝。caller 必须保证隔离目录没有 writer；首片不取得全实例停服锁，所以不接收 live database。借入 FD 不关闭，worker 使用独立 duplicates；输入 key 复制后清零、不清除 caller buffer。

worker 在任何 SQL 之前安装既有地址空间/SQLite heap/CPU/FD/输出限制；只通过已 pin 的 FD，`mode=ro&immutable=1`、`query_only`、禁 mmap、禁扩展/ATTACH、无 writable_schema/迁移。先复用完整 schema 准入，再 integrity/FK/引用/类别验证，再逐页核验全部保留密文及原 key proof。GitHub 分支复用 `credentialAAD`、原 AEAD 与 `github.DecodeStoredToken`，不为调用方法伪造无连接 Store；configuration 分支复用 `newConfigurationAEAD`/`configurationAAD`，不重写密码方案。原 observation policy 从既有 proof 解封，严格验证现有 `(0,24h]` 边界及 canonical 数值，返回给后继启动；不拿默认 1h 覆盖备份策略。

隐藏模式固定为 `--cognate-restore-database-worker-v1`；只接受恰好该参数和预期匿名 FD，直接普通命令行调用无 FD 返回非零。FD3 请求、4 响应、5 同一 executable、6 parent、7 database、8 独立 32 raw byte key 管道。请求仅 nonce/FD 身份/limits，使用有界严格 typed JSON（≤4KiB）；响应仅状态/无密钥 evidence（≤16KiB），拒绝重复/未知字段和尾随字节。这是进程瞬时控制消息，不是新的备份/重启导入格式，绝不写磁盘或日志。key 不进入控制 JSON、argv、env。复用资源控制函数，不复制另一套 SQLite 准入/备份逻辑。

**红绿任务（以下命令仅为后继实施输入，本次没有运行）：**

- [ ] **T1.1 RED：** 新建下列真实行为测试，再执行 `CGO_ENABLED=0 go test ./internal/server/state -run '^TestRestoreDatabase' -count=1`；初始应因新入口/evidence 未定义编译失败，记录精确失败。补入口后针对每个拒绝项建立“撤去该检查会错误成功”的局部负控，不能把编译失败当作安全规则的全部 RED。

| 测试名 | 精确断言 / GREEN 条件 |
|---|---|
| `TestRestoreDatabaseValidReadOnly` | 两 owner、共享归档引用、双 Pack、分类/关联、加密 config/GitHub 行；Database/schema/key/canonical references 数与既有 snapshot 输出一致；无引用实例也通过；数据库及目录内容前后相同 |
| `TestRestoreDatabaseNoMigration` | 未批准新表、schema 缺列、未知 trigger/sqlite_ 伪装、内部 NULL 元组变造、缺 key proof 分别拒绝；DB hash 不变，无 WAL/shm/journal/补元数据出现，Valid=false |
| `TestRestoreDatabaseOriginalKey` | 正确 recovery key 已解封但原 master key 错/缺/31/33 bytes 均拒绝；正确原 key 通过；不生成替代 key，caller key buffer 不变 |
| `TestRestoreDatabaseAllRetainedCiphertexts` | 两 owner 多行，最后一条 GitHub/config ciphertext 损坏或 AAD owner/revision 不符均拒绝，不能 LIMIT 1；正文/未知 schema payload 只验 AEAD、不启动插件或丢未知字段 |
| `TestRestoreDatabaseReferences` | FK 孤儿、跨 owner Pack item/分类关联、技能数/路径/hash 非法、references 或 category 预算超限均拒绝；正常双 owner 正控通过 |
| `TestRestoreDatabasePolicy` | 合法非默认 policy 原样返回；proof 错 key/AAD/非 canonical/0/超过 24h 拒绝；不得静默补默认 |
| `TestRestoreDatabasePrivateIdentity` | 非私有/非 regular、symlink、多硬链、父目录项被换、非只读 FD、任意 sidecar 拒绝；borrowed parent/DB 仍能读，未关闭 |
| `TestRestoreDatabaseFailureOwnership` | 超时/取消/worker 超 heap/CPU/DB 预算、短响应、输出/关闭故障无有效 evidence；已知退出排空，不明持有/隔离自有资源，不自动 close 重试；脱敏 fmt/JSON/slog/error 中无合成秘密 |

- [ ] **T1.2 GREEN：** 按表新增实现，最大限度直接调用旧 helper；worker 内检查完成、SQL/rows/连接/自有文件明确关闭、子进程已 Wait、宿主重新核验 DB hash/身份之后，外层才构造有效 evidence。错误优先级为不明/清理 > 普通失败/取消；本片不得用全局可变 fault hook。
- [ ] **T1.3 RED→GREEN：** `TestRestoreDatabaseFromAuthenticatedBundle` 使用现有 CaptureSnapshot 真实产物，经 BeginOpen/NextRef/AcceptFile/Finish/CopyFile 后仅新建候选 DB；源目录移走后仍验真，原 key 错则拒绝。`TestRestoreDatabaseSourceCompatibility` 证明旧 cryptographic fixture 与内容 inventory 没改格式。
- [ ] **T1.4 RED→GREEN：** `TestRestoreDatabaseActualServerWorker` 构建一次真实 server executable，测试正确继承 FD 的 worker 成功、裸 flag/多余参数/错 executable/截断 key 管道拒绝；不测试 CLI/GUI 或 18 目标矩阵。TestMain 接点与生产 main 接点均覆盖，避免仅 test helper 能跑。
- [ ] **T1.5 有界验收：** `CGO_ENABLED=0 go test ./internal/server/state -run '^TestRestoreDatabase' -count=1`；`CGO_ENABLED=0 go test ./internal/server/backup -run '^TestRestoreDatabase' -count=1`；`CGO_ENABLED=0 go test ./cmd/cognate-server -run '^TestRestoreDatabaseActualServerWorker$' -count=1`。最后对纯接点相关的既有 `TestSnapshotTask0TrustedCatalog` 与 `TestSnapshotUnknownSchemaRefusal` 做一次定向回归；只对新增/改动 Go 文件 gofmt。输出实际 PASS/FAIL/SKIP 与命令，不用历史全仓收据外推。
- [ ] **T1.6 自审交接：** 核对只写上述 12 条路径、无 import cycle、无 Schema.Create/活库快照调用、无错误 evidence；提交给主线程 review。此计划不要求执行 git 操作，是否合并由主线程现有流程负责。

## 7. 后继实施片、依赖与真实产品验收门

T1 → T2；T3 可在 R2/R3 完成后独立推进；T2+T3+R4/R5 → T4；T4 → T5。下表是完整 C4 路线，不能在 T1 合入后删掉 T2–T5。

2026-11-01 主线程另授权可与 T1 并行的 T2a 叶片：仅新增
`internal/server/backup/s3_restore.go`、`s3_restore_test.go`，实现既有 S3Target 的
`CopyExact(ctx, ObjectRef, io.Writer) (VerifyResult, error)`。复用 Afero S3 受限读取与
既有 transport、取消排空和 `Verified` 枚举；完整对象只取一次，必要 HEAD/安全 probe
为固定有限请求，不重试。精确长度、hash 和关闭全部成功前输出字节都不可信；错误可留下
部分候选，方法不关闭借入 writer，不承诺回滚。此片没有头文件引导、候选发布或恢复入口。

T2a exact2 后继已实现并独审限定 ACCEPT；65 新叶子、62 回归叶子、追加 9 叶及重复检查
通过，主线新增例与 backup vet 通过。独审报告与边界见 `SERVER-BACKUP.md` 的 T2a 小节。
这不关闭 T1 的 P1 或后面的完整 T2–T5。

T2 的 `WithRestoreHead` 还必须显式接收 caller 的私有 parent FD，用于有界 scoped 暂存；
不能在无目录能力的 API 中悄悄采用系统临时目录。借入 parent 与两个只读头文件的生命周期、
清理错误和源 scope 关闭在实现前单独冻结。该设计修正不扩大 T1/T2a 的已授权写集。

T2c 先以不接产品入口的纯 inventory 检查片实施：新 `internal/server/restore/inventory.go`
及其单元/真实生产者测试。只消费既有 SealMetadata、受控 reader 与 T1 canonical references
的长度/hash/计数；在显式 inventory/archive/content 预算内核对完整 inventory 字节、严格 v1
必填头、CutID、引用字节与记录数、repository/pack owner 去重数、仓库归档集合双向相等和
既有 NUL 域 closure 公式。不会创建数据库/归档或签发 Candidate/VerifiedCandidate；归档安全
扫描、每仓库技能核对、文件与目录生命周期仍属于完整 T2。reader 必须合作返回，取消不另起
无法排空的 goroutine；原始读取/JSON 错误脱敏，不关闭借入 reader。不另造备份格式或复制
旧 snapshot 验真规则；canonical bytes 的权威仍由 T1/生产 snapshot 产生。

T2c 后继已独审限定 ACCEPT，三源 SHA 与冻结一致；真实生产者/源移走后的 T1 对接和主
staged-tree 组合通过，详细命令与边界见 `VERIFICATION.md`。不是完整 Candidate：后继须
绑定真实 DB evidence 的全部身份字段，实际核验归档与每仓库技能，不能只信被动期望。
T2d 归档语义叶片另以新 `internal/server/restore/archive.go` / `archive_test.go` 隔离实施：
只消费有界 borrowed bytes 与 canonical 仓库技能期望，复用 repository.InspectArchive，
不修改 T2c、不添加文件/网络或激活能力；完整 T2 的文件材料化/关闭与 Candidate 生命周期
仍由后继协调器负责。

T2d 后继已独审限定 ACCEPT，两源与冻结 SHA 一致，主 staged-tree 组合通过。下一实施
应完成完整 Candidate 而不是把纯检查升级为发布能力：从已验真的 canonical inventory
引用建立仓库/技能期望（根技能目录为 `.`，不自行加 SKILL.md），避免再造独立 SQL 扫描；
实际 DB 的 bytes/hash/schema/key 必须与 T1 evidence 绑定，所有归档文件集合双向相等，
单次 archive 预算必须纳入整个候选的累计预算。材料化、借入源关闭、私有 FD/目录身份与
清理不明、WithVerified 的作用域及后继发布所有权转移须在编码前冻结并测试。

| 任务 / 精确预期写集 | 消费 / 产出接口与验收 |
|---|---|
| **T2 完整候选闭包**：新 `internal/server/restore/candidate.go`、`inventory.go`、`verify.go`、对应 `_test.go`；新 `internal/server/state/restore_assets.go`（T1 helper 仅在真实所需时扩展）；新 `internal/server/backup/s3_restore.go` 及测试 | 消费 bundle 和 T1 evidence；`StageCandidate(ctx context.Context, bundle backup.CryptoVerifiedBundle, parent *os.File, originalMasterKey []byte, limits CandidateLimits) (*Candidate, error)` 只借 bundle，返回自有文件/密钥副本的 Candidate，不授予激活权限；`(*Candidate).Close() error` 明确取消/排空/清零与精确清理。协调器先完成 §3.12 的源清理，再调用 `(*Candidate).WithVerified(ctx context.Context, consume func(context.Context, VerifiedCandidate) error) error`；CandidateLimits 在 candidate.go 定义显式资源上界，VerifiedCandidate 只在该回调有效。完整双向集合/closure/AEAD/附件关系通过才进入回调；不把候选编成新的持久导入格式。错切点/少私有 owner/缺或多 archive/伪 header/count/closure/损坏分类全有 RED；脱离源目录、禁止上游 fetch 的多 owner fixture GREEN；追加“头文件最终 close 故障时绝不调用 Activate”的顺序测试 |
| **T3 稳定运行根与 startup 栅栏**：新 `internal/server/runtimegen/lease_unix.go`、`lease_other.go`、`selection.go`、`layout.go` 与测试；串行修改 `internal/server/app.go`、`backup_runtime.go`、`internal/servercli/root.go`、`internal/server/state/masterkey.go` 和相关测试 | 消费 R2/R3；`OpenRuntime(ctx context.Context, root string) (*Runtime, error)` 在新包定义，只完成安全根检查和 SH lease；`(*Runtime).Resolve(originalMasterKey []byte) error` 完成 current 认证并固定业务代，Resolve 前禁止取得业务路径；空实例 key 初始化只接受该 lease 证明的严格空状态。`Close() error` 最后释放。恢复协调器通过同包 `LockOffline(ctx context.Context, root string) (*OfflineRoot, error)` 取 EX。两者同一 root/inode/fence 权威；legacy 只在没有任何恢复控制痕迹时接纳。真实 server（backup 开/关各一）存活或 startup/close 阻塞时 EX 失败；多普通服务 SH 不互斥；恢复持锁时 startup 在 SQL、cache/log 写前拒绝。worker 继承必要 lease，父进程死亡仍无并发写入口 |
| **T4 撤销、发布、status 与回退**：新 `internal/server/state/restore_credentials.go` 及测试；新 `internal/server/restore/operation.go`、`activate.go`、`rollback.go`、`status.go` 及测试；新 `internal/server/runtimegen/publish.go` 与测试 | 消费候选 capability、OfflineRoot、R4/R5；`Activate(ctx context.Context, root *runtimegen.OfflineRoot, candidate VerifiedCandidate, request ActivateRequest) (Result, error)`、`Reconcile(ctx context.Context, root *runtimegen.OfflineRoot, operationID string) (Result, error)`、`Rollback(ctx context.Context, root *runtimegen.OfflineRoot, request RollbackRequest) (Result, error)`；类型均在 operation.go 定义。request 固定 UUID/预期 current/digest/策略版本，Result 是非秘密 phase/code/身份摘要，错误不代表没切换。以 §4 每个 durable 边界注入失败并重启新进程，验证无混代、未知拒启、撤销不复活；候选事务后重新验真，切换前取消不改 current，切换后取消不能声称回滚 |
| **T5 产品入口及停服→恢复→启动→业务读取**：新 `internal/servercli/restore.go` 及测试，串行修改 `root.go` 注册 restore 子命令；新 `cmd/cognate-server/offline_restore_integration_test.go`；后继产品文档 `docs/SERVER-RESTORE.md`，最后串行更新 REQUIREMENTS/LOCAL-SNAPSHOT/SERVER-BACKUP/FORMAL-RELEASE-AUDIT | `cognate-server restore prepare/status/activate/reconcile/rollback` 独立离线入口，不创建 App/listener；私有配置文件供应两 key 路径、recovery key ID、源 attempt、原始密文目录或只读 S3、预算和部署信息；operation/current/digest 明确确认。restart 先进入受限验收 gate：禁后台 refresh/Lists/备份、非本地入口及业务 mutation；只开放新登录和验收读取，显式确认通过才进入正常服务。真实二进制演练覆盖下述 8 项，不得只 App.Open/库测试 |

T3/T4/T5 的现有共享文件修改须由主线程分配串行集成窗口，不能让后继实现者据此与当前 UI/npx reviewer 并写。后继接口中尚未存在的类型均是拟新增；第一片没有调用这些类型或依赖它们已合入。

**T5 必须留下的端到端收据：**

1. 启动真实 server，使用完全合成账号/上游/秘密创建两个 owner、公开+私有仓库、共享归档、双 Pack、分类/附件/配置资产，创建 session/share/MCP/native 凭据；调用既有手动备份入口产出原格式密文。另留“备份后已撤销但备份中有效”的 token 作为复活反例。
2. 原进程运行时 restore 拒绝；发 SIGTERM，确认端口退出、进程终结/锁释放；关闭不明或 worker 未退出则恢复拒绝。明确区分受控进程故障测试与硬件断电资格。
3. 移走原运行目录，禁 GitHub/原 cache 网络读取；从下载的同一原始密文和两外置合成 key 恢复全新目标，也做覆盖恢复。真实 TLS S3 fixture 是传输合同证据，外部供应商隔离 bucket 的演练仍属 E8，不接触本机真实 credentials。
4. 重启实际 server 进入验收 gate，经真实 Login/GetSession、新会话读取仓库、Pack、分类、附件/排序元数据；受权读取加密配置验证合成秘密完全一致且响应/log/storage 安全，业务状态不是空库。schema/原 key/部署策略错误必须在服务开放之前拒绝。
5. 经真实分发 RPC 下载 skills，逐文件比对完整目录、隐藏/二进制/可执行位与 content hash；确认私有内容不出现在匿名目录；分类删除不删技能的既有行为仍成立。重新显式授权 share/MCP 后读取成功，所有旧 session/share/MCP/native token 拒绝。
6. 验收期间普通 mutation/外部调度均拒绝；明确记录部署者确认后才开放正式写入。此阶段 key/账号口令和 token 不进入 argv/env/日志/磁盘收据，只有合成秘密摘要与操作结果。
7. 在候选复制、撤销事务、current rename 前后、fsync/close、启动和业务读取各边界终止/注入故障，重启后观测真实 current/previous；unknown 必须 fail-closed，只读核对之后显式收敛。逐项记录实际落盘而非“原子回滚成功”。
8. 制造 candidate 启动失败和业务读失败，确认新进程停止后显式回退 sanitized previous、再真实启动和业务回读；旧 token 仍拒绝。首版无 previous 的全新实例失败保持停服，不发明回退；对已开放写入后的回退明确记录数据丢失确认。

只有 T5 的这些实际产品路径通过，才能称 C4 的该平台范围完成。完整 F4 还需自动调度、保留/孤儿生命周期与 E8 外部供应商恢复演练；其它承诺平台保留各自实现/运行门，不能把 Linux 内部 GREEN 扩成完整 GA。

## 8. 本次设计自检与交接日志

- [x] 读取四份指定文档及现有 seal/open/bundle、snapshot/reference、cache/library、key/目录、startup/maintenance 代码；用当前实现覆盖过时边界，不重复历史备份实现。
- [x] 首片定义精确新增 API、opaque evidence、12 条 disjoint 写集、真实正控/负控与定向命令；完整产品验收保留在 T5，未偷减用户范围。
- [x] 明确双 key、普通模式锁缺口、自动迁移风险、完整闭包、schema/降级门、隔离候选、unknown/fail-closed、撤销、发布与回退；待决 ruling 显式挂在依赖片前。
- [x] 仅在 /tmp 写计划；未实现源码、未访问真实 credentials、未运行产品测试/构建矩阵、未 spawn agent 或做任何 git 写操作。
- [ ] 后继实施前由主线程确认 R2–R6 与平台/文件系统资格；T1 可独立进入现有 review 队列，不要求当前提交等待这项工作。
