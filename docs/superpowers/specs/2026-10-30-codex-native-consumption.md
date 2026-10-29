# CX-NATIVE-GRANT/C2：Codex provider 原生消费合同

日期：2026-10-30。状态：**待非作者设计审的单规格，无 plan/source/proto/依赖授权，未实现本片**。C2 指 Codex capability map 的原生授权消费，不是 Claude/Linux CLI C1。本文冻结未来完整实现和验收合同，不把合成向量、codec 或持钥证明标成可安装能力。

## 1. 权威、目标与现有缺口

完整需求仍为 `docs/REQUIREMENTS.md` F12 及十四类 agent 矩阵；首个 Codex provider 纵切仍需 A+B+资产 C1+C2+D。官方 ChatGPT/OAuth、MCP、prompt、账号管理、自动应用和其它 agent 不借本片缩减或变成 provider-key fallback。

| 固定输入 | 身份 / 本片用途 |
| --- | --- |
| Codex map | `2026-10-29-codex-capability-map.md`，SHA256 `d6691bd0da4a3e9907ad4f51370d406826af2025af03359c623c74ca018992bd` |
| A spec / neutral 类型 | `2026-10-29-codex-provider-planner.md` SHA256 `a210da792d9487200fa3616e6d7abb58ef7ec81f139af17db646595f9e1d981e`；`internal/agentconfig/codex.go` SHA256 `7a4bdd46d339500ab834276b2fe7742c2b4ddf9ad80bafd66cfb000c75379e78`，A 交付 `c7d8ab60` |
| B spec / plan | `2026-10-29-codex-local-codec.md` SHA256 `000b17f750a0e258e81d1a96dbf34100f761ff227a6be1133b90c22bf5fbfcb7`；`2026-10-30-codex-local-codec.md` plan SHA256 `0952f57d3a2d1a2e7dcc0a1ec27d0dde082600aec4708eddc5d00666de1f9229` |
| 既有 native 合同 | `2026-10-29-native-consumer-protocol.md`、`2026-10-29-native-configuration-store.md`、`2026-10-29-native-configuration-transport.md` plans；以现有六 RPC、真实 MAC/Ed25519、四表事务与 transport 为接缝，不复活被拒签旧稿 |
| 旧客户端 proof golden | `internal/configclient/testdata/native-proof-v1.json` SHA256 `1bcece6b35dad4e58b1be2b644b963bb096a34f7c6f1d5ffe271531558fdba74`，包括旧 bind/prepare/authorize/authorize-auto/consume/status 及 preparation/apply/patch hashes，**逐字不改** |

只读事实基线为 `de0c1d0e869a6f6aa717bd9811fca0014d9c84f2`。当前 native schema、proof/pin、`NativePreparedData` 的七操作数组及客户端 `NativePreparation.Patch` 均为 Claude-only；现有 `Codex` typed 资产主动拒绝 native 消费。不能仅给枚举添 Codex 就转调 Claude 分支。

B exact14 正在独立 lane 实施；本文只消费已签 B API/Binding 合同，不读移动实现、不修改 B/spec000b17/A。B 的 NativeProfile、D 的真实 effective-scope/ownership/auth/witness 与发布仍分别待独立签交。本片可定义并验收协议和 qualified synthetic 接缝，但不能提前签完整可用 Codex flow。若 B 最终只改 accessor 拼写，可机械适配；若改变绑定语义须先复审，不在 C2 偷改 B。

## 2. 唯一执行次序与信任边界

```text
已有 owner session → Begin/Bind → Prepare 完整 A 声明
→ D-preflight 可信观察 → B.CheckProvider 得到唯一 opaque plan
→ 读取同 plan.Binding / 本次明确逐槽及最终批准 → 完整 intent
→ Authorize → Consume 至多一次 → B.RenderProvider(同 plan, 精确材料)
→ D 重新验证 root/所有 baseline/ownership/witness/selection → publish
```

- Begin/Bind 沿用现有 owner MAC、精确父 session、operation/public key/challenge 绑定；其 purpose 仍为 configuration-apply。普通 owner、MCP、Pack、其它 native 子会话不能代替消费 bearer。四个后续 RPC 的 proof 必须针对同 native session、operation 和精确 preparation。
- **D-preflight 必须在 Authorize/Consume 之前完成**，不需要新 provider secret。D 验真实受控 root、链接/权限/对象身份、有效配置来源与覆盖、auth-store/登录观察和 journal ownership；B 在此基础上先 clone、检查全部三 role 和输出可构造性。unknown、foreign 冲突、无已签 NativeProfile 或模板、unsupported representation 均零 plan、零 Authorize/Consume。先消费再补判定是违规，不是降级路径。
- B 的 `NewResolution(..., witness [32]byte)` 和 Binding 摘要只是纯值载体。**caller 造出 32B、完整 JSON 或签名均不证明 OS 事实、D 已执行或人类点击。** 服务端鉴权仍来自既有 MAC/Ed25519、父/子 session、owner/资产/CAS/事务。服务器只校验并绑定本地声明，不认证 keyring/root 事实。proof 不是官方客户端 attestation。
- 未来生产本地编排必须从已签 D 适配器取得有私有来源/生命周期的 preflight 结果及原 B plan；不得提供把任意 digest、path map 或 caller bool 转成“可信 D 已完成”的公共构造入口。没有该适配器或已签 NativeProfile 时，生产桥拒绝进入 Authorize，不能靠测试 profile/环境开关放行。C2 不实现该 OS 适配器、NativeProfile 探证、keyring、文件读取/写入、journal 或发布锁。
- D 发布前在其真正发布资格内重查同一批 root/目标存在性与 hash、对象身份、auth/effective-scope witness、ownership 和 selection。变化即 Conflict/NeedsAttention，丢弃候选；不得重解成另一个 bool、换 plan/base、auto-rebase、再 Consume 或无新预览覆盖。认证到 driver COMMIT、远端收包到本地发布不是跨机器原子操作；Consume 不等于 Applied。

## 3. 完整声明、Binding 与单槽材料

### 3.1 精确 provider 分支

仅 `agent=codex / kind=provider / schema=1 / target=codex.config / scope=user`，固定 `builtin.codex@1.1.0`、API1、唯一 capability `codex.provider-plan.v1`。当前 definition digest 为 **`f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4`**，按既有 Registry 对含 manifest+module 的完整 deterministic definition 计算；不是 manifest-only hash。未来 client pin 必须独立常量化并与真实 Registry/A 输出做双向 parity，不在客户端加载 JS/server 包，也不允许 caller 选择任意 plugin/version/digest。

服务端从同 owner、同 asset envelope revision 读取 typed Codex provider 和 key presence，完整调用既有 A `PlanCodexProvider`。仅 presence 进 JS，真实 key、旧 env/文件、OAuth、本地观察和 owner 不进插件。保存的 key 缺失、复制后无 key、未来 extras 或不支持的 kind/schema 都在鉴权之后返回固定 precondition，不能冒充官方账号/native-login 或零槽成功。

