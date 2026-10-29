# MyGo Native Configuration UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task after explicit source authorization. Steps use checkbox (`- [ ]`) syntax. 当前仅授权本文与 ignored 作者报告；不授权 Go、plan 外文档、stage/commit/push、spawn 或实施。本计划须非作者设计审；源码须在 E2 正式签交后串行另授。

**Goal:** 交付已接受 Claude provider 原生配置子规格的完整垂直链路：typed 资产 CRUD、保存与本机应用分离、同次 exact 安全预览/逐 slot/最终批准、状态/Unknown/合法恢复，以及真实原生事件与资源收尾验收；不是仅密钥入口。

**Architecture:** 现有 `desktop.Service` 追加窄可选配置能力，复用 generated Connect、`nativeconfig.Client` 和现有 MyGo Model/View。调度复用 E2 的 slots/workers/真实 pendingJobs、publication/generation/Close/Wait；不另建 worker 池、锁、JS、crypto、journal 或确认协议。配置专属状态/服务/界面各自独立文件，旧生命周期仅加小接入。

**Tech Stack:** Go1.27.3、CGO_ENABLED=0、pinned MyGo v0.7.1 原生 ui、Connect/Protobuf、已交 clientkey/configclient/nativeconfig/configapply；无新依赖、wire、DB schema 或客户端 moejs。

**Spec:** `docs/superpowers/specs/2026-10-29-native-configuration-ui.md`，SHA256 `280ad3c5efc9d7cdc046473900b3cb37fe9fa06abb12404f22ad796d06d0d033`；非作者 Spec/Quality design ACCEPT 报告 `.superpowers/sdd/2026-10-28-release/native-configuration-ui-spec-review.md`，SHA256 `9357a9eebf63e360c78427d87394cea6fb68bbc0be360c786565b7bef39ab66a`。spec 的历史依赖状态不改写；以下补精确 successor 接缝，不把旧候选追认为已签。

## Global Constraints

- 完整 `docs/REQUIREMENTS.md` F12、`docs/CCSWITCH-CONFIGURATION-MATRIX.md` 14-agent 与总体 `2026-10-28-configuration-ui.md` 继续有效。本片仅 Claude/provider/schema1；其他已知/未知 agent/kind/schema 安全 metadata-only、编辑/应用拒绝，不丢条目、不泛化 JSON。值级/文件全文 diff、完整跨重启历史、多 agent/MCP/prompt/workspace、导入/Pack 配置与自动应用继续是缺口。
- 当前 C1 已由主交付 commit `3b0345ead6bc27b906e89e9fb55bed8a8f081c72`，其 `internal/nativeconfig/flow.go` blob `a88bf39bd08e714203dbfac20885801a09531a38` 与 spec 读取的旧接口相同；保留旧 feb 候选拒签历史，不复签 C1。已签 portability commit `656c211d01a9d7d2632c5cee12b49f6fb3229ca9` 的 cast/VERIFICATION 不算本片修改。
- **E2 只作为被冻结 API 输入，不当已验收：** base656，tree `3a4cda344ee816cf1db17b09d027f033e473fe0b`，manifest SHA `a5606189d51424317847f65e7aade62a196ad2f8e7ccee145df54e052f182376`，`/tmp/cognate-entrypoints-E2.6CgS5ncj/clean-final`。Gauss findings/作者修复优先，本任务不动该候选。实施必须等主交付 E2 最终 ACCEPT+精确 commit，机械核本文接缝；若 successor 改 API/生命周期，先明确 scoped 复核，不靠此计划越过 gate。
- key 唯一政策来自 entrypoints spec SHA `4bf561538e1ddc46a546c7d99652df5f4336cee5ec61e57706b919f9b0806d3a`。唯一初始化为账号 LocalSettings 的 E2 `Backend.InitializeClientKey(context.Context) error`/`Model.InitializeClientKey()`；配置页缺 key 只导航账号，不加第二按钮/隐式 init。远端编辑、登录、公开浏览不需要 key。
- 固定默认 `Service.Home` 与 `home/.local/share/skillhub`，无 root/path/raw-key 选择器。只 `clientkey.LoadExisting`；副本在 C1.New 复制后立即 clear，defer 兜底。key 准入物理锁在后续构造/HTTP/业务前释放；Model 逻辑占用不等于安装锁，不能持物理锁等 HTTP/人类。
- 资产 RPC 复用 Service 的 origin/禁 jar/redirect/同 origin 策略，单请求20s、序列化请求≤128KiB、未压缩响应≤1MiB、父 ctx 更早则从父。真实 owner 由同 origin/session 的 `AuthService.GetSession` 取得，不猜用户名。服务端逐请求授权/CAS仍为最终边界。
- Apply 固定 HTTPS，由 `configclient.NewNativeClient(origin,*http.Transport)` 自己落实网络约束；不得注入通用 RoundTripper 绕过。测试只真实 TLS+合成 RootCAs。本机任务 **2分钟总预算**（准入/网络/确认/执行），确认截止=min(job deadline, SafePlan.ExpiresAt)，不延长核心期限。远端编辑沿现有60s job，每次 RPC仍≤20s。
- Linux 本机 Apply/Restore/Status；非 Linux 在 key/FS/网络授权前固定 unsupported，资产编辑仍可用。cross-build不是 runtimePASS。所有测试 TempDir/合成 owner/session/key/文件，禁止真实 HOME、账号/upstream、CGO1/race。
- UI eventloop 只更新状态/非阻塞回复，所有 IO/锁/C1.Close/NativeClient.Close 在已登记 worker；关闭靠 ctx 解除等待，不依赖关闭后 dispatch，既有 App.Run 返回后 Model.Wait 真 join。不能以 Busy=false、取消请求或 UI 回调完成冒充实际 drain。
- 原生 MyGo 成熟组件与系统主题，ui-ux-pro-max/emil/apple/animate 原则用于可见异步反馈、焦点/稳定布局/无新动画。没有真实原生窗口证据不得签视觉完成；E2 已记录 X 授权失败不是本片窗口证据。

