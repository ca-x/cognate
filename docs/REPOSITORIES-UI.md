# 仓库与分类 Web UI

日期：2026-10-31。Task 2 的独立切片，基线为 `59da539509f6d7aa10c28f652b72c879157be63c`。仅限定七文件，不包含主线程来源修复或其它 UI worker；不代表首发或 MyGo 原生验收完成。

## 设计与行为

| Before | After | 原因 |
| --- | --- | --- |
| 仓库列表后台失败会卸载详情，丢失技能筛选 | 错误与未知状态独立显示，保留暂存记录、所选详情和筛选 | 缓存不是当前成功，也不应造成草稿丢失 |
| 仓库和分支层级相近，选择只靠颜色 | 明确所选标记、独立分支与完整归档 SHA-256 | 区分来源候选、收录记录和经过校验的快照 |
| 分类筛选使用原生 select | 复用项目 Radix Select 封装 | 保持成熟组件的键盘、弹层与焦点语义 |
| 分类重命名冲突后仍可重复提交旧版本 | 保留原始 CAS，单次尝试后关闭并重新读取；丢回执不重放 | 失败不等于未提交，不在局部 UI 中猜测新 revision |
| 删除后原触发器消失 | Radix `onCloseAutoFocus` 落到稳定页面标题 | 不自研焦点陷阱或将焦点丢给 body |
| 200% 长英文受共享 nowrap 与嵌套留白挤压 | 切片内按钮整词换行、窄屏缩减留白、发布状态换行 | 不隐藏重要动作，不靠缩小文字或隐藏横向溢出过关 |

仍复用 React、TanStack Query/Router、现有 Button/Input/Dialog/Select。没有新增依赖或基础控件；`sources`、PublicationControl、SkillCategories、ImportRepositories、RepositoryRefreshControl 只读。CSS 只在本切片或其活动页面弹层生效，英文增量自行注册，不改共享 CSS、`en.ts`、路由、API 或生产 Go。

公开状态仍由 PublicationControl 的真实查询提供。收录默认不公开，发布需要原有完整目录确认；取消公开不承诺收回已经发出的内容。私有来源文案描述服务端 GitHub 授权条件，不声称 GitHub 公开就等于已发布，不要求客户端提供或显示 GitHub 凭据。

频繁筛选、数据刷新不增加动画。沿用共享按压反馈与 Radix 入场：Press / Tap feedback、Fade in；使用现有 easing/duration 和输入模式规则。无新增循环、假进度、退出阻挡、手势或动效依赖；键盘与 ReduceMotion 不增加位移。

## 真实专项验收

`repositories_ui_browser_fixture_test.go` 启动隔离 SQLite、真实生产 Go HTTP/Connect 处理器和 TLS，并嵌入生产 Web build。只有上游 GitHub 归档为合成传输边界，返回实际 tar.gz，经生产下载/扫描/缓存/事务路径收录。浏览器不拦截或 mock RPC 成功。故障模式仅在持 capability 的回环控制面中提供；公开服务不暴露控制凭据。`lost` 模式先执行真实写入，再丢弃成功回执；完整性测试实际移除 fixture 归档对象。

所有用户名、密码、分类与技能都是 synthetic。密码和 capability 仅位于权限 0600 的临时 readiness 文件中，截图在登录表单卸载之后拍摄；交付证据排除 readiness、HOME、会话及控制凭据。

专项覆盖：

- 真实空资料库、收录 loading、技能读取、搜索空结果；两仓库真实选择、独立分支和技能内容 hash。
- 列表与详情有缓存时的失败、未知状态、重试及筛选保留；分类查询有缓存时不伪装最新计数。
- 真实分类创建、取消、重命名、CAS 冲突、关联分配与筛选、删除；删除后技能仍存在。
- 先真实提交再丢回执，界面不成功、保留草稿，单次请求且禁止旧弹层重放，显式重新读取对账。
- 公开 opt-in、未确认不可发布、取消确认不发布、真实发布后的匿名元数据、撤销后不可再浏览；源刷新上游故障保留原快照。
- 外来仓库和分类 ID、跨 owner 分类关联及匿名 owner RPC 的真实拒绝。
- 不存在来源不入库，停止等待提示结果可能已提交；归档实际缺失返回 DataLoss，而非空技能或更新成功。
- English/中文、深浅、跟随系统；1440px 与 320/375px 的 200% 文字。布局检测元素、文字范围、英文整词及横向越界；真实 Tab 滚动揭示完整保存按钮，Tab/Enter 取消并恢复触发器焦点；删除后稳定标题焦点。ReduceMotion 实际 media query 与弹层 computed animation 验证。

复现（仓库根目录）：

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
npm --prefix web run build
CGO_ENABLED=0 go test ./internal/server/httpapi -run '^TestRepositoriesUIFixtureGuards$' -count=1 -v
CGO_ENABLED=0 go test -c ./internal/server/httpapi -o /tmp/repositories-ui.test
node scripts/verify-repositories-ui.mjs \
  --fixture-bin /tmp/repositories-ui.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --output /tmp/repositories-ui-evidence
