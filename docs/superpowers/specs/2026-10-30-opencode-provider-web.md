# OpenCode provider Web UI — OC-WEB 独立规格

状态：设计候选，待非作者审查及主线程明确源码授权。固定产品基线 `1b037cde2dbb8a7ec5ea654d5d3bbde017d7733c`；不消费 moving main。旧 OC-ASSETS T1–T3 不包含 Web；本规格新建该合同，不虚构旧 Task4。

## 1. 目标、假设和边界

为已登录 owner 在现有配置资产页提供 OpenCode **V1/V2 专属结构化表单**，经已交付 ConfigurationService 七 RPC 管理私有服务端资产。复用 React19.3.1、TanStack Query/Router、Radix/shadcn、现有 Connect/Buf TS、i18next/中英字典及主题；不加依赖。用户是管理 provider/model 的开发者；沿用现有工作空间，不重新设计应用 shell。

权威：`docs/REQUIREMENTS.md` F12、`docs/CCSWITCH-CONFIGURATION-MATRIX.md` OpenCode行；已接受 backend spec/plan 的 `artifacts/design.tar`；固定base的 `configuration.proto` OpenCode消息、`internal/agentconfig/opencode.go`、`configuration/opencode_{domain,envelope}.go`。旧design中的候选身份不替代已交付base；若该来源合同与实际接口冲突，报具体冲突，不擅改backend。

假设与决策：metadata schema_version仍为1，与原生格式V1/V2不同；创建时显式选择 OpenCode，再显式选择V1或V2，无默认。格式一旦选择即固定；编辑不做跨格式转换，要另一格式则另建资产。providerID可相同地存在于多个资产，不设唯一current，不改变任何其它资产。保存/复制/删除不探测连通性、不安装SDK、不选择provider、不写客户端文件，无登录/应用/可应用宣称。

**R1持久排序仍必交，但另片：**本片保留服务端metadata既有列表顺序，无资产拖放/上下移，不把createdAt/ID或本地排序当用户排序。后续需要独立owner/order-revision/schema/RPC授权。模型map编辑和原生有序array编辑不是R1。完整14agents/F12、native D、导入/Pack/MCP/prompt/OMO均不在本片内。

## 2. 页面信息架构与交互

沿用“新建资产 → metadata列表 → 独立编辑dialog”；列表仅id/agent/kind/name/schema/revision/时间，不自动ReadDraft，不放provider树、slot、payload或新秘密进Query缓存。明确展示四provider支持范围，不把其它agent按钮启用。metadata未知类型仍只可看metadata和CAS删除。

OpenCode编辑区分：①资产名/format/providerID；②格式专属provider字段；③models及嵌套字段；④被引用凭据槽；⑤无秘密的结构化变更摘要与保存。重用 `DialogContent` 的焦点圈闭/恢复、Button/Input/Select/Checkbox；OpenCode dialog可单独用 `sm:max-w-4xl`，不改变其它三家的宽度/行为。以fieldset/legend、原生details分组；展开/折叠仅影响视图，不丢值、不改变presence。

所有字段有可定位label；新增row聚焦首个字段，删除row聚焦相邻row或“新增”；有序项提供键盘可用上下移，不依赖拖拽。提交失败有可聚焦错误摘要、逐项链接及inline `aria-describedby`/`aria-invalid`，路径与固定错误说明不插入输入值/服务器低层错误。长model/key标识可换行，桌面1440与375px、200%zoom无页面横向滚动，触摸操作至少44px，中英长标签不截断关键含义。保留light/dark tokens及明显focus ring；不加字体/图标/动画库，不加装饰性运动；reduced-motion下功能/焦点不变。

设计依据是已有Codex/Gemini表单的逐项presence和dirty/CAS保护、VS Code设置的显式override分组、Postman式header key/value行的可读性；不引入后两者的脚本执行或raw body编辑。视觉签名为固定格式/版本身份头与“未设置/已设置”行，不堆叠装饰卡片。

## 3. 完整字段与控件合同

`?`均有显式“设置此项”开关：关闭=absent，打开不补业务默认。可选boolean为absent/true/false三态；number用原样文本输入，不能用 `valueAsNumber`、浮点运算/locale格式化改写。message/container presence与内部集合独立；present-empty wrapper保留。required-empty无效，不因optional支持而放宽。所有下表的 `extensions` 是已授权typed扩展map，与unknown stored extras不同；各层extensions在wire也是optional OpenCodeObject，同样保留absent/present-empty，表内不逐处重复标`?`。

