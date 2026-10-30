# 开发预览验证

## 2026-10-31：Native HTTP 配额测试改用被动观测

Grok Build 候选全仓运行实际失败于 `TestCodexNativeTransportLifetime`：八个慢 body 未达到测试探针期待的配额状态，原失败保留。原探针通过 `service.Call(no-op)` 自身占槽，可能使同时入场的第八个真实请求被拒；受控交错已在未改生产服务的情况下复现同一断言失败，不推断原运行的精确调度或归因为机器负载。

现在仅在测试中被动记录八个真实 body 的首次成功读取；仍验证第九个真实 HTTP 请求返回 429/no-store、Close 取消排空和停止后拒绝新工作。服务容量 8、20 秒期限、原 5 秒测试边界均不变。双入口重复 25 轮通过；将生产容量临时编译为 9 时，两条原第九请求断言均普通失败，恢复后通过，独立验收亦通过。包含该测试修复、Grok 候选、已验收客户端编排及许可数据的固定组合全仓为 6,835 PASS、110 显式 SKIP、47 个测试包、零 FAIL，vet/module verify 通过；跳过不代表消费者或平台资格。旧 logging/Browse 历史失败仍保留其各自未明原因，不由此改绿。

## 2026-10-31：Darwin 密钥核心限定运行验收

`f422bb1` 的实际双 APFS 安全套件与调用隔离合计 51 项通过，实际清理及两个封存 artifact 已经独立核验。旧 `646fe4d` 的控制器前置检查失败保留，不追溯改绿。CLI、Desktop、安装和 Pack 的五项产品失败也仍保留；本结果只授予该提交与原生环境的密钥核心资格。完整身份、测试范围和 artifact 摘要见 [Darwin 密钥核心记录](DARWIN-CLIENT-KEY-CORE.md)。

本记录自 2026-10-28 开发预览阶段起按提交保留验收与失败历史，各节描述对应时间点，不是首版发布或 M2 至 M7 全部完成的声明。最新 MyGo Claude 原生配置交付见末节。权威功能范围见 `REQUIREMENTS.md`；持续进度见 `DEVELOPMENT.md`。

## 2026-10-30：配置事务取消测试的清理锁竞争

Build `40985001579`（源码 `671db64`）实际失败于 Gemini 日志隐私测试内的 `DROP TRIGGER` 清理步骤，二进制与 Docker jobs 因此跳过；原失败保留，不用重跑替代诊断。

在 Go 1.27.3、`CGO_ENABLED=0`、隔离 HOME 下，单核调度使原 Gemini 事务取消测试重复 500 轮全部在清理处失败，1000 个 proto/JSON 子例均观察到 SQLite `SQLITE_BUSY(5)`；原 Codex 同类测试重复 10 轮的 20 个子例也在清理处失败。较高并发度下原测试可以通过，不能以一次绿色证明无竞态。

`database/sql` 的取消回滚可能先将事务标为 done，再完成物理回滚；此时显式 `Rollback` 返回不证明数据库写锁已经释放。测试用于建删 trigger 的独立连接原先没有 busy timeout，可能立即与仍在回滚的连接冲突。修复仅让这两个测试连接通过 file URL 的连接级 DSN 使用 `busy_timeout(10000)`，与 Store 一致；不改生产事务、HTTP 取消、零持久化断言或隐私断言，也不忽略清理错误。

同一单核条件下，候选 Gemini/Codex 事务测试各重复 100 轮通过，完整 Gemini 失败日志隐私测试重复 20 轮通过。候选完整仓库 `go test -mod=readonly ./... -count=1 -timeout=600s` 与 `go vet -mod=readonly ./...` 实际 exit0，保持默认包并行。Build 工作流新增单核定向回归（三轮），并保留原全仓测试，防止仅靠默认调度掩盖回归。以上是本地结果；后继提交的 Actions 状态须按其自身 SHA 核查，不借用旧绿色。

## 独立验收范围

- Linux CLI Pack 订阅、显式同步、跨 Pack 去重与保守卸载；匿名公开 skill 安装复用内容校验，但不隐式加入订阅，不需要 token 或 client.key。
- 服务端 Lists 定时增量/公开来源收录、公开目录与分类、默认私有及取消公开后的匿名拒绝；分类变更不修改 skill 内容 hash。
- MCP 用户级 token 生命周期、权限/有效期校验、取消与关闭行为，以及 Web 管理和一次性显示。后续 `e707dc0` 已接通分类与发布 tools/scopes，详见末尾阶段记录；不能把该增量追溯为早期切片已具备。
- Web 匿名和管理界面的 light/dark/system、zh-CN/en；保留主题/语言切换前的未提交表单。独立代表保留并重放了 OIDC 失败提示后切换语言导致 MCP 草稿丢失的回归，修复后双向通过，且登录会话切换的清理边界未被削弱。
- cc-switch 在后续 `f429da9` 已接通自包含只读 SQLite snapshot 授权上传及显式选择仓库收录；Magpie 同样支持授权上传预览与仓库选择。下节保留早期切片及实际失败历史；这不等于 skill 内容或客户端配置迁移已完成。CLI 和 MyGo 不执行插件。

验收不是仅凭构建结果：公开目录使用实际浏览器与受控或公开归档来源；MCP 管理使用真实嵌入式服务端和浏览器；后端争用/过期/取消及客户端安装另有保留回归测试。截图只在一次性凭据已清除后采集；不保留账号、密钥、数据库、会话导出或含凭据 HAR。

2026-10-28 最终阶段整合审查结论为 Spec PASS / Quality PASS，限本阶段整合接缝读审与冻结输入、既有验收证据核对；新增确认 P1/P2 均为 0，另有三项非阻断文档建议。该审查未重新执行全套测试或浏览器验收，不代表 M2–M7 全量完成、首发验收或远端 CI 成功。

## 可复现检查

### Magpie 上传与仓库选择收录

2026-10-28 本轮独立代表最终结论为 Spec PASS / Quality PASS，确认 P1/P2 为 0。代表在冻结副本独立执行全部 Go tests/vet、Buf lint/生成/breaking 和定向真实 TCP 测试；另以可控 RPC 的真实 React 界面独立验证取消、大小限制、延迟旧账号 401 回填和容量/未知错误停止，最终嵌入式服务上验证真实授权上传、HEAD/head、键盘、偏好与清理。以下另行记录实现者/主线程证据，不混称为代表亲自重复全部交叉构建或真实 GitHub 收录。

- Go 1.27.3、`CGO_ENABLED=0`：编译可运行的行为 red 曾观察到缺失路由、旧 4096 字节上限和慢连接拒绝不及时；实现后 HTTP/App 上传预览、准入、大小/格式/压缩、重鉴权、跨 owner、HEAD/head、脱敏、真实 TCP deadline 与 Close 排空测试通过。
- 既有 Collect 的选中仓库、重复/并发、部分失败、缓存损坏、取消/响应丢失收敛测试直接 green，作为回归证据，不伪造它们的 red。全量 `go test -mod=readonly ./... -count=1 -timeout=240s`、`go vet -mod=readonly ./...`、`go mod tidy -diff`、`go mod verify` 均通过。
- Web `npm run typecheck --prefix web`、build、生产依赖 audit（0 vulnerabilities）通过。额外从暂存区完整导出，执行干净 `npm ci`/build、与本地 dist 逐字比较、全部 Go tests/vet 和两份 Buf 模板生成比较均通过。
- `buf lint`、对本阶段父提交的 breaking 检查、Go/Connect/TS 逐字生成一致通过；CLI 依赖树无服务端、moejs 或 importer。Linux/macOS/Windows × amd64/arm64 的 CLI 与服务端共 12 项 `CGO_ENABLED=0` 交叉构建通过，不代表这些平台的原生运行验收。
- `protoc-gen-es` 生成的 TS 与既有生成文件一样带末尾空行，保持生成原样以通过逐字比较。非生成物使用默认 whitespace check；生成物仅豁免 `blank-at-eof`，其他 whitespace 检查不变。
- 最终生产 bundle 重新嵌入本地服务端后，通过具名隔离浏览器会话复验：选择文件 0 请求；显式预览 2 候选、默认 0 选择/0 Collect；逆序勾选后切换语言/主题保留选择。真实缺失仓库失败后继续收录 `obra/superpowers`，两个请求串行、库仅 1 条，详情 15 个技能均有 SHA-256；重试后库仍 1 条。
- 最终 bundle 在 390px 无文档级横向溢出；三个候选中已有记录仍验证缓存，第二项在途取消后为成功/待确认/未尝试，只有 2 次新 Collect。退出清候选和待确认状态，新 owner 资料库为空、偏好保留；localStorage 只有 theme/locale，sessionStorage 为空，配置秘密哨兵未显示。
- 浏览器账号使用页面内生成的临时随机密码，不保留凭据、账号库、截图、accessibility snapshot 或 HAR。源码中 synthetic fixture 不是用户真实导出；浏览器证据不能替代外部 OIDC/GitHub 授权验收、MyGo 原生 GUI 打包或容器运行。