## Review Focus

1. absent≠present-empty、两个 secret 的 Keep/Replace/Clear 和未知保留；尤其 copy/冲突后不得夹带原 Replace（Task1/2）。
2. 同一 `Apply(true)` 的安全视图与最终批准，零 secret 也必须确认；false预览/另一次 operation 不可复用批准（Task3/4）。
3. 等待 Modal 时远端授权/消费与目标写为零、安装锁可被子进程取得；取消与实际 Close/drain之间不能抢开本地任务（Task3/5）。
4. 错误同时携带本机状态/RemoteUnknown/Close失败；不因 generic job 丢弃安全结果、报成功或重放消费（Task4/5）。
5. 非常规 fmt/私有容器、迟到 dispatch、脏草稿取消与身份失效、窄窗/200%/危险 Enter，均须真实反例而非 Stringer/HasText 推断（Task2/3/5）。

## 0. 固定身份、候选写集与接口

### 0.1 唯一未来候选8 Go路径（仍需主另授）

| 路径 | 责任 |
| --- | --- |
| `internal/desktop/model.go` | configuration 状态接入；Cancel/clearOwner/Close 的私有缓冲与批准清理；复用 E2 job/实际占用，禁止复制另一组计数/锁 |
| `internal/desktop/view.go` | 保留 Page0–3/原 key；追加 Page4“AI 配置”、导航脏草稿门与账号到本机状态/恢复链接 |
| `internal/desktop/configuration_service.go`（新） | typed wire转换/预算/身份绑定、CRUD；C1本机三动作薄代理及安全 outcome |
| `internal/desktop/configuration_model.go`（新） | 可选 backend、typed 编辑与安全状态、一次性确认桥/结果分类、资产/profile切换 |
| `internal/desktop/configuration_view.go`（新） | 列表/结构化编辑/确认Modal/状态恢复，成熟原生组件 |
| `internal/desktop/configuration_service_test.go`（新） | 真 SQLite/HTTP/TLS、C1、加密/文件/子进程锁及故障oracle |
| `internal/desktop/configuration_model_test.go`（新） | 真实事件接服务、代际/实际drain、纯生命周期 witness、隐私/负控 |
| `internal/desktop/configuration_view_test.go`（新） | headless鼠标键盘/主题/窄窗/200%与安全截图；原生运行证据另存/tmp |

`service.go`、`watch.go`、storage、cmd/main、key/core、server、proto/gen、module、其他 tracked docs不在候选写集。新方法可以属于原 `Service`，无需改 service.go。若现有生命周期、故障overlay或真实 native focus能力确需新增路径/修改依赖，先给具体反例申请窄扩集；不能偷扩或削掉对应验收。

