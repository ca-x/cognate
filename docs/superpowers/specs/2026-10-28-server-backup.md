# Cognate F4 服务端备份与恢复设计

日期：2026-10-28。状态：**设计候选，未实现、未做恢复演练、未验收 F4**。

本 sidecar 只写本文，不修改源码、依赖、协议、index 或现有生产数据，不阻塞 patch/UI 主线。下列“必须”是后续实施的验收约束，不代表当前已经具备。没有启动 Docker、访问外部 S3 或读取真实用户数据库。

## 1. 已确认范围与非目标

- 依据 `docs/REQUIREMENTS.md` §3、F4：可选的服务端功能，S3 兼容存储起步，使用 Afero；支持手动、自动备份，以及恢复。包含收录的 skill 内容、仓库配置、Pack 配置。
- §3 早期条目仍有“云后端待确认”旧文案，以同节后续确认项和用户本轮确认为准：**S3 + Afero 不再是待决策项**。适配库、SDK 版本和具体供应商尚未锁定。
- v1 建议为**运维级全实例灾备**，不是普通 owner 的 SQLite 下载接口。一个 SQLite 同时包含多个 owner，不能靠请求里的 owner 参数把整库变成 owner 备份。
- 恢复是离线运维动作，不在运行中的 HTTP 请求内覆盖数据库，不自动向 AI agent 应用配置，不在恢复中拉 GitHub 补内容。
- owner 导出/导入、跨实例合并、跨 owner 迁移、增量链、跨备份内容去重、多节点调度、PITR、WebDAV 是后续独立范围；不能伪装成 v1 已支持。

## 2. 只读盘点与证据边界

### 2.1 当前数据流

| 数据/路径 | 目前事实与设计含义 |
| --- | --- |
| `internal/server/app.go` | 在 data directory 下打开 `server.db`、`archives`；master key 从独立配置路径读取。App 持有 library、packs、scheduler 等服务，不能只暂停一个 HTTP handler 就称全局停写。 |
| `internal/server/state/store.go` | ent + entsqlite，WAL、`synchronous(NORMAL)`；打开时执行 schema/key/policy 等检查或更新。`.schema.lock` 只保护打开/初始化，不是运行期快照锁。 |
| `internal/server/state/masterkey.go`、`key.go` | 主密钥为外置 32 raw bytes 私有文件，数据库有 key ID 和认证证明；错误主密钥不能靠忽略校验恢复。 |
| `internal/server/library/cache.go` | `archives/<sha256>.tar.gz`，写前校验 hash，以私有目录、临时文件和本地 link 发布；有 `.cache.lock`、持久化 `.cache-budget`。默认容量 512 MiB，但不是备份空间预算。 |
| `internal/server/library/service.go`、`scheduler/scheduler.go` | 先写入归档，再提交收录元数据；可能留下无引用归档。library 获取/下载依赖本地归档校验，不是随时从上游重新下载。 |
| `internal/server/ent/schema/collectedrepository.go`、`collectedskill.go` | 仓库归 owner，保存 archive hash；skill 保存仓库内 path、content hash。一个归档可包含多个 skill。 |
| `internal/server/state/pack.go`、`ent/schema/packitem.go` | Pack 有 owner、revision，items 引用 skill 和 slug；不是单独落盘的完整内容包。 |
| `internal/server/packs/manifest.go`、`download.go` | 分享 manifest 从当前持久状态计算；下载用 archive hash/path/content hash，并检查分享权限与 revision。不能只备份派生 manifest 而丢 DB 关系。 |
| `internal/repository/archive.go` | 当前单仓库归档最多 32 MiB，另有展开/文件数限制；这些现有源内容限制仍要用于恢复验证，不能由备份层放宽。 |

源码定位均为本轮只读观察，未打开实际 `server.db`。本文不假定存在单独的 `source/` 或 `content/` 目录：当前内容来源是 repository archive，服务层是 library，权威引用保存在 state。未来若新增本地权威内容，必须扩展闭包枚举器和版本，而不是递归打包整个 data directory。

### 2.2 权威数据与派生数据

