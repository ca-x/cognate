# 首个正式版范围与门禁审计

审计日期：2026-10-31。只读观察基线：`main` / `8612bf640c041a6415e5200e44c2f3157c1b10cc`。这是独立范围审计，不是实现、运行验收或发布授权。本轮唯一写入为本文；未运行测试、构建、cgo、发布或远端修改命令，未新增 agent。

## 后继阶段进度

本段由主线程补记 2026-10-31 的后继交付，不改写下面独立审计的原始基线结论。

- C1 的原生 Grok 编辑窄范围已由 `88d3a99` 交付，仍不是本机应用或完整十四 agent。Web 共享视觉之后，本轮完成原生共享外壳、用途状态过渡及短窗可达性，独立代表复核发现均闭合；全产品首发 UI 门禁仍保持打开，见 `UI-DESIGN.md`、`NATIVE-UI-DESIGN.md`。
- C2 后继新增同切点完整引用/归档闭包与 `CaptureSnapshot` 内部密文连接，经独立 source/content 审查、密文离线回读、空实例和双页 manifest 验证；不能据此关闭 C3 产品任务/S3/管理入口、C4 生产恢复/代切换或完整 F4。
- C3 后继接入显式启用的 Linux App、独立恢复 KEK、私有加密任务日志与六个管理员 RPC：手动登记、状态分页、取消和只读精确对账。真实 TLS S3-like App 链路覆盖 HTTP 取消后继续、complete 最后、重启同 request ID 与终态操作；构造不明保留所有权的审查发现补以真实子进程锁回归。它不关闭自动调度、管理 Web 页、保留/孤儿清扫、外部供应商验证或 C4；见 `SERVER-BACKUP.md` 与后继 `VERIFICATION.md`。
- C3 控制内核初次独审曾 REJECT：最后 guard-close 不明的 reopen fence、16MiB 后继记账预留与 clean-budget poison、Reconcile 覆盖取消。后继七文件修复经 Aquinas 独立三轮负面探针 30 PASS、必要回归 27 PASS 后，三项 finding 范围内 ACCEPT；旧拒绝与旧绿色均保留，不改记为修后证据。最后物理释放确认后若 marker 删除已可见，后续清理不明允许新进程进入，但旧能力永久 closed/quarantined，不能宣称所有不明都保留物理锁。公开目录 UI 已独立导出、独审、提交 `753f6a0` 并推送，逐页账本见 `UI-DESIGN.md`。
- C3 修后新组合以隔离环境、Go 1.27.3、CGO0 重新执行全仓：7884 PASS / 161 SKIP / 0 FAIL、49 测试包；全仓/24 个实际 MyGo 包 vet、modules、Buf lint/breaking/双模板 parity、三组件十八目标构建和 metadata 通过，非文档输入与 Web 嵌入前后一致。收据及资格见 `VERIFICATION.md`；这关闭的是 Linux 手动管理员控制平面切片，不关闭完整 F4 或全产品 UI。
- 上个切片最终组合为 7768 PASS / 158 SKIP / 0 FAIL，vet/modules/MyGo vet 和三组件十八目标 CGO0 构建通过；精确命令、源码身份、原始失败及证据边界见 `VERIFICATION.md`，不转记为后继 App/公开目录切片的验证。构建不等于真实平台安装，当前仍无完整正式版放行条件。

## 结论与证据口径

**完整正式版目前不能放行。** 硬缺口是 F4 完整备份恢复、十四 agent 配置闭环、Pack 附件授权自动应用，以及 CLI/GUI/OIDC 与安装恢复的完整跨平台交付。真实 GitHub、外部 OIDC、懒猫升级/选择器及非 Linux 原生运行证据另缺。它们不是多跑一次全仓测试可以关闭的门禁，也不能降为 Linux、三个成熟 agent 或安全拒绝的兼容子集。

- 权威需求：完整读取 `docs/REQUIREMENTS.md`、`docs/TODO.md`、`docs/RELEASING.md`；核读 `docs/VERIFICATION.md` 的当前交付、独立验收边界、平台、发布与历史失败段落，并对照下面的生产代码、专项合同和可用原始收据。需求优先于 TODO 勾选、阶段文档和记忆。
- 状态词：**已实现**仅指该行明示的窄范围有生产路径和具名行为交付记录；**未实现**也包括仅有部分底座、未接产品入口；**缺直接运行证据**表示不能签收，不推断代码必然错误。复合行明确分别列出代码与证据状态，不按最低共同子集宣称完整。
- 下表“权威证据位置”同时列需求/合同与实现/收据；文件或测试存在本身不是运行通过。本文没有重新执行这些行为，历史签收不自动覆盖后继变更；后续正式候选仍需精确提交、产物身份、实际命令/结果和边界一致的证据。
- 基线 Build `41177161095` 与 Platform `41177161108` 均 success；主线程于 2026-10-31 用 `gh run view --json status,conclusion,headSha` 重新核实，两者均 completed/success，headSha 为 `8612bf640c041a6415e5200e44c2f3157c1b10cc`。它们证明该基线流水线范围，不覆盖后继提交，不证明十四 agent、真实账号、完整 F4、应用或安装已交付；本侧没有补造 job/artifact 或原生消费者收据。
- 预览交付不是 GA：三组件最初共同源码 `2bb9b13`，server rc.2 / CLI rc.1 / GUI 0.1.0 为预览；后续 server rc.4 和懒猫 rc.7 的记录见 `docs/VERIFICATION.md:583`。不能把新 main 的代码算进旧 tag。GUI 数字版本无 rc 不改变其 prerelease/unsigned 属性。
- 并行边界：主线程正做 grokclient 内存输入和 typed 脱敏读取；本审计不接管、不修改其工作。后续出现的未跟踪计划或实现 WIP 不计入上述基线资格。

## 一、可编码推进项：按依赖排序

