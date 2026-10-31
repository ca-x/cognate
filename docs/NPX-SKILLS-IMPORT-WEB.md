# npx skills 选择导入：Web 切片

## 入口与边界

在「仓库」页的现代「选择导入技能、仓库与配置」表单中选择
`npx skills lock v3 · 1 MiB`，授权选择 `.skill-lock.json`，然后点击「上传并预览」。
选文件本身不会上传；预览不收录仓库、不创建配置资产、不下载或安装技能。

- 仅接受非空的 v3 JSON lock，文件最大 1 MiB。
- 单批最多 128 个技能候选、16 个仓库。即使文件小于 1 MiB，超过候选上限仍以
  `ResourceExhausted` 整批拒绝，不截断为部分候选。使用较小的导出副本重新预览，
  不修改实际全局 lock，不自动重试。该错误也可能表示请求限流。
- 仅支持 GitHub 仓库引用。其它 provider、来源展示元数据与未知部分按服务端安全提示排除；
  不把这些数据变成配置，不迁移凭据，不自动应用到设备。
- lock 树哈希不是 Cognate 内容/归档哈希。技能引用必须显式绑定当前账号已收录的同仓技能，
  并确认「来源内容未比对」；不会根据名称、路径或 lock 哈希推断绑定。
- 缺失仓库可先单独选择收录，提交后重新预览，再显式绑定现有技能。仓库、技能与配置均默认不选，
  所有排除提示必须明确确认。

现代来源表独立维护 enum、文件类型、大小与预览预算：Magpie 为 5 秒、cc-switch 为 15 秒、
npx 为 5 秒。npx 使用 protobuf `NPX_SKILLS_LOCK = 3`，不会被二分来源判断送到 cc-switch。
旧版仅仓库流程继续只有 Magpie、cc-switch 两个来源。

## 私有生命周期与不确定结果

沿用现有 owner/session/origin 隔离、草稿放弃确认和 Radix 控件。
切换来源、语言、账号、会话、丢弃草稿、过期及卸载时沿用文件与私有字节清理；
原始 lock、文件名、票据与密钥不进入浏览器持久化存储或截图。

提交后客户端停止等待不代表撤销。结果进入 Unknown 时，禁止再次业务提交或更换来源，
只能显式查询同一 operation。`NOT_OBSERVED` 不证明没有提交。
服务端来源身份或 parser digest 更新后，旧预览需要重做；已有 operation 的 receipt 查询不变。

新增 npx 提示在本地 `npx-import-i18n.ts` 内提供中文、英文，不修改共享 locale、CSS 或 API context。

## 真实浏览器验证

夹具使用实际生产 Web 构建、TLS Connect handler、认证、SQLite、selection service 和
服务端 adapter。只把上游仓库归档替换为确定性的合成 tar.gz；不伪造成功的 preview、commit 或 status。
丢失回执场景先执行真实 commit 并检查 HTTP 200，再丢弃响应，以验证 Unknown 后的显式 status。
录制响应的 wrapper 通过 `Unwrap` 保留真实 transport deadline 能力。

前提：Node 26.12.0、仓库要求的 Go 工具链、已安装的 Web 依赖、agent-browser 和 Chrome。
`AGENT_BROWSER_BIN`、`CHROME_BIN` 应指向实际可执行文件，不依赖未配置版本的 shim。

```bash
EVIDENCE="$(mktemp -d /tmp/cognate-npx-web.XXXXXX)"
(cd web && npm run build)
CGO_ENABLED=0 go test -c -o "$EVIDENCE/fixture.test" ./internal/server/httpapi
CGO_ENABLED=0 node scripts/verify-npx-selection.mjs \
  --fixture-bin "$EVIDENCE/fixture.test" \
  --browser-bin "$AGENT_BROWSER_BIN" \
  --chrome-bin "$CHROME_BIN" \
  --output "$EVIDENCE/browser"
```

与其它切片串行执行 Web build，且 build 完成后保持 `web/dist` 不动，直到所有依赖 embed 的
Go 编译结束。fixture binary 编译完成后，浏览器读取的是 binary 内嵌的那次 Web 构建。
`--behavior-only` 仅供调试行为，不替代完整布局验收。

脚本检查：

