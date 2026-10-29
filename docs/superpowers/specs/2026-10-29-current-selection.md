# CurrentSelection：认证本地选择查询

日期：2026-10-29。状态：**文档冻结候选，待 Lovelace 独立设计审；不是 Go 源码授权或实现签收。**

## 1. 目标与范围

为后续 native 客户端提供可靠的当前选择计数，避免重启后猜 1、读取最后一条或循环试 revision。依据 `2026-10-28-configuration-native-apply.md`；沿用已签 B 的存储、加密与生命周期，不增加持久 schema、RPC、依赖或权限。

本片仅候选修改 `internal/configapply/native_selection.go`、`internal/configapply/native_selection_test.go`。必须等 B 精确提交及相关 CI RED 根因关闭，主线程另授源码写权才实施。C0-K 安全初始化/丢 key 恢复仍是独立首发缺口；不调整旧 key 创建入口，不实施 C1 CLI/MyGo 编排、自动应用或其它平台支持。

## 2. 接口与分区

```go
type SelectionState struct {
    Exists               bool
    SelectionRevision    uint64
    ConfirmationRevision uint64
    PolicyRevision       uint64
    AutoApply            bool
    DeviceID             string
}

func (engine *Engine) CurrentSelection(ctx context.Context, origin, owner, profile string) (SelectionState, error)
```

- 输入 origin 沿用 NewSelection 的规范 HTTPS origin 校验，owner/profile 沿用 nativeIdentifier；不自动规范化成另一分区。非法输入返回 ErrSelection 与零结果；对合法输入，nil/零值/已关闭 Engine 返回 ErrClosed 与零结果。
- 分区严格复用 selectionKey：origin/owner/profile/**claude/claude.settings/user**。DeviceID 只是该分区当前值，不是查询条件或额外分区键；不增加 agent/target/scope 参数。不得跨 owner/origin/profile 回退查询。
- Exists=true 时逐字段返回 inventory 当前选择的全部三个 revision、AutoApply、DeviceID，不自行递增、重置或推导。结果无 patch、secret、token、key、hash、路径、资产内容；不是可记录任意输入/内部结构的许可。
- 只有完整认证 inventory 成功且精确分区不存在，才返回 `SelectionState{}, nil`。损坏、错 key、pending、容量、权限、取消、锁/目录错误不能伪装成 absent，也不能返回部分计数。

## 3. 生命周期、真实性与副作用

调用者必须先显式 `Open(ctx, home, stateRoot, existingKey)`，持有成功打开的 Engine，再查询并最终 Close。仍受 B 的 Linux/配置根支持限制；getter不查真实 HOME、不建 key、不绕过 Open、不返回秘密。

每次查询复用 engine.run：可取消地获取 Engine mutex，再获取共享 install session/锁，核验 home/state/vault 路径与 inode/owner/mode、pending 安装状态，重新读取真实加密 inventory。成功前必须在同一持锁区间，**重新读取并验证**现有 `native-configuration-root.v1` 的存在/私有 regular 文件身份、版本/type/id、AEAD/key-id 及 home/state/vault binding；不能只依赖 Open 时检查，不能调用可能初始化的 openVault 来补缺失 root。复用现有有界读取/openRecord/readInventory 验证，不另造解密或分区逻辑；未知/损坏记录、链/序号/容量错误均拒绝。成功结果只来自本次验证后的 inventory，不是 Engine 缓存。

“无业务写入”指本方法不追加 selection/operation/backup、不改 revision/policy、不写目标配置，不自动 Apply/Restore/Reconcile。**不是零 FS 副作用：**既有 Open 可初始化 vault并恢复 operation；run/OpenSession 可触及 DB/锁，readInventory 的既有 recovery 可整理记录。沿用这些行为及失败语义，不扩大恢复范围；认证失败也不承诺此前未发生既有 recovery 的 FS 副作用。

## 4. 返回、释放与一致性

- 先取得候选值，再等 run 的全部 defer（inventory 清理、session.Close/锁释放）完成；任一错误均丢弃候选值、返回零结果，保留已有错误语义（包括 ErrOutcomeUnknown），不把 callback 成功误作方法成功。成功发布前再检查 ctx；可观察到的取消返回 ctx 错误和零结果，不声称取消与返回原子。
- Engine.Close 与 getter 由既有 mutex 串行化；关闭先完成则 getter ErrClosed，查询先完成则 Close 后续执行。getter不自行 Close Engine。**调用方**若需要“查询→Close→使用”结果，必须暂存到 Close 成功后才交付；Close 失败丢弃暂存结果并报告，不能声称能撤销已经返回给其它调用者的值。
- 同一已打开 Engine 在另进程完成受锁提交后重新调用，必须看见最新完整计数；读锁期间的结果是一个一致快照，不承诺返回后永远最新，也不提供针对恶意整库历史回放的外部单调性证明。
- 成功 Restore 后读到既有 pauseAfterRestore 的真实结果：selection/policy revision 递增、AutoApply=false，confirmation/device按既有持久值，不猜增量。跨重启同样成立。
- 返回值不是授权、reservation、plan 或设备证明。只有 verified absent 才允许后续调用方提出初始 1/1/0；已有值上的递增/溢出由后续流程处理，本 getter 不实施。后续 Preview/Apply/Restore 仍保留原 selection/TTL/confirmation/CAS，读后竞争不能用本返回绕过。

## 5. 验收与不签范围

所有 fixture 为合成 key/HOME/TempDir、真实 B 落盘与安装锁；Go1.27.3、CGO_ENABLED=0，不用 CGO1/race。必须覆盖：

1. 真 Preview→Close→重开→查询，三个 revision/device 精确返回；认证空分区 absent；多 owner/origin/profile 隔离，换 device 同分区。真实 Apply→PreviewRestore→确认→Restore→重开后暂停计数不丢失。
2. 子进程完成受锁更新，同一父 Engine 的第二次查询看见新计数；锁等待取消、Close 竞争/重复调用正常收尾。用真实子进程锁，不用内存 mock 冒充跨进程。
3. Open 时错 key；成功 Open 后替换/删除 root、交换绑定目录、篡改加密记录/key-id、未知项/链断裂/pending，均错误且全零；没有 selection/operation/backup 或目标配置的新增业务写入。
4. callback 得到非零值后，真实 release/session Close 失败仍全零；调用方 Engine.Close 失败控制验证不交付暂存值。用测试 overlay/受控真实资源故障，不新增生产注入 seam；若两源码候选内无法可靠证明，先报告门禁阻塞/申请调整，不能删验收或扩写。
5. 在隔离副本去 root 验证、绕过解密或强制缓存返回、忽略 release 错误：编译成功后相应测试必须实际 RED。保留原始 RED 与恢复 GREEN。

仅签本 getter 的 Spec/Quality 与真实行为，不签 C0-K、C1、远端授权、完整 native apply、14 agent 或三 OS 运行能力。
