# GitHub Sources UI：有限切片

日期：2026-10-31。基线由交付根目录 `BASE` 记录；使用 `git archive HEAD` 的私有副本，不含主线未提交的备份候选。

> 历史说明：前半保留原六文件交付记录，不作为当前无条件通过声明。Hilbert 已指出 v1 `verified` 的 exit 0 漏掉 GetCatalog Internal；用户随后授权最小来源持久化修复。当前修复、RED/GREEN、扩展写集和复现命令见文末“Hilbert BLOCK 修复追加报告”。原独立 BLOCK 不由作者自行撤销。

## 编码前确定的范围与验收计划

沿 `docs/UI-DESIGN.md` 的精密控制台方向：系统字体、分层中性材质、冷色操作强调、细边界。只改善 `/sources` 标题、连接状态、授权入口、Lists 扫描/选择与反馈。复用 React、TanStack Query、Radix Checkbox、既有 Button/Input/DeviceFlow、Lucide；不新增依赖或自绘控件。

固定写集：`web/src/sources.tsx`、`web/src/sources.css`、`web/src/locales/en.ts`、`internal/server/httpapi/sources_browser_fixture_test.go`、`scripts/verify-sources-web.mjs`、本文件。Go 文件仅测试 fixture。生产 Go/API、权限、请求次序、凭据生命周期、DeviceFlow 实现、共享布局和主题均不改。`DiscoveredRepositories` 保持原样。

| Before | After（验收目标） | 原因 |
| --- | --- | --- |
| 服务名、连接提示、授权细节权重接近 | 标题 → 服务连接状态 → 授权入口；只展示 API 已提供的身份 | 不虚构 GitHub login/avatar；明确当前状态 |
| 列表名、slug、订阅状态散落 | 整行可选、名称优先、slug 次级、已选明确 | 长名称可折行，保留真实 Checkbox 语义 |
| 同步与订阅共享模糊忙提示 | 分开表达同步、保存订阅与完成结果 | 不制作百分比、进度或乐观成功 |
| 查询失败可能仍呈现缓存 Lists | 错误/未知与空列表严格分离 | 不把未知当空，过期继续隐藏旧内容 |

执行次序：先构建真实 Go TLS + SQLite + Connect fixture 与浏览器验收脚本，采集基线截图/预期失败；再做 UI/CSS/英语文案；生产 build、fixture 测试、浏览器交互及截图全部复验。使用 Go 1.27.3，所有 Go 命令显式 `CGO_ENABLED=0`，禁止 race/cgo。已加载 use-modern-go 完整 1.27 指南；新 JSON 使用 encoding/json/v2。

验收矩阵：连接前/后、真实 token password 输入与清空、服务端验证/加密/不回读、generation 冲突、Device flow 发起/取消、首次 loading、查询 error/后台 error、过期隐藏、空与长 Lists、筛选无匹配、订阅→同步及失败、手动同步；中英 × 深浅 × 1440/375/320px、200% 文字、键盘 Tab/Space、ReduceMotion。成功与空状态仅由真实 API 返回驱动；合成 upstream 不访问 GitHub，不使用真实凭据。控制端口只绑定 loopback、随机 capability、不记录 token/headers。

动效 gate：频繁 Lists 选择/筛选及键盘操作不增加入场、位移或延迟；复用既有按钮按压反馈，hover 只在 fine pointer；新增行反馈只做 120ms 背景/边界色 transition，可中断，ReduceMotion 关闭。无弹簧/连续装饰/虚构进度，不引入动效库。

已加载 ui-ux-pro-max、emil-design-eng、apple-design、animate、animation-vocabulary、ub-mode；ub-mode 的提交/推送及子代理建议被本次明确边界覆盖。Nowledge thread 定向检索 `Cognate sources UI DESIGN GitHub Lists` 无命中；设计依据为仓库文档。

## 基线与交付身份

- 精确基线：`c120214210c8fb790c84b9c1d30217dc72e74849`，初始 `git archive HEAD`。没有混入主线未提交备份候选，也没有改成后来推送的 `1fb8ee6`。
- 私有工作副本：`/tmp/cognate-sources-xzlFPw/work`；纯基线对照：同根目录 `before-work`，仅附加相同 fixture/脚本以运行原 UI。
- 此仓库 SourcesPage 的实际注册路由是 `/`，不是 `/sources`。沿用现有路由，不在固定写集之外修改 `main.tsx`。
- `delivery/sources-ui.patch` 和 `delivery/sources-ui.tar.gz` 只含上述六文件；`delivery/SHA256SUMS` 为逐文件校验，`delivery/ARTIFACT-SHA256SUMS` 标识交付包及测试二进制。证据另包，不包含 node_modules、测试账号 readiness 文件、数据库或浏览器 profile。
- 构建生成的 `web/dist` 仅留在私有副本供真实 embed 使用，不进入六文件源补丁。node_modules 只读复用主线 symlink；Go cache/modcache 复用本机缓存。