此切片只新增 Magpie JSON 的仓库候选上传及选择收录，F6/M6、首版均未完成。远端 CI 需按实际推送 SHA 单独核查，不从本地测试推断成功。

交付跟进：源码提交 `bf556fd` 已通过 SSH 推送且远端 SHA 读回一致。Actions `40667951091` 首次尝试在既有 `TestRepresentativeDistributionConnectDeadlineUnblocksSocketWrite` 第 170 行失败，观察到 `context.Canceled` 而非断言要求的 `DeadlineExceeded`；本轮 Magpie/App 测试通过，后续二进制/OCI 因依赖测试失败而跳过。当前提交定向重复 40 次也观察到 1 次同样失败；该分发实现和测试本轮均未修改。保留失败证据，仅请求一次 failed-jobs 重跑，不删除、skip、放宽断言或修改无关生产路径；重跑成功也不等于这个既有不稳定点已修复。

独立代表随后在未修改的父提交 `dda1dd9` 导出副本重跑：40 次均通过，额外 100 次有 95 次通过、5 次同位置失败；这确认既有基线即可复现，次数不是 CI 失败概率估计。相关分发实现/测试、packs 及依赖与本轮源码提交逐字未变。代码因果分析表明 socket 写 deadline 与父 context 计时器没有保证先后顺序；写失败返回后的下载清理可能先取消子 context，使 Err 为 Canceled，而原断言要求唯一的 DeadlineExceeded。未插桩捕获该次 CI 内部事件，也未修复这项无关竞态。保留为后续独立事项。

同一源码 SHA 的 `40667951091` attempt 2 已实际 `completed/success`：test、六组 CLI/服务端二进制、amd64/arm64 OCI 构建全部通过，独立代表也单独查询确认。没有重写源码或断言来得到此结果；容器实际运行、非 Linux 实机与 MyGo GUI 仍未验收。本段后续纯文档提交的 CI 应按其自身 SHA 查询，不能借用源码提交的结果。

### 全项目命令

工具链：Go 1.27.3、Node.js 26、Buf 1.75.0，所有 Go 命令设置 `CGO_ENABLED=0`。从暂存区导出到独立临时目录验证，避免未暂存工作掩盖缺失文件；不得复制运行数据或密钥。

```sh
npm ci --prefix web
npm run build --prefix web
npm audit --omit=dev --prefix web
CGO_ENABLED=0 go test -mod=readonly ./... -count=1 -timeout=240s
CGO_ENABLED=0 go vet -mod=readonly ./...
CGO_ENABLED=0 go mod tidy -diff
CGO_ENABLED=0 go mod verify
buf lint
generated="$(mktemp -d)"
buf generate -o "$generated"
diff -ru gen "$generated/gen"
buf generate --template buf.gen.web.yaml -o "$generated"
diff -ru web/src/gen "$generated/web/src/gen"
```

另对 `./cmd/skillhub`、`./cmd/skillhub-server` 执行 `CGO_ENABLED=0 GOOS=… GOARCH=… go build -trimpath`，目标为 Linux/macOS/Windows × amd64/arm64；用 `go list -deps ./cmd/skillhub` 检查客户端不依赖服务端、moejs 或服务端 MCP SDK。Web 必须先构建，才能验证最终嵌入资源。

2026-10-28 从暂存源码独立导出后执行上述检查：npm 干净安装、Web 类型/生产构建、生产依赖审计（0 漏洞）、全部 Go tests/vet、模块 tidy diff/verify、Buf lint 和生成逐字比对、客户端依赖隔离及十二项交叉编译均退出 0。该结果不包含远端 Actions，也不证明依赖没有未知漏洞。

## 首轮真实 CI 失败与 fixture 修正

2026-10-28，提交 `91bcb261` 的 Actions 运行 `40657038713` 首轮真实 CI 失败：`TestPrepareUninstallRejectsChangedInstallation/symlink` 报 `uninstall_test.go:130: accepted symlink change`。保留日志位于 `.superpowers/sdd/2026-10-28-client-subscriptions/ci-first-failure.log:68`；日志没有记录替换前后的 inode，不能据此声称已实测 CI 发生 inode 复用。

原 fixture unlink 旧 symlink 后创建同 target 的新 symlink，而生产辅助身份 `linux-dev-ino-v1` 仅包含设备号和 inode；安装完成已关闭旧 symlink 的句柄。旧对象释放后 inode 可复用，因此该 fixture 不能保证身份一定改变。源码成功路径和 CI 症状高度支持这一归因，但 CI 上的具体复用仍属推断，不是新增的实测结果。

修正仅将旧 symlink rename 到保留路径，再创建同 target 的新 symlink，并用 `Lstat` / `os.SameFile` 显式断言前后为不同对象；仍要求 Prepare 拒绝且不产生 backup 或 pending operation，没有 skip、重试或生产身份算法变更。此次本机 Btrfs / Go 1.27.3 / `CGO_ENABLED=0` 验证：原 symlink case 50 轮通过，修正后的完整 changed-installation 测试 100 轮通过，`./internal/install` tests（`-count=1`）和 vet 均退出 0。日志在同目录 `ci-fixture-fix-evidence/`，归因与命令见 `ci-fixture-fix-report.md`。本次验证使用当前工作树，没有重新暂存导出，也没有重跑全仓或远端 CI。

这只消除了测试对 inode 永不复用的依赖，**没有完成 inode 复用下的历史连续归属保障**。`INSTALL-RECOVERY.md` 和原 `uninstall-review.md` 的 trusted, stable-path model 仍适用；持久化 dev/ino、generation 和 hash 不证明历史连续所有权。本次修正不扩大删除授权，不宣称能识别所有已释放后复用 inode 的替换，也不代表远端 CI 已恢复成功。

### 后续整合与实际 CI

修正经独立代表复核，三个变更分支各 100 轮与相邻成功/故障准备各 10 轮通过；主线程另从 index 加精确二文件补丁独立导出，全仓 Go tests/vet 退出 0。提交 `0924a5b6636982463ef6be7912e379a6491a1447` 经 SSH 非强推至 `main` 并以 SSH 读回一致。对应 Actions `40659619868` 最终 `completed / success`：test、六组 CLI/服务端二进制任务和 amd64/arm64 OCI 构建任务全部通过。该结果补充前述修正时尚未重跑 CI 的时间点，不改写首轮失败或 inode 根因仍属推断的事实。

首轮 CI 实际上传的 Web artifact 已下载，与本机独立导出的生产 Web 构建逐字一致。二进制任务中的客户端是 CLI，不是 MyGo 原生 GUI；OCI 构建成功也不是容器运行验收或镜像仓库发布。所有结果均对应上述明确提交，不预先承诺后续提交的 CI 状态。

## 构建与未覆盖项

`.github/workflows/build.yml` 对 push/PR 执行检查，生成 CLI/服务端六平台二进制、Web 和双架构 OCI 构建产物。检查具体提交的 Actions 结果，不能把本机测试视为远端 CI 成功。构建产物不等于 GitHub Release；MyGo 原生客户端及其打包仍待实现。

