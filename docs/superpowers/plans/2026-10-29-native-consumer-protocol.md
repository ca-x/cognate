# Native configuration consumer protocol implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 冻结一个可实施的 Claude provider 原生秘密消费合同，使 CLI/MyGo 共用内核能够授权消费固定资产 revision；不新增 Web/MCP Reveal，不把下载成功当成本地应用成功。

**Architecture:** 既有 owner session 只用于两步 bootstrap；持钥绑定后签发短期 native-purpose 子会话，继承并持续复核父 session 的有效性。Prepare 只固定 revision、safe patch 和 preparation identity，不接收或声称完整 intent commitment；客户端收到 patch 后构造完整 IntentPlan，Authorize 首次原子绑定其 commitment，必要时签发最多 60 秒的一次性 grant。Consume 在同一 SQLite 写事务内复核身份、资产、adapter 和 grant，提交后最多输出一次敏感响应。状态查询永不重发秘密。

**Tech Stack:** 项目现有 Go 1.27.3、CGO=0、Connect/Protobuf/Buf、ent + entsqlite；建议使用标准库 Ed25519、SHA-256、HMAC、crypto/rand，无新增运行依赖。协议和新增存储方法均为提案，本文没有改 proto/Go/gen/module/index。

**Spec:** `docs/superpowers/specs/2026-10-28-configuration-native-apply.md` §§4–5、10；typed domain 与已落盘 provider patch 合同为依赖。原 spec §5 仍具约束力，§4 的本地 IntentPlan/selection 不变。

**修订证据：** 前一冻结稿 SHA-256 为 `289df36709fea8553f0ff4bd954feca33ceececfc6793dd8a1bf6cbe94aeadd1`，Carver 指出两项 P2：完整 commitment 先于唯一 server patch 不可构造，以及 FK 删除策略不完整可能阻断 RevokeSession。本次仅修正两阶段 DTO/proof/状态及完整删除矩阵，旧稿不是可实施签收；不新增通用 Reveal、attestation 或其他功能。

## Global Constraints

- 日期 2026-10-29，状态：**待主线程/typed RPC 作者签认的最小合同，不是实施或发布证据**。本轮只新增本文；不动 providerpatch、desktop 身份、UI/codec 或其他生产 WIP，不 stage/commit。
- 第一片仅当前 owner 的 `claude / provider / schema 1 / claude.settings / user`；其它 scope/agent/Pack 附件一律 unsupported，不能据此缩减完整 F12 首发矩阵。
- 生产认证 TLS、固定同 origin、不跟随跨 origin redirect；仅合成 loopback 测试允许显式 HTTP。无 cookie 认证、无 URL token、无 body tracing；native session/grant/秘密只入 Go 敏感 transport/wrapper。
- Preview/IntentPlan 最长 5 分钟；grant 最长 60 秒、一次性、绑定 exact session；响应丢失不得重放。Prepared 之后更新资产/注销不能回收已经交付的 bytes，服务端与本地磁盘无分布式原子提交。
- 客户端没有 server/plugin/moejs 依赖；服务端 plugin 只生成无秘密 patch。消费的 secret 直接交本机 Go 内核，不进 Web view、MCP、剪贴板或普通日志。
- 持钥 proof **不是二进制 attestation、不是用户实际点击的证明、不是本地文件状态证明**。服务端只绑定 opaque commitment；selection、确认、TTL、文件 CAS 仍由原生内核执行。

## Review Focus

1. Logout/资产更新与 Consume 的交错必须由数据库写事务排序，不能在 handler 认证一次后只携带 owner。
2. COMMIT/响应丢失只能得到未知结果或 consumed 状态，不能靠幂等缓存重发敏感 envelope。
3. native-purpose 子会话不是普通 bearer 的自报 header；普通 session、MCP/Pack 凭据和其他子会话不可消费该 grant。
4. 编码歧义、重复字段/slot、未知 nested wire、签名用途串用必须拒绝；真实 proto DTO 的 fmt/slog 不安全，不能靠 debug_redact 自我安慰。
5. 持钥协议不能区分持有效 owner 凭据的恶意程序和官方客户端；不得用 CORS/UA 来掩盖此信任边界。

## 1. 实际代码接缝与缺口

本轮只读核对以下当前文件；并行 WIP 会变化，实施时应重新定位，不将本计划当生产 frozen tree：

| 当前入口 | 已有能力 / 本片不能假定的能力 |
| --- | --- |
| `internal/server/auth/service.go` `sessionDigest` / `Authenticate` | 私有 sessionDigest 校验现有 87 字符 token 的 MAC，并得到 token SHA-256；Authenticate 调 SessionUser 后只返回 owner。不能凭 owner 找回究竟是哪一个 session，更不能在消费时复核它。 |
| `internal/server/state/accounts.go` `SessionUser` / `RevokeSession` | SessionUser 是单次有效 session 查询；RevokeSession 通过 token hash 删除行。现有语义无 purpose/key binding/native 子会话。登录新 session 不自动撤销所有旧 session，不能虚称已有全局 session rotation。 |
| `internal/server/ent/schema/session.go` | session 有 owner FK、token_hash、issued/expires；无 native purpose、公钥或一次性 grant。建议新增子表，不改现有 token/wire。 |
| `internal/server/state/configuration.go` `ReadConfigurationAssetVersion` | 同一行返回 metadata + 经 owner/revision AAD 解密的 payload；但是经 `store.state.client` 读取，不接受当前消费事务，不等于“session+grant+asset”原子读取。 |
| 同文件 `UpdateConfigurationAsset` / `DeleteConfigurationAsset` | 使用 catalogTransaction 和 revision CAS。新消费事务必须与这些写事务和 Logout 由 SQLite 排序，不能只用 Go mutex。 |
| `internal/server/state/catalog_transaction.go` | 最多四次 SQLITE_BUSY 重试；回调可能多次执行，提交错误不一定等于没提交。敏感结果不能在回调内发送，commit uncertain 不可盲重跑。 |
| `internal/server/configuration/{domain,types,envelope}.go` | load/Edit 内部解码并只给 safe view；Store 接口是现有 CRUD。它已经依赖 state，故 state 不能反向 import configuration。新增独立服务/接口，不能把普通 Edit 改为 Reveal。 |
| `internal/server/agentadapter/{provider_patch,registry}.go` | 现有 `PlanClaudeProvider` 接受 shared typed desired，不收 secret；registry 为不可变构造、校验 exact API/version/digest/capability。definition digest 是规范化 definition（含 manifest+module）的 SHA-256 hex，不是版本字符串或 manifest-only hash。 |
| `internal/server/httpapi/{auth,configuration,guard,configuration_codec}.go` | authenticate 丢弃 session 身份；配置 handler 使用 8 slots / 20s，普通 CRUD 128 KiB 请求限制。guard/codec 的 strict-wire 能复用模式，但不能假定当前并行 codec 修复已完成，更不能擅改其 WIP。 |
| `proto/skillhub/configuration/v1/configuration.proto` | 现有 CRUD service/编号保留；generated DTO 不会因 debug_redact 在 Go 下自动安全打印。 |

## 2. 最小新增 wire 合同（拟冻结编号）