| 顺序 / 需求 ID | 状态与真实阻塞 | 权威证据位置 | 下一具体动作 |
| --- | --- | --- | --- |
| C1 / F12-Grok-UI | 未实现完整 MyGo 编辑；service/model 已实现，Create/Update 拒绝，无生产页面注册。Web 与 schema24 导入不等于本机应用 | `internal/desktop/grok_model.go:126`；`internal/desktop/grok_service.go:10`；`docs/VERIFICATION.md:3`；`docs/DESKTOP.md:3` | 等主线程完成内存/typed API，接 native-login/custom 七字段、Keep/Replace/Clear、主页面及关闭/登出/换身份 Close；真实窗口验证，再另做本机 codec/apply。禁止临时明文文件绕路 |
| C2 / F4-闭包 | 未实现同切点数据库 + skill 全内容闭包；仅数据库候选/引用分页可用 | `docs/REQUIREMENTS.md:96`；`docs/LOCAL-SNAPSHOT.md:1`；`internal/server/state/local_snapshot.go`；`internal/server/backup/local_snapshot_crypto_integration_test.go` | 在受管屏障内固定归档引用与对象身份，流式收齐完整内容，验证缺失/损坏/GC/并发刷新；屏障释放后不得再从可变 live cache 补内容 |
| C3 / F4-发布任务 | 未实现产品级手动/自动备份、管理入口、保留和未知结果对账；加密容器与 S3 内部层已存在，不是待从零重写 | `internal/server/backup/seal.go`；`internal/server/backup/s3_target.go`；`internal/server/app.go:53`；`docs/REQUIREMENTS.md:101`；`docs/TODO.md:22` | 将 C2 输出串接 seal → 条件上传 → 完成标记 → 持久任务状态；增加 owner/admin 管理、调度、保留清理与条件删除。提交不明必须查同对象身份，不盲目重传/删对象 |
| C4 / F4-恢复 | 未实现完整离线恢复/运行代切换，不能用数据库回读或本机 config restore 代替 | `docs/LOCAL-SNAPSHOT.md:1`；`docs/TODO.md:28`；`internal/server/backup/`；`docs/CONFIGURATION.md` 的 Linux 恢复边界 | 实现离线解封、数据库/归档/引用闭包校验、隔离候选代、切换与失败回退；明确外部主密钥、凭据撤销/旧会话、schema 与不安全降级策略。完成真实停服→恢复→重启→业务读取演练 |
| C5 / F12-十四目标 | 未实现完整矩阵；有元数据不等于有渲染、表单、切换和消费路径 | `docs/CCSWITCH-CONFIGURATION-MATRIX.md:15`；`internal/server/agentadapter/builtins.json:1`；下文十四行矩阵 | 按每目标实际上游语义逐切片补资产/宿主校验/原生格式/应用恢复，保存与启用分离；已有共享内核可复用，不能把不支持全部定义成完成 |
| C6 / F12-Codex | 未实现生产资格登记；codec 识别两个 profile 不等于生产获准 | `internal/configapply/codex_preflight.go:18`；`internal/codexconfig/profile.go:43`；`docs/VERIFICATION.md:55`；`docs/CODEX-NATIVE-PROFILES.md:1` | 固定消费者/模板/ModelInfo 与启动身份，经普通生产 CLI 完整消费矩阵取得新鲜证据，再登记 `codexQualifiedProfiles`；保持 v1/v2 历史恢复与拒绝透明降级。不要直接放开空表 |
| C7 / F5/F7/F12-Pack | 未实现附件消费和授权自动应用；Store/所有者 RPC/Linux CLI 管理元数据已实现，所有目标明确 NativeUnavailable | `internal/server/state/pack_configuration.go:36`、`:235`；`internal/packattachments/client.go:353`；`docs/PACK-CONFIGURATION-CLI.md:1` | 先补 Web/MyGo 附件选择与 stale/双 CAS 审阅，再设计受权消费绑定；接订阅/同步到逐目标预览、明确 opt-in、基线重验、备份/冲突/恢复。普通分享和匿名安装不得夹带密钥或触发配置写入 |
| C8 / F6/F12-导入 | 三类选择导入已有实际路径；完整来源/agent/配置类型及凭据授权闭环未实现 | `internal/server/importer/selection_service.go`；`internal/server/importer/source_grok.go`；`docs/IMPORT-SELECTION.md:1`；`docs/VERIFICATION.md:11` | 保持 schema24 + Claude/Codex/Gemini/Grok 窄能力；补 Magpie 配置、其余目标及 MCP/instructions，CLI/MyGo 三类交互一致；源密钥省略必须显式提示，按槽独立授权补密钥，不默迁会话/GitHub token |
| C9 / F6-适配器/插件 | npx skills/通用扫描及第三方插件受控注册管理未实现完整产品入口 | `docs/REQUIREMENTS.md:128`；`docs/PLUGIN-IMPORT.md:11`；`internal/server/importer/`；`internal/server/agentadapter/registry.go` | 补 `source_type == github` lock 导入及写侧兼容、canonical 授权扫描、Magpie 多路径/缺失静默/CLI 回退；统一 manifest、受控注册/版本/权限/管理入口，验证宿主白名单与限额。不向客户端引入 moejs |
| C10 / F3/F7/§5-平台 | 非 Linux 登录/本地写入不仅缺运行，仍有生产拒绝分支；WSL copy、Windows junction/copy 与安全恢复不能靠发包解锁 | `internal/cli/auth_run_other.go:1`；`internal/install/projection.go:55`；`internal/desktop/service.go:153`；`internal/server/state/files_other.go:1` | 分别补 Darwin CLI 密钥调用入口与安全安装生命周期、Windows 受控目录/锁/ACL/投影及服务端启动、WSL 强制 copy；再接 CLI/GUI OIDC。保留不安全路径拒绝，按平台逐项验收 |
| C11 / F5-缓存/恢复 | 归档预算拒绝可用，但无引用对象 GC/完整性修复和产品故障恢复入口仍缺；不能直接删受引用对象腾空间 | `docs/SOURCE-REFRESH.md:30`；`docs/CLIENT-SUBSCRIPTIONS.md:59`；`docs/INSTALL-RECOVERY.md` | 明确引用/备份闭包的保留关系，补可中断标记清扫与校验修复；中断安装/卸载展示 needs_attention 和显式恢复，不将保守拒绝写成回滚成功 |
| C12 / 质量-SecretService | 旧失败根因 UNKNOWN；诊断增强已实现，不是已修复根因 | `docs/TODO.md:20`；`docs/VERIFICATION.md:499`、`:549`；`internal/secretservice/process_linux_test.go:628`；下文历史核对 | 保留旧失败和脱敏分类，未来复发采集同一阶段的错误类型/允许错误名/context；有界构造普通失败反例后才修根因。不凭新 CI success 关闭旧 TODO，不先加重试或延长期限 |

