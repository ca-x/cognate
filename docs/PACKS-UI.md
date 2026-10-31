# Pack / 分享 Web 切片

日期：2026-10-31。基线：`59da539509f6d7aa10c28f652b72c879157be63c`。范围仅为并行 UI 计划 Task 3，不代表 F5/F12、原生客户端或首发整体完成。

## 实现与约束

- 只修改 `web/src/packs.tsx`、`web/src/pack-shares.tsx`，新增本说明、局部 CSS/i18n、浏览器 fixture 和专项脚本，共七文件。组件自行导入资源，不修改共享路由、英文总表、API、生产 Go 或依赖。
- 使用既有 React/TanStack Query、Button/Input/Checkbox 与 Radix Select/Dialog。仓库选择、有效期、危险确认使用成熟控件；没有另造 portal、焦点陷阱或基础选择控件。
- 当前服务端 revision、更新时间、选择数与安装别名/hash 分层展示。选择数不冒充去重后的安装数；两个相同内容的选择在真实 manifest 中只返回一个技能。
- 编辑器首次打开时固定 CAS revision。列表/详情背景查询失败仍保留组件、选择、别名和输入，同时明确状态未知；不会把新读到的 revision 偷换成旧草稿的 CAS 基准。分享查询失败不显示缓存记录为“已撤销”或“已过期”。
- 保持既有写入顺序：保存成功后刷新详情/列表，再退出编辑；创建分享仅在授权成功回执中取得令牌，再刷新列表；撤销先关闭确认/清除相应新令牌，再刷新列表。mutation 明确不重试。保存冲突/结果不明停止本次编辑写入；分享、撤销及删除失败必须显式读取核对，不自动重放。
- 分享仅包含技能，不包含配置内容或密钥；一次显示，不进入 URL 或浏览器持久存储。令牌隐藏或离开该组件后不再回显。撤销/删除不能收回已经分发或安装的内容；不显示虚构设备同步状态，不暗示配置附件自动应用已经实现。
- 100 个 Pack、每 Pack 1–256 个选择沿用服务端边界。所有者、分享有效期与容量校验仍在原服务端协议处理，没有扩张授权范围。

## 视觉与动效

| Before | After | 原因 |
| --- | --- | --- |
| 列表/详情查询错误直接替换整个工作台/编辑器 | 缓存与草稿保持挂载，错误及未知状态独立呈现 | 不以丢输入掩盖失败 |
| 修订信息只在列表，技能与分享缺少范围层级 | 服务端修订、别名/hash、技能分享范围和独立危险区 | 明确内容身份，不虚构安装/同步完成 |
| 原生 select 在窄屏截断，长英文可能拆词 | Radix Select，仓库标识在 `/`、`-` 后提供断点，窄屏削减重复内边距 | 保留完整词与可操作区域 |
| 危险操作为内联二次按钮，删除后焦点可能落回 body | Radix 确认；撤销返回触发器/分享标题，删除落到稳定页面标题 | 可真实 Tab/Space/Enter/Escape，操作后有落点 |
| 对话框关闭按钮与标题留白随 200% 文字过度放大 | 窄屏保持 44px 图标按钮及相应标题留白 | 长英文确认标题不拆词，不遮挡动作 |

遵循指定 ui-ux-pro-max、emil-design-eng、apple-design、animate、animation-vocabulary 与 ub-mode。动效门槛：列表导航频繁，不做整列表位移；按钮按压为直接反馈，偶发 Select/Dialog 的 Scale in + Fade in 建立操作层级。复用共享 `--ease-out`，按压 150ms、Select 180ms、Dialog 200ms；无退出动画保留遮罩、无循环装饰、无假进度、无输入延迟。真实键盘和 ReduceMotion 不播放入场/按压位移动效。页面颜色/材质沿用共享主题 token。

## F1 后台分享查询焦点修复（限定后继）

独审报告 `/tmp/cognate-task3-packs-review.mgSgHTZa/REVIEW.md`（SHA256 `34ea6bf9aefea43c529f2a38b4abd69956b07fc1ab63fcc616934ad105f0b6db`）指出：持焦撤销按钮被后台 ListShares 错误分支卸载后，焦点落到 BODY。原 132 项验收没有覆盖该场景，不能用原绿色代替本项修复；原工件和报告均保留。

