# Codex Native Consumption / C2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans after main grants the fixed source baseline and exact write-set. Steps use checkbox (`- [ ]`) tracking. No spawn/stage/commit/push under this plan; each task requires actual RED/GREEN evidence, followed by non-author review of the complete freeze.

**Goal:** 实现已签 C2 的完整 Codex provider 协议、独立双方 canonical/proof、真实四表事务和客户端消费核心；生产 D/NativeProfile 准入保持关闭，不把协议材料化说成本机 Applied。

**Architecture:** 在原六 RPC/parent-native MAC/Ed25519/Store/NativeClient 生命周期内增加严格 typed Codex 分支。Codex 与 Claude 的 preparation/apply/record 类型分别持有，不把旧七操作数组改成 any；成熟 B 仅作客户端依赖，不进 server，也不修改它。JS 在事务外，权威快照与原 revision/输入在 COMMIT 事务内复核。

**Tech Stack:** Go **1.27.3** / **CGO_ENABLED=0**、现有 Connect/protobuf/Buf、Ent+现有纯 Go SQLite、AEAD/Ed25519、server/plugin.RunJSON 及已交 A；无新增依赖或模块变更。

**Spec:** `docs/superpowers/specs/2026-10-30-codex-native-consumption.md` SHA256 **`d7380ad914f73b0cb698eb62af209e27c29a423dd4e14ba5285af21174ff56c7`**，commit `ff83bdc7fad983bc4d5b6bebcbc5377787dc782e`。Gauss fix1 scoped ACCEPT，不覆盖旧 5bff REJECT/nonce 反例。本计划候选待独立设计审，**不是源码授权**。

## Global Constraints

- 仍是完整 A+B+资产 C1+C2+D 与十四 agent/F12 目标；此片不提供 NativeProfile、D、OS attestation、文件读取/写入、journal、CLI/MyGo/Web 入口、账号/OAuth/MCP/prompt 或自动应用。
- 仅 `codex/provider/schema1/user/codex.config`；API1、`builtin.codex@1.1.0`、capability `codex.provider-plan.v1`，完整 definition pin `f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4`。唯一必需/批准/返回槽为 native slot3 / `codex.provider-key`；ExplicitConfirmation revision>0，拒绝 Codex auto/零槽/官方账号 fallback。
- A 每字段/presence/顺序都纳 HD；三角色 presence/hash、policy 原值与解析、ownership/managed-model 原序、witness、独立 NativeProfile 证据 manifest hash/模板纳 HB。C0 profile 与 NativeProfile 不混同。B 的 `ProfileDigest` 不是原始签交证据 manifest hash，不能拿它替代 spec 中 profileSHA。
- 顺序固定 Begin/Bind→Prepare→D-preflight/B.Check→完整 intent→Authorize→Consume 至多一次→原 plan.Render→D 发布前复核。caller32B/签名/构造的 Binding/测试 capability 均不证明 OS/D/人类事实。
- NativeProfile/D 正式生产来源尚 OPEN。生产客户端 Authorize 前 gate 无有效来源即固定 precondition/unavailable，HTTP Authorize=Consume=0；不得用 synthetic profile、环境变量、导出 setter、任意 digest/bool 或临时 hook 打开。**私有协议核心必须完整正向运行；不能将其全部 unsupported 来替代协议/事务实现。**
- Claude API/patch/零槽登录/auto policy/JSON record、旧五 proof 加 authorize-auto、Hprep/Happly/Hpatch fixture 逐字不变；Codex 使用新域及新增标签，不复用 reserved/tag21，不把旧字段改 oneof。
- bootstrap8KiB、其它请求/未压缩响应 512KiB、header16KiB、service-wide8slot、真实 20s 全请求/写出/排空、plugin3s；protobuf 递归≤16、累计 field/value≤1024。合法 packed/unpacked 有序枚举均接受并计费；不按自报长度分配。
- A desired/catalog/declaration JSON≤48/32/64KiB，models≤32；canonical A≤64KiB、BP≤8KiB、P≤4KiB、transcript≤512KiB；完整字段逐项界沿 spec/A，不 trim/alias/排序。新 key 为 1..8192 UTF-8 bytes、无 NUL/CR/LF。
- preparation 保存认证原 PrepareNonce32B，精确+36B；unbound4148B、bound12396B 上界，当前 GCM 总 overhead28B、ciphertext12424B、最大示例 charge13096B。plaintext/ciphertext/实际 charge 各≤16KiB，四表每行 4/4/16/4KiB 及 spec§6.1 全部逐表/三 scope/combined 行数与 bytes 限额不抬。
- 先 MAC/精确父子/session/purpose/operation/proof（Consume 含 token）再 asset/plugin；合法 stale 才 Conflict。未知 COMMIT/丢回执不得返 token/key、不得重放 Authorize/Consume/Bind，仅安全 Status。终态未删除前仍计费，Authorize replace+grant insert 同后态、GC/Logout/BUSY 边界保留。
- raw 生成 DTO 及受信 copy 均敏感；业务 handle 私有间接 payload、deep clone、默认全部 fmt 动词/私有容器/JSON/Text/stdlog/slog 脱敏。Secret8 只经现有 borrow/Clear 生命周期使用，不回添 `.data`、raw shadow、公开取钥旁路或 Go 堆物理擦除声称。