这些工作可在没有用户真实凭据的情况下继续编码、合成环境演练和静态验收；不能将 C2–C11 全部归因于“等用户测试”。真实平台资源是执行平台验收的前提，不是省略实现的理由。

## 二、必须取得用户或外部平台证据

| 需求 ID | 当前状态 | 权威证据位置 | 下一具体动作 / 证据提供方 |
| --- | --- | --- | --- |
| E1 / F1、F2-私有源 | 缺直接运行证据：真实 GitHub device flow、用户 Lists 勾选/水位、私有仓库访问与失权。合成授权链已实现 | `docs/REQUIREMENTS.md:65`、`:223`；`internal/server/githubauth/`；`docs/GITHUB-SYNC.md`；`docs/SOURCE-REFRESH.md:38` | 用户在可信设备完成授权；记录版本、List 选择/取消、私有首收录/刷新、撤权/轮换后的拒绝和数据保留。只留安全结果与资源代号，不索要/导出 token |
| E2 / F3-OIDC | 服务端协议/本地签名 provider 浏览器闭环已实现；真实 provider 部署、显式绑定缺直接运行证据；CLI/GUI OIDC 还须 C10 实现 | `internal/server/app.go:189`；`internal/server/oidclogin/oidc.go`；`docs/OIDC.md:29`；`docs/CLI-AUTH.md` | 部署者配置真实 issuer/client/callback；验证本地登录保留、同邮箱不合并、绑定冲突/撤销/重启/代理边界，补各客户端登录注销证据 |
| E3 / §5-懒猫 | 用户已确认安装成功，但没有确认实测包版本；OIDC、升级保留、文件选择器及 rc.7 最终下载复算缺直接运行证据 | `docs/VERIFICATION.md:25`、`:583`；`docs/TODO.md:17` | 用户回报准确版本/平台；在已有数据实例升级，回读原 UUID/密码/主密钥与数据、OIDC 绑定、选择器授权/取消；下载最终 LPK 完整字节复算发布 hash。商店接收和本地 LPK 解包不替代真机 |
| E4 / F7、F8、F12-macOS | 密钥核心有真实双 APFS 窄证据；CLI/GUI 安装/订阅/匿名安装/配置应用恢复尚未实现完整路径且缺运行收据 | `docs/DARWIN-CLIENT-KEY-CORE.md`；`docs/VERIFICATION.md:147`；`docs/REQUIREMENTS.md:140` | C10 完成后在真实 macOS、两种 APFS 上逐项运行正式候选；记录实际 OS/架构/文件系统/二进制身份、普通成功/拒绝和清理，不借密钥核心 48+3 用例外推五项产品能力 |
| E5 / F7、F8、F12-Windows/WSL | junction/copy、WSL 强制 copy、安装/同步/应用/恢复缺实现和运行证据；Windows server 安全目录同样未开放 | `internal/install/projection.go:55`、`:152`；`internal/server/state/files_other.go:1`；`docs/TODO.md:48` | C10 后在真实 Windows 与 WSL 验证 ACL、junction 失败回退 copy、WSL 不建 symlink、冲突/重启恢复与服务端私有目录；区分 amd64/arm64 产物与运行资格 |
| E6 / GUI-跨平台体验 | Linux 原生窗口 smoke 已有；三 OS 双架构打包不等于各平台安装/窗口/键盘/可访问性 | `docs/VERIFICATION.md:129`、`:323`、`:440`；`.github/workflows/desktop.yml`；`docs/DESKTOP.md` | 平台持有人使用最终包安装启动，验证真实控件、主题/语言/窄窗口/焦点、取消/关闭排空、登录/安装/同步/恢复；记录包 hash、环境与限制 |
| E7 / 发布签名 | macOS Developer ID/公证与 Windows 包签名未完成；无签名预览不能直接升级正式支持 | `docs/TODO.md:54`；`docs/RELEASING.md:1`；`.github/workflows/desktop.yml` | 维护者提供发布身份/证书与权限，接签名、公证和下载后验证；密钥不得入库或日志。随后检查实际安装包签名链，不只看 workflow 配置 |
| E8 / F4-S3 | 内部真实 TLS fixture 不等于目标 S3 部署与灾备；产品入口也尚缺 | `internal/server/backup/s3_fixture_test.go`；`docs/REQUIREMENTS.md:101` | C2–C4 后，由部署者在隔离 bucket 授权最小权限，演练备份→断连/结果不明对账→保留删除→全新实例恢复，保留对象身份/认证校验/结果，不保存凭据 |
| E9 / 正式分发 | 已有预览字节/镜像交付；当前 main 完整 GA 无逐组件候选安装和验收收据 | `docs/RELEASING.md:88`、`:120`；`docs/VERIFICATION.md:583`；`docs/PREVIEW-RELEASE.md` | 全部门禁闭合后，维护者选各自新 tag，冻结源码/清单并完成下载校验、镜像 digest 拉取、安装运行/升级和 Release 说明；不移动预览 tag，不把正式版需求转成首发后反馈 |

## 三、完整功能门禁矩阵

子 ID 是本文为追踪新增的门禁编号，不改变 F1–F13 权威定义。上面的 C/E 表是本矩阵的可执行展开，而不是可选范围。

