# Gemini provider assets — GX-PROVIDER-ASSETS

日期：2026-10-30。**候选规格，待非作者 Spec / Quality 设计审；不是实现计划、源码授权或功能验收。**

## 1. Objective、身份与完整目标

让所有者通过 Gemini 专属 Web 表单维护 typed provider 资产：显式认证模式、endpoint、两处独立 model、单逻辑 API key 的安全 mutation，真实 owner/CAS CRUD；服务端 moejs 仅生成宿主独立验证的无秘密声明。保存不选择、不安装、不写设备文件、不测试凭据有效性。最终成功标准是下文 fixture 可逐项执行，不是给现有 metadata-only Gemini adapter 加一个“支持”标签。

- Cognate 事实基线固定 `4a2c0c0b5d8b0d35cc78cb0f2f38577f05a9a527`；早期研究 `cfbcbd4e53a7d0022a8d9d6526eb37a1c112bffd` 保留。已核这段差异包含 Codex typed domain/wire/Web，不再沿用“Draft 只有 Claude”的旧事实。Gemini builtin、neutral types 和 adapter 实现仍未获得 provider capability。shared WIP、native UI 待审 successor 不作为本规格前置合同。
- 上游固定本地 `/home/czyt/code/rust/cc-switch`，commit `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`、`package.json` version `4.0.21`，checkout clean。只读仓库，不启动 upstream、不读取真实 HOME、OAuth/ADC/账号/密钥或用户数据库。源码分支的注释/测试不是 Gemini CLI 运行时实测。
- 权威范围为 `docs/REQUIREMENTS.md` F6/F7/F12 与 `docs/CCSWITCH-CONFIGURATION-MATRIX.md`，不是上游 README 的十二项概述。完整目标仍覆盖 `claude`、`claude-desktop`、`codex`、`gemini`、`grokbuild`、`opencode`、`openclaw`、`hermes`、`pi`、`mcode`、`kimi`、`dsh`、`agy`、`omp` 十四项；本片只推进其中 Gemini provider 资产能力，不关闭完整 F12 或 M7。
- 下文枚举、字段、bounds、capability/version 都是本次**请求设计审批准的明确选择**；不把本作者选择称为代表已签。不分配 wire tag，不改既有 native protocol/auth。

### 1.1 单能力与后继边界

| 层 / 交互 | 本片合同 | 本片不能宣称完成的后继 |
|---|---|---|
| Provider assets | 所有者 list/get/read/create/update/copy/delete；API key mutation、revision CAS；专属 Web 表单 | provider 排序、设备 current/独占切换、当前引用删除保护、MyGo 同能力编辑 |
| Server target adapter | `GeminiProviderDesired` → 无秘密 `GeminiProviderDeclarationV1`；真实 moejs + host oracle | native-purpose Prepare/Authorize/Consume/Status 扩展、客户端无 JS pin/parity、完整 intent |
| env / settings | 独立表达 provider-owned 字段，显示两份脱敏逻辑预览 | `.env` 词法 codec、本机 settings patch、双文件 journal/publisher、真实 restore；不是通用 raw editor |
| 认证 | native-login 声明或 API key；不接管本机登录态 | Vertex/ADC/服务账号、GOOGLE_API_KEY 变体、自定义 header、更多传输机制、登录获取/刷新 |
| 来源 / Pack | 定义后继必须保留的类型、秘密与选择边界 | cc-switch 配置导入、来源版本转换、Pack 配置附件、订阅自动应用 |
| 其他 Gemini 能力 | 不被 provider payload 吞并 | agent MCP、`GEMINI.md`、agent-global settings/env、路由/代理/failover、模型发现/端点测速 |

这是一个资产能力的跨层规格，不是给以上后继自动授权。本片完成后 Gemini 仍不可由当前 native 消费链应用；不能把 assets/planner ready 变成 canApply，亦不能将 `agy` 当成相同 writer。完整 F12 依赖矩阵这些行继续 OPEN；收窄本次可编辑 variant 不等于从总需求删除其余模式。

## 2. 固定上游事实与不照搬的交互

以下路径均相对上述 cc-switch 固定根；行号用于定位固定源码，不代表运行测试。