1. SQLite 保存用户身份/归属、仓库来源及分支、观察/同步状态、skill 描述与 hash、Pack 及其关系，以及其他已落库的配置和加密秘密。**物理全库快照必然包含超出 F4 三项的敏感表**，必须按最高敏感级别保护，不宣传为脱敏配置备份。
2. 被快照 SQLite 引用的归档，是实现离线恢复所需的**权威内容副本**，即使类名叫 cache 也不能省略。每个 archive hash 复制一次；普通用户不得通过 hash 探测其他 owner 是否拥有相同内容。
3. 无引用归档、`.stage-*`、锁文件、SQLite sidecar、下载暂存、日志、客户端缓存/skills 投影不进备份。`.cache-budget` 是运行策略，不作为不透明归档内容复制：记录其格式/预算，恢复时验证并受控重建。
4. 备份不遍历主密钥路径、不把主密钥或 S3 credentials 连同数据自动上传。未落库的部署配置仅列出恢复所需配置名称和版本，不复制环境变量或整个配置文件。
5. 新的持久资产类型必须登记为“闭包内权威数据”或“可重建派生数据”，测试漏项；不能只认当前几张表就宣称未来完整。

### 2.3 外部依据与检索记录

- 已加载本地 `ddia-principles`：采用权威/派生分离、明确一致性切点、发布点、未知结果处理、幂等和恢复演练原则，而不是把单库事务推断成跨文件事务。
- Nowledge targeted query：`afero-s3 nowledge-mem-snap`，top 3，沿用当前已配置身份/Space，不修改 scope；仅提取标题和架构关键词，未输出凭据。参考记忆 `crystal_e65793d9137e` / `crystal_a6fb8273d4bf`：任务级 retention、授权根目录、限定清理前缀，以及 `github.com/fclairamb/afero-s3` / `github.com/lib-x/aferodav` 的历史用例。**案例不是本项目选型或运行证据**；未为 graph 扩大检索权限。
- 本地 module 源码：Afero v1.15.0 `afero.go` 具有 Rename/Sync 等接口，但接口存在不证明远端实现具有原子 rename、durable fsync 或有界写入。entsqlite v0.2.6 `sqlite3.go` 是 modernc driver 的注册包装；本轮未验证其具体 snapshot/cancel 行为。
- SQLite 官方 `https://www.sqlite.org/lang_vacuum.html` §2.1，本轮 curl 成功读取：`VACUUM INTO` 是一致 SQLite 快照途径；过程中崩溃可能产生不完整目标。它只解决数据库快照，不解决归档闭包。driver、文件持久性和取消仍须用项目版本测证。
- AWS 官方 `https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html`：本轮读到条件写入说明；`https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html` 只取得部分正文后超时，读到单 key 原子、无跨 key 原子更新的说明。不能将 AWS 语义直接推广到所有 S3 兼容供应商。
- 适配库 `fclairamb/afero-s3` 的猜测源码地址本轮返回 404，**没有源码核实该适配库能力**。后续须固定版本后审阅真实文件和测试，不从 README 推定达标。

## 3. 一致性契约：先定义切点，再上传

成功备份必须满足：存在一个 SQLite 一致切点 C；恢复出的 DB 等于该切点的逻辑状态；C 中所有内容引用可在备份内解析并验证；仓库、skill、Pack 关系和 revision 来自同一切点。不是“几个目录差不多同时复制”。

### 3.1 v1 保守方案：全写入屏障 + 私有 staging

v1 采用冷启动时显式加入的 managed-instance 模式，单 data directory 只允许一个合作写进程，同进程多个受管 Store 共享同一 B1 gate；不先引入复杂的在线 GC pin 表。默认 `state.Open` / 零值 options 保持原有多 Store、多进程行为，并不获得备份一致性保证。`state.Options.MaintenanceManaged=true` 由 Store 按已校验的 DB canonical parent 自行 Join，禁止热挂、任意目录 Instance 注入及 Join 失败后降级；非 Unix 明确 unsupported，但默认模式不受影响。

默认 `state.Open` 在既有非 Unix 实现中本来就因目录安全验证 unsupported 而失败；本片保留其原顺序和错误分类，不能把历史失败归因于新增 B1。仅 managed 分支在既有 ctx/key/绝对 clean path 基础参数检查通过后，先调用纯能力检查 `maintenance.CheckPlatform(ctx)`，在 EvalSymlinks/Stat 等任何文件系统访问前决定平台支持：Unix 仅返回 `ctx.Err()`；非 Unix 优先保留 ctx 错误，否则返回既有 B1 `ErrUnsupported`，即使绝对 path 的父目录不存在也不先返回 canonical/FS 失败、不创建任何条目。CheckPlatform 不碰目录、不 Join/持锁、不含平台名单或任意 Instance 注入；默认分支不调用。Unix 后续 canonical parent/inode → Join → 原目录/DB 验证和父目录身份复核顺序保持不变，失败释放 handle。B1b 后续可复用同一能力检查，不能复制平台知识。CGO0 跨编译通过不等于非 Unix 运行通过，真实 Windows 测试另由平台 CI 验收。