新文件建议 `proto/skillhub/configuration/native/v1/native.proto`，package `skillhub.configuration.native.v1`；Go package `github.com/ca-x/cognate/gen/skillhub/configuration/native/v1;configurationnativev1`。保留历史 skillhub wire namespace，不因 Cognate 改名另破兼容。本文代码块是提案，**不是生成物**；所有标签只用于新消息，不占用既有 proto 编号。

只支持 Connect unary protobuf；本切片拒绝 JSON、gRPC-Web、GET/query 编码与压缩请求，不给 Web/MCP 生成业务调用入口。HTTP route 是 `/skillhub.configuration.native.v1.NativeConfigurationService/<Method>`；bootstrap 两方法用现有 owner bearer，其余只接受新 native bearer。所有响应设置 `Cache-Control: no-store`，拒绝浏览器 Origin/Sec-Fetch 调用、不开 CORS；这些是缩小暴露面，不替代身份或 attestation。

```proto
syntax = "proto3";
package skillhub.configuration.native.v1;
option go_package = "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1;configurationnativev1";
import "skillhub/configuration/v1/configuration.proto";

service NativeConfigurationService {
  rpc BeginNativeConsumer(BeginNativeConsumerRequest) returns (BeginNativeConsumerResponse);
  rpc BindNativeConsumer(BindNativeConsumerRequest) returns (BindNativeConsumerResponse);
  rpc PrepareNativeApply(PrepareNativeApplyRequest) returns (PrepareNativeApplyResponse);
  rpc AuthorizeNativeMaterialization(AuthorizeNativeMaterializationRequest) returns (AuthorizeNativeMaterializationResponse);
  rpc ConsumeNativeMaterialization(ConsumeNativeMaterializationRequest) returns (ConsumeNativeMaterializationResponse);
  rpc GetNativeMaterializationStatus(GetNativeMaterializationStatusRequest) returns (GetNativeMaterializationStatusResponse);
}

enum NativePurpose { NATIVE_PURPOSE_UNSPECIFIED = 0; NATIVE_PURPOSE_CONFIGURATION_APPLY = 1; }
enum Target { TARGET_UNSPECIFIED = 0; TARGET_CLAUDE_SETTINGS = 1; }
enum Scope { SCOPE_UNSPECIFIED = 0; SCOPE_USER = 1; }
enum SecretSlot { SECRET_SLOT_UNSPECIFIED = 0; SECRET_SLOT_CLAUDE_API_KEY = 1; SECRET_SLOT_CLAUDE_AUTH_TOKEN = 2; }
enum EnvSlot {
  ENV_SLOT_UNSPECIFIED = 0;
  ENV_SLOT_API_KEY = 1; ENV_SLOT_AUTH_TOKEN = 2; ENV_SLOT_BASE_URL = 3;
  ENV_SLOT_MODEL = 4; ENV_SLOT_HAIKU = 5; ENV_SLOT_SONNET = 6; ENV_SLOT_OPUS = 7;
}
enum GrantState { GRANT_STATE_UNSPECIFIED = 0; GRANT_STATE_NOT_ISSUED = 1; GRANT_STATE_ISSUED = 2; GRANT_STATE_CONSUMED = 3; GRANT_STATE_EXPIRED = 4; GRANT_STATE_RETIRED = 5; }

message BeginNativeConsumerRequest {
  NativePurpose purpose = 1;
  bytes operation_id = 2;
  bytes consumer_public_key = 3;
  bytes client_nonce = 4;
  string expected_owner_id = 5;
  string expected_server_origin = 6;
}
message BeginNativeConsumerResponse {
  string challenge_id = 1;
  bytes challenge = 2;
  bytes native_session_binding = 3;
  string owner_id = 4;
  string server_origin = 5;
  int64 challenge_expires_at_unix_ms = 6;
  int64 native_session_expires_at_unix_ms = 7;
}
message BindNativeConsumerRequest { string challenge_id = 1; bytes proof_signature = 2; }
message BindNativeConsumerResponse {
  bytes native_session_token = 1 [debug_redact = true];
  bytes native_session_binding = 2;
  int64 expires_at_unix_ms = 3;
}
message AdapterBinding {
  uint32 api_version = 1;
  string plugin_id = 2;
  string plugin_version = 3;
  string definition_digest = 4;
  repeated string required_capabilities = 5;
}
message PreparationBinding {
  string owner_id = 1;
  string server_origin = 2;
  bytes native_session_binding = 3;
  bytes operation_id = 4;
  reserved 5;
  string asset_id = 6;
  int64 asset_revision = 7;
  string agent_id = 8;
  skillhub.configuration.v1.AssetKind asset_kind = 9;
  uint32 schema_version = 10;
  AdapterBinding adapter = 11;
  Target target = 12;
  Scope scope = 13;
  string profile_id = 14;
  uint64 selection_revision = 15;
  repeated SecretSlot required_slots = 16;
  int64 prepare_expires_at_unix_ms = 17;
  bytes consumer_key_sha256 = 18;
  string prepare_id = 19;
  int64 prepared_at_unix_ms = 20;
  bytes patch_sha256 = 21;
  reserved 30 to 39;
}
message ApplyBinding {
  PreparationBinding preparation = 1;
  bytes intent_commitment = 2;
  int64 intent_expires_at_unix_ms = 3;
}
message SetLiteral { string value = 1; }
message SetSelectedSecret { SecretSlot slot = 1; }
message RemoveEnv {}
message PatchOperation {
  EnvSlot slot = 1;
  oneof action { SetLiteral set_literal = 2; SetSelectedSecret set_selected_secret = 3; RemoveEnv remove = 4; }
}
message ClaudeProviderPatch { repeated PatchOperation operations = 1; }
message PrepareNativeApplyRequest {
  string expected_owner_id = 1;
  string asset_id = 2;
  int64 expected_asset_revision = 3;
  string agent_id = 4;
  uint32 schema_version = 5;
  AdapterBinding adapter = 6;
  Target target = 7;
  Scope scope = 8;
  string profile_id = 9;
  uint64 selection_revision = 10;
  bytes operation_id = 11;
  reserved 12;
  bytes client_nonce = 13;
  bytes proof_signature = 14;
  reserved 15;
}
message PrepareNativeApplyResponse {
  PreparationBinding binding = 1;
  ClaudeProviderPatch patch = 2;
  bool materialization_required = 3;
  bytes authorize_challenge = 4;
}
message ExplicitConfirmation { uint64 confirmation_revision = 1; }
message AutoPolicy { bytes policy_id = 1; uint64 policy_revision = 2; }
message AuthorizeNativeMaterializationRequest {
  string prepare_id = 1;
  bytes operation_id = 2;
  bytes intent_commitment = 3;
  repeated SecretSlot requested_slots = 4;
  oneof authorization { ExplicitConfirmation explicit_confirmation = 5; AutoPolicy auto_policy = 6; }
  bytes client_nonce = 7;
  bytes proof_signature = 8;
  int64 intent_expires_at_unix_ms = 9;
}
message AuthorizeNativeMaterializationResponse {
  string grant_id = 1;
  bytes grant_token = 2 [debug_redact = true];
  bytes consume_challenge = 3;
  int64 expires_at_unix_ms = 4;
  ApplyBinding binding = 5;
  bool materialization_required = 6;
}
message ConsumeNativeMaterializationRequest {
  string prepare_id = 1;
  string grant_id = 2;
  bytes grant_token = 3 [debug_redact = true];
  bytes operation_id = 4;
  bytes intent_commitment = 5;
  bytes client_nonce = 6;
  bytes proof_signature = 7;
}
message SlotValue { SecretSlot slot = 1; bytes plaintext = 2 [debug_redact = true]; }
message ConsumeNativeMaterializationResponse {
  ApplyBinding binding = 1;
  repeated SlotValue slot_values = 2;
  string grant_id = 3;
  int64 consumed_at_unix_ms = 4;
}
message GetNativeMaterializationStatusRequest {
  string prepare_id = 1;
  string grant_id = 2;
  bytes operation_id = 3;
  bytes client_nonce = 4;
  bytes proof_signature = 5;
}
message GetNativeMaterializationStatusResponse {
  GrantState state = 1;
  string grant_id = 2;
  int64 expires_at_unix_ms = 3;
  int64 consumed_at_unix_ms = 4;
}
```

