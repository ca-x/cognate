# Web 首发视觉与动效阶段记录

日期：2026-10-31。范围仅为 Web 共享设计与关键配置页，不代表全产品首发功能验收完成。

## 设计约束

精密控制台：冷色强调仅用于操作与定位，深浅主题均使用分层中性材质、细边界、明确文字层级。保留原有 logo、React/Tailwind、Radix 与 TanStack，不添加依赖、字体下载、虚构统计或持续装饰动画。

| Before | After | 原因 |
| --- | --- | --- |
| 深色面板及边框较亮，层级接近 | 收敛背景、侧栏、面板和边界，统一阴影 tokens | 阅读内容优先于面板轮廓 |
| 导航主要靠轻微背景变化表示选中 | 文字、图标、细边框和左侧定位线共同表示选中 | 不只依赖颜色识别当前位置 |
| 顶部整块研发切片和缺口说明 | 紧凑编辑能力摘要 + 原生 `details/summary` | 把任务操作放在完整研发边界之前 |
| 保存结果与正文混在一起 | 独立中性反馈面板，空状态不占额外行高 | 清楚区分已保存与已应用，保持 live region |
| 资产元数据桌面两列、操作同权重 | 宽屏四列元数据、分隔后的操作区、删除与编辑分开 | 提高扫描效率及操作辨识度 |
| 空状态缺乏视觉锚点 | 复用 Lucide 图标、统一说明宽度与材质 | 提供清楚且不虚构数据的下一步语境 |
| 移动端重复工作空间上下文占一行 | 375px 隐去冗余上下文，保留全部导航、偏好及账号操作 | 给实际任务腾出版面 |

页面标题下仍直接显示“保存不选择 provider，不修改设备文件，也不表示已经应用”。元数据列表仍直观说明已保存凭据不会返回原文；完整支持范围及缺口可以鼠标、触摸或键盘展开。新增文案同时提供中文与 English。

## 动效决策

- 目的：进入时定位新内容、按压反馈、弹层建立空间关系；不是装饰。
- CSS `transform` + `opacity`；复用 `--ease-out: cubic-bezier(0.23, 1, 0.32, 1)`。
- 按压 150ms、页面及对话框进入 200ms、Select 进入 180ms；不延迟输入、数据或焦点。
- 关闭即时完成：实测 Radix Presence 退出动画会短暂保留模态阻挡，影响紧随其后的真实点击，因此不保留退出动画。
- `prefers-reduced-motion: reduce` 禁用这些动画；hover 样式仅在 `hover: hover` 且 `pointer: fine` 下启用。
- 输入模式按实际键盘/指针事件记录，面板挂载时锁定动效偏好；键盘输入取消当前动效，不会在下一次鼠标点击时重播。原有转发 ref、Radix 焦点管理和 Escape 语义保持。

## 实图证据

本机证据根目录：`/tmp/cognate-ui-polish`。截图只含隔离 fixture 的合成账号和非秘密配置元数据；没有展示真实账号凭据或新输入的 API key。图片未作为二进制资产加入仓库。

| 场景 | Before | After |
| --- | --- | --- |
| 深色空配置页 / 中文 / 1440 | `before-dark-ui/configuration-empty-dark-zh-1440.png` | `release-dark/configuration-empty-dark-zh-1440.png` |
| 深色配置列表 / 中文 / 1440 | `before-dark-ui/configuration-dark-zh-1440.png` | `release-dark/configuration-dark-zh-1440.png` |
| 浅色空配置页 / 中文 / 1440 | `before-light-ui/configuration-empty-light-zh-1440.png` | `release-light/configuration-empty-light-zh-1440.png` |
| 浅色配置列表 / 中文 / 1440 | `before-light-ui/configuration-light-zh-1440.png` | `release-light/configuration-light-zh-1440.png` |
| 深色编辑器 / English / 375 | `before-dark-ui/grok-dark-en-375.png` | `release-dark/grok-dark-en-375.png` |
| 浅色编辑器 / English / 375 | `before-light-ui/grok-light-en-375.png` | `release-light/grok-light-en-375.png` |