本次只改 `pack-shares.tsx`、本页验证脚本和本文。稳定的列表 callback ref 在列表真正卸载、且活动元素仍属于该列表时，才将焦点交给既有「已有分享」标题。成功查询、普通每秒渲染，以及焦点位于列表之外时不移动焦点。不恢复旧撤销动作、不改 Unknown/CAS/mutation 次序或 token 处理；没有修改 Go fixture、共享代码或依赖。

持久定向入口：`scripts/verify-packs-ui.mjs --focus-regression`，其余必需参数沿用原脚本。每轮真实等待 31 秒超过既有 30 秒 staleTime，再用 visibilitychange 触发真实 TLS/Connect/SQLite ListShares；仅 fixture 读取故障注入，不改时钟、查询缓存或路由成功结果。

- RED：原冻结 fixture 上实际读取 1 次、unknown=true、旧撤销动作数 0、写入计数不变，而 activeElement=BODY，断言退出 1。
- GREEN：新生产 Web build 嵌入原 Go fixture 后，持焦动作在后台错误时交给 H4；语言偏好持焦时后台错误不抢焦；成功后台查询保留原撤销按钮焦点。两次显式 Tab/Enter 读取恢复均只增加 1 次 ListShares，CreateShare/RevokeShare/UpdatePack/DeletePack 计数不变。
- `web build`、fixture 定向编译、Node 语法检查及本入口均退出 0；5 张最终无 token 截图，收据含 5 个定向观察。未重跑原 132 矩阵、全仓 Go、交编或备份。
- 初次新增探针误用原 helper 的默认 Enter，打开了确认框；保留为 harness-key-red，不作为产品 RED。改为仅 Tab 定位后，才取得上面的有效 RED→GREEN。

本次证据根 `/tmp/cognate-task3-focus.yU0g3zFs/evidence`，有效收据为 `red/receipts.json` 与 `green/receipts.json`。这是原作者修复与专项证据，待 Zeno 限定独审，不自行改写 Spec/Quality 结论或宣称完整 GA。

## RED → GREEN 与证据

私有 archive 副本：`/tmp/skillhub-packs-ui.SyEgTO`。证据根：`/tmp/skillhub-packs-ui-evidence.SyEgTO`。

- `red-final/receipts.json`：原生产 Web 上点击刷新，真实 `ListPacks` 失败后 `#pack-name` 从草稿变为不存在；断言 RED。`red-final/before-detail-dark-zh.png` 为原真实基线，`red-final/failure.png` 为失败现场。
- `green-slice/receipts.json`：最小修复后相同断言 GREEN，然后再打磨。
- `full-v5` / `full-v6` 保留删除后焦点回 body 的 RED；Radix `onCloseAutoFocus` 指向稳定标题后 GREEN。
- `full-v8`、`word-diagnostic`、`full-v10` 保留长英文正文/标题和确认标题拆词的 RED，后续以真实 Range 几何修复，不用隐藏溢出、缩小文字或 force-click 绕过。
- `full-v11/receipts.json`：132 条证据，68 组页面/文本几何，56 张无秘密截图，`failure: null`，fixture `exit.code: 0`。最终重建与脚本复验见 `acceptance/receipts.json`；只认可该目录作为冻结版本的最终浏览器证据。

真实验收链：生产 `npm run build` → 嵌入构建资产的 Go TLS `NewWithOptions`/Connect 处理器 → 真实 Pack/library/distribution 服务 → 私有 SQLite 与真实归档扫描/缓存。仓库上游仅提供确定的 synthetic 归档字节，不是 GitHub 账号验收；业务成功没有 route mock 或假 200。故障只在 fixture 控制口注入：读取失败、延迟，以及真实处理器已提交后丢弃回执。

已覆盖：实际创建/编辑/删除、跨仓库重合选择及 manifest 去重、CAS 冲突保留输入、分享一次显示/隐藏、过期、撤销、缓存读取失败/恢复、提交后丢回执核对与写入次数不增加。中英 × 深浅 × 1440/375/320，窄屏 100%/200% 根文字；编辑器、详情、已有分享合计 60 组，确认框另 8 组 200%。逐字 Range 检查可见 UI 英文单词不拆行，opaque 仓库标识/哈希/分享 ID 允许分隔换行。真实 Tab/Space/Enter/Escape 揭示完整动作并恢复焦点；还验证系统主题变化、指针弹层入场、键盘及 ReduceMotion 无入场。

最终截图示例（均位于 `acceptance/`）：

