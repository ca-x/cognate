# C7 B + D5：数据、协议和状态，v2拟议规范

**设计，不是已存在的Go/proto/数据库类型。** 公共C7合同version=2；新表/本机record格式version=1；二者不是Codex/Gemini contract version。旧A只在未批准设计中出现，不曾产品签发，因此新代码不接受A会话证书来创建B权限。SHA32、operation16、nonce32、Ed25519 key32/signature64；所有整数先验证域/溢出，再编码，JS通过bigint/十进制字符串，不经number损失revision。

## 1. 唯一新增表与准确关系

Ent entity：`PackConfigurationPolicy`；表：`pack_configuration_policies`。两种显式row_kind=device/consent，没有target专属表、没有伪Web session或临时phase长期化。敏感列标记Sensitive，所有加密使用原server configuration AEAD、独立purpose AAD，不新建masterkey。

| 列组 | 字段/类型/规则 |
| --- | --- |
| 共同header | id INTEGER正主键；public_id不空唯一immutable（现有rand.Text规范，max64）；row_kind enum immutable；record_version=1；origin规范https max2048 immutable；recipient_user_id正FK immutable；gate_version非负；created_ms/updated_ms正；charge精确计算 |
| 共同终态 | state=active/revoked；revoked_ms可空，revoked要求正且不能回active；authority ciphertext认证state/revision，不相信裸列篡改 |
| device专有 | device_id规范非空max64；device_public_key32；enrollment_child_binding_hash32；auth_revision正；control_revision正；mailbox_revision正；stop_generation非负；authority_ciphertext；command_ciphertext可空；report_ciphertext可空 |
| consent专有 | parent_device_id正self-FK；owner_user_id正FK；pack_id正FK；share_id可空FK（跨owner必有）；grant_revision正；policy_revision正；policy_enabled bool显式false；expires_ms可空（空=直到撤销/版本失效）；authority_ciphertext；policy_ciphertext |
| 不存在的列/权限 | 没有owner bearer、issuer session FK、agent secret、旧本机secret/secret hash、长期native child/grant、任意本机路径或shell命令 |

device行所有consent专有列必须SQL NULL；consent行所有device专有列必须SQL NULL。authority_ciphertext共同必有；device的policy_ciphertext为空，consent的command/report为空。用命名CHECK证明row_kind本行shape、bytes长度、positive/nullable与phase组合；Ent0.14.6已有entsql.Checks/IndexWhere，不引新依赖。跨行关系在原事务验证：parent必须同recipient/origin的active device，且parent_device_id本身为空；不允许consent→consent、self-cycle、错误owner/share/Pack分区。FK不代替这些证明，不新增trigger。

FK/edges：User拥有recipient policies和issued consents两条role命名edge；Pack/PackShare反向consents；self device→consents；新表到native challenges/sessions的真实parent edge。recipient/owner删除cascade相关authority，Pack/share删除cascade对应consent；owner不是recipient时不删其device锚点。child删consent不删device；删除/清扫授权仍受预算与事务，不能按未证明ID分页漏行。

索引：public_id唯一；device `(recipient_user_id,device_id)`、`(recipient_user_id,device_public_key)`、`enrollment_child_binding_hash`各自仅row_kind=device唯一；consent `(parent_device_id,public_id)`；查询 `(owner_user_id,pack_id,state,id)`、`(recipient_user_id,row_kind,state,id)`、`(parent_device_id,row_kind,state,id)`、`(expires_ms,id)`。新版本建立新consent，不对同Pack/device做“只能一条”的唯一约束，保留Superseded/撤销历史。

新quota（待随本设计批准，不缩旧公开域）：每recipient最多16个active device、全服务256；每owner和recipient各最多256条consent、全服务4096；包括revoked历史每账号最多1024条consent，超限需显式受权清理，不能静默删除Unknown历史。新row charge<=192KiB（固定开销+四ciphertexts+全部列实际长度），global总charge<=64MiB、每recipient<=8MiB、每owner grant总charge<=4MiB。先证明完整rows/charge，再预留post-state quota；历史/当前都计费，采用remaining差值避免加法溢出。旧native owner/global quota不扩大，另对真实device parent聚合，不能每新consent绕开device总额。

