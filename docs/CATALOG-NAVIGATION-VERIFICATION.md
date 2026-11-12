# 目录与导航交互验证（2026-11-12）

范围：Web 公开目录与共享工作区导航；参考用户指定的
`davila7/claude-code-templates`。不涉及后端权限、安装/公开语义、导入协议、
数据库、CLI 或 MyGo 原生界面。本轮为源码维护，不创建发布 tag。

## 参考与设计边界

- 参考提交：`0604ce8bbf9464744a60ad610dc8179a90d336d4`；实际浏览
  `https://www.aitmpl.com/skills/`，不只阅读 README。
- 借鉴搜索、分类、视图切换和导航结构；没有复制代码、素材、统计或业务协议。
- 沿用 React/TanStack/Radix、SearchInput、原生 details、蓝色 tokens 和系统字体；
  无依赖、Go、Protobuf 或构建流程变更。
- UIUX 检索先匹配文档站、后匹配通用 P2P 市场，均不适合作为本产品视觉权威，
  未采纳或持久化其设计系统。采用经核对适用的键盘/导航 UX 建议及现有设计约束。
- 目录视图切换不请求 API；筛选/分页保留当前视图。分类快捷项与当前搜索草稿合并
  后明确提交，不改成自动搜索。完整分类、UTF-8、错误和游标边界保留。
- 快速导航只过滤十条本地路由元数据；不是私人技能全文搜索。Ctrl/Cmd+K 不抢占
  composition、其他 Dialog 或 Select；Esc 先清空，再关闭。
- 抽屉复用 Radix 的模态、焦点和键盘行为；关闭即时，键盘和 reduced-motion
  不播放入场；切换桌面关闭抽屉并回焦可见侧栏。

## 最终执行结果

最终 Web 产物经 HTTP fixture 实际嵌入后运行浏览器，不以静态构建代替交互验收。
证据根目录 `/tmp/cognate-ui-templates`，使用隔离 HOME、合成账号和仓库，
没有读取或提交真实备份、token、密钥、fixture 或截图。

| 检查 | 结果与边界 |
| --- | --- |
| `npm --prefix web run build` | TypeScript 与 Vite 生产构建通过 |
| `CGO_ENABLED=0 go test -c -o /tmp/cognate-ui-templates/verification-httpapi.test ./internal/server/httpapi` | 最终 Web HTTP fixture 编译通过；不是全包测试 |
| `verify-public-catalog-web.mjs` | 最终 `final-catalog/receipts.json` 为 pass，111 条证据，fixture exit 0 |
| `verify-workspace-navigation.mjs` | 最终 `verified-navigation-final/receipts.json` 为 pass，34 条证据、29 条布局记录，fixture exit 0 |
| 三项状态脚本 | 导入来源/代理、Grok/OpenCode 导入、备份设置通过；备份脚本 35 checks |
| `CGO_ENABLED=0 go test ./web` | 通过 |
| `node --check` 两个浏览器脚本、`git diff --check` | 通过 |
| 独立用户代表 | 只读复核本轮导航/焦点问题，最终未发现明确源码阻断；浏览器结果由主线负责 |

浏览器使用本机 agent-browser 0.39.1 / Chromium 156。命令形态：

```sh
export COGNATE_BROWSER_EXECUTABLE='<actual-chromium-path>'
node scripts/verify-public-catalog-web.mjs \
  --fixture-bin /tmp/cognate-ui-templates/verification-httpapi.test \
  --browser-bin '<agent-browser-path>' \
  --chrome-bin scripts/chromium-desktop-pointer.sh \
  --output /tmp/cognate-ui-templates/final-catalog
node scripts/verify-workspace-navigation.mjs \
  --fixture-bin /tmp/cognate-ui-templates/verification-httpapi.test \
  --browser-bin '<agent-browser-path>' \
  --chrome-bin scripts/chromium-desktop-pointer.sh \
  --output /tmp/cognate-ui-templates/verified-navigation-final
node scripts/verify-import-sources-state.mjs
node scripts/verify-grok-import-state.mjs
node scripts/verify-backup-settings-state.mjs
```

