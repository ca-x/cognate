# Codex Local Codec / B Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development or executing-plans to implement this plan task-by-task **only after main grants the exact source/dependency scope**. Steps use checkbox (`- [ ]`) tracking. Do not stage/commit/push or add paths without a separate instruction.

**Goal:** 在客户端纯内存中实现完整provider TOML候选、auth JSON只读保持、owned catalog JSON候选；所有检查先于新secret解析，任何失败无部分bundle，不以保存或Consume冒称Applied。

**Architecture:** 新独立`internal/codexconfig`消费现有A声明，成熟TOML语义decoder + 私有raw-span适配器、严格JSON raw-span、私有间接payload共同组成Check→Render。NativeProfile只从编译内已签表选择；未闭合时生产入口固定拒ready，合成profile只在`_test.go`覆盖同一纯转换核心，不成为产品兼容证明。

**Tech Stack:** Go **1.27.3**、**CGO_ENABLED=0**；标准`context`/`encoding/json/jsontext`/SHA-256及另批后精确锁定的`github.com/pelletier/go-toml/v2 v2.4.3`。客户端不得依赖server/moejs或带IO职责的configapply engine。

**Spec:** `docs/superpowers/specs/2026-10-29-codex-local-codec.md`，SHA256 **`000b17f750a0e258e81d1a96dbf34100f761ff227a6be1133b90c22bf5fbfcb7`**；独立Spec/Quality ACCEPT/0 findings，review SHA256 `7a7cfc270e67b0b173d9519d8cfe0f53e98a292eb453258482fd64e5e0e6520d`。spec状态行未回写不抹已签身份。本计划候选待独立设计审，不授Go/module/native访问。

## Global Constraints

- 唯一声明复用`agentconfig.CodexProviderDeclarationV1`；A commit `c7d8ab60c2f7ae6d2c4b18b1163b72fe65d59dc8`，中性类型blob `6004a34d6c5923725354cdd0e606614cf405b341`、SHA256 `7a4bdd46d339500ab834276b2fe7742c2b4ddf9ad80bafd66cfb000c75379e78`。B独立校完整A合同，不import server validator，不新造desired/catalog DTO。
- 本片必须同时交付**config + auth保持 + catalog**；MCP、AGENTS.md prompt、账号/OAuth管理、代理和全14-agent/F12后续范围不被删除。普通bytes转换不是本机支持或完整A+B+C1+C2+D。
- 无文件/网络/命令/keyring/数据库/JS/plugin/环境凭据发现，产品代码不读取本地路径。只有测试可读本包合成testdata；NativeProfile consumer探证属另授工作，不把exec或IO加进codec。
- 固定逻辑角色：`codex.config`、`cognate-model-catalog.v1`、内部观察`codex.auth-baseline.v1`。D映射受控根的config.toml / cognate-model-catalog.json / auth.json；ref不是任意path。auth永无write/delete action。
- Preserve-local-login-v1按已签scope/store/witness解析，不接任意bool。未读/缺失/已存在三态分开；未知观察/归属/版本/模板/有效范围拒绝，不能把读取错误转missing或先Consume后补判定。
- caller输入与返回copy不alias；plan/material/images/resolution/candidate均私有间接payload且默认fmt/JSON/Text/slog固定脱敏。所有错误固定sentinel，不附parser原文、位置、URL、模型、OAuth、secret、hash或长度；ctx错误保原context sentinel。
- 固定八哨兵：`ErrInvalidDeclaration`、`ErrInvalidLocalImage`、`ErrInvalidSecret`、`ErrOwnershipConflict`、`ErrUnresolvedLocalAuth`、`ErrUnsupportedNativeProfile`、`ErrUnsupportedRepresentation`、`ErrCodecLimit`。不新造可携带parser正文的错误类型，不wrap私密原错。
- 每image输入/输出≤256KiB，总基线/保留bundle≤768KiB，实际config+catalog写入≤512KiB；auth keep仍计retained。声明≤64KiB、catalog≤32KiB、models≤32；A desired交叉核对时≤48KiB。
- JSON depth≤32、每object≤512、每array≤4096、每image≤32768 tokens；TOML≤4096 expressions/32768 AST nodes、key≤32段/value nesting≤32；整bundle≤512个不重叠edit；静态profile模板≤64KiB。unknown也计费，溢出拒绝，不裁剪。
- 不把parse后计数当第三方parser内部RAM/CPU硬界；不以可遗弃goroutine包同步parser来假装可取消。入口/阶段/遍历/返回前fresh ctx gate。只读并发Render独立clone；Destroy由调用者外部同步，不承诺与Render并发安全或Go堆安全擦除。

