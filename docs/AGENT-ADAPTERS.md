# Agent 适配插件边界

状态：首个 bounded 切片已实现内置 Claude/Codex/Gemini 元数据 registry 与 Claude settings JSON 的纯 env additive merge。Codex/Gemini 仅 metadata，配置导入、客户端应用和恢复仍未接通。此文不代表完整 provider switch 或 cc-switch 交互已交付。

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
| Claude | `claude.settings` | `json` | `claude.provider-env.v1`，仅显式 env 更新 |
| Codex | `codex.config` | `toml` | `ErrUnsupported`，无 TOML 渲染 |
| Gemini | `gemini.settings` | `json` | `ErrUnsupported`，无 Gemini 渲染 |

内置 metadata 来自 `builtins.json`，与调用者提供的第三方 `Definition` 都经过 `NewRegistry` 的同一验证。`ParseDefinition` 严格解码 JSON；未知字段、未知 agent/target/format/capability、无效版本、重复 ID、路径形式的 ID 和越限模块均拒绝。提供第三方 definition 是受信宿主的显式操作，不是开放 RPC 上传 JS 或插件商店；注册时不运行模块，语法或 hook 错误在 Render 阶段脱敏失败。

### Go 接口

- `Builtins()` 返回上述三个内置 descriptor 的 registry。
- `ParseDefinition(data)` 与 `NewRegistry(definitions)` 用于受信宿主注册自定义转换模块；不是客户端 API。
- `Registry.List()` 返回排序且隔离可变 slice 的 metadata 与 definition digest，不返回 JS。
- `Registry.Render(ctx, request)` 返回 `RenderResult`；`RenderJSON` 对 request envelope 严格解码后执行相同流程。

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
- 固定分类错误不包含输入、解析器细节、JS 异常或秘密。输出内容本身可能含秘密，调用者不得记录完整 request/result；本切片没有加密存储、secret slot、遥测或日志接入。

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
