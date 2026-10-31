# 公开目录与技能详情：首发 UI 精致化

日期：2026-10-31。基线：`main` / `8a701c5`。本阶段为公开目录与公开技能详情的作者实现及验证，不是完整首发 UI 或 GA 签收。

## 方向与写集

延续 `UI-DESIGN.md` 的精密控制台：分层中性材质、明确文字层级、冷色定位与操作。公开目录采用目录索引而非等权信息卡；详情将来源标识和匿名安装分区。继续使用系统字体、既有语义 tokens、React、TanStack、现有 Button/Input、原生 select 和 Lucide；共享偏好仍由既有 Radix 组件提供。

总写集七文件（含独审后仅几何修复）：

- `web/src/explore.tsx`
- `web/src/explore.css`：组件直接导入，全部规则限定在 `.public-catalog` 内，不修改全局样式。
- `web/src/styles.css`：独审后仅追加公开工作区页脚的换行约束，不修改业务 `main.tsx`。
- `web/src/locales/en.ts`：仅新增九项必要翻译；中文继续使用原有 i18next 机制，用户技能内容不自动翻译。
- 本文档。
- `scripts/verify-public-catalog-web.mjs`：永久浏览器回归与截图入口。
- `internal/server/httpapi/public_catalog_browser_fixture_test.go`：仅真实 API 的隔离测试 fixture，不修改生产 API。

没有增加依赖、替换框架、修改 native 或备份文件，也没有提交、推送、新建分支或启动 agent。主线程 C3 在途依赖不属于本写集。

| Before | After | Why |
| --- | --- | --- |
| 搜索容器占据宽列，实际输入框仍受旧 `.filter-field` 固定宽度约束 | 局部覆盖为满列输入，搜索、分类、提交形成明确操作行；375px 单列排列 | 让用户输入的主操作获得应有空间，保持原标签与原生控件 |
| 结果卡片标题、来源、分类、路径权重接近 | 连续目录索引：标题与描述为主，来源次级，分类/路径独立；窄屏自然堆叠 | 提高扫描效率，完整保留描述，不做假热门、总量或评分 |
| 已提交的查询语境只在输入控件中体现 | 具名的当前筛选组与明确的清除筛选操作 | 区分当前结果条件和正在编辑但尚未提交的表单值 |
| 分类与技能分页的语境较散 | 分类分页保留在搜索面板，技能页码、容量边界和真实本页数量分层显示 | 保留两套独立 cursor，不把本页数量误写为目录总量 |
| 详情元数据和安装说明纵向铺开 | 宽屏来源/安装双面板，窄屏保持 DOM 顺序堆叠；完整 hash 和路径可换行 | 来源、内容身份和下一步操作各有位置，键盘顺序不被 CSS 重排 |
| 安装说明与复制反馈同权重 | 命令独立底色、原生复制按钮和状态区；单独解释“公开不等于安全审查” | 强调来源信任、仅复制未执行、撤回公开及私有 Pack 授权边界 |
| loading、空结果与错误多为零散文字 | 独立状态面板，保留 live status、错误原义和重新读取入口 | 不用假数据填空，不把读取失败解释为成功或零结果 |

## 保留的边界与动效

- TanStack 查询键、公开客户端、`staleTime`、请求取消与分页大小不变；技能 24 项、分类 64 项，最多 100 页。已重复的 cursor 和本次浏览上限仍禁止继续，不自动追页。
- 表单仍按 trim 后的 UTF-8 字节数限制为 128；输入属性限制不代替字节检查。超限深链接不发技能列表请求，仍允许修正搜索。
- 未分类技能保持可见；已选择但不在当前分类页的值保留。分类计数来自真实匿名 API，不从私有管理计数推断。
- 仓库外链仍使用原有严格的 owner/repository 格式检查、HTTPS GitHub 目标及 `rel="noreferrer"`。完整 SHA-256、分支、路径和发布 revision 保留，不伪装为安全认证。
- 安装命令继续调用原有 `publicInstallCommand`，保留当前 origin 和 shell 单引号转义。没有添加 token、凭据、`--insecure` 或其他安装选项；页面不执行安装，也不请求下载接口。
- 唯一新 motion 是指针 hover 的方向反馈：链接箭头在具备精细指针的 hover 环境中轻移 2px，复用 150ms / `--ease-out`。键盘焦点即时恢复静态；ReduceMotion 不注册该过渡。不增加进入、搜索、分页、输入或布局动画，无循环装饰、shimmer 或假进度。共享按钮保留既有即时焦点/按压反馈。