本机没有 Docker，尚无本机容器运行验收；交叉编译不证明非 Linux 原生运行，Windows 服务端、WSL copy、断电恢复、外部真实 GitHub/OIDC、恶意插件操作系统级隔离仍不在已验证范围。无 cgo 测试也不替代 race detector。私有源、源快照自动替换、持续客户端自动同步、S3、完整导入、MyGo GUI 和首发验收仍待后续阶段。

## 2026-10-28 仓库候选导入与 Cognate 改名

导入切片提交 `f429da936d5bd317c3f8db6a22c1d6f42a3457da`，对应独立签收的30文件 tree `3684568b4dee166d8ab70832650dda89196c07e7`，Spec/Quality PASS、未关闭P1/P2为0；SSH推送至已更名的仓库并读回一致。Actions `40681924445` 首次 completed/success，测试、六组CLI/服务端二进制及双架构OCI全部成功。该切片仅为服务端cc-switch一致SQLite上传预览、Magpie CLI消费及Web显式选择/逐仓收录，不是完整配置迁移或M6完成。独立发现的Deserialize重建pager覆盖cache_size问题保留red，再在Deserialize后、任何查询前恢复策略并独立复验green。写入阻塞、尾flush、HTTP/2和同连接复用的增量验证及原始失败证据均保留。

改名提交 `c96b4ae3baae03ecf36bd629eee88748c4ed7cfb` 精确匹配独立签收 tree `baa17d9e4514e2c83acc1bd78608eb75e2c0613c`，基线为前述导入提交。主线程从干净index导出，执行npm ci/typecheck/build/audit（0漏洞）、Go1.27.3/CGO0全量tests/vet/tidy-diff/module verify、Buf lint和Go/TS生成逐字比对，以及CLI+server六目标共12次CGO0交叉构建，全部退出0。独立代表另行导出并核对616个tracked blob，复验Spec/Quality PASS、P1/P2为0，没有纳入GUI、MCP、agent插件、配置资产或旧deadline fixture WIP。

Go module及go_package按用户要求迁移，原始Buf FILE检查明确报7项go_package source API breaking，未隐藏为原样兼容。CI仅在临时历史基准中规范化精确的旧Go module前缀，仍运行全部FILE规则；normalized检查通过，另一个go_package与wire package变更负例仍拒绝。Connect namespace、持久化目录、加密与hash域等按 `COGNATE-RENAME.md` 有意保持旧标识。

独立代表实际运行旧、新服务端，同DB/key/端口重启后旧session、账号、repository/skill/hash及公开metadata均可读；fixture使用旧版Store/ArchiveCache API明确构造，不冒充真实GitHub收录，首次504及缺cache的fixture失败亦记录。独立新binary Chromium验证Cognate标题、匿名安装命令、旧非敏感偏好键与中文/English切换，以及实际 `skillhub.*` RPC。没有复用旧服务页面充当新binary验收，没有输出真实密钥或token。

改名提交经SSH推送并读回一致；对应Actions `40685515081` 首次 completed/success，测试、六组CLI/服务端二进制及双架构OCI全部成功。仓库现为 `ca-x/cognate`，尚无本阶段tag/Release，OCI成功仍不等于容器实跑或镜像发布。完整F1–F12/M1–M7首发门禁继续有效。

用户提供的cc-switch本地checkout只读对照基线为 `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`，交互矩阵见 `CCSWITCH-CONFIGURATION-MATRIX.md`。源码注册14种工具，独占/共存/特殊默认、MCP、prompt和OpenClaw配置必须分别验证；没有启动upstream或读取用户真实配置。是否首发纳入代理/协议转换/failover/模型路由已异步询问用户，不能静默决定或宣称完成；其余已明确配置开发继续进行。

## MCP、原生桌面与配置存储增量

2026-10-28，以下阶段均 SSH 推送并读回 main SHA，提交 tree 与各自独立报告完全相符，不含当时其它 WIP：

| 阶段 | Commit / 独立签收 tree | 已证明范围 |
| --- | --- | --- |
| MCP 分类与发布 | `e707dc0d22150576f89cc7233cc400a4e8d2cff0` / `a7e9b2b0bb858a0335ea4d45367ca1f4098266f9` | 七 scopes、17 个 typed tools、全部十个业务写工具事务前/提交前复核权限、旧 token 不扩权；独立 SDK、真实旧凭据/新 binary 浏览器、失权负例通过 |
| MyGo 桌面首片 | `611f2a069acd5c6dbed40a86dd99a1636b3aa7be` / `6f189c637734959ac2245dca7db6baa83c1bd059` | 真实 Linux 原生窗口、headless 原生组件+HTTP/共用内核流程、六 CGO0 构建、Linux 包内容；不等于跨系统安装或完整 M7 |
| 两个 HTTP fixtures | `4c668b28858fd0bfc241837276a461ae46cc5d47` / `e1531dd3e5ce7a34530f5cd9901bafc9cb032767` | request body 每次重建、分发 socket timeout 与连接错误传播排序；独立确定性耗尽 body red/green、默认/单核压力与缺失 WriteDeadline 负例有效；没有生产代码修改 |
| 配置资产内部存储 | `94668b7ad70949d50808fb16a8386c521b30300d` / `2e3029ecdce370bfb4d6e3d254d3811b867a664d` | ent+独立 HKDF/AES-GCM、owner/AAD/修订绑定、metadata/payload 隔离、CAS/容量竞争、单 SELECT 同版本、旧 schema 迁移；独立深审与整合测试通过，不等于配置产品功能 |

Native desktop Actions `40691777676` 精确绑定 `611f2a0`，首次 completed/success：verify、Linux/Windows/macOS runner 的双架构包装全部 success，publish=false 按设计 skipped。Windows 两架构 NSIS Setup、macOS 两架构 DMG、Linux 两架构 deb/tar 均有实际构建检查，构建日志包含 Linux 原生实窗捕获。这是 unsigned/ad-hoc artifact，不是 Developer ID/公证、Authenticode、实际跨系统安装或正式 Release。

不能将该桌面 workflow 的绿色替代普通 Build：MCP Actions `40689163056` 在 `mcpserver/lifecycle_test.go` 复用 body 上失败；修复后普通 Build `40691768850`、`40692998730`、配置存储提交对应 `40694979772` 又在既存 `catalog_lifecycle_test.go:238` 的 DeadlineExceeded/Canceled 排序上失败。原始远程失败日志保留，未靠 rerun 隐藏；catalog fixture 的定向修复另行冻结独立验收。配置存储的 clean-export 全仓测试/vet/Web build/Buf 已通过，但不将本机通过记成这些远端 run 成功。

后续 catalog fixture 经独立原始失败复现、固定版本100轮、close/disconnect对照与缺失生产WriteDeadline负例验收后，按 tree `af4266c5ee05f96dbc4109c98089df3e5c1010ad` 提交并 SSH 推送 `fb45608bfe951497591e081b35352f88d20f842a`。对应普通 Build Actions `40698837803` completed/success，实际 test、六组 CLI/server binaries 和 amd64/arm64 两 OCI jobs 全部 success。该真实后续绿色不覆盖此前失败历史，也不把 OCI artifact 当运行中的容器。

整合代表另外确认了既存 MCP 非 UTC 时区授权问题：SQLite 时间文本带不同 offset 时不能依赖 SQL 字符串排序表达真实时间。该问题在旧基线同样存在，不由配置存储引入；最终独立修复记录见下节，不借存储签收隐式关闭。MCP 发布归档缓存和审计 append 仍不与领域写事务构成跨资源原子提交。

完整首发继续未达成：typed domain/API/编辑器、完整十四 agent 与导入、配置选择/Pack 附加、原生受控应用/恢复、源自动替换、持续客户端同步、S3、私有源和真实外部授权/跨平台运行等仍按原需求逐项推进，不以阶段 PASS 缩减范围。

## 时区、moejs 目标适配与 Typed Configuration

