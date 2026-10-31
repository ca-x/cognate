# 登录与账号 UI 切片

日期：2026-10-31。基线：`59da539509f6d7aa10c28f652b72c879157be63c`。
仅覆盖 Web Task 1；不代表原生 MyGo、真实 OIDC 部署或完整首发签收。

## 范围与协议

- 保留 `AuthScreen`、`AccountPage`、React/TanStack Query/Router、既有 Radix 偏好和 Button/Input。没有增加依赖，没有改全局 CSS、路由、英文总表或生产 Go/API。
- 注册继续严格执行 Register → Login → GetSession；GetSession 成功之前不设置前端 session。注册已成功但后续失败时保留登录模式和表单，不自动重放注册。认证请求期间保持字段只读、防止重复提交，密码不回显。
- OIDC 登录使用原始 StartOIDCLogin；绑定仅在账号页显式发送 `linkCurrentAccount: true`。不按邮箱合并、不转移其他账号的身份。
- **协议缺口**：GetSession 只提供用户标识，没有身份来源或已绑定身份清单。页面展示会话显示名称与真实用户标识，并明确“不是已绑定清单”；`Available` 仅指服务启用了 OIDC，不表示账号已绑定。没有增加 RPC 或伪造身份状态。
- 查询初始 loading、确实未启用、可用、错误分别呈现。后台刷新失败时即使缓存仍存在也显示“状态未知”，隐藏缓存绑定入口，提供显式重试；失败不被当成未启用。账号页可主动刷新设置，不必等缓存过期。

## 设计与动效

| Before | After | 原因 |
| --- | --- | --- |
| 登录方式查询失败静默隐藏 OIDC | 始终保留登录方式区域，显示错误与键盘重试 | 不把未知伪装成未启用；本地表单仍可使用 |
| 深浅模式都保留深色品牌块 | 使用共享主题材质与细边界；流程图保留语义而非假状态 | 形成克制的控制台层级，不增加装饰或假统计 |
| 账号只有 OIDC 卡片和用户名 | 会话身份、稳定用户 ID、授权边界和服务状态分层 | 区分当前身份与尚未暴露的绑定状态 |
| 长按钮继承 nowrap | 切片内按钮整词换行，标识符单独允许断行 | 320px 与 200% 文字下仍可操作 |

按 `ui-ux-pro-max` 的错误恢复/可访问性查询，结合 `emil-design-eng`、`apple-design`、`animate`、`animation-vocabulary`：本切片不添加入场、循环装饰或 loading 旋转。只有既有 **Feedback**（按压反馈）和 Radix **Origin-aware animation**（偏好菜单）有用途。复用共享 `--motion-feedback: 150ms`、`--ease-out`，不新建曲线；键盘无按压位移，ReduceMotion 关闭 transition。状态和输入立即响应，不用动画延迟输入或查询。错误摘要可键盘聚焦，不重挂载表单。默认平台字体；图标均为 Lucide。

## 真实验收

`auth_ui_browser_fixture_test.go` 使用独立临时 SQLite、真实 auth.Service、TLS HTTP/Connect、生产 `web.Handler()` 嵌入的 Vite 产物。两套生产 handler 切换真实 OIDC 未启用/启用状态。测试 OIDC manager 只向本地回环 issuer discovery/authorize 发请求；该 issuer 只返回明确的取消，不发行 token，也不模拟业务成功。

错误/慢响应仅在测试 HTTP 边界注入，不替换正常业务成功。fixture 回执只记录 RPC 方法和绑定布尔值，不记录请求正文、密码、cookie、Authorization 或 token。控制面单独监听回环地址，要求随机 capability、无 Origin/查询串；准备文件 0600，仅为合成凭据。浏览器命名会话与临时 HOME 不复用真实个人 profile。

### 可复现命令

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:$PATH
export CGO_ENABLED=0
(cd web && npm run build)
go test -run '^TestAuthUIFixtureTLSAndControls$' -count=1 ./internal/server/httpapi
go test -c -o /tmp/auth-ui-fixture.test ./internal/server/httpapi
node scripts/verify-auth-ui.mjs \
  --fixture-bin /tmp/auth-ui-fixture.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome \
  --output /tmp/auth-ui-browser-evidence
