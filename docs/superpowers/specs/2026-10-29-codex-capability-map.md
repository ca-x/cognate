# Codex capability map — 代表签核候选，非 spec / 实施授权

日期：2026-10-29。事实基线：Cognate `de2886ea21a6ad0cf3fbd2afb21cb71df3dfb9fd`；本地 cc-switch `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`。沿用 `next-Codex-vertical-proposal.md`（SHA256 `a248d2bd68fb9becddeca80f5efc21f58d33cd50c1e0d973a46f818dec03af76`）。只读固定源码，未运行 upstream，未读真实 HOME/配置/账号/密钥。C0-S、K 初始化、B1c moving WIP 均不纳合同。本文件仅划 capability/ownership 与待决项；先签 map，再签 A spec，不冻结 wire 编号或新认证协议。

## 1. 模块、输入输出与依赖

下列 `CX-*` 是**提议的工作模块标识**，不是注册的 plugin/capability/wire ID。基线真实 `builtin.codex@1.0.0` 只有 `codex.config`/TOML metadata，`capabilities=[]`；不可当成已有可应用能力。[L1]

| 模块 / 对应片 | 有界输入 → 输出 / 副作用边界 | 前置依赖 |
|---|---|---|
| **CX-PROVIDER-PLAN / A** | typed provider desired、认证分支、secret presence/ref、模型与目录声明 → safe typed owned patch、required logical slots、diagnostics；服务端 moejs 确定性映射，Go host 校验；无文件/网络/账号操作、不传 secret 明文给 JS、不输出任意路径 | 先签下文 A 决策；小型 neutral types/patch seam |
| **CX-LOCAL-CODEC / B** | 合成本地 TOML/JSON/Markdown bytes + typed owned operations → candidate bytes、diff、冲突/不支持；保留未选字节/注释/顺序；纯转换，无磁盘/网络/命令/JS | A 的 owned-operation seam；无损 TOML 方案/依赖另批，不能预设 go.mod 新库 |
| **CX-ASSET / C1** | typed Codex draft、KEEP/REPLACE/CLEAR、owner/CAS → 加密资产、脱敏 view、copy 无秘密；Web 仅保存资产，不 apply/reveal | A 类型与 slot 合同；复用现有 envelope/CRUD，不预设新表 |
| **CX-NATIVE-GRANT / C2** | typed selection、明确敏感 slot 审批、现有 proof/session、完整 intent → 六 RPC 授权/一次消费/Status、pinned safe patch；secret 只给获授权 native consumer | A + C1；显式扩展现有 canonical 编码/strict codec/pin，旧 Claude 向量不改义；重验配额 |
| **CX-LOCAL-PUBLISH / D-core** | B 候选 bytes、受控 root/各 artifact baseline、C2 结果 → preview、确认、journal、publish/reconcile/restore 结果；Consume≠Applied | B+C2；C0/K 签交后串接其锁/key/编排；网络/确认期间不持 install/target 锁 |
| **CX-CLIENT-UX / D-shell** | typed assets / 本机选择 / 显式审批 → CLI 与 MyGo 薄编排、取消/错误/恢复状态；客户端只连 server，不跑 moejs | D-core；MyGo 真原生、敏感输入不日志；当前桌面连 Claude 配置 tab 也未交付 |
| **CX-MCP / 后续纵切** | typed stdio(command/args/env/cwd) 或 remote URL/header，独立秘密 refs → 单 MCP 增删/目标开关及 native diff/apply | 复用 C1/C2/B/D 扩展；命令只是数据、不执行；不混 provider 表归属 |
| **CX-PROMPT / 后续纵切** | 私有 Markdown、启停/复制/本地变更选择 → 受控根目录 AGENTS.md 的 preview/确认/恢复 | 复用 C1/C2/B/D；不能借公共 raw payload 上传本地内容 |
| **CX-CODEX-COMPLETE / 完整 Codex 门禁** | 官方/本地账号引用与发现、当前项保护/排序、代理转换/路由/failover/Stack 等矩阵行为 → 各自真实交互与验收 | 独立后续能力合同；OAuth 保持本机，不因 provider 首片而删除或勾完 |

**构建顺序**：代表签 map → 签 A spec/小 seam → A；B 与 C1 可在 seam 冻结后并行，A/B 先以合成 fixture 交付；C2 在 A/C1 合同签交后集成；D 等 B/C2 与 C0/K 正式交接；最后 shell 真实交互验收。首个 provider 完整纵切是 **A+B+C1+C2+D**，不是单独 A 或 C1。MCP/prompt/账号等继续逐能力推进；全 **14-agent/F12** 矩阵不缩减，不以通用 raw JSON 代替 typed 能力。

## 2. 候选 disjoint 写集（未来授权，不是本次写权）

