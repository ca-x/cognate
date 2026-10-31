# Cognate Native Desktop

## Grok Build 原生资产编辑（2026-10-31）

主窗口新增 Grok Build 页面，复用 `grokclient.NewInput` / `View.Editable` 的内存接口，没有临时明文文件。支持 native-login/custom-model、七模型字段及 env absent、Keep/Replace/Clear、无凭据复制、删除与完整分组排序。Replace/Clear 需要独立授权，提交前显示绑定身份、revision 和差异的审阅并要求精确确认；未知字段只读、CAS 冲突和 Unknown 不自动重放。取消、离页、登出、切身份和关闭清理内存凭据并关闭独立会话。

这仍然只管理服务端配置资产，不切换 provider、不应用本机、不执行客户端插件。其他 agent、本机应用、完整 UI 精致化与跨平台运行验收继续按 `FORMAL-RELEASE-AUDIT.md` 推进，不能由本页入口或构建结果推断已完成。

独立审查发现并关闭两项真实问题：排序提交及失败曾无条件清除独立表单 Dirty，可能让重新载入跳过放弃确认；现仅成功 Create/Update 清除该标记。空闲切页曾无条件显示取消告警；现仅实际忙碌任务取消才显示，generation 失效与取消清理保持。真实排序成功/冲突、表单 CAS/Unknown、reload 保留草稿与成功保存均有回归。worker 最终 desktop 为 221 PASS / 1 SKIP / 0 FAIL，独立 reviewer 的六测试函数及子场景通过；证据 `/tmp/cognate-desktop-cancel-status.siv1Tysp`。

Linux 无桌面会话下，通过独立 cage headless Wayland/pixman 实际启动 MyGo 原生窗口，再用 wtype 的 Tab/Return 进入 Grok 未登录页；截图与日志位于 `/tmp/cognate-ui-native-combined.xhw6k5op/native-launch.png`、`native-grok-tab.png` 和 `native-app.log`。日志确认 `NATIVE_WINDOW_CREATED`，实际采用 llvmpipe 软件绘制。这仅证明该 Linux 环境的窗口渲染和未登录页键盘入口，不是已登录完整流程、用户显示器或 macOS/Windows 真人验收。

修复后的最终六目标构建再次启动，`final-native-grok-page.png` 为实际 Grok 页截图，底部保留初始引导而非误报取消。`final-native-grok-tab.png` 是前一次键盘尝试进入 AI 配置页的截图，不把文件名当作 Grok 验收；最终 Grok 证据以上述实图内容为准。

## Grok Build 接线底座历史（2026-10-31，后继见上）

`grok_service.go` / `grok_model.go` 提供独立私有资产会话：复用所有者身份与 grokclient，支持列表、安全摘要、无凭据复制、删除、排序、精确审阅确认与 revision CAS。结果不明后当前客户端封锁后续写入，不自动重放；这不是跨实例持久幂等保证。关闭时取消并排空请求，再释放待审阅操作。

此阶段没有主 Model/页面注册，也不是完整 Grok GUI。Create/Update 明确拒绝；native/custom 七字段编辑、密钥 Keep/Replace/Clear 仍须先补充 grokclient 的内存输入与结构化脱敏读取接口，不能靠临时明文文件或 raw JSON 绕过。未来窗口关闭、登出与身份切换必须显式调用此独立会话的 `Close()`；取消审阅不等于撤销已提交请求。没有本机应用或新原生窗口验收。

## Implementation plan (2026-10-28)

1. Verify MyGo v0.7.1 through the normal Go proxy and default checksum database; use a documented authentic cached version only if necessary.
2. Implement a native `ui` client with an asynchronous, cancellable controller and the shared catalogue, installation, subscription and file-import clients. Keep credentials in memory and reject unsafe origins.
3. Exercise real HTTP fixtures and headless native widgets in isolated homes; cross-build six CGO-disabled targets and run the available Linux native smoke.
4. Add build-only packaging automation, separate release publication, and document platform/runtime dependencies and outstanding acceptance gates.

Only the desktop write set and Go module files belong to this implementation. No commit or push is performed. Production acceptance, independent review, Windows/macOS execution, signed distribution and M7 completion remain pending until supported by evidence.

## 当前实现与边界

