# B3：服务端本地快照的归档内容闭包

日期：2026-10-29。状态：**子规格候选，待独立设计审；未实现、未运行本规格验收**。本轮只写本文与 ignored 作者报告，不授权计划、源码、依赖或产品入口。

## 1. 权威、模块边界及前置条件

本模块是既有 Backup B 划分中的 **B3 内容闭包**，不是新阶段。权威输入：

| 输入 | 固定身份 / 职责 |
| --- | --- |
| `docs/REQUIREMENTS.md` F4 | 自动/手动备份、skill 内容、仓库/Pack 配置、SQLite 本地快照、S3、恢复，完整首发需求不缩减 |
| `docs/superpowers/specs/2026-10-28-server-backup.md` | SHA256 `ca0a6c84ef6bf976917cf51c3dbbc2c006564bc7dc494fb8a9cb89e4462beae3`；全实例一致切点、敏感数据及后续模块边界 |
| `.superpowers/sdd/2026-10-28-release/backup-B-implementation-proposal.md` §6 | SHA256 `b6ae699db9b9012b3f8f277d28285bf12897f662e5a2aa9cc354356e3ba13e10`；B3 为 coordinator/inventory/stage、受限 cache 复制、既有 archive 检查薄封装 |
| `docs/superpowers/specs/2026-10-29-server-local-snapshot.md` §2、§5—§7 | SHA256 `8b9f6f19611d7d6cabc8af253a1ceb8eca7a5255d8a8de0d88a4d7bfd2f309f8`；已签 B2 scoped view、引用格式、同一 Exclusive 的 B3 交接 |

B2 原计划的 REJECT 证据保留；后继计划的设计修订即使通过，也不等于 native Backup 能力、源码或运行验收通过。B3 依赖的是已签 B2 合同，**不把 B2 API 当作现有实现**。在真实 B2、受管 cache pin 及本规格行为门全部通过前，不能返回产品 `snapshot_ready`。

B3 的唯一交付能力：可信进程内调用获得同一切点 C 的独立 DB、完整引用与全部被引用仓库归档，经领域校验后形成短生命周期私有 sealed 候选。不是公开下载接口，不启动 managed App，不注册 HTTP/Connect/MCP/CLI/UI、定时器或自动备份，不实现加密、S3、远端提交或恢复。B4 的 App/真实服务屏障验收与其余 F4 模块仍必需；不能把“本地闭包完成”称为备份可用。

## 2. 现实接缝与最小受信任接口

当前 `ArchiveCache` 的事实：`Put` 已有 managed writer admission；`Read`/`Verify` 复用私有 `copyVerified`，按固定 hash 名读取、限 32 MiB 并重算 SHA-256。当前私有复制器的 deferred Close 不汇报关闭错误，也不提供覆盖整个 B2 的生命周期 pin，不能直接改名导出便宣称达标。`ValidateMaintenancePair` 通过 Store→cache nested writer admission 验实际 gate/根；双 unmanaged 也可返回 nil，且该函数**不能在 Exclusive 内调用**。

后续计划须提供以下窄能力，名称是本规格的候选 API，不代表已有符号：

- `ArchiveCache.WithSnapshotSource(ctx, store, consume) error`：只接受真实 Store/cache 对象，二者必须 managed；在 Quiesce **之前**建立独立 cache/root pin、完成现有实际配对验证，再同步调用可信 `consume(ctx, source)`。source 绑定确切 cache、Store、物理父目录/根及本次存活期；不接受 owner 字符串、自报 ready、path、Instance、Exclusive 或替代 FS。两者同 gate 仍须由 B2 绑定确切 DB，不能把另一个 basename 的数据库混入。
- 不透明 `SnapshotArchiveSource.CopyVerified(ctx, hash, destination) (ArchiveCopySummary, error)`：只从该 cache 的固定根复制一个合法 archive hash；destination 是受控 coordinator 创建的受限 writer。方法检查 source 存活、ctx、源物理身份及限额，读取前后核 inode/大小，汇报 read/write/short-write/Close 错误，成功摘要仅含精确长度/hash。它自身不取得 writer lease，也不声称取得 Exclusive；coordinator 只在 live B2 callback 中调用它。
- 可信 coordinator 的 `WithSnapshot(ctx, limits, consume) (SnapshotSummary, error)`：绑定构造时验证的 Store/cache，无任意路径或查询参数；内部先取得上述 source scope，再调用一次 `Store.WithLocalSnapshot`。成功 B2 返回后才允许向同步可信消费者交付不透明 sealed view，消费结束清理私有产物后才返回最终 summary。没有返回 live 路径或长期可复用 handle 的 API。
- `repository.InspectArchive(ctx, data)` 仅薄封装既有 `inspectArchive`，使用相同扫描、路径/包体限制及 `skill.ContentHashContext` 算法；不下载、不产生另一套 parser/hash 算法。该内部检查结果只留在受信任调用链，不送到日志或普通响应。