## 实现范围

| Before | After | 原因 |
| --- | --- | --- |
| “你的 skill 来源”、连接状态弱分隔 | “GitHub 来源”、服务卡、状态徽标、真实 viewer ID | 不把 opaque ID 冒充 GitHub handle，不造头像或身份 |
| 列表选择区和状态分散 | 复用 Radix Checkbox，原生 label 覆盖整行；选中材质和文字同时提示 | 不实现自有 checkbox、焦点管理或控件库 |
| 长名称/200% 文字挤出容器或逐字断行 | 列表可折行，服务头按可用空间自然换行 | 不截断名称、slug、错误或操作文字 |
| 失败查询仍呈现缓存列表、缺失 catalog 当作未连接 | 查询错误隐藏旧 Lists；缺失 catalog 走现有错误/重试 | 不把未知当空，不发额外请求 |
| 同步失败后，服务端隐藏的观察结果显示“还没有列表” | 暂时无法确认、真实错误、重试同步；不展示 0/0 伪空计数 | 不掩盖失败，不声称未完成的同步成功 |
| 一个忙提示混合两种操作 | 实际 mutation 的同步/保存订阅状态，原有成功回执 | 无百分比、假进度、乐观订阅或额外延迟 |

TokenForm 的 password 输入、提交即清空、服务端验证、generation CAS、服务端加密及不回读保持原样。DeviceFlow 模块未修改。`connected → invalidate + sync`、`set subscription → sync → invalidate`、30 秒 catalog 轮询、2 秒 device 状态轮询及过期定时器均保留。`DiscoveredRepositories` JSX 未修改；共享 useCatalog 只增加缺失 catalog 的错误判定。

动效仅增加行材质的 120ms background-color transition，复用 `--ease-out`；键盘与 ReduceMotion 下关闭，hover 限 fine pointer。按钮按压、Select、焦点、Escape 等均来自已有共享组件/样式；没有手写 focus、modal、Select、Checkbox 或动画库。

## 复现命令

本机为 Linux，Go 1.27.3、Node 26.12.0、agent-browser 0.39.1、Chrome for Testing 143.0.7499.4。以下命令在私有副本执行。环境变量必须覆盖不可用的默认 Go/agent-browser shim。

```bash
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
ROOT=/tmp/cognate-sources-xzlFPw
cd "$ROOT/work"
CGO_ENABLED=0 go version
CGO_ENABLED=0 sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
npm --prefix web run build > "$ROOT/artifacts/final-build.log" 2>&1
CGO_ENABLED=0 go test ./internal/server/httpapi -run 'Test(SourcesBrowserFixtureRealConnectAndGuards|GitHubAPI|DeviceAuthorizationHTTPIsolationAndCancellation|DeviceFlowDisabledIsExplicit|ExpiredCredentialCanBeReauthorizedWithoutReturningOldContent)' -count=1 -v
CGO_ENABLED=0 go test ./internal/server/httpapi -run '^TestSourcesBrowserFixtureRealConnectAndGuards$' -count=5
CGO_ENABLED=0 go test ./internal/server/httpapi -count=1
CGO_ENABLED=0 go test -c ./internal/server/httpapi -o "$ROOT/artifacts/verified.test"
node scripts/verify-sources-web.mjs \
  --fixture-bin "$ROOT/artifacts/verified.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --output "$ROOT/artifacts/delivery-run" --phase after
```

基线：在仓库只读执行 `git archive c120214210c8fb790c84b9c1d30217dc72e74849 | tar -x -C "$ROOT/before-work"`，为它链接同一 node_modules，复制本切片 fixture 与脚本，再执行相同生产 build、`CGO_ENABLED=0 go test -c ./internal/server/httpapi -o "$ROOT/artifacts/baseline.test"`。浏览器命令改用该二进制、`--phase before --output "$ROOT/artifacts/baseline-delivery"`。`before` 会记录预期 UI 差异而不立即终止；实际 API/交互失败仍会 exit 1，不能把它标作通过。

## 真实 fixture 与安全边界

