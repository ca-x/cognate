# cc-switch 配置交互矩阵与下一阶段合同建议

日期：2026-10-28。状态：源码事实审阅完成；用于冻结下一阶段需求，**不是功能交付或 RPC 已冻结声明**。只读研究，不改生产代码、module、生成物；不执行 upstream，不读取真实用户配置或密钥。

## 1. 版本、方法与结论

- 上游：<https://github.com/farion1231/cc-switch>。
- 固定提交：`6d6b7162f122ac0ce1356a14688c8c4f712db3c8`，提交时间 `2026-10-28T07:39:35Z`；`package.json` 为 `4.0.21`。这是 main 源码版本，不等于已验证的最新 release tag。
- 本地用户提供 checkout：`/home/czyt/code/rust/cc-switch`。本次检查 `git rev-parse HEAD` 与上述提交一致，`git status --short` 输出为空。沿父目录与仓库未发现适用 `AGENTS.md`；已加载 `rust-best-practices`，仅只读 Rust。
- 前期外部获取遵循 Firecrawl skill，使用 `firecrawl-cli@1.26.24` 的 keyless scrape，缓存 `/tmp/cognate-ccswitch-research-8m8S8D`；随后直接读上述本地 checkout 对照 UI、Rust command/service、原生文件投影。网络未阻塞；无需用户再次拉取仓库。
- 方法边界：读到了实际交互组件和后端执行路径，但**没有启动 Tauri/真实 agent 做点击或应用验收**。上游文档宣称的实测不等于本任务实测。Nowledge 是历史 case，不代替当前源码。
- 关键差异：README 能力表列 12 个工具，实际 `APP_IDS` 注册 **14 个**，新增可见 `dsh`（DeepSeek Harness）、`agy`（Antigravity CLI）。源码能力门控优先于 README 概述；不能只做 Claude/Codex/Gemini 就称完整，也不能用一个 JSON 文本框代替全部交互。[S01][S02]
- 当前 Cognate `ImportService` 两个 preview RPC 只返回仓库候选；`source_enabled` 也只是候选的来源开关。它们没有配置资产、模型、MCP、提示词或本地应用语义。完整配置需要增量合同，不能在 `repository`/`branch` 等现有字段里塞 JSON。[L01]

## 2. Agent 能力总表

符号：✓ 为上游明确提供；— 为此版本该能力门控未提供；“特例”须按下一节语义实现，不得归一成全局 current provider。以下是**上游事实**，不是 Cognate 已实现清单。[S01][S02][S03][S04]

