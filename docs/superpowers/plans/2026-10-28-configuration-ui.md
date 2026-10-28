# Cognate AI Configuration Web Implementation Plan

> **For agentic workers:** API 冻结后使用 executing-plans 按任务实施；本文件仅为设计与合同需求，不授权修改生产文件、不 stage/commit/push，不代表配置功能已交付。步骤使用 checkbox 跟踪。

> **2026-10-29 checkpoint：** 上述开工限制和下方“API 尚未冻结”为原设计基线，不是当前接口状态。Claude typed domain/Connect API 已随 `8a7eebe` 提交；主线程随后单独授权 Claude provider 私有资产编辑首片，六个 Web 文件及 embed 路由已按 tree `569372e` 独立整合签收、提交并 SSH 推送 `f659706`。保存成功与导航阻塞 resolver 并发的旧候选曾被拒签，新树经 scoped 浏览器复验及最终运行时字节身份核验关闭问题，证据见 `VERIFICATION.md`。其它十四类 agent、选择、Pack 附件、原生预览/应用/恢复仍按本计划目标推进；本首片不缩减范围。

**Goal:** 基于 cc-switch 的实际能力，为配置资产提供可验证的专属编辑、显式选择、差异预览及每设备应用/恢复交互，而非单个 raw JSON 编辑器。

**Architecture:** 服务端保存加密资产并通过版本化、类型化 Connect DTO 提供经过脱敏的编辑视图；来源 importer 与目标 moejs adapter 都仅在服务端执行。资产、期望选择、导入候选、设备应用记录为不同资源；Web 不探测或写入用户机器文件，不把保存或选择冒充应用。

**Tech Stack:** 现有 React、TypeScript、Connect/Buf、TanStack Query、react-i18next、Tailwind 与现有 Button/Input/Select/Checkbox/Dialog；不预先增加依赖。

**Spec:** `docs/REQUIREMENTS.md` F6/F10/F11/F12；`docs/CCSWITCH-CONFIGURATION-MATRIX.md`；`docs/superpowers/specs/2026-10-28-configuration-assets.md`；`docs/AGENT-ADAPTERS.md`。

## Global Constraints

- 2026-10-28 设计基线：cc-switch `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`，实际注册 14 agents。矩阵为上游事实，不是 Cognate 已支持清单。
- 本轮唯一写集为本文件；原 MCP UI checkpoint 不变。AI 配置 API 尚未冻结，下面 DTO/RPC 名称仅表达需求，不是新增 proto 或后端实现承诺。
- 不以内部 encrypted envelope 推导公开 opaque payload API。普通列表/Get 仍仅元数据；编辑需独立、owner-scoped、只含安全字段与 secret presence 的类型化视图。
- 资产不可变 owner/agent/kind；名称 trim 后 1–128 UTF-8 bytes，无控制字符；schema version 1–65535；每 owner 最多 250 项；内部 envelope 1–256 KiB。HTTP/DTO/列表/编辑的更细限额由后续合同冻结，不把 envelope 上限当文件上传上限。
- 类型固定为 provider、mcp、prompt、agent-settings、workspace。所有写入 CAS；未知 agent、能力、schema、操作 fail closed，不能自动转换为通用 JSON 写入。
- wire `skillhub.*`、浏览器 `skillhub.theme`/`skillhub.locale` 兼容 key 保持不变；用户可见品牌使用 Cognate。不得从配置 DTO 推导新的 crypto 域或改变已有持久标记。
- 只持久化已有非敏感偏好；token、表单、秘密、原始上传、设备配置和 diff 不进入 local/session storage、IndexedDB、URL、日志或错误消息。
- 服务器不可指定任意本机路径。Web 不执行 moejs/SQL、agent 命令、MCP command、npm 字符串或 plugin 代码，也不直接探测任意 endpoint。
- 不因登录、上传预览、保存、复制、改序、选中或 Pack 安装而自动应用；Web 与 MyGo 的实际执行能力分别验证。

## Review Focus