source scope 的独立 pin **不是** `Instance.Enter` 租约：若持 writer lease 等待自己的 Quiesce 必然自阻塞。配对时临时 lease 须先全部释放。Close 拒绝新 source、等待已接纳 pin 和 writer drain 后才关物理根、最后释放实例引用；正常嵌套 writer/旧默认模式原义不变。Close 已开始则拒新 scope，已接纳 scope 允许完成，不在持生命周期 mutex 时等 SQL/I/O/callback。递归 snapshot、writer context、过期 source/view 及跨调用复用均拒绝；回调不得逃逸 reader、另开未等待 goroutine 或使用 background context 脱离取消。

此外，coordinator 必须为每次 attempt 取得**独立的实例生命周期 ownership**：在配对/source pin 仍存活、任何最后源引用可能释放之前，由受控内部接缝从已验证物理实例取得一个真实 instance 引用并登记 active attempt；正常路径在 Quiesce 前完成，失败不得创建 staging 或继续 B2。该引用与源 Store/cache 的引用分别持有，绑定同一物理父目录、实例锁身份及本次私有 staging root；仅持 Store/cache Go 指针、root FD、路径或 attempt ID 均不算取得 ownership。caller 不能注入 Instance/path，也不能在源引用已全部释放后重新 Join 来补洞。此 owner 不持 writer lease/Exclusive，不阻止原 Store/cache 在消费阶段正常 Close；它独立保持实例锁和 staging 的所有权，覆盖消费、view/child drain、清理及必要目录 Sync，具体释放/转移见 §7。

这是 B2 §6 已指出的必要 cache 接缝，**不假定 map 当年的“只导出 CopyVerified”已经足够**。后续源码授权必须显式包含 cache 生命周期实现及直接测试；仅在 `cache.go` 导出复制函数不足时停止申请精确扩集，不绕过 ArchiveCache、自行打开 archives 路径或修改公共 FS/maintenance 来凑闭包。此规格不授予该扩集。

## 3. 切点、锁序与唯一接受点

操作顺序固定如下，所有内容验证/复制都在第 4 步同一个 callback 中完成：

