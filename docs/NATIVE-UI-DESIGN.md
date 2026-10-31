# 原生首发 UI 第一轮精致化

日期：2026-10-31。基线：`main` 的 `88d3a99`。范围是 MyGo 原生共享外壳，不是完整桌面首发验收。

## 方向与写集

精密原生控制台：冷色只承担操作与定位，中性背景、内容面板和反馈区形成层级。保留成熟 MyGo `ui` 控件、原有 logo、系统字体、系统偏好和键盘反馈。不采用 Web，不启用 cgo，不增加依赖，不自绘控件或实现动画引擎。

实际写集仅为 `internal/desktop/view.go`、新增的 `internal/desktop/native_shell.go` 与 `internal/desktop/native_shell_test.go`，以及本文档。没有修改业务 Model、Grok 编辑器、configuration Model 或状态协议，也没有提交或推送。业务表单仅调整 Pack 列表的布局高度边界，保留内容和交互；并行 F4 工作的文件不属于本轮写集。

使用 ui-ux-pro-max 的键盘/对比度/导航规则、Emil 的频率与反馈原则、Apple 的层级与偏好适配原则，以及 animate / animation-vocabulary 的目的性动效门禁。Web 专属 CSS、弹簧与浏览器方案不移植到原生界面。

| Before | After | 原因 |
| --- | --- | --- |
| 整窗白底，品牌、说明与内容缺少材质区分 | 中性冷灰窗底、独立内容面板、细边界；深色同步分层 | 让任务内容成为视觉主体 |
| 页签只呈现短暂焦点框 | 复用 PrimaryButton、Bold 与 Selected，持续显示当前位置，另保留原生焦点框 | 当前页与当前键盘焦点不是同一概念 |
| 服务地址仅有 placeholder，账号语境分散 | 服务连接区保留原来的输入标签；顶部显示会话对应的账号身份 | 输入框不代表已连接，填写用户名不代表已登录 |
| 品牌很强，任务标题很弱 | 品牌收紧，工作区标题独立使用系统字号比例 | 保留业务标题和全部安全说明，增加扫描层级 |
| 反馈与取消挤在底部，缺少独立层级 | 紧凑反馈栏：任务活动、原始 Status、取消任务；必要时保留 WatchStatus | 状态不能被装饰性成功或虚构指标替代 |
| 窄窗导航可能横向拥挤 | 原生 Row.Wrap；品牌副标题和长身份使用 SingleLine | 保留六个原标签与键盘顺序，不做自绘折叠菜单 |

## 安全与原生偏好

- `origin`、`page-0` 至 `page-5`、`cancel` 的原 Key 字面量、所有原导航标签和业务控件保持；原 `SetOrigin`、`configurationNavigate`、`Cancel` 路径保持。
- 身份只依据进程内已有 session：未登录不会把编辑中的 Username 当成已认证身份；有会话才展示用户名，缺少用户名时仅显示内存会话。会话内容不进入文本、截图或日志。
- 未接入额外网络探测，不声称服务器已连接。未知结果、未应用、未重载等安全状态直接显示原始 `model.Status`；不猜测成功，不虚构统计。后台任务活动只依据既有 Busy / WatchActive / WatchStopping。
- 主题从 `Context.Theme()` 复制，仅调整中性材质。系统 Accent、Focus、Font、字号缩放保留；HighContrast 时完全保留系统主题与背景。标题使用 `Theme.Rem`，间距使用 `Theme.Space`。
- 新工作区包裹层没有重写业务表单、确认弹窗或敏感审批。P2 修复仅为原来不滚动的 Pack 页补上原生 Scroll；已有整页滚动的业务页不重复包裹。全量既有控件事件及安全焦点测试继续运行。

## 动效决策

唯一新增的 motion 是 **state indication / feedback**：真实任务从空闲转入活动、或活动结束时，反馈栏内活动标签的底色通过 MyGo 原生 `ElementTransition{Colors: true}` 做 160ms 颜色过渡。