### 0.2 消费的已存在签名

E2 `Model.jobWithTimeout(label string, limit time.Duration, work func(context.Context)(func(),error))`、`localWorkBlocked() bool`、`publish(ctx,update)`、`slots/workers/pendingJobs`、`generation/life/closed`、`Cancel/clearOwner/Close/Wait` 是本计划接缝：本机方法和按钮均在开始前检查 `localWorkBlocked`，任务返回前含全部实际清理；远端资产 IO不拿本机安装锁。不把这些候选私有字段升级为外部 API。

```go
nativeconfig.New(home, stateRoot, origin string, existingKey []byte, native *configclient.NativeClient) (*nativeconfig.Client, error)
(*nativeconfig.Client).Apply(ctx context.Context, request nativeconfig.ApplyRequest, ownerSession string, ui nativeconfig.Interaction) (nativeconfig.Result, error)
(*nativeconfig.Client).Restore(ctx context.Context, operationID string, apply bool, ui nativeconfig.Interaction) (nativeconfig.Result, error)
(*nativeconfig.Client).Status(ctx context.Context, operationID string) (configapply.SafeResult, error)
(*nativeconfig.Client).Close() error
```

`ApplyRequest{OwnerID,AssetID,ProfileID string; AssetRevision int64; Apply bool}`；`Interaction{Preview func(context.Context,configapply.SafePlan)error; Approve func(context.Context,configapply.SafePlan,[]agentconfig.ClaudeSecretSlot)(nativeconfig.Approval,error)}`；批准仅 `Approval{Confirmed bool; ApprovedSlots []agentconfig.ClaudeSecretSlot}`。不导出 Confirm/token/material/私有 Flow。

### 0.3 本片产生的窄接口/数据（不新增共享 DTO 或 RPC）

在 configuration_model.go 定义包私有可选 `configurationBackend`，Model 对原 Backend 做类型断言；旧 Backend 无能力则显示 unsupported，不改所有 legacy fake，不 fallback 到直接文件写：

```go
ConfigurationIdentity(ctx context.Context, origin, session string) (configurationIdentity, error)
ConfigurationAssets(ctx context.Context, identity configurationIdentity) ([]configurationAsset, error)
ConfigurationDraft(ctx context.Context, identity configurationIdentity, id string) (configurationDraft, error)
SaveConfiguration(ctx context.Context, identity configurationIdentity, edit configurationEdit) (configurationDraft, error)
CopyConfiguration(ctx context.Context, identity configurationIdentity, asset configurationAsset, name string) (configurationDraft, error)
DeleteConfiguration(ctx context.Context, identity configurationIdentity, asset configurationAsset) error
ApplyConfiguration(ctx context.Context, identity configurationIdentity, asset configurationAsset, profile string, apply bool, ui nativeconfig.Interaction) (configurationOutcome, error)
RestoreConfiguration(ctx context.Context, operationID string, apply bool, ui nativeconfig.Interaction) (configurationOutcome, error)
ConfigurationStatus(ctx context.Context, operationID string) (configurationOutcome, error)
```

