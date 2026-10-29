# CurrentSelection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans after explicit source grant; task-by-task TDD, independent acceptance. No subagent spawning, stage, commit or push without separate authorization.

**Goal:** 查询真实认证 inventory 的当前选择，消除客户端重启猜 revision。

**Architecture:** 在现有 Engine.run 的 mutex/共享 install lock 生命周期内验证 root 与 inventory，复用 selectionKey 投影安全值。候选结果只有在释放路径成功且最终 ctx 检查通过后交付；无新业务写入，保留既有 recovery 副作用。

**Tech Stack:** Go1.27.3、CGO_ENABLED=0、现有 configapply/install/加密文件存储；无新增依赖。

**Spec:** `docs/superpowers/specs/2026-10-29-current-selection.md`。

## Global Constraints
- 本计划是文档冻结候选；Lovelace 独立设计审、主线程批准、B 精确提交与 CI RED 根因关闭后，才可另授 Go 写权。当前不得执行下列源码步骤。
- 源码候选仅 `internal/configapply/native_selection.go`、`internal/configapply/native_selection_test.go`；不动 B 其它文件、持久 schema、RPC、模块、CLI/桌面、key 初始化或既有计数规则。
- 全部 fixture 合成 TempDir/HOME/key；Go1.27.3、CGO_ENABLED=0，不读真实配置，不用 CGO1/race。跨平台 compile 不冒充运行支持。

## Review Focus
- Open 后 root 被替换/删除：必须每次持锁重验，不能仅凭已打开 Engine 信任成功。
- callback 有结果但释放失败：不得逃逸候选值；Engine.Close 的调用方边界另测。
- 跨进程新 revision：不能缓存；device 变化不产生新分区。
- recovery 与只读名称冲突：核实无新增业务写入，保留并披露既有 FS 整理，不制造零副作用承诺。
- absent 与错误、读与授权混淆：任何失败全零，成功快照之后的原 CAS 不减。

## Task 1：认证查询最小纵切片（获授权后）

**Files:** Modify `internal/configapply/native_selection.go`；Test `internal/configapply/native_selection_test.go`。

**Interfaces:** 产出 spec §2 的 `SelectionState` 与 `(*Engine).CurrentSelection(ctx, origin, owner, profile)`；消费现有 `engine.run`、`selectionKey`、state.read/openRecord、inventory.selections。无新持久对象。

- [ ] 固定主线程交付的 B commit/tree/18 manifest；记录源码 identity、Go/CGO 环境，读相关现有 root/inventory/release/restore 测试及 use-modern-go 完整 list。确认两路径可容纳实现/故障测试；不能则先报阻塞。
- [ ] 写 `TestCurrentSelectionRoundTrip`：真实 Preview 持久化→Close→重开，精确三个 revision、AutoApply、DeviceID；认证空分区零值 nil；非法输入/零值/closed Engine。先运行并保存缺接口或缺行为 RED，再最小实现接口、输入校验、run 内投影。
- [ ] 写 `TestCurrentSelectionRootAndInventoryValidation`：Open 后 root 缺失/替换/密文或 key-id 篡改、目录 binding 换位、inventory 未知项/断链/pending；另测 Open 错 key。逐条实际失败断言全零；在本次锁内重验现有 root，复用有界解密验证，不用 openVault 补 root，再 GREEN。
- [ ] 写 `TestCurrentSelectionPartitionsAndRestore`：混合 owner/origin/profile，device 变更仍同分区；实际 Apply/Restore/重开核暂停计数与 confirmation/device 原值。读取前后对比业务记录与目标 bytes/mode，不把允许的 recovery 整理误当 getter 业务写入。保留原 Preview/Apply stale CAS 拒绝控制。

## Task 2：发布/并发失败闭环与独立交付（同两路径）

- [ ] 写 `TestCurrentSelectionCrossProcessFreshness`：测试二进制 helper 用同一 TempDir/key 完成受锁更新，父 Engine 两次读取之间等待子进程真实完成，验证最新值；加锁阻塞取消/Close 排队控制与严格 timeout，无真实 HOME/网络。
- [ ] 写 `TestCurrentSelectionReleaseFailure`：callback 已取得非零值后制造真实 session/release 故障，要求错误且 SelectionState{}；另写调用方暂存→Engine.Close 失败丢弃值控制。测试专用 overlay 可留证据目录，不新建生产 seam；无法在候选范围可靠控制则报告，不用 mock/删断言顶替。
- [ ] 最小完成错误清零与最终 ctx 检查；验证所有返回路径、锁顺序、inventory 销毁、nil/closed行为，不改变 release/Close 的底层语义。
- [ ] 在隔离副本机械退化 root 校验、解密校验/缓存新鲜性、release错误处理；每个变体必须先 compile，再指定行为测试 RED。保存 patch/hash/退出码，恢复精确候选 GREEN，不改共享源码做负控。
- [ ] 运行以下 focused 命令并保留完整日志/JSON；每个 top-level 计数核对无 skip/fail。必要再跑 configapply 包回归，main 负责组合全量，不重复认领历史 B 全验。

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
export CGO_ENABLED=0 GOTOOLCHAIN=local
go version
go env CGO_ENABLED GOTOOLCHAIN
go test -mod=readonly -run '^TestCurrentSelection' -count=1 -v ./internal/configapply
go test -mod=readonly -run '^TestCurrentSelection' -count=3 -shuffle=on -json ./internal/configapply
go test -mod=readonly -count=1 ./internal/configapply
go vet -mod=readonly ./internal/configapply
```

- [ ] freeze 精确两源码 delta/base/tree/manifest、RED/GREEN/负控/环境及未闭边界，停止源码写入；交非作者独立 Spec/Quality/真实行为审。未验收不把框打勾，不自行签 PASS，不自动 commit。

## 文档交接

本轮仅生成本 spec/plan，交 Lovelace 独立设计审、主线程合入；不授作者 Go 实施权。C0-K 初始化仍单列首发缺口，C1 编排不在本计划。
