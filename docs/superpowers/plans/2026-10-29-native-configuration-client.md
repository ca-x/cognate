# Native Configuration Client C1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: executing-plans、tdd、ub-mode、use-modern-go（完整 list）。仅在主线程另授源码及固定基线后逐项执行；不得自行 spawn/stage/commit/push。当前授权仅此文档，非 Go、用户 Apply 同意或功能签收。

**Goal:** 交付 existing-key-only 的 Claude/user/Linux 手动 CLI 纵切片，复用 A 的 native proof/消费与 B 的受控发布；同一编排可供后续 MyGo 使用。

**Architecture:** C 负责 typed A→B 转接、认证当前计数、锁外安全展示/逐 slot 人类审批、一次授权消费与错误收尾；不实现 renderer/crypto/发布器。CLI 只负责 flags、凭据/TTY I/O及安全输出，不让 core 依赖 Cobra、TTY或服务器。远端和本地不构成分布式原子事务。

**Tech Stack:** Go1.27.3、CGO_ENABLED=0、GOTOOLCHAIN=local、readonly modules；现有 configclient/configapply/agentconfig/importclient 与标准库。平台 I/O 如获另授，仅复用已存在 x/sys，不加 go.mod 依赖。

**Spec:** `docs/superpowers/specs/2026-10-28-configuration-native-apply.md` §§4–7/9/10.2；C0-S spec/plan。受控输入：ignored `native-client-C-execution-proposal.md` SHA256 `51b80b9a69762afba32b9c9c33b2ba3832cfef9a664594156d45b7c816562568`，`native-client-C-execution-plan-review.md` SHA256 `b874c72eeadb99d520af85d69a20e212509f584b6123f3388c3ec4c3e06a83e9`。下述六项订正均为硬门禁，不沿提案旧 d281。

## 1. 固定基线与权限

| 合同 | 固定 identity / 实施前条件 |
| --- | --- |
| A | commit `58d5a02c392df64b2d260f06697cb5565bbe0c2c` 的 configclient native 源合同；现有 NewNativeClient、OpenConsumer、Prepare、Authorize、Consume、Status、Close，不改 proof/transport |
| B | exact18 commit `0839b6235027399d128db819e0febbab9093e253`，已签5ff来源；不改 B 的 engine/journal/renderer、发布与恢复语义 |
| C0-S | exact2 commit `04ad076529b032082dfa3b8ef1d36d601fba67a1` / tree `1f7963d3153c320a44bf0c12db81c1a650e57497`，manifest SHA256 `6e3e00c048c6bb8eb3d85b1060537ea40cbb8d3058818aa25f37c034a1ddbc32`；**已完整独立 PASS 并提交**，Gauss report SHA256 前缀 `56ef348fb…` |
| K | 尚未实施；本片只加载既有32B key，不暗建/替换，不因零订阅忽略旧数据；新装初始化/丢 key 恢复仍是首发 blocking gap |

当前可实施基线为 commit `34c22a1070e3bdc907d177f89e1bdc426b342e58` / tree `b25f02bb46342d445179940201984ff2c37869d6`；主线程全量验证已 exit0，Gauss 已完成组合机械 ratify，CI pending。本轮仍仅文档授权，C1 Go 源码须另授。

源码 grant 前主线程须提供包含以上已验 delta 的精确 commit/tree；实施者逐 blob核对，不消费移动WIP。rootfix既有 CI 绿不等于 C1 运行证据。当前不改server/proto/gen/module/install/state/A/B/C0或他人B1c；不启其它agent/平台/自动应用/Pack附件。

## 2. 五路径不足：先冻结窄 I/O 前置，不凑 scope

**原五个源码候选保持：** 新 `internal/nativeconfig/flow.go`、`flow_test.go`；新 `internal/cli/native_configuration.go`、`native_configuration_test.go`；改 `internal/cli/root.go` 注册入口及仅 native/config 子树的固定分类、有界错误退出接缝。前两文件仅编排/真实集成测试，后两文件仅 CLI/测试；root 不改变其它旧命令的行为，不引入全仓日志/退出码框架。

