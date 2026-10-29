# CX-LOCAL-CODEC/B：Codex provider 本地无损 codec

日期：2026-10-29。状态：**单纵切规格候选，待独立设计审；无实施/依赖授权，未实现本片**。B 是 Codex capability map 的 CX-LOCAL-CODEC，不是 server backup 的 B2/B3。以下“必须”是验收合同，不是现有产品能力。

## 1. 权威、目标与诚实前置门

| 固定输入 | 身份与用途 |
| --- | --- |
| Codex capability map | `2026-10-29-codex-capability-map.md`，SHA256 `d6691bd0da4a3e9907ad4f51370d406826af2025af03359c623c74ca018992bd`；本片为 provider TOML + auth JSON 保持 + catalog JSON，不取代 MCP/prompt 后续纵切 |
| A 已签规格/已交源码 | `2026-10-29-codex-provider-planner.md` SHA256 `a210da792d9487200fa3616e6d7abb58ef7ec81f139af17db646595f9e1d981e`；commit `c7d8ab60c2f7ae6d2c4b18b1163b72fe65d59dc8`，`internal/agentconfig/codex.go` blob `6004a34d6c5923725354cdd0e606614cf405b341` / SHA256 `7a4bdd46d339500ab834276b2fe7742c2b4ddf9ad80bafd66cfb000c75379e78` |
| 固定 cc-switch | `6d6b7162f122ac0ce1356a14688c8c4f712db3c8` 的 `live/project/codex.rs`、`services/provider/codex_direct.rs`、`codex_config.rs`；只作行为对照，不执行其账号/CLI发现/登录代码 |
| 官方 config reference 快照 | `/tmp/cognate-codex-config-reference.md` SHA256 `a69ba7c5ef08e316a22f5e43dd60019b990ad93080abeaa75eba0722a59e30cf`；来源 `https://developers.openai.com/codex/config-reference/`，不是某个 native binary 的兼容实证 |
| 已运行库能力 | `codex-toml-runtime-mirror-report.md` SHA256 `fdc549454ecdfae2c4d9b93470c3034d7ecc2dce19a8ec90b381d90d01064695`；Go1.27.3/CGO0、SumDB正常、31项含subtest通过及raw-only负控；不是完整B验收 |

目标：在客户端纯内存中，把**一个已选 provider**的 A typed 声明、已授权取得的本地基线（测试仅合成）、可信 preflight 观察转换为完整且独立的候选 bundle。无文件、网络、命令、JS/moejs、keyring、数据库、grant 消费或发布。codec 不做 Importer，不接 raw CRUD/任意路径/任意 edit-map，不创建新认证协议，不宣称 Applied/可恢复。

本规格冻结 provider 的转换、保全与拒绝规则；以下事实门尚未闭合，不能用猜值掩盖：

1. **依赖门**：推荐锁 `github.com/pelletier/go-toml/v2 v2.4.3`；Sum=`h1:GTRvJQutkOSftxIFD5xw9aepkYNuPWmVJpffdDPYVpY=`，GoModSum=`h1:2gIqNv+qfxSVS7cM2xJQKtLSTLUE9V8t9Stt+h56mCY=`。产品 module 变更仍须 main 另批；不得 vendor/禁SumDB绕过。unstable 接缝私有且升级必须重验。
2. **NativeProfile 门**：任何ready候选或native兼容声称之前，必须独立冻结目标 Codex binary/version/来源摘要、其实际 TOML dialect、catalog deserializer/schema及必填默认模板、relative catalog 路径解析规则、auth-store默认及credential优先级。不得用“0.149起”注释、最新在线文档、库接受语法或cc-switch模板替代实证。未匹配已签profile时返回固定 unsupported，不能提供ready候选；提前编写合成codec测试不等于该门通过。
3. **可信观察门**：D-preflight 的 effective config/ownership/auth-store观察与本地witness、C2完整intent/pin parity尚须各自签交。B只校纯输入形状和一致性，不证明OS/登录事实。缺少可信适配器时unknown拒绝；不能先Consume后补判定。

前置事实未落实，可推进独立合成codec测试，但不得给本纵切完整ACCEPT/安装支持标志。完整首个provider仍需A+B+C1+C2+D；MCP/prompt/账号等后续能力与全14-agent/F12矩阵不缩减。

