# B2：服务端本地一致 SQLite 快照

状态：设计冻结候选，待独立设计审；**尚未实现**。本文件细化 `2026-10-28-server-backup.md` §3.1 / §11 中 B 的数据库子片，不替代总规格，不是实施计划。

## 1. 权威、范围与待补接缝

权威需求为 `docs/REQUIREMENTS.md` F4：自动/手动触发、收录 skill 内容及仓库/Pack 配置、本地 SQLite 快照、S3 远端存储、恢复。Go 1.27.3、CGO_ENABLED=0、ent、SQLite、Afero 等既定技术不变；B2 不借缩减 F4 获得验收。

当前基础：B1 managed instance / Store writers / cache admission，B1b library Collect、Refresh、独立 RefreshAutomatic 的外层 scope，以及 B1c selected-Lists 首次 cache→DB publisher。`Store.WithMaintenanceWriter` 覆盖 cache Put 至提交/回滚及清理；`library.ValidateMaintenancePair` 通过 nested cache admission 和实际 root 复核配对。默认 unmanaged 的多进程行为不变。

总规格尚不足以直接实施的接缝在此固定：

1. `Instance.Quiesce` 只 drain writer；Store.Close 先关闭 SQL client，再释放 instance。仅拿 Exclusive **不能**保护 Store SQL 生命周期。cache.Close 亦有类似问题，不能把短暂配对验证当作长期 cache pin。
2. Store 当前未保留可供快照使用的 DB 路径/文件身份，也没有导出 SQL pool；不能从生成 ent client 反射取 driver，或让 caller 注入 path/Instance。
3. 已有 SQLite 能力测试验证了 VACUUM INTO 的 WAL/取消/中断行为，但不是配额、Store 生命周期或内容闭包实现。普通 Afero 限流 writer 拦不住 SQLite 自行写目标；事后 Stat 不是运行中硬配额。
4. SQLite 文件完整不等于归档闭包完整；B2 完成不能成为释放屏障后再找 live archives 的理由。

B2 只提供 **受管 Store 的 scoped 数据库候选及有界引用清单**。不新增 App managed 开关、coordinator/HTTP/RPC、scheduler 暂停入口、归档复制、加密/上传/保留/恢复，不写业务表或 key，不为默认模式增加目录锁。B3 负责内容闭包，后续片负责其余 F4 能力。

## 2. 受信任的最小 API 与生命周期

候选入口为 Store 方法 `WithLocalSnapshot(ctx, limits, consume) (LocalSnapshotSummary, error)`；`consume` 是进程内可信 coordinator 的同步回调，接收 scoped context 与不透明 `LocalSnapshotView`。这不是可供用户、JS、插件或 RPC 注入的回调。候选名供后续计划固定，不在本规格阶段创建 Go 类型。

- caller 不能提供源/目标路径、DSN、Instance、Exclusive、SQL、已有目标文件或任意 filesystem。源唯一绑定接收者 Store；目标只能由宿主在其 canonical dataDir 下创建。
- 双 unmanaged 的 `ValidateMaintenancePair` 会返回 nil，因此 B2 必须另行要求 Store managed；nil、closing、poison、非受管均拒绝，不自动 Join/降级。平台能力复用 `maintenance.CheckPlatform`，不重复平台名单。
- Store 成功 Open 后保留私有的 canonical DB parent、DB basename 与物理身份。B2 进入、打开专用连接后及结束前复核 parent/DB；拒绝 symlink、hardlink、路径替换及别的 DB 文件。相同 gate/dataDir **不等于相同 DB**。路径验证复用现有 state 目录/文件规则；不得把可替换的字符串别名当作身份。
- Store 内新增独立的 operation pin：在生命周期 mutex 下检查 closing/poison 并计数，覆盖排队、SQL、检查、回调和清理；不借用 writer 租约。Close 先拒新操作，再等待 writer 和 snapshot pin 都 drain，最后才关闭 SQL/释放 instance。不得持 writer lease 等自己的 Quiesce，也不得 Quiesce 后再 Enter(writer)。
- 顺序为：ctx/参数/平台与身份检查 → Store pin → 本实例 Quiesce(ctx) → 复核身份/预算 → 快照及验证 → consume → 关闭 reader/删除 B2 临时产物并处理结果 → Release Exclusive → unpin。Exclusive 必须属于此 Store 的 live instance，不能采用 caller token。同 gate 多 Store 共享互斥；另一快照不得并发进入回调。
- 有效 writer context 内调用、递归调用及已经过期的 view 都固定拒绝。新 writer 在 quiescing 时仍遵守现有 ErrQuiescing 行为，不排入无限隐式队列。
- view 只提供受限元数据副本、按固定顺序的引用分页、向 caller writer 流式复制已验证 DB；不导出路径、ent client、连接或可逃逸句柄。不接受用户查询。每个方法重验 view live/ctx；异步 child 不得越出回调，回调返回即失效。B2 内部不创建 detached 工作 goroutine。
- 回调及外层均成功、所有资源/临时产物明确收尾后才返回 summary；任一失败结果为零。调用方必须以**外层 nil error**为结果发布门，不能在 callback 内对外宣称成功。panic 必须先走同等清理；不把 panic 当成功。

