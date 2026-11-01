# Gemini provider 原生闭环实施授权（2026-11-01）

这是主线程在完整F12目标内的后继技术裁定，不是用户逐项原话或代码验收。
批准下方冻结HANDOFF的G1–G5及exact64整组实施；其“尚未授权”保留冻结时历史，由本节取代。
原设计SHA256 `b30931e6ed0c6a079e21a55ed390c9a10d0a78b850910405397faac149a03631`，
精确清单见同目录 `2026-11-01-gemini-native-apply-writeset.tsv`，原SHA256
`8a0b62cc334d4f18f0b5c0fc20310910104c7f64256f56d59534454056324843`。
原文除多余末尾空行外完整保留；实施新建私有archive，不改原设计、主线或他人冻结。

## 裁定与验收边界

1. **G1批准：** 采用固定官方0.61.0完整449文件包、固定Node26.13.0 Linux/amd64及source-pinned
   engine-owned Go观察器。preview不启动会迁移/写设置的官方loader，不伪造config/read接口。
   必须绑定实际dotenv搜索链、trust、workspace/system、process env、Node注入与模块解析来源；
   不能凭两份目标文件或caller witness补齐有效态。官方消费者不同意Go观察结果即资格失败，
   不通过清环境后假称原launch可用、补默认值或吞未知字段来洗绿。观察不是远程attestation。
2. **G2批准：** 原六RPC增量追加target3/slot4及独立Gemini typed variant，明确使用方案所列tag41，
   不占reserved30–39、不改Codex tag40或旧标签/签名域。旧Claude/Codex的wire、canonical proof、
   合法加密记录与AAD必须逐字兼容；新family/version仅在认证/解密后分流，混合variant全拒绝。
   Buf lint/breaking/Go生成parity必须通过；native schema仍不得进入Web生成或客户端server/moejs闭包。
3. **G3批准：** 允许对精确六provider字段做明确首次takeover，不把Foreign伪装Managed。
   默认拒绝；完整安全审阅绑定当前私有基线、具体字段/动作/clear、nonce与新计划，
   独立秘密槽及最终确认不可省。旧本地秘密不回显、不上传，不通过通用前缀接管其它字段。
   native-login只处理已批准的selector/自管清理，绝不认领OAuth/ADC/keyring。
4. **G4批准：** 仅机械提取有界bundle事务/vault原语，不复制整个Codex引擎或创建任意文件writer。
   固定角色最多3，Codex旧角色/检查点/auth Keep/0-v1-v2记录/AAD/filename/顺序不变。
   Gemini独立两角色与journal域，env先、selector后；真实两rename窗口必须显示，
   提醒停止运行/热加载Gemini，不能宣称阻止外部读者或把文件Completed说成进程已加载。
   先建旧Codex/Claude对照，再抽取；最终普通CLI真实C6消费和旧记录交叉读取必须重验，
   不能只用编译/新Gemini绿代替原核心回归。失败后事后分配FD不当作最终保留保证。
5. **G5批准为首个profile的资格前提，不缩产品范围：** 已存在可信根、已确定trusted cwd/launch、
   两文件Missing/Present、两API transport及native-login selector/既有登录原样保留。
   不自动授予workspace trust，不迁移无关旧设置；不支持输入明确拒绝，不猜测有效态。
   MyGo、其它平台/版本、MCP/prompt、Vertex/ADC与完整十四agent义务仍未完成，
   不因该profile通过就关闭整个Gemini/F12或GA条目。native-login的auth-required或OAuth marker
   保持只证明未接管/未沿用旧API-key，不冒称真实已登录请求、账号刷新或供应商资格。

## 完整交付要求

交付必须包括服务端真实moejs/Go oracle、原生授权及逐槽批准、共享客户端内核、
普通控制TTY完整差异/takeover/最终确认、双文件CAS/选择、真实崩溃/Unknown/status/恢复/还原，
以及固定未修改官方消费者的API-key A→B→Restore-A矩阵和明确native-login边界。
一次CLI调用同一原期限，不在观察/网络/确认阶段续期；Unknown持久intent在副作用之前。
不得先交NativeAvailable=true、空壳接口、metadata-only或只解析codec来报功能完成。

API、registry和可读许可包须在最终正常编译源码上取得新鲜行为证据、完整精确tuple及非作者验收，
之后主线程才合入。私有候选可以承载拟登记字面tuple用于正常CLI资格测试，但未获验收不进主线。
所有Go明确1.27.3/CGO_ENABLED=0、无race/新依赖；原失败保留，禁止真实用户秘密和外部模型调用。
源/包/Node pin不等于上游签名或Rust/Node可复现构建；分发许可实际打包另验。
需第65路径、schema/其它agent/UI变更时先报具体接口缺口，不隐含扩写。

以下保留冻结设计；冲突以本节与权威需求为准。

---

# Gemini Provider Native Apply Implementation Handoff