实际检查：`importclient.ReadSession(io.Reader)` 只限2049B读取，没有 context/等待上限；`loadClientKey(home,false)` 可复用但不能调用 prepareClientHome（会建 skills 目录）。`prepareWatchOutput` 已有可取消 FIFO/TTY输出，但普通 reader 无对应受控输入，regular writer分支也不能提供硬等待上限。没有可直接复用的独立 `/dev/tty` reader。Linux ioctl/nonblocking实现若塞进 common Go 文件会破坏其它OS构建，不能假设 runtime.GOOS 分支能隐藏不编译的符号。

**另授前置候选三路径：** 新 `internal/cli/native_configuration_io_linux.go`、`native_configuration_io_other.go`、`native_configuration_io_linux_test.go`。只为本命令提供有界 session stdin、独立确认TTY及最终错误诊断所需的有界输出，不造通用输入/key框架。common接口声明随 native_configuration.go；平台 helper 与五路径需作为同一冻结候选一起编译，非独立可发布半接口。总候选为**五+三=八路径**，Gauss 已批准此方向，主已接纳 C1-PLAN-01 的 root 窄扩展；设计修订仍须独立复审及主另授源码。完整交互 Apply/Restore 不得以仅 preview 或无限等待替代，不新增第九路径。

I/O接口候选：`readNativeSession(ctx context.Context, input io.Reader) (string,error)`；`openNativeConfirmationTTY(ctx context.Context) (nativeTerminal,error)`，nativeTerminal只提供 ReadContext/WriteContext/Close。Linux保留原stdin，不改 borrowed fd 的flags/deadline；对可重开本地fd创建私有O_NONBLOCK/CLOEXEC句柄、验证身份/能力、使用真实deadline与取消并收尾；内存reader或受控ReadContext reader可供测试，未知reader先拒绝。只在有界reader之上调用原 ReadSession，不复制session格式校验。禁止TTY作为凭据输入（避免回显），无TTL的EOF等待不合格。regular文件若无法证明可取消限时读取则明确拒绝，不偷偷降级。

确认只开本进程控制TTY，验证为terminal；没有TTY、EOF、拒绝、过长/未知答复均不批准，绝不回退token stdin。TTY不读秘密，不更改raw/echo模式；canonical下只读有界明确答复。非Linuxhelper固定unsupported，不打开目录/设备。真实PTY/阻塞pipe/满输出测试须验证取消、独立句柄释放、borrowed fd仍可用；不能仅用strings.Reader模拟TTY。reuse prepareWatchOutput仅限已证明有界的类型，不能把其regular-file直写分支当硬deadline；普通不可取消reader/writer在网络/授权前拒绝。输出总量≤64KiB，每次line/答复≤128B；凭据输入≤2KiB/10s；每次输出等待≤5s，交互等待≤min(60s,plan剩余期限,总deadline)。不启动无法join的读写goroutine。

最终错误出口同属此边界：在 ExecuteContext 返回后，root 对已识别的 config 子树请求（含未知子命令、flag/args 前置失败及未进入 RunE 的失败）只走固定分类的有界诊断，不再落入旧的无界 fmt.Fprintln(os.Stderr, terminalText(err.Error()))。不得依赖 RunE 包装错误或仅设置 Cobra Err writer 来识别；无法安全输出时丢弃诊断仍 exit1。不得修改 caller FD flags/deadline、借 dup 修改共享 OFD，或遗弃写 goroutine；取消/超时后的诊断也须在≤5s内结束，不能因原 ctx 已取消退回无界输出。此本地收尾预算不允许新网络请求或重放业务。

## 3. 可复用 core 接口与所有权

在 `internal/nativeconfig/flow.go` 定义以下最小合同（类型使用现有agentconfig/configapply/configclient）：

