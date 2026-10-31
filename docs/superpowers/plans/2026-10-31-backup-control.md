# F4 备份产品控制平面实施计划

基线：`8a701c5`。目标是将已交付的同切点 Capture 与 S3 target 接到真实服务端管理员入口，而不是再次把内部层称为完整 F4。完整首发仍须补管理 UI、自动调度、retention、完整离线恢复和运行代切换。

## 已定范围与安全决策

- 复用 Go/ent/SQLite/Afero/Echo/Connect+Buf，不改数据库 schema 或可信 catalog，不新增依赖，不引入 cgo。
- 备份默认关闭。显式 `--backup-enabled` 后，App 的 Store/cache 一起进入既有 managed 模式；未启用的部署行为保持不变。失败不回退普通模式。
- 这是敏感全实例运维灾备，不是 owner 导出；所有读取/启动/取消/对账先验证现有服务端管理员。普通 owner 和匿名请求不能探测设置、目标或任务。
- 独立 32 raw byte 恢复 KEK 从外置私有文件读取；既有 server master key 派生控制日志密钥，不派生 KEK，不上传任一原始密钥。S3 凭据也只来自外置私有配置，不进入 RPC、日志或任务 DTO。
- 最多一个实际执行任务，忙时拒绝，不无限排队。任务登记后不绑定 HTTP 请求生命周期，显式取消独立进行。自动调度及自动删除在本片保持关闭。
- 控制状态是私有、加密、版本化的操作日志，位于 `backup/control`，不塞进 skill 数据库或伪造新的 catalog 资格。该操作日志不是 skill 内容权威闭包；恢复需显式重新配置控制平面。

## 固定内部接口

`internal/server/backup` 新增以下接口，供 App/handler 接线。构造失败不返回半有效句柄，默认 fmt/JSON/slog 不显示控制器内部信息。

```go
type ControllerOptions struct {
    Generation [32]byte
    AppVersion string
    ContentLimits library.ContentSnapshotLimits
    MaxJobs uint32
    JobTimeout time.Duration
}

type Job struct {
    ID, Phase, Code string
    CreatedAt, UpdatedAt time.Time
    Objects uint32
    CiphertextBytes int64
    CancelRequested bool
}
type JobPage struct { Jobs []Job; Next string }

func OpenControlStore(context.Context, *os.Root, []byte) (*ControlStore, error)
func (*ControlStore) InstanceID() [16]byte
func (*ControlStore) Close() error
func NewController(context.Context, *ControlStore, *library.Service, *S3Target, SealEngine, ControllerOptions) (*Controller, error)
func (*Controller) Start(ctx context.Context, principal, requestID string) (Job, error)
func (*Controller) Get(ctx context.Context, jobID string) (Job, error)
func (*Controller) List(ctx context.Context, limit int, before string) (JobPage, error)
func (*Controller) Cancel(ctx context.Context, principal, jobID string) (Job, error)
func (*Controller) Reconcile(ctx context.Context, principal, jobID string) (Job, error)
func (*Controller) Close(context.Context) error
```

`principal`、`requestID`、`jobID` 是规范 UUID；HTTP handler 只能从已鉴权会话取得 principal。`ErrControlConflict`、`ErrControlNotFound`、`ErrControlBudget`、`ErrControlUncertain` 为稳定错误；复用既有 invalid/busy/closed/denied/unavailable，任何原始错误先脱敏。Job 是刻意安全的 DTO，不含 principal、目标、路径、密钥、manifest 正文或 SDK 原因。

## 控制日志与恢复语义