- `configurationIdentity` 提供安全 Origin/OwnerID、内部 opaque session；来自 GetSession，身份切换立即作废。本机 Apply 在其 worker内 fresh GetSession并比较 OwnerID，不接受表单 owner。CRUD沿session由server重新授权，不把缓存身份当grant。
- `configurationAsset` 仅 ID/AgentID/Kind/Name/SchemaVersion/Revision/可编辑能力；保全合法未知 metadata。`configurationDraft` 含该 metadata、typed Claude auth mode、endpoint+四模型各自 `*string` presence、两slot presence/readiness/preserved-extras，不含原secret。读草稿先 GetAsset判能力，再 ReadDraft；列表不预取草稿。
- `configurationEdit` 私有负载包含 asset ID/expected revision（创建无ID）、name/auth mode、endpoint/四模型presence与两个 secret 的精确 Keep/Replace/Clear 联合；Replace缓冲不出私有负载。沿现域 name≤128B、endpoint≤2048B、各model≤256B，非空/UTF-8/无控制字符和auth/endpoint联合仍由域最终判断，不能trim或截断修好输入。草稿和session/请求私有部分采用 opaque closure-backed payload，不直接把敏感 string/[]byte放进可递归打印的 Model字段；暴露方法仅是本包编辑/清理，不能新增任意JSON编辑或提取秘密的公共getter。所有具体容器仍需privacy反例证明，不把closure形式本身当PASS。
- `configurationOutcome` 是仅安全的 SafePlan/SafeResult深拷贝、原 RemoteState/RemoteUnknown、固定失败分类与最终cleanup状态。原生 `Result,error` 在service工作域提炼后丢弃原始error/DTO引用；Close失败合并判失败，不能只返回error丢掉已知安全operation ID。Model格式化/日志不接raw wire/private结果或error树。
- Model新方法固定为 `LoadConfigurations`、`NewConfiguration`、`SelectConfiguration(id)`、`SaveConfiguration`、`CopyConfiguration`、`DeleteConfiguration`、`PreviewConfiguration`、`ApplyConfiguration`、`RestoreConfiguration(apply bool)`、`ConfigurationStatus`、`ReplyConfigurationApproval(confirmed bool)`。增加私有 `configurationNavigate(page int)`、`configurationCancel(identityLost bool)`；本机三操作均绕过 remote-ready/login 条件中不适用的部分，Restore/Status可离线。
- confirmation 包含安全快照/绑定的 generation+origin+owner+asset/revision+profile+operation、截止与 **每操作 cap1回复channel**；requiredSlots有序克隆、勾选初始全false。worker持ctx，UI仅非阻塞一次回复，不能把channel或privatedata当持久授权。

## Task 1: typed资产服务、秘密与CAS（spec §§2.1、3）

**Files:** 新 configuration_service.go / configuration_service_test.go；configuration_model.go 中只放接口和必要私有类型。**Consumes:** 当前 auth/configuration generated clients、Service.client(origin)、§0类型。**Produces:** 上述 Identity/Assets/Draft/Save/Copy/Delete 真服务方法；不依赖本机key。

- [ ] 固定最终接受E2基线clean export、8候选原blob/mode；读完整spec/本plan及modern-Go1.27完整list、ub/TDD/同步与UI skill。记录C1与E2 successor接缝，不把movingworkspace当候选。
- [ ] **先编译再行为RED**：`TestNativeConfigurationAssetService`，真实server+SQLite、两owner/session、无keyhome。List未知agent/schema保留且不ReadDraft；GetAsset/ReadDraft合法Claude；创建/更新/复制/删除真实HTTP，wrongowner拒绝，Copy无secret，250配额/非法metadata/128KiB send/1MiB解压response/20s父取消/redirect/jar界。缺功能但compile失败只算setup，不算RED。
- [ ] `TestNativeConfigurationPresenceAndCAS`：endpoint/default/haiku/sonnet/opus的absent、present有效与present空；native-login+endpoint按现域拒绝，不偷偷清空。两个slot分别Keep/Replace/Clear、空Replace拒绝、模式切换不删另slot、extras原样；新建Keep按现域拒绝，UI新建默认未保存/Clear或显式Replace，不能把没有原值的slot标成“已保留密钥”；复制/另存新资产同样不携原secret。hold-response制造revision冲突与撤权，核原revision/加密记录未覆盖。错误树含合成URL/token不准进入日志。秘密用真域加密存储验证，不仅比较mock参数。
- [ ] 最小实现 generated clients：`connect.WithSendMaxBytes(128<<10)`/`WithReadMaxBytes(1<<20)`，每RPC `context.WithTimeout(parent,20*time.Second)`；请求session仍走现auth header合同。wire仅在worker局部构造，bound检查/已知字段合法性failclosed不截断保存；资产最多250按现域限值，不import server包到client。服务端仍最终校验/授权。测试真实gzip与实际编码预算，不用proto.Size猜JSON字节。
- [ ] 保存只 Create/Update，不触发 key/Store/selection/Prepare/Authorize/Consume/目标写。Copy只sourceID+expected_source_revision+name，不接旧secret；Delete固定expected_revision并由域处理引用拒绝。代码返回固定分类，不把parser/connect/transport原文拼入UI。重跑同selectors GREEN+相邻域协议回归；只在本任务有行为绿后进入Task2。

## Task 2: 完整原生编辑/导航与保存分离（spec §§3、6–7）

**Files:** model.go/view.go小接入；新 configuration_model.go / configuration_view.go / configuration_model_test.go / configuration_view_test.go。**Consumes:** Task1服务、E2普通job/代际。**Produces:** 列表→typed表单→显式保存/复制/删除、冲突/dirty/秘密清理；保留原账号/Pack/Magpie/公开目录。

