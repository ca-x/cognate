# 服务端实例备份控制平面

本阶段将同切点数据库与归档闭包、既有认证密文容器和 S3 条件发布接到真实 App、管理员 Connect RPC 与 Web 管理入口，并提供默认关闭的周期自动备份。**这是实例备份控制平面，不是完整 F4 或已验收的灾难恢复方案。** 保留清理、完整离线恢复与运行代切换仍未交付。`remote_committed` 只表示已登记密文对象通过精确远端回读，不等于 `restore_verified`。

## 内部恢复进度

只读数据库验真、inventory/归档语义和 Candidate 核心已经实现；Candidate 将借入的认证
bundle 材料化到自有私有目录，可信源完整关闭后再次核验 DB、文件集合及全部共享技能，
仅提供一次作用域内的内部验证能力。辅助配置排序/Pack 附件关系也在同一受限只读 worker
检查，拒绝跨 owner、非法分区或序号，不改变合法 stale/Unsupported 的读取语义。

Linux SH/EX 运行根 lease 内核及 legacy App 启动参与已实现：普通/backup-enabled 进程均
在业务/key 写入前持 SH，完整排空 HTTP/后台/Store/日志后才释放。首次缺 key 仍由程序
创建，但必须由 live Runtime 证明只有两件具体协调对象，不能笼统忽略点文件；原失钥
保护不变。发现保留的 current/generations/restore/intent 痕迹时只拒绝，不自动选旧库。

这还不是全系统停服或运行代选择：不合作旧程序须停用，恢复操作级持久 intent 尚未完成；
同步 snapshot worker 的同 OFD 交接及父死退役已单独验收，但不能代替恢复操作协议，
也不保证根/协调 inode 被整体替换后的同路径全局互斥。
部署必须维持稳定命名，不可删除协调或恢复痕迹来强行回退旧 binary。

技术澄清：普通进程意外退出，在全部 writer 退役后仍允许原 SQLite/WAL/journal 恢复，
不增加全局启动脏标记。非 SQLite 的 layout/current/rollback 必须在修改前建立自己的
持久操作 intent；仅在错误返回后补写 poison 不能覆盖中途死亡窗口。worker 接线与
默认关闭的自动备份已交付，计划见 `superpowers/plans/2026-11-01-runtime-worker-lease.md`
和 `superpowers/plans/2026-11-01-backup-automatic.md`，验收范围见 `VERIFICATION.md`。

可信 S3 Source 内部 API 已接通原密文对象认证与真实 Candidate，头 scope 不承载整个恢复，
自有资源全部正常关闭成功后才开放验证。它只执行 GET/HEAD；目前仅有真实本机 TLS 合成
S3 的运行资格，不是外部供应商 IAM/一致性证书，也未提供生产恢复命令。

真实 MinIO 源码构建工具的有限 QA 已跑通密文链和拒绝场景，但一次上传/WithFiles
ErrInvalid 仍待定因；后继通过不等于该稳定性问题已解决，不扩大上述验收范围。

后继两次新复现定位到条件 PUT 的 HTTP412 缺 APIError 误分类，协议级修复已独审：
412 只进入原完整回读，不直接信为 Existing；条件 DELETE 只报 Conflict，不声称删除。
修后固定源五次真实 MinIO 回归通过，但观测412均有 APIError；无 APIError 分支由确定性
TLS/SDK 回归证明。最早未诊断事件与 MinIO 更深原因仍 UNKNOWN，不以绿色次数抹去历史。

凭据撤销内部 helper 已通过独审：仅借入已验真的隔离事务执行撤销及后置检查，保留业务和
原密文/AAD。它不拥有 Commit/Rollback/Close，也不是幂等操作；提交不明不得自动重放。
后继 T4 coordinator 已接通下述隔离候选净化；运行代切换/回退、离线 CLI 及真实重启业务
回读仍待交付，不能手工移动 Candidate 到活目录冒充恢复。准确证据见 `VERIFICATION.md`。