device锚点及已消费enrollment binding的tombstone不因owner Logout/短child prune/普通grant清理删除；另限每recipient共64个device锚点、全服务1024（含revoked），满则明确Quota而非丢single-use证据。T4/账号完整删除时policy与原child同时清除，旧token也不存在，不靠保留一个不可用FK维持长期授权。

## 2. 加密authority/policy与本机key

### DeviceAuthorityV1

包含record/header digest、规范origin、recipient、device ID/key、auth_revision、state/revoked_ms。auth_revision仅授权/终态改变，不因心跳或report更新。gate_version是SQL事务并发栅门，不是新授权版本。device撤销后旧key/旧ID不能在原行恢复；重新登记新key/ID需recipient当前普通账号+fresh enrollment批准。issuer没有此权。

### EnrollPackDevice：复用真实ordinary Begin/Bind，一次性消费child

**不新增BeginEnrollment，不新增enrollment challenge表/假phase。** 设备在尚未登记、没有任何Pack parent时，使用当前recipient普通session走原ordinary native Purpose1：

1. dedicated新operation ID、fresh ephemeral Ed25519 key、nonce调用原BeginNativeConsumer；server真实随机challenge/session binding按原challenge行持久化，TTL<=60s，原parent/owner/预算不变。
2. 用fresh child key调用原BindNativeConsumer。Bind在原事务检查同一当前ordinary parent/recipient、challenge unused与原期限，验原Bind proof，原子置used=true并创建<=5min的普通native child。没有第二次消费/复活该challenge。
3. 取得此**真实新child**后才调用EnrollPackDevice；请求仍带当前recipient普通认证，另带redact的enrollment child token和下面两签。不能用Pack purpose2/3、另一session的child、已做Prepare/Grant的child或自报server nonce替代。

EnrollPackDeviceRequest新message字段：version1（值2）、device_id2、device_public_key3、operation_id4、enrollment_native_session_token5(redact)、native_session_binding6、child_signature7、device_signature8、client_nonce9、expected_recipient_id10、可选device_label11。child公钥/issued/expiry和真实parent ID从Store取，不信客户端；device key必须与fresh child key不同。请求体仍<=8KiB，所有秘密只设备当前操作内存，不进浏览器、argv/env/日志。

canonical enrollment body按顺序编码：`cognate/pack-enrollment/v2`、version2、规范origin、recipient、真实ordinary parent的既有OwnerSessionCredential 32字节摘要（沿原session token digest算法，不杜撰session_binding列）、server发行child session_binding、child operation/public-key SHA、child原issued/expiry、device ID/public key、完整可选label presence/value、client nonce。child_signature签加role=`child`的body，device_signature签加role=`device`的同body；两个签名均必需，server key来源/recipient/当前parent均准确，不能只签客户端nonce。freshness来自原server challenge→Bind产生的一次性child与随机session binding链，不把已used challenge当新的unused证明。

Enroll的**同一原Store事务**：验证当前ordinary session credential；以child token找purpose1+准确session parent+recipient+origin+operation，要求原TTL仍活、Pack parent全NULL且无preparation/grant；验证两PoP；计算`enrollment_child_binding_hash=SHA256(规范origin,recipient,child session_binding,child operation,key SHA)`；检查未消费、quota与device唯一，再原子创建device行和此唯一hash。行的authority/AAD也认证该hash；原identity/time gate在commit前再验。并发两个Enroll只一个可建，重放/换device key/换id对同child都冲突拒绝，无返回新权限。

这一次性消费的持久位置是**新S1 device记录的唯一enrollment_child_binding_hash**，不是把原Bind challenge清used或造一条session。该hash不是session token/FK/永久bearer；普通session/child过期或注销后device独立存活。tombstone保留防删行后同child重放；T4先清native child再清policy，因此恢复也不能重放。Enroll提交失包不自动重发，用当前recipient普通账号ListPackDevices按本次device ID显式核对；无行/不明必须重新审阅、fresh child，不用old proof补一次。

