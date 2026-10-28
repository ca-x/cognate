# 开发进度

目标：完成需求文档全部功能并发布首个可用版本。首版尚未发布；M2–M7 不算完成。里程碑由独立用户代表验收，用户在首发后进行真人测试。

## 2026-10-29 已推送检查点

- `02339276` 接入前台 `pack watch`：复用共享订阅内核，立即首轮、串行周期、有界退避，每轮释放安装锁后才输出或等待。独立验收关闭满管道取消问题；Linux pipe/TTY 使用独立非阻塞句柄，普通文件仍受不可中断内核 I/O 限制，未知 writer 启动前拒绝。非 Linux 原生 stdout watch 暂不支持，不能把交叉编译通过当作当地安装/持续同步通过。它不代表源仓库自动刷新或 MyGo 自动同步已接通。
- `b282ede5` 接入独立 native auth/state seam：真实 owner MAC、绑定证明和持久父子 session 一起校验。独立验收发现并修复 COMMIT 后已取消仍发布凭据的问题；现扣留返回凭据但保留已提交子记录，不伪造回滚、不自动重试。最后一次 context 检查与 return 不是原子操作，清理可变 scratch 也不保证 Go 字符串/运行时副本物理清零。未接 native HTTP、Prepare/Grant、配置消费或本地文件应用。
- `88610c9c` 新增合作式 maintenance 原语：每个嵌套 writer lease 独立计数，过期父 lease 不得新借，异步子 lease 阻止 drain；实例锁与 handle 生命周期已有独立语义负控验收。Store/App/实际 writers 尚未接入，不能称备份切点安全或灾备完成。此前一次辅助验证误开 CGO1，证据保留但排除签收；正式复验全部 CGO0，不宣称 race-detector 覆盖。
- 三个提交均已 SSH 推送并读回相同 SHA；Actions `40757385125`、`40758500265`、`40759173580` 对应构建全部 completed/success。本地各自干净源码 npm/build、完整 Go tests/vet、module verify 与相同 delta 的组合验证通过。
- `7a709be3` 修复上述 MCP 测试准入竞态：先握手确认真实 partial body 已消费，再单次探测限流，并给 Close 独立的原值超时预算。独立调度反例旧红新绿、语义负控和完整 CGO0 验证通过，Actions `40762071345` completed/success。保留 `0c59971` / Actions `40749215897` 的旧失败，未改该测试的中间绿色不算根因修复。
- `a895cfd3` 接通 owner 手动源刷新和 Connect RPC：真实抓取后按 hash/publication revision CAS 提交，保持同路径技能身份/分类，受影响 Pack 各升一次版本。已公开内容变化、被引用删除、别名冲突或异常引用整次拒绝。独立代表先在旧候选发现孤儿 PackItem 校验遗漏，修复原反例百轮绿和负控红后才签收新树；旧拒签记录保留。完整 Go/Web/Buf 双模板生成一致性及独立 SQLite/HTTP 验证通过，Actions `40762804410` completed/success。这不是定时源更新、私有内容获取或完整 F5。
- `f02944bf` 接通 Web 明确确认的“检查源更新”，与页面只读刷新分离；固定打开时 CAS、应用级只提交一次、取消不声称回滚、未知结果不报成功。发布变更同步刷新库详情的 generation，真实 Publish→Unpublish→Refresh 已旧红新绿。独立真实浏览器覆盖两 Pack、实际提交后响应截断、取消/换号/迟到回填、中英主题与 390px。三个后续提交均已 SSH 推送读回；Web Actions `40765110690` 在本次记录时仍进行中，不预报构建成功。自动源 worker、MyGo 后台同步及首发仍需后续交付验收。

## 提交节奏

- 用户于 2026-10-28 明确要求每完成一个阶段就提交和推送。阶段应先完成独立验收和相应测试，再由主线程串行提交相关源码、生成协议与文档，推送当前分支，并核对远端提交和 CI 的实际状态。
- 不把临时验收工作区、构建目录、依赖目录、数据文件或凭据纳入提交；不为提交清理或覆盖用户工作。阶段提交不代表创建 tag、发布首版或完成尚未验收的里程碑。