- 复制并拥有 root capability；Afero RootedFS 访问固定文件名，物理 owner/private/nlink/身份前后复核，进程间锁确保单控制器。操作日志最多 16MiB，最多 1000 个保留任务；达到容量显式拒绝，不自动丢幂等历史。
- AES-GCM 新 nonce，HKDF-SHA256 域 `cognate/server-backup/control/v1`，固定版本/实例等进入认证；有界解码、拒绝未知字段/重复键/非法状态。未知或损坏文件不能当成空实例重建。
- 更新是候选副本 → 私有临时文件 → 文件 Sync/Close → 原子 Rename → 目录 Sync。成功后才替换内存状态；任务登记/catalog 发布先预留最坏后继记账，干净容量拒绝不 poison。关键不明保留/隔离原能力与错误，不假装回滚或重试 close。最后锁 close 不明不能声称物理锁仍持有：先持久 `control.fence` 阻断跨进程重开，部分标记亦拒绝。确认物理释放后旧能力永久 closed，再移除/Sync 标记；这之后的清理不明若删除已可见，新进程可能进入，但旧 closed 能力不能再写或附着控制器。不得将所有清理不明一概描述为仍持有 OS 锁/持久标记。不得在正常日志或 fmt 中输出密文/明文控制状态。
- 随机实例身份只在明确空控制目录首次创建，绑定同一 master key。幂等键绑定 principal、完整配置 Generation 与选项；相同键同输入回原任务，变化冲突。Generation 是 App 计算的有密钥承诺，包含规范 S3 配置、恢复 key 身份和预算。
- 在任何远端上传前，持久化本次所有 ObjectRef 身份/长度/hash。完整 catalog 和 complete-attempted 意图先于相应远端副作用。分片/manifest/bootstrap 条件上传验证后才尝试 complete，complete 最后。
- PutUnknown 不自动重发 PUT；只允许精确 VerifyExact 对账。远端成功不能靠 List/ETag 推断。只有所有已登记对象精确 verified 才返回 `remote_committed`；它不等于 `restore_verified`。
- 重启时运行中任务变 interrupted；可能尝试过 complete 的任务保持 commit_unknown。Reconcile 只读已登记确切对象，全部通过才能升级成功；缺失/未知不得自动生成新 attempt 或删除旧对象。配置 generation 不匹配则拒绝探测旧目标。
- cancel 先持久化请求，再撤销 worker context；取消不表示回滚已发生的远端副作用。complete 可能发送时保留 unknown，而不是假装不存在。Reconcile 落盘重查最新取消，不由过期网络副本覆盖 canceled；complete-attempted 不明仍为 commit_unknown，全部确切对象验证通过仍可 remote_committed 并保留取消标志。同任务对账不重新快照。
- Controller.Close 拒新任务、取消并有界 drain；超时或清理不明返回关键错误。App 在失败时不能提前关 library/store、根能力或实例锁。成功后按 controller → target/engine/control store → 原 App 服务的次序释放。

## 写集与并行

- 控制平面 worker：新增 `internal/server/backup/control_store*.go`、`controller*.go` 及直接测试，不修改旧 seal/target/source 文件或 App/proto/UI。不提交，不再派子 agent。
- 主线程：App/CLI startup 配置、严格秘密文件解析、Connect+Buf protobuf/生成物、管理员 handler/测试、需求与进度文档。立即本地完成协议和生产入口，不把主线程的下一步交给 worker 等待。
- UI worker：独立公开目录/详情精致化固定写集，与备份接口无交叠；不能用此 UI 片替代备份管理页。
- 源码稳定后，非作者分别复核控制平面、App/RPC 与公开目录；最后同一组合回归与 SSH 提交读回。

## 行为验收

- [x] private root/进程锁/加密日志篡改/错误 key/未知格式/预算/原子发布及 close uncertainty；在进程退出与故障注入模型内验证重启幂等、完成意图及 private release fence，不外推硬件断电。
- [x] 同切点 Capture → 真实 TLS S3-like → complete 最后 → 各对象精确回读；源屏障释放后才有网络慢上传。
- [x] 同主体幂等、跨主体隔离、Generation 冲突、并发忙、HTTP 取消不杀持久任务、显式取消/关机 drain/提交竞态和 restart 对账。
- [x] 真实 App 显式 enabled/disabled、managed 配对、管理员/普通用户/匿名真实 Connect、错误脱敏、CLI 参数；配置不进入默认 fmt/log。
- [x] 独立 App/RPC 审查与控制内核三项反例复核、修后全仓 CGO0/vet/modules/十八目标构建及 Buf 双模板生成 parity。

修后收据见 `docs/VERIFICATION.md`；非作者三项 finding 范围内 ACCEPT 及必要资格见 `/tmp/cognate-kernel-recheck.KZcIAz/REVIEW.md`。残留/部分 fence、旧满额日志无自动恢复/迁移能力；确认释放且标记删除可见后，旧能力必须永久 closed/quarantined，不能将后续清理不明当作仍持物理锁。阶段提交、SSH 推送和记忆由主线程在上述门禁后执行。

本片不关闭完整 F4；无外部供应商隔离 bucket 和完整恢复演练证据时，不宣称供应商兼容或可恢复首发完成。