文字、状态和取消资格立即更新，不等待过渡。没有导航进入动画、键盘动画、位置/尺寸过渡、循环脉冲、数字滚动、假进度或每次按键特效。原生按钮自身的 hover / press / focus 保持。`Preferences.ReduceMotion` 时不注册这项过渡；切换深浅主题或高对比度时只更换非交互活动标签的身份，不让旧底色与新文字短暂失配。底栏和取消按钮身份稳定，主题切换不丢失取消按钮焦点。

本轮发现并修复外壳自身的回归：深色切换最初残留浅色底栏；底栏增加两行后挤压既有配置恢复操作的滚动视口；最初给整条底栏更换主题身份会丢失取消按钮焦点。最终只在非交互活动标签上使用主题隔离的原生颜色过渡，保留底栏控件身份，改为紧凑单行底栏并释放内容底部空间。新增主题切换、取消按钮焦点保持和真实 fixture 恢复入口测试，未修改业务实现或既有测试。

## 实图与键盘证据

证据根目录：`/tmp/cognate-native-shell.B8Lw3t`。这些都是实际组件渲染或 compositor 截图，不是生成图或 mockup，不作为二进制资源加入仓库。

| 阶段 / 场景 | Before | After |
| --- | --- | --- |
| 原生 Grok 未登录页，1280×720 | `before/native-grok-1280x720.png` | `after/native-grok-1280x720.png` |
| 原生真实键盘：Tab 到 Grok、Return 进入 | 原基线没有持续选中样式 | `after/native-grok-keyboard-focus.png`、`after/native-grok-1280x720.png` |
| 原生焦点移回服务地址，当前页仍选中 | 原基线依赖焦点框辨认 | `after/native-grok-focus-moved.png` |
| 640×600 浅色账号页，MyGo Tester | `before/headless-dark-false-page-3.png` | `after/headless-dark-false-page-3.png` |
| 640×600 深色账号页，MyGo Tester | `before/headless-dark-true-page-3.png` | `after/headless-dark-true-page-3.png` |
| 420×600 深色 Grok，换行导航，MyGo Tester | 本轮基线没有此尺寸截图 | `after/dark-true-width-420-page-5.png` |

第一行 Before 原图来自用户指定的 `/tmp/cognate-ui-native-combined.xhw6k5op/final-native-grok-page.png`，仅复制未加工。另在修改前运行原有 `TestNativePagesLightDarkAndNarrow`，生成 640×600 深浅主题四页基线；修改后重跑同一测试，并额外保存六页 × 深浅主题 × 420/640/920/1280 四宽度共 48 张原生 Tester 截图，统一高度 600。

真实窗口在独立 cage headless Wayland 上启动，设置 `WLR_BACKENDS=headless WLR_RENDERER=pixman GDK_BACKEND=wayland`，私有 `XDG_RUNTIME_DIR`，隔离 HOME / XDG 配置和数据目录。grim 采 compositor 实图，wtype 用七次 Tab 到 Grok，再 Return 和一次 Tab，证明入口与焦点移开后的持续选中。进程通过 smoke 正常退出，没有登录真实账号或写真实配置。

`native-session.log` 确认 `NATIVE_WINDOW_CREATED`，实际采用 llvmpipe 软件绘制；不能把输出中失败的 GPU 尝试描述为硬件 GPU 验证。

## P2：短窗与大字号 Pack 可达性

Godel 独立复核发现：内容标题和内边距占用垂直空间后，原本非滚动的 Pack 页没有溢出处理，导致业务控件位于固定反馈栏后方。其 overlay 在 640×540 默认字号、920×600 的 125% 字号复现；后者旧壳停止文字 y464–491，新壳 y553–580，而底栏约从 y538 开始。原独立证据为 `/tmp/cognate-shell-readonly-review.uwdJj9/{before,after}.png`。首轮 shell 边界测试只检查标题、导航和底栏，未检查业务控件可见性；277 PASS 不能证明这一点。

