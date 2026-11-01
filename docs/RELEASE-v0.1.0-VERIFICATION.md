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

### 喵喵 Exec LPK 0.1.0

包装仓库 `lazycat-contrib/cognate-lzcapp` 的 `v0.1.0` 固定包装源码 `4c7df600a3425f0281c07094fadd4a7245714a69`，上游精确固定 `server-v0.1.0` / `9d2070a1ae9c51e7e2710ec74970582e1d6c561c`。Actions `41278328477` completed/success，真实 root 启动门禁及 LPK/喵喵发布成功；官方商店关闭，稳定标签不执行预览标记。

最终 `community.lazycat.app.cognate-v0.1.0.lpk` 为 77,757,952 字节，SHA256 `5e03f8025575162f1a63b357ed76e43f983a30bce3005f1954ec749478009d91`。完整下载、解包白名单/类型/权限、原 wrapper、源提交、CGO0/Go/目标身份及许可核验通过，二进制与本地固定源码构建逐字相同；对这份最终 Release 的真实解包内容重新运行完整 namespace 启动 suite，exit0。喵喵回执 published=true、skipped=false，app `670` / version `3838`，与 GitHub asset URL/版本和上述 SHA256 一致；Release 回读为公开、非 draft、非 prerelease。

旧启动测试只识别早期的丢密钥文案，因此新源码在正确拒绝且未重建 key 时使旧断言 RED；测试改为精确校验现有 runtime refusal 协议，同时要求非零退出、没有替代 key、SQLite bytes/dev/inode 不变，并增加拒绝负控。不改变服务端、wrapper 或向导参数，也不把任意错误当作安全通过。最终安装、原管理员/原 key 重启、upgrade、各不安全卷和祖先拒绝均通过；真机平台 OIDC 与实际用户数据升级仍待用户测试。

普通下载及首轮分段中断/不完整保留，后继恢复完整字节并与 API digest、实包内容及商店回执核对后才声明下载验收。包装收据文档提交 `808c29906c7edeb69ce94c4ba307933e2da22b58` 已 SSH 推送；版本 tag 保持在原包装源码，不重发或移动。

## Darwin sealer 后继窄修

原 producer 将严格校验后的 PTY 诊断帧从 9 个 raw events 中移除，再按 8 个事件分类；旧 sealer 直接分类未归一化的 9 个事件。本地执行原代码确定复现 `RuntimeError: unexpected native events`。仅新增同一严格 parser 及 PTY hook，原始详情须等于封闭 capability receipt，producer/classifier/core51/caller50/22 源、三 binary 绑定、预算与清理均不改。

作者和非作者分别运行实际抽取的 producer/sealer：原代码 RED exit1，修后完整正例 exit0、18 个负例均 exit1；8 份 Python workflow body 编译与 actionlint 结构检查通过。主线程核对两份 manifest、基线及候选 workflow 的确切 hash 后合入后继主线，不移动任何正式 tag。机械用例并非真实 Mac；远端 raw 与首个异常未上传，不能排除其它问题，原失败 run 保持失败，后继运行须有自身收据。

后继主线 `3e98be38ca6613070ae660a8dcbe4f0963b0df15` 的 Platform run `41278181617` 实际 completed/success。主线程取得三份原 ZIP，逐一核 API size/digest、CRC、完整 manifest、精确 commit/tree/全部 tracked blob，以及双卷 detach0/无剩余挂载/无 unknown 或辅助清理失败。core 收据包含 51 个通过条目、caller 收据包含 50 个通过条目；caller start/finish 都绑定 22 源和 3 binary。这只取得该源码与该有限原生门的收据，不把四项保留的普通产品 RED 改为支持，不倒签旧正式 tag 的安装、Pack 或 GUI 资格。

额外本机的已下载 GUI 窗口探针未取得结果：宿主无 `xvfb-run`（exit127），当前 `DISPLAY` 亦无法连接（exit1）。两份环境失败日志保留，不计为通过的本机窗口验收，也不据此覆盖原生 CI 已完成的窗口 smoke；完整平台体验仍待真人测试。