```go
type ApplyRequest struct {
    OwnerID, AssetID, ProfileID string
    AssetRevision              int64
    Apply                      bool
}
type Approval struct {
    Confirmed     bool
    ApprovedSlots []agentconfig.ClaudeSecretSlot
}
type Interaction struct {
    Preview func(context.Context, configapply.SafePlan) error
    Approve func(context.Context, configapply.SafePlan, []agentconfig.ClaudeSecretSlot) (Approval, error)
}
type Result struct {
    Preview       configapply.SafePlan
    Local         configapply.SafeResult
    RemoteState   configclient.NativeGrantStatus
    RemoteUnknown bool
}

func New(home, stateRoot, origin string, existingKey []byte, native *configclient.NativeClient) (*Client, error)
func (client *Client) Apply(ctx context.Context, request ApplyRequest, ownerSession string, ui Interaction) (Result, error)
func (client *Client) Restore(ctx context.Context, sourceOperationID string, apply bool, ui Interaction) (Result, error)
func (client *Client) Status(ctx context.Context, operationID string) (configapply.SafeResult, error)
func (client *Client) Close() error
```

- NativeClient由构造方拥有/Close；nil仅供离线 Restore/Status，Apply必须拒绝。当前A没有公开Origin getter，origin必须显式传入；CLI使用同一原值构造A并传C，C在Prepare后再次核对，**不得读私有字段或修改A**。nil native的纯本地实例可省origin，不得调用Apply。Client拥有32B key副本及其打开的短期 Engine/NativeFlow，关闭取消并join；不代Close调用方的NativeClient。同一Client一次只有一个操作；不持内部串行锁调用UI/网络/Close等待，busy/closed明确错误。引用只在内存，不建新state。
- Client以强opaque表示保护所有fmt verbs/外层private carrier/真实slog/JSON，不能只给原始private字段加Formatter（沿已修B的根因）。不把ownerSession放入可打印Request；函数接收后只在必要内存保留，错误固定分类、不包装原始网络/IO错误，不log request/plan/材料。
- safe回调必须合作可取消、有界，core不靠泄漏goroutine“强行timeout”。回调不重入同一Client的操作或同步Close；取消时先返回，由外层cancel并Close/drain，避免等待自身worker。Preview只含Plan.View派生安全metadata；Approve收到克隆slots/安全view，不能拿到opaque Plan、commitment、材料、整文件diff。Apply=true但Approve=nil必须拒绝；preview模式绝不调用Approve/Authorize/Consume。CLI确认摘要另带已选server/owner/profile的安全转义展示，不能只凭asset名让用户猜账户；不将这些原始输入写日志。Result只报告安全事实与unknown，不暴露bearer/grant/密钥/patch/secret前后缀长度hash；禁止把AppliedOnDisk叫Activated。
- CLI只用默认home的 `.config/skillhub/client.key`，loadClientKey(home,false)，stateRoot显式传home下 `.local/share/skillhub`；不依赖ambient路径在core内查HOME，不创建agent目录。CLAUDE_CONFIG_DIR非默认按B拒绝，不新增路径flag。调用方existingKey及从材料CloneBytes产生的临时副本均clear，B SecretMaterial/A NativeMaterial均Destroy；不承诺清除Go string/GC所有副本。

## 4. 明确状态机与计数/typed转接

