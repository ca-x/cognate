# CX-ASSET / C1 — Codex typed 加密资产与 Web 编辑

日期：2026-10-29。状态：**设计候选，待非作者 Spec/Quality 审；未实施、未授权 plan/source**。本文的 C1 是 Codex capability map 中的 **CX-ASSET**，不是 Claude/Linux CLI 的 nativeconfig C1。本轮只写本文及 ignored 作者报告。

## 1. 目标、权威与能力声明

权威为 `docs/REQUIREMENTS.md` F12、`docs/CCSWITCH-CONFIGURATION-MATRIX.md`、已签 `2026-10-29-codex-capability-map.md`（SHA256 `d6691bd0da4a3e9907ad4f51370d406826af2025af03359c623c74ca018992bd`）。本文消费已交 Codex A spec SHA256 `a210da792d9487200fa3616e6d7abb58ef7ec81f139af17db646595f9e1d981e` 与实现固定 commit **`c7d8ab60c2f7ae6d2c4b18b1163b72fe65d59dc8`**。A 是声明 planner，不是文件 renderer、secret 授权、安装或原生 apply。后续 B/C2/D、完整 Codex 与其余13个agent/F12要求均不删减。

本片交付边界：owner 私有的 **custom HTTPS/native Responses/provider-key** typed provider 资产，单 slot 的 KEEP/REPLACE/CLEAR、加密 CRUD、copy 无 slot credential、Web 专属结构化编辑。只保存服务器资产，不选择本机 provider、不生成可安装 patch、不改变文件/keyring/auth.json、不申请 native grant。不得因资产可编辑或 `credential-complete` 就显示“Codex 应用已支持”。

**官方账号与 API key 分开：** 官方 ChatGPT/OAuth/本机账号卡的发现、账号引用、切换与 current 保护仍属后续完整 Codex 纵切。它不是 `provider-key` 的空 key 变体，不是 Claude `native-login`，不把 OpenAI 的 hostname 当作认证分支判据。首片不接受官方账号/OAuth/env-key/headers/command/AWS/keyless 请求；Web 明示限制，不提供可成功保存的伪官方账号表单。未来扩展需新分支合同，不能给本片空凭据自动 fallback 官方账号。

## 2. 已有 seam 与最小必要增量

下表为固定 c7 源码事实与后续候选职责，不是写集授权。

| 已有路径/入口 | 当前事实 → 本片必要增量 |
| --- | --- |
| `internal/agentconfig/codex.go` | A neutral desired/catalog/枚举/limits/slot 已存在。域复用其类型与常量，不复制第二套 model schema，不改 A 或 module。 |
| `internal/server/agentadapter/codex_patch.go::Registry.PlanCodexProvider` | 已有真实 moejs/host重算，仅接受 `KeyPresent=true`。本片不改 runtime/注册/JS，不把 planner当缺凭据资产的存储验证捷径。 |
| `internal/server/configuration/{types,domain,envelope}.go` | 当前 Draft/EditView 与 envelope 是 Claude 专属；新增明确 Codex provider 变体与分支分派、专属校验/单槽合并/clone，不重写通用资产系统。 |
| `internal/server/state/configuration.go` | 已有 owner+ID 的同快照 metadata/payload 读取、AAD加密、事务quota/CAS；agent metadata 已能保存Codex。本片复用现有表/Store接口，不新建 provider/key/catalog 表、Ent schema 或迁移。 |
| `proto/skillhub/configuration/v1/configuration.proto` | 7个 CRUD RPC、Draft.payload oneof、SecretMutation oneof、SecretStatus/readiness/extras flag 已有。仅增加 Codex typed payload/messages/enums；不新增RPC/REST/动态schema。 |
| `internal/server/httpapi/configuration{,_mapping,_codec}.go` | 复用 session owner、strict JSON/proto、限制/取消/错误分类；增Codex mapper与分支校验，不能只改metadataSupported就放行全部agent。 |
| `web/src/configuration{,-state,-form}.tsx/.ts` | 当前是Claude专用表单、两secret slots、requireClaudeView与真实CRUD request lifecycle。新增Codex专属form/view guard、typed分派，复用已有对话框/请求门禁，Claude分支不改义。 |
| `internal/server/configuration/native_consumer.go` 及 native pin/codec | c7原生链路仍只支持Claude。**本片不扩展这些能力**；必须实测Codex资产在既有native入口被拒，不能因CRUD分派放宽而误通Claude七slot路径。若发现需改native生产代码，先报告精确反例另授。 |