### 2.1 字段校验与 patch 映射

- 新 opaque ID 限 1–64 字节 ASCII `[A-Za-z0-9_-]`，profile_id 同限；asset_id 沿现有 validPackKey 规则，owner_id 沿既有身份 ID，不扩大格式。origin 最多 2048 字节，plugin ID/version/capability 各沿 registry 规则且分别不超过 64/32/64 字节。operation_id 固定随机 16 bytes；client nonce、challenge、session binding、commitment、consumer key hash、grant/native token 均 32 bytes；Ed25519 公钥 32/signature 64 bytes。私钥不上传。native bearer 是 `cn1.` + raw-URL-base64(token)，严格 canonical；独立 digest domain，不走现有 87 字符 owner token parser。
- owner、origin 由服务端核定；请求 expected 值必须精确匹配。origin 取可信部署配置的 canonical origin，不能拿任意 Host/X-Forwarded-Host 当 authority；保留端口，拒绝 userinfo/query/fragment/path，不因 DNS/IP 相似而等价。生产未配置可信 origin 则关闭本服务。
- agent=`claude`、kind=PROVIDER、schema=1、target=CLAUDE_SETTINGS、scope=USER；revision/selection/confirmation/policy revision 均正数。profile_id 为本地随机不透明标签，不发 HOME/文件路径。
- adapter API=1，plugin=`builtin.claude`、version=`1.1.0`，digest 为 registry 产生的 64 位 lowercase hex；required_capabilities 仅 `claude.provider-switch.v1`，精确列表、不允许 additive fallback。实际 digest 不预填，跟随最终经过验证的 definition。
- Patch 恰有七个唯一 EnvSlot，按编号排序。literal 只允许 BASE_URL 和四个模型；两个 auth env 只允许 SetSelectedSecret/Remove。SecretSlot 映射到 shared `claude.api-key` / `claude.auth-token`。无秘密原文、无旧文件内容，不回传 RenderResult.Content。
- required_slots 由同 revision typed auth mode 推导：api-key 恰 `[CLAUDE_API_KEY]`、auth-token 恰 `[CLAUDE_AUTH_TOKEN]`、native-login 空；requested_slots 必须精确相等，重复/多要/少要都拒绝。native-login 也用 Authorize 完成第二阶段 intent 绑定，但其响应 materialization_required=false、仅返回 ApplyBinding，字段 1–4 必须为空/0，不创建 grant；Consume 返回 FailedPrecondition，不凭空造空 grant。仅有 Prepare 不能应用，包括 native-login。
- 时间均正 int64 Unix milliseconds。Prepare 不接收 intent expiry，固定 prepare expiry=`min(prepared+300s,native session expiry,parent expiry)`。客户端收到 patch 才能构造完整 IntentPlan，其 expiry 不晚于 prepare expiry 和本地 preview TTL。Authorize 请求必须满足 `now < intent expiry <= prepare expiry`，服务端精确绑定该值、不修改延长；grant expiry=`min(authorize+60s,intent expiry,native session expiry,parent expiry)`，严格 `now < expiry`。不足完整 60s 可得到更短 grant，绝不靠重试延寿；本机独立检查原 plan expiry。
- PreparationBinding 明确不是完整 IntentPlan：没有 commitment/文件身份/base hash。其中 patch_sha256 仅覆盖无秘密 patch，不是含秘密整文件 hash。ApplyBinding 只在 Authorize 成功后存在。该 proto 尚未实现，修订移除的 PreparationBinding 5、Prepare request 12/15 保留为 reserved，禁止接受旧阶段字段来绕过新合同。
- 状态请求 grant_id 允许空，仅用于 Authorize 响应丢失时查询该 prepare 唯一 grant；其余 ID 不可空。所有 enum=0/未知值、unknown field、重复 singular/oneof、未知 nested wire、非规范签名输入拒绝；不为 forward compatibility 忽略鉴权字段。
- 请求/响应各 512 KiB 上限，Echo MaxBytesReader + Connect + 严格 codec 同时设置；bootstrap 建议额外收紧到 8 KiB。8 slots/20s deadline，解码 recursion 16，metadata/header 另限额；全 service 不改现有 CRUD 128 KiB 预算。

## 3. proof 协议与目的隔离

**签名编码不使用 JSON，也不直接签 proto.Marshal**。固定协议版本 1：`U32/U64` 为 big-endian，所有字符串为原样 UTF-8 `B(x)=U32(len(x))||x`，枚举为 U32，正时间/revision 为 U64，bytes 同 B；列表为 U32(count) 加逐项编码，不设 map。拒绝非规范列表顺序/重复，不做静默 Unicode/URL 规范化。

`Sig = Ed25519.Sign(key, B("cognate/native-config-proof/v1") || B(method-purpose) || U32(1) || fields)`，Ed25519 签原串，不另套可混淆的隐式 hash API。每个签名 purpose 固定为以下之一：

| purpose | 有序 fields（B/U 由字段类型决定） |
| --- | --- |
| `bind` | server_origin, owner_id, purpose enum, operation_id, consumer_public_key, Begin client_nonce, challenge_id, challenge, native_session_binding, challenge_expiry, native_session_expiry |
| `prepare` | server_origin, owner_id, native_session_binding, operation_id, consumer_key_sha256, Prepare request 1–11、13（按编号，不含 signature 14；12/15 reserved 不编码） |
| `authorize` | H(preparation), authorize_challenge, prepare_id, operation_id, intent_commitment, requested_slots, authorization tag（explicit=1/auto=2）及其消息字段, client_nonce, intent_expires_at_unix_ms |
| `consume` | H(apply), authorize_challenge, grant_id, H(grant_token), consume_challenge, grant_expiry, operation_id, intent_commitment, client_nonce |
| `status` | H(preparation), authorize_challenge, prepare_id, grant_id（空也编码）, operation_id, client_nonce |