## Review Focus

1. 同名cognate表/sidecar或旧模型不等于owned；不能删除foreign模型来凑声明顺序（Task3/4/5）。
2. model_messages canonical指令、use_responses_lite和OAuth先行刷新能让“写出了JSON/flag”与真实效果不同；必须卡NativeProfile而非丢字段（Task0/3/4/5）。
3. fully-dotted/quoted表示与root前置注释可能合法却被插到最后section；必须有真实支持正控而非全unsupported（Task2）。
4. present空TOML、present空JSON、not-observed auth及catalog absent+foreign pointer不能混作missing（Task1/3/5）。
5. Render后catalog失败、取消或材料多一槽不能泄露半成品config/旧OAuth，private容器可绕过普通Stringer（Task1/4/5）。

## 0. 固定输入与三个不可替代的硬门

观察main `cfbcbd4e53a7d0022a8d9d6526eb37a1c112bffd`仅作本计划来源，不是未来代码授权base；main合入其它已签片后另给准确执行tree，先逐blob核A类型/既有Claude无漂移，不用moving index。当前无`internal/codexconfig`，go.mod无本TOML依赖。

| 门 | 所需精确证据/批准 | 当前结论与失败处理 |
|---|---|---|
| Dependency admission | main另批仅go.mod/go.sum最小pin；版本v2.4.3，Sum=`h1:GTRvJQutkOSftxIFD5xw9aepkYNuPWmVJpffdDPYVpY=`、GoModSum=`h1:2gIqNv+qfxSVS7cM2xJQKtLSTLUE9V8t9Stt+h56mCY=`，正常SumDB/代理/只读验证，禁止vendor/禁SumDB/顺带升级 | 31项库研究不等于module准入。本计划不下载/改依赖；未批前不得实施依赖产品代码。批准后main自己单独交依赖delta，B作者只消费已签依赖base。 |
| NativeProfile | 真实binary/version/来源摘要、dialect全文件输入输出、native catalog完整schema/必填defaults及精确模板bytes/hash、relative ref解析、全部A枚举/字段、credential优先级两支实际consumer正控，独立签交 | 当前**无已签profile**；任何生产ready候选及完整B ACCEPT阻断。不能用新旧文档/库接受/cc-switch模板补值。 |
| D observation + C2 parity | D可信effective scope/store/ownership/model归属/witness及发布前复核；C2完整intent/canonical/pin/proof/配额，独立签交 | B仅校输入形状、一致性、绑定，不做OS认证。真实链未接前不能声称本地授权/发布已闭合；unknown零plan，绝不先Consume。 |

**新官方补证必须影响门禁，而不能暗改000b17 spec：** `/tmp/cognate-native-primary-evidence/REPORT.md` SHA256 `bd075eebd45414897ee54f79ba3a8d615ba99f5b2522dcd2609893833af08a9f`；其MANIFEST SHA256 `55cbea2aefb01c57efa228571c97b6b9c6b4baad2bf786e63d256f749171ab0b`。官方0.164.0源码pin `6aaac694803004a63c6f393d153c96d22521a6b9`；binary SHA256 `b93f6811d3f653f0fb83be4050d857fb9abbb111dfa2ce160563327daff7f306`。数学签名核对不等于完整Sigstore信任链或profile批准。旧88观察报告SHA256 `d71f1ffa15a7b4aab06cde4b9342f5c9a5f684ba01ec5ff89db912ba03f0f94b`保持原失败/timeout，不重复为计划造新native证据。