S0没有device行，不能Enroll；需要真实S1升级门。除了既定新policy表增加hash列/唯一索引，不改原ordinary Begin/Bind challenge TTL/phase/schema或single-use语义；两native表parent union变化仍只为后续Pack purpose，不是未登记device的引导依赖。

### ConsentAuthorityV1

包含header digest、device ID/key hash及原enrollment auth_revision、owner/recipient、state/grant_revision、expiry、可选share、Pack ID/双revision/整有序snapshot SHA，以及最多16项精确有序entry。entry：attachment ID/ordinal、asset ID/owner/revision、agent/kind/schema/target、真实NativeTargetDescriptor digest、canonical required-ref count/list SHA、owner批准bitmap/批准集合SHA。bitmap支持原完整域：MCP已知上限512 refs/附件，不截成1或128；超长名称不重复装入密文，引用摘要由真实typed资产重建核对。

owner授权的是当前明示范围。recipient实际请求refs必须等于真实renderer所需集合且属于owner批准集合，不能把“获准集合”当“实际必需集合”，不能静默去掉未批准server/header/env。

数据库state只有active/revoked。Superseded是当前pins/epoch与持久pins不等而派生的只读DTO状态，Expired/Unavailable也不新增持久枚举值；计算状态不能自动写回、更新grant_revision或换版本。

申请ticket<=4096，限5分钟，密封绑定origin、recipient、active device ID/auth_revision/key SHA、Pack/share、fresh nonce/申请ID/expiry；owner Issue原事务仍查当前device锚点/Pack/pins/share。ticket不是授权行、不随issuer session签发永久bearer，恢复后缺device行就拒。owner Review evidence/cursor绑定同一完整selected snapshot/ref digest/槽序和原期限，不混页。票据/审批只当前操作内存和受控私密交接，不放URL、日志、browser persistence；Issue receipt只含ID/revision/pins/expiry安全摘要，脱库无权。

### RecipientPolicyV1（server mirror）

consent ID/grant_revision、policy_revision、enabled=false默认、device/auth_revision、批准pins/slot/target descriptor摘要、本机local policy ID/revision、expected local head摘要、fresh engine plan的安全diff digest、device批准签名。仅设备可信UI批准/准确旧opt-in可创建enabled=true；recipient Web只能请求启用，不自行造签名。server disable为单调撤权，递增policy_revision并令enabled=false，不需设备先在线。owner既不能读此payload也不能改它。

AAD分域：`cognate/c7/device-authority/v1`、`.../consent-authority/v1`、`.../recipient-policy/v1`、`.../device-command/v1`、`.../device-report/v1`。每部分绑定对应role、完整不可变header和其自身revision/state，避免更新report导致owner grant被意外重新签授权；更新authority必须重seal，不能只改state裸列。原masterkey/权限/失钥拒绝合同不变。

本机仍用原client key/私有目录/安装锁和密文journal；device seed的独立HKDF info `cognate/pack-device-key/v2` 长度前缀绑定origin/recipient/device。本机LocalPackPolicyV1保存同意/pins/批准/expected head与enabled；PackJobV1引用原renderer操作/backup，不复制一套应用内核。无交互session bearer、无ephemeral private key或短grant持久化。key损坏/缺失不替换，临时seed尽力清理不冒称完整进程内存证明。

## 3. native真实parent union，不造session

对`native_consumer_challenges`和`native_consumer_sessions`：

- parent_session_id改SQL NULL可选；新增pack_policy_id可选正FK、pack_authority_revision可选正、pack_policy_revision可选正。
- purpose=`configuration-apply`：parent_session_id正，三个Pack列全部NULL，依旧真实parent session/原24h上限校验。
- purpose=`pack-configuration-apply`：parent_session_id为NULL，pack_policy_id指consent，authority_revision=grant_revision，policy_revision准确；跟随device锚点检查auth_revision。普通CRUD绝不接受。
- purpose=`pack-device-control`：parent_session_id为NULL，pack_policy_id指device，authority_revision=auth_revision，pack_policy_revision为NULL；仅允许本device通道/manifest安全读取，不能Prepare/Authorize/Consume。
- SQL CHECK用精确NULL/purpose组合，原事务再校验FK的row_kind。不得用0/负数session ID、nullable后默认跳过身份检查或通用“session missing就device fallback”。

