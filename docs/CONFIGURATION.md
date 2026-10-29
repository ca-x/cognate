# AI 配置资产

> 2026-10-30：Claude 与 Codex 私有 provider 资产管理已有 Web 入口；Codex typed API/Web 在 `4d0e6c0` 交付，但尚不能本机应用。Claude 的 Linux 手动 CLI 配置工作流在 `3b0345e` 交付，Linux arm64 编译修复另见 `656c211`。MyGo Claude provider/schema1 原生编辑、预览、显式批准应用、状态及恢复在 `bd00bc0` 交付；本机操作仅限 Linux。其它 agent 的完整配置、完整历史/文件差异及非 Linux 配置应用仍未交付；证据与 CI 状态见 `VERIFICATION.md`。

Cognate 的配置资产与 skill 仓库、Pack、设备应用状态是不同资源。当前提供 **Claude Code 与 Codex provider 的私有资产管理**；保存不切换 provider，不写入本机配置，也不表示端点连通或 agent 已加载。

## Web 使用流程

登录后从“AI 配置”进入，或直接访问 `/configuration`。列表只读取当前账号的元数据；点击编辑后才读取脱敏草稿。当前不提供通用 raw JSON 编辑或原文凭据查看。

1. 新建资产时先明确选择 Claude 或 Codex；选定后不能在同一草稿里切换目标。Claude 支持原生登录、API key 或 Auth token 模式；Codex 当前仅支持自定义 HTTPS Responses/provider-key，不支持官方 ChatGPT/OAuth 登录。
2. Claude 可按需勾选自定义端点和 default/Haiku/Sonnet/Opus 模型。未勾选是 absent，不等于提交空字符串；模型不自动修剪或改变大小写。端点只保存，不由服务端探测。
3. 每个凭据分别选择 keep、replace 或 clear。已有凭据只显示是否存在；clear 已存在的值需确认。切认证模式时必须显式清理不兼容的旧凭据，不自动删除或把占位符写回。
4. 点击“仅保存配置资产”。缺少所需凭据可以保存为草稿；“凭据结构完整”不是可应用或已应用状态。
5. 复制仅复制非秘密 typed 配置，不复制凭据、选择或设备状态。删除仅移除本账号服务端资产，不能撤销本机配置，也不删除 skill。

列表中其它合法 agent/kind/schema 的资产可以查看元数据及按版本删除，但不能借此推断支持其编辑、复制或应用。

### Codex provider 表单

- 必填 HTTPS 端点与主模型 ID；顶层推理等级可不设置，显式 `none` 不等于 absent。主模型与目录字段使用专属控件，不提供任意 JSON/path/template 输入。
- 模型目录可整体不设置；启用后可添加、移动和删除目录行。每行的显示名、context window、输入 modalities、base instructions 与 reasoning levels 分别保留 presence，支持并行调用为三态，显式 `false` 不会被省略。有序 modalities/推理等级和目录行按用户保存的顺序保留，base instructions 的空白不自动修剪。
- 唯一凭据槽为 `codex.provider-key`。允许缺 key 的草稿；复制后的资产没有 key，须自行补充。clear 已存 key 仍须确认，不从本机登录、环境或旧配置补值。
- 含未来未知字段的 Codex 资产只读展示已知内容，不能保存或复制；仍可由所有者按 revision 明确删除。CAS 冲突后的“放弃草稿并重新读取”需单独确认，不合并或重放旧写入。
- 编辑及放弃确认弹窗内均可切换语言；切换清除新凭据并终止当前等待。关闭后焦点回到原操作按钮；若它因删除或语言刷新已不存在，则回到新建按钮。

这些能力仅管理服务端资产。Codex 的本机 codec、登录保留策略解析、授权消费和设备应用仍需后续独立交付。

## 草稿、冲突与取消

- 未保存草稿离开前有放弃确认，包括 Escape、遮罩、关闭按钮和路由导航。取消继续编辑，确认放弃清除草稿与新凭据。
- 主题切换保留表单；语言切换保留非秘密草稿，清除新输入的凭据并取消在途请求。账号退出或会话切换直接清理敏感状态，不被草稿确认阻塞。
- 编辑、复制和删除绑定读取时的 revision。发生 CAS 冲突后不会自动覆盖新版本或重试；关闭、刷新列表，再读取当前状态。
- 网络中断、取消或关闭表单不等于撤销已提交写入。结果待确认时先刷新核实，不重复提交。
- 表单、凭据、上传及 diff 不进入 URL、localStorage、sessionStorage 或查询缓存。只有既有主题/语言偏好可持久化。

## 服务端与客户端边界