- CLI普通auth-store源码默认File，MCP OAuth的Auto是另一enum；managed/CLI覆盖仍需D有效观察。native Auto某些读路径会error fallback，**B仍按已签更保守合同：Unknown/error不fallback**。
- provider显式bearer在最终auth resolver优先于requires flag，但具体provider路径可先等待ambient OAuth read/refresh。仅静态先后不足以关闭true/false两支实际request及OAuth不外发门。
- native canonical `model_messages.instructions_template`存在（包括空字符串）时优先于legacy `base_instructions`；其它siblings要保留。只写legacy、删除整个model_messages或忽略config/history指令覆盖均不能冒充typed语义实现。000b17禁止擅造新指令载体，本计划不授权更改映射。
- native0.164.0 `ModelInfo`无`supports_parallel_tool_calls`；`use_responses_lite`会影响parallel但不是等价alias。**不能把false/true/absence吞掉、改别的flag、把catalog排除或只放行config/auth以关闭B。** 当前0.164.0不能据这些材料注册ready profile。main可另行选择经完整实证的目标，或让spec owner提出并独立签映射后继；不假设一定有现成兼容版本。

### Task 0: 冻结实施前提与授权边界（无产品写入）

**Files:** 本计划与ignored作者说明；只读上述spec/A/review/R&D。未来module两路径及native探证不是本计划当前授权。

- [ ] 重算spec/A/研究输入身份；记录唯一待批准源码写集、main的dependency准入与执行base，保存各门OPEN/CLOSED及独立收据，不把本计划ACCEPT当source grant。
- [ ] 若仅授合成实现：下列Tasks1–5仍实现三artifact完整纯核心，`profile_test.go`内synthetic-only profile运行正控，生产profile表不含它；生产Check对无已签profile返回`ErrUnsupportedNativeProfile`。不加环境开关、caller模板/可变注册表或测试profile导出。
- [ ] 合成实现可以独立冻结为**qualified synthetic codec evidence**；不得签“完整B/ready/native compatible”。Task5生产ready及真实consumer门必须等NativeProfile独立签交，真实D/C2链另等其接口签交；不以这种分阶段方式删除auth/catalog或把所有核心正控改unsupported。

## 1. 后续唯一源码申请：exact14，全新增

以下是待另授的独立实现边界，不是本轮写权限；module两路径不算在内且只main另批另交。每任务只能写其列出路径，跨任务共用文件串行，不能并行改provider/private/profile。

| 路径 | 单一职责 |
|---|---|
| `internal/codexconfig/provider.go` | 固定public两阶段API、presence/角色、受限观察构造、A独立校验、sentinel/budgets及整bundle编排 |
| `internal/codexconfig/private.go` | 间接payload、深clone、受信copy/binding accessors、Destroy和全部默认输出脱敏 |
| `internal/codexconfig/profile.go` | 只读已签profile表/固定schema模板、dialect及auth/ownership解析；绝无IO/发现/动态加载 |
| `internal/codexconfig/toml.go` | 成熟decoder+私有unstable span adapter、root/table/comment锚点、有限splice及复解码 |
| `internal/codexconfig/json.go` | auth/catalog完整JSON验证、decoded身份、raw spans及有限局部编辑，不复制Claude业务 |
| `internal/codexconfig/catalog.go` | 全8字段native映射、presence/模型归属/顺序及profile必填合成 |
| `internal/codexconfig/provider_test.go` | A parity、两阶段/全bundle/绑定/生命周期与取消正负控 |
| `internal/codexconfig/private_test.go` | wrapper/容器fmt/slog/JSON/copy/Destroy/secret材料及limits |
| `internal/codexconfig/profile_test.go` | test-only synthetic profile及全auth/scope/ownership/dialect门；绝不进生产表 |
| `internal/codexconfig/toml_test.go` | 所有已承诺表示/布局/成熟validator/节点与edit边界 |
| `internal/codexconfig/json_test.go` | 所有层级重复/数值/转义/raw保持/预算，auth三态 |
| `internal/codexconfig/catalog_test.go` | 完整native字段/模型顺序/保留/删除/归属/模板与actionable拒绝 |
| `internal/codexconfig/testdata/a-v1-golden.json` | 来源固定c7的独立A声明/错误/presence/有序字段golden；不是B自己输出再自期望 |
| `internal/codexconfig/testdata/bundle-v1-golden.json` | 全合成三image/raw token/完整预期候选、safe preview及绑定变化fixture；不含用户配置/账号文本 |

不修改A共享类型、configapply/Claude/C0/C1/C2/D、server/proto/gen/UI/CI/其它tracked docs或依赖文件来凑通过；不新增公共parser库、测试产品failpoint或产品test-profile开关。需要额外路径先列具体失败申请。