- [ ] **RED** `TestNativeConfigurationEditEvents`：真实Model→Service→HTTP，Click/Type/Key创建和编辑，不直接调用方法代替事件。认证三选、endpoint+四模型各“显式设置”checkbox、两slotpresence+Keep/Replace/Clear/Password、readiness/extras文案；未登录、加载、未选、不支持、脏草稿/需凭据状态可见。列表只读metadata；选中才读draft。无key也能CRUD。
- [ ] `TestNativeConfigurationSaveNeverApplies` 独立HTTP/FS计数断言：初render/跨页/login/保存均零key创建、零native Prepare/Authorize/Consume/目标写；保存成功revision/presence更新而不改变本机selection。复制显式无secret；删除确认“只删服务端资产，不撤销既有本机写入”。未知agent/kind/schema不能被强转/丢失/编辑。
- [ ] `TestNativeConfigurationDirtyAndConflict`：普通切页/切资产/新建有dirty确认，Cancel保非敏感草稿；identity/origin/logout/Close不等dirty对话框，立即清理。冲突不rebase、不拉新版后重试；保非秘密草稿、清Replace并置需重新选择secret操作，提供确认丢弃重载和不带secret另存新资产。任何成功/失败/取消/切资产/离开编辑器清Replace/构造请求引用；既有表单密码/session清理由E2保证并回归。
- [ ] View追加稳定 `configuration-entry`，旧Page0–3和控件key不变；原Account唯一init保留，配置缺key仅“前往账号→本机设置”。920×760列表+详情，640×600/200%单列列表→详情→返回，取消/结果不被裁；按asset/slot ID而非索引绑定。原生 `Column/Row/Scroll/Text/List+ListState/Select/TextInput.Label.Password/Checkbox/Button.Disabled`，不新造基础组件或动画。
- [ ] 身份/编辑数据仅opaque私有负载；非秘密safe view深拷贝，publication及真正dispatch执行都验 generation/life/ctx/选择身份。所有动作无rawDTO/error入Model或debug输出。接口缺失backend固定unsupported。重跑事件/GREEN，保原desktop回归不删skip/断言。

## Task 3: 一次在途exact确认桥与实际排空（spec §§2.2、4、6）

**Files:** configuration_model.go / configuration_service.go / configuration_view.go 及对应3测试，必要model.go生命周期接入。**Consumes:** E2 `jobWithTimeout/localWorkBlocked/publish/Close/Wait`、C1 API。**Produces:** `PreviewConfiguration`(false) 与 `ApplyConfiguration`(true)独立动作；确认仅回复原true调用，不另起Apply。

