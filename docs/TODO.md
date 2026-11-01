# Cognate 后续任务

- [x] Grok provider Web 资产编辑：native/custom、七模型字段、密钥三态、无凭据复制、只读未知字段及 Unknown/CAS 防重放；不包含本机应用与 MyGo。
- [x] 懒猫首次管理员、UUID、OIDC 与 GitHub 安装向导；rc.4 已由成功 CI 提交喵喵商店。
- [x] 懒猫安装启动：2026-10-31 用户确认安装成功，关闭原 master-key 启动故障；平台 OIDC、升级数据保留与文件选择器仍待单独验收。
- [x] 服务端增加 `--init-key-if-missing`：程序内随机生成、进程间锁、不覆盖发布，仅空数据目录可生成；已有数据丢 key 则拒绝。
- [x] 懒猫包装收紧自身私有卷、显式传递目录并切换程序内初始化，移除脚本生成；固定 `server-v0.1.0-rc.4` / `a576aeb`，增加发布前隔离启动门禁。rc.6 被 CI 测试脚本的挂载权限门禁拦下，未发商店；rc.7 修正测试脚本暂存路径，发布结果单独记录。
- [x] 服务端首次管理员启动参数及原子初始化：已有实例拒绝自动提权，重启不覆盖原身份或密码；使用私有密码文件交接。

更新：2026-11-01。服务端、CLI、GUI 的既有预览分发不等于首个正式版；本轮目标是补齐完整正式版门禁后发布，而非给预览更换标签。没有勾选的条目不代表已经可用。用户允许多 agent 并行：主线程集成、独立验收与互不重叠的客户端实现分工推进。

## 发布收尾

- [x] 修复并核对原生测试Web embed构建前置：f5faa0c的run41263657100缺dist失败保留；3aa1766新run41264397380四job成功，真实Mac双APFS/core51及原五项普通产品RED、两个封闭artifact的API digest/CRC/manifest/清理均核实。只关闭缺构建输入，不扩大Darwin客户端支持或关闭旧APFS UNKNOWN。
- [x] 发布服务端 `server-v0.1.0-rc.2`，验证 amd64/arm64、匿名拉取与镜像 digest。
- [x] 发布 CLI `cli-v0.1.0-rc.1` 六平台归档、许可证与 SHA256SUMS，核对下载产物。
- [x] 发布 GUI `gui-v0.1.0` 八份预览安装产物与 SHA256SUMS，标明未签名及平台限制。
- [x] 在 `lazycat-contrib/cognate-lzcapp` 构建 Exec LPK `0.1.0-rc.2` 并提交喵喵商店，官方商店关闭。
- [ ] 懒猫平台 OIDC、升级数据保留及文件选择器验证；安装成功已由用户确认。
- [ ] 首发后收集真人测试反馈，优先处理数据安全、登录、安装与同步问题。
- [ ] 排查 Build `41161084838` 的 Secret Service 私有 fixture `fixture-open-session` 失败；本地同例连续 200 次通过，尚未复现，新增脱敏错误分类待捕获证据。不能用本地通过覆盖原远端失败，暂不添加重试或调整期限。

## 服务端