## 3. 无 cgo 快照机制与 ent 接缝

### 3.1 选型及实施前能力 gate

推荐已锁定的 `modernc.org/sqlite v1.60.1` **原生在线 Backup API**，不新增库、不调用 sqlite CLI、不复制 live main/WAL/shm，不使用全库 `Serialize`。`github.com/lib-x/entsqlite v0.2.6` 的 sqlite3 driver 返回 modernc 原始连接；可通过专用 `database/sql.Conn.Raw` 在私有窄接口上使用 `NewBackup`。这是替代总规格“建议先验证 VACUUM INTO”的机制选择，不改变一致切点/闭包合同。

理由：VACUUM 的既有能力 green 不证明其输出与临时空间的运行时硬界；Backup 按页复制，允许在同一固定源切点预检 page count、逐步检查进度和取消。它可能保留 freelist/已删除内容，**不是净化工具**；全实例物理敏感边界不变。不得把现有 VACUUM 测试冒称 Backup 已验证。

实施的第一 gate 必须用 pinned driver、真实 WAL 和临时 DB 证明下述完整路径，包括目标 pager 模式、页数/字节界及失败资源归属。若不成立，停止此实现并申请规格修订；不得静默切回无硬预算的 VACUUM、引入自制 writable VFS/新库或只跑 Happy Path。

### 3.2 连接及复制协议

1. Exclusive 内打开唯一专用源 pool（max-open/max-idle 各 1）及 Conn，宿主构建 URI `mode=ro`，**live 源禁止 immutable**。源必须已存在，保留其 WAL、synchronous 等设置；不改变源 journal mode、不运行迁移、checkpoint、修复、用户 SQL 或外层写事务。复核其绑定 DB 身份；以可信查询取得 page_size/page_count 并做检查溢出的预算运算。合作 writer 已 drain，因此该源视图在整次操作中固定；墙钟/data_version 不用于发明 revision。
2. 私有 staging 中 O_EXCL 预建空的 0600 目标，复核 inode/parent；`NewBackup` 只能打开该受控文件（mode=rw，不允许缺失时创建另一文件）。目标 URI 固定 **journal_mode=OFF，仅用于这份一次性、未发布、失败即丢弃的目标**。不在目标执行领域写入。这避免把不受计数的目标 WAL/rollback journal 放在预算外；不更改 live 源配置，不作为运行 DB/restore 的推荐模式。失败/中断后绝不复用该目标。
3. 在同一 Raw callback 内创建 Backup；每次 `Step` 至多 64 页，前后检查 ctx、页数、实际已复制/目标字节及全部预算。BUSY/LOCKED 只允许继承总 deadline 的有界重试，不改变业务 COMMIT/CAS/Source/MCP 逻辑。不能调用 `Step(-1)`、无限循环或因 busy 重新开放 writer。必须确认 target 页大小与源一致、无意外目标 sidecar/临时文件；能力 gate 要证明空目标+固定分页复制的文件增长界，不能只靠测试结束后 Stat。
4. 源 page_count×page_size 作为硬 admission 上界；复制前超过 MaxDBBytes 则根本不启动 Backup。固定切点下输出按源页布局复制，始终不得超过该上界；每一步核对 Backup.PageCount 与预检值，偏离即失败。`max_page_count` 即使加作防御也不能代替此证明。删除/空洞页仍计入预算，不能按活记录大小猜测。
5. 无论完成还是中止，已创建的 Backup 都只调用一次 `Backup.Commit()` 终结（其底层为 backup_finish，名称不代表未完成副本也成功）；若取得目标 driver.Conn，必须**显式检查 Close 结果**。只有 Step 已报告完成、ctx 有效及全部验证成功才认可候选；中途取消即使 finish/Close 成功也仍失败。再关闭专用源 Conn/pool，逐个确认。不能调用会忽略目标 Close 错误的 `Backup.Finish()` 然后宣称已确定关闭。初始化/Step/Commit 错误路径同样负责终结 backup 对象；若 driver 错误分支内部丢失 Close 结果，按 §7 不确定资源处理，不推断已关闭。
6. 所有写句柄已明确关闭后，复核目标文件类型、0600、单链接及身份；File.Sync、Close 并同步 staging 目录。新建 staging 的父目录亦同步。不得依赖 SQLite 隐式 fsync 或拿 SQL 成功替代持久性。同步失败不返回有效候选。