- 同一 agent 多设备状态不同：期望选择更新不能把另一设备标成 applied；过期回执、离线与部分失败必须明确显示。
- 秘密 keep/replace/clear、复制及原始编辑：占位符绝不能覆盖原值，普通响应、查询缓存与工具提示不出现秘密。
- 独占/共存/special 混淆：Kimi 原生默认、MiniMax 引用保护、agy 会话启动与 OpenClaw primary/fallback 都不能退化为单个 current ID。
- 编辑与外部修改并发：远端 revision、本地基线或 adapter digest 变化后拒绝旧计划；未识别字段不能静默丢失。
- 未完整实现的 agent/格式：可说明能力缺口，但不能出现能点击的伪应用、自动 fallback 或“完整迁移成功”。

## 1. 页面结构与组件

### 1.1 导航与主工作区

- 新的业务页入口“AI 配置”，与“仓库来源导入”和 F10“Cognate MCP 访问令牌”并列，不替换已有入口。
- 页首：agent Select、按能力可用的业务分区、服务端能力/adapter 版本说明。“上游支持”“服务端可编辑”“可渲染”“设备可应用”分开，不由一个 enabled 布尔值代表。
- 桌面采用列表/详情双栏；移动端先列表、点开详情并有返回按钮。按钮保留文字，不只用图标。没有新 tab 依赖：少量分区用原生导航按钮/链接加 `aria-current`，不伪造不完整 ARIA tabs。
- 一级分区：Providers、MCP connections、Prompts/Memory、Agent settings、Workspace；只显示该 agent 的适用分区。导入与应用记录为独立入口，OpenClaw 拆分见第 5 节。
- 页首状态只陈述当前范围：如“已保存 5 项 · 期望启用 2 项 · 本设备状态未读取”。不能将来源 `source_enabled` 或资产存在性当作设备状态。
- 直接访问不支持分区时给固定分类错误与返回入口，不渲染一个假的空成功页面。无配置的空状态允许新增/授权导入；只有功能真正接通才挂生产路由。

### 1.2 Provider 列表

- 每项显示名称、agent/provider 类型、协议与经校验的非敏感摘要、模型数量、revision、资产状态、期望选择及选定设备的应用状态。endpoint 若包含用户信息/敏感 query 必须服务端拒绝或安全投影，不能直接展示原输入。
- 明确的操作：新增、编辑、复制、删除、上移/下移；独占的“设为期望 provider”、共存的“加入/移出期望集合”、special 的专用操作，不统一叫“启用”。
- 列表排序是资产展示顺序；共存成员顺序、模型 fallback 顺序、failover 队列分别属于独立资源。不用一个 sort 字段复用四种含义。
- 排序采用有文字标签的上移/下移 Button，键盘可操作、边界禁用、变更后保留焦点并播报新位置；不依赖 drag。排序 RPC/CAS 尚未定义，不能在浏览器保存排序冒充服务器已保存。
- 复制 Dialog 要求新名称并列出不复制内容：secret、账号/OAuth 会话、设备应用记录、期望选择、自动应用授权。复制默认无 secret，保存后可能处于“需要凭据”的不可应用状态。
- 官方账号卡/原生受保护节点由能力返回的 operation policy 禁止不合法复制/删除；当前/默认/Pack 引用项删除先列依赖。不得偷偷清空引用；引用检查未接通就不开放删除。

### 1.3 专属编辑与原始编辑

- 复用 Input/Select/Checkbox，fieldset+legend 区分 Provider 私有、Agent 全局、模型、凭据。不新增通用 JSON 组件作为主表单。
- 模型目录是结构化行编辑，模型 ID、显示名、限制、reasoning、input types、variants/cost/options 按 agent 展示；默认引用必须落在有效模型集合。
- 多行文本先用带 label 的原生 textarea；仅需长文本编辑时再评估现有依赖，不先引入代码编辑器。
- 高级原始编辑是显式第二模式：由服务端提供允许的 agent/slot/format 编辑视图与验证，保留未识别字段和字节保真策略。不能把 TOML、JSONC、ENV、YAML 都包装为 `raw_json`，也不能把整份含秘密的原生文件默认为可读。
- 结构化/原始模式切换必须先校验并说明保留/不可表达字段；解析失败保留内存草稿并阻止保存。契约未提供无损转换时不提供切换按钮，不能静默重建配置。
- 保存前执行领域验证，字段级错误只用固定 code+field path+安全参数，不回显提交的秘密、无界 parser 消息或上游响应。
- 离开脏草稿、切 agent/资产有确认；取消保留当前草稿。locale/theme 切换不重置组件 key 或草稿；账号切换/unmount 无条件清理，不以脏草稿对话框阻塞安全清理。

