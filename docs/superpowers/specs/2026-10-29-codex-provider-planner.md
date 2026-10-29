# CX-PROVIDER-PLAN / A — Codex provider 声明式 planner spec

日期：2026-10-29。**本 spec 待独立设计审；未授权 plan 或实施。**

## 1. 身份、已接受裁定与 objective

- 主线程与 Gauss 已接受 [capability map](2026-10-29-codex-capability-map.md) 的模块/DAG/边界及五项裁定。map 为保持证据逐字复制，历史 candidate 标题不代表尚未获规划接受；其内容 SHA256 为 `d6691bd0da4a3e9907ad4f51370d406826af2025af03359c623c74ca018992bd`。审批实物为 release `codex-capability-map-review.md`，接受的是规划，不是 wire/运行验收。
- 本 spec 的 Cognate 基线为 `34c22a1070e3bdc907d177f89e1bdc426b342e58`；已核 `internal/agentconfig`、`internal/server/agentadapter` 相对 map 基线 de2886e 无差异。上游事实仍固定本地 cc-switch `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`，仅源码核对，无 upstream 运行/CLI 兼容实测。C0-S、K、B1c 在途实现不纳合同。
- **目标**：服务端以 typed desired + 单槽 presence，经现有 moejs Registry 得到可独立验证、确定、有界的 Codex **custom 原生 Responses / provider bearer 安全声明**。B/C1 可消费该 neutral seam；输出含未解析 local-auth policy，**不是完整 intent、可安装 patch、授权凭证或文件内容**。
- 使用者是后续 typed asset/native orchestration；A 不提供 UI/HTTP，不处理真实 credentials，不访问本机登录/OAuth。完整 provider 纵切仍需 A+B+C1+C2+D；MCP/prompt/账号发现/排序与 current 保护/代理路由/failover/Stack、其余 agent 与完整 F12 均保留，不因首片通过而删减。

下文具体新类型名、capability、数值与 owned ID 为**本次设计审候选合同**，不是声称代表已签这些细节；五项已接受裁定不得回退。

## 2. 技术栈、真实 review seam 与候选路径

复用 Go `1.27.3`、`encoding/json/v2`、`github.com/MoeForge/moejs v0.1.0-alpha.11`、`internal/server/plugin.RunJSON`；`CGO_ENABLED=0`。不加依赖、不改 plugin runtime。现有入口是 `agentadapter.Registry.PlanClaudeProvider`，文件名为 **`provider_patch.go`**，并非 `claude_patch.go`。现有 `validDefinition` 只允许 Claude 非空 capabilities；`Builtins` 仅为 Claude 配 module；definition digest 为现有 deterministic Definition JSON 的 SHA256。这些是真实 A 注册接缝，不新造插件系统。

未来候选 **8 路径**，当前不授权写：

| 路径 | 唯一职责 |
|---|---|
| 新 `internal/agentconfig/codex.go` | neutral typed desired、slot、声明与枚举；不引入 server/client/文件系统依赖 |
| 新 `internal/server/agentadapter/codex.mjs` | 纯 `planCodexProvider` 映射，无 host IO/secret bridge |
| 新 `internal/server/agentadapter/codex_patch.go` | bounded input、clone、Registry gate、独立 expected result、strict host validation |
| 新 `internal/server/agentadapter/codex_patch_test.go` | 合成 fixtures、真实 moejs、bounds/恶意模块/取消/ownership/parity/隐私测试 |
| `internal/server/agentadapter/builtins.go` | embed 与 Codex module 分派，不改变 Claude module |
| `internal/server/agentadapter/builtins.json` | Codex planner manifest 精确升级；其他 manifest 不变 |
| `internal/server/agentadapter/registry.go` | 仅新增该 Codex target/capability/module 的精确允许分支 |
| `internal/server/agentadapter/registry_test.go` | descriptor/digest、非法组合、旧 Claude/gemini/metadata 回归 |

这 8 项已经包含 source 与 tests；不是 8 个独立可并行任务。真正的小 review seam 是 **neutral desired/declaration + Go host expected oracle**，随后审 module/注册一致性；同一 owner 串接共享 Registry 文件。若实际需要修改 `types.go`、`provider_patch.go`、`render.go`、runtime 或新增第 9 路径，先报告精确反例/原因申请，不能借本 spec 扩权。B 只实现 provider codec；C1 只消费 neutral types，二者不得并改 A 注册文件。