`H(preparation)=SHA256(B("cognate/native-config-preparation/v1") || encode(PreparationBinding))`，按字段 1–4、6–21 的编号递归编码，reserved 5 不编码；AdapterBinding 1–5 按编号递归编码。`H(apply)=SHA256(B("cognate/native-config-apply/v1") || B(encode(PreparationBinding)) || B(intent_commitment) || U64(intent_expiry))`。时间按 U64，其余 enum/uint32 用 U32、uint64 用 U64、字符串/bytes 用 B、列表用计数规则。consent oneof 中 explicit 为 U64(confirmation_revision)，auto 为 B(policy_id)||U64(policy_revision)。两个 binding hash 不混用，Status 在未授权时也能签名，不需要尚不存在的 commitment。

`patch_sha256=SHA256(B("cognate/native-config-patch/v1") || U32(target) || U32(operation_count) || operations)`；每个 operation 按 slot 排序，编码 U32(env_slot)、U32(action_tag)，set_literal tag=1 后接 B(value)，set_selected_secret tag=2 后接 U32(secret_slot)，remove tag=3 后无内容。必须先校验七槽完整、oneof 唯一、无多余字段再编码。客户端只校验通用 patch schema/能力和此 hash，不推导 typed provider→patch、不实现 moejs 映射。

`H(grant_token)=SHA256(raw token)` 仅用于签名绑定，不作秘密日志字段。所有 grant/Consume 中的 owner/session/operation/consumer key/origin/revision/adapter/patch/commitment 都由事务内已存 preparation + 首次授权 ApplyBinding 重构；不能接受另一个 preparation 的 commitment。Authorize proof 同时签 H(preparation)、完整 commitment、expiry、slot 和 consent，防跨 patch 或跨确认替换。

Begin 的 challenge 最长 60s 且绑定 exact 父 session/owner/公钥/operation，Bind 原子使用一次。Prepare 以子会话 binding 和唯一 client nonce 防跨会话重放；返回的 authorize challenge 仅能为该 prepare 签发一个 grant。Authorize 与 Consume 的 challenge 不能串用；重复 Consume 即使完全相同 proof 也不重发秘密。Status 是只读重放安全方法，重用其签名不会改变授权或返回秘密，不消耗 grant。

Intent commitment 仅在收到并校验 Prepare 的唯一 patch 后由原生内核用本机随机密钥 HMAC-SHA256 计算，绑定原 spec §4 的完整 IntentPlan（包括 H(preparation)、完整受管 slot 操作/必要 secret references、本地身份、base hash、selection、confirm/auto-policy revision 和 expiry），仅上传 32 bytes。Authorize 前不存在完整 commitment，不能拿占位值签最终意图；服务端不能验证本地路径/文件状态。不要把含秘密整文件 hash 发给服务器充当 commitment。

### 3.1 不能被忽略的 native 身份限制

Bind 返回独立目的 token；普通 owner bearer 只能 Begin/Bind，不能 Prepare/Authorize/Consume/Status。子会话绑定父 session 行、公钥、单 operation、origin 和目的，不能由 request header `Native: true`、User-Agent、agent 名或普通 SessionUser(owner) 构造。MCP/Pack/匿名凭据不允许 bootstrap。

这是 **owner 显式授予的持钥 capability**，不是“服务器知道这是官方原生二进制”。拥有 owner bearer 且可以创建自己的 key 的恶意程序能尝试同一 bootstrap；WebCrypto 也能产生签名。产品 Web/MCP 不接此 API、不获得 token/私钥/秘密，加上无 CORS/拒绝 browser metadata，能防现有 Web 路径误用，**不能承诺对任意恶意同权限客户端的远程鉴别**。若产品要求即使持有有效 owner 凭据的恶意浏览器/程序也绝不能自建 consumer，必须新增外部可信的注册/批准或 attestation 合同，不能在本片伪称已解决。

建议最小签认：允许 owner 在 CLI/MyGo 明确确认后 bootstrap，信任其本机密钥/selection 管理；不把网络端自报“确认”当硬件用户存在证明。auto-policy 是本机已经明确授权的政策断言，服务端仅绑定其 ID/revision，不声称独立核验了本机政策。本片签认前默认关闭服务。

## 4. 存储模型及精确新方法提案

### 4.1 新增最少状态（未来独占写集才实施）

1. `NativeConsumerChallenge`：opaque ID、parent Session FK、owner FK、origin、purpose、operation、公钥、nonce/challenge、预分配 session binding、两种 expiry、used_at。Begin 每 session 有界（建议 8 活项），过期清理、登录级 rate budget。
2. `NativeConsumerSession`：opaque ID、parent Session FK（ON DELETE CASCADE）、owner FK、purpose、origin、operation、公钥/hash、binding UNIQUE、token digest UNIQUE Sensitive、issued/expires、mutable gate_version。只存 token digest，不存 native token。
3. `NativeApplyPreparation`：opaque ID、native session FK、owner FK、operation、完整 PreparationBinding（含 patch hash）、无秘密 validated patch、authorize challenge、请求 nonce、expiry、状态；首次 Authorize 后另存不可变 ApplyBinding/consent。UNIQUE(native_session_id,operation_id)，一个 operation 只有一个 preparation，不用更新其 revision/patch 来续期。数据中的用户字段仍非 safe-to-log，必要时按现有主密钥派生专用 AEAD 加密；无秘密原文。
4. `NativeMaterializationGrant`：opaque ID、prepare FK UNIQUE、native session/owner FK、随机 token digest Sensitive、consume challenge、全部绑定摘要/slot、授权类型及 revision、issued/expires、consumed_at/retired_at。数据库不存 grant token、响应缓存或秘密 envelope。

子会话最长 5 分钟且只服务一个 operation；父 session 到期/删除立即使它不可用。新 owner 登录不会自动废止其他 session；“换 session 不可复用 grant”指 grant 必须匹配原 exact session 关系，不冒称全账号轮换已实现。用户 logout 仍由现有 RevokeSession 删除父行，事务内查询+FK 保证子会话一起失效。key 换了必须新建子会话，不能更新旧记录绕过绑定。

### 4.1.1 完整 FK / 删除 / 状态保留矩阵

下表是本片必须生成的真实 SQLite FK，不是 ent 回调清理约定。四表所有新增 FK 均显式 `ON DELETE CASCADE`，不允许遗漏成默认 NO ACTION/RESTRICT/SET NULL；迁移/每条连接必须启用 foreign_keys。新增表除下列关系外不加隐藏 FK：

| 子表 / FK | 被引用表 | 删除效果 |
| --- | --- | --- |
| NativeConsumerChallenge.parent_session_id | Session.id | 删父 session 时删除全部 challenge（含 used/expired）。 |
| NativeConsumerChallenge.owner_id | User.id | owner 真正删除时清除 challenge。 |
| NativeConsumerSession.parent_session_id | Session.id | 删父 session 时删除全部 native 子会话。 |
| NativeConsumerSession.owner_id | User.id | owner 真正删除时清除 native 子会话及其后代。 |
| NativeApplyPreparation.native_session_id | NativeConsumerSession.id | 删子会话时删除全部 preparation（含已授权/过期/retired）。 |
| NativeApplyPreparation.owner_id | User.id | owner 真正删除时清除 preparation 及其后代。 |
| NativeMaterializationGrant.prepare_id | NativeApplyPreparation.id | 删 preparation 时删除 grant，包括 consumed 状态记录。 |
| NativeMaterializationGrant.native_session_id | NativeConsumerSession.id | 删子会话时清除该会话全部 grant；和 prepare 路径是有意的双 cascade、无环。 |
| NativeMaterializationGrant.owner_id | User.id | owner 真正删除时清除 grant。 |