## 2026-10-28 当前阶段验收汇总

- 新增 Magpie 有限导入链路：Web 明确选择 JSON → 授权上传 → 服务端 moejs 无状态预览 → 默认不勾选的仓库候选 → 按选择顺序复用既有 Collect。私有默认值、逐仓事务、缓存验证和自然键去重不变；不迁移本地 skill 修改或客户端配置。独立代表最终 Spec PASS / Quality PASS，确认 P1/P2 为 0；本地与干净 index 验证均通过。
- 新上传入口解码前会话鉴权和准入（全局 2、每 owner 1），2 MiB wire/1 MiB 原文，拒绝压缩，真实 TCP 证明 5 秒慢 body 截止与关闭排空；原文不落盘、不记日志。普通 RPC 大小限制不放宽。
- Magpie 源码已 SSH 提交推送为 `bf556fd`。CI `40667951091` 首次暴露既有分发 deadline 断言的取消顺序竞态，独立代表在旧基线 140 次中复现 5 次；没有夹带无关修复或放宽断言。同一 SHA 重跑 attempt 2 的 tests、六组二进制及双架构 OCI 已全部成功，风险与覆盖边界保留在 `VERIFICATION.md`。
- 最终生产 Web bundle 的真实浏览器复验已通过：先预览再勾选、语言/主题保留选择、两仓串行收录且单项失败继续、重复收录不重复、在途取消保留成功/当前待确认/后续未尝试、换号清草稿和隔离资料库、390px 无横向溢出；本机真实公开 GitHub 来源扫描出 15 个带 hash 技能。详见 `VERIFICATION.md`。

- 首批源码已提交并通过 SSH 推送：`91bcb261`；真实 CI 暴露一处 symlink 测试 fixture 的 inode 不复用假设，保留失败并经独立代表复核修正，未放宽生产身份算法。修正提交 `0924a5b` 已 SSH 推送并读回一致，Actions `40659619868` 的 tests、六平台 CLI/服务端二进制和双架构 OCI 构建全部通过；容器实际运行、MyGo GUI 和首发仍未验收。
- 以下旧章节是按时间保留的过程记录，其“进行中/未接线”由本节限定范围的最新状态覆盖：客户端订阅/显式同步/去重卸载、Lists 调度、Magpie 只读预览、公开后台/匿名 CLI 消费、MCP 后台和 Web 管理均已有独立代表验收。
- 公开后台 SQLite 争用、MCP 查询后过期及协议 deadline、Web 提示翻译重建 client 导致草稿丢失均已由原实现者修复、原代表定向复验关闭；不通过弱化身份或秘密清理边界掩盖问题。
- 可复现验证及明确未覆盖范围写入 `VERIFICATION.md`。整合提交与远端 CI 以 Git 和对应 SHA 的 Actions 为准，不在过程记录中预先宣称提交、推送、Docker 运行或首版成功。
- 插件保持服务端 moejs；Magpie 授权 JSON 预览和仓库选择收录已接通，cc-switch 一致导出上传、完整插件机制、skill 内容迁移及客户端配置附件/自动应用仍待完成，不能把这个切片当作完整 F6。

## 2026-10-28 并行客户端、调度与新增范围