以下三个阶段均从精确冻结 tree 导出验证、由非作者独立验收，再单独提交并通过 SSH 推送与读回。它们不包含当时未冻结的 Web 编辑器或其它 WIP。

| 阶段 | Commit / 签收 tree | 已证明范围 |
| --- | --- | --- |
| MCP 时间兼容 | `f3cae917cece5c1367131d487bd75f4b39fe76d7` / `c38d470fd61e51a18f47c51531fda08f3f791e92` | 查询 credential/owner 后 Go instant 校验；新 credential/audit 时间 UTC；启动锁内有界兼容旧 numeric zone/monotonic 文本，保留授权与审计身份 |
| 服务端目标适配器 | `538c466b8e499ad0c656a192131c2dbe29e4344a` / `02d6b4d5e063e241247e77f46309b6f20c8fa4b8` | 真实 moejs registry、精确版本/digest/capability、Claude 四键 additive merge；未选旧 env/secret 留 Go 宿主；Codex/Gemini 仅 metadata，不是 provider switching |
| Claude typed domain/API | `8a7eebe960a1270ac1ef24b98e0c77eb08e06c94` / `01f694d9cb0e91ca309914c5d0b47a240b1dc80d` | owner/CAS CRUD、同版编辑读取、keep/replace/clear、不带秘密复制、严格有界 Connect、真实日志/响应隐私；未知 agent 仅合法 metadata 浏览/删除，保存不等于应用 |

MCP 旧 lexical 比较与真实 Kathmandu numeric zone 旧库分别保留 red。仅 credential 修复的旧候选没有被当作最终签收：audit 同样存在旧行无法读取及本地时区新写不可读，最终七文件候选把两者一并修复。代表用真正旧生产 writer、真实进程 TZ、MCP SDK endpoint/restart、审计 append/读取和撤销复验；错误 key、后批坏时间、幂等、owner/scope、transaction/commit 时间 gate 另有独立负例。每批128条、每时间输入256字节，坏批回滚，无法恢复的值固定错误停止启动。升级前停止旧 writer，不承诺新旧 writer 长期混用。

Typed API 首个默认打印合同 P2 已在当时所测直接格式范围独立关闭，但不是给 generated DTO 或任意 domain wrapper 新增通用日志安全承诺：只允许明确构造的无秘密状态 DTO 进入 logger，原始 Protobuf/Connect request 的 String/JSON/slog 能包含 replacement，严禁用于日志/持久化/error details。`debug_redact` 注解保留，当前 Go runtime 不因此自动安全。真实 proto/JSON 成功、失败、超限请求捕获标准 log/slog/HTTP ErrorLog 和响应；三项临时 mutation（stdlog、slog、错误回显）均触发实际隐私断言 red，去除后 green。窄 AST gate 不是全程序污点分析。2026-10-29 对五种既有 wrapper 的补充 probe 发现非字符串动词/私有容器边界，未证实现有生产 sink；精确限制与后续开发约束见 `SECURITY-LOGGING.md`，不将历史有限 PASS 扩大为所有格式安全。

主线程对上述各冻结 tree 重新执行 clean-export 全 Go tests/vet、Web build；最终 API 另有 domain count20、HTTP/App count10、Buf Go/TS 双模板完整生成目录 parity、六系统/架构组合 CLI/server 共十二次无 cgo 构建。独立代表执行自己的 package/protocol/日志负例，不借作者通过替代验收。完整日志索引在 `.superpowers/sdd/2026-10-28-release/`，关键行为合同已随本文件和各 spec 提交，临时合成数据库/凭据不提交。

Build Actions `40709123040` 精确绑定 MCP 提交、`40709433501` 精确绑定 adapter 提交、`40709705439` 精确绑定 typed API 提交，均 completed/success；逐 job 核对 test、六组 binaries、amd64/arm64 两组 OCI 构建全绿。前述历史失败继续保留。没有新 tag/Release；OCI 构建不是容器运行，七键 provider switch、秘密原生消费、选择/Pack 附件与真实本机 apply/restore 尚未由本节交付。

## 容器实跑与七键声明式变更 checkpoint

2026-10-29 续接核对：下列两个阶段已独立验收，按精确 tree 提交并 SSH 推送；没有包含仍在验收的 Web 编辑器、桌面身份门禁或本地 JSON codec。

| 阶段 | Commit / 签收 tree | 已证明范围 |
| --- | --- | --- |
| 双架构容器实跑 | `9cf109edcbcebe1a342db72391b891c096fe3bf8` / `fdd10f58d4cd291d84411e34f04bd8ab49ea5f4d` | CI 实际加载镜像，以 UID 10001、只读根目录、禁网络及私有持久卷启动；health、内嵌 Web、CLI help、SIGTERM exit 0、错误主密钥 exit 1 且精确错误、原密钥重启通过 |
| Claude 七键声明式变更 | `059bf928afbb73e869d8eed8112f667b35555f60` / `7c96c64b208c36f5cf704c04a8e42ce2849dbe2a` | 服务端 moejs `claude.provider-switch.v1`，七个受管 env slot 的 set/remove；凭据只使用 typed 引用，真实秘密和旧 env 不进入 JS；Go 独立验证输出，无未知能力 fallback |

Build Actions `40719035614` 精确绑定容器提交，`40722843461` 精确绑定七键提交，均 completed/success，test、六组 binaries、两架构 docker jobs 全绿。容器提交两 job 的真实 smoke step 均 success，并输出固定完成标记；这不是仅构建 OCI 或命令替身的结果。arm64 在 QEMU 环境，不宣称原生 ARM 硬件运行。本机没有 Docker/Podman 的历史事实不变。

七键能力保留旧四键 additive renderer，新增能力明确区分两种认证 slot 与五个非认证 slot，空值/非法联合/额外成员均拒绝。独立 reviewer 复验真实 moejs 和输出篡改负例；主线程对最终 clean export 执行 Web build、全 Go tests/vet、runtime 重复测试、模块校验与客户端依赖边界检查。它仅产生声明式 patch，不提供秘密传输、设备授权或本地文件应用，不能标记完整 provider 切换体验已完成。

当前 Web 编辑器曾被独立验收拒签：保存成功响应与离开确认并发可能留下阻塞对话框；本地 JSON codec 也曾因非常规 fmt 动词泄漏秘密的可逆字节被拒签。修复与新树复验另行记录，不能用原树的全量测试通过覆盖这些行为缺陷。没有 tag/Release，完整首发目标仍未完成。

## Claude 私有资产 Web 编辑首片

2026-10-29，按 tree `569372e78883a36f3ea39b76df93ae22a41ef467` 独立整合验收，提交并 SSH 推送 `f659706ddb0c81cc0ac4e20835dd14b2039c03d2`，远端 main 读回一致。八文件只含六个 Web 消费文件及 embed 路由/测试，不带当时的本地 codec 或其它 WIP。

- `/configuration` 提供当前账号 Claude provider 资产列表、新建、typed 编辑、非秘密复制、revision CAS 删除；secret 为 keep/replace/clear，未知合法 agent/schema 仅 metadata 浏览/按版本删除。保存不切换本机 provider。
- dirty 草稿有关闭/导航放弃确认；主题切换保留表单，locale 切换清除新凭据并取消在途操作，账号/会话切换直接清理敏感状态。凭据不进入缓存、URL 或浏览器持久化。GET/HEAD 直达与相邻路径 404/CSP 回归通过。
- 原保存成功与导航 resolver 的竞态 P2 在最终代码中关闭：不再使用提交时捕获的旧 resolver；独立浏览器 held-response Create/Update/Copy、同时响应、继续/放弃、CAS 失败、locale/auth/unmount 等回归得到 86 项 boolean 观察全真，另有真实未知 schema Store、刷新和隐私检查。旧拒签记录保留。
- 独立 reviewer 对最终 tree 核全部 695 blobs/mode，八文件与已实测修复候选相同；fresh 构建的完整 Web dist 与 server executable 也逐字相同，将此前真实浏览器证据绑定最终运行时，而不是声称又跑了一次新 origin。最终 fresh Web/server/HTTP configuration seams 通过；主线程另跑 clean-export Web build、全 Go tests/vet、相关包 count10、模块校验全部通过。

