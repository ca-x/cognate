# Typed configuration DTO 冻结建议

2026-10-28；状态：供主线程冻结 proto 的建议，**未改协议、存储或 renderer**。继承已定 `docs/superpowers/specs/2026-10-28-configuration-assets.md`，范围依据 `docs/CCSWITCH-CONFIGURATION-MATRIX.md` 的 14 agents，不将首发缩为三家。

## 1. 建议先冻结的公共规则

- 保持已有 storage 合同：不可变 owner/agent/kind，正 revision，更新/删除 CAS；kind 仍是 provider、mcp、prompt、agent-settings、workspace。不要向现有导入候选或 Pack 字段塞配置 JSON。
- 新公共 DTO 使用 `oneof provider | mcp | prompt | agent_settings | workspace`；kind 必须与分支一致，agent/variant 必须与 capability registry 一致。**不公开内部密文 envelope，不提供通用 raw_json/Struct payload**。
- `ConfigurationMetadata` 与 `ConfigurationDraftView` 分开：既有 List/Get metadata 继续不返回 payload。独立 owner-scoped 编辑读取才返回 typed 非秘密字段及 secret 状态；非秘密不等于公开内容，prompt/文档/扩展字段仍按私有资产保护。
- draft 使用完整 typed replacement；请求必须带 expected_revision。普通 optional 字段区分 absent/false/0/empty；不在同一接口混入隐式 merge/FieldMask。局部 UI 编辑先保留完整读取的 draft，再 CAS 提交。secrets 与 extras 的规则例外见后文。
- `schema_version` 定义 typed payload 版本，不是源软件版本或 adapter 版本；原生 variant、来源版本、adapter/capability 版本分别标识。未知必需能力/oneof/variant 不可回退执行；保留未知原生数据不等于接受未知操作。
- 保存资产不启用 agent。exclusive/coexist/special 的选择、跨 agent 开关和每设备实际状态不放进 provider/MCP payload；本建议不复做 selection/apply/renderer。

概念形状（不是 proto 标签分配，也不是新增存储 schema）：

```text
ConfigurationDraft = agent_id + kind + name + schema_version + Payload
Payload = oneof ProviderSpec / McpSpec / PromptSpec / AgentSettingsSpec / WorkspaceSpec
DraftView = metadata + typed_nonsecret_payload + repeated SecretStatus + ExtrasSummary
UpdateDraft = asset_id + expected_revision + ConfigurationDraft + repeated SecretMutation
SecretStatus = slot_id + present                 // 不返回原文/前后缀/hash/长度
SecretMutation = slot_id + oneof Keep / Replace(bytes utf8) / Clear
ModelRef = oneof ManagedModel(asset_id, model_id) / NativeModelReference(agent_id, value)
```

`slot_id` 是宿主按 typed 字段身份确定的资产内逻辑标识，不是调用者指定的文件路径/JSON Pointer；重复 slot 拒绝。`NativeModelReference` 用于保留来源已有但不在目录的引用，标记 unresolved；不是任意路径或可执行内容，不得冒充已验证模型。

## 2. Provider / models DTO

### 2.1 通用字段与必须保留的 variant

`ProviderSpec` 建议含 `native_provider_key`、显示描述、`oneof agent_profile`。endpoint/credential/models 可以复用小 message，但**不能把所有 agent 压成一个 base_url+api_key+model**。资产 ID 与原生 provider key 分离；修改后者是带引用校验的显式重命名，不是改名称字段的副作用。

