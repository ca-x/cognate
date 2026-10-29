# MyGo 原生配置交互：Claude typed 编辑与本机确认闭环

日期：2026-10-29。状态：**待非作者设计审的子规格，未实施**。本轮只授权本文，不授权计划、Go/UI、wire、数据库、依赖或初始化策略改动。以下“必须”均为未来实现与验收要求，不是当前功能或截图验收声明。

## 1. 权威、依赖与非目标

- 完整需求仍是 `docs/REQUIREMENTS.md` F12 与 `docs/CCSWITCH-CONFIGURATION-MATRIX.md` 的 14-agent 矩阵。本文是原生主链路的 Claude provider 垂直切片，不把完整需求改成 Claude-only，也不把已交 Web 编辑或 Codex 声明 planner 当成原生应用交付。
- 消费 `docs/superpowers/plans/2026-10-28-configuration-ui.md` 的保存/应用分离、secret tri-state、未知能力与冲突原则。该总体计划中的候选 DTO 不替代实际 Connect 合同；其 native apply/restore 不能因本文出现而勾完成。
- 原生核心以冻结 C1 tree `feb621b2baf1a4fc5f2b1a6ddc3772df2e03651e`、base `848104bde551781e8a914b6efa9b3420bf7ab903` 为接口事实，来源 `/tmp/cognate-native-C1-final.EGWj4rPg/clean/internal/nativeconfig/flow.go`。**Gauss 完整实现审尚在进行；本规格不替它签收。** 实施先取得通过后的精确核心身份；修订若改变接口须显式复核本文。
- key 准入唯一权威是已签 `2026-10-29-client-key-entrypoints.md` spec，SHA256 `4bf561538e1ddc46a546c7d99652df5f4336cee5ec61e57706b919f9b0806d3a`。`clientkey.LoadExisting(ctx, home)` 与唯一 `InitializeClientKey(ctx)` 服务动作保持原合同；初始化按钮只在账号页“本机设置”。配置页只导航，不增加第二按钮或隐式初始化。
- 同名 entrypoints plan SHA256 `c2746861b7e93d8fc49e826e1c73c72549b99e362d86d380a81e88a38512c68c` 仅作串交分析，**尚未设计接受**。其中候选 CLI19/MyGo13 文件、私有 worker 字段名不是本文的源码授权或锁定接口。
- 不新增远端选中状态 RPC、自动应用授权、后台配置同步、配置 Pack 分享/导入、原始文件编辑器、MCP/prompt/settings/workspace 编辑、代理启动或 agent 登录操作。不引入客户端 JS/moejs、WebView/React、第二套 crypto/网络协议/journal。上述未实现项继续留在 F12，不以灰色空页面宣称实现。

## 2. 现有接口的真实边界

### 2.1 服务端资产与身份

直接消费 `proto/skillhub/configuration/v1/configuration.proto` 的 `ConfigurationService`：`ListAssets/GetAsset/ReadDraft/CreateAsset/UpdateAsset/CopyAsset/DeleteAsset`。资产 metadata 与 `DraftView` 分开，写入使用原 `expected_revision`；复制使用 `expected_source_revision`。`ReadDraft` 返回 secret presence，不返回原密钥；未知扩展只显示 `has_preserved_extras`，不请求解密或用空对象覆盖。

当前编辑分支只接受 `agent_id=claude`、provider kind、schema version 1 和现有 Claude payload。其他已知 agent、未知 agent/kind/schema 可显示有界 metadata 与“不支持此编辑/应用能力”，不能 panic、强转 Claude 或启用通用 JSON 编辑。没有 descriptor RPC 时不伪造服务端 capability 列表；应用阶段仍由 C1 检查实际 adapter/capabilities。

现 `desktop.Service.Login` 只返回 session；用户名不是 owner ID。配置 worker 用同 origin、同 session 调现有 `AuthService.GetSession` 获得 `user_id`，作为私有绑定上下文；失败拒绝，不猜用户名、不由表单编辑 owner、不新增身份 RPC。登录/账号/origin 变化使该绑定失效。所有 CRUD 均由服务端按 session 授权，UI 不能把 metadata 或 GetSession 成功当作后续写权限。原账号/公开目录/Pack/Magpie UI 保留。