### 3.3 无副作用检查器

再开独立 mode=ro 的 snapshot reader；只有已完成、私有、不再修改的目标可使用 immutable。用 `ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, pool)))` 接现有 ent 查询，或使用等价私有参数化 SQL；明确单一 close owner，不双重 Close。不改 ent schema/生成器/生成输出，不取 live ent 私有 driver，不调用 state.Open/Schema.Create 或会补 key proof/history 的 helper。

必须完整消费并检查：integrity_check 仅一个 `ok`、foreign_key_check 零行；SQL rows/error/Close 均验证。纯检查现有 ServerMetadata 的 key identity/proof，使用 Store 已有 key 能力，不创建/修复记录、不暴露实际 key。未知/缺失 schema、错 key、缺字段、孤儿或领域关系不成立整次失败。检查前后文件 SHA-256 相同且无新增 sidecar；不得把 SQLite integrity 通过当成应用 schema 兼容批准。

## 4. staging、预算与可取消边界

- 位置固定为受管 dataDir 中宿主控制的私有快照 namespace；目录 0700，所有文件 0600、regular、单链接。每次独立随机不透明 ID（至少 128 随机 bit），O_EXCL/新目录，不以 owner、repo、时间或 URL 命名。SQLite 文件系统仍为真实 OS，不能用可注入 Afero MemMapFs 冒充 OS 身份；未来 S3 仍按既定 Afero 能力边界接入。
- `limits` 为实例可信策略的必填正值：MaxDBBytes、MaxInventoryBytes、MaxRecords、MaxStagingBytes、DrainTimeout、OperationTimeout。无零值无限、隐式产品容量默认；容量值由后续实例策略批准，不能把 cache 的 512 MiB 当备份上限。新超时只能缩短 caller deadline。页/长度/加乘/总和先查溢出。
- B2 自身最多一个源 Conn、一个 backup 目标、一个只读检查器（阶段间关闭），固定 64 页复制步长及最多 128 条/页引用；单条编码最多 64 KiB、metadata 最多 64 KiB，按字节/条数逐条预检再写。记录超界失败而非截断。不得全表 All、ReadAll、全库 []byte、无界 channel 或 offset 扫描；已有非法/超大领域数据不能因备份而被修改。
- 64 页是复制步长而非整个进程的 RAM 承诺；计划必须同时计入源/目标 SQLite pager cache、完整性检查的页数相关 bookkeeping、ent 分页及编码 buffer，并用既定 MaxDBBytes 推导最坏界。cache_size 只能作优化，不能冒称硬内存 quota；真实大页/freelist fixture 记录峰值，不把小库低分配等同无限规模保证。
- MaxStagingBytes 硬计入 DB 全页上界、实际 inventory 和元数据；预留各自上界再允许生成，以受限 writer 控制宿主输出，Backup 固定页数/无目标 journal 控制 SQLite 输出。不能用最终长度合格掩盖中途超写。若 B3 复制到另一 staging，**两份 DB、引用、archives 及加密工作区均另行计入整次任务预算**，不能重复使用同一份额度。
- 可用空间预检只是 admission 信息，不能保证 ENOSPC 不发生或等同 filesystem block quota；稀疏/元数据/同卷别的应用仍可耗尽空间。此处硬预算是本片控制的文件逻辑字节，不承诺隔离整个 volume 的物理块消耗，不暗加运维 quota 配置。若能力 gate 无法证明 SQLite 自有文件的上述逻辑字节界，属阻断而非允许超预算。
- 所有可取消 SQL 使用同一 scoped ctx；Backup Step 本身无 context 参数，必须实际证明 bounded Step/BUSY 行为，取消在步间生效。内核 I/O/fsync/Close 无硬实时取消保证；不以 goroutine+select 提前返回并遗留写者。截止后仍先 drain/收尾，不把“请求超时”说成句柄已经关闭。
- callback 只作有界本地消费，不做网络、KDF、JS、远端重试或新领域写入；它须传播 ctx，不能另起未等待任务。B2 不为不合作的任意 callback 提供强行中断能力。未来 coordinator 必须使用受控实现，而非允许外部注册 hook。

