# Codex CX-PROVIDER-PLAN / A Implementation Plan

> **For agentic workers:** 在主线程另授源码后，使用 `executing-plans` 技能由同一 owner 串行执行下列 checkbox；不得 spawn。本轮仅提交计划供非作者设计审，不实施、不 stage/commit/push。

**Goal:** 实现确定、有界的 custom 原生 Responses/provider bearer 安全声明 planner；secret 不进 JS，host 独立重算并验证，不产出完整 install patch。

**Architecture:** neutral typed desired → Registry 身份/预算校验 → 现有 moejs 纯 hook → host 全字段 expected oracle → 自有深复制声明。固定 unresolved local-auth policy 留给未来 D-preflight，后者在完整 intent/C2 授权之前，不形成消费秘密的环。

**Tech Stack:** Go1.27.3 / CGO0，现有 `encoding/json/v2`、moejs v0.1.0-alpha.11、`plugin.RunJSON`；不加库或改 module/runtime。

**Spec:** `docs/superpowers/specs/2026-10-29-codex-provider-planner.md`，SHA256 `a210da792d9487200fa3616e6d7abb58ef7ec81f139af17db646595f9e1d981e`；map SHA256 `d6691bd0da4a3e9907ad4f51370d406826af2025af03359c623c74ca018992bd`。Lovelace 独立设计 ACCEPT receipt `codex-provider-planner-spec-review.md` SHA256 `d506bc4d823d75c68d172e1879568bb778bef44b4d1fb14fc758690279ec16ea`；不把此设计接受当运行 PASS。

**Fixed base:** `d40503e2dde3c0874a3e417904c3cc302fbc78c4`。已核 agentadapter/agentconfig 相对 spec 基线 `34c22a1` 无 diff；map 的 upstream 事实仍固定 cc-switch `6d6b716`。主线程随后提交 map/spec/plan 后，源码授权若以新 base 发出，只能在核实这两个源码目录未变后更新 manifest base，不将其它 WIP 偷带进候选。

## Global Constraints

- 五项裁定不回退：custom native Responses、单槽 `codex.provider-key`、声明式 unresolved policy、D-preflight→完整 intent→C2 授权、最小 ownership/foreign 保留。无新 auth/proto/pin API。
- 身份为 `builtin.codex@1.1.0`、target `codex.config`/TOML、唯一 `codex.provider-plan.v1`；请求绑定注册的 version/digest/target/精确 capability。只修改下述 8 路径，不能追加第 9 个文件。
- 所有 bounds/presence/ownership 以 spec §§4–6 为准，不放宽既有 Claude/runtime；desired/catalog/request/declaration 累积接受界分别 48/32/64/64 KiB。既有 runtime 3s/16MiB/1MiB/50000 nodes 不变，64KiB 不是预解码分配界。
- secret/reference 不混淆：JS 只接 presence，不接 raw key、owner/asset、本地 bytes、OAuth；endpoint/model/catalog 正文也不能整体日志。全合成、无真实 HOME/账号/配置/upstream execution；如测试要落文件仅 TempDir。
- A 非 install patch，不运行网络/磁盘/命令、无 DB/锁/事务。完整 provider 需 A+B+C1+C2+D；MCP/prompt/其它认证/代理/failover/Stack/全部14-agent/F12仍待各片，不减目标。
- 不改 spec/map/旧 Claude planner/renderer、plugin runtime、native proof/client、proto/gen、CLI/MyGo、K/C1/B1c、schema、依赖、CI；需要范围外先报。

## Review Focus

1. JSON optional 的“有成员但 null/空对象”不能被 typed 零值擦除；Task 2/3 以真实 JS object own-key 与 exact-member oracle 拒绝。
2. 同形合法输出偷偷改变 model/目录 hidden presence/顺序也属越权；Task 2/4 的独立 expected 与坏模块/关闭比较负控必须命中。
3. ctx 在执行前、无限循环中或结果提交前取消：固定 sentinel+零声明、interpreter 实际退出；Task 2/4 不以全测试 timeout/遗弃 goroutine算证明。
4. 注册 Codex cap 不能连带开放 generic Render 或放松 Claude/metadata定义；Task 3 覆盖老 descriptor/module/digest 与 renderer 回归。
5. 一次调用后改 input/output 指针/slice 不能影响另一结果；Task 1/2/4 覆盖深复制、累计超限和反复/并发只读调用，不能靠 caller 自觉替代返回值隔离。