## 2. 固定API与后续接缝

Task1在provider.go定义下列名称，私有载体统一由private.go持有；Task5提供Check/Render完整实现。wrapper值传递只分享只读private payload，构造时deep clone；副本/Destroy所有权按Task1测试，不导出可修改字段。

```go
NewImage(p Presence, body []byte) (LocalImage, error)
NewImages(config, auth, catalog LocalImage) (LocalImages, error)
NewOwnership(provider, catalog Ownership, managedModels []string) (OwnershipFacts, error)
NewAuthFacts(store AuthStore, keyring KeyringState, fileLogin LoginState) (AuthFacts, error)
NewResolution(profileID string, scope EffectiveScope, ownership OwnershipFacts, auth AuthFacts, witness [32]byte) (LocalResolution, error)
NewSelectedSecret(slot agentconfig.CodexSecretSlot, body []byte) (SelectedSecret, error)
CheckProvider(ctx context.Context, images LocalImages, declaration agentconfig.CodexProviderDeclarationV1, local LocalResolution) (ProviderPlan, error)
RenderProvider(ctx context.Context, plan ProviderPlan, selected []SelectedSecret) (ProviderCandidate, error)
```

- `Presence`为Missing/Present/NotObserved，零值Invalid。config不得NotObserved；非Present必须nil/零bytes；Present空TOML可用、空JSON非法。config/auth/catalog角色由NewImages位置固定，无path→bytes map。NotObserved只给本次不需读的auth/catalog。
- `Ownership`为Absent/Managed/Foreign/Unknown；managedModels为journal已证明owned模型ID列表，clone并绑定其原序，不能从A声明或同名slug推导。重复ID/归属与images矛盾拒绝；缺归属模型不补到新数组末尾。
- `AuthStore`为File/Keyring/Auto/Ephemeral/Unknown；`KeyringState`为FoundCredentialLogin/Missing/Unknown/NotObserved；`LoginState`为CredentialLogin/NoCredentialLogin/Unknown/NotObserved。这些是观察类别，不带OAuth/API key/JWT/account内容，不能传requires任意bool。
- `EffectiveScope`为CompleteEffective/Unknown（零值Unknown）。witness是D对完整scope/覆盖来源/ownership/auth观察的SHA-256摘要占位载体，零摘要不能代表有效观察；**这只固定B内32B绑定，不定义D证据原始格式、认证、C2 canonical或信任来源**。任意caller能造摘要不等于可信；真实入口仍待已签D适配器。
- profileID必须匹配编译内已签profile身份，不能是语义版本范围/最新标签。NewResolution做shape/clone，Check才做profile/全语义一致性；synthetic profile不能借constructor逃进生产allowlist。
- `ProviderPlan.Preview()`只给固定role/owned field ID和insert/set/keep/reorder/managed-model-remove等enum与布尔，secret固定占位。`ProviderPlan.Binding()`返回opaque `ProviderBinding`；受信copy accessors给逐role presence+原始SHA256、完整A声明clone、profile身份/模板digest、policy原值+解析结果、ownership及观察witness。默认输出不泄露hash/正文；不暴露新协议canonical字节。
- `ProviderCandidate.CopyImage(role)`返回独立LocalImage副本；image受信`CopyBytes()`才给clone，`Action(role)`仅Keep/Write，Auth只能Keep；NotObserved保持无bytes。plan/candidate/local images/selected material均可`Destroy()`，释放各自owned缓冲；返回candidate不得依赖plan或caller材料的存活。
- 每个constructor失败返回零wrapper；`CheckProvider`失败零plan，`RenderProvider`失败零candidate。caller显式copy获得的bytes仍敏感，不得流入DTO/log/普通preview；不能声称B摘要具有MAC/授权/反重放效力。
- 所有任务先增加命名测试并运行selector，记录实现前的真实RED原因，再做最小实现、运行fresh GREEN。编译期未定义API可作首次TDD RED，但**不算**后文要求的编译成功行为负控。共享backend文件只在任务边界串交；不把阶段性未完成API或全unsupported当任务业务正控。

### C2 / D 串交表（本B不实现这些域）