| 事实锚点 | 实际事实 | Cognate 决策 |
|---|---|---|
| `src-tauri/src/live/project/gemini.rs:1` | provider 投影至 `.env` 与 `settings.json`；settings 只动 `security.auth.selectedType`、`model.name` | 两个独立 artifact；server 不给任意路径，不将 `.env` 当 JSON |
| `src-tauri/src/live/project/gemini.rs:18` | native selectedType 为 `oauth-personal` / `gemini-api-key`；从官方分类推导，不信旧行回填值 | 显式 typed mode 决定；不通过名称/推广位/endpoint 推断认证 |
| `src-tauri/src/services/provider/gemini_auth.rs:36` 与 `src-tauri/src/services/provider/gemini_direct.rs:39` | promotion/name/site 检测与 `category=official` 共同决定官方 | 不复制这些启发式；“Google”名称本身没有权限含义 |
| `src-tauri/src/live/floor.rs:166` | 上游拥有所有 `GOOGLE_*` 及指定 GEMINI/CODE_ASSIST 变量；不是全部 `GEMINI_*` | 不接受这个前缀作为 Cognate ownership 或 secret 分类 |
| `src-tauri/src/gemini_config.rs:298` | strict 验证空 env 放行，否则只检查 GEMINI_API_KEY / GOOGLE_API_KEY / GOOGLE_GENAI_USE_VERTEXAI 任一键存在；注释涉及 ADC | 键存在不等于有效认证；Vertex/ADC 不能冒充普通 API-key ready |
| `src-tauri/src/services/provider/gemini_direct.rs:29` | env 用 private file；同一 AppWrite 操作写两文件与 current；编辑也能写 live | Cognate asset save 只写服务端资产；未来两文件必须可恢复事务，不能声称 pair 的两次 rename 原子 |
| `src-tauri/src/services/provider/gemini_editor.rs:1` | provider 关键字段与 global edits 分开；全局编辑三方比较；旧行非关键内容保留 | 无 silent dropping、无全局回填到 provider、无保存资产即写 live |
| `src/components/providers/forms/hooks/useGeminiConfigState.ts:1`、`src/components/providers/forms/ProviderForm.tsx:1021` | hook 可分别保存 env GEMINI_MODEL 与 config.model.name；只有 agy 设置 preferConfigModel；raw hook trim/split/重复后值覆盖 | 本片两处 model 独立、有 presence，不静默同步/trim/last-wins |
| `src/components/providers/forms/GeminiConfigSections.tsx:1`、`GeminiFormFields.tsx:1` | env 与 settings 分编辑器；OAuth 提示、key/endpoint/model、获取模型/测速 | 专属表单与分文件预览；本片不发 endpoint 探测、不自动打开登录浏览器 |
| `src-tauri/src/live/project/gemini.rs:218` | 源测试含 `GEMINI_API_KEY_AUTH_MECHANISM=bearer`；proxy_contract 改为本地 proxy URL/placeholder，只留路由 model | bearer 字面值有源码依据，但未做 Gemini runtime 验收；proxy takeover 不是 direct-provider 原生配置 |

预设的商家列表、模型名称、探测注释不构成当前可用 endpoint/model catalog 或本片默认值来源。不导入上游调试错误的行内容/原文回显方式。OAuth 文件不在本片两文件 ownership 中；不需要识别或遍历任何 OAuth/ADC/keyring 文件名才能完成本片。

## 3. Typed 资产、presence 与 bounds

使用现有 `agent_id=gemini`、`kind=provider`、资产 `schema_version=1`，新的内部 envelope variant 为 `gemini-provider`、envelope version 为 `1`。它不是 `settings.json` 的 schema/version，也不复用 Claude/Codex payload。Draft 增加互斥 Gemini typed variant；一次且仅一次选择一个 agent payload，不能把 Gemini 对象放进 Claude 的字段。

### 3.1 非秘密 provider shape

| 字段 | 精确语义与有效域 |
|---|---|
| `authMode` | 必填枚举，仅 `native-login`、`api-key`；空/unspecified/unknown 拒绝。不是 bearer token 类型、不是 Cognate session |
| `endpoint` | optional 字符串；仅 api-key 可存在；缺省表示没有 provider endpoint override，而不是 hardcode 某 Google URL。存在为绝对 HTTPS URL，1..2048 UTF-8 bytes，有 hostname、端口若存在为 1..65535，无空端口/userinfo/query/fragment/反斜线/控制字符，URL 解码后 path 同样不得含反斜线/控制字符或非法 UTF-8；不 trim、不追加版本路径/尾斜线、不请求 DNS/网络。HTTP（包括 loopback）本片不接受；后续例外另签 |
| `keyTransport` | optional 枚举；api-key **必须显式存在**且为 `native-default` 或 `bearer`，native-login 必须 absent。native-default 声明“不设置传输 override”，不虚构 `x-goog-api-key` 对应的 env 枚举字面值；bearer 的未来落点见 §5 |
| `envModel` | optional，表示 `GEMINI_MODEL`；存在为 1..256 ASCII bytes，`[A-Za-z0-9][A-Za-z0-9._:/-]*`，大小写敏感，不 trim。不验证模型在线存在 |
| `settingsModel` | optional，同上 bounds，表示 `settings.json` 的 `model.name`；与 envModel 独立；可均 absent、只一项、相同或不同 |

