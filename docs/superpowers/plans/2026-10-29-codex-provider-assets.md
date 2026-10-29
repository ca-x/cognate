# Codex Provider Assets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 本文件是待独立设计审的计划，不是源码授权；不自行 stage/commit/push 或启动 agent。

**Goal:** 让用户在既有配置入口结构化管理 Codex API provider 私有加密资产，包括完整模型目录、单槽凭据变更与无凭据复制；只保存服务端资产，不产生原生应用能力。

**Architecture:** 在现有 ConfigurationService/Store/AEAD/CAS 上增量加入 Codex typed 分支，复用 agentconfig 的目录类型与限制。Claude 的存储解码、native load 和七字段授权合同保持原义；Codex CRUD 用独立的同快照 loader，不把现有 Claude native 入口泛化。Web 用既有 React/Radix 组件与请求生命周期，新建专属目录表单。

**Tech Stack:** Go1.27.3、CGO_ENABLED=0、encoding/json/v2、既有 ent/entsqlite、Connect/Buf、React/TanStack/Radix/i18next。无新依赖、表、migration、Go toolchain 或客户端插件。

**Spec:** `docs/superpowers/specs/2026-10-29-codex-provider-assets.md`，SHA256 `d240a7ffd91ef1ea3e212e614418fc9b2b1563bd3bb5e03599ded6d2799f77fc`；非作者 design ACCEPT 报告 `codex-provider-assets-spec-review.md` SHA256 `5e6839afa3982f60e7480d07c29c47011972016472ea21cc0ec5b6a0ea3dc3d0`。CX-ASSET/C1 不是 Claude/Linux CLI C1。

## Global Constraints

- 固定实施事实基线 `656c211d01a9d7d2632c5cee12b49f6fb3229ca9`；规格提交 `fe87e84` 只加本文所消费的 spec，不改相关实现。源码授权前须记录其完整提交身份并核相关 source subtree parity，不从 moving WIP 取新接口。
- 用户 F12/十四 agent、Codex map 的 B/C2/D、官方账号/MCP/prompt 等后续能力保持未完成。新 UI 明确“保存到服务端”和“尚不能在客户端应用”；禁止安装、selection、Authorize/Consume 或本机文件/keyring 副作用。
- 只接受 provider-key、原样 HTTPS endpoint、native Responses 声明语义；不是 OAuth/官方账号/native-login/env-key/header/command/keyless 或代理转换。
- 单槽 `codex.provider-key`；REPLACE 为1..8192 UTF-8字节、无NUL/CR/LF、不trim；创建KEEP拒绝，更新缺省/KEEP保持，CLEAR明确删除。缺凭据可保存但 readiness 为 NEEDS_CREDENTIAL。Copy不带凭据或设备/授权/自动应用资格。
- 跨agent合计250资产，payload≤256KiB，desired≤48KiB，catalog≤32KiB，HTTP请求≤128KiB、未压缩响应≤1MiB；8个即时slot/20s父ctx维持。所有尺寸按实际编码UTF-8字节，不能截断、去条目或放宽限制。
- Catalog为1..32有序唯一model；全部8字段、absence/false/显式none、模态/effort顺序与引用约束按spec §3，直接复用 neutral types/constants。不改 A validator/JS/registry，不伪造 KeyPresent=true 以绕过缺凭据校验。
- 请求unknown/重复/非法enum/null拒绝；已有密文unknown仅在已知部分完整有效时只读保全。Update/Copy拒绝保留extras，Delete仍须owner/CAS；AEAD损坏或未知version/variant不返回伪typed view。既有Claude密文零迁移。
- 测试仅合成HOME/owner/token/key、TempDir、真实纯Go SQLite/loopback服务。token/正文/原错误不入日志、URL、公开目录或浏览器持久化；不宣称不可变string/浏览器内存可物理擦除。
- 手工文件改动只用apply_patch；Go编辑前完整读取modern-Go1.27 list。按ub先可编译行为RED→GREEN，再正确性/负控；编译失败与零匹配不是行为负控。精确freeze后非作者验收。

## Review Focus