| 步骤 | B交出/消费 | 外部owner必须另行提供 |
|---|---|---|
| Begin/Bind→Prepare(A) | 输入唯一A声明与完整fixed slot/policy/catalog，不能换成server任意edit-map | C2保持既有Claude向量，增Codex声明/pin/strict codec/配额，不复用旧tag改变含义 |
| D-preflight→Check | 固定三role images、ownership+managed模型范围、effective scope/auth facts/witness；先clone/check/index，无新secret | D受控根/权限/link/真实effective overrides/登录观察、journal归属；未能证实就Unknown/拒绝，不读取多余auth/foreign sidecar |
| Check→完整intent→Authorize | opaque Binding中的所有基线presence/hash、全声明含presence/order、profile/template、policy及解析值、ownership/witness | C2独立冻结canonical transcript/golden+server/client pin parity，另绑定asset revision/adapter digest、选择/审批/operation；B不自造wire/MAC或忽略这些外层字段 |
| Consume→Render | 精确一次`codex.provider-key`材料；同plan，不再传base/path/endpoint/目录 | C2一次消费/丢包只Status、秘密不经浏览器；缺/额外slot不允许补从旧auth/env读取 |
| Render→D复核→publish | 两write候选+Auth Keep、safe preview和原binding，不回Applied | D在实际写前核所有基线/ownership/观察；变更即Conflict/新显式预览，不能重新解bool、auto-rebase或再Consume；多artifact journal/reconcile/restore非OS原子多写 |

## Task 1: 中性捕获、A独立合同和opaque载体

**Files:** provider.go、private.go、provider_test.go、private_test.go、testdata/a-v1-golden.json。**Consumes:** 固定A类型/constants及§2。**Produces:** §2 constructors/types、私有declaration校验/clone，后续任务共用的fixed roles/budgets；暂不声称Check/Render已完成。

- [ ] 先写`TestCodexADeclarationParity`、`TestCodexInputPresenceAndClones`、`TestCodexPrivateCarriers`、`TestCodexSelectedSecret`；独立A owner/reviewer对c7来源golden及SHA复核，B包不import server或执行moejs生成自己的期望。
- [ ] 校A APIVersion1/target/providerID=cognate/wireAPI=responses、唯一credentialReference与RequiredSlots=[codex.provider-key]、唯一policy；HTTPS/无userinfo/query/fragment/backslash/控制字符、port1..65535、endpoint≤2048B；model ASCII1..128B且首字符alnum、其余仅alnum或`._:/-`；不trim/alias。
- [ ] 覆盖displayName1..256B、context1..2147483647 exact uint64、instructions1..2048 UTF8且仅CR/LF/TAB控制例外；modalities是1..2个text/image、levels是1..7个none/minimal/low/medium/high/xhigh/max，均有序不重复；default须属于显式levels，主model须在catalog，顶层effort须与该model显式levels兼容。所有optional false/present/absent及累计bytes独立复核。
- [ ] 实现capture后篡改caller bytes、A pointers/slices、返回copy不影响内部的测试；所有wrapper值/指针/typed nil/外层private字段/多层容器fmt verbs+flags+width/precision、JSON/Text/slog只固定标记，不能靠Stringer掩盖直接slice载体。
- [ ] 材料合法1/8192B、空/8193/invalidUTF8/NUL/CR/LF拒绝；新材料只存私有clone，slot必须精确，Render精确集合解析留Task5。构造/Destroy不改变caller缓冲，Destroy与并发Render外部同步合同明确。
- [ ] 运行`go test -mod=readonly ./internal/codexconfig -run '^TestCodex(ADeclaration|Input|Private|SelectedSecret)' -count=1 -v`，记录真实run/pass及编译后缺字段/presence/私有载体泄漏负控，不删旧Claude隐私回归。

## Task 2: 成熟TOML验证、真实span和布局转换

**Files:** toml.go、toml_test.go；复用provider.go的roles/budgets，不改Claude。**Consumes:** Task1私有clone/声明，已批准精确库pin。**Produces:** 私有`indexTOML`与有限splice后复解码结果，编排仅能传固定owned字段，public无通用edit-map。