未来计划须精确列出域新增Codex文件/测试、HTTP/mapping/protocol/logging测试、公共proto及正常Go/TS生成物、Web分派/表单/文案和真实浏览器证据路径。优先新增专属文件，不先做整包接口重构；没有固定文件数豁免实际编译闭包。公共 proto/gen 与 CX-NATIVE-GRANT/C2、native apply 串行冻结、正常Buf生成；双方不得并写共同输出，不手改生成器/生成文件、旧tag/Claude枚举或旧native向量。

具体拆分候选为新增域 `codex_domain.go/codex_envelope.go` 与专属测试、Web `configuration-codex-form.tsx`；共享变更集中在现有types/domain分派、HTTP mapping/codec和Web `configuration.tsx/configuration-state.ts`。正常生成输出为 `gen/skillhub/configuration/v1/configuration.pb.go`、`web/src/gen/skillhub/configuration/v1/configuration_pb.ts`；Connect Go handler是否有实际生成diff以双模板输出为准，不为凑清单手改。现有Claude envelope、Web Claude表单、A八源码、state/Ent优先不动；任何额外必要源码/test闭包在计划审先列明再申请。

## 3. 资产身份、typed 内容与 presence

由 payload 分支确定 `agent_id=codex`、`kind=provider`、`schema_version=1`；创建者不能用额外agent字符串重标记。复用现有 opaque asset ID、name、revision、时间metadata。name按既有规则trim后1..128 UTF-8字节且无控制字符；provider/catalog值不trim、不改大小写、不偷偷补默认。

域建议 `CodexProvider` 只存 `AuthMode/Endpoint/Model/ReasoningEffort/Catalog`，其中可选值与 `agentconfig.CodexProviderDesired` 对齐。Draft/EditView 使用精确的Claude/Codex typed联合（任一且仅一），不是 `any`/map/raw JSON；ReadDraft输出分支必须匹配metadata。`KeyPresent` 从真实存储slot推导，**不是可写draft字段、不存调用方bool**。`CredentialReference/RequiredSlots/LocalAuthPolicy/providerID/wireAPI/definition digest` 不是用户资产可编辑字段，亦不复制进payload冒充native intent。

| 字段 | 必须保持的语义/限制（同A） |
| --- | --- |
| authMode | 仅 `provider-key`；必填，未知/未指定非法。official account不能用同形数据绕过。 |
| endpoint | 必填1..2048 UTF-8字节，原样绝对HTTPS、hostname非空、合法端口1..65535；无首尾空白/控制字符、userinfo/query/fragment/反斜线。HTTP含loopback本片不支持；不加`/v1`、不探测DNS/远端、无代理转换。 |
| model | 必填1..128 ASCII字节，`[A-Za-z0-9][A-Za-z0-9._:/-]*`，大小写敏感。无catalog时不声称模型存在或服务可达。 |
| reasoningEffort | optional；`none/minimal/low/medium/high/xhigh/max`；absent不同于显式`none`，不补默认。 |
| catalog | optional对象；absent=不声明目录override，不是上传/删除任意本机目录。存在则models为1..32有序条目，model ID唯一；不去重/排序吞数据。主model必须在目录里。 |
| catalog.model | 每条必填，同model语法。不是展示名，删除或改名必须重新验证主model引用。 |
| displayName | optional；存在1..256 UTF-8字节，无首尾空白/控制字符。absent不回填model。 |
| contextWindow | optional精确整数1..2147483647；不接收字符串/浮点进行宽松转换，不使用JS不安全number或locale文本转换。 |
| supportsParallelToolCalls | optional bool；absent、false、true三种不同状态；不能把false折叠成absent或自动true。 |
| inputModalities | optional有序集合，存在1..2个不重复的`text/image`；空、未知项、重复非法；不排序。 |
| baseInstructions | optional私有正文，存在1..2048 UTF-8字节；允许原样CR/LF/TAB，拒绝其余控制字符；不trim、不丢“高级/隐藏”字段。 |
| reasoningLevels | optional有序集合，存在1..7个唯一允许effort；absence不是空集合。 |
| defaultReasoningLevel | optional；存在须reasoningLevels同时存在且包含该值。主model的levels与顶层reasoningEffort同时存在时，须包含顶层值。 |