## 真实 fixture 与实图

本机证据根目录：`/tmp/cognate-public-catalog-ui.ouNrB5aU`。PNG 为 Chromium 实际渲染截图，不是生成图、HTML mock 或静态拼图，不加入仓库资源。

fixture 使用实际 TLS HTTP App、加密 SQLite、真实归档收录/公开状态/分类关联与 PublicCatalog RPC。上游 transport 仅提供测试内合成 tar.gz；27 个公开技能分为 24+3 项两页、66 个公开分类分为 64+2 项两页，同时包含未分类项、多语言内容、未公开技能和仅关联未公开技能的分类。所有数量为测试库实际行数，不作为产品统计展示。测试使用随机临时密钥、合成账号与隔离 HOME/XDG；没有读取真实凭据或配置。

- `before/`：编辑 UI 之前采集的 16 张原始全页 Before，`before/receipts.json` 为通过，fixture 正常退出。
- `before-final/`：复用编辑前已编译的 `before.test`，补齐相同 viewport 的 Before 和固定 `Change review` 技能详情，共 24 张。这样详情比较使用相同名称/内容，而不是随机首项。测试数据库 ID、端口及 cursor 每次重新生成，因此目录排序和安装地址并不要求逐像素相同。
- `final/`：最终作者验证，**48 条证据，其中 39 张 PNG**；`final/receipts.json` 为 `pass: true`，fixture exit 0。包含八组深浅主题 × 中英 × 1440/375 的目录 viewport、目录全页及技能详情，以及状态与行为截图。

| 实图场景 | Before（根目录下） | After（根目录下） |
| --- | --- | --- |
| 深色中文目录，1440×960 | `before-final/explore-dark-zh-CN-1440.png` | `final/explore-dark-zh-CN-1440.png` |
| 浅色 English 目录，375×812 | `before-final/explore-light-en-375.png` | `final/explore-light-en-375.png` |
| 浅色 English 同技能详情，375px | `before-final/detail-light-en-375.png` | `final/detail-light-en-375.png` |
| 深色中文同技能详情，1440px | `before-final/detail-dark-zh-CN-1440.png` | `final/detail-dark-zh-CN-1440.png` |

附加证据在 `final/`：`keyboard-search.png`、`repeated-cursor-guards.png`、`loading-results.png`、`empty-results.png`、`error-results.png`、`recovered-results.png`、`copy-command-only.png`、`copy-failed-manual-command.png`、`unavailable-detail.png`、`reduced-motion-en-375.png`、`large-text-detail-en-375.png`、`landscape-detail-en-812x375.png`。公开 RPC 调用记录为 `public-rpc-trace.json`，未记录凭据原文。

## 作者验证与复核命令

本次 Go 使用 `/home/czyt/.local/share/mise/installs/go/1.27.3/bin`，写 Go 前完整读取 Modern Go CLI 的 1.27 规则。所有 Go test/build/vet 显式 `CGO_ENABLED=0 GOTOOLCHAIN=local`，未运行 race/cgo。

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
EVIDENCE=/tmp/cognate-public-catalog-ui.ouNrB5aU
sha256sum -c "$EVIDENCE/source-freeze.sha256"
npm --prefix web run build
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -count=1 -timeout=180s -run '^(TestPublicCatalogBrowserFixtureDataAndGuards|TestCatalog|TestLibraryRefreshRPCVersionIsolationAndPublication)' ./internal/server/httpapi
CGO_ENABLED=0 GOTOOLCHAIN=local go vet ./internal/server/httpapi
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -c ./internal/server/httpapi -o "$EVIDENCE/review.test"
node scripts/verify-public-catalog-web.mjs \
  --fixture-bin "$EVIDENCE/review.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --output "$EVIDENCE/review"
