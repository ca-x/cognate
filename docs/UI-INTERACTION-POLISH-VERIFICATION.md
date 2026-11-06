# Web 交互与 React Bits 动效维护验收

日期：2026-11-06。基线 `9d39f07b73c6b469ce4bba10b4c02f20e71f84ab`。
范围仅 Web 维护；代码验收时尚未创建新的分发，也不改变 CLI/MyGo 资格。
后续已随 Server/懒猫 0.6.2 发布，准确 tag、产物与回执见 `RELEASE-v0.6.2-VERIFICATION.md`。
没有访问真实 cc-switch 数据库或用户备份文件；所有浏览器场景使用本机合成 TLS 上游、
真实服务端/SQLite 与独立测试浏览器，不连接外部 OIDC 提供方或用户 GitHub 账号。

## 变更与边界

- 登录故事区、公开目录介绍和 GitHub 来源卡片采用局部追光；登录流程说明一次性渐入，
  同步图标只在真实请求 pending 时旋转。未给列表筛选、输入或键盘操作增加延迟。
- React Bits 固定来源与精简适配 API 见 `web/src/components/react-bits/README.md`。
  没有 GSAP/Canvas/Three/CDN 或持续 RAF；使用既有 motion/theme tokens。
- 完整原始许可 2,060 字节，source/dist SHA256 均为
  `4bdd1fa59a6b7d5954bc3b213dbc34e455c635a94bbcecf9293553797d0fad02`；
  实际嵌入服务匿名 HTTP 回读核对相同字节，不只是 CI 暂存目录存在。
- 搜索清空后保留焦点，Escape 不破坏 IME；显示可见数/总数与总已选数。
  无可导入结果时禁用批选，确认、通知 ack 与原提交上限不变。
- 备份未改动时不发送保存；显式丢弃确认默认聚焦继续编辑；同 revision 也能重置草稿。
  保存直接采用已确认响应，不追加 GetSettings，先取消旧读取，异常结果不自动重放。
  缓存页面再次挂载不自动读回覆盖草稿，管理员可显式重新读取；CAS 仍以服务端为准。
- 保留原生 required 校验，展开并聚焦折叠区域中的非法字段；凭据不进 React state，
  不回显，不日志输出，提交/重读时清空。没有修改备份运行服务或主密钥校验。

## 本机验证

环境：Go 1.27.3、Node 26.13.0、agent-browser 0.39.1、Chrome 156.0.8078.47。
证据根目录为 `/tmp/cognate-interaction-polish`，不提交测试凭据、私有数据库或截图。

| 项目 | 结果 | 本机证据 |
| --- | --- | --- |
| `npm --prefix web run build` | TypeScript + Vite 通过 | `web-build-final.log` |
| `verify-backup-settings-state.mjs` | 35 项通过 | 周期解析、数值等价、dirty/凭据替换意图 |
| `verify-backup-web-state.mjs` | 67 项通过 | 既有意图和安全状态边界 |
| `verify-grok-import-state.mjs` | 通过 | Grok/OpenCode 预览、分组通知原始顺序和确认 |
| `verify-react-bits.mjs` | 通过 | 原始与构建许可全字节哈希 |
| `verify-auth-ui.mjs` | 10 项、52 张截图、33 种布局通过 | `auth-final-8/report.json` |
| `verify-sources-web.mjs` | 398 项通过，零意外后端错误 | `sources-final-3.log` |
| `verify-public-catalog-web.mjs` | 108 条证据通过 | `public-final-3/receipts.json` |
| `verify-backup-settings-web.mjs` | 28 项通过 | `backup-final-2/receipt.json` |
| `verify-magpie-browser.mjs` | 通过 | `magpie-final-2/report.json` |
| `CGO_ENABLED=0 go test ./web ./internal/server/httpapi ./internal/server` | Web/server 通过；httpapi 有一次 CAS 断言失败，见下文 | `targeted-go-tests.log` |
| CAS 单用例 `-count=5` | 五次通过，未改后端或用例 | `cas-isolated.log` |
| `CGO_ENABLED=0 go test ./internal/server/httpapi -count=1` | 完整该包再次通过，272.040s；不覆盖首次失败 | `httpapi-go-repeat.log` |