1. `domain.load/metadataSupported` 被native共用：不能为CRUD支持Codex而让旧Claude grant入口接受新变体。
2. protobuf repeated enum默认packed，而当前codec只认Varint：必须真实JSON/proto双协议round-trip，不以Go mapper测试代替。
3. protojson把null当未设置：仅新Codex树必须保留原始null拒绝，不能借此改变旧Claude的兼容语义。
4. 已有密文的嵌套unknown可能藏secret：只读投影不代表允许copy，不能重编码已知部分吞数据。
5. locale/owner切换、Abort或COMMIT回执丢失：清掉replacement但保非secret草稿语义，不能缓存旧写或自动重试。

## 1. 精确候选写集与串行归属

下列为未来请求的 **22路径**，不是当前源码授权。所有shared proto/gen由一个owner串行修改，C2不得并写。E1 CLI、E2 desktop、B2 state维护文件都不在此集。

| 路径 | 必要职责 |
| --- | --- |
| `proto/skillhub/configuration/v1/configuration.proto` | additive Codex typed oneof/messages/enums |
| `gen/skillhub/configuration/v1/configuration.pb.go` | 正常Buf Go生成 |
| `web/src/gen/skillhub/configuration/v1/configuration_pb.ts` | 正常Buf Web生成 |
| `internal/server/configuration/types.go` | CodexProvider/closed draft-view分支、单槽status、extras标志 |
| `internal/server/configuration/domain.go` | 既有CRUD薄分派及Claude status切片；native load保持Claude |
| `internal/server/configuration/codex_domain.go` | Codex校验/clone/单槽合并、同快照CRUD/copy |
| `internal/server/configuration/codex_envelope.go` | Codex envelope、严格已知字段与unknown只读保全 |
| `internal/server/configuration/codex_domain_test.go` | 真实Store/CAS/quota/secret/副作用/A parity |
| `internal/server/configuration/codex_envelope_test.go` | duplicate/null/unknown/tamper/预算/clone与保全 |
| `internal/server/configuration/codex_native_boundary_test.go` | 真实native service拒绝Codex、旧Claude向量 |
| `internal/server/httpapi/configuration_mapping.go` | 现有mapper按typed分支分派，不改7RPC |
| `internal/server/httpapi/configuration_codec.go` | 新Codex null gate/packed枚举校验接缝 |
| `internal/server/httpapi/configuration_codex_mapping.go` | Codex presence/enums/slot双向映射 |
| `internal/server/httpapi/configuration_codex_codec.go` | 新树原始JSON presence扫描与有界packed枚举helper |
| `internal/server/httpapi/configuration_codex_test.go` | 真session双owner双协议CRUD与limits/cancel |
| `internal/server/httpapi/configuration_codex_protocol_test.go` | 嵌套unknown/null/duplicate/packed边界与旧client向量 |
| `internal/server/httpapi/configuration_codex_logging_test.go` | 真stdout/log/slog/响应隐私、取消与故障 |
| `web/src/configuration.tsx` | metadata+payload分派、创建目标选择、只读/复制/写结果 |
| `web/src/configuration-state.ts` | Codex guard和closed view union；不改请求生命周期含义 |
| `web/src/configuration-codex-form.tsx` | 全字段/有序目录/显式凭据动作的成熟组件表单 |
| `web/src/configuration-codex-state.ts` | 纯表单转换、严格数字/presence/引用校验 |
| `web/src/locales/en.ts` | 新界面中文key的English翻译 |

正常双模板必须对全部输出做parity；Connect handler若真有新增生成diff，先提交精确路径与原因请求，不手改生成物或忽略diff。现有handler `configuration.go` 的slot/owner/ctx路径可原样复用，未申请写权。现有Claude form/envelope/native consumer生产代码、state/configuration、schema/module/agentadapter均不改；编译闭包若证明缺路径先申请，不暗扩。

## 2. 已核接缝与实现决定

### 2.1 保留旧Go调用面，严格选择一个分支

