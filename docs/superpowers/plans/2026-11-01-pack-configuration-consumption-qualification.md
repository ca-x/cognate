# 完整十四agent / GUI / 平台义务

源：固定archive `docs/REQUIREMENTS.md:111/:142/:175`、`FORMAL-RELEASE-AUDIT.md:64/:104`、`CCSWITCH-CONFIGURATION-MATRIX.md`。该矩阵不是“只有native enum三个值”的缩表。每一个cell分别需要typed asset、planner/host验证、真实native消费者/格式、Pack三阶段、local CAS/备份/恢复、CLI/MyGo与D5脱敏投影/设备审批、实际OS资格；依赖缺失显示具体Unavailable，不授假Available。

| agent | 不可借别的agent代替的合同 |
| --- | --- |
| claude | 独占/current保护、未知env/permissions/hooks保留；provider与.claude.json MCP/GEMINI等别域，MCP进程声明不等于可执行插件；Pack后台另测 |
| claude-desktop | 独立Desktop provider/model mapping、MCP结构/路径和真实app行为；不是Claude Code别名 |
| codex | 官方账号/API分离、responses/effective ModelInfo、三文件baseline/观察原期限/late conflict；C6唯一已资格Linux/amd64 fixed0.166.0 tuple不等于GUI/Pack自动资格，registry不改 |
| gemini | env/settings双文件、OAuth/API边界、GEMINI.md与MCP分离；保留已批准Target3/slot4/41/response6；Harvey手动native与C7后台/GUI分别签收 |
| grokbuild | 独立TOML/default托管模型引用、env key/name、MCP headers；未知全局保留；不借Web资产冒native |
| opencode | 共存add/remove、SDK只声明、options/variants/cost/headers、schema1/2分开、OMO独立；无任意SDK安装 |
| openclaw | primary/fallback与provider分离、agents/env/tools、Workspace/Daily Memory；上游MCP N/A不可强造，但不把整个agent标不支持 |
| hermes | built-in/custom key、models/rate limit、Memory/SOUL.md各域；安全投影专用codec |
| pi | Enable/Remove共存、API/provider/model、MCP/AGENTS/SYSTEM/templates；不抢不相关profile |
| mcode | 只改custom节点、默认引用保护；不套OpenCode文件格式 |
| kimi | 原生default_model与OAuth保护；不能一概清current |
| dsh | 独立agent/apiKeyEnv/DSH_HOME、局部YAML override，不接受tags/anchors注入 |
| agy | 官方临时启动参数/第三方私有目录启动与MCP分开；静态文件成功不等于应用/后台可重放 |
| omp | Pi-family交互但独立YAML/profile/discovery，不抢建文件破坏上游迁移 |

普通share/匿名、GitHub凭据、F10 token从不变agent secret。prompt/workspace/router/stack等对应C5 typed门逐项消费，不用generic JSON/source文本当codec。上游明确单项N/A可据来源标注，未实现不是上游N/A。

D5 Web为recipient显示设备真实typed reported diff/冲突/状态/恢复请求、device审批进度；owner只见自己grant，不看跨owner设备数据。MyGo/CLI用同一nativeconfig内核和完整设备可信UI。主题/语言不丢非秘密草稿，秘密/审批按identity和关闭清理，真实keyboard/focus/dirty/cancel、375px与浏览器200% zoom/reduced-motion均为产品门。

设计完整、元数据可编辑或少数消费者通过均不能关闭十四agent或跨平台总门。当前C7没有任何新cell资格执行收据，非Linux未借交叉编译或已成功key core冒本机应用资格；未宣称完整F12/C7/GA。