## 5. 私有格式、身份与引用 oracle

本地候选组成固定为 `snapshot.sqlite`、`references.ndjson`、`candidate.json`；这些是固定内部 basename，不是路径 API。metadata 标识 `format=cognate-local-sqlite-candidate`、version=1、`contentClosure=not-included`，包含随机 ID、DB bytes/SHA-256、SQLite 版本、受支持 schema fingerprint、引用流 bytes/SHA-256、各 kind 总数及总条数。时间仅展示，既无全局 revision 也无恢复承诺。

- 指纹来自已支持应用 schema 的规范化表/索引/FK 定义（固定顺序、长度前缀后 SHA-256）；实现须固定独立 fixture oracle，排除运行计数/路径等非 schema 数据。记录 fingerprint 不等于允许任意新 schema，不能拿当前未知 schema 自算 hash 后自己通过；必须匹配实施基线明确支持的定义。没有凭空发明的迁移版本。
- metadata/引用严格 JSON v2 编码；固定字段、版本、kind、UTF-8，拒绝未知/重复/尾随、计数/长度溢出及不一致。读取限额先于分配，不按 DB 自报计数申请大内存。此本地格式不是总规格的远端加密 manifest，不兼容通用上传/恢复入口。
- 引用以固定 kind 顺序 repository→skill→pack→pack_item，每 kind 按 DB 主键 keyset（id > last，固定 LIMIT）枚举，分页不因排序临时全表物化。规范输出必须包括：repository 的 ID/public ID/owner/canonical repo/branch/archive hash/skill_count/revision；skill 的 ID/public ID/repository/path/content hash；Pack 的 ID/public ID/owner/revision/item_count；item 的 ID/Pack/skill/slug。具体 revision 对应现有字段（repository 的 publication_revision、Pack.revision），不另造全局计数。
- **全实例所有收录仓库/skills、所有 Pack/items**，含 private、未 published、未进 Pack 的收录内容；不能过滤成某 owner 或当前可下载内容。先按现有领域规则验证路径、hash、slug、revision、计数、唯一性和 owner 关系，禁止 dangling/跨 owner item。大字段先以受限长度查询检查，不能先读入无限字符串再拒绝。
- 应用 schema 其余表（源配置/加密凭据、用户、session、私有历史、native/configuration/proofs 等）由物理 DB 完整保留；不因不属 archive 枚举而删表，也不把秘密展开进引用。多 repo 共用 archive hash 可以重复引用，B3 可按 hash 去重，但不丢 repo/skill 关系。
- 完成一遍全关系校验及 inventory 封存后才调用 consume；view 分页读同一已封存引用流，DB 流复制受长度上限和 hash 复核保护。B2 只证明 DB 关系及引用语法，不证明被引用的 archive 真存在/内容正确。
- DB、metadata 和引用均为全实例敏感数据；hash 只是完整性检测，不是来源认证/加密，不可用攻击者可重写的 hash 自证可信。无公开路径、下载链接、owner DTO 或日志内容。summary/view 的 fmt（非常规 verb/容器）、JSON、slog 均须无内容泄漏，借鉴已有 opaque 私有 payload 模式；不能仅依赖 Stringer。

## 6. B3 内容闭包的强制交接