```

- `final-build.log`：生产 build 通过，包含 TypeScript 检查；`final-compile.log`：实际嵌入最终 Web 资源的测试二进制编译通过。
- `final-catalog-tests.json`：定向公开目录、发布、生命周期、fixture 与刷新兼容测试 **15 PASS / 0 SKIP / 0 FAIL**；`final-vet.log`：当前 HTTP API 包 vet 通过。没有运行或宣称全仓通过，主线程负责的 generation 测试不纳入本作者结果。
- 浏览器实际操作验证：真实两套分页、跨分类页保留选择、搜索与清除、UTF-8 表单/深链接拦截且不增加技能列表 RPC、双重复 cursor 禁止下一步、空/loading/error/独立重试、Tab/Enter 跳过导航后搜索、精确 origin/id 的匿名命令、仅复制反馈与剪贴板拒绝回退、详情不存在时不显示安装命令、公开请求不带 Authorization 且没有下载调用。
- 375px 深浅中英均无页面横向溢出；English 125% 根字号和 812×375 横屏详情无溢出。ReduceMotion 下新箭头过渡为 `0s`，子树无运行中的动画。
- `red/receipts.json`：新增“当前筛选”语义检查对编辑前 bundle 为 RED；不是声称旧版存在权限回归。该项及全部后续浏览器行为在最终 bundle 为 GREEN。
- 作者迭代失败保留在 `after/`、`after-v2/`：一次是测试查询 `Change` 同时命中描述，三项预期不成立；一次是 cursor 故障注入只处理 JSON，未覆盖实际 binary 协议。fixture 随后同时支持 binary/ProtoJSON，并用带 `Unwrap` 的 recorder 保留真实 socket deadline；重复 cursor 模式仅关闭该次合成响应的压缩以重编码故障。独立机械测试证明故障仍保留真实行数据，未以 HTML/假列表替代 API。
- 主线程 C3 类型在途期间没有修其业务文件；使用 `git archive 8a701c5` 的干净临时 export 验证 fixture，记录 `export-fixture-tests.log`、`export-catalog-tests.json`、`export-vet.log`。主线程通知可编译后，最终 fixture 和验证重新在当前 workspace 执行。
- `source-freeze.sha256` 固定五个实现/验收代码文件；`freeze-files.sha256` 再加本文档。实现冻结已通知主线程，可交由其安排非作者只读独审。

本阶段仅证明本机 Chromium 的隔离 TLS/SQLite fixture、响应式渲染及列出的自动化行为。没有执行截图中的安装命令，也未做真实生产账号、真实 CLI 下载/安装、读屏器、触屏设备或跨浏览器真人验收。fixture 自签名证书只在隔离浏览器会话中忽略，不放宽产品的 TLS/安装边界。**据主线程反馈，首轮局部及几何修复独审均 ACCEPT；文末新增 P3 复制按钮碎字修复仍待非作者复核，不据此宣称完整首发 UI 或 GA 完成。**

## 独审后追加：200% 文本窄屏重排

本轮只修改 `web/src/styles.css`、`web/src/explore.css`、永久浏览器回归脚本和本文档。产品 TSX、翻译、Go fixture、公开权限与安装行为保持首轮冻结摘要不变，不新增页面、控件或动效。

根因是 flex 固有宽度：公开页脚未允许换行，英文匿名文本项将文档撑至 406px；分类、技能分页与复制按钮虽允许文字换行，但仍可按自身最小内容宽度超出父容器。修复仅为公开页脚增加 `flex-wrap: wrap`，为这三组按钮增加 `max-width: 100%`、flex 换行及必要的单词内换行。保留 200% 字号、完整标签、现有内边距、焦点环和控件；不以缩小字体、隐藏溢出、删文字或禁用缩放掩盖问题。

新证据根目录：`/tmp/cognate-public-catalog-reflow.1h4jIZAD`。`red/` 使用首轮冻结的真实 `final.test`，不是旧版假 HTML；其摘要在 `before-binary.sha256`。在修改产品 CSS 前已运行新增永久回归，保存 **RED / exit 1**，fixture 正常退出 0，并留下全部矩阵的布局与 56 张实图。下述页宽和边界均来自实际浏览器几何，不是图片推算：

| Before | After | Why |
| --- | --- | --- |
| 375px、200% English：目录与详情页宽均 406px，页脚文字右边界 406.125px | 页宽 375px，页脚自然换行，文字无视口外矩形 | 修复匿名 flex 文本项撑宽，不改页脚业务内容 |
| 320px、200% English：More categories 右边界 334.156px，父容器右边界 248.609px | 按钮及完整文字留在分类分页容器内 | 同时约束按钮外框与内部文字，不只检查页面总宽 |
| 320px、200% English：Copy command 右边界 325.328px，命令容器右边界 248.609px | 复制按钮在命令容器内纵向扩展，可用 Enter 激活 | 保留完整命令、按钮和字体，避免仅修页脚后遗留内部越界 |

### 永久回归与图证

- `red/reflow-layouts.json`：24 份布局记录，覆盖深浅 × 中英 × 320/375px × 目录首分类页/后分类页/详情；English 12 份命中原问题，中文 12 份未误报。
- `green-geometry/receipts.json`：专项 GREEN，58 项证据、56 张 PNG，fixture exit 0。所有 24 份布局均为 `scrollWidth === width`、根字号 `32px`，元素/文字/容器内控件越界数组为空。
- `final/receipts.json`：原有完整交互回归加新矩阵 GREEN，105 项证据、95 张 PNG，fixture exit 0；对应布局在 `final/reflow-layouts.json`，匿名 RPC 记录在 `final/public-rpc-trace.json`。
- 每组 200% 矩阵都以真实 Enter 激活分类前后页、技能前后页、搜索和复制；断言真实结果数、准确 origin/id 的匿名安装命令及“仅复制、未安装”反馈。原有真实 Tab 顺序、UTF-8、重复 cursor、错误重试、复制拒绝、ReduceMotion 和 125%/横屏检查仍全部通过。没有使用 force-click 或额外产品等待绕过交互。
- `build.log`、`compile.log`、`catalog-tests.json`、`vet.log`：生产 build/TypeScript、CGO0 嵌入最终资源的 fixture 编译、15 PASS / 0 FAIL / 0 SKIP 定向 Go 测试、HTTP API vet 均通过；不宣称全仓测试通过。

下表图片路径相对于新证据根目录。目录排序与随机端口可能变化；详情固定同一名称和内容的 `Change review`。

| 场景 | Before / RED | After / GREEN |
| --- | --- | --- |
| 375px English 浅色页脚 | `red/reflow-light-en-375-200pct-footer.png` | `final/reflow-light-en-375-200pct-footer.png` |
| 320px English 浅色更多分类 | `red/reflow-light-en-320-200pct-categories.png` | `final/reflow-light-en-320-200pct-categories.png` |
| 320px English 深色后分类页 | `red/reflow-dark-en-320-200pct-next-categories.png` | `final/reflow-dark-en-320-200pct-next-categories.png` |
| 320px English 浅色详情复制 | `red/reflow-light-en-320-200pct-detail-command.png` | `final/reflow-light-en-320-200pct-detail-command.png` |

### 新冻结与复核命令

`source-freeze.sha256` 固定六个代码文件（首轮五个加页脚样式）；`freeze-files.sha256` 再加本文档，共七文件。`delta-freeze.sha256` 仅列本轮四个变更文件。原证据目录及其旧摘要保留，不覆盖历史 RED/GREEN。

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
EVIDENCE=/tmp/cognate-public-catalog-reflow.1h4jIZAD
sha256sum -c "$EVIDENCE/freeze-files.sha256"
npm --prefix web run build
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -c ./internal/server/httpapi -o "$EVIDENCE/review.test"
node scripts/verify-public-catalog-web.mjs \
  --fixture-bin "$EVIDENCE/review.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --output "$EVIDENCE/review"
```