所有输入须合法 UTF-8；非秘密 scalar 不接受 null、空白占位、Unicode control 或前后空白；已列举的 ASCII model 语法比上游宽松 raw editor 窄，超出者明确 unsupported/只读，不转换或丢弃。模型不同会显示警示“两个 native 来源不同；未验证运行时优先级”，仍可保存为资产；不伪称选中了有效唯一模型。

optional 使用真实 presence，不能用 `""`/zero/default 偷补。Update 提交完整 typed draft：原字段变 absent 是删除**资产中的 override**，不是 native 删除授权；本片不生成 remove。后续 native preflight 必须将 absent 对应的保留/受管清理决策绑定完整 intent：foreign 内容保留，只有可信 journal/ownership 证明且显式确认才可清理；无法证明不冲突时 fail closed。不能在 planner 默默继承本机值后宣称已验证。

### 3.2 累积限制

沿用已签资产边界：每 owner 最多 250 资产；名称 trim 后 1..128 UTF-8 bytes、无控制字符；expected revision 为合法正数，增长不能溢出。加密前 envelope 为 1..256 KiB；普通配置 HTTP request 128 KiB、response 1 MiB；现有 8 slots / 20s 及 body/strict-codec 边界不放宽。字节、行数均不得靠截断满足。

本 Gemini 非秘密 desired 的确定 JSON 编码 ≤8 KiB，planner request 含身份 envelope ≤16 KiB，接受的 declaration ≤16 KiB。字段分别合法仍需累计检查；没有任意 env/settings map、Struct、raw JSON、raw dotenv、path、command、header 或 account ID 逃生口。内部 stored envelope 含保留未知字段时扫描最大深度 16，总量仍受 256 KiB 限制；限制拒绝不能输出其内容。

## 4. 单槽秘密、模式切换与资产事务

唯一逻辑 secret slot 为 **`gemini.api-key`**，bearer 和 native-default 使用同一 key，不生成第二份 token。当前 assets API 的 keep/replace/clear 机制继续使用；不增 Reveal/MCP secret getter。`GEMINI_API_KEY` 的未来文件落点是这一 slot 的解析结果，不是 provider 非秘密 env 成员。

- replacement 沿用 1..8192 UTF-8 bytes、无 NUL/CR/LF、不 trim；空 replace 拒绝。包含引号、`#`、`$`、`=`、空格等仍是原始秘密字节，不能以 dotenv 拼接/插值改变；未来 codec 必须证明精确 round-trip 或明确不支持，不能默默损坏。secret 不进入 JS。
- 新秘密容器必须 opaque（包含私有容器时也不反射出 raw bytes），覆盖 fmt 所有动词/flags、value/pointer/slice/map/嵌套私有容器、JSON/Text、log、slog Text/JSON handler；不能仅加 Stringer/Formatter 后保留 raw 字段影子。复用容器若未通过该矩阵须先修边界，不能以既有类型名保证安全。generated DTO 本身不是 safe-to-log，`debug_redact` 不改变 Go 标准 String 行为。
- ReadDraft/Create/Update/Copy 响应只返回固定一项 `SecretStatus{slot=gemini.api-key,present}`，无原文/hash/长度/尾位。只表示 server envelope 的存在性，不表示本机 OAuth/key 已验证。任何配置内容（endpoint/model 也包括）不进日志；HTTP/业务错误只用固定类别，不回显字段或插件异常。

| 操作 / 转换 | 必须行为 |
|---|---|
| Create api-key | omitted/CLEAR→absent，允许存 needs-credential 草稿；KEEP 拒绝；REPLACE→present；无 key 时不能 planner 成功 |
| Update api-key | omitted/KEEP 保留同一 asset expected revision 的旧值；REPLACE 写新值；CLEAR 删除后 needs-credential；不能从并发新 revision 取 key |
| native-login | 槽必须 absent，endpoint/keyTransport absent；REPLACE 拒绝。readiness=credential-complete 仅表示不需 server key，不承诺已登录；model 字段仍可声明 |
| api-key → native-login | 有旧 key 时必须在本次 CAS 显式 CLEAR；omitted/KEEP 不得暗中清掉；UI 另确认。原不存在时 omitted/CLEAR 可通过；清理 endpoint/keyTransport 必须显式展示，不从 UI 静默丢草稿 |
| native-login → api-key | 显式选择 transport；可无 key 保存 needs-credential；不借用本机 OAuth/GOOGLE_API_KEY，不自动转 Bearer |
| Copy | 仅同 owner，校验 source revision；新 ID/revision=1；复制 typed 非秘密配置但**不复制 key**。API-key 副本 needs-credential；native-login 副本无 server key 需求。两者均不选择/应用 |
| Delete | owner + expected revision，仅 metadata/CAS 删除；不解密以决定能否删。删除服务端资产不删除本机文件或 OAuth，不伪报已解除 current |