B1 独立计数租约/实例锁原语与 B1a Store writers、启动/Close 接线已独立验收并提交 `0c542727`，包含正式 Task2C 串交后的 native 写者。`nativeTransaction` 自持独立 nested 租约覆盖 SQLite 全部 retry；Begin/Bind、Prepare/Authorize/Consume 另持最外层逻辑租约，覆盖 quota rollback → prune → run、纯 Store callback 捕获的 leased context 与 Consume 的事务后取消/结果发布清理；三类 Preflight、Snapshot、Status 和 GC 同样受 helper 保护，proof/四表配额/GC 业务不改。**受管 App、所有内容发布者和 coordinator 尚未完成接线，不存在可用的全写屏障或备份入口**。后续完整屏障必须满足下列验收约束，不能把本片测试等同于备份可用。此为合作协议，不防任意 raw SQLite、旧 binary、运维脚本或同 UID/root 直接写；受管备份启用前须停掉旁路写者并从专用 dataDir 冷启动。

B1b Task1 的 typed Store writer scope 与 managed cache 冷启动/Put/Close 生命周期已独立验收并提交 `68d13325`。完整私有源 S1 已提交 `107f2610`。基于该固定源，Task2 候选为 library Collect、owner Refresh、独立 RefreshAutomatic 在 fetch/scan 后加 Store 最外层租约，覆盖 cache Put 到最终 DB commit/rollback 与清理，传播返回的上下文并保留源凭据/MCP/Lists/CAS/BUSY/未知 COMMIT 规则；NewService 拒绝混合模式或不同物理 dataDir，默认 unmanaged 不新增单进程限制。此为待 Poincare 独立验收的六路径实现，不是全局屏障完成声明。scheduler 的 selected-Lists 首次发布仍有独立 cache→DB 路径，未在该六路径中改动；其 library 独立自动刷新路径才属于本次覆盖。尚未启用 managed App、备份 coordinator、snapshot/restore、自动清理或 S3/backup endpoint。

1. 服务生命周期锁明确 data directory 只有一个合作写进程；备份 job 另有单 writer 排他控制。所有 Store/cache 独立持有目录身份绑定的生命周期引用，不能用关闭一个 Store 放走另一个。现有 schema/cache 局部锁不足以提供这个保证；已知存在旁路写者的部署不启用受管备份。
2. 触发时先预检 staging 空间、预算、目标/key 配置；进入维护态，停止接收新的领域写操作，暂停 scheduler、imports、账号/session、MCP/configuration、分享和所有后台持久化写入。读请求若有 last-seen 等副作用也算写。仅关闭 library 不成立。
3. 等已登记 writer 退出，带有限 drain deadline；超时取消备份，释放维护态，不无限等网络任务。网络/KDF/JS 准备在屏障外，DB/归档提交重新取得写租约；顺序固定为 admission → token/Lists gate → SQLite。租约覆盖完整逻辑 retry、callback、OnCommit、commit/rollback，传播返回的 leased context；nested lease 独立计数，失效 parent 不能借用，异步 child 必须真实 drain。受管 Store 启动在 schema 锁/DB 创建/迁移/策略与完整性修复之前入租约；Close 先拒新写并 drain，再关闭 SQL client，仅成功后才释放 instance 引用，不能先释放最后目录锁。SQL Close 失败或结果未知时保留 instance/root ownership，原 Store 继续拒新写；`closeOnce` 缓存失败，并发或重复 Close 不重试、不清除失败、不静默释锁。恢复需进程退出，或后续另行设计并授权的显式恢复协议；本片没有恢复 API，不能把返回错误当成物理连接已关闭。
4. 屏障内使用受控新文件名生成 SQLite snapshot，建议首先验证项目 driver 的 `VACUUM INTO`。在专用连接上执行，不包在外层事务中，不把用户路径拼成 SQL。若能力/取消/完整性测不过，**不启用自动备份**；只提供明确停服后的运维流程，不能静默退化为复制活动 DB。
5. snapshot 成功后，以不会触发 schema/策略更新的只读连接枚举其内容引用闭包。不能调用会初始化数据的 `state.Open` 当作无副作用检查器。执行 integrity/foreign-key 检查；用分页/流式枚举，生成受限 inventory。
6. 屏障继续覆盖归档修改/删除/GC，按快照引用复制并重新计算 archive hash 到独立私有 staging。不能只记录活文件路径、释放锁后再上传；不能把可被修改的硬链接当作不可变副本。缺失、hash 不符、skill path/content hash 不符均整次失败，不在线补拉上游。
7. 验证 DB→仓库→归档→skill→Pack 的闭包与 owner 关系，写本地 sealed inventory，flush 文件与目录后释放屏障。之后压缩/加密/上传只读 staging，正常服务恢复；慢 S3 不延长全局维护窗口。
8. 生成不透明 snapshot ID 和切点说明。当前没有经证实的全局 revision，因此不凭时间戳或 SQLite `data_version` 发明跨任务顺序。备份创建时间只是展示信息。