- 更新：下载器孤立 PAX、调度 deadline 耗尽导致跨重启饥饿、client.key FIFO 阻塞三项独立发现均走单独修复与原代表复验；round1 均已关闭。之前“进行中”是过程记录。客户端订阅、Magpie 只读预览和卸载的限定范围独立 gates 通过，不等于 F5/F6/M2 全部完成。
- 新增 F11 主题/语言需求写回 REQUIREMENTS，并保存 Nowledge `55d2844e-2f68-44c5-ad65-942f69449c1d`：默认跟随系统，浅色/深色/系统三态，简体中文/English，匿名与管理界面，偏好持久化而非 token 持久化，严格 CSP 不变。Web 实现正在实际浏览器验收。
- 主线程接通独立匿名消费者 `packclient.NewPublic` 与 `install-public --server ORIGIN --skill ID`，复用有界验证下载、canonical/hash/投影/独立归属；不让空 token 绕过私有 Pack，不创建 client.key。App 接 MCP/公开服务并在库关闭前取消等待依赖。实际 ent+缓存+本地来源归档 fixture 的 App/Connect/CLI 集成通过默认不公开、确认公开、分类/搜索/删除不改 hash、二进制/隐藏文件/执行位、重复安装、取消公开拒绝；这是本地真实协议测试，不是实际外部 GitHub 账号/源更新验收。
- 最新局部证据：Go1.27.3/CGO=0 packclient/install/cli/server tests 三轮退出0（2.597/12.661/0.992/1.166s），相关五包 vet 退出0；SPA 深链接回归与 Buf lint 通过。MCP/公开后台/新增消费者/UI 仍按各自冻结快照独立验收，最终全量/生成/Web/六平台整合验证待合并 gates 后运行。

- 按用户最新授权拆分下载器、服务端调度、Magpie 适配和卸载执行四个实现 agent；主线程实现客户端 ent 订阅 desired/applied 状态、加密凭据、安装归属、整合与 CLI。独立审查席位与实现者分开，证据/裁决在本轮计划与 `.superpowers/sdd/2026-10-28-client-subscriptions/`。
- 客户端真实 HTTP 集成测试接通：跨 Pack 同 slug/hash 仅下载/安装一次，显式同步复用、Pack revision 更新先备份卸载、部分失败保持 applied 不变且重试不重下已完成项、最后引用才卸载、既有本地匹配安装不被认领删除。CLI subscribe/list/sync/remove 实际服务往返和本地链接/备份测试通过。
- 凭据使用独立 HKDF/AES-GCM，AAD 绑定 ID/origin/Pack，根密钥位于私有 `~/.config/skillhub/client.key` 而非 SQLite；软/硬链接、过宽权限、坏 key/错误 key 拒绝。补充回归先复现“已有订阅丢 key 自动生成替代”，随后改为必须恢复原 key；不再误生成。
- Magpie 的限定只读预览独立审查双 verdict ACCEPT；卸载执行在可信稳定路径假设下无 P1/P2。下载器独立审查发现尾部孤立 PAX 元数据被误认作 tar 终止块，保留三种红例交回原实现 agent 修复，未跳过阻塞项。调度器独立审查与客户端整合验收仍在进行。
- Lists 定时/持久水位线/选中列表条目跳过、过期重新拉取与 CAS 栅栏自动收录由调度 agent 实现并报局部测试通过。此处“公开”指上游 GitHub 仓库可公开获取，不代表发布到匿名目录；源替换、私有获取仍待完成，不能混淆 F8 opt-in 发布。
- 用户追加的成熟前端组件、不登录公开目录、技能分类、匿名安装和自管 MCP token 已完整写回 REQUIREMENTS F8–F10 与 `PUBLIC-CATALOG-AND-MCP.md`，保存 Nowledge `a40d3a2b-93d8-488e-8e4d-95d54b2113c7`。MCP 与公开目录协议/Web 分别有独立实现 agent，尚未验收，不宣称功能已交付。
- 最新主线程局部证据：CGO=0 Go1.27.3 的 subscription/state/install/cli tests 与 vet 退出 0；全量与跨编译待合并修复后再运行。全部代码仍未提交、推送或发布。

## 2026-10-28 Pack 分组、分享与流式资源

