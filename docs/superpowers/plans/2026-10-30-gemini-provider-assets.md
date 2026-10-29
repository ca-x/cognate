# Gemini Provider Assets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task-by-task after separate source authorization. Steps use checkbox (`- [ ]`) syntax. Main assigns independent reviewers; this plan does not authorize spawn, stage, commit or push.

**Goal:** 实现 GX-PROVIDER-ASSETS 的真实 owner-scoped typed 资产、无秘密服务端 Gemini planner 和专属 Web CRUD；保存绝不变成本机应用。

**Architecture:** 沿用 encrypted configuration Store 与同 envelope CAS revision；普通七 RPC additive 扩展，客户端不跑 moejs。先补现有 Secret 的真正 opaque 边界，再按 neutral contract 分开 domain 与 Registry；HTTP/正常生成物串交后接成熟 Web 表单和真实服务浏览器 fixture。没有新的 native-purpose API、codec、授权或本机 writer。

**Tech Stack:** Go `1.27.3`、`CGO_ENABLED=0`、`encoding/json/v2`/`jsontext`、现有 ent/SQLite/Connect/Buf/moejs `v0.1.0-alpha.11`；现有 React/Radix/Tailwind/i18next/TanStack。无新依赖/框架/数据库表。

**Spec:** `docs/superpowers/specs/2026-10-30-gemini-provider-assets.md`，SHA256 `1f7a4b085a6b173de5817f6e47e35a4c44215e137d1e0b5ca760176f9836fbba`。Gauss Spec/Quality ACCEPT 报告 `gemini-provider-assets-spec-review.md` SHA256 `1a977b7126aa5f450dcdb5faf241190ebcd648e948981a2365c2eb1c8d1bbeb5`；主已接受 §11 五项选择。spec 的历史 candidate 标题不改变这次签收身份。

## Global Constraints

- 观察/未来重构 base 固定已交付 main **`3f687a9211ab6d3e71af9f49f04c61fc52d27fc7`**；相关 agentconfig/adapter/domain/普通 proto/HTTP/Web 接缝相对 spec base `4a2c0c0b5d8b0d35cc78cb0f2f38577f05a9a527` 已核无变更。实施前核 blob；main 后续无关提交不混 candidate，接缝变化须先串交。cc-switch 事实仍固定 `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`，不重跑 upstream/读用户 HOME。
- Auth 仅 `native-login` / `api-key`；唯一 `gemini.api-key`，旧 key→native-login 必须 same-CAS 显式 CLEAR。API transport 为显式 `native-default` / `bearer`；native-login endpoint/transport absent、无 server key。native-login complete 不是本机已登录。
- endpoint optional、仅 HTTPS、1..2048 UTF-8 bytes；models 各 optional、1..256 ASCII bytes，`[A-Za-z0-9][A-Za-z0-9._:/-]*`；两处 presence/value 独立。provider 值不 trim/补默认/探测；名字沿原 trim 后 1..128 bytes。URL 的 userinfo/query/fragment/空端口/反斜线/控制字符及 decoded path 非法值拒绝。
- replacement 1..8192 UTF-8 bytes、无 NUL/CR/LF、不 trim。每 owner 总计 250 assets、envelope 1..256 KiB；desired ≤8 KiB、planner request/declaration 各 ≤16 KiB。普通 HTTP request128 KiB/response1 MiB、8slots/20s operation ctx；plugin 保持3s/16 MiB/1 MiB输出/50000nodes。hook 接受界不冒称 runtime 解码分配界，普通 operation ctx 不冒称 native socket 尾 flush 保证。
- metadata 可 list/get、owner/CAS delete 不解码；unknown stored extras 私有只读且禁止 typed update/copy/planning。新请求 unknown/nested/duplicate/enum/null/type fail closed。秘密不进 JS/日志/公开目录/默认 Pack/浏览器持久化；DTO 的 debug_redact 不使 Go String 安全。
- Gemini builtin 此前 metadata-only，升级仅为无秘密 declaration capability；**没有 install patch/canApply/Applied**。Vertex/ADC/GOOGLE_API_KEY/header/raw 编辑/native 两 artifact/更多 auth/MyGo parity/import/Pack/路由/发现以及十四 agent 完整 F12 均继续 OPEN。
- 本轮仅计划文档，以下源码清单是待独立设计审与主另授的请求，不是已经获得源码权限。禁止改 shared spec、Go/proto/JS/module/index 或提前开工。

## Review Focus

1. 旧 `Secret{data []byte}` 在私有嵌套 fmt 容器中不能凭 Stringer 视为 safe：Task0 必须先真实 RED，再消除 raw-bearing字段；不以 DTO 改 String 掩盖。
2. KEEP 与竞争提交可能混 revision，contended SQLite BUSY 也不等于 CAS conflict：Task2 三个受控 schedule、同快照和 zero-view/no-retry 负控。
3. enum absence、不同 model 与 native-default 容易被 UI/mapper 自动补值：Task1/3/4 分别验证全部 presence 和原样值，协议 null 不能折叠 absent。
4. unsupported metadata / stored extras 容易冒充 editable/planner ready：Task2/3/4 各自拒绝，List/Get/Delete 混合资产仍可用；旧 native loader不扩成Gemini。
5. 真 COMMIT 后丢包/locale或owner切换，晚成功可能覆盖新 UI 或重发保存：Task3/5 用真实 Store commit witness+响应屏障/截断，不用 sleep、假成功 response 或 timeout 作 oracle。

## 1. Exact paths、串交与交付边界

### 1.1 请求的生产/测试写集