- 真实文件选择、预览、默认不选、排除确认、仓库选择、确认框取消/焦点恢复、commit、status。
- 空文件、超过 1 MiB、无效 JSON、有效空 lock、其它 provider，以及小于 1 MiB 的
  129 技能与 17 仓库整批拒绝；不将错误冒充空结果或截断成功。
- 320/375 px × 浅/深色 × 中文/英文，200% 根字号、reduced-motion；检查正文与来源菜单几何、
  英文断词、键盘选择和 Escape 焦点恢复。仅验证此切片，不重跑无关 160 项矩阵。
- 丢失真实回执后只有一次 commit，一次显式 status；SQLite 中真实 owner 仓库存在，另一 owner 不可见。
- 已收录仓库不会隐式绑定技能；换账号及重新登录后不带入文件、预览、operation。
- 旧流程仍只有两个来源；截图前检查无密码输入、raw 私有 marker、密码、capability 或文件名。
- `receipts.json` 记录脚本 exit、fixture 终态、截图和完整覆盖；通过条件见下方严格终态门禁。
  私有 ready/input 文件位于权限受限的 fixture 子目录，日志不记录 raw。

## 严格终态与 CI 自测

`--self-test` 不需要 fixture/browser 参数、不启动浏览器、不访问上游，只依赖 Node 内置模块：

```bash
CGO_ENABLED=0 node scripts/verify-npx-selection.mjs --self-test
```

本机复验固定 Node 26.12.0、`CGO_ENABLED=0`；CI 沿用 `node-version: '26'`，
未精确锁定该 patch，新 Node-only 自测步骤也未另设 Go 环境。自测直接调用正式收尾使用的同一门禁，
包括 full/behavior 正控，以及真实 Node 子进程的非零退出、signal、抛异常、PASS 后输出 FAIL、
缺 PASS、忽略 SIGTERM 后被 SIGKILL 并 reap，以及 stop 超时但最终 exit 0 仍拒绝的反控。
另外检查缺失、重复、未知及零语义收据，缺失/矛盾的 Go PASS、FAIL/panic，布局和截图缺失/重复，
以及已有失败、未完成验证、未 reap、错误 mode 和超时不得被成功收据掩盖。

正式门禁要求全部同时成立：

- `verify()` 正常完成且没有行为或清理失败；不读取旧 `receipts.json` 作为成功依据。
- 固定 19 个唯一必需语义 labels，定义在 runner 的 `semanticLabels`；缺失、重复、未知 labels 均拒绝。
- full 模式额外恰好 16 个唯一矩阵截图 labels、16 个唯一布局记录，覆盖两宽度、两主题、两语言和
  preview/source-menu 两个表面；所有布局断言在收尾时再次检查。总计 35 labels、26 张 PNG、16 布局。
- behavior 模式恰好 19 labels、10 张语义 PNG、零布局，不可混入 full 模式矩阵。
  截图必须实际存在、非空且具有 PNG 签名；额外、缺失或重复截图均拒绝。
- fixture clean exit 0、signal null、无 spawn 错误且已 reap；不得发生 stop 失败、超时或强制终止。
- 现有 Go 日志中必须各有一次 fixture 的 `=== RUN`、`--- PASS: TestNPXSelectionBrowserFixture (...)`、
  独立 `PASS` 汇总行。只有汇总 PASS 或零测试不够；任何 FAIL/panic 都拒绝，即使退出码为 0。
- `/stop` 等待最多 5 秒，自然退出最多 10 秒，随后 SIGTERM 最多 2 秒、SIGKILL/reap 最多 2 秒。
  超时或被迫 kill 必定失败，即使后来 exit 0。未能观察到 close/reap 时标记 `reaped: false`，
  释放句柄仅用于防止 runner 挂死，不能把它当作已 reap 或成功。

仅全部条件成立才输出最终 PASS。失败或超时不允许被 receipt、截图数或 exit 0 单独覆盖。

## 写集与交付范围

本切片直接修改且只修改以下六个文件：

1. `web/src/import-repositories.tsx`
2. `web/src/import-selection-form.tsx`
3. `web/src/npx-import-i18n.ts`
4. `internal/server/httpapi/npx_selection_browser_fixture_test.go`
5. `scripts/verify-npx-selection.mjs`
6. `docs/NPX-SKILLS-IMPORT-WEB.md`