purpose1/2/3在proto NativePurpose的新enum分配分别是原configuration_apply、拟议pack_configuration_apply、拟议pack_device_control；它与Target3/Gemini slot4是不同enum，不复用含义。challenge仍至多60s，child/prep仍至多5min，grant仍至多1min；device控制子会话也短期、自然到期可fresh Begin/Bind，服务端仍每次检查device未撤。原nonce/PoP/单次challenge/child quota/时钟检查保持，不能由于持久root而取消短期控制。

Begin/Bind的device分支要同时有device持久key和fresh ephemeral key证明，绑定当前purpose/parent ID/revision、origin/recipient、operation、nonce、child pubkey/期限；Begin带<=60s start validity，Bind再签服务器fresh challenge+完整start digest。只窃取child bearer而无对应PoP无权。新分支不产生OwnerSessionCredential，不走普通用户cookie/header fallback。自然到期换新child不是重放业务；认证提交不明暂停查询/显式新尝试，不循环制造身份撞quota。

所有native usage scope改成typed `{kind: owner-session|pack-policy, id:positive}`，不把两个表的同数值ID或负号混当ParentID；Pack root聚合+recipient/global原budget同时约束。

## 4. 公共Pack binding和prepared envelope

`PackConsumptionBindingV2`：version2；device policy ID/auth revision/key SHA；consent ID/grant revision/policy revision/authority SHA；recipient/owner；Pack ID/双rev/snapshot SHA；attachment ID/ordinal；asset ID/revision/owner；native target descriptor SHA；approved refs集合SHA；local policy/confirmation digest。缺项/重复字段不一致即失败，不能剥掉上下文走asset-only。

NativeTargetDescriptor：agent/kind/schema/target/scope=user/os/arch、该renderer真实profile/adapter/contract identity和已批准tuple。Codex必须完整fixed manifest/profile/template tuple；Claude/Gemini只用其真实合同，不杜撰全零摘要表示Available。未知agent/schema/未资格cell只读Unavailable。

`PackPreparedEnvelopeV2`包住**恰一**renderer原payload和可信TrustedPackSummaryV2（<=2048），summary由同一Prepare事务的policy+asset gate产生，不能客户端造或由Get生成。普通无Pack旧record decoder/hash字节不改。包裹后仍row charge<=16KiB，包括密文/AAD相关行开销；不只测plaintext。既有Codex最长编码+2048须用真实合法域实测；超限先拒，不能截断或缩旧domain。新表authority<=16KiB、policy<=8KiB；按16附件×原合法ref/ID上界做编码证明，未实现前不宣称容量已过。

零秘密Pack也发真正单次grant并terminal Consume；`consumption_required=true`不等同`materialization_required`。响应空typed材料ack仅此version2/Pack apply purpose允许，普通零槽旧合同保持。

## 5. additive proto：准确入口/号码

### 原 `proto/skillhub/configuration/native/v1/native.proto`

原六RPC不删不改型。已有Codex40、Gemini41/Prepare响应6全部归原branch；本设计不import未合Gemini符号。

| message | Pack拟议新字段 |
| --- | --- |
| BeginNativeConsumerRequest | 60 pack_device_start |
| BeginNativeConsumerResponse | 60 pack_parent_summary |
| BindNativeConsumerRequest | 60 pack_device_proof |
| BindNativeConsumerResponse | 60 pack_parent_summary |
| PrepareNativeApplyRequest | 60 pack_binding；61 pack_device_attestation；62 pack_contract_version=2 |
| PreparationBinding | 60 pack_binding；61 pack_target_descriptor；62 pack_required_references |
| PrepareNativeApplyResponse | 60 consumption_required |
| AuthorizeNativeMaterializationRequest | 60 pack_policy_approval |
| AuthorizeNativeMaterializationResponse | 60 consumption_required |
| ConsumeNativeMaterializationRequest / GetNativeMaterializationStatusRequest | 60 pack_binding_sha256 |
| ConsumeNativeMaterializationResponse | 60 pack_slot_values；Pack返回旧slot_values必须为空 |