> 给后继执行者：本文件仅设计，须主线程逐项批准 G1 至 G5 及 exact64 WRITESET 后才可实施。批准后使用 executing-plans/TDD，在新的固定 archive 顺序执行，不 spawn、不写主线、不 branch/worktree/commit/push。任务组是同一完整闭环的实施顺序，不是可冒充功能完成的元数据小片。

**Goal:** 所有者显式选择 Gemini provider，经真实 server-only moejs 声明、宿主独立验证、native-purpose 授权和独立秘密批准，在本机 lossless 地更新 .env/settings.json，提供完整预览、双文件 CAS、选择状态、崩溃/Unknown 核实及新审阅恢复/还原；共享内核供后续 MyGo 直接复用。

**Architecture:** 保留既有资产域与 Gemini codec，兼容追加原六 RPC 的 Gemini typed variant。Go engine 独占生成新鲜有效输入观察及本地完整 intent；从 Codex 机械提取共用多角色 stage/publish/inspect 核心，Gemini 只补两角色策略、观察器和记录类型，不复制 Codex 的整套引擎。

**Tech Stack:** 既有 Go 1.27.3、CGO_ENABLED=0、no-race、Connect/protobuf、SQLite、moejs、Linux FD-relative primitives、Cobra/真实控制终端。无新服务、外部 Go/JS 依赖、队列或后台自动应用；Node 仅是已有官方 Gemini consumer 的固定运行时，不在 Cognate 客户端执行插件或嵌入 JS。

**Spec:** 固定 archive 的 docs/REQUIREMENTS.md F12、CCSWITCH-CONFIGURATION-MATRIX.md、Gemini provider assets spec；既有 native-apply/consumer 与 Codex 生命周期合同。完整十四 agent 目标不缩减。本次 provider 闭环不包含 Gemini MCP/GEMINI.md、Vertex/ADC、路由/代理接管或 MyGo 页面实现。

## 0. 固定事实、设计状态与非目标

- 基线 commit：`7244ced287f3f7386478592dbb6e5b1d44ea6f3a`；tree：`7476b4d51d1acb2ef6e93221409fd9ed1f9f05a2`。私有 base.tar SHA256：`89603bf1c6ebe97634584133de4fe4e6e0dc8d12548b2059791f5c5a440cd224`。不借后来的 R3/WIP。
- 已有 Registry.PlanGeminiProvider 使用真实 moejs，Go 从 desired 重算并精确比较声明；agentconfig/gemini.go 已区分 native-login/api-key、两种 transport、两处独立 model 和单槽 gemini.api-key。资产域/API/Web 已存在，不重做 CRUD。
- internal/geminiconfig 已有两文件 lossless codec；但 profile.go:101 的公开 CheckProvider **无条件返回 UnsupportedNativeProfile**。现有 source.json 的 consumerBuilt/Executed/qualified 均 false。词法测试和现有非零 witness 不等于 engine-owned observation。
- Claude Engine 的 plan/record 是单文件，不能循环两次 Apply 冒充 bundle。Codex Engine 有三角色、固定 auth Keep、catalog/config 发布顺序和独立 journal/AAD，不能把 Gemini settings.json 装进 Codex config.toml 或把 .env 当 JSON。
- 现 native wire 仅 target1/2、slot1/2/3；不能沿用 Claude slot1 或 Codex contract-version 来偷渡 Gemini。设计需 G2 的明确兼容扩展批准。
- 本设计只核源码、官方公开静态 artifact 和完整路径清单；未执行 Gemini、未跑 Go/consumer/native 资格测试，未授予 NativeAvailable。

## 1. 官方版本与真实格式核对

固定的是官方 **@google/gemini-cli 0.61.0**，不声称最新版本：

| 项目 | 本次核实值 |
| --- | --- |
| 官方 tag | v0.61.0 → commit bb523741c7429a44d03e964bc124c7c92df59d5f，tag 指向 commit 而非猜测 |
| 官方来源 tree | c4226f654bb0b35109c4f5ce34535c5addecda47；本次公开 Git API 核对 commit/tree，并对照 archive 核验 12 个关键文件的 Git blob；未重算完整 Git tree |
| npm tar SHA256 | bc4efa5c925c4430105b552820ed3164bbeffa9dc227990fc922f954733bcd7d |
| npm SHA512 | 已逐字验证 registry dist.integrity，见 upstream/npm-v0.61.0.json |
| 发布入口 | bundle/gemini.js，入口 SHA256 0b6e283ae88682b0e27e8ef85a608ab74807a1513dc0c053e5aa80d5b80b29ab |
| 完整包 manifest | 449 个文件，evidence/gemini-package-files.sha256；该 manifest SHA256 为 d937b098022f074463d0d26af37c5e7669cfeccbf6966e9795002f03f7db76b6 |
| Node 候选 tuple | Linux/amd64 Node26.13.0；本机二进制 SHA256 0adde40d1b7dfa57c8bc83cbc319758c3ec6ebba21363bb2fa752ea6b267ad1b；仅候选，未运行 consumer 资格 |
| 固定 profile ID | gemini-0.61.0-node26.13.0-linux-amd64-provider-v1；profile digest 由固定 source/package/Node/codec/launch contract 字段确定，不接受 caller 替换 |
| 格式来源 | lockfile dotenv17.1.0、@google/genai1.30.0；settings 是 strip-json-comments 后 JSON.parse，不是 dotenv，也不等于允许所有 JSON5 |
| cc-switch | 固定 6d6b7162f122ac0ce1356a14688c8c4f712db3c8 / 4.0.21；从 Git 对象另取 Gemini projection/direct/auth 源，不执行上游 |