## Review Focus

1. MAC 有效但 record 为 Codex：旧 `nativeLoadPrepared` 只能解 Claude JSON；必须从认证内文选分支，不能先试 Claude 失败再 fallback（Tasks2/3）。
2. Prepare 重启/重复时只比 HP 会漏 nonce，外部 row.request_digest 也不受原 AAD 直接保护；用认证原 nonce 重建并比较，损坏先 Unavailable（Task2）。
3. 真实 A 插件输出与事务内资产输入之间发生 CAS 变化，或坏 proof 遇不同资产版本，不能形成 plugin/存在性 oracle（Tasks2/3）。
4. 新 known proto 字段的空/零/presence、packed 枚举与累计预算可能绕过旧 Claude-onlyvalidator；strict 两端不能只看反序列化值（Tasks1/3/4）。
5. 新 flow 复用现有 Close/admission 但遗漏 agent 隔离或 attempted，可能跨 Claude 重用状态、早释放 slot 或 Unknown 重放（Tasks4/5）。

## 0. Source / caller / ownership / generation inventory 与开工门

只读观察 HEAD `ff83bdc`，不是未来执行 base。39 项固定源码/外部冻结输入 SHA、实际 caller 搜索及两份 Buf 模板已存作者证据；实施者必须在实际 source grant 时重新冻结一次，不从移动 workspace 取 WIP。

| 现有接缝 | 实际限制与最小接法 |
| --- | --- |
| `nativesessionauth/native_apply.go` | PrepareRequest/PreparationFields/ApplyFields、HashPatch 及验证器均 Claude-only，旧 verifyApply 还固定旧 stage 域；新增 Codex typed/canonical/verifier 文件，复用私有 verified capability 的承载但**不调用旧 verifyApply 算新 stage digest** |
| `state/native_auth_seam.go` / `native_configuration.go` | NativeApplySnapshot 含旧 JSON record，四类 preflight 再走真实写事务；新增 Codex snapshot/record/事务函数，复用 nativeConsumerGate/nativeTransaction、原 AAD、额度/GC helpers，不改 Ent schema 或旧 snapshot 含义 |
| `auth/native_configuration.go` | 已有 credential 转换、两次 preflight/执行验证、issued grant；新增同包 typed Codex 方法，不让业务自行从 digest 构造 VerifiedApplyProof |
| `configuration/native_consumer.go` | 旧 nativePlan/Prepare/Authorize/Consume 只会 Claude；新 Codex boundary 复用同 Store+registry，并在既有 Consume/Status 入口按认证记录协议路由。**此共享文件当前唯一 owner 为 main Secret8，交付后才能串行写** |
| pending Secret8 | 输入 tree `380acf91541317d0017c80f4adc577017dbc0302` 仅作只读 API 观察，尚不当签交 base。Secret 已为 closure-owned，使用`secretBytes(secret)`受限 borrow 及`Secret.Clear()`；`decodeCodexEnvelope`回`(CodexProvider, Secret, extras bool, error)`。不修改其其它 7 路径，不还原旧 raw 字段/清理方式 |
| `nativeconsumer/service.go` / HTTP 六 handler | 共享 Call/8slot/20s 和真实写 deadline 保留；只增两个 typed 转发和有限 dispatch/映射；Consume/Status 不能接 caller target hint |
| `configclient/native_{client,flow,proof,codec,pin}.go` | NativePreparation.Patch/NativeApproval.Slots 及 pin 当前为 Claude；保原 API。用同 NativeFlow.start/sign/call/Close 及 client.flows 追踪，增私有 Codex 状态与独立方法，不新建 HTTP/crypto/lifetime。NativeClient 本身预计无需改文件 |
| 已签 B a186 | `ProviderPlan.Binding()`→`CopyDeclaration/CopyBaselines/CopyObservation`，三 Role/Presence、完整 Observation、clone/Destroy 均已实际核对。`CheckProvider/RenderProvider`仍生产 profile 闭合前 unsupported；不修改 B 或用该状态删协议正控 |
| 已交 public C1 | `configuration/v1/configuration.proto`中 Codex catalog/enum 复用；native proto 只 import 它，不改普通 CRUD/domain/proto/TS/Web |
| 普通 Buf 闭包 | `buf.gen.yaml`对整个 proto 正常生成 Go+Connect；预期仅`gen/skillhub/configuration/native/v1/native.pb.go`变化，六 RPC 不变故 native.connect.go 应逐 byte 不变；public Go/Connect 全不变。`buf.gen.web.yaml`排除整个 native 目录，全部 TS 应逐 byte 不变。必须真实生成验证，不手改或凭预期免检；若闭包超出本清单先报来源申请，不删生成差异凑数 |
| 下游 caller | `internal/nativeconfig/flow.go`、CLI native_configuration 及 configapply journal/plan tests 继续只消费旧 Claude NativePreparation/Approval；本片只回归、不改这些 caller。App/root/install guards 不变 |