A 的每个字段必须存在于声明/绑定中：`APIVersion/Target/ProviderID/WireAPI/Endpoint/Model/ReasoningEffort?/Catalog?/CredentialReference/RequiredSlots/LocalAuthPolicy`。catalog 逐行绑定 `Model/DisplayName?/ContextWindow?/SupportsParallelToolCalls?/InputModalities?/BaseInstructions?/ReasoningLevels?/DefaultReasoningLevel?`。模型、modalities、levels、managed-model 列表都按原序，不排序；optional false 与 absent 不同，none 与 absent 不同，CR/LF/TAB 原字节不 trim。缺字段、空列表/null、未知值、同形错值不能用模板、JSON 默认值或客户端旧配置补齐。A 的全量语义及 48/32/64KiB 界保持不变；endpoint 不做 DNS/网络探测。

### 3.2 只消费 B 的既有 Binding

Check/Render 接口、`ProviderPlan.Binding()`、opaque clone/Destroy 均沿 B plan §2，不另造第二套 codec。C2 从受信 copy accessors 取完整快照，先比对其 A 声明与本次 prepared declaration 完全一致，再形成以下投影；不能只带一个“B 已成功”的 bool/hash：

| 成员 | 规则 |
| --- | --- |
| C0 profile ID / selection revision | 属于 preparation；与下面的 NativeProfileID **不是同一个 ID**。其真实本地选择/计数由 D/已签核心提供，不能把输入数字当 current-selection 授权快照 |
| NativeProfileID / profile SHA256 / template SHA256 | ID 精确匹配 B 编译内已签 profile，digest 绑定该 profile 身份及精确模板；服务器不把收到的 ID 当 OS 认证。禁止 caller 模板/版本范围/下载补模板 |
| effective scope | 只接受 CompleteEffective；Unknown 无法形成可授权 plan |
| provider/catalog ownership | 完整 Absent/Managed/Foreign/Unknown 值及已有 managed-model ID **有序**列表；写入位置须符合 B 的归属规则，不以同名表/sidecar/模型推导 managed |
| policy 原值 + 解析值 | 原值精确 `preserve-local-login-v1`；显式 `requiresOpenAIAuth` bool，加 AuthStore/KeyringState/FileLoginState 分类，沿 B 表独立复核，不能凭新 key 或 auth 文件存在推出 bool |
| observation witness | 非零 SHA25632B，绑定 D 的完整有效范围/来源/ownership/auth 观察；只传摘要，不传 OAuth/JWT/account/本地路径。原始证据格式、取证与 fresh 比较属于 D，不由 C2 自造 |
| 三 role baseline | 固定顺序 config、auth、catalog，每个精确 Missing/Present/NotObserved，Present 的原始文件 SHA256；不上传 bytes、大小、identity inode 或原文。D 另持真实身份/锁证据 |

来源严格区分：Binding 已提供 NativeProfileID 和 template digest；`profileSHA256` 是独立 NativeProfile 签交时冻结的**原始证据 manifest bytes** 的 SHA256，由 D 持有的同一正式身份回执补入，须与 Binding 的 ID/模板 digest 一致。**不要求 B 增加 accessor 或修改其 Binding**，也不从 profileID 字符串猜一个 hash、在 C2 自造 profile/模板/可变登记表。没有正式身份回执时生产桥 unsupported；§10 的合成 profile hash 只是 canonical 参数，不伪造该回执。服务器仍只将它绑定为客户端声明，不把收到的 32B 当作本机事实认证。

config 必须已观察，可 Missing 或 Present；Present 空 TOML 的 SHA256 与 Missing 不同。auth/catalog 只有 B 合同允许本次不读时才 NotObserved，读失败不得伪装 Missing/NotObserved。catalog 声明存在时不得 NotObserved；auth 为 file 登录判定所必需时必须已观察；keyring/auto Found 分支不为此额外读取无关 auth。NotObserved 不等于 Missing，D 仍必须核验原有效来源范围，不能扩大成忽略新增覆盖。

policy 分类编码固定：AuthStore `File=1,Keyring=2,Auto=3,Ephemeral=4,Unknown=0`；Keyring `FoundCredentialLogin=1,Missing=2,Unknown=3,NotObserved=4`；FileLogin `CredentialLogin=1,NoCredentialLogin=2,Unknown=3,NotObserved=4`。file 用已确定 fileLogin；keyring Found/Missing 分别 true/false；auto 仅 keyring 明确 Missing 才看 file，Found 为 true，Unknown 绝不 fallback；Ephemeral/Unknown 拒绝。需要的观察为 Unknown 或与 baseline/effective store 矛盾也拒绝。此编码映射 B 的语义名，不依赖 B Go enum 的底层数值。

唯一 requested/required/returned slot 均为 **`codex.provider-key`**；没有第二槽、零槽 Codex 或官方登录材料。值为 1..8192 UTF-8 bytes，无 NUL/CR/LF、不 trim。Consume 校验 binding、grant/operation、时限和精确一个槽之后，材料只交原 plan 的 Render；Render 无替代 base/declaration/policy/profile 参数。缺/重/多槽、别的 secret ref、响应已成功但 binding 不符均失败并清理自有缓冲，不从旧 auth/env/catalog 补值。Render 失败没有部分 candidate；auth 只能 Keep，D 的发布/恢复不得被 C2 返回值冒充。

客户端新增窄 Codex flow，不把既有 Claude `NativePreparation.Patch` 或 `NativeApproval.Slots` 改成 any/兼容影子。签交目标为复用 NativeClient 的 bootstrap/transport/lifetime，提供 Codex Prepare（既有 NativeSelection 的 asset/revision/C0 profile/selection 字段）、Authorize（原 B ProviderBinding 的完整副本、明确 confirmation revision 与精确槽）、Consume（仅内部已授权 handle）、Status、Close。C2 自算 HI，不接受 caller-only32B；返回准备/授权/材料均 opaque、只读 clone。B Binding 只是上述完整输入，不被 API 名字升级成 D 认证凭证；生产编排的可信 preflight gate 仍须由 D 独立实现并实测。

## 4. Native schema 的兼容扩展合同

仍是现有六 RPC、相同 Connect service/URL、protobuf-only native 包。**不把旧字段改 oneof、不换旧类型、不复用任何已用或 reserved tag**；特别保留 PreparationBinding reserved5/30..39 与 PrepareRequest reserved12/15，旧 patch_sha25621 不改称 declaration hash。

| 位置 | 唯一拟新增值/字段 |
| --- | --- |
| Target enum | `TARGET_CODEX_CONFIG=2`，原 Claude=1 不变 |
| SecretSlot enum | `SECRET_SLOT_CODEX_PROVIDER_KEY=3`，原 1/2 不变；EnvSlot 不增 Codex 假 env |
| PrepareNativeApplyRequest | tag40 `uint32 codex_contract_version`，Codex 必须为 1 且实际出现；Claude 不得出现，即使零值 |
| PreparationBinding | tag40 `CodexPreparationV1 codex`；其 tag1 contract_version=1、tag2 declaration_sha25632B。Codex 必须有它且无 tag21；Claude 必须原 tag21 且无 tag40 |
| PrepareNativeApplyResponse | tag5 `CodexDeclarationV1 codex_declaration`；Codex 无旧 patch2、materialization_required 必须 true；Claude 无 tag5。两 payload 同时有或同时缺拒绝 |
| AuthorizeNativeMaterializationRequest | tag40 `CodexLocalBindingV1 codex_local_binding`，Codex 必填，Claude 禁止；intent_commitment3 仍为真正完整 intent 的 SHA256，不借旧 32B 字段代替完整投影 |
| ApplyBinding | tag40 `CodexLocalBindingV1 codex_local_binding`；Codex 必须与已授权投影完全相等，Claude 禁止。Preparation/commitment/expiry 原 1/2/3 不改义 |