1. 校验 ctx、正限额、平台能力；取得受管 cache source pin，临时 Store→cache admission 配对并释放临时 leases。在该 source scope 保护下取得并登记独立 attempt ownership，复核同一物理实例；此时尚未 Quiesce/创建 staging。source pin 覆盖后续排队、复制、B2 外层结果处理与本次源资源收尾，attempt owner 另覆盖其后的独立消费及清理。
2. 调用 B2；由 B2 取得自己的 Store pin、对该 Store 的 instance 执行 Quiesce/drain，再绑定实际 DB/父目录。**不得**另一次 Quiesce、另开 live SQL 事务或在切点内调用 writer admission。
3. B2 验证自己的 DB 与封存引用后进入唯一同步 callback。此时再核 source parent/root、缓存 policy、文件身份；初始配对不是整次操作的永久身份豁免。
4. 从 B2 view 向本次私有 staging 复制 DB/引用并重算长度/hash；从其引用及该独立 DB 枚举全闭包。逐个从 scoped ArchiveCache 复制归档、重验压缩包和 skill 内容、owner/Pack/分类关系。所有文件、只读 SQL、归档 reader 明确关闭；所有文件 Sync、目录 Sync、最后写 seal 并 Sync。此时仅为 **provisional sealed**，不得发成功通知或调用后续消费者。
5. callback 返回；B2 自己完成临时 DB/引用清理、连接关闭与 Exclusive 释放，并返回外层结果。**B2 外层非 nil、晚取消、release/cleanup 错误均否决 provisional seal**，只清理独立产物，不能补读 live 源或换一次切点接上旧结果。
6. B2 外层成功、本次源资源收尾与身份复核成功，且已取得的独立 attempt owner 仍正常持有并登记 active 时，才正常释放 source pin，coordinator 随后把本地候选交给可信有界消费者，标记本次 `snapshot_ready`。归档与 DB 均已独立，消费绝不再访问 live DB/cache；源 Store/cache 均可关闭，但不得因此释放 attempt 自己的实例引用。消费者结束后先使 view 拒新调用、drain 全部已接纳 child/reader/复制调用，再删除、Sync、关闭 staging 资源，按 §7 最后释放 attempt owner。消费/删除/父目录 Sync/ownership 释放失败时最终返回零 summary 和受控错误；已经发生过消费者调用必须如实分类，不能谎称从未交付。最终 summary 的 nil error 还要求消费、drain、本次清理及 ownership 释放完成；此全过程不延长或重取 B2 Exclusive。

`provisional sealed`、`snapshot_ready`、`remote_committed`、`restore_verified` 是不同状态。B3 的成功 receipt 不含远端/恢复承诺；文件 seal 不是 API 成功 receipt，取消或丢响应也不能由看到 seal 推断成功。不自动重试、auto-rebase 或重用旧尝试 ID；显式新尝试必须重新取得整套切点。独立候选在释放后存活仅为有界消费，不是继续补闭包的时间窗口。

## 4. 全实例引用闭包与领域 oracle

唯一权威是 B2 固定 schema 的独立 DB 与同次 `references.ndjson`；不是 live `Get/List`、owner RPC、可下载清单、Pack manifest 或缓存目录扫描。B3 可对自己的已关闭写句柄的 DB 建立独立 **mode=ro** 连接，采用 B2 相同的受支持 schema 指纹；不使用 `state.Open`、迁移、修复、checkpoint 或可写临时 SQL 表。只对确定无 sidecar 且不会再修改的独立副本允许 immutable；不能把该参数套给 live DB。

| 来源 | 必须保留并验证 | 不能替代它的集合 |
| --- | --- | --- |
| 所有 `CollectedRepository` | ID/public ID/owner、规范 repo/branch、archive hash、skill_count、publication_revision；每条仓库都解析归档 | manual/star/plugin 来源只是收录原因；private、unpublished、未被 Pack 引用的仓库同样包含 |
| 所有 `CollectedSkill` | ID/public ID/repository/path/content hash；对应归档扫描出的完整 skill 集合与路径/hash一一相符，计数一致 | 只校 tar.gz 的 hash 或仅扫描 `SKILL.md` 文本不够 |
| 所有 `Pack` / `PackItem` | Pack owner/revision/item_count、每项真实 Pack/skill/repo/owner、slug；反查全部 item，包括失去 Pack 的孤儿 | 不用 inner join/filter owner 把损坏行隐藏；新增/删除/重复引用必须由固定切点决定 |
| category 与关联表 | category owner/revision/唯一性；每个关联的 category/skill/repo owner 一致、无孤儿 | 只因 B2 固定四类引用不含 category 就略过领域关系是不完整的 |
| 其余物理 DB 表 | 按 B2 原样保留用户/session、源配置及密文、private 历史、配置资产、native/proofs、审计等；由 schema/FK/完整性门保护 | 不 Reveal 密文，不从旧下载凭据/native history 猜额外 archive hash，不把敏感行复制到清单 |

当前 schema 中外部 archive 内容的权威引用是 `CollectedRepository.archive_hash`，skill/Pack/分类经关系间接引用；Lists/star/plugin 的远端元数据不增加尚未收录的仓库。支持的 schema 必须显式登记此分类；未来新增 archive-bearing 表/字段不得只自算新指纹便默认通过，需要修改闭包规则和漏项测试。源地址、部署文件、master key、环境变量不是 B3 遍历根。