- [ ] main 交付正式 baseline：含已签 public Codex 资产、**已签 B a186 精确 14**及 Secret8 独立验收/提交身份；核它们相对本 inventory 的 delta、spec d738 及旧 Claude fixtureSHA。Secret8 未交付前不得触碰 native_consumer.go，不按 380 候选预先写共享源。
- [ ] main 明确源码 exact33、独立 reviewer 及串行任务 owner 后，git archive 到独立/tmp，合成 HOME/SQLite/凭据，读 Modern Go 完整 1.27 list、executing-plans/TDD。任何实际必要 scope 增量先申请，不能借本计划获得授权。

## 1. 候选最小 exact 写集：33 路径

M=修改现有；N=新增；G=仅正常生成。按当前调用链为**29 手写 Go + 1 native proto + 1 Go 生成物 + 2 合成 fixture/参考脚本**。计划进度/其它文档不自动计入后续源码 grant；本轮只有本 plan 文档写权。

```text
M proto/skillhub/configuration/native/v1/native.proto
G gen/skillhub/configuration/native/v1/native.pb.go
N internal/nativesessionauth/native_codex.go
N internal/nativesessionauth/native_codex_test.go
N internal/server/state/native_codex_record.go
N internal/server/state/native_codex_record_test.go
N internal/server/state/native_codex_configuration.go
N internal/server/state/native_codex_configuration_test.go
N internal/server/state/native_codex_auth_seam.go
N internal/server/state/native_codex_auth_seam_test.go
N internal/server/auth/native_codex_configuration.go
N internal/server/auth/native_codex_configuration_test.go
M internal/server/configuration/native_consumer.go
N internal/server/configuration/codex_native_boundary.go
M internal/server/configuration/codex_native_boundary_test.go
M internal/server/nativeconsumer/service.go
N internal/server/nativeconsumer/native_codex_test.go
M internal/server/httpapi/native_configuration.go
M internal/server/httpapi/native_configuration_codec.go
N internal/server/httpapi/native_codex_configuration.go
N internal/server/httpapi/native_codex_configuration_test.go
N internal/server/httpapi/native_codex_codec_test.go
M internal/configclient/native_proof.go
M internal/configclient/native_codec.go
M internal/configclient/native_flow.go
N internal/configclient/native_codex_proof.go
N internal/configclient/native_codex_proof_test.go
N internal/configclient/native_codex_flow.go
N internal/configclient/native_codex_flow_test.go
N internal/configclient/native_codex_pin.go
N internal/configclient/native_codex_pin_test.go
N internal/configclient/testdata/native-codex-proof-v1.json
N internal/configclient/testdata/native-codex-proof-v1.py
```

顺序依赖：Task0→Task1→Task2→Task3→Task4→Task5。Task1 两端 encoder 在不同包独立实现，不能互相 import 求“相等”；公共 proto/gen、旧 proof 分派、共享 native_consumer 与 flow 文件只由当时单一 owner 串行写，不并行拆 writer。

### Task 1: schema + 双方独立 proof/pin + literal oracle

**Files:** proto/G、nativesessionauth 新增 2、configclient native_proof.go 和新增 native_codex_proof/pin 各 2、两份 testdata。**Consumes:** spec§3 至§5/§10、现 public Codex 类型。**Produces:** server 的 Codex typed fields/capability、客户端纯 encoder/pin，正常生成后的固定 schema。