NativeConsumerSession **不反向引用**已使用的 Challenge，删除 challenge 不撤销已创建子会话；challenge 只记录 used，不保留对子会话的阻塞 FK。prepare 的 asset ID/revision、adapter identity 仅逻辑绑定，不引用活 asset/registry 的 FK，资产删除不受 preparation 阻挡，随后授权/消费重核失败。所有冗余 owner/native/prepare 关系必须在写入和消费事务中核对相等，不能因为各 FK 单独合法就跨 owner 关联。

| 动作 | 精确清理及可观察状态 |
| --- | --- |
| RevokeSession / logout / 显式撤销一个父 session | 保持现有 `DELETE Session WHERE token_hash=...` 真实删除；同一数据库语句的 cascade 清除该父会话 challenge/native/preparation/grant。不能改成假成功 soft revoke、不能要求 HTTP 手动清四表。其它 owner/同 owner 的其它 session 不受影响；旧 native Status 随即 Unauthenticated。 |
| 删除 native 子会话 | CASCADE preparation/grant；其父 Session、同父其他 native 子会话、challenge 不反向删除。 |
| owner 删除 | 四表对 User 均 CASCADE，不引入新阻挡。本仓库旧表已有各自删除约束，本片不宣称现在任意 `DELETE User` 即可成功；账号删除事务须按既有约束先清理其 Session 等旧子表，再删 User。新表应由上述 cascade 清理，无额外手工补洞；owner delete 回滚也整体回滚。 |
| challenge cleanup | used challenge 可按有界批次删除；unused 仅过期后删除。已创建子会话不会因清 challenge 失效。删除只按自有 ID/expiry，不能清其他 owner 活跃 challenge。 |
| 过期 session cleanup | 父 session 到期时即鉴权失效，不等 cleanup；之后删除该过期 Session 复用同一整链 cascade。native session 自身到期可单独删除，不能删除仍有效的父 session。 |
| preparation/grant 状态保留 | 父/子会话仍有效时，保留 consumed/expired/retired 元数据供有权 Status 查询；无消费响应/secret 缓存。不得为了保存 consumed 历史阻止 logout 删除。子会话过期/撤销/owner 删除后可整链丢弃，Status 不保证长期可查；在 live 子会话内最多 8 个绑定状态项，满则 ResourceExhausted，不无限保留。 |

本片不新增第五张 audit 表，也不为保留审计改 existing MCP audit。运维状态日志只能保留随机 event ID、固定阶段/结果码、时间/计数，无 FK，不持有 session/grant/owner/asset/commitment/patch/slot value/公钥或 proof 内容，不得用日志重建消费响应或恢复授权。它不阻止任何 DELETE、不提供已注销会话的公开状态查询，不承诺长期用户级审计；长期审计需求另立合同。

验收必须在真实 SQLite 中用 `PRAGMA foreign_key_list` 核对全部九条 FK 的 on_delete，`foreign_key_check` 为零，并对装满四表（含 consumed grant）的父 session 调用**现有 RevokeSession**，断言 DELETE 成功、全链为零、再调用仍幂等；不能只 mock logout 返回值。另测 native/prepare/challenge 单独删除、过期清理、多级双路径 cascade、owner 按既有顺序真实删除、事务回滚及另一 owner/session 完整保留。上述均为待实现测试，本文没有运行这些测试。

### 4.2 建议新增 Go 接口（名字/责任需一起冻结）

以下均**尚不存在**。credential/materialization 参数是私有 bytes、redacted String/GoString/LogValue 的 server-side wrapper，不是 proto DTO；state 不反向 import configuration，也不 import gen 或 agentadapter/plugin。

```go
// auth.Service：保留精确父 session 证据，不仅返回 owner。
OwnerSessionCredential(ctx context.Context, raw string) (state.OwnerSessionCredential, error)

// state.Store：每次从 DB 重查 credential，而不是相信预检得到的 owner。
BeginNativeConsumer(ctx context.Context, parent OwnerSessionCredential, input NativeConsumerStart) (NativeConsumerChallenge, error)
BindNativeConsumer(ctx context.Context, parent OwnerSessionCredential, input NativeConsumerBind) (NativeConsumerBound, error)
PrepareNativeConfigurationApply(ctx context.Context, consumer NativeConsumerCredential, input NativePrepareInput, validate NativePrepareValidator) (NativePrepared, error)
AuthorizeNativeConfigurationGrant(ctx context.Context, consumer NativeConsumerCredential, input NativeAuthorizeInput, validate NativeReadinessValidator) (NativeAuthorizationResult, error)
ConsumeNativeConfigurationGrant(ctx context.Context, consumer NativeConsumerCredential, input NativeConsumeInput, materialize NativeMaterializer) (NativeMaterialization, error)
NativeConfigurationGrantStatus(ctx context.Context, consumer NativeConsumerCredential, input NativeStatusInput) (NativeGrantStatus, error)

// state 内部 helper，只给上述事务用，不暴露 ent.Client 给 domain。
readConfigurationAssetVersionTx(ctx context.Context, tx *ent.Tx, owner, id string) (ConfigurationAsset, ConfigurationPayload, error)
```

- OwnerSessionCredential：auth 先沿现有私有 sessionDigest 验 MAC/格式，wrapper 保留 32-byte digest；state 再按当前 tx 查父 session。不得从 owner 反查任一 session，亦不得把 digest 发给客户端。原 Authenticate/普通 CRUD 不必改变返回值。
- NativeConsumerCredential：native handler 严格解析 `cn1` token 后得 domain-separated digest，state 查 native session + 父 Session；无 ordinary-owner fallback。
- `NativeConsumerBind` 含 challenge ID/proof 和 **服务端生成**的新 token digest；raw token 仅 handler service 内存保留，Bind 提交成功后才响应。回复丢失无法重取 token，重新 Begin/Bind；不在 DB 加 token 明文缓存。
- `NativePrepareValidator = func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativePreparedData, error)`；只做 bounded typed 解码、readiness、与预计算 patch 的纯比较。moejs Plan 在事务外用固定 registry 运行，再在事务内重读 exact revision 并核对 typed desired/patch，不让 plugin/网络进入写事务。
- `NativeReadinessValidator = func(context.Context, ConfigurationAsset, ConfigurationPayload) error`；Authorize 再核对 fixed revision/required slots/readiness，不能只信 Prepare 旧状态。
- NativePrepared 仅含 PreparationBinding/patch/challenge，无 commitment；NativeAuthorizeInput 才首次携带完整 commitment/intent expiry/consent。NativeAuthorizationResult 返回精确 ApplyBinding 和可选 grant 元数据；native-login 为已绑定但无 grant 的结果，不强迫制造空 grant。
- `NativeMaterializer = func(context.Context, ConfigurationAsset, ConfigurationPayload, NativeGrantBinding) (NativeMaterialization, error)`；在事务内纯解码、只选 required slot，并构造有界敏感响应。NativeMaterialization 是私有编码 bytes 的 wrapper；由 HTTP service 闭包使用 native proto 编码，不使 state 依赖 proto，且只在提交确认后允许 Reveal。回调不能做 IO、调用 plugin、发响应或记录 payload。
- 新内部 `configuration` consumer decode helper 复用 envelope 解码/readiness/Secret，不拓宽 public EditView。unsupported metadata 返回 FailedPrecondition，不能把 unknown agent/schema 的 payload 当原始文件下发。
- shared immutable registry 由 coordinator 在事务前取得 generation/read lease；整个消费持 lease，state input 携带可信 adapter identity 并与已存 grant 精确比较。当前 registry 不变更可直接固定该实例；未来热更新必须与 lease 排序，不能信客户端说 digest 仍有效。