固定官方源码的关键事实：

1. settings.ts:561 的 dotenv 搜索从 cwd 向祖先扫描，trusted 时优先各层 .gemini/.env，然后该层 .env，最终 home fallback；不是永远读取 ~/.gemini/.env。loadEnvironment:655 保留已继承变量，trust=false 会选择不同路径并 sanitize。只看两份目标文件不能证明有效配置。
2. settings.ts:757 读取 system-default/user/workspace/system，处理 trust、环境插值并在 :988 调 migrateDeprecatedSettings；:461 的 setValue 可调用 saveSettings。因此生产 preview **不能启动官方 loadSettings 作为“只读探针”**。
3. config.ts:843 的优先级为 CLI --model > GEMINI_MODEL > settings.model.name；两者不同不能静默同步。均 absent 时是官方 auto alias，不硬写一个在线模型默认值。
4. contentGenerator.ts:63 的 auth literals 为 oauth-personal / gemini-api-key 等；:277 识别 bearer，默认是 x-goog-api-key；:352 使用 GOOGLE_GEMINI_BASE_URL。最终 header 还经过固定 SDK，不能仅凭一个源码赋值声称 bearer 一定不带其它 SDK header。
5. 发布入口实际 import chunk 文件并可能 relaunch；仅入口 hash 或 --version 不构成完整 consumer pin。449 文件、Node、受控启动与额外模块解析边界必须一起绑定。

## 2. 请求主线程裁定的五项选择

| ID | 推荐裁定 | 代价与理由 |
| --- | --- | --- |
| G1 观察 | 固定 0.61.0/Node26.13.0 Linux amd64，使用 source-pinned、engine-owned、无网络/无执行的 Go 有效输入观察器；以未修改官方 consumer 请求反证/资格测试校准，不伪造 config/read API | 官方此处没有已核实的完整无副作用 config/read；运行它还可能改 settings。观察器不是 attestation，私有 HMAC witness 不接受调用者自报 facts 代替 |
| G2 wire | 给原六 RPC 兼容追加 Gemini target=3、secret slot=4、独立 declaration/preparation/local binding；不增加 RPC、登录主体或 DB schema | 当前 wire 确实不足，不能单改 available。旧字段/签名域/记录保持，新增严格互斥分支 |
| G3 首次接管 | 六个 provider 字段首次来自 foreign 时，允许用户对精确字段显式 takeover；绑定当前值的私有基线、动作与新确认。默认仍 Conflict，不因字段名/文件归用户就当 Managed | 不接管则已有真实 Gemini 配置永远不能首次切换。需要扩充 codec 的明确 takeover fact，绝不把 Foreign 偷改成 Managed；无关字段永不接管 |
| G4 复用/事务 | 从 Codex 仅机械提取有限角色事务和 vault primitives；保留其 record schema/AAD/filename/状态/顺序，Gemini 新 domain 两角色 journal；existing-root 内 env 先、settings selector 后 | 复用成熟故障路径而不复制几千行；必须承认两文件非原子窗口，选择提交最后，不承诺任意并发 Gemini 读者看不到混合态 |
| G5 首批能力 | 完整闭环限定“已存在可信 .gemini 根 + 确认可解析且已 trusted 的 launch/cwd”；支持两目标文件 Missing/Present、API-key 两 transport、native-login 的 selector/不接管 OAuth。根缺失、trust 不明、外层认证/路由影响先 Unsupported | 不替用户授予 workspace trust，不创建 OAuth/ADC、不给 native-login 冒签已登录。支持范围须如实显示；其余模式与十四 agent 总目标仍 OPEN |

五项都需要主裁定后才进入代码。若不接受 G1 或要求首片覆盖不受限的 project/system/OAuth 有效态，必须换完整观察合同，不能在当前方案上静默开门；若不接受 G3，明确拒绝全部 foreign provider-field 首次切换，不假称已覆盖正常 cc-switch 接管。

## 3. 推荐复用路径与公共接口

最朴素但不采用的方案是复制 CodexEngine 后改角色名；它复制了观测/auth/catalog 和 journal 状态机，后续修复会漂移。推荐只抽机械内核，不做新的通用插件平台。

```text
owner asset / encrypted revision
  → server Gemini moejs + independent Go oracle
  → existing Begin/Bind/Prepare, safe declaration only
  → configclient typed flow
  → nativeconfig.ApplyGemini ← CLI controlling-TTY / later MyGo callbacks
      → Gemini engine-owned fresh observation + lossless Gemini codec
      → exact field takeover + separate slot approval + final new-plan approval
      → Authorize / one-shot Consume (API-key only)
      → shared bundle stage/publish/inspect kernel
      → encrypted Gemini journal + two target artifacts + selection outcome
```