屏障耗时与本地复制量相关，不承诺零停写。需测量维护窗口；若不能接受，下一片才引入“事务切点 + 不可变归档 pin + GC fencing”。在线方案必须证明快照读视图与 pin 原子关联，不能只在事务后重新查询当前 DB。

### 3.2 本地快照与完成状态

- 本地 staging 不是普通 owner 可下载资产，目录 0700、文件 0600，拒绝符号链接和不可信父目录。SQLite 本身并未全库加密，临时 snapshot 含明文元数据；权限、空间限制和清理不能省略。
- 状态区分 `snapshot_ready`（本地 sealed）、`remote_committed`（远端完成）和 `restore_verified`（某次独立演练）。不把“上传完成”展示为“已验证能恢复”。
- 可选保留本地快照应保留**相同的加密恢复格式**，不是无限保存明文 staging。断电遗留明文的处理必须在下次启动执行受限清理；不承诺 SSD 上安全擦除。

## 4. 加密格式、资源上限与密钥绑定

### 4.1 密钥角色

- **服务端 master key**：现有 DB 字段解密和身份/分享签名派生所需；备份不替代它。密钥 ID 写在加密 manifest，恢复要校验既有数据库 key proof 和代表性加密记录。
- **备份恢复 KEK**：建议独立、随机 32 bytes、带不透明 key ID，由运维在备份介质之外保管。每次尝试生成随机 DEK，由 KEK 认证封装；KEK 不在远端对象、日志、API 错误或 DB 任务 DTO 中。
- 同源灾备恢复需要备份 KEK **和**原服务端 master key。仅拿到 S3 密文或只拿到 DB 均不能宣称可恢复。v1 不提供口令猜测式 recovery、自动换 master key 或秘密重加密迁移。
- 可选择以现有 master 派生备份 KEK 减少保管数量，但失去密钥职责分离；这是待确认策略，不默认偷偷采用。恢复 key escrow/轮换必须随保留期延续；删除旧 key 前先证明所有仍保留备份可恢复。

### 4.2 格式 v1 候选（实现前需密码格式审查）

不对整库 `ReadAll`，不把完整 tar/zip 放内存，不因 SDK 内部缓冲使流式承诺失效。以下是审查用格式约束，不是已实现的加密协议：

1. 小型 bootstrap header：magic、format version、不透明 backup/attempt ID、算法标识、KEK ID、认证封装的 DEK 及必要随机参数。其字段有长度上限，AAD 绑定版本、ID、用途；未知版本/算法直接拒绝。header 不公开 owner、文件名、URL、明文内容 hash 或 master key ID。
2. 用经过评审的分块 AEAD 实现；候选为 AES-256-GCM + HKDF-SHA256 按 attempt/对象序号/用途派生独立子密钥。每帧 nonce 必须在该子密钥下唯一，序号/长度/类型有严格编码；AAD 绑定 backup ID、对象 ID、帧序号、格式版本。manifest、数据、完成认证使用不同用途域。
3. 强制连续帧与认证终帧，终帧覆盖总明文长度、帧数、entry hash。拒绝丢帧、重排、重复、跨对象拼接、截断和尾随字节。单帧 AEAD 成功不代表整文件完整。
4. 每次新尝试全新 DEK/attempt 随机量。重试只重传同一份已 sealed 密文对象；输入改变必须换 attempt/key，不用相同 nonce 加密不同明文。不支持未经验证的中途重建密文续传。
5. SQLite 大文件按 extents 分割，manifest 指定连续 offset/length。归档无需解压再打包；不默认增加第二层压缩。恢复提取 skill 做语义校验时继续遵守现有展开上限，防压缩炸弹。
6. 解密后每个 entry 还校验完整明文 SHA-256、预期长度和类型；hash 在认证加密 manifest 内，不泄露到对象名。只写到隔离 staging，全部验证前不得启动 server。

建议初始预算（**待容量评估，不是产品承诺**）：帧明文 1 MiB，对象明文最多 64 MiB，2 个上传 worker、每 worker 1 个待重试密文对象；bootstrap/完成标记各最多 16 KiB，单 manifest 分页最多 1 MiB，最多 100,000 个数据/manifest 页对象；总体字节上限和单 DB 上限由实例必填策略控制。每个对象/单 key 的 AEAD 调用次数另设远低于算法安全限额的硬上限，不能只依赖 uint64 序号不溢出。