1. **本地presence前置，网络锁外。** 显式选择claude.settings/user与资产/revision/profile；Open B→CurrentSelection(origin,owner,profile)→成功Close后才使用结果/发网络。错误/Close失败全停，绝不当absent。Exists=false才候选1/1/0并crypto/rand生成一次opaque DeviceID；Exists=true则SelectionRevision与ConfirmationRevision各+1（任何uint64溢出先拒绝），保留PolicyRevision与DeviceID，AutoApply=false。不让flags传计数，不猜1、不扫描最后一条、不换device绕分区，不自动重试CAS。
2. **A准备。** 单次OpenConsumer(ctx,owner,session)→Prepare(NativeSelection{AssetID,AssetRevision,ProfileID,SelectionRevision})。C无直接GitHub/renderer/moejs调用；A生成OperationID。资产int64 revision必须>0后无损转B uint64，SchemaVersion=1。B OperationID是A `[16]byte`的固定小写32位hex；PreparationID原样；不生成第二OperationID或重签proof。
3. **精确转B。** A NativePreparation的Origin/OwnerID/ProfileID/AssetID/revision/schema/selection必须匹配输入；Adapter完整descriptor/definition digest、RequiredCapabilities、typed Patch和PreparedAt/PreparationExpiresAt/NativeSessionExpiresAt逐字段深拷贝到SelectionInput，不重新映射字段。填既有confirmation/policy/device，Selected=true/Scope=user，PolicyID空。NewSelection→重新Open B→Preview→Close，成功后才交付Plan.View。仅显式选择触发Preview的加密selection/Planned记录；preview不是零state写入，但目标配置零写、零Authorize/Consume。B Plan.ExpiresAt已取server preparation/session与本地上限的min，不从现在新开5m。
4. **本plan人类动作。** Close完成后展示安全slot动作、权限收紧、absent/create、可恢复缺失窗口及“不保证agent加载”。逐RequiredSlot各要求明确approve，再单独确认精确当前view/operation；native-login即使零slot也必须最终确认。严格核ApprovedSlots与当前RequiredSlots相等且无重复/扩展/旧approval复用；全部成功、ctx/期限仍有效才Plan.Confirm。任何否定/输出失败/取消/过期停止，不Authorize/Consume、不触目标。登录、token输入与代表设计ACCEPT不是批准。
5. **授权、消费仍锁外。** Authorize的PreparationID/OperationID/Commitment/IntentExpiresAt/Slots/ConfirmationRevision均取原A/B绑定与Plan安全getter；显式分支PolicyID=""、PolicyRevision=0，**不是**B本地暂停policy计数。Authorize返回materializationRequired=false时零Consume；true仅一次Consume。A58d当前每次为零或**一个**RequiredSlot，NativeMaterial为单份bytes：以A已验证的唯一slot映射B SecretMaterial，不把API key填auth-token/反之，不假装支持同次双slot；两种slot均以独立真实fixture批准/拒绝。若将来出现多个slot，C立即拒绝、不循环Consume绕过A合同。
6. **本地发布。** 有完整材料（或native-login无材料）才重新Open B→Apply(original confirmedPlan, exact slot map)→成功Close后报告。保持原TTL/selection/target inode+bytes CAS；秘密消费成功不保证本机能应用。CAS/过期/外部修改保留原错误/文件，不刷新计数/换plan/补秘密重试；整个reserve/publish阶段不含网络或UI。
7. **远端unknown。** Authorize/Consume通信不明仅在原活flow、仍有可用ctx时至多一次Status（A自行新nonce），不自动再Authorize/Consume/OpenConsumer，不捏造rollback/成功。Consumed只证明服务端消费事实，缺材料绝不Apply；Canceled无可用ctx就返回unresolved，不新开detached后台请求。Prepare响应丢失且无可签绑定同样unresolved，不能假称能Status。退出Destroy ephemeral私钥/grant/bearer；跨重启远端旧flow无法签Status，明确能力缺口，不持久化token/私钥补洞。
8. **本地unknown。** 返回安全durable OperationID，后续显式Status调用B.Open既有恢复与Status（必要沿B已有Reconcile），不直接重放Apply。Status不是纯FS读：Open/recovery既有副作用披露。安全结果NeedsAttention/恢复事实照实输出；历史terminal操作返回事实不是再次授权，不能当agent当前Activated。

## 5. 离线 Restore

Restore不需要NativeClient/session/server网络。Open B→PreviewRestore(sourceOperationID)→Close→展示SafePlan→最终人类确认→Confirm→重新Open→Restore(Plan)，不是以历史ID盲写。无secret slot不省最终确认；必须保原exact postimage CAS、先备份当前postimage、外部编辑保护与policy暂停。返回前再次B.Status(restorePlan.OperationID)读取真实selection/policy计数，Close成功才交付。Status只提供这两个计数，不伪造confirmation/device；下次Apply必须重新CurrentSelection读全量，不从restore缓存推导。无需给B添owner/profilegetter或猜用户传入分区。不从logout/远端删资产推导restore许可。

