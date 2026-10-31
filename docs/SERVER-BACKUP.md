# 服务端实例备份控制平面

本阶段将同切点数据库与归档闭包、既有认证密文容器和 S3 条件发布接到真实 App、管理员 Connect RPC 与 Web 管理入口。**这是手动实例备份控制平面，不是完整 F4 或已验收的灾难恢复方案。** 自动调度、保留清理、完整离线恢复与运行代切换仍未交付。`remote_committed` 只表示已登记密文对象通过精确远端回读，不等于 `restore_verified`。

## Web 手动管理

登录工作空间后进入「服务端备份」(`/server-backup`)。页面复用现有 React、TanStack Query/Router、Button 及 Radix Dialog/Checkbox，没有新增依赖或自研基础控件。普通账号由真实 RPC 拒绝，页面不会据此猜测备份是否开启，也不会读取任务历史。管理员可区分关闭、旧服务端不支持、加载失败、无任务及实际历史；页面不接收目标凭据或恢复密钥。

- 开始前必须在 Radix 对话框中勾选未默认选中的全实例范围确认；包含所有 owner 的数据，不是个人导出。确认后焦点交回稳定的任务标题，取消则由 Radix 恢复原触发器；长内容先聚焦标题，异步详情加载不把初始焦点推到屏外。
- Start 发出前，当前 origin/标签页/owner 的 `sessionStorage` 仅保存非秘密 request UUID。写入失败不发请求。丢失或非法回执保留原 ID，重新登录或重载后只能显式「继续原请求」；不会自动重试 mutation 或静默换 ID。正常确认后才清除此记录。
- 「清除浏览器记录」只影响本标签页，不取消服务端任务、不删除远端对象、不承诺回滚。浏览器记录与服务端任务身份分离；关闭页面不终止已登记备份。
- 历史每页 20 项，最多手动浏览 50 页；只为本页已知活动任务每 5 秒刷新，不显示推测百分比或把 catalog 字节数当已上传量。后续页失败、非法或空时仍能返回已保存的较新页；空后续页不冒充实例从无历史。
- 详情使用 Get 读取当前状态；取消需要独立确认，远端提交可能已经完成；精确对账只读登记对象，不重发 PUT、不重新快照，也不恢复数据。切换身份或退出时 abort 旧读取/操作，旧回调不能更新新身份界面。

生产嵌入明确接受 GET/HEAD `/server-backup`，不扩大到任意子路径。Web 浏览器证据与原始失败见 `VERIFICATION.md`；合成 TLS S3-like 测试不是外部供应商或恢复资格。

## 显式启用

默认关闭，不创建备份目录、不改变普通实例的维护模式。本阶段仅 Linux 有隔离快照 worker 运行资格；其他平台显式启用时拒绝启动，仍保留默认模式的 CGO0 交叉构建。启用后 Store 与归档缓存同时使用既有 managed 模式，任何初始化失败均不回退普通模式。

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
  "job_timeout_seconds": 900
}
```

`session_token` 可选。三个资源字段省略或为零时取示例默认值：数据库范围 1MiB–8GiB，内容范围为至少数据库预算且不超过 16GiB，任务时限 60–86400 秒。内容预算不替代加密暂存、对象数量与控制日志的独立上限。恢复 key ID 是外置 key 的稳定身份，必须为规范非零 UUID；它不是密钥材料。配置变更、密钥或版本变化会改变任务 generation；旧任务不自动切换目标或重试上传。

## 管理员接口与任务语义

复用既有站内会话和服务端管理员授权。首次管理员仍按原初始化参数创建，升级不覆盖用户名或密码；普通 owner、匿名调用、share token 和 MCP token 都不能操作或探测全实例备份状态。HTTP 响应为 `no-store`，错误与任务 DTO 不含目标、配置路径、主体、密钥或 SDK 原因。

服务为 `skillhub.management.v1.BackupManagementService`，由 Buf 管理，Go 与 Web 消息同步生成：

| RPC | 用途 |
| --- | --- |
| `GetBackupStatus` | 管理员读取是否启用，固定实例作用域；自动备份和恢复当前均为 false |
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

目前没有备份专用 CLI 任务命令或管理 Web 页，也没有外部 S3 供应商隔离 bucket 与完整恢复演练的发布资格。内部 TLS S3-like 测试证明本地合同，不代表所有 S3 实现均兼容。完整剩余项见 `TODO.md` 与 `FORMAL-RELEASE-AUDIT.md`。

## 本阶段验收

2026-10-31，App/RPC 构造不明所有权经独立真实子进程反例及正控闭合；控制内核初审三项缺陷另经原生 close-EIO、新进程 fence、精确容量边界和并发取消的非作者复核后局部 ACCEPT。修后同组合全仓 7884 PASS / 161 SKIP / 0 FAIL，Buf 双模板 parity 与三组件十八目标 CGO0 构建通过。完整命令、冻结身份、初次失败与后继收据见 `VERIFICATION.md`；不是自动调度、管理 UI、保留清理、硬件断电、外部供应商或完整恢复的验收。