| Agent profile 分支 | 需要 typed 的字段/语义 |
|---|---|
| Claude Code | endpoint；auth mode：API key / auth token / native login；对应 secret slot；default/fast/模型角色；经验证的 provider 兼容开关。切 auth mode 必须明确处理旧凭据，不能双键残留 |
| Claude Desktop | 官方 / direct / model-mapping 分支，模型→provider 映射与账号引用；不是 Claude settings 文件别名。无本地 mapping runtime 时不能标记可应用 |
| Codex | 原生 provider key、endpoint、auth mode/账号引用、模型目录及默认项、reasoning effort；原生 responses 与路由转换协议分字段。不得导入 auth.json/OAuth 登录态来补普通 API key |
| Gemini | auth mode、endpoint、key slot、model；`.env` 与 settings 的归属分开，OAuth 登录由工具管理 |
| Grok Build | profile/model-table key、upstream model、endpoint/auth、默认表引用；不能仅保存一个 model 字符串后删除任意同名表 |
| OpenCode | **oneof V1 / V2**：V1 为 npm/options/models；V2 为 package/canonical/env/settings/headers/body/models。保留原生 format，不能递归混合 provider/providers 两种声明 |
| OpenClaw | api protocol、endpoint、authHeader、key/headers、models 数组；primary/fallback/catalog 属 AgentSettings，不随 provider 加入动作隐式改变 |
| Hermes | 内置/自定义 provider key、endpoint/key、模型及受支持限流字段；内置 key 不可随意改名 |
| Pi / Omp | 分别声明 variant；provider 与 model 级 api/endpoint/compat，Omp discovery；共享组件不代表共享 JSON/YAML writer |
| MiniMax / Dsh | 分别声明 variant；SDK options、models；Dsh apiKeyEnv 为变量名引用，不是密钥值；原生官方节点/默认引用保护 |
| Kimi | provider 类型、endpoint/key 或受控官方账号引用、models 与原生 default_model 引用；受保护 OAuth 节点不能当普通 secret 覆写 |
| Agy | 官方启动模型 / 第三方 key profile 分支；model 显示名、endpoint/key。明确启动作用域，不假定改全局 settings 即切换 |

官方账号引用只能引用本 owner、同 agent、已授权的账号标识，不能让调用者填任意 credential ID。尚无账号服务的分支应明确 unsupported，而不是接受 token 字符串假装 OAuth。[U1][U2]

### 2.2 模型、端点与扩展字段

- `ModelSpec`：稳定本地 model_id、可选 upstream_id/name/alias、可选 context/output limits、input/output modalities、reasoning capability/efforts、显式 compatibility、cost、variants。agent-specific 模型参数放对应 typed 分支，不把所有参数变字符串。
- 模型列表与 variant/fallback 列表使用 repeated 保持顺序，ID 唯一；引用使用稳定 ID，不使用表单行号。默认模型、primary、fallback、catalog alias 是不同字段。已有未配置引用允许保留并提示；新增/改动的引用须按 agent 规则验证，不能一律阻断合法 native model 引用。
- token/window/count 使用有 presence 的整数，protobuf JSON 使用标准整数表示约定；价格用带单位的规范十进制字符串（如每百万 token、币种），不以 double 存价格。所有数值须受目标 agent 范围约束，不把原生未知大整数经 JS double 往返。
- `EndpointSpec`：用途明确的 URL、可选 protocol 与有序候选端点；URL 禁 userinfo/嵌入秘密，查询串的认证参数须改 secret slot 或拒绝。保存 URL **不触发请求**；本地模型服务允许的地址与远程探测 SSRF 策略分别校验，不能把 Cognate server 的同 origin 限制套给所有 provider 端点。
- headers/env 使用可校验的命名条目；header name 按 HTTP 大小写不敏感去重，env 名按目标平台/格式规则验证。header/自定义 env 值默认作为敏感 slot，避免未知 `X-Key` 被错误归为公开字符串。
- OpenCode SDK options/body、模型 cost/variants 等已有扩展必须保留；“保留”不能授权执行 npm、JS、hook 或发网络请求。非秘密的可编辑扩展逐版本加入 typed 分支，其他数据见 §5。

## 3. MCP / prompt / agent-settings / workspace DTO

