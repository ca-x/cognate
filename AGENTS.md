# Cognate 项目约定

完整需求以 `docs/REQUIREMENTS.md` 为准；当前开发状态见 `docs/TODO.md` 和
`docs/MAINTENANCE-HANDOFF.md`。不要把某个组件的发布当作全部产品验收通过。

## 前端与交互

用户指定：后续涉及前端开发、样式调整或交互打磨时，优先读取并尽量采用以下本地
skill 的规范与技巧：`frontend-design`、`ui-ux-pro-max`、`emil-design-eng`、
`apple-design`、`animate`。具体约束和既有设计记录见 `docs/UI-DESIGN.md`。

- 沿用既定技术栈和成熟框架组件，不为视觉效果另造基础控件或更换框架。
- Web 沿用 React/TanStack/Radix；桌面沿用 MyGo 原生组件，不改成 Web UI。
- 动效服务于操作反馈、状态与空间关系；不延迟高频操作、输入或键盘响应。
- 保留主题、多语言、窄屏、键盘焦点及 reduced-motion；适用时用真实截图和
  交互验证检查成品，不以代码构建通过代替视觉与可用性验收。