补充关系要求：

- B2 的 repository→skill→pack→pack_item 固定 kind/主键顺序、总数/摘要必须与独立 DB 对应；不借重编码改变 B2 引用格式。额外 category 校验仅查询独立副本。repository 的 revision 是 publication_revision，不创造 source/global revision。
- Pack slug 沿用 `skill.ValidatePortableSlug` 与现存领域规则：同 slug 同 content hash 的合法复用不能误判，异 hash 冲突整次失败。保留全部 PackItem，不为备份重新 compact/dedup、增加 Pack revision 或重算出另一个业务 manifest。
- 两 owner 相同 repo/hash 可共用一个独立 archive 文件，但关系逐条验证，不能合并 owner、skill 或 Pack 身份。发布状态与分类从 DB 原样保存，不发布、不取消发布、不重新授权、不更改 hash/revision。
- 现有 archive 解释器上限固定沿用：压缩数据 32 MiB、展开文件内容 64 MiB、tar 流 80 MiB、10,000 entries、512 skills、单 SKILL.md 1 MiB，以及现有路径深度/长度、UTF-8、重复 entry 等规则。不能为老归档放宽解释器；当前规则下不可解析的被引用归档整次失败。
- 缺归档、空/截断、大小或 SHA-256 不符、压缩 hash 正确但 skill path/hash/计数不符，均整次失败。**禁止在线补拉、Collect、Refresh、修 DB 或删除 live cache 来修复**。无引用的旧归档/Put 后提交失败残留不属于闭包，不复制、不顺便 GC；相关 cache policy 与固定控制文件仍须验真。

## 5. 有界枚举、去重及空间预算

所有限额显式正值，由可信调用者给定；0/负数/溢出/不可能容纳固定开销在任何 staging I/O 前拒绝。沿用 B2 的 `MaxDBBytes`、`MaxInventoryBytes`、`MaxRecords`、`MaxStagingBytes`、`DrainTimeout`、`OperationTimeout`，B3 增加 `MaxArchiveBytesTotal`、`MaxArchives`、`MaxConsumerDuration`；禁止另给 B2/B3 各一份相同额度而重复花费。

- 每页最多 128 行、单引用或派生记录最多 64 KiB，metadata/seal 各不超过 64 KiB；读前限制长度，不能先按 DB 自报 count 分配。四类引用按 B2 主键 keyset，category 按自身主键，关联表按固定复合键 keyset；每页 rows 关闭后再取下页，不用 OFFSET、全表 All 或构造与库大小线性增长的 RAM map。
- `MaxRecords` 覆盖 repository/skill/Pack/item/category/关联的完整枚举与派生 archive 条数；各类分别计数，分页 cursor 必须前进，计数核算检查加法溢出。关系验证用有界索引点查/分页 anti-join，超限不能截断为“成功备份前 N 条”。空库合法。
- 唯一 archive 按 repository 主键首次出现顺序写 `archives.ndjson`；文件名为受验证小写 64 hex hash 加固定 `.tar.gz`。以私有 stage O_EXCL 文件及对应有界记录去重，不建全实例 hash map。重复 hash 必须重验已完成 stage 文件、复核各 repo 的完整 skill 关系；不能仅因存在同名文件就跳过检查。最终检查 stage 内档数/总字节/引用集合恰相等，无陌生文件。
- 同时仅处理一个 archive，允许沿用当前解释器的单包有界 materialization；压缩包、解包、metadata/entry 结构的峰值受上述现有单包限制，逐包释放，不能把全库归档装入 `Read` 或并行队列。不宣称 RSS 精确等于 64 MiB，也不改成自制 streaming parser。
- `MaxArchiveBytesTotal`/`MaxArchives` 限去重后的文件总量；重复读取仍受记录总数与总操作 deadline 约束。destination 先做剩余量预检，再以受限 writer 防 short-write/超写；输入尺寸/hash不可信。缺 EOF/溢出不成功。
- `MaxStagingBytes` 是**整次同时存在的文件逻辑字节高水位**：B2 临时 DB/引用/metadata + B3 独立 DB/引用 + archives + inventory/seal +受控工作文件均计入。DB 双份、引用双份不能只算一次；B2 已用/预留空间通过其可信 view 元数据与已验证界核算，预算不足则在复制前拒绝。`MaxInventoryBytes` 同样覆盖同时存在的两份引用及新增清单，不依赖去重降低最坏需求。
- readonly SQLite 的连接数、cache 和 temp 使用固定有界设置；验证查询不得因大排序溢出到未计费 temp 文件。实现计划须逐条给查询形状与索引证据，无此证据不得用“LIMIT 128”掩盖无限扫描/排序分配。
- 这不是全卷物理配额，不能保证其他进程不占空间；磁盘块、文件系统 journal、SQLite/Go 固定开销与 ENOSPC 仍可能失败。ctx/timeout 覆盖 drain、校验、复制和消费，但不能强制中断任意 kernel I/O；不得 goroutine+select 遗弃仍运行的操作来伪造有界完成。

