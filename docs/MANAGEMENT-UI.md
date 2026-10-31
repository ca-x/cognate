# MCP Token 与管理员日志 UI（Task 4）

基线：`59da539509f6d7aa10c28f652b72c879157be63c`。实现日期：2026-10-31 至 2026-11-01（UTC）。
工作副本：`/tmp/cognate-task4-Mc6hikHi`，由 `git archive` 创建；未使用 worktree、分支、提交或其它 agent，未向主线写入源码。

范围对应 `REQUIREMENTS.md` §2/F10/F11/F13 及 `UI-DESIGN.md` 的 `/mcp-settings`、`/server-logging` 两行，不代表全产品首发或 GA。GUI 仍为 MyGo 原生；本片没有 agent MCP 配置资产、连接、设备应用或原生 GUI 能力。

## 恰八文件

修改：

- `web/src/mcp-settings.tsx`
- `web/src/mcp-settings-i18n.ts`
- `web/src/server-logging.tsx`
- `web/src/server-logging-i18n.ts`

新增：

- `web/src/management-ui.css`
- `internal/server/httpapi/management_ui_browser_fixture_test.go`
- `scripts/verify-management-ui.mjs`
- `docs/MANAGEMENT-UI.md`

不改共享路由、API、协议、CSS、locale 总表、依赖或生产 Go。复用 React / TanStack Query、既有 Radix Dialog / Checkbox / Select、Button / Input、Lucide 及原生 details。CSS 只针对本片页面、弹层及其内部布局。

## 行为与设计

| Before | After | Why |
| --- | --- | --- |
| 缓存查询失败后，旧凭据仍显示确定状态且可操作 | 标记上次读取、状态未核实，隐藏当前数量并禁用操作；成功读取才恢复 | 不把旧数据当作当前授权状态 |
| MCP 管理 token 与连接资产边界需阅读长说明 | 固定显示二者区别；权限与撤销说明可键盘展开 | 不暗示新增客户端配置能力 |
| 凭据名称和状态在大文字窄屏争夺一行 | 按文字尺度自动换行，权限勾选与说明必要时分行 | 保持完整英文单词及真实控件 |
| 撤销后触发器已禁用、异步错误重试节点消失 | 返回可用触发器，否则回稳定标题；不依赖 body 焦点 | 键盘继续有定位 |
| 日志读取失败保留草稿，但旧健康说明仍像当前正常 | 保留草稿与旧计数，明确其为上次确认样本；健康状态未核实 | 查询失败不是健康，也不是停用 |
| 草稿重读直接替换，收紧保留范围只有文案 | Radix 确认放弃草稿或允许清理；取消保持草稿 | 破坏性结果需明确选择 |
| 编辑、健康、隐私与文件预算层级接近 | 分离策略、真实修订、计算上限、健康样本与隐私说明 | 不虚构实时用量或统计 |

动效沿用已审共享 tokens：按压反馈 150ms，偶发弹层空间定位 200ms，`transform` / `opacity` 与 `--ease-out`。退出即时完成，不等待动画再释放焦点；键盘与 ReduceMotion 禁用位移。没有新动效库、循环装饰、假进度或输入延迟。这里使用的是 Press / Tap feedback 与 Enter / Exit，不是弹簧或装饰性漂浮。

## 安全和协议

- token 原文只在成功创建或轮换后的组件内存中出现，不进 Query 缓存、列表、浏览器持久存储、截图、报告或原文日志；关闭、离页、刷新或更换账号不会重新显示。隐藏后的提示不声称用户已经保存。
- 真实权限仍由服务端逐调用检查。读写分开，分类权限分开，发布需要 `publication:write`；`catalog:write` 不授予发布。管理 token 与会话、GitHub、分享 token、agent MCP 连接资产相互独立。
- CAS、归属、到期、撤销和请求取消语义保持。Unknown / 丢回执 / 冲突不自动重放；查询失败不丢草稿、不虚构保存成功。
- 日志只有部署者显式指定的管理员可改。没有日志路径输入。收紧限制可能已清理旧日志，即使后续发布失败也不承诺恢复。
- 日志健康是响应进程的最后确认样本，不是持续监控或无损审计保证。计数缺失或读取失败不补零、不显示健康。
- 截图函数遇到一次显示区或非空密码框立即拒绝；失败诊断也遵守此门禁。只用 fixture 合成账号；不会读取真实账号或配置。

## 真实验收接缝

生产 Vite build 由 `web.Handler()` 嵌入同一 Go test binary，经真实回环 TLS、Connect、认证、文件 SQLite 的 MCP 领域层及真实文件日志 manager。日志策略遵循现有文件持久化实现，并非声称其存于 SQLite。

fixture 的私有控制端使用独立随机 capability、0700 目录 / 0600 readiness，并拒绝 Origin、query 和无 capability 请求。readiness 不含会话或 MCP token。