## 2. 14-agent 显式交互分派

下表是需要 API/fixture 支持的目标，不声称当前生产可用。列表由服务端 capability 描述，Web 将已知 editor kind 映射到受审专属组件；未知 editor kind 不加载任意脚本或 schema 指令。

| Agent ID | Provider 选择模式与专属页面需求 |
| --- | --- |
| `claude` | 独占；key/auth-token 类型、endpoint、模型组与兼容开关。provider env 与 agent-global permissions/hooks/plugins 分开；MCP 另属 slot。当前 additive-env adapter 不等于切换/删除旧认证已完成。 |
| `claude-desktop` | 独占官方/第三方卡与专属模型映射；不显示 Desktop 原生 MCP/独立 prompt 库。Stack 门控与实际可用性单独验收。 |
| `codex` | 独占；官方账号卡与 API provider 分开，模型目录/reasoning/default、原生 responses 协议；TOML 与认证投影分离，不导出账号登录凭据。 |
| `gemini` | 独占；OAuth/API 模式、endpoint/model；ENV 与 JSON settings 分 slot，认证模式变更有独立计划，不把 OAuth 会话复制为配置资产。 |
| `grokbuild` | 独占；profile/upstream model 与模型表/default 引用；TOML 保留非托管条目及注释，不删除用户后来选择的任意模型表。 |
| `opencode` | 共存；npm/API 类型、headers/options、模型 map/variants/cost。模型在原生工具选择；OMO/OMO-slim 是单独专属设置，不冒充路由或普通 provider。 |
| `openclaw` | 共存；provider 集合不等于 primary/fallback；七个专属面板见第 5 节，不显示 MCP 支持或伪 Skills 能力。 |
| `hermes` | 共存；内置 key 与自定义 key 分支、models/rate limit；Memory 与原生 SOUL.md 语义，不强套通用 prompt 文件名。 |
| `pi` | 共存；Pi API/provider/models，Enable/Remove 与 SYSTEM.md/prompt templates 为单独操作。 |
| `mcode` | 共存；仅允许的 custom 节点；默认引用项删除/禁用保护。可复用表单字段，不复用错误的原生格式投影。 |
| `kimi` | 共存加原生 default_model 特例；单独显示原生默认引用与 OAuth 节点保护，不标唯一“当前 provider”。 |
| `dsh` | 共存且是独立 agent；支持 apiKeyEnv。仅受管 web profile YAML override，禁 tags/anchors/aliases，项外字节保留。 |
| `agy` | special；官方卡是当次启动 `--model`，第三方经私有目录启动。表单可复用 Gemini 字段，不写成 Gemini settings 或持久 current；MCP 与 AGENTS.md 映射独立。 |
| `omp` | 共存；Pi-family 字段与 discovery；models YAML 与 config YAML 默认引用分开，保留项外字节。旧 models.json 迁移门控未通过不得抢建新 YAML。 |

路由/代理/failover/Stack、Desktop mapping、账号启动与 projects 不能靠保存参数完成。主线程需确认完整首发范围及每项 owner；凡矩阵要求而缺少运行时 API/执行证据者列 release gap，不静默取消需求。

## 3. 凭据与 secret 三态