| Agent / ID | Provider 主要语义 | 专属结构化编辑 | MCP | 提示词/工作区 | 路由 / failover | 聚合/Stack |
|---|---|---|---|---|---|---|
| Claude Code / `claude` | 独占切换；当前项删除受保护 | endpoint、key 类型、模型/兼容开关 | ✓ | `CLAUDE.md` | ✓ / ✓ | ✓ |
| Claude Desktop / `claude-desktop` | 独占；官方/第三方/模型映射 | 独立 Desktop provider 表单 | —，相关扩展入口作用于 Claude Code | 非独立提示词库 | 模型映射经本地服务 / 无 failover | 源码 `STACK_APP_IDS` 含 Desktop，需单独验收；README 概述未列 |
| Codex / `codex` | 独占；官方账号卡与 API provider 不同 | endpoint、key、模型目录、reasoning、账号选择 | ✓ | `AGENTS.md` | ✓ / ✓ | ✓ |
| Gemini CLI / `gemini` | 独占；API key/OAuth 模式 | endpoint、model、auth；env/settings 双编辑 | ✓ | `GEMINI.md` | ✓ / ✓ | — |
| Grok Build / `grokbuild` | **独占**；模型表与 default 引用切换 | profile/upstream model 与 TOML 投影 | ✓ | `AGENTS.md` | ✓ / ✓ | — |
| OpenCode / `opencode` | 共存；加入/移出原生配置；工具内选模型 | npm/API、headers、options、模型及 variants/cost | ✓ | `AGENTS.md` | — / — | —；OMO/OMO-slim 是另外的专属配置，不等于路由聚合 |
| OpenClaw / `openclaw` | 共存；provider 与全局 primary/fallback 分开 | 协议、key、endpoint、模型、agents/env/tools 面板 | — | **Workspace + Daily Memory**，不是通用 prompt 库 | — / —；原生模型 fallback 不是 CCS failover | — |
| Hermes / `hermes` | 共存；内置 provider key 与自定义 key 有区分 | endpoint/key/models/rate-limit 等专属字段 | ✓ | Memory 交互，原生 `SOUL.md` | — / — | — |
| Pi / `pi` | 共存；Enable/Remove | Pi 专用 API、provider/model 配置 | ✓ | `AGENTS.md`、`SYSTEM.md`、prompt templates | — / — | — |
| MiniMax Code / `mcode` | 共存；只管理允许的 custom 节点，默认引用保护 | 专属表单复用 OpenCode 字段组件，不能混用文件格式 | ✓ | `AGENTS.md` | — / — | — |
| Kimi Code / `kimi` | 共存及原生 default_model 特例；官方 OAuth 节点保护 | 专属 provider/models、账号/原生节点校验 | ✓ | `AGENTS.md` | — / — | — |
| DeepSeek Harness / `dsh` | 共存；独立 agent，不是 MiniMax 别名 | Mcode 表单复用；可配置 apiKeyEnv | ✓ | `DSH_HOME/AGENTS.md`，默认 `~/.dsh/AGENTS.md` | — / — | — |
| Antigravity CLI / `agy` | **专属语义**：官方通过启动参数临时选模型；第三方 key 经私有目录启动 | Gemini 风格表单，模型显示名；不能当 Gemini 文件原样写 | ✓，独立开关 | `~/.gemini/config/AGENTS.md`，不是 Gemini 的 `GEMINI.md` | 不在 CCS proxy/failover 集合 | — |
| Oh My Pi / `omp` | 共存；Pi-family Enable/Remove | Pi 表单复用；有 discovery 特有配置 | ✓ | `AGENTS.md` | — / — | — |

注意：`is_additive_mode()`/`ADDITIVE_APP_IDS` 不能替代 agent 特例。Rust `current()` 在 Kimi 分支先查询原生 current，之后共存通用分支才返回空；UI 不得把所有共存工具都显示唯一 current，也不得抹掉 Kimi 原生默认模型。源码明确的未支持与本轮“未核对”不同，后者不能显示成 upstream 不支持。[S02][S04]

## 3. 需要保留的实际交互

### 3.1 Provider 公共操作与保存/应用分离

上游列表/card 有新增、编辑、复制、删除、排序及模式相关操作；UI 调用独立的 provider CRUD 与 add/remove-live/switch 命令。复制不是启用；保存资产、选择 provider、写入本地文件是不同状态。官方账号卡可能禁止复制/删除，不能统一开放所有按钮。[S03][S04]

建议 Cognate 对应交互：

1. 选择 agent → provider 列表 → 选择 preset 或自定义 → 专属结构化表单；检查名称/ID/协议/模型引用。
2. 保存只改变服务端资产；客户端预览应用计划后才写本地。不要照搬上游“编辑全局字段立即写 live”的隐式副作用。
3. 编辑器分“随 provider 切换的字段”与“agent 全局字段”，显示真实目标文件/逻辑 slot；原始编辑与表单双向验证，不可静默丢掉未识别字段。
4. 独占：显示当前项、显式切换；删除引用中的项前要求切换或解除引用。共存：显示已启用数量、逐项加入/移出；删除资产与只移出本地分别确认。
5. 草稿取消/离开警告；保存时检查远端 revision 和本地基线。外部修改冲突列字段/文件，明确“保留外部变更”或重新预览覆盖，不能最后写入获胜。[S05]

### 3.2 原生格式与每个核心 agent 的专属表单