资产 Connect 请求沿用现有 Service 的 origin 限制、禁 jar/redirect、ctx与安全错误映射，序列化后请求不超过128KiB、未压缩响应不超过1MiB；字段/数量仍按现有域合同校验，不截断后保存。每个资产/GetSession请求上限20秒，且服从父job取消。现有账号/资产连接可保留明确 loopback HTTP 的原边界，**本机 Apply 必须满足 NativeClient 的严格 HTTPS origin 合同**；不能为了界面统一放宽 native 到 HTTP。NativeClient只由原构造器创建，其transport策略不被通用Service.client覆盖；测试使用真实TLS及合成RootCAs，不通过任意RoundTripper绕过原生限制。

### 2.2 原生核心与预览能力

复用真实 API，不复制其内部步骤：

| C1 API | UI 消费合同 |
| --- | --- |
| `New(home,stateRoot,origin,existingKey,native)` | worker 内，在共享 key 准入完成并释放准入锁后构造。固定现有默认 home/stateRoot，无路径选择器/raw-key 参数；入口副本按 ownership 清理。 |
| `Apply(ctx, ApplyRequest, ownerSession, Interaction)` | request 固定 `OwnerID/AssetID/AssetRevision/ProfileID/Apply`；C1 自己读取当前 selection、递增计数、绑定 Prepare、处理 Authorize/Consume/本机事务。UI 不生成 device、nonce、counter、commitment。 |
| `Interaction.Preview(ctx, SafePlan)` | 已关闭本轮 engine 的安全视图回调，非目标文件全文。 |
| `Interaction.Approve(ctx, SafePlan, requiredSlots)` | 同一次 C1 调用的最终确认桥；仅返回 `Approval{Confirmed, ApprovedSlots}`，不把 plan token 交 UI 重造。 |
| `Restore(ctx,sourceOperationID,apply,Interaction)` | 同一确认桥，但恢复由现有加密 journal 与本机基线验证，不重新下载旧 secret。 |
| `Status(ctx,operationID)` | 本机操作状态，不是远端 bearer 历史查询，也不是无副作用保证。 |
| `Close()` | 取消并等待核心工作结束、清理材料；必须在 worker 中完成后才发布最终结果/释放本地任务占用。不能在事件循环或回调内同步 Close。 |

`SafePlan` 实际提供 operation/kind/agent/target、asset/revision、adapter ID/version/digest、slot/action、required secret slots、readiness、expires、权限收紧、可恢复缺失窗口和 restore source operation；**没有 literal 值、旧值、完整文件、base/new 内容摘要或可复用的 Confirm 句柄**。

Apply 的七个 env slot 固定为 `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MODEL`、`ANTHROPIC_DEFAULT_HAIKU_MODEL`、`ANTHROPIC_DEFAULT_SONNET_MODEL`、`ANTHROPIC_DEFAULT_OPUS_MODEL`。逐行展示 C1 实际给出的动作，不从表单推导或把 `claude.api-key` 等 secret-slot ID 当作 env 键。未知 slot/action 或不完整的安全视图不能被 UI 忽略后继续批准，应安全拒绝并报告能力不匹配。

因此本片的“最终 exact 预览”定义为：**同一次在途 C1 调用的安全 slot/action 计划与身份完整展示，并批准该原计划**。UI 标题使用“本次安全操作预览（非文件全文差异）”，不得标成完整文件 diff。编辑表单里的 endpoint/model 是资产草稿，不可拼进 SafePlan 冒充核心已验证的旧/新值。完整 F12 的值级/文件差异仍是明确缺口；若要交付该能力，须另授最小核心 safe-view 扩展并独立验收，不能在桌面读取原文件、重跑 renderer 或暗加 getter。

`Apply(false)` 可留下加密 Planned 记录，但不 Authorize/Consume/写目标；不能称“磁盘零变化”。其返回 SafePlan 不能用于稍后直接 Confirm。点击“仅预览”后再应用，必须重新开始 `Apply(true)`、展示新计划并重置所有批准。主应用交互直接在一次 `Apply(true)` 的 `Approve` 等待处确认，绝不先 false 预览后悄悄 true 换计划。