- [ ] 先写`TestCodexTOMLRepresentations`、`TestCodexTOMLLayoutAnchors`、`TestCodexTOMLSemanticValidation`；显式表/quoted+escaped/header、root fully-dotted必须实际转换成功，解析后语义正确且未触字节逐字相等；owned inline/closed parent/array-table/scalar准确unsupported，未知未选合法表示可保持。
- [ ] 完整decoder先验证duplicate/冲突table/invalid key/value；私有unstable接缝在NextExpression前复制semantic key段、完整assignment/header/comment spans，不保留Node/Parser/alias。用KeyValue.Raw证明完整赋值，不把Array.Raw空/InlineTable.Raw开括号当整值，不用正则、LastIndex或decoded文本长度重造语法。
- [ ] 按spec九字段顺序只改叶value；已有表在本section边界补缺，root项放首header及其连续standalone注释前；新provider仅在parent可扩且absent/managed时追加全限定表。不盲重开implicit/dotted/inline表，不在EOF的最后section追加假root。
- [ ] CRLF/原无末尾LF/首EOL选择/尾assignment注释/EOF注释run、quoted单段含dot、未知secret与大整数、混合嵌套结构逐byte goldens；原语法dialect不受profile支持时拒ready，库接受TOML1.1不等于native接受。
- [ ] 检查4096/4097 expressions、32768/32769 nodes、32/33层及key段、512/513 edit、不重叠与输入/输出bytes；未知也计费。实测锁定库最坏合法/非法输入的耗时/分配并记录，不以post-parse计数冒称pre-parse内存硬界；不满足时报告收窄/重审而非自造parser。
- [ ] 运行`go test -mod=readonly ./internal/codexconfig -run '^TestCodexTOML' -count=1 -v`；raw-only validator、EOF-root、decoded-key split、整文件Marshal各先编译0再由原断言有限RED，恢复fresh GREEN。

## Task 3: 严格JSON、auth只读与可信观察解析

**Files:** json.go/json_test.go、profile.go/profile_test.go、testdata/bundle-v1-golden.json。**Consumes:** Task1 capture、Task2语义索引；只读固定NativeProfile证据。**Produces:** 私有JSON span树、auth验证/Keep、resolved policy、profile/dialect/ownership检查；生产已签profile表与test-only profile严格分离。

- [ ] 先写`TestCodexJSONRawAndLimits`、`TestCodexAuthPreserved`、`TestCodexLocalAuthPolicy`、`TestCodexNativeProfileGate`、`TestCodexOwnershipConflict`；auth对象含合成OAuth/API-key形状/metadata/未知大整数、转义与非默认顺序，逐byte和presence不变。
- [ ] 用jsontext验证所有层级decoded重复key、尾随/非UTF8/非对象/空JSON、depth/members/array/tokens；记录raw成员/value offset，只对新owned literal使用encoder。escaped身份不可混同，未知subtree/大整数不能经float64或整map重编码。auth不做JWT/expiry/login推断，unused auth保持NotObserved，不为测试“完整JSON”读取它。
- [ ] 固定policy表：File按可信CredentialLogin/NoCredentialLogin true/false；Keyring仅Found/Missing；Auto Found优先，只有明确Missing再按已观察file；Unknown/错误/未知scope/store及Ephemeral零plan。explicit store与profile/effective观察不一致拒绝，默认只读已签profile，不从KeyPresent/新key/文件存在/非空JSON推断登录。
- [ ] 本次选中受管范围的归属Absent与images对应；Managed需D witness及model范围；选中范围Foreign/Unknown、credential auth/env_key/headers/command冲突、未知actionable字段拒绝。catalog absent+foreign pointer不属本次覆写范围，保持且不追读，但Binding标记该观察范围，不给D可应用保证。
- [ ] profile_test.go内构造synthetic-only profile，调用同一**私有**解析核心完成true/false等正控；不能通过公开Check注入schema/模板/任意bool。生产unknown profile必`ErrUnsupportedNativeProfile`；0.164.0 canonical instructions/parallel无等价映射作为不可批准fixture，不删除字段或借lite alias“修绿”。
- [ ] 运行`go test -mod=readonly ./internal/codexconfig -run '^TestCodex(JSON|Auth|LocalAuth|NativeProfile|Ownership)' -count=1 -v`；Unknown→Missing/false、Auto错误fallback、file存在→login、坏auth→missing、仅根重复检查各编译0→真实断言RED→fresh GREEN。

## Task 4: 完整catalog映射、presence、模型归属和原文保持

