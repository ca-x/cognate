# AI 配置资产

> 2026-10-30：Claude 私有 provider 资产管理已有 Web 入口；Linux 手动 CLI 配置工作流已在 `3b0345e` 交付，Linux arm64 编译修复另见 `656c211`。MyGo 配置界面、其它 agent 的完整配置及非 Linux 应用仍未交付；完整证据与后续状态见 `VERIFICATION.md`。

Cognate 的配置资产与 skill 仓库、Pack、设备应用状态是不同资源。当前实现先提供 **Claude Code provider 的私有资产管理**；保存不切换 provider，不写入本机配置，也不表示端点连通或 agent 已加载。

## Web 使用流程

登录后从“AI 配置”进入，或直接访问 `/configuration`。列表只读取当前账号的元数据；点击编辑后才读取脱敏草稿。当前不提供通用 raw JSON 编辑或原文凭据查看。

1. 新建资产并设置名称，选择原生登录、API key 或 Auth token 模式。
2. 按需勾选自定义端点和 default/Haiku/Sonnet/Opus 模型。未勾选是 absent，不等于提交空字符串；模型不自动修剪或改变大小写。端点只保存，不由服务端探测。
3. 每个凭据分别选择 keep、replace 或 clear。已有凭据只显示是否存在；clear 已存在的值需确认。切认证模式时必须显式清理不兼容的旧凭据，不自动删除或把占位符写回。
4. 点击“仅保存配置资产”。缺少所需凭据可以保存为草稿；“凭据结构完整”不是可应用或已应用状态。
5. 复制仅复制非秘密 typed 配置，不复制凭据、选择或设备状态。删除仅移除本账号服务端资产，不能撤销本机配置，也不删除 skill。

列表中其它合法 agent/kind/schema 的资产可以查看元数据及按版本删除，但不能借此推断支持其编辑、复制或应用。

## 草稿、冲突与取消

- 未保存草稿离开前有放弃确认，包括 Escape、遮罩、关闭按钮和路由导航。取消继续编辑，确认放弃清除草稿与新凭据。
- 主题切换保留表单；语言切换保留非秘密草稿，清除新输入的凭据并取消在途请求。账号退出或会话切换直接清理敏感状态，不被草稿确认阻塞。
- 编辑、复制和删除绑定读取时的 revision。发生 CAS 冲突后不会自动覆盖新版本或重试；关闭、刷新列表，再读取当前状态。
- 网络中断、取消或关闭表单不等于撤销已提交写入。结果待确认时先刷新核实，不重复提交。
- 表单、凭据、上传及 diff 不进入 URL、localStorage、sessionStorage 或查询缓存。只有既有主题/语言偏好可持久化。

## 服务端与客户端边界

配置使用 owner-scoped、revision 绑定的加密存储；普通 List/Get/ReadDraft 响应不含秘密原文。Connect 请求合法的 replace 需要在经授权的传输中携带原值；原始 generated DTO 的 String/JSON/slog **不保证脱敏**，禁止用于日志、持久化、错误 details 或调试 body dump。`debug_redact` schema 注解不能代替该约束。

目标适配器只在服务端通过 moejs 执行，CLI/MyGo 不执行插件。已提交的 Claude `claude.provider-switch.v1` 支持七键声明式变更，包括删除旧认证和未选择的受管模型项；真实秘密和旧 env 不进入 JS，Go 宿主独立验证结果。旧四键 additive renderer 继续保留，但不能替代新能力，也不能单凭 renderer 成功声称本机已应用。

本地 JSON codec 在 Go 内存中将声明式 patch 与受控凭据合并为 settings bytes，保留无关 JSON 内容；codec 本身不读取文件、授权获取秘密或写入本机。Native-purpose schema 与 Web 生成隔离；实际授权、受控发布和恢复由独立消费/应用内核及下述 Linux CLI 流程执行，不能把单独的 codec 成功当成已应用。

## Linux 手动 CLI 应用与恢复

先显式运行 `cognate client-key init`，并单独安全备份本机密钥。配置命令只加载已有密钥，不隐式创建替代；初始化及安全输出限制见 `CLIENT-SUBSCRIPTIONS.md`。