| 需求 ID / 完整验收门 | 状态 | 权威证据位置 | 下一具体动作 |
| --- | --- | --- | --- |
| F1-A Device/manual token、GraphQL Lists、分页/选择/定时水位、服务端加密 | 已实现合成协议/领域路径；真实账号缺直接运行证据 | `internal/server/githubauth/`；`internal/server/scheduler/`；`docs/GITHUB-SYNC.md`；`docs/VERIFICATION.md:163` | E1；实测 public/private scope、slow_down/失权、取消选择和不变水位；客户端不得拿 GitHub token |
| F2-A 手动 URL/owner/repo/branch、先归档扫描再收录、来源去重、完整元数据 | 已实现有界归档/扫描/所有者路径；真实私有源缺直接运行证据 | `internal/server/library/`；`internal/repository/`；`internal/skill/`；`docs/REPOSITORY-LIBRARY.md`；`docs/SOURCE-REFRESH.md` | E1；覆盖 HEAD/隐藏目录/name-description 兜底、坏归档不入库；不改为 REST 主获取 |
| F3-A 本地账号、密码哈希、签名会话、Web/API 共用与显式管理员 | 已实现；首次管理员重启/原子性有具名 App 收据 | `internal/server/auth/`；`docs/BOOTSTRAP-ADMIN.md`；`docs/VERIFICATION.md:39` | 正式候选复核现有账号不提权、密码文件不进 argv/env/log、重启/过期/注销；懒猫升级走 E3 |
| F3-B OIDC issuer+subject、显式绑定、浏览器防重放 | 已实现协议/合成签名浏览器路径；外部部署缺直接运行证据 | `docs/OIDC.md:1`；`internal/server/oidclogin/`；`internal/server/app.go:189` | E2；不得静默合并邮箱，不能删本地账号入口 |
| F3-C CLI/GUI 跨平台认证与 OIDC | 未实现完整；Linux `auth run` 仅本地账号，GUI 本地会话不等于 OIDC | `internal/cli/auth_run_other.go:1`；`docs/CLI-AUTH.md`；`docs/DESKTOP.md` | C10 + E2/E4/E5/E6；同源、秘密输入、注销和未知结果合同逐平台成立 |
| F4-A 一致数据库/归档/仓库/Pack/分类同切点闭包 | 未实现完整；快照 Core27/维护锁仅为已实现前置 | `docs/LOCAL-SNAPSHOT.md`；`docs/VERIFICATION.md:71`、`:477`；`internal/server/state/local_snapshot.go` | C2；跨所有者内容引用、缺失对象、写入并发及资源预算不得被数据库绿测代替 |
| F4-B 认证加密、S3、自动/手动、结果对账、保留 | 未实现 App 闭环；容器/S3 内核已实现 | `internal/server/backup/`；`internal/server/app.go`；`docs/REQUIREMENTS.md:101` | C3 + E8；故障/取消/关闭不明不得返回可用备份 |
| F4-C 离线完整恢复、运行代切换、回退/凭据策略 | 未实现 | `docs/TODO.md:28`；`docs/LOCAL-SNAPSHOT.md:1` | C4 + E8；从远端对象恢复全部业务而非仅 SQLite 可打开；可选开关不意味着功能可不交付 |
| F5-A Pack CRUD/分享签名-过期-撤销/中转-校验/双 Pack 去重 | 已实现 Linux 窄闭环，跨平台缺实现/证据 | `docs/PACK-DISTRIBUTION.md`；`docs/CLIENT-SUBSCRIPTIONS.md:1`；`internal/server/packs/`；`internal/subscription/` | 对正式候选验证两 Pack 同内容只装/更新一次、撤销/过期和下载逐块授权；平台扩展 C10 |
| F5-B 源自动更新→Pack revision→下轮 CLI/GUI 同步 | 已实现公开/私有合成来源、Linux watch/MyGo 生命周期；真实账号及非 Linux 缺直接运行证据 | `internal/server/app.go:265`；`internal/server/sourcerefresh/integration_test.go:197`、`:204`；`docs/SOURCE-REFRESH.md:38`；`docs/DESKTOP.md` | E1/E4/E5；旧需求补记的“待自动更新”已过时，不重写已有 worker。已发布仓库变化须先取消公开再更新，不能后台扩大公开内容 |
| F5-C 缓存 GC/完整性、取消与中断恢复 | 部分已实现；无引用 GC/修复和完整恢复入口未实现 | `docs/SOURCE-REFRESH.md:30`；`docs/CLIENT-SUBSCRIPTIONS.md:59`；`docs/INSTALL-RECOVERY.md` | C11；保存旧引用/未知提交现场，检验满额和重启 |
| F5-D / F7-B / F12-D Pack 配置附件管理→选择→授权自动应用→冲突/恢复 | 未实现完整；元数据存储/RPC/Linux CLI 已实现，全部目标 NativeUnavailable | `internal/server/state/pack_configuration.go:235`；`internal/packattachments/client.go:353`；`docs/PACK-CONFIGURATION-CLI.md` | C7；Web/MyGo 与订阅消费者补齐，测试只针对所选目标自动应用且不影响无关配置 |
| F6-A cc-switch schema24/HEAD、Magpie、三类独立选择、票据/状态 | 已实现部分源配置与内容引用闭环；不等于本地修改迁移或十四 agent 完成 | `internal/server/importer/selection_service.go`；`internal/server/importer/source_grok.go`；`docs/IMPORT-SELECTION.md`；`docs/VERIFICATION.md:11` | C8；保留严格键名/128 UTF-8 字节与 Grok 单模型边界；不让旧“三 agent”文档漏掉已交付 Grok |
| F6-B npx skills + 通用、Magpie 多路径/回退、写侧互操作 | 未实现完整 | `docs/REQUIREMENTS.md:128`；`docs/PLUGIN-IMPORT.md:15`；`internal/server/importer/` | C9；缺失来源静默，授权文件/内容绑定与写侧兼容需行为证据 |
| F6-C 同机制插件 manifest/隔离/受控注册/宿主 fetch-storage-log | 内置 moejs 与宿主校验已实现；完整第三方注册管理及宿主能力未实现 | `internal/plugin/`；`internal/server/agentadapter/registry.go`；`docs/PLUGIN-IMPORT.md:11`；`docs/AGENT-ADAPTERS.md` | C9；固定版本/权限/预算、非法输出拒绝；插件只在服务端，不能将单进程 runtime 宣称 OS 恶意代码沙箱 |
| F7-A canonical/按已存在 agent 投影、hash/备份/卸载仅自有内容 | 已实现 Linux 安全模型；Darwin/Windows/WSL 未实现完整 | `internal/install/projection.go:55`；`internal/install/uninstall_execute_other.go:1`；`docs/CLIENT-SUBSCRIPTIONS.md:23` | C10 + E4/E5；验证有效链接/卸载无自有残留、非自有不删、目录缺失跳过、Windows junction 失败 copy 与 WSL copy |
| F7-C 安装/配置故障状态、恢复/回滚、升级兼容 | 部分已实现 journal/backup/显式恢复；完整跨平台/中断恢复未实现 | `docs/INSTALL-RECOVERY.md`；`docs/CLIENT-SUBSCRIPTIONS.md:59`；`docs/CODEX-NATIVE-PROFILES.md` | C10/C11；验证取消/断电/晚期冲突真实部分落盘，不伪称原子回滚；拒绝不安全降级 |
| F8-A 默认私有/显式发布、匿名目录详情搜索分类、免 token 完整目录下载/撤销 | 已实现服务端/Web/Linux CLI/MyGo 窄路径；非 Linux 安装未实现完整 | `internal/server/publiccatalog/`；`docs/PUBLIC-CATALOG.md:141`；`docs/PUBLIC-WEB.md`；`internal/desktop/service.go:153` | 正式候选串联隐藏/二进制/执行位/hash、取消公开逐块拒绝及无私有泄露；E4/E5 补客户端 |
| F8-B §2 外部 skills.sh 搜索 | 已实现独立外部索引请求，不被本地公开目录替代；真实外部服务当前行为缺新鲜运行证据 | `internal/search/skills.go:45`；`internal/cli/search.go`；`internal/desktop/service.go` | 在正式候选做无秘密有界搜索 smoke，记录外部不可用/429 的安全降级，不改变 Pack 只访问服务端的要求 |
| F9 分类 CRUD/多分类/筛选/公开计数/跨用户隔离/删分类不删技能 | 已实现，具名真实 SQLite/Connect 与历史浏览器验收 | `docs/PUBLIC-CATALOG.md:34`、`:141`；`docs/PUBLIC-WEB.md:35`；`docs/VERIFICATION.md:163` | 候选补整合回读：删除分类后 skill/Pack/安装仍在、hash 不变、私有计数不泄露 |
| F10 MCP 官方 SDK、token 一次显示/摘要/轮换撤销、scope/归属/过期、限界/审计/关闭 | 已实现领域/官方 SDK/浏览器路径；完整真实外部 AI 客户端操作缺本轮直接收据 | `internal/server/mcpserver/server.go`；`internal/server/mcpserver/tools.go`；`docs/MCP.md`；`docs/VERIFICATION.md:163`；`internal/server/app_mcp_test.go:30` | 用真实 MCP 客户端完成允许写/只读拒写、过期/撤销/他人拒绝及日志审计；不把 F12 MCP 配置资产当作管理 token |
| F11 中英/light-dark-system、刷新偏好/草稿、系统实时变化、键盘/窄屏/CSP/无秘密存储 | 已实现历史公开/管理浏览器范围；最新所有新增表单尚缺统一直接浏览器证据 | `docs/PUBLIC-WEB.md:109`；`docs/VERIFICATION.md:163`；`docs/VERIFICATION.md:11`；`web/src/` | 对正式候选所有新增 Grok/MCP/Pack 页面补中英、主题切换草稿、390px/键盘/axe、存储检查；state 向量/构建不能替代浏览器 |
| F12-A 十四 agent 来源版本矩阵、CRUD/排序/模式/结构化与原始编辑/切换 | 部分已实现，完整未实现 | `docs/CCSWITCH-CONFIGURATION-MATRIX.md:15`；`docs/CONFIGURATION.md`；`internal/server/agentadapter/builtins.json:1` | 按下节逐 agent 交付；上游明确 N/A 才可 N/A，未做不得包装成上游不支持 |
| F12-B MCP/提示词/工作区/路由-failover/Stack 的实际交互 | 未实现完整；Claude MCP 资产服务端/Linux CLI 仅一片，Web/MyGo 和设备应用未齐 | `docs/REQUIREMENTS.md:169`；`docs/MCP-CONFIGURATION-CLI.md`；`internal/mcpclient/`；`docs/CCSWITCH-CONFIGURATION-MATRIX.md:15` | C5/C8；按上游门控补，区分 OpenClaw Workspace/Daily Memory、Hermes Memory、Pi templates、OMO 专项，不用通用 JSON 框代替 |
| F12-C 加密资产→受权消费→本机差异/CAS→备份/状态/恢复，Web/MyGo 一致 | Claude/Linux provider 窄链已实现；Codex 生产资格、其余 agent 与跨平台完整未实现 | `internal/nativeconfig/`；`internal/configapply/codex_preflight.go:18`；`docs/CONFIGURATION.md:90`；`docs/VERIFICATION.md:440` | C5/C6/C10；未知字段只读、保留无关值、每槽独立授权、不明结果禁重放，不把安全拒绝称完成应用 |
| F13 文件结构化日志/轮转保留、管理员设置持久化、脱敏/并发/磁盘故障/排空 | 已实现 Linux 已验收范围，不是 spec-only；非 Linux 仍受平台门禁约束 | `docs/SERVER-LOGGING.md:74`；`internal/server/logging/`；`internal/server/app_logging_test.go`；下文已复核三份独立收据 | 无需重做 F13；候选复核合成秘密不入 file/stderr、管理员重启持久、普通/MCP 拒绝，C10 后补对应平台 |