这是生产 Web dist → Go embed → httptest TLS → 原 Connect handler → 真实鉴权和 SQLite → 合成 GraphQL/device upstream，不是静态 mock 页面。upstream transport 只接受明确的三个 GitHub URL，其它请求失败；浏览器只允许 127.0.0.1，不登录 GitHub。测试用户及凭据每次随机，ready.json 权限 0600，控制端口使用随机 capability，拒绝 Origin/查询参数及错误 capability，结束删除 readiness/home。fixture 日志不记录请求 body、token 或 headers；浏览器失败仅输出脱敏的断言和安全状态。

`safe-state.json` 只含 RPC 方法名、错误 code、上游校验计数、HTTP 响应无 token 布尔值、SQLite/WAL/SHM 无 token 明文布尔值。最后两项结合真实 Credential 保存和成功同步使用，而不是拿空数据库扫描当加密证据。截图前检查 password 字段为空；不输出 secret 注入表达式或原始浏览器错误。

loading/error 使用 fixture 响应故障；`absent` 删除 catalog 字段，验证未知状态；`short-expiry` 只在 fixture 缩短响应 TTL 到 3 秒，验证浏览器定时隐藏。服务端过期则用真实 Store 写入已过期的合成凭据，再验证 generation 替换、授权提示及旧 Lists 隐藏。故障模式与时间缩短不是生产逻辑。

## 实测结果与证据

- 生产 build（含 TypeScript）：通过，`artifacts/final-build.log`。
- 六项 GitHub/Device/expiry/fixture 聚焦测试：通过，`artifacts/final-focused-tests.log`；fixture 集合/订阅/空→长/过期替换测试重复五次通过，`artifacts/fixture-transitions.log`。
- httpapi 全包：通过，`artifacts/full-httpapi-tests.log`，156.021 秒。没有运行 race、cgo 或全仓 Go 测试。
- 最终样式完整浏览器轮次 `artifacts/verified/receipts.json`：exit 0，272 条证据记录；120 组几何检查（连接前、连接后 28 Lists、真实空、查询错误、服务端凭据过期 × 中英 × 深浅 × 1440/375/320 × 100%/200%），元素/文字越界均为 0。实际文字放大方式是 root font-size 200%，不是截图缩放或浏览器页面 zoom。
- 最终脚本 `artifacts/delivery-run/receipts.json`：exit 0，274 条记录、121 组几何检查全部通过、140 张真实页面截图，`unexpectedBackendFailures` 为空。包括额外 token 表单 320px/English/浅色/200% 文字检查。不是以截图数代替交互断言。
- 同一最终脚本的纯基线 `artifacts/baseline-delivery/receipts.json`：exit 1，233 条正常证据、121 组几何检查中 39 组越界、42 项 UI 差异、141 张截图（多一张真实 Internal 错误）。遇到后端 Internal 后显式同步恢复继续取证，但退出码仍为 1。
- `CGO_ENABLED=0 go vet ./internal/server/httpapi`：exit 0，`artifacts/final-vet.log`。既有不透明文字 token 的十二组对比计算均 ≥4.5:1，最小 4.90:1（浅色选中背景上的次级文字），见 `artifacts/contrast.json`；不是整页自动可访问性审计。
- 已执行真实 password 输入/错误/清空、验证后连接、Device flow 发起和取消（验证码撤除）、订阅成功及失败恢复、`SetListSubscription → Sync` 次序、真实 upstream 延迟下 busy/禁用、筛选无匹配、手动同步错误/恢复、初次和缓存查询错误、缺失 catalog、客户端 TTL 及服务端过期、stale generation 拒绝且不启动 sync、刷新后同步恢复、Tab 到 Checkbox/Space 取消及可见焦点、ReduceMotion。

| 场景 | Before（`artifacts/baseline-delivery/`） | After（`artifacts/delivery-run/`） |
| --- | --- | --- |
| 连接前 / 深色 / 中文 / 1440 | `disconnected-dark-zh-1440.png` | `disconnected-dark-zh-1440.png` |
| 已连接 / 28 Lists / 深色 / 中文 | `connected-dark-zh-CN-1440.png` | `connected-dark-zh-CN-1440.png` |
| 窄屏 / 浅色 / English / 375 | `connected-light-en-375.png` | `connected-light-en-375.png` |
| 200% / 浅色 / English / 320 | `disconnected-light-en-320-text200.png` | `disconnected-light-en-320-text200.png` |
| 订阅失败，不当空列表 | `subscription-error-dark-zh-1440.png` | `subscription-error-dark-zh-1440.png` |
| 缓存查询错误，不显示旧 Lists | `cached-error-dark-zh-1440.png` | `cached-error-dark-zh-1440.png` |
| 服务端过期 | `credential-expired-dark-zh-1440.png` | `credential-expired-dark-zh-1440.png` |