## 2. 输入、两阶段 API 与逻辑 artifact

候选纯接口为 `CheckProvider(ctx, images, declaration, localResolution) (ProviderPlan, error)` → `RenderProvider(ctx, plan, selectedSecrets) (ProviderCandidate, error)`。名称由实施计划落实，不在本轮创建Go类型或wire。唯一声明类型复用 `agentconfig.CodexProviderDeclarationV1`，其 catalog/model 继续用现有 typed 类型；不再造一套公开 desired/catalog schema，也不import server/moejs validator。B独立校验A完整合同，并以固定A golden证明parity。

- `images` 是固定artifact角色及明确 `missing/present/not-observed` 状态，不是外部path→bytes map。present与missing不同；present空TOML可合法，present空JSON非法，不把读取错误伪装missing。not-observed只用于本次不应读的auth/catalog，不能代替读取失败。
- `localResolution` 含已签NativeProfile标识、effective-config范围、归属结论、auth观察及绑定witness。不得注入任意模板/schema/原生path/任意bool策略。profile只从实现内已签列表选择；localResolution本身不是认证/attestation，调用方必须是后续已签D可信适配器。
- Check先clone输入，核边界/声明/presence/支持集/冲突，完成全部无需新秘密的验证和编辑索引，才返回opaque plan。plan绑定所有已观察artifact的存在性与原始hash、完整声明及可选字段presence/order、policy原值/解析结果/witness、profile身份与归属。不能用分别Get/Read或字符串拼接遗漏基线。
- Render只接受该有效plan及精确单槽材料，不能再传替代base/endpoint/catalog/path。它不是再次preflight、不是CAS重试或auto-rebase；任何失败全bundle零值，不能config成功而catalog失败仍返回部分候选。
- Preview默认只返回固定owned字段ID、insert/set/keep/reorder等enum及布尔，secret为固定占位标识；不得返回原文件、unified diff、旧secret、未知文本或含秘密hash/长度。批准后的真实candidate只在opaque受控copy接口供D使用，不是普通UI/RPC DTO。

| 逻辑角色 | 固定本机落点（D职责） | B的权限 |
| --- | --- | --- |
| 现有target `codex.config` | 受控Codex root内 `config.toml` | provider owned叶节点的无损候选 |
| A已有ref `cognate-model-catalog.v1` | 同root `cognate-model-catalog.json` | 仅声明catalog存在且归属通过时生成/更新；无任意外部目录 |
| 本片内部观察ref `codex.auth-baseline.v1` | 同root `auth.json` | **只读/保持**，不是新wire ID，不写provider key、不删除、不创建、不刷新OAuth |

ref不是路径。config中的 `model_catalog_json` 只可编码固定受控basename，且NativeProfile必须证明其解析到对应Codex root；若实际target要求另一种解析，先修订受控resolver合同，不能默用cwd或放开任意路径。读取/权限/链接/目录身份与journal归属由D验证；B处理调用者已取得的本地bytes，不follow任何pointer，不上传OAuth或本地未知配置给server/plugin。

## 3. 全部 owned 字段、presence 与冲突

provider-key声明必须满足A固定版本/target/providerID=`cognate`/wireAPI=`responses`/policy/唯一slot、endpoint/model/catalog全部逐项及累计bounds。nil/absence不等于空值/null，不容忍多余operation/未知枚举/偷偷降级。输出位置与稳定新增顺序固定：

| 顺序 / native位置 | 来源与规则 |
| --- | --- |
| 1 顶层 `model_provider` | 固定 `cognate`；改变当前项必须在D完整预览确认，B不自动选中资产 |
| 2 顶层 `model` | A.Model原样、大小写敏感，不trim/转alias |
| 3 顶层 `model_reasoning_effort` | 仅A存在时set；absence保留现值，不remove；现值与新声明/已签native支持集不兼容或无法判定则冲突，不偷偷继承 |
| 4 顶层 `model_catalog_json` | 仅A.Catalog存在时set受控逻辑ref落点；absence保留现pointer，不读取或覆写foreign sidecar |
| 5 `model_providers.cognate.name` | 固定 `Cognate` |
| 6 同表 `base_url` | A.Endpoint原样，沿用A完整HTTPS/native校验，不代理转换 |
| 7 同表 `wire_api` | 固定 `responses` |
| 8 同表 `requires_openai_auth` | 只能由§4受限policy解析，不能来自A.KeyPresent/新secret/外部任意bool |
| 9 同表 `experimental_bearer_token` | 仅Render精确解析一次 `codex.provider-key`，禁止literal key声明或从旧文件/环境取值 |