总计 **35 手写路径 +2 正常生成路径 =37 tracked 源路径**。序号唯一；Task0/2 与 Task0/3 有明确重复消费，不把重复文件多算。没有 docs/CONFIGURATION、CI、module、ent、auth/proof/state 产品改动；若实际闭包需要第38路径，先给编译/行为反例申请，不能偷拓。

| ID | 路径 | owner / 最小职责 |
|---|---|---|
| 01 | `internal/server/configuration/types.go` | domain owner，T0 Secret 定义迁移；T2 Gemini pointer/slot/typed view |
| 02 | 新 `internal/server/configuration/secret.go` | domain owner，T0 opaque Secret 实现/私有访问/生命周期 |
| 03 | 新 `internal/server/configuration/secret_test.go` | domain owner，T0 真私有容器 fmt/JSON/log/slog、ownership/旧业务回归 |
| 04 | `internal/server/configuration/domain.go` | domain owner，T0 无raw字段访问；T2 closed classifier/CRUD薄分派 |
| 05 | `internal/server/configuration/envelope.go` | domain owner，T0 仅 Secret 读取/清理适配；Claude 持久字节/错误语义不变 |
| 06 | `internal/server/configuration/codex_domain.go` | domain owner，T0 Secret 适配；T2 现 loadEditable/metadata 门加 Gemini，不搬家/泛化 native load |
| 07 | `internal/server/configuration/codex_envelope.go` | domain owner，T0 Secret 读取/清理适配，Codex envelope 语义不变 |
| 08 | `internal/server/configuration/native_consumer.go` | **仅T0** Secret 访问/清理替换；不改算法、stage proof顺序、资格、事务、agent gate或wire |
| 09 | 新 `internal/agentconfig/gemini.go` | adapter owner，neutral structs/constants；类型checkpoint供domain消费 |
| 10 | 新 `internal/server/agentadapter/gemini.mjs` | adapter owner，纯 planGeminiProvider 映射 |
| 11 | 新 `internal/server/agentadapter/gemini_plan.go` | adapter owner，Registry gate/bounds/clone/独立host expected |
| 12 | 新 `internal/server/agentadapter/gemini_plan_test.go` | adapter owner，真实moejs/恶意模块/取消/compiled负控 |
| 13 | `internal/server/agentadapter/builtins.go` | adapter owner，新增Gemini embed/module分派，不改Claude/Codex模块 |
| 14 | `internal/server/agentadapter/builtins.json` | adapter owner，仅Gemini1.1.0精确capability升级 |
| 15 | `internal/server/agentadapter/registry.go` | adapter owner，仅Gemini合法组合分支 |
| 16 | `internal/server/agentadapter/registry_test.go` | adapter owner，新身份/旧metadata-only显式fixture与既有agent回归 |
| 17 | 新 `internal/server/configuration/gemini_domain.go` | domain owner，validate/clone/mutation/CRUD/只读projection/desired快照 |
| 18 | 新 `internal/server/configuration/gemini_envelope.go` | domain owner，严格stored scan/已知projection/extras-preservation |
| 19 | 新 `internal/server/configuration/gemini_domain_test.go` | domain owner，真SQLite/owner/CAS/三schedule/copy/quota/rollback与unknown |
| 20 | 新 `internal/server/configuration/gemini_envelope_test.go` | domain owner，schema/unknown/null/重复/bytes/AAD与projection隐私 |
| 21 | 新 `internal/server/configuration/gemini_boundary_test.go` | domain owner，同包真实native拒绝/registry parity/零副作用 |
| 22 | `proto/skillhub/configuration/v1/configuration.proto` | transport owner，§3 additive tags；仅普通配置协议 |
| 23 | `internal/server/httpapi/configuration.go` | transport owner，**仅**Create/Update拥有的SecretMutation离开时清理，旧7RPC/slot/auth/ctx不改 |
| 24 | `internal/server/httpapi/configuration_mapping.go` | transport owner，Gemini dispatch；部分构建失败的mutation清理 |
| 25 | `internal/server/httpapi/configuration_codex_codec.go` | transport owner，现JSON scanner增加Gemini strict subtree；不改Claude legacy null/packed行为 |
| 26 | 新 `internal/server/httpapi/configuration_gemini_mapping.go` | transport owner，strict双向typed/presence/slot/readiness映射 |
| 27 | 新 `internal/server/httpapi/configuration_gemini_test.go` | transport owner，真Connect proto/JSON owner生命周期、limits/取消/COMMIT故障 |
| 28 | 新 `internal/server/httpapi/configuration_gemini_protocol_test.go` | transport owner，wire/JSON negatives、旧descriptor与Buf回归 |
| 29 | 新 `internal/server/httpapi/configuration_gemini_logging_test.go` | transport owner，真实log/slog/HTTP隐私与窄静态gate |
| 30 | 新 `internal/server/httpapi/configuration_gemini_browser_test.go` | transport owner，**test-only**真实Store/Web fixture+控制屏障，详§7，无生产hook |
| 31 | 新 `web/src/configuration-gemini-state.ts` | Web owner，typed转换/验证/guard/逻辑预览数据 |
| 32 | 新 `web/src/configuration-gemini-form.tsx` | Web owner，专属Radix表单/显式清理确认/原生textarea非必需 |
| 33 | `web/src/configuration-state.ts` | Web owner，closed view union+Gemini dispatch；复用既有request/generation，不重写生命周期 |
| 34 | `web/src/configuration.tsx` | Web owner，第三目标、editor/copy/只读/写结果及明确范围文案 |
| 35 | `web/src/locales/en.ts` | Web owner，新中文key对应English；不另造翻译框架 |
| G1 | `gen/skillhub/configuration/v1/configuration.pb.go` | transport唯一owner，正常Buf Go输出 |
| G2 | `web/src/gen/skillhub/configuration/v1/configuration_pb.ts` | transport唯一owner，正常Buf Web输出 |