## 四、十四 agent 不缩范围矩阵（F6/F7/F12）

共同正式门：来源/版本固定 → 支持的配置类型及每项实际交互 → 服务端 moejs 声明/Go 校验 → 加密私有资产与独立凭据授权 → Web/MyGo 一致审阅/差异 → 原生应用/CAS/恢复 → Pack 明确 opt-in 自动应用 → 各承诺 OS 运行证据。**任一行只有 metadata、插件 planner 或安全 Unsupported 都不能整行签收。** 技能目标目录元数据不等于该 agent 配置能力。

权威上游语义依据 `docs/CCSWITCH-CONFIGURATION-MATRIX.md:15`，固定 cc-switch `6d6b7162f122ac0ce1356a14688c8c4f712db3c8` / 4.0.21；这是已存档源码事实，不声称最新 release。当前实际模块绑定见 `internal/server/agentadapter/builtins.go:31` / `builtins.json:1`；没有绑定模块的 metadata 不构成可用 planner。

| 需求 ID / agent | 当前实现/证据边界 | 下一具体动作 |
| --- | --- | --- |
| F12-claude | 已实现 typed provider、Web、MyGo/Linux provider 手动应用/恢复和 Claude MCP 资产窄片；完整提示词/路由/failover/Stack、Pack 自动应用与跨平台未实现 | 保持独占/current 删除保护；补 MCP Web/MyGo/原生、CLAUDE.md 及上游实际路由/Stack，再过共同门 |
| F12-claude-desktop | 未实现完整；非 Claude Code 别名，现有目录/metadata 不证明 Desktop provider 和模型映射 | 独立 Desktop 表单与格式/账号保护，按上游实际本地模型映射及 Stack 语义实现；不要凭共享 Claude 名称认领 Code MCP |
| F12-codex | 已实现 typed 资产、planner/codec、观察/生命周期/CLI 编排核心；生产 `codexQualifiedProfiles` 为空。未实现完整生产消费、GUI/Pack/平台交付 | C6；官方账号与 API provider 分离，保留 parallel/effective ModelInfo 真实语义，补 AGENTS.md/MCP/路由/failover/Stack |
| F12-gemini | 已实现 provider/双文件 codec、Web 与部分来源导入；完整 MyGo、设备应用/恢复/Pack 未实现 | env/settings、API key/OAuth、MCP/GEMINI.md/路由/failover 分别验证，不把资产保存当切换 |
| F12-grokbuild | 已实现私有 Web/CLI、schema24 显式单模型导入、MyGo service/model；七字段 MyGo UI 与本机应用未实现 | C1；独占模型 default 引用与 native-login/custom 分开；再补 AGENTS.md/MCP/路由/failover，保留未知/多模型拒绝直到有无损支持 |
| F12-opencode | 已实现 V1/V2 Web 资产编辑及声明 planner；完整 MyGo/本机共存应用、MCP/提示词/OMO 专项与 Pack 未实现 | 保留 npm/options/headers/variants/cost/递归值与顺序；实现 add/remove 而不是全局独占 current，OMO/OMO-slim 不冒充路由聚合 |
| F12-openclaw | 未实现完整，未见绑定生产渲染模块；metadata 不算应用 | 补 provider 共存、primary/fallback 分离、agents/env/tools、Workspace/Daily Memory；上游无 MCP 不强造 MCP |
| F12-hermes | 未实现完整，未见绑定生产渲染模块 | 区分内置 provider key/custom key，保留 models/rate-limit，补 MCP/Memory/SOUL.md 与原生恢复 |
| F12-pi | 未实现完整，未见绑定生产渲染模块 | Enable/Remove 共存、Pi API/provider/model、MCP、AGENTS.md/SYSTEM.md/templates，避免独占模型 |
| F12-mcode | 未实现完整，未见绑定生产渲染模块 | 仅允许 custom 节点与默认引用保护；可复用表单但须独立格式/原生 codec，补 MCP/AGENTS.md |
| F12-kimi | 未实现完整，未见绑定生产渲染模块 | 实现共存及原生 default_model 特例、官方 OAuth 节点保护、MCP/AGENTS.md；不能按共存通则抹掉默认模型 |
| F12-dsh | 未实现完整，未见绑定生产渲染模块 | 作为独立 agent 实现 apiKeyEnv、独立格式、MCP 与 DSH_HOME/AGENTS.md，不当 mcode 别名 |
| F12-agy | 未实现完整，未见绑定生产渲染模块 | 官方临时启动参数选模型、第三方 key 私有目录启动，独立 MCP 开关与 `~/.gemini/config/AGENTS.md`；不能直接套 Gemini 文件 |
| F12-omp | 未实现完整，未见绑定生产渲染模块 | Pi-family Enable/Remove + discovery 专项、MCP/AGENTS.md，分别验证不覆盖未知本地项 |