其它真实截图包括 `loading-*`、`token-error-*`、`device-pending-*`、`sync-pending-*`、`sync-error-*`、`filter-no-match-*`、`unknown-*`、`client-expired-*`、`keyboard-focus-*`、`reduced-motion-*`、`generation-conflict-*` 和 `token-form-light-en-320-text200.png`。所有原图保持完整页面；裁剪图仅用于人工复核，不代替原图。

## 未通过与未验边界

1. **后端间歇 Internal 仍 OPEN**：纯基线 `baseline-release` 实际 exit 1，41 条 UI 差异已记录，随后在过期后重新授权的 Sync 收到 `internal`；安全状态还记录一次 GetCatalog `internal`。UI 迭代轮次 `release`、`accepted` 也出现相同 Sync `internal`；同一最终脚本的 `baseline-delivery` 再次复现。没有修改生产 API 或生产重试逻辑。最终 verified 与 delivery-run 整轮通过不消除该间歇性；安全请求序列与截图一起保留，交主线另审。最终 harness 若再次命中此已知后端错误，会记录 `unexpectedBackendFailures`、保留截图，再显式点击现有“立即同步”只为补采后续证据，整轮仍 exit 1，绝不把重试后成功算作通过。
2. 本机 Chromium 合成环境不是 GitHub 真实部署验收；没有验证真实 OAuth 用户授权完成、拒绝/超时/慢轮询、真实 token scopes 或 refresh grant。只验证既有 DeviceFlow 发起/等待/取消，不声称完成外部授权。
3. 未做真人读屏、物理触屏、Safari/Firefox、跨平台、真实 OS 字体放大或真实浏览器 zoom。检查主内容元素/文字及 document 横向尺寸；共享外壳布局和截断行为不在写集内。
4. 不包括 `/repositories`、收录内容、API/权限改造、备份管理页或新增 `/sources` 路由。不依此关闭完整产品视觉门禁。

## 执行日志摘要

### Hilbert BLOCK 后续：编码前定位与计划（2026-10-31）

独审报告 SHA256 为 `2b72dccd18e70ba3f2f311ce06c6310060fb44497b57107be0daab53de82b515`。其 BLOCK 与原始 RPC-only/浏览器失败证据保留在交付根的 `artifacts/hilbert-block/`，不撤销历史红轮或此前漏检的事实。继续原 archive 副本，不改主线。

当前追踪发现 `GitHubCatalog` 的 generation 锁和 Sync 各持久化阶段都先 SELECT 再 UPDATE；并发事务可能在读快照建立后才竞争写锁。先用独立 RPC-only 和可控 Ent 查询/写入交错的持久测试取得 RED，只输出固定阶段、允许的错误类型和 SQLite 数字码，不记录 `err.Error()`、token、headers 或 SQL 参数。验证假设后才修生产根因，不加 retry/sleep，不扩大 UI 请求时序或凭据生命周期。

计划写集：保留原六文件；新增 `internal/server/state/github_transaction.go` 和 `internal/server/state/github_transaction_internal_test.go`；生产修改仅 `internal/server/state/credential.go`、`internal/server/state/sync.go`、`internal/server/state/catalog.go`。不修改 auth、store 全局设置、web api/main/embed、backup 或全局文档。原 CSS 只改善极窄宽度徽标/按钮的自然排版，不减字号、不裁剪。

验收顺序：持久测试 RED → 单一事务锁序修复 → 同测试 GREEN 及既有 CAS/过期/隔离测试 → 全阶段失败账本门禁及负向探针 → 无恢复重试的 RPC-only 30+轮 → 生产 build 与真实 TLS/SQLite 浏览器完整矩阵。所有 Internal 都使退出非零；预期 unavailable/aborted/not_found 必须匹配阶段和方法。原失败证据继续随追加交付保留。

根因已取得可重复 RED：六阶段（catalog/credential/begin/claim/commit/subscription）的可控交错均返回 `*sqlite.Error` 数字码 517；没有用原始错误字符串定位。无 UI、移除恢复同步的 30 轮 RPC-only 同样失败，见 `artifacts/repair/state-red.log` 和 `artifacts/repair/rpc-red/`。SQLite 官方 isolation/result-code 文档将此码定义为旧快照提升写事务失败。修复为来源事务的第一条 SQL 先通过目标凭据零增量 UPDATE 获取写锁，再执行原有读/CAS/解密/写入；既有 busy_timeout 不变，无新增 sleep/retry、全局 DSN 或互斥锁。不存在凭据时零行 UPDATE 也先取得写事务，再由原逻辑判断未连接/首次创建。