## 1. 精确路径与串行 ownership

| 路径 | Task / 责任 |
|---|---|
| 新 `internal/agentconfig/codex.go` | T1 neutral types、常量；无 server/client/IO import |
| 新 `internal/server/agentadapter/codex_patch.go` | T2 校验/clone/expected/strict output 与方法；不得改 `provider_patch.go` |
| 新 `internal/server/agentadapter/codex_patch_test.go` | T1–T4 全部新字段/host/真实 hook/security tests |
| 新 `internal/server/agentadapter/codex.mjs` | T3 纯 named hook |
| `internal/server/agentadapter/{builtins.go,builtins.json}`（2路径） | T3 embed/manifest/module 分派 |
| `internal/server/agentadapter/{registry.go,registry_test.go}`（2路径） | T3 精确允许分支与身份回归 |

8 路径由一个 owner 串行持有。先交 neutral types+独立 host oracle 的 review seam，再串接 module/registry；不得为“并行”让 B/C1 并写这些文件。没有 `internal/agentconfig/codex_test.go` 的第九路径：neutral 契约测试放在已授权 `codex_patch_test.go`。本 plan 自身不是未来 A8 源清单的第九生产文件；随文档批单独提交。

## 2. Neutral Go shapes / presence 决策

以下是**计划中的声明草图**，仅存在本文，不创建 Go 源码；命名须在下游 tasks 一致，不偷偷换成 map/raw JSON。公共类型全部位于 `internal/agentconfig/codex.go`。

| 类型 | 精确成员（JSON 名为 lowerCamelCase，已有 envelope APIVersion 名沿既有风格） |
|---|---|
| `CodexAuthMode`, `CodexReasoningEffort`, `CodexModality`, `CodexSecretSlot`, `CodexLocalAuthPolicy` | 各为 distinct string type；enum 只接受 spec 集合；局部 `const` 不修改 `types.go` |
| `CodexCatalogModelDesired` | `Model string`; `DisplayName *string`; `ContextWindow *uint64`; `SupportsParallelToolCalls *bool`; `InputModalities *[]CodexModality`; `BaseInstructions *string`; `ReasoningLevels *[]CodexReasoningEffort`; `DefaultReasoningLevel *CodexReasoningEffort` |
| `CodexCatalogDesired` | `Models []CodexCatalogModelDesired`，必填、非空有序 |
| `CodexProviderDesired` | `AuthMode CodexAuthMode`; `Endpoint string`; `Model string`; `ReasoningEffort *CodexReasoningEffort`; `KeyPresent bool`; `Catalog *CodexCatalogDesired` |
| `CodexProviderPlanRequest` | 既有 envelope 形状：`APIVersion int`, `PluginID/PluginVersion/DefinitionDigest string`, `Target Target`, `Capabilities []Capability`；`Desired CodexProviderDesired` |
| `CodexProviderDeclarationV1` | `APIVersion int`; `Target Target`; `ProviderID/WireAPI/Endpoint/Model string`; `ReasoningEffort *CodexReasoningEffort`; `Catalog *CodexCatalogDesired`; `CredentialReference CodexSecretSlot`; `RequiredSlots []CodexSecretSlot`; `LocalAuthPolicy CodexLocalAuthPolicy` |

必填成员不得 `omitempty/omitzero`：例如 keyPresent=false 仍是 false，随后判不 ready；可选 pointer 用 `omitzero`。nil pointer=absent，非 nil `*bool(false)`=显式 false；非 nil list pointer 到 nil/空 slice **非法**，不能序列化 null 来伪造“未指定”。深复制两层 catalog、每个 optional pointer、optional lists、models 与 requiredSlots；不排序/trim/默认补值。`DefaultReasoningLevel` 不是全局 `ReasoningEffort`，不得复用一个字段吞掉其一。