**固定接口：** server 新增`CodexPrepareRequest`（原 PrepareRequest 字段及 ContractVersion）、opaque `CodexPreparation/CodexLocalBinding/CodexApply`，与`CodexPrepareFields/CodexAuthorizeFields/CodexConsumeFields/CodexStatusFields`；后两种复用原 ConsumeRequest/StatusRequest 的 ID/token/nonce 字段，不把旧 PreparationFields 塞 Codex。提供`HashCodexDeclaration/HashCodexPreparation/HashCodexLocalBinding/HashCodexIntent/HashCodexApply`及四个`Codex*FieldsDigest`；Verifier 新增`VerifyCodexPrepare/Authorize/Consume/Status`返回原私有`VerifiedApplyProof`，新 stage 域只在该真实签验路径建立证据。

客户端独立`CodexPrepareTranscript/CodexAuthorizeTranscript/CodexConsumeTranscript/CodexStatusTranscript`，消费 native typed wire；`HashCodexDeclaration/Preparation/LocalBinding/Intent/Apply`各自在 configclient 实现，返回原 opaque Transcript/Signature 或 32B 受信 hash，不调用 server encoder。`CodexNativeAdapterPin() agentconfig.Descriptor`返回固定 pin 克隆，旧 NativeAdapterPin 不变。所有新增值构造校全 shape、clone 并默认脱敏；trusted copy 不是 safe-to-log DTO。

server 构造接缝固定为`NewCodexPreparation(consumer ConsumerFields, request CodexPrepareRequest, prepareID string, preparedMS, expiresMS int64, declarationSHA256 [32]byte) (CodexPreparation,error)`和`NewCodexLocalBinding(declarationSHA256 [32]byte, wire *nativev1.CodexLocalBindingV1) (CodexLocalBinding,error)`，只校形状/clone、不授权限。hash 函数均返回`([32]byte,error)`；Intent 输入为`(preparation CodexPreparation, local CodexLocalBinding, confirmationRevision uint64, intentExpiresMS int64)`，Apply 输入为`(preparation CodexPreparation, commitment [32]byte, intentExpiresMS int64)`。server/client 分别在自己包实现相同 spec 投影，不共享 encoder 或新增公共 schema 包；跨包 copy 均为显式受信副本。

`NewCodexApply(preparation CodexPreparation, local CodexLocalBinding, confirmationRevision uint64, intentExpiresMS int64) (CodexApply,error)`从完整输入自行重算 HI，Store 再与请求 commitment 比较；不以 caller hash 构造已认证 apply。CodexPreparation 的`CopyRequest()`保留原请求 nonce，canonical P 仍不含它；record loader 从认证独立 nonce 恢复该请求。三个 opaque 值的`CopyWire()`返回各自声明过的 native PreparationBinding/CodexLocalBindingV1/ApplyBinding 独立敏感副本，作为 HTTP 映射和同包 fixture 的明确出口，不返回共享 protobuf 指针。

- [ ] 写`TestCodexNativeSchemaBranches`、两端各`TestCodexNativeCanonicalGolden`及 client `TestCodexNativePinParity`。冻结 spec 全部五 transcript 完整 hex/签名、HD/HB/HP/HI/HA 及§10.3 四 compact 答案，不由任一产品 encoder 生成期望。独立参考脚本 从公开 literal 独立展开，真实 OpenSSL raw Ed25519 交叉验证；server/client 均逐 byte 吻合。
- [ ] 按 spec§4 精确新增 target2/slot3 及 tag40/PrepareResponse5；reserved5/12/15/30..39、patch21 及旧 AutoPolicy 不动。Codex 标签即使空/零也不得混入 Claude；Codex 声明与 patch 互斥，unknown optional/raw presence 不能被零值折叠。
- [ ] 新函数未定义可作初始 TDD compile RED，但另做实际可编译的缺 false/order/新 stage 域/slot 变异 RED 后恢复 GREEN。完整独立复核 A 逐项+累计 bounds，catalog32 行、有序隐藏字段/presence/none、整数及 CRLF/TAB；不能只 hash 原 JSON。
- [ ] `buf lint`、FILE breaking、双模板完整临时生成 parity；核预期唯一 G 路径，Connect/public/TS 全不变。保持 12 种旧 public proof root allowlist，三个敏感 response 仍不得经公共 MarshalNativeProof 输出；新嵌套结构只能经明确定义的 typed 验证入口，不接受任意 proto.Message root。
- [ ] 两端各跑`-run '^TestCodexNative(Canonical|Schema|Pin)' -count=20 -json`，检查非零 run/零 skip。旧 native-proof-v1.json SHA 仍`1bcece6b35dad4e58b1be2b644b963bb096a34f7c6f1d5ffe271531558fdba74`且原 proof tests 实际 PASS，不只 cmp 文件。

### Task 2: compact + 权威 preflight + 完整四表事务

**Files:** state 新增 6、auth 新增 2。**Consumes:** Task1 server typed/canonical、原 gate/事务/AAD/额度。**Produces:** opaque `NativeCodexPrepared/NativeCodexAuthorization/NativeCodexApplySnapshot`、auth `IssuedCodexNativeGrant`；不改旧 Claude 类型。