- 用户再次询问前端进度，已明确 Web 初版不等于前端全部完成，MyGo 原生 GUI 和首发仍未完成。本轮推进 F5，不缩减原目标。
- 新增 ent Pack/PackItem/PackShare、同用户成员校验、revision CAS、失败回滚、可移植安装别名、同 slug/hash manifest 去重；分享只持久化摘要，独立 HKDF/HMAC 签名，创建一次返回原文，可过期/撤销。具体数据与协议见 `PACK-DISTRIBUTION.md`。
- 接入 Buf 的 PackService/DistributionService、Go/TypeScript 生成及 App。资源只通过服务端读取已核验缓存，重新核验子树 hash 后，确定性 tar.gz 同步分块发送；区分逻辑 hash 与传输 SHA256。Create/Update 的 HTTP 白名单为 64 KiB，其余仍 4 KiB。
- Web 增加「组合」：跨仓库选择/筛选/alias/编辑删除、分享有效期、一次性令牌展示/隐藏、确认撤销。原文不进入 query/mutation 缓存、URL 或存储。沿用既有资料库视觉与可访问性系统，不增加装饰性动画或动画依赖。
- 真实浏览器收录 `vercel-labs/agent-skills` 默认分支与显式 main 两个快照，各 9 个技能；三个选择在 manifest 去重为两个。实际 Connect 流的 gzip、连续 offset、正常 final 与传输 SHA256 核验通过；alias 更新后旧分享跟随 revision 2，旧 manifest 下载 `aborted` 且无数据帧。另一个 session 提交 revision 3 后，网页旧编辑失败并保留输入。分享撤销和 Pack 删除后，新旧分享均返回 401。320/390px 无横向溢出，axe A/AA 零违规及零待人工项；浏览器测试未输出分享凭据。
- 已执行第一轮全量 CGO=0 test/vet/mod verify、Web build/audit、Buf lint/Go+TS 生成比对和 CLI/server 六平台编译；CLI 依赖仍不包含服务端、moejs 或 OIDC provider SDK。
- 独立代表第一轮已通过跨 Store CAS、SQL 故障回滚、跨进程签名与撤销、最大 256 项 HTTP/manifest、归档范围与 hash、实际 20 秒阻塞写上限和 App.Close；发现「数据库查询跨过期仍授权」和「短 Connect 超时未中断阻塞写」两项 P2。保留代表回归并本地确认预期红，再将授权改为查询后有效期复核/签名再验、传输改为实际 Connect ctx 的读写 deadline，并保留监听直到 HTTP trailers 结束；两项本地均转绿，真实 TCP 用例约 0.23 秒退出。代表二轮正在复验，整个 Pack 子阶段尚不宣称验收通过。
- 代表增补生命周期回归：发现正常协议完成主动 cancel 子 context 会误伤 keep-alive，本地复现第二次响应 503 后修复。读写 deadline 改为同一协调点：真实到期/外层取消立即截止，正常协议取消恢复外层期限，并在锁下重核避免关闭与恢复竞态。半包 Close、正常连接复用、HTTP/1 短超时与 HTTP/2 窗口背压的到期/关闭回归本地连续五轮通过；新增测试依赖只将既有固定版本 `x/net` 从间接标记为直接，不升级依赖。
- 最终独立复验通过：过期/短超时、半包 Close、HTTP/2 窗口背压超时与关闭各重复十次，最后连接复用回归十次通过（1.403s）；本子计划阻塞项已关闭，不等于完整 F5/M4 通过。此前“正在复验”为过程记录，最终结果以本条为准。
- 最终全量验证退出 0：CGO=0 全部 Go tests、vet、mod verify/tidy diff、Buf lint/Go+TS 生成比对、Web 生产构建与生产依赖审计（0 漏洞）、CLI/server 六平台编译。QA 浏览器/服务和临时密钥、数据库、截图、二进制已清理；远端 Actions、Docker、真实外部授权及非 Linux 原生仍未验证。
- 续接检索：nmem CLI，query `Cognate 公开仓库收录 Web 技能浏览接通`，mode `fast_bm25_vector`，筛选 `-n 1`，返回 space_id `default`；rank 1 为 `849d6ff7-7259-4f81-ac44-cb2cb62b1966`「Cognate 续接：公开仓库收录与 Web 技能浏览接通」，score 0.99。CLI 无法确认 Nowledge 图浏览器的同一 owner/space 会话，因此按技能要求跳过自动图展示，保留检索证据。
- 保存前去重检索：nmem CLI，query `Cognate Pack 分发 令牌`，mode `fast_bm25_vector`，筛选 `-n 2`、返回 space_id `default`；rank 1 为 `48555360-fb14-4bad-8c6a-cc48336d45cc`「Cognate 续接：服务端入口与首版 Web 面板已接通」，score 0.9415329726968429；rank 2 为 `99073a0e-6328-40bf-9f97-211e1222fc71`「Cognate 续接：GitHub 设备授权与 OIDC 登录绑定已接通」，score 0.8905406607078119。未命中本阶段专门记录；图同样按 CLI 会话身份限制跳过。