无任意 decoded-input API：主入口接 typed request。若内部测试/模块结果采用 JSON，逐层 unknown/duplicate/null 规则仍须验证；Go typed struct 无法保留的 unknown 不得被误称已检测。typed API 不接受 credential value、providerID、caller policy 或文件路径。目录 hidden 字段 private 不等于 safe-to-log。

## Task 1: neutral contract 与数据约束（小 seam）

**Files:** 新 `codex.go`、新 `codex_patch_test.go`；所有路径前缀见 §1。

**Consumes:** spec §§3–5。**Produces:** 上述 neutral types/constants，供 T2/T3/B/C1；无注册/HTTP/存储副作用。

- [ ] 先完整运行/阅读 Modern Go CLI `list --go-version 1.27`，核 `go version` 为 go1.27.3、CGO0；读现有 `types.go`、`provider_patch.go`、`registry.go`、`builtins.go` 与 runtime，不扩大扫描。记录固定 base 与旧 Claude module/manifest/digest fixture。
- [ ] 在 `codex_patch_test.go` 写 `TestCodexProviderPresenceEncoding`：absent optional 不出现、false 出现且 false；有序两模型/levels/modalities 不变，八目录字段全保留。写 `TestCodexProviderNeutralSchema`：request 无 base/raw secret/owner/policy 输入字段，声明无 resolved bool/raw bytes。初始缺类型是 author TDD 编译 RED，明确标为编译缺失，**不是**后续安全 oracle 行为 RED。
- [ ] 仅在 `codex.go` 定义上述 types/constants/tags；所有新功能常量限此文件，复用现有 `APIVersion/CodexConfig/TOML`。再次运行 T1 测试，必须编译成功并真实执行/通过；交 neutral seam 给 B/C1 的时间点只在设计/源码授权后。

**Command:** `CGO_ENABLED=0 go test ./internal/server/agentadapter -run '^TestCodexProvider(PresenceEncoding|NeutralSchema)$' -count=1`。期望确有两 test 的执行记录，无 no-tests 假绿。

## Task 2: host oracle、边界与调用生命周期

**Files:** 新 `codex_patch.go`、`codex_patch_test.go`。

**Consumes:** T1 types。**Produces:** `Registry.PlanCodexProvider(ctx context.Context, request agentconfig.CodexProviderPlanRequest) (agentconfig.CodexProviderDeclarationV1, error)`；内部小函数：`cloneCodexDesired(desired) desired`、`validateCodexDesired(desired) error`、`expectedCodexDeclaration(desired) declaration`、`validatedCodexDeclaration(output map[string]any, expected declaration) (declaration,error)`、`finalizeCodexDeclaration(ctx context.Context, output map[string]any, expected declaration) (declaration,error)`（这里 desired/declaration 是 T1 完整类型的文档缩写，不定义新的别名）。finalizer 是生产方法处理真实 module 结果的唯一最终出口，执行 strict validation 后 fresh ctx gate，再返回自有声明；不是测试 callback。