桌面入口为 `cmd/cognate-desktop`，module 为 `github.com/ca-x/cognate`。本实现是 **MyGo `ui` 原生窗口与原生绘制组件**，没有 URL/Web 内容、Web 前端构建或 Cobra 调用。组件不是操作系统的 NSButton/ListView 等系统控件；不应将“原生窗口”扩大描述为“全部采用系统原生控件”。

- 账号页：本地账号登录、注销。密码输入使用 `Password()`；密码在提交后清除，登录会话仅在内存保存。注销会立即清除本地会话，再尝试服务端撤销；断网/取消时不能保证远端撤销已经完成。
- 公开目录：匿名搜索、分页、列表键盘选择、经共用 `packclient` 核实详情。查看详情不会下载；必须勾选确认后再点击安装。改选和详情刷新都会清空旧详情/确认，刷新回填新版内容后必须重新确认。
- 安装：下载、归档校验和快照清理由 `packclient` 完成；安装账本、去重、agent 投影及锁复用 `install` / `state`，没有另写安装内核。当前本地安装内核仅支持 Linux；Windows/macOS 会显示真实“不支持”错误，不能视为已安装。
- Pack：刷新、保存订阅、显式同步、移除订阅，复用 `subscription.Manager`。保存/移除只改变订阅；同步按钮明确提示会应用**全部订阅**的安装、更新与备份移除变更。分享令牌使用安全输入并在提交后清空；持久化的是共用内核的加密凭据，不是明文。登录凭据不进入该存储。
- Pack 后台同步：原生“启用后台同步 / 停止后台同步”控件，默认关闭且不跨进程记忆。只同步已明确保存的全部本地订阅（可能多个服务地址），不是仅当前文本框或登录账号对应的 Pack；不会执行 JS 或自动应用 AI 配置。
- Magpie：文件路径、预览、逐仓库勾选、显式应用，直接复用 `importclient.PreviewFile` / `ApplyFile`。预览绑定文件 SHA256，文件变化拒绝旧预览；显示逐项结果。此操作收集服务端仓库，不直接在本地安装技能。
- 品牌图标只有一份来源：`assets/branding/client.png`。`assets/branding/branding.go` 的 `clientlogoassets.ClientPNG` 嵌入原图，供原生 UI 解码显示；MyGo manifest 的打包图标也引用该原图，不复制 PNG。

按 `docs/COGNATE-RENAME.md`，`~/.local/share/skillhub`、`~/.config/skillhub/client.key`、canonical `~/.agents/skills`、现有密钥派生域和 `skillhub.*` wire namespace 有意保持兼容。不要因改名重建密钥或搬移账本。

## 异步与安全约束

`internal/desktop/model.go` 只在 UI 线程变更模型。网络和文件/状态操作在受跟踪的 worker 中运行；工作快照不包含 `ui.Context` / `Element`。生产 dispatcher 使用异步 `Window.Update` 提交结果。每次任务、取消、地址/账号变化及关闭都会推进 generation；旧 generation 的闭包不能回填。单 worker 槽串行化内核操作，排队任务同样接受 context 取消。

关闭回调先 cancel 并清空会话，不在 UI 线程等待 worker。`App.Run` 返回后才 `Wait`；worker 不等待 UI 回调完成，避免退出时互相等待。取消不是跨网络/磁盘事务回滚：若写入已开始，状态提示要求刷新核实账本，不能承诺“取消后绝无落盘”。

GUI 暂无安装恢复面板。出现 pending/needs_attention 时，先使用独立 CLI 的 `cognate install-status` 查看，按 `docs/INSTALL-RECOVERY.md` 保守处理；不要删除账本或猜测 stage 归属。桌面不会为了恢复调用 Cobra，也不承诺自动恢复。

### 后台 Pack 策略与生命周期

显式启用后立即执行首轮，随后每 **5 分钟**一轮，单轮 context 截止 **2 分钟**。复用已签收的 `subscription.Watch`：串行执行，失败从 **30 秒**开始退避，依次 60/120/240 秒、最长 5 分钟，成功后重置。不另建 ticker 或重试内核；截止依赖底层取消合作，不是强杀线程的硬实时保证。

每轮在现有单 worker 槽内 fresh open `subscription.Manager`，Sync/List 完成或失败后同步 Close，再发布摘要并等待下轮；idle 不持安装锁或 Manager。后台期间及停止排空期间，手动安装与 Pack 操作在原生控件和方法层同时拒绝；浏览仍可继续，不因此偷偷停止后台。其他客户端进程仍可在 idle 期间取得内核锁；后续周期重新读取订阅，不缓存分享 token。