新 `CodexDeclarationV1` 字段/tag 固定：`api_version1,target2,provider_id3,wire_api4,endpoint5,model6,optional reasoning_effort7,catalog8,credential_reference9,repeated required_slots10,local_auth_policy11`。api_version 为 uint32，其余普通文本为 string，required_slots 为新增后的 native SecretSlot（必须仅 3）；reasoning 使用已有公共 CodexReasoningEffort enum，catalog 复用公共 `CodexCatalog` 的完整 typed shape；只映射，不修改 public proto/TS。枚举映射回 A 的原字符串后才 canonical。null 不存在于该 native 协议，JSON 入口始终拒绝。

新 `CodexLocalBindingV1` tag 固定：`binding_version1,native_profile_id2,native_profile_sha2563,template_sha2564,effective_scope5,provider_ownership6,catalog_ownership7,repeated managed_models8,auth_store9,keyring_state10,file_login_state11,local_auth_policy12,optional bool requires_openai_auth13,observation_witness14,repeated baselines15`。version 为 uint32 且=1，ID/policy 为 string、hash/witness 为 bytes、managed_models 为 repeated string、其余分类为本文固定值的 enum；scope `CompleteEffective=1/Unknown=0`；ownership `Absent=1/Managed=2/Foreign=3/Unknown=4`。ID 最多 128B，hash32B，managedModels≤32 且 ID 沿 A model grammar、唯一有序；超出拒绝，不截断 B Binding。

`CodexRoleBaseline` 为 `role1` 加 **oneof** `missing2 / present3 / not_observed4`；role `Config=1,Auth=2,Catalog=3`，Missing/NotObserved 是不得含任何成员的空消息，Present 仅有 tag1 `sha256` 且必须 32B。必须恰好三项、按 1/2/3 原序，不接受重复/漏项或一个 slot 带两个状态。present(false)/额外空/null 不能借 Go omitzero/解码最后值胜出掩盖真实 member presence。

新增类型不接 raw JSON、任意 path、schema/template bytes、OAuth 或本地 image。既有 AutoPolicy 消息保留以维持 Claude 向量；**Codex v1 仅 ExplicitConfirmation**，revision>0 且精确槽批准，auto 分支拒绝而不偷偷忽略。完整自动应用仍留 F12 后续，不给本片新自动策略。

Consume/Status 请求不需新 caller target hint：服务器从已认证、精确 prepare/native/operation 的持久记录选择 Codex 分支，不能先猜 Claude 再试到成功。Consume response 的 ApplyBinding 与 slot3 必须完整匹配，Status 仍只有安全 state/ID/time，不回投影、声明、hash、patch、key/token。所有新增 Go 生成物由正常 Buf 生成；Web template 继续排除 `proto/skillhub/configuration/native` 整目录，不手改 generator/生成物，不新增 Web native reveal。

## 5. 字节级 canonical 合同

### 5.1 基元与旧向量隔离

`U32/U64` 为无符号固定宽度 big-endian；时间为正 Unix milliseconds，拒绝负数/溢出，revision 为正且 asset revision≤MaxInt64。`L(x)=U32(byteLen(x)) || x`，`T(s)=L(UTF8(s))`；hash 固定原始 32B 经 L，绝不编码 hex ASCII。`B(v)=U32(0|1)`。`O(f)`为 `U32(0)`，或 `U32(1)||encode(f)`；list 为 `U32(count)||各元素编码`。空/缺省不靠零值猜测，不 Unicode normalize、不 trim、不排序。累计尺寸先检查再分配。

**Bind 继续原 v1 prefix/算法**，同样输入产生逐字相同 transcript/signature。Codex 后四用途用新 prefix `Q(p)=T("cognate/native-codex-proof/v1")||T(p)||U32(1)`，p 仅 prepare/authorize/consume/status。新 hash 域如下，不向旧 Claude canonical 追加字节；Claude 所有旧函数/测试向量保持原义和 bytes。

```text
A = T("cognate/native-codex-declaration/v1") || U32(1) || U32(APIVersion)
    || T(Target) || T(ProviderID) || T(WireAPI) || T(Endpoint) || T(Model)
    || O(T(ReasoningEffort)) || O(Catalog)
    || T(CredentialReference) || List(T(RequiredSlots)) || T(LocalAuthPolicy)
Catalog = List(ModelEntry)
ModelEntry = T(Model) || O(T(DisplayName)) || O(U64(ContextWindow))
    || O(B(SupportsParallelToolCalls)) || O(List(T(InputModalities)))
    || O(T(BaseInstructions)) || O(List(T(ReasoningLevels)))
    || O(T(DefaultReasoningLevel))
HD = SHA256(A)
```

catalog 外层 O 只在真实存在时编码其非空 models；各 optional 的次序是上式，不是 JSON/map/protobuf 序列化顺序。全 A 的 bounds/合法组合先校验；未知成员/enum/缺必填不能先 hash 后放行。

`AD = U32(adapter.apiVersion)||T(pluginID)||T(pluginVersion)||T(definitionDigest)||List(T(requiredCapabilities))`，digest 是既有 64 位小写 hex **字符串**，与上文原始 hash32B 区别明确。requiredCapabilities 精确单项。`S=U32(1)||U32(3)` 是 native slot 列表，不是逻辑字符串 RequiredSlots 的编码。

```text
P = T(owner) || T(origin) || L(nativeSessionBinding) || L(operationID)
    || T(assetID) || U64(assetRevision) || T("codex") || U32(kind=1)
    || U32(schema=1) || AD || U32(target=2) || U32(scope=1)
    || T(C0ProfileID) || U64(selectionRevision) || S
    || U64(prepareExpiresMS) || L(consumerKeySHA256)
    || T(prepareID) || U64(preparedMS) || U32(codexContractVersion=1) || L(HD)
HP = SHA256(T("cognate/native-codex-preparation/v1") || P)

BP = T("cognate/native-codex-local-binding/v1") || U32(bindingVersion=1) || L(HD)
    || T(NativeProfileID) || L(profileSHA256) || L(templateSHA256)
    || U32(effectiveScope) || U32(providerOwnership) || U32(catalogOwnership)
    || List(T(managedModels))
    || U32(authStore) || U32(keyringState) || U32(fileLoginState)
    || T(localAuthPolicy) || B(requiresOpenAIAuth) || L(observationWitness)
    || U32(3) || Role(1) || Role(2) || Role(3)
Role(n) = U32(n) || U32(presence) || L(rawSHA256 if Present else empty)
presence: Missing=1, Present=2, NotObserved=3
HB = SHA256(BP)

I = T("cognate/native-codex-intent/v1") || U32(1)
    || L(HP) || L(HD) || L(HB) || U32(consentKind=1)
    || U64(confirmationRevision) || S || U64(intentExpiresMS)
HI = SHA256(I)
HA = SHA256(T("cognate/native-codex-apply/v1") || L(HP) || L(HI) || U64(intentExpiresMS))
```