## 5. 消费事务：真正的线性化入口

不能实现成 `Authenticate → ReadConfigurationAssetVersion → markConsumed → 回应` 四个独立动作。建议 `ConsumeNativeConfigurationGrant` 内执行以下**同一写事务**：

1. 校验 ctx/长度，开始 tx。首先对匹配 credential 的 native session 执行条件 `UPDATE gate_version=gate_version+1`，谓词同时限制 purpose/origin/operation/公钥绑定、parent FK、父/子 session 当前时间有效；影响行必须恰为 1。读取前先取得 SQLite writer 权，避免 deferred read 后升级产生 stale snapshot。gate_version 溢出 fail closed，不靠递增值做跨机器时钟。
2. 同 tx 读 parent Session + owner，确认 issued≤fresh now<expires，记录 exact owner。Logout 先提交则步骤 1 失败；消费先取 writer/提交则 Logout 随后生效，两者由数据库排序。不可用 process mutex 代替；也不假装 SQLite 有 SELECT FOR UPDATE 行锁。
3. 读取 grant/prepare，按当前 native session、owner、operation、commitment、origin、adapter、slot、expiry 重核，常量时间比较 token digest；核验消费 proof。错误只给稳定分类，不回显请求或证明材料。
4. 调事务内 helper 一次读取 asset metadata+ciphertext，以原 AAD 解密，比较 asset ID/owner/revision/kind/schema 与 grant；用纯 typed callback 重新验证 readiness、确切 secret slot、patch 兼容性，构造不超过 512 KiB 的敏感 wire bytes。任何失败回滚、清空临时 plaintext，grant 仍未 consumed。
5. 在读取/锁等待/回调后再次采样服务端时钟，复核父子 session、plan、grant expiry 及有效状态。以未 consumed/未 retired/尚有效为谓词 CAS 写 consumed_at，影响行恰 1。紧接着 COMMIT，不在这两步之间执行网络/plugin/外部回调。
6. 只有 COMMIT 明确成功的这一次调用能从私有 wrapper 返回 encoded response。commit error、ctx 取消或未知结果销毁本地敏感 buffer，返回稳定 `MaterializationUnknown`/取消；以后只准 Status，无响应缓存、无 replay。

时间有效性定义在**事务最终授权判定时刻**，使用服务端 fresh clock，不把请求进入时间一路复用。判定之后不可避免的 COMMIT/传输时延不等于“签名在每个纳秒持续有效”；需要防长阻塞则约束 tx deadline/剩余有效期，不声称能在网络对端接收瞬间撤回 bytes。

重试不能直接照搬 catalogTransaction 外置 result 捕获：仅允许明确回滚的 SQLITE_BUSY 分支用新 tx 重试，每次重新鉴权/采样时间，销毁上次 buffer；COMMIT uncertain 不自动重试。并发两个 Consume 只能一个 CAS+commit 成功，另一个不返回秘密。若客户端收到了完整 envelope 但本地验证/写盘失败，不能要求服务器重放；重新明确授权新 grant。

Prepare/Authorize/Bind 的最终创建也必须在同类父子会话重核写事务内完成。各自的读预检不算授权提交；Authorize 在同一事务内以 preparation.state=PATCH_READY 且未绑定为谓词，重读 exact asset revision/readiness/adapter，校验 H(preparation)/proof/slot/expiry 后首次写不可变 ApplyBinding/consent，并在有 required slot 时创建唯一 grant。绑定 CAS 与 grant 创建要么一起提交、要么全部回滚。首次成功才返回随机 token，UNIQUE(prepare_id) 阻止重复签发，同请求 retry 也不回 token；不同 commitment/expiry/consent 不能覆写已绑定值。native-login 同样 CAS 为 INTENT_BOUND_NO_SECRET，但不生成 grant/token。

最小状态转移：`PATCH_READY（只有 preparation）→ INTENT_BOUND_WITH_GRANT（绑定与 grant 同事务）→ CONSUMED`，或 `PATCH_READY→INTENT_BOUND_NO_SECRET`；expiry/retired 为不可逆终态。Prepare/Status 不会从 PATCH_READY 提升授权，Consume 仅接受 INTENT_BOUND_WITH_GRANT。在 Authorize 之前 asset revision/adapter 变更则整次拒绝、不偷偷更新 preparation；已经绑定后变更则 Consume 拒绝，不以旧 safe patch 重渲染新资产。

## 6. 完整流程、丢响应与本机责任

1. 原生内核获得 owner session、明确选资产与 target，生成单 operation ephemeral Ed25519 key。Begin/Bind 绑定 parent session；Web 登录成功本身不创建 native 子会话或 selection。
2. 先调用 Prepare，提交 expected owner/revision/adapter/target/selection/operation 和 proof，**不提交 commitment 或假定已完成 IntentPlan**。服务器固定 exact revision、完整 safe patch、H(preparation)、expiry/authorize challenge。客户端校验 patch schema/hash/identity 后，才读取并绑定可信本机 target，依据唯一 server patch 构造完整 IntentPlan（含 slot 动作、secret references、本地 base/CAS 身份、selection/consent revision、expiry），计算本机 HMAC commitment；不复制 moejs 映射。不得在 Prepare 之前展示完整已确认 intent，未物化不假装知道最终 new-file hash；不会上传本地文件 bytes。
3. 本机 UI 确认准确完整 plan 或匹配已授权 auto-policy；Authorize 首次提交 commitment/intent expiry、确切 slot 和 consent 断言，proof 同时覆盖 H(preparation)。服务端事务重核并返回精确 ApplyBinding；本机必须验证与自身完整 intent 一致。native-login 也完成这一步，但只返回无秘密绑定确认、不产生 grant，然后在本地形成 MaterializedPlan，跳过 Consume。服务端不因收到 explicit_confirmation 就声称真实观察到点击。
4. Consume 正确一次后，native client 将 bounded proto 解码立即转换成 Secret wrapper，验证返回 binding 完全一致、slot 唯一且正好 required_slots，结合已验证 patch 形成本地 MaterializedPlan；清除 transport buffer，严禁传入 Web view 或 generic renderer 日志。
5. 本地继续执行原 spec 的 selection/TTL/父目录身份/目标 CAS/journal/publisher 复核。服务端消费成功不是磁盘 Applied；资产更新与本地 rename 不具原子性，无法远程撤回已发 bytes。
6. Consume 响应缺失/截断/超时：本地进入 `MaterializationUnknown`，不写盘；Status 只回 issued/consumed/expired/retired 等安全状态。如果 consumed，不重发原秘密；若 issued，原生也不能自动重试消费（旧请求仍可能在途），应重新明确授权。
7. 需要新 grant 时用**新 operation、新短期 key/子会话、新 Prepare、完整 intent 与新明确授权**，不修改既有 preparation 的 patch/commitment。新 prepare identity 会改变完整 IntentPlan，不能在相同 durable operation 下偷换 plan。旧操作已可能有本地 durable 文件动作时先 reconcile，不能以新 operation 绕过未知发布；没有文件动作则本机标记旧尝试停止并使其迟到响应无法发布。旧服务端 grant 只按已有 consume/expiry/撤销规则终止，不声称新操作自动撤回已发 bytes。
8. 完全相同 Prepare nonce+参数可返回同一安全 patch/challenge且不延长 expiry；同一 operation 另换 nonce/参数冲突，不创建第二个 preparation。Authorize/Consume/Bind **不做敏感重放**；Authorize 丢响应时用 prepare_id 查询状态，拿不到丢失的 grant token，只能按上一步新 operation/授权。native-login 无敏感响应，仍不靠重复 Authorize 改 commitment，回到明确新 plan 流程。Status 不返回 slot values、patch literal、token/hash 或 payload；INTENT_BOUND_NO_SECRET 的 grant state 为 NOT_ISSUED，仅凭此状态不能替代已收到并验证的 ApplyBinding。

