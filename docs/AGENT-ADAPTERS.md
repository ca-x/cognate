# Agent 适配插件边界

状态：内置 Claude/Codex/Gemini/OpenCode/Grok Build provider 描述符及独立 Claude MCP 描述符使用同一 registry，并各自声明已实现的服务端转换能力；旧 Claude env additive merge 保留。Grok Build 当前仅有类型化服务端 planner，尚无其资产/RPC/编辑器或本机应用。各 agent 的 Web 与客户端实际范围见 `CONFIGURATION.md`；已接通的导入和 Pack 附件元数据管理不等于配置自动分发/应用闭环。转换成功不等于设备已经应用。下文保留各历史切片的当时状态，不能以较早的待办覆盖后续交付，也不能将完整 cc-switch 交互视为已完成。

## Grok Build 类型化声明（2026-10-31）

`builtin.grokbuild` 1.0.0 仅声明 `grokbuild.provider-plan.v1`，目标为 `grokbuild.config`。官方 native-login 不带自定义模型或秘密槽；custom-model 明确区分本地 profile 和上游 model，保留 endpoint、name、responses backend、context window，可同时声明选中的 API key 存在性与环境变量名。变量名不是变量值，模块不会读取环境，也不会接收真实 key。

宿主执行真实服务端 moejs，并独立核对完整结果、字段 presence、凭据引用与所需槽；旧五个描述符及模块输入不变。未知 backend 明确 Unsupported，不静默改写。该声明不写 TOML、不删除当前默认模型表、不触达用户本机；未来清理旧表必须有已认证的托管归属证据和明确批准。完整资产加密/版本 CAS、RPC/编辑器、MCP/提示词、路由及客户端应用仍需后继实现，不代表 Grok Build 或十四类 agent 全部完成。详见 `superpowers/specs/2026-10-31-grokbuild-provider-planner.md`。

## 分工

| 层 | 职责 | 不允许 |
|---|---|---|
| 来源导入插件 | 解析用户授权的 cc-switch / Magpie / lockfile 导出，产出仓库、skill、配置候选 | 自行获取远端客户端路径、自动入库或应用 |
| 服务端 agent 插件 | 提供能力与字段描述，解析/校验/渲染指定 agent 的配置，输出声明式文件变更 | 任意客户端绝对路径、执行命令、返回可执行 JS |
| 服务端 Go 宿主 | 隔离 moejs、限时限额、授权/加密、校验输入输出、记录版本与内容摘要 | 把插件输出直接当文件写入授权 |
| CLI / MyGo 共用内核 | 本地检测、明确选择、差异/冲突检查、受控路径映射、备份、原子应用与恢复 | 加载 moejs、执行插件、悄悄覆盖用户已有配置 |

## 最小合同

- Manifest 区分 `source-importer` / `agent-adapter`，带稳定 ID、版本与协议版本。内置与第三方适配器使用同一校验入口，不允许 RPC 上传任意源码即执行。
- 目标 agent 元数据描述支持的 skill 投影、配置类型及客户端必需能力；目标使用逻辑 ID，不能使用任意文件路径。
- 配置处理保持纯转换：显式选择的配置和必要的当前文件内容作为输入，输出受限的 JSON / TOML / 文本文件结果及所需能力。不向插件暴露其它用户、其它配置或未选择的密钥。
- 客户端只接受自身支持的操作和逻辑目标。未知协议、未知字段类型、越界路径、大小超限或不支持的配置格式均拒绝；更新插件不能绕过客户端安全策略。
- 应用计划绑定 agent/plugin 版本、目标、原文件摘要和新内容摘要。客户端执行前重新检查本地状态；发现并发修改时停止并要求重新预览，不采用最后写入覆盖。
- 敏感配置默认只对所有者可见；附加到 Pack 不等于允许匿名或分享持有人读取密钥，需要单独的明确授权策略。

## 迭代与验收

先按 cc-switch 实际源码建立能力矩阵，再以 Claude Code、Codex、Gemini 的配置转换做首批垂直切片；具体格式以当前来源版本及 fixture 为准，不虚构兼容性。现有静态 skill 安装目标继续可用，逐项替换并验证后才切到插件元数据。

必须验证：真实 moejs 执行、内置/第三方同入口、无客户端插件依赖、JSON/TOML 保留无关配置、往返转换、未知目标拒绝、恶意路径拒绝、取消和资源上限、秘密不泄露、旧客户端对新能力 fail closed，以及实际 CLI/MyGo 应用和恢复。不能用设计文档或仅能构建替代运行验收。