仍待：客户端 Pack 消费/安装同步与跨 Pack 去重、Lists 定时/增量及实际自动收录/更新、M2 卸载更新/恢复和 Windows/WSL、S3、完整服务端插件导入/客户端配置附件与应用、MyGo 原生 UI/打包，以及真实外部授权、Docker/远端 CI/跨平台原生与首发验收。当前代码未提交、推送或发布。

## 2026-10-28

- 核对需求全文和 Nowledge 续接记忆，确认当前仍无 Web 面板和原生 GUI。
- 按用户纠正将 moejs 与 cc-switch 适配器迁到服务端，移除客户端插件命令。回归测试证明旧命令不可执行；CLI 构建依赖检查覆盖服务端与 moejs 边界。
- 将 slug 路径规则放入 skill 领域包，解除服务端导入器对客户端安装与状态层的依赖。
- 新增独立 `skillhub-server` 入口、私有数据目录、外部主密钥文件、可选 TLS、受控 HTTP 监听及有序停机。通过真实 HTTP 注册、登录、重启后会话验证和错误主密钥拒绝测试。
- 认证入口增加有限容量限速、请求体限制、Origin 检查、通用错误和不缓存响应，避免请求正文进入日志。
- 接通 Connect GitHub API：手动 token 验证与加密保存、Lists 发现、订阅选择及手动同步。模拟 GraphQL 上游的真实 HTTP 测试验证两账号隔离，不代表真实 GitHub 验收。
- 新增 React 管理面板：本地账号注册登录、来源连接、Lists 选择、仓库元数据浏览与筛选；Go 内嵌静态产物并支持已实现页面的深链接。使用原始 Logo 的 128px 衍生图，按登录后路由延迟加载，未引入外部字体或分析请求。
- 浏览器验证注册后登录、来源页、仓库空状态、退出清空输入与存储；390px 布局无水平溢出。来源页 axe WCAG A/AA 检查无违规。注册页初始检查发现流程插图的 ARIA 角色不完整，已补 `role=img`。
- 独立代表发现并驱动修复：默认 Host 来源校验、TLS 私钥权限、创建目录前的祖先校验、Web 缓存到期隐藏。GitHub catalog 改为同一事务读取列表与仓库，并向客户端提供由观察期限和凭据期限共同约束的失效时间。
- 自动构建增加 Web 产物、Go/TypeScript Buf 生成比对、服务端二进制及服务端 Docker 入口；尚未运行远端 Actions 或 Docker。
- 本机没有 `crapper`、`dryer`、`mutator` 命令；本轮使用行为测试、类型检查、vet 与独立审查验证，未宣称运行这些工具。
- 独立复验剩余 sticky 目录竞态后，进一步将每层 `Lstat` 身份与打开的目录句柄绑定，核验属主及私有权限后才写入下一层。代表的 `0777` 竞态夹具已通过；其跨 UID `chown` 未成功，不声称验证了真实跨 UID 攻击。
- Web 过期隐藏在浏览器中用短有效期响应夹具验证：先显示列表，到期后内容隐藏且出现重新同步提示。agent-browser 网络替身不带 Content-Type，最初测试超时；最终使用仅限该测试页面的有效 JSON Response 夹具验证，未放宽产品协议校验。
- 最终验证：`npm run build --prefix web`、生产依赖 npm audit、`CGO_ENABLED=0 go test ./...`、`go vet ./...`、`go mod verify`、Buf lint 与 Go/TS 生成比对通过。CLI 与服务端的 Linux/macOS/Windows × amd64/arm64 编译通过，非 Linux 原生运行、Docker、远端 CI 与真实 GitHub/OIDC 未验。

