# Typed Configuration Domain

2026-10-28。实现合同；完整目标仍为 `docs/CCSWITCH-CONFIGURATION-MATRIX.md` 的十四类 agent 和 F12，而非将首发缩为 Claude。本文冻结第一个可独立验收的 typed domain 切片；RPC、编辑器、选择和原生应用依次消费它，不凭存储成功声称已应用。

## 公共边界

- 沿用 `2026-10-28-configuration-assets.md` 的 owner 隔离、容量、加密和 revision CAS。内部 envelope 不是公开 raw JSON API。
- `internal/server/configuration` 负责 typed 校验、秘密合并、脱敏视图和内部 envelope 编解码；不执行 JS、网络探测、命令或客户端文件操作。
- 新建、编辑读取、更新、复制、删除使用 owner-scoped Store 接口；List/Get metadata 不读取 payload。读取编辑视图必须一次取得同一 revision 的 metadata 与 payload，禁止分别 Get/Read 拼装。
- 保存仅改变资产，不更新选择、Pack 或设备状态。未知 agent/kind/schema/variant 的 typed 编辑、更新与复制返回明确 unsupported，不生成可应用成功。List/Get 只返回合法存储元数据，不能因同一用户另有未知 agent 资产而让整个列表不可用；Delete 也仅按 owner 与 CAS 删除，不将 metadata 操作冒充支持该 agent 的应用能力。
- domain 请求中的 replacement 使用默认打印/JSON/Text/slog 脱敏的 secret wrapper；普通编辑视图只有 slot ID 和 present。无 Reveal RPC。原始 Protobuf/Connect wire DTO 必须携带经授权的 replacement，因此属于敏感传输对象，不能记录、打印、持久化或作为错误 details；标准生成的 String/JSON 方法不承诺脱敏，不得当作 safe-to-log 使用。schema 的 debug_redact 标注不代表当前 Go runtime 自动实现该保证。服务只允许固定分类错误/状态与明确脱敏的 domain 视图进入日志，真实 proto/JSON 成功、失败及超限请求要捕获验证日志中无 replacement/session 原文。
- endpoint、model ID、名称等非秘密仍为私有内容。凭据不允许放 URL/userinfo/query、模型字段或持久化浏览器状态。错误不回显非法输入。

## 第一个 domain 切片

冻结 `agent_id=claude`、`kind=provider`、schema 1。后续用独立 typed variant 增补其它 agent/资产种类，不提供兜底 object/map 来冒充支持。

`ClaudeProvider` 具有 `Endpoint`、`AuthMode`、`Models`。AuthMode 为 native-login/api-key/auth-token；Models 分别表达 default/haiku/sonnet/opus 的 optional 字符串，absent 不等于空值。Endpoint optional，若存在须为绝对 http/https URL、有 host、无 userinfo/query/fragment/控制字符，最多 2048 UTF-8 bytes；本地地址允许保存但不发请求。模型值存在时为 1–256 UTF-8 bytes，无控制字符，不做大小写或静默 trim。native-login 不接受自定义 endpoint；该模式仅表达交回原生登录，不导入 OAuth 登录态。

资产名称与 identity 使用既有 storage 合同。公开 draft 只有 name、typed Claude provider；agent/kind/schema 由该 variant 明确决定，不允许请求在更新时换类型。存储 envelope 有明确 version 和 variant，严格拒绝未知/重复成员、尾随数据、歧义以及超限；不得通过 JSON float 转换 native 大整数。此切片只编码自身 typed 数据，不宣称已支持来源 native extras 导入；既存 envelope 中未知版本/variant fail closed，不能丢弃后重存。

固定 secret slots 为 `claude.api-key`、`claude.auth-token`。每个 mutation 为 slot + keep/replace/clear；重复、未知 slot 或未知 action 拒绝。replacement 使用默认 String/GoString/JSON/Text/slog 脱敏的 wrapper，1–8192 bytes、合法 UTF-8、无 NUL/CR/LF，不 trim。secret 原文仅内部显式读取以编码加密 envelope；禁止普通导出结构将它暴露。

| 操作 | 秘密语义 |
| --- | --- |
| Create | omitted/clear 为 absent，显式 keep 拒绝；replace 写入新值 |
| Update | omitted/keep 保留同资产同 expected revision 原值；clear 删除；replace 替换 |
| Copy | 默认不复制任何秘密；复制 typed 非秘密配置，返回 needs-credential |
| Edit | typed provider + metadata + 两个固定 slot 的 present + readiness；不返回秘密、hash、长度或末位 |

native-login 要求两个 slot 都 absent；api-key 不允许 auth-token 残留，auth-token 不允许 api-key 残留。相应凭据缺失允许保存为 needs-credential 草稿，不允许声称可应用。切换模式必须显式清理旧秘密；不能将 omitted 的 KEEP 偷变 CLEAR。已具备凭据只表示该 schema 的 credential completeness，不等于 renderer/本机已支持或已应用。

Update 在读取后先比较 expected revision，再进行 typed/secret 合并，最后调用 Store CAS；任一竞争返回 conflict，不自动重试。Read 与 Copy 使用同次 select 的 metadata/payload；Copy 必须匹配调用者的 expected source revision，不修改来源。删除必须 CAS。domain 不暴露含秘密的原始 materialize 输出，本地消费以后单独冻结最小授权合同。

## 可执行验收

- 先 red 再 green：owner 隔离、Create/Edit/Update/Copy/Delete/重启；metadata 与普通编辑响应无 sentinel；wrapper 所有标准打印/JSON/Text/slog 路径脱敏。
- 覆盖 secret omitted/keep/replace/clear、重复/未知 slot、空 replacement、非法 UTF-8/CRLF、create keep、模式切换残留、缺失凭据草稿以及 Copy 不带秘密。
- CAS：stale update/delete 无修改；两个并发更新只有一个成功，KEEP 不取另一个 revision 的秘密；Read 返回同 revision 对；Copy 与并发来源变化不混合 metadata/payload。
- 严格 envelope：重复/未知成员、尾随 JSON、错版本/variant/超限、非法组合均固定分类失败，不返回输入。遇到 storage 保存的其它合法 agent 资产，domain 不把它作为 Claude 解码。
- URL/model presence 与各字节边界、取消 context、状态层错误映射；对异常 Store 返回 fail closed。
- 使用真实临时 Store 集成测试，不读取用户配置；无新依赖、无 cgo。运行 focused tests/vet、全量回归、clean-index验证，再由独立代表审验。不得把本切片标记为完整 F12 或正式 Release。

## 实施顺序

1. storage 原子 metadata+payload 读取及加密/CAS验收。
2. domain 类型、严格 envelope、secret 三态与 Store 集成测试；写集仅 `internal/server/configuration/**`。
3. additive Buf/Connect typed 服务和 owner/session/限额/错误映射；再冻结前端生成 API。协议不使用 raw bytes/Struct 来代替 typed draft。
4. Claude 专属表单和 native 预览/应用流程；同时按矩阵分批扩展其它 agent/provider、MCP、prompt、settings、workspace。所有缺口保持在首发清单，不因第一切片通过而删除。