附加实图：`release-{dark,light}/configuration-scope-*-zh-1440.png`（完整边界展开）、`configuration-*-en-1440.png`、`configuration-empty-*-zh-375.png`、`sources-*-zh-1440.png`。最早的 `before-dark-ui/sources-dark-zh-1440.png` 抓到了懒加载空档，不作为来源页有效对比。最终来源截图等待页面挂载，展示真实错误态；此配置 fixture 不提供 GitHub 成功链路，不能据此声称 GitHub 同步已验证。

## 验证矩阵与命令

- `npm --prefix web run build`：最终通过，含 `tsc --noEmit`；见 `final-build.log`。
- `node scripts/verify-grok-web-state.mjs`：通过；见 `final-state.log`。
- `CGO_ENABLED=0 go test -c ./internal/server/httpapi -o /tmp/cognate-ui-polish/final.test`：仅编译真实 TLS + SQLite 浏览器 fixture；见 `final-compile.log`。未运行全仓 Go、race 或 cgo。
- `/tmp/cognate-ui-polish/verify-ui.mjs`：从仓库 `scripts/verify-grok-web.mjs` 复制，补充截图、真实主题断言及 UI 矩阵；没有改仓库验收脚本。
- `release-dark/receipts.json`、`release-light/receipts.json` 均为 `exit: 0`，各 11 条证据：原生/自定义模型创建、加密凭据保存、无凭据复制、环境声明及 replace/clear、语言切换清除新凭据、375px 键盘焦点、CAS 冲突、未知字段只读、损坏回执与提交后丢回复禁止重放，另含能力展开、指针/键盘/reduced-motion 和 375/768/1024 无横向溢出检查。
- 保留上游原脚本浅色复验：`original-light/receipts.json` 为 `exit: 0`（9 项原始证据）。

复现 UI 矩阵（仓库根目录）：

```bash
export PATH=/home/czyt/.local/share/mise/installs/node/26.12.0/bin:/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
node /tmp/cognate-ui-polish/verify-ui.mjs \
  --fixture-bin /tmp/cognate-ui-polish/final.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --output /tmp/cognate-ui-polish/review-dark --theme dark
```

将 `--theme` 换成 `light`、使用独立 output 可复验浅色。fixture 正常停止及零副作用检查由原脚本负责，私有 readiness 和控制凭据仅在权限受限的 fixture 目录内。

## 真实失败与修复记录

1. 环境：mise 的 Go shim 报 `No version is set for shim: go`；改用已安装的 `go/1.27.3/bin`，Node 与 agent-browser 也使用安装绝对路径。没有改机器配置。错误可见本次终端工具输出；初次 `before-compile.log` 已被同名成功重编译日志覆盖，不将该文件冒充原始失败证据。
2. 基线原脚本深色失败：断言查询 `.dark` class，但生产实现始终使用 `document.documentElement.dataset.theme`。`before-dark.log`、`before-dark/receipts.json` 保留失败；只在临时 UI harness 修正，未为测试添加无意义主题 class。
3. 首轮新动效失败：全局输入模式切换会让已挂载面板动画从禁用切回启用，可能重播。改为 `useMotionRef` 挂载快照，键盘可取消但指针不重启同一面板。
4. 随后真实浏览器仍复现点击被遮挡：Radix Select/对话框关闭动画的 Presence 保留了模态层；快速选择目标后，下一次保存/选项点击命中遮罩而非控件。`after-dark-ui.log`、`after-light-ui.log`、`debug-dark-ui.log`、`debug2-dark-ui.log`、`debug3-dark-ui.log`、`final-dark-ui.log` 及相关 `failure.png` 保留。去掉退出动画而保留进入及反馈，完整快速交互恢复通过；未用 force-click、额外等待来绕过失败。

`checked-dark-ui` / `checked-light-ui` 是去除退出阻挡后的中间成功证据；`release-dark` / `release-light` 才是最终样式、状态与响应式调整后的证据。最终源文件及 fixture 二进制摘要见 `final-sha256.txt`。仍需主线程独立视觉/功能 review；这里不代替其它页面、设备应用或完整首发验收。

## 主线程与独立复核