子会话到期/注销后 Status 也不能旁路鉴权；可返回 unauthenticated，此时本机视为未知并用新 owner 授权流程重建 plan，不能凭 grant_id 公开查询。无须新建全局 Reveal/Download 或恢复秘密 response 的接口。

## 7. 错误与验收合同

稳定 Connect 分类：格式/未知字段/enum/签名编码错为 InvalidArgument；无效/过期父子会话为 Unauthenticated；普通 owner 直接调消费方法、错误 purpose 为 PermissionDenied；跨 owner/不存在 prepare/grant/asset 返回相同 NotFound（只在有效 native 主体下）；旧 revision 为 Aborted；unsupported adapter/kind/schema、needs-credential、已消费/retired/过期 grant 为 FailedPrecondition；容量/限额为 ResourceExhausted；未能确认 COMMIT 的返回 Unavailable + 固定文案 `materialization outcome unknown`。不携带请求值/原始 DB/crypto/plugin/SDK 错误 details。Status 中不存在和已过期清理的 ID 也不形成跨 owner oracle。

消费时 wrong token/proof 的失败保持统一授权错误，不透露某 grant 是否存在/secret 是否 present；普通业务 readiness 只在已授权 owner 的 Prepare 阶段展示。响应成功字节出现于 native channel 是预期行为，测试不得为了“日志安全”把它篡成脱敏值然后谎称功能正确。

### Task 1：冻结 wire 与证明向量（先独立签认）

**未来文件：** Create `proto/skillhub/configuration/native/v1/native.proto`；Buf 生成对应 `gen/skillhub/configuration/native/v1/**`。客户端 shared proof helper 建议 `internal/configclient/native_proof.go`，只含 stdlib/共享 wire 类型，无 server 依赖。

**生成隔离的未来写集与冻结门禁：** Task1 还需独占修改 `buf.gen.web.yaml` 的 input filter；`buf.gen.yaml`、`buf.yaml` 和现有 `.github/workflows/build.yml` parity 逻辑只核对，不因 native 隔离缩减 Go/Buf 验证范围。本轮仍只改计划，不改这些配置/生成物。此前计划冻结 SHA 为 `96737b4ecdca9e247856b926ea3f2d15e739b940b7d13038d4055eb1de15cd43`，上游 `native-protocol-logical-final.sha256` 对本计划的该旧 hash 需由主线程在新稿签认后更新，不由本任务修改 manifest。

当前 `buf.gen.web.yaml` 的 `inputs: [{directory: proto}]` 会涵盖全部 proto；未来 Task1 明确改为以下 input 配置，保留既有 plugin/out/opt 不变，不能把 filter 放到 Go template 或仅靠前端不调用来隔离：

```yaml
version: v2
inputs:
  - directory: proto
    exclude_paths:
      - proto/skillhub/configuration/native
```

这是 **Web template 的 input 片段**，不是覆盖完整文件。Buf 官方 v2 说明（`https://buf.build/docs/configuration/v2/buf-gen-yaml/`，2026-10-29 已读取）列出 input.exclude_paths，其行为对应 CLI --exclude-path；directory 示例中的 paths 相对执行目录。仓库当前 root `buf.yaml` 声明 module path=proto，因此实施必须从含该 buf.yaml 的仓库根执行，以实际 workspace 验证 `proto/skillhub/configuration/native` 这个目录 filter，而不是猜成 module 内相对路径 `skillhub/configuration/native`。本轮没有 native proto，也没有运行生成实验，不能把文档语义当作本仓库已通过的 filter 证据。

- [ ] **Go 正向门禁**：保持 Go template 输入全量 `directory: proto`，不加 native exclude；全量 Buf lint/breaking 继续覆盖 native。在隔离验证 tree 中运行既有 `buf generate -o "$generated"`，确认 native Go message 与 Connect handler/client 均实际生成，再执行完整 `diff -ru gen "$generated/gen"`；不得跳过 native 子目录来换取 parity green。
- [ ] **Web 负向与普通业务正向门禁**：同一 workspace 根用修改后的 Web template 生成到全新空 output，要求 `$generated/web/src/gen/skillhub/configuration/native` 不存在，整个 TS 输出没有 native service/message descriptor；仓库 `web/src/gen` 也不能残留该目录。普通 configuration/auth 等既有 TS 必须仍生成，并保留完整 `diff -ru web/src/gen "$generated/web/src/gen"`、既有 Web typecheck/build。不能只断言“没有 native”却使所有 TS 都不生成，也不能用 diff exclude、手删临时生成结果或删普通 TS 掩盖问题。
- [ ] **可失败 filter 证据**：在隔离 tree 中先用无 exclude 的 Web template 对真实 native proto 生成，证明 native TS 会出现；再用上述目录 filter 生成到另一个空 output，证明 native 消失而普通 TS 保留、Go native 不受影响。记录 Buf/plugin 版本、根目录、命令及结果；CLI flags 不得覆盖 template 的排除路径。该红/绿验证只在未来 Task1 执行，本轮不运行或修改生产配置。
- [ ] **Web 源码边界**：核查 `web/src` 的 TS/TSX 静态/动态 import、re-export 及服务调用入口，不得引用 native generated package、NativeConfigurationService 或 native wire procedure；普通 Web proto 不得反向 import native proto，否则应修依赖边界而不是打开 include_imports。无 native 生成物与无业务 import 两项独立验收，保留现有全量 Go/Web parity，不增加 Web Reveal 或把“无 TS”误称 attestation。CI 如需新增持久 gate，由主线程另授其 workflow 写集，本计划不授权改根 build.yml。