桌面指针适配器配置真实 Blink hover/pointer 媒体能力，不覆盖 JavaScript matchMedia。
移动 viewport、媒体偏好和键盘验证不等于实体触屏或原生桌面运行验收。

目录覆盖深浅/中英、1440/375px、320/375px 的 200% 字号重排、卡片/列表
无额外请求、分类与草稿组合、清空/IME/Escape、分页、错误/独立重试、UTF-8、
重复游标门禁、详情与命令复制。匿名请求仅访问公开 RPC，不带 token，
不下载/安装、不展示私有 fixture 内容。

导航覆盖深浅 × 中英 × 320/375/768/1440px：实际命中和点击移动触发器，
十条导航、当前页焦点、Esc 回焦、同页选择后立即再次打开、切桌面后可见回焦；
页面查找过滤/空态/Tab/Enter/IME/快捷键不抢其他模态、打开与过滤不新增私人 RPC；
另测 `/account`、375px、200% 字号、低动效下当前焦点滚入导航区域及明确退出。

## 前后实图

截图路径相对证据根目录；均已实际查看，不提交二进制素材。

| 场景 | Before | After |
| --- | --- | --- |
| 目录深色中文桌面 | `before-public/explore-dark-zh-CN-1440.png` | `final-catalog/explore-dark-zh-CN-1440.png` |
| 目录浅色英文桌面 | `before-public/explore-light-en-1440.png` | `final-catalog/explore-light-en-1440.png` |
| 目录深色中文手机 | `before-public/explore-dark-zh-CN-375.png` | `final-catalog/explore-dark-zh-CN-375.png` |
| 后台深色中文桌面 | `before-workspace-fixed-harness/workspace-before-1440.png` | `verified-navigation-final/workspace-dark-zh-CN-1440.png` |
| 后台深色中文手机 | `before-workspace-fixed-harness/workspace-before-375.png` | `verified-navigation-final/workspace-dark-zh-CN-375.png` |

附加实图：`final-catalog/catalog-list-view.png`、
`verified-navigation-final/workspace-light-en-320.png`、
`verified-navigation-final/navigation-dark-en-375.png`、
`verified-navigation-final/navigation-reduced-motion-large-text.png`。
参考页截图为 `reference-skills-loaded.png`。

## 发现的问题与保留失败

- 生产 CSS 优化会删除抽屉的 `translate:none`，让共享 Dialog 的半幅 translate
  重新生效。使用明确的 `translate-x-0 translate-y-0` utility 覆盖，最终生产浏览器通过。
- 英文 320px 下，leading 的零 flex-basis 让导航/品牌被偏好控件遮住；真实物理命中
  检查失败而非强行点击。改为内容 basis，并让小屏偏好独占下一行，所有控件中点命中
  检查通过。失败证据保留在 `navigation-header-diagnosis`。
- 200% 字号下账号区零 basis 导致用户名逐字符换行、导航被挤没。账号区使用可换行
  内容 basis，用户名单行省略且保留完整文本/title。导航当前项先 focus 再即时滚入
  可视区，最终图与边界断言通过；先前失败保留在 `final-navigation`。
- Radix 挂载首帧可能尚未允许 pointer events，回焦亦为异步。脚本只等待实际
  pointer-events/焦点条件，不覆盖层样式、不固定 sleep 或强制点击；关闭后继续真实点击
  并检查没有残留 modal/body 阻挡。登录 session 仅内存，切主题/语言使用真实控件，
  不以 reload 伪装为保持登录。
- 未修改的 `verify-repositories-ui.mjs` 在旧基线 binary 与本轮 binary 均于
  `Check and update snapshot` 操作失败，原始日志为 `baseline-repository-control.log`
  和 `final-repositories.log`。临时加入就绪等待的控制实验能越过该处、随后 fill 仍失败；
  该脚本已恢复原样，不计为通过，也不据此认定本轮业务回归或根治旧 harness。

本轮没有重跑全仓 Go，不引用上一轮 53 包结果作为本轮成绩。没有执行真实 GitHub
拉取、商店发布、懒猫升级或 MyGo 原生验收。待发布能力与完整 backlog 继续以
`TODO.md` 和 `MAINTENANCE-HANDOFF.md` 为准。