共用内核的边界：

- 原样复用 nativeDirectory 的 no-follow FD、identity/mode/link/ACL、读写/交换/fsync/remove-own/reserve；以及实际 client.db.install.lock 的现有 guard。不要复制一份 OS 安全写入层，不扩其它平台。
- bundle_transaction 只拥有 bounded role images、backups/stages、durable publish intent、逐 role exchange、actual Before/After/mix 核实及恢复机械步骤。可信编译期 adapter 给固定 role names/order、pre-publish observation recheck、现有 record save 和 selection finish；不接受来自服务端/用户的任意 path/callback。
- Codex adapter 仍为 config/auth/catalog 三角色、auth 永远 Keep，沿用原 publicationOrder 和 checkpoint 名。旧 0/v1/v2 record/pin 不升级或重标。bundle_vault 只提取加密/有界文件写原语，原 Codex JSON/AAD/域完全不改。
- Gemini adapter 只有 EnvRole=.env、SettingsRole=settings.json，pair≤384KiB、每文件≤256KiB；固定角色上限 3，不能用这个抽取引入任意文件 writer。
- 新 Gemini vault 域固定 gemini-local-anchor-v1 / gemini-local-backup-v1 / gemini-local-candidate-v1，独立 gemini-local-anchor.v1；32 retained operations、256 selections、64KiB metadata、96MiB vault 上限，不清历史绕容量。复用同一互斥锁不等于可读对方记录。
- 不能完整 lossless 处理的 dotenv/JSONC 明确 Unsupported；保持现有深度/累计 bounds，禁止重复定义 last-wins、trim 或 JSON map 重序列化覆盖未知字段。

公共接口延续既有命名与类型隔离，所有计划/材料/观察为 opaque private carrier：

- configapply：OpenGemini、CurrentGeminiSelection、ObserveGemini、PreviewGeminiObserved、ConfirmGemini、MaterializeGemini、ApplyGemini、StatusGemini、ReconcileGemini、PreviewGeminiRecovery/RecoverGemini、PreviewGeminiRestore/RestoreGemini、AbandonGeminiBeforeWrite、RetireGeminiUnpublished、Close。
- GeminiLaunch 仅 NodeExecutable/PackageRoot/Directory，绝对规范路径，不搜索 PATH、不接受任意 argv、env map 或 caller-supplied “CompleteEffective”。GeminiObservation 由同一 engine 持有，绑定 generation/nonce/原期限。
- nativeconfig：ApplyGemini(context, GeminiApplyRequest, ownerSession, GeminiInteraction) 返回 GeminiResult；GeminiInteraction 仅 Preview/Approve callbacks，Approve 返回 exact ApprovedSlots、explicit field decisions、FinalConfirmed。LocalGemini 管理既有 operation 的 status/recover/restore/abandon/retire；每次修改都新 plan。
- CLI 扩展现有 config 子命令，显式 --target gemini.settings；apply 追加 --gemini-node、--gemini-package、--gemini-cwd，沿用 server/owner/asset/revision/profile/token-stdin/--apply。无 --apply 仅预览。local 子命令不重新消费 server secret，不隐式拿 session 代替批准。
- MyGo 后继只需 Service 调用上述 shared kernel，经既有 identity、worker、Window.Update/generation 和安全副本 callbacks 接线；不调用 Cobra，也不把 raw candidate/secret 放 view/model。此次 WRITESET 不包含 desktop。

## 4. 新鲜观察、ownership 与双文件 CAS