**固定接口：** Store 新增`PrepareNativeCodexApply/AuthorizeNativeCodexGrant/ConsumeNativeCodexGrant/NativeCodexGrantStatus`及三项`PreflightNativeCodexPrepare/Authorize/Consume`；参数沿原方法的 ctx/credential/origin/proof/token 布局，分别用 Codex prepare/authorize typed request、原 ConsumeRequest/StatusRequest。新增`NativeCodexPrepareValidator func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeCodexPreparedData,error)`及 Codex materializer（参数用 CodexApply，其余沿原 NativeMaterializer，返回原 NativeMaterialization）。`NativeCodexPreparedData`持有已核 Adapter+完整声明 clone/HD，Store 不调用插件。

auth 新增对应`PrepareCodex/AuthorizeCodex/ConsumeCodex/StatusCodex`和`PreflightCodex*`，与现 credential/verifier 私有生命周期一致。另有只供内部路由的`ConfigurationProtocol(ctx, bearer, operation, prepareID)`：经过 MAC/真实父子 gate 后认证解密并严格验证原 record，返回 Claude/Codex 判别；不是授权 capability。不得先读取资产、返回外部可见 kind 或用 caller agent hint 选择 Consume/Status。

`NewNativeCodexPreparedData(adapter nativesessionauth.AdapterFields, declaration agentconfig.CodexProviderDeclarationV1) (NativeCodexPreparedData,error)`负责声明深 clone 与 HD，作为 domain validator 的生产者，不作为 proof 来源。NativeCodexPrepared 提供`Binding() nativesessionauth.CodexPreparation`、`CopyDeclaration()`及`Challenge() [32]byte`；NativeCodexAuthorization 提供`Binding() nativesessionauth.CodexApply`及沿原接口的 ID/Challenge/ExpiresMS，IssuedCodexNativeGrant 另沿原 Reveal/Destroy。NativeCodexApplySnapshot 提供 Consumer 和对应 AuthorizeFields/ConsumeFields/StatusFields 的 typed 副本，只能由真实 Store gate 构造。后继 handler/materializer 不需要修改旧类型或窥探跨包私有字段。

- [ ] 先写`TestCodexNativeCompactGolden`、`TestCodexNativePrepareNonceRestart`、`TestCodexNativeRequestDigestTamper`：真实 SQLite unbound/bound 关 Store 及子进程重启，原 nonce32B 重建原 stage digest；同 nonce 同全部请求返回原结果，不同 nonce/改字段 Conflict；外部同长度 digest 篡改先 Unavailable，零行变更。测试完整 prefix/version/字段 offset/length/EOF，不接受旧 455/910B 草稿或 zero/Begin nonce 补齐。
- [ ] 实现先 AEAD 认证再分派的 compact loader/sealer，使用原 AAD。Codex 内文完整 P/BP/HI/expiry/confirmation+原 PrepareNonce，绝不保存全目录/本地 image；重载先核 consumer/row 及原 stage digest 再比较本次请求。未知 prefix/version/phase、hash/row 不符统一 Unavailable，不 try-Claude-then-Codex。
- [ ] 写`TestCodexNativeStoreLifecycle`、`TestCodexNativePreflightIsolation`：真实两 owner/parent/native/operation，prepare→authorize→consume 单 slot3，资产删除/换版/撤销/expiry 及 token/proof 不符先于 validator，显式屏障核零 asset/plugin callback；权威写事务内复核资格/CAS/HD 与输入快照、grant consumed 及返回前 ctx gate。
- [ ] 写`TestCodexNativeQuotaPostState`：P/BP 上界及 32KiB 合法目录，真实 AEAD 长度/36B 增量/13096B 示例 charge 与 16KiB 边界；四表单行、逐表 parent/owner/instance 及 combined 全部后态，含 preparation replace+grant insert、terminal 计费、两个 Store/子进程抢最后 slot；旧表 GC≤64roots/256rows/1MiB/50ms 和 Logout 级联不变。
- [ ] 写`TestCodexNativeUnknownCommit`：真实 driver/响应边界 overlay 模拟成功 COMMIT 后取消/丢回执及不可判断 COMMIT，不回 token/key、不把 unknown 当 rollback 重试；仅明确未提交的 BUSY 点可沿旧有界重试。反例必须实际事务后态，不只改 mock 返回值。
- [ ] compiled 负控撤 nonce、跳 rowdigest 比较、只比 HP、漏 36B/replace/terminal charge、移 preflight 到 callback 后；每个 compile0→原断言普通 RED→恢复 GREEN。state/auth 新 selector count20 及原 native/maintenance/GC suites 实际 PASS，所有原 Claude 行可重载，不改 schema/generated Ent 闭包。