## 3. 导航、编辑与保存

### 3.1 原生页面层级

原有页面 key 及行为不重排；追加稳定 key 的“AI 配置”入口。正常宽度以现有列表+详情布局显示，窄窗转为列表→详情→返回，保留同一资产 ID/草稿，不同时挤压表单。固定分区：服务端资产、编辑器、本机操作状态。保存按钮写明“保存到服务端”，本机按钮写明“预览并申请应用到本机”，不使用同一个“应用”按钮涵盖两种副作用。

配置页必须可见以下状态，不只用 toast：未登录/绑定失效、加载中、未选择、草稿未保存、需要凭据、保存冲突、不支持、等待确认、执行中、正在取消并排空、结果未知/需要处理。保持现有 origin 输入；变更立即取消旧配置任务、清理凭据和批准，不自动应用新服务。

Agent 支持说明链接矩阵并明确 Claude 本片范围；其余 13 agent 不显示伪造成功按钮。已保存的非本片资产仍有安全 metadata 展示，不把未知条目丢掉或按 Claude 保存。Copy/Delete 仅在实际域能力允许时调用现有 RPC，不声称能够列出尚无合同的全部依赖。

### 3.2 Claude 结构化表单

- 名称；认证模式三选一：原生登录/API key/Auth token。原生登录只表示不引入新配置凭据，不登录 Claude、不复制 OAuth 文件，也不验证 agent 已登录。
- endpoint 与四个模型字段（default/haiku/sonnet/opus）分别使用“显式设置”复选框+输入框。未设置与空字符串不是同义：关闭表示字段 absent；开启但空值按域校验拒绝，不能 UI trim 成 absent。域仍是最终验证者，客户端不发明宽松 endpoint 规则。
- 两个资产 secret slot `claude.api-key`、`claude.auth-token` 各有 presence 标签与明确 Keep/Replace/Clear 选择；Replace 才启用 Password 输入，空 Replace 拒绝、不变成 Keep。Clear 提示它删除服务端保存的该 slot，不直接清本机 env；首次创建/复制无 secret 时不得显示“已保留密钥”。
- 切换 auth mode 不隐式清空另一个资产 secret，亦不自动批准其使用；七 slot 本机认证切换/删除语义属于已验证 patch/core。readiness 仅凭据完整性，不表示网络可达、凭据有效或 agent 已加载。
- `has_preserved_extras` 显示“存在宿主保留字段，本表单不编辑”；不能提供会丢它们的 raw JSON 转换。默认/未知值必须按 typed presence 保持，不经 map 重建或 string/number 隐式转换。

读列表不调 ReadDraft 全量预取。选择编辑项才读草稿；创建/保存只经真实 Connect CRUD，不打开本机 key/Store/engine，不写 Claude 文件。保存后显式更新 revision 和 secret presence，**不选中本机、不启动预览、不申请 grant**。

CAS 冲突保持非敏感草稿、清空 Replace 明文并恢复需重新选择的安全 secret 状态；固定文案说明“服务端已变化，未覆盖”。提供“重新载入并放弃草稿”与“另存为新资产”显式动作，前者需确认、后者不带旧 secret；不自动读取新 revision 后重试原写入。Copy 明示不复制 secret、设备操作/批准或自动应用资格。Delete 固定 expected revision 并确认“只删服务端资产，不撤销既有本机写入”；域拒绝引用/权限时保持原状态，不擅自解除引用。

普通离开脏编辑器提示放弃；取消保留草稿。账号/origin 变化、退出与关闭属于安全清理，不得等待脏草稿确认。成功、失败、取消、切资产与离开编辑器均清空 Replace 输入及已构建的请求引用；失败保留的非秘密草稿不含替换值。Go string/原生输入法内部拷贝不承诺可靠物理擦除；不得额外持久化、复用为重试载荷或进入调试对象。

## 4. 选择、逐 slot 批准与最终确认