1. 在任何远端准备/秘密前先验证平台与固定 profile 可用；有资格后，key admission/短锁读取 selection，释放锁，再 fresh context/RPC。准备声明后 engine 重新读取本地 pair 与全部有效来源。所有期限取现有 native session、prepare、计划开始+2 分钟的最小值；慢扫描/用户等待绝不续期。
2. pin Node identity+digest、完整 package manifest、文件类型/链接/父目录与额外模块解析来源；profile 限 512 package files/128MiB 总量。未知额外模块/optional native-addon 来源不悄悄混入。预览不执行官方包，真实 consumer 测试固定绑定 GEMINI_CLI_NO_RELAUNCH=1 recipe（不绕过 auth/trust），不靠忽略未知后代进程装作已排空。
3. 捕获 home、cwd 与祖先的 FD/缺失链、两份 target images、祖先 .env/.gemini/.env、home fallback、workspace settings、system/settings defaults、trustedFolders 与相关 process env/flags。除目标 pair 外不写这些文件，不读取 OAuth/ADC/keyring/login 文件。输入最多 64 个 config sources/深度 64/累计 1MiB，不截断。观察前后两轮核对所有 stamps，出现变化即 Conflict；HMAC witness 由同一 engine 私钥、nonce、原期限及全部 stamps 生成，不能用 NewResolution 的公共 facts/witness 绕过 engine 再核验。
4. 首 profile 要求 cwd trust 由现有配置确定，非 IDE 瞬态或调用者谎报；不会设置 TRUST_WORKSPACE 或关闭 folderTrust。实际 dotenv 来源必须已经是目标 user .env，或目标缺失且全部更高优先来源/非目标 fallback 均缺失；不能因创建 .gemini/.env 而悄悄替换另一个已生效 dotenv 文件的全部变量。
5. process 中 provider override、Node injection、代理、替代 Gemini home、Vertex/GCA/ADC/custom headers 等存在就 Unsupported，不删除继承值后假称普通 launch 已成功。workspace/system 若影响 auth/endpoint/model/忽略 env，同样拒绝；可证明无影响的其它字段只保留并绑定。两 model 按固定优先级计算“此 launch 的有效选择”，从不声称所有工作目录均相同。
6. 阻止运行时迁移导致假只读的已知路径：general.disableAutoUpdate/disableUpdateNag、ui.accessibility.disableLoadingPhrases/enableLoadingPhrases、context.fileFiltering.disableFuzzySearch、tools.approvalMode、experimental.codebaseInvestigatorSettings/cliHelpAgentSettings/plan。不替用户自动迁移这些无关字段。非 provider sibling 仍 lossless 保留；未获固定 profile 证明无影响的未知 selector/扩展先 Unsupported，不丢弃再应用。
7. profile 对 env interpolation、modelConfigs、enforcedType/useExternal、sandbox/ignoreLocalEnv/excludedEnvVars、动态模型路由等影响执行路径的设置 fail closed；尤其不得允许未知字段引用新 gemini.api-key 后进入 UI/日志。普通已证明无影响的注释/未知 sibling 必须 byte-preserved。secret 的 dotenv 表示和实际 HTTP header 不能精确承载时，在 Consume 后的本地 materialize 返回 UnsupportedRepresentation、零 target 写，记已消费未应用，不 trim 修复。
8. 六字段仅 GEMINI_API_KEY、GOOGLE_GEMINI_BASE_URL、GEMINI_API_KEY_AUTH_MECHANISM、GEMINI_MODEL、/model/name、/security/auth/selectedType。Absent、既有可信 journal Managed、Foreign、本次明确 Takeover 分开。缺省 override 不等于删除；仅受管项显式 Clear 或经 G3 的精确 takeover+clear 可删，不认领 GOOGLE_* 前缀。native-login 要清掉自己此前的 key/endpoint/bearer 时，同一 review 明确列清理，OAuth 凭据仍完全不在 ownership 中。
9. full intent 绑定 origin/owner/asset revision/adapter tuple、target/user scope/profile、selection+confirmation revisions、两文件 presence+identity+mode+bytes hash、字段动作/ownership/cleanup、来源/launch/profile witness、新 operation 和期限。pair 原文/hash 只在私有内存或加密记录；给 server 的是带域隔离的随机客户端密钥 HMAC commitment，不上传路径或 raw 文件 hash。
10. 显示预览、最终批准、Authorize 前、接纳材料前、每个 role 发布前、恢复前重新观察；自身已发布 role 只允许认证记录里的 After，其余必须仍是 Before，外部 source stamps 不变。不得把中间 pair 重新当 fresh 未受管基线。漂移在第一写前拒绝；晚期漂移保留真实部分状态。

## 5. Wire / 服务端最小增量