GitHub needs_reauthorization 的所有者明确重授权已补齐：现有 Catalog 只给被动身份和
预期 generation，PAT/device 两路复用原 CAS，在验证旧 AAD 后原子生成新代/新密文。
普通内容读取仍要求 ready，旧票据不复活；不表示运行代发布或恢复产品入口已交付。

### T4 隔离候选净化

已认证且源资源正常关闭的 ready Candidate 只能消费一次。Linux coordinator 在同一 EX
运行根租约下完成只读初检、真实撤销事务、Commit/Rows/Conn/pool/FD 关闭、实际 child Wait、
文件与目录 Sync，再独立执行 T1、撤销后置及完整已知 schema 的保留数据投影检查。
只有这些阶段全部成功，才交付作用域内的 SanitizedCandidate；失败或结果不明不重放。

撤销固定时刻退役五类认证链、share/MCP 和 GitHub 在途授权，MCP revision 恰加一；业务、
密码、原密文/AAD、历史及 sqlite_sequence 必须保持。原认证 metadata/inventory/归档及源
数据库摘要不重标，新数据库摘要独立保存。不打开 live Store、不运行迁移、不复制归档。

新 WithRetainedWorkerFiles 在同 lease mutex 内、解锁前隔离原 owning root/lock/fences；
callback 或后置准入不明后，Close 固定返回 ErrUncertain，不物理关闭原资源，不在失败后
尝试分配备用 FD。真实 EMFILE、并发 Close 及新进程 SH/EX 拒入已有独立行为证据。
这不承诺进程死亡后仍持 flock，也不关闭旧 R2 150ms 正向 EX 探针的 UNKNOWN。

未来发布只能在活 scope 内先移交唯一 files/key/删除责任，得到 pending owner；整个外层
WithCandidate 及 root 后置准入成功后才 ready，返回后另借 EX 才允许 rename。旧 owner
不能删除已移交内容。这是内部发布接缝，不是 current/intent、切换、回退或 restore_verified
演练证明；完整 R3、离线 CLI、保留清理及完整 F4 仍未完成。

## Web 手动管理

登录工作空间后进入「服务端备份」(`/server-backup`)。页面复用现有 React、TanStack Query/Router、Button 及 Radix Dialog/Checkbox，没有新增依赖或自研基础控件。普通账号由真实 RPC 拒绝，页面不会据此猜测备份是否开启，也不会读取任务历史。管理员可区分关闭、旧服务端不支持、加载失败、无任务及实际历史；页面不接收目标凭据或恢复密钥。

- 开始前必须在 Radix 对话框中勾选未默认选中的全实例范围确认；包含所有 owner 的数据，不是个人导出。确认后焦点交回稳定的任务标题，取消则由 Radix 恢复原触发器；长内容先聚焦标题，异步详情加载不把初始焦点推到屏外。
- Start 发出前，当前 origin/标签页/owner 的 `sessionStorage` 仅保存非秘密 request UUID。写入失败不发请求。丢失或非法回执保留原 ID，重新登录或重载后只能显式「继续原请求」；不会自动重试 mutation 或静默换 ID。正常确认后才清除此记录。
- 「清除浏览器记录」只影响本标签页，不取消服务端任务、不删除远端对象、不承诺回滚。浏览器记录与服务端任务身份分离；关闭页面不终止已登记备份。
- 历史每页 20 项，最多手动浏览 50 页；只为本页已知活动任务每 5 秒刷新，不显示推测百分比或把 catalog 字节数当已上传量。后续页失败、非法或空时仍能返回已保存的较新页；空后续页不冒充实例从无历史。
- 详情使用 Get 读取当前状态；取消需要独立确认，远端提交可能已经完成；精确对账只读登记对象，不重发 PUT、不重新快照，也不恢复数据。切换身份或退出时 abort 旧读取/操作，旧回调不能更新新身份界面。

生产嵌入明确接受 GET/HEAD `/server-backup`，不扩大到任意子路径。Web 浏览器证据与原始失败见 `VERIFICATION.md`；合成 TLS S3-like 测试不是外部供应商或恢复资格。

## 显式启用