选中列表项只是编辑焦点，不是本机 current。仅**已保存且无脏草稿**的 Claude 资产可发起本机动作；固定当前 asset ID+revision。Profile ID 是明确可见的本机选择维度（初值 `default`，可显式修改），不等于目录；非空、有界且满足核心标识校验，目标仅 `claude.settings/user`。不开放项目 scope、文件路径或后台 AutoApply。更改 profile 使旧计划失效。跨客户端要使用相同 profile ID 才属于相同 selection partition；不能宣称任意 CLI profile 都与该默认值相同。

发起动作前展示 owner（来自 GetSession）、规范 origin、profile、资产 revision；worker 以该快照调用 C1。表单保存后有另一客户端更新资产，应用应在固定 revision 上被拒绝，不跟随最新版。UI 不另写 selection JSON，不把列表焦点冒充持久 selection；C1 是 current selection/counter 的唯一消费入口。本片没有既有 current selection 的独立展示 API，不显示未经核验的“当前已启用 provider”。

### 4.1 同一在途计划的确认桥

1. 用户点击“预览并申请应用到本机”，获得本地任务资格后启动 worker；LoadExisting 成功且准入锁释放后创建 NativeClient/C1，调用 `Apply(true)`。缺 key 只显示“前往账号 → 本机设置”，不在此初始化。Stop Watch 必须由用户显式操作并实际排空，不能自动停/启来抢锁。
2. `Preview` 将深拷贝 SafePlan 投递 UI；`Approve` 发布该调用的确认请求并在 worker 等待回复或 ctx 取消。UI 不能在 Preview 后调用第二次 Apply。准备/预览不是允许消费；未回复前必须实测 Authorize/Consume 与目标写均为零。
3. 成熟原生 Modal 展示 operation ID、资产/版本、origin/owner/profile、逻辑 target、adapter ID/version/digest、七 slot 的 set/remove/reference 动作、所需 secret slot、到期、权限收紧与缺失窗口警告。set 不展示其 literal、reference 不展示凭据。Restore 使用其实际安全视图，不伪造七行旧值。
4. 逐 secret slot 使用独立、默认未选的复选框（“允许本次使用已保存 API key/Auth token”）；仅列 C1 提供的 required slots，顺序按原数组。零 slot 时显示“本次不请求新凭据”，仍须最终显式批准。七 env slot 的逐行动作检查与 secret 取用授权不能混成七个凭据授权。
5. 最终按钮“批准并应用本次计划”仅在全部所需 slot 明确选中、同 generation/operation 有效且未过期时可用；返回 exact 有序 `ApprovedSlots` 与 Confirmed=true。不得全选默认、跨计划沿用、用账号登录当批准或按 Enter 自动勾选。Cancel/Escape/关闭对话框只拒绝，不能成为确认。
6. 确认回调只回复一次。等待中到期立即禁用确认、拒绝并排空；不刷新 TTL/Prepare/后台重试。C1 在回调后继续独立核 expiry/绑定；UI 倒计时不是安全判断。撤销/失权/本地外改/Close 失败均不转成成功。

本机任务总上限取 2 分钟，包含准入、网络、人类确认、执行；确认有效期取该 job deadline 与原 `SafePlan.ExpiresAt` 的较早值。不能直接借当前 generic `Model.job` 的 60 秒 timeout 而不分析确认生命周期，也不能用新 timeout 延长核心 plan。总期限到了仅取消和等待资源收尾，不声称同步系统调用可瞬时中断或已发生副作用自动回滚。

## 5. 状态、冲突、Unknown 与恢复

| 真实结果 | 必须展示与允许动作 |
| --- | --- |
| 预览完成/用户拒绝 | 未申请应用或未批准，不称已写目标；可能有 Planned 记录。新尝试必须新预览/新批准。 |
| 本机 Applied 与可能的 Close 失败 | 只有无错误且已收尾时可报“本机事务已应用”；不声称 Claude 进程已重载。含错误或 NeedsAttention 不套成功文案。 |
| revision/基线冲突、计划过期 | 原计划不可继续；保留外部文件，不自动覆盖/重算/确认。用户处理后显式新预览。 |
| `RemoteUnknown`/本机 `OutcomeUnknown` | 独立展示远端已知状态与本机状态；Consumed 不等于 Applied，远端后来查到状态也不抹掉已发生 unknown。禁止重放 Authorize/Consume或自动“重试应用”。 |
| `NeedsAttention`/BUSY/权限或信任失败 | 固定分类与下一步，不从 error 原文猜安全状态、不移锁/删 journal/修权限。需要处理时先 Status/合法恢复；不能加“忽略并覆盖”。 |
| 取消/超时/Close 错误 | 明示“操作停止或未完整收尾；已发生写入/消费不保证回滚”，不显示“未发生变更”。占用保持到实际 drain。 |