原生停止按钮、取消任务、修改服务地址、退出/更换账号和窗口关闭均取消后台；停止中保留排空状态，实际后台调用返回并关闭 Manager 后才重新允许本地写入/重启。UI 线程不等待 worker，worker 不等待 UI 回调；关闭后由入口已有的 `Wait` 排空。取消/关闭与异步入队由短 publication mutex 串行化，不包围网络、磁盘或 worker 排空；dispatcher 必须像 `Window.Update` 一样只入队，不执行/等待 UI 回调。旧排队结果还会检查 run 身份、取消与关闭状态，不回填新 origin/owner。界面仅显示固定安全摘要、计数和下次间隔，不显示原始 error、响应体或 token。

默认复用 Sync 的保守 preflight：本地修改、冲突或待恢复操作拒绝覆盖；远端撤权失败保留本地内容，不当作空清单卸载。正常清单移除/更新仍可能触发共用内核的备份与安装变更，启用说明明确提示。周期不是跨全部技能的单一回滚事务，取消/失败可能已有部分落盘，须核实账本。当前安装/后台内核仅 Linux，Windows/macOS 原生按钮标明不可用，不以交叉编译冒充安装支持。

回归包括实际 MyGo `ui.NewTester` 按钮事件、停止中的延迟 worker 与旧回调、真实 Connect/SQLite 两 Pack 重合技能的首轮/周期更新去重、撤权保留、本地修改拒绝、五种生命周期动作的实际 HTTP 取消与安装锁释放。另用 `testing/synctest` 验证真正 worker 排空及完整默认退避；所有 HOME/keys/DB/skills 为合成 TempDir。具体候选 base/tree、源码清单、负控和 native smoke 以本切片报告为准，不把这些测试扩大为完整 M7 或跨 OS 产品验收。

服务地址仅接受 HTTPS origin 或明确的 loopback HTTP；禁止 URL 用户凭据、路径、query/fragment、无效 host/port。HTTP 客户端禁用 cookie jar、拒绝重定向，并限制同 origin；Pack 多 origin 同步由共用客户端逐订阅限制。匿名请求不带登录会话。远端认证错误只展示安全摘要，不回显响应正文/凭据。Go 字符串不能保证从进程 RAM 中安全抹除；“仅内存”不等于可验证的内存零化。

登录和注销均显式限制响应为 4096 字节；HTTP timeout 不能代替字节上限。注销的 1 MiB protobuf 未知字段响应有专门拒绝回归测试。

生产依赖图不得包含 `internal/server`、`internal/cli`、MoeJS 或 Cobra。集成测试有意使用**测试专用**真实服务端 auth/httpapi/publiccatalog/packs 和隔离 SQLite，不会把服务端链接入桌面产品，也不访问 GitHub。

## 本地开发与验证

需要系统 Go **1.27.3**；示例不允许自动下载其他 toolchain，也不启用 cgo：

```sh
export GOTOOLCHAIN=local CGO_ENABLED=0
go run ./cmd/cognate-desktop

env -u DISPLAY -u WAYLAND_DISPLAY go test -mod=readonly -count=1 -timeout=120s ./internal/desktop ./cmd/cognate-desktop
go test -mod=readonly -count=1 ./internal/importclient ./internal/packclient ./internal/subscription ./internal/install ./internal/state
go vet ./internal/desktop ./cmd/cognate-desktop
go tool mygo vet internal/desktop
go tool mygo vet cmd/cognate-desktop
```

MyGo vet 每次只接受一个目录，分别检查 UI 实现目录与入口。Headless 使用真正 `ui.NewTester` 组件树与输入机制，不需要显示服务器，但仍需要平台文字渲染库。真实 auth/公开目录/安装/Pack HTTP fixtures 仅在 Linux 跑通；测试使用隔离 HOME、TMPDIR、状态目录与密钥，检查本地技能和 agent 链接，并检查没有持久化明文凭据。Magpie 使用生成的真实 Connect HTTP handlers 验证客户端协议与文件哈希；它不替代服务端 importer 自身的测试。

