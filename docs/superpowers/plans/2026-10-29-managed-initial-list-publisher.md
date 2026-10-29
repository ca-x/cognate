# B1c Managed Initial Lists Publisher

> **For agentic workers:** 获源码授权后使用 executing-plans、tdd、ub-mode、use-modern-go 完整版本指南；不得自行 stage/commit/push 或 spawn。

**Goal:** 把 scheduler selected-Lists 首次 cache.Put→CollectListRepository 纳入同一维护 writer 闭包。

**Spec:** `docs/superpowers/specs/2026-10-28-server-backup.md` §3.1；不另造需求 spec。

**Architecture:** 提取 library 已有 physical-peer 配对检查供两个 constructor 共用；scheduler 复用 Store.WithMaintenanceWriter。网络在 scope 外，leased context 覆盖内容发布与真实事务结束。

**Tech Stack:** Go1.27.3、CGO_ENABLED=0、readonly modules，现有 SQLite/library/cache/maintenance，无新增依赖。

## 授权与固定写集
- 当前仅授权本计划。必须等 Task2 精确提交、相关测试根修及 CI RED 关闭，主线程交付固定 commit/tree 后另授 Go；不得消费移动源或自行开始以下步骤。
- 候选四 Go 路径：`internal/server/library/service.go`、`internal/server/library/service_test.go`、`internal/server/scheduler/scheduler.go`、新 `internal/server/scheduler/maintenance_integration_test.go`。无法在此范围可靠证明门禁则先报告，不增生产 seam。
- 不改 Task1/Task2 其它文件或冻结文档、state/maintenance/platform、schema/module；不启 App managed、备份 endpoint、coordinator 或 S3，不宣称全写屏障/备份已可用。
- 仅合成 TempDir/HOME/key/凭据和 loopback HTTP；不读真实配置，不用 CGO1/race。默认 unmanaged 多进程行为保持；managed 非 Unix failclosed 复用现有能力检查，不新增平台名单或任意 Instance。

## 1. 配对校验：先行为 RED，再提取
- [ ] 固定授权 base/Task2 四相关 blob，记录工具链与 scope；读取现有测试和 modern-go 完整 list。
- [ ] 通过真实 scheduler.New 测 mixed mode、foreign physical root、替换目录拒绝，同物理 peer/default 接受；拒绝后调用方 Store/cache 仍自有可用。保存旧实现编译成功的错误配对行为 RED。
- [ ] 导出 `library.ValidateMaintenancePair(ctx context.Context, store *state.Store, cache *ArchiveCache) error`：nil 拒绝、模式一致；managed 内按原逻辑 Store.WithMaintenanceWriter→cache 私有 enterWriter→verifyDirectory→释放临时 admission。不同 gate 由现有 nested admission 拒绝，不用路径字符串或可伪造标识证明身份。unmanaged 不新增 FS/单进程门禁；保留取消/关闭错误语义，不接管组件生命周期。
- [ ] NewService 原检查改为调用此函数；scheduler.New 在参数验证后、建立 lifetime 前调用（签名不变，沿现有 constructor 使用 context.Background）。不为校验构造闲置 Service；两个入口回归 GREEN。

## 2. 首次发布闭包：真实纵切 RED→GREEN
- [ ] 真实 SQLite、Lists HTTP、归档/cache 跑 RunOnce 首次收集；受控停在 Put 发布后、DB 操作前，发 Quiesce，证明 Exclusive 等待且已准入的 nested DB 仍可结束。旧实现须编译后断言 RED；不可用缺接口、panic、skip 或整体超时代替。
- [ ] BeginListSourceAccess/容量检查、FetchSource 授权及 fetch/scan、snapshot 检查留在 scope 外。只包 cache.Put→CollectListRepository 的 Store.WithMaintenanceWriter，返回 typed CollectedRepository；WithSourceAccess 必须基于回调 leasedCtx，保留原 ticket/result/reference。真实 retry/COMMIT/rollback、Put 清理返回后才释放；成功后的 cache.Verify 留在 scope 外。
- [ ] 已收集分支原 AuthorizeSource→Verify→CheckSourceAccess 不改。不重写分页/游标、ErrBusy、总 deadline、lifetime/Close；SourceGuard/SourceFence、凭据/Lists/MCP 拒绝、历史 private、CAS、未知 COMMIT 规则不减。授权检查不冒称与 COMMIT 原子。

## 3. 失败、网络与寿命门禁
- [ ] HTTP entry barrier 阻住真实 fetch：Exclusive 可获得；持 Exclusive 释放响应，后续发布被 admission 拒绝，不能产生 stage/DB 内容变更。不得只用 sleep 或 callback 次数猜网络是否开始。
- [ ] Put 失败的真实 cleanup 尾部、scheduler.Close/cancel、Store/cache 独立引用与合作子进程锁：Exclusive/物理 Close 等实际清理，无泄漏/提前放锁。沿原失败语义，不因 cleanup 把取消/unknown 改报成功。
- [ ] 凭据轮换、Lists 撤选/expiry、并发收集/CAS 保全 DB；实际已有分支仍校验权限。失败后 immutable cache orphan 不等于 authority。真实 COMMIT 后丢回执与提交前拒绝各覆盖：错误分类/无成功结果、物理 readback、仅一次尝试/无盲重放；可复用既有测试 overlay，不新增生产钩子或削弱 MCP/SourceFence 原测试。
- [ ] 隔离 mutant 至少四项：去外层 scope、丢 leasedCtx、绕过 pair 检查、把 fetch 移进 lease。各自编译后对应有界行为 RED（网络 mutant 以 Exclusive 无法及时获取断言并真实取消收尾，不以死锁/panic 判红）；恢复精确源 GREEN。保存 overlay/patch/hash/命令/退出码。

## 4. 验证与 freeze
- [ ] Go1.27.3/CGO0：新测试统一 `TestManagedInitialLists` 前缀，运行 focused count1，再 shuffle count10 JSON；核 top/subtest 真 run/pass、零新 skip。跑 library/scheduler/state 回归与 vet；default 多进程、Task2 原测试保持。跨编译只记静态，不冒称平台 runtime。
- [ ] 记录实际 RED/GREEN/receipt/负控及 invalid setup；冻结四 Go delta/base/tree/manifest 与报告，停止源码写入交非作者独立审。主线程负责组合全量与提交；未实际通过不勾验收项。