| Agent | 上游实际交互与原生目标 | Cognate 首发必须避免的简化 |
|---|---|---|
| Claude Code | key/auth-token 选择、endpoint、模型组、模型获取/选择、兼容开关；JSON 编辑器显示切换后的 `~/.claude/settings.json`。MCP 为 `~/.claude.json` 的另一归属域 | 不能把所有 env 当 provider 私有字段；保留 permissions/hooks/plugins/未知自定义 env。toggle 的全局/单 provider 归属必须显式 |
| Codex | 模型目录、reasoning、默认模型、官方账号卡；`config.toml` 原始编辑，key 单独字段、涉及 `auth.json`。表单注释明确原生 `wire_api` 为 responses，转换协议是代理层选择 | 不把整个 TOML/auth 作为单个 JSON 覆盖；不混淆原生协议与代理转换；官方登录/OAuth 凭据不随普通配置导出 |
| Gemini | OAuth/API 模式、endpoint/key/model；`.env` 与 `settings.json` 两个编辑器 | 不能用 JSON 解析 `.env`；认证模式变化需独立预览，不导出用户登录凭据 |
| Grok Build | 专用表单，`models.default` 指向模型表；`~/.grok/config.toml` 原始编辑，其他全局字段独立 | 切走删除的是 CCS 记录的上次托管模型表，不是用户后来选中的任意模型表；保留其他 TOML/注释/格式 |
| OpenCode | provider ID、npm SDK/API 类型、key/base URL、headers、SDK options；模型 map 的 ID/name、context/output limits、variants/cost/options；原始配置入口 | 不是一组 endpoint/key/model 就够；不得执行表单中的 npm 字符串或安装第三方 SDK；OMO 特例不能假装普通 provider |
| OpenClaw | provider 协议、endpoint/key/User-Agent/headers；模型 ID/name、reasoning、input types、context/maxTokens/cost。额外 Config 面板见下 | 加入 provider 不等于切换默认模型；不能伪造 MCP/Skills 支持，也不能把 primary/fallback 排序压成普通字符串 |

证据：核心表单、专属 Grok/OpenCode/OpenClaw 组件，Rust `get_provider_editor_view`/保存命令及写入分发。[S03][S05][S06][S07][S08][S09]

其他 agent 不是空占位：`ProviderForm` 将 Kimi、Mcode/Dsh、Pi/Omp、Desktop、Grok 分发到专属表单，Hermes 有独立字段组件；需要这些原生字段/引用校验的垂直切片，才可显示“配置应用已支持”。MiniMax 默认模型引用的 provider 禁止删除/禁用，采用兼容目录锁；Kimi 官方原生 OAuth 节点受保护；agy 第三方 key 通过启动环境传递，**仅写一个配置文件无法实现其完整交互**。[S04][S06][S10][S18]

本地源码补核对发现的额外格式边界：[S19]

- Dsh 仅管理 `~/.dsh/profiles/web/cordis.patch.yml` 的指定 override，其他 profile 归原生工具；替换项不可带 YAML tags/anchors/aliases，项外字节需保留，不能因其他项含 `!!js` 而重新序列化整个文件。
- Omp 使用 profile agent 目录的 `models.yml`/`models.yaml`，default 另在 `config.yml`/`config.yaml`；只编辑受管 provider 项，保持项外字节。仅有旧 `models.json` 时不得抢先创建新 YAML，破坏原生迁移。共享 Pi 表单不等于共享 Pi JSON 写入器。
- **agy 文档/源码存在差异**：接入说明仍描述官方卡修改真实 settings 的模型；实际 `agy_config.rs::prepare_launch` 官方卡返回 `--model`（仅该次会话），真实 settings 原则上不写，只有清理旧测试版 `modelProvider` 例外。以源码为准，不把文档旧语义冻结进 RPC。

### 3.3 MCP 不是 provider 快照字段，也不是 Cognate 自身的 MCP 服务

- 上游有统一 MCP server 列表/表单、按 agent 的启用矩阵、单项/批量开关、导入已有配置、重新同步、编辑/删除确认；会展示逐 agent 写入结果。MCP connection 的 command/args/env/url/headers 等需结构化校验，不只是 JSON 文本。[S11]
- `MCP_APP_IDS` 明确排除 Claude Desktop 和 OpenClaw。Desktop 页面相关入口不能被解释成 Desktop 原生 MCP 支持。Pi-family 等写入/保存顺序也有专属分支。[S02][S11]
- Grok 使用 `[mcp_servers]`，传输由 command/url 推断；转换器移除不被原生接受的 type，将共享 converter 的 `http_headers` 改为 Grok `headers`。Codex 与 Grok 不是共享原文即可。[S12]
- 历史 Nowledge case 要求：从 provider 快照剥离托管 MCP；覆盖前保留未知本地项，再 project 已知启用/禁用项。当前代码也有 strip/projection 路径，但新版不再整份 backfill，应按**当前关键字段写入模型**实现，不照抄历史整文件覆盖。[N01][S05][S09][S12]
- Cognate F10 的“AI 管理 Cognate 的 MCP token”与这里的“agent MCP connection 配置”是两个领域，RPC/secret 类型必须隔离。