```sh
go build -mod=readonly -trimpath -o /tmp/cognate-desktop ./cmd/cognate-desktop
HOME="$(mktemp -d)" /tmp/cognate-desktop -smoke=2s -capture=/tmp/cognate-native-content.png
```

Smoke 会创建**实际原生窗口**，通过 `Window.Update` 执行内容捕获，再退出；不登录、不安装、不触碰真实 skills。捕获 PNG 是原生内容绘制结果，不是桌面 compositor 截图。无 DISPLAY/WAYLAND 的真实窗口启动应明确失败；不能用 headless 测试通过代替真实窗口验收。CI 使用 Xvfb 执行同一机制。

## 固定依赖与正常模块校验

正式版本固定 `github.com/egoist/mygo v0.7.1`，源码 revision `76dd068b72cc15526cda19db84d70a63c7a8843a`，上游 go directive 为 1.27.3。正式 module sum：

```text
github.com/egoist/mygo v0.7.1 h1:hCEgDRz3j6YWby2oAbby4vtMRUUnZZ5AZ/I1yMNBRbs=
github.com/egoist/mygo v0.7.1/go.mod h1:dVav53PwF2xEotjf4oA5lTY2Jp2exgzyjLhbSXhbMwE=
```

最初默认 `https://proxy.golang.org,direct` 的 IPv6 连接超时，随后使用**逐命令** `GOPROXY=https://goproxy.cn`，保留默认 `GOSUMDB=sum.golang.org`，正常完成 download/get/tidy；没有修改全局代理、生产 replace、禁用 SumDB 或伪造 checksum。改名后重新 `go mod verify`，并保存真实签名 SumDB lookup。正式构建不是早期 spike 的离线/本地源码替代模式，也不使用 v0.6.2 fallback。

## 自动打包与发布

配置在 `cmd/cognate-desktop/mygo.json`，不是根目录 Web 配置。MyGo CLI 按目录寻找 `mygo.json`，因此明确传入入口目录：

```sh
CGO_ENABLED=0 go tool mygo build -platform linux/amd64,linux/arm64 -o /tmp/cognate-packages cmd/cognate-desktop
```

`.github/workflows/desktop.yml` 与根 `build.yml` 独立，分为三个阶段：

1. `verify`：Go 1.27.3、CGO0 headless/HTTP 测试、Go/MyGo vet、六目标交叉编译、CgoFiles 和禁止依赖断言、Linux Xvfb 原生 smoke。
2. `package`：Linux/Windows/macOS **各自 runner** 构建 amd64 与 arm64。Linux 产出 `.deb`、带图标/desktop entry 的 tar.gz；`install.sh` 是额外产物，不声称它在 tar 内。Windows 安装 NSIS，逐架构断言 `*-setup-*-ARCH.exe` 存在且非空，避免 MyGo 跳过 installer 却退出成功。macOS 逐架构断言 `.dmg` 并检查 `.app`。
3. `publish`：只在显式手动 `publish=true`、已有 tag 与当前 commit/config version 一致、前两阶段通过后，创建 **draft** Release；使用可直接核验下载文件的 SHA256SUMS。普通 PR/tag 构建不自动发布。

当前 macOS 使用 `-sign '-' -skip-notarize`，仅 ad-hoc，不是 Developer ID 签名或 notarized；Windows installer 未配置 Authenticode。产物和 draft notes 都标明 unsigned preview，不能声称已签名。真正受信任分发需要后续独立签名/公证和真实 runner 证据。

## 平台依赖与未完成验收

- **CGO0 不等于静态二进制。** Linux 原生后端通过 purego 加载 GTK3、Pango/PangoCairo、Cairo、epoxy/OpenGL 等运行库；ELF 仍依赖 glibc/libdl/pthread。Debian 包声明 GTK3 依赖；native 配置 `linux.webview=false` 不要求 WebKitGTK。不能当作 scratch/纯 musl 程序分发。
- 中文 glyph 依赖系统 CJK 字体；Linux CI 安装 `fonts-noto-cjk`，应用不额外嵌入一套字体。
- Windows/macOS 两架构的编译不证明该 OS 上的输入、IME、焦点、窗口关闭、安装内核或打包产物可用。macOS 系统 framework 加载也不能被扩大承诺为“绝无 WebKit 符号”。
- 本机 Linux 图形栈是 llvmpipe CPU fallback；原生窗口与内容绘制已验证，硬件 GPU、Wayland、OS compositor 截图、真实多屏/缩放/无障碍仍需验收。
- 下方 checkpoint 已有 GitHub 三系统 runner 的真实包装证据；它不证明安装/卸载、最低 OS 版本或可信签名。Linux arm64 为交叉构建证据，不是运行证据。
- 当前桌面不包含完整 cc-switch AI 配置交互或原生配置应用。服务端已另行交付 moejs adapter registry 与 Claude 声明式 provider patch，但桌面不运行插件，也尚未消费该 patch 写本机配置；不能据此把完整首发或 M7 标记完成。
- 独立代表 review 和最终产品验收是独立门禁，实施者的测试不替代它们。