- [ ] 先写 `TestCodexProviderDesiredBounds`、`TestCodexProviderReferences`、`TestCodexProviderKeyReadiness`、`TestCodexProviderOwnership`。为每个 spec bounds 编写 min/max/+1 及累计超限；主模型必需、catalog主模型 membership、default level membership、effort membership、duplicate/case-sensitive ID；KEY false/error全零/requiredSlots严格一项，未拥有节点无声明。暂用包内 expected/validation 直接测 host，不依赖未实现 Registry 注册。
- [ ] 实现固定输入规则：auth 只 `provider-key`；endpoint HTTPS 1..2048、无 userinfo/query/fragment/反斜线/非法端口；model ASCII 1..128；levels 七 enum且 unique 1..7；目录 1..32；displayName 1..256；window 精确整数1..2147483647；modalities text/image唯一有序1..2；baseInstructions1..2048仅允许 LF/CRLF/TAB 的正文控制符。UTF8/首尾空白及文本字段规则逐项按 spec。null/空集合/0不能偷偷转 absent。
- [ ] 校验逐项与累计 deterministic JSON 大小，先有限输入验证再深 clone；request≤64KiB、desired≤48KiB/catalog≤32KiB，候选 accepted declaration≤64KiB，任一超限返回固定 error/零值。测试必须构造“每项合法但累计+1”的真实多条目录；禁止缩短 fixtures 后假称已验上界。数值 token 表示与 JS Number 区分：typed ContextWindow uint64无小数输入；模块 JS Number 必须有限/非负/整值/范围内并与 expected 精确相等，**不四舍五入/强制截断**，2^31界内无精度损失借口。
- [ ] 写 `TestCodexProviderHostExactMembers`、`TestCodexProviderHostExpectedParity`：使用输入 map 测遗漏必需成员、任何层额外空/null/未知成员、数组换序/hidden字段丢失、wrong type/ref/policy/providerID/target、resolved bool、过大结果。先运行原尚未验证实现达到**编译后断言 RED**；实现 own-key/数量/类型/完整值比较并返回自己构建的 expected clone；不只 Unmarshal struct 后比零值，不让原 map/JS对象出门。
- [ ] 实现方法按顺序 ctx→注册 envelope gate→desired bounds/clone→host expected→JSON→`plugin.RunJSON(ctx,module,"planCodexProvider",input,&output)`→`finalizeCodexDeclaration`严格验证/fresh ctx/返回。注册 gate 与现有 private helper风格一致，精确 caller identity/cap，不以 owner/target string 代替 digest；非 context 执行错映射 `ErrPluginExecution`，坏输出 `ErrPluginResult`，坏输入 `ErrInvalidRequest`。固定错误不得 wrap plugin exception/用户字段。
- [ ] 写 `TestCodexProviderDeepClone`：输入调用后 mutation 和返回对象 mutation不影响另一次调用或 baseline，optional list/pointer全覆盖。不要在一个调用内部并发改同一输入制造 data race后要求支持；并发测试仅只读 request/immutable Registry。

**Command:** `CGO_ENABLED=0 go test ./internal/server/agentadapter -run '^TestCodexProvider(DesiredBounds|References|KeyReadiness|Ownership|HostExactMembers|HostExpectedParity|DeepClone)$' -count=1`。T2 在受控 definition 注册尚不可用前，方法的完整集成由 T3 补齐，不签“方法已可用”。

## Task 3: 真实 module 与精确注册串接

**Files:** 新 `codex.mjs`；改 `builtins.go`、`builtins.json`、`registry.go`、`registry_test.go`；扩 `codex_patch_test.go`，需要时仅补 T2 文件。不新增其它路径。

**Consumes:** T1/T2。**Produces:** Builtins 中可执行该 planner（非 generic renderer）的精确 definition/digest，真实 module/host parity。

- [ ] 先写 `TestCodexProviderBuiltinsIdentity`（registry_test.go）：manifest精确升为1.1.0/单cap、module非空/digest真实；错版本/摘要/target/多cap/重复cap/无cap 的请求拒绝；旧 Claude与gemini manifest/module行为不变。现有 Codex1.0.0 metadata路径不能通过 Plan。写 `TestCodexProviderRenderStillUnsupported`，保持既有 Render 对非Claude target拒绝。
- [ ] 运行这些 tests 记录 compile0/断言 RED；随后更新 Builtins embed/仅Codex module分派、builtin Codex manifest与 `validDefinition` 的精确 Codex agent/target/TOML/single known cap/nonempty module 分支。保留既有 metadata-only定义规则与Claude分支，不泛化成“任意 target可非空cap”；不修改 Render/Claude code来让测试勉强过。
- [ ] module 只 named export `planCodexProvider`，将校验过 desired映射为第5节精确声明，不 normalize/推断/默认补值；只有传入的 presence与固定 ref，无 credential bytes。不能 import服务/文件/网络或 eval。host仍自己计算预期，不能通过执行 JS 再把相同输出当 expected。
- [ ] 写 `TestCodexProviderRealMoejsParity`：真实 Builtins、真实 RunJSON，多目录含全 hidden字段/显式 false/absent、无catalog两个分支，完整expected逐字段比对且encoded≤64KiB。加入 `TestCodexProviderUnresolvedPolicy`：始终仅固定 policy identity，没有 bool、路径、readiness-as-installed；空key真拒绝，不fallback/篡改auth.json。
- [ ] 复跑 Task1/2全部与旧 adapter tests；新增 cap导致既有 metadata-only tests变化只能通过合法原契约，不删/弱化旧断言。若发现必须动第九路径或未授旧测试，停止报告。