### Task 3: A/Secret8/domain → service → 真实 TLS HTTP

**Files:** configuration 共享 native_consumer.go 及 codex_native_boundary.go/_test.go、nativeconsumer 两路径、HTTP 五路径。**Consumes:** Tasks1/2，**Secret8 已正式交付的共享文件**。**Produces:** 六 RPC 中的真实 Codex 协议分支及服务端完整声明/单槽材料，仍无本机应用入口。

- [ ] 写`TestCodexNativeBoundaryCASAndAuth`：真实加密 Codex 资产、Registry+moejs、两 owner/session；native-login/未来 extras/缺 key/copy 无 key/unknown kind/schema 拒绝。修改既有 codex_native_boundary_test 只将已获明确 source 授权的精确 provider 分支由旧拒绝升级为真实成功，其余拒绝、Claude 断言不能删除。
- [ ] 新`nativeCodexPlan`在权威 preflight 之后读取单一 revision/envelope，拒 extras，借 Secret8 私有 secretBytes 仅取 presence 构 A desired，解密 secret 总在作用域末 Clear。A 完整调用真实 PlanCodexProvider，事务 validator 逐项比较同 metadata/desired/HD/pin，不在写事务内跑 JS。Consume 借选中 key 仅 marshal 一次受控 slot3 响应后 Clear；不可换 revision/从其它 credential fallback。
- [ ] 同包新增`NativeService.PrepareCodex/AuthorizeCodex`，由 nativeconsumer.Service 窄转发；既有 Consume/Status 在认证 ConfigurationProtocol 之后调 Codex 或原 Claude 路径，始终在 asset/plugin 前验证对应 proof/token。未知/混合 record 不回退；没有新 caller hint、任意 Instance/注册层或第二套服务。
- [ ] HTTP 新增映射 helpers，现 handler 仅显式 dispatch 及 reply；codec 按实际 member presence 拒 Claude/Codex 混入，包括零 tag40/空 payload。客户端/服务端分别校所有嵌套 unknown/重复 singular/oneof、非法 enum/overlong varint/wiretype、false-vs-absent、role 精确三项、有序 packed/unpacked 列表及 field/value 预算；临时对象全校成功才替换。错误均沿原固定分类，不输出 parser/secret/DTO。
- [ ] 写`TestCodexNativeTLSLifecycle`、`TestCodexNativeStrictWire`、`TestCodexNativeTransportLifetime`及`TestCodexNativeHTTPPrivacy`：真 TLS+Connect+SQLite+MAC+Ed25519+moejs 全六 RPC、slot 精确原 bytes；各 body/header/未压缩预算、8slot/真实 20s 慢读慢写 H1/H2、取消/EOF/Close 真实 drain、jar/redirect/compression/Origin/GET/JSON 拒绝；记录真实计数，捕获 stdlog+slog/正常与错误响应。沿既有 20s 虚时/可控 I/O 屏障，不引入墙钟成功预算 fixture 竞态。
- [ ] `go test ./internal/server/configuration ./internal/server/auth ./internal/server/state ./internal/server/nativeconsumer ./internal/server/httpapi` focused Codex count20，再跑旧 Claude 六 RPC/transport/deadline/Close 全部相邻 tests。没有匹配/意外 skip/未建 web/dist 的 setup 失败不计产品 RED/PASS。

### Task 4: client codec + flow + 关闭的生产 D 准入

**Files:** configclient native_codec.go/native_flow.go 及新增 native_codex_flow.go/_test.go；必要 semantic dispatch 在 Task1 的 native_proof.go 串行补充。**Consumes:** Task1 独立 client encoder/pin、Task3 真实服务、已签 B accessors。**Produces:** 同 NativeFlow 的独立 Codex 状态/API，不改原 NativePreparation/NativeApproval 字段或任何 CLI/nativeconfig caller。

**接口：** `(*NativeFlow).PrepareCodex(ctx context.Context, selection NativeSelection) (CodexNativePreparation,error)`；`AuthorizeCodex(ctx context.Context, plan codexconfig.ProviderPlan, approval CodexNativeApproval) (CodexNativeAuthorization,error)`；`ConsumeCodex(ctx context.Context) (NativeMaterial,error)`；`StatusCodex(ctx context.Context) (NativeStatus,error)`，Close 沿旧接口。新增 preparation/approval/authorization 为 opaque 克隆 handle；approval 只表示精确 slot3 批准、最终批准、confirmation revision 及 intent expiry，不接受 caller HI/HA/bool 式“D 已验证”。trusted accessors 返回完整声明/绑定副本，均敏感。