这不是通用patch API。已有字段仅改值token，新增按上表顺序；不重排已有未选字段。A不拥有 `auth.json`、review_model、plan_mode_reasoning_effort、agents/memories、MCP、approval/sandbox/permissions、context/compact/verbosity/web_search、其他provider表、legacy顶层base_url/key/routing，本片不顺手删除或迁移。当前路由与这些节点发生无法判定冲突时Check拒绝，由后续能力另行显式预览；“字节保留”不是忽略actionable冲突的许可。

`cognate`表/sidecar同名不等于owned：D只能交 absent 或可信journal/基线证明managed的结论；foreign、无法确认、scope不一致均拒绝覆盖。表内 `auth`/`env_key`/headers/command等其他凭据路径、未知actionable字段与本provider声明冲突必须在Render前拒绝，不把它们删掉再声称切换成功。已签profile明确忽略的非actionable未知字段可原字节保留；不能以未知即安全扩大allowlist。

catalog absent而foreign pointer存在时，B保持pointer及其未读状态，不追随/上传、不宣称目录与新model兼容。D若因实际effective config无法证明选中声明可用，必须在Authorize前拒绝；不能用B“保留成功”冒称可应用。B不从provider发现接口补目录。

## 4. preserve-local-login-v1：纯观察输入与可信解析边界

固定cc-switch的 `requires_openai_auth(RouteAuth::Bearer, login)` 对自己的bearer通道采用login布尔；`login_after`按file/keyring/auto选择本机登录，auto的keyring Unknown不会回退file。官方reference仅说明auth-store和provider flag的配置语义，不是B探测登录的权限，也没有提供跨版本“文件存在即登录”的证明。

B的provider始终是自己的bearer，但policy仍是**未解析声明**。D-preflight在没有Consume材料时先取得下列受限观察，B按表校验/解析；typed观察字段不携带或提取OIDC/OAuth value、账户ID、JWT解码结果，也不接受任意auth更改计划。必要的本地auth原始image可能含OAuth，只作为opaque本地基线验证/绑定/保留，不上传或流入普通输出：

| effective store / 可信观察 | B接受的policy结果 |
| --- | --- |
| file / 已判定credential-login，且绑定已观察auth JSON基线 | `requires_openai_auth=true` |
| file / 已判定无credential-login，含明确missing auth | false；已有文件的metadata/空token/仅API-key形状不能由B自行当作登录；具体判定须来自已签native适配器 |
| keyring / 明确Found credential-login 或明确Missing | 分别true/false；不需要/不读取无关auth文件，not-observed可合法；keyring I/O失败不是Missing |
| auto / keyring明确Found | true；不以file覆盖已找到的keyring状态 |
| auto / keyring明确Missing，再得到与file相同的明确观察 | 按file结果；必须有“Missing而非Unknown”的witness |
| keyring/auto Unknown、权限/解密/adapter错误、未知store | 固定unsupported/unresolved，零plan，不fallback file/false |
| ephemeral | 本片没有可信运行中Codex进程状态来源，unsupported；不能把它猜成登出或转file。未来支持另签，不改本policy名字冒充已支持 |

effective store若在本次受控TOML显式出现，必须是profile支持的string并与观察一致；缺省行为由**已签native profile**固定，不能把cc-switch helper的默认file当目标CLI事实。profile/环境/CLI/system policy覆盖及profile文件可改变effective状态；D必须给可验证的有效范围与witness，未知/多个来源歧义拒绝，不让B只看一个root TOML就自称看到了真实生效配置。

Auth JSON若为观察所必需：B只做严格对象语法/预算与baseline绑定，完整保留字节、成员顺序、转义、大整数及未知子树。语法坏/重复/类型歧义不是missing。**B不以auth.json存在、`OPENAI_API_KEY`存在、A.KeyPresent或新key非空推导登录/flag**；不验证token有效期、不解JWT、不探keyring、不刷新登录。D观察必须明确由受支持native版本认可的credential-login状态，而不是泛化为任何非空JSON。unused auth不得为了B“完整处理JSON”而额外读取。

