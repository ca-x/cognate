# Grok provider Web Implementation Plan

> 主线程按 executing-plans 执行，不新增子 agent。沿用需求 F12、Grok 服务端/G1/G2 合同及现有 React/Radix/TanStack 表单，不替代完整十四类 agent、MyGo、导入或本机应用。

## 本轮合同

- 创建、重开、编辑、无密钥复制、按版本删除和现有排序入口均识别 Grok Build provider schema 1。
- native-login 不带 model 或服务器密钥；custom-model 分开编辑 profile、upstreamModel、HTTPS endpoint、name、responses backend、1..9007199254740991 的十进制 contextWindow，以及可选 envKey。保留字段原文与 absent，不静默 trim 或用浮点数处理 int64。
- key 只有 grokbuild.api-key 槽，Keep/Replace/Clear 独立；Clear 和模式切换显式确认。未知持久字段只读且禁止复制；回执校验完整 provider、revision、名称及 key presence，异常回执进入既有 Unknown 禁重放。
- 新凭据只存在未受控 password DOM/ref，不进入 React 文本状态、预览或持久缓存；切语言、提交结束或卸载时清空。沿用请求取消、CAS、未保存离开确认、键盘焦点和主题行为。
- 沿用 Radix 既有反馈，不增加动画或依赖；手机单列、宽屏双列、逐字段错误和焦点、中英翻译均随功能交付。

## 文件与验证

- [x] 先用现有 requireConfigurationView + 已生成 Grok protobuf 构造合法服务端回执，取得普通断言 RED，不能以新模块缺失的编译失败冒充行为 RED。
- [x] 新增 configuration-grok-state.ts 和 configuration-grok-form.tsx；只把 Grok 分支接入 configuration-state.ts/configuration.tsx，保留其它 agent 行为。
- [x] 补 locales/en.ts 与 configuration-order-i18n.ts 的范围描述，不改变历史预览标签。
- [x] scripts/verify-grok-web-state.mjs 复用现有 rolldown 做类型、presence、边界、只读和回执验证；不引入测试框架。
- [x] 复用私有浏览器 fixture 的真实 auth/TLS/SQLite、控制能力与关闭机制，补 Grok 持久字段/key 布尔观察；不接触实际账号或 keyring。通过 agent-browser 验证完整操作、丢响应/冲突、主题与英文/中文、窄屏键盘及清理。
- [x] 类型检查、Web 构建、受影响 Go fixture 测试、必要组合验证后提交并 SSH 推送。只把实际通过的项写入验证记录，MyGo 等未交付部分继续留在 TODO。