### 3.4 提示词与 OpenClaw 工作区

上游 prompt 库提供新增、Markdown 编辑、删除、启用/停用、导入当前文件、复制到其他 agent、复制内容；启用写 agent 对应提示词文件，需保存原内容。Pi 有专属 PromptPanel；Hermes 显示 Memory，不能强套 Claude 文件名。[S01][S13]

OpenClaw 至少有以下**独立交互**，不能仅渲染一个 JSON 编辑器：[S08][S14][S15]

| 面板 | 操作与字段 |
|---|---|
| Providers | provider CRUD、加入/移出、模型条目 CRUD；模型引用校验 |
| Agents defaults | primary model、按序 fallbacks、workspace、timeout、contextTokens、maxConcurrent；保留其他已存在字段 |
| Model catalog | agents.defaults.models 的 catalog/allowlist，和 provider 内模型不是同一个资源 |
| Env | 整个 env 节的 JSON 编辑，支持 env.vars/env.shellEnv 嵌套；敏感变量默认遮罩 |
| Tools | profile（minimal/coding/messaging/full）、allow/deny 列表；未知 profile 保留到用户明确更换 |
| Workspace | `AGENTS.md`、`SOUL.md`、`USER.md`、`IDENTITY.md`、`TOOLS.md`、`MEMORY.md`、`HEARTBEAT.md`、`BOOTSTRAP.md`、`BOOT.md`：存在状态、Markdown 编辑、保存时才创建 |
| Daily memory | 日期列表/预览、搜索、今天新建、编辑、删除确认；隐私与删除恢复策略需单独设计 |

路径是上游默认值示例，不是授权 Cognate 扫描这些文件。读取/上传、备份、覆盖和删除分别由客户端明确授权；服务端不能指定任意用户路径。

### 3.5 路由、failover、备份及外围交互

- 路由：Claude/Codex/Gemini/Grok 的 Direct/Routing 视图切换本身不写配置；真正开始/返回直连是单独操作。本地代理需 endpoint takeover、恢复直连、生命周期管理和协议转换。Claude Desktop model mapping 是另一模式。[S01][S02][S16]
- Failover：路由中独立显式启用/确认；入队/出队、优先级排序、每 agent 重试/超时/熔断阈值、健康状态/实际在用项、请求日志。**聚合不执行 failover**。这些是运行时服务行为，不是保存一个队列 JSON 就已交付。[S16]
- Stack/聚合：README 讲 Claude/Codex，源码还列 Desktop。需要模型→provider 映射、默认项、冲突与刷新行为验证；本轮不将 README 与门控差异推断成已实测兼容。[S01][S02]
- 备份：上游数据库自动/手动备份、导入/恢复前备份、保留策略；原生配置首次写入备份；skill 备份为另一类别。SQLite DB 内还含 proxy logs、usage、health；设备设置含路径、current pointer、同步认证等。**导入 provider 不代表授权迁移这些东西**。[S10][S17]
- Projects、session 浏览/恢复、usage/计价、账户登录/刷新、端点测速/模型发现、系统托盘/启动工具、云同步属于真实外围能力，不能写“全量克隆 cc-switch 已完成”而省略。与配置交互有关的项目组合、账户引用、模型发现应有明确首发决策；历史会话/usage 不默认上传。[S01][S03][S10][S18]

## 4. 首发范围判定（供主线程冻结）

依据 Cognate `docs/REQUIREMENTS.md` F6/F7/F12 与用户“完整 AI 配置交互”要求，不以本批 repo preview 限定整个首发。[L02]