```

验收脚本通过实际浏览器输入/点击/Tab/Enter，只有偏好初始化、文本缩放和几何读取使用 evaluate；不在浏览器模拟 RPC 返回值，不把 RPC mock 当业务成功。

`--phase before` 使用相同专项，在只有新增测试而没有 UI 修改的生产基线构建上保存 before 截图，然后必须因缺少明确登录方式错误失败。最终证据见工件中的 `red/report.json`、`green/report.json` 和 `receipts.json`。运行中间失败证据不冒充最终绿色结果。

### 2026-10-31 最终结果

- `npm run build` 通过；Go `TestAuthUIFixtureTLSAndControls` 与既有 `TestAuthHTTP` 专项通过；`gofmt -l` 无输出、`node --check` 通过。所有 Go 使用显式工具链 PATH 与 `CGO_ENABLED=0`。
- 两个独立 RED 均已观察：基线隐藏登录设置错误；第一轮作者实图自审发现绑定失败丢焦点。最终生产重建后的专项输出：`PASS auth UI: 8 checks, 49 screenshots, 32 layouts`，fixture 正常退出 0。
- 32 组 = 登录/账号 × 中英 × 深浅 × 320/375px × 100/200% 文字。全部无横向溢出、无越界控件/文本；实际 Tab 与必要的 PageUp/PageDown 揭示完整动作和焦点环。账号矩阵实际 Enter 触发失败并保持按钮焦点，不是只截焦点样式。
- 另含真实注册 → 登录 → GetSession 顺序、错误密码、GetSession 失败不建立 UI 会话且不自动重放、退出、登录 pending 锁定、OIDC 未启用/启用/Start 失败/本地 issuer 取消、缓存失败隐藏旧绑定入口、显式绑定位、草稿/偏好/焦点保留、跟随系统实时切换与 ReduceMotion。持久存储只含主题和语言键。
- 密码输入在每张截图前断言为空。最终绿色 49 张截图、基线 RED 6 张截图，以及 focus-RED 报告分开封装；fixture 的私有 ready.json、临时 HOME、cookie、数据库、浏览器 profile 和测试二进制不进入证据包。
- 作者自审已核看深色桌面、中文 375px、英文浅色 320px/200% 两页的真实焦点截图，以及带缓存的错误状态。没有启动独立 reviewer；主线程仍需独审，不以本报告替代审查。

### 行动记录

1. 完整读取主线计划的 Global Constraints、Review Focus、Task 1，读取需求 F3/F11 与 UI 设计约束/逐页账本；定向 Nowledge thread 搜索无匹配结果，遵循当前文档而非猜测旧决策。
2. 用 `mktemp` + 固定 commit 的 `git archive` 建私有副本，仅链接主线现有 node_modules；没有修改主线、创建分支/worktree、安装依赖、启动子 agent 或提交/push。
3. 修改 Go 前完整运行 use-modern-go `list --go-version 1.27`；fixture 使用 json/v2、testing.Context、typed concurrency 与现代 API。没有运行 race/cgo。
4. 先新增真实 fixture/浏览器专项并运行生产基线；确认登录方式失败静默隐藏的 RED。再修复反馈与页面层级，先通过 Web build 和 fixture 机械专项。
5. 调整验收脚本的既有英文 loading 文案及 30 秒缓存刷新触发；明确使用用户刷新而非假定 focus 必然绕过 staleTime。Home 的浏览器原生平滑滚动需结束后再 Tab；字号/viewport 变更后必要时用真实 PageUp/PageDown 揭示焦点并记录步数，不用 JS 强制滚动/聚焦制造成功。矩阵保存真实可见区域截图，另保留 320px/200% 全页概览。
6. 实图自审发现绑定失败后原生 disabled 会使焦点离开按钮；新增键盘专项看见 `binding failure lost keyboard focus` 的 RED。以守卫 handler + aria-disabled 保持原 Button 的可聚焦身份，刷新/重试始终保留同一个按钮，不靠 effect 抢焦点。矩阵逐项真实 Enter 触发绑定失败并断言焦点保持；最终结果和 SHA256 见工件。
7. 工具 `crapper`/`dryer`/`mutator` 不在 PATH，未安装或声称运行；遵循 ub-mode 最小切片、RED→GREEN、逐步验证与行动记录。

## 未验边界

### Dewey I1 / M1 限定修复复现

- I1：汇总报告前统一要求 fixture outcome 存在、`code === 0` 且 `signal === null`；非零退出、signal、等待超时均写入 `passed: false` 和错误，并使顶层命令非零退出。保留原始终止结果，不以杀进程等同正常结束。
- CI 可独立调用 `node scripts/verify-auth-ui.mjs --self-test --output /tmp/auth-terminal-self-test`。四个子进程覆盖正常、非零、signal 和 null 超时，逐项检查同一个生产报告出口的 JSON 与实际子进程退出码；这是有界终止契约自测，不冒充四次真实 Go 崩溃或业务成功。
- M1：账号和登录页均只在被移除的 OIDC 按钮实际持焦时，把焦点交给既有稳定刷新按钮。后台读取失败仍隐藏过期入口并显示未知；不重新开放旧设置，不从用户名或偏好控件抢焦点。稳定 React ref cleanup 在卸载前判断焦点，无全局焦点 effect。
- `--focus-only` 配合原有三个 binary 参数与 `--output` 可运行限定浏览器回归：真实等待查询超过 30 秒 staleTime，触发后台读取失败，覆盖登录/账号 × 原按钮/其它控件持焦四案，再真实 Enter 或点击显式恢复，并断言没有 StartOIDCLogin 自动调用。没有浏览器内 RPC mock。
- 本次只复跑终止契约、限定焦点专项和 Web build/fixture 两个 Go 专项；原 49 张矩阵证据不冒充本次重跑。修复工件需由 Dewey 限定复审，本作者不对自己的修复出独审 ACCEPT。

- 不读取真实账号/凭据，不访问外网 OIDC/GitHub；没有证明真实 provider 的签名 token 登录或成功绑定。取消/失败链路走本地真实 OIDC manager，不能替代生产端到端身份签收。
- 不修改或验证已有可信网关身份头配置、身份解绑/枚举、管理员初始化、device flow 或会话跨设备管理。
- 文字 200% 是浏览器中根字号 200% 的 text-resize 检查，不谎称是操作系统字体或浏览器工具栏缩放；320/375 是真实 CSS viewport，截图不是浏览器 zoom 后的推断。
- 无屏幕阅读器、物理触摸设备、跨浏览器、MyGo 原生或全仓/交叉平台构建证据。仅专项与 Web build；主线程负责七文件组合验证和独立 spec/quality 审查，作者自审不替代独审。

## 主线程接收与限定复审

原七文件及 I1/M1 四文件增量均已核验摘要并集成。增量 patch SHA256 为 `1bc834c6788542f9b7a3be7ad18d2c3461d2b19ad9633eeb3576fa016a9e6ae4`；Dewey 限定复审为 **Spec ACCEPT / Quality ACCEPT / I1、M1 CLOSED**，无新增 finding。报告 `/tmp/cognate-task1-auth-recheck.H09Suta7/REVIEW.md`，SHA256 `824ac25d2b7fcd36875115ecba3b77fbfdcb8fcb6ead14f0d9ecbaacf9acdd73`；原 BLOCK 报告不改写。

主线程以包含来源 N1、备份锁修复及三条 UI 切片的新 Web 构建重编 fixture，完整登录/账号浏览器专项再次通过：8 项场景检查、49 张安全截图、32 组布局，fixture code0/signalnull；四案终止契约自测通过并接入 Build workflow。证据 `/tmp/cognate-integrated-release.JWDjDE0I/auth-ui-browser`。本页签收不是外部 OIDC、原生客户端或完整 GA 签收。