- [ ] **RED** `TestNativeConfigurationExactApproval`：真实TLS GetSession→Prepare→C1→ui.NewTester Modal。未回复时零Authorize/Consume/目标写，独立子进程取得原安装锁；Preview/Approve返回actual SafePlan。主动作一次 `Apply(true)`；另“仅预览”只false、允许Planned磁盘记录但无Authorize/Consume/目标写，后续true必须新operation、新预览/批准。编译先成功再以请求/文件计数断言RED。
- [ ] Service本机Apply先ctx/平台/typedasset/revision/profile与origin的非创建能力检查，再 `LoadExisting` 并完成全部准入锁release；成功取得key立即注册clear兜底，覆盖后续取消、身份RPC和构造失败。全部release成功后重新检查ctx，才fresh GetSession匹配原identity的OwnerID，再 `NewNativeClient`→C1.New→立即clear入口key→C1.Apply。key不进入Model/identity，不删fresh身份复核，也不以预先取得安装Session替代此顺序。仅从Service.HTTP里的真实 `*http.Transport`取允许的TLS设置交原构造器；自定义RoundTripper不得替代native运输。全阶段同2分钟parentctx；不用prepareHome暗建无关agent目录。
- [ ] `TestNativeConfigurationKeyAdmissionBeforeIdentity`：真实TLS计数与合成HOME，在本次本机动作启动前取计数基线（不计此前无key远端编辑/登录）。分别覆盖初始化必需、恢复必需、不可用、不支持四类core sentinel，以及等待准入锁被取消和release后fresh ctx gate取消；本次fresh GetSession、native RPC、业务Store/engine打开、目标写计数必须均为0。有效key则在全部准入release后才出现fresh GetSession，成功继续；撤权/owner不一致停止，入口key始终清理。用真实锁/受控barrier验证次序，不能用mock调用顺序代替；纯远端CRUD/登录/浏览仍无key可用。
- [ ] `configurationJob` 在 configuration_model.go 作为现 `jobWithTimeout` 的薄编排，本机开始前localWorkBlocked闸；不复制slots/pendingJobs/WaitGroup。worker return前实际C1.Close合并error、NativeClient.Close、clear私有输入后才释放E2占用。把已脱敏outcome与固定错误分类作为同一个completion处理，不能让generic job `err!=nil`路径吞安全operation/Unknown，亦不能靠忽略error显示成功；ctx/generation已失效时不回填旧结果，用既有预览ID和“不保证回滚”提示及freshStatus查验。
- [ ] 固定completion规则以适配E2实际helper：worker将outcome+error合成仅安全的completion（失败分类也是数据）；ctx仍有效才返回更新配置结果/覆盖generic Status的closure，通用job返回nil仅表示该closure可交付，**不表示操作成功**。closure按completion的失败/Unknown/cleanup字段决定状态，禁止无条件成功。ctx已结束则返回原ctx错误给E2安全取消/超时出口，不为交付结果绕过generation/ctx；Cancel路径立即展示不保证回滚并保留本身份可用的安全预览ID，identity失效则清理。`TestNativeConfigurationUnknownAndClose`须同时断言安全operation可见和非成功分类，deadline/queuedCancel另验无旧状态回填。
- [ ] Preview深拷贝安全视图。Approve创建cap1reply并投递Modal，worker `select(reply, confirmationCtx.Done)`；confirmationCtx截止=min(jobdeadline,ExpiresAt)，到期拒绝不延TTL/重Prepare。event仅非阻塞reply一次，Cancel/Escape/outside-dismiss/资产或profile变更/identity/Close均拒绝并取消；关闭后无dispatch也必须收尾。`ui.Context.After`仅请求倒计时重绘，workerctx才保证expiry收尾；不加每次取消遗留的timer/goroutine。
- [ ] Modal标题“本次安全操作预览（非文件全文差异）”，逐项显示owner/origin/profile/asset+revision/operation/target/adapterID+version+digest、Readiness/ExpiresAt/权限收紧/可恢复缺失窗口/sourceOperation；不展示literal/旧值/文件/摘要/token。Apply核七env slot及实际动作完整、无重复/未知slot/action；Restore显示自身实际Operations不伪造七行旧值。只允许逻辑 `claude.settings/user`，不读目标文件补diff。
- [ ] required secret slot原数组顺序默认全未选，只允许 `claude.api-key`/`claude.auth-token`实际被请求项；七env动作不是七份秘密授权。七env固定为 ANTHROPIC_API_KEY、ANTHROPIC_AUTH_TOKEN、ANTHROPIC_BASE_URL、ANTHROPIC_MODEL、ANTHROPIC_DEFAULT_HAIKU_MODEL、ANTHROPIC_DEFAULT_SONNET_MODEL、ANTHROPIC_DEFAULT_OPUS_MODEL。必须全部required明确勾选且绑定/期限有效，再最终按钮回复exact有序ApprovedSlots+Confirmed。零slot仍最终显式确认；不默认全选、不按Enter勾选。
- [ ] **GREEN** `TestNativeConfigurationApprovalLifecycle`：TLS/真实安装锁/等待人工分别被Cancel、资产/profile/origin/账号/logout/Close切断；事件仍可Tab/取消，取消未实际return前重复click/直接Model写均拒绝，Close不UI-thread Wait，实际Model.Wait不用回调就join。真实I/O barrier与纯Model受控witness分开；MyGo raster不放synctest bubble。queued旧generation回调执行时也拒绝，不只测试投递。

## Task 4: 真实应用/Unknown/Status/恢复（spec §5及完整本机主链路）

**Files:** configuration_service/model/view 三文件及对应测试。**Consumes:** Task3同次审批桥、C1.Result/SafeResult。**Produces:** 用户能区分已应用/冲突/未知/待处理，能合法离线Status/Restore；无重放或平行恢复实现。