- `Draft.Provider`/`EditView.Provider`保留现有Claude值类型，以免无关native/tests发生大范围指针迁移；新增 `CodexProvider *CodexProvider`。CodexProvider仅含AuthMode/Endpoint/Model/ReasoningEffort/Catalog，catalog直接用neutral类型，**没有KeyPresent/secret/ref/policy**。
- 私有classifier逐字段判断Claude是否完全zero（AuthMode空、所有optional nil）。Codex非nil要求Claude完全zero；Codex nil要求现有Claude有效；两者都无或混合都ErrInvalid。不用reflect/map/JSON猜分支。返回view必须由metadata agent/kind/schema和payload分支共同匹配；在内部JSON tag上用omitzero避免虚假零Claude对象，不声称这是新的raw API。
- `EditView.Secrets`由固定[2]改为owned `[]SecretStatus`：Claude恰2、Codex恰1，顺序固定；修改现有editView构造并clone，不能第二项留空冒充Codex单槽。新增HasPreservedExtras bool，只由存储解码推导，不能由draft写入。
- 保留原 `Service.load` 和 `metadataSupported` 为Claude-only，供nativePlan与原Claude分支继续使用。新私有 `loadEditable` 用现有ReadConfigurationAssetVersion同快照、ready/failure/validateMetadata身份校验并只放行Claude/Codex provider schema1；不先Get再Read，不接受任意agent。
- Create先classifier；Update先按输入分支分派并核已存metadata同variant，不能转换旧ID；Edit/Copy用loadEditable按metadata分派。Claude decodeEnvelope/encodeEnvelope保持字节语义及错误分类不变。Codex loader、decode/merge/create/update/copy在专属文件；native production入口不扩权。

### 2.2 密文、unknown与缺凭据校验

- envelope version1、variant `codex-provider`，typed provider和secrets内唯一provider key。沿用Store AAD和payload，私有Secret保持owned slice并在临时用完clear；不添加新的加密服务或reveal API。
- 已存JSON由标准jsontext token parser先完整有界扫描：至多payload字节数数量级的tokens、最大嵌套16；逐层重复名称/非法JSON拒绝，未知成员和值不输出。用路径/descriptor明确已知成员（含catalog数组项），只有未知成员可以保留任意合法JSON值；已知字段null、错型、未知enum一律拒绝。再json/v2解码已知结构并做全部语义校验，不允许unknown绕过已知字段校验。
- decoder返回owned provider、secret、extras bool；不返回未知值/raw map。extras为true时仅Edit得到typed只读view；Update和Copy在任何写/新ID分配前拒绝，原ciphertext/revision/hash不变。unknown variant/version/schema不构造typed view。
- shape validator不调用A，不硬设KeyPresent。合并真实slot之后用真实presence投影desired测48KiB预算，catalog实编码≤32KiB；缺slot仍测false投影大小并允许NEEDS_CREDENTIAL。A parity测试只对真实存在slot且无extras资产调用实际Registry.PlanCodexProvider；secret字节永不传入JS。
- Copy读取同快照并核source revision，以owned已知typed内容和全新空secret创建；source读取后发生更新可复制验证过的旧snapshot，不暗宣称source-commit联锁。

### 2.3 Wire的精确增量

- 先核空闲tag后 `Draft.payload.codex_provider=3`。CodexProvider字段1 auth_mode、2 endpoint、3 model、4 optional reasoning_effort、5 catalog。CodexCatalog.models=1。CatalogModel字段1 model、2 optional display_name、3 optional uint64 context_window、4 optional bool supports_parallel_tool_calls、5 input_modalities存在性message、6 optional base_instructions、7 reasoning_levels存在性message、8 optional default_reasoning_level。
- 新CodexAuthMode enum0 UNSPECIFIED/1 PROVIDER_KEY；CodexReasoningEffort enum0 UNSPECIFIED、1 NONE、2 MINIMAL、3 LOW、4 MEDIUM、5 HIGH、6 XHIGH、7 MAX；CodexModality enum0 UNSPECIFIED/1 TEXT/2 IMAGE。两存在性message的repeated values均tag1。旧tag/enum不变，replacement继续已有debug_redact，不宣称生成Go格式化自动安全。
- configurationWireValid对新repeated enum同时验证合法packed BytesType与unpacked VarintType，逐元素检查合法枚举及非负范围，残缺varint/超界拒绝；保留singular duplicate/oneof/unknown和深度16。mapper另拒重复值及present-empty，不能把合法生成proto请求拒掉。
- JSON在protojson之前用std token parser+实际descriptor识别Draft里的codex_provider/codexProvider树；此树任意已知字段显式null拒绝，包括整个分支为null。识别两个别名不代表允许重复alias；由既有protojson重复/oneof检查拒绝。旧Claude树保持原codec语义，uint64合法protojson十进制字符串仍由标准codec解析后域限界，不当成源导入宽松转换。
- mapper精确clone所有optional/arrays；Metadata、Draft和status条数交叉验证后才返回。Wire/DTO/private正文仍敏感，固定code错误不包含parser原文或replacement。