完整绑定采用分域 hash 组合：HP 已覆盖 owner/origin/父衍生 session/operation/consumer key/asset revision/adapter pin/C0 selection/期限，HD 覆盖**每一个 A 值、presence、列表顺序**，HB 覆盖 B 全投影（含同一个 HD），HI 再覆盖明确批准及有效期。不是省掉 catalog/基线/政策后只签一个 caller commitment。服务器必须独立重算 HD/BP/HB/HI 并逐项比较请求，客户端必须从原 opaque plan 的副本重算，不提供生产 `Authorize([32]byte)` 快捷入口。

### 5.2 四个 Codex proof transcript

```text
bind = T("cognate/native-config-proof/v1") || T("bind") || U32(1)
    || T(origin) || T(owner) || U32(nativePurpose=1) || L(operationID)
    || L(consumerPublicKey) || L(beginClientNonce) || T(challengeID)
    || L(bindChallenge) || L(nativeSessionBinding)
    || U64(challengeExpiresMS) || U64(nativeSessionExpiresMS)
prepare = Q("prepare") || T(origin) || T(owner) || L(nativeSessionBinding)
    || L(operationID) || L(consumerKeySHA256)
    || T(expectedOwnerID) || T(assetID) || U64(assetRevision) || T("codex")
    || U32(schema=1) || AD || U32(target=2) || U32(scope=1)
    || T(C0ProfileID) || U64(selectionRevision) || L(operationID)
    || L(clientNonce) || U32(codexContractVersion=1)
authorize = Q("authorize") || L(HP) || L(authorizeChallenge) || T(prepareID)
    || L(operationID) || L(HI) || S || U32(consentKind=1)
    || U64(confirmationRevision) || L(clientNonce) || U64(intentExpiresMS) || L(HB)
consume = Q("consume") || L(HA) || L(authorizeChallenge) || T(grantID)
    || L(SHA256(grantToken)) || L(consumeChallenge) || U64(grantExpiresMS)
    || L(operationID) || L(HI) || L(clientNonce)
status = Q("status") || L(HP) || L(authorizeChallenge) || T(prepareID)
    || T(grantID_or_empty) || L(operationID) || L(clientNonce)
```

Ed25519 **直接签上述 transcript bytes**，不先 SHA256 后签、不用 Ed25519ph；key32/public32、signature64，nonce/challenge/session binding32、operation16。正常 key/operation/challenge/nonce 沿用既有 crypto/rand 生成，重复字节与固定 seed 仅供合成向量。沿用现有客户端检查 private seed 与附带 public half 一致的规则。grantToken32 只在 consume transcript 中以 SHA256 参与。内部已验证 stage evidence 为 `SHA256(T("cognate/native-codex-stage-evidence/v1")||L(transcript))`，仍封装在现有 verifier 的私有 verified capability，不能由 request 的 digest 构造已认证快照。

### 5.3 双方独立性

服务器 nativesessionauth 与客户端 configclient 各自实现 encoder/验证，不互相 import 或用同一生产 encoder 生成期望值；共享 neutral A 类型不等于共用授权逻辑。§10 的独立输入、长度、SHA、签名为双方同一 oracle；未来源码把重构出的完整 transcript hex 冻结到 testdata，两端都逐 byte 对比并用真实 Go Ed25519/OpenSSL 交叉验证。仅比较两实现互相相等不够。

## 6. 服务端事务、额度与未知结果

Prepare/Authorize/Consume 均先走既有权威 preflight：验证 owner/native MAC、精确父行、purpose/session/operation/签名（Consume 还需原 grantToken hash）后，才读取/解密资产或跑 A。无效 proof/token 在资产更新、缺失、其它 owner 和私有内容下不得产生可区分的 asset/plugin oracle；原 TASK2C-01 根修不能退化。

Prepare 固定当前 encrypted envelope 的单一 CAS revision、HD、requiredSlot3 与 pin。JS 在写事务外运行，最终写事务内重读同版 metadata/payload、父/子资格和声明输入快照，比较独立期望再插入。Authorize 同样先认证快照，再在事务外重算 A；写事务内重新验证 session/asset revision/HD/pin、完整投影形状/policy 一致性、HI、确认与 expiry，并将 preparation 绑定与 grant 插入作为一个事务后态计费。Consume 校验同一个已存 projection/HI/HA、当前资产版本及原 slot3，在同一写事务把 grant 标 consumed；只有成功 COMMIT、返回前 ctx fresh gate 后才输出一次精确 key。

### 6.1 不用大 catalog 冲破既有 16KiB 行限额

现有 preparation plaintext/charge 均≤16KiB，四表预算不能为新 catalog 暗中放宽。**不持久化完整 A 声明/目录，也不保存本地 image**：A 从同版资产+immutable pin 重算，完整值由 HD 绑定。Codex record 使用下列严格 compact binary 内文；旧 Claude JSON record/旧向量不变，二者解密后分支，无不可信反序列化或 decoder 猜测 fallback：

```text
CodexRecord = ASCII("CX2\0") || U32(1) || L(P) || L(PrepareNonce) || U32(hasBinding)
    || [ L(BP) || L(HI) || U64(intentExpiresMS) || U64(confirmationRevision) ]
       （仅 hasBinding=1；否则这整个尾部不存在）
```

保留既有 configuration AEAD 与原 preparation row AAD（owner/origin/phase/session/operation），先认证解密再看固定内文 prefix。旧合法 JSON 内文不可能以 CX2/NUL 起始；未知版本、坏前缀、尾随字节、重复/不一致或 hash/row 身份不符固定 Unavailable，绝不把 Codex 解释为 Claude。`patch-ready` 必须无绑定，`bound-with-grant` 必须有绑定；过期/retired 保留真实已绑定与否。C2 不生成 bound-no-secret。P/BP 必须与原 record 字段/phase/charge 和外部行一致；退出/回滚清理自有解密副本。

`PrepareNonce` 是**原已验签 Prepare 请求同一输入快照**的 clientNonce 原始 32B，独立以 `U32(32)||32B` 保存在上述 AEAD 认证内文，位于 P 后、hasBinding 前；不得用 Begin/Authorize nonce、AEAD 随机 nonce、零补位或外部 row.request_digest 代替。授权绑定、过期/retired 转换及重启后均保留原值，不重新生成。解析前检查剩余 bytes 和长度，必须恰好 32B；缺省、0/31/33B、截断、重复尾部或旧无 nonce 草稿布局一律固定 Unavailable，不猜版本/fallback，不修改既有 Claude JSON record。