## 证据索引

改名前的正常依赖获取证据在 `.superpowers/sdd/2026-10-28-release/mygo-spike/evidence/production/`：`module-v0.7.1.json`（默认代理失败）、`proxy-cn.json`、`go-get-v071.log`、`module-verify.log`。它们保留历史真实名称，不回写。

改名后的验证放在 `.superpowers/sdd/2026-10-28-release/mygo-spike/evidence/cognate-production/`。最终复核修正后以 `validation-final.log`、`crossbuild-final.log`、`dependencies-final.txt`、`package-final.log`、`native-smoke-final.log`、`native-content-final.png`、`native-process-final.maps`、`no-display-final.log` 为准；`modules.log` 保存模块校验，`build-directory.txt` 指向仓库外临时产物。早期日志保留历史结果；不以 README 的平台列表代替验收。

2026-10-28 实测：Cognate 桌面测试最终 0.638s 通过，共用 importclient/packclient/subscription/install/state 测试通过；Go/MyGo vet 和 actionlint 通过；六目标 CGO0 构建及 CgoFiles/禁止依赖断言通过。六目标 MyGo 包装最终耗时 13.8s：Linux 两架构 deb/tar、Windows 两架构 GUI exe、macOS 两架构 app 实际生成；**NSIS 缺失明确跳过 Setup，Linux 主机使用 `-skip-dmg` 没有生成 DMG**。本机没有 dpkg-deb，改用 `ar`/`tar` 实际读取 deb control，确认 GTK3 依赖；这不是安装 deb 的验收。

打包后的 Linux amd64 二进制在真实 DISPLAY=:100 创建 Cognate 原生窗口、通过 Window.Update 捕获品牌图标/原生界面并退出 0；移除 DISPLAY/WAYLAND 后启动退出 1。进程 maps 没有 WebKit/JSC/libsoup，渲染为 llvmpipe；`runtime-packaging-details.log` 保存 deb control、Windows GUI subsystem、macOS Mach-O 类型、tar 内容及 canonical/打包 PNG 一致的 SHA256。`source-comparison.log` 记录正式 SumDB module 与已核实官方 revision 的 **1,893 个文件全部一致**。这些通过项不改变上述 pending 门禁。

独立只读 review 复现了三项缺陷并反馈实施方修正：详情刷新复用旧内容确认、注销响应未设字节上限、SHA256SUMS 把自身纳入清单。相应 UI/HTTP 回归的修前失败、修后通过证据分别为 `confirmation-red.log`、`logout-bound-red.log`、`review-regressions-green.log`；八个隔离假产物的校验清单修前失败/修后全通过见 `checksum-regression.log`。workflow 发布前也执行 `sha256sum -c`。这些是假产物的校验逻辑测试，不是实际发行包发布。

独立 reviewer `01a1824f-c9a8-73f0-8ce8-719da9580452` 已再次复验三项修复，无剩余确认缺陷；该结论仅限本轮修复，不替代跨平台产品/M7验收。完整交付摘要、独立结论和证据映射见 `.superpowers/sdd/2026-10-28-release/mygo-spike/PRODUCTION-REPORT.md`。

## 远端打包 checkpoint

2026-10-28：桌面切片提交 `611f2a069acd5c6dbed40a86dd99a1636b3aa7be` 与独立签收 tree `6f189c637734959ac2245dca7db6baa83c1bd059` 完全一致，已 SSH 推送。手动触发 Native desktop Actions `40691777676`，`publish=false`：verify、Linux、Windows、macOS 三个原生 runner 包装任务全部 success，publish 按设计 skipped。verify 包括 headless HTTP/原生组件测试、六 CGO0 构建及 Linux Xvfb 实窗捕获；各包装任务实际检查两架构 Setup/DMG/Linux archive 等产物存在。