2026-10-31：主线程查看修改前后真实截图，确认配置首屏将研发说明收为可展开边界、导航选中态和空状态层级改进，并核看浅色 English 375px 编辑弹层。独立只读 reviewer 对共享布局、ref/焦点接线、主题/语言不额外重挂载、键盘取消与 reduced-motion 检查未发现阻断问题；另外独立运行 typecheck/build、Grok state、脚本语法与空白检查通过。

主线程重新执行 Web build、Grok state、来源导入 state，并使用当前构建编译真实 TLS/SQLite fixture，分别运行仓库原验收脚本的 light/dark 两套流程，均通过。证据目录 `/tmp/cognate-ui-native-combined.xhw6k5op`：`web-build.log`、`web-state.log`、`import-state.log`、`browser-light/receipts.json`、`browser-dark/receipts.json`。仓库脚本现已修正历史 `.dark` class 误断言，同时检查 `dataset.theme` 和 computed `colorScheme`，不向生产 UI 添加测试 class。

这关闭本次共享设计及关键配置页切片，不关闭其它页面、完整原生 UI、所有平台或完整首发视觉门禁；没有把截图当作动效本身的运行证明，也没有将合成 fixture 当作真实 GitHub/OIDC 部署。

## 首发逐页验收账本

以下按真实 `main.tsx` 注册入口列出后继工作，避免把“共享组件已打磨”当成所有页面已验收。已通过的合成测试不替代真实账号、外部 provider 或跨平台真人测试。

| 入口 | 已有本轮证据 | 后继 UI 门禁 |
| --- | --- | --- |
| 未登录工作空间 / `AuthScreen` | 七文件切片及 I1/M1 修复已独审 ACCEPT；新组合本地注册/登录与 OIDC loading/error/取消、32 组布局和 49 安全图通过 | 本页局部 UI 签收不覆盖外部 OIDC 成功或客户端 device flow；见 `AUTH-UI.md` |
| `/` GitHub 来源 | 517 六阶段与 N1 可取消准入修复均获限定独审 ACCEPT；新组合 395 条浏览器检查、121 组几何、140 安全图通过 | Lists/授权局部证据不覆盖真实 GitHub 外部授权；锁准入精确 BUSY 轮询不等于业务自动重试，见 `SOURCES-UI.md` |
| `/repositories` 仓库/技能 | 仓库/分类七文件切片及 F1/F2 修复独审 ACCEPT；新组合 65 条证据、24 组布局通过 | 真实私有 GitHub 权限、其它浏览器/平台仍需各自证据；见 `REPOSITORIES-UI.md` |
| `/packs` 与内嵌分享管理 | 七文件切片与 F1 修复均已独审 ACCEPT；最终组合 CRUD/分享/撤销/Unknown 132 条证据、另五项焦点观察通过 | 配置附件消费另缺实现，不只是样式；本页不表示设备已同步，见 `PACKS-UI.md` |
| `/categories` 分类 | 同一仓库/分类切片已覆盖 CRUD、长名、失败保留草稿及丢回执后稳定错误/焦点，独审 ACCEPT | 以上为本页局部 Chromium UI 签收，不扩大成全部 F9 或真实平台验收 |
| `/account` 账号 | 与登录同一切片，I1/M1 已独审；身份、显式绑定、后台错误焦点与注销的新组合专项通过 | 外部 provider 成功绑定、真人读屏及跨浏览器仍 OPEN；不枚举或推断已有绑定清单 |
| `/mcp-settings` | 八文件管理页切片及撤销焦点 F1 已独审；主线真实权限/一次显示/轮换/撤销/过期、CAS/Unknown、160 组联合矩阵通过 | 本页局部签收不覆盖外部剪贴板成功复制、其它浏览器/真人读屏或客户端 MCP 配置资产；见 `MANAGEMENT-UI.md` |
| `/configuration` | 共享与关键配置/Grok 编辑切片已独审，见本文件前文 | 所有已支持 agent 的空/列表/编辑/只读未知/冲突/Unknown 场景逐一覆盖 OPEN；十四 agent 功能缺口不以 UI 填空代替 |
| `/server-logging` | 同管理切片已独审；真实普通用户拒绝、管理员策略/保留确认、草稿与 CAS/Unknown、健康样本未知和窄屏键盘通过 | 不代表持续监控或完整外部磁盘故障资格；全产品与平台门禁仍 OPEN，见 `MANAGEMENT-UI.md` |
| `/server-backup` | 管理员手动页，真实 TLS App/SQLite/S3-like 88 条收据及 56 组布局/焦点检查 | 中英深浅、320/375px/200%、真实键盘滚动与 ReduceMotion 已有本页证据；全产品、外部供应商及完整恢复仍 OPEN，见 `SERVER-BACKUP.md` |
| `/explore`、`/explore/$skillId` | 公开目录/详情局部 ACCEPT；24 份 200% 几何、复制完整词、真实分页/错误/匿名边界，见 `PUBLIC-CATALOG-UI.md` | 自动化验收限本机 Chromium；真人读屏、触屏、真实公开安装与其它浏览器仍 OPEN |