- 普通列表、Get metadata、安全编辑视图只提供 `SecretFieldState`：稳定受控 field ID、presence（present/absent/unknown）、required、允许的变更，不返回 secret 内容、前后缀、hash 或可猜测预览。
- 更新用显式 `SecretChange` oneof：`keep`、`replace(value)`、`clear`。不允许空字符串同时表示保持/清空；未发送变更的语义须合同写明，UI 初始化为 keep。
- Keep 显示“保留已保存凭据”，不把 `********` 放入可提交值。Replace 才挂载新的 password Input、禁 autocomplete/spellcheck；Clear 要单独确认并验证必填/其他认证引用，不用输入框清空动作隐式删除。
- 新建无原 secret；只能选择受支持的无凭据模式或显式 replace。Copy 返回新资产缺凭据状态，不能返回原秘密再让前端删除。若未来支持显式复制凭据，必须独立授权/确认及审计，不包含在本默认合同。
- headers、env、MCP args 等也可能含秘密，不能只给 apiKey 做 secret 保护。服务端 typed schema 标记敏感字段/引用；导入预览对无法安全分类字段不返回原文。
- 原始编辑不可旁路三态。secret 在原始视图使用不可提交的受控引用；更改凭据仍由独立 SecretChange。若无法安全映射就拒绝该原始编辑能力。
- 不设计默认“显示已保存秘密”。确需 owner reveal/渲染给已授权设备时另签合同，不能借安全编辑视图自动实现。
- replacement 值仅在组件内存；不进入 React Query query/mutation cache、错误对象、devtools metadata、console、URL 或持久化草稿。使用组件内受控 promise 执行敏感提交，保持脱敏元数据查询。
- 成功/取消/关闭/切资产/账号/unmount 清空敏感状态并取消请求；失败重试策略须明确，默认清 replacement 后要求重新输入。JS 字符串无法保证物理内存归零，不能对用户宣称可实现。

## 4. 配置生命周期：保存、选择、应用、恢复

| 资源/动作 | 可以显示的状态 | 不允许推断 |
| --- | --- | --- |
| 导入候选 | 未选、已选、校验失败、已保存资产 | 预览就已应用或已迁移本地内容 |
| 资产 CRUD | 草稿、保存中、已保存 revision、CAS 冲突、结果待确认 | 已启用/已安装/本机文件已改 |
| ConfigSelection | 未选择、期望独占项/共存成员/special 选择、selection revision | 某设备已切换；共存只有一个 current |
| ApplyPlan | 待授权读取、已预览、过期、冲突、明确确认 | 服务器渲染成功就本地应用成功 |
| DeviceApplyRecord | 未上报、待应用、进行中、成功、失败、部分成功、unknown、外部漂移 | 离线/无回执就是成功或自动重试安全 |
| Restore | 可恢复、基线冲突、恢复中、成功、部分失败/unknown | 删除服务端资产自动恢复所有设备 |

- Web 应用面板先选具名设备及实际可用执行通道。普通浏览器无本机文件/原生通道时明确“请在原生客户端预览并应用”，不提供伪执行按钮。
- 来源文件、来源已启用标记与服务器 desired selection 分开；导入选择初始为零，批量保存也不选择/应用。repository preview 旧 RPC 不承载配置候选。
- 预览计划绑定资产 IDs/revisions、selection revision、adapter ID/version/digest、schema/capability 版本、device ID、逻辑 slots、基线及结果摘要、到期时间与 plan digest。
- diff 默认脱敏，明确新增/替换/删除/保留/未知字段冲突及认证切换影响；读取/上传本地片段需单独授权，不自动上传完整配置。
- 确认只作用该计划；本地摘要、远端 revision 或版本变化使旧计划失效。原生客户端再次校验、备份/journal、写入并上报逐 slot 结果；取消等待不等于撤销已写入。
- 对在途取消或断线显示 unknown，禁止自动重试写。读状态/重新预览后再决定；已成功项不整批回滚，失败恢复有明确入口与安全基线。
- 恢复只处理自身托管变更，保留未知/外部更新。服务端回执仅为脱敏报告，不替代真实文件验收，也不把回执当授权凭证。
- Pack 附件显示为显式可选资产引用+revision，配置消费授权与默认公开/Pack 分享分离；引用删除保护先实现，旧客户端能力不足时 fail closed。

## 5. 独立面板

### 5.1 Agent MCP connections（不属于 F10 token 管理）