备份默认关闭。本阶段仅 Linux 有隔离快照 worker 运行资格；支持的平台在启动时即成对初始化 managed Store 与归档缓存，并创建私有 `backup/settings` 目录，以便原先关闭的实例也能无重启启用；关闭时不初始化控制日志或加密暂存。其他平台显式启用时拒绝启动，仍保留默认模式的 CGO0 交叉构建。任何 managed 初始化失败均不回退普通模式。

**Linux 的同一 data directory 现在只允许一个 server 进程，即使备份关闭也一样。** 这是无重启热启用所需的受管 writer 排空边界，不再支持旧默认模式的多 server 共用数据目录。第二进程在既有约1秒维护门预算内拒绝，并给出 `another server is using this data directory or maintenance ownership is unavailable`；不会降级为 unmanaged 来绕过锁。首进程保持健康，完整关闭后新进程可以正常接替，既有 master key 不覆盖。运行根 SH/EX 原语本身不变，也不代表支持不参与维护协议的旧程序并发写入；懒猫部署应继续保持 `multi_instance: false`。

管理员可以通过下述 RPC 直接输入 S3 凭据并热应用，不需要提供 S3 配置文件。以下 CLI 文件配置仅为没有已保存管理员设置时的初始化途径；存在已保存设置后以其为准。

```
cognate-server --data-dir /private/cognate/data \
  --key-file /private/cognate/master.key \
  --backup-enabled \
  --backup-s3-config-file /private/cognate/backup.json \
  --backup-recovery-key-file /private/recovery/cognate.key
```

路径必须替换为部署中受信任的绝对路径。文件与目录遵守既有 private-file 安全校验，不接受符号链接、公开权限、多硬链接或不可信可写祖先；不要通过放宽校验适配部署。恢复 key 文件必须是独立于 server master key 的 **32 raw bytes**，不是 base64 或 hex 文本。恢复 key 与原 master key 均需单独离线保管，程序不会将任一原始密钥上传到 S3。密文备份不包含可替代原 master key 的材料，不能声称仅凭 S3 对象与恢复 key 即可恢复站内加密资产。现有实例缺失 master key 不能生成替代 key。

私有 JSON 配置最多 64KiB，拒绝未知字段、重复字段、尾随 JSON 和无效目标。示例仅展示结构，不含可用凭据：

```json
{
  "endpoint": "https://s3.example.invalid",
  "region": "region-1",
  "bucket": "private-backups",
  "prefix": "cognate",
  "access_key_id": "<private access key ID>",
  "secret_access_key": "<private secret access key>",
  "recovery_key_id": "<canonical nonzero UUID>",
  "max_database_bytes": 268435456,
  "max_content_bytes": 1073741824,
  "job_timeout_seconds": 900,
  "automatic_interval_seconds": 0
}
```

`session_token` 可选。三个资源字段省略或为零时取示例默认值：数据库范围 1MiB–8GiB，内容范围为至少数据库预算且不超过 16GiB，任务时限 60–86400 秒。内容预算不替代加密暂存、对象数量与控制日志的独立上限。恢复 key ID 是外置 key 的稳定身份，必须为规范非零 UUID；它不是密钥材料。配置变更、密钥或版本变化会改变任务 generation；旧任务不自动切换目标或重试上传。

## 自动备份策略

`automatic_interval_seconds` 省略或0时关闭；开启仅接受60–86400的整数秒。管理员可通过设置 RPC 热启用备份及修改自动策略，不要求重启或 `--backup-enabled`。开启自动策略必须配置至少一个规范非零管理员账号ID（包含既有bootstrap解析结果），否则启动失败；部署者须确保该账号能够通过既有站内认证登录。

以下是主线程技术裁定的本期策略，不是对用户原话的引用：采用进程内monotonic固定延迟，真实App.Serve开始后先等完整interval；每次检查完成后再等完整interval。无jitter、无积压、无补跑，失败退避为固定interval。重启不保留due slot、不追赶停机时段，不承诺跨重启exactly-once调度；这明确替代旧规范的持久UTC slot要求。只Open App不会开始自动任务。