读取一次获得同 revision metadata+payload；Update 在合并 secret/校验前比 expected revision，再用 Store CAS 写整个 envelope。非秘密配置与 credential 共用同一 envelope revision，无虚构的独立 credential-version。并发 KEEP/REPLACE/CLEAR 只能一个成功；冲突不得自动重试，Copy 与原资产竞争不能拼接版本。Store 保持加密落盘，事务里不跑 moejs/网络/磁盘回调。

所有 owner 来自现有认证服务，不接受请求自报 owner；跨 owner 的读取/更新/复制/删除不得泄露存在性。ctx 取消不回成功 view；commit unknown 不宣称 rollback，不自动重复 create/copy。保存操作没有消费 grant、写客户端、Pack/选择或后台 apply 副作用。

## 5. Native ownership / OAuth 与后继硬门

本片只冻结最小**逻辑 ownership 上限**，不授权 native 写入；以下允许项也必须在后继 codec/pin/preflight/intent/授权/journal 全部接受后才启用。`gemini.settings` 是现有逻辑 target，manifest `format=json` 不代表 `.env` 是 JSON 或允许 generic Render 输出两份文件。

| 逻辑声明 | 后继最多允许的 native 落点 | 本片禁止 |
|---|---|---|
| api-key slot reference | Gemini root 下 `.env` 的精确 `GEMINI_API_KEY` | actual key 下发 Web/MCP、整份 env 上传/覆盖 |
| api-key endpoint | `.env` 的 `GOOGLE_GEMINI_BASE_URL` | 认领整个 `GOOGLE_*` 前缀、凭 endpoint 推断模式 |
| api-key transport=bearer | `.env` 的 `GEMINI_API_KEY_AUTH_MECHANISM=bearer`；native-default 不声明 override | 猜测其他机制字面值、将 Bearer 作为另一 secret slot |
| envModel | `.env` 的 `GEMINI_MODEL` | 用 settingsModel 自动覆盖它 |
| settingsModel | `settings.json` 的 `/model/name` | 覆盖 `/model` 的其他字段或整个 settings |
| authMode | `settings.json` 的 `/security/auth/selectedType`，分别为 `oauth-personal` / `gemini-api-key` | 覆盖 `/security/auth` 其他字段、账号数据、OAuth/ADC/keyring 文件 |

所有未列项归用户/其他能力：`GEMINI_SANDBOX`、`GEMINI_CLI_HOME`、`GEMINI_SYSTEM_MD`、telemetry、UI、tools、permissions、hooks、MCP、项目/工作区设置等必须保留。settings 未知 sibling 与 dotenv 无关行/注释/顺序/换行不得为便利而重新序列化丢失；既有 managed/foreign 的区分由真实 journal/baseline 决定，文件名/键名相同不等于已被 Cognate 接管。

下列是**已识别但本片不支持的 auth/routing 输入**，不是可放心忽略的普通 extras：`GOOGLE_API_KEY`、`GOOGLE_GENAI_USE_VERTEXAI`、`GOOGLE_APPLICATION_CREDENTIALS`、`GOOGLE_CLOUD_PROJECT`/location、`GEMINI_CLI_USE_COMPUTE_ADC`、`GEMINI_DEFAULT_AUTH_TYPE`、`GEMINI_CLI_CUSTOM_HEADERS`、`CODE_ASSIST_ENDPOINT`/version、外部认证选择器。GOOGLE_* 其余未知键同样无 ownership；可能含秘密或影响认证，不反映到公共预览。

如果后继发现这些变量/设置、继承环境/额外 env 搜索根/项目配置影响有效认证，不能“保留然后声称应用正确”，必须 unsupported/conflict 停在授权前，直到对应 typed variant/host validation/本机环境 precedence 证据获签。未知 selector/混合 key/不能确认的外部 auth 也不能降级为 missing/native-login。本片无 native readiness 探测，不能为了判断“OAuth 存在”读取登录文件。

未来 native 最低门禁：客户端只访问 Cognate server、不跑 JS；先 key admission→释放锁→fresh ctx→RPC；经既定 Begin/Bind/Prepare safe declaration，preflight 无秘密地绑定两 artifact 的完整基线/ownership/未解析策略，再构完整 intent→Authorize→Consume；逐槽人类批准，最终批准初始焦点安全（Enter 不得默认执行）。proof 仅持钥证明非 attestation。不得为 Gemini 发明新 auth/descriptor API 或抢先扩 native wire；现协议如不足以表达双 artifact，应先提交最小兼容合同供独立审。

两文件 publication 必须纳入同一可恢复记录和 selection outcome，先备份，严格冲突/链接/root/权限检查，`.env` 私有权限；不能持 install/target 锁等待用户确认或网络。丢 Consume 响应/unknown 只查询 Status，不重取 key；Consumed≠Applied；部分写入/取消/重启要可 reconcile、preview-confirm Restore。Windows/macOS 的 kernel 未获证据不得报 applied。以上全部是**后继验收条件，非本片实现状态**。