新请求中所有optional的显式null/错误类型均拒绝，不当absence；已存在的Claude合同不借本片改义。更改资产时提交完整已知typed草稿，关闭optional开关意味着**资产override变为absent**，不代表授权删除本机文件字段；本机absence的处理仍按A/B/D的既定ownership。清空catalog前明确提示将移除资产目录声明，不能等同native sidecar删除。

域的形状校验按上述A合同，包含嵌套/总量；不为了校验把缺key资产的`KeyPresent`硬设true，也不修改A接受缺凭据。对完整资产建立独立parity测试：由实际slot推导Desired，调用真实已交A，验证字段/presence/顺序/local-auth policy一致；缺凭据资产不得进入planner。若将来提取共享validator，须独立授权A写集，不能在本片暗改注册层。

## 4. 单 secret slot 与复制合同

唯一slot为 `codex.provider-key`。普通API key与provider bearer不是两个独立slot；不新增`OPENAI_API_KEY`第二份凭据，不接受env/header/keyring路径或command。保存于同一加密资产payload，不另造secret服务。slot字符串在已有SecretMutation/Status中精确匹配；Claude槽、重复槽、未知槽或额外replacement成员均拒绝。

| 操作 | 创建 | 更新 | 结果 |
| --- | --- | --- | --- |
| 缺省mutation | 无slot | 保持既有slot | 不把缺省当CLEAR。Web仍明确显示选择。 |
| KEEP | 拒绝（不存在可保持的基线） | 保持存在/不存在状态 | 永不返回旧值。 |
| REPLACE | 允许 | 允许 | 新值1..8192 UTF-8字节、无NUL/CR/LF，不trim；空值不是CLEAR/KEEP。 |
| CLEAR | 允许，仍无slot | 显式删除slot | 不能清本机官方登录、目标文件或所有agent秘密。 |

新保存与copy允许缺slot，Readiness=`NEEDS_CREDENTIAL`；存在有效slot则`COMPLETE`。这只是凭据完整性，不验证真实key、endpoint、模型支持、原生登录或可应用能力。所有成功Codex DraftView返回恰好一个canonical SecretStatus，`present=false`也不省略；不返回长度、末尾、hash、masked原值或credential ref指向的内容。client不能以SecretStatus覆盖真实状态。

Copy使用原CopyAsset RPC及固定source revision，新ID/revision1；只复制本片已知typed provider/catalog与新name，**不复制slot credential、selection/device/grant/consent/自动应用资格或任何opaque payload**。copy结果必须`present=false/NEEDS_CREDENTIAL`，下一次KEEP仍缺凭据。baseInstructions是受保护的typed私有正文而非credential slot，可随typed内容复制；不声称有任意正文token的DLP识别能力，正文仍不得日志/公开分享。unknown extras中可能藏secret，见下一节拒绝copy，不能raw-envelope clone后只删一个key。

未知/未指定auth mode、official-account不能被CLEAR转成native-login；不得给缺slot资产从master key/本机账号/环境变量补凭据。删除只删除本owner资产及其slot，不修改设备。无reveal/export secret RPC，公有目录、默认Pack分享、MCP token等不因此获得资产读取或秘密授权。

## 5. 加密载体、unknown保全与版本