该次远端产物补上了前述本机缺少 NSIS/DMG 工具的构建证据，不反向改写本机历史。Windows 安装器未做 Authenticode，macOS 仅 ad-hoc、未 Developer ID 签名/公证；此 checkpoint 不证明安装、首次启动、IME、各平台技能安装与完整配置体验，也不是 tag/Release。对应普通 Build Actions `40691768850` 则在旧 catalog deadline fixture 失败，未将桌面 workflow 的绿色替代完整构建结论；后续 fixture 修复和完整回归单独记录。

## 下载产物身份复核

2026-10-29 续接已完成上述 run 的三份 artifact ZIP 下载与摘要复核，提取六个实际 payload，未执行 Windows/macOS 二进制。`file` 确认相应 PE、Mach-O、ELF 架构；`go version -m` 全部显示 Go 1.27.3、CGO_ENABLED=0 和 revision `611f2a069acd5c6dbed40a86dd99a1636b3aa7be`。Linux payload 仍动态链接，不扩大为静态分发承诺。

此复核也发现原 Windows 两架构 payload 的 `vcs.modified=true`，而 macOS/Linux 为 false。原因是 MyGo v0.7.1 在 go build 前临时生成 main 包下的 `mygo_windows_<arch>.syso`，构建后再清理；原忽略规则没有排除该生成资源。隔离 Git clone 中真实 Windows amd64 构建复现 dirty；仅排除这个精确生成文件模式、不改 tracked 源码后，amd64 与 arm64 实际重编均为 clean。不能将旧六包整体描述为 clean，或使用关闭 VCS 信息来隐藏该问题。

修复候选包含 scoped ignore 与六平台 payload 元数据门禁（Go、CGO、GOOS、GOARCH、精确 revision、clean 状态）。原 Windows 产物触发门禁 red，隔离重编两包 green；尚需独立验收、提交及新三系统实际打包 CI，不能将本地 payload 重编等同 NSIS 安装器重打包。

后续 checkpoint：两文件修复已按独立签收 tree `9c5b5d45de94f8c3265ab166630910a910f17aa4` 提交并 SSH 推送 `39013dacfc01b2b50d6633cee3b2c324f09bf945`。独立 reviewer 另做同源 A/B/A 实际 MyGo Windows 重编：不排除资源为 dirty、精确排除后两架构 clean、撤回排除后再次 dirty，未关闭 VCS 信息。Native desktop Actions `40726579335` 以 `publish=false` 手动触发，精确绑定该 commit；当前 checkpoint 仅记录启动，不将尚未核对完成的三系统结果写成全绿，也不证明安装或可信签名。

完成 checkpoint（2026-10-29 续接核对，Actions 原始日志日期为 2026-10-28）：该 Native run 已 completed/success，verify 及 Linux、Windows、macOS 三个 package jobs 全绿，publish 按设计 skipped。已逐 job 核对真实 metadata step success 及六条实际完成输出：两架构分别满足 Go 1.27.3、CGO0、正确 GOOS/GOARCH、精确 `39013dac` revision、`vcs.modified=false`。Windows 两架构 Setup 存在性断言也实际成功；这是新 runner 上的真实打包与身份检查，不是旧 payload、本机交叉编译或文本 fixture。

该 run 的 Linux、Windows、Darwin unsigned artifacts 均已登记；新 ZIP 下载和落地摘要/包内容复核独立进行，不能把 CI 日志核对冒称全部下载完成。此 checkpoint 不代表安装/卸载、跨 OS 原生运行、可信签名、正式 Release 或完整客户端配置功能。对应普通 Build `40726569509` 同样精确绑定 `39013dac`，completed/success，不再用 Native 绿色替代普通构建结果。

下载复核 checkpoint（2026-10-29）：上述 run 的三个 ZIP 已实际下载，大小与 SHA-256 均匹配实时 GitHub artifact API，28 个 ZIP 条目逐项 CRC/hash 匹配落地内容。独立 reviewer 在无网络、空环境、只读输入的隔离中复核六个真实 payload：Go 1.27.3、CGO0、各自 GOOS/GOARCH、精确 `39013dac` revision、`vcs.modified=false` 全部成立；两个 Windows payload 不再带旧 dirty 标记。八个包装产物存在且非空，包括两个 Setup；14 个 metadata 拒绝负控实际失败。该复核没有执行、安装任何 payload；DMG 没有挂载，Setup stub 的机器类型不用于断言其内部 payload 架构。