插入时将原已验证 Prepare stage digest 写入 row.request_digest。**每次 load（包括重复 Prepare 路径与重启重读）先认证解密、核 P 与实际 consumer/row 身份，再用 P 全部原请求字段加认证内文 PrepareNonce 重建§5.2 原 prepare transcript**：expectedOwnerID 必须等于 P.owner，consumer/request 两处 operation 均取 P.operationID，consumer key hash 必须等于当前已认证 child 公钥的 SHA256，schema/adapter/selection/contract 等值全部取已核 P，不能从本次新请求补字段。按原 `SHA256(T("cognate/native-codex-stage-evidence/v1")||L(transcript))` 重算，并与外部 row.request_digest 精确 32B 常量时间比较；不相等固定 Unavailable、零结果，不跳过校验、不信任/回写修复外部 digest，不改其为 HP/HD 或 ciphertext hash。P、HP、五用途 transcript/签名及旧 Claude 向量因此不增加任何字节。

原行完整性通过后，重复 Prepare 才比较本次独立验签的请求 digest：同 nonce、同全部请求字段沿现有幂等语义返回同 preparation 的原既定结果；同 child/operation 下换 nonce（即使其它字段/P/HP 相同）或改任一请求字段，沿原 Conflict 语义拒绝，不新建/覆盖行、不刷新 challenge/期限、不重新发 grant 或材料。外部行同长度 request_digest 篡改必须先作为记录损坏 Unavailable，不能误当新请求 Conflict。unbound 和 bound 原 nonce 均须在新 Store/进程重载后重建出原 digest；这不改变 Unknown 时敏感动作只能安全 Status、不自动重放的客户端规则。

P 各字段沿现有 bound 并≤4KiB，BP≤8KiB、managedModels≤32；新增 `L(PrepareNonce)` 精确 36B，unbound plaintext 上界 4148B，bound 上界 **12396B**（原草稿 12360B+36B）。沿现行 `NewGCMWithRandomNonce` 的 28B 总 overhead，bound ciphertext 上界 12424B。preparation charge 沿原公式 `512+len(publicID)+16+32+32+len(ciphertext)+len(phase)`；以 publicID≤64B 及最长合法 phase16B 计算为 **13096B<16384B**，没有放宽原 16KiB。授权前后完整 plaintext/ciphertext/真实 charge 仍分别检查 16KiB，以实际 AEAD.Overhead/Seal 长度和全事务后态核算，不拿此算术代替实现测试。若真实 16KiB 不足，返回 Quota/ResourceExhausted、无半 grant、不消费 challenge；须先复审存储设计，不删字段或扩额迎合。回执中可返回完整≤64KiB 声明，持久层只存精确 HD，不把 hash 当作可找任意新 revision 的 cache key。

四表既有单行 Challenge/Child/Preparation/Grant 分别 4/4/16/4KiB；parent 合计 56 行/416KiB、owner128/896KiB、instance512/3584KiB，全部终态/过期行在真实删除前仍计费。每次 admission 检查四表完整后态，Authorize 含 preparation replace+grant insert。charge=512+全部实际持久可变 bytes（含 AEAD/重复），不是只数 key 或声明长度；SQLite/WAL/进程 RAM 不在此逻辑上限承诺内。GC 仍≤64roots/256rows/1MiB/协作 50ms、串行 30s 周期；Logout 真实级联不受 GC 批限，跨 Store 进程争用不得各抢最后一额度。

各表的行数/合计 KiB 也分别保持，不能仅检查上面的 combined 总额：

| 表 | parent | owner | instance |
| --- | --- | --- | --- |
| Challenge | 8/32 | 32/128 | 128/512 |
| Child | 16/64 | 32/128 | 128/512 |
| Preparation | 16/256 | 32/512 | 128/2048 |
| Grant | 16/64 | 32/128 | 128/512 |

### 6.2 Unknown 与生命周期

child/prepare 最多既有 5 分钟且不晚于父 session；intent≤prepare/child/批准期限；grant≤60 秒且≤intent/父/子有效期。取消/Close 不延期限，远端收包时身份仍有效不是原子保证。旧 signer/session/MAC/key 初始化策略不改。

客户端在开始实际 Authorize/Consume 前置 attempted，出错/丢回执/断流/COMMIT 后取消均不能因 Status=issued/not-issued 再发送该动作，也不重试 Bind。Unknown 的敏感后继只能是原 flow 的 **安全 Status**（沿既有最多一次自动查询；额外人工核实须显式、有界），不回收重造 grant、不重放秘密、不从服务器缓存敏感响应。服务端 COMMIT 未知不给 token/key；确认已提交但未交付仍保留事实，Status 可为 issued/consumed，但从不返回材料。

SQLITE_BUSY 仅在能证明未进入不确定 COMMIT 的既有有界重试点重做；不得把 ambiguous commit 当 rollback 重试。请求回调不发送响应，真正 drain 后才释放 8slot/worker；失败零结果，不能候选 config 成功就隐藏 catalog/auth 失败。C2 失败不自动新建 operation/调用 D.publish，正常进程退出要真实 join interpreter/HTTP/Body/Close，不留遗弃 goroutine。安全状态不暗示本机 rollback/applied。

## 7. 编解码、错误与秘密边界

| 项 | 不扩大的接受界 |
| --- | --- |
| native bootstrap | Begin/Bind 各 8KiB；header16KiB；其余请求/未压缩响应各 512KiB |
| HTTP 生命周期 | 8 个 service-wide 槽；20s 从 body 前到真实响应写/flush/排空，既有健康 EOF/取消/Close/H1/H2 处理不退化；plugin 仍原 3s，不延预算 |
| strict protobuf | 两端递归≤16、累计 field/value 预算≤1024；包括新嵌套 catalog/optional/list/oneof；不按自报长度预分配；合法 packed/unpacked enum 序列映射相同有序列表 |
| A | 全部既有逐项/模型≤32/目录 JSON≤32KiB/声明 JSON≤64KiB/desiredJSON≤48KiB；canonical A 另≤64KiB，不用 native protobuf 更紧凑豁免 A 界 |
| 本地投影/crypto | BP≤8KiB；任何 transcript≤512KiB；managed-model IDs≤32，单 ID 沿 A 128B 界，ID/profile 沿既有 128B；hash32/operation16/signature64 |
| B 本地预算 | 仍由 B 处理每 image256KiB/总 768KiB 及 parser/edit 界；C2 没有 raw image/body 参数，不把只传摘要冒称验证了这些 OS/解析预算 |

外层先限 bytes 再反序列化；两端拒绝任意深度 unknown、重复 singular/oneof/同一 presence 分支、错误 wire 类型、非法/未知 enum、overlong varint、错误长度、尾随或真实额外空成员。optional false 必须保留 presence；解码入临时对象，校验全部后才替换目标，失败清零。兼容合法 enum packed/unpacked，不接受重复 slot 或借重复 field 最后值覆盖；raw 字段出现性检查不能只看 Go 解码后 omitzero。首片 Native 仍拒 JSON/gRPC-Web/GET/query/request compression/浏览器 Origin/Sec-Fetch/cookie 认证，固定真实 TLS/public-origin/禁 jar/redirect/no-store，Web/MCP 无材料入口。

错误只沿既有 InvalidArgument/Unauthenticated/PermissionDenied/Conflict 或 FailedPrecondition/ResourceExhausted/Unavailable/Canceled/DeadlineExceeded 固定分类；父撤销/无效 proof 先于资产/plugin，合法 stale 才 Conflict，unknown/body 损坏固定 Unavailable。未知 NativeProfile/D 观察、B unsupported representation/ownership/policy 失败在本机 Authorize 前分类拒绝，不把 parser/URL/keyring 原错送远端或日志。Status 不能用错误不同泄漏其它 owner/grant 信息。