新的presence有安全意义的scalar（包括Pack contract version/consumption_required）用proto optional，不能在Go中把显式0/false与缺席混为一谈。严格raw-wire旧入口还须在unmarshal前拒任何60+ Pack字段，即使编码值是0/空bytes；新optional不是允许旧validator忽略它。

不为ApplyBinding再塞一套Pack，已有preparation包含完整绑定。版本/authorization来源恰一，ordinary无Pack所有60+字段**缺席**（包括false/空集合不EmitUnpopulated污染旧wire）。Pack字段是正交上下文，不是第四renderer。

renderer互斥：Claude target1没有Codex/Gemini字段，仅原patch；Codex target2仅版本/Preparation/LocalBinding40及响应declaration5；Gemini target3仅版本/Preparation/LocalBinding41及响应declaration6。多个分支同时在场、显式零/不匹配target、未知version、protoJSON null/重复别名、binary重复singular拒绝；普通合同presence不变。

新messages自身tag从1开始：

- PackDeviceStartV2：version1、parent_policy_id2、authority_revision3、policy_revision4（control缺席）、device_public_key5、start_expires_ms6、device_signature7；原top字段的purpose/recipient/origin/operation/child pubkey/nonce全部纳入签名。
- PackDeviceProofV2：version1、device_signature2；绑定server challenge/start digest，非单签DeviceID。
- CredentialReferenceV1：version1，oneof provider_slot2/canonical_reference3；旧provider enum映射Claude1/2、Codex3、Gemini4不变。MCP引用只走typed ref。
- CredentialValueV1：reference1、plaintext2(redact)；完整集合/不多不少/重复拒绝。required_slots/requested_slots须准确等于typed集合的provider投影。
- PackPolicyApprovalV2：version1、binding_sha2、authority_sha3、requested_references4、approved_set_sha5、local_policy_id6、local_policy_revision7、local_binding_sha8、device_approval_signature9。explicit时6/7缺席，8绑定本次真实plan；auto时6/7/8全有并与外层AutoPolicy、durable policy一致。外层explicit/auto仍恰一。

### 原 `proto/skillhub/pack/configuration/v1/configuration.proto`

保留Get/Replace/Snapshot所有tag。新增owner普通账号methods：ReviewConfigurationConsent、IssueConfigurationConsent、ListIssuedConfigurationConsents、GetIssuedConfigurationConsent、RevokeConfigurationConsent、RevokeDeviceConfigurationConsents、RevokeConfigurationConsents（最后一项双CAS bump Pack epoch）。Issue返回安全receipt（ID/rev/pins/expiry）不是可脱库授权的证书；表行必须当前存在。Review128引用分页，64KiB旧预算，不静默总cap128。

### 新 `proto/skillhub/pack/device/v1/device.proto`

一个共同设备通道，不每target新服务。无native proto import/本机原文/secret/materialization message；生成Web TS安全DTO和Go Connect。

| 认证入口 | 精确methods |
| --- | --- |
| recipient当前普通账号 | EnrollPackDevice（另验device PoP）、ListPackDevices、RevokePackDevice、RequestPackConfigurationConsent、ListReceivedConfigurationConsents、GetReceivedConfigurationConsent、RequestDeviceCommand、GetDeviceCommand、ReadDeviceReportPage、DisablePackConfigurationPolicy |
| Pack-device-control child + device PoP | PollDeviceCommand、ReportDeviceCommandProgress、SubmitDeviceReportPage、AcknowledgeDeviceStop、SetPackConfigurationPolicyApproval、ReadDeviceConsentManifest |

Web管理method没有接受device bearer的fallback；device methods没有普通cookie替代device批准的fallback；F10/匿名/share永远无权。owner不是recipient时前一组设备读取/控制全部拒绝，不能因有Pack而加owner OR条件。native材料只在原Pack apply purpose六RPC消费，Web设备服务不转发它。