08 的必要性可由当前 `.data` 实际访问证明：nativeDesired 和 Consume materializer 都直接读/clear Secret.data；只改 Secret 无法编译。不允许 raw兼容影子绕过。其基线 blob 为 `afcaed32e3db3ddb75de85204ee3f113c9c6cc22`，本计划只申请纯访问迁移；后继 Codex native作者若拥有此文件，main须先完成明确单文件串交才能授权T0，不能并写。state.ConfigurationPayload、raw stored-envelope与generated DTO仍是敏感内部载体，禁止整体日志，不因Secret修复宣称它们所有fmt边界也安全；本片不扩state写集。

### 1.2 构建顺序 / 可并行性

- Task0 foundation 与 Task1 adapter 可以独立隔离并行；Task1 的09先冻结**接口checkpoint**供domain消费，不把只有types称Task1完成。
- Task2依赖Task0通过+09冻结；domain owner继续串行01/04/06。Task1 module/host可同时做，不能与domain共享Registry文件。Task2最终parity门等完整Task1接受。
- Task3单owner串行proto/gen/HTTP；开始前接Task2精确domain快照与已通过Task0。Web可在G1/G2/wire identity冻后开发，但实际浏览器验收必须等完整Task3合并。
- Task5消费全部已冻切片构精确组合；30由transport owner维护，Web作者不并改fixture。每片交base/tree/blob+mode/SHA/patch与实际logs；主单独决定整合/提交。本计划本身只进设计审。

## 2. Task0 — 先闭 Secret 安全复用边界

**Files:** 01–08。**Consumes:** 现 `NewSecret([]byte) Secret`、所有 `.data` 使用点。**Produces:** 同名公共类型/构造器与新增 `(*Secret).Clear()`；`SecretMutation` 调用面不变。

- [ ] 新 `TestConfigurationSecretPrivateContainers` 使用直接值/指针、mutation/credentials、slice/map以及不可导出字段的嵌套容器；fmt正常/非常规verbs与flags、Unicode/raw/hex/base64/decimal byte-sequence oracle，JSON/Text、标准log、slog Text/JSON handler。只能打印case ID/布尔，不输出sentinel。旧实现应 compile0 后至少一个真实容器泄漏RED；原有6种fmt正测不能替代它。
- [ ] 02 中 `Secret` 唯一持有 `private func() []byte`，不保留raw slice/string/指向raw holder的struct字段。`NewSecret`复制调用者bytes进入闭包；零值为空；保留String/GoString/MarshalJSON/MarshalText/LogValue，增加Formatter，但opaque底层才是私有嵌套的根修。`Clear()`擦除本Secret拥有的buffer并置当前值为空；值复制共享该拥有buffer，调用方不得与读取并发Clear。不引入通用全局secret库/锁或公开Reveal。
- [ ] 私有 `secretBytes(Secret) []byte` 仅包内借读、`cloneSecret(Secret) Secret` 复制owned值；borrow不返回外部、不日志、不写入长期字段。merge输出与decoded current、HTTP mutation、调用者NewSecret值互不别名；Service不销毁caller拥有输入。拥有的decode/merged临时值在成功/错误/取消分支均defer Clear；replacement输入随后由HTTP或其真实调用者清理。字符串编码副本/GC无法物理全清零，报告披露。
- [ ] 04–08只迁移访问/必要owned清理；Claude/Codex envelope内容、slot选择、readiness、错误分类/提交语义不变；08不得改原proof-before-business、MAC/Ed资格、finalTx或agent许可。`TestConfigurationSecretOwnership`验证输入copy、clone独立、clear真实借读为空、同输入并行只读不被callee清理；旧Claude/Codex CRUD与actual native secret-once/proof-order全通过。
- [ ] 跑 `CGO_ENABLED=0 go test -mod=readonly ./internal/server/configuration ./internal/server/httpapi -count=1` 及focused privacy count20。overlay退回raw field但保留Formatter→compile0/原私有容器oracle RED，恢复GREEN；只Formatter补丁不能过关。Task0完整freeze后才由Gemini复用Secret。

## 3. Task1 — neutral Gemini contract 与真实 server-only planner

**Files:** 09–16。**Produces:** `(*Registry).PlanGeminiProvider(context.Context, agentconfig.GeminiProviderPlanRequest) (agentconfig.GeminiProviderDeclarationV1, error)`；无RPC/DB/文件/秘密。