raw generated DTO、A declaration/catalog、BP、hash、proof/native/grant token、session、材料与候选均敏感，**不得整体 fmt/JSON/log/slog**；debug_redact 不保证 Go String 安全。业务只接 opaque 私有间接 payload，构造和受信 copy 都 deep clone，默认所有 fmt 动词/私有容器/JSON/Text/stdlog/slog 固定脱敏，不输出正文、hash 或长度。仅固定 code、计数和无敏感的布尔可入正常日志；连测试失败也不 dump 输入。secret 只在授权传输、受控 materialization 到 B/D；JS/Web/public/MCP 不接触。本机 OAuth/token 不上传，hash 也不当匿名化凭证。Destroy 只尽力清自有 buffer，不许诺 Go 字符串/副本/GC/WAL 物理擦除。

## 8. 混合版本、持久升级与生成约束

- 新 server+旧 Claude client：原请求/回应不得带任何 Codex 字段，canonical/旧 golden 逐 byte 相同；同 owner 的两种 agent 子操作分别绑定，无跨 purpose/agent/target 重放。
- 旧 server+新 Codex client：Prepare 的新 enum/tag40 被旧 strict codec 拒绝；客户端固定 incompatible/unsupported，零 Authorize/Consume，不降级成 Claude/旧插件/官方账号。不因 Begin/Bind 成功宣称能力可用。
- 新 client 收到旧/残缺 Codex 响应：缺 Codex version/payload/hash/完整 Binding、未签 pin 或混入 Claude 字段都在敏感后继前拒绝；不推断默认值。不增加无认证 descriptor 握手或自动 probe retry。
- 新 server 读旧 Claude encrypted record 原样可用；新 Codex compact record 严格版本化。旧 writer 不能正确解释新 Codex 记录，部署必须先停旧 writer 再升级/迁移，回滚须停止新操作并按兼容方案处理未过期 Codex 记录；不能声称新旧 writer 长期混跑或把坏记录抹为 missing。
- 只增 native proto 标签与正常 Go/Connect 生成闭包；已有 public Codex DTO 直接复用，public wire/Go/TS 不暗改。Web 模板继续整个 native 目录负向排除，Go 完整生成正向 parity；Buf FILE breaking 与旧 Claude6RPC/current 一并验。未来删字段须 reserved，不能占用旧 reserved 槽重新解释。

## 9. 完整实现必须取得的验收（本 spec 未执行这些产品门）

Go1.27.3/CGO0，实施前完整 Modern Go list；真实合成 SQLite、TLS 六 RPC、MAC/Ed25519、moejs 和独立 client，不读真实 HOME/OAuth/keyring/凭据，不 CGO1/race，不以 mock 调用顺序、编译失败、panic/总 timeout/零匹配当行为 RED。每个 test 记录实际 run/pass/fail/skip、有限 cleanup 与 exact tree。schema/pin/store/client/HTTP 是完整切片，不能仅交 mapper 或 secret getter。

| 门 | 必需独立正控/反例 |
| --- | --- |
| canonical / 双 pin | §10 全部 bytes/hash/signature 两端独立吻合；真实 A Registry/module 的 digest 与 client pin 相同，完整声明每字段/presence/order 跨 A→wire→Binding→server 持久重验；全部旧 Claude 向量逐字不变 |
| 单槽/三 mode 边界 | 真 owner Codex/provider-key：Prepare→D/B 准备见证→完整 Authorize→一次 Consume，slot 恰好 3/key 原字节；missing/copy 无 key、native-login/官方 OAuth、重复/多余/Claude 槽、其它 agent 一律拒绝且无 fallback |
| 全 A 和 B 投影 | 全 optional absent/false/none、context 边界、CRLF/TAB、32 行有序目录及隐藏字段；三 role presence 与 SHA 不同；两个 profile ID 区分，模板/policy/effective-scope/ownership/managed IDs/witness 任一改变必须失配 |
| 鉴权与同事务 | 两 owner/两 parent/两 native/两 operation、revoke/expire/asset 更新/删除与 Authorize/Consume 真实屏障；坏 proof/token 在 asset 不同版本下同固定拒绝、零 plugin/解密；合法 stale 为 Conflict；SQL COMMIT 后取消/响应丢失不返材料 |
| D/B 顺序及 Render | 可信 preflight 未完成/Unknown/profile 不支持时实际 HTTP 计数 Authorize=Consume=0；材料返回只送原捕获 B plan；更换 base/plan/catalog/order/policy/profile 即失败；Render 失败全零；D 发布前 baseline/witness/selection 变动拒绝且不重新 Consume |
| record/额度/重启 | 32KiB 合法目录不被重复塞入 16KiB preparation；§10.3 compact 独立逐字段/完整 bytes golden、32B 原 PrepareNonce、unbound/bound 新 Store 及子进程重启均重算原 request_digest；同 nonce 同请求原幂等结果、不同 nonce/改字段 Conflict、同长度外部 row.request_digest 篡改 Unavailable 且零行变更；0/31/33B/截断/无 nonce 旧草稿拒绝。绑定前后真实 AEAD 长度/charge、四表三 scope 边界/replace 与两 Store 最后 slot 竞争均测，不将+36B 漏计；旧 Clauderecord 及错误 version/AAD/phase/尾随拒绝、terminal 行与 GC 级联不漏 |
| strict HTTP/codec | 真 TLS 请求及回执：未知/重复/空 nested/enum/oneof、falsevsabsent、合法 packed/unpacked；8slot/真实 20s 写 deadline/取消/Close/H1 健康 EOF/H2、no-store/redirect/jar/解压预算不退化；oldserver/newclient failclosed |
| Unknown/隐私 | 真丢 Authorize/Consume 回执与 driver commit-unknown；服务器 Status 安全、客户端 attempted 不回退、不重放/新 grant；捕获 stdlog+slog+HTTP error 与正常/错误响应，对 proof/key/session/正文/raw/可逆 hex sentinel 独立检漏 |
| 串交诚实性 | 无已签 NativeProfile/D 时只能 qualified synthetic contract，生产入口 unsupported、零敏感 RPC；完整 C2 生产桥及 D 发布验收另需正式输入身份，不把 caller digest 或局部合成成功当安装支持 |

至少八项可编译行为负控：①漏 A optional false/catalog 字段或重排目录；②漏任一 role presence/hash、模板或 witness；③接受仅 caller32B 而未验证完整投影 HI；④把 Unknown/auto-keyring 错误当 Missing/false，或跳过本机 preflight；⑤移除 proof/token 先于 asset/plugin gate；⑥Consume/Authorize 丢回执后自动重发；⑦漏 preparation replace/terminal 行 charge；⑧raw DTO/parser 私密错误/容器泄漏。每项先 compile0，再用独立原断言有限 RED，恢复 exact 源 GREEN；不得放宽旧 Claude/预算/锁/断言。