浏览器覆盖中英、深浅主题、320/375px、200% 文本、真实键盘操作与 reduced-motion。
追光层不参与点击命中；键盘立即停止装饰，再移动鼠标恢复 hover 但不重播入场。
登录五个流程节点的布局、可见性、有限动画与完成状态有独立断言；CSP 无新增违规。
触摸/笔 PointerEvent 注入不激活追光；移动 viewport 不代表实体触屏硬件验收。
Magpie 使用原生文件后缀、60 项有界预览、筛选/清空/IME、分组确认及真实单次提交；
不将此结果扩大为全部第三方配置映射或用户真实数据导入验收。

已查看登录的稳定态、公开目录与备份确认框截图。例：

- `auth-final-8/login-spotlight-settled-dark-1440.png`
- `public-final-3/explore-dark-zh-CN-1440.png`
- `backup-final-2/discard-zh-CN-light-375-200pct.png`

测试机 headless Chrome 默认报告 hover/pointer:none，不能拿静态降级冒充追光生效。
桌面动效验证使用 `scripts/chromium-desktop-pointer.sh` 配置 Blink 媒体能力；没有覆盖
JavaScript matchMedia，也没有移除域名白名单。先设置 `COGNATE_BROWSER_EXECUTABLE`
为实际 Chromium 路径，再给 auth/sources/public-catalog 脚本传入该 wrapper 的
`--chrome-bin`；backup/Magpie 可直接传原 Chromium 路径。通用运行方式：

```sh
npm --prefix web run build
CGO_ENABLED=0 go test -c -o /tmp/cognate-httpapi.test ./internal/server/httpapi
CGO_ENABLED=0 go test -c -o /tmp/cognate-server.test ./internal/server
node scripts/verify-auth-ui.mjs --fixture-bin /tmp/cognate-httpapi.test \
  --browser-bin "$AGENT_BROWSER_BIN" --chrome-bin scripts/chromium-desktop-pointer.sh \
  --output /tmp/cognate-auth-verification
```

其余脚本沿用自身 `--fixture-bin` / `--browser-bin` / `--chrome-bin` / `--output` 参数；
Magpie 脚本的相同四项是位置参数。测试设置不应用于产品浏览器。

## 独立审查与失败保留

独立 reviewer 发现两个 P2，均先在旧构建获得真实 RED，再修复并跑完整 GREEN：

1. 旧读取晚返回可能覆盖保存成功后的新 revision。修复为保存前取消对应查询，且
   缓存页面回访不发起后台读。实际等待缓存 stale 31 秒后站内离开/返回，确认零新增
   GetBackupSettings，保留已确认 revision。
2. 重载不同 revision 卸载表单后，旧 ref 无法恢复焦点。将 reload 目标 ref 移到稳定
   父组件；第二个真实管理员会话修改服务端 revision，本页确认丢弃后验证新表单、
   新设置、未误保存草稿与键盘焦点恢复。

原始 RED 在 `backup-settings-stale-return-red-1793975948/receipt.json` 与
`backup-settings-reload-focus-red-1793975843/receipt.json`，未删除或削弱断言。
浏览器工具的空字符串 fill/ControlOrMeta 快捷键兼容问题改为真实 Control+A/Backspace，
并明确断言字段为空、required 无效且没有保存；auth 脚本同步了 0.6.1 已更名的 OIDC
按钮文案，没有为测试修改账号业务逻辑。

第一次上述 Go 三包回归中，`TestConfigurationOwnerLifecycleAndCAS/json` 报告
`CAS outcomes: 1 success, 0 conflicts`；该包 215.670s，Web 与 server 包均通过。
本轮未改此 Go 用例或配置后端；随后隔离 `-count=5` 及完整该包复测均通过，仍不能
证明该失败已根治。
保留原输出，不添加自动 retry、放宽期限或减少断言。该记录也不替代远端 CI 结果。

## 续接记忆

nmem CLI targeted search：query `Cognate 备份 导入 界面 动效 0.6.1`，
mode `fast_bm25_vector`，scope `default`，无业务过滤，limit 2。
rank 1 `d72a37c5-fcf1-4535-aaa9-8aeeeecd05ba`（在线备份/导入边界，score 0.99）；
rank 2 `6db8ad1e-1d5a-4792-b7c6-1eaa51763174`（UI 与有用途动效目标，
score 0.8512186540121659）。图形页面的同 owner/Space 授权边界未获确认，因此不自动
打开记忆图。新版本发布、实体触屏、真实懒猫升级与原生 GUI 仍须各自验收。