- 独立列表/新建/编辑/复制/删除与逐 agent 期望启用矩阵；编辑为 transport oneof：stdio(command,args,env)、HTTP(url,headers) 等实际支持项，敏感字段用 SecretChange。
- command/args 只是配置数据，Web/服务端预览不得执行；如后续健康检查需要启动本地进程，必须独立客户端能力与授权。
- 按矩阵排除 `claude-desktop`/`openclaw`；Codex/Grok 的 headers/type 等原生映射由 adapter 负责，不复制原始配置互用。
- “导入已有”“重新同步”“批量期望开关”均独立确认，显示逐 agent 结果；未知本地 server 条目保留，不以整文件覆盖实现关停。
- Cognate 自身 MCP token 不进入 connection picker、导出、复制或资产 secret 引用。既有七项管理 scope 不自动授权配置资产管理；配置授权模型待后端冻结。

### 5.2 Prompts / Memory

- prompt 列表、新建、Markdown 编辑、复制内容/跨 agent 复制、删除与期望启用分别提供操作；复制内容是用户明确操作，不自动写剪贴板。
- target 文档由 capability/受控 slot 指定，展示路径仅作客户端解析后的非敏感说明，不能让服务器提供任意路径。
- Pi 的 prompt templates/SYSTEM.md、Hermes Memory/SOUL.md 采用专属编辑分派；不能把 OpenClaw workspace 全归成一个 prompt。
- 读取当前文件、覆盖与备份各需授权；Markdown 预览不执行 HTML/脚本/远程嵌入。未经安全 renderer 验证先用文本预览，不新增未审核依赖。

### 5.3 OpenClaw 专属工作区

| 面板 | 结构化交互与关键边界 |
| --- | --- |
| Providers | 协议、endpoint、headers/User-Agent、秘密、模型 CRUD；加入/移出不改变默认模型。 |
| Agents defaults | primary model 引用、可键盘排序的 fallbacks、workspace 逻辑目标、timeout/contextTokens/maxConcurrent；未知字段保留。 |
| Model catalog | 独立 catalog/allowlist 条目，不能与 provider 模型集合复用同一 resource ID。 |
| Env | 嵌套 env.vars/env.shellEnv，受控结构化树/高级该节编辑，逐秘密 keep/replace/clear；不回显已有 secret。 |
| Tools | profile minimal/coding/messaging/full 与 allow/deny 列表；未知 profile 显示“保留原值”，仅在明确替换时改变。 |
| Workspace | AGENTS.md、SOUL.md、USER.md、IDENTITY.md、TOOLS.md、MEMORY.md、HEARTBEAT.md、BOOTSTRAP.md、BOOT.md；存在/缺失、Markdown 草稿，保存才创建；slot 白名单而非任意路径。 |
| Daily memory | 日期列表/预览、搜索、今天新建、编辑、删除确认；日期/时区合同、隐私、备份保留/恢复/硬删除需独立冻结，未定不能开放破坏性操作。 |

OpenClaw 原生模型 fallbacks 不等于 CCS runtime failover。没有 API 支持的面板只进入本计划/差距清单，不上线一组空页面冒充已交付。

## 6. 类型化 Connect DTO 需求（待冻结）

以下为语义边界而非 wire message 定稿；版本化增量 proto，不复用现有标签。金额/大整数、revision、时间使用明确 protobuf 类型/单位，不经不安全 JS number 转换。稳定 ID 不受品牌重命名影响。