| 分类 | 建议冻结内容 | 通过门槛 |
|---|---|---|
| 首发必需：配置资产闭环 | 按支持 agent 的 provider CRUD/复制/排序、模型/端点/协议、专属表单+原始编辑；保存与应用分离；独占/共存/特殊 current 语义 | Web/MyGo 功能一致；CLI 共用内核；不止导入原始 JSON；不支持字段不得静默丢失 |
| 首发必需：配置类型 | agent MCP CRUD/逐目标开关、prompt 库、OpenClaw defaults/catalog/env/tools/workspace；各 agent 特殊格式/引用校验 | 上述实际支持矩阵逐行有 fixture 与端到端证据；不能只验三家而宣称完整 |
| 首发必需：来源/Pack | 明确选择导入仓库/skills/配置各类资产；配置独立维护，显式附到以 skills 为主的 Pack；版本和 secret 策略明确 | 默认不选、不自动应用；旧 Magpie/仓库协议不破坏；公开 Pack 不带密钥 |
| 首发必需：安全应用 | diff、revision/hash 前置条件、选定目标、备份、受控写入、冲突拒绝、状态与真实恢复；仅对明确启用配置自动应用 | 本地并发修改不覆盖；未知结果不伪报 rollback；取消/崩溃恢复可验证 |
| 必须显式定界，不能偷偷延期 | 路由/代理/failover、聚合/Desktop mapping、官方 OAuth/agy 启动、project 组合、OpenClaw daily memory | 若“完整”含这些交互则是首发阻塞；若主线程明确不要求对等克隆，应列未支持项及原因，不能仅保存参数冒充实现 |
| 不因 F12 自动迁移 | 历史会话/usage/logs、同步凭据、任意本机路径/端口/daemon 状态、程序登录 token | 默认不读不传；单独需求与授权 |

**建议首个垂直切片可选 Claude/Codex/Gemini，但这只是开发顺序，不是把首发验收范围缩成三家。** Grok、OpenCode/OpenClaw 及其他注册工具必须有支持/缺口追踪；来源读取失败或未知 agent 需明确诊断，不静默漏导。

## 5. 首个数据模型/RPC 冻结建议

下列为建议合同，不改现有 proto、不与 Lovelace 的 adapter 实现重复。建议先冻结**资产与本地应用分离、agent ID/能力、类型、secret/版本/引用规则**，再填各 agent 表单。不要先定 `map<string,string>` 或单个 `raw_json`。

### 5.1 建议最小领域模型

| 对象 | 最小语义 |
|---|---|
| AgentDescriptor | 稳定 agent_id、adapter/version、能力版本；模式 exclusive/coexist/special；支持的 asset kinds、表单 schema/专属 renderer ID、逻辑文件 slots；客户端必需能力 |
| ConfigAsset | owner/id、agent_id、kind、name、revision、schema_version、adapter_version、typed payload、保留的扩展字段及敏感字段策略；不含任意本机路径 |
| Provider payload | 协议/endpoint、credential reference、模型目录及 default 引用、provider 专属 options；与 agent-global config/MCP 分开 |
| Mcp / Prompt / AgentSettings / Workspace | 不同 kind 与类型化 payload；MCP agent 开关/transport；prompt scope；OpenClaw primary/fallback/catalog/env/tools；workspace 仅受控文档 slot |
| ConfigSelection | 明确的资产 ID+revision、目标 agent、启用选择/顺序；独占 provider 与共存成员集合分开；未选择不执行 |
| Secret | 独立引用/用途/权限；keep/replace/clear 三态，不用脱敏占位符覆盖原密钥；默认不跨用户复制或随分享输出 |
| ApplyPlan | 输入资产 revision、adapter/schema 版本、目标 slot、基线摘要、结果摘要、变更所有权、权限及能力要求、plan digest；不含命令/任意路径 |
| LocalApplyRecord | 客户端实际文件状态、journal/backup 引用、逐项 outcome、恢复资格；服务器 desired state 不等于本机已经 applied |

ConfigAsset 的 kind 不可把 agent MCP 和 Cognate 管理 token 混为同类。服务器 current selection 与每设备实际 selection 也必须分离；同一用户两台设备不得互相冒充已切换。