NativeProfile的credential优先级门必须用隔离HOME、合成file/keyring观察及真实目标consumer证明：true/false两支都使用新provider bearer访问第三方，不把本机OAuth/API-key fallback发送给该endpoint；不能仅凭cc-switch函数或文档默认false得出安全结论。未证明的profile禁止ready。

顺序保持 **Begin/Bind→Prepare声明→D-preflight/Check→完整intent→Authorize→Consume→Render→D复核并publish**。C2/pin需绑定原policy、解析结果、profile、ownership、观察witness、所有相关基线及asset revision/adapter digest；D发布前重新验证观察与基线，变更即冲突/重新显式预览，不重新解析成另一个bool、不重发Consume。B内部hash不是MAC、DB授权、重放防护或物理attestation，proof也不能证明native诚实执行观察。具体witness/canonical wire仍是C2/D前置门，不在本片另造认证框架。

## 5. TOML无损后端、支持表示与上下文

采用成熟库完整decoder作语法/语义验证，再用锁定的unstable parser索引raw spans，编辑后再次完整decoder验证；不以raw scanner替代重复key/table检查，不map→Marshal整个目标文件。仅新owned scalar可用库/已验证scalar encoder生成TOML token；未知部分永不重编码。库原始error/Highlight/Data不得返回或记录。

已实跑确认：quoted/escaped dotted key按decoded segment而非split(".")标识；字符串Raw含完整引号/多行token；Array.Raw为空、InlineTable.Raw只有开`{`，**KeyValue.Raw覆盖完整赋值**。每个expression的节点/span/key数据必须在NextExpression前复制；input由plan自有clone，不能保留Node/Parser或别名。KeepComments用于布局，不是万能round-trip保证。

| 表示 / 操作 | 必须执行的策略 |
| --- | --- |
| 显式 `[model_providers.cognate]`、quoted/escaped等价header/key | 成熟语义索引确认同一路径，已有叶值精确splice；缺字段只在该section边界内插入，不能重复开同表 |
| root fully-dotted provider keys | 保持dotted表示，在root区新增同一完整semantic path，不能盲改成重复header；quoted单段含dot不能误匹配 |
| inline/closed-inline parent或provider、array-table、同名scalar | 不允许用外部header/key扩closed-inline；首版对这些owned位置明确unsupported representation。未选区域的合法同类结构仍原样保留；这不是不解析未知结构的豁免 |
| 顶层字段缺失 | 在root区、首个table header及其连续前置standalone注释之前插入；无table则在root末尾注释run前。不在EOF的最后section内假装插root |
| 新provider显式表 | 仅当成熟decoder证明parent可扩展且本次absent/owned时，在合法table上下文追加固定全限定header；不重开已有隐式/dotted/inline定义 |
| EOL/注释/尾部 | 已有字节不normalize；新增行沿用首个已出现EOL，无EOL用LF；原无末尾换行时仅为新增语法加必要分隔。尾注释仍跟原assignment；standalone注释run仍与原后继header/EOF相邻，不从中间插字段 |

实施前须为header/section/comment锚点提供成熟parser范围的真实fixture；没有完整可证span时固定unsupported，不用正则/逐行扫描/LastIndex括号/decoded字符串长度发明TOML语法。inline支持如后续扩展须另签span adapter门，不能通过重编码容器“支持”。不承诺所有合法TOML表示首版都可编辑，但必须交付上表显式/quoted/dotted真实支持，不得所有case都unsupported后自称B完成。

edit集合必须有界、不重叠、按原始offset应用；未选bytes保持原顺序和逐byte相等，不能借删除旧auth、迁移旧custom provider或去掉unknown字段使结果看起来合法。TOML1.1 multiline inline已被本库probe接受，**不等于目标native支持**；profile必须验证整个输入/输出dialect，未知区域含target不支持语法也不能获得ready。

## 6. Auth JSON 与 catalog JSON：沿用现有JSON策略