`NewCodexNativeApproval(confirmationRevision uint64, intentExpiresAt time.Time, providerKeyApproved, finalApproved bool) (CodexNativeApproval,error)`仅在 revision>0、正 expiry 及两个明确批准均 true 时构造；Authorize 另校 expiry 不晚于 prepare/child/父期限。这些批准不是 OS/D/人类 attestation，不能填补生产准入缺口；没有自动批准、批准另一槽或直接传 commitment 的重载。

客户端 profile 证据 manifest 和 D 准入的生产来源**不在本片发明**：flow 私有 admission 初值为空，无导出构造器/setter/配置/环境开启；现`AuthorizeCodex`必须在网络/attempted 之前判定此门，缺来源固定拒绝。未来 D 签交后另授将其真实结果与原 B plan 注入的 producer 及 publish 前复核。新方法的完整协议核心用包私有`authorizeCodex`承载，`_test.go`仅以明确标记的 literal projection 进入该核心；不能把 fixture producer 编进产品或称为 D 证明。

- [ ] 写`TestCodexNativeClientFlow`，以 Task3 真实 TLS/SQLite（不是 mock handler）运行私有完整 Prepare/Authorize/Consume/Status 核心，完整 A/Binding/slot 原 bytes 独立比对；typed current-selection/assetrevision/pin/expiry 均不可自 rebase。client 只依赖中性 A/B 及 gen，不 import server/moejs；server 依赖只可出现在 integration `_test.go`。
- [ ] 写`TestCodexNativePreflightClosed`：public 生产入口遇零/外来 plan、合法形状 caller32B/完整 JSON、无正式 D/profile 来源时 Authorize=Consume HTTP 计数均 0；不得借 B 生产 unsupported 省掉上一条全部正控。无已签 NativeProfile 时不声称真正跑通 B.Check/Render：B 完整合成转换证据归其原包，本轮协议 fixture 不伪造其私有 plan。
- [ ] projection mapper 只读 B Binding 的 CopyDeclaration/CopyBaselines/CopyObservation 并复核与 prepared A 逐项一致；语义枚举按 spec 映射，不强转 B 底层 enum。manifest hash 必须来自未来正式身份回执且匹配 ID/模板，不能用 B.ProfileDigest 或 `hash(profileID)`替代；额外 ProfileDigest 保留在原 plan 的本机捕获快照，未来正式 profile 回执必须能与该元数据指纹核对，未有映射事实不解生产 gate，不加 wire 标签或发明 D 证据格式。保留原 plan 所有权/生命周期合同，后继 D 的材料接收只能送该捕获 plan.Render；没有 D 生产桥时这条实际 Render/publish 门仍 OPEN，不为测试修改 B/profile。
- [ ] native_flow.go 仅增私有 Codex 状态、agent 互斥 guard 及同一 Close 清理，复用 start/admission/sign/call/worker；首次准备后禁止跨 Claude/Codex 复用。Authorize/Consume 发送前 attempted 不可回退，丢回执/unknown 后仅原有有界 Status，不重发/重开 Bind/新建 grant，Status 不回材料。Close 取消并 join 真实 HTTP/Body/afterfunc 后才清自有 buffer，不在 UI 线程等待，不新造网络/crypto/key 初始化策略。
- [ ] native_codec/旧 proof semantic 入口增加明确 Codex 分支，不放开通用反射 root；raw DTO 一直敏感。写`TestCodexNativeClientStrictAndMixedVersions`，oldserver/newclient 与残缺/混 payload/newpin 拒绝且后继计数 0；旧 Claude/newserver 原 bytes/行为不变，含 packed/unpacked/所有 presence 及 512KiB/16/1024 边界。
- [ ] 写`TestCodexNativeClientUnknownAndClose`、`TestCodexNativePrivateContainers`：真网络丢 Authorize/Consume 回执、in-flight 取消/Close/晚回调，单次 material/无重放；fmt 全部 verb/flags/width/precision、typed nil、值/指针及私有多层容器、JSON/Text/stdlog/slog 均不泄露原文/hash/长度。clone 与 Destroy 相互独立，材料清理不破坏另一个 owned 副本。

### Task 5: 完整验收、compiled 负控与不可变交付

**Files:** 仅前四任务已有 tests/testdata 补强，无额外源码路径。**Consumes:** Tasks1 至 4 全部真实实现。**Produces:** exact33 freeze、命令退出/实际 run/skip、negative patch/compile/RED/restored 日志及剩余生产门。