本机状态页只记录本进程已返回/已预览的安全 operation ID 与 SafeResult（有界最近 20 项，离开身份上下文清理），不持久化新设备日志。另提供有界“本机操作 ID”输入，用 C1 `Status` 找原 durable 记录；只接受核心标识字符/128B上限，不作为路径、URL或RPC拼接。不凭内存列表冒充完整历史，也不扫描/解密私有目录来补缺失的枚举 API。跨重启完整历史浏览是明确后续内核接口缺口。

Restore 从已知本机 operation ID 发起，先 `Restore(false)` 可安全预览；真正恢复仍用一次 `Restore(true)` 的新 exact 预览+最终批准，与 Apply 相同的取消桥。source operation 必须可见，基线冲突不得强制恢复。恢复成功不等于改变服务端资产或恢复远端凭据授权。

本机 Status/Restore 可由 C1 以 native=nil/origin空执行，不强迫登录、服务在线或原资产仍存在。独立“本机状态/恢复”入口保留在本机设置可达的配置区域；登出先取消旧任务并清理旧 UI，用户再显式发起本机恢复，不复用旧网络 session。所有实际域认证、原 key/journal 信任、平台/根准入仍执行；“离线可恢复”不是任意 HOME/密钥的授权。

## 6. 异步、敏感数据与关闭合同

- 所有 UI 状态仅在现有 dispatch/window.Update 线程提交。worker、Modal 事件之间用每操作容量 1 的一次性回复通道与 job ctx 连接；事件只非阻塞回复，不等待 worker/Close/HTTP/锁/确认完成。回调不持 publication mutex 等待事件循环。不得为每次取消遗留一个后台等待 goroutine。
- 同一 Model 的本地写、Watch、key 初始化、Apply/Restore/Status 共用 entrypoints 最终接受的 admission/drain 生命周期，不新增平行 local slot。排队/运行/取消待排空都占资格；`Busy=false` 不证明 slot、Session 或 worker 已释放。读服务端资产不持本机安装锁；配置网络/人类确认由 C1 既有 engine Open/Close 分段保证，不扩展锁范围。
- 每 job 捕获 generation、origin、session 私有身份、owner、asset/revision/profile 与确认 operation ID；投递时和实际 dispatch 执行时复核代际/closed/ctx。新 asset/profile/账号/origin 或退出清除 pending approval 并取消，旧成功不得回填新界面。取消并排空前重复点击/直接 Model 方法也不能抢开第二个本地任务。
- `Model.Close`/window OnClosed 仅使生命周期取消并关闭 UI 回复，不同步等待。worker defer 合并 C1 Close 错误、结束 NativeClient、清理 key 与会话引用并释放 admission；沿 `cmd/cognate-desktop/main.go` 当前 App.Run 返回后的 `model.Wait()` 真实 join。若现有 Wait 不能涵盖新 worker，先提出最小接缝，不写“关闭完成”掩盖未 join。不得依赖窗口关闭后仍能 dispatch 来解除 worker 等待。
- 共享 key 只能 LoadExisting，不进入 Model/事件 payload；所有 release 完成后才业务 Open/网络。资产保存不需本机 key；缺 key 不能阻断公开浏览/登录或远端配置编辑。Linux 本机功能按实际支持准入；Darwin/Windows UI 显示“本机配置应用/恢复当前仅支持 Linux”，动作在 key/FS/网络授权前拒绝，cross-build不计runtime支持。
- raw generated DTO、请求正文、Replace 输入、owner session、core 私有 Result/Flow/错误链都不传入日志或 UI 字符串格式化。`debug_redact` 不保证 Go String 安全。错误只按可信 sentinel/code映射固定文案，拒绝原始 parser/Connect/transport error、URL/body/stack。嵌套 Model 私有 payload 也要通过非常规 fmt 动词/容器/slog/JSON 反例，不能仅凭 Stringer/私有字段宣称安全。
- UI只接安全不可变视图和明确私有编辑缓冲；切身份/完成/取消要清理 Replace、密码、批准、会话引用与旧结果。输入密码默认遮罩，不新增复制/显示秘密按钮、剪贴板、自动保存或截图调试秘密的路径。安全 metadata 也做长度/控制字符处理，只用于 UI，不把资产名/origin/任意字段打进常规日志。