自动与手动共用同一Controller、单活槽、密文任务日志、捕获/封装/上传及预算。自动任务主体是持久实例ID对应的UUID actor，不创建账号或会话；所有当前管理员可用原Get/List/Cancel/Reconcile管理它，不要求与任务Principal相等。普通owner、匿名、share/MCP token仍无全实例权限。

忙、任一generation的interrupted/commit_unknown、1000条历史或16MiB日志上限都会阻止本次自动登记；不追加排队记录、不删幂等历史、不重新提交unknown或换UUID掩盖结果。未尝试complete的interrupted可由管理员Cancel退役；其余沿原任务精确只读对账，不自动Reconcile。只有解除阻塞后的下一完整周期才可登记独立新任务；已终结的failed/canceled任务也不会以原UUID重试。

Web为interrupted任务保留原取消确认入口，包括崩溃前已记录CancelRequested、重启后仍为interrupted的遗留状态；可显式重复Cancel使原记录收敛为canceled。commit_unknown不能借此退役或假称回滚。

间隔是调度策略，故不进入target generation/options hash；省略/0保持原HMAC。仅改间隔不妨碍同目标旧任务对账；目标、凭据、恢复key、master key、资源预算或版本变更仍形成新generation，旧任务不能改目标或探测新目标。跨generation未决历史不会因重新配置而被自动忽略。

GetBackupStatus的automatic_enabled只表示配置已开启，不是执行健康灯或下次成功承诺；Web接受该既有字段并区分自动+手动/仅手动。活动、待对账或容量已满时可暂停产生新任务。历史满额及crash orphan磁盘治理不在本片自动清理；最短间隔持续成功也会在1000条后停止产生新任务，不能把有限控制日志当无限备份服务。

Close先禁止新登记、停止并有界等待timer生产者，再取消/排空Controller，最后沿原App runtime链释放资源及运行根lease。并发Serve尾Stop和App.Close共享同一done，不明时保留强引用/SH，不重试未知关闭。日常启停和设置切换使用下述热应用，无需停服；换旧binary不属于设置热应用，仍需正常排空，并确认旧版本配置格式兼容，不能删除或重建控制历史来回退。

## 管理员设置热应用

`GetBackupSettings` 返回管理员专属配置视图及正数 `revision`，不返回 access key ID、secret 或 session token，只有 `credentials_configured` 标志。`UpdateBackupSettings` 必须包含当前 `expected_revision` 和完整 `settings`；修订过期返回 `aborted`，不会覆盖其他管理员的更新。资源预算字段为0时沿用当前值（首次为上述默认值）；`settings.revision` 和 `credentials_configured` 是只读显示字段，以 `expected_revision` 为准。

凭据通过独立的只写请求字段提交：`credentials_action="keep"` 必须不带三个凭据字段，保留已保存凭据；`"replace"` 要求完整 access key ID 与 secret，并可带 session token，不带 token 表示清除旧 token。不接受空动作或在 keep 时夹带值。proto 字段带 `debug_redact`，Go 非生成扩展额外保护 fmt、slog 与普通 JSON 格式化；HTTP handler 返回时丢弃敏感请求字段。不要显式调用请求的 protobuf `String()`/prototext 或记录原始 HTTP body：当前 protobuf Go runtime 不按 debug_redact 自动遮盖它们。

`gen/skillhub/management/v1/backup_redaction.go` 是手写的同包脱敏扩展，不是生成器输出；
CI 仅对这一确切文件排除目录生成比对，其余 Go/Connect/TS 输出仍逐字核验。

目标参数通过既有 S3 校验，endpoint 只能是无 userinfo、query 或 fragment 的 HTTPS URL。恢复 key 通过 `recovery_key_file` 指定服务器上既有的受信任绝对路径，继续严格校验私有目录、拥有者、regular file、链接数和权限；必须恰为32 raw bytes，且与 server master key 独立。程序不生成、替换或覆盖恢复 key 文件；浏览器不上传 key 内容。首次启用或更换配置前应由运维创建并离线备份它。只关闭且保持原后端配置时，不要求重新读取已加载的恢复 key。

