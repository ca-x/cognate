# Grok Native Editor Implementation Plan

**Goal:** 完成 Grok Build 的 MyGo 原生资产编辑入口，不把资产保存等同于本机应用，也不替代其余正式版需求。

**Architecture:** 主线程实现 grokclient 的无文件、独立所有权内存输入与结构化脱敏读取；并行 worker 只接 MyGo 表单、模型与生命周期。复用现有鉴权、审阅票据、revision CAS、未知字段只读和 Unknown 封锁，不新增客户端插件执行。

**Tech Stack:** Go 1.27.3、MyGo 原生组件、Connect RPC；生产、测试与构建均 `CGO_ENABLED=0`。

**Spec:** `docs/REQUIREMENTS.md` F12、`docs/CONFIGURATION.md`、`docs/DESKTOP.md`。

## 接口与分工

- 主线程只写 `internal/grokclient`：新增 `Mode`（`NativeLogin` / `CustomModel`）、`Draft{Name, Mode, Model}`、`Model{Profile, UpstreamModel, Endpoint, Name, APIBackend, ContextWindow, EnvKey}`；无密钥字段。
- `NewInput(ctx, Draft, KeyAction, []byte) (*Input, error)` 校验并复制所有可变输入，沿用既有完整 provider 验证，不触碰文件系统，Close 清理独立密钥。
- `(*View).Editable(ctx) (EditView, error)` 返回独立 DTO：`Metadata`、`Draft`、`KeyPresent`、`ReadOnly`、`NeedsCredential`；不暴露底层 protobuf 或秘密，关闭/取消后拒绝。
- GUI worker 只写 `internal/desktop`，接 model、原生页面与主 Model 分派；复用上述 API 和已有 `grokAssetsModel`。必须关闭/登出/身份切换排空会话，取消审阅不声称撤销已提交操作。
- 独立审计 agent 只写 `docs/FORMAL-RELEASE-AUDIT.md`，保持首个正式版完整范围，不缩成当前能力。

## 验收步骤

- [x] 先写内存输入、读取快照、别名隔离、取消/关闭、无日志泄露的失败测试。
- [x] 实现最小 API，真实 Connect create/update/read 验证七字段、最大整数、env absent、Keep/Replace/Clear、切 native 的显式清理及 Unknown。
- [ ] 接原生表单和入口，替代未完成占位；明确独立密钥授权，模式/取消/身份切换清密钥，未知字段只读，CAS 冲突不覆盖。
- [ ] 原生组件测试、可用 Linux 真实窗口 smoke、无 cgo 多平台构建；不把交编算真人平台验收。
- [ ] 独立验收后提交、SSH 推送读回，更新 TODO/跨机器记忆；整体正式版目标保持 active，直到完整审计满足才发布。

## 内存 API 阶段证据

2026-10-31：`NewInput` 与 `View.Editable` 已通过独立只读审查。显式 `CGO_ENABLED=0` 的 grokclient/CLI 全包测试通过，新增凭据边界、结构化日志脱敏、关闭后独立快照与真实 RPC 保留/清理测试后，grokclient 全包再次通过；两包 vet 通过。证据目录 `/tmp/cognate-grok-editor.qvwKBdEu`。这不是 GUI 页面、原生运行或全仓候选验收证据。