配置使用 owner-scoped、revision 绑定的加密存储；普通 List/Get/ReadDraft 响应不含秘密原文。Connect 请求合法的 replace 需要在经授权的传输中携带原值；原始 generated DTO 的 String/JSON/slog **不保证脱敏**，禁止用于日志、持久化、错误 details 或调试 body dump。`debug_redact` schema 注解不能代替该约束。

目标适配器只在服务端通过 moejs 执行，CLI/MyGo 不执行插件。已提交的 Claude `claude.provider-switch.v1` 支持七键声明式变更，包括删除旧认证和未选择的受管模型项；真实秘密和旧 env 不进入 JS，Go 宿主独立验证结果。旧四键 additive renderer 继续保留，但不能替代新能力，也不能单凭 renderer 成功声称本机已应用。

本地 JSON codec 在 Go 内存中将声明式 patch 与受控凭据合并为 settings bytes，保留无关 JSON 内容；codec 本身不读取文件、授权获取秘密或写入本机。Native-purpose schema 与 Web 生成隔离；实际授权、受控发布和恢复由共用消费/应用内核执行，Linux CLI 与 MyGo 均复用该内核，不能把单独的 codec 成功当成已应用。

## MyGo 原生 Claude 配置

从 MyGo“AI 配置”页载入当前账号的私有资产。该原生编辑器仅支持 Claude provider/schema1；Codex 和其它合法 agent/kind/schema 只显示元数据，可按版本明确删除，不支持在此编辑、复制或本机应用。这不限制已交付的 Codex Web 专属表单，也不表示十四类 agent 已完整适配。

1. 编辑 Claude 原生登录、API key 或 Auth token 模式，以及可选端点和 default/Haiku/Sonnet/Opus 模型。两个凭据分别选择 KEEP、REPLACE、CLEAR，新凭据默认遮罩；模式转换须显式 CLEAR 不兼容的旧槽。仅保存更新服务端资产，不修改本机选择或文件；复制不带秘密。冲突必须明确重读，不自动合并或重试旧 revision。
2. 本机操作前，在唯一的“账号 → 本机设置”入口显式初始化密钥。配置页只导航到该入口，不隐式生成或替换 key；远端浏览和编辑不需要本机 key。Watch 或上一任务仍运行/取消排空时不能抢开本机操作，须显式停止并等待真实排空。
3. 对已保存、未修改的资产和明确 profile 请求预览或应用。安全预览显示实际 operation、身份/资产版本、adapter、逻辑目标和 slot/action，不含秘密或原文件全文，也不是完整值级 diff。“仅预览”不消费新凭据或写目标，但可能留下加密 Planned 记录；其计划不能沿用于后续应用，真正应用须展示同一次在途操作的新计划并重新批准。
4. 对话框内逐个勾选本次所需 secret slot，再明确批准最终计划。勾选槽本身不授权消费，零槽仍须最终批准。默认焦点是“拒绝本次操作”；删除默认“取消”，离开 dirty 草稿默认“保留草稿”。初次 Enter 不批准危险动作，仍可通过明确 Tab 导航或鼠标选择后确认；Escape 只拒绝/取消。
5. 本机状态与恢复按 operation ID 发起，可离线操作，不要求当前账号重新授权或下载旧秘密；仍须合法本机 key 和原加密记录，恢复另须通过当前基线校验。真正恢复同样须新预览和最终批准。界面只保留有界的本进程安全结果，不提供跨重启完整历史枚举；已有 operation ID 可用于核实持久化状态。

取消、超时、换身份或关闭会停止旧任务并等待实际资源收尾；这不保证已发生消费/写入回滚。Unknown、NeedsAttention、冲突或 Close 失败不能显示为成功，不能自动重放秘密或自动“重试应用”。保存、登录、Pack 同步和后台 Watch 都不会自动应用 AI 配置。

密码、session、分享 token 和新凭据采用不透明内存存储，覆盖非常规 fmt 动词及私有嵌套容器的默认日志隐私检查；这不是允许主动取出秘密后记录日志，也不承诺 Go 字符串、副本或 GC 的物理擦除。新凭据和批准状态在相应任务收尾或身份失效时清理，不进入日志或额外持久化缓存。

**本机配置预览、应用、状态和恢复仅支持 Linux。** 非 Linux 显示 unsupported 并拒绝本机动作；Darwin/Windows 交叉编译、原语测试或打包成功都不等于这些功能已可运行。实际支持的原生窗口与键盘证据边界见 `VERIFICATION.md`，不把 headless 测试当作新窗口验收。

## Linux 手动 CLI 应用与恢复

先显式运行 `cognate client-key init`，并单独安全备份本机密钥。配置命令只加载已有密钥，不隐式创建替代；初始化及安全输出限制见 `CLIENT-SUBSCRIPTIONS.md`。