仅复核新增矩阵时追加 `--geometry-only` 并使用独立 output；复现 RED 时将 fixture-bin 换为 `/tmp/cognate-public-catalog-ui.ouNrB5aU/final.test`，预期末尾的 200% 重排断言失败。新增专项仍只代表本机 Chromium 的隔离 fixture 自动化，200% 为根文字大小而非浏览器页面缩放；没有新增跨平台真人验收结论，没有执行安装，未提交。主线程已回报此几何增量独审 ACCEPT，后续 P3 增量单独记录如下。

## P3 追加：复制按钮保持完整单词

本轮只改 `web/src/explore.css` 的安装命令/复制按钮样式、永久浏览器脚本和本文档。根因是 200% 根字号下，多层 rem 内边距让 320px 复制按钮的文字区过窄；上一轮 `overflow-wrap: anywhere` 虽消除了越界，仍将英文标签拆成碎字。

最小修复：只在窄屏为安装命令框和复制按钮设置 `padding-inline: clamp(8px, 2.5vw, 16px)`；复制按钮退出任意单词内换行规则，继续允许词间换行、flex 换行及原有宽度约束。其他分类/分页按钮规则不变。没有改字号、文本、业务行为，没有新增 overflow 裁剪或隐藏；完整命令和复制反馈均保留。