manifest 不能无限增长：以有界页链组成，根记录页数/总大小/每页密文 hash，页内 entry 按稳定序排序且禁止重复。解码先验长度/计数再分配；内存上界按帧、worker、SDK buffer、inventory 分页共同计算并 benchmark。上传前估算 staging DB + 闭包 + 最多并发密文对象 + 安全余量，运行中再执行硬配额；磁盘不足先失败，不删除现有有效快照腾空间。

## 5. Manifest 与提交协议

### 5.1 布局与权限

建议逻辑 key 空间：`<授权root>/<instance-id>/backups/<backup-id>/<attempt-id>/...`；所有 ID 由服务生成，不接受任意路径/owner 名。root prefix 在 target 创建时固定，按对象 key segment 验证（含编码后、分隔符、空段、前缀碰撞），不能只做字符串 `HasPrefix`。全实例任务无 owner 可控制的目录拼接。

加密 manifest 保存：format/应用/schema 版本、来源实例 ID、scope=`instance`、切点说明、master key ID、cache policy、DB 长度/hash/extents、全部引用归档及长度/hash、skill 内容验证信息、owner 与关系清单摘要、对象密文长度/hash、页链信息和恢复所需外部配置名称。实际 owner 标识和秘密不得放明文完成标记。

### 5.2 发布步骤

1. 本地 sealed snapshot → 上传 bootstrap、各不可变密文对象、manifest 页及根。
2. 每次写必须检查 write/close/commit 的真实错误；精确 GET 回读并校验密文长度/hash（不是只比较 ETag）。S3 multipart ETag 不作为内容 SHA-256。
3. 最后写小型 `complete`，认证绑定 backup/attempt ID、bootstrap hash、manifest 根密文 hash、对象数/总字节数。恢复先 unwrap DEK 再验证 complete，随后逐对象验证。没有有效 complete 的目录永远不是可恢复备份。
4. complete 写入回复丢失是 `commit_unknown`：精确 GET 该 key 并验证。成功即确认提交；未明确不存在时不宣布失败后立即重写另一个“同名完成”。取消与 commit 的竞态以远端有效标记为准，已提交不能谎称“从未完成”。
5. `latest` 指针只是可选提示，不是事务或真相来源；先做按明确 backup ID 恢复。列举可以漏新项，但不能把半成品当成功。有效旧 complete 被恶意回滚/删除不靠 AEAD 自动发现；独立审计记录、版本控制/Object Lock 属后续增强。

## 6. Afero / S3 能力边界

使用 Afero 不等于用它模拟完整 POSIX：本地 snapshot/staging 可使用现有安全根目录和 Afero；远端提供一个内部窄 `BackupTarget` 能力层，仍通过所选 Afero 适配器接入，但显式约束 context、提交、精确读取、分页和受限删除。名称为设计建议，非当前公有 Go API。

| 操作 | 必须验证/禁止的假设 |
| --- | --- |
| 新 key 写入 | 顺序有界写入，Close/提交失败可见；不假设 `O_EXCL` 在远端是条件 PUT。使用随机 attempt 隔离和单 writer；若需跨进程写同 key，须验证条件写或拒绝该部署。 |
| Open/GET | 支持 ctx 取消、超时、有界读，缺失与权限/网络失败能区分；精确 key 回读校验。若兼容端暂不可读则重试至 deadline，不能提前发布完成。 |
| Rename/Sync/Chmod/锁 | 不用于远端提交；不依赖目录存在、原子目录 rename、append、seek、fsync、flock、硬链接或 POSIX 权限。远端安全靠 IAM/TLS/加密，而不是 chmod 成功。 |
| multipart | 验证队列/part 大小、总缓冲上限、ctx 取消、失败 abort、遗留 upload 清理；若库整文件缓冲且无法限制，就不合格。64 MiB extents 允许选择有界单 PUT，但仍须核实 SDK。 |
| List/Delete | 有界分页、prefix 隔离，拒绝 bucket/root 级 RemoveAll；删除仅接受程序生成的合法 key，缺失幂等。List 不是跨 key 快照。 |
| 弱兼容端 | 不假定 AWS 的一致性及条件请求行为；运行 capability suite。缺少必须能力则拒绝启用；受控补充 SDK capability 需单独审查，不能把真实需求藏到 Afero 外绕过审查。 |

S3 endpoint/region/bucket/root 由可信运维配置，普通 owner 不能提供 URL 造成 SSRF。默认 HTTPS、校验证书，重定向不得带凭据跨 origin；生产禁止任意明文 endpoint，loopback HTTP 仅合成测试开关。凭据从外置秘密引用加载，最小 IAM 仅授权专用 prefix；read/write 与 prune 权限尽量分离。认证错误、带 query 的 URL、SDK 原始响应及请求体不得原样进入日志。