## 3. Typed interfaces 与 Registry 身份

设计入口：`Registry.PlanCodexProvider(ctx context.Context, request agentconfig.CodexProviderPlanRequest) (agentconfig.CodexProviderDeclarationV1, error)`；不增加通用 raw render、descriptor API 或 auth。命名用于约束下一 plan，不是在本轮写 Go。

- Request 包含现有 envelope 字段 `APIVersion/PluginID/PluginVersion/DefinitionDigest/Target/Capabilities` 与 `Desired`，不包含 base bytes、任意 path、environment map、extras 或 secret value。
- 候选身份：`APIVersion=1`，`builtin.codex@1.1.0`，agent `codex`，target 复用 `codex.config`，format `toml`，唯一 capability `codex.provider-plan.v1`。这是 planner capability，不是已获 native apply capability；无新 wire ID。digest 必须匹配当前注册 definition，不能由 caller 自报可信 pin。旧 `builtin.codex@1.0.0` 不可冒充可执行版本。
- Request capabilities 必须精确一项，不接受重复、超集或只凭 agent string；unknown plugin→`ErrUnsupported`，身份/digest/target/capability 不匹配→`ErrIncompatible`。nil Registry 安全拒绝。`Render`/`RenderJSON` 不因此获得 Codex 文件写出能力。
- `Desired` 精确成员：`authMode`（仅 `provider-key`）、`endpoint`（必填）、`model`（必填）、可缺省 `reasoningEffort`、`keyPresent`（bool，必须 true）、可缺省 `catalog`。无调用方可选 provider table ID/local-auth policy/任意 auth placement；这些由本合同固定。
- `catalog` 的成员仅有非空有序 `models`。每项必填 `model`；可缺省 `displayName/contextWindow/supportsParallelToolCalls/inputModalities/baseInstructions/reasoningLevels/defaultReasoningLevel`。用明确 presence 类型区分 absent/false/0/空集合，不用零值或 JSON null 猜测。typed 实例深 clone 所有 slice/pointer，输出与 caller 不别名；caller 在调用期间仍不得并发写同一输入。
- JS hook 只收到已校验/深复制的 desired（含 presence bool），不含 envelope、owner、asset、OAuth、本地 baseline 或 secret 明文；返回同第 5 节固定 declaration schema。Go host 使用自己的规则由 desired **独立重算全部 expected declaration**，逐字段/次序/数量比较后构建返回值；只校验插件“长得像”不够。不得把原始 JS object 直接回传或继续执行。

## 4. 输入/目录 bounds 与 presence 合同

全部上限为闭区间，长度按 UTF-8 bytes；所有输入必须合法 UTF-8。非正文字符串不得含 Unicode control、首尾空白、NUL；**不 trim、不大小写折叠、不自动补默认值**。未知 enum/成员、重复 JSON key、null、错误 JSON 类型拒绝；标准 typed 调用没有 raw extras 入口，若测试/内部编码引入 JSON 必须逐层 strict。