B2 **不产生 snapshot_ready**，只在回调期间提供数据库候选。B3 必须在同一次 callback、同一个 Exclusive 持续有效时：验证真实 managed Store/cache physicalpeer、独立持有 cache/root 生命周期 pin、将 DB/引用与所有引用的归档复制到自己的私有 staging，重算 hash，并以既有 repository/archive 限制验证 skill path/content hash、owner/Pack 关系；flush 文件/目录并封存完整本地 inventory。少一个引用、缺档、超界或解包不符均整次失败，不能在线补拉。

现有 `ValidateMaintenancePair` 是 writer admission 检查，**不能在 Exclusive 内调用**，更不能替代 B3 cache pin。B3 需要在 Quiesce 之前完成配对/生命周期 admission，并在其后持续复核物理身份；其窄接缝及源码写集另行授权。本片不暗授该 cache API。无 B3 时允许测试回调检查/复制 DB，但不允许产品备份入口。

B3 的产物只在外层 B2 成功并明确释放/收尾后可被 coordinator 接受为本地 sealed 候选；callback 内 seal 不可提前发布状态。不得返回 live 文件路径、硬链接或释放后再补拷。B2 的临时 DB 总是清理，B3 独立副本另负预算和失败清理责任。远端加密/上传在 Exclusive 外处理独立 sealed staging；remote_committed、restore_verified 保持不同状态。B2/B3 均不授权长期保留明文候选。

## 7. 失败、unknown、清理与信任范围

- 普通取消/SQL/验证/预算/callback 失败：零 summary；确认 backup/所有 SQL/文件句柄已关闭，删除本次受控子目录并同步父目录，然后释放 Exclusive/pin。不能删除别的任务/有效备份，也不能覆盖原始 DB。unknown COMMIT 的既有业务 receipt 规则不改；备份不是重试/修复领域事务的入口。
- **关闭不确定与内容不合格分开**：Step/Commit/driver 内部 Close 丢失结果、显式 Close 失败等导致仍可能有资源时，Store 进入 cached snapshot poison。保留未决对象和 Exclusive/instance/root ownership；拒绝后续写/快照，Close 返回固定 cached error，不重试、不误释最后目录锁。operation pin 的控制计数可终结，但未决 ownership 转入 poison，不让 Close 在自身 active 上永等。不得复用 partial target 或声称 rollback 完成。现有 writer Close 正常/失败语义不得被重写削弱。
- 单纯删除/目录同步失败、且所有句柄确定关闭时，不必伪造未知 SQL 状态：返回固定 cleanup 错误、标记私有 orphan，允许释放 gate。失败产物绝无 ready 身份；遗留明文是明确风险，不是后台静默成功。若不确定还存在可写句柄则仍按 poison。
- 错误仅固定分类（ctx canceled/deadline、unsupported/unmanaged/closed、identity/busy/budget/invalid、cleanup/uncertain）；不包装输出原 SQL/driver/路径/DB行/回调错误。回调细节同属敏感，不用 errors.Join 把秘密再带出。可保留 errors.Is 受控分类，不把恢复材料写日志。
- 崩溃无可恢复 receipt：下次启动不得信任 candidate.json 为完成记录。后续 coordinator 冷启动取得同 dataDir 实例锁后，以固定 namespace、可信父 root、生成名/文件类型/owner/权限/无 symlink/hardlink 校验后做受限清理；遇未知结构保留并报警，不递归跟随任意路径。本片没有自动扫描/清理入口，测试只清理自己生成的子目录。无 SSD 安全擦除承诺。
- 保证范围是冷启动加入 managed gate 的合作 writer；默认 Store、旧 binary、raw SQLite、手工脚本、同 UID/root 恶意写者不受物理实例锁约束。B2 不探测/“修好”旁路写者、不承诺对其一致性。产品启用前必须停止旁路并证明所有持久 writer 纳入屏障；本片不宣布 App 已满足。

## 8. 真实行为验收与负控（未来实现，未执行）