## 已实现切片（2026-10-28）

`internal/agentconfig` 只声明共享类型、枚举和限制，没有 server、moejs 或其它包依赖。`internal/server/agentadapter` 使用现有 `server/plugin.RunJSON`；没有文件 I/O、命令执行、网络、数据库或 RPC。没有修改安装 targets。

| agent | 逻辑 target | manifest 格式 | 当前 Render |
|---|---|---|---|
| Claude | `claude.settings` | `json` | additive Render；另有七键声明式 PlanClaudeProvider，见下文 |
| Codex | `codex.config` | `toml` | `ErrUnsupported`，无 TOML 渲染 |
| Gemini | `gemini.settings` | `json` | `ErrUnsupported`，无 Gemini 渲染 |

内置 metadata 来自 `builtins.json`，与调用者提供的第三方 `Definition` 都经过 `NewRegistry` 的同一验证。`ParseDefinition` 严格解码 JSON；未知字段、未知 agent/target/format/capability、无效版本、重复 ID、路径形式的 ID 和越限模块均拒绝。提供第三方 definition 是受信宿主的显式操作，不是开放 RPC 上传 JS 或插件商店；注册时不运行模块，语法或 hook 错误在 Render 阶段脱敏失败。

### Go 接口

- `Builtins()` 返回上述三个内置 descriptor 的 registry。
- `ParseDefinition(data)` 与 `NewRegistry(definitions)` 用于受信宿主注册自定义转换模块；不是客户端 API。
- `Registry.List()` 返回排序且隔离可变 slice 的 metadata 与 definition digest，不返回 JS。
- `Registry.Render(ctx, request)` 返回 `RenderResult`；`RenderJSON` 对 request envelope 严格解码后执行相同流程。
- `Registry.PlanClaudeProvider(ctx, request)` 返回 `ClaudeProviderPatchV1`，不接收或返回本地 settings 原文，不执行本地合并/写入。

Manifest API 当前为 `1`；plugin version 使用有界 `major.minor.patch`，不接受 `latest` 或版本范围。请求必须匹配精确 plugin version、由宿主计算的 manifest+module digest、逻辑 target 和所需 capability。相同版本号的模块内容变化也会使旧请求失效。原生客户端将来必须声明自己支持的 target/操作；未知协议或能力 fail closed，不能回退为执行 JS 或任意路径写入。

`RenderRequest.Base` 是当前 JSON 的原始字节，`ProviderEnv` 是本次明确选择的更新。`RenderResult` 只有 `Target`、`BaseSHA256`、`NewSHA256` 和 `Content`；两个摘要分别绑定原始 base 与实际输出字节，不是签名、权限或已经应用的凭证。JSON helper 中的 Go byte slice 使用 JSON base64 表示；尚无新增 wire/RPC 合同。

### Claude additive merge

支持的更新键仅为 `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MODEL`。不支持删除、通用 env 编辑、JSON Patch、hooks、MCP servers、permissions 更改或 shell 模板。值必须是有界、有效 UTF-8、无控制字符的非空字符串；不进行 provider 连通性探测、URL 域名授权或密钥有效性验证。

moejs 输入只有本次显式 `providerEnv` 更新。已有完整 env、旧认证值、`OTHER_TOKEN` 等未选秘密和其它 settings 均不进入插件。插件输出只能包含固定 target 和与本次更新逐键逐值相等的 env；宿主校验后由 Go 合并回完整旧 env。插件不能借结果增改未选键或返回路径/命令。其它顶层 JSON 设置留在 Go 中，包含大于 JavaScript 安全整数范围的数值也不经 JS 转换。

只保证 additive merge：没有声明完成 provider switch。更新 API_KEY 不会自动删除现有 AUTH_TOKEN，反之亦然；双键残留及认证优先级必须等后续来源/目标/auth matrix、显式删除计划、用户确认和恢复语义。已有无关 env/settings 会保留，但不是授权执行其中的内容。

重复 JSON member、非法 JSON、非对象根、null/非对象 env、null/非字符串 env 值和未知协议字段均拒绝。已有无关 settings 字段保留但不解释；这不等于允许 request/manifest/result 增加未知指令。

### 限制与秘密