## Task 1: 域加密CRUD与原生边界

**Files:** 上述configuration7路径。**Produces:** 可独立真实Store验证的typed域，不开放HTTP/native能力。

- [ ] 建立可编译新types/seam；`TestCodexProviderAssets`先见缺CRUD行为RED，再最小实现。涵盖缺slot/KEEP/REPLACE/CLEAR、copy缺secret、所有catalog字段/presence、重开、clone、混合Claude/Codex和跨owner拒绝。对copy目标通过真实Store认证解密断言无旧slot，不仅搜索随机ciphertext。
- [ ] `TestCodexProviderAssetConcurrency`：真SQLite同revision并发单赢家、MaxInt64拒绝、跨agent250总额、256KiB/48KiB/32KiB精确边界；copy读取前/后barrier验证现有snapshot语义。不改state生产锁或交易。
- [ ] `TestCodexProviderStoredExtras`：根/provider/catalog/数组项/secrets未知成员、合法unknown null、重复/错型/已知null、AEAD身份tamper、未知version与metadata不配对；只读Edit及Update/Copy零写，前后真实密文/revision同一。
- [ ] `TestCodexProviderPlannerParity`：独立expected，不调用宿主expected helper，真实Registry/moejs测完整/absence/false/有序目录；无secret进入JS。缺slot/extras不调用planner，不生成requires_openai_auth或native pin。
- [ ] `TestCodexAssetNativeDenied`：真实NativeService/auth/Store对Codex asset的准备/消费拒绝、零grant/selection；旧Claude六RPC/向量继续GREEN。若旧native load被泛化则此oracle必须RED。
- [ ] targeted configuration tests/vet及CLI/desktop必要编译GREEN；freeze域增量给后续mapper消费。不能以A声明成功宣称native支持。

## Task 2: Additive协议与真HTTP

**Files:** proto/gen及HTTP7路径，消费Task1精确域接口。**Produces:** 原7RPC支持Codextyped资产，双协议行为一致。

- [ ] 先核tag3可用、正常Buf双生成；保留旧生成历史向量/编译用例。新客户端Claude仍可与旧server通信，旧client遇Codex只unsupported；用固定旧binary/descriptor，不伪造“旧client”等于新mapper空结构。
- [ ] `TestCodexConfigurationLifecycle`对真session、两owner、ConnectJSON及protobuf做create/edit/update/copy/delete/reopen，presentfalse仍返回单status；API/reflection注入caller presence、Claude slots、unknown/auth/enum一律失败。
- [ ] `TestCodexConfigurationProtocol`含生成器标准packed enum正控、unpacked正控、截断/unknown enum/duplicate singular/oneof冲突；每层unknown/null/错型、present-empty wrapper、数字UTF-8/BigInt范围，原Claude成功失败向量不改义。
- [ ] `TestCodexConfigurationLimitsAndCancel`保128KiB/1MiB含gzip、8slot即时拒绝/20s父ctx、真事务取消/未知COMMIT不交伪成功；固定错码、没有raw error或secret响应。
- [ ] `TestCodexConfigurationLogging`捕获真实标准log/slog/HTTP出错路径，替换值/session/baseInstructions/未受信错误sentinel均不出现在输出；默认DTO仍敏感，不能以Stringer代全部格式化证明。
- [ ] Buf lint/breaking、fresh双模板parity、HTTP/configuration/native/auth必要回归与vet GREEN。禁止改native proto、MCP token权限或公共skill/Pack边界。

## Task 3: 成熟组件的Codex编辑器