- [ ] 09冻结string枚举 `GeminiAuthMode`（native-login/api-key）、`GeminiKeyTransport`（native-default/bearer）、`GeminiSecretSlot`（gemini.api-key）、`GeminiLocalAuthPolicy`（preserve-local-login-v1）；常量 `GeminiProviderPlanV1="gemini.provider-plan.v1"`、`GeminiPlannerPluginVersion="1.1.0"`、`MaxGeminiDesiredBytes=8<<10`、`MaxGeminiRequestBytes=16<<10`、`MaxGeminiDeclarationBytes=16<<10`。
- [ ] `GeminiProviderDesired`字段为 `AuthMode GeminiAuthMode`、`Endpoint *string`、`KeyTransport *GeminiKeyTransport`、`EnvModel *string`、`SettingsModel *string`、`KeyPresent bool`；JSON camelCase，optional用omitzero；bool不省略。request沿原APIVersion/PluginID/PluginVersion/DefinitionDigest/Target/Capabilities+Desired，无asset/owner/path/raw/env/extras。
- [ ] declaration字段精确为 `Version int`（JSON `version`，不是现Codex输出的apiVersion）、`Target`、`AuthMode`、四个原样optional、`SelectedType string`、`CredentialReference *GeminiSecretSlot`、`RequiredSlots []GeminiSecretSlot`、`LocalAuthPolicy`。native-login selectedType=oauth-personal、keyPresent=false、reference nil、slots为非nil空列表；api-key selectedType=gemini-api-key、keyPresent=true、reference固定单槽且slots精确单元素。没有resolved local auth/current/安装状态/remove/set map。
- [ ] `TestGeminiProviderPlan`先编译接缝stub再真实行为RED；native-login与API-default/bearer、endpoint absent/present、models全部四种presence与不同值、max合法值/编码总界。坏组合/URLdecoded控制/UTF-8/auth/transport/缺key/大小写变形拒绝；回值深clone。最大合法输入检查总界，不编造不可达的“逐字段合法但>8KiB”样例。
- [ ] 10纯named export `planGeminiProvider`，只接validated desired；11独立validate+deepclone并在运行前重算expected，真实`plugin.RunJSON`后校验完整相等/额外成员/类型/null/requiredSlots顺序/presence、累计bytes，返回受控值。保既有3s/16MiB/输出解码上界、fresh ctx/零结果；实际取消join。未注入network/fs/log/dynamic-code能力，异常只固定类别。
- [ ] 13–16精确升级Gemini builtin，definition digest由原manifest+module算法计算，旧1.0空cap仍可显式注册为metadata但不得plan；wrong version/digest/target/cap重复/超集拒绝。原Claude/Codex module/digest与generic Render行为不变，RenderJSON仍不获得Gemini输出权。当前registry_test的“Gemini永远无cap”改为新身份断言，同时另保旧metadata fixture，不能删断言了事。
- [ ] `TestGeminiProviderHostRejectsMaliciousModules`实际moejs运行 schema合法但单值错、删/增optional、伪selectedType/ref/slots/policy、unknown/path/header/raw map、异常/超限/无限循环等；Go独立比较必须拒绝。编译后overlay仅停比较，**同一probe**真RED；再恢复GREEN。另测并发独立runtime与ctx在运行前/中/返回前取消，无遗弃worker。
- [ ] `go test`运行agentconfig/agentadapter/plugin，Gemini focused count20且确认匹配非零，vet0。类型checkpoint不是运行签收；本Task不把keyPresent当认证/owner证明。

## 4. Task2 — owner资产/domain/envelope与安全desired快照

**Files:** 01/04/06、17–21，Task0接口已冻。**Consumes:**09 neutral types和既有configuration.Store原子metadata+payload/CAS。**Produces:** 原Service七方法支持Gemini；新增仅server内用 `(*Service).GeminiProviderDesired(ctx context.Context, owner, id string, expectedRevision int64) (agentconfig.GeminiProviderDesired, error)`，不注册HTTP/MCP/native入口。

- [ ] `GeminiProvider`为AuthMode/Endpoint/KeyTransport/EnvModel/SettingsModel，与neutral同标量/pointer类型但**没有KeyPresent或secret字段**；Draft、EditView加`GeminiProvider *GeminiProvider`，唯一新slot `GeminiAPIKeySlot="gemini.api-key"`。原Claude值分支保留、Codex pointer保留：非nilCodex/Gemini互斥且要求Claude完全zero；两pointer都nil才按原Claude校验。零draft/混合分支ErrInvalid；不能换旧asset的agent/schema/kind。
- [ ] TDD `TestGeminiProviderAssetsLifecycle`真TempDir/keys/SQLite，两个owner、create/edit/update/copy/delete/restart，provider完整原样、slot恰一项。native-login complete但明确无本机验证；missing API key存needs-credential，desired/planner拒绝；Copy无key、新ID/rev1，不改source，native-login副本无server key需求。
- [ ] 17 normalize/clone/validate按spec界；mode switch检查合并后key absent，已有key只omitted/KEEP必须失败；显式CLEAR成功。APItransport显式required/native-login absent；pointer开关只改资产override，不发native remove。所有mutations含错误action/重复slot、CREATE KEEP、REPLACE特殊字符/min/max/+1、非法组合真红绿。
- [ ] 18 envelope JSON精确 `{version:1,variant:"gemini-provider",provider:<camelCase typed>,secrets:{api_key?:<raw internally>}}`，secrets对象必有且已知字段不得null。jsontext先逐层扫描重复/深度16/已知shape/null/总bytes，再decode已知projection+语义校验。unknown合法成员只设extras；其值/键名不返回，原encrypted payload不改。未知schema/variant/auth→unsupported、坏已知类型/value→invalid；不能从空值补默认。17 typed Update/Copy/desired有extras→unsupported。
- [ ] loadEditable在06允许合法Gemini provider/schema1；原`Service.load`/`metadataSupported`仍Claude-only。Service.List/Get/Delete继续仅metadata与owner/CAS；混合Claude/Codex/Gemini/future-agent/future-schema不整体失败，wire未知kind不被放行。旧validation测试已用future-agent，不应再无依据改旧fixture。
- [ ] `TestGeminiProviderCAS`用真实多Store、snapshot屏障：contended、KEEP-first、REPLACE-first（再覆盖CLEAR对KEEP/REPLACE）；一个赢家，contended loser仅Conflict或bounded Unavailable、零view/零额外写；有序stale loser必须Conflict。记录reads/writes/revision/真实key等值布尔，不强改SQLite BUSY分类、不retry/rebase。Read/Copy同版本、stale Copy先判revision，source不变；250跨agent总quota，byte/+1/AAD-tamper/restart验证不新增表。
- [ ] `GeminiProviderDesired`一次ReadConfigurationAssetVersion+同revision校验、known/noextras/readiness后从**实际secret presence**构neutral值（defer清临时key），无caller自报presence。调用者退出Store读取事务后才跑Registry；声明可因后续资产修改而过时，不是授权/安装证明。`TestGeminiProviderDesiredAndRegistryParity`比较全声明与资产，missing/extras/stale均不进入JS；不能将moejs放写Tx。
- [ ] 新Gemini路径在业务校验后、写调用前与返回view/desired前做fresh ctx gate；若真实COMMIT成功后gate看到取消，零view/ctx sentinel，保留已提交事实，不假删、不重发。用owned Store wrapper在真提交后同步cancel/注入unknown证明；check→driverCommit/return不是原子操作，不能声称取消与返回完全无TOCTOU，日志/报告保留此限度。
- [ ] 21复用`newNativeOrderFixture`真实父session/Ed签名/PreflightPrepare正控，沿用现合法Claude purpose/agent/adapter，只把assetID/revision指向Gemini并重新签名（与已有Codex边界probe同形，不给client/proto增加Gemini支持）。Preflight正控通过后原native.Prepare必须unsupported、preparation/grant计数0；旧Claude actual两slot一次Consume仍通过，Codex拒绝不退。CRUD/desired/planner前后synthetic native目录/current/selection/Pack未变，provider网络trap调用数0；不能拿metadata-present当ready。
- [ ] 使用实现签名stub→编译成功行为RED→GREEN；固定`TestGemini*` count20+configuration全包/HTTP旧包；退回mixed classifier、copy携key、忽略extras、泛化native load、KEEP重读新revision各compile0/真RED/恢复GREEN。含secret的内部raw envelope/ConfigurationPayload仅作敏感序列化载体，绝不日志。