retention 默认不自动删成功快照；确认策略后才启用。只处理登记的本实例完整备份与过期残留 attempt，保护正在恢复/演练/上传的 backup lease。至少保留一个已完成且有恢复验证记录的快照；没有可验证替代品时不删最后一个。删除先进入 tombstone 状态，再按受限清单重试；优先去除可发现性并阻止新 restore，等待既有 restore lease 结束后删对象。外部进程下载不受本地 lease 保护，跨工具 prune 需另定协议。不能拿远端目录的 mtime 判断完成/租约过期。

## 7. 任务、取消、幂等与 owner 隔离

### 7.1 状态与调度

建议持久化 job：`queued → preflight → quiescing → snapshotting → snapshot_ready → uploading → verifying → committing → remote_committed`，另有 `failed`、`canceled`、`interrupted`、`commit_unknown`；恢复演练是关联记录，不覆盖 job 的上传状态。

- F4 默认关闭。手动与自动走同一 coordinator、预算、权限和 manifest 协议，不重复实现两套内核。手动调用尽快返回 opaque job ID，不把大备份挂在短 HTTP deadline 上。
- 手动任务仅运维能力主体；启用开关不授予所有本地账号备份权限。鉴权前不能探测目标或 key 是否存在；普通 owner 不能 list/get/cancel/restore 全库任务，也不能读别人的任务错误。
- 使用持久化 idempotency key，绑定运维主体/实例/目标配置版本/选项；相同 key+相同参数返回原任务，不同参数 conflict。该请求 key 不作为 S3 key；hash/摘要也不泄露秘密选项。
- 一个实例最多一个快照/上传 job，队列有上限；自动任务繁忙时合并/跳过，不无限积压。调度记录 UTC 到期 slot，DST/时钟回拨不重复触发同一 slot；随机 jitter、失败退避和错过时段补跑上限写成配置策略，不用时间戳当 fencing token。
- target/key 配置变化形成新 generation。进行中的任务持有不可变已授权配置版本，不能一半上传到旧 bucket、一半到新 bucket；敏感撤销可显式 cancel，取消后回调不得把终态覆写成成功。
- HTTP 取消仅取消触发请求，不隐式杀死已登记持久任务；独立 cancel 操作鉴权并撤销 job context。job 内磁盘、SQL、上传、验证均传 ctx；cancel 后先停止生产，再中断网络/SQL、回收 worker、清理暂存。关机先拒绝新任务、cancel，再有界 drain，不在 UI/HTTP 锁内等待回调。
- 崩溃重启将未完成 job 标 interrupted；扫描仅已登记的自有 attempt。可能完成的提交先 reconciliation，不仅看本地状态。先清理旧尝试并新建 attempt，v1 不自动续加密流；不会承诺 exactly-once 上传。

### 7.2 隔离与秘密

owner 身份只来自服务端已验证主体及 DB 关系；不能由 manifest 的字符串自动授予权限。物理恢复保留原 owner ID/身份映射，不按邮箱或 agent 名重新归并。若以后提供 owner export，必须从该 owner 的关系闭包生成独立逻辑格式，不能复制 SQLite 后简单删除几张表；共享内容 hash 也不能成为跨 owner oracle。

状态/审计仅记录 opaque ID、阶段、计数、字节、耗时、稳定错误码；不记录 SQL、manifest 正文、origin query、token、原始 SDK error 或路径中的用户名。正常日志、debug fmt/slog、HTTP 错误都须测试。备份控制服务不暴露原始配置/请求 DTO 的 String 输出。

## 8. 离线恢复协议