| owner lane | 候选独占路径 / 并行限制 |
|---|---|
| A | 新 `internal/agentconfig/codex.go`；`internal/server/agentadapter/{codex.mjs,codex_patch.go,codex_patch_test.go}`；同 owner 修改该包 `builtins.go/builtins.json/registry.go/registry_test.go`。新 seam 由 A 独占，B/C1 只消费，不并写。 |
| B | 新 `internal/codexconfig/{parser,provider,mcp,prompt}.go`、该包 tests/testdata；不碰 configapply/C0。无损解析依赖批准前不动 module。 |
| C1 | 公共 `proto/skillhub/configuration/v1/configuration.proto` + 正常 Go/Web gen；`internal/server/configuration/{types,domain,envelope}.go` 及新 Codex 文件；`internal/server/httpapi/configuration.go`、`configuration_{mapping,codec}*.go`；`web/src/configuration*`。公共 gen 与 C2 Go gen 分批生成，不同时重写同一输出。 |
| C2 | native proto + 仅 Go gen；`internal/nativesessionauth/native_apply*`；`internal/server/{configuration/native_consumer*,state/native_configuration*,nativeconsumer/*}`；`internal/server/httpapi/native_configuration*`；`internal/configclient/native_{pin,codec,flow,proof}*`。与 C1 的 configuration 包需基于签交 seam；不准同时改共同文件，不改 auth 体系。 |
| D-core / D-shell | core：`internal/configapply/native_*` 与新 Codex publisher/reconcile/restore；shell：`internal/desktop/{configuration_*.go,model.go,view.go,service.go}`。CLI 的具体入口路径须等 C0 交接再定，当前不抢占；core/shell 仅冻结接口后并行。 |

**现行排除**：不接触 K、Carver C0-S2、Lovelace B1c 的在途写集。以上不是 glob 扩权；后续各片必须精确列文件、生成文件与 module owner。MCP/prompt 尚不获实施写权，不借本 map 扩为大批生产修改。

## 3. A typed planner 签 spec 前必须确认的精确问题

本节“源码事实”仅指固定 cc-switch 实现/注释，不等于已实测 Codex CLI 各版本行为；不能据此声称兼容验收。

| 待签项 | 源码事实 / 精确字段 | 建议最小选择与未决门禁 |
|---|---|---|
| **逻辑 secret slots** | `row_key` 优先输入 `settings.auth.OPENAI_API_KEY`，再选中 provider table 的 `experimental_bearer_token`，再 legacy 顶层 token。[U1:341] 当前投影把普通 custom key 写到 provider-scoped token，而非直接写 auth.json。[U1:9,373] | 建议普通 key 只设**一个逻辑 provider credential**，候选名 `codex.provider-key`（未批准）；不要照搬 Claude 两槽，把 APIKEY/bearer 当成两份独立秘密。须签 KEEP/REPLACE/CLEAR 的 requiredness、换认证分支的旧槽清理/禁止残留与无凭据分支；env/header/aws/command/OAuth 不得混进该槽。 |
| **认证分支 / placement** | `declared_auth` 先 `env_key`，再 auth/aws 或独立 Authorization headers；`custom_route` 无声明才注入 token；危险 keyless official fallback 被拒绝。[U1:259,373] 分支为 EnvKey/Bearer/Headers/None。[U1:65] | 首片建议 custom 原生 Responses + provider bearer；其他分支明确 unsupported 而非猜测降级，完整能力仍 pending。需签 endpoint/HTTPS或本地例外、header/env refs 的未来独立槽、token 空值语义；不能默认移动/删本地官方 auth.json。 |
| **requires_openai_auth 的权威性** | Bearer/EnvKey 的该 flag 随**操作后实际本机登录**；Headers/None 不得回退官方凭据。[U1:78] file/keyring/auto 不同；auto 仅 keyring **确定 missing** 才 fallback file，unknown 不能假作 missing。[U2:333] | server-only A 无法知道本机登录。须选择：声明式 local-auth policy 由 D 在绑定本地基线/完整 intent 后解析，且 C2/pin 有确定 parity；或首片遇未可判定 auth-store 明确拒绝。**不能**由“有 provider key”推出 flag，不能上传 OAuth/让 JS 查询 keyring。此决策未签前 A 不得声称输出完整可安装 patch。 |
| **provider/global 归属** | upstream 归一到 `model_providers.custom`；ROW_TOP_FIELDS 为 model/review_model/model_reasoning_effort/plan_mode_reasoning_effort/disable_response_storage。[U1:22,52] floor 还列 routing、catalog、agents.default_subagent_model/default_subagent_reasoning_effort、memories.extract_model/consolidation_model；exclusive 有 web_search/context/compact/reasoning summaries/verbosity。[U3:120] | 签 Cognate 自有 table ID、碰撞/旧表迁移/原 current 保护；不自动征用 `custom`。签逐字段 allowlist：模型/default/reasoning/subagent、review/memory 是否属本片；approval/sandbox/permissions、MCP/未选 provider tables 默认不动。upstream floor 清单不是 Cognate 越权写许可。 |
| **native wire 与 proxy** | 表单说明 native `wire_api=responses`；apiFormat 属 proxy conversion。[U5] | A typed 分支明确 native Responses；不得把 chat-completions proxy 设置直接写成 native wire。proxy/failover/Stack 后续独立合同，非“支持 Codex”即可混用。 |
| **模型目录** | model、displayName、contextWindow(string或number)、supportsParallelToolCalls、inputModalities、baseInstructions、reasoningLevels(有序)、defaultReasoningLevel 均存在；隐藏字段也保留。[U4:280][U5] upstream 只反解自有 catalog，外部目录保留，且缺省回填存在折叠语义。[U6:2735] | 签去重/大小写、主/子/review/memory 模型引用、reasoning 默认须在列表、presence vs empty、整数范围/顺序/长度预算，隐藏字段不丢。Cognate 自有 sidecar 名称/逻辑 ref 未定；不把任意服务器路径当输出。foreign catalog 默认本机保留，替换需显式授权；不 lossy 导入/默认接管。 |
| **unknown ownership** | upstream editor 分离 floor/exclusive/global 节点；模型目录有比简化表更丰富的外部结构。[U6][U7] | 源资产 unknown extras 只能 encrypted/private 并回摘要；公有 typed view 不开放任意 map。目标 unknown bytes/tables 归 B 本地保留，不上传；未知 actionable auth/字段、重复/歧义 TOML 必须拒绝 apply，不能以“保留”冒充语义验证。host 限定 target/slot/operation 与大小，moejs 输出不是最终授权。 |