- [ ] 主线程与 typed RPC 作者签认 §§2–3 的包名、编号、bootstrap 信任边界及短期 key/session 策略；未签认不注册 endpoint。
- [ ] 固定每个 purpose 的二进制 transcript 和 signature 合成 known-answer fixtures；改变 owner/origin/session/operation/revision/digest/slot/expiry/consent 任一字段均验证失败。
- [ ] 两阶段先红后绿：只有 server patch 才可形成完整 IntentPlan；Prepare 无 commitment，Authorize proof 覆盖 patch/preparation identity + 完整 commitment/expiry，Consume proof 覆盖最终 ApplyBinding；跨 preparation 换 patch/commitment、抢先消费、覆写已绑定 intent 均拒绝。native-login 必须通过无 grant 的第二阶段绑定。
- [ ] 测跨方法 proof replay、重复字段/slot、未知 nested wire/enum、非规范 base64、错误长度与 JSON/压缩入口拒绝，先红后绿；Buf lint/generate parity 检查。

### Task 2：native 子会话及单事务 grant storage

**未来文件：** Create `internal/server/state/native_consumer.go`、`native_configuration.go` 及对应 tests；新增上述四个 ent schema 并由生成器维护 ent；auth 新增 `native_consumer.go` 仅保留精确 session credential 接缝。实施前独立领取 ent/state/auth 写集，不能与当前任务并发写。

- [ ] TDD：用真实临时 SQLite、合成两 owner/多 session，证明普通 token/MCP/Pack 拒绝、子会话精确 parent/key/operation 绑定、Logout 删除父行后全部失败；Begin/Bind challenge 过期/二次使用拒绝。
- [ ] 按 §4.1.1 逐条核对九 FK 的 CASCADE 和实际迁移产物；四表完整链含 consumed grant 后运行现有 RevokeSession 真 DELETE/幂等再 DELETE，级联清零且其他 owner/session 保留；测 challenge cleanup 不反删 native、owner 按既有约束顺序删除、回滚和过期清理。加回 NO ACTION 的 mutation 必须使 DELETE 验收失败。
- [ ] 先实现私有 `readConfigurationAssetVersionTx`，复用现有 AAD/长度/owner 校验，不把 ent.Client 泄露给 domain；读写并发测试固定 revision。
- [ ] 用可控 barrier 测 Logout-before-consume、consume-before-Logout、更新/删除-before-consume、多 Store 同 DB、并发重复消费、回调后过期、callback fail、rollback/commit uncertain。每个线性化断言应有 mutation 红绿证据。
- [ ] 验证 store 不保存 raw token/grant/秘密 envelope；内部敏感 buffer 在 retry/error 路径销毁，COMMIT 未确认无 secret 返回，无 owner 字符串替代 credential 的捷径。

### Task 3：domain/adapter 与真实 Connect 切片

**未来文件：** Create `internal/server/configuration/native_consumer.go`、`internal/server/httpapi/native_configuration*.go`；独立新 coordinator 可放 `internal/server/nativeconsumer/**`；按必要最小范围接 `httpapi/auth.go` Options/注册、guard 新 procedure 预算与 App。这些是未来建议，不是本轮写入授权。

- [ ] 事务外 PlanClaudeProvider + 事务内纯解码/patch 固定比较，真实 moejs fixture 证明 plugin 输入只有 presence/typed nonsecret、输出没有原文；digest 一字节变化拒绝。
- [ ] 真 HTTP Connect protobuf fixtures：owner session 仅能 bootstrap、native 登录态再 Prepare/Authorize/Consume，REPLACE 已保存秘密 exact-match 下发，KEEP 仍相同、clear/native-login 不泄露旧值、另一 slot 不出现。
- [ ] 对消费回复注入“已 COMMIT 后连接丢失”和“返回截断”，Status 为 consumed 且任何重复调用都不返回秘密；Authorize/Bind 失包无 token replay；新 operation 不接收旧迟到响应，旧状态只保留至父/子会话生命周期、注销后不得再查，符合 §6。
- [ ] 捕获标准 log/slog/HTTP errors/codec/errors/strict decode/oversize，synthetic session、grant、proof 材料、replacement 不得出现；不对真实 generated DTO 的 String 安全作假设。
- [ ] 验证 no-store、TLS/origin、不跟 redirect、拒绝 browser metadata、无 CORS/cookie；用恶意 client 证明 proof 只是持钥，将此测试结果作为信任边界而不是偷偷宣称 attestation。

### Task 4：native client decode 与本机内核接线（后续独立写集）

**未来文件：** `internal/configclient/native*.go`、`internal/configapply/**`；CLI/MyGo 只调用 shared 内核，GUI 冻结写集未授权前不改。

- [ ] 测 exact binding/slot envelope 校验、超限/截断清空、response unknown 不写盘、旧 owner/origin generation 不能回填、key 在操作结束后弃置。
- [ ] 依赖 gate 自动证明 configclient/configapply 无 server/moejs；单独验本机 selection/TTL/journal/publisher，不能用 HTTP green 签收三 OS Applied。
- [ ] 独立代表 review 后再解除相应 unsupported；跨 owner 分享/其它 agent/scope/平台仍保留首发缺口。

所有未来测试用 `t.TempDir`、隔离 HOME/合成 credential、loopback server，不接触真实配置。实施时先完整加载 use-modern-go 指导，再 `CGO_ENABLED=0 go test -mod=readonly -count=1` 运行各自新增包与相关真实 state/auth/configuration/httpapi 包，以及 vet/Buf parity；包存在后执行，本文没有假装当前测试命令已能全部运行。

## 8. 只剩的签认门禁与下一窄片

上游 native-apply spec §10.2 已记录代表对 absent 窗口和一次性秘密消费取舍的 ACCEPT，**不再将其产品选择列为待定**。实现必须披露暂时缺失、在移动旧 entry 前完成 stage/加密备份/durable intent、窗口内不等待网络授权或用户确认，并对并发新 entry 保全双方、必要时 NeedsAttention；真实崩溃恢复/平台证据仍是硬门禁。该 ACCEPT 不等于本协议已冻结、整体开工授权或 full native apply/F12 签收。

1. **主线程冻结 wire/方法/标签与持钥编码**，包含 bootstrap 允许 owner 主动授予子会话这一信任模型；若需求是恶意 owner 客户端也不能模拟 native，本片不足，必须先另定可信注册/证明。
2. **签认子会话时长/单 operation 与本机 auto-policy 断言边界**。建议 5 分钟、父会话持续有效、grant 最多 60 秒；更长 key 生命周期不能悄悄改作长期可重用消费授权。
3. **签认 storage 写集与迁移**：四个新表、parent FK cascade、事务 writer gate/不确定提交策略、adapter immutable registry lease；不与当前 ent/state/codec WIP 并发修改。
4. **实际运行证据仍 pending**：Ed25519 编码向量、SQLite 竞态/commit fault、真实 Connect、secret privacy、native decode，以及原 spec §10.2 已 ACCEPT 条件对应的 publisher 崩溃恢复与三 OS 证据。协议 green 不等于 native apply green；独立 docs 审阅也不代替实现验收。

建议下一实施窄片为 **Task 1 的 proof 向量 + Task 2 的父 session/子会话认证事务测试**，先不加秘密 HTTP 响应、不触本机文件/GUI；由主线程确认独占写集后才能开工。最小出口是“已撤销/跨 owner/不同父 session/key 的 consumer 在真实 SQLite 事务内必被拒绝”，不是“已能应用配置”。本设计不会阻塞当前 providerpatch、desktop 身份或 UI/codec 提交。