| DTO 概念 | 必要字段/oneof/验证语义 |
| --- | --- |
| AgentDescriptor | agent_id、display_name、source evidence/version、adapter ID/version/digest、capability/schema version、provider_mode(exclusive/coexist/special)、kinds、已知 editor_kind、logical slots/formats、supported operations、设备必需能力。服务端注册能力与原生客户端声明取交集。 |
| OperationPolicy | operation enum、allowed、安全 reason code、受保护原生节点/当前引用策略；不能只用可见按钮推断后端权限。每个写 RPC仍必须服务器授权。 |
| ConfigurationAssetMetadata | 稳定 ID、agent/kind/name、revision/schema version、created/updated；不返回 payload、密文、payload hash 或 secret preview。UI装饰性的模型数等应是另签的安全摘要，不偷偷扩展内部 Get。 |
| EditableAssetView | metadata + payload oneof(provider/mcp/prompt/agent_settings/workspace) + secret presence + validation/capability 信息；独立 owner-scoped read，永不默认包含已保存秘密。 |
| ProviderSpec | agent-specific oneof（14 agents 允许字段分支，复用 typed 子消息而非共用整文件）；协议、endpoint、安全 options、认证 kind/credential refs、typed model entries/default refs。不同 agent 的限制单独验证。 |
| ModelSpec | ID/name、context/output/maxTokens 等具单位字段、reasoning/input types、typed variants/cost/options；agent 不支持字段必须有受控诊断，不静默舍弃。 |
| AgentSettingsSpec | agent-specific oneof；OpenClaw defaults/model catalog/env/tools 与 provider 分离，Claude 全局设置等分域；引用用 IDs 而非裸 JSON 指针。 |
| McpSpec | 连接 ID/名称、transport oneof、command/args 或 url、env/headers 的 SecretField 结构；agent support 矩阵和启用选择另建，不混 F10 Credential。 |
| PromptSpec / WorkspaceSpec | 内容、受控文档 slot、scope/template 类型或日期文档类型、存在状态/内容 revision；不接受绝对路径、任意文件名拼接或命令。 |
| SecretFieldState / SecretChange | presence enum + required/可操作；更新为显式 keep/replace/clear oneof，多个字段/嵌套 env/headers 支持稳定 field ID。不以 `map<string,string>` 代替 secret 与领域类型。 |
| PreservedExtensions | 服务端持有的保留句柄/版本和安全未知字段诊断；必要的高级片段有 explicit format/slot/size/parse contract。保留未知字段不授权未知行为，不执行任意 schema/script。 |
| ConfigSelection | 独立 revision、target agent、exclusive asset ref / coexist ordered members / special selection oneof；Kimi default、OpenClaw primary/fallback、agy session intent 不能挤进单个 current_provider。资产引用固定 ID+revision。 |
| DraftValidation | field paths、安全 error/warning code、缺失凭据、引用/能力检查；不返回原 parser 报错或完整提交值。 |
| ConfigImportPreview | 授权 source format/version + snapshot digest、bounded opaque candidate IDs、typed safe summaries、secret presence、unknown/unsupported diagnostics、expiry；provider/MCP/prompt/settings/workspace 与 repository candidates 分开。原文不进响应/URL。 |
| CommitConfigImport | preview pin/digest、明确 candidate ID+顺序、领域范围和 secret 授权，逐项保存结果；不能顺便设置选择、分享或写本机。 |
| RawEditorView / RawEditValidation | 明确 format（JSON/JSONC/TOML/ENV/YAML/Markdown）、logical slot、脱敏内容/保留句柄、基线/版本、无损保证及不可表达字段；并非资产通用 raw_json。 |
| ApplyPlan | 固定资产/selection/adapter/schema/device 版本，logical slot 变更、base/result digest、脱敏 diff、所需授权、plan digest/expiry；敏感渲染内容仅给获授权执行消费方，不默认返回浏览器。 |
| DeviceApplyRecord / RestorePlan | device scope、plan/revision IDs、逐项 outcome、safe failure code、journal/backup opaque refs、恢复资格/基线、脱敏时间；不含真实文件内容、凭据或任意 server path。 |

### 6.1 操作分组与 API 冻结问题

- 能力读：ListAgentCapabilities/GetEditorCapabilities；必须区分可导入、可安全编辑、可渲染、可设备应用和仅上游存在，不把注册 agent 当可写。
- 元数据 CRUD 与安全编辑：List/Get metadata、GetEditableView、Validate/Create/Update/Delete；agent/kind 不可修改，expected revision/分页/容量/鉴权均需冻结。
- Copy 与 Reorder：专用操作（或后端认可的等价事务合同）；copy secret 默认排除，排序须作用明确 scope 并带集合 revision，不给 UI 仅靠 N 次 Update 冒充原子重排。
- Selection：Get/Set CAS，exclusive/coexist/special typed oneof；是否 per user/per device desired 由主线程冻结，实际应用始终 per device。
- Import：另建 preview/commit 合同，最大 raw/wire bytes、TTL、source schema 与 secret 选取策略须冻结；沿用旧仓库 preview 的字节数/候选字段不可推定适用。
- Render/Apply/Restore：Web 只调用可用授权通道；计划失效、消费权限、设备登记/报告真实性、备份生命周期及结果查询合同需先定，不能把 Report 当本机执行。
- 网络 TestEndpoint/DiscoverModels 不属于保存动作；需另冻结凭据用途、SSRF/域/地址限制、超时/响应上限与用户授权，默认不自动请求。
- 配置独立 scopes/会话权限、设备消费/Pack 附件授权未冻结；不能默认借用 catalog:write 或 publication:write，也不在此计划新造已生效 scope 字符串。
- 路由/failover/聚合/官方账号启动等 runtime API 不在资产 CRUD 合同内；若首发要求完整交互，必须由对应 owner 冻结真实执行/恢复合同。