## 6. CLI合同、命令与退出

`newNativeConfigurationCommand()` 产出 `config` 父命令，root 负责 AddCommand 与仅此子树的固定分类、有界错误退出接缝（§2）；其它旧命令行为保持。apply flags与已有CLI风格一致：`--server` HTTPS origin、`--owner`、`--asset`、`--revision`正int64、`--profile`、`--target claude.settings` 均显式；`--token-stdin`必须；`--apply`默认false。拒未知target/scope、不提供文件path/secret/token值flag、env、通用--yes、auto开关。`--timeout`默认2m，必须>0且≤5m；凭据/网络/确认各上限再取min。非交互默认preview可行；显式--apply无控制TTY固定拒绝，不能静默当已批准或转成成功preview。

```sh
# SESSION_INPUT 是已打开的受限输入流示意；不把token放argv/env/示例历史。
cognate config apply --server https://hub.example --owner OWNER --asset ASSET --revision 7 --profile default --target claude.settings --token-stdin < SESSION_INPUT
# 同次重新准备/显示/逐slot确认；不是复用上次preview的批准。
cognate config apply --server https://hub.example --owner OWNER --asset ASSET --revision 7 --profile default --target claude.settings --token-stdin --apply < SESSION_INPUT
cognate config restore --source-operation OPERATION_ID
cognate config restore --source-operation OPERATION_ID --apply
cognate config status --operation OPERATION_ID
```

restore默认只preview；status仅本地durable operation，不声称跨重启远端查询。preview成功/实际本地成功/已知状态查询成功exit0；拒绝、取消、过期、冲突、Close失败、NeedsAttention或unknown整体非0（沿当前root Execute的1，不扩退出码框架）。正常可写stderr只接收固定安全分类诊断，不含原始error/路径/请求body或用户原样flag/args；满pipe、closed stderr、不可证明有界的sink允许零诊断，但仍须有界 exit1，不能让root再次无界打印。失败也可经安全输出返回原操作已发生的事实：Authorize/Consume/本地发布后的取消或收尾失败不得改称零写入、已回滚或未消费。所有输出含Cobra前置错误及最终出口都受§2限制；输出失败不能继续Authorize。真实目标/备份计数/readiness显示无“可apply已授权”的误称。

## 7. Review Focus（与验收一一绑定）

- presence读取后Close失败、损坏库存及uint64溢出：不能发RPC、归零重建或改变设备分区（Task1）。
- stdout/stdin/TTY停滞、EOF、Ctrl-C、伪终端答复/旧批准：无秘密消费、无目标写、无goroutine/fd泄漏（Task0/3）。
- 准备与确认间过期/撤销/换owner/origin/selection、确认时第二进程取安装锁：原plan绑定和最终CAS必须生效（Task2）。
- Consume响应丢失/真实COMMIT后取消与本地unknown：只Status，不重放秘密，不伪造rollback或Activated（Task2/4）。
- 外部编辑、restore后暂停、新设备值与跨重启：不丢本地extras/计数，不按历史ID强写（Task1/4）。

## 8. TDD任务与真正行为门禁（均未执行/未授权）

### Task0：裁定I/O写集与基线，未决即阻塞交互源码grant
- [ ] 非作者设计审本计划并裁定五+三；主另授精确写集、交付C0-S独立PASS/commit与A/B组合identity。若不扩三路径，不称五路径足够完成首发CLI交互。
- [ ] 读全部绑定spec/六项订正/modern-go完整list，记录Go1.27.3/CGO0及固定base/blob/manifest；复核已有safe output的能力限制。
- [ ] 如另授I/O，先写真受限pipe未EOF/超限/取消及控制PTY子进程、满PTY/pipe输出/独立borrowed fd测试RED→GREEN；未授不得编码。最早拒绝unsafe reader/writer，token通道与人类通道分离，真实TTY缺失零Authorize/Consume。nativeclient不引入跨OS编译破坏。