- [ ] **RED→GREEN** `TestNativeConfigurationApplyAndRestore`：纯合成TLS/server/SQLite/真实Claude文件，API-key→auth-token→native-login三转换；七slot精确set/remove/reference、未知大整数/object env/转义key保留由真实C1+codec结果独立JSON oracle核。选列表只是编辑焦点；Apply只已保存无dirty，固定assetID+revision/profile(default)；跨客户端同profile才同partition，不自行写selection或显示未核验“当前provider”。
- [ ] `TestNativeConfigurationConflictPreservesFiles`：确认中改远端revision/撤权、本机外改、到期/profile无效；固定revision失败保全，不自动换最新版/重算确认。使用真实子进程/核心state检查partition+counter+CAS，不用UI缓存当前项跳过。profile/operation输入不当路径，≤128B、非空、核心标识字符（ASCII 0x21–0x7e且排除 `/`、`\`、`.`、`"`），最终仍core校验。
- [ ] `TestNativeConfigurationUnknownAndClose`：真实TLS受控丢响应/授权或消费不确定、已签核心FS/Close故障overlay接真Service；安全remote state与local state分开，Consumed绝非Applied，Unknown sticky不被后来Status洗绿，不自动再Authorize/Consume/Apply。成功仅无error、无NeedsAttention且实际cleanup完成；BUSY/权限/信任/expiry/取消只固定分类与核实/恢复提示，不删锁/journal、修权限或强制覆盖。若原故障seam不在允许路径，先申请测试路径，不加shippinghook。
- [ ] `ConfigurationStatus`仅C1.Status，输入原operationID；UI有界最近20个safe操作（含已预览/已返回ID）且identity失效清理，不新增持久历史/目录扫描/远端grant查询。显式ID可跨重启找本机原durable记录；不声称Status零副作用。
- [ ] `RestoreConfiguration`不需远端登录/origin/资产存在，平台/LoadExisting/本地可信root照常，C1.New(native=nil,origin="")。从账号本机设置可导航到配置本机状态区，不增加init按钮；logout先清旧context，用户再显式发起。Restore(false)仅预览；true新operation/新exact批准，展示sourceID，基线冲突拒绝强制恢复，不回填旧secret、不改远端资产/grant。
- [ ] UI完整结果矩阵含：未批准/仅预览、Applied未声称agent重载、revision/基线冲突、Expired、RemoteUnknown、OutcomeUnknown、NeedsAttention/BUSY/权限、取消/timeout/Close错误；最后一类写明“已发生写入/消费不保证回滚”。不能以generic“完成”盖掉这些结果。重跑同selectors、key/CLI/Pack/Watch/nativeconfig/configapply真实回归。

## Task 5: 完整原生可用性、隐私与独立负控/冻结（spec §§7–9）

**Files:** 候选8范围内测试/必要实现收尾；证据只ignored报告或/tmp，不在本plan勾运行结论。**Consumes:** Task1–4真实链路。**Produces:** 精确immutable8候选及足够让非作者复验的证据；不自行签收或提交。

- [ ] `TestNativeConfigurationKeyboardAndLayout`：MyGo `Modal/AlertDialog`、`Handle.Focus/Focused/OnShortcut`、`ListState`现API；Modal打开焦点安全说明/取消、Tab/ShiftTab不逃出、Escape/outside-dismiss拒绝、危险最终动作不绑定默认Enter，关闭回发起控件或身份失效后的安全入口。真实Click/Key/Type、焦点断言和截图库联合，不只HasText。确认Modal未实现containment则明确blocked并申请最小能力，不自造focus-trap或豁免。
- [ ] light/dark×920×760/640×600×Tester.SetScale(2)安全截图，单列scroll/返回焦点/固定取消和结果、标签/Password/checkbox/无障碍焦点、正文4.5:1；无新动画。沿成熟主题/偏好；当前native无共享locale偏好接缝时列为跨模块依赖，不偷加独立存储或宣称F11全覆盖；本片固定中英文操作/分类文案对照一致，不改用户资产名。
- [ ] `TestNativeConfigurationPrivacy`：raw DTO明确敏感、debug_redact不保Go String；在普通stdlog/slog+fmt所有非常规verbs、slice/map/私有容器/JSON对model/edit/identity/prompt/outcome及成功失败oversize/error-overlay路径捕获，合成key/session/proof/Replace/原文文件/error URL不得出现。必须有恶意error/wire返回与嵌套payload oracle，不能只Stringer或private字段自证。截图不显示secret/原文，切身份/完成/取消后不残留Replace或构造请求引用，不承诺Go string/IME物理擦除。
- [ ] `TestNativeConfigurationUnsupported` 在Darwin/Windows真实runner核metadata编辑仍可、本机限制原因和key/FS/网络授权零副作用；Linux host与crosscompile分开记。至少Linux实际MyGo原生窗口→真实mock-free TLS服务闭环及键盘/Cancel/关闭截图/事件证据。无display/X授权不能用headless冒充window，也不能用旧E2失败抹成本片PASS；保留pending并交主安排真实runner。
- [ ] 至少六类**先compile0再bounded行为RED**负控：①保存偷调Apply；②false预览后复用批准到新true计划；③漏单slot或零slot最终确认（两个变体）；④提前释放本地占用/删dispatchgeneration检查（两个变体）；⑤吞Close/Unknown自动重放（两个变体）；⑥rawDTO/error/privatepayload输出。每个mutant保存patch/hash/selector/命令/exit/实际断言、有限时cleanup；panic/全局timeout/compile失败/零匹配皆invalid，不记RED；恢复exactsource再freshGREEN。
- [ ] 运行下列门禁，逐项记命中/skip/exit。冻结精确base/tree、8路径blob/mode/SHA、patch/tar、作者报告与依赖边界；如授权最终不是8路径，按主批准清单核，不自行凑数。source停写交非作者Spec+Quality+native视觉验收。无stage/commit/push/spawn；主另授精确提交。