| Before | After | Why |
| --- | --- | --- |
| 非滚动 Pack 内容超过面板高度，滚轮无法使停止控件到达底栏上方 | 仅 Pack 页使用成熟 `ui.Scroll`，固定标题和反馈栏不随业务内容滚动 | 短窗和大字号仍可用滚轮或 Tab 到达业务操作，不缩字号、不删控件 |
| 列表在外层无限高度布局中可能塌缩或无限扩张 | 原生订阅列表保留自身滚动、选择与 Key，高度限制为 8–16 rem | 保留原生列表键盘导航；不对账号、AI、Grok 等已有整页滚动的页面再包裹 |

本次 P2 证据根目录为 `/tmp/cognate-native-shell-scroll.Lp5pOi`：

- 永久 `TestNativeShellPackShortWindowControls` 先在未修源上运行，`red.log` 中 16 个场景全部 RED；覆盖四组窗口/字号 × 深浅主题 × 滚轮或键盘。断言完整文字矩形在标题下方、固定底栏上方，随后点击或 Return 必须真实取消 watch context 并进入停止中，而不只是 `HasText`。
- 修复后 `green.log` 全部 GREEN；新增 `TestNativeShellPackShortWindowListNavigation` 用 32 条合成订阅验证首行点选、End 选择末项及末项可见，避免外层滚动破坏列表选择与可达性。
- `red-overlay.json` 只替换构建输入的两份修复前源码，工作树保持 GREEN；同一永久测试重放于 `red-replay.log` 再次得到 exit 1、16 场景 RED。
- `red/920x600-scale-1.25-dark-false-keyboard-true-initial.png` 与 `green/920x600-scale-1.25-dark-false-keyboard-true-stop-visible.png` 为修前/修后 MyGo Tester 实际渲染；`green/pack-short-window-list-end.png` 为末项导航证据。
- `green/native-640x540-{initial,stop-visible,stop-action}.png`、`green/native-920x600-scale-1.25-{initial,stop-visible,stop-action}.png` 为真实 cage compositor 截图。wtype 的 Tab 使停止按钮完整出现在固定底栏上方，Return 实际执行停止；`native-640.log`、`native-920.log` 均记录 `WATCH_ACTIVE true` → `false`、`WATCH_STOPPING true` → `false`。

真实窗口使用私有 HOME/XDG 和 headless Wayland/pixman，未读取真实配置。`probe-overlay.json` 仅把临时 `native-probe.go` 作为入口构建输入，调用真实 desktop Model/界面并启动后台同步，地址仅为不可达的本机 `127.0.0.1:1`。窗口使用 frameless 保证内容尺寸分别为 640×540、920×600；125% 由 probe 对原生 theme 字号乘以 1.25，日志确认字号 17.50，属于明确 fixture，不冒充系统 portal 字号验收。`wlr-randr` 仅解包至证据目录的 tools，没有安装或增加项目依赖。实际绘制仍为 llvmpipe 软件绘制。