对应 Build Actions `40727877287` 精确绑定该 commit，completed/success；实际 test、六组 binaries、两架构 docker jobs 全绿，不由本地通过推断。独立最终报告为 `configuration-ui-final-review.md`，旧竞态拒签与两次修复复验均保留。该首片不是十四类 agent 完整编辑器、来源配置导入、选择/Pack 附件或 CLI/MyGo 原生 apply/restore，也不关闭本地 JSON codec 的独立 finding。用户操作说明见 `CONFIGURATION.md`。

## 桌面打包身份门禁结果

桌面修复 `39013dacfc01b2b50d6633cee3b2c324f09bf945` 的 Native Actions `40726579335` 已 completed/success，verify 与三系统双架构实际包装全部成功，publish=false/skipped。六 payload 在各 runner 的真实 metadata 检查全部验证 Go/CGO/目标架构、精确源码 revision 与 clean 标记；旧 Windows dirty 的因果复现和拒绝历史继续保留。普通 Build `40726569509` 也 completed/success。已核实际 job 结论与六条执行输出，不将后续 artifact 下载、安装或跨系统运行自动算作完成；完整限制见 `DESKTOP.md`。

2026-10-29 下载独立复核：该 run 三个 unsigned ZIP 的大小/摘要与实时 artifact API 匹配，28 个条目与落地 bytes 一致；六个 payload 的 Go 1.27.3、CGO0、目标架构、精确 `39013dac` 与 modified=false 均通过，八个包装存在且非空，14 个 metadata 负控被拒绝。没有执行或安装 payload、挂载 DMG、验证可信签名或证明包装内部同一性；下载摘要和 embedded VCS 信息不等于源码到二进制的密码学证明。三个实际 ZIP SHA-256 及限制见 `DESKTOP.md`，独立报告为 `desktop-downloaded-payload-review.md`。此项不签收 native auth/state、备份或完整首发。

## 本地 JSON codec 与 Native schema

2026-10-29 续接，两个切片分别独立验收、提交；它们不包含原生秘密消费、文件发布、备份恢复或完整 F12。

| 阶段 | Commit / 签收 tree | 已证明范围 |
| --- | --- | --- |
| Claude 纯内存 JSON codec | `d856f5d4216b9cad21c1275e4aa182fef253ec03` / `b0a33ec4ee651ddb87abb240b22001de84f5628f` | 七键 patch 与精确 credential slot 独立校验；严格 jsontext、有界输入/结果；局部修改保留未知 token/大整数；无磁盘、HTTP、CAS 或已应用状态 |
| Native schema / Web 生成隔离 | `356a2d4` / `df3c8797cc2bed8cae85cf6270d22139ed14c758` | 独立 native-purpose proto 包与 Go 生成物；Web template 排除该目录，保留所有普通 TS 和完整 parity；CI 明确检查 Go 正向与 Web 负向，没有注册服务端 endpoint |

Codec 两条隐私 P2 均在最终 tree 独立关闭：初版缺 Formatter，非字符串动词可输出可逆 bytes；仅加 Formatter 后，外层私有字段可绕过方法派发，实际 fmt/slog 仍泄漏。最终 wrapper 采用双层不透明间接表示并保留值接收者 Formatter，不批准“只允许公开容器”的豁免。原 30 项隐私 probe 100 轮通过，新增 23 动词、私有嵌套容器与真实 slog 双 handler 通过；退化为直接 bytes 的原 10 项、退化为单层指针的 `%t` 均重新 red。任意主动 unsafe/反射取出秘密或显式 CopyBytes 不属于默认日志保护承诺。

Codec 独立完整 scoped 100 轮、fresh fuzz/vet/deps 通过；主线程最终 clean export Web build、全 Go tests/vet、codec 100 轮、客户端禁止 server/moejs 依赖检查与模块校验通过。对应 Build `40733265755` 精确绑定 codec commit，completed/success，test、六 binaries、两 docker jobs 全绿。旧拒签记录没有被删除或以原树全量通过覆盖。

Native schema 的独立 review 核 23 messages、6 enums、6 RPC 的全部 token 与已签协议一致，包含 reserved/options；实际 Buf 1.75.0 全 Go/TS parity、breaking against `a168eb5`、lint/format、CGO0 generated 包 compile/vet、Web build 与 actionlint 通过。未过滤模板真实产生 native TS 并使 boundary/parity red；过滤后普通 auth/configuration TS 保留而 native TS 缺席。CI exact gate 的 11 项负例、删除新 tag 的 breaking 负例均有效。静态 source literal gate 只是开发回归检查，不是防恶意同权限客户端的身份认证或 attestation。

主线程对 schema 最终 clean export 另跑全仓 Go tests/vet、Web build、完整双模板生成 parity 与兼容性、模块校验全部通过。原生 proof、父/子 session 事务、一次性 grant、敏感 transport、平台安全 publisher 和真实 apply/restore 仍分别待验收；schema 可编译不能等同 native capability 可用。后续 CI 结果另按精确提交核对，不从本地通过推断。

## Native Pure-Proof Checkpoint

2026-10-29，纯 proof 切片按 exact tree `f30019c466d20baedbba207a3cd347dd51f77b0f` 独立 Spec/Quality PASS，四文件提交 `b5e2e0c426761d219a0068048104b676e1f2ed49` 已 SSH 推送并读回。五 purpose 的 canonical transcript、Preparation/Apply/Patch hashes、Ed25519 sign/verify、safe patch 验证和 strict raw protobuf decoder 已实现。decoder 在 merge 前拒绝重复 singular/oneof 与 nested unknown，失败不改变目标；12-type allowlist 明确排除三种秘密响应。Transcript/Signature 使用双层 opaque 表示；不承诺原始 DTO、显式 bytes/hash 或私钥适合日志。

原 tree `69c6c4d8c4395ab6ee3ec501dee59a0f6b403759` 虽主全量通过，独立发现 auto-policy ID 错限任意 32 bytes：合法短 ASCII 被拒绝、32 个 NUL 被接受。该 P2 没有通过改协议豁免；最终复用已签 1–64 bytes ASCII opaque ID 规则，typed/raw 原反例 100 轮通过，独立字符边界通过，退回旧实现的 overlay 重新 red。原拒签、16 个独立 known-answer vectors、strict-wire/格式化隐私及四项有效 mutation 证据保留于 `native-proof-final-review.md`；作者六个 Python/OpenSSL fixture 的 parity 不替代独立 oracle。

最终 clean export 的 Web build、Buf lint、完整 Go/Web 双模板 parity、全仓 Go tests/vet、proof 100 轮与模块校验通过，均 Go 1.27.3 / CGO0；没有运行需要 cgo 的 race detector。Build `40745938862` 已登记但本 checkpoint 未核完成，不声明远端 CI 通过。本片无 HTTP/磁盘/秘密消费，不证明 owner MAC、重放/授权、数据库时间与撤销、grant、客户端发布或完整配置应用；后续 native state/auth 及 watch WIP 不在本次签收内。

## Selection Read And Initial Lists Publication Checkpoint

2026-10-29 后续交付，以下两片已分别通过非作者独立 Spec/Quality 验收（开放 P1/P2 为 0）、提交、SSH 推送和远端 SHA 读回。本节补充后续证据，不追认早期 checkpoint 或其未核 CI 已经通过。

| 切片 | 精确提交 / Build | 已证明范围 |
| --- | --- | --- |
| C0-S 当前选择读取 | `04ad076529b032082dfa3b8ef1d36d601fba67a1` / `40832542043` completed/success | 每次在真实安装锁内重新认证已有 root 与完整 inventory；返回三个 revision、AutoApply、DeviceID；验证后不存在才 absent，取消/释放/损坏错误返回全零；重启、Restore 后暂停与跨进程新鲜读取有行为验证 |
| B1c 首次 Lists 内容发布 | `34c22a1070e3bdc907d177f89e1bdc426b342e58` / `40833359423` completed/success | 共享物理维护配对验证；首次 cache.Put 到 CollectListRepository 真实事务完成和清理的外层 writer scope，传播 leasedCtx；网络准备在外；真实子进程、Close/Quiesce、提交回执丢失与失权回归有证据 |