## 6. Server-only moejs 声明与 host 独立验证

候选入口 `Registry.PlanGeminiProvider(ctx, GeminiProviderPlanRequest) (GeminiProviderDeclarationV1, error)`，neutral types 归 `internal/agentconfig`；不依赖 server/client/gen。身份使用现有 envelope 字段 `APIVersion/PluginID/PluginVersion/DefinitionDigest/Target/Capabilities`，候选 `builtin.gemini@1.1.0`、`agent=gemini`、`target=gemini.settings`、`format=json`、唯一 capability **`gemini.provider-plan.v1`**。旧 1.0.0/empty-capability 行继续 metadata-only，不能降级授权。

- desired 精确为 §3 typed provider + `keyPresent` bool；native-login 要求 false，api-key 要求 true。assets 允许 missing-key draft 不代表 planner 允许。hook `planGeminiProvider` 仅收到校验且深复制的 desired；不含 owner/asset、envelope、raw secret、baseline、path、environment/extras map。无 host fetch/FS/DB/日志 bridge，无动态代码能力。
- declaration 精确成员：`version=1`、`target=gemini.settings`、`authMode`、上述 optional endpoint/envModel/settingsModel/keyTransport 原样 presence/value、`selectedType`（按 §5 计算）、optional `credentialReference`、有序 `requiredSlots`、固定 `localAuthPolicy=preserve-local-login-v1`。api-key reference 为 `gemini.api-key` 且 slots 精确单元素；native-login reference absent、slots 精确空列表。不返回 actual file、remove/set map、登录状态、resolved local decision、credentials、任意 path/diagnostics。这个 policy 仅表示不取得/修改本机登录凭据，不证明某 OAuth 已就绪。
- Go host **自行由 desired 重算完整 expected declaration**，严格比较成员/顺序/数量/每个 presence/value/slots/policy，拒绝未知成员、重复 key、null、错误数字/enum/类型、其他 agent/capability、秘密/命令/路径注入、JS 擅自默认值或删 model；最后构造受控返回值，不转发 JS raw object。只验证“在 allowlist”而不验与输入一致不合格。
- 沿用 Registry definition digest 与真实 source pin，不接受 caller 自报 digest 作为信任。unknown plugin→unsupported，wrong identity/digest/caps→incompatible，bad desired→invalid request，module exception→固定 execution 类，伪造输出→固定 result 类；不回 raw source/stack/input。generic Render/RenderJSON 不获得 Gemini .env/settings 输出能力。
- ctx/bounds/identity 在跑 moejs 前检查；返回前 fresh ctx gate；保持现有 `plugin.RunJSON` 的 3s、16 MiB memory、最大 1 MiB 输出/50000 nodes，真实 interpreter 停止，不遗弃 goroutine。16 KiB declaration 是本 hook接受界，不谎称 runtime 解码前仅分配 16 KiB。不持 DB Tx 跑 JS。
- 输出不含 secret 不代表 safe-to-log：provider 配置仍私有，禁止任何 request/body/module/result 日志。Registry 可并发调用，deep clone optional/output，无结果/caller 共享可变别名；不承诺 caller 并发篡改输入安全。

## 7. 兼容、未知字段、资产 HTTP 与公开边界

1. 沿用普通 `ConfigurationService` 七个 RPC，不增加 Gemini 专用 auth。Draft 的 Gemini variant、ReadDraft 的同 typed view 是 additive，具体 tag 由 proto owner 在后续 plan 与当时 schema checkpoint 统一分配，**不在此抢用已存在的 Claude=2/Codex=3**；旧 tag/enum 不重用。Go/TS 由正常 Buf 生成，不能手改生成器 String 或以 JSON map 代替 typed oneof。
2. 新请求的任何 unknown oneof/enum、nested JSON 成员、binary unknown fields、重复 singular/oneof/map 字段、错误类型/越界均 fail closed；非秘密名字也不回显。沿用现有普通 CRUD 的 Connect/JSON 与严格 codec 政策，不借本 spec 放宽 transport；native 六 RPC 专属、no-Web 的严格 proto 边界不变。
3. List/Get 返回该 owner 所有合法 storage metadata，未知 agent/schema/variant 不拖垮混合列表。storage kind 未知在 wire fail closed；agent 字符串不是 apply 能力。Delete owner/CAS 不解码 payload，保持旧客户端能管理合法 metadata。
4. ReadDraft 对已知 Gemini v1 且已知字段可验证的 stored envelope，未知额外成员只设置 `hasPreservedExtras=true`，返回已知非秘密 projection + 固定槽 presence，全部内容只读；opaque extras 留在加密 envelope，**不返回未知值/键名/raw preview**。Edit 表单不能据这个 projection 重建覆盖原文。未知版本/variant/auth 或已知字段错误，ReadDraft 返回固定 unsupported/invalid，不伪造可编辑空 draft。Update/Copy/planner 对 extras/unsupported 都拒绝，不丢弃后保存。
5. “只读”不等于可读取未知敏感内容。新请求 unknown 与既存存储 forward-preservation 是不同通道；本片没有 raw import/extra mutation API。未来 importer 必须按版本、敏感分类与授权转换；无法分类即保留私有 opaque 数据且禁止 typed 应用，不暗中上传本机整文件。
6. 凭据仍加密落盘；普通 API 永远无 Reveal，MCP 无 raw/materialize/新 scope，公开目录不含配置，默认 Pack 分享不带配置或 key。Web 可向 owner API 发送用户刚输入的一次 replacement，但只得到脱敏结果；不可取回已存 key/本机 OAuth。后继 Pack 必须显式选择资产 revision、共享范围与秘密授权，不能自动附加本片新资产。