- 原六 RPC 不变。Target 追加 TARGET_GEMINI_SETTINGS=3；SecretSlot 追加 SECRET_SLOT_GEMINI_API_KEY=4。PreparationBinding.gemini=41、ApplyBinding.gemini_local_binding=41、PrepareRequest.gemini_contract_version=41、AuthorizeRequest.gemini_local_binding=41；PrepareResponse.gemini_declaration=6。不占 reserved30..39，不改 tag40 Codex 或老标签。
- 新 GeminiDeclarationV1 完整表达现 agentconfig declaration：version/target/authMode/optional endpoint/keyTransport/envModel/settingsModel、selectedType、optional credentialReference、exact requiredSlots、localAuthPolicy。复用已有 public Gemini enum；optional presence、空/缺失/非法值严格区分。
- GeminiPreparationV1 含 contract_version=1、declaration digest。GeminiLocalBindingV1 含 binding_version=1、固定 native/profile identity、CompleteBoundLaunch scope、preserve-local-login-v1、ordered 两 role presence、六 field decisions、observation witness 与 pair 的 opaque HMAC commitments；不装入 CodexLocalBinding，不上传 raw source/secret/path/hash。
- 新 message 的固定字段分配：GeminiPreparationV1 为 contract_version=1/declaration_sha256=2；GeminiLocalBindingV1 为 binding_version=1/native_profile_id=2/native_profile_sha256=3/codec_profile_sha256=4/effective_scope=5/local_auth_policy=6/observation_witness=7/roles=8/field_decisions=9。GeminiRoleBaseline 为 role=1/presence=2/baseline_hmac=3；GeminiFieldDecision 为 field=1/ownership=2/action=3/cleanup=4。新 enum 的零值均无效，role 按 env/settings=1/2，presence 按 missing/present=1/2，field 按本文件六字段顺序=1..6，ownership 按 absent/managed/foreign/explicit-takeover=1..4，action 按 keep/write/remove=1..3，cleanup 按 preserve/clear=1/2，effective_scope 仅 CompleteBoundLaunch=1。proof 的新域固定 cognate-native-gemini/1/prepare、authorize、consume、status，不能改变既有域。
- 各 message 严格目标/variant 一致且恰一个 body；Claude/Codex/Gemini 混搭、null/重复别名/未知 enum/unknown fields/额外 secret slots 全拒。新 proof canonical domain 分 prepare/authorize/consume/status，精确绑定所有上述字段及原 challenge/nonce/parent/current owner，不能跨 agent 重放。
- API-key 只允许 slot4 一项；native-login exact 空 slots，经原 bound-no-secret durable authorization，无 grant/Consume，不伪造“消费零字节 key”。两种模式都必须最终明确确认；proof 只证明持钥，不是本机观察的远程 attestation。
- 复用 NativeApplyPreparation 的 encrypted ciphertext、现 AEAD/AAD、parent session、nativeTransaction、资产 revision 事务重验、quota 和 grant 单次消费。Gemini record 有新严格 family/version，NativeConfigurationProtocol 必须先认证并解密再路由，不信客户端 family hint。不加 Ent 表/迁移或第二 token 系统。
- 服务端原 Gemini PlanGeminiProvider 和 host oracle 原样复用：moejs 只看 key-present，绝不看 plaintext、本地基线或路径。Prepare 与 Authorize/Consume 均重验同一资产及 envelope revision；网络/JS 不能放 SQL 事务内，最终事务须再 CAS。legacy/unknown asset envelope 保持只读，不能导出未支持秘密。

## 6. 发布、Unknown、崩溃与恢复的完整闭环

- Preview 不写 target/selection，不取 key；确认前不持 install/target 锁等待用户或网络。TTY 分别确认精确 takeover/cleanup、gemini.api-key 槽（API-key 才有），最后确认本次新 plan ID；Enter/空输入默认拒绝。native-login 不因没有新秘密就跳过最终批准。
- AuthorizeAttempted 和 ConsumeAttempted 必须在远端调用前各自 durable；response 丢失或解码/本地校验不明归 RemoteOutcomeUnknown。只能显式 status 核实，不重发 Consume，不自动换 operation 伪装重试。Consumed 不等于文件 Applied；缺失 plaintext 不能靠 Status 重新获得。
- 消费后内核再次验证 intent/observation，render 两 candidate，建立 Materialized binding；两原件加密备份、两 candidate/stage fsync、预算预留及 durable CandidatesReady 全部完成后才第一 target exchange。.env 与所有 stage/备份文件 0600；首 profile 的既有 .env 也须 0600，不安全旧 mode 明确 Unsupported，不能 Restore 时暗中放宽权限而违反这条。目录/ACL/硬链接/软链接/root inode 变化拒绝。无需读取旧 key 内容用于显示。
- 发布每 role 前先 durable PublishIntent，再交换/fsync 及实测 actual image；env 先、settings 后，**两次 rename 不是 pair 原子**。只有两角色 After 都实际验证并成功持久化，才 CAS 推进 active selection。取消或错误不能把半写/未 sync 状态说成 rollback。
- 明示用户关闭正在运行/自动重载的 Gemini；Cognate lock 只协调自身 writers，不阻止外部编辑器/Gemini 读取窗口。外部晚写触发 Conflict/NeedsAttention，不 force 覆盖、不谎称强制停净。
- readonly Status/Reconcile 只核实 Before/After/混合/foreign，零新 grant、Consume 和发布。不因重启加载 journal 自动完成第二文件；必须新 Recovery plan+完整 fresh observation+新的最终确认。认证且仍完整的 candidate 才能完成本来已批准的剩余本地发布，不取得新远端权限。
- Restore 也是新 operation/plan/confirmation，显示 source operation 和原资产/选择，双文件当前 After CAS 后从加密原件恢复原 presence/bytes/permissions；仅删本操作创建的精确文件，不碰 OAuth、未知 sibling、其它 agent。Recover/Restore 如需把 key 写回 .env，同样独立批准该原槽并标注 local-backup/durable-candidate 来源，不冒充新的 server key 消费。外部更改或已产生无关新内容时拒绝 destructive restore，保留备份及诊断。
- consume 前 Abandon 与未发布 Retire 明确区分；后者必须证明两 target 从未发布且基线仍一致，记录真实 remoteUnknown/Consumed 状态，不冒充远端撤销。丢 Consume 且无 durable candidate 只能核实/显式退役未发布操作，不能恢复出不存在的秘密。
- state 类别固定为 Previewed / AuthorizeAttempted / Authorized(NoSecret 或 Grant) / ConsumeAttempted / CandidatesDurable / PublishingEnv / PublishingSettings / Completed / RemoteOutcomeUnknown / ReconcileRequired / NeedsAttention / RestorePrepared / Restoring / Restored / AbandonedBeforeWrite / RetiredUnpublished。error/status DTO 是固定 enum/安全标识，不传 raw OS/Node/HTTP/JS 错误正文。
- Client.Close/取消先拒新工作并取消/join 现有 RPC/worker，排空后才销毁密钥及关闭 FD；不明保留依赖，不重复 Close/强制 unlock 来装成功。restart 不恢复旧内存审批或 grant token；本地恢复只能用新的明确审阅。