现有 `internal/configapply/claude_json.go` 已用 `encoding/json/jsontext` 的ReadToken/ReadValue/InputOffset记录raw member/value spans、局部splice，保留未触token，而非map/float64整文件重编码。本片沿用这一策略与严格默认，针对完整JSON验证UTF-8、所有层级重复decoded key、尾随、深度/计数及对象形状；只用jsontext编码新owned literal。不要复制Claude env/schema成为另一套Codex DTO，也不为复用parser而import带磁盘/journal职责的engine；若后续需抽私有共享span helper，须先申请精确写集，不改已冻Claude源码。

Auth JSON是只读opaque基线：required时严格校验但永不生成write/delete candidate。present对象逐字保持，missing仍missing，not-observed仍不读取；无第二份provider key，不造新的login。若未来需要官方账号/auth变更，必须独立能力/spec，不能在本provider-key片里加remove。

### 6.1 Catalog完整映射与presence

A.Catalog absent：不修改pointer、不写/删sidecar、不要求读取foreign目录。存在：只允许受控ref与absent或已证owned sidecar；foreign pointer/file/无法确认归属在秘密消费前冲突，A没有覆盖foreign的许可。

Native catalog root必须按已签profile的实际schema构建并由真实target consumer接受；不能直接把 `CodexCatalogDesired` 的camelCase JSON当native。基本映射固定如下，native字段变化需要profile/spec修订而非静默兼容：

| A typed字段 | Native成员 / 规则 |
| --- | --- |
| models顺序 / model | root `models`有序数组，entry `slug`原样；1..32、大小写敏感、无重复，严格保留声明顺序 |
| displayName | `display_name`，显式存在才override，不trim；description不凭空等同displayName |
| contextWindow | 精确整数 `context_window`及 `max_context_window`，存在时同步设置为A值，禁止float64/字符串转换；不改config顶层context字段 |
| supportsParallelToolCalls | `supports_parallel_tool_calls`；显式false必须写false，不丢为absent |
| inputModalities | `input_modalities`；保留显式有序集合，不按model名推断image/text或默认补image |
| baseInstructions | `base_instructions`原样含LF/CRLF/TAB；不得trim、用旧模型指令/官方账号文本替换，或擅造model_messages等新指令载体 |
| reasoningLevels | `supported_reasoning_levels`中的effort按原顺序逐个保留；native需要description时仅可用已签profile固定映射，不能借cc-switch canonical排序/丢unknown实现 |
| defaultReasoningLevel | `default_reasoning_level`；存在须属于声明levels。absence不猜最高档；保留owned现值时亦须与本次levels兼容，否则冲突 |

每个optional absent表示未声明override：已有owned entry保留该native成员原值及raw bytes；新entry若native允许省略则省略，若必填仅能使用**已签profile明确列出的默认值**。所有A字段/presence均纳plan绑定，不以false/empty/null折叠。已有侧文件中未声明的owned旧model可被本次完整models列表明确替换，但必须在预览中列为managed成员删除；无法证明归属的额外model/重复slug不得静默删除或拼到声明顺序末尾，整次冲突。

profile模板必须逐字段列出必填/optional、值/来源、可覆写字段与指令/工具能力含义，并绑定精确原始bytes/hash及目标consumer fixture。不允许运行时从任意本机catalog/cache、provider网络、已登录账号或CLI发现命令取模板。固定cc-switch模板和生成器有neutral base_instructions、context/default reasoning、tools/shell等额外默认/推断；**不是Cognate已签模板**。本规格不据其注释猜一个可用ModelInfo。所需字段无A值/可保留基线/已签默认时 `ErrUnsupportedNativeProfile`，不返回缺字段的catalog或忽略声明后报告成功。

更新owned JSON按decoded slug定位并沿raw成员span编辑；unknown成员/子树、大整数、转义、原成员次序保持。模型数组顺序是显式owned范围，可按声明重排完整raw entry片段及必要分隔，但不能map→Marshal整个文件或丢entry中的未触字段。unknown actionable工具/认证/指令成员必须按profile拒绝，不能以opaque preservation放行；profile明确忽略的非actionable未知成员才可保留。新建文件使用固定字段顺序的有界jsontext输出，没有旧文件normalize。全bundle再解析及typed/native parity校验后才能返回。

## 7. 单 secret、opaque 与错误合同