1. 显式选择 backup ID，先下载成有界**加密恢复包/对象集**；需支持从本地私有目录完全断网执行验证和 restore。S3 凭据不是离线恢复的前提，KEK/master key 仍必需。
2. 在独立空 staging、隔离 home/data directory 执行；源服务停写并停止相关进程，取得独占生命周期锁。目标非空默认拒绝；替换已有实例需单独确认和可回滚计划。普通 owner HTTP 调用不得触发此流程。
3. 有界解析 header、验证 key unwrap 和 complete、manifest 页链与所有对象认证。检查实例/scope/schema、全部字节/数量预算。未知格式、重复 entry、缺对象、路径穿越、绝对路径、反斜杠混淆、symlink/hardlink/设备文件直接拒绝；不执行包内程序。
4. manifest 中的逻辑路径仅允许 `database` 与规范 hash 归档等固定类型；生成本地文件名，不相信包内任意路径/权限。先验 ciphertext hash，再 AEAD、总长度、plaintext hash、连续 extents，失败不得覆盖目标。
5. 使用不会自动建表/改策略的只读 SQLite 检查 integrity/foreign keys、owner/仓库/Pack 关系与 key proof；对引用闭包重算 hash，按现有 repository 规则验证 skill path/content hash。单有 DB `integrity_check` 不足以通过。
6. 相同受支持应用/schema 优先恢复。旧 schema 的迁移在另一份候选副本执行并再验真，不能在唯一备份上试迁移；更新 schema 由旧程序读取时 fail closed。部署的 cache/observation 等策略必须与快照兼容，不通过直接改元数据绕过现有校验。
7. 不复制 `-wal/-shm/-journal` 或锁文件。重建 cache budget 时要求容量覆盖恢复闭包且通过现有上限校验；预算不足显式错误，不丢归档来“修好”。
8. 切换前做本地模拟服务验收：登录身份、各 owner 仓库列表、Pack revision/items、下载内容 hash、加密配置可读，且网络被禁止。恢复的自动任务/上游同步保持关闭，等待运维明确重新启用。
9. 同源/异机全实例恢复默认要求原实例身份绑定与 master key；不同 instance 只能进入明确的灾备接管流程，防止同时运行两个同身份实例。文件替换不跨文件系统假装原子：停服下在同一可信父目录准备候选 generation，记录可恢复切换 journal，flush 后切换；跨卷先复制并校验，不能直接 rename 宣称完成。
10. 保留旧 generation 和切换 journal，启动失败回滚到旧 generation；每个切换崩溃点要能离线确定使用哪一代。不能只分别替换 DB 和 archives 两次 rename。首次启动完成冒烟后才写恢复成功记录，尚未做演练的备份不改称 restore_verified。

恢复旧 DB 也会回退 credential/configuration/MCP 撤销状态；旧 session、分享链接及定时任务可能复活。建议恢复先进入封闭维护模式，轮换/清除所有 session，默认撤销外部分享和 MCP 能力凭据，保留资产与 owner 关系；让运维显式重授权并说明客户端订阅需更新。具体覆盖策略需产品确认，不能声称“恢复所有权限原样有效”且同时承诺“旧 token 不复活”。v1 必须在这个策略确认/实现前阻止对外开放恢复后的服务。

## 9. 本地合成验收矩阵（未来实施，当前均未执行）

所有 fixture 使用 `t.TempDir`、隔离 home、随机合成 key/token 和纯 Go SQLite；测试启动的 server 只绑 loopback，默认禁止外部网络。Afero MemMapFs 用于接口测试，真实临时目录验证 SQLite/WAL/fsync/链接边界；故障对象模型模拟 S3，不能让 MemMapFs 的 rename 成功掩盖对象语义。

| 类别 | 必须证明的断言 |
| --- | --- |
| 真实内容 round-trip | 两 owner、重用归档、多个仓库/分支、skill、Pack/revision/slug、typed 加密资产；通过实际 library/packs 服务读取恢复结果，与合成原始切点比较，不只比较文件数。 |
| 一致性/并发 | writer 在屏障前后提交、scheduler/import/configuration/auth 写入、归档 Put 成功但 DB 失败、未来 GC 删除竞争；只允许完整 C 或完整 C+1，不能混合 DB/归档；漏登记 writer 的 mutation test 必须红。 |
| SQLite | WAL 有未 checkpoint 的已提交事务；快照包含它但不复制活动 DB/sidecar；SQL 取消、快照中崩溃、损坏文件、schema/policy/key mismatch 均拒绝；项目实际 driver CGO=0 运行。 |
| 认证/隔离 | unauth/普通 owner/跨 owner job ID 均拒绝；恶意 manifest owner/路径、prefix 近似匹配、encoded traversal、symlink、其他实例 key 禁止读写/清理。 |
| 加密/边界 | 错 KEK/master、改 header/manifest/frame/complete、调序/截断/尾随/重复/extents 缺口、超大计数、压缩炸弹；输出只留 staging，目标不变；内存/磁盘预算可测，不随整库大小线性占 RAM。 |
| S3 非 POSIX | Rename/Sync/seek 均返回 unsupported；GET 延迟可见、List 漏项/乱序、PUT/Close 失败、multipart abort 失败、complete 回复丢失、已存在 key、ETag 非 SHA；只有有效 complete 才成功。 |
| 任务 | 并发手动/自动、幂等参数冲突、队列满、时钟回拨、崩溃重启、target generation 改变、各阶段取消与 commit 竞态；无泄漏 worker，无假成功。 |
| retention | 只删本实例自生成 key；不跨 prefix、不删受保护恢复任务/唯一已验证备份，半删和重试幂等，未知网络错误不当不存在。 |
| 离线灾备演练 | 从加密本地包恢复到新空目录，禁外网、禁原 data directory 访问，独立校验内容和 Pack；旧 key 不在包中仍需人工提供；错误/中断不覆盖旧实例。 |
| 日志 | 捕获标准 log、slog、HTTP/SDK 错误、审计、取消/oversize/strict decode；synthetic key/token、manifest 字段和请求内容不得出现；不打印故意泄密样本。 |
| 切换/凭据回退 | 每个 rename/journal/flush 崩溃点可恢复到完整一代；旧 token/撤销状态的处理符合确认策略，默认维护态不对外泄露。 |

