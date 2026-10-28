# 开发预览验证

本记录对应 2026-10-28 开发预览阶段，不是首版发布或 M2–M7 全部完成的声明。权威功能范围见 `REQUIREMENTS.md`；持续进度见 `DEVELOPMENT.md`。

## 独立验收范围

- Linux CLI Pack 订阅、显式同步、跨 Pack 去重与保守卸载；匿名公开 skill 安装复用内容校验，但不隐式加入订阅，不需要 token 或 client.key。
- 服务端 Lists 定时增量/公开来源收录、公开目录与分类、默认私有及取消公开后的匿名拒绝；分类变更不修改 skill 内容 hash。
- MCP 用户级 token 生命周期、权限/有效期校验、取消与关闭行为，以及 Web 管理和一次性显示。分类与发布 MCP tools/scopes 尚未接通。
- Web 匿名和管理界面的 light/dark/system、zh-CN/en；保留主题/语言切换前的未提交表单。独立代表保留并重放了 OIDC 失败提示后切换语言导致 MCP 草稿丢失的回归，修复后双向通过，且登录会话切换的清理边界未被削弱。
- cc-switch 仍仅有服务端内部只读预览；Magpie 已新增授权上传预览和 Web 显式选择仓库收录，下节记录本轮证据。完整插件机制、skill 内容及客户端配置迁移仍未完成。CLI 和 MyGo 不执行插件。

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