### 5.2 建议增量 RPC（名称待主线程定稿）

| 合同组 | 建议操作 | 关键约束/客户端影响 |
|---|---|---|
| 能力 | ListAgentCapabilities / GetConfigSchema | 返回版本化能力/结构化字段；未知操作 fail closed；客户端保留原生专属组件 |
| 资产 | List/Get/Create/Update/Delete/CopyConfigAsset | revision CAS、owner 隔离、分页/大小限额、引用检查、敏感字段默认遮罩；复制不自动启用、不默认复制 secrets |
| 选择 | Get/SetConfigSelection | CAS；exclusive/coexist/special 显式语义；只改变期望状态，不宣称改了远端机器文件 |
| 导入 | PreviewConfigImport / CommitSelectedConfigImport | 独立于旧 RepositoryCandidate；绑定原文 SHA、source 格式/版本、候选 ID/顺序、资产类型与 secret 选择；确认后才保存资产，仍不本地应用 |
| 计划 | RenderConfigPlan / ValidateConfigDraft | 授权的当前片段+基线摘要；adapter/asset 版本固定；计划绑定输入与输出摘要；返回类型化验证与 diff 元数据 |
| Pack 配置附件 | revision 化 attachment 引用/manifest 扩展 | 兼容旧客户端；必需能力不满足就拒绝相关附件，不忽略后声称完整；密钥授权与普通 Pack 分享分离 |
| 应用回执 | 可选 ReportConfigApplyStatus | 原生执行不由服务器路径 RPC 代劳；只传脱敏结果/摘要/设备作用域，receipt 不能替代实际文件验收 |
| 模型/端点探测 | 可选 DiscoverModels/TestEndpoint | 必须单独授权网络与凭据用途，域/地址/响应上限及 SSRF 策略明确；不是客户端直接访问任意插件 URL |

首个建议冻结单元：**版本化 ConfigAsset + AgentDescriptor + CAS CRUD + secret 三态**，加 typed provider/models/MCP/prompt/OpenClaw settings 形状；随后 source preview/import 与 render/apply plan 各自独立。这可避免 UI 被通用 JSON 形状锁死。对所有操作增加 unknown/unsupported 的显式结果；未知字段保留不等于未知操作可执行。

## 6. Adapter 边界（仅矩阵输入，不重复实现）

沿用主线程 `docs/AGENT-ADAPTERS.md`：来源 importer 与目标 adapter 分开；moejs **只在服务端**提供声明式能力/表单元数据、解析、规范化、验证和渲染。客户端原生核心拥有本地授权、slot→路径映射、格式安全、hash/revision 检查、diff、备份、写入与恢复。[L03]

本矩阵对该设计新增的约束：

- 同一“provider”跨 agent 不同型，表单 schema 必须表达模型集合/引用/排序/secret 三态；OpenClaw 要多面板，不是单一 endpoint 表单。
- server render 的可信度不等于本地写入授权。插件不能返回任意绝对路径、命令、可执行 JS 或任意文件删除；路径与 write ownership 由客户端决定。
- 用户当前配置可能含 secrets/未知 MCP，不能为预览默认整目录上传；按明确授权的最小片段与内容摘要处理，若确需原文则单独确认。
- 本地代理/failover/进程启动/OAuth/文件锁/daemon 重启不能由纯 server moejs 实现；需要独立原生能力且用户显式启用。agy 第三方 key 的启动语义是直接反例。
- adapter 新版本不能绕过旧客户端 capability gate；有本地不支持的语法、权限或必需 slot 就拒绝应用，并明确缺口。

## 7. 必需行为验收与未证缺口