- `cognate config apply` 默认只预览。需明确指定服务端 origin、owner、资产 ID/revision、本机 profile 和 `claude.settings` 目标；会话仅由 `--token-stdin` 从有界非 TTY 标准输入读取，不作为命令行参数或日志输出。服务端敏感消费接口的实际 TLS 要求仍适用。
- 增加 `--apply` 才进入真实控制 TTY 的逐凭据槽批准和最终计划确认；没有槽也需最终确认。网络与人类等待不持本地安装/目标锁；确认后重新检查文件基线、版本及目标身份，不覆盖并发本地修改。
- `cognate config status --operation OPERATION_ID` 查询本地持久化状态。远端消费或本地写入结果未知时不能重放秘密；取消、超时及丢失回执不代表回滚。
- `cognate config restore --source-operation OPERATION_ID` 默认预览恢复，增加 `--apply` 后仍需独立确认并重开校验 CAS。恢复是受控的本机操作，不等于服务端整库备份、S3 或任意文件恢复。

当前限 Claude/provider/schema1 和 Linux；不表示 Codex 已可应用、MyGo 已有配置页面，或配置随 Pack 自动应用。每次命令有总期限，`--timeout` 须大于零且不超过五分钟；实际选项见 `cognate config apply --help`。

## Native 内部消费核心（Task2C）

2026-10-29：`880cef72` 的内部核心与 `7d8eff15` 的敏感 HTTP/App 接线已分别通过精确源码的独立 Spec/Quality 验收并 SSH 推送。合成数据库、真实 owner MAC/Ed25519、真实服务端 moejs、SQLite 和 TLS 六 RPC 验证 Prepare→Authorize→Consume→Status 闭环；当时未接 CLI/MyGo 或本机应用。后续 Linux 手动 CLI 接线见上文，不代表 MyGo、完整 F12 或首发完成。

Prepare 先固定版本与安全七槽 patch，Authorize 后才绑定完整 intent；API key、Auth token 分别验证精确一次原文返回，native-login 不生成 grant。凭据与配置同处一个加密 envelope，其版本由资产 CAS revision 覆盖，没有独立 credential_version。普通 Web/配置 CRUD/MCP 不获得秘密。

四张 native 表执行已审逐表及 parent/owner/instance 行数与字节硬额度（总额 56/416 KiB、128/896 KiB、512/3584 KiB），所有未物理删除的终态/过期项仍计费；这是逻辑 charge，不是 SQLite/WAL 或整库磁盘上限。真实 Logout 删除父 session 并级联全部后代，不受 GC 批额度阻止。有界 prune 在启用的 native 服务启动时运行，随后串行按 30 秒节拍调度；这是调度间隔，不是实际删除的实时 SLA。

grant 最长 60 秒，未知 COMMIT 或提交后取消不返回 grant/秘密、不重放响应，已提交事实保留；Status 不回 token/hash/patch/value。检查与 driver COMMIT/return 之间不是原子保证，不能保证远端收包时身份仍有效。仅清理已知 owned buffer；Go 字符串、副本、GC 与 WAL 不允许宣称完全清零/物理擦除。raw generated DTO 仍不安全，禁止日志。冻结实施合同见 `superpowers/plans/2026-10-29-native-configuration-store.md`。

## Native 传输与部署

服务端同时配置 `--tls-cert`、`--tls-key` 和规范 HTTPS `--public-origin` 才启用 native 接口；请求必须实际使用 TLS。反向代理终止 TLS 后转发到 HTTP 后端暂不支持此接口，Host/Forwarded/X-Forwarded-* 不能替代受信 origin 或实际 TLS，不因此扩大代理信任。

六个接口仅接受 Connect unary protobuf POST，不接受 JSON、GET/query、gRPC-Web、压缩请求、浏览器 Origin/Sec-Fetch 或 cookie 认证。Begin/Bind 使用 owner bearer，其余只接受绑定的 native-purpose 子会话。响应及错误均 no-store；八个并发槽与 20 秒预算从 body 读取前覆盖解码、实际响应写出和取消排空，显式 Flush 不逃出计数。响应丢失不能重发秘密；proof 是持钥证明，不是官方客户端 attestation。详细合同见 `superpowers/plans/2026-10-29-native-configuration-transport.md`。

来源配置导入、十四类 agent 的完整专属交互、Pack 配置附件、MyGo 配置选择/预览/应用/恢复界面、非 Linux 实际应用，以及明确选择后的自动应用仍为后续门禁。Linux 手动 CLI 的受控发布与恢复不替代这些能力。完整目标见 `REQUIREMENTS.md`、`CCSWITCH-CONFIGURATION-MATRIX.md` 和 `VERIFICATION.md`，不以已交付部分缩减首发范围。
