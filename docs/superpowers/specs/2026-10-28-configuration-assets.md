# Cognate 配置资产基础切片

状态：F12 的内部存储合同；不等于完成用户配置交互。完整能力依据 `../../CCSWITCH-CONFIGURATION-MATRIX.md`，基线 cc-switch `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`。本项目仍以 skill 为主，配置为可选辅助资源。

## 目标与模块

先建立 owner 隔离、加密、revision CAS 的配置资产存储，后续来源导入、类型化配置 API、Web/MyGo 表单、Pack 附件和客户端应用都复用它。**服务端保存资产不表示任何设备已启用或应用。**

| 模块 | 本切片 | 依赖 |
|---|---|---|
| configuration-assets | ent schema、加密记录及内部 CRUD | 既有用户/私有 Store |
| configuration-domain | agent-specific provider/models/MCP/prompt/settings/workspace 类型与验证 | 后续；不是直接暴露 opaque bytes |
| configuration-import | 授权导出→来源插件→类型化候选→显式入库 | 后续；依赖 domain/assets |
| configuration-management | 版本化 Connect、专属 Web/MyGo 表单与 secret 三态 | 后续；依赖 domain/assets |
| configuration-application | 选择与附件、服务端渲染、原生 diff/CAS/备份/应用/恢复 | 后续；依赖上述模块 |

内部存储可以保存有界密文 envelope，但不能以此将公开 API 或界面设计成单个 raw JSON 字段。typed domain 必须分别表达 provider、MCP、prompt、全局设置和 workspace；独占/共存/特殊默认选择在独立 selection/每设备 apply 记录中表达。

## 存储合同

- 使用既有 ent + entsqlite，无新依赖、无 cgo。新增 `ConfigurationAsset` 表及 User edge，不改现有身份、GitHub 凭据或 Pack 数据格式。
- 元数据：随机稳定 `public_id`、不可变 `user_id`、不可变 `agent_id` 和 `kind`、`name`、正整数 `schema_version`、正整数 `revision`、不可变 `created_at`、`updated_at`、Sensitive 的非空 `ciphertext`。
- `kind` 为 `provider`、`mcp`、`prompt`、`agent-settings`、`workspace`。agent ID 使用 1–64 字节小写稳定标识符，不借品牌更名改变。该层不把未知 agent 当可应用：具体支持能力必须由 domain/adapter gate 校验。
- name 去除首尾空白后为 1–128 UTF-8 字节，无控制字符；schema_version 为 1–65535。agent ID 匹配 `[a-z][a-z0-9-]{0,63}`。每用户最多 250 项，单项明文 envelope 为 **1 byte–256 KiB**（非空，不要求最小 1 KiB）；分页/HTTP limits 属后续 API 层，内部 list 至多返回该用户的上限。
- 列表和普通 Get 仅返回元数据，不返回明文、密文或 secret 预览。读取明文必须调用独立的 owner-scoped 方法，返回默认打印/JSON 编码均不泄露的 secret wrapper，只通过显式 Reveal 拿到克隆 bytes。
- 使用外部主密钥的独立 HKDF-SHA256 域 `cognate/configuration-assets/v1` 派生 AEAD key；AES-GCM 使用随机 nonce。AAD 至少绑定用途/版本、owner、asset ID、agent、kind、schema version 和 revision，不能将密文跨用户/资产/类型/修订搬运后成功解密。
- 不把明文 payload/hash 存入公开元数据或日志。内部状态层不接受 JS 源码、绝对路径、执行命令或自动探测真实用户配置。
- 更新必须带 expected revision；agent/kind 不可被更新改写。payload/name/schema revision 同一事务提交；删除也必须 CAS。不存在或其它 owner ID 返回同一 not-found，不暴露跨 owner 存在性。
- 创建容量检查、更新、删除都在事务内。并发 stale writer 不能覆盖新版本；计数竞争不得越过上限。沿用已有 SQLite 有界 BUSY 重试，不用后台无限排队。
- 本切片不提供复制秘密、选择/应用、MCP 管理工具或分享；后续 Copy 默认不复制 secrets，三态 keep/replace/clear 在 domain 层明确实现，不能用脱敏占位符覆盖原值。将来 Pack 引用检查必须先实现，再允许删除被引用资产。

## 内部接口

在 `internal/server/state/configuration.go` 暴露 metadata、draft 和默认脱敏的 payload wrapper；方法遵循既有 Store 风格，显式 context/owner：Create、List、Get metadata、Read payload、Update(expected revision)、Delete(expected revision)。名字冻结为 `CreateConfigurationAsset` / `ListConfigurationAssets` / `ConfigurationAsset` / `ReadConfigurationAsset` / `UpdateConfigurationAsset` / `DeleteConfigurationAsset`；每个方法返回受控固定分类错误，不透出输入、SQL 或密文。

State 层的 draft 包含 agent/kind/name/schema version/payload。Update 仍提供完整 draft，以便明确拒绝 agent/kind 改写。payload wrapper 的 Reveal 返回副本；调用者修改输入或返回副本不影响保存的数据。

## 验证与边界

TDD：先通过公开 Store 接口写可编译行为测试并观察预期 red，再实现；不读取用户真实配置，所有 payload 为合成 sentinel。

必须验证：CRUD/重启、不同 owner ID隔离、普通查询和格式化/JSON不含payload、原始 SQLite/WAL 不含 sentinel、nonce 非确定性、错误 key/篡改/跨owner/跨asset/revision/schema 类型密文替换失败、输入/输出克隆、坏元数据/上限/CAS、并发更新仅一个成功、容量边界及旧 Store 功能回归。只检查源码 Sensitive 标记不算验证秘密保护。

执行 `CGO_ENABLED=0 go test -count=1 ./internal/server/state`、对应 `go vet`、`go mod tidy -diff` 和 `git diff --check`；最终由主线程隔离导出与独立代表复验后提交。遵守 Go 1.27.3 modern-go、ub-mode 与 DDIA；本阶段不生成 tag/Release。

写集：新增 schema/测试/状态文件、User edge、必要 Store 的派生 key 初始化及 ent 生成物。不得修改已冻结的 rename index、现有 MCP gate、HTTP/proto、GUI、CLI 或 go.mod。若 generation 影响其它 schema 逻辑需停止报告，不顺带改造。