新证据根目录：`/tmp/cognate-public-catalog-copy-words.Dt8MRiro`。CSS 修复前，新增永久检查使用已通过几何独审的旧 `after.test` 真实复现 RED；`before-binary.sha256` 固定该二进制。检查用每个英文单词的精确文本节点起止偏移创建 Range，并逐词读取 `Range.getClientRects()`，不是只检查按钮外框或字符串存在。

| Before / RED | After / GREEN | Why |
| --- | --- | --- |
| 320px / 200%：Copy 为 2 个 Range 矩形，command 为 4 个 | 每个单词均为 1 个矩形 | 释放横向内边距，并按单词换行，不缩小 28px 按钮文字 |
| 375px / 200%：command 为 2 个矩形；复制后的 Command 也被拆分 | Copy/command 与 Command/copied 各自完整 | 同时覆盖复制前后标签，避免只修初始状态 |

- `red/receipts.json`：逐词断言真实 RED / exit 1，fixture exit 0；原 24 份几何记录仍通过，证明新检查命中的是碎字而非旧越界。
- `green/receipts.json`：GREEN，7 项证据、4 张必要 PNG，fixture exit 0；`green/copy-word-layouts.json` 含深浅主题 × 320/375px × 复制前后的 8 份英文记录、16 个单词，全部恰为一个 Range 矩形，按钮字体仍为 `28px`。
- `green/reflow-layouts.json`：原深浅/中英/320/375px 的 24 份几何记录全部重跑通过，根字号仍为 `32px`，页宽等于视口，元素、文字和容器内控件越界均为零。
- 这次没有重做上一轮 105 项图证，也没有重复宣称全仓测试通过。`--geometry-only` 继续执行全部矩阵和 Enter 分类/技能分页、搜索、复制行为，但只拍英文浅色 320/375px 的复制前后四张图。完整匿名命令精确断言、仅复制反馈和无下载/无 Authorization 边界仍通过。
- `build.log`、`compile.log`：已重新执行生产 build/TypeScript 和显式 `CGO_ENABLED=0 GOTOOLCHAIN=local` 的 Go 嵌入 fixture 编译；脚本语法、空白及冻结摘要检查通过。