唯一secret slot为 `codex.provider-key`。Render的材料集合必须正好一个entry、ref完全匹配且只解析一次；nil/零wrapper/缺失/重复/多余slot或非provider credential均拒绝。沿用现有客户端material合同：**1..8192 UTF-8 bytes，无NUL/CR/LF，不trim**；不存在从旧TOML/auth/env补缺口或CLEAR降级官方登录。A.KeyPresent是planning readiness，不是材料或登录证明。

新secret只流入一个受管TOML bearer叶节点；不能进入catalog、auth.json、preview/diff/log或server插件。材料编码时正确转义引号/反斜杠/control，不对原文件Marshal。codec的“精确消费”只表示一次解析这份材料，不是C2 token抗重放/一次网络消费。调用方材料不被改写；临时clone尽力clear，输出candidate自身仍属秘密，不能许诺Go堆/库字符串安全擦除。

input/observations/plan/材料/candidate全部clone并以私有间接payload保存；可借鉴当前codec的 `*privatePayload` 内再间接持bytes模式，不在wrapper里直接放可被fmt遍历的slice。零值/typed nil安全；默认fmt各verb/flags/width/precision、值/指针/外层私有字段/多层容器、JSON/Text/log/slog均为固定脱敏标记，不输出正文、hash或长度。只用显式受信任copy/binding accessor，返回副本无别名；Destroy不得使已返回独立candidate或另一plan意外变化。只Stringer/Formatter不够，必须覆盖fmt绕过方法的私有容器路径。

受控哨兵类别：`ErrInvalidDeclaration`、`ErrInvalidLocalImage`、`ErrInvalidSecret`、`ErrOwnershipConflict`、`ErrUnresolvedLocalAuth`、`ErrUnsupportedNativeProfile`、`ErrUnsupportedRepresentation`、`ErrCodecLimit`；ctx取消/到期保原context sentinel。错误无库原文/%w私密parser错误、无路径/URL/owner/model/secret/hash/行片段或输入位置。全部失败给零plan/candidate、无部分artifact、无输入修改；拒绝不得偷偷输出“尽力转换”的候选。

## 8. 明确预算与纯生命周期

| 项 | 本片接受界 |
| --- | --- |
| 每个present本地artifact，输入及输出 | 各≤256KiB，与现有MaxConfigBytes一致；先限bytes再进入parser |
| 总基线 / bundle所有images | 各≤768KiB；实际write candidate仅config+catalog合计≤512KiB；auth只读keep仍计 retained bytes |
| A声明/目录/模型等 | 完整沿用A：声明≤64KiB、typed catalog≤32KiB、models≤32及各字符串/enum/数字界，不用native模板豁免累计量 |
| JSON | depth≤32、每object≤512成员、每array≤4096元素、每artifact≤32768 tokens；unknown也计费 |
| TOML | 每artifact≤4096 expressions、32768 AST节点、semantic key≤32段、value nesting≤32；未知表/array/inline均计费 |
| 编辑 | 整bundle≤512个不重叠叶/span edit；nativeProfile静态模板≤64KiB，不接受caller模板byte blob |

所有计数检查溢出，不按自报长度预分配；超限拒绝，不裁unknown/模型/secret。单文件输入界与锁定库的内部nesting安全界先保护解析；本片更窄的node/depth接受界必须检查，**不得把解析后计数冒充库解析前的精确RAM/CPU硬界**。profile/实现门须实测最坏合法/非法输入分配与解析行为，不合预算的库路径必须收窄/拒绝或重新审，不写第二套parser绕过。

Check/Render入口、阶段边界/遍历检查ctx，返回前fresh ctx gate，失败零结果。不为同步第三方parser起可遗弃goroutine+select；未提供任意kernel/JS/network操作，亦不声称ctx可在库内部任意指令点强杀。并发调用不共享可变parser、map、plan/input/material缓冲；同plan并发Render只读捕获副本，Destroy需明确外部同步，不伪承诺安全共享Destroy。

## 9. 必须实际执行的验收与负控（本spec未运行）

Go1.27.3/CGO0、synthetic HOME/配置/key，禁止读取真实HOME/keyring/token；B普通测试纯内存。NativeProfile证明是另授的隔离consumer能力门，不把产品codec加上exec/IO。每条报告命令、实际exit/run/skip、exact源码/模板/profile/原始fixture身份；已有31probe只证明库接缝。