C0-S 独立 reviewer 额外验证认证全局库存后才认 absent、root 移除/恢复、真实 pending、uint64 极值；五类独立编译后行为退化均产生断言 RED，恢复精确源码 GREEN。B1c 独立 reviewer 在真实归档发布/清理与 COMMIT 尾部设屏障，以第二 Store readback 验证真实提交、Close/Exclusive 等待及过期 leasedCtx；去外 scope、丢 leasedCtx、重置 helper context 的三项负控各三次真实断言 RED。编译失败、panic、整体超时与父进程空 helper 不算行为证明。

组合 tree `b25f02bb46342d445179940201984ff2c37869d6` 恰为基线 `de2886e` 加 C0-S 两路径与 B1c 五路径，独立机械 ratify 逐项 blob/mode 及完整导出一致。主线程对该不可变 clean export 执行 npm ci/build、全仓 Go tests/vet、go mod verify、Buf lint、Go/Web 双模板生成逐字比较，以及 CLI/服务端/MyGo 的 Linux amd64、Windows amd64、Darwin arm64 九项 CGO0 编译，全部 exit0。交叉编译不代表这些平台的实际安装或恢复验收；主线程全量不冒称独立 reviewer 重跑全仓。

这两片依赖已交付的 Linux/Claude 本地加密 journal、备份、确认/CAS 发布及恢复内核（`0839b62`），但仍不是 CLI/MyGo 配置应用的完整产品闭环。CurrentSelection 返回的快照不是授权、reservation 或防恶意历史回放证明；网络撤销与本地落盘不是跨机器原子事务。B1c 也不是 App 已启用 managed 模式、全局备份协调器或 S3 恢复完成。

显式客户端密钥准入的 capability map、core spec 与 plan 已在 `d40503e2dde3c0874a3e417904c3cc302fbc78c4` 提交推送，Build `40834848617` completed/success；这是设计文档交付，不是 key 初始化实现验收。旧 CLI/native 自动创建入口关闭、真实人类逐槽批准与安全 TTY、MyGo 配置 UI、自动/Pack 附件、其它 agent/平台、完整 F12 和首个正式 Release 仍分别待完成；不能因以上 CI 全绿勾掉这些门禁。

## Explicit Client-Key Core And Contention Checkpoint

2026-10-29 后续交付；下列四项均按独立验收的精确写集提交、SSH 推送并读回。此节更新后续事实，不把上一节当时尚未实现的功能追认为已完成。

| 阶段 | 精确提交 / Build | 范围 |
| --- | --- | --- |
| 配置争用测试 | `bcbed01477120e6e46e97743b2f09bba8015e583` / `40842683164` completed/success | 保留真实并发下限次重试的 Conflict/Unavailable 分类，增加 KEEP-first、Replace-first 两个同版本快照后真实提交顺序；确定性失者必须 Conflict，不能重读重试或重合并秘密；不改生产逻辑 |
| Codex 声明设计 | `a8a6ad8cece9744fa2bac2fa6c5aca375986ea2e` / `40843367375` completed/success | capability map、typed 声明 spec 与 plan；独立设计审全部 finding 已闭；不是 Codex provider 实现或本机安装 |
| 显式客户端密钥核心 | `5637945e0e8a8921cf720dc0ea5288adaa2a8d81` / `40843635870` completed/success | Linux 的 LoadExisting/Initialize；只有显式初始化生成，先持久化 key 与必要父目录再持久化严格 marker，retry 重做同步；合法 legacy key 原样登记，缺 key 且有 marker/state 残留拒绝；尚未接入旧 CLI/MyGo 入口 |
| 初始 Lists 子进程预算 | `ef004f2fbfc9c7fd81daf3525c8af4d9866b2c38` / `40844151112` 本节仅观察 in_progress | 预期成功的完整 SQLite 初始化独立限 3s；预期被维护锁阻塞仍限 250ms，且要求 nil Store、DeadlineExceeded 与 maintenance.ErrLock；不改生产锁等待语义 |

密钥核心独立验收包含真实双向 Session/初始化锁争用、严格 marker 与残留、legacy 原 key、取消/释放错误 withholding、真实 fsync/父目录/解锁 EIO，以及八项编译后行为负控和精确源码恢复。故障注入不是物理断电证明；部分 lost-receipt 模拟与真实资源调用失败分别标注。非 Linux 仅编译，返回 unsupported，不能以九项构建宣称这些平台可初始化或安装。

两个测试修复也分别经过非作者完整 Spec/Quality 验收。配置测试有真实 SQLite COMMIT 后屏障、取消及四项编译后负控；scheduler 使用真实受控慢读取和物理锁验证，回退成功预算、弱化锁证明均产生断言 RED，恢复 GREEN。原 `dff2546` 的 Build `40837382467` 确实失败，其日志与调查保留；没有通过重新运行原提交抹除失败，也不宣称已测得远端当时的具体物理延迟。

主线程对六个代码路径的组合 tree `3b2ea6c4bf506d0f493940a9bbdcc9a909bc5c78` 做不可变 clean export，npm ci/build、全仓 Go tests/vet、模块校验、Buf lint/Go+Web 双模板 parity 和 CLI/服务端/MyGo 三目标平台九项 CGO0 编译均 exit0。最终 `ef004f2` tree `3a2f40063353c4716308e6fa3d5503f5645100d0` 相对该已测试树只增加上述独立设计验收的三个 Codex 文档，六代码路径 blob/mode 不变；密钥实际交付树还单独 fresh 跑过 key 包测试与 vet。完整主线程验证不冒称由独立 reviewer 重跑。

CLI 真实逐槽确认/应用/恢复、旧入口安全准入与原生初始化 UI、MyGo 配置编辑、Codex 及其他 agent 的完整适配、导入/Pack 配置附件、自动应用、完整备份/S3 与正式 Release 仍分别待完成。本节不关闭这些首发门禁。

## Codex Provider Planning Checkpoint

2026-10-29，服务端 Codex 声明式 provider planner 已以 `c7d8ab60c2f7ae6d2c4b18b1163b72fe65d59dc8` 提交、SSH 推送并读回；对应 Build `40853241980` completed/success。此前的客户端密钥入口与本地快照两份规格以 `3c1348a5e0af77d12c296f8b374619680c9d53a4` 提交，对应 Build `40849671013` completed/success；规格交付不等于这些功能已经实现。

Codex 实现的精确八路径候选 tree 为 `25fb32052ce748b34e6468029743f407d02430db`，经非作者完整 Spec/Quality 验收，未关闭 finding 为 0。独立验证包含真实 moejs、provider/catalog presence 组合与隐藏成员、恶意输出、取消与 clone 隔离；恢复回归 716 个含子用例、shuffle 4600 个含子用例通过，三项先编译成功的行为负控分别断言失败，恢复后通过。实际交付 tree 为 `5d97c8c6a85534fdc97b05c6759ab4882728152a`。

该能力只生成单一 `codex.provider-key` 槽位、固定 provider/Responses 协议及尚未解析的 `preserve-local-login-v1` 策略声明。宿主基于独立输入快照重算完整 JSON 树，并返回自身持有的副本；整个 DTO 仍敏感，不得整体日志。真实运行时会省略 JavaScript `undefined` 成员，因此验证边界是归一化后的 JSON 树，不是 JavaScript own-key 反射；真正 JSON 中的未知成员、null 或错误值仍被拒绝。此交付不包含 Codex 本地 codec、登录解析、客户端应用或完整 F12。

主线程从不可变 A-only tree `d8227ddcc5b5369dc007e8de5f71371a07a80bb2` 导出，完成 npm、全仓 Go tests/vet、模块校验、Buf Go/Web 双模板生成比对、客户端无服务端/moejs 依赖检查，以及 CLI/服务端/MyGo 九项 CGO0 交叉构建，均退出 0。最终交付相对该验证树只增加上述两份已接受的规格文档，不冒称独立 reviewer 重跑了主线程全量验证，也不以交叉编译证明非 Linux 原生运行。