| 区域 | 必须提供的全部控件（名称按native语义，wire用现有generated camelCase） |
|---|---|
| 共同 | 资产name（既有trim后1–128UTF8），providerID必填；format V1/V2必选且固定 |
| V1 provider | `npm?`, provider `name?`, `options?`, `models?`, `extensions` |
| V1 options | `baseURL?`专属HTTPS输入；`apiKey?`是slot reference选择，不是密码文本；`headers?` header→slot表；`extensions` |
| V1 model map | map key为modelID；`name?`, `limit?`, `options?`typed object, `modalities?`, `thinking?`typed value, `cost?`typed value, **`variants?`map**, `extensions` |
| V1 limit/modalities | limit `context?`,`output?`非负精确整数，无V1 input控件；modalities `input?`,`output?`有序字符串array和extensions；limit也有extensions |
| V1 variants | variantID→typed object，不能变成V2 array；每项object内用同一typed value editor |
| V2 provider | `package?`,`name?`,`canonical?`,`env?`有序环境变量**名**array、`settings?`,`headers?`,`body?`typed object、`models?`map、extensions |
| V2 settings | `timeout?` oneof“number原文”或“原生false”（wire disabled=false，不发true）；`chunkTimeout?`number、`compaction?`、`transport?` http/websocket、extensions |
| V2 model map | `modelID?`,`family?`,`name?`,`package?`,`disabled?`三态、`settings?`,`headers?`,`body?`,`compatibility?`,`capabilities?`,`limit?`,`cost?`,`variants?`、extensions |
| V2 modelSettings/compaction | settings有`compaction?`和extensions；compaction必选type summary/native及extensions |
| V2 compatibility | `reasoningField?`只允许secret-reference；`requireReasoning?`,`maxTokensField?`枚举max_tokens/max_completion_tokens、`requireFinishReason?`,`requireAssistantAfterTool?`,`supportsPromptCacheKey?`及extensions；省略reasoningField合法 |
| V2 capabilities/limit | capabilities启用后tools为明确true/false，input/output为**present**有序array（可空），及extensions；limit context/input/output可选精确整数及extensions |
| V2 cost | 先选single或tiers；single与tiers每一项含必填input/output number、`cache?`(read?/write?/extensions)、`tier?`(type=context,size必填整数/extensions)、extensions；tiers为有序array，不排序/去重，present空tiers不丢 |
| V2 variants | **有序array**；每项必填id，`settings?`,`headers?`,`body?`,extensions；保持顺序，拒重复id，不转map |
| 全域扩展/开放值 | 每一层允许的extensions、V1 options/model options/thinking/cost/variants、V2 body及variant body均有相同typed树控件；保留它们各自位置，不扁平合并 |

V2没有通用baseURL/apiKey字段：SDK特定endpoint/key在其原有body/extensions位置通过typed字段及slot reference表示，不搬到V1 options。`env`只是名字，不读取进程环境。npm/package仅字符串metadata，不能执行/安装；不存在“从环境/文件解析秘密”功能。

### 3.1 Typed value与map/order

递归editor先显式选择 null、boolean、canonical-number、array、object或secret-reference；未选类型不能保存。null oneof发null=true；boolean=false必须保留。array有序，支持增删/上下移；object用可编辑key/value行，提交前检查重复，不先折叠成JS object。所有map（models/headers/extensions/objects/V1 variants）无顺序承诺、无“持久排序”按钮；载入显示可按key确定性排列，但不排序array。rowID仅用于React key/焦点，不编码。

**开放字符串默认秘密，没有literal逃生口。** 不提供OpenCodeValue.literal输入，即使填入 `{env:...}` / `{file:...}` 也只能作为尚未解析的秘密值替换slot，不被本片解释。已知name/package/id/modality/enum字段按本来类型编辑，不把它们全塞secret slot。header值、V1 apiKey和reasoningField只选reference；不提供公开header value字符串输入。开放object键 `__proto__`/`constructor`按普通自有key处理；用null-prototype记录/安全defineProperty，不原型展开/合并，不执行用户键路径。

## 4. 验证、保真与预算

沿用固定backend约束：provider/model/variant ID 1..128 ASCII `[A-Za-z0-9][A-Za-z0-9._-]*`；npm/package≤256合法npm-style单名或@scope/name，非URL/命令；已知显示文字1..256UTF8且无control/坏surrogate，不trim；V1 endpoint≤2048绝对HTTPS，无userinfo/query/fragment/反斜线/坏decoded-path或无效端口，保拼写。

累计models64、variants128、headers64、extensions256、递归value节点2048、每棵value根深度1且native深度≤12、唯一refs≤64。proto wrapper-depth不是native-depth，不再人为折半。headers key为≤128 RFC-token，大小写重复无效；其它map按原case检查重复。array/object的空与缺省不合并，strings array≤256项，每项按原字段约束；正文必须完整覆盖字段，不截断。

number保持canonical decimal原文，拒NaN/Infinity/指数或非canonical写法，不舍入/coerce；安全整数范围±(2^53−1)，known integer字段不得有小数，V1 limit非负。UI可用数值解析作拒绝检查但不能用解析结果生成请求；服务端仍是精确数值语义的最终裁决，不以JSON.stringify(generated wire)长度冒充native provider大小。