## 6. 私有 staging、格式与物理身份

由宿主在已绑定 canonical dataDir 下创建固定私有命名空间 `.backup-staging`，每次使用内部随机 attempt ID，0700 目录、0600 普通文件；不接收外部 root/path/basename、已有 stage、FS 或 raw SQL。创建/读取/删除均 rooted 到已验证目录；校验父目录、打开 root 与按路径观察的 inode 一致、当前 UID、权限、普通文件及 nlink=1，拒 symlink/hardlink/目录替换。不能只验证一次字符串或仅 HasPrefix。!unix 复用既有 `maintenance.CheckPlatform` failclosed，不新增平台支持承诺。

每个 attempt 的 sealed 文件集合固定：

| 相对名 | 内容 |
| --- | --- |
| `snapshot.sqlite` | B2 DB 的独立复制，精确 bytes/SHA-256，与 B2 metadata 一致 |
| `references.ndjson` | B2 封存引用原字节副本，精确 bytes/SHA-256/各 kind counts |
| `archives/<hash>.tar.gz` | 每个被引用唯一 archive 的独立副本，无链接到 live cache |
| `archives.ndjson` | 固定记录字段 `hash`、`bytes`、`first_repository_id`，按首次 repository 主键顺序，严格唯一 |
| `inventory.json` | `format=cognate-local-content-snapshot`、version=1、scope=instance、随机 attempt ID、`contentClosure=verified`、B2 schema/key identity、DB/引用/archives清单的精确字节/hash、各类关系计数、已验 cache budget policy/值、唯一归档数及总字节 |
| `seal.json` | 固定格式/version/attempt ID、inventory 的精确 bytes/SHA-256；不含路径、owner、secret 或远端状态 |

记录数/字节数是非负且不大于 MaxInt64 的 JSON 整数，不接受浮点/指数或数值字符串；revision 沿用领域正 int64，hash 为小写 64 hex，attempt ID 是内部生成且不含路径分隔符的有界 ASCII 标识。schema/key identity 继承 B2 的已验证表达，不能把 key bytes 或新推导值写入其中。格式/字段布局须在后续计划以独立固定 fixture 明列，不能从任意运行时对象反射生成或宽松 map 解码。

沿用严格 JSON v2：拒未知/重复字段、非法 UTF-8、尾随文档、未知版本/kind、超长/溢出/计数不一致。hash 基于落盘原字节，不用未定义的 JSON canonicalization。seal 是本地完整性描述，不是签名、加密或来源认证；攻击者改写 payload+hash 不因此变可信。B2 的 `contentClosure=not-included` 候选 metadata 不改写成 B3 receipt；B3 metadata 独立标识且不可喂给 B2 冒充原候选。

顺序为数据文件写完→逐文件 Sync/Close→archives 目录 Sync→inventory 写/Sync/Close→seal 最后 O_EXCL 写/Sync/Close→attempt 及命名空间目录 Sync。各步骤检查错误及身份，任何临时文件不属于 sealed 集合。最终核目录成员与清单相等，不复制 live `.cache.lock`/`.cache-budget`、cache `.stage-*`、DB WAL/shm、instance/schema 锁、master key 或部署配置。cache policy 仅把已验证固定格式/预算值写入 inventory，不把控制文件当内容。