持久修订另有必测编译后负控：移除/错误替换原 PrepareNonce 导致合法 unbound/bound 重启失败；跳过原 request_digest 重算/比较会让同长度外部行篡改被接受；只比 P/HP 或忽略 nonce 会让不同 nonce 的重复 Prepare 被当作同请求；漏计 36B 会使实际 AEAD/charge 与原断言不等。每类均须 compile0→普通行为断言 RED→恢复 GREEN，不以 Python 数学/parser controls、单进程 round-trip、编译失败或 timeout 代替真实 SQLite/新进程产品证据。

以下是未来实施命令，本轮没有执行；具名新 C2 与旧 Claude selectors 及 count20 必须在实际计划中锁定，并检查非零 run/零意外 skip。生成比对在独立临时输出目录，BASELINE 指向未来已冻结旧协议导出的实际根，不是移动 HEAD：

```sh
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
CGO_ENABLED=0 go test ./internal/nativesessionauth ./internal/configclient ./internal/server/configuration ./internal/server/state ./internal/server/nativeconsumer ./internal/server/httpapi -count=1
CGO_ENABLED=0 go vet ./internal/nativesessionauth ./internal/configclient ./internal/server/configuration ./internal/server/state ./internal/server/nativeconsumer ./internal/server/httpapi
buf lint
buf breaking --against "$BASELINE/proto"
GENERATED=$(mktemp -d)
buf generate -o "$GENERATED"
diff -ru gen "$GENERATED/gen"
buf generate --template buf.gen.web.yaml -o "$GENERATED"
diff -ru web/src/gen "$GENERATED/web/src/gen"
CGO_ENABLED=0 go list -deps ./cmd/cognate ./cmd/cognate-desktop
```

不能以没有匹配测试、gen-only 或最终一次 GREEN 隐藏产品负控；实际 D/NativeProfile/native consumer 门与合成协议测试分报。

## 10. 可跨机器重建的双方 known-answer 向量

以下**全为公开合成输入，不是凭据或可用 profile**。它们只锁 encoding/签名数学，不授权该 profile、D 或本机发布。未来 server/client 的独立实现必须同时匹配；不可以同一 encoder 生成“双方期望”。本轮仅用独立 Python 编码/OpenSSL3.6.5 签验完成数学自检，未跑产品 C2/Go/gRPC/native 窗口。

### 10.1 完整输入

`h(s)=SHA256(UTF8(s))`，无尾换行；seed=`h("cognate codex native consumption synthetic vector v1")`，Ed25519 按标准 seed 导出 public，consumerKeySHA256=h(raw public)。origin=`https://proof.example:8443`、owner=`owner-synthetic`、asset=`AAAAAAAAAAAAAAAAAAAAAAAAAA`（26 个 A）、assetRevision7、schema1、kind1、C0ProfileID=`profile-c2`、selectionRevision9、prepareID=`prepare-synthetic`、grantID=`grant-synthetic`、confirmationRevision11。AD 沿§3 固定 pin；target2/scope1/requiredSlot3。

operation=16 个 0x01；nativeSessionBinding=32 个 0x05；prepare/authorize nonce 均 32 个 0x06；authorizeChallenge=32 个 0x0b；consumeChallenge=32 个 0x0a；synthetic grantToken=32 个 0x07；consume nonce=32 个 0x0c；status nonce=32 个 0x0d，status grantID 为空。preparedMS=1700000000000、prepare/childExpiresMS=1700000300000、intentExpiresMS=1700000240000、grantExpiresMS=1700000060000。Bind 另用 clientNonce32 个 0x03、challengeID=`challenge-synthetic`、challenge32 个 0x04、challengeExpiresMS=1700000060000，其它 consumer 字段同上。Bind 仍原 prefix/算法。

```json
{
  "apiVersion":1,"target":"codex.config","providerID":"cognate","wireAPI":"responses",
  "endpoint":"https://provider.invalid/v1","model":"alpha","reasoningEffort":"high",
  "catalog":{"models":[
    {"model":"alpha","displayName":"Alpha","contextWindow":131072,
     "supportsParallelToolCalls":false,"inputModalities":["image","text"],
     "baseInstructions":"Keep\nspaces\t","reasoningLevels":["high","none"],"defaultReasoningLevel":"high"},
    {"model":"beta"}
  ]},
  "credentialReference":"codex.provider-key","requiredSlots":["codex.provider-key"],
  "localAuthPolicy":"preserve-local-login-v1"
}
```

NativeProfileID=`synthetic-codec-profile-v1`、profileSHA=`h("synthetic-native-profile")`、templateSHA=`h("synthetic-catalog-template")`、scope1、provider/catalog ownership 均 2、managedModels 原序 `["beta","alpha"]`；AuthStore2、Keyring1、FileLogin4、requiresOpenAIAuth=true、policy 原值同 A、witness=`h("synthetic-D-observation")`。auth 为 NotObserved 无 hash；config/catalog 均 Present，其 SHA 取下列 UTF8 原始 bytes（每个最后一行含 LF，config 共 7 行）：

```toml
model_provider = "cognate"
model = "beta"
[model_providers.cognate]
name = "Cognate"
base_url = "https://old.invalid/v1"
wire_api = "responses"
requires_openai_auth = false
```

```json
{"models":[{"slug":"beta"},{"slug":"alpha"}]}
```

catalog 只是该 canonical fixture 的基线 bytes，不是已签 NativeProfile 必填模板或真实 Codex 可用目录。所有未列 optional 都 absent；不加默认 display/false/空集合。

### 10.2 分域已知答案

| 编码 / bytes | SHA256 |
| --- | --- |
| A /381 | `755604daa9081d56c02c402cd3fdef0c3cef16cb55bb2b8972468b9748205a58` |
| BP /399 | `80c825add5feae09fac68e302612f5a08018924c40a1617298b41f4ba2a5c3a4` |
| preparation 编码 /478 | `b5774b4a50e51548f42a657983df8d4867210a7f4b8da9ca362f9d50395f91ec` |
| I /174 | `4276b09de36c101751a3f8a0c4144dd5a80f2d6137d2ff9433641b3aa58246ce` |
| apply 编码 /113 | `f1d6db947372648799395bcb7ca42c9d8822b5232f3422200ece54fd47f521e8` |

| transcript / bytes | SHA256 | Ed25519 signature hex（64B） |
| --- | --- | --- |
| bind /302 | `9107fd9cf7854802543378fcc6e20bb4bd77b0aa8ca0198e08f1dcc3927a95a9` | `5dc2f9edf8db5be5dc0c5cecabea8a151b2d3739a0b5494577cbcd244bb654d94e271aed6b7e85b5d5a9bb4e04b7074f34af3748ccdbcd26ab8fc421c56b6d00` |
| prepare /477 | `fdc7d0014030c482df632e3b100444f6c078ef8df6917e3b71dc2b1f58893df5` | `42504b6c4a2bc9c9e19807a83f5ae1a04f3190d5f4f2fd9a3c505956117e3a97471baeb47fee4a1e9d20a4ed9d4cfec708560ca772deeeddbda744134cd21e00` |
| authorize /299 | `19808bf5efa5ee02ec32879d6767e91c587608b2598f6db672e22d9acd721db6` | `a1fafff3cb57daff89ed457165880d3bf82f72d0ec7565d2e97adb18528ebb103a42d99a92653079112b0ee2cd1b64df3742ee7006e2aafae76dcdb08b5b0307` |
| consume /311 | `cfe576fba0055872f10b66c59ccd6bf175f0426b9b028445777e4c4bc883818d` | `acd7b31645cca915062275ea147ef582185c7111e8e1e3c2fd6f55785838c98cd71cbe22980be3f59d6a9079b01c26c33d485312fc2eb61b5762339cc56bd208` |
| status /200 | `3cc559e8a9014a7f7746a7da1a3def6a58e5a22104af11042de81ac5bffa3662` | `e0d701a08cec8254555c4e61d3cef25e8e1968ac480001ab35f4730f26c928953013cff313ffb31e88cd875c2c6b436f81902260f6b8ca19b1cda766f5f6da05` |