- 最多 32 个 definition；definition JSON 256 KiB；模块 64 KiB；request JSON 512 KiB。
- base 和最终 settings 各 256 KiB；最多 512 个顶层设置；原 env、最终 env 和插件输入各至多 64 KiB。
- 最多 128 个合并后 env 键，键长度至多 128 字节，env 值至多 8 KiB，显式更新最多四个白名单键。
- 运行时复用现有三秒期限、context 取消/中断、16 MiB JS 内存配置和有界结果解码。不等于进程硬内存限制、恶意插件 OS 沙箱或全局并发准入。
- 固定分类错误不包含输入、解析器细节、JS 异常或秘密。旧 Render 输出内容本身可能含秘密，调用者不得记录完整 request/result；旧 additive 接口没有 secret slot，新增 patch 仅用符号引用，没有秘密读取/传输、加密存储或日志接入。

纯函数接收 base 不意味着将来的 RPC 可以自动上传用户完整配置。远端传输须单独授权，或将来改用客户端原生合并声明式更新；当前没有新增上传或客户端读取动作。

### 实测与未交付

Go 1.27.3、`CGO_ENABLED=0`：

```sh
go test -mod=readonly ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin -count=100 -timeout=120s
go vet -mod=readonly ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin
go list -mod=readonly -deps ./internal/agentconfig
```

前两条退出 0。依赖检查只输出 `github.com/ca-x/cognate/internal/agentconfig`，没有 server/moejs。测试使用真实 moejs，包含第三方 module、无关 settings/大整数保留、恶意输出、未知字段/路径、精确版本与摘要、资源上限、取消及并发实例隔离；各测试重复 100 轮通过。

TDD 记录保存在 `/tmp/cognate-agentadapter-evidence/`：registry 缺失 metadata、未实现 Render、null env 误转换和完整旧 env 暴露分别观察到行为 red。主线程审阅提出 least-privilege 问题后，先用恶意 module 尝试读取 `input.env.OTHER_TOKEN` 并返出 sentinel，实测 red，再改成 updates-only；最终同时验证该秘密读不到、宿主仍保留它和其它未选字段。独立 overlay 重新暴露旧 env 时，该测试必须失败；另一个 overlay 把真实 Claude JS 改成返回空 env，正常渲染测试必须失败。两项均已实测，不以宿主自行合并冒充执行了正确插件。

2026-10-28 checkpoint fresh 验证：Go 1.27.3、`CGO_ENABLED=0`，上述测试重新执行 100 轮，零 fail event；隐私与 additive-auth 两项各通过 100 次。`agentconfig` 无测试文件；其依赖仍只有自身。vet 退出 0，绝对路径 Go 1.27.3 `gofmt -l` 无输出，范围内 `git diff --check` 通过。默认 `gofmt` shim 未配置版本，已改用同一工具链绝对路径检查，未修改全局环境。

本次日志位于 `/tmp/cognate-agentadapter-evidence/`：`checkpoint-focused-count100.jsonl`、`checkpoint-vet.log`、`checkpoint-shared-deps.log`、`checkpoint-gofmt.log`。fresh 两项 mutation 对照分别在隐私测试与正常合并测试得到预期断言失败（退出 1），不是编译失败：`checkpoint-leaking-input-red.log`、`checkpoint-broken-module-red.log`。`checkpoint-production-sha256.txt` 记录本切片代码快照。这些是当前共享工作区的本地包级证据，不是隔离全量、完整首发或远端 CI 签署。

后续事实基线参考 `docs/CCSWITCH-CONFIGURATION-MATRIX.md`（用户本地 cc-switch `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`、14 agents）；本片仍仅三项 metadata 和 Claude env additive merge，不代表完整 provider switch 或 14 agents 支持。此 checkpoint 停止生产写入，交主线程 alternate-index 隔离全量与独立 review；不混入配置资产/schema 写集。

仍未交付：cc-switch provider 配置 schema/选择交互、配置加密/附件、provider switch、Codex/Gemini 转换、JSONC/TOML 编辑、第三方分发与信任管理、RPC/UI、CLI/MyGo 受控路径映射、配置备份/CAS/应用/恢复，以及 Windows/macOS 原生配置验收。未 stage/commit；既有分发 fixture WIP 不在本切片写入范围。

### 独立 review P2：顶层字段上限 round-trip 修复

旧实现只检查输入最多 512 个顶层字段，未计入无 env 时新增的字段，可能生成 513 字段且无法再次 Render 的结果。现在在执行 JS 前校验 prospective final count：已有 env 不增加计数；无 env 必须为新增字段预留位置。超限返回 `ErrInvalidRequest` 和空结果，不删除或裁剪无关设置。