## 5. Task3 — additive wire / normal Buf / strict HTTP

**Files:**22–30/G1/G2；与T0共享的01等不再并写。**Consumes:**Task2真实domain、Task0 Secret.Clear。**Produces:** 同七RPC两种现有普通协议支持Gemini typed资产；不新增任何RPC/auth/Origin配置。

### 5.1 精确 additive 编号与生成

本base Draft原Claude=2/Codex=3。仅新增如下编号（进入源码前重核无冲突，冲突就checkpoint重新分配，不能按moving schema猜）：

| 对象 | 新成员/编号 |
|---|---|
| `GeminiAuthMode` | `GEMINI_AUTH_MODE_UNSPECIFIED=0`、`GEMINI_AUTH_MODE_NATIVE_LOGIN=1`、`GEMINI_AUTH_MODE_API_KEY=2` |
| `GeminiKeyTransport` | `GEMINI_KEY_TRANSPORT_UNSPECIFIED=0`、`GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT=1`、`GEMINI_KEY_TRANSPORT_BEARER=2` |
| `GeminiProvider` | `GeminiAuthMode auth_mode=1`；`optional string endpoint=2`；`optional GeminiKeyTransport key_transport=3`；`optional string env_model=4`；`optional string settings_model=5` |
| `Draft.payload` | `GeminiProvider gemini_provider=4`；保留name1/claude2/codex3、所有旧tags/enums |

无新增SecretSlot native枚举、native message/TS入口、DraftView字段或canApply bool。G1/G2由正常两模板生成；service descriptor新增类型不会要求改变Connect handler文件内容；若生成发现其它diff，先解释/授权，不手改/删diff。所有已生成产物全parity、breaking against固定base；Web native排除不弱化。

- [ ] `TestGeminiConfigurationProtocol`锁定字段编号、optional presence、JSON camelCase名；28嵌套unknown/duplicate/null/错型/enum/oneof多分支/binary unknown/重复optional、未知tag/深度/oversize均真实HTTP拒绝。固定base `3f687a9`完整FileDescriptorSet用于旧客户端：无Draft4，旧读者把4留unknown不变Claude；旧descriptor server拒Gemini新请求，旧Claude/Codex vectors照旧。不得以删新mapper代运行历史协议fixture。
- [ ] 26入口 `configurationGeminiInput(*configurationv1.Draft, []*configurationv1.SecretMutation) (configuration.Draft, []configuration.SecretMutation, error)` 与 `configurationGeminiView(configuration.EditView) (*configurationv1.DraftView,error)`；只接受Gemini槽，clone各optional，完整核metadata/oneof/mode/transport/slot数量和readiness；bad service result→Unavailable/固定错误，绝不拼成Claude。24在现input/view显式分派Gemini，混合domain view拒绝。
- [ ] 25复用当前`configurationCodexJSONValid(data, descriptor, strictProvider, depth)`扫描（可改参数名不搬helper），从`Draft.gemini_provider`进入strict context，与Codex一样拒其顶层和nested null；现Claude legacy null语义、合法packed repeated-enum继续通过。`configuration_codec.go`和普通128KiB/Echo guard无需修改；若严格性反例需额外文件先申请。
- [ ] 23只在Create/Update mapping成功后defer遍历owned mutations的Replacement.Clear；24部分转换失败也清已分配secret，输入临时bytes使用后清掉。不改generated String/日志，不声称raw wire string或框架buffer已经物理清零。domain返回非别名view，cleanup不改变已存秘密；复验其它agent。
- [ ] `TestGeminiConfigurationOwnerLifecycleAndCAS`真实auth sessions/alice+bob/匿名、proto/JSON七RPC；模式迁移/Copy/restart、owner存在性、混合metadata/delete，错误码依现InvalidArgument/NotFound/Aborted/ResourceExhausted/FailedPrecondition/Unavailable/context。stale Copy不读取错误版本secret。
- [ ] `TestGeminiConfigurationLimitsCancelAndUnknown`复用有界Store wrapper/真实SQLite，验证128KiB含压缩膨胀/1MiB、8slot即时quota/20s操作ctx/父cancel、真实worker drain。区分precommit取消无写、真实Store返回成功后cancel扣留view但实际row保留、**真COMMIT后注入error**的unknown；各调用/writes/commits计数不重发，不伪造rollback。也不把普通HTTP预算描述为native传输强界。
- [ ] 29不parallel修改global log/slog；cleanup恢复原logger/output。真实proto/JSON create/update/KEEP/CLEAR成功、非法mutation/strict-codec/oversize/异常/取消/unknown的标准log、slog两handler/HTTP错误输出无replacement/session/私有model/endpoint/异常sentinel。窄AST/静态扫描配置production路径无request/body logging，raw generated DTO String危险正控保留；放回一次真实body日志的/tmp overlay必须触发同日志oracle RED。
- [ ] 30按§7完成fixture机械正负控；taskHTTP全测试、原Claude/Codex协议/logging/limits、auth/server组合与vet。所有新增HTTP fixture是本slice测试，不更改production App/guard/auth注册或native transport。