| 组 | 必须补的验证 |
|---|---|
| 表单与原文 | 每 agent typed→raw→typed 往返；未知字段保留；JSON/JSONC/TOML/YAML/env/Markdown 按各格式验证；错误原文不可覆盖有效配置 |
| Provider 语义 | 独占切换/删除保护；共存多 provider 不互删；Kimi/MiniMax 原生引用；Grok 只删除托管模型表；Desktop/agy 特例 |
| MCP | 同名未托管项保护；托管启用覆盖/禁用删除；transport 改变清旧字段；每 agent header/type 差异；失败不丢本地未知 MCP |
| Prompt/OpenClaw | prompt 启用前保存原文；workspace 缺文件仅保存时创建；primary/fallback 去重与排序；catalog 引用；env/tools 未知值；memory 删除授权 |
| hash/权限 | 上传前同次读取校验已确认 hash；本地应用前再校验基线；symlink/越界路径/非 regular 拒绝；私有 secret/backup 权限；无覆盖生成 |
| 部分失败 | 实际写入、未写入、取消、unknown 分开；已成功不伪造全事务 rollback；备份恢复也要校验未被他人改写 |
| 来源数据库 | 合法 SQLite backup 快照 fixture；WAL 源不能直传裸 DB；导出目标经 SQLite journal_mode=DELETE 并关闭后检查自包含 rollback header，不能翻 header 字节；此为主线程要求，导出器本任务未实现/验证 |
| 网络/敏感信息 | 不自动探测用户目录、不读真实密钥；HTTPS/same-origin/禁 redirect/有界输入输出；secrets 不进 URL/日志/state/公开 Pack；跨 owner 拒绝 |
| 客户端一致性 | 真实 Connect fixture、CLI/MyGo 共享核心、旧客户端 capability 拒绝；Web/MyGo CRUD/选择/diff/冲突/恢复端到端 |

明确缺口：未运行 upstream GUI/agent；本轮没有完成各额外 agent 全字段 round-trip、Desktop Stack 实际行为、所有平台锁/原子性和 OAuth/代理运行验证。它们是**验收待办**，不能从注册门控推断已实现兼容。只读研究没有运行 Go/Cargo build/test/race；不产生 CGO1/race 证据。

## 8. 可追溯证据索引

以下上游链接均固定到同一 SHA；本地前缀均为 `/home/czyt/code/rust/cc-switch/`。可按路径与符号复核；无需读取真实 `~/.cc-switch`。关键定位：`APP_IDS`/`PROXY_APP_IDS`/`STACK_APP_IDS`/`ADDITIVE_APP_IDS`/`MCP_APP_IDS`，`ProviderService::{current,delete,add_to_live_config,remove_from_live_config,switch}`，`ProviderForm` 路由和 `get_provider_editor_view`。