复用现有Store的`ConfigurationPayload`和AEAD/AAD，新增同代envelope的明确`codex-provider`变体；内部包含version、typed provider与仅一份stored provider key。沿用owner/user/asset/agent/kind/name/schema/revision的AAD绑定与原随机nonce算法，不引新crypto/key/表。Claude既存`claude-provider`密文无需迁移或重写，decodeClaude入口保持严格。读取使用 `ReadConfigurationAssetVersion` 的同一metadata/payload快照，不分两次Get/Read后拼接。

必须区分三种“未知”：

1. **公共请求未知成员/enum/oneof：拒绝。** Web不能提交任意JSON、`extras` map、未知catalog行字段或opaque保留句柄。body中的unknown永远不成为宿主保留数据。
2. **已有加密资产中的未知字段：保全，不猜义。** 受限扫描须先拒绝任意层重复key/歧义/非法JSON。若已知version/variant及全部已知字段可以完整校验，但存在额外成员，ReadDraft最多返回已知typed私有view+`has_preserved_extras=true`，绝不返回未知值；Web只读并明确原因。Update和Copy必须fail closed，原ciphertext/revision不变，不通过重编码已知字段丢掉unknown。无需为首片造通用unknown merge、数据迁移或额外表。只有Delete在原owner/CAS/明确确认下仍可删除整个资产。
3. **未知variant/version/schema/enum或已知字段无效：不构造假typed view。** List/Get仍可返回合法metadata；Edit/Update/Copy返回受控unsupported/不可用分类，保留原密文，不降级成Claude/generic JSON，不修复或覆盖。损坏AEAD/伪造身份等走固定不可用错误，不回显解密/parser原文。

这里“保留”是明确的**拒绝有损写并保全原数据**，不是声称现有Claude有unknown无损merge，或首片能编辑任意扩展。未来importer/unknown-aware编辑另需字段ownership与copy脱敏合同；未解决不能放开写。A永远不接收extras/完整envelope/secret：本片带extras的资产即便key存在也不能被认作可plan。该只读限制须实际UI/API测到，不仅展示警告后仍允许提交。

## 6. owner、CAS、配额与不确定写入

- 身份仍由现有session认证解析owner，不新增caller指定owner或复用MCP token权限。跨owner读取/更新/复制/删除不可探知内容；错误不区分其他人的具体存在状态。UI显示的asset ID不授权读取。
- Update/Delete仍要求`expected_revision>=1`；Update拒绝MaxInt64溢出，CAS一次`+1`，失败不rebase/重试。不能通过更新改agent/kind/variant，Claude↔Codex须新建独立资产，不复用旧ID偷偷换type。
- Copy沿现实现：原子读取source metadata+payload并核expected_source_revision，以该快照创建无secret新资产；不声称source从读取到目标提交始终加锁不变。并发变化在读取前应冲突，读取后可复制已验证旧快照，绝不转用最新版或重新读取secret。destination事务继续执行quota。若产品将来要求更强source-commit联锁，另签Store合同，不在UI假装已有。
- 每owner所有agent/kind合计250资产，单解密payload≤256KiB（含envelope/secret/未知保留），不为Codex另开250。A desired deterministic JSON≤48KiB、catalog≤32KiB仍约束本片typed内容；用实际presence投影计算，不能先截断/去条目。外层请求≤128KiB，未压缩响应≤1MiB，gzip不豁免解压预算；尺寸按UTF-8编码字节而非字符数。
- 复用HTTP handler 8个即时admission slot、20秒父ctx上限、取消贯穿Store/事务及固定错误映射；拒绝超额不等队列、不自启goroutine补偿。不为长catalog放宽限制/网络缓冲，不扩大native grant配额。
- 超时/断连/取消/未知COMMIT不得以UI取消推断回滚。保持既有请求generation、AbortController、write-uncertain阻断：刷新metadata并显式重新ReadDraft后才能新动作，不能自动重放Create/Copy/Update/Delete；Copy/Create可能已创建，先刷新列表而非再建一个。任何写后端返回不确定也不交付伪成功草稿。