## 6. Task4 — 专属 Web、existing state与成熟组件

**Files:**31–35。**Consumes:**已冻G2、原`ConfigurationDraft=MessageInitShape<typeof DraftSchema>`/`SecretChanges`和`useConfigurationRequest`，这些别名不需改`configuration-form.tsx`。**Produces:** `ConfigurationGeminiForm`以及第三种closed ConfigurationView；无raw编辑器/native apply按钮。

- [ ] 31定义`geminiSlot`、`supportsGemini(metadata)`、`requireGeminiView(view)`、`geminiFields(provider?,name?)`、`validateGemini(fields)`、`geminiDraft(fields)`，返回typed而非任意map；`GeminiView`mode为editable/readonly。guard核metadata+payload+单槽唯一presence、native-login无key/transport/endpoint与readiness；unsupported/malformed不变成空编辑。payload非秘密在modal内存，不进metadata query cache。
- [ ] 表单本地fields：name、authMode、endpoint enabled/value、transport明确枚举或未选、envModel/settingsModel各enabled/value；UTF-8按TextEncoder bytes先拒非法surrogate，绝不truthy归零。endpoint使用URL仅校验不以URL.toString重写。models不同显示未验证优先级警告。两逻辑preview按owned字段呈现slot ref/状态而非原secret或可安装dotenv；只读unknown view不展示未知键名/值。
- [ ] 32使用现Input/Select/Checkbox/Button/Dialog；mode change先保存pending目标，在Dialog列明需移除的endpoint/transport与key action，取消维持原草稿，明确确认才切换并设显式CLEAR；native-login→API transport由用户显式选择，不能猜key。创建action默认CLEAR、编辑KEEP；REPLACE才password；CLEAR已有值另确认。mode变更确认后不能残留旧replacement，frame state不携原文。
- [ ] 33/34扩展`ConfigurationView`union与target select，edit/copy分派不能只看agent；Copy先read同source revision确认editable，回新ID/rev1/无key与正确readiness。extras→form/save/copy禁用，Delete仍owner/CAS显式确认。范围与成功文案只说资产，native-login complete也不“已登录”；metadata-only项不显示ready。
- [ ] 复用现`run`的active-ref即时拒绝重入、Abort/generation/unknown blocked和explicit reload；候补不排不可取消mutex/队列。元数据身份、sourceRevision、保存responseagent/id/revision/typed内容严格核对。Gemini接现Codex的冲突后“明确放弃/读取”入口；不改Claude/Codex原语义或重写shared hook来隐藏晚回填。
- [ ] secret置DOM/ref，所有submit finally（含校验拒绝）、mode确认放弃/CLEAR、cancel、locale、owner/origin/asset切换/logout/unmount清replacement/DTO引用；输入仍活跃时不得snapshot/截图/console记录。非秘密草稿遇locale保留、主题切换保留；未知结果不重发，先重新读。字符串/GC副本不能保证清零。busy本地submit ref防同一tick重复，disabled不当唯一门。
- [ ] `Dialog`取消/保留first+autofocus，初始Enter没有clear/delete/discard副作用，主动Tab/Enter成功；Tab/ShiftTab/Escape、可见focus、回真实opener，locale控件在modal内部可达。35只加中文key→English，简中资源沿原派生；不另造国际化/动画/focus trap。
- [ ] 正常`npm ci/typecheck/build`只证明编译；纯转换/hook可用既有临时编译验证，但必须继续Task5真实browser，不向无测试runner的Web加新框架或把“no tests to run”算测试。

## 7. Task5 — real browser、负控、零副作用与组合freeze

**Files:**不增shipping源路径；消费30固定fixture，临时控制/断言脚本只在自己的0700 `/tmp`证据根。**Produces:** 每scenario原始命令/exit、安全receipt、source绑定与完整限制，非完整Gemini/native/F12签收。

### 7.1 现成 harness 的真实来源与控制协议

既有可复用的是 **test-only Go fixture机制，不是tracked测试框架或生产API**：`/tmp/cognate-codex-browser-harness/codex_assets_browser_fixture_test.go`，当前读取SHA256 **`c487b346d23f6936d869191c4096166e0b9741405f99611597e723a0d907f719`**，原mechanism报告 `codex-assets-browser-harness-report.md`；这个后继有45m test lifetime，不冒充旧报告中543f前缀的原12m helper字节。机制包含`configuration.Store`嵌入wrapper记录真实成功commit、`httptest.NewRecorder`持有成功响应、hold/drop/stop/caller-abort。实施前重新核所消费helper精确SHA；不要复制它的旧产品PASS或假定临时文件永久可用。