| Payload | 建议 typed 结构与验证 |
|---|---|
| `McpSpec` | 稳定 native server key；`oneof Stdio(command, repeated args, env secret slots, optional cwd_binding) / StreamableHttp(url, headers secret slots) / Sse(url, headers secret slots)`；已验证 timeout 等 options。不得同时有 command 与 remote URL。command/args 是配置数据，不由服务端执行；凭据不得藏 args/URL，需 env/header slot。cwd 使用本地受控绑定，不从来源绝对路径直接应用 |
| `PromptSpec` | UTF-8 Markdown content、`oneof global / supported project scope / Pi system / Pi template`，以及目标支持的逻辑 prompt slot；Hermes 的 prompt slot 为 SOUL.md，不是 memory/user 文件。content 私有且可能敏感；不另带 enabled/current。不同 scope 不可共享一个开关；跨 agent copy 要重新校验格式/上限 |
| `AgentSettingsSpec` | `oneof agent-specific settings`；OpenClaw 分支再用 `oneof defaults / catalog / env / tools`，每项资产只拥有一个 section。defaults(primary, ordered fallbacks, workspace_binding, timeout_seconds, context_tokens, max_concurrent)；catalog(model ref+alias)；tools(profile, allow, deny)。完整 replacement 仅作用于该 section，其他 section 不因缺省被覆盖 |
| 其他 agent settings | 按 schema 明确的全局字段分支（例如 Claude global quick toggles、Gemini auth/settings、各 agent 已支持全局参数）；与 provider-owned 字段不能双重拥有。未定义的 hooks/plugins/permissions 等只能原地保留，不经一个通用 object 获得写权限。后续增补 typed 字段不改变现有 oneof 标签 |
| `WorkspaceSpec` | `oneof KnownDocumentSlot / DailyMemory(calendar_date) / HermesMemorySlot(memory 或 user)` + UTF-8 Markdown content；OpenClaw 九种文档为 allowlist，memory 日期严格 YYYY-MM-DD；Hermes 的 MEMORY.md/USER.md 分别管理，字数预算/enable 控制属 Hermes settings。资产不含任意相对/绝对文件路径，root 仅客户端绑定；删除须单独确认，不能用空内容伪装删除 |

MCP 的 `enabled` 是目标选择，不是连接 payload。基于当前不可变 agent_id，每个 MCP asset 面向一个 agent；统一 UI 可批量创建/复制同一逻辑连接，但不得把 storage 偷改为一个跨 agent asset。跨 agent 复制拒绝不支持项，不能给 OpenClaw/Desktop 假造 MCP 支持。agent MCP 配置也不是 Cognate 自身 MCPManagement credential。[U1][U3]

OpenClaw env 支持嵌套 vars/shellEnv：已知控制字段 typed，变量值 secret slot；未知嵌套字段原地保留。未知 tools.profile 不自动换默认：读取显示 preserved/unsupported，用户未编辑则保留，主动更换只接受目标已支持值。timeout/timeoutSeconds 的来源兼容由 adapter version 区分，不能同时下发冲突值。[U4]

## 4. Secrets：keep / replace / clear 精确语义

| 请求情形 | Create | Update（必须 CAS） |
|---|---|---|
| 未提交该 slot | 不创建值；可保存 needs-credential 草稿 | KEEP：保留**同资产当前预期 revision**的原值，不当作 clear |
| 显式 KEEP | 拒绝，无可继承值 | 保留；缺失仍缺失，不借其他 owner/asset 的引用补值 |
| REPLACE(value) | 写新值 | 替换；空值不作为清空，空/非法 UTF-8/超限拒绝；不 trim/大小写归一密钥 |
| CLEAR | 已无值可定义为 no-op | 删除该 slot；可能转为 needs-credential，apply 必须拒绝缺少必需凭据的草稿 |