先隔离并记录基线 → 加载明确指定的设计/动效/ub-mode 与完整 Go 1.27 指南 → 本文先写范围/验收 → 构建 fixture/脚本、真实基线 build 和截图 → 六文件内实现 → 聚焦测试、全包测试与生产 build → 浏览器黑盒交互及跨主题语言/尺寸矩阵 → 人工查看完整截图的裁剪，修正几何未越界但逐字断行的服务头 → 复验与打包。

过程中修复过验收 harness 本身的错误假设：CLI 空字符串 fill 未触发 React 受控输入（改为原生 setter + input 事件）；对每个 upstream 请求都延迟会把 28 Lists 放大到服务端超时（仅延迟真实 listing 请求）；generation 更新后旧观察不可见但连接仍有效，应由既有“立即同步”恢复，不假定一定需要重新授权。以上不改变生产请求时序，也不把 harness 调试失败算作产品通过。

## Hilbert BLOCK 修复追加报告

### 身份与边界

继续 `/tmp/cognate-sources-xzlFPw/work`，基线仍是 `c120214210c8fb790c84b9c1d30217dc72e74849`。旧 `delivery/` 工件不覆盖；追加交付使用 `delivery-v2/`。独审原报告及原始 RPC-only、浏览器失败保留在 `artifacts/hilbert-block/`。v1 漏检的 `artifacts/verified/safe-state.json` 也单独保全；不能只保留原来的绿 receipt。

累计写集为原六文件加 `internal/server/state/{credential.go,sync.go,catalog.go,github_transaction.go,github_transaction_internal_test.go}`，共十一文件。生产改变只有三个现有 state 文件的六处事务入口替换和一个 25 行事务助手；测试另行持久保存。`sources.tsx`、`en.ts` 与 v1 哈希相同；保留原成熟组件和 UI 风格。没有改 auth、Go store 全局设置、API/schema/权限、web api/main/embed、backup、依赖或全局文档。没有提交、推送或新分支。

主线更新到 `59da539509f6d7aa10c28f652b72c879157be63c` 不改变本交付基线。只读核对确认五个已有写集文件（`sources.tsx`、`en.ts`、`credential.go`、`sync.go`、`catalog.go`）在 c120214 与 59da539 的 Git blob 完全相同，六个新文件在两基线均不存在。另在私有目录仅导出 59da539 的相关已有文件，检查并实际应用累计补丁，十一文件结果哈希与交付一致。此项只证明补丁文本兼容；没有切换开发基线、修改主线或验收新主线整树运行/备份集成，也不推断管理员 CI 的结果。复核记录见 `artifacts/repair/scope-audit.json` 和 `package-validation.json`。

### F1：定位与根因修复

GetCatalog 的 `lockCatalogOwner`、Sync 的 reserve/claim/commit、凭据更新与订阅都使用延迟事务：先读用户/凭据，再写。另一个连接在首次读之后提交，当前读快照便不能升级成写事务。原 busy_timeout 不能使旧快照重新有效；这不是 UI 动画、轮询、凭据密文或“需要多等一会儿”的问题。

先写持久回归，再改生产：Ent 拦截器仅在尚未获得写锁的首次用户查询之后，插入并提交独立无关用户写入。六个阶段全部稳定 RED，安全诊断只记录阶段、`*sqlite.Error` 和数字码 517。修复后同一测试的交错不再能发生在读前写锁之间。另有两个 Store/独立连接池的 30 轮真实并发 catalog/reserve 回归，不依赖进程互斥锁。

`beginGitHubTransaction` 将目标凭据 `sync_sequence += 0` 的 UPDATE 作为第一条 SQL，在创建任何读快照之前获取 SQLite 写事务。零行首次连接也先锁；没有修改 generation、密文、序列值、过期时间、状态或 fetch ID。后续原有身份检查、CAS、解密与提交逻辑均保留。事务在网络取数之前释放；不在整个上游调用期间持锁。不改全局 DSN，不加 retry/sleep，不吞掉 517，也不将它映射为成功或空列表。