| 字段 / 累积项 | 本 A 合同 |
|---|---|
| endpoint | 1..2048 bytes；绝对 `https` URL，有 hostname、合法端口；无 userinfo/query/fragment/反斜线。原字符串保留，不追加 `/v1`、不探测 DNS/网络、不改路由。HTTP（含 loopback）本片 unsupported；后续本地服务例外需单独合同。URL literal 不承载 credential。 |
| model / catalog model | 1..128 bytes，ASCII `[A-Za-z0-9][A-Za-z0-9._:/-]*`；大小写敏感。此为本片收窄，不声称 Codex 全部 ID 必然如此。重复 model 拒绝，不去重吞数据。 |
| reasoningEffort / reasoningLevels | 候选允许 `none/minimal/low/medium/high/xhigh/max`；不是任意新 enum passthrough。levels 缺省则不覆盖；存在必须 1..7 项、唯一、有序，保留用户顺序。defaultReasoningLevel 若存在则 levels 必须存在且包含它；不猜最高档或借 upstream 模板补值。 |
| catalog/models | catalog 缺省=不声明目录变更；null/存在空列表非法。存在时 1..32 models，顺序严格保留；主 model 必须在该列表；若主 reasoningEffort 和该 model 的 levels 同时声明，前者必须在 levels 中。无 catalog 时只校验模型语法，不声称已证实 provider 支持该模型。 |
| displayName | absent 保留 absent；存在 1..256 bytes。 |
| contextWindow | absent 保留 absent；存在为精确整数 `1..2147483647`，不是字符串/浮点。上游 string/number 导入转换属于未来 importer，不在 A 中静默转换。 |
| supportsParallelToolCalls | absent 与显式 false 不同，不默认为 true/false。 |
| inputModalities | absent 与显式集合不同；存在时 1..2 个唯一有序成员，仅 `text/image`；其他 modality 本片拒绝，不忽略未知项。 |
| baseInstructions | absent 保留 absent；存在 1..2048 bytes，允许原样 LF/CRLF/TAB，拒绝其余控制字符/NUL，不 trim；是私有正文，不能作为 safe-to-log 字段。 |
| 累积编码 | desired deterministic JSON ≤48 KiB；其中 catalog 编码 ≤32 KiB；带 envelope 的 request 编码 ≤64 KiB；host 接受的 declaration 编码 ≤64 KiB。逐项满足不豁免总量。禁止截断或丢条目。 |

absence 是“未指定该 override”，不是“写空值/清空本机”。A 的目录是 typed **声明**，不是已经生成的 Codex catalog JSON；不复制 cc-switch 的默认模板、供应商能力推断、neutral base instructions 或现有账号信息。B 的 catalog renderer 必须另签实际 native schema/缺省模板与 pin parity：若运行目标需要某必填字段而声明/已签模板无法提供，必须拒绝 materialize，不能猜值。该限制不阻碍 A 的确定映射测试，也不能掩盖为 A 已可安装。

## 5. 输出、最小 ownership 与单 secret slot

`CodexProviderDeclarationV1` 只含版本、target、固定 `providerID=cognate`、固定 `wireAPI=responses`、原样 endpoint/model/可选 reasoning/可选 catalog、`credentialReference=codex.provider-key`、精确 requiredSlots 单元素列表，以及固定 `localAuthPolicy=preserve-local-login-v1`。**无**文件路径/原始 TOML/JSON、实际 key、resolved `requires_openai_auth`、installed 标志、任意 operation map、opaque extras 或 diagnostics 正文。文件编码由 B 做，授权/绑定由 C2 做；A 不越层。

这个候选 providerID 是 Cognate 自有标识，不征用 upstream 的 `custom`。后续 materialize 的最小允许映射仅为：

| 逻辑声明 | 允许的 native 落点（B/D 未来验证，A 不写盘） |
|---|---|
| providerID / endpoint / wireAPI / reference | `[model_providers.cognate]` 的固定 `name="Cognate"`、`base_url`、`wire_api="responses"`、`experimental_bearer_token`（仅 Consume 后解析 reference）；同表 `requires_openai_auth` 仅按第 6 节 policy 解析 |
| provider 选择 / model / reasoning | 顶层 `model_provider="cognate"`、`model`、声明存在时的 `model_reasoning_effort`；缺省 effort 不要求删除现有值，遇不兼容现值必须 preflight 拒绝/另行预览，不能悄悄继承后声称已验证 |
| catalog 存在 | 逻辑 sidecar ref `cognate-model-catalog.v1`；B/D 后续仅可将其解析为受控 Codex root 下 `cognate-model-catalog.json`，并设置 `model_catalog_json`。ref 不是可传路径；root/override/链接验证不是 A IO |

不拥有 review_model、plan_mode_reasoning_effort、agents/subagent、memories、web_search、context/compact/verbosity 顶层项，也不拥有 approval/sandbox/permissions/MCP、其他 provider 表、legacy 顶层路由/key、auth.json。不能为“清理旧认证”顺手删除它们；若这些既存字段与新路由冲突，D 明确拒绝并要求后续能力/新预览，而非 A 发越权 remove。