新messages的tag从1顺排，公用header明确version、device ID/auth revision、consent ID/revisions、command/report IDs/sequence/digests、expected control/mailbox revision、nonce/proof/期限；payload用typed oneof，绝不JSON塞profileId/old fields。proof/opaque申请ticket均debug_redact；error只固定安全码，不回原请求/正文。

预算：原Pack64KiB/native512KiB/bootstrap8KiB不扩大；新设备metadata/command16KiB、report page128KiB为独立procedure预算，max wait20s不改变body cap。先prebody认证/限流、再有界decode与完整domain验证；无chunk body积累或任意分页索引逃逸。

## 6. D5 command/report协议

### DeviceCommandV1（command ciphertext<=16KiB）

version、command_id16、device/auth revision、sequence正、requester=recipient、kind、requested_ms/expiry_ms、consent/pins/policy revision、expected local head/review ID/redacted diff digest、new operation ID、request digest、state/ack。kind仅Review/RequestApply/RequestEnable/DisableStop/RequestRestore；restore是原backup/operation逻辑ID，不接受文件路径/argv/网络URL。每device仅一个未完成command；另有stop_generation可单调抢先，不覆盖原结果。

Web Request不是grant/秘密批准。device收到后先durable Received/Attempted，再在原共享锁下验证local policy/真实基线并调用原engine。缺准确opt-in则NeedsDeviceApproval；Restore总新计划/设备确认。web/server的command Accepted/Delivered不能显示Applied。所有Submit/ack用sequence+digest+CAS，失包先查现状，不重发执行。

### DeviceReportHeaderV1 / DeviceReportPageV1（当前一页<=128KiB）

header：device/auth revision、report ID、command ID/sequence、report revision、consent/pins/policy/operation、renderer tuple、logical target roles、reported_at/原expires、phase、conflict/error安全枚举、完整typed change count/redacted digest。不含任意device路径/真实username/旧内容摘要/旧secret长度或hash。

Web可见的baseline只用设备生成的随机review handle/engine keyed commitment，不返回auth.json等原文件SHA、原native local-binding原文或server mirror中的本机细节。真实baseline hash仍在原设备引擎/受限native链内部按原合同校验，Web不能拿报告handle造审批；owner grant DTO也不暴露recipient policy ciphertext解密结果。

page：header SHA、page ordinal、首条change ordinal、entry count、chunk offset/length、previous/next digest与完整digest。只存当前页，recipient读页必须显式提交相同report的上一页ack；device下一次poll再送下一页。所有合法条目经128分页，长非秘密value可分块；页大小不是总数上限。Web完整收齐并核digest才显示完整review；少页/变更/期限到达只读不批准。

脱敏从engine实际before/after/冲突生成，codec白名单只投影已知非秘密值；credential仅存在/Keep/Replace/Clear。未知字段、raw TOML/JSON/env/备份、shell arguments、secret hash一律不序列化；不先Marshal整plan再靠regex删secret。无相应safe projection的renderer不可假报完整diff，必须补该cell资格。

device保持本机密文report snapshot，重复读取同页不执行应用。server重启保留command/header/当前page；已完成页历史可由device只读重送。设备离线或快照丢失显示ReportedDetailUnavailable/NeedsFreshReview，不重建旧Apply。报告签名认证key来源，**不是磁盘远程attestation**。

Unknown命令/本地job未解决前不接受下一条自动变更绕过原target；显式查询和真实stop/join后，须原engine允许的新计划/新operation/新设备审批。恢复自己的旧受管record不要求已被撤销的issuer grant仍有效：device在本机验证该record确属同origin/recipient/device的原操作，不能用已删除consent ID推导任意路径权利。

控制状态：Queued→Delivered→NeedsDeviceApproval/ReviewReady→Applying→ReportedApplied/ReportedConflict/ReportedPartial/ReportedUnknown；StopRequested与StoppedReported分开。UI加Offline/Stale/Expired，不用单一绿点混淆。issuer grant-revoked仍可让recipient用活device control child读/恢复自己的历史，但不能新Apply；recipient device-revoked则所有device child/control立即失权。已Consume材料不可追回边界保持。