`TestClaudeTopLevelFieldLimitRoundTrip` 先在旧实现观察到行为 red，覆盖 511/512/513 字段与有无 env 的六种组合。511 字段的两种输入、512 字段且已有 env 均接受，逐字段保留无关 JSON（包括大整数），并验证输出再次 Render 内容与摘要稳定；512 字段且无 env、513 字段的两种输入均拒绝。把生产实现独立 overlay 回旧冻结源码后，同一测试再次按预期断言失败。

修后 fresh Go 1.27.3 / `CGO_ENABLED=0`：renderer/plugin 各 100 轮通过，零 fail event；六个边界子测试共通过 600 次，隐私与 additive-auth 测试各通过 100 次；vet、指定工具链 gofmt、scoped diff 检查通过。证据位于 `/tmp/cognate-agentadapter-evidence/field-limit-*`，包含 red、green、旧源码 overlay red 及完整 count100 JSON 日志。本次源码变化仅 `internal/server/agentadapter/render.go` 与 `internal/server/agentadapter/render_test.go`；旧冻结 `79b9252286e4e4ec2785656091036baf382bd950` 不再用于修后签收，须由主线程重新 freeze、隔离全量和 review。无 stage/commit/push，修后 checkpoint 停止写入。

## Claude provider patch（服务端声明式转换）

`builtin.claude@1.1.0` 保留 `claude.provider-env.v1`，新增 `claude.provider-switch.v1`。API version 仍为 1；Codex/Gemini metadata 不变。manifest 可以声明两个已知能力的非重复子集，第三方 definition 经同一验证；两个调用入口都检查 manifest 确实声明所需能力，switch-only 不能进入旧 Render，additive-only 不能进入新接口。请求每次只选择一个精确能力，并匹配 plugin version、definition digest、target。旧 1.0.0 或旧 digest 请求拒绝，不升级、不 fallback；1.1.0 上的 additive 调用仍只 additive，不自动删除认证。

### 共享接口与操作联合

`internal/agentconfig/types.go` 仍为无依赖的纯声明式类型。`ClaudeProviderPatchRequest` 只含既有 API/plugin/version/digest/target/capabilities 身份字段和 `Desired ClaudeProviderDesired`。Desired 包括 auth mode、optional endpoint、optional default/haiku/sonnet/opus，以及两个 secret-present bool；**没有 Base、旧 env、旧认证、secret string/bytes、任意引用或文件路径**。没有新增 JSON request helper、RPC 或 wire 合同。

`Registry.PlanClaudeProvider(ctx, request)` 调用真实 moejs `planClaudeProvider` hook。JS 只收到深拷贝并验证后的 typed desired 数据，不收到请求身份或任何机器配置。返回 `ClaudeProviderPatchV1{Target, Operations}`，不是 RenderResult；恰七项固定顺序：

1. `ClaudeEnvAPIKey` → `ANTHROPIC_API_KEY`
2. `ClaudeEnvAuthToken` → `ANTHROPIC_AUTH_TOKEN`
3. `ClaudeEnvBaseURL` → `ANTHROPIC_BASE_URL`
4. `ClaudeEnvModel` → `ANTHROPIC_MODEL`
5. `ClaudeEnvHaikuModel` → `ANTHROPIC_DEFAULT_HAIKU_MODEL`
6. `ClaudeEnvSonnetModel` → `ANTHROPIC_DEFAULT_SONNET_MODEL`
7. `ClaudeEnvOpusModel` → `ANTHROPIC_DEFAULT_OPUS_MODEL`

每项 `ClaudeProviderPatchOperation{Slot, Action, Literal *string, Reference *ClaudeSecretSlot}`。JSON 成员名为 `slot/action/literal/reference`，仅下列联合合法：

| action | slot | 必需内容 | 禁止内容 |
| --- | --- | --- | --- |
| `set-literal` | 五个非认证 slot | literal，精确等于 selected typed 值 | reference 或其它成员 |
| `set-selected-secret` | API_KEY / AUTH_TOKEN | reference 为对应 `claude.api-key` / `claude.auth-token` | literal 或其它成员 |
| `remove` | 任一受管 slot | 仅 slot + action | literal/reference 或其它成员，即使空或 null |

宿主先解实际 JS 返回成员 map，再逐项核成员存在性、顺序、数量、slot/action、literal/reference 与调用前独立 desired 快照完全等价。**不先转 DTO 再 omitzero 重编码**；额外字段即使空/null 也拒绝。修改 JS 输入不能改变宿主比较基准；缺项、重复、重排、未知成员、路径/命令和不等价值均返回零结果 `ErrPluginResult`。返回 slice/pointer 与输入及后续调用不共享可变存储。消费者仍须独立校验联合：任意手工构造的共享 DTO 不因类型名称而自动可信。