| RED 证据 | GetCatalog Internal | Sync Internal | 退出 |
| --- | ---: | ---: | ---: |
| Hilbert 原独审 RPC-only，30 轮 | 2 | 17 | 1 |
| 作者初次原生产二进制、去掉恢复同步的 RPC-only，30 轮 | 10 | 14 | 1 |
| 同最终脚本/fixture/UI，仅不应用生产锁序修复的 archive 对照，30 轮 | 3 | 22 | 1 |

第三行使用 `repair-red-control`，不加载 UI、不重试失败 RPC；`rpc-red-control/receipts.json` 绑定最终脚本 SHA256 与旧生产测试二进制 SHA256。对应 `state-red-control.log` 的六阶段同样均为 517。原有首轮 `state-red.log` 和 `rpc-red/` 也保留，不倒置“先 RED 后修复”的执行历史。

### F2：全阶段失败账本与负向门禁

fixture 现在为所有实际 Auth/GitHub RPC 失败以及显式注入失败记录 `sequence/stage/mode/method/code`，不记录 error message、SQL 参数、token、session 或 headers。注入 `unavailable` 也必须入账；无法解析的失败记为 unknown，不静默忽略。harness 在成功和异常退出路径都会最终读取并保存账本，检查事件与 failures 数量/顺序完整性，再决定退出码。读不到最终账本也失败。

允许的错误只有：token-invalid 的 ConnectToken unavailable；upstream-error 模式、subscription-error/sync-error 阶段的 Sync unavailable；query-error 模式和阶段的 GetCatalog unavailable；generation-conflict 的 ConnectToken aborted；列明页面加载阶段正常设备状态查询的 GetDeviceAuthorization not_found。其它错误失败，所有 Internal 无条件失败，即使处于有意错误阶段，或后来观察恢复 200。删除 v1 为采集证据而显式重试重新授权 Internal 的分支。

- 纯门禁自测 59 项 PASS；把每条允许规则换成 Internal/unknown/permission_denied 或错阶段均失败，缺失/不完整账本也失败。封包核对还补齐了即使 failures 为空、账本字段缺失也必须失败的边界。
- 历史 v1 verified safe-state 经新 `--audit-state` 实际 exit 1，证实原假绿现在被拒绝。
- 最终活体负向探针：五阶段 × 六方法（包括 Auth.GetSession），注入 30 个单次 Internal，每次后续真实 GetCatalog 都已 200，最终仍 exit 1、完整报告 30 个 unexpected，未被后来的正常观察洗绿。

### 最终 GREEN 与窄屏取证

- 最终脚本和 fixture 二进制 SHA256 由每份最终 receipt 的 `identity` 及交付 `IDENTITY.json` 精确标识。启动时计算，不能用后来修改的脚本冒充当轮来源；封包前的旧身份轮次另存 preflight，不作最终脚本的轮次。
- `rpc-delivery-a/b/c` 三个独立 fixture 各 100 轮：共 300 轮过期/重新授权/同时 GetCatalog+Sync，均 exit 0、所有账本无 unexpected；未加载 UI、没有恢复性 Sync 重试。此前另三批各 100 轮也绿，不能替代这三份绑定最终身份的证据。
- `browser-delivery`：真实生产 dist + TLS/Connect/SQLite，exit 0，395 条证据、140 张完整截图、121 组几何与控件英文整词检查全部通过。`browser-round1`、`browser-final` 也分别完整通过；最后一轮才作为最终身份绑定的交付轮。
- 原 120 组中英/深浅/宽度/字号矩阵、token/过期/generation、Device flow 取消、订阅/同步、键盘及 ReduceMotion 保留。新增词范围几何检查，不再仅用容器不越界证明 Connected 没有碎为 Connect/ed。
- CSS 只减少极窄卡片的相对留白、恢复按词换行、按钮文本平衡和 token 按钮用满可用宽度。320px/200% English 浅色完整截图及人工查看裁剪在 `inspection/`：Connected 为一行，Update connection 为两个完整词的两行；字号未缩小、内容未裁剪。裁剪只辅助复核，原图仍完整保存。
- 生产 Web build/TypeScript、完整 state 包（26.167s）、完整 httpapi 包（143.199s）和两包 vet 均 exit 0。持久回归重复与其它相关包的原始结果保存在 `state-final-repeat10.log`、`related-tests.log`，生产 Go 构建日志为 `server-build.log`。所有 Go 命令均 CGO_ENABLED=0，无 race/cgo。

`backendRecoveryRetries: 0` 指没有为未预期后端失败增加或执行恢复重试。原 30 秒 catalog/2 秒 device 观察与 DOM 状态等待未改；对有意注入 unavailable 的测试仍主动操作现有“重试/立即同步”来验证用户恢复行为，并与 Internal 门禁分开记账，不能把这些测试操作说成生产重试策略。