- `cognate` 表/sidecar 已存在，不等于属于 Cognate。D 仅允许 absent 或已由可信 journal/基线证明是本能力管理的结构；foreign/无法确认/未知 actionable 字段冲突时拒绝，不 merge 接管。切换当前 provider 必须完整预览/明确同意，不能凭表名覆盖 current。
- foreign `model_catalog_json`/外部目录默认保留且不读取上传；有本片 catalog 声明时不得自动覆盖 foreign pointer。需要另行显式授权的替换流程；A 不提供该许可。catalog 缺省不改 pointer，也不宣称 foreign 目录已验证支持当前 model。
- 唯一逻辑槽 **`codex.provider-key`** 是代表已接受的名字，不分配 native proto 编号。上游 APIKEY 输入与 bearer 落点不是两份秘密。A 只接 `keyPresent`；requiredSlots 必须正好该一项，keyAbsent→`ErrInvalidRequest`，不能转为 None、env/header、官方登录。
- C1 后续复用现有 mutation：KEEP 仅在当前资产已有该槽时保持；REPLACE 必须非空并受秘密层既有 bounds；CLEAR 可保存一个缺 key 的非就绪资产，但 **不可成功 plan/apply**，不得清空后自动切官方 auth。创建 KEEP 无既存 key 也不 ready；copy 不带 secret 后同样不可 plan。A 本身不接 mutation/value、不改资产；这是 readiness 接缝，不授权 C1 实施或新秘密 bounds。
- 未知源 extras 仍为 encrypted/private，只有摘要，不能插入 A desired；未知目标本地 bytes 归 B 保留，不上传。未知 auth/可执行节点不能借 opaque preservation 得到 apply 许可。

## 6. 未解析 local-auth policy 与无环 preflight 接缝

`preserve-local-login-v1` 是受限声明，不是 caller 给任意 bool：provider bearer 为自己的请求凭据；未来只在可信本机状态可判定时解析同表 `requires_openai_auth` 为“操作后是否有受支持本地登录”。不读取、导出或删除 OAuth；A 无本地探测、无 session/account 参数、JS 无钥匙串能力。该行为来源是固定 upstream 逻辑，不是跨 Codex CLI 版本实测承诺。

未来顺序必须是 **现有 Begin/Bind → Prepare safe declaration → D-preflight → 完整 intent → Authorize → Consume → D-publish**：

1. D-preflight 不依赖 Consume secret。先使用可信本地能力判定 auth-store 模式/可判定状态，验证 root/table/catalog ownership、全部目标基线与 parser 支持，生成受限 policy resolution；unknown/error/不支持的 keyring 或 auth-store 状态必须拒绝，不能当成 missing。此 spec 不授权实现这些本机读取，也不让 A 查询 OAuth。
2. preflight 把 policy 原值、解析结果、所依据的本地状态 witness/各 artifact 基线和完整计划纳入完整 intent；C2 Authorize/Consume 在既有 proof 模型下绑定 owner/origin/asset envelope CAS revision/adapter digest/selection/intent，且 native pin 必须独立校验与 server 一致。**不能**先 Consume 再发现解析结果不匹配、偷偷重解，或把 D-publish 对 C2 的依赖倒过来当 preflight 依赖。
3. publish 前重验 witness/baselines；变化→冲突/重新显式 preview，不静默改 bool、路径或 patch。不支持的平台/无法可信判断的状态给明确 unsupported；不得由“磁盘存在 auth.json”或 keyPresence 假设登录成立，auto/keyring unknown 尤其不能 fallback file。
4. A 只冻结 policy identity/声明 shape；具体 witness 编码、canonical 扩展、resolved intent encoding、原子重验接缝/多 artifact journal 在 C2/B/D 独立合同与兼容向量签交前**没有**获 apply 能力。proof 是持钥证明非 attestation，不证明客户端忠实执行 preflight；不新增认证、通用签名框架或 server descriptor API。

## 7. Host validation、错误、隐私与生命周期