- `shares-en-dark-1440-100pct.png`、`shares-en-light-1440-100pct.png`：实际分享记录、修订与危险区。
- `editor-en-light-320-200pct.png`、`shares-zh-CN-dark-375-200pct.png`：文字放大后的真实窄屏。
- `confirm-en-light-320-200pct.png`：实际键盘滚动揭示且完整可见的确认动作（viewport 图）。
- `cached-detail-error-draft-preserved.png`、`lost-update-receipt-preserved-no-replay.png`：真实失败保留输入。
- `empty-after-lost-delete-reconciled.png`：真实提交但丢回执，显式核对后的空列表。

截图函数拒绝可见新令牌或正在签发令牌的页面；不记录 RPC 正文、Authorization、密码或分享 token。manifest 验证只返回状态码/去重数量，token 只留浏览器内存并随即隐藏。fixture readiness 为私有临时文件，收尾删除；控制口需要 capability 且拒绝 Origin/query。所有测试仅用 synthetic 账号/内容，没有读取真实账号或凭据。

## 复现

在独立 archive 副本执行，不要把 fixture 暴露到公网。使用主线已存在 `node_modules` 的只读意图链接，不安装依赖：

```bash
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
(cd web && npm run build)
CGO_ENABLED=0 go test -c ./internal/server/httpapi -o /tmp/packs-ui-fixture.test
node scripts/verify-packs-ui.mjs \
  --fixture-bin /tmp/packs-ui-fixture.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /usr/bin/google-chrome-stable \
  --output /tmp/packs-ui-browser-evidence
CGO_ENABLED=0 go test ./internal/server/httpapi -run '^TestPackRPCManagementAndDistribution$' -count=1
CGO_ENABLED=0 go test ./internal/server/packs -run '^Test(SignedShareManifestAndStreaming|ShareForgeryExpiryAndStreamRevocation)$' -count=1
```

Go 1.27.3 / Node 26.12.0。Go 编辑前完整读取 Modern Go Guidelines `list --go-version 1.27`，使用 v2 JSON、`testing.T.Context`、`strings.CutLast`、`new(value)`。没有 race/cgo、全仓测试、全平台构建、npm install、commit、push、新分支或 worktree，也未启动其他 agent。

## 行动记录与未验边界

顺序：完整计划/权威有关段/指定技能 → 固定基线私有 archive 与 Nowledge 定向检索 → 真实失败 fixture/RED → 最小切片 GREEN → UI 与语义/焦点打磨 → 实际业务、异常回执和矩阵 → Web 重建、专项 Go 与最终浏览器复验 → 七文件身份/补丁可应用/源与证据摘要冻结。中间失败不是最终验收证据；详细回执保留在上述临时目录。

作者已做 spec/质量自检；按本任务“不启动 agent”约束没有另起独审，需主线程接收冻结工件后独立 review。未验：真实 GitHub/OIDC 部署、真实用户/读屏器/触屏设备、Chromium 以外浏览器、浏览器原生页面缩放（此处为 200% 根文字）、其它操作系统、MyGo、客户端实际安装/持续同步、容量极限的浏览器交互、配置附件消费/自动应用与完整 F5/F12。Go 原有所有者/分发专项不替代这些运行证据。`crapper`/`dryer`/`mutator` 本机未找到，不安装工具或扩大为泛化审计；不声称突变测试或复杂度门禁通过。

## 主线程接收与 F1 限定复审

原七文件与 F1 三文件增量均已核验摘要并集成。增量 patch SHA256 `1cf159ac591fe81998e6d6dd3ea2057fb851f203332d902c22cb800277674608`；Zeno 限定复审为 **Spec ACCEPT / Quality ACCEPT / Overall ACCEPT，F1 CLOSED**，无新增 finding。报告 `/tmp/cognate-task3-f1-rereview.WrN3BQV5/delivery/REVIEW.md`，SHA256 `4011b2f308bdc6f8e2d343701d4b332e1dd108ad37059dc4c274ac1ea81ac489`；原 BLOCK/RED 不改写。

最终冻结组合的生产 Web 与重新嵌入资产的真实 fixture 再次通过完整专项：132 条证据、56 安全图、fixture code0/signalnull。另一次 `--focus-regression` 通过五项实际焦点/恢复观察及六条场景证据，后台错误/成功均不增加业务写入，未知状态不恢复旧动作，也不抢列表外焦点。证据 `/tmp/cognate-integrated-release.JWDjDE0I/final-packs-ui-browser`、`final-pack-focus`。以上不关闭配置附件、原生设备同步或完整 GA。