**Files:** catalog.go/catalog_test.go；补充bundle-v1-golden.json。**Consumes:** Task1 A声明、Task3 profile/schema/JSON raw索引与owned模型范围。**Produces:** 私有catalog计划/候选与managed模型删除preview；不独立返回半bundle到public。

- [ ] 先写`TestCodexCatalogAllFields`、`TestCodexCatalogPresenceAndOwnership`、`TestCodexCatalogRawPreservation`、`TestCodexCatalogRequiredNativeFields`。1与32模型均真实正控，顺序与大小写原样；额外foreign模型/重复slug/未知actionable字段拒绝，不拼接/删掉以过关。
- [ ] root models→有序entries，model→slug；displayName→display_name；contextWindow精确同步context_window/max_context_window；supportsParallelToolCalls→supports_parallel_tool_calls显式false也写false；modalities→input_modalities有序；instructions→base_instructions保CR/LF/TAB；levels→supported_reasoning_levels的effort原序（description只来自已签固定映射）；default→default_reasoning_level且属于levels。此表是000b17合同，不默改为native0.164新载体。
- [ ] 每optional absent保owned现值和raw bytes，新entry仅profile允许省略才省略；必填无A值/保留基线/已签默认即unsupported，全失败无catalog成品。新levels与保留default/顶层effort不兼容冲突，不猜最高档、context、image或neutral指令。
- [ ] 更新按decoded slug定位、局部值splice；模型数组owned顺序可重排完整raw entry及必要分隔，未知非actionable成员/子树/大整数/转义/成员次序保持。只允许删除journal证明managed的未声明旧model，并在safe preview列managed-model-remove；不重编码整个文件。
- [ ] catalog absent时pointer/auth/catalog保持，foreign sidecar可NotObserved；catalog存在只许固定basename/ref且归属通过。root必填模板/static defaults逐字段列来源、覆写边界、指令/工具语义及≤64KiB原始hash，不从账号cache/网络/CLI模型发现取模板。
- [ ] 运行`go test -mod=readonly ./internal/codexconfig -run '^TestCodexCatalog' -count=1 -v`；camelCase直接输出、false丢为absent、排序levels/modalities、trim instructions、猜必填/删canonical或foreign模型均要独立golden RED；profile未签不能据synthetic native-shaped JSON声称实际consumer可用。

## Task 5: Check→Render完整bundle、绑定、取消与差分验收

**Files:** provider.go/private.go、provider_test.go/private_test.go，补充两golden；其余backend路径发现根因须串行窄改并列交接，不扩14集。**Consumes:** Tasks1–4同一私有核心。**Produces:** §2完整两阶段API、safe preview、opaque Binding、config/catalog Write + auth Keep；D/C2消费接缝固定但不接其产品代码。

- [ ] 写`TestCodexProviderBundle`、`TestCodexProviderBindingChanges`、`TestCodexRenderSecretExactness`、`TestCodexProviderZeroOnFailure`、`TestCodexProviderCancelAndConcurrentCopies`。合成profile下同时成功config+catalog+auth逐byte Keep；catalog malformed/超限/取消使整个candidate零值，不能留下可用config copy。
- [ ] Check先clone并完成所有不需新secret的schema/profile/policy/ownership/dialect/引用/跨度及最终native必填验证；不解析材料、不提供config-only候选。Render仅同plan+精确1槽，拒zero/nil/重复/额外/wrong slot，精确一次取私有材料并只编码一个TOML bearer叶；新key不能进auth/catalog/preview/binding/log，不能从旧key/env fallback。
- [ ] 修改每一基线的presence/原始bytes、A字段/presence/false/order、profile/template、ownership/model范围、policy解析值、scope/witness分别导致绑定变化或拒绝；测试独立expected变化，不仅比较B自己算出的同值。Binding不冻结C2 transcript，也不能漏asset revision/adapter等由C2外层负责的绑定要求。
- [ ] caller修改base/A/slices/material、返回copy再修改/Destroy另一plan或candidate不污染已捕获对象；同plan并行Render独立，不共享parser/map/缓冲。阶段取消含临界返回前fresh ctx，失败零结果；同步parser不使用遗弃worker。
- [ ] 隐私/限额覆盖所有wrappers、nested private容器及原始parser error canary；总基线768KiB、写入512KiB、每image256KiB、声明/catalog、JSON/TOML/edit与overflow双侧边界；无secret/旧OAuth/raw片段/hash长度泄漏。
- [ ] 至少六强制负控raw-only、EOF-root、全文件重编码、Unknown-auth fallback、漏catalog仍返config、额外secret槽放行；另加auth写入/删除OAuth、漏profile/观察/order绑定、吞取消/输出超限。每个保存patch/hash/selector/compile exit0/预期有限断言RED/恢复fresh GREEN；panic、全局timeout、零匹配、编译失败不算行为RED。
- [ ] 若NativeProfile仍OPEN：只交完整三artifact synthetic证据与生产unknown拒绝，标qualified/not-ready；**不能删除未通过的catalog/auth正控、接受任意模板、把所有内部转换case unsupported或宣布完整B ACCEPT**。若profile另签且获source更新授权，在profile.go登记其精确entry后，用相同完整正负控并另取真实consumer收据，才申请完整B验收。