- [ ] F4 完整备份与恢复：一致数据库快照与 skill 归档、仓库、Pack、分类形成同切点内容闭包。
- [x] F4 内部内容源：全 owner 引用分页、归档去重和语义验真、确切 B2 reader pin、关闭排空及清理不明保留；独审发现的生命周期和错误优先级问题已闭合。
- [x] F4 内部 Capture 接入既有密文容器：外层成功后才封完成标记；源移走后的认证离线回读、空实例、129 extent 双页和 64MiB+1 分块验证通过，不等于生产恢复入口。
- [x] 备份维护锁释放接缝：先关闭目录能力，成功后才释放物理锁；故障注入与真实第二进程验证完成。锁本身关闭失败仍按结果不明拒绝重入，不声称仍持有物理锁。
- [x] C3 Linux 显式启用的管理员手动控制平面：同切点密文上传、持久幂等任务、六个 Connect RPC、显式取消与精确只读对账；App/RPC 及内核三项反例独审闭合，修后全仓与十八目标 CGO0 构建通过。不是完整 F4 或外部供应商资格。
- [x] 备份手动管理 Web：真实管理员边界、显式全实例确认、同 request ID 继续、浏览器记录清除、任务详情/分页/取消/只读对账；复用 React/TanStack/Radix，保留服务端私有凭据，不代表完整 F4。
- [x] Linux 默认关闭的固定延迟自动备份：私有间隔配置、实例 actor、原子单活/Unknown 阻断、Serve 启停排空及 Web 策略状态经非作者独审；真实60秒周期和当前构建浏览器链通过，不代表完整 F4。
- [ ] 保留/孤儿清扫与跨任务磁盘回收；任务上限不能代替完整生命周期治理，未决任务与幂等历史不能被自动删除。
- [ ] 离线完整恢复与运行代切换，明确凭据撤销和回退策略；内部快照库不等于可用备份功能。
- [x] T3 Linux 运行根 SH/EX lease 内核：独审限定 ACCEPT，逐 holder 持久 fence 防关闭不明后重入；legacy 启动参与另由 T3S 接线，稳定命名与停用不合作旧程序的部署前提不可省略。
- [x] Linux 同步 snapshot worker 继承同 OFD 根 lease，覆盖真实 Start/取消/Wait/父死退役；exact14 经非作者限定 ACCEPT，组合全仓9049 PASS/173 SKIP/0 FAIL、vet及18个 CGO0 构建通过。保留双 SH 和正常 SQLite crash recovery；不等同完整 R2/R3。
- [ ] 核查独审一次 reap/Close 后正向 EX 150ms 探针超时：后继30次诊断与原码10轮未复现，根因仍 UNKNOWN；不放宽阈值或用重复绿次抹去原失败。
- [x] T4 Linux EX 同 OFD inspector、候选完整撤销/写后验证及唯一所有权移交：exact26 经非作者限定 ACCEPT，当前组合全仓9206 PASS事件/176 SKIP/0 FAIL、vet及18个 CGO0 构建通过；真实 EMFILE/并发 Close 下在 owning lease 内隔离原资源，不关闭旧 R2 UNKNOWN 或完整 F4。
- [ ] R3 运行代发布、持久认证 intent/current、真实 App 资格演练与回退，以及离线恢复 CLI；T4 的 pending/ready 发布接缝不是已可使用的恢复入口或 restore_verified 证明。
- [x] T3S Linux legacy 启动准备：业务/key 写前 SH、typed fresh-root key 证明、保留痕迹拒绝、App/HTTP/日志完整排空及不明所有权保留经独审；没有 Resolve、worker 父死或进程退出后的 durable 业务故障 fence，不放行完整 R2/R3 或离线恢复。
- [x] 恢复传输前置 T2a：Afero S3 精确密文复制，完整验证与物理响应关闭后才可信；独审通过。没有恢复头引导、候选闭包或产品恢复入口。
- [x] 恢复验真 T1：只读隔离数据库、原 key/policy 与全部保留密文检查；非正 ID 全域准入 P1 经原七反例独立闭合，exact12 主线集成与真实 worker 验证通过。不是归档闭包、运行代切换或恢复产品入口。
- [x] 恢复传输 T2b 头引导：显式私有 parent FD、有界只读 bootstrap/complete scope；exact3 独审通过，隔离主线组合与旧 S3 回归通过。不是认证、候选发布或恢复产品入口。
- [x] 恢复 T2c 纯 inventory 检查：严格头、精确 canonical 尾、owner/归档集合、内容预算及既有 closure 摘要；三文件独审和真实生产者/T1 对接通过。被动期望不是候选发布 capability。
- [x] 恢复 T2d 纯归档语义检查：精确 bytes/hash、复用 Afero 安全解包/扫描、逐个共享仓库完整技能目录 path/contentHash 比对；exact2 独审及隔离组合通过。只验证被动声明，不认证来源或磁盘文件集合。
- [x] 恢复 CopyFile 目录定位：沿用认证目录排序改为二分及匹配范围，保持 extent 完整性与关闭/取消；exact3 独审 ACCEPT，不是完整恢复或端到端加速倍数。
- [x] T2 Candidate 核心：材料化真实 DB/inventory/归档文件集合，绑定 T1 和完整共享仓库技能；源关闭屏障、累计预算及关闭生命周期经独审，staging panic 自等待 P2 已闭合。不包含远端 Source 或发布能力。
- [x] T2 可信 S3 Source bridge 内部 API：头 scope 外完成全对象认证，所有解封/输入 scope 关闭成功后才开放候选验证；原 Capture 密文经 TLS 到真实 Candidate 的组合经独审，不含生产 CLI 或真实云供应商资格。
- [ ] 生产离线恢复命令与真实 S3 权限/供应商验收：接通原始 key、S3 Source、候选和运行代全过程；内部 API 通过不等于用户已能执行恢复。
- [ ] 定位真实 MinIO QA 一次 Capture.WithFiles/上传 ErrInvalid；已固定官方源码 CGO0 工具并跑通真实密文链及拒绝场景，但后继五进程通过不关闭原失败。只加固定类别阶段取证，不先给产品加重试；见 `VERIFICATION.md`。
- [x] 修复后继真实复现的 HTTP412 无 APIError 条件请求误分类：完整回读后才 Existing、DELETE 只 Conflict，原 typed API 语义/条件头/单次请求不变；独审及两包809 PASS、六目标 CGO0 构建通过。原291具体来源与供应商缺 APIError 深因不冒称已还原。
- [x] R4 恢复凭据撤销内部事务 helper：五类会话链、share/MCP、GitHub 在途状态按固定时刻退役，保留业务及原密文/AAD；非作者独审和主 T3S 组合定向通过。非幂等，不含事务 coordinator、发布或恢复产品入口。
- [x] GitHub needs_reauthorization 的明确重新授权 CAS：所有者经现有 Catalog 取得预期 generation，沿 PAT/device flow 验新凭据后原子生成新代/新密文并转 ready；旧 AAD、stale/refreshing/跨 owner 与旧票据门保持。非作者独审通过，不代表恢复产品或真实账号已验收。
- [x] T1 辅助配置关系 gate：在既有只读 worker 核验排序及 Pack 附件的 owner/分区/成员/序号；公开预算域和 LIMIT 溢出 P2 经原独审闭合，不改变合法 stale/Unsupported 读取语义。
- [ ] 用真实 GitHub 账号验证 device flow、Lists 选择、私有仓库权限和失权行为。
- [x] 来源重新授权的 SQLite 517 六阶段修复及 N1 可取消准入：限定独审 Spec/Quality ACCEPT，新组合 395 条浏览器证据、取消/跨连接重复专项及修后全仓通过。只有精确 BUSY 的尚未开始 BEGIN 使用 5ms/10s 准入轮询，不重放业务、CAS、提交或网络，见 `SOURCES-UI.md`。
- [x] 备份认证 SQLite `8714` 的确定性锁机制修复：O_PATH inode pin、父进程 metadata 检查和受限 worker 完整有界检查，非作者独审 ACCEPT；新组合 630 次 TLS/SQLite/S3-like 备份、管理页及修后全仓通过。不加恢复性重试，历史 v7 根因仍 UNKNOWN，不把此片当完整恢复，见 `BACKUP-SQLITE-LOCKS.md`。
- [x] Secret Service 私有 broker 就绪握手：修复 Lstat 在 bind 后/listen 前误报就绪；真实 syscall 延迟旧 RED/新 GREEN、整包五轮及修后全仓通过，非作者独立对照后限定 ACCEPT。不据此归因旧 `fixture-open-session` 或等待断言，见 `SECRET-SERVICE-FIXTURE-READINESS.md`。
- [ ] 用真实 OIDC provider 验证部署登录及显式身份绑定；保留本地账号。
- [ ] 完善归档缓存无引用对象回收、完整性修复及故障恢复入口，不直接删除受引用内容。
- [ ] 完成第三方服务端插件的受控注册与管理流程；插件继续仅在服务端执行。