| 门 | 真实正控与拒绝断言 | 编译后有效负控 |
| --- | --- | --- |
| A parity | 固定c7完整声明及所有presence、false、有序隐藏catalog字段，B独立复核；错误版本/slot/policy/值/数量拒绝 | 漏字段/丢presence/重排levels的mutant必须与独立golden不等，不能自己输出再自己期望 |
| 三artifact完整性 | config + owned catalog全部成功且auth逐byte/存在性不变；catalog失败全bundle零；catalog absent不读/写foreign sidecar | 只返回成功config、把新key写auth、删除旧OAuth各触发原断言RED |
| TOML语义与布局 | duplicate/quoted/dotted/table冲突、root/已有表/新表锚点、CRLF/注释/无末尾LF、unknown含旧env secrets/大整数；显式/quoted/dotted必须真支持，inline拒绝准确 | raw-only validator、EOF插root、按点误拆decoded key、整文件Marshal各由未触bytes/完整语义断言RED；不能以`{`当完整容器，未来若扩inline须另做span负控 |
| auth观察 | file login/no-login、keyring Found/Missing、auto Found/Missing→file、Unknown/error/ephemeral；缺省/覆盖profile未知失败，auth坏JSON不能当missing | keyPresent→flag、Unknown→Missing/false、auto错误fallback、file存在→login各必须RED |
| native profile | 固定真实target解析TOML/catalog及全部枚举/必填/relative ref；合成第三方request验证flag真/假均只用provider bearer、OAuth不外发；外网禁止且日志不打印头/值 | 缺必填模板、native不支持TOML1.1/enum、错credential优先级必须拒ready；不能只测go-toml接受 |
| catalog | 32模型、有序levels/modalities、false、absent/保留/新必填缺失、raw unknown字段、大整数；foreign ownership/pointer、孤立额外model/重复slug/未知actionable拒绝 | camelCase desired直接当native、猜context/image/默认reasoning、重排/trim/丢隐藏字段各独立RED |
| JSON策略 | 深层decoded重复key、escaped身份、尾随/非UTF8/非对象/超限，auth/catalog未触token逐字相等；不是只校root | map/float64重编码、仅根重复检查、坏auth→空对象的mutant必须RED |
| 材料/绑定 | 精确1槽/1次、1/8192正与0/8193/NUL/CRLF/invalidUTF8负；修改调用者base/声明/材料/map/返回副本不改已捕获plan/candidate | 使用旧key/fallback、遗漏catalog/观察/profile/order基线绑定各RED；不宣称B能完成DB授权 |
| 隐私/限额/取消 | 所有wrapper私有容器fmt/slog/JSON、原始parser错误、输入输出/总bytes/节点/深度/edit边界与取消零结果，无秘密sentinel | 直接slice载体/泄漏error、吞输出超限/取消各有限断言RED；编译失败/panic/超时不算有效负控 |

至少raw-only、EOF-root、全文件重编码、unknown-auth fallback、漏catalog与单槽额外材料六类mutant要在编译成功后触发原断言RED，恢复fresh GREEN。不能为过门删除旧codec/privacy回归、放宽baseline或让全case变unsupported。独立审签exact tree，不以作者probe/main全仓绿替代。

## 10. 授权出口与剩余事实

后续实现承载按map为独立 `internal/codexconfig` provider/parser/privateJSON/profile及tests/testdata，路径须另由计划精确申请。MCP/prompt不借本片写入；不得修改A共享schema、现有Claude/engine/C0、C1/C2/D、server、proto/gen、module或UI来凑通过。依赖批准与NativeProfile/witness事实门先闭，需扩写集先问；本轮只有本文及ignored作者说明。

真实待定而非猜测：目标native version/dialect/catalog schema+必填模板及hash/credential precedence/路径解析实证；D可信effective store/keyring观察适配器与witness；C2 intent/pin编码串交；产品依赖pin批准。它们不影响本spec固定纯codec拒绝规则，但未闭合时不得对用户声称provider可预览应用的完整链已支持。后续完整MCP、AGENTS.md prompt、账号/代理路由和全14-agent能力继续独立推进，不因provider首片而删需求。