**Command:** `CGO_ENABLED=0 go test ./internal/server/agentadapter -count=1`；要求新case与旧Claude/Render/metadata均实际执行，无skip替代。

## Task 4: adversarial/cancel/privacy + 编译后有效负控

**Files:** 仅 `codex_patch_test.go`、必要 `codex_patch.go` 内修复。临时独立 mutants/命令/证据放 `/tmp` 或 ignored release，不能借测试新增生产 seam/第九路径。

- [ ] `TestCodexProviderMaliciousModules` 用真实 `NewRegistry` 注册受控定义（可沿用builtin ID/版本但 module与digest真实对应），调用真实 Plan/RunJSON；模块依次增加path/raw bytes/secret literal/unknown空或null、变policy/requiredSlots、错target、漏hidden/多字段/篡改default/顺序/合法形状错误值，所有 variants要求精确 `ErrPluginResult`+零结果。固定错误不能打印或包含模块输出。
- [ ] `TestCodexProviderPluginBoundary`：实际 hook witness 逐个确认输入只有 desired成员及keyPresent，无 envelope/key/local-state；运行 `throw` 含合成私有正文、恶意 credential sentinel 输出、missing hook/无效module与巨量输出。错误只固定分类，拒绝回显；synthetic key从不放入生产 request字段来“证明”接口接收秘密。
- [ ] `TestCodexProviderCancelLifecycle`：预取消、真实执行中循环+cancel、现有实际3s无限循环上限、结果fresh gate取消；用真实 ctx/真实 runtime退出确认无pending worker，不把总测试超时当行为oracle，不加短runtime预算或goroutine假返回。post-result 子例先以真实 RunJSON 得到有效输出，再 cancel 同一 ctx，再调用生产 `finalizeCodexDeclaration`，要求 context.Canceled+零声明；这是确定性白盒阶段测试，另源码核实 Plan 确实经同一 finalizer 返回，不假称人为安排了整个公开方法内的调度窗口。不导出测试callback。ctx检查/return非原子，不承诺取消绝对不发生在最后检查之后。
- [ ] `TestCodexProviderPrivacy`：顺序捕获标准log与slog，cleanup恢复不t.Parallel；合成 sentinel错误路径/no body logging，显式校验敏感私有URL/正文不出错误/日志。测试失败只固定caseID/错误分类布尔，不 `%+v` 打 DTO。`TestCodexProviderConcurrentReadOnly` 验 immutable Registry+分离输入只读并发，无共享可变结果。
- [ ] 创建 `/tmp` overlay **host-comparison-bypass**：仅跳过validated输出比较、返回expected clone；先 `go test -overlay=... -run '^$'` 成功编译，再跑 `^TestCodexProviderMaliciousModules$` 必须断言RED。**presence-collapse** mutant仅将module结果显式false与absent混为一谈，`HostExactMembers`中针对该case断言RED；**cancel-gate-removed** mutant仅去fresh ctx gate，确定性fresh-gate case断言RED。每项与恢复源码分别记录命令/源码diff/exit/日志；未compile、panic、全测试超时、未命中test都不算负控成立。
- [ ] 恢复exact源并复核8项SHA，再跑原反例为GREEN。上述作者有效负控是给后续非作者复审的材料；独立审者另行构造/验证oracle与编译后负控，不能把作者日志改称独立运行。无race-CGO1或跨OS运行声明。

**Commands:**