```

先 build 再编译 fixture；Go 的 `web` embed 必须来自同一份生产输出。`--phase before` 用于原始基线：在收录和技能筛选后注入 ListRepositories 失败，必须在“缓存列表失败仍保留筛选”断言处 RED；不能将原始基线意外通过当成成功。

## RED → GREEN 与边界

### Lorentz F1 / F2 限定修复

独审 `/tmp/skillhub-task2-independent-review-8eNP2znD/REVIEW.md`（SHA256 `ec2cb31868a9d9022210fb090c56e41871ce3d2837cce379026b30e084d00a0a`）对原交付提出两个 P2，原判定仍为 REQUEST CHANGES，不被作者自验覆盖。

- F1：删除在 SQLite 已提交但丢回执，自动对账卸载分类行及 Dialog。删除错误现在由页面保留，不依赖该行存活；仍显示未确认，不因列表已无此项而宣称删除成功。自动刷新不清除此错误，只有用户显式重新读取成功才清除；读取失败仍保留。没有新增写入重试，也不修改 `expectedRevision` / CAS 或一次性 attempted 门禁。
- F2：后台读取失败时创建按钮可能变为 disabled。分类编辑与删除的 `onCloseAutoFocus` 共用页面内的回退：触发器消失、断开、disabled 或删除已收到成功回执时，落到稳定的 `categories-title`；有效触发器仍交由 Radix 原逻辑恢复。没有新的 trap、基础控件、焦点计时器或共享样式改动。
- 永久探针加入既有专项脚本，`--phase focus` 可单跑；默认专项也包含它。相同脚本先跑原 Task2 冻结 Web，再跑修复 Web，两组分别为 English/light/320px 与 中文/dark/375px，均为 200% 根文字并启用 ReduceMotion。每组均验证真实删除丢回执后的持续错误、稳定标题焦点、单次删除请求；后台分类错误保留草稿、Tab/Enter 取消落点且零创建请求；有效触发器取消及正常删除为对照。
- 后台失败通过等待既有 30 秒 staleTime 到期，再派发窗口 visibilitychange 触发正常 refetch；31 秒等待仅为探针的缓存时效条件，不是 UI 或业务重试修复。没有修改 QueryClient、伪造 RPC 成功或把 DOM 消失当成真实提交：仍用独立的生产 ListCategories 读取核对。

单跑复现（先按前文 build 并编译同一份 Web 的 fixture）：

```sh
node scripts/verify-repositories-ui.mjs \
  --fixture-bin /tmp/repositories-ui.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --phase focus --output /tmp/repositories-ui-focus-evidence
```

本修复只改原七文件中的 `categories.tsx`、专项脚本及本文档；fixture、其它页面、CSS/i18n 和所有生产 Go 均未变。冻结增量相对原 Task2 交付，不应直接应用到固定 Git 基线。命令终态、RED/GREEN 收据与安全截图由本次修复报告和哈希清单绑定，须交 Lorentz 限定复审后再由主线组合验证。

原始 RED 保留于 `/tmp/skillhub-task2-red`：真实收录成功后，缓存列表失败使技能筛选输入变成 null。中间 RED `/tmp/skillhub-task2-green-v2`、`/tmp/skillhub-task2-green-v3` 保留 320px/200% 嵌套布局溢出；修复页面内留白、发布状态与 grid 最小宽度后过关。另有验收驱动修正：浏览器 CLI 清空字符串没有产生 React 状态变更，改用真实 Control+A/Backspace；发布对话框取消按钮真实名称为 Back，而非 Cancel。没有 force-click、延迟输入或 RPC 假成功。

最终收据、截图、构建与专项日志随冻结工件交付；以该工件 SHA256 为准。作者自审不是独立 reviewer 签收，主线程仍须组合验证来源修复与其它 worker。

未验边界：真实 GitHub 私有账号/授权、真实外部网络与限流、最大容量、其它浏览器、物理触屏/读屏、真实设备安装/下载、MyGo、完整产品及首发均未由本片证明。弹层为模态，外部语言菜单在弹层打开时不可操作；已验页面内语言/主题切换保留收录草稿和选择，没有假称在模态内操作背景偏好。仅专项与 Web build，未跑全仓、race/cgo 或交叉编译。未创建 branch/worktree、未提交或 push，未写主线。

## 主线程接收与限定复审

原七文件及 F1/F2 三文件增量均已核验摘要并集成。增量 patch SHA256 `1bc5df80351a7d3ccd327a1ef58312ace8e2e3d415d14c8f1ed3f6716f569020`；原 reviewer 限定复审为 **Spec ACCEPT / Quality ACCEPT / F1、F2 CLOSED**。报告 `/tmp/skillhub-task2-f1f2-independent-q0GuUDjo/REVIEW.md`，SHA256 `80956fee87614d0c277e83322bab913cd34a91c7c18e8cb8c9571dbc9af9fcd8`；原 REQUEST CHANGES 与 RED 保留。

主线程的新组合生产 build/fixture 通过，完整仓库/分类专项再次通过 65 条证据、24 组布局，包含永久 F1/F2 探针，fixture 正常退出。证据 `/tmp/cognate-integrated-release.JWDjDE0I/repositories-ui-browser`。这些是本 UI 切片与既有业务边界的证据，不关闭真实私有 GitHub、其它平台或完整首发门禁。