| ZIP | SHA-256 |
| --- | --- |
| `desktop-darwin-unsigned.zip` | `5088009688c8f06b269632cc1f2a409885f41fced0dcd71356b6af0976a35ee7` |
| `desktop-linux-unsigned.zip` | `1ffa7ac3b6d35bee936a35f2f2f99fa217a4d03689c686962c34a45f8bc11ac3` |
| `desktop-windows-unsigned.zip` | `3eb70d10836ab43d80c7f8aa8425fc4e513523d94741dc9a4e0e4c40ed7c65bf` |

证据为 `.superpowers/sdd/2026-10-28-release/desktop-downloaded-payload-review.md` 与 `desktop-identity-{darwin,windows,linux}-payloads.log`。摘要证明下载 bytes 与该 API 声明一致，VCS 字段是实际读取的构建声明；二者不构成源码到二进制的密码学证明、供应链 attestation、签名/公证、包装内部同一性或跨 OS 运行验收。完整首发与最终功能版本的打包验收仍未关闭。

## 后续实际产物与 Linux 安装复核（2026-10-30）

Native desktop Actions `40942868634` 精确绑定 `89e4cf5cada447109aeef620c7e69bcab22ebd88`。独立读取 GitHub API 确认该 run 为 completed/success，verify 和三系统 package jobs 全部 success，publish skipped；不是正式 Release，也不表示最新版本。实际下载复核的是以下三个包装 artifact，而非 `desktop-verification`：

| Artifact | GitHub artifact ID |
| --- | --- |
| `desktop-windows-unsigned` | `12929005713` |
| `desktop-darwin-unsigned` | `12928950790` |
| `desktop-linux-unsigned` | `12927693111` |

主线程使用 `gh` 下载解包；独立复核对落地的 **28 个文件**建立本地 SHA-256 清单并在测试后再次逐项核对。此次没有保留下载原始 ZIP，**没有核对本次 ZIP digest/CRC 或证明 ZIP 与 API digest 相符**；前节旧 run 的 ZIP 证据保持原范围。六个实际 PE/Mach-O/ELF payload 均读取到 Go 1.27.3、CGO0、正确 GOOS/GOARCH、上述精确 revision 和 `vcs.modified=false`。这些是二进制内的构建声明，不是源码到二进制的密码学证明。

Linux 两架构 tar 内的二进制与裸 binary、deb 内 binary 逐字节相等；tar 和裸 PNG 与该 revision 的 `assets/branding/client.png` 相等。deb 图标是 256/512 像素缩放版本，仅核格式和尺寸，不宣称与原 PNG 字节相等。Windows 两个 Setup 仅核 NSIS 结构；macOS 两套 plist、ICNS、DMG 的 UDIF 尾部仅核结构，没有挂载 DMG、核验 codesign 或证明安装器内部 payload 与裸 binary 相同。

独立 reviewer 在自己的全新 0700 合成 HOME，以 `bwrap --unshare-all`、固定 uid/gid 1000、零 capabilities、只读包输入和无外部网络执行原 Linux amd64 `install.sh`：安装、重装、卸载均通过，实际安装的二进制和图标与归档相等，launcher 路径正确。`desktop-file-validate` 退出 0，仍有 `Development;Utility;` 双主类 hint，未将其隐藏。配置/状态 canary、无关 launcher 和链接均保留；缺归档、路径穿越、缺二进制归档被拒且原安装保留，namespace uid 0 的安装也被拒。全子进程 exec trace 确认没有启动应用或执行 curl/wget。

独立证据、命令、摘要和审阅结论见 `.superpowers/sdd/2026-10-28-release/desktop-packages-40942868634-independent-review.md`。这是已知实际包的隔离安装脚本 smoke，不是任意恶意归档/符号链接安全认证；没有执行 deb 安装、Linux arm64 应用、Windows/macOS 安装或应用启动，没有核验 Authenticode/Developer ID/公证，不关闭 macOS/APFS 技能安装或完整首发功能门禁。