```sh
CGO_ENABLED=0 go test ./internal/server/agentadapter -run '^TestCodexProvider(MaliciousModules|PluginBoundary|Privacy|ConcurrentReadOnly)$' -count=20
CGO_ENABLED=0 go test ./internal/server/agentadapter -run '^TestCodexProviderCancelLifecycle$' -count=1
```

真实3s runtime上限需实际等一次；多轮快测不得冒称把全套取消预算测20次。mutation执行时完整命令须带真实overlay路径/selector/count；不要管道tee吞exit，redirect日志后保存真实退出码。

## Task 5: 作者验证、精确 freeze 与非作者交接

**Files:** 不再扩生产写集；报告/manifest/日志仅 ignored release或/tmp。本轮计划阶段**不勾选**这些步骤。

- [ ] 在源码授权后固定base+八路径重构clean candidate（优先immutable export；若用alternate index只用临时index，绝不真实index），逐blob/mode证明除8路径外与base相同。构建不依赖moving K/C1或本机用户数据；变更第九路径先报。不能用主仓全绿代替exact candidate验证。
- [ ] 完整局部回归/vet及现代Go指导全部通过；全仓需要web/dist时在隔离export正常npm ci/build，不伪造embed文件或写共享生产dist。模块无需新增，mod验证不得禁用SumDB。保存工具版本、实际命令、exit、case数、skip与真实预算；以下是未来指令，当前没有运行PASS声明。

```sh
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go version
CGO_ENABLED=0 go test ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin -count=1
CGO_ENABLED=0 go test ./internal/server/agentadapter -run '^TestCodexProvider' -count=1
CGO_ENABLED=0 go vet ./internal/agentconfig ./internal/server/agentadapter ./internal/server/plugin
CGO_ENABLED=0 go test ./internal/configclient ./internal/nativesessionauth -count=1
go mod verify
git diff --check
# 仅在隔离完整candidate且确需全仓embed时：
(cd web && npm ci && npm run build)
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go vet ./...
```

- [ ] 若overlay负控需依赖编译已缓存不影响“compile0”判定，但每个selector实际executed/fail记录不可省。既有Claude descriptor/digest/module与canonical vectors未改义；Native/client回归不能宣称新增Codex pin/六RPC已实现。
- [ ] 交付base/精确tree/八文件SHA256+mode+blob manifest、clean重构说明、真实正负控与恢复日志、≤1页author报告，注明未安装/未materialize/无OAuth/无跨平台运行/无F12完成。停止8路径写入，交主线程安排Lovelace非作者实现验收，不stage/commit/push/spawn。主线程之后是否提交另行裁定。

## 自审 / 覆盖矩阵（仅计划审，不是运行签收）

| 已签 spec 部分 | 本 plan 落点 |
|---|---|
| §§1–3 objective/真实8文件/Registry/interfaces | §1–2 / T1 / T3，exact identity与Render隔离 |
| §4全部bounds/hidden/presence/无默认模板 | §2 / T1 / T2；T3真实module、T4空/null/false负控 |
| §5owned fields/单槽/readiness/foreign | T2 Ownership/KeyReadiness、T3 UnresolvedPolicy；不生成auth.json或foreign动作 |
| §6 Dpreflight→intent→C2无环 | Global及T3声明shape；A仅policy，不实现D读盘/witness/canonical扩展 |
| §7 clone/host/context/error/privacy | T2独立expected与零错误、T4真实恶意module/ctx/log/负控 |
| §§8–9 commands/约束/不签范围 | T4/T5；不动旧vectors/模块，无JS client/安装宣称 |

自审已核类型/方法/JSON名贯穿一致；optional pointer-to-slice的nil/empty拒绝与模块实际own-key保留都有oracle；unknown值不能被struct解码擦掉；正常/恶意模块都经过真实moejs。Task1初始缺类型编译RED与Task2/4安全断言RED分开，禁止混称。八文件串行owner、边界外需重授、编译后独立负控与实际预算验收均有明确步骤。未将B/C1/C2/D的未实现接缝伪装成A交付；完整provider/14-agent/F12门禁保留。

**状态：计划已作者自审，待Lovelace非作者设计审与主线程提交文档后另授源码。所有checkbox未执行。**