Claude/Linux 手动 CLI 配置工作流 C1 的独立候选 tree `feb621b2baf1a4fc5f2b1a6ddc3772df2e03651e` 尚未签收或交付。非作者完整验收发现两个 P2：未知子命令打印帮助后 exit0；满 stdout/stderr 时，1s 命令 deadline 未传到底层嵌套写入，约 10.06s 才退出。已有 focused 52/52 通过不能覆盖这两个真实进程反例；原候选与失败证据保留，须修复并复验。主线程曾验证的 A+C1 组合 tree `1ddbe8f66dc0ecf3db315206cbc222a09f33f38b` 全量通过，也不能替代此独立验收或为后续修订源码提供通过证明。

显式密钥入口迁移、MyGo 配置 UI、完整十四 agent、配置导入/Pack 附件/自动应用、加密备份/S3/恢复、跨平台实际运行及首个正式 Release 仍分别未完成。

## Claude Native CLI Apply And Restore Checkpoint

2026-10-29，上一节未签收的 C1 已由原作者修复，两个独立反例分别闭合；原拒签、原 RED 与原样独立 oracle 均保留。接受的 successor tree 为 `a4b79217d59dbfa415c760329f38cacde4a70c7c`，仍为原八路径；独立 scoped 复验连同原完整实现审签收该精确范围，非作者重新执行 focused 63 个含子用例全部通过、零 fail/skip，且分别撤销两处修复的 overlay 均先编译成功再由原断言产生 RED。满输出的真实 1s 命令约 6.02s 自行退出，遵守原有命令预算加独立 5s 固定诊断尾预算，未放宽原 6.5s oracle，也不声称整个进程仅耗时 1s。

该八路径以 `3b0345ead6bc27b906e89e9fb55bed8a8f081c72` 提交、SSH 推送并读回，交付 tree 为 `3ef3bbf250cfc50569f6e2ef2f253b96c396b3ad`。能力限 Linux/Claude 手动 CLI：`config apply` 默认预览，明确应用需要真实控制 TTY 的逐 secret slot 批准与另一次最终计划确认；`config restore` 独立预览、释放锁、确认、重开并校验 CAS；`config status` 为本地状态查询。网络与人类等待不持本地安装/目标锁；Unknown 只允许原 flow 内最多一次 Status，不重放秘密。已有客户端密钥必需，不创建新 key，不等于后续旧入口迁移或 MyGo 配置应用已经完成。

主线程从 base `65d8c113fe9596947c3ab5bcb558dbb5c84ca42a` 加精确 successor 八路径构造 tree `a8593d2f1de1a6afc5938413ac0c81f289d45bc5`，fresh npm、全仓 Go tests/vet、模块校验、Buf 双模板生成比对、客户端依赖门禁与九项 CGO0 构建均退出 0。非作者另核全部 924 个 tracked blob/mode、八路径来源及主验证日志归属；不是重新执行主全量。最终交付相对该测试树只增加已独立接受的 B2 plan 与原生配置 UI spec 两份文档，无其他源码变化。交叉构建不证明非 Linux 实际运行，CLI 正确处理不证明 GUI 或完整 F12。

实际交付的 Build `40866724425` completed/failure：全仓测试、amd64 OCI 与其余五组二进制成功，但 Linux arm64 二进制及 arm64 OCI 在 `internal/configapply/native_publish_linux.go:35` 编译失败。该架构的 `unix.Stat_t.Nlink` 为 uint32，而受控文件身份的 Links 为 uint64；此前九项主线程构建未包含 Linux arm64，不能拿其绿色声称六目标全覆盖。主线程在原不可变导出中以 Linux arm64 真正复现相同编译错误，保留原 CI 与本地 RED；需要单独修复显式类型转换并验证，而不是重跑原提交掩盖失败。

已接受的密钥入口实施计划支持后续 CLI 与 MyGo 两个不相交写集并行；本地备份快照实施计划也已接受。此处只记录设计与后续实施边界，不提前签收入口迁移、原生配置 UI、完整备份/S3/恢复或首个正式 Release。

## MyGo Explicit Client-Key Entrypoints Checkpoint

2026-10-29，MyGo 密钥入口迁移已按精确十三路径独立 Spec/Quality PASS，零开放 finding，并以 `3fc1326ec84a170e095f30c1c85602d8441752b1` 提交、SSH 推送及远端读回。交付 tree 为 `1f1c694f2d7c0b128b4b0c5e9cf5a3281f6c24ea`；相对文档基线 `c24710d056f484d872e650f183ffde502f322e3e` 的十三项 blob/mode 均等于已签候选，其余沿用基线。客户端显式初始化持久密钥；已有密钥加载不再隐式创建，缺失或不确定状态须明确恢复。异步准入、取消后真实排空、generation 以及关闭错误保持原权限和生命周期边界；这不是 MyGo 配置编辑或应用界面的完成声明。

非作者独立执行 desktop/clientkey 测试、vet、自建 late-key-loss、真实 drain、排队 generation、异步隐私及初始化释放失败 oracle；五项负控均编译成功后产生有限断言 RED，恢复后 GREEN。另以隔离 HOME/runtime 的私有 Cage/headless Wayland 实际启动生产 MyGo 窗口、捕获并正常退出；账号初始化的鼠标/键盘与截图仍是 `ui.NewTester` 证据，不冒称物理桌面或该原生窗口的账号输入验收。

主线程在新鲜组合导出中执行 npm ci/build、全仓 Go tests/vet、模块校验、六平台乘 CLI/server/MyGo 共十八项 CGO0 构建，全部退出 0；另核 Buf 双模板生成一致、客户端依赖不含服务端或 moejs。非作者机械复核全部 931 项 tracked blob/mode 及十八个产物的 Go1.27.3/CGO0/平台元数据；这不是独立重跑主线程全量测试。交付的 Build `40888776765` 已实际观察 completed/success，九个 job 全部成功，包括双架构 OCI；交叉构建不证明非 Linux 实际运行。

前述 Linux arm64 类型错误另以 `656c211d01a9d7d2632c5cee12b49f6fb3229ca9` 的显式 uint64 转换修复，Build `40869447717` completed/success；原失败 `40866724425` 保留，不能用新提交绿色改写旧结果。CLI 密钥入口另片、MyGo 原生配置 UI、完整十四 agent、导入/Pack 配置附件/自动应用、备份/S3/恢复及跨平台运行门禁仍需各自关闭；目前无正式 tag/Release。

## Codex typed provider 资产（2026-10-30）

提交 `4d0e6c0cac5399dc29f2fd36b7a28bc71f90c2fe`，tree `5d0905bf12b5277d53a4f7e26bc2675f64d3a4b4`，精确 23 路径，已 SSH 推送并远端读回。域、HTTP、Web 及最终组合分别经非作者 Spec/Quality 验收，范围内零未关闭 finding；该提交不包含仍未签收的 MyGo 配置界面。

已支持 owner-scoped 加密 Codex provider-key 资产 CRUD、无凭据复制、revision CAS、严格 Connect JSON/protobuf 映射和 Web 专属表单。optional false/none、目录字段 presence、空白和有序列表保留；未来字段只读、不允许保存或复制。缺 key 可保存草稿，结构完整不等于本机可应用或真实端点已验证。

主线程在不可变精确源码导出中执行 30 项门禁全部退出 0：npm 安装/构建、全仓 Go tests/vet、模块校验、Buf lint/breaking、Go/Web 正常生成与两次 parity、六平台乘三入口的十八项 CGO0 构建及客户端依赖隔离。独立最终审核核对 947 项 tracked blob/mode、组件来源和十八个产物元数据；不冒称由 reviewer 重跑全部门禁。