## 7. 真实 MyGo 组件与视觉/键盘标准

基于当前 `github.com/egoist/mygo v0.7.1` 本地 pinned API，非 Web 替代品：`ui.Column/Row/Scroll/Text`、`List`+`ListState.Key/Label/Selected`、`TextInput(...).Label().Password()`、`Select(c,*string,[]string)`、`Checkbox`、`Button/PrimaryButton.Disabled().Clicked()`、`Modal`/`AlertDialog`。使用 `context.Key` 绑定稳定 asset/slot ID；不按列表索引绑定凭据。焦点采用现有 `Focus/FocusRing/Focused/OnShortcut`，不手写 overlay/focus-trap、动画库或未知 accessibility API。

设计应用 ui-ux-pro-max/emil/apple/animate：沿现有标题/系统字体/品牌、20DIP外边距和8/12DIP组距建立列表→分区→正文层级；单一主操作，危险动作与保存分开。light/dark 使用现有主题语义，不硬编码灰字；普通正文目标对比度4.5:1。920×760与640×600验可见性，200%缩放采用单列滚动，不裁掉固定取消/结果区；详情返回保留列表焦点。状态不只依赖颜色/小图标。中英固定文案同义，不新增独立偏好存储；沿既有偏好机制，若 native 尚无相应机制需明确依赖而非从 F11 删掉。

Modal 打开焦点落在安全说明/取消方向，不把危险确认设为默认 Enter；Tab/Shift-Tab 包含在 Modal 内，Escape 拒绝，关闭回到发起按钮（身份失效则回到安全入口）。Password、presence开关与checkbox均有独立标签；切模式不挪焦点到隐藏字段。列表与日常键盘操作不加动画；不自造 spring、等待时淡出确认或以动画延迟禁用。沿成熟组件的默认反馈，减少动态偏好下没有新持续运动。辅助技术支持需目标平台实测，不能以 CSS/ARIA 或 headless 图像替代 native accessibility 结论。

## 8. 窄写集提案与串交 gate（不是源码授权）

推荐先复用同一 `desktop.Service`，新增窄配置后端能力接口而非复制 Backend/worker 架构；旧 Backend 未实现时明确 unsupported，不能退回绕过 core 的实现。资产方法消费 typed draft/secret mutation 的私有缓冲；本机方法仅代理 C1 Apply/Restore/Status 与 Interaction，不能公开 Confirm、material 或原生文件内容。GetSession 在真实服务内绑定身份，不必改变 Login wire 或把 owner 猜入现有字符串返回值。

候选最小 8 Go 路径：`internal/desktop/model.go`、`view.go`（仅导航/生命周期接入）；新增 `configuration_model.go`、`configuration_service.go`、`configuration_view.go` 及对应 `configuration_model_test.go`、`configuration_service_test.go`、`configuration_view_test.go`。新方法可属于既有 Service，无需先改共享 service.go。现 main Close/Wait 结构优先保持；若真实证据要求改 service.go/main/旧 test fake 或内核接口，须先报额外写集，不偷扩。本文不预授这8路径，也不保证精确路径数能替代正确生命周期设计。

实施串交次序：①C1 非作者签交精确接口；②key entrypoints 计划获批、源码独立验收并串入共享 Model/View/Service/admission；③同一 owner在最终基线上落实本片接入，再独立审。共享生命周期文件不得同时由两个 owner 写；可以先只读设计，不借 plan 候选私有字段另建 pendingJobs/localWorkBlocked 克隆。不得为了合入 UI 复活 key 自建策略或假定 native C1 已签。上述 gate 未闭时仍可审本文，但不能授源码落地或宣称配置可应用。

## 9. 验收合同与负控