## 续接来源

本轮使用 nmem CLI 查询 `Cognate moejs cc-switch 服务端 插件`，返回模式 `fast_bm25_vector`、scope `default`，筛选 `-n 3`。依次命中 `00ddbdf3-0fc6-4318-bad6-fc4377f62d80`（moejs 与 cc-switch 预览，score 0.99）、`d997ef30-2bb3-4b00-a97c-1cf71e743185`（权威需求全文，score 0.9693432410725604）、`ccfed0c3-2e8c-4783-97f3-63724a75f5ed`（历史技术讨论，score 0.8692263761444201）。插件位置以用户最新纠正与 `6149f906-6bb6-45be-85c0-e0f9ff6db311` 为准；旧前端选型不覆盖文档。当前 CLI 宿主无法确认浏览器的同 owner/space 会话，未自动打开记忆图。

## 2026-10-28 设备授权接线

- 接通按账号隔离的后台 device flow，客户端只获取用户验证码和任务状态；token 经 Lists viewer 验证后条件加密保存。
- 增加启动、状态与取消的 Connect API、Cobra client ID 配置及管理面板按钮；申请 `user` scope 需明确勾选并展示包含个人资料写入权限。
- synctest 验证成功、取消、拒绝、过期、旧授权不能覆盖新凭据，以及关闭期间启动和凭据读取的取消/等待。HTTP 测试验证跨账号不可查询或取消，设备码不会返回客户端，过期凭据可重新授权。
- 独立复审指出启动凭据读取在生命周期登记之前，以及终态任务挤占容量；已移动登记并添加终态驱逐，64 个终态之后仍可授权的回归先失败后通过。
- OIDC 背景查询参考 `crystal_119364277f16`（score 0.99）和 `2f0ef4a9-04a4-42ef-8bc0-5c5ee2f4dfd3`（score 0.7165699516098085）；本项目不采用其邮箱自动合并策略。已读取 cat-led 的 OIDC 和签名会话源码，计划使用 issuer + subject、ID token 验证、nonce、PKCE 和显式绑定。
- 浏览器已验证设备验证码显示、私有 scope 勾选、官方验证链接、取消后隐藏验证码及 axe 零违规。初始页面替身错误地把 Uint8Array 请求体直接 JSON.parse；替身改用 TextDecoder 后通过，产品协议未放宽。
- 进一步修复容量扫描与 Close、Cancel 被凭据写入锁阻挡的生命周期问题：状态使用单一原子值，取消先发 context 信号再读取结果；已提交的结果仍返回 Complete，不承诺撤销已授予权限。

## 2026-10-28 OIDC 接通