## 8. Gemini 专属 Web 表单

复用现有 React/Radix/Tailwind/i18next 与 Configuration page 生命周期，不加框架/依赖。表单使用专属组件/typed state，不复用 Claude 标签或将 Gemini 塞 raw JSON。主列表可显式选 Gemini 新建；所有按钮正确区分“保存服务端资产”“复制非秘密配置”“删除资产”；没有“本机已应用”提示或虚假 switch。

- 显式 mode selector（native-login / api-key）；native-login 显示“由原生 Gemini 登录；Cognate 不读取/导入/刷新登录凭据”。API-key 显示 endpoint optional presence、transport 必选、key KEEP/REPLACE/CLEAR；创建默认 CLEAR/空 key，编辑默认 KEEP，不以遮罩字符串当旧 key。
- envModel / settingsModel 两个独立 presence 控件/标签，显示各自 target；不把 absent 变空字符串，不互相同步。mode 切换会使字段/已存 key 不兼容时，需保留草稿到用户确认或取消，清理列表明确，不偷偷 CLEAR。CLEAR 旧 key、删除资产、离开 dirty 草稿均单独确认；初始焦点是取消/保留，初始 Enter 无副作用，主动键盘选择批准才成功。
- 分 `.env` / `settings.json` 两份**只读、脱敏、provider-owned 逻辑预览**：API key 只显示 slot reference/present，非实际序列化 env；明确“不含全局/未知内容，非可直接安装文件”。没有以 JSON 解析 dotenv；没有 full-file raw editor 或未分类 extras 上传；F12 的受控原始编辑由后继 codec/ownership 能力完成，不能用本预览冒充。
- owner/origin/asset/revision 变化、取消、logout、卸载视图会 abort 在途操作并以 generation 防旧结果/旧错误回填；快速重复保存有可取消串行准入，disabled 不是唯一后端门。CAS conflict 保持非秘密草稿并要求重新读取/比较，不自动重试覆盖；所有失败只显示固定分类/字段 ID，不显示 raw error/URL/正文。
- replacement 只在受限内存/输入控件活跃期间存在，不进入列表/cache/query key/router/history/URL/localStorage/sessionStorage/IndexedDB/console/telemetry/DOM data 属性。submit 成功/失败、校验拒绝、切模式放弃、取消、语言切换、owner/origin/资产切换与 unmount 都清 DOM/ref/DTO/mutation 引用；主题切换不丢非秘密草稿。JS 字符串/GC 副本不能保证物理清零，报告必须披露；清引用不冒称已擦除全部内存。
- 简中/English、亮暗/系统主题、明确 label/aria 与字段关联错误、键盘完整操作。使用成熟 Dialog/Select/Input/Checkbox 组合；表单反馈无额外动画/焦点陷阱。浏览器真实输入/焦点/导航/网络与脱敏检查是验收，不把纯状态测试当点击证据。

## 9. 项目结构、style 与边界

后续 plan 根据本规格冻结**精确写集与串行 owner**，本表仅标已有职责，不授权这些路径实施或自授第 N 文件：

| 接缝 | 所在位置 / 规则 |
|---|---|
| neutral desired/declaration | `internal/agentconfig/`；无 server/gen/client、JS/文件依赖 |
| moejs + host oracle | `internal/server/agentadapter/`；复用 Registry/Builtins/digest，不改其他 agent module |
| assets/envelope | `internal/server/configuration/`；复用现有 Store atomic-read/encryption/CAS，不新增 ent 表或 credential version |
| ordinary HTTP / wire | `internal/server/httpapi/configuration*.go`、`proto/skillhub/configuration/v1/configuration.proto`、正常 Go/TS gen；严格 codec、旧 agent 回归 |
| 专属 UI / fixtures | `web/src/configuration*` 及既有真实浏览器测试目录；复用页面 auth/generation、i18n/UI primitives，不修改 native 入口 |