provider/desired64KiB、request/declaration96KiB、HTTP及envelope预算沿base不变；客户端按native镜像序列化UTF8做保守校验，boundary fixture由实际Go marshal验证；无法验证的结构阻止提交并固定说明，不删分支/静默降级。Web复用base `createConnectTransport` 与connect-web2.3.0的JSON默认；Create/Update在发送前用相同generated request schema的ProtoJSON UTF8长度检查96KiB，包含所有metadata/wrapper/mutation开销，不只数payload；临时秘密序列化文本不入缓存/日志且finally去引用。browser实测Content-Type须确为JSON；两codec的HTTP能力由fixtureRPC独立证明，不冒称浏览器跑了protobuf。秘密单slot1..8192bytes，无NUL/CR/LF；本CAS全部已知replacement合计≤64KiB，原有未知长度由服务端最终把关，UI不取回长度。超量只报固定错误、保留非秘密草稿。

## 5. Slot生命周期、CAS和错误

slot固定 `opencode.api-key`、`opencode.value.01`…`.63`，asset-local，可复用同slot；显示slotID/引用位置/存在bool，不显示秘密、长度、hash、尾位。`R`=当前typed树所有必需refs；读取status必须恰为R，无重复/多余/缺失，readiness恰为R全present（R空也Complete）否则NeedsCredential。Readiness不是native可应用。

Create不发KEEP，可稍后补充（省略秘密），明确REPLACE才发送。Update默认省略/KEEP与原revision绑定；每slot最多一个mutation。CLEAR须逐slot确认，确认取消不改原草稿/引用；移除最后一次引用或删除含ref的model/父节点时，若旧slot有值，确认同时移除引用并在**同CAS**显式CLEAR，否则不提交。共享ref删除一处不清其它引用；slot不自动重编号。新ref可引用未填slot，明确展示NeedsCredential。总引用/新replacement/已确认CLEAR分别记账，不把R外的pending CLEAR错误删除。

新秘密只驻留password DOM/ref与一次性owned发送对象，不入普通form state、Query/mutation缓存、URL/history、storage、日志、错误或结构预览。发送结束/失败、确认丢弃、卸载、切语言、换owner/origin/logout均清本地新秘密和发送对象引用；JS不承诺内存物理擦除。语言切换保留非秘密草稿，清新秘密及CLEAR确认、取消等待；若曾发写入，保留结果待确认与blocked状态，重新确认当前草稿需要的CLEAR，不自动修复引用。取消/关闭不宣称回滚已提交事实。

读写复用 `useConfigurationRequest` generation/abort/单次写门，沿用禁止重试。CAS Aborted保非秘密草稿、清新秘密、阻止旧revision重发；Unknown/断连/commit后丢回执同样阻止重放，明确要求关闭刷新或确认丢弃后重新读，绝不“保存失败即未写入”。迟到read/write响应不能污染新owner/语言/editor。明确取消dirty放弃前保持原草稿；确认放弃才清理并恢复trigger焦点。

写成功还校验assetID/agent/kind/schema/name/revision、完整provider语义等价（map无序，array有序）、slot集合/presence和readiness。Copy先读同sourceRevision、拒unknown extras，再要求新ID/rev1、typed树/ref全等、**所有slot absent**（R空Complete，否则NeedsCredential），不选择新副本。Delete仅metadata CAS和明确确认。

## 6. 兼容、未知数据与数据流

仅 `agentId=opencode/kind=PROVIDER/schemaVersion=1`允许专属读；ReadDraft payload必须恰为opencodeProvider且format恰选一分支。未知agent/type/schema保持metadata-only；known projection带HasPreservedExtras时用相同结构只读，不能保存/复制，不显示unknown原文，仍可明确CAS删除。typed extensions是可编辑已知成员，不能误当preserved extras；返回畸形树/slots/readonly写回用固定DataLoss/FailedPrecondition，不误转Claude分支。

旧三provider所有type/controls/presence/CAS/密钥/unknown/late-response合同及golden不变，不全局增加codec/大小/通用JSON编辑能力。Query继续只缓存`metadata()`投影，retry=false/gcTime=0；ReadDraft与write view只在当前editor本地owned state，list不携带它们。账号/来源切换销毁editor，失效请求无权改缓存/弹成功提示。保存/复制/删除的成功提示只说服务端资产结果。

## 7. 可验收结果及非目标

执行计划定义真实TLS/SQLite/auth/ConfigurationService fixture、自检七RPC两codec、真实构建的浏览器表单业务纵切；不是mock controller/静态截图验收。必须覆盖V1/V2最小省略及全字段、false/0/null/empty、map/array次序、合法深度12/非法13、多个复用slot、CLEAR取消/确认/移ref、COPY无key、未知extras只读、owner/CAS/late/Unknown、dirty导航/语言与秘密清理、四provider回归、metadata缓存脱敏、375px/light/dark/键盘。编译成功的退化控制必须被普通行为断言拒绝，正源恢复后再次通过。

规格通过只批准该设计；生产授权仍由主另发。持久排序R1、native应用、其它agents/F12不得因本UI交付标记完成。命令、精确写集、任务映射和浏览器fixture合同见同名plan；没有已跑UI/浏览器/源码测试的主张。