- `oneof` 的空、未知或重复 mutation 拒绝；“••••••••”等遮罩只在 UI 展示，不能自动成为 REPLACE。UI 未编辑秘密必须省略 mutation，不回传占位符；服务端不靠猜测某字符串是否像真实 key。
- slots 绑定 owner+asset+typed 字段身份+revision；模型/header/env 项使用稳定 key，不用 repeated 的下标。删/改 slot 所属项须显式 CLEAR 或明确迁移操作，不能把省略的 KEEP 偷变删除、把旧 secret 绑定到新名字。
- auth mode 切换必须成组校验。例如 API_KEY→AUTH_TOKEN：新 token REPLACE、旧 key CLEAR；未确认的双凭据冲突拒绝进入可应用态。当前 Claude additive renderer 没有删除能力，不能靠此 DTO 宣称 provider switch 已实现。
- Copy 默认不复制秘密，返回 needs-credential。也不把原资产 slot_id 当共享 secret 引用；未来显式复制/授权须独立合同。公开 Pack、导出及普通编辑返回均不能泄露秘密。
- 仅返回 `present`，不返可猜测的 hash、末四位或长度。只有明确授权的消费/render/materialize 路径可得到必要原文；不新增通用 Reveal RPC。replacement 与 native draft 原文不进入日志/trace/error details/浏览器持久存储。
- revision CAS 必须覆盖 typed 字段、secret 合并和 extras 保留的同次更新；失配时不自动重试，不能把旧 draft 的 KEEP 应用到别人刚更新的 revision。

## 5. Native extras 与 JSONC / TOML / env 保留策略

**两种保留必须分开**：A. 来源资产内未知扩展，随加密资产保留；B. 目标机器已有未知配置，留在目标本地、不默认上传。二者不能自动 merge 成“全局配置”，不能跨 agent 自动投影。

- typed API 不返回任意 `extras: Struct`。服务端保留同 asset revision 下的有界 native sidecar，普通 DraftView 最多返回 `has_preserved_extras`/可编辑能力状态；同 variant 更新默认保留。sidecar 可能含未知秘密，与 payload 同级加密和访问控制，不能以“extras”绕过脱敏。
- 已 typed 的 key 和 sidecar key 不允许重叠；更新 agent/native format 不可悄悄套旧 sidecar。跨 agent/格式 copy 必须显式转换，有未支持 extras 就给诊断/拒绝可应用，不静默丢弃或伪称等价。
- 原始编辑是**显式 owner-scoped 操作**：agent+variant+逻辑 slot+base revision/字节摘要+用户输入原文，经解析→typed 校验→秘密与 extras 分类→diff 确认；不是把任意 bytes 保存为公开 ConfigAsset.payload。普通脱敏编辑不能靠秘密占位符往返，secret-only 操作仍用 §4；原始敏感全文编辑/上传需要单独明确授权。
- 所有输入/输出有界（不得突破 storage 单项 256 KiB），错误返回固定分类、逻辑字段位置，不回显值/原文。当前能力不能无损改写时拒绝，不做 stringify 整文件作为“尽力保留”。

| 原生格式 | 冻结的保留/失败规则 |
|---|---|
| JSON | 保持未选语义/大整数；重复 member 或歧义目标拒绝；null 与 absent 区分。不因新增空 env 把原 null 自动变对象 |
| JSONC | 必须有 JSONC-aware、限定字段的保留能力；注释、尾逗号及未选文本不经 JSON parse/stringify 丢失。客户端或 adapter 不支持则显式 unsupported，不退化成 JSON |
| TOML | 保留未选表、inline table/数组、注释和顺序；managed fields 精确归属，MCP 与模型引用分别投影。重复 key/冲突路径/不能保留的结构拒绝；不能删整个表再重建以省事 |
| `.env` | 按变量归属编辑，不 source/展开 `${}`/执行命令；保留未知行、注释、引号和换行。受管变量重复且无法唯一解释则拒绝，或经明确能力支持的预览列出全部移除项；不静默依赖 first/last-wins |
| YAML（Hermes/MiniMax/Dsh/Omp） | 不能以共享表单推断共享序列化。只改受管节点、保留项外字节；被修改节点的 tags/anchors/aliases 在无明确无损支持时拒绝。Dsh 只管授权 profile，Omp 不抢先破坏旧 JSON→YAML 迁移 |
| Markdown | 文档 slot/scope 与内容分离；保留未改文档，不自动改换行/模板注入；读缺失与读空文件不同，保存才创建，空内容不表示删除 |