## AI 配置与导入

- [x] npx skills v3 global lock 选择导入的服务端、CLI 与现代 Web：GitHub-only、有界解析、显式已收录技能绑定与 Unknown 只读回执；核心及 W1 焦点修复均独审 ACCEPT。不含锁写回、canonical 通用扫描、MyGo 或完整 F6。

- [ ] 按 cc-switch 能力矩阵完成各 agent 的 provider、模型、MCP、提示词交互，不把元数据支持当作可应用。
- [x] 接通 Grok Build CLI 编辑器与认证白名单，保留完整终端审阅、密钥独立批准、CAS 和 Unknown 禁自动重放；不开放本机应用。
- [x] Grok Build cc-switch schema24 显式单模型来源导入：七字段、env 存在位、无源密钥迁移、票据绑定，Web/CLI 审阅与真实 Connect/SQLite 提交回读；不含完整导入矩阵或本机应用。
- [x] MyGo Grok 私有资产 service/model 底座：所有者核验、安全读取、无凭据复制、删除、排序、审阅确认及 CAS/Unknown/关闭排空；后继已接生产 UI 入口。
- [x] 为 MyGo 编辑补充 grokclient 的内存输入与结构化脱敏读取接口；独立复制密钥与模型，默认脱敏，不通过临时明文文件绕过。
- [x] 验收 native/custom 七字段及 Keep/Replace/Clear 原生表单；独立审查发现的排序提交误清脏标记与空闲切页误报取消均已修复并通过回归和复核。Linux headless 原生渲染不代替真人跨平台验收。
- [ ] 补齐 Grok Build 本机应用；Web/MyGo 资产管理及受限来源导入已单独交付。
- [x] Codex 普通 CLI 唯一固定 profile 登记：Linux/amd64 0.166.0 effective-v2 完整 tuple，真实普通 CLI/PTY/App/SQLite和新鲜消费者矩阵、独立指令/effort负控与可读许可包验收通过；其它平台、版本和 MyGo不据此开放。
- [ ] 完成各 agent 本机配置应用、差异预览、冲突拒绝、恢复及版本兼容；不静默丢弃未知字段。
- [ ] 完成 cc-switch/Magpie 的 skills、仓库、配置独立选择导入闭环，保留来源版本与权限边界。
- [x] MyGo 三来源选择导入入口：复用共享选择协议，独立上传批准、三类默认不选、真实 owner/仓库 pin、最终审阅及一次提交；Unknown 仅按原操作查询，取消和身份切换实际排空。两项功能分别独审后合入同一模型，新增真实 App/TLS/SQLite 共享生命周期回归；不开放来源尚不支持的候选或完整跨平台资格。
- [x] Web 私有 MCP 连接配置资产编辑：有序stdio/HTTP/SSE、显式启停/存在位、凭据三态和每旧引用Clear、preview/CRUD/无密钥复制/排序、Unknown屏障及主题语言草稿保持经非作者独审；真实浏览器与原生200%缩放通过，不连接MCP或应用设备。
- [x] MyGo Claude schema1 MCP 配置资产完整编辑：有序stdio/HTTP/SSE、存在位/启停、凭据三态/每旧引用Clear、CRUD/无密钥复制、精确审阅/逐槽批准、CAS/Unknown与身份排空经非作者限定 ACCEPT；普通Linux窗口真实登录及一次新建保存通过，不执行MCP或应用本机。
- [x] MyGo MCP 后继独立普通 Linux 实窗：已登录 update/无密钥 copy/delete、四类旧引用的 KEEP/明确空 REPLACE/CLEAR、一次非空替换及已提交丢响应 Unknown、取消排空和真实 reduced-motion 代表链通过；仅为实际记录的有限矩阵，不推断其它 Unknown/transport/平台组合。
- [ ] MyGo MCP 普通窗口逐凭据与 transport 的完整矩阵、其它 Unknown/在途身份变化、真人/跨平台体验与本机配置应用资格；不把有限实窗、组件闭环或构建当作这些验收。
- [ ] 接通 Pack 配置附件的客户端选择、独立授权、消费和显式启用后的自动应用；公开分享仍不带配置密钥。
- [x] MyGo Pack 所有者附件元数据编辑：完整有序引用、双 revision、精确一次确认、CAS/Unknown 只读核对、取消和身份排空；分别独审及共享生命周期回归通过，当前组合普通 Linux 窗口完成真实登录与一次替换读回。只保存引用，不读取正文/密钥，不代表客户端消费、授权或自动应用。
- [x] Pack 配置附件所有者 Web 元数据管理：有序引用、双 CAS、显式审阅、Unknown 只读核对；143/156 读取/移动/移除焦点 PC-F1 独审 CLOSED。只保存引用，不读取正文/密钥，不代表客户端消费或自动应用。
- [x] Pack 元数据读取的同步重复激活去重（P3）：组件内同步 guard 与 finally 释放，真实双激活/后继显式读取/失败释放/Unknown 零重放通过；独立旧/新构建验收 CLOSED，不声称全站查询互斥。
- [x] MyGo 通用配置分区排序：完整元数据、未知schema/250项、全排列独立CAS、一次审阅与Unknown显式重读、内容Dirty及generation排空经非作者独审；Linux headless窗口及主线338 PASS/1 SKIP/0 FAIL通过，不代表全平台真人验收。
- [ ] 补齐 MyGo 其它 agent 编辑及应用状态入口，不能用 Claude 已支持推断全部 agent 可用。