- `slow` 只延迟真实请求，不制造成功响应；`error` 是固定 Unavailable。
- `drop` 先执行真实 handler 并确认服务端已成功，再丢掉响应，返回固定 Unknown；原文仅留在瞬时内存。
- 日志 CAS 冲突先通过真实 manager 更新修订；token CAS 冲突先通过真实所有者领域操作推进修订，随后旧 UI 请求真实失败。
- degraded 样本来自真实 logger 拒绝无效事件后的丢弃计数；关闭 manager 后的不可用也是实际失败。不将其冒充真实磁盘故障。
- 测试专用 `/_fixture/authorize` 只把真实 `mcpaccess.Authorize` 的结果转成状态码，用于内存 token 的逐次权限、轮换和撤销验证。这不是官方 MCP 客户端或外部 AI 集成验收；既有 MCP 管理/日志授权专项另跑。
- 剪贴板拒绝只注入失败，不模拟复制成功，不把真实 token 写进 OS 剪贴板。成功复制到外部系统剪贴板及其历史保留仍需真人验收。

## RED → GREEN 与证据

证据根目录：`/tmp/cognate-task4-Mc6hikHi-evidence`。RED 与后继成功轮独立保留，不覆盖旧失败。

- `red-token-v2/receipts.json`：基线缓存读取失败后操作仍可用，确定失败 `false !== true`，fixture 正常停止。
- `red-log/receipts.json`：基线日志重读失败后仍声称健康，确定失败 `true !== false`，草稿实际保留。
- `red-whole-words/receipts.json`：200% 根文字下名称/权限布局断开英文词；几何无横溢仍不足以通过。
- `matrix-v3/receipts.json`：160 个页面矩阵组合通过，但后加的逐词 Range 检查暴露额外问题；不把旧绿色当最终签收。
- `word-diagnostic/receipts.json` 与 `frozen/receipts.json`：定位普通文案中的权限标识与嵌套提示区长词。权限标识改用成熟 Trans 的语义 code 节点；保留警告改成不失原意的短句，不以缩小文字规避 200%。标识符/用户不间断 ID 可按字符换行，普通英文单词不可断裂。
- `release/receipts.json`：最终完整通过，`exit=0`、fixture `code=0 / signal=null`，179 条收据，其中 160 个不重复矩阵组合、14 张无秘密截图。包括真实延迟/错误、生命周期、只读拒写、轮换旧 token 失效、撤销、过期、CAS、已提交丢回执、草稿、焦点、管理员隔离与持久存储不含 token。
- `green-token/receipts.json`、`green-log/receipts.json`：原两个针对性 RED 在最终 production binary 上均 `exit=0`。断词 RED 由完整 160 组 Range 逐词检查覆盖为 GREEN。
- `release-build.log`：最终 `tsc --noEmit` 与生产 Vite build 通过；`release-compile.log`：同一 build 后编译真实 fixture 通过。
- `release-targeted-go.log`：7 项 MCP 管理、日志授权/CAS/边界和新增 fixture 专项通过；`release-logging.log`：真实 logging 包专项通过。
- `release-self-tests/terminal-self-tests.json`：正常及六个异常终止契约 7/7 通过。最终脚本另经 Node 语法检查。

代表截图（均位于 `release/`）：`mcp-empty-dark-en.png`、`permissions-{en,zh-CN}-dark-320-200.png`、`mcp-settings-{en,zh-CN}-{light,dark}-375-200.png`、`server-logging-{en,zh-CN}-{light,dark}-375-200.png`、`logging-degraded-dark-en.png`、`logging-health-unavailable.png`、`logging-member-restricted.png`。截图前已隐藏一次显示区；不提供任何 token 原文截图。旧几何轮在 200% 重排后曾未完成指针路由导航，具体原因未独立确认，保留失败而不冒称服务端错误；最终路由使用真实键盘 Enter，页面主要操作仍有真实指针及键盘验证，不据此声称共享导航的全部指针场景通过。

矩阵：中文 / English × 浅 / 深 / 跟随系统（实际切换系统浅深）× 正常 / ReduceMotion × 1440px/100%、375px/100%、320px/100%、375px/200%、320px/200% × 两个页面，共 160 个组合。200% 用真实根字号，不用 deviceScaleFactor 或 transform 代替。检查页面、权限表单和确认弹层的几何与完整英文词；通过真实 Tab / Space / Enter / Escape 激活和取消，检查可见焦点及稳定恢复。MCP 模态框遵守 Radix 焦点隔离；系统主题和宽度变化保留其草稿，非模态日志表单可通过真实偏好控件切换语言/主题并保留草稿。

终止契约的持久自测在同一脚本：正常、非零退出、signal、缺 stop 收据、缺 PASS 收据、收据后 FAIL、超时。成功必须同时满足 `code=0`、无 signal、明确 stop 及 Go PASS 收据且无 FAIL/panic。异常一律非零退出，包括针对性 RED 模式的早返回路径；不会因浏览器检查完成而把 fixture 失败当成功。

## 最小复现