真实 SQLite/HTTP/Chromium 验证覆盖创建/重开/keep/clear/复制补 key、CAS 显式重读、丢回执 Unknown 禁重放、目录及嵌套列表实际排序保存后恢复、成功响应与离开确认交错、提交后切语言清新凭据并保非秘密草稿、焦点恢复、owner/origin 隔离以及 375px 深色与更宽浅色界面。最终新鲜运行十项 PASS、八条存储收据对应五次预期写入/提交，服务和 browser 均正常停止。另有独立真实服务 Web 回归及旧版 BODY 焦点断言负控；原 domain 拒签、原焦点 RED 和失败自动化尝试均保留，不以新运行改写。

Codex 官方 ChatGPT/OAuth、本机 provider 选择/文件写入/登录保留、原生授权消费及应用、其它 agent、来源导入/Pack 配置附件/自动应用、完整备份恢复及正式 Release 仍未由本次交付完成。六平台交叉构建不代表非 Linux 实际运行。

原提交的远端 Build `40930852848` completed/failure：`TestRepresentativeTimeoutDoesNotStarveNextRepository` 的第二次成功工作在 200ms 测试预算内超时，二进制和 Docker job 被跳过。后继 test-only 提交 `43fa42e555b0f3453a2b820386b247b3e1d097fb` 已 SSH 推送读回，保留首轮 200ms 真实超时及重启后顺序/收录断言，仅将成功工作预算分离为 3s；可取消的 250ms 成功 fixture 确定性复现旧预算不足，移除 cursor 推进的负控仍编译成功后断言失败。非作者独立复验签收，精确 one-file 候选的完整 30 项门禁全部退出 0；不修改生产期限或以本地绿色抹去原 CI 失败，后继远端 CI 仍须另行读回。

## MyGo Claude 原生配置交付（2026-10-30）

提交 **`bd00bc03411f3d1979e237da8e03abc5f0ca16fb`** 已精确交付十三个 desktop 路径、SSH 推送及读回，tree **`7fbfb9a537c426fcd638593802ba106a37f319c5`**。基线 `e373db83edd411950c5cb31db2dd8ae306b50e13` 的 tree 为 `3afc079373b9f8ed3de43b997363bc9ddc8b1f8a`；其 Darwin8 与已签计划保持不变。MyGo 支持 Claude provider/schema1 原生资产编辑、服务端保存与本机应用分离、无秘密复制、预览、逐 secret slot 加最终批准的应用，以及本机 Status/Restore；本机操作仅限 Linux。Codex 等其它资产在 MyGo 仅 metadata，不代表支持本机应用，详细操作与缺口见 `CONFIGURATION.md`。

非作者完整实施审及 safe-focus successor 复验接受 tree `55a09520db96790ccaf2d856fcb9e284a0536789`。原四个独立 probe 文件未改：六项独立测试三轮共 18 PASS，包含 Apply/Restore/Delete/dirty-discard 初次 Enter、opaque 隐私、真实 TLS/SQLite 七槽转换和零网络离线恢复。只回退旧 view 的 overlay 先编译成功，再由四个原行为断言全部 RED，恢复后四项 GREEN；deliberate Tab/Enter、slot 与最终批准门禁、焦点包含/返回及身份失效测试二十轮共 340 个含子用例 PASS，隐私原 oracle 一百轮 PASS。独立 fresh desktop 为 186 个含子用例 PASS、一个既有 fault-overlay 条件 skip，真实请求 deadline 用例运行 20.12s，vet/build 通过；不把该 skip 说成已测故障分支。

`NATIVE-UI-01` 在新候选关闭：三个 Modal 的安全控件优先，拒绝位于凭据 checkbox 与最终批准之前，零槽也不能默认 Enter 应用/恢复。没有禁用正常键盘确认或自造 focus trap。密码、session、分享 token 与新凭据保持 opaque，无 raw 兼容影子；非常规 fmt、私有容器、可逆 hex、JSON/stdlog/slog 检查通过，不承诺主动取出秘密后的日志安全或物理擦除。组件独立报告 `native-configuration-ui-focus-fix1-independent-review.md` SHA256 为 `00a8673821a098d81c72f42c2fd6aa7511851670ef61d9cff4d473fc2b3f68d9`；旧隐私/焦点 REJECT、四个原 RED 与旧基线并行 state deadline RED 全部保留，不用主线程绿色抹除。

主线程对上述精确最终 tree 的隔离导出执行 **40 项门禁，全部实际 exit0，终态 FINAL_EXIT=0**：

| 主线程实际门禁 | 项数 / 结果 |
| --- | --- |
| npm ci/build | 2 / exit0 |
| 默认包并行全仓 Go tests、vet、模块校验 | 3 / exit0；未使用 `-p=1`，包输出没有 cached 行 |
| Buf lint/breaking、Go/TS 正常生成及双 parity | 6 / exit0 |
| 六 OS/architecture 组合 × CLI/server/MyGo | 18 / Go1.27.3、CGO0 build exit0 |
| CLI/MyGo 生产依赖边界 | 1 / 无 server/moejs |
| Darwin amd64/arm64 × cli/clientkey/desktop/install/subscription | 10 / test-compile exit0，不是执行 |

对应导出 `/tmp/cognate-native-focus-final.AQe6mf/source`，主线程 `verification.log` SHA256 为 `37135c038434f363cfd773ee0a0e83635408fa914ffe454fb0cac5072acd3cc9`。非作者 whole 机械 ratify 核对 961 项 tracked bytes/mode、精确十三路径的 base/new blob 与完整 binary diff、基线文档/module 保全，以及 28 个产物实际 Go1.27.3/CGO0/目标平台与包路径；两份生成目录另逐项比对。新 Darwin desktop 测试与 opaque Model 的兼容性由两个架构实际 test-compile 验证，不是只读推断。独立最终报告 `native-configuration-ui-focus-final-combined-review.md` SHA256 为 `4ed4810b9ca9c1dd1b1c949529c8eca526b141773030c2f882d0524ddf6589aa`。这部分核验实际主日志和产物，**没有冒称 reviewer 重跑 40 门**。

没有为本次 safe-focus 修复重新打开原生窗口。既有 Linux Xvfb/llvmpipe 截图、鼠标/键盘与真实服务闭环保留原 `5cf14557` 候选归属；新焦点证明来自 MyGo headless 真事件及真实 HTTP/SQLite/文件，不将旧截图记作新窗口焦点 PASS，更不证明 Darwin/Windows 配置应用或 accessibility。

另保留基线 `e373db83` 的 platform Actions **`40936595059` actual completed/failure**：三 OS primitives jobs 成功；Darwin baseline 编译成功，但 APFS fixture 创建/挂载失败，在 capability 与六项 ordinary 行为 probes 之前停止，随后 cleanup/sealer fail-closed，只封存拒绝收据。原日志 SHA256 为 `e24e299e708b7a17c90565ba777b289c1281676faff8863db24b52fba281e0c4`。**这不是六项 native 普通行为 RED 已取得，也不是安装能力 PASS**；不能将基础设施失败当作预期产品 RED，根因与后继修复须单独验证。

本次 native 交付 checkpoint 起初仅观察到后继 CI pending/in_progress；随后主线程实际等待并 fresh 核对 Build **`40937407082`** 为 completed/success，精确 head 为 `bd00bc03411f3d1979e237da8e03abc5f0ca16fb`，九个 jobs 全部成功。platform **`40937406971`** 则实际 completed/failure，同样在 APFS fixture RuntimeError 处、capability 与六项 ordinary probes 之前停止，cleanup/sealer 也未取得完整能力收据；原失败日志 SHA256 为 `4374e2be86896a46023ab658c74c541a5da0e74dd95b9cea372652fec1999006`。本次没有该 run 其它三 OS primitives 的完整 fresh 核验，不借用 e373 的成绩称新 run 全部成功。Build 绿色不覆盖 platform 失败，更不等于六项 native 普通行为 RED、Darwin 安装/配置应用或新窗口 PASS。

完整历史、值级/文件 diff、其它 agent 本机支持、来源配置导入、Pack 配置附件、自动应用、非 Linux 配置应用和完整 F12/M7/正式 Release 仍未由该十三路径交付。