## 7. i18n、主题、窄屏与无障碍

- 新文案用独立 configuration namespace（具体文件/API冻结后批准），中英完整覆盖标题/空状态/错误/secret 三态/逐设备状态/确认/ARIA。既有 MCP namespace 和已完成七项 scope UI 不顺带重构。
- asset/model 用户内容不自动翻译；错误用稳定 code 映射，不显示英文 parser 文本或 secret。数字、日期、时区按 locale；IDs/协议保持原样。
- 复用 bg-card、border-border、text-foreground、text-muted-foreground、destructive 等语义 token，不硬编码白底/灰字。不靠颜色区分成功/冲突/unknown；跟随系统主题更新但不重置表单。
- 390px 为最低真实浏览器检查点：单列、按钮可换行、长模型 ID/endpoint/code break-all，fieldset/min-w-0、无页面横向溢出；必要原始文本编辑局部滚动，diff 可切单栏。
- 既有控件至少 min-h-11；Checkbox 可 Space 操作；所有字段有 label、fieldset/legend、aria-describedby、错误后聚焦首字段。不能通过只读禁用造成说明完全不可访问。
- Dialog 有标题/说明/焦点恢复；删除/clear/覆盖用显式确认，不用浏览器 alert 取代成熟组件。保存/执行期间互锁，支持停止等待并说明已提交操作不回滚。
- 列表重排、选择变化与逐项处理用节制的 live region；不播报 secrets 或整份 diff。尊重 reduced-motion，不为本任务加入动画依赖。

## 8. API 冻结后的实施任务与验证门槛

以下路径为后续申请写集的建议，不是本轮授权；按现有 router/API 结构最终定名。没有一个任务允许先上线无后端的 placeholder 页面。

### Task 1: 冻结合同与能力显示

**Files:** 后续建议 `web/src/configuration.tsx`、独立 `web/src/configuration-i18n.ts`，以及经批准的 route/API 接线；proto/生成物由主线程/backend owner 负责。
**Consumes:** 冻结的 AgentDescriptor/OperationPolicy、元数据 list API。
**Produces:** 真实 capabilities 驱动的列表/错误/空状态，不开放未实现编辑/应用。
- [ ] 用合成响应固定 14-agent 模式、未知 agent/editor/version fail closed 与缺少执行能力的行为测试。
- [ ] 实现列表与中英/主题/390px 布局；未支持项有可追溯诊断，不把它们计为已完成。
- [ ] 真浏览器验证键盘、focus、横向溢出、切账号取消与缓存隔离。

### Task 2: Provider CRUD、复制、排序与 secret 三态

**Files:** 后续建议 `web/src/configuration-providers.tsx`、`web/src/configuration-provider-editors.tsx`，及上述 i18n 文件；按字段复杂度经 review 拆专属组件，不建万能 JSON 表单。
**Consumes:** typed EditableAssetView、Validate/CRUD/Copy/Reorder/CAS，secret 三态与引用保护合同。
**Produces:** 每个已接通 agent 的真实保存/复制/排序，不影响 selection/apply。
- [ ] 先锁定行为：keep 不提交占位符、replace/clear、复制不带 secret、已有引用不能删、CAS 冲突不覆盖、排序失败/unknown 不宣称已保存。
- [ ] 按矩阵逐 agent 接专属表单；Claude/Codex/Gemini 只是实施顺序，14 家各有验收行与真实 fixture，不能用头三家验收替代。
- [ ] fixture 使用 sentinel；抓网络/缓存/console/storage/URL 确认无意外泄漏，保存不调用选择/应用。
- [ ] 校验原始/结构化模式未知字段、格式保留、认证切换及草稿取消行为；无安全 raw API 时不开放该能力。