## 7. 最小公共wire增量与兼容

沿用 `ConfigurationService` 与现有message/field语义，候选增量为 `Draft.payload` 的新分支 **`codex_provider = 3`**（现Claude为2），以及该分支的typed子消息。最终tag须实施前Buf/基线核空闲，若被其他签交占用先报告，不复用旧tag。无新agent通用map、raw_payload、secret读API或selection/apply字段。

候选结构：`CodexProvider{auth_mode,endpoint,model,optional reasoning_effort,catalog}`；`CodexCatalog{repeated models}`；每model含§3全部8字段。auth/effort/modality为封闭typed enum，0=UNSPECIFIED拒绝，effort的`none`须有独立非0值。context_window为optional uint64并域限制，parallel为optional bool，display/baseInstructions/defaultEffort为optional；modalities与reasoningLevels各用存在性message包裹repeated enum，保留absent与present-empty的区别；catalog本身也是message presence。不是把repeated缺省空当未设置。

字段不能经`Struct/Value/map<string,string>`、CSV或JSON文本逃逸类型校验。JSON/proto两协议必须在任意嵌套拒绝unknown、重复singular、oneof冲突、非法enum、null/错型与超深输入；数组元素按§3验证重复和顺序。只对新Codex字段收紧新合同，不暗改Claude旧兼容语义。protojson合法的uint64十进制编码由标准生成codec处理，域范围仍严格，不把合法proto数值编码与“把上游contextWindow字符串宽松导入”混为一谈。

旧client遇Codexmetadata只显示unsupported，不能误开Claude表单；ReadDraft新oneof对旧client不能转成空Claude再写回。新client接旧server仍可使用既有Claude；Codex创建得到固定unsupported/invalid拒绝，不能退回generic JSON/Claude字段。新server不因旧client缺payload分支猜类型或补值。Buf lint/breaking、Go/TS正常双模板fresh parity与旧Claude成功/失败向量是硬gate。保留raw generated DTO敏感政策，replacement用标准debug_redact标注，但**不声称Go String/Marshal自动脱敏**，不手改生成器。

## 8. Web结构化交互与敏感生命周期

复用现有配置入口、owner-scoped client/query隔离、Button/Input/Select/Checkbox/Dialog/Textarea及i18n/主题/窄屏样式，不新增通用代码编辑器/组件库。创建显式选择Claude或Codex API provider，编辑dispatch按metadata+payload精确匹配；不按agent字符串强转。官方账号入口只有准确说明与不支持原因，不提供会暗落入API-key路径的提交按钮。保存按钮为“保存到服务端”，无安装/切换/Authorize/Consume副作用。

Codex专属表单分为基础（名称、HTTPS endpoint、model、optional effort）、凭据（单slot presence+KEEP/REPLACE/CLEAR）、模型目录（显式启用、增删行、上移/下移、选主model）三组。每行全部8字段可编辑，较少用的字段可折叠但不能丢；optional显示“未设置/显式设置”，parallel提供未设置/false/true，modalities与levels保留选择顺序并有键盘上移/下移。default effort只在已选levels里选择；改变主model/删除行先校验引用，不静默改到第一项。contextWindow是严格十进制整数字段，不用浮点/指数形式自动修复。私有baseInstructions使用有label的Textarea，保留CR/LF/TAB；不伪装公开说明或日志安全字段。

REPLACE只在明确选择时展示password输入，不把服务端presence标签/遮罩当真实key预填。CLEAR若原来存在须明确确认；保留与删除不能由输入框空值决定。成功、失败、取消、切资产/owner/origin、logout/unmount、locale切换时清理替换明文与请求引用；不能把SecretMutation放TanStack query cache、URL、local/sessionStorage、console/toast/error对象、analytics或浏览器持久草稿。query缓存只放最小metadata，ReadDraft与私有正文停留在owner作用域短生命周期编辑状态；失败后需重新输入secret，不缓存旧request自动重试。JS/浏览器内存不承诺可证明物理擦除，不新增持久副本。