30将这个有限机制移成Gemini命名的可重建test-only fixture：`TestGeminiAssetsBrowserFixture`仅显式`COGNATE_GEMINI_BROWSER_DIR`启用，正常包运行未启用可skip**此手动harness入口**，不skip任何产品测试。另`TestGeminiBrowserHarnessGuardsAndDrain`始终运行，证明未授权control401、正常stop、held时stop、caller-abort实际释放/join、真COMMIT后drop。不得把harness skip/启动成功当浏览器通过。

- 独立public/control两个随机loopback监听；使用真实auth/Store/configuration.Service/NewWithOptions+web.Handler，外部provider roundtripper trap；all synthetic owner/password/key/HOME/profile。control token随机，仅0600 ready文件/内存；不输出token、SQL、raw DTO、请求/响应正文、真实文件路径或明文哈希。没有production hook、公共路由、真实HOME/default8080。
- control最小命令：arm next postcommit hold/drop，release held；读取计数/布尔receipt；对指定合成asset推进真实revision或写入受控unknown fixture；stop。receipt含requests/writes/commits/held、assetID/revision/agent/auth/slotPresent/expectedProviderEqual/expectedSecretEqual/sideEffectsZero等安全布尔；不返回raw key/extras/任意SQL或任意path。fixture控制只用常量fixture与Store公开wrapper。
- hold/drop发生在实际Store mutation成功/commit witness之后；drop用部分响应+connection close，不能在commit前返回假500。held请求同时监听request ctx/fixture ctx/release；ctx关闭则取消实际handler、释放barrier并join后才Close Store/server。fixture45m只约束人工采证进程，**不改产品3s/20s**；超时/kill不是正常cleanup PASS。
- before/after synthetic native files、current/selection/Pack基线零变化；provider trap计数0，浏览器请求只允许自己的产品origin。断言不请求Gemini端点/OAuth/ADC/本机探测；CSP与默认public/MCP边界不变。

### 7.2 必需真实用户路径 / 同步屏障

- [ ] agent-browser真实键鼠：native-login create、API缺key create→REPLACE→KEEP、两transport切换、每个model presence及不等值、endpoint optional、save/reopen、Copy无key→补key、CLEAR/API→native-login确认/取消、删除确认；Store布尔与屏幕/revision/commits一致。原初始Enter安全与主动Tab批准都验，不能只点handler。
- [ ] unknown stored extras通过受限control注入，ReadDraft只有已知projection/只读，save/copy无请求/commits；owner明确Delete成功。mixed metadata另owner看不到；stale save用真实external CAS推进，不sleep，保留草稿/blocked，明确reload不重放。
- [ ] 真COMMIT→drop：UI unknown、writes=commits=1次增量、禁止自动重发，重新List/Get/Read读到新revision。真COMMIT→hold时分别执行locale（modal内真实控件）、close→cancel或discard、origin真实导航/重新登录bob、asset重新打开；abort/response release由屏障receipt控制，晚response不能填旧成功/错误。bob为空；取消不伪装rollback。不同scenario独立fresh fixture而非跨fixture拼计数。
- [ ] logout/owner切换需真实document/认证边界，origin变更用127.0.0.1→localhost新origin或第二受控origin，不把组件prop注入说成网络输入。无原地origin切换契约不伪造。private query cache/DOM检查只在受控范围使用无输出布尔；会话/token只在必要内存通道。
- [ ] language中英、light/dark与375×812/1440×1000，实际截图肉眼检查布局/焦点；截图前退出REPLACE/清值。console/errors/URL/storage/cookies/CacheStorage/IndexedDB/query cache受控检查无synthetic凭据；不能导出含登录正文的HAR/网络raw capture。未检查的浏览器内部/GC内存明确不作已擦除承诺。
- [ ] 正常完成执行control stop、浏览器close，并poll原fixture handle到终态；每个失败也cleanup，保留错误分类/原日志。新smoke不使用原native窗口/真实DISPLAY；本片只Web，不称MyGo验收。

### 7.3 编译后独立负控（最少八类，保持原probe）

| 控制 | 单一overlay变异与必须RED的既有oracle |
|---|---|
| N1 opaque | raw字段返回、Formatter仍在；T0私有容器输出检测RED |
| N2 host | 放过schema合法但selectedType/model单值与desired不同的module；T1真实moejs全结果oracle RED |
| N3 branch | 允许Gemini+Claude/Codex混合、或把native load扩大；T2 mixed/native正控授权后拒绝oracle RED |
| N4 CAS | KEEP重新读取当前revision或跳过expected-source；T2有序schedule/Copy同快照oracle RED，不靠busy/timeouts |
| N5 secrets/extras | Copy带key、或忽略stored extras并重写；分别T2原文presence/原envelope无改动oracle RED |
| N6 codec | 接受Gemini null/未知enum/重复oneof；T3真实proto+JSON原错误码oracle RED，不能因此拒合法旧Codex packed |
| N7 lifecycle | 去掉generation或unknown后自动retry一次；在同样postcommit hold/drop真实browser下旧回填/多写计数oracle RED |
| N8 privacy | 故意记录真实replacement/body或保留unmount secret ref；T3捕获日志/T5DOM与存储oracle RED；sentinel不输出到摘要 |

每类先正常compile/build0，运行定向原断言exit非零，再恢复exact source运行GREEN；保存overlay hash/编译log/断言log/实际exit。编译失败、全局timeout、无匹配、削弱assert、用旧Go二进制未重buildWeb不算。browser变异必须重建dist/binary并绑定其source/hash，不能只跑node静态检查。

## 8. 实施命令、完整门与freeze合同（现在均NOT RUN）