全部仅合成 owner/session/key/provider，TempDir HOME/state，真实隔离 SQLite 与服务、真实 TLS Connect、真实核心和配置文件；不用操作者 HOME/账号，不执行 upstream。Go1.27.3/CGO_ENABLED=0，源码实施先运行 modern-Go 完整 list 并遵守同步/性能 skill；不以CGO1/race、交叉编译、mock调用顺序或零匹配测试冒充行为通过。以下是未来门禁，**本轮均未执行**。

1. **真实事件→保存：** `ui.NewTester` 的 `Click/Key/Type` 触发实际 Model→Service→HTTP CRUD，核 presence/Keep/Replace/Clear、copy无secret、revision冲突/失权保全；初 render/切页/保存/登录不触发 key/Prepare/Authorize/Consume或目标写。明确请求/目标/加密记录的独立计数，不只mock“被调用”。未知 agent/schema列表不崩且不能编辑/应用。
2. **真实 exact确认：** 同一在途 Apply(true) 从 authenticated GetSession 到 Prepare→安全预览→逐slot→最终确认→至多一次Consume→本机 Applied；对话框未回复时独立HTTP/FS计数为零Authorize/Consume/目标写，原安装锁可被子进程取得。零slot仍需最终确认。API key→auth token→native-login三转换真JSON验证七slot、未知大整数/object env保留。表单保存与本机选择没有隐式耦合。
3. **旧计划与冲突：** 仅预览后再应用必须另一个operation并重新批准；确认中远端revision/撤权、目标外改、过期/失效profile拒绝不覆盖。跨进程本机操作证明partition/counter/CAS由核心保持；UI不以列表当前项规避核心。Status/Restore针对原operation，恢复冲突保全文件；离线登出仍可合法本机恢复，不请求原secret。
4. **Unknown与故障：** 真受控丢response/授权或消费不确定、FS故障/Close失效，UI不报成功、不重放、不把Consumed当Applied；展示可用safe operation ID与分类，Status不变造grant历史。把核心已验故障overlay连接真实Service，不引产品测试后门；错误树包含合成secret/URL也不可进入屏幕/日志。保留invalidsetup，不把编译错误或整体超时当RED。
5. **异步/关闭：** 真子进程占安装锁、TLS barrier、等待人类确认分别下点击Cancel/换资产/换origin/logout/关闭；UI仍响应Tab/取消，回复桥被ctx解除，实际worker/FD/Close/Wait收尾，取消未drain时重复本地操作被拒。barrier精确制造旧generation成功、Close错误晚回填，不能污染新身份。测试必须检查dispatch执行时gate，不只发布时gate。
6. **视觉与隐私：** headless light/dark×宽/窄+200%缩放截图与实际键盘路径（不是只HasText），焦点环/Modal containment/危险Enter/Escape/取消可达。随后目标OS真实MyGo窗口截图与事件证据，至少实际支持Linux走真实mock-free服务闭环；只headless不能签视觉/原生window。截图仅合成、秘密遮罩；日志/stdlog+slog/fmt非常规动词、slice/map/私有容器/JSON捕获不含secret、session、proof、原文件/错误原文。三OS功能runtime与compile-only分别统计，非Linux实际显示限制且零副作用。
7. **回归与范围：** 账号初始化只有唯一按钮且离线可达，公开目录/原生Pack/Watch/Magpie路径不回归；CLI/server客户端依赖边界不引moejs，native无新wire/DB/dep。完整F12缺口逐项仍在，不签C2/Codex安装、多agent完整配置、平台安装或首发完成。

至少六个独立编译成功后的行为负控：保存后偷调Apply；false预览后复用批准到新计划；跳过单slot/最终确认；取消提前释放本地占用或删dispatch代际检查；吞Close错误/Unknown自动重放；把rawDTO/错误或私有payload格式化输出。每项保存mutant patch/hash/命中selector/命令/exit及有限时真实cleanup，断言RED后恢复exact source GREEN。真实网络/锁/文件oracle与生命周期受控witness分别说明，不把二者混称全真实磁盘故障。非作者最终Spec/Quality与native runtime证据均是交付前条件；本文冻结仅供设计验收。