不得 hardlink/reflink 依赖原内容、不改 live 文件权限/mtime、不 checkpoint 原 DB，不利用 mutable file descriptor 在 seal 后继续写。所有复制出的敏感关系/hash/文件名只存在私有候选，不是普通 API 可返回数据。

## 7. 错误、资源所有权、清理及隐私

错误至少区分受控类别：unsupported、not-managed/peer、closed/expired/lease、identity、limit、missing-archive、integrity/relation、I/O、canceled/deadline、cleanup-pending/uncertain。名称由实现计划冻结；不能把 raw OS/SQLite/解包 error 拼进公开错误，不能输出路径、URL、owner、repo、skill、hash、key、内容或回调 sentinel。零结果失败合同包括晚 Close/Sync/取消；不可吞错返回非零候选。

独立 attempt owner 从 §3 步骤 1 起连续持有真实实例引用和本次 staging 资源所有权。正常释放顺序是：消费者返回或失败 → view 失效并拒新 child/reader/复制 → 等已接纳子资源真实 drain → 关闭所有文件/只读 SQL → 删除登记文件并完成必要 attempt/namespace/父目录 Sync → 关闭 staging roots → 保留 releasing 登记并最后释放独立实例引用 → 明确释放成功才注销登记。引用 Close 失败则按下文先转移强 holder，不出现先注销后发现 uncertainty 的空窗。不得仅凭消费者返回、ctx 取消或 B2/source 已结束而提前注销/释权；不把这些操作的 mutex 持过 I/O 或等待。原 Store/cache 全部 Close 后，该 owner 仍须使同物理实例的其他进程无法取得锁；保留 App 或额外见证 Store/Instance 不能代替这一要求。同进程受限清理也须在同一协调下检查 active/releasing/uncertain 登记，跳过正在消费、drain、清理或持有不确定资源的 attempt，不能仅凭目录年龄/seal 将其当崩溃残留。

所有 I/O、SQL、callback 同步收尾后才返回；不把 timeout 当 syscall 已结束证明。普通失败且资源已知关闭时，删除仅本次登记的独立文件并 Sync；不做 live GC、不 RemoveAll dataDir/archives、不跨 namespace 清理。仅删除/目录 Sync 失败的已关闭残留是 `cleanup_pending`，**不等于仍持活连接的永久 poison**；本次失败、拒交付，后续需受限清理，不能谎称无明文。只有全部子资源已 drain、所有句柄/root 明确关闭且不会再发起 I/O 时，才可进入已关闭 cleanup_pending 的释放流程；仍保留 releasing 登记直至独立实例引用明确释放成功，再转换为清理器可接手的残留。不能用此分支掩盖 Close/终止状态不确定。

实际 close/取消后无法证明句柄终止时，相关缓存/候选拒新操作，**先**将仍可得的真实句柄、物理根、独立 attempt 实例引用及必要 source pin 的 ownership 转移给进程强根 holder、确认其强引用已发布，再允许普通 scope 返回或放弃自己的 ownership。转移前不得注销成可清理残留或释放最后实例引用；holder 对该 attempt 的 uncertain 登记保留到进程退出，没有中间无主窗口。不能只留路径、错误、Store-local cycle 或 KeepAlive，也不因反复 Close/GC 释放最后 root；实例引用自身 Close 的不确定失败同样守权。Close 返回固定 uncertainty 分类，不重试不确定资源、无 hidden finalizer/后台重试。B2 自身的 SQL/Backup/Exclusive poison 仍完全按 B2 合同收尾，B3 不能代释它的 gate。B3 返回 callback 前不得留下会继续读写的任务；仅已停止 I/O 但 close 不确定的 B3 资源由自身 holder 保留，不虚称能凭普通 callback error 接管 B2 Exclusive。若真实接缝做不到这一点，必须停止并申请 provider 合同修订，不降格为释放后兜底复制。