- [ ] 自审 spec 每节覆盖：§1 至 3→Tasks0/1/3/4；§4 至 5→Task1 及 Tasks3/4 strict；§6/6.1 含 nonce fix1→Task2/3；§6.2→Tasks2/4；§7→全部；§8 混合/生成→Tasks1/3/4；§9 全门→本 Task；§10 所有旧/新 golden→Tasks1/2；§11 授权→Task0。任何 gap 先补实际测试，不删规格。
- [ ] 至少 12 类最终源码派生负控分别 compile0→独立原断言有限 RED→恢复 exact 源码 GREEN：漏 A false/catalog 字段或排序；漏 role/hash/manifest/template/witness；仅信 caller HI/32B；Unknown-auth fallback/绕 preflight；proof/token gate 移到 asset/plugin 后；Unknown 自动重放；漏 replace/terminal charge；raw DTO/parser/私有容器泄漏；撤/换认证 PrepareNonce；跳 rowdigest 重算；只比 HP 接受 differentnonce；漏 36B/真实 AEAD charge。保每次 patch/SHA/selector/退出/cleanup，不以编译错误/panic/timeout/空匹配/setup 当 RED。
- [ ] 所有新命名 selector count20/shuffle 实际非零 run、0 意外 skip；独立 server/client 逐 byte golden、真实 TLS/SQLite/子进程重启/两 Store 竞争/driver unknown 及旧 Claude suites 全部执行。fullGo/vet/modules、Buf 双模板完整 parity、CLI/MyGo client deps 与 CGO0 跨构建由主隔离整合复验，不用作者 focused 结果冒称主全量。
- [ ] 仅 exact33 导出逐 path A/M/G、mode/blob/SHA、固定 base/tree/tar/sourcepatch 及全部命令证据；generated 以正常 Buf 精确 parity 为准，手写 diff-check 须 clean，不手改标准 EOF。主其他 WIP 及 public/module/CI/README/14agent 文档保全。转非作者 Spec/Quality 审，作者不自签。
- [ ] 最终状态分报：协议/事务/client 私有完整核心的 qualified 验收，与生产 D/NativeProfile 门。后者 OPEN 时 public 生产敏感后继仍关闭，不能显示安装/Applied/全 Codex 支持；也不能借 OPEN 省掉前者的完整实现/独立验收。

## 2. 统一执行命令与停止条件

以下为 source 授权后的命令，不是本轮已运行的产品测试；从唯一固定 base 独立导出，Go 绝对工具链 1.27.3、CGO0、临时 HOME/TMP，保 GOPATH/module cache，禁止实际用户凭据与外网 provider：

```sh
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
CGO_ENABLED=0 go test -mod=readonly ./internal/nativesessionauth ./internal/configclient ./internal/server/state ./internal/server/auth ./internal/server/configuration ./internal/server/nativeconsumer ./internal/server/httpapi -run CodexNative -shuffle=on -count=20 -json
CGO_ENABLED=0 go test -mod=readonly ./internal/nativesessionauth ./internal/configclient ./internal/server/state ./internal/server/auth ./internal/server/configuration ./internal/server/nativeconsumer ./internal/server/httpapi ./internal/nativeconfig ./internal/configapply -count=1 -json
CGO_ENABLED=0 go vet -mod=readonly ./internal/nativesessionauth ./internal/configclient ./internal/server/state ./internal/server/auth ./internal/server/configuration ./internal/server/nativeconsumer ./internal/server/httpapi
go mod verify
buf lint
buf breaking --against "$BASELINE/proto"
GENERATED=$(mktemp -d)
buf generate -o "$GENERATED"
diff -ru gen "$GENERATED/gen"
buf generate --template buf.gen.web.yaml -o "$GENERATED"
diff -ru web/src/gen "$GENERATED/web/src/gen"
CGO_ENABLED=0 go list -deps ./cmd/cognate ./cmd/cognate-desktop
```

BASELINE 必须是 Task0 交付的旧协议固定导出，不是移动 HEAD；首次 proto 变更时比较正常生成 closure，最终命令对实施后的完整 gen 做 parity。每条保真实 exit 及 run/skip，&&链未执行项不算 PASS；archive 缺 ignored web/dist 先按现有 npm ci/build 生成再跑依赖该 embed 的 tests，setup 失败原样保留。无明确 sourcegrant、Secret8 共享 owner 未释放、闭包需额外路径、任一旧 Claude/安全门退化或原生 D/Profile 事实缺口被试图伪造时停止相应实现并报主；不自行改 spec/B/module/schema/依赖来迎合。

本轮仅本 plan 及 ignored 作者报告；不 Go/proto/module/index/stage/commit/push/spawn，不执行上列未来产品门。批准 plan 不等于授 33 源码路径或打开生产应用 gate。