1. **先能力后集成**：实际 public Store 入口与 pinned driver，CGO0/Go1.27.3，TempDir/合成 key/双 owner；保留原 VACUUM 能力 gate。新 Backup gate 验 mode=ro 源 WAL、目标 OFF/无 sidecar、逐页界、Commit→显式 Close、取消/BUSY/完整性。旧实现缺接口/编译错误不算行为 RED；先建立可编译最小 seam 再用真实 I/O 出 RED。
2. **WAL 独立 oracle**：建立未 checkpoint 的已提交 revision、独立未提交事务、后续 commit，证明 snapshot 含且仅含切点状态；独立只读 SQL/ent oracle 对照双 owner 仓库/skills/Pack/revision/私有历史/配置行，原 DB bytes/WAL 不被检查器改写。main-only 拷贝必须被同 oracle 拒绝。已删大数据/freelist/页边界及满预算使逻辑页上界真实受测，不仅小空库。
3. **drain/lifetime/配对**：真实 Store/cache/library Collect、Refresh、RefreshAutomatic、selected-Lists 初次发布在 cache→commit/rollback/尾部 cleanup 设受控 I/O barrier，Exclusive 必须等整个 publisher；网络 fetch 停住时 Exclusive 可先进入。多 Store 同 gate、不同 DB basename、不同 physical root/混合模式、DB/parent 替换、过期 view、递归/writer ctx 均拒绝。Close 与排队/Step/reader/callback/cleanup 竞争必须等 pin；物理 Close 失败仍持锁，真实同目录子进程打不开 managed instance。默认 unmanaged 多进程行为原样。
4. **真实故障**：备份中 barrier 到达后取消/kill 子进程，不以 sleep 猜时序；重启不得消费 partial。逐项 File.Sync/parent Sync/Conn.Close/pool.Close/driver Commit/删除失败、空间不足、超字节/记录/超大字段、truncate/篡改、错 schema/key、孤儿/FK/计数不一致，全部零结果，资源/锁/明文遗留结果准确。仅 fault seam 返回错误不能冒充已证明真实磁盘掉电持久；late-cancel/晚 Close 结果必须 withheld。ctx 取消后无遗留后台 worker；真实 syscall 阻塞不靠 overall timeout/panic 充 RED。
5. **B3 边界**：临时测试消费者在 callback 内复制 DB/引用并 fsync；回调阻塞期间 writer/GC 不得穿透，callback 返回后才释放。缺 archive 的 B2 关系检查可以通过，但不能得到 snapshot_ready；未来 B3 负控单独证明闭包缺失整次失败，不以本片 DB green 抵账。
6. **隐私及格式**：严格版本/重复/尾随/超界、snapshot 文件缺失不得创建；未知数据不能被迁移“修好”。正常/错误/取消/poison 的 fmt 各 verb、私有容器、JSON、标准 log/slog 捕获均不含合成 key/token/敏感行/回调 sentinel。正常日志仅受控分类/boolean/计数。
7. **至少三类真正负控**：编译成功后分别移除 Store lifecycle pin、改成 live main-only 拷贝、提前释放 Exclusive（callback/cleanup 前）；各由原 oracle 有界行为 RED，恢复 GREEN。另覆盖页预算跳过/忽略 Close/读取 live 而非 snapshot/只枚举某 owner。报告每个 mutant 的实际命令、run/pass/skip、退出和 cleanup；死锁/panic/编译失败不算有效 RED。

Unix 的真实子进程测试与 Linux/受支持 Unix 行为验收分开记录；非 Unix failclosed 用现有 CheckPlatform 分类及必要平台运行验证，crossbuild 仅证明编译，不能宣称 runtime PASS。不使用 CGO1/race、真实 HOME/DB/用户 key/网络账号。

## 9. 候选最小写集及授权出口

后续计划候选：修改 `internal/server/state/store.go`（私有 DB 身份）、`internal/server/state/maintenance.go`（独立 pin、Close/poison），新增 `internal/server/state/local_snapshot.go`、`local_snapshot_unix.go`、`local_snapshot_other.go`、`local_snapshot_unix_test.go`、`local_snapshot_other_test.go`，共七源码路径；文档进度另列。新 helper 默认私有，只有 Store scoped API/view/limits/summary 必要公开。若跨平台拆分或既有 Close fixture 需要别的路径，先列精确扩集审，不偷改维护原语/业务 writer。

不改 App/library/cache/scheduler、ent/schema/generated、module/dependency/proto/UI、备份总规格或已经冻结的 B1 文档。B3 所需 cache pin/coordinator 不借本授权实现。此文件通过 design review 后才可制定计划及另授源码；当前只有**子规格候选**，不声称本地 snapshot、全写屏障或备份已可用。