配置保存在 `backup/settings/settings.enc`，0600 文件、0700 目录；使用由 master key 与专用域派生的 AES-256-GCM key、随机 nonce 和固定 AAD `cognate/server-backup/settings/v1`。先写独占临时文件并 Sync/Close，再原子 Rename 和目录 Sync。格式/认证损坏或不安全文件拒绝打开，不回退 CLI 配置、不覆盖坏文件。原 CLI JSON 不被修改；将凭据从 CLI 移到 UI 后，运维仍应安全清理自行保留的明文文件。

真实运行期由动态 backend facade 串行化任务 API、自动登记及重配置。只在没有活动任务、没有任一 generation 的 interrupted/commit_unknown 且控制状态确定时，才正常关闭并排空旧运行资源，打开新目标/key/engine/controller，持久提交设置并发布新修订。忙时返回 `resource_exhausted`，不会取消任务来强行改配置；控制不明返回 `unavailable`。明确的提交前失败会恢复旧配置；关闭或 Rename/Sync 结果不明时隔离能力、保留依赖，不宣称应用成功，也不自动重试未知关闭。

启用、禁用、S3 目标/凭据/key 路径与自动间隔的成功更新均在当前进程立即生效，原先关闭的部署也支持。禁用不删除远端对象、任务历史或恢复密钥。所有成功设置变更会重新等待完整自动间隔；旧到期事件按 revision 丢弃，没有补跑。自动关闭/再开启不永久关闭调度器。generation HMAC 与历史 policy 保持原算法：仅调度间隔不改 generation，目标/凭据/key/资源或版本变更仍隔离旧任务，不能把旧任务对账请求导向新目标。保存只验证本地配置及资源构造，不冒充远端认证成功或真实备份成功；S3 可用性仍由后续真实任务证明。

## 管理员接口与任务语义

复用既有站内会话和服务端管理员授权。首次管理员仍按原初始化参数创建，升级不覆盖用户名或密码；普通 owner、匿名调用、share token 和 MCP token 都不能操作或探测全实例备份状态。HTTP 响应为 `no-store`，错误与任务 DTO 不含目标、配置路径、主体、密钥或 SDK 原因。

服务为 `skillhub.management.v1.BackupManagementService`，由 Buf 管理，Go 与 Web 消息同步生成：

| RPC | 用途 |
| --- | --- |
| `GetBackupSettings` | 管理员读取当前设置、凭据存在标志及 revision，不返回凭据 |
| `UpdateBackupSettings` | revision CAS、凭据 keep/replace，空闲确定状态下无重启热应用 |
| `GetBackupStatus` | 管理员读取是否启用及automatic_enabled配置状态，固定实例作用域；restore_available仍为false |
| `StartBackup` | 以客户端生成的规范非零 UUID `request_id` 登记任务，相同主体/输入幂等 |
| `GetBackupJob` | 按 opaque job UUID 读取安全任务状态 |
| `ListBackupJobs` | 默认 20、最多 50 项，以返回 cursor 继续读取，不自动追页 |
| `CancelBackupJob` | 持久化取消请求并撤销活动任务，不宣称回滚远端副作用 |
| `ReconcileBackupJob` | 对已登记的精确对象进行只读回读，不重新快照或 PUT |

最多一个实际执行任务；忙时拒绝，不无限排队。HTTP 请求取消不会取消已经登记的备份，需使用显式取消。任务状态包括 `pending`、`capturing`、`uploading`、`committing` 和终结/待对账的 `remote_committed`、`commit_unknown`、`interrupted`、`failed`、`canceled`。已提交任务的取消不能把成功改成“已回滚”。

在首次远端操作前持久化本次全部对象身份、长度和 SHA256；complete 的发送意图也先持久化。密文采用条件上传和精确回读，complete 最后发布。PUT 结果不明不会自动重发，重启后使用原任务/对象进行只读对账；generation 不同则拒绝探测旧目标。任何清理不明优先于普通取消，保留所有权而不是误报已释放。

## 本地状态与操作边界

