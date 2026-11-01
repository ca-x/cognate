# 正式组件版本发布验证

## 候选验证（2026-11-01）

- 发布候选基于 `70f47c7`；生产 Go 代码未改，增加 Pack 配置测试夹具的真实 TLS `/healthz` 就绪握手，并将桌面版本从已发布预览的 `0.1.0` 升至 `0.1.1`。
- Build `41275009544` 的 `TestPackConfigurationConnectionCloseRejectsFurtherWork` 失败已复现：单 CPU 连续 20 次均在夹具清理时报告 `Serve join runtime lease: invalid or unsafe input`。夹具先返回，服务 goroutine 尚未开始便被 `App.Close` 关闭；生产拒绝关闭后的 Serve 是正确行为，不更改该检查。
- 加入真实 TLS 就绪握手后，同命令 `CGO_ENABLED=0 go test -mod=readonly ./internal/desktop -run '^TestPackConfigurationConnectionCloseRejectsFurtherWork$' -count=20 -parallel=1 -cpu=1` 通过。握手使用测试 context、5 秒 HTTP 超时、有限响应读取，夹具清理在握手前登记；不增加重试或等待睡眠。
- Go 1.27.3、`CGO_ENABLED=0`：`go test -mod=readonly ./...` 退出 0，51 个测试包通过、77 个包无测试；`go vet ./...` 退出 0。这是本候选的实际输出，不借用历史用例计数。
- Node 26.12.0：Web typecheck/build 退出 0；Buf lint、Go/Web 两份生成与跟踪文件的完整 diff 均退出 0。
- 非作者限定复核接受 TLS 握手与桌面版本变更：独立单 CPU 目标 10 PASS，相关 Pack 配置 24 PASS/1 个既有 native gate SKIP；原夹具 overlay 反控编译后真实失败。报告 SHA256 为 `881fcc8ecc165a3d88c9db4dff9d877cfae0f022e6d608382b81a94732379ba3`，不扩大为完整产品或发布资产资格。
- CLI/GUI 的远端打包、实际下载 SHA256、许可与元数据校验、服务端镜像验证和喵喵回执须在发布后分别记录；本节不提前声明成功。

## 历史失败与支持边界

Platform `41275009573` 的三个 OS primitives、Darwin core/caller runtime、baseline observation 和双卷清理步骤成功，但 evidence sealer 失败，因此整个 run 仍为 failure；不能把步骤成功称为完整封存资格。后继修复与收据单独记录，不重跑旧 run 抹去失败。

正式组件的受支持范围见 `RELEASE-v0.1.0.md`；完整产品目标、未完成的恢复回收、配置自动应用及原生平台门禁保持在 `TODO.md`，不会因组件 tag 而关闭。