不修改 domain/state、protocol/generated、CLI、共享 locale/CSS/API context 或其它切片写集；
不新建分支、提交、推送或启动子 agent。

验收不覆盖真实公网 GitHub 可用性、全仓回归、所有客户端配置组合、实际全局 lock 写回、
自动安装、完整技能绑定提交矩阵、服务端全部畸形输入与限流矩阵。服务端及 CLI 的独立验证由其切片负责。

本机证据根目录：`/tmp/cognate-npx-web-evidence`。
首次真实 RED 为 `red-entry/receipts.json`，失败原因为现代来源菜单缺少 npx，fixture 正常退出。
最终组合的构建、binary 编译与浏览器证据记录于 `limits-build.log`、`limits-compile.log`、
`final-verified/receipts.json`；最终脚本 exit 0，35 项证据、16 个布局，fixture exit 0、signal null。
`limits-exits.json` 记录 Web build 与 fixture 编译均 exit 0。源码、dist 与 binary 摘要分别记录于
`source.sha256`、`dist.sha256`、`binary.sha256`，不把编译完成当作业务成功。

终态门禁修复证据独立保存在 `/tmp/cognate-npx-terminal-gate`，不覆盖上述原始证据。
`red-self-test.log`/`.exit` 记录旧 gate 对真实 clean/PASS 子进程的零语义收据未拒绝，回归自测 exit 1；
`green-self-test.log`/`.exit` 记录严格 gate 自测。`full/receipts.json`、`full/fixture.log` 与 `full.exit`
记录复用原 `limits-fixture.test` 的完整复验。本次增量只修改 runner 和本文档，不改另外四个冻结源码，
也不重建或清理 main 的 dist；exact2 delta 与四文件不变证明在同一证据目录归档。
本轮固定 Node 26.12.0、`CGO_ENABLED=0`：39 个终态自测 exit 0；full 复验 exit 0，35 labels、
26 张 PNG、16 布局；fixture exit 0、signal null、`reaped: true`，无 stop 失败、超时或强制终止。
`exact2.delta.patch`/`.sha256` 与 `two-after.sha256` 记录本次精确两文件增量和最终摘要；
`frozen-four-before.sha256`、`fixture-before.sha256` 用于复核冻结源码与复用 binary 未变。

## 独立验收 W1：来源弃稿焦点

原独立报告 `/tmp/cognate-npx-independent.pBpipKng/evidence/web/WEB-REVIEW.md`
保留 W1/P2 BLOCK：选择 npx 文件但未上传时改选 Magpie，弃稿弹窗取消或确认后焦点落到
`BODY`。这不是预览或提交失败，修复不得增加业务请求。

永久回归加入现有 `keyboard-source-picker-npx` 收据：取消保留文件、确认清理文件，两条路径
都返回稳定来源选择器，并验证 Preview/Commit 请求计数不变，不放宽原 19/35 项终态门禁。
主线程在旧构建真实复现 `canceling a source discard lost the stable source focus`；最早一轮
菜单准备失败另行保留，不作为产品 RED。生产只修来源控件与弃稿对话框的焦点交接：排除已卸载
或处于临时菜单内的返回目标，优先可用原控件，否则返回稳定来源选择器，最后回到稳定标题；
不增加 timer、全局监听、自动重试或提交。

主线程证据位于 `/tmp/cognate-npx-selection.TB9Vd2tL`：
`npx-discard-red-v2-156/receipts.json` 为旧构建实际 RED；
`npx-discard-green-143/receipts.json` 为 19 项行为收据，
`npx-discard-green-156/receipts.json` 为 35 项收据、16 项布局及 26 张截图。
两次 GREEN 的 fixture 均 exit 0、signal null、已回收且未强制终止。
独立增量验收已关闭 W1，限定 Spec/Quality ACCEPT；原反例、扩展焦点探针与独立完整
35 项/16 布局/26 图通过，fixture `0/null` 且已回收。报告
`/tmp/cognate-npx-independent.pBpipKng/evidence/web-v2/FINAL-REVIEW.md`，SHA256
`472c4067953b240975c198847e01c2a6612c7ff28a625c79db804ad4f64eeb37`。
主线另修正该独审指出的 CI Node patch 精度表述；只有本文档变化，不改变冻结生产字节。
这不表示完整 F6 或 GA 完成。