三个编码区分向量：只将 alpha 的 parallel false 改为 absent，HD=`d7101fb858499f8a46b465e52eec678148115b69390a5c01c1d7f57c36aa6361`；只交换 A catalog 两行，HD=`7e578111366bf3ac4a4a048883d0e18195649a9bd04f24fa6b7c77f57800abd9`；只将 auth 的 NotObserved 改为 Missing（无 hash），HB=`a8ecb9fb0018216076bbf5e333b67b1913e04af0cee5a2bdb07b5bd5816058d2`。最后一个只证明编码区分，不意味着 keyring 分支可额外读取 auth；业务合法性仍按§3。

还必须对每个 purpose/revision/operation/slot/pin/selection/profile/template/policy/witness/role presence/hash/consent/expiry 逐项变异，原签名在新 transcript 上失败，完整 intent 不相等；交换 purpose、将 hex 字符串当 rawhash、LE 长度/count 遗漏及与 Claude 交叉均失败。此为未来双方真实负控要求，不把本轮 Python 编码变化说成已编译的产品 mutation RED。

### 10.3 Compact 自身的独立 known-answer 与边界

使用§10.1 同一组公开合成输入，`PrepareNonce=32×0x06`，P 为 439B、BP 为 399B，HI 仍§10.2 原值。unbound 无整个 binding 尾部；bound 尾部取原 BP/HI、intentExpiresMS=1700000240000、confirmationRevision=11。下列是**完整 compact plaintext**的长度/SHA，不是 HP、stage digest 或 AEAD 密文 hash；未来 server 独立编码/重载须匹配完整 bytes 与各字段，不用产品 encoder 自己生成期望。

| record | bytes | SHA256 |
| --- | --- | --- |
| unbound / nonce06 | 491 | `53a87c54cfa585db0e8132aa884bf52b8b39911968347e837c09a5cbea943b48` |
| bound / nonce06 | 946 | `6b28cca143875abcaf2c5a403407760015c74612eca56e5c129838f4acfa5276` |
| unbound / 仅 nonce 改 32×0x22 | 491 | `b2f86502995586e7be14d3dbaf3eaa76b8158d2cf816d656efbbe49e6a8cff23` |
| bound / 仅 nonce 改 32×0x22 | 946 | `b34669bca3789d1a59e48ee7831486dba4362103506a146fc14d4d85c234e782` |

0-based 字节位置固定如下；P/BP 全部内层字段仍由§5 和§10.1 独立展开，不改顺序/类型。表中分隔符只作描述，不入 bytes。

| 字段 | offset | 独立期望 |
| --- | --- | --- |
| prefix / version | 0 / 4 | `43583200` / `00000001` |
| `L(P)`长度 / P 起点 | 8 / 12 | `000001b7` / 439B；SHA256(P)=`cd4f9290526242947bbe375b4195f4d24b902414b18094ae0f361b4bedf7ffa4` |
| `L(PrepareNonce)`长度 / 原 nonce 起点 | 451 / 455 | `00000020` / 32×`06`（变异 fixture 为 32×`22`） |
| hasBinding | 487 | unbound=`00000000`且 EOF=491；bound=`00000001` |
| bound `L(BP)`长度 / BP 起点 | 491 / 495 | `0000018f` / 399B，其 SHA 为原 HB |
| bound `L(HI)`长度 / HI 起点 | 894 / 898 | `00000020` / 32B，其值为原 HI |
| bound intentExpiresMS / confirmationRevision | 930 / 938 | 原值按 U64 big-endian 编码；EOF=946 |

nonce06 重建的原 Prepare stage digest 为 `4676152340097bc5f51583d47cbeaca22f69e31eac628b8991aa1309259ecb21`；仅 nonce22 则为 `65cc82f7cada942677a7fc0f13046f280442105f0b984217e0850831f4476bc5`。两者 P/HP/HD/BP/HI 和其余四用途向量均不因持久布局改变；**认证 compact 已不再相同**，load 可从各自原 nonce 独立核 row.request_digest。原无 nonce 草稿 455/910B 及其反例/REJECT 保留，不作为可兼容持久格式、不用 zero/Begin nonce 补齐。

结构边界用 P=4096B/BP=8192B 分别核 unbound4148B、bound12396B 及+36B/AEAD28B/charge13096B；这是上界算术，不代表任意占位 P/BP 可通过内部语义。P4097/BP8193、nonce0/31/33、hasBinding2、HI31B、截断/尾随与无 nonce 旧布局均拒绝；真实产品仍须先认证再解析、严格校完整 P/BP/phase/row 并测试 16KiB charge 边界。此处本轮只执行合成 Python compact 编码/独立 Reader 结构与 digest 对照；未执行 C2 Go、SQLite、AEAD、重启/HTTP 或任何编译后产品负控，不将数学结果标产品 PASS。

## 11. 候选接缝与授权出口

未来计划须精确申请 native proto 及正常 Go/Connect 生成闭包；`nativesessionauth/native_apply*` 增 Codex typed/canonical/verifier 分支；`server/configuration/{codex_native_boundary*,native_consumer*}` 完整 A/密钥读取及事务 validator；`server/state/native_configuration*` compact record/四表额度/commit 未知；`server/auth`仅现有 preflight/opaque 快照必要串接；`server/nativeconsumer`、`httpapi/native_configuration*`严格 codec 与真实 TLS；`configclient/native_{pin,codec,flow,proof}*`窄 Codex 类型/opaque 响应及 testdata。尽量新增同前缀 Codex 文件，不把 Claude patch 数组泛化为 any/map。若实际要改 Ent schema、nativeconfig/本地核心、B、App guard、Web、CLI/MyGo、CI 或依赖，先报具体必要性另授，不偷扩本规格为源码权。

后续计划先冻结实际 B exact14 签交身份与 accessor、public Codex C1 生成基线、NativeProfile/D 生产桥状态，再分配串行公共文件写权。C2 不能授 D 发布/多 artifact journal，不能把目前无 profile/D 的产品调用开关改成成功。只有协议/双方 known-answer/真实服务事务与隐私全部闭合、D/profile 另签，才能在后继完整纵切中宣称可本机应用。

本文及 ignored 作者说明是本轮唯一写集；不写 plan/Go/JS/proto/module/CI，不 stage/commit/push，不读真实 HOME/DB/keyring/key，不执行 upstream、不 spawn。原 A/B/spec000b17、旧 Claude 向量及所有历史 REJECT/RED 保持不动；完整十四 agent/F12 目标不变。