- 先校验 ctx、envelope、各项/累计 bounds，再跑现有 `plugin.RunJSON`（当前其内部 3s deadline、16 MiB runtime memory、最大输出 1 MiB/50000 nodes），不加长该预算。A 的 64 KiB **结果接受界**不冒充 runtime 在解码前的内存界；不修改 runtime。返回前 fresh ctx gate，取消/超时为原 context sentinel，输出零值，实际 interpreter 已停止，无遗弃 worker。
- clone 后 Go host 独立重算 expected 并验证 **完整相等**：target、固定 ID、顺序、slot、policy、每个 presence 与模型隐藏字段、所有值/数量、累计输出大小、无未知或多余节点。模块输出即使合法 TOML 目标也不能扩大权限；输出 actual secret、JS 自创 ref/path、resolved bool、其他 target/capability/legacy remove 全拒绝。module 内不得解析调用方脚本/执行网络、磁盘、命令；不向其注入这些 host functions。
- 利用现有固定错误：bad desired→`ErrInvalidRequest`；plugin 运行失败→`ErrPluginExecution`；伪造/未知/越权输出→`ErrPluginResult`。不包含用户字段、URL、模型、正文、raw DTO、key、plugin exception stack 或源代码；现有 context 分类不吞。记录仅固定错误类别，**不新增 request/body/module/output logging**。
- “safe declaration”表示不含 credential value，不表示可整体 fmt/slog：endpoint/model/catalog 正文仍属私有配置。禁止日志整个 typed DTO 或插件返回物；测试错误也只打印固定 case ID/类别/布尔。无 secret wrapper 需求就不造 unused wrapper；若后来接口要接 raw secret，必须先扩审而非给 Stringer 后继续塞进 JS。
- 无 DB/事务/文件锁/网络；不在写事务内跑 JS。不对 caller 并发修改作安全承诺；Registry 只读调用可并发，结果不别名。没有任何安装/账户/后台任务副作用。

## 8. Style、commands 与 tests（未来验证要求，非本轮实跑结果）

沿用 typed request/零结果+固定 error/Registry 方法风格；Go 用 gofmt、具名非单字母标识、`encoding/json/v2` strict/deterministic 编码、深复制 slice/optional。无 arbitrary maps/extras 暴露给 consumers。JS 用 named export、纯输入输出、稳定顺序，不运行动态代码。风格示例仅为**声明片段**（不是完整返回值或 native patch）：

```json
{"credentialReference":"codex.provider-key","requiredSlots":["codex.provider-key"],"localAuthPolicy":"preserve-local-login-v1"}
```

下一实施者须先完整读 Modern Go CLI 输出；预期命令如下。**本轮只写文档，没有执行下述 Go/JS 测试，也没有 plan/实现。**

```sh
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go version
CGO_ENABLED=0 go test ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin -count=1
CGO_ENABLED=0 go test ./internal/server/agentadapter -run '^TestCodexProvider' -count=20
CGO_ENABLED=0 go vet ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin
CGO_ENABLED=0 go test ./internal/configclient ./internal/nativesessionauth -count=1
git diff --check
```

`TestCodexProvider*` 是未来新增 test 命名约定，未创建；不得以“no tests to run”算验收。现有 Claude registry/render/patch、native client/pin/proof 回归必须仍真实运行；原 Claude definition/module/digest 与 canonical vectors 不变。无 deps 不跑下载、更不修改 GOSUMDB；不需要 npm/真实 HOME 或启动 upstream。CGO0 不跑要求 CGO1 的 race，不能伪称 race 已验。