### Task1：core presence、typed转接与preview纵切片
**Files:** nativeconfig/flow.go、flow_test.go；消费A/B/C0-S，产出§3接口及无秘密Result。
- [ ] 写 `TestNativeConfigurationPresenceAndPreview`：真实临时HOME/default state、既有key、SQLite/服务端moejs/六RPC Connect TLS fixture；无key/坏inventory/CurrentSelection Close错误/溢出都零RPC、零key/agent目录新建。认证absent1/1/0与已有递增/Device继承分别真实落盘、重启可读；getter读后并发改选择，Preview拒绝而不循环计数。
- [ ] 最小实现 New/Close、presence查询→Close、一次OpenConsumer/Prepare→深拷贝转B→Preview→Close；纯客户端生产依赖不得import gen/server/moejs，fixture依赖仅测试。保存初始缺接口RED与随后真实行为GREEN，二者不混。
- [ ] `TestNativeConfigurationBindingsAndTTL`：所有typed字段/operation编码/三个权威期限真实贯通，服务端prep快过期不能在C重开TTL；native-login/两个secret分支各preview无Authorize/Consume/目标写。只显示安全View；fmt私有外层carrier/真实slog/JSON/错误与临时state不含合成token/secret。

### Task2：明确人类批准、一次消费与真实本地发布
**Files:** 同core两路径；依赖Task1接口，测试用受控合作回调而非mock A/B内核。
- [ ] `TestNativeConfigurationExplicitApproval`：API key、auth-token两个独立真实provider fixture，各批准/拒绝/EOF/取消/slot不匹配；native-login最终批准但零Consume。证明两个slot不交叉，拒绝零Authorize/Consume/目标写；登录/token/Preview/旧Approval均不能自动同意。
- [ ] `TestNativeConfigurationLocksAndCAS`：真实HTTP entry和人类回调双barrier，第二进程可取install锁；Apply后发布实际bytes/mode、备份加密、无关env/native extras保全。用户等待期间外部edit/credential轮换/撤权/换owner/origin/过期/并发selection全部拒绝，不偷偷再Prepare/消费。B.Close错误后不交付成功，clear/Destroy所有自有材料/临时key副本，取消真实drain。
- [ ] `TestNativeConfigurationRemoteUnknown`：真实服务端Authorize或Consume已COMMIT再扣响应/返回错误，原活flow只能新nonce Status；计数断言每步一次且无新flow/补发Consume，物理服务端状态与本地无材料不写均核实。非secret分支同样不把未知Authorize当批准；Prepare丢响应如实unresolved。

### Task3：CLI薄接与独立交互
**Files:** cli/native_configuration.go、native_configuration_test.go、root.go；仅在Task0补授权后消费原三路径平台I/O函数。root 除注册只改 config 子树的固定分类/有界错误出口，保其它旧命令，不扩八路径。
- [ ] `TestNativeConfigurationCLI`：真实TLS/backend、合成HOME/key，运行实际命令进程；默认preview/flags/缺key与unsupported/退出1，真正控制PTY逐slot+最终确认，token只走独立pipe。阻塞token/TTY/输出、拒绝/EOF/Ctrl-C、超长答复、关闭输入后原fd可用及worker排空；非交互--apply拒绝，无--yes/envtoken后门。
- [ ] 注册config父命令并接通§2/6的root最终错误出口；校验参数与I/O能力后才联网。未知config子命令、未知flag、缺必填flag、错误args/值等RunE前失败也必须分类并有界退出，不打印原样参数。CLI不重复A/B映射、crypto或provider渲染；status/restore不读token、不连网络。断言输出不含秘密、hash/长度/整文件diff，不记录敏感snapshot/HAR/真实配置。
- [ ] `TestNativeConfigurationExecuteExit`：编译真实main二进制，实际调用cli.Execute而非仅ExecuteContext；合成HOME、真实loopback fixture与受控父子进程分别覆盖已填满且无人读取的stderr pipe、closed stderr、可写stderr正控及取消/命令超时。对前置flag/args失败和已进入RunE的失败均断言：在命令取消/超时或错误确定后≤5s收尾预算内自行exit1，父进程真实Wait/join；前置失败零Authorize/Consume、零目标写。正常sink应有固定分类诊断且不含注入参数/secret；unsafe sink可丢诊断但不变exit1。父持borrowed pipe FD检验前后flags不变，子进程内另验取消收尾无遗弃worker/句柄；排空、关闭fixture并回收全部进程。超时kill只能使该测试失败并清理，不能充当有界退出证据。后段取消另核实际远端/本地事实，不声称回滚。
- [ ] 对最终出口加入编译成功的退化控：将config错误退回旧无界stderr打印，满pipe用例须在其有界退出断言真实RED（随后kill/join清理，不靠整测timeout）；恢复候选GREEN。保可写stderr正控和其它旧命令行为回归，不能以静默所有命令/只测RunE error来通过。