### desired-state 与错误边界

- api-key 要求 APIKeyPresent 且 AuthTokenPresent 为 false；auth-token 反向；native-login 要求两个均 false 且无 endpoint。缺必要凭据、残留另一凭据、未知 auth 或非法值均 `ErrInvalidRequest`，不返回可应用 patch。
- native-login 删除两认证；模型仍按显式 desired 映射。endpoint/models 的 nil 表示 remove，不是 KEEP；present 空串非法，不自动 trim 或归一化为 nil。
- endpoint 最多 2048 UTF-8 bytes，仅绝对 HTTP(S)、非空合法 host，无 userinfo/query/fragment/控制字符；模型各 1–256 UTF-8 bytes、无控制字符，保留空格与大小写。本地 endpoint 允许声明，但不做网络探测。
- 五个 literal 输入大小逐字段受限，JS 输入另限 64 KiB；输出沿现有 RunJSON 的 1 MiB/50000-node 解码边界，之后还必须恰七项并等价。复用现有三秒期限、16 MiB 运行时设置、父 ctx 取消/中断；不改共用 runtime，不声称 OS 沙箱或全局 admission。
- 未注册插件/manifest 未声明所需能力为 `ErrUnsupported`；精确身份/请求能力不匹配为 `ErrIncompatible`；运行时异常为固定 `ErrPluginExecution`；context 错误保留取消/超时分类。所有失败均零结果，无输入/JS 异常细节，不记录 request/result。

present 由受信调用者提供，只是转换前提；固定 secret slot 引用**不是**已验证的 owner、asset/revision、selection 或物化授权。本片不读 domain/store，不传秘密，不执行本地合并、预览确认、文件发布、恢复或 OAuth 登录。七键转换成功不能称为“provider 已切换”；未知 env/JSON 大整数的本地保留由后续原生 codec 独立实现/验收，未上传旧配置来假装完成。

### 行为证据与待独立验收

证据根目录 `/tmp/cognate-claude-provider-patch-evidence/`：先观察旧 registry/version/capability 行为 red，再观察新 API 可编译 stub 对 native-login 七项 remove、完整五 literal/一 selected-secret 映射的行为 red，随后真实 moejs green。测试覆盖三 auth × 32 optional presence 组合、所有非法凭据组合、各 literal 字节边界、旧精确身份 fail closed、实际 JS 输入隔离、输出无 alias、取消/异常/超限及 31 个恶意输出变体（包括空/null 成员和修改输入后伪造结果）。

独立 overlay 去掉宿主复核后恶意输出/输入变异测试失败；删掉真实 JS 一项输出后正常完整映射失败；恢复旧 Render 的“仅检查 capability 非空”后 switch-only 门禁测试失败。三者均为行为断言 red，不是编译失败。

Go 1.27.3、`CGO_ENABLED=0`、`-mod=readonly` 的 fresh `go test ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin -count=100 -timeout=180s` 通过，零 fail event；adapter 12.223s、runtime 10.604s。96 个 auth/presence 子测试合计 9600 次通过，31 个恶意输出子测试合计 3100 次通过；旧 additive、隐私、deadline、512-field round-trip 回归同时执行。相同三包 vet 退出 0，gofmt/scoped diff 检查通过；shared package `go list -deps` 仅自身，`cmd/cognate` 依赖图无任何 `internal/server` 或 moejs。新 Go API 没有另加网络 JSON request envelope，既有 request JSON 限制不被误称为新 wire 合同。

本次实际写集仅九文件：`internal/agentconfig/types.go`、`internal/server/agentadapter/{builtins.json,claude.mjs,registry.go,registry_test.go,render.go,provider_patch.go,provider_patch_test.go}`、本文件；`builtins.go`、原 render/boundary tests、共用 plugin runtime、domain/API/Web/native/module 与 fixture 均未改。证据目录的 `freeze-sha256.txt`、`freeze-git-blobs.txt` 和 `provider-patch.diff` 绑定本次交接内容；red/green 分别为 `01-capabilities-red.log` 至 `04-seven-slot-green.log`，三个负向 overlay 为 `06-host-check-mutation-red.log`、`07-module-mutation-red.log`、`08-render-capability-mutation-red.log`，最终重复证据为 `09-focused-count100.jsonl`。源码冻结后不继续实现、不 stage/commit；主线程仍须独立导出全量及非作者 review，本片不自签其结果。