### 追加复现命令

```bash
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
ROOT=/tmp/cognate-sources-xzlFPw
cd "$ROOT/work"
CGO_ENABLED=0 go test ./internal/server/state -run '^TestGitHubTransactions' -count=10 -v
CGO_ENABLED=0 go test ./internal/server/state -count=1
CGO_ENABLED=0 go test ./internal/server/httpapi -count=1
CGO_ENABLED=0 go test ./internal/server/auth ./internal/server/githubauth ./internal/github -count=1
CGO_ENABLED=0 go vet ./internal/server/state ./internal/server/httpapi
npm --prefix web run build
CGO_ENABLED=0 go test -c ./internal/server/httpapi -o "$ROOT/artifacts/repair/sources-repair.test"
CGO_ENABLED=0 go build -o "$ROOT/artifacts/repair/cognate-server" ./cmd/cognate-server
node scripts/verify-sources-web.mjs --self-test-gate
node scripts/verify-sources-web.mjs --audit-state "$ROOT/artifacts/verified/safe-state.json"
# 上一条应 exit 1：拒绝历史假绿，不应以 set -e 跳过后续命令。
node scripts/verify-sources-web.mjs --fixture-bin "$ROOT/artifacts/repair/sources-repair.test" \
  --rpc-only --rounds 100 --output "$ROOT/artifacts/repair/rpc-reproduce"
node scripts/verify-sources-web.mjs --fixture-bin "$ROOT/artifacts/repair/sources-repair.test" \
  --rpc-only --negative-gate --output "$ROOT/artifacts/repair/negative-reproduce"
# 上一条应 exit 1，30 个 Internal 都必须被保存并报告。
node scripts/verify-sources-web.mjs --fixture-bin "$ROOT/artifacts/repair/sources-repair.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --output "$ROOT/artifacts/repair/browser-reproduce" --phase after
```

同脚本 RED 对照的构建只撤去生产修复，保留新 fixture 和 UI。不要对当前工作副本反向打补丁：

```bash
ROOT=/tmp/cognate-sources-xzlFPw
CONTROL="$ROOT/reproduce-red"
mkdir "$CONTROL"
git -C /home/czyt/code/go/skillhub archive c120214210c8fb790c84b9c1d30217dc72e74849 | tar -xf - -C "$CONTROL"
cd "$ROOT/work"
tar -cf - web/src/sources.tsx web/src/sources.css web/src/locales/en.ts \
  internal/server/httpapi/sources_browser_fixture_test.go scripts/verify-sources-web.mjs \
  docs/SOURCES-UI.md internal/server/state/github_transaction_internal_test.go | tar -xf - -C "$CONTROL"
ln -s /home/czyt/code/go/skillhub/web/node_modules "$CONTROL/web/node_modules"
cd "$CONTROL"
npm --prefix web run build
CGO_ENABLED=0 go test ./internal/server/state -run '^TestGitHubTransactionsDoNotUpgradeStaleSnapshots$' -count=1 -v
# 上一条应 exit 1，六阶段的 SQLite 517；没有生产助手，故不可能假冒修复后测试。
CGO_ENABLED=0 go test -c ./internal/server/httpapi -o "$ROOT/artifacts/repair/red-reproduce.test"
node "$ROOT/work/scripts/verify-sources-web.mjs" --fixture-bin "$ROOT/artifacts/repair/red-reproduce.test" \
  --rpc-only --rounds 30 --output "$ROOT/artifacts/repair/rpc-red-reproduce"
# 上一条应 exit 1；采完 30 轮，无额外恢复请求。
```

### 工件使用与仍保留的边界

`delivery-v2/sources-repair.patch` 是相对“原基线 + 原六文件 patch”的九文件增量；不能单独打在裸 HEAD 上。`sources-ui-v2.patch` 是相对原 c120214 的十一文件累计补丁，两者二选一，禁止叠加。`sources-ui-v2.tar.gz` 恰含十一文件；SHA256SUMS 标识最终源码，ARTIFACT-SHA256SUMS 标识两个 patch、源码包、证据包和实际测试二进制。两条补丁路径均在私有干净对照应用并逐文件核验，不在主线试打。

证据包包括原 v1 证据包、Hilbert BLOCK 和失败原图/账本、原假绿 safe-state、全部修复日志/账本及最终 browser-delivery 的 140 张原图；中间轮及 preflight 浏览器只收 receipts/账本，不重复打包其 PNG（私有目录仍保留）。不包含账号 readiness、数据库、浏览器 profile、node_modules 或测试二进制。原始失败历史没有删除或重命名成成功。