**代表需签的结论**：模块/DAG/ownership；A 首片认证覆盖范围与唯一逻辑槽；local-auth policy 接缝；字段/目录 ownership。数值 bounds、枚举、wire/encoding 只在下一 A spec 中冻结，本 map 不自定。先验收 A 真实 SQLite/moejs 合成映射与恶意 plugin host 拒绝、无秘密输出，再分别验 B 字节保真与 C2 pin/server parity；最终 TLS 六 RPC、一次敏感授权、丢包只 Status、多 artifact 失败/restart/restore preview-confirm、CLI/MyGo 真交互各有证据。跨文件 journal 不能冒称 OS 原子多文件写，未知状态不得重发 secret。

## 源码证据索引（均固定在页首 SHA）

- [L1] Cognate `internal/server/agentadapter/{builtins.json,builtins.go,registry.go}`；`internal/agentconfig/types.go`；公共 proto 的 `Draft.payload` 仅 Claude；`internal/server/configuration/domain.go::metadataSupported` 仅 Claude/provider/schema1。metadata 能显示 Codex ≠ typed 编辑可用。
- [L2] Cognate `proto/skillhub/configuration/native/v1/native.proto`；`internal/nativesessionauth/native_apply.go`（`PatchFields [7]`、Claude pin/target）；`internal/configclient/native_{pin,flow}.go`；`internal/configapply/native_{selection,plan,engine}.go`；`internal/desktop/view.go`。须显式兼容扩展，不能改 agent 字符串即称支持。
- [U1] cc-switch `src-tauri/src/live/project/codex.rs:9`、`:22`、`:52`、`:65`、`:78`、`:259`、`:341`、`:373`。
- [U2] `src-tauri/src/services/provider/codex_direct.rs:333`（login_after）、`:533`（AuthGoal/Planned）。
- [U3] `src-tauri/src/live/floor.rs:120`（floor/exclusive/native table ownership）。
- [U4] `src/types.ts:280`（CodexCatalogModel）。
- [U5] `src/components/providers/forms/CodexFormFields.tsx:200`；`hooks/useCodexConfigState.ts:35`（hook 完整路径同 forms 目录）。
- [U6] `src-tauri/src/codex_config.rs:2735`（foreign catalog 与缺省）；同文件 resolve catalog path 的根目录/链接约束。
- [U7] `src-tauri/src/services/provider/codex_editor.rs:56`、`:120`、`:426`；`src-tauri/src/mcp/codex.rs`（mcp_servers/http_headers，不写 type）；`src-tauri/src/prompt_files.rs:32`（AGENTS.md）。上游 reset malformed MCP table 不作为 Cognate 合同。
- 完整目标依据固定 Cognate `docs/CCSWITCH-CONFIGURATION-MATRIX.md` 与 `docs/superpowers/specs/2026-10-28-configuration-contract-roadmap.md`。本 map 无运行验收声明，不替代 14-agent/F12 签收。