- 新增 ent OIDCIdentity 唯一 `(issuer, subject)`、不可变用户归属；OIDC 与本地账号共用已签名的随机会话和 session hash，不按邮箱合并。
- 接通 discovery、S256 PKCE、nonce、签名 ID token、issuer/audience/expiry/azp 验证、固定 callback、短期浏览器绑定与一次性 cookie 领取。登录、账号绑定和 URL 标记清理已接入 React 面板。
- 独立代表复现绑定前撤销会话仍可提交，以及 SDK 后台 JWKS 请求不随关闭取消；已将原会话摘要核验移至绑定事务，并增加拥有取消及等待能力的传输层。代表对两项定向复验通过，未宣称真实 provider 或整个 M3 通过。
- HTTPS 集成测试验证 Secure/HttpOnly/SameSite/host-only cookie、另一浏览器拒绝、固定无 token 重定向及一次性领取。签名 provider 的完整浏览器流程验证显式绑定与后续 OIDC 登录均保留原用户；390px 无溢出，axe A/AA 零违规。
- 手动浏览器夹具首次因当前 agent-browser 不支持 addinitscript 命令而未完成并按时退出；改用其支持的 AGENT_BROWSER_INIT_SCRIPTS 后，完整流程及夹具正常退出通过。
- 最终全量 `CGO_ENABLED=0 go test ./...`、vet、mod verify、Buf lint/Go+TS 生成比对及前端生产构建通过；CLI+服务端 Linux/macOS/Windows × amd64/arm64 编译通过，客户端依赖仍不含服务端、moejs 或 OIDC provider SDK。生产前端依赖审计无已知漏洞，当前仅代表本机验证；Actions、Docker 和非 Linux 原生仍未验。

待续：定时与增量同步、实际仓库收录、pack 分发同步、实际卸载更新、S3 备份、完整服务端插件导入、管理面板其余功能、MyGo 原生 GUI、客户端打包与首发。真实 GitHub device flow、真实 OIDC provider、非 Linux 原生运行、Docker/远端 CI 尚未验证。

## 2026-10-28 仓库内容收录与前端进展

- 用户询问前端是否完成：已明确仅有 Web 初版，MyGo 原生 GUI、pack/订阅同步/完整导入/配置应用/备份界面未完，未宣称首发完成。
- 新增服务端公开 GitHub 整仓归档获取、Afero 有界提取与扫描、ent 收录仓库/技能模型、内容 hash 私有缓存，以及 Buf/Connect 收录/列表/详情接口。网页现可输入 owner/repo、URL 和分支收录，浏览真实名称、描述、路径、hash；Lists 元数据单独展示并可手动收录公开内容。
- 缓存先同步再事务入库，owner/name 大小写和 HEAD 归一，单用户隔离；并发重复收录保持唯一记录及技能。20 秒上下文、两个重型 IO 槽位、关闭取消并等待；默认 512 MiB 全局对象预算计入遗留文件，策略持久化且不同实例必须一致。当前没有在线改预算、GC 或归档自动修复，详见 `REPOSITORY-LIBRARY.md`。
- 真实 `vercel-labs/agent-skills` 归档实测收录 9 个技能；先发现 GitHub PAX global header 被误拒，定位并增加红→绿兼容回归。独立代表再发现异常 PAX 字段、重复收录缺失归档、无引用增长、最终并发返回漏验和配置不一致预算问题，均补回归及修复；最终定向复验通过，不等于整个里程碑验收。
- 浏览器验证默认分支/HEAD 大小写幂等、显式 main 单独快照、重启保留内容、技能筛选、无技能仓库不入库且保留表单。临时 QA 归档损坏后详情显示明确错误且无技能卡，恢复并重试回到 9 个技能。修复手机导航文字挤压；320/390px 无横向溢出，axe A/AA 零违规及零待人工项。
- 全量无 cgo test/vet/mod verify、前端 build/audit、Buf lint/Go+TS 生成比对以及 CLI/server 六平台交叉编译通过。小型归档 benchmark 约 0.40ms/op、1.53MB/op，不代表大仓库压力验证；无 cgo race、Docker、远端 Actions、非 Linux 原生和断电测试没有声称完成。
- 下一步继续 pack 分发核心链路、Lists 实际自动收录/更新与 M2 卸载恢复，然后 S3、服务端插件完整导入及客户端配置应用、MyGo 原生界面/打包；首发全量目标保持不变。仍未提交、推送或发布。
- 进度已存为 Nowledge `context`：`849d6ff7-7259-4f81-ac44-cb2cb62b1966`（公开仓库收录与 Web 技能浏览接通），供跨机器接续；测试浏览器、临时服务均已关闭，临时密钥/账号库/归档/构建产物已清理，仅保留项目源码和构建所需 Web 产物。