CAS冲突提示固定文案、保留非secret草稿但清Replace并阻断旧提交；明确“放弃并重新载入”或“另存新资产（不复制凭据）”，不读取新revision后自动merge/重交。unknown extras只读，Copy也禁用并解释，不靠前端禁用代替后端拒绝。copy弹窗列明不含credential/设备状态；delete只服务端资产、不能撤销本机应用。错误只用固定code文案，不回显endpoint、key、私有正文、上游或parser错误。键盘Tab/Enter/Escape、初始焦点/返回焦点、field error关联与焦点可见、中英/light/dark/375px窄屏均须真实事件与截图验收；不把typecheck通过当UX完成。

## 9. 未来验收与独立负控（当前未运行）

仅TempDir、合成owner/key/token、隔离真实SQLite/HTTP及真实moejs；Go1.27.3/CGO_ENABLED=0，实施先modern-Go完整list与TDD，不用真实HOME/账号、不运行upstream。保存以下层次独立证据，不以作者/主全绿代非作者审：

1. **域/存储：** Codex+Claude混合真实CRUD/reopen、owner隔离、密文/AAD tamper拒绝；未保存key与CLEAR/COPY的readiness、clone隔离，copy响应及经真实Store认证解密的目标envelope均无原slot值，不能只搜索随机密文中是否出现sentinel。250跨agent quota、payload/desired/catalog总预算边界与并发CAS单赢家、MaxInt64、snapshot copy前后barrier。unknown stored嵌套字段只读/写copy拒绝且原密文revision原样，损坏/未知version不能转typed成功。
2. **A消费：** 全catalog8字段、absent/false/显式none/有序数组、边界值构成独立oracle，完整资产由真实slot投影后真实Registry.PlanCodexProvider得到同声明；key缺失或extras只读绝不伪造presence进入A。确认secret值/未知内容不进入JS，localAuthPolicy仍未解析；不生成requires_openai_auth或可安装内容。
3. **协议/隐私：** 真session JSON/proto成功/失败，任意嵌套unknown/重复/enum/oneof/null/空集合/错型；128KiB请求/1MiB未压缩响应及gzip、8slot/20秒取消。真实标准log+slog、HTTP响应/错误、源日志policy审查不含合成replacement/session、私有正文或错误原文；DTO默认printing仍明确敏感，新增safe wrapper若宣称安全须用非常规fmt动词/容器/JSON实测，不能只测Stringer。
4. **真实Web：** 浏览器走真实服务（不是mock-only）创建→编辑全部catalog字段→保存→重开→copy→缺凭据→补key→CLEAR；服务端落盘状态与屏幕一致，主model引用与有序列表不丢。冲突/未知COMMIT/owner切换/Cancel/unmount以barrier验证无旧generation回填或自动重放，storage/console/缓存/截图不含秘密。light/dark、中英、窄屏和键盘焦点有截图/事件证据；截图只用合成且遮罩的credential。
5. **边界/回归：** CRUD动作独立计数证明零native Prepare/Authorize/Consume、零客户端FS/keyring/安装写；既有native入口收到Codex资产明确拒绝，不能落进Claude七slot解码。Claude CRUD/Web/旧wire与native向量继续通过；public/Pack/MCP边界未被扩权，Buf正常生成parity/客户端依赖与module无改。

至少六个编译成功后真实行为RED的overlay/浏览器负控：信任caller keyPresent；copy直接克隆包含slot的envelope；丢parallel=false或catalog数组顺序；忽略stored unknown后重写；取消/CAS冲突自动重试旧写或删generation gate；错误路径输出raw replacement/正文。每项保存patch/hash/命中selector/命令/exit/有限cleanup，恢复exact source后GREEN；编译失败、panic、整体timeout、零匹配或mock调用次数不能充行为RED。独立Spec/Quality在精确源码freeze之后进行；本文只有设计冻结，不签C2/native apply、完整Codex、14-agent或F12完成。