取消可导致已 seal 但未接受、或消费者已观察成功后最终清理失败；均不得产生最终成功 receipt，必须保留真实阶段/cleanup 分类。消费必须同步、纯本地且受 `MaxConsumerDuration` 约束；本模块不授权网络/KDF/JS 长回调。消费者不得存储逃逸 view；返回后 view 及所有 child 失效。实现不能强制终止不合作 callback，因此只允许受控内部消费者，不对任意 callback 承诺强制 deadline。

正常完成必须消费并删除 plaintext；没有消费者就不制造可长期保留候选。崩溃残留不自动成为可用候选，恢复进程只能在获得同一实例生命周期锁后，对固定 namespace 中结构、owner/mode/inode/link 均合法的尝试做有界清理，含旧 provisional seal；不重放为 success、不跟随异常条目。预算不足/异常结构时阻止本模块新尝试并报固定 cleanup 状态。该清理能力属于 B3 内部生命周期，**不自动接入 App 启动**；实际进程重启集成由后续 B4 授权。无 SSD 安全擦除、崩溃后立即自动删除或完整掉电持久性承诺。

summary/view/source/copy 摘要均采用私有不透明 payload 与 safe-default 展示；值/指针/零值、外层未导出字段、多层容器、所有 fmt verb/flags/width/precision、JSON、log/slog 均不得泄漏数据、长度/hash/路径。必要计数只在明确受信任 accessor 中取得，clone/copy 无别名。不靠 Stringer/GoStringer 单独兜底；ordinary error/health 只含固定分类、boolean/计数。不得把 raw DB/引用/解包 DTO 认作 safe-to-log。

## 8. 可验证验收与红绿矩阵（未来门禁）

所有证明使用 Go 1.27.3、CGO0、TempDir、合成 key/双 owner、真实 SQLite/缓存/子进程；不读真实 HOME/用户数据，不 CGO1/race，不以 stub/缺符号/编译失败作行为 RED。报告 exact source/tree、命令、退出、实际 run/skip、预算及产物；原独立 RED 永久保留。