**Files:** Web5路径，消费Task2固定生成物。**Produces:** 可真实浏览器操作的结构化CRUD，保存与本机应用严格分离。

- [ ] 新建目标显式Select Claude/Codex API provider；edit/copy依metadata+oneof精确dispatch。Codex官方账号仅限制说明，无伪可提交表单；list metadata无需把私有草稿放query cache。
- [ ] 实现`supportsCodex`/`requireCodexView`及closed view union，核agent/kind/schema、oneof、readiness、唯一canonical slot。extras返回只读view而不是伪空编辑；Save/Copy禁用且后端仍拒绝。Claude guard/form不改义。
- [ ] 专属form用现有Input/Select/Checkbox/Button/Dialog；私有正文用带label的原生textarea，项目没有Textarea wrapper时不新增编辑器/组件库。Catalog最多32行，各字段都有presence控制，parallel为未设置/false/true；modalities/levels可键盘上移下移，删除/改主model须显式修复引用，不静默排序或补默认。
- [ ] contextWindow文本只接受正十进制整数字符串并以BigInt校验1..2147483647，不用Number/locale/指数宽松转换；wire uint64编码交标准生成器。其余字段UTF-8/大小写/控制字符按spec，不自动trim provider值。
- [ ] 凭据KEEP/REPLACE/CLEAR显式选择；REPLACE才显示password，CLEAR已有值需确认。成功/失败/取消/owner或origin变化/locale/logout/unmount清替换明文和请求引用；非secret草稿按既有用户离开确认策略，不靠reload藏竞态。
- [ ] 复用useConfigurationRequest的Abort/generation/uncertain/CAS阻断；后端可能已提交时刷新metadata并显式ReadDraft再操作，不自动retry。Copy解释不含credential/授权状态，Delete说明不撤销已写本机配置。未知extras只读仍可原owner/CAS明确删除。
- [ ] `npm run build --prefix web`/现有typecheck GREEN；不向无测试体系的Web新增孤立测试框架。浏览器实际验收如下，不能以构建通过替代。

## Task 4: 浏览器行为、负控、精确交接

**Files:** 不再增加shipping路径。**Produces:** 可追溯真实证据，不伪称完整Codex/F12。

- [ ] 用真实服务/SQLite/已构建Web与合成owner/key启动隔离loopback fixture；可在/tmp对候选`configuration_codex_test.go`做test-only overlay加入受控fixture/barrier，不改production代码、不加公开测试endpoint或任意SQL/RPC。fixture进程有明确ready/stop/ctx、必须join；helper启动不计产品PASS。若既有seam不够先申请具名test-only路径。
- [ ] agent-browser真实create→全catalog编辑→save→reopen→copy缺key→补key→CLEAR；从真实Store检查屏幕/落盘一致。截图用合成/遮罩数据，抓console/storage/缓存检查无credential/session/private正文持久副本。light/dark、中英、375px/正常宽度、Tab/Enter/Escape/焦点往返有证据。
- [ ] barrier制造save成功与离开确认交错、owner/origin/locale切换、Abort/unmount、CAS冲突、COMMIT后回执丢失；旧generation不回填，无旧请求自动重放，replacement清掉，刷新可核实已提交结果。
- [ ] 至少6类独立编译后负控：信任caller presence；copy携secret；丢false/目录顺序；忽略stored unknown重写；删除generation或冲突后自动重试；raw replacement/正文进入错误日志。分别保存compile0与精确断言RED，恢复exactsource GREEN。另把native load泛化、拒合法packed或接受Codex null作为范围回归负控；编译失败、panic、整体timeout、零匹配不充数。
- [ ] 精确22路径新tree/BASE/blob+mode/SHA manifest，执行npm、全Go/vet/module、Buf lint/breaking/双parity、客户端无server/moejs依赖门禁，六平台×CLI/server/MyGo18项CGO0build。若源集扩展须先独立审和授权，不把整目录WIP纳入。
- [ ] 独立非作者Spec/Quality验收；author RED/GREEN与主全绿都不替代。经主精确stage/commit/SSHpush/readback/CI读回与nmem后才交付；不自建tag/Release，不把资产编辑当可应用Codex。B/C2/D及全部十四agent继续推进。