## 3. 可执行验证与冻结交付（未来实施阶段，不是本计划已执行）

统一Go1.27.3/CGO0/GOTOOLCHAIN=local/合成HOME，无真实用户配置；产品依赖准入后才执行：

```sh
go test -mod=readonly ./internal/codexconfig -count=1 -v
go test -mod=readonly ./internal/codexconfig -shuffle=on -count=20 -json
go vet -mod=readonly ./internal/codexconfig
go list -deps -test ./internal/codexconfig
go test -mod=readonly ./internal/configapply ./internal/configclient ./internal/nativeconfig ./internal/agentconfig -count=1
go mod verify
```

- 审核真实required顶层/关键subtest run/pass、零FAIL/SKIP/零匹配；go list不得依赖server、moejs、configapply/nativeconfig/CLI/UI，不能加CGO依赖。旧包回归与module验证不代替B独立业务oracle。fuzz补在toml/json测试的私有parser入口，不能只fuzz无profile的public Check而全unsupported。
- 用synthetic最坏合法/非法bytes测解析时间/分配、取消延迟并保原命令/退出；bounds声明与实际parser内约束分开。CGO0不以race/CGO1结果替代；未运行任何项就记未执行。
- NativeProfile consumer正控需另授隔离runner：精确目标解析完整三角色、全部枚举/必填/ref、OAuth真假flag实际请求只含provider bearer、第三方无OAuth/API fallback、外网禁止。保schema/template/命令/退出/超时及来源manifest，不能把旧88失败洗绿或把source静态事实当新runtime。
- source grant后仅exact14新增包路径（加本计划进度若另授），main的module delta与NativeProfile证明另有来源；各交付固定base、逐path A/M/mode/blob/SHA、tree/patch/确定性archive、依赖版本/sums、template/profile/golden SHA、命令退出及限制，由非作者Spec/Quality审。依赖/观察/C2仍OPEN时不得把synthetic签收扩大为完整B或设备应用。

## 4. Spec覆盖与停止条件

| Spec | 落地任务 |
|---|---|
| §1权威/依赖/profile/观察门 | Task0；Tasks3/5生产profile拒绝与synthetic隔离 |
| §2两阶段/角色/presence/clone/preview | Tasks1/5、§2 API与C2/D交接 |
| §3九owned字段/ownership/冲突 | Tasks1/2/3/5 |
| §4整个auth policy表、auth保持与顺序 | Tasks3/5、C2/D交接，不替D取得观察 |
| §5成熟TOML/dialect/span/锚点/无损 | Task2，Task5端到端未触bytes |
| §6全部JSON/auth/catalog八字段/必填/未知/顺序 | Tasks3/4/5，不缩成config-only |
| §7材料/opaque/固定error | Tasks1/5；每backend归一错误 |
| §8全部bytes/structural/edit/cancel/并发 | Tasks1–5分别入口计费、Task5全bundle复核 |
| §9真实正控/compiled负控/原生门 | 各Task测试及§3；未运行不声称通过 |
| §10exact路径/权限/后续 | exact14与module分权、C2/D串交、无MCP/prompt/14agent范围削减 |

当前只有计划与作者说明的文档授权。等待独立设计审和main后续明确source/dependency授权；事实门缺失立即报告，不以观察超时重启其它工作、不更改spec/plan之外源码来回避。NativeProfile无解时保持阻断并列出具体不兼容字段，不用“支持部分配置”取代完整provider三artifact目标。