不要求所有 renderer 此刻完成这些能力，但能力未实现前不得给该 variant “可应用”状态。JSONC/TOML/env 策略是合同验收条件，不是本任务对现有实现的通过声明。[U2][U5]

## 6. Proto 冻结清单 / 关键反例

建议先冻结：payload oneof+agent/variant；模型引用/顺序/presence；MCP transport oneof；OpenClaw sections 与 workspace slot；SecretMutation 三态；metadata-only Get 与独立 DraftView；revision 绑定 extras/secret 原子合并。**不在本提案分配 field numbers，不改 ent/state、renderer 或现有 wire namespace。**

必须在协议评审中通过的例子：

1. 未改 secret 的完整表单保存 KEEP；stale revision 拒绝，不覆盖新 key；REPLACE 空串不执行 CLEAR。
2. 改 provider 名不改 native key；跨资产/跨 owner slot 引用拒绝；删除模型/header owner 不把旧 secret 绑定到另一行。
3. OpenCode V1/V2 extras 同名 key 不递归混入；未知大整数不变 double；JSONC 不因表单保存丢注释。
4. MCP stdio 与 HTTP/SSE 互斥；Grok headers/Codex http_headers/OpenCode command 数组按目标转换；保留未知本地 MCP，不由 provider asset 覆盖。
5. OpenClaw fallback 顺序/未配置引用保留；unknown tools.profile 未编辑不丢；workspace 越界/任意文件名拒绝。
6. 资产 CRUD、secret 替换、原始 draft 验证均不触发网络/本地写/命令；needs-credential 与 supported-but-not-applied 不混淆。

## 7. 证据与范围

本轮直接只读 `/home/czyt/code/rust/cc-switch`，HEAD `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`；检查时 clean。下列均为该 SHA 的本地相对路径：

- [U1] `src/config/appConfig.tsx`；`src-tauri/src/app_config.rs::AppType`；`src/components/providers/forms/ProviderForm.tsx` 及各专属表单。14 agents/能力/分发以源码为准。
- [U2] `src/types.ts` 模型与 OpenClaw DTO；`src-tauri/src/opencode_config.rs::{get_providers_with_format,native_provider_problem,native_model_problem}`；OpenCode V2 引用的是上游代码注明的 native schema，本任务未另行验证 OpenCode runtime。
- [U3] `src/components/mcp/mcpDraft.ts`；`src-tauri/src/mcp/{grokbuild,codex,opencode}.rs`；上游遮罩实现只作交互事实，不照抄为 wire 秘密策略。
- [U4] `src/components/openclaw/{AgentsDefaultsPanel,EnvPanel,ToolsPanel}.tsx`；`src-tauri/src/prompt_files.rs`；`src-tauri/src/agy_config.rs::prepare_launch`。
- [U4b] `src/components/hermes/HermesMemoryPanel.tsx`；`src-tauri/src/hermes_config.rs::MemoryKind`：SOUL.md prompt 与 MEMORY.md/USER.md 独立，不能共享同一个启用或文件 slot。
- [U5] `src-tauri/src/live/patch/{json,toml,dotenv}.rs`；`src-tauri/src/{dsh_config,omp_config}.rs`。本提案保留要求不等于声称所有上游格式都字节无损。
- 本项目：已定 configuration-assets spec；`docs/AGENT-ADAPTERS.md` 与 `internal/agentconfig` 现有窄能力边界。未将 additive env merge 等同 provider switch。
- Nowledge 本轮检索 `Cognate 配置 asset typed secret keep replace clear`，`fast_bm25_vector` 返回 0 项；故本提案以已定 spec/受控矩阵/实际源码为依据，不臆造既有 typed 协议决定。

仅产出此建议文档；未改任何生产代码/proto/生成物/module；未读真实用户配置，未运行 GUI/Go/Cargo/race。