不要求上游明确没有的能力；也不据此删掉目标本身。完整范围是十四个各自的真实能力，不是强行十四个同构 provider 文本框。

## 五、架构、非功能与正式交付门禁

| 需求 ID | 状态 | 权威证据位置 | 下一具体动作 |
| --- | --- | --- | --- |
| §2/§3 技术栈、单二进制/无 cgo、MyGo 原生非 Web 模式 | 已实现当前依赖/构建架构；不是各 OS 产品资格 | `go.mod:1`；`cmd/cognate-desktop/`；`.github/workflows/build.yml`、`desktop.yml`；`docs/REQUIREMENTS.md:29` | 后继保持 Go/ent+entsqlite/moejs/MyGo/Echo/Connect/Buf/Cobra/Afero/S3 和既定 Web 栈，检查 CLI/GUI 依赖不引入 server/moejs 或 cgo |
| §2 获取/元数据/skills.sh/canonical、§5 并发限速/取消/hash/资源预算 | 已实现若干生产路径和合成拒绝测试；完整平台与新模块资源证据未齐 | `internal/repository/archive.go`、`private_archive.go`、`private_metadata.go`；`internal/search/skills.go:45`；`internal/install/`；`docs/SOURCE-REFRESH.md` | 候选保留完整目录/隐藏/二进制校验、429 冷却和取消排空；明确每层限额不是进程总 RSS 证明，不放宽限额以绕过实现缺口 |
| §5 密钥加密、日志/错误不含明文、目录隔离/主密钥保全 | 已实现若干已审边界；Windows 安全路径和完整备份密钥灾备未完成 | `internal/server/state/`；`internal/clientkey/`；`internal/secretservice/`；`docs/SECURITY-LOGGING.md`；`docs/VERIFICATION.md:573` | C4/C10；审阅新入口秘密传播，仅空实例可生成 key，已有数据丢 key 必须恢复原 key；不能降权限校验“修”部署 |
| §3 Connect/Buf schema、旧 namespace/路径/加密派生兼容 | 已实现工具链与旧标识保留策略；新功能还需增量兼容验证 | `proto/`；`buf.yaml`；`docs/COGNATE-RENAME.md`；`.github/workflows/build.yml` | 正式候选对基线做 Buf 兼容检查/生成一致核对；禁复用字段标签，旧数据路径/HKDF 域不能机械改名 |
| §5 三 OS × amd64/arm64 CLI/server 构建，GUI 原生包/窗口 | 已实现构建/部分打包；完整运行/安装/签名缺实现或直接证据 | `docs/RELEASING.md:88`；`docs/VERIFICATION.md:129`；`docs/TODO.md:48` | C10 + E4–E7；Linux amd64 本地通过不外推 arm64、macOS、Windows/WSL，不以原语 CI 代替产品链 |
| §5 独立 tag、双架构镜像/匿名 digest 拉取、CLI/GUI 草稿及许可证/hash | 预览分发已实现并有收据；本次完整 GA 未交付 | `docs/RELEASING.md:1`、`:29`、`:88`；`.github/workflows/verify-server-image.yml` | E9；新源码用新 tag，失败发布先核对远端状态，不重推旧 tag/盲重发；最终下载完整文件复算及运行，非只信上传成功 |
| §5 懒猫 Exec、独立包装仓库/版本、仅喵喵、私有卷和首次管理员 | 包装/发布及安装成功已有窄记录；升级/OIDC/选择器/最终下载缺证据 | `docs/VERIFICATION.md:25`、`:583`；`docs/BOOTSTRAP-ADMIN.md` | E3；保持应用专属卷 0700、显式 data/key 目录、root 支持，不碰共享祖先；官方商店继续关闭 |
| §7/§8 完整验收与升级/恢复/限制说明 | 未完成；里程碑建议不是范围裁剪授权 | `docs/REQUIREMENTS.md:223`、`:234`；`docs/TODO.md:57`；本文全部矩阵 | 按 §7 九项验收逐项绑定真实闭环；完成所有未实现项并关闭缺证据门，冻结候选，做独立审查，再决定正式 tag |