| 场景 | 必须观察到的真实行为 | 编译后负控 / 不能充当证据的替代 |
| --- | --- | --- |
| 全闭包 | 空库及每 kind 超 128 行、private/unpublished/not-in-Pack、manual/star/plugin 收录、同 hash 跨 owner、2 Pack 共用 skill；独立只读 SQL+archive oracle逐条对比，原 DB/hash/revision/发布与分类不变 | 仅枚举一个 owner/Pack/第一页的 mutant 必须漏项 RED，不能只拿作者 inventory 验自己 |
| 同切点 | 真 publisher 在 Put→DB COMMIT/rollback→清理之间受控暂停，B2 callback 与末次 Sync 期间 writer/未来 GC 不穿透；fetch 在外；结果只含 C 或 C+1，非混合 | 提早释放 Exclusive 后真实 peer writer 在复制间隙提交，原 oracle 有界断言 RED；不得靠 sleep/总超时/死锁充 RED |
| 缺档/内容 | 删除 referenced archive、截断、压缩 hash 错、压缩 hash正确但DB path/content hash/skill_count错均整次失败；HTTP source stub计数0 | 跳过 CopyVerified 或 InspectArchive 的各自 mutant 被不同 oracle 捕获；无引用坏档不被复制，合法全包正控仍成功 |
| 关系损坏 | raw SQL 合成 orphan PackItem、跨 owner、缺 repo/skill/category、坏 count/revision、slug异 hash冲突，各自失败；同 slug同 hash合法正控 | 过滤 owner/inner join 隐去孤儿 mutant 必须由全部原始 rows 反查红；不改实体 schema/FK来凑通过 |
| 独立性/配对 | 同物理 parent 的真实 Store/cache、两 Store；混合/全unmanaged/不同root/stale source拒绝；把已绑定view/source换成同gate另一DB亦拒绝。成功消费期间停用原cache仍可验证独立文件，nlink=1 | 以 writer lease冒充pin、提前unpin/复用root、hardlink 原archive、跳过identity检查分别有界失败 |
| Close/取消 | cache/Store.Close 在排队、复制、callback、B2 cleanup 的真实屏障后等待；晚取消/外层release错误即使 provisional seal 存在仍不交付；已消费但删除失败最终不成功 | 去pin、callback 内先公布 snapshot_ready、吞B2外层错误 mutant 均明确断言 RED，不以任意 sleep或无限 Close hang充 RED |
| 独立 attempt ownership | 真实 B2 返回后暂停独立消费者，**关闭全部源 Store/cache**，无额外 Store/Instance/App 引用作见证；独立文件仍可用，真实第二进程 Join 必须有界拒绝。同进程清理跳过该 active attempt。放行消费者后，再分别在 child drain、删除、parent Sync 屏障处证明锁仍受持有，全部完成才准子进程 Join 成功 | 编译后仅提前释放独立 attempt 引用的 mutant，保 source/B2/copy逻辑不变，由相同子进程有限拒绝断言 RED；不能靠保留见证引用或延长 Exclusive 得到假 GREEN |
| attempt 故障转移 | 在最后源引用已关闭的消费尾部注入真实 Close/终止不确定，强 holder 转移前后、GC/重复 Close 后子进程仍不得 Join，清理器不得删除 uncertain attempt；另目录正控可用。另测全部句柄已关闭但删除/Sync失败：零结果+cleanup_pending，按 releasing 流程明确释放实例引用后才转为受限清理可接手的残留 | 漏 holder 转移/先释权后转移各须触发原 ownership 断言；已关闭 cleanup_pending 不能被误判永久 poison。此处是未来真实行为门，不声称本次 prose 修订跑过 RED/GREEN |
| 预算/分页 | DB/引用双份与最后一个归档恰边界/多1byte、超record/page/line/count、巨大字段、重复hash多关系、ENOSPC、短写均失败且无隐式截断 | 漏算B2重叠空间/把超限EOF当成功各RED；证明SQL查询不落未计费temp，不能只有极小Happy Path |
| 物理故障 | root/parent/archive替换、symlink/hardlink/权限、每个read/write/Sync/Close/remove失败；真实子进程锁+可用FD证明uncertainty守根，另目录可正常运行 | close失败后重复Close/GC不可释锁；ordinary已关闭删除失败不被误poison。overlay只证故障路径，不伪称真实掉电持久 |
| seal/清理 | 真实子进程在复制/写seal/外层返回前被屏障控制kill；重启严格拒消费partial，清理只触本namespace，无越界，无长期明文成功路径 | 删除seal或改清单hash/未知字段/重复key/尾随/超界/陌生archive均拒绝；旧临时产物不能凭文件名恢复成功 |
| 生命周期/隐私 | expired/nested/writer ctx、view child/reader逃逸拒绝；消费者错误/超时不假成功；fmt私有容器/JSON/slog及错误捕获无合成sentinel | 展示绕过私有wrapper与吞lateClose各有效RED；现有MCP/CAS/unknown-COMMIT/默认unmanaged回归不变 |

必需独立负控至少覆盖：漏一种引用、提前释放 cut、跳过 archive 内容验真、漏 cache pin、提前释放独立 attempt ownership、忽略 B2 外层失败、漏双份空间；每个须编译成功并在原 oracle 的有限断言上 RED，恢复候选后 GREEN。原始 writer/MCP/selection/CAS 回归与 B4 真 App 门分开列，不能由本片孤立 gate 测试代签全服务。

## 9. 后续授权出口与未完成声明

本 spec 对应 map 的 B3：后续最小实现承载仍为 `internal/server/backup/{coordinator,inventory,stage}.go` 及直接测试、ArchiveCache 的受限复制/生命周期接缝及直接测试、`internal/repository/archive.go` 薄封装及直接测试。B2 view只读复制/分页、cache pin/poison、schema固定查询与预算同时核算须在实施计划先锁定；实际需要触及 `cache_maintenance.go` 等现有文件时明确列路径另授，不能凭本 spec 偷扩写集或改 B2 未签源码。

不修改 App、scheduler、业务 writer、ent/proto/generated、module、公共 FS、HTTP/UI 或 S3。现有成功测试、设计签收与接口候选都不是 B3 实现证据；接受本文只允许进入独立计划设计。B4 的 managed App/真实服务接线、加密/认证封装、S3 提交、恢复演练、自动/手动入口及保留策略仍是完整首发 F4 的未闭合模块。
