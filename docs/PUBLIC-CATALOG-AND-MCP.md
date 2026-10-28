# 公开目录、分类与 MCP 增补范围

来源：用户 2026-10-28 在当前开发会话补充；权威需求同步见 `REQUIREMENTS.md` F8–F10。当前是设计与验收约束，不代表已经实现。

追加 F11：Web 使用浅色/深色/跟随系统主题和简体中文/English 界面，成熟国际化库、非敏感偏好持久化，覆盖匿名页与管理页，不降低 CSP。界面由同一 Web 写入者整合，避免页面文案与全局偏好并行覆盖。

## 能力与顺序

| 模块 | 职责 | 依赖 |
| --- | --- | --- |
| skill-categories | 用户隔离的分类和技能多分类关联 | 已有用户、收录技能 |
| public-catalog | 显式仓库发布、匿名搜索/详情/分类目录 | 分类、已核验的仓库缓存 |
| public-distribution | 匿名 manifest/下载及取消公开核验 | 公开目录、现有安全归档链路 |
| public-client | CLI/MyGo 无 token 安装并复用安装/去重内核 | 匿名分发、正在开发的共享客户端内核 |
| mcp-credentials | 用户自管、限权、到期、轮换和撤销 token | 本地会话和用户状态 |
| mcp-tools | 基于官方 Go SDK 的受控业务维护 | MCP 凭据、既有领域服务 |
| management-ui | 发布/分类/MCP 凭据管理与免登录目录 | 上述 RPC、成熟 React/shadcn 组件 |

顺序：完成本轮并行客户端基础 → 分类/发布状态与 MCP 凭据并行 → 匿名分发/MCP 工具 → 管理界面/匿名目录 → 独立验收。服务端 ent 生成由同一写入者串行执行，不能与 Lists 调度任务同时覆盖生成文件。

## 安全边界

- 新收录和源更新不自动扩大公开范围。发布需展示将公开全 skill 目录的提示；公开下载不附带客户端配置或任何凭据。
- 公开目录是自托管精选库，外部 skills.sh 搜索是发现入口，两者并存。此阶段不臆造下载排行、社区评分或广告系统。
- 分类是用户命名空间内的多对多标注，未分类是筛选状态而非伪造分类行；只返回已公开技能相关的匿名分类计数。
- 匿名和分享分发的授权入口分离，复用归档安全检查与传输完整性代码，不复用“空 token 代表公开”的隐式判断。
- MCP 仅执行声明的领域操作；不开放数据库、文件系统、任意插件脚本、任意 URL fetch 或命令行工具。读/写/发布权限分开，管理 token 的权限不授予 MCP 工具本身。
- MCP 原文只显示一次；轮换使旧凭据失效，撤销和过期必须在新调用即时生效。审计不记录请求参数或敏感内容。
- 使用官方 MCP Go SDK 的 HTTP 传输和类型化工具，不自行实现 JSON-RPC/SSE。依赖版本在实际接入时根据官方发行信息验证、固定并验证无 cgo 交叉编译。

## 验收矩阵

| 场景 | 预期 |
| --- | --- |
| 未登录查询私有/猜测 ID | 不返回元数据、计数或内容 |
| 发布自己的仓库 | 匿名目录出现技能、分类与必要来源信息 |
| 发布其他用户仓库 | 拒绝且无状态变化 |
| 匿名 CLI 安装 | 仅访问服务端，校验 stream/transfer/logical hash 后进入 canonical |
| 下载时取消公开 | 后续块不获授权、不发送成功 final |
| 跨用户分类关联/删除 | 拒绝；合法删除不改变 skill 内容或 hash |
| MCP 只读 token 尝试写/发布 | 拒绝且无状态变化 |
| MCP token 过期/撤销/轮换后重用连接 | 新调用被拒绝，不能依赖旧会话缓存授权 |
| MCP token 正常获准维护 | 与 Web 领域校验一致，资源仍按 token 所属用户隔离 |
| 后台 token 列表/日志/浏览器持久存储 | 无 token 原文 |

## 检索记录

- nmem CLI：query `Cognate 公开 分类 MCP token`，mode `fast_bm25_vector`，filters `-n 3`，返回 Space `default`。按服务端排序：rank 1 `5edb90de-de20-4ad8-9143-86c82594b5d4`「lazycat-mcp 自定义 MCP Provider 实现细节」score 0.7561088131235001；rank 2 `d997ef30-2bb3-4b00-a97c-1cf71e743185`「Cognate 需求文档全文(跨机器版,交付Codex)」score 0.7203789201879923；rank 3 `8d87caa2-1194-4da1-93c9-0d6673bd9bc1`「Smarticky MCP 能力设计方案：懒猫内免 token、外部 token 鉴权」score 0.7676219132568584。没有命中已锁定的 Cognate 公开/MCP 设计，以上新需求以当前用户消息为准，不挪用其他项目免 token 鉴权。CLI 无法确认图表会话同 owner/space，按技能边界跳过自动图。
- 原设计时记录：已查看 skills.sh 目录及 `modelcontextprotocol/go-sdk` 官方仓库；仅参考目录交互模式与成熟协议实现，不复制其全产品范围，当时具体 SDK 版本尚未锁定。当前实现已锁定 `github.com/modelcontextprotocol/go-sdk v1.8.0`，详见 [MCP.md](MCP.md) 与 [go.mod](../go.mod)。
