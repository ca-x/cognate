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

GUI `gui-v0.1.1` 的 run `41276593020` 在原生集成测试编译阶段因 `web/embed.go: pattern all:dist: no matching files found` 失败，没有打包或创建 Release。加入与平台测试相同的真实 Web 构建前置；不提交占位 dist、不减少测试、不重跑或移动旧标签，桌面版本改为 `0.1.2` 后重新触发分发。

后继从 `git archive` 创建无 dist 的私有干净源码，真实运行 `npm ci`、Web build 和工作流原命令 `env -u DISPLAY -u WAYLAND_DISPLAY go test -mod=readonly -count=1 -timeout=120s ./internal/desktop ./cmd/cognate-desktop`，退出 0（desktop 88.576 秒）。非作者五路径静态限定 ACCEPT，actionlint 结构检查与新增命令 ShellCheck 通过；全工作流保留原有 SC2155/SC2035 两项告警，不将它们谎称全绿。

Platform `41275009573` 的三个 OS primitives、Darwin core/caller runtime、baseline observation 和双卷清理步骤成功，但 evidence sealer 失败，因此整个 run 仍为 failure；不能把步骤成功称为完整封存资格。后继修复与收据单独记录，不重跑旧 run 抹去失败。

正式组件的受支持范围见 `RELEASE-v0.1.0.md`；完整产品目标、未完成的恢复回收、配置自动应用及原生平台门禁保持在 `TODO.md`，不会因组件 tag 而关闭。

## 正式分发收据（2026-11-01）

| 组件 | 标签与源码 | 成功 Actions run | 实际回读 |
| --- | --- | --- | --- |
| Server | `server-v0.1.0` / `9d2070a1ae9c51e7e2710ec74970582e1d6c561c` | `41276592652` | 双架构 OCI smoke、原产物提升、两份 distinct manifest 拉取/执行 help、匿名 index 验证 |
| CLI | `cli-v0.1.0` / `9d2070a1ae9c51e7e2710ec74970582e1d6c561c` | `41276592480` | 六归档与 SHA256SUMS 全量下载、逐字节 hash、许可及 Go/VCS/CGO0/目标元数据、隔离 Linux amd64 help |
| GUI | `gui-v0.1.2` / `bd139713684869c11513fdc5377d68cae927bf94` | `41277181998` | 八安装产物及 SHA256SUMS 全量下载/hash；Linux tar binary 元数据、tar/deb 许可回读，原生 CI 检查 DMG/NSIS 内部许可与所有目标 binary 身份 |

服务端 index 为 `sha256:d0c51a74281945dd726070f40125101a8460931a3258025d3916c02329819691`。主线程另用匿名 registry grant 取得实际 index 字节，复算 SHA256 与 header/工作流 digest 一致，精确包含 Linux amd64/arm64。不创建 Docker latest 或服务端 GitHub Release。

CLI/GUI 仅在完整下载校验后公开，并回读为 `draft=false`、`prerelease=false`；另用无认证 GitHub API 检查，资产数分别为 7/9（含 checksum 文件）。正式说明明确未完成能力、Linux 优先及桌面签名限制，不把其它平台归档当作完整运行支持。

普通流式 CLI 下载因低速中止，部分字节保留；改用官方 GitHub asset API 的有限分段传输，拼接后与 API digest 及发布 SHA256SUMS 两重核对。没有重传或替换发布资产。全部六 CLI 包和八 GUI 包均取得完整字节，不用部分下载或远端 digest 单独冒称下载验收。

喵喵 Exec LPK 的独立版本、启动门禁和商店回执另行记录；服务端/CLI/GUI 成功不代替 LPK 成功。

## Darwin sealer 后继窄修

原 producer 将严格校验后的 PTY 诊断帧从 9 个 raw events 中移除，再按 8 个事件分类；旧 sealer 直接分类未归一化的 9 个事件。本地执行原代码确定复现 `RuntimeError: unexpected native events`。仅新增同一严格 parser 及 PTY hook，原始详情须等于封闭 capability receipt，producer/classifier/core51/caller50/22 源、三 binary 绑定、预算与清理均不改。

作者和非作者分别运行实际抽取的 producer/sealer：原代码 RED exit1，修后完整正例 exit0、18 个负例均 exit1；8 份 Python workflow body 编译与 actionlint 结构检查通过。主线程核对两份 manifest、基线及候选 workflow 的确切 hash 后合入后继主线，不移动任何正式 tag。机械用例并非真实 Mac；远端 raw 与首个异常未上传，不能排除其它问题，原失败 run 保持失败，后继运行须有自身收据。