各后继切片统一验收：深浅/跟随系统、中英、不丢未提交草稿、375px 与 320px/200% 文字、真实 loading/empty/error、键盘与焦点恢复、ReduceMotion。动效只服务定位、直接反馈与状态关系，不加持续背景装饰、假统计、假进度或输入延迟。破坏性与含密钥动作保持现有授权/确认语义；没有实现的能力明确标记不可用，不用动画或按钮假装完成。

MyGo 原生验收仍单独跟踪 `NATIVE-UI-DESIGN.md`，不能用 Web 页面矩阵代替原生窗口、安装、IME 或其它平台证据。此账本记录剩余目标，不声明新增代码或页面已经验收。

## 手动备份页后继

2026-10-31：继续使用 React/TanStack 的既有路由、查询和会话；Button 为项目现有封装，确认/勾选/焦点陷阱与恢复来自 Radix。长内容初始焦点和确认后稳定目标仅通过 Radix 公开事件定制，不实现自己的 trap、portal、modal 或 Checkbox。

| Before | After | 原因 |
| --- | --- | --- |
| 只有六个管理员 RPC，没有产品入口 | 独立手动任务区与部署/恢复边界，真实空、拒绝和错误状态 | 不让实现细节挤占主要操作，也不伪造成功或进度 |
| 后续页错误时返回按钮消失 | 分页独立于成功非空分支，非法/失败/空页均可回较新页 | fail-closed 不应切断已知安全的本地导航 |
| 异步详情加载把原自动焦点推离视口；确认后触发器可能消失 | 稳定标题初始焦点与确认后标题落点；真实 Tab/Space/Enter 揭示并激活动作 | 200% 文字不仅检查横向几何，还必须能滚到并完整显示实际控件 |

实图目录 `/tmp/cognate-backup-web-browser-v8`：深色 English 1440px 空/成功、中英深浅 320/375px/200% 的历史/确认/详情/键盘揭示、普通账号拒绝和真实 disabled App。主线程查看 `light-en-320-200pct-confirm-revealed.png`，确认完整按钮及焦点环在可见区域内；v5 `failure.png` 保留详情自动焦点在屏外的 RED。复用共享按压/入场 tokens；频繁刷新数据不增加位移，键盘和 ReduceMotion 没有入场动画。

来源页另有六文件交付，但独立完整轮和无 UI 的 RPC-only 复现重新授权 Internal，且原验收脚本存在漏计其它阶段 Internal 的假绿风险；目前仍 BLOCK、未合入，不将该页 121 组零越界或来源页截图视作签收。

后继来源候选见 `SOURCES-UI.md`：累计十一文件已接入 `59da539` 的主线组合，新增六阶段 SQLite 517 的确定性 RED/GREEN、读前写锁及全阶段失败账本。新的真实浏览器 395 条检查和 121 组几何/整词检查通过，但作者冻结和主线程组合绿不能自行撤销原独审 BLOCK。真实入口为 `/`；作者历史报告中的 `/sources` 是页面称呼，不是新增路由。

用户随后要求加速并行，登录/账号、仓库/分类、Pack/分享已拆为互不重叠的实现写集，分别独立 CSS/i18n 注册且沿用既有框架组件；主线程统一组合验收及阶段提交，不让每条实现线重复全仓测试。三线在收到冻结工件与独审前都保持 OPEN，见 `superpowers/plans/2026-10-31-parallel-workspace-ui.md`。