故障模型和本地 HTTP S3-like fixture 只能证明协议/预算/故障处理。**不能证明适配库或供应商兼容**。另设集成门：固定适配库版本和源码、TLS/取消/缓冲检查、供应商隔离测试 bucket/prefix 的 PUT/GET/multipart/list/delete 与恢复演练。Docker/本地 S3 服务/外部 S3 均待后续授权执行，本文没有这些运行证据。

## 10. 真正需要确认的决策

1. **产品范围/权限**：v1 是否接受运维级全库灾备（含其他敏感表），而不是面向每个 owner 的逻辑备份？若需后者，另开格式/权限 slice，不允许先把全库给 owner。
2. **可用性预算**：首版可接受的维护窗口、最大数据量/磁盘余量、超时与自动任务频率、时区/补跑策略、目标 RPO/RTO。建议先单实例维护屏障，测量后再决定在线 pin。
3. **密钥托管**：独立恢复 KEK 或同 master 派生；谁持有离线 master+KEK、如何 escrow/轮换、丢失恢复责任。默认建议独立 KEK，不自带密钥上传。
4. **恢复安全策略**：旧 session/分享/MCP 撤销状态回退的处置、灾备实例身份接管和重授权体验。需确认后才能解除恢复维护态。
5. **运维后端策略**：具体 S3 供应商/region、IAM/prefix、是否要求 Object Lock/版本控制、远端与本地 retention/配额。适配库由能力验证后选定，不把参考库当用户已锁依赖。

这些不阻塞当前 patch/UI。可以先实现下面纯本地机制和测试；涉及产品选择的出口保持关闭，不用默认猜测变成隐含合同。

## 11. 可拆最小实施片与验收出口

| 片 | 交付边界 | 独立验收/依赖 |
| --- | --- | --- |
| A：快照能力验证 | 仅测试项目 SQLite driver 的 CGO=0 snapshot/cancel、WAL 与私有目录边界；不接 S3。 | 临时 DB 真实快照 round-trip、故障中断拒绝，无生产 DB；决定 VACUUM INTO 可用性。 |
| B：一致切点与内容闭包 | 新 coordinator/全写屏障、封存 snapshot、分页引用枚举、归档复制验真；逐项接入所有 writer。 | 双 owner 真服务并发测试、漏 writer mutation 红绿；依赖 A，不开放网络备份按钮。 |
| C：离线加密格式 | 固定格式/密码审查、流式封装与有界本地 target，独立 KEK/manifest/complete 验证器。 | 大小/截断/改包/nonce 域/预算/秘密日志测试；无 S3 也能加密导出和离线验包。 |
| D：离线 restore | 隔离 staging、schema/key/闭包检验、停服 generation 切换/回滚、恢复维护态。 | 断网新目录恢复+完整真服务演练；依赖 B/C 及恢复安全策略，失败不改旧目录。 |
| E：S3+Afero target | 适配器源码选型与能力 suite，精确 GET 校验、commit_unknown、abort/清理。 | 先故障对象 fixture，再经授权的实际适配库+隔离 S3 演练；未过则仍只开放本地恢复包。 |
| F：任务控制与可选开关 | 手动/自动共用 coordinator、持久 job/幂等/预算、受限运维接口、关机。 | 真实 HTTP 权限/并发/取消测试，不泄露 DTO；依赖 B/C/E，协议由独立任务冻结。 |
| G：retention/发布门 | 受限清理、key 生命周期、运维文档与演练记录。 | 故障删除/恢复 lease/唯一快照保护；完成一次可复现恢复演练后才允许生产启用自动 prune。 |

每片实现时再确认独占写集，不在本 sidecar 修改 ent/proto/module。最终 F4 验收需要：明确产品决策、源码与依赖版本、CGO=0 测试、对象能力证据、离线恢复演练、实际部署后端证据及独立 review。**当前只完成设计，以上实现/测试/运行证据全部 pending。**