状态位于显式数据目录下的 `backup/control`，是独立私有 AES-GCM/HKDF 操作日志，不修改 ent schema，也不是内容权威 catalog。上限 16MiB、最多保留 1000 个任务；任务登记与 catalog 发布均先预留最坏后继状态、取消、完成意图与重启记账空间。容量不足在物理写入前干净拒绝，不删除幂等历史，也不当作持久化不明。被拒旧候选产生的已满额日志不做自动迁移或修复。`backup/artifacts` 为私有加密暂存；现阶段尚无 crash orphan 清扫与跨任务全局磁盘回收，不能把单任务预算当成完整磁盘生命周期治理。

正常 Close 先拒绝新任务、取消并有界等待活动备份，再释放 target、engine、key、日志及原 App 服务。构造或关闭关键不明时 App 保留依赖与引用，原控制能力 quarantine，不继续复用、不重试 close。**不明不代表物理 flock 已知仍被持有**：最后锁 FD 关闭前，私有 `control.fence` 先落盘；最后释放不明或进程退出留下该标记时，真实新进程也拒绝打开。部分标记也 fail-closed，不自动删除或修复。

只有确认最后物理锁关闭成功后，旧控制器/日志能力才永久 closed，再移除标记并 Sync 目录。其后标记移除、Sync 或最终 root 清理仍可能不明；原能力继续 quarantine，但如果标记删除已经可见，新进程可能进入，此时旧 closed 能力不能再写状态或附着控制器。不能将这个释放后的清理错误描述为“仍持有 OS 锁或持久标记”。由运维核实原状态，不提供 force、自动清空或静默重建；控制日志损坏、错误 key、未知格式或非空但不完整的初始化均不能作为空实例重建。

Reconcile 在回写时重新读取已持久化取消，而非使用网络调用前的旧副本。缺失/未知结果不会把已取消任务改回普通 interrupted；重复 Cancel 可以收敛遗留取消标志。已尝试 complete 的不明仍为 `commit_unknown`，所有确切对象均验真时仍可明确为 `remote_committed`，并保留取消标志；取消从来不表示回滚远端提交。

目前没有备份专用 CLI 任务命令，也没有外部 S3 供应商隔离 bucket 与完整恢复演练的发布资格。内部 TLS S3-like 测试证明本地合同，不代表所有 S3 实现均兼容。完整剩余项见 `TODO.md` 与 `FORMAL-RELEASE-AUDIT.md`。

## 本阶段验收

### 2026-11-01：内部归档语义检查 T2d

新增纯 `verifyArchive`：只借 bytes/被动仓库期望，先按显式 32MiB/合计 references 预算
验证精确长度与 hash，再复用 repository.InspectArchive 的 Afero 解包/扫描；每个共享
仓库的完整技能数、目录 path 与内容 hash 均须匹配，不只检查第一仓库或压缩 hash。
输入不修改/清零、不保留，无文件/网络/激活副作用；沿用 producer 链接安全忽略及内容
hash 语义，错误脱敏。exact2 经独审与主 staged-tree 组合验证通过，见 `VERIFICATION.md`。

该片不认证声明来源，不证明实际候选文件集合、FD/持久化/清理或完整 Candidate。全局
累计预算与真实 DB/canonical references 绑定、恢复入口及运行代切换仍待后继，不改变 GA 门禁。

### 2026-11-01：内部 inventory 检查 T2c

新增恢复包的纯检查接收既有 metadata 与权威 canonical references 三元组，在显式预算内
验证完整 inventory 字节、严格 v1 必填头/CutID、精确引用尾、owner 去重、仓库归档集合及
既有 closure 摘要。只借 reader、不关闭、不写文件/联网；不另造备份格式或 snapshot 规则。
三文件经独审限定 ACCEPT，真实生产快照移走源后与 T1 evidence 对接及 staged-tree 组合
通过；详细冻结、RED/GREEN、运行范围与额外内存开销边界见 `VERIFICATION.md` 首节。

三元组本身只是被动期望，后继仍须绑定真实 DB evidence、材料化并核验实际归档与每个仓库
的技能。此处没有 Candidate/激活能力、恢复入口、运行代切换或完整 C4/F4 资格。

