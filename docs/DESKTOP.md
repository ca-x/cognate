# Cognate Native Desktop

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
- Magpie：文件路径、预览、逐仓库勾选、显式应用，直接复用 `importclient.PreviewFile` / `ApplyFile`。预览绑定文件 SHA256，文件变化拒绝旧预览；显示逐项结果。此操作收集服务端仓库，不直接在本地安装技能。
- 品牌图标只有一份来源：`assets/branding/client.png`。`assets/branding/branding.go` 的 `clientlogoassets.ClientPNG` 嵌入原图，供原生 UI 解码显示；MyGo manifest 的打包图标也引用该原图，不复制 PNG。

按 `docs/COGNATE-RENAME.md`，`~/.local/share/skillhub`、`~/.config/skillhub/client.key`、canonical `~/.agents/skills`、现有密钥派生域和 `skillhub.*` wire namespace 有意保持兼容。不要因改名重建密钥或搬移账本。

## 异步与安全约束

`internal/desktop/model.go` 只在 UI 线程变更模型。网络和文件/状态操作在受跟踪的 worker 中运行；工作快照不包含 `ui.Context` / `Element`。生产 dispatcher 使用异步 `Window.Update` 提交结果。每次任务、取消、地址/账号变化及关闭都会推进 generation；旧 generation 的闭包不能回填。单 worker 槽串行化内核操作，排队任务同样接受 context 取消。

关闭回调先 cancel 并清空会话，不在 UI 线程等待 worker。`App.Run` 返回后才 `Wait`；worker 不等待 UI 回调完成，避免退出时互相等待。取消不是跨网络/磁盘事务回滚：若写入已开始，状态提示要求刷新核实账本，不能承诺“取消后绝无落盘”。

GUI 暂无安装恢复面板。出现 pending/needs_attention 时，先使用独立 CLI 的 `cognate install-status` 查看，按 `docs/INSTALL-RECOVERY.md` 保守处理；不要删除账本或猜测 stage 归属。桌面不会为了恢复调用 Cobra，也不承诺自动恢复。

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
- CI workflow 已配置但不等于已经在 GitHub 各 runner 跑过。Windows Setup 与 macOS DMG/可信签名、安装/卸载及最低 OS 版本仍 pending；Linux arm64 为交叉构建证据，不是运行证据。
- 当前骨架不包含新增的完整 cc-switch AI 配置交互，也没有将 agent 适配迁移成服务端 MoeJS 插件；这两项属于协调后的后续范围。不能据此把完整首发或 M7 标记完成。
- 独立代表 review 和最终产品验收是独立门禁，实施者的测试不替代它们。

## 证据索引

改名前的正常依赖获取证据在 `.superpowers/sdd/2026-10-28-release/mygo-spike/evidence/production/`：`module-v0.7.1.json`（默认代理失败）、`proxy-cn.json`、`go-get-v071.log`、`module-verify.log`。它们保留历史真实名称，不回写。

改名后的验证放在 `.superpowers/sdd/2026-10-28-release/mygo-spike/evidence/cognate-production/`。最终复核修正后以 `validation-final.log`、`crossbuild-final.log`、`dependencies-final.txt`、`package-final.log`、`native-smoke-final.log`、`native-content-final.png`、`native-process-final.maps`、`no-display-final.log` 为准；`modules.log` 保存模块校验，`build-directory.txt` 指向仓库外临时产物。早期日志保留历史结果；不以 README 的平台列表代替验收。

2026-10-28 实测：Cognate 桌面测试最终 0.638s 通过，共用 importclient/packclient/subscription/install/state 测试通过；Go/MyGo vet 和 actionlint 通过；六目标 CGO0 构建及 CgoFiles/禁止依赖断言通过。六目标 MyGo 包装最终耗时 13.8s：Linux 两架构 deb/tar、Windows 两架构 GUI exe、macOS 两架构 app 实际生成；**NSIS 缺失明确跳过 Setup，Linux 主机使用 `-skip-dmg` 没有生成 DMG**。本机没有 dpkg-deb，改用 `ar`/`tar` 实际读取 deb control，确认 GTK3 依赖；这不是安装 deb 的验收。

打包后的 Linux amd64 二进制在真实 DISPLAY=:100 创建 Cognate 原生窗口、通过 Window.Update 捕获品牌图标/原生界面并退出 0；移除 DISPLAY/WAYLAND 后启动退出 1。进程 maps 没有 WebKit/JSC/libsoup，渲染为 llvmpipe；`runtime-packaging-details.log` 保存 deb control、Windows GUI subsystem、macOS Mach-O 类型、tar 内容及 canonical/打包 PNG 一致的 SHA256。`source-comparison.log` 记录正式 SumDB module 与已核实官方 revision 的 **1,893 个文件全部一致**。这些通过项不改变上述 pending 门禁。

独立只读 review 复现了三项缺陷并反馈实施方修正：详情刷新复用旧内容确认、注销响应未设字节上限、SHA256SUMS 把自身纳入清单。相应 UI/HTTP 回归的修前失败、修后通过证据分别为 `confirmation-red.log`、`logout-bound-red.log`、`review-regressions-green.log`；八个隔离假产物的校验清单修前失败/修后全通过见 `checksum-regression.log`。workflow 发布前也执行 `sha256sum -c`。这些是假产物的校验逻辑测试，不是实际发行包发布。

独立 reviewer `01a1824f-c9a8-73f0-8ce8-719da9580452` 已再次复验三项修复，无剩余确认缺陷；该结论仅限本轮修复，不替代跨平台产品/M7验收。完整交付摘要、独立结论和证据映射见 `.superpowers/sdd/2026-10-28-release/mygo-spike/PRODUCTION-REPORT.md`。