### Task4：restore、重启unknown与负控/冻结
**Files:** 同五路径及获另授的三I/O路径；不改B或生产seam。
- [ ] `TestNativeConfigurationRestoreAndRestart`：真实Apply→restart→PreviewRestore→锁外确认→Restore，actual postimage备份和旧preimage/absent恢复、外部修改拒绝、Status读取暂停计数、下一Apply CurrentSelection读取全计数。历史ID不能绕Preview/Confirm；local unknown重启按durable operation恢复，不声称可签旧远端Status。
- [ ] 隔离overlay/mutant至少：自动批准/省最终确认、读计数失败当absent、slot交叉、延长TTL、持安装锁等待确认/网络、重建plan绕stale CAS、unknown重试Consume、secret外层carrier泄漏、无界TTY输入。各先compile再按指定有界断言真实RED；不得以缺接口/panic/整测timeout当负控。B的原CAS不删：可在C mutation中模拟错误“重读计数并重建plan重试”，以外部编辑/选择竞争控制捕捉，不扩写B。
- [ ] 保存每次RED/GREEN、错误fixture、负控patch/hash/commands/退出码，回归原A/B/C0-S不减少。逐top/subtest统计run/pass/skip；新测试不准以skip掩盖缺TTY/不支持路径。三OS仅compile可另记，Linux runtime不代替其它OS。
- [ ] 精确冻结已授权路径/base/tree/manifest/报告，停源码写入交非作者Spec/Quality与真实行为审；主负责组合全量/提交。旧C0-S冻结证据不覆盖；本计划验收后也不等于Go已授权。

## 9. 验证命令（获授权实现后）

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
export CGO_ENABLED=0 GOTOOLCHAIN=local
go version
go env CGO_ENABLED GOTOOLCHAIN
go test -mod=readonly -run '^TestNativeConfiguration' -count=1 -v ./internal/nativeconfig ./internal/cli
go test -mod=readonly -run '^TestNativeConfiguration' -count=3 -shuffle=on -json ./internal/nativeconfig ./internal/cli
go test -mod=readonly -count=1 ./internal/nativeconfig ./internal/cli ./internal/configclient ./internal/configapply
go vet -mod=readonly ./internal/nativeconfig ./internal/cli ./internal/configclient ./internal/configapply
go mod verify
```

真实fixture只绑定loopback TLS，A的可注入RootCAs消费测试CA，禁止InsecureSkipVerify/降级HTTP/redirect。有界deadline/PTY真实子进程不等于spawn代理；跨OS构建单列日志，不启CGO1/race。此处命令不是已执行PASS。

## 10. 后续保留

MyGo C2另片保留：复用本core安全回调与结果，native控件/worker/generation/cancel/drain、账户切换清理和真实GUI验收，不从Cobra调用或注入通用--yes。K初始化、其它13agent、非Linux、自动应用/Pack附件、跨重启远端proof恢复及完整F12依然独立首发追踪，不能被本Claude/Linux/手动/CLI片删除。