## 7. 能力与 RED→GREEN / 真实 consumer 计划

### A. 每组都属于同一交付的行为门

1. **真实 native-purpose RED**：现固定树对 Gemini target=3 返回 Unsupported，随后新测试期待实际 Prepare/Authorize。编译缺符号不算安全 RED。GREEN 必须真实 App/TLS/SQLite+moejs/Go oracle，API-key 与 no-secret 两模式，双 owner、改 revision、登出/撤销、普通/share/MCP 拒绝。
2. **codec/观察 RED**：公开 CheckProvider 当前 hard-close 是已知行为负控；固定真实 baseline/launch 后新 preview 应成功。保留原 lossless vectors，新增 LF/CRLF、注释/顺序、#/$/引号/等号秘密、重复/不支持表示、settings 未知 sibling、独立模型、takeover 默认拒绝/精确批准才可写。profile 未资格时仍先于任何 target/RPC/secret 拒绝。
3. **减门 RED**：分别移除 per-publish fresh observation、第二 role CAS、durable ConsumeAttempted、混合目标 proof gate、selection-last；测试必须编译并实际抓住错误，复原后 GREEN。不能拿 setup/依赖/timeout 当安全 RED。
4. **crash/reopen**：独立子进程在备份同步、candidate 同步、publish intent、每个 exchange/dir sync、selection sync 前后 SIGKILL，真实 Wait；fresh engine/真实文件核对，不只看 hook。覆盖 pair Missing/Present、write/keep/remove、阶段损坏/容量/ACL/链接、owner/origin/profile 切换、跨进程并发、取消/Close/排空及每步外部写。原失败全部保留。
5. **Unknown**：实际 server Commit 后截断 Authorize/Consume 收据；只有一次 Consume，显式 status 不再下发 key、不改 target；有/无 durable candidate 的重启路径分开。确认远端已消费也不能伪称已应用。Recover/Restore 用新 plan，旧 confirmation、旧 profile/launch、旧 expiry 均失败。
6. **原核心回归**：抽取前在固定 source 建立 Codex/Claude 正控与 legacy journal fixture，抽取后跑相同测试；Codex zero/v1/v2 旧记录读写/AAD/恢复、auth Keep、发布顺序、实际 C6 资格及 Claude 单文件路径不变。新旧 binary 对固定合成记录交叉读，新增 Gemini 记录不得混入旧域。
7. **CLI/共享内核**：真实控制 PTY 完整显示两 role/field/selection/revision/new-plan、独立槽批准、takeover/clear、默认拒绝、非 TTY/输出关闭/取消拒绝、Unknown 不重放。只有 stdin session 不授予业务批准。shared nativeconfig 另以安全 callbacks 实跑，证明后续 MyGo 不依赖 Cobra 或 server/moejs。

### B. 真正官方 consumer，不能用 codec/parser 绿替代

- 使用完整已验 SHA512 的官方 npm 包和固定 Node，无源码 patch、mock loader 或自己重建“相似 Gemini”。自有合成 HOME/已 trusted cwd，环境/搜索层与 profile 一致；不打开真实 HOME、登录文件或 keyring。
- API-key 正控必须是实际未修改 CLI 的固定非交互 prompt+JSON output，经可信测试 CA 到 loopback HTTPS fixture，观察实际请求 path/model/认证槽内容的相等性，不把 HTTP200 或 --version 当应用成功。网络出口限 fixture；无真实 Google 请求、供应商账号或有效秘密。
- 两套 A→B→Restore-A：native-default A 到 bearer B 再还原、bearer A 到 native-default B 再还原；每步真实 CLI 至少一请求，精确验证 endpoint 和选择、不得沿用旧 key。不要预设 bearer 必须不带 SDK 的其它 header，以固定包实际语义记录并独立核对。
- 另测两文件初始 Missing、仅 envModel、仅 settingsModel、两者相同/不同（env 优先）、均 absent（保留官方 auto，不虚构默认模型）、未知 benign sibling/CRLF 被保留且实际 CLI 正常。每个关键 case 的 receipt 记录实际 POST 数和完成/Wait，不把父子重复测试累计成独立覆盖数。
- native-login：产品不读取/导入/变更 OAuth/ADC/keyring；有合成 OAuth marker 时仅证明应用进程完全不碰它。未登录的真实 CLI 应进入已固定的 auth-required 路径而非继续使用旧 API-key；无本机 OAuth 健康或刷新承诺。若主要求已登录 native-login 真实请求，须另外用新鲜合成 OAuth + 隔离 code-assist TLS fixture 核证，不得拿真实账号凭据或改变生产路由 gate 造绿。
- 测试 CA、禁止非 fixture 出口及合成 trust 只是 fixture 输入，不能偷改生产 accept policy。上游写 session/cache 的副作用只发生在合成 HOME；与 Cognate 自身的 target CAS 变化区分。源 profile 没覆盖的 launch 继续 Unsupported。
- 若消费者/namespace/CA/PTY 条件不可用，报告具体 setup 缺口并保持资格门关闭，不能 skip 后宣称完整 native 可用。官方程序本次尚未执行，这些是后继必须取得的新证据。