### 2026-11-01：内部恢复头 scope T2b

`S3Target.WithRestoreHead(ctx, parent, expected, consume)` 借入显式私有 parent FD，
按 canonical attempt 顺序只读下载 bootstrap/complete；HEAD/范围 GET 各一次，共四请求。
bootstrap 恰 156 bytes、complete 为 1..16384 bytes，复用 Afero、两槽与 120 秒期限。
全部物理响应关闭及本地 Sync/关闭/只读重开通过才借给回调；最终本地清理成功才返回 nil。
不 List/写远端/重试；新长度发现与坏流健康门仅在新 mode 生效，旧精确验证不放宽。

回调必须合作返回，借 FD 不关闭/复制/逃逸，不能在回调中激活。后置关闭不明 retain 并隔离
target，不能声称全部已释放或自动回滚。成功也不代表认证；仍须 BeginOpen，退出全部源
scope 后才推进可激活候选。exact3 独审及排除并行 WIP 的 staged-tree 组合通过，精确证据
见 `VERIFICATION.md` 首节；不是完整恢复或外部 S3 供应商资格。

### 2026-11-01：内部数据库恢复验真 T1

后继只读候选数据库检查已合入：原 master key 与所有 retained GitHub/configuration 密文、
policy proof、精确 schema、integrity/FK 和 canonical 引用/分类均在受限 Linux worker
核验，失败不给有效 evidence。首次非正 ID 分页漏验 P1 经原七反例独立闭合；不迁移或修复
候选，不改变 live 启动或 S3 任务语义。实际生产 worker、密文→候选库与 CopyExact 的
主线定向组合及三包 vet 通过。详细身份与边界见 `VERIFICATION.md`、`LOCAL-SNAPSHOT.md`。

T1 不证明 inventory/归档闭包或停服锁；无运行代切换、权限撤销、恢复 CLI 和重启业务验收。
T2b 头引导正在隔离实施，尚未合入。完整 C4/F4 与正式版门禁仍保持开放。

### 2026-11-01：内部恢复读取前置 T2a

`S3Target.CopyExact(ctx, ref, output)` 增加显式对象的密文流式读取，复用 Afero S3 和既有
受限 transport：固定安全 probe、HEAD、一次完整 GET，最多三个请求，不重试、不列举或修改
远端对象；沿用两槽并发、120 秒操作期限及 target Close 取消/排空。

只有精确长度与 SHA-256、全部响应物理关闭和操作健康状态共同成功，才返回
`Verified, nil`。在此之前，writer 中即使已有完整字节也只是**不可信候选**；失败可能留下
部分输出，不能承诺回滚。方法不关闭借入 writer；任意 writer 必须从 Write 返回，取消与
target Close 才能完成排空。后继协调器必须写入私有暂存并按最终结果决定是否采用。

exact2 已独审限定 Spec/Quality ACCEPT，报告
`/tmp/cognate-copyexact-independent-7942388-KwQ59D/REPORT.md`，SHA256
`4b636d3b4539534e5430da3d4ea39c132eab956dc20c6e035ef9b53e83245644`。
独立 65 个新叶子、62 个既有回归叶子、9 个新增对抗叶子及重复取消/排空检查通过；主线合入后
重新执行新增例和 backup vet，未用父子 PASS 事件冒充独立用例。此处没有头文件引导、数据库/
归档闭包、恢复 CLI、运行代切换或撤销，因此不代表完整 C4/F4、外部 S3 供应商资格或 GA。

2026-10-31，App/RPC 构造不明所有权经独立真实子进程反例及正控闭合；控制内核初审三项缺陷另经原生 close-EIO、新进程 fence、精确容量边界和并发取消的非作者复核后局部 ACCEPT。修后同组合全仓 7884 PASS / 161 SKIP / 0 FAIL，Buf 双模板 parity 与三组件十八目标 CGO0 构建通过。完整命令、冻结身份、初次失败与后继收据见 `VERIFICATION.md`；不是自动调度、管理 UI、保留清理、硬件断电、外部供应商或完整恢复的验收。
