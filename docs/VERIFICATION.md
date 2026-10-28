# 开发预览验证

本记录对应 2026-10-28 开发预览阶段，不是首版发布或 M2–M7 全部完成的声明。权威功能范围见 `REQUIREMENTS.md`；持续进度见 `DEVELOPMENT.md`。

## 独立验收范围

- Linux CLI Pack 订阅、显式同步、跨 Pack 去重与保守卸载；匿名公开 skill 安装复用内容校验，但不隐式加入订阅，不需要 token 或 client.key。
- 服务端 Lists 定时增量/公开来源收录、公开目录与分类、默认私有及取消公开后的匿名拒绝；分类变更不修改 skill 内容 hash。
- MCP 用户级 token 生命周期、权限/有效期校验、取消与关闭行为，以及 Web 管理和一次性显示。分类与发布 MCP tools/scopes 尚未接通。
- Web 匿名和管理界面的 light/dark/system、zh-CN/en；保留主题/语言切换前的未提交表单。独立代表保留并重放了 OIDC 失败提示后切换语言导致 MCP 草稿丢失的回归，修复后双向通过，且登录会话切换的清理边界未被削弱。
- cc-switch、Magpie 仅完成服务端 moejs 只读导入预览。完整授权上传、选择性持久化、客户端配置附件及自动应用仍未完成。CLI 和 MyGo 不执行插件。

验收不是仅凭构建结果：公开目录使用实际浏览器与受控或公开归档来源；MCP 管理使用真实嵌入式服务端和浏览器；后端争用/过期/取消及客户端安装另有保留回归测试。截图只在一次性凭据已清除后采集；不保留账号、密钥、数据库、会话导出或含凭据 HAR。

2026-10-28 最终阶段整合审查结论为 Spec PASS / Quality PASS，限本阶段整合接缝读审与冻结输入、既有验收证据核对；新增确认 P1/P2 均为 0，另有三项非阻断文档建议。该审查未重新执行全套测试或浏览器验收，不代表 M2–M7 全量完成、首发验收或远端 CI 成功。

## 可复现检查

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

## 构建与未覆盖项

`.github/workflows/build.yml` 对 push/PR 执行检查，生成 CLI/服务端六平台二进制、Web 和双架构 OCI 构建产物。检查具体提交的 Actions 结果，不能把本机测试视为远端 CI 成功。构建产物不等于 GitHub Release；MyGo 原生客户端及其打包仍待实现。

本机没有 Docker，尚无本机容器运行验收；交叉编译不证明非 Linux 原生运行，Windows 服务端、WSL copy、断电恢复、外部真实 GitHub/OIDC、恶意插件操作系统级隔离仍不在已验证范围。无 cgo 测试也不替代 race detector。私有源、源快照自动替换、持续客户端自动同步、S3、完整导入、MyGo GUI 和首发验收仍待后续阶段。