| 验收组 | 必需真实 oracle / 负控 |
|---|---|
| Registry / identity | Builtins list 的新身份/digest 与模块真实一致；wrong version/digest/target/重复或额外 cap 拒绝；旧 Claude/gemini 行为不变；generic Render 无 Codex 权限。 |
| 映射 / parity | 实际 moejs 跑 custom Responses；Go 独立 expected 比较全结果；相同输入结果稳定；caller mutation-after-return 不改结果。B/C1 无需 server 实现依赖即可使用 neutral 数据。后续客户端无 JS pin parity 是 C2 验收，不假称本 A 已交付。 |
| bounds / presence | 逐项 min/max/+1 与累计 +1；UTF-8/control/URL/auth 分支；absent vs false；空 catalog/重复模型/invalid reference/unknown enum/hidden 字段完整性；不同 field order 不改变语义，ordered lists 不被排序。 |
| 恶意 module | 以受控 registry definition/真实 moejs 模块改 target/slot/ref/policy、注入路径/extra、掉隐藏字段、改模型默认/顺序、偷添 literal secret、输出 resolved bool，逐项 host 拒绝；不能只 mock expected。模块返回合法形状但值与 desired 不符必须 RED；关闭 host 比较的临时负控必须能使测试失败。 |
| key / readiness | keyPresent=false 真拒绝，不 fallback；单槽输出严格；仅 presence 进入 JS 的真实 hook witness。KEEP/REPLACE/CLEAR/copy 的 A 侧 readiness fixtures，无 asset 保存宣称；合成 secret sentinel 不进入 JS 输入/输出/错误/log。 |
| policy / ownership | 不因任何本机登录推测产生 bool；拒绝 caller policy/providerID/path；foreign/global 节点无生成操作。D-preflight/intent witness 的实现未交，测试应明确“不可安装声明”，不是模拟消费来勾掉 D。 |
| ctx / privacy | 预取消、运行中 cancel、无限循环超时、返回 gate；真实 fmt/slog/错误捕获不出现合成 secret；JS 主动抛含私有输入文本仍只固定错误。全用合成数据，若需磁盘仅 TempDir，不读 HOME。 |

## 9. 边界与设计签收门

**Always**：独立设计审通过后才写 plan；精确小写集；复用现有 Registry/runtime；先 TDD 真红绿，再交 manifest/源码与实际日志；保持 unknown fail closed/secret 不进 JS/host 独立验证。

**Ask first**：任何第 9 路径、module/库、runtime limits、schema/proto/encoding/权限扩张、HTTP endpoint、local filesystem/keyring、catalog native 模板或 unsupported auth 增量。bounds/ID/最小字段如因明确反例不足，先列反例交设计审，不能边实施边扩大。

**Never**：本轮 plan/Go/JS/proto/依赖修改、stage/commit/push/spawn；读真实 HOME/用户配置/OAuth；上传 account、明文 secret 到 JS/Web/MCP；把 plugin 输出当授权、任意 raw JSON 当 typed 能力；弱化现有 proof/strict codec/Claude pin；称 A 已安装、CLI/MyGo 已用、F12 已完成。

**本 spec 需独立审签的细节**：第 3 节精确 neutral schema/候选身份，第 4 节 bounds/presence，第 5 节 owned ID/sidecar ref/最小字段，第 6 节无环 policy 接缝，第 8 节真实 oracle。未冻结部分明确留给 B/C1/C2/D 各自 spec，不以待定占位伪造可安装能力。代表五项裁定与完整 F12 目标不重开；未获本 spec 签交前停止于文档。

## 源码事实索引与限制

- Cognate 固定 `34c22a1`：`internal/agentconfig/types.go`；`internal/server/agentadapter/{provider_patch.go,registry.go,builtins.go,builtins.json,render.go,errors.go}`；`internal/server/plugin/runtime.go`。A8 是新设计候选，不是已有 Codex 实现。
- cc-switch 固定 `6d6b716`：`src-tauri/src/live/project/codex.rs:9`（custom key 落点注释）、`:65`（auth 分支）、`:78`（local-login policy）、`:259`（declared_auth）、`:341`（row_key）、`:373`（拒 keyless fallback）；`services/provider/codex_direct.rs:333` 路径前缀同 `src-tauri/src/`（file/keyring/auto）；`live/floor.rs:120` 同前缀（非 Cognate 全量 ownership 许可）。
- 目录实际字段见 cc-switch `src/types.ts:280`、`src-tauri/src/codex_config.rs:1420`、`:2735`，表单 hidden-field preservation 见 `src/components/providers/forms/hooks/useCodexConfigState.ts`。上游有默认补值、容错丢条、string/number 兼容；本 A 的 strict/presence/bounds 刻意不照搬。原生 Responses 与 proxy conversion 区别见 `src/components/providers/forms/CodexFormFields.tsx:200`。
- 所有上游版本/行为描述仅源码事实，不是 Codex CLI/OS/keyring 运行结论；没有真实 OAuth、TLS/native apply、跨文件恢复、MyGo/14-agent 验收证据。