Go 1.27.3、CGO0、gofmt、具名类型与零结果+固定 error；presence/secret 容器不导出 raw 字段；JSON 用既有 json/v2 strict/deterministic 规则；TS discriminated union、不把 presence 压成 truthy bool；JS named export、纯映射。最小非秘密声明片段（不是完整 output 或 wire）：

```json
{"authMode":"api-key","keyTransport":"bearer","credentialReference":"gemini.api-key","requiredSlots":["gemini.api-key"],"localAuthPolicy":"preserve-local-login-v1"}
```

- **Always**：synthetic TempDir/HOME/keys、源码版本与 fixture provenance、真实 red→green/独立负控、secret-free fixed diagnostics、旧 Claude/Codex 行为回归；实现前完整 Modern Go 指导。
- **Ask first**：新依赖/表/transport/secret API/native wire 与权限、扩 auth modes/ownership/HTTP 例外、超后续签定写集、真实外部 provider/runtime 测试；当前不读真实配置。
- **Never**：本片下发可安装 patch/JS/命令/绝对路径、执行客户端 moejs、自动申请 OAuth/读取 ADC/keyring、secret 日志/公开分享/浏览器持久化、以 rawJSON 充当完整交互、静默忽略 unknown、stage/commit/push 或提前实施。

## 10. 可独立执行 fixture 矩阵与验收

所有下列运行结果目前 **NOT RUN / PENDING**；这是设计要求而非作者声称的已有实现。fixture 数据完全合成，公开上游源码只能作映射事实，不复制真实凭据。故意撤销校验的 overlay 必须先 compile0 后实际 RED，不能把编译失败/no-tests 算负控成功。

| ID / 组 | 正向、失败与反例 oracle |
|---|---|
| G01 模式/presence | native-login 无槽、API-default/bearer 单槽；endpoint absent/present；envModel/settingsModel 四种 presence 和不同值全保留；unknown/unspecified/null/empty/错误 mode 组合拒绝；不按名称推模式。模型不在线探测 |
| G02 bounds | 名称/URL/model/secret/envelope 各 min/max/+1；UTF-8/控制/前后空白、URL userinfo/query/fragment/端口/解码 path、原字符串保留；检查最大合法 desired 的确定编码仍在累计界内，request/output/storage 的可达超界反例拒绝、不截断，不伪造一个字段全合法却无法构造的累计超界案例 |
| G03 mutations | Create omitted/clear/keep/replace；Update omitted/keep/clear/replace；重复/未知槽/action、替换中 CRLF/NUL/非法 UTF-8；模式切换显式 CLEAR、Copy 无秘密；特殊 secret 字符字节不变 |
| G04 SQLite/CAS | 真加密 Store+真实 domain 读/写/重启；密文文件无 sentinel；同 revision metadata/payload；多 Store 并发 KEEP/REPLACE/CLEAR 一个成功；stale update/delete/copy 无副作用；取消与 commit unknown 不自动重发 |
| G05 owner/HTTP | 真实 Connect proto 与 JSON CRUD，owner/session/anonymous/跨 owner；混合合法未知 metadata、storage kind fail-closed；128 KiB body/1 MiB response/8slots/20s；取消实际 drain，无原始错误回显；不新增 MCP/public/native 业务入口 |
| G06 strict codec | nested unknown/重复 JSON 与 protobuf singular/oneof 字段、unknown enums/binary fields、null/类型/深度/溢出均拒绝；Claude/Codex 既有 vectors/parity 仍绿；旧客户端不误把 Gemini 当 Claude |
| G07 opaque/隐私 | fmt 非常规所有 verbs/flags，value/pointer/slice/map/私有嵌套，JSON/Text、log+slog 两 handler；真实 HTTP 成功 create/update、非法 mutation/codec/oversize/取消的 stdout/stderr/error/log 捕获无 synthetic replacement/session。原 raw DTO String 仍不安全的事实保留，禁止生产记录它；放回 raw 容器或新增 request logging 的独立负控真实 RED |
| G08 stored unknown | 已知 v1+嵌套 extras：已知 view 只读，未知值/键名不出响应，Update/Copy/planner 不执行，原 envelope 字节不变；unknown schema/variant/auth、损坏已知字段拒绝 typed 读；List/Get/Delete metadata 仍可用。无“删 extras 后重建” |
| G09 moejs/host | 真实 module 经 RunJSON；身份/version/digest/caps/target 严格；host 全字段 expected 与确定输出相等；深 clone 不别名；真实取消/3s 限界 interpreter drain；generic Render 仍拒绝 Gemini 文件输出 |
| G10 恶意 module | 返回错误 selectedType/model/secret ref/slots/order/policy、unknown/path/command/header/env map、null/重复字段、增删 optional、超界输出/exception；编译后的测试替换 module 为 schema 合法但单值错的结果，必须被 host 拒绝；停用 host 独立比较则真 RED。无插件 IO/secret 输入 |
| G11 Web 真交互 | 专属 Gemini 表单、两份逻辑预览、两种模式、独立 models、KEEP/REPLACE/CLEAR/复制/删除/CAS/脏草稿；初始 Enter 取消与主动批准成功；不同主题/语言/小视口/键盘；实际 server+合成 SQLite+浏览器网络证据，不以 mock 状态替代 |
| G12 stale/privacy UI | 真实延迟响应后 owner/origin/asset 切换，不回填；取消/validation/failure/unmount 清 replacement；持久存储/缓存/console/日志/URL/DOM 非敏感属性无 sentinel。放回旧 generation/clear 路径后负控 RED；不输出 sentinel 本身 |
| G13 本片无副作用 | CRUD/planner/页面不会创建 native files/current/selection/Pack、不会发 provider 请求或启动 OAuth；keys 全留 envelope/输入限定通道；无 native Go/TS 业务入口进 Web；无 moejs 进 CLI/MyGo 依赖。设陷阱 FS/HTTP fixture 的调用计数必须为零 |
| G14 后继 native / F12 OPEN | 后继独立验 dotenv quoting/duplicates/comments、settings sibling/MCP 保留、Vertex/ADC/unknown auth 阻断、两 artifact 冲突/备份/崩溃恢复、授权/失包/Status/真实 restore、无 JS pin parity、MyGo parity、导入/Pack 链路。未运行这些不能称 Gemini 可应用，更不能称十四 agent 完整 |