复核命令（RED 命令预期失败；GREEN 不使用 overlay）：

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
EVIDENCE=/tmp/cognate-native-shell-scroll.Lp5pOi
sha256sum -c "$EVIDENCE/final-source.sha256"
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -overlay="$EVIDENCE/red-overlay.json" -count=1 -run '^TestNativeShellPackShortWindowControls$' -v ./internal/desktop
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -count=1 -run '^TestNativeShellPackShortWindow' -v ./internal/desktop
```

## 验证与边界

所有 test / build 均显式设置 `CGO_ENABLED=0 GOTOOLCHAIN=local`，使用 `/home/czyt/.local/share/mise/installs/go/1.27.3/bin`，没有运行 `-race`。

```sh
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -count=1 -timeout=180s -json ./internal/desktop ./cmd/cognate-desktop
CGO_ENABLED=0 GOTOOLCHAIN=local go vet ./internal/desktop ./cmd/cognate-desktop
CGO_ENABLED=0 GOTOOLCHAIN=local go tool mygo vet internal/desktop
CGO_ENABLED=0 GOTOOLCHAIN=local go tool mygo vet cmd/cognate-desktop
```

- 新增覆盖：六页 × 深浅色 × 四宽度的导航、身份、标题、反馈边界；Tab/Return、切窄窗；未保存草稿导航保护；会话清理和无敏感文本；原始 Unknown / WatchStatus 保留；系统主题/高对比度/字号缩放；ReduceMotion；主题切换不动画；短暂任务反馈无持续刷新；fixture 中应用后恢复入口及安全初始焦点。
- 首轮目录 `/tmp/cognate-native-shell.B8Lw3t` 的 `desktop-tests.json` / `desktop-tests.stderr` 为 P2 修复前历史结果：**277 PASS / 1 SKIP / 0 FAIL**（含子场景），不作为修复后通过证据。
- `shell-tests.log`：新增外壳测试和原有截图测试通过。
- `go-vet.log`、`mygo-vet.log`：Go vet 与两个 MyGo vet 均通过。
- `build-{linux,windows,darwin}-{amd64,arm64}.log`：六个 CGO0 目标构建通过，仅 Linux amd64 二进制在此环境启动。
- P2 目录 `/tmp/cognate-native-shell-scroll.Lp5pOi` 的 `desktop-tests.json` / `desktop-tests.stderr` 为修复后最终结果：**295 PASS / 1 SKIP / 0 FAIL**（含子场景）；入口无测试文件。既有 `TestClientKeyEntrypointsCoreFaults` 环境门禁 SKIP 保持，不把它当作通过；该目录的 `go-vet.log`、`mygo-vet.log`、`build.log` 记录 Go vet、两个 MyGo vet、正常 Linux 入口 CGO0 构建通过。
- 冻结后的相同三个 Go 源重新完成 Linux / Windows / macOS × amd64 / arm64 六目标 CGO0 构建，记录为 P2 目录的 `build-{linux,windows,darwin}-{amd64,arm64}.log`。使用 `CGO_ENABLED=0 GOTOOLCHAIN=local GOOS=<目标系统> GOARCH=<目标架构> go build -mod=readonly -o <证据目录中的二进制> ./cmd/cognate-desktop`；构建通过不代表异平台运行通过。
- P2 目录 `final-source.sha256` 固定三个 Go 源文件；冻结后不再扩 UI 范围或改源，已直接通知 Godel 对同一 P2 做只读复核。本文档更新不阻塞其审阅。

## 独立代表复核

Godel 对冻结的三个 Go 文件完成只读复核，关闭上述 P2，本轮修复范围未发现新增阻塞；没有修改工作树或提交。

| Before | After | Why |
| --- | --- | --- |
| 短窗或 125% 字号下，停止同步被固定底栏遮挡 | 仅 Pack 内容增加成熟 MyGo 滚动区，限制列表高度；滚动与 Tab 可完整露出停止操作 | 保持原控件 keys、真实取消逻辑及底栏稳定，不为其他已滚动页面重复嵌套 |

- 原独立反例覆盖六种尺寸/字号 × 鼠标/键盘：错误快照 10 FAIL / 2 PASS，冻结修复后 12/12 PASS。点击或 Return 确实取消 watch context 并更新停止状态，而非仅检查文本存在。
- 新增永久控件回归独立重放：错误快照 16/16 FAIL，冻结修复后 16/16 PASS；最大订阅列表选择和 End 到末项通过。
- 独立 `^TestNativeShell` 定向回归 exit 0；原反例与负控 overlay 保存在 `/tmp/cognate-shell-readonly-review.uwdJj9`。主线程另外独立执行 `^TestNativeShellPackShortWindow` 通过，见 `/tmp/cognate-capture-main-review.l3G4b9V1/native-short-window.json`。

这些证据只证明本环境的 Linux 原生窗口、离屏布局、键盘入口与自动回归。不等于真人显示器验收、已登录完整 UI 人工验收、硬件 GPU、IME、多屏缩放、系统读屏器、Windows/macOS 运行验收或完整跨平台首发完成；完整首发 UI 目标仍按 `FORMAL-RELEASE-AUDIT.md` 验收。