### 未来门禁命令（不是本次已执行结果）

在主交付最终E2基线的独立clean export，显式Go1.27.3工具链和仅合成runtimeHOME；不得改全局Go选择。

```sh
GO=/home/czyt/.local/share/mise/installs/go/1.27.3/bin/go
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" version
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" test -mod=readonly ./internal/desktop -run '^TestNativeConfiguration' -count=1 -v
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" test -mod=readonly ./internal/desktop -run '^TestNativeConfiguration' -shuffle=on -count=20 -json
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" test -mod=readonly ./internal/clientkey ./internal/desktop ./internal/cli ./internal/configclient ./internal/nativeconfig ./internal/configapply ./internal/install ./internal/subscription -count=1
(cd web && npm ci && npm run build)
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" test -mod=readonly ./... -count=1
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" vet -mod=readonly ./...
CGO_ENABLED=0 GOTOOLCHAIN=local "$GO" mod verify
```

另记录六OS/arch桌面build及Darwin/Windows tests compile-only、host无DISPLAY desktop测试、客户端deps无server/plugin/moejs。资产协议未改不重生成proto来制造无关diff；若集成要求Buf双模板parity在隔离export核，不写回源。真实native截图/键盘/生命周期运行需其自身命令/OS/exit，不由crossbuild替代。

## Spec覆盖与停线合同

| spec | 落点 |
| --- | --- |
| §1 权威/14agent/依赖/不缩F12 | Global、Task0签交gate、Task5报告 |
| §2.1 metadata/身份/typedCRUD/限额/网络 | Task1真实HTTP/加密/CAS；Task2remote无key |
| §2.2 真实C1/安全视图/无值级diff/false计划 | §0.2、Task3、Task4；非平行协议 |
| §3 导航/全字段/secret联合/dirty/copy/delete | Task1+Task2全部事件与失权/冲突 |
| §4 选择/固定revision/profile/逐slot最终确认 | Task3exactModal+拒绝零slot捷径；Task4跨进程 |
| §5 Unknown/安全历史20/Status/离线Restore | Task4全结果矩阵及真实故障 |
| §6 代际/ctx/实际drain/隐私/关闭 | Task2敏感清理、Task3worker桥、Task5privacy |
| §7 mature原生/主题键盘/200%/实际窗口 | Task2组件、Task5视觉与runner |
| §8 共享8候选/串行写权 | §0.1及E2正式签交后另授；不改core/main |
| §9 真CRUD→exactApply→恢复/负控/独立验收 | Task1–5与门禁命令，未执行不勾选 |

本计划作者只做文档与接口只读校验。E2 finding先修、最终签交+本plan非作者ACCEPT+主明确Go写集三门齐备前不开源码；若发现缺真实接口/额外root或授权/需要依赖/非原生组件才能成立，先停对应实现申请裁定，不改权威spec或缩验收。计划冻结不是运行PASS、源码授权或F12/首发完成。