### C. 固定验证命令与交付

所有 Go 调用显式 CGO_ENABLED=0、GOTOOLCHAIN=local、Go1.27.3，无 -race；真实 consumer 仅在上述隔离 fixture 执行。先窄门，再包回归：

```sh
CGO_ENABLED=0 /home/czyt/.local/share/mise/installs/go/1.27.3/bin/go test ./internal/geminiconfig ./internal/nativesessionauth ./internal/configapply ./internal/configclient ./internal/nativeconfig -run 'Gemini|Bundle|Codex|Native' -count=1
CGO_ENABLED=0 /home/czyt/.local/share/mise/installs/go/1.27.3/bin/go test ./internal/server/state ./internal/server/auth ./internal/server/configuration ./internal/server/httpapi -run 'GeminiNative|NativeGemini|CodexNative|NativeConfiguration' -count=1
CGO_ENABLED=0 /home/czyt/.local/share/mise/installs/go/1.27.3/bin/go test ./internal/cli -run 'Gemini|CodexQualification|NativeConfiguration' -count=1
```

资格 harness 使用显式包/Node 路径与 private fixture，不自动下载或查找用户 Gemini。最终列 exact argv、结果、真实进程数、每次 Wait、请求和安全布尔收据；定向 vet/依赖闭包与非 Linux 编译拒绝边界另录。客户端闭包不得引入 server/agentadapter/server/plugin/moejs。

生产资格条目只能在该完整闭环的真实 consumer/故障矩阵通过后，以精确 tuple 加入并随本片交非作者独审；作者的私有 fixture GREEN 不代替非作者签收。最终源码包含资格条目时还要用普通 compiled CLI 重跑，不仅用 test-only profile 注入。未通过仍保持 Unsupported，不能提前交一个“功能完成”冻结。

交付 patch/fullsource、精确最终路径与每文件 SHA、全部 RED/GREEN/Unknown/清理证据和紧凑 REPORT；旧 Codex、Gemini source-review 和其它原冻结不改，真实失败不覆盖。只在主新 archive 做集成，不能复制旧共享 docs 覆盖并行 WIP。

## 8. 精确 WRITESET、风险与回滚

完整清单为同目录 **WRITESET.tsv：64 路径，22 修改 + 42 新增**，包含测试/固定公共 manifest/许可证/文档；显式超过 8 文件，但没有新服务。这是实现申请，不是本次实际源码修改。不要静默加文件；任何需要 Ent/schema、其它 agent、UI 或未列路径的变化先给主具体原因再改。

其中 server moejs 模块/agentconfig Gemini typed 声明原样复用；现元数据 CRUD、普通 ConfigurationService/proto、Web 编辑器、desktop、native OS 原语、install、runtimegen、备份/R3 共享源、go.mod/go.sum 及 workflows 不在写集。Connect service 不变，native.connect.go 重生后应逐字相同，不列入修改。

最脆弱前提是固定源的 env/trust/merge 语义足以被 bounded Go observer 等价捕获；如果官方 consumer 与之不一致，profile 不合格，而不是补环境默认值绕过。另一个高风险是共用 bundle 抽取破坏 Codex 历史；必须先有旧记录/实际消费对照，再机械移动，不能顺手重构。

- 外部依赖失败：不影响资产保存；本机新应用在资格/输入观察阶段拒绝，已有 receipt/status 不凭新版本重标。
- 规模：用户资产 250 不转化为批量本机写；一次一个 selection/operation，两文件及有限 journal，容量满显式停止，不自动清扫。
- 回滚：新 Gemini anchor/domain 不被旧 binary 解释，保留加密备份；只用具有原固定 profile 的新版 CLI 新审阅恢复，然后才回滚。Codex 的旧 schema/AAD 不变；不得删 journal 或强行把 Gemini 当 Claude 恢复。
- 范围诚实：首批 Linux/amd64、固定 Gemini/Node、user target、已证明 launch；不是 Windows/macOS、任意版本/工作目录、OAuth 登录/刷新、完整十四 agent/F12/GA。MyGo 是同内核后继接线义务，不因本片 CLI 完成而从总需求删除。

请主明确批准/修改 G1 至 G5 和 exact64 后，后继再取得实施授权；本次到设计交付为止。