- `cognate config apply` 默认只预览。需明确指定服务端 origin、owner、资产 ID/revision、本机 profile 和 `claude.settings` 目标；会话仅由 `--token-stdin` 从有界非 TTY 标准输入读取，不作为命令行参数或日志输出。服务端敏感消费接口的实际 TLS 要求仍适用。
- 增加 `--apply` 才进入真实控制 TTY 的逐凭据槽批准和最终计划确认；没有槽也需最终确认。网络与人类等待不持本地安装/目标锁；确认后重新检查文件基线、版本及目标身份，不覆盖并发本地修改。
- `cognate config status --operation OPERATION_ID` 查询本地持久化状态。远端消费或本地写入结果未知时不能重放秘密；取消、超时及丢失回执不代表回滚。
- `cognate config restore --source-operation OPERATION_ID` 默认预览恢复，增加 `--apply` 后仍需独立确认并重开校验 CAS。恢复是受控的本机操作，不等于服务端整库备份、S3 或任意文件恢复。

当前 CLI 本机流程限 Claude/provider/schema1 和 Linux；不表示 Codex 已可应用或配置随 Pack 自动应用。MyGo 的独立交付见上节，不能由 CLI 签收推断 GUI 验收。每次命令有总期限，`--timeout` 须大于零且不超过五分钟；实际选项见 `cognate config apply --help`。

## Native 内部消费核心（Task2C）

2026-10-29：`880cef72` 的内部核心与 `7d8eff15` 的敏感 HTTP/App 接线已分别通过精确源码的独立 Spec/Quality 验收并 SSH 推送。合成数据库、真实 owner MAC/Ed25519、真实服务端 moejs、SQLite 和 TLS 六 RPC 验证 Prepare→Authorize→Consume→Status 闭环；当时未接 CLI/MyGo 或本机应用。后续 CLI 与 MyGo 的独立交付见上文，不倒推为该阶段已有这些入口，也不代表完整 F12 或首发完成。

Prepare 先固定版本与安全七槽 patch，Authorize 后才绑定完整 intent；API key、Auth token 分别验证精确一次原文返回，native-login 不生成 grant。凭据与配置同处一个加密 envelope，其版本由资产 CAS revision 覆盖，没有独立 credential_version。普通 Web/配置 CRUD/MCP 不获得秘密。

四张 native 表执行已审逐表及 parent/owner/instance 行数与字节硬额度（总额 56/416 KiB、128/896 KiB、512/3584 KiB），所有未物理删除的终态/过期项仍计费；这是逻辑 charge，不是 SQLite/WAL 或整库磁盘上限。真实 Logout 删除父 session 并级联全部后代，不受 GC 批额度阻止。有界 prune 在启用的 native 服务启动时运行，随后串行按 30 秒节拍调度；这是调度间隔，不是实际删除的实时 SLA。

grant 最长 60 秒，未知 COMMIT 或提交后取消不返回 grant/秘密、不重放响应，已提交事实保留；Status 不回 token/hash/patch/value。检查与 driver COMMIT/return 之间不是原子保证，不能保证远端收包时身份仍有效。仅清理已知 owned buffer；Go 字符串、副本、GC 与 WAL 不允许宣称完全清零/物理擦除。raw generated DTO 仍不安全，禁止日志。冻结实施合同见 `superpowers/plans/2026-10-29-native-configuration-store.md`。

## Native 传输与部署

服务端同时配置 `--tls-cert`、`--tls-key` 和规范 HTTPS `--public-origin` 才启用 native 接口；请求必须实际使用 TLS。反向代理终止 TLS 后转发到 HTTP 后端暂不支持此接口，Host/Forwarded/X-Forwarded-* 不能替代受信 origin 或实际 TLS，不因此扩大代理信任。

六个接口仅接受 Connect unary protobuf POST，不接受 JSON、GET/query、gRPC-Web、压缩请求、浏览器 Origin/Sec-Fetch 或 cookie 认证。Begin/Bind 使用 owner bearer，其余只接受绑定的 native-purpose 子会话。响应及错误均 no-store；八个并发槽与 20 秒预算从 body 读取前覆盖解码、实际响应写出和取消排空，显式 Flush 不逃出计数。响应丢失不能重发秘密；proof 是持钥证明，不是官方客户端 attestation。详细合同见 `superpowers/plans/2026-10-29-native-configuration-transport.md`。

来源配置导入、十四类 agent 的完整专属交互、Pack 配置附件、完整历史枚举与值级/文件 diff、非 Linux 配置应用，以及明确选择后的自动应用仍为后续门禁。已交付的 Claude CLI/MyGo 手动流程不替代这些能力。完整目标见 `REQUIREMENTS.md`、`CCSWITCH-CONFIGURATION-MATRIX.md` 和 `VERIFICATION.md`，不以已交付部分缩减首发范围。