### Task 3: MCP、Prompts 与 OpenClaw 独立资源

**Files:** 后续建议 `web/src/configuration-mcp.tsx`、`web/src/configuration-prompts.tsx`、`web/src/configuration-openclaw.tsx` 及配置 i18n；F10 `mcp-settings.tsx` 保持领域隔离。
**Consumes:** 分类型 DTO、受控 slots、批量期望开关与 OpenClaw 引用/日期文档合同。
**Produces:** 真实独立编辑/保存，不能通过一个 provider payload 通吃所有配置。
- [ ] 测试 transport/agent 门控、Codex/Grok header/type 映射、未知 MCP 项保留、命令不执行、secret 不回显。
- [ ] 测试 prompt 映射/跨 agent 复制不应用、Markdown 安全、OpenClaw 七面板与 primary/fallback/catalog 分离。
- [ ] Daily memory 缺日期/删除恢复合同则标记 release gap，不开放破坏性操作；Hermes/Pi 专属文档行为有 fixture。

### Task 4: 导入、选择与逐设备计划/应用/恢复

**Files:** 后续建议 `web/src/configuration-import.tsx`、`web/src/configuration-application.tsx`，以及明确获批的 Pack 附件入口；不重用旧 repository candidate 语义。
**Consumes:** preview pin/commit、typed selection、render plan、实际客户端执行/状态/restore 通道与授权合同。
**Produces:** 可追溯保存→期望选择→预览→显式应用→逐设备状态→恢复闭环。
- [ ] 授权上传前零请求、默认零选择；切来源/agent/account/unmount abort+generation 防旧响应回填，清除原文及结果草稿。
- [ ] 验证同用户两设备不同状态、旧 adapter digest/asset revision/本地基线计划失效、取消/断线 unknown、部分失败与安全恢复。
- [ ] 缺原生执行通道时 Web 不伪造 applied；必须以真实本机 diff/backup/apply/restore 证据签署支持能力。
- [ ] Pack 默认分享不含秘密；附件引用/消费授权、旧客户端拒绝与删除引用保护通过后才接入口。

### 每项任务共同的完成证据

- [ ] 先行为 red 再 green；只在仓库已有合适测试设施时加自动测试，不新增一套空壳 harness。浏览器与真实执行是独立门槛，不能用类型检查代替。
- [ ] `npm --prefix web run typecheck`；`npm --prefix web run build -- --outDir <临时目录>`；限定写集 `git diff --check`。
- [ ] 真浏览器中文/English、light/dark/system、390px、键盘、locale/theme 不丢草稿、account/unmount 清理；query/storage/日志无 sentinel。
- [ ] 独立代表按矩阵逐项记录通过/失败/未实现；尚未接通的 runtime/格式/设备能力保持 gap，不称完整 F12/M6。
- [ ] 只向主线程提交差异与证据，未经新指令不 stage/commit/push。

## 9. Checkpoint 与本轮行动记录

- MCP UI 保持完成 checkpoint：七项独立 scope，新增 categories:read/categories:write/publication:write 默认未选；发布风险已对齐仓库级所有技能完整目录，不涉及配置资产授权。
- 上一轮实测：npm typecheck、临时 build `/tmp/cognate-mcp-web-build.qmUHog`、限定 diff check 通过；七项 scope label/help 在中英实际 i18next namespace fallback 通过。不是本轮新测，也不是 AI 配置已实现证据。
- 本轮只读 matrix、内部资产 spec、F12、adapter 边界及现有组件目录；读取 ui-ux-pro-max/ub-mode/writing-plans，采用现有组件而不重新设计产品外观。
- Nowledge CLI thread 检索 `Cognate cc-switch configuration assets matrix` 返回 0；没有用历史记忆替代当前本地文档，也未保存推测性决定。
- 本轮只写本文件，未跑生产构建/浏览器，未修改任何生产代码/API/生成物/依赖/兼容 key；未 stage/commit/push。计划与合同需求审阅通过、API 冻结和明确写集授权后才实施。