## 六、历史失败与证据核验

### Secret Service：已记录修复与旧 UNKNOWN 必须分开

1. 历史 `4f1df548228a7d99076258cccc29f883616048bc`（`fix(ci): provide portable private Secret Service fixtures`）确有**已记录的 fixture 可移植性修复**：在标准 bin/sbin 目录找 daemon、Ubuntu 安装两项测试依赖，缺依赖仍 fatal；只改 workflow 与 fixture。这不是晚于 `41161084838` 的 OpenSession 根因修复，不能拿标题“fix”消掉后者。
2. `73ddf19` 的 Build `41161084838` 实际 failure，`TestProcessExitReclaimsLiveWorker` 在业务断言前 `fixture-open-session` 失败；Platform `41161084843` success 是另一范围。`docs/VERIFICATION.md:499` 明确保留这个事实，旧 TODO 仍有效。
3. 后继 `b4361dc4aec9d6f90ab71320a22b4311a89d6927` 是**诊断增强**：固定八个 D-Bus 名、deadline/canceled/other、错误类型与 context_done，不输出任意错误文本/body/未知 name；没有重试、期限放宽或生产行为修复。200 次原例通过和整包 32 PASS 证明没在本机复现，不证明远端根因解决。对应原始观察日志本轮重新计算 SHA256 为 `e1a5baab54a9719021f9d67f1b0668862eb28d066d333c968bd77528fb9c9f3d`，与 `docs/VERIFICATION.md:549` 一致。
4. 所以应保留“原失败原因 UNKNOWN，已加安全取证”的状态。最新两次 CI success 只记为后继通过，不能覆盖历史 UNKNOWN。下一次需要同阶段分类证据；若欲关闭待办，应提供可定位的反例/修复或明确、可审计的风险处置，而非再凑若干绿色次数。本审计不修改 TODO。

### 本轮核到的直接材料及局限

- `/tmp/cognate-grok-import.SIpZg5z4/combined-full.json` 重新计算 SHA256 为 `3eb70e03e8b5e5bb2e3b53413abda6643388c861cb198d79d1640e29f0a5fd3b`；`final-full.json` 为 `7470d77f69ca65ac062717f51ddcb431d75b8071682bd7d63a37388b0c1ff4ae`，均匹配 `docs/VERIFICATION.md:3`、`:11`。这仅确认引用日志身份，没有在本轮重跑 7523/7517 用例，更不把 skip 变 PASS。
- F13 三份独立收据本轮重新计算并匹配 `docs/SERVER-LOGGING.md:74`：`.superpowers/sdd/2026-10-29-server-logging/task-1-fix1-review.md` → `ff9a757a4ff2f3491c6cffb79536e6d9edcb4c7af4ba249c1e9b46dc3a917e03`；`task-2-3-final-review.md` → `c87b4f8dfe7bd0f2bac480eddd4b5f818dd49ada639a6cfdde985a5bd1d101e8`；`combined-final-review.md` → `d33b3012e17527c5787599f7544298c347b6a3ba3f4f712f3f231afc932707ca`。不得把原“日志需求待实现”的历史话术继续当当前缺口，也不外推非 Linux。
- `docs/VERIFICATION.md` 按阶段保留大量早期“未做”陈述：源自动刷新、私有来源、watch、三类选择导入、GUI 和发布必须结合后继生产路径读，不能仅搜索 TODO 字样判缺失。相反，最近一段 full green 也不能覆盖旧 fixture/APFS/Docker 发布失败；Docker 许可证后继的真实双架构闭环与 Secret Service 的 UNKNOWN 性质不同。
- 懒猫最新“用户安装成功”只关闭启动故障的设备验收阻塞；此前 rc.4/rc.5 的 UNKNOWN 留作历史，不再说当前安装仍失败。实际安装版本未单独确认，不能自动签 rc.7、OIDC、升级、选择器或超时的最终下载探针。

### 定向 Nowledge 检索记录

只执行一次 CLI memory search：`nmem --json m search 'skillhub 正式版 SecretService fixture 8e5944d5-cc6c-4bd1-9eee-bb4f243b1062' -n 3`。使用已配置身份和默认 Space，未切换作用域；模式为 CLI 默认，未额外加 label/date/source 过滤。可见相关命中包括 Secret Service 诊断交付及早期完整范围约束；本轮未保留搜索输出文件，故不补造完整排名、模式内部名或分数。