实图均为真实 TLS/SQLite fixture，相对路径如下：

| 场景 | Before | After |
| --- | --- | --- |
| 320px 英文复制前 | `red/reflow-light-en-320-200pct-detail-command.png` | `green/reflow-light-en-320-200pct-detail-command.png` |
| 320px 英文复制后 | `red/reflow-light-en-320-200pct-detail-copied.png` | `green/reflow-light-en-320-200pct-detail-copied.png` |
| 375px 英文复制前 | `red/reflow-light-en-375-200pct-detail-command.png` | `green/reflow-light-en-375-200pct-detail-command.png` |

### P3 冻结与组合复验

`delta-freeze.sha256` 固定本轮三文件；`source-freeze.sha256` 固定全部六个代码文件，`freeze-files.sha256` 加文档共七文件。CSS 已变化，**主线程最终 source 组合复验必须先重建 Web，再重编译 Go 嵌入资源，不能复用上一轮二进制**。

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
EVIDENCE=/tmp/cognate-public-catalog-copy-words.Dt8MRiro
sha256sum -c "$EVIDENCE/freeze-files.sha256"
npm --prefix web run build
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -c ./internal/server/httpapi -o "$EVIDENCE/review.test"
node scripts/verify-public-catalog-web.mjs \
  --fixture-bin "$EVIDENCE/review.test" \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /home/czyt/.cache/ms-playwright/chromium-1200/chrome-linux64/chrome \
  --geometry-only --output "$EVIDENCE/review"
```

复现本轮 RED 时将 fixture-bin 换为 `/tmp/cognate-public-catalog-reflow.1h4jIZAD/after.test` 并使用独立 output，预期逐词 Range 断言失败。这是作者冻结时的状态；后继集成验收见下节，不扩大本机 Chromium 验证的范围。

## 独立验收与独立切片集成

Godel 对初版目录/详情进行非作者源码、实图和真实浏览器复核，局部 ACCEPT；额外检查即时切换主题/语言不丢草稿、Radix/Escape 焦点恢复、Tab/Enter 与 125% 字号布局。第一轮发现的 200% 页脚/分页几何问题在独立同脚本 RED→GREEN 中闭合：24/24 几何通过，证据 `/tmp/cognate-public-catalog-reflow-green-review.Ecxkzq`。没有以几何通过掩盖随后发现的复制按钮碎字。

P3 由独立 reviewer 使用原 binary RED、新 binary GREEN 复验，原 binary 命中 12 个碎字单词；新 binary 深浅主题/320和375px/复制前后共 8 记录、16 个词均恰为一个 Range 矩形，字体仍为 28px。原 24 几何与 Enter 分页/搜索/复制仍通过，Authorization 与下载调用均为零；局部 ACCEPT，无遗留 P3。证据 `/tmp/cognate-copy-words-red-review.koVPcM`、`/tmp/cognate-copy-words-green-review.Kxc4Ta`。这些是三个有界切片的审查，不是完整产品或真人跨平台签收。

主线程为确保 UI 提交不依赖未验收备份 WIP，从干净 `git archive 8a701c5` 导出，仅覆盖本阶段七文件；重新构建 Web、编译 Go 嵌入资源后运行真实 TLS/SQLite fixture 的完整 `--checks-only` 浏览器回归。结果 pass=true、82 项证据/71 PNG、fixture exit0，包含最新复制词完整性与原 24 份几何；15 项定向 Go 测试零 skip/fail、HTTP API vet 和 TypeScript/生产 build 均通过。证据 `/tmp/cognate-catalog-standalone.raeuY6AR`。没有复制备份 App/RPC/controller 文件，避免将它们的在途审查混入 UI 签收；使用已有依赖，无新包安装。

本阶段七文件可单独提交，仍不把公开目录的精致化称为全产品首发 UI 完成；其它页面、原生与真实平台证据按完整首发门禁继续推进。