本次证明的是上述可复现 SQLite 根因和当前候选的门禁修复，不保证每个历史 Internal 都已归因，也不穷尽所有并发部署/跨进程异常。原有真实 GitHub 外部授权、Safari/Firefox、真人读屏、物理设备、真实 OS 字体缩放/浏览器 zoom、全仓 Go 测试等未验边界继续保留。作者修复验证 GREEN 不自动撤销 Hilbert 的独立 BLOCK，仍需独立复审；不以截图数、最后绿轮或恢复成功替用户签收。

## 主线程修复独审 N1：写锁准入的可取消等待

原 v2 独审报告 `/tmp/cognate-sources-review-v2-YHluhh/work/REVIEW.md`，SHA256 `9614aba8473107686f458047aa2f4f41b4b7075a0103ab39e88ec3a66a238ca2`：Spec 对原 P1/P2 修复点 ACCEPT，但 Quality / 总体 BLOCK。新增初始 UPDATE 在 SQLite busy handler 内等待外部写锁，100ms deadline 后仍不返回；有/无凭据两例均到约 1530ms 人为解锁后才结束。原基线同探针立即返回错误，不代表旧目录读取成功。该 finding 与备份认证读取的 8714 不同。

主线程新增持久 `TestGitHubTransactionLockWaitCancellation`，两个独立 Store、外部真实写锁，覆盖 deadline/显式取消 × 无/有凭据，并在取消后验证同一 Store 的正常读取和身份。先在 v2 上实际观察四例 RED：一秒后仍阻塞，解锁后才清理结束；失败日志在 `/tmp/cognate-sources-main.cxZAL9iu/cancel-red.log`。

修复保留普通连接池和原 pragma，不在已使用的物理连接上临时改 busy_timeout。只有显式 GitHub 事务上下文进入独立准入池：busy_timeout=0、BEGIN IMMEDIATE；在任何业务读取、生成代次校验或写入之前取得 SQLite 写锁，仍由原 Ent client 配置构造事务，保留 hooks/interceptors。遇到精确 SQLITE_BUSY 时只重试尚未开始的 BEGIN，以 5ms 可取消等待、最长 10s 为限；不重试 517、不重放 CAS/提交/网络请求，也不是 UI 自动重试。成功事务使用原调用 context，取消与 rollback 仍释放锁；两个池统一关闭。此处存在明确的锁准入轮询，不能继续笼统声称“没有新增任何 retry”。

修复写集是在累计 v2 之上增加 `store.go` 的 driver 构造接线、`github_transaction_driver.go` 和 `github_transaction_cancel_test.go`，并给原助手标记 GitHub 事务上下文；不修改 schema、API、generation、密文或全局 journal_mode。四文件增量 `/tmp/cognate-sources-main.cxZAL9iu/n1-fix.patch` SHA256 `a4eaaf253fb33c175787b03cd30861ee139a90c1e1733b379def9fe13325c3c7`，仅能应用到原 v2，不与累计补丁混用。

Go 1.27.3、全部显式 CGO_ENABLED=0：取消矩阵及六阶段 517/独立连接池专项 GREEN；`go test ./internal/server/state -run '^TestGitHubTransaction' -count=10 -timeout=120s` exit0（7.883s）；state/auth/githubauth/github/httpapi 五包 `-count=1 -timeout=300s` 全通过；相关 vet exit0。日志为同证据目录的 `cancel-green.log`、`cancel-repeat.log`、`cancel-related.log`、`cancel-vet.log`，原 RED 保留。

原 reviewer 的限定 N1 复审现为 **ADDRESSED / Spec ACCEPT / Quality ACCEPT**。冻结报告 `/tmp/cognate-sources-review-v2-YHluhh/work/REVIEW-N1.md`，SHA256 `c9771a94d050805195a5487dad75ac372ca9c7a1615746dcac738b14a8bcfd67`；它在独立 v2 副本核对四文件摘要，原 deadline 探针改为约 100ms、外部解锁前返回。新增独立普通池隔离、hooks、CAS/业务不重放、准入后取消 rollback 与双池/managed Close 探针三轮通过，准入预算实测 10000ms 返回 SQLite code 5，state vet exit0。该复审只解除 N1 的限定阻断，原 BLOCK 与 RED 不重写；备份 8714、最终组合和完整 GA 仍需各自证据。