## CLI、GUI 与平台

- [ ] CLI 跨平台账号交互与 OIDC；本轮 `auth run` 仅支持 Linux 本地账号。
- [x] 合入已独审 Darwin CLI 密钥调用候选及原生工作流：当前组合全Go/vet/交编与封存有限正反例通过，保留新Web前置和清理拒绝门；不代表取得原生资格。
- [ ] Darwin caller原生资格：首次run41266403574在core/caller步骤实际失败，三ZIP仅refusal/unconfirmed，双卷清理成功。先补有界角色/子断言诊断定位UNKNOWN，再取得真实caller50/core51及三份封闭收据；不重跑旧run刷绿，密钥准入不等于安装/Pack/GUI资格。
- [ ] 完成 macOS/Windows 本地安装、订阅同步、配置应用及恢复的逐项运行验收；交叉编译不算运行支持。
- [ ] GUI 各平台真实安装、窗口交互、键盘与可访问性验证；已有 Linux 窗口 smoke 不覆盖其它平台。
- [ ] macOS Developer ID 签名与公证、Windows 安装包签名；预览包保持 unsigned/ad-hoc 标识。
- [ ] 服务端 Windows 安全数据目录与启动支持；当前 Windows server 仅能构建，不能据此部署。

## 正式版验收

- [x] Linux 公开实链 smoke：真实公开 GitHub→站内分类/显式公开→匿名 CLI 安装与取消公开，经独立源码/产物绑定及 fakeCLI 行为 RED；只断言一个安装目录/SKILL.md，不代替完整 F8、文件树 hash、UI 或跨平台验收。
- [x] 配置工作空间 Web 精修与排序：五分支、密钥三态及 Unknown/CAS 保持；pending 保存和取消重读两项焦点 P2 经 143/156 独立旧/新对照闭合。只代表配置资产管理界面，不代表全部 agent 本机应用。
- [x] 修正 OpenCode provider 表单的旧排序“后续阶段”文案（独审 P3 CLOSED）；中英文说明指向真实可用入口，独立旧/新构建对照、窄屏几何与零写入通过，不借文案扩大可用能力。
- [x] 原生共享外壳首轮精致化：深浅分层、持续选中导航、身份/反馈层级及克制的活动状态过渡；独立代表复核短窗/125%字号下鼠标与键盘停止同步，修复不改变业务操作或新增依赖。
- [x] 公开目录/详情精致化：真实搜索/分类/分页、来源与匿名安装分区、中英深浅/键盘/ReduceMotion；独审推动 200% 几何与复制标签碎字 RED→GREEN，七文件独立导出验证并推送 `753f6a0`。不是全产品 UI 签收。
- [x] 登录/账号 UI 切片：I1 fixture 终止契约和 M1 后台失败焦点修复均经非作者复审 ACCEPT；最终组合 8 项场景、49 安全图、32 组布局通过，四案门禁接入 CI，不包含外部 provider 成功或完整 F3。
- [x] 仓库/分类 UI 切片：F1 丢删除回执的错误/焦点和 F2 查询失败取消焦点修复经非作者复审 ACCEPT；最终组合 65 条证据、24 组布局通过，保留公开 opt-in、私有来源和所有者边界。
- [x] Pack/分享 UI 切片：后台分享查询失败的焦点 F1 经非作者复审 ACCEPT；最终组合真实业务 132 条证据与独立焦点专项通过。配置附件自动应用仍是独立功能缺口，不假装设备已同步。
- [x] MCP/日志管理 UI 切片：缓存错误、权限/秘密、CAS/Unknown 与真实管理业务已独审；撤销后焦点 F1 在 Chromium 143/156 独立复核 CLOSED。最终主线 180 条收据、160 组矩阵及七案终态门禁通过，非客户端 MCP 资产或全产品签收。
- [ ] UI 精致度、科技感和用途动效为首发重要目标：共享设计与关键页先行，保留真实前后截图、中英/深浅主题/Web 375px/键盘/reduced-motion 与原生运行证据；不以本阶段 Web 打磨代替全产品签收。
- [ ] 按 `UI-DESIGN.md` 首发逐页账本覆盖真实工作空间入口与状态，不以公开目录、共享外壳或关键配置切片推断其它页面已验收。
- [ ] 对照 [需求 §7](REQUIREMENTS.md#7-验收要点) 完整验收，并记录真实账号与真实平台的证据边界。
- [ ] 验证两 Pack 重合去重、源刷新后的下一轮同步、公开安装与取消公开、分类隔离。
- [ ] 验证 MCP 权限、过期/撤销、跨用户隔离、日志隐私，以及中英主题和窄屏键盘交互。
- [ ] 补齐升级、拒绝不安全降级、备份恢复及已知限制说明，再决定正式版标签。

详细合同保留在 [需求](REQUIREMENTS.md)、[配置](CONFIGURATION.md)、[桌面](DESKTOP.md)、[本地快照](LOCAL-SNAPSHOT.md) 和 [发布](RELEASING.md) 文档中；本清单不替代这些合同或缩减原定范围。