### 10.1 后续实施命令要求（本轮未执行）

实际 plan 须记录精确 synthetic HOME/XDG、工具版本与每条 exit；下列为仓库已有工具/路径上的门禁入口，不是新 test 存在或全量通过声明。不得用真实 HOME 启动 server/agent，不跑 upstream/cargo，不改全局代理。

```sh
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go version
CGO_ENABLED=0 go test -mod=readonly ./internal/agentconfig ./internal/server/agentadapter ./internal/server/configuration ./internal/server/httpapi -count=1
CGO_ENABLED=0 go test -mod=readonly ./internal/configclient ./internal/configapply ./internal/nativesessionauth -count=1
CGO_ENABLED=0 go vet -mod=readonly ./internal/agentconfig ./internal/server/agentadapter ./internal/server/configuration ./internal/server/httpapi
buf lint
buf format --diff --exit-code
npm --prefix web ci
buf generate --template buf.gen.yaml
buf generate --template buf.gen.web.yaml
npm --prefix web run typecheck
npm --prefix web run build
CGO_ENABLED=0 go test -mod=readonly ./... -count=1
CGO_ENABLED=0 go vet -mod=readonly ./...
go mod verify
git diff --check
```

Buf 必须在隔离生成目录/clean export 做 parity，不能污染并行写集；breaking against 签定实施 base，所有已存在 tag/enum 保留，Web 生成仍排除 native。没有现成 `npm test` script，不虚造浏览器测试成功；后续 plan 必须选仓库实际的测试工具/合成启动 fixture、逐项列真实输入与网络/隐私 oracle。Go focused Gemini tests 必须检查确实匹配并执行、加重复/负控；CGO0 不声称 race 验收。

## 11. 设计签收条件、未决门禁与不签范围

请独立 reviewer 逐项裁定：① native-login/API-key 两 variant，Vertex/ADC/Google-key/header 等先只读并保留 F12 阻塞；② 单槽 `gemini.api-key`、native-default/bearer 与 mode-switch 显式 CLEAR；③ 两处 model 独立 presence、HTTPS 收窄与最小 ownership；④ 只有无秘密声明而非 install patch，unknown/extras 不可 typed 修改/复制/应用；⑤ 专属 Web CRUD/预览与后继 raw/native/Pack 分离。不同意应改本规格再签，不留给实现者猜。

后续 plan 准入还须冻结精确 path/串行 owner、wire additive tag、现有 secret wrapper 的真实可复用安全边界、真实浏览器 harness/验证命令；这是可执行接缝门，不代表当前有这些实施授权。任何额外 auth mode/native codec 或新依赖需独立规格/裁定，不从此推导。

本规格签收至多允许进入本资产能力的实施计划；**不签** Gemini CLI 运行兼容、OAuth/ADC readiness、双文件原子性/恢复、native 消费/应用、MyGo 功能对等、来源/Pack/路由/模型发现或全十四 agent/F12/Release 完成。作者本轮只完成文档与固定源码事实核对，无 Go/JS/proto/module/运行环境改动。