执行者先在新/tmp由固定base归档构隔离root（不branch、不碰shared index），设置synthetic HOME/XDG/私有tmp，保留真实工具cache明确路径；Go实际必须1.27.3、CGO0、默认SumDB/module校验不改。运行完整Modern Go list并读适用指导，随后TDD。以下是未来门禁，不是本轮运行结果；focused正测必须解析`go test -json`确认TestGemini等非零。

```sh
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go version
CGO_ENABLED=0 go test -mod=readonly ./internal/server/configuration -run 'TestConfigurationSecret' -count=20 -json
CGO_ENABLED=0 go test -mod=readonly ./internal/server/agentadapter -run '^TestGemini' -count=20 -json
CGO_ENABLED=0 go test -mod=readonly ./internal/server/configuration ./internal/server/httpapi -run '^TestGemini' -count=1 -json
CGO_ENABLED=0 go test -mod=readonly ./internal/server/configuration ./internal/server/httpapi -run '^TestGemini(ProviderAssets|ProviderCAS|ProviderDesired|ConfigurationOwnerLifecycleAndCAS|ConfigurationProtocol|ConfigurationLogging)' -count=20 -json
CGO_ENABLED=0 go test -mod=readonly ./internal/agentconfig ./internal/server/plugin ./internal/server/agentadapter ./internal/server/configuration ./internal/server/httpapi -count=1
npm --prefix web ci --no-audit --no-fund
buf lint
buf format --diff --exit-code
buf breaking --against '.git#ref=3f687a9211ab6d3e71af9f49f04c61fc52d27fc7,subdir=proto'
buf generate --template buf.gen.yaml
buf generate --template buf.gen.web.yaml
npm --prefix web run typecheck
npm --prefix web run build
CGO_ENABLED=0 go test -mod=readonly ./... -count=1 -json
CGO_ENABLED=0 go vet -mod=readonly ./...
go mod verify
```

Buf breaking在有该固定ref的只读对象仓库中执行，若clean archive无`.git`则先从base导出proto到独立目录，`buf breaking proto --against "$BASE_PROTO"`；实际命令/输出入证据。生成必须在独立output目录与候选所有generated文件逐字parity，G1/G2以外无diff；保持Web排除native及既有全部parity。fresh web/dist由正常build得到，不计tracked源。

浏览器fixture单独terminal启动（同synthetic环境）：`COGNATE_GEMINI_BROWSER_DIR="$GEMINI_EVIDENCE" CGO_ENABLED=0 go test -mod=readonly ./internal/server/httpapi -run '^TestGeminiAssetsBrowserFixture$' -count=1 -timeout=50m`；目录0700、ready0600，不cat ready。加载`agent-browser skills get core`后使用具名session：`agent-browser --session "$GEMINI_BROWSER_SESSION" open "$GEMINI_APP_URL"`，实际ref键鼠和私有control脚本随证据保存；close同session并join原Go进程。无现成npm test script，不虚造一次unit命令替代browser门。

完整组合再验：普通Claude/Codex typed/parity、原native真实secret-once/proof-before-business/20s传输与EOF门不退（必要实际20s一轮，快项重复20）；client CLI/MyGo依赖图无server/moejs。六目标Linux/Darwin/Windows×amd64/arm64，各 `CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build -mod=readonly -o "$OUT/<target>/<name>" ./cmd/cognate`、`./cmd/cognate-server`、`./cmd/cognate-desktop`，共18build；build不代表这些OS runtime或Gemini本机应用通过。CGO0不宣称race。若base有真实RED，保留并准确归属，不私改无权文件、加timeout/retry或以serial绿覆盖。

冻结交接：37精确路径与base匹配、manifest每项mode/blob/SHA/bytes与生成/手写分类；source tar、tree与可复建方法、所有命令/terminal exit/正负控和安全日志清单、≤1页作者report；说明raw DTO/internal payload仍敏感、内存清零限制、helper真实停机。无索引混入/commit/push。非作者完整review通过和main组合/CI实际证据齐备后才算本资产片交付。

## 9. 覆盖/自审与仍然OPEN

| 已签spec | 本计划负责门 |
|---|---|
| §§1–2目标/事实/十四agent边界 | Global与§1，固定base/上游，无重跑/真实HOME |
| §§3–4 typed/bounds/mutation/CAS | T0/T1/T2/T3；G01–G07，N1/N3/N4/N5/N6/N8 |
| §5 ownership/OAuth/native后继 | T2 native正控后拒绝、T5零副作用；本片不写两artifact |
| §6真实moejs与完整host验证 | T1/T2parity；G09–G10，N2 |
| §7未知/兼容/普通HTTP | T2/T3/G05–G08；metadata与typed分离 |
| §8真实Web用户控制/隐私 | T4/T5/G11–G13；真实focus/locale/late response，N7/N8 |
| §§9–11结构/命令/签收 | Exact37、完整门与分片freeze；G14继续OPEN |

执行顺序、类型/字段名/编号、shared owner与测试oracle已逐项自审；各Task以可运行正负控收尾，不交只有schema/types/假列表的完成。需要独立设计审裁定这个exact37/08纯迁移/30test-only fixture与具体接缝后，主才能按片另授源码。

**本片即使全部GREEN仍不签：**Vertex/ADC/服务账号/GOOGLE_API_KEY/headers/更多auth；env全局与raw编辑；Gemini CLI真实兼容/有效模型；native codec/pin/双artifact preflight→intent→Authorize→Consume/Status→journal/publish/reconcile/Restore与平台runtime；MyGo对等、import/Pack/autoapply、MCP/prompt/routing/failover/发现。它们是完整F12的剩余阻塞，不是从需求删掉的“可选项”。无secret声明、asset metadata或CredentialComplete均不能替代canApply，也不声称完整M7/F12/Release。