不安装依赖；隔离副本可链接现有 `web/node_modules`。从本片冻结源码根目录运行：

```bash
WORK=$(mktemp -d /tmp/cognate-management-source-XXXXXXXX)
git -C /home/czyt/code/go/skillhub archive 59da539509f6d7aa10c28f652b72c879157be63c | tar -x -C "$WORK"
tar -xzf /tmp/cognate-task4-Mc6hikHi-delivery/task4-management-ui-source.tar.gz -C "$WORK"
ln -s /home/czyt/code/go/skillhub/web/node_modules "$WORK/web/node_modules"
cd "$WORK"
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
export CGO_ENABLED=0
OUT=$(mktemp -d /tmp/cognate-management-repro-XXXXXXXX)
node scripts/verify-management-ui.mjs --self-test --output "$OUT"
npm --prefix web run build
go test ./internal/server/httpapi -run '^(TestMCPManagement|TestLogManagement|TestManagementUIFixture)' -count=1
go test ./internal/server/logging -count=1
go test -c ./internal/server/httpapi -o "$OUT/management.test"
node scripts/verify-management-ui.mjs \
  --fixture-bin "$OUT/management.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --output "$OUT/browser"
```

脚本显式要求 Node 26.12.0。Go 全程 1.27.3、CGO0，未运行 race/cgo、全仓或全平台构建。只复现业务可加 `--skip-matrix`；`--red token-error` / `--red log-health` 是持久针对性验收，不等同于完整矩阵。

## 交付边界

作者自检，不是非作者独审、主线程组合验收或 GA。未覆盖真实账号、外部 AI/MCP 客户端、真人读屏/触屏、其它浏览器、其它操作系统及 MyGo 原生；不据本页证据替其它账本行签收。不改主线程的共享账本，待主线程接收冻结八文件并独审后决定集成。

冻结交付包含累计 patch、恰八文件源码 tar、逐文件 SHA256、证据 tar 及外部交付摘要；无 node_modules、fixture 数据库、readiness capability、会话或 token 原文。

## 作者收尾记录

1. 读取全局约束、Task 4、需求与逐页账本、指定 skills；定向 Nowledge thread 检索无匹配，不借用其它切片结论。
2. 从固定提交创建 archive，链接既有依赖；先记录真实基线和两项确定 RED。
3. 在八文件内修正缓存/状态/草稿/焦点，分离权限、日志策略与健康样本；不修改授权或生产协议。
4. 逐步加入异常收尾自测、真实业务、矩阵与整词 Range 门禁；保留所有 RED，修复后重跑相关门禁。
5. 最终生产 build → fixture、完整浏览器、两个 RED 专项 GREEN、7 项 Go 专项、logging 包及 7 项终止自测通过；主线四个同名已有文件与基线逐文件 SHA 相同，HEAD 仍为原基线。
6. 单独作者检查八文件边界、错误码文案、CAS / Unknown 不重放及秘密门禁；冻结 patch、tar 和摘要。作者自检不代替非作者审查，没有创建 reviewer 或签署 GA。

## 主线程集成与 F1 闭合（2026-11-01）

主线程在 `e200385` 上集成并核验八文件原始摘要。真实生产浏览器先通过 179 条收据/160 矩阵，两个相关 Go 包为 1223 PASS / 10 SKIP / 0 FAIL，vet、生产 Web/fixture/server CGO0 build 通过；这些绿色没有消除独审发现的 F1。

首次 Hilbert 独审仅阻断成功撤销后的焦点：Chromium 143 禁用旧按钮时先 blur 到 BODY，156 则仍能由原 layout effect 接住。主线程先在持久脚本加入取消零写入/回原触发器及慢列表成功后 H1/恰一次写入断言，旧生产 binary 在 143 得到精确 RED；生产仅在明确 `revokedNotice` 时清空旧返回触发器，沿用 Radix 关闭回 H1，不再依赖 disabled 的浏览器时序。没有新增 timer、全局焦点监听、重放或秘密处理变动。

修后主线 143 专项 10 条通过，156 完整 **180 条收据、160 组矩阵、14 张安全截图**通过，fixture 均 0/null；Web/fixture/server build、七案终态 self-test 和源码/53 项 dist 核验再次通过。非作者独立构建并以同一加强探针在旧143取得仅三项 F1 RED，修后143/156各 12/12 PASS，另验证取消/普通关闭、后台不抢焦点和撤销精确一次写入，结论 **F1 CLOSED、限定 Spec/Quality ACCEPT**。原 BLOCK、版本差异、旧指针导航未知和所有未验边界保留。

复审报告 `/tmp/cognate-task4-independent.MGt1w9CC/work/REVIEW-F1.md`，SHA256 `3702e502bcdfae16567d5aea5d9627a4d301dd77a46e65f45b07cb15be7adf80`。新增 Build 单行七案 self-test 接线另获限定 ACCEPT；它不是远端完整浏览器测试。实际命令、收据和增量身份见 `VERIFICATION.md`，不扩签 GA。