- [S01] [README 能力表与交互说明](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/README.md)；[package.json](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/package.json)。
- [S02] [前端 agent 注册与能力门控](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/config/appConfig.tsx)；[Rust AppType](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/app_config.rs)。
- [S03] [Rust provider 命令](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/commands/provider.rs)；[ProviderCardActions](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/providers/ProviderCardActions.tsx)；[ProviderList](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/providers/ProviderList.tsx)。
- [S04] [ProviderService 实现与回归测试](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/services/provider/mod.rs)。
- [S05] [编辑交互/字段归属/冲突说明](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/docs/user-manual/en/2-providers/2.3-edit.md)。
- [S06] [ProviderForm 专属组件分发](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/providers/forms/ProviderForm.tsx)；同目录 `ClaudeFormFields.tsx`、`CodexFormFields.tsx`、`GeminiFormFields.tsx`、`HermesFormFields.tsx`、`PiProviderForm.tsx`、`McodeProviderForm.tsx`、`KimiProviderForm.tsx`、`ClaudeDesktopProviderForm.tsx`。
- [S07] [OpenCode 结构化字段](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/providers/forms/OpenCodeFormFields.tsx)。
- [S08] [OpenClaw provider 表单](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/providers/forms/OpenClawFormFields.tsx)；[OpenClaw 命令面](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/commands/openclaw.rs)。
- [S09] [live 写入分发及来源处理](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/services/provider/live.rs)；[Grok 表单](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/providers/forms/GrokBuildProviderForm.tsx)。
- [S10] [配置文件/数据库/备份/原生引用说明](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/docs/user-manual/en/5-faq/5.1-config-files.md)。
- [S11] [UnifiedMcpPanel 实际交互](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/mcp/UnifiedMcpPanel.tsx)；同目录 `McpFormModal.tsx`、`mcpDraft.ts`；[Rust MCP service](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/services/mcp.rs)。
- [S12] [Grok MCP 原生转换/写入](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/mcp/grokbuild.rs)；同目录 `codex.rs`、`agy.rs`、`mcode.rs`。
- [S13] [PromptPanel](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/prompts/PromptPanel.tsx)；同目录 `PiPromptPanel.tsx`。
- [S14] [OpenClaw AgentsDefaultsPanel](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src/components/openclaw/AgentsDefaultsPanel.tsx)；同目录 `EnvPanel.tsx`、`ToolsPanel.tsx`。
- [S15] [OpenClaw Workspace/Daily Memory 操作说明](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/docs/user-manual/en/3-extensions/3.5-workspace.md)。
- [S16] [路由 failover 交互与边界](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/docs/user-manual/en/4-proxy/4.3-failover.md)。
- [S17] [SQLite backup/restore 实现](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/database/backup.rs)。
- [S18] [Antigravity 专属接入语义与上游自述测试](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/docs/agy-integration-zh.md)。
- [S19] [实际 prompt 文件映射](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/prompt_files.rs)；[Dsh 原生 YAML 管理](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/dsh_config.rs)；[Omp 原生 YAML/迁移保护](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/omp_config.rs)；[agy 实际启动/环境实现](https://github.com/farion1231/cc-switch/blob/6d6b7162f122ac0ce1356a14688c8c4f712db3c8/src-tauri/src/agy_config.rs)。
- [L01] 本项目 `proto/skillhub/import/v1/import.proto`：`PreviewLibraryExport`、`PreviewCCSwitchSnapshot`、`RepositoryCandidate`。
- [L02] 本项目 `docs/REQUIREMENTS.md` F6/F7/F12；用户本线程明确补充需求。迁移中的文件仅作当前工作树观察，不宣称已发布。
- [L03] 本项目 `docs/AGENT-ADAPTERS.md`；本矩阵不替代 Lovelace 的 agent plugin 设计/实现。

### Nowledge 检索与历史 case

使用已配置 ambient identity/Space；未切换 Space。可复查检索：`nmem --json m search 'cc-switch Grok additive provider' -n 3`，返回 `search_mode=fast_bm25_vector`，命中 N01 rank 1、score `0.99`、`space_id=default`。其余 Raindrop/Tunnel 命中与本任务无关，不作 cc-switch 证据。此前 `cc-switch` 检索与定向读取也命中 N02/N03。

- [N01] `09aac727-ce83-475c-a5d1-4e21897707a7`，标题“cc-switchy v0.4.x Grok 兼容与 TUI 文案决策”。历史基线 CC Switch `v3.18.0` / `a377d79303bc1e592d2783d559ca5bd6b8ba1417`，cc-switchy v0.4.0 `8bac6b7b571cd271a0a7c6526ffcdfd0ab0de4d7`、v0.4.1 `5602e8c1974a5271c7fca60e5a540888cc566714`。明确 Grok 独占、`~/.grok/config.toml`、MCP 托管/未知项保护、旧 DB 只读兼容与用户文案。不能把当时 user_version16 当今天的 schema。
- [N02] `5d8188f1-94ce-43a0-8c6c-0d9d2544c745`，标题“cc-switchy v0.2.1 与 CC Switch 3.17 schema 兼容结论”。历史 CC Switch `v3.17.0` / `3d176b98cc0bfd151a42882e88ab59b62083b92f`，强调 local-only proxy/usage 表同步保护；不是当前配置全量兼容证明。
- [N03] `c35f77cf-291c-4d21-a61d-c242a3541323`，历史 SkillHub 来源/仓库导入方案。其 repo-only 范围已被本次完整配置需求扩展，不能拿历史方案否定 F12。
- 有界读取过历史线程 `codex-019f8bd4-3c2a-7501-9a0a-66e746b5c281` 的首 8 条，未取得比上述 case 更精确的当前源码事实，不继续翻页。未发现本工作目录常见 sibling 路径里的 cc-switchy checkout；没有将 glob 未命中夸大成整机不存在。
- 未生成 Nowledge 图谱：CLI 可检索但未建立可确认的同一浏览器身份/owner session，不生成未经核验的图链接。此限制不影响文本检索和源码证据。