另仅定点 `nmem --json m show 8e5944d5-cc6c-4bd1-9eee-bb4f243b1062`，标题“Cognate Grok来源导入与MyGo底座已推送：严格TOML、128字节名称、7523测试”，返回 `space_id=default`；show 无 rank/score。该记忆确认本基线 MyGo 未注册 UI、Create/Update 拒绝和主线程下一切片，与源码/验证记录一致，只作背景而非验收权威。没有抓完整历史线程、扩图、另开 agent 或写入中间推测记忆；本侧仅允许审计文档写入，不启动需要本地生成文件的图谱回退。

## 七、UI 首发硬门禁（用户追加，2026-10-31）

用户最新明确：**UI 精致度、科技感与有用途的动效，是首个正式版本的一大重要目标，不是首发后优化。** 本节作为新增首发门禁，与原功能、安全、十四 agent、备份恢复及跨平台门禁同时成立，不替换或降低任何原审计结论。权威来源为本次用户追加要求；本轮仅补审计文档，没有进行 UI 实现或重新运行浏览器/原生窗口验收，不能据已有 F11、截图或测试绿色宣布本节完成。

| 需求 ID / 首发门 | 当前状态 | 权威证据位置 / 必交材料 | 下一具体动作与放行条件 |
| --- | --- | --- | --- |
| UI-01 精致度、科技感与一致设计 tokens | 缺完整首发视觉验收证据；不能以功能可用替代设计目标 | 用户追加要求；`docs/DESIGN.md`；`web/src/styles.css`；`internal/desktop/view.go`；`internal/desktop/configuration_view.go` | 明确可截图核对的视觉方向，统一语义色、字体层级、间距、圆角、边框、阴影及动效 tokens；Web 与原生保持一致的信息层级和状态语义，并尊重平台控件习惯。逐屏消除临时样式、密度失衡和不一致状态，不以堆砌霓虹/装饰制造科技感 |
| UI-02 Web/原生成熟组件与交互一致性 | 既定组件路线已有；新增页面及完整产品一致性缺专项证据 | `docs/REQUIREMENTS.md:29`；`docs/REQUIREMENTS.md:44`；`docs/DESKTOP.md`；实际组件清单与运行截图 | Web 继续使用既定 shadcn/ui 等成熟组件及 TanStack，桌面继续使用 MyGo 原生控件，不改 Web 容器、不自造基础组件轮子；核对表单、弹窗、导航、确认/错误/忙碌状态与键盘行为，不能仅统一颜色而保留交互割裂 |
| UI-03 真实截图对照、真实数据与状态 | 缺冻结正式候选的完整逐屏对照证据；既有窄范围截图不自动覆盖新增页面 | 精确提交/产物、真实运行的 Web 页面与各承诺平台原生窗口截图、视口/语言/主题/数据来源清单 | 用同一可追溯场景做修改前后及设计目标对照，覆盖加载、空态、成功、拒绝、冲突和结果不明。产品不得用虚构统计、活动、同步进度或成功状态填版；验收可用明确标注的合成数据，但不得冒充真实用户数据、真实平台交付或实际业务成功。截图须清除凭据，不用 mockup/headless 控件树替代真实渲染 |
| UI-04 中英、light/dark、375px、键盘 | F11 有历史窄范围验收；本首发组合与所有新增界面缺完整直接运行证据 | `docs/PUBLIC-WEB.md:109`；`docs/VERIFICATION.md:163`；中英 × light/dark × Web 常规/375px、原生受支持窗口尺寸截图及键盘操作记录 | 对 Web 与原生逐项验收中英完整文案、浅深色可读性；Web 验证 375 CSS px，原生验证产品支持的最小窗口尺寸，不擅自增加桌面移动端支持承诺。长文本/校验错误不得截断关键操作或横向溢出；验证 Tab/Shift+Tab、可见焦点、弹窗焦点进入/返回与无键盘陷阱。另保留 system 实时跟随、偏好持久化和切换不丢草稿 |
| UI-05 有用途动效与 reduced-motion | 缺完整交互清单、真实运行及减弱动效对照证据；不能用静态截图证明动效通过 | 各动效的用途/触发/属性/时长/曲线/中断退出清单；真实录屏或逐帧记录；Web prefers-reduced-motion 与原生系统减弱动效运行记录 | 动效只服务状态反馈、层级/空间连续性或用户操作因果，保持统一 motion tokens；可取消、可中断，不阻塞输入，不用无意义循环/闪烁装饰，不把动画完成当业务提交成功。开启 reduced-motion 后提供即时或低运动替代，信息与功能不丢失，Web/原生均验证 |
| UI-06 独立视觉签收与首发收口 | 缺完整首发 UI 签收，不因当前构建或功能验收通过而豁免 | UI-01–UI-05 证据索引、截图问题清单及修复后对照、精确候选身份、独立视觉/交互复核结论 | 在功能实现过程中并行打磨，冻结候选后逐屏复核科技感、精致度与可用性；所有承诺平台/页面的阻断问题及证据缺口闭合后方可签正式版。不能把本节移入“后续优化”、仅验 Linux/Web 或用组件库默认外观直接视为达标 |

本节执行次序：先建立视觉方向与 tokens/组件基线（UI-01/02），随 C1/C5/C7/C10 的页面实现持续取得真实对照（UI-03），再完成组合状态与动效验证（UI-04/05）及独立签收（UI-06）。**原放行规则仍适用，且正式版还必须同时通过上述 UI 首发门禁。**

## 放行规则

先完成 C1（与主线程衔接），并行推进 C2→C3→C4 与 C5/C6/C8/C9；在各目标消费安全可用后闭合 C7；C10/C11 不能延后到 GA 后才算支持。同时预约 E1/E2/E3/E4–E8 的真实资源，不让外部验收成为最后才发现的阻塞。C12 保留历史风险与有界取证。

每个剩余门应补“精确源码/产物 → 命令或真实操作 → 实际结果与拒绝/恢复场景 → 环境/权限 → 日志/截图/对象身份及 hash → 独立复核边界”。所有行在原定范围闭合之后，才进入 E9 新标签交付。**本审计不宣布首个完整正式版完成，不批准缩 scope，也不要求回滚已有预览。**