## 7. Canonical域与事务证据

不用proto序列化/JSON当canonical。使用既有big-endian uint32/uint64、length-prefixed bytes/UTF-8、显式presence、数组count和规范顺序；未知/重复/溢出/非规范origin/ID拒绝，不先修复。SHA输入不包含签名自身，绝不hash旧secret。domains：

| 域 | 绑定的authority |
| --- | --- |
| `cognate/pack-enrollment/v2` | 当前recipient普通认证、device key、origin、fresh challenge、请求revision/nonce/expiry |
| `cognate/pack-native-session/v2` | begin/bind stage、Pack apply/control purpose、真实parent union及revision、device key、child key、op/nonce/challenge/原expires |
| `cognate/pack-target/v2` / `cognate/pack-binding/v2` | 完整真实target descriptor / 上述全部Pack pins与authority摘要 |
| `cognate/native-pack-apply/v2` | prepare/authorize/consume/status stage、renderer恰一、consumer/child/parent、binding/authority SHA、原nonce/challenge/TTL、target declaration/local binding、授权方式/intent/refs |
| `cognate/native-pack-preparation/v2` / `cognate/native-pack-apply-binding/v2` | 可信prepared完整字段/Pack上下文 / 同一prepared SHA加真实local intent、policy、expiry |
| `cognate/pack-policy-approval/v2` | 精确policy/consent/grant/device代、pins/refs/tuple、expected local head、真实plan/审批、mode/expiry |
| `cognate/pack-device-command/v2` / `cognate/pack-device-report/v2` | command identity/sequence/CAS/payload / report header+page/full digest、recipient/device、命令绑定 |

target-specific纯payload/declaration/local-binding规范编码仍复用Claude/Codex/Gemini原合同，不把consumer伪装成asset owner来调用旧proof。新opaque Pack VerifiedProof只能经对应verifier产生；device-control证据不能转apply，Pack apply证据不能转OwnerSessionCredential。普通Claude/Codex/Gemini无Packcanonical/golden bytes与现有C6 tuple全部保留。

### 公共旧proof API必须拒绝新增已知Pack字段

`internal/configclient/native_proof.go`的knownTree仅“字段已知/UTF-8/预算”不足以证明字段属于该合同。新增显式legacy contract allowlist，递归拒绝Pack60+/Pack目的/两parent变体，覆盖knownMessage、validBegin/Challenge/Prepare/Preparation/Apply/Authorize/Consume/Status、strictProofWire、StrictUnmarshalNativeProof及validDecodedProof。不得把旧函数改成自动识别Pack；Pack用独立入口/新域/typed parent。

公共旧Claude入口BindTranscript、PrepareTranscript、AuthorizeTranscript、ConsumeTranscript、StatusTranscript、HashPreparation、HashApply、ValidatePreparedPatch，以及CodexPrepare/Authorize/Consume/StatusTranscript、HashCodexPreparation/Intent/Apply、ValidateCodexPreparedDeclaration（定义在native_codex_proof.go）都必须先legacy-no-Pack gate；纯patch/declaration/local-binding helper只有其自身原字段域，没有“因为是纯函数就忽略外层不合法字段”的宽松投影。已批准Gemini对应公开API逐一纳入同一矩阵，不借其WIP符号。

负控对每个新已知Pack字段/parent mode注入旧合法输入：显式非空/empty message、optional0/false、nested preparation/apply、混40/41、binary singular重复/未知/空tag；每个public builder/hash/validator/strict decoder必须实际拒绝，destination不部分更新，原合法旧known-answer bytes逐字不变。Sign/Verify只处理成功构建的opaque Transcript，不能从混域raw字段重建。共享库层关闭后再验证HTTP映射，不允许仅HTTP/Pack入口兜底。

三阶段原事务检查device/consent每项权利和current资产行；idempotent Prepare不能早return漏验；完整ID/foreign/成员/ordinal域在after=0/128分页之前证明，category/无关合法域不误拒。rows.Close/cleanup不明优先于普通取消，任何失败零可执行证据。细节及schema域见配套恢复文件。
