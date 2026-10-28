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

## 构建与未覆盖项

`.github/workflows/build.yml` 对 push/PR 执行检查，生成 CLI/服务端六平台二进制、Web 和双架构 OCI 构建产物。检查具体提交的 Actions 结果，不能把本机测试视为远端 CI 成功。构建产物不等于 GitHub Release；MyGo 原生客户端及其打包仍待实现。

本机没有 Docker，尚无本机容器运行验收；交叉编译不证明非 Linux 原生运行，Windows 服务端、WSL copy、断电恢复、外部真实 GitHub/OIDC、恶意插件操作系统级隔离仍不在已验证范围。无 cgo 测试也不替代 race detector。私有源、源快照自动替换、持续客户端自动同步、S3、完整导入、MyGo GUI 和首发验收仍待后续阶段。
