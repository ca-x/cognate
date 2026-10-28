# Cognate 命名迁移

2026-10-28，用户将 SkillHub 更名为 **Cognate**，继续原项目与完整首发范围。

- GitHub：`https://github.com/ca-x/cognate`；SSH：`git@github.com:ca-x/cognate.git`。
- Go module：`github.com/ca-x/cognate`，仓库内 Go imports 和 Protobuf `go_package` 同步更新。
- 命令：`cognate`、`cognate-server`、`cognate-desktop`；入口对应 `cmd/` 下同名目录。Web、原生窗口、安装包和自动构建产物统一使用新品牌。
- 本地 checkout 目录不自动移动，以免破坏在途进程、工作区和工具路径。

## 有意保留的兼容标识

改名不是数据迁移，不机械替换以下标识：

- Protobuf 文件路径、package `skillhub.*` 和 Connect RPC URL。仅 Go module 路径改变，wire namespace 不变；原有客户端仍能访问。
- Go 源码 API 的 module / `go_package` 路径按用户要求改变，外部 Go 调用方需要更新 imports。这会触发 Buf 的 `FILE_SAME_GO_PACKAGE` 检查；CI 仅在临时历史基准中将精确的旧 Go module 前缀规范化为新前缀，再运行完整 FILE 检查。不禁用任何 breaking rule，也不规范化 package、字段、标签或 RPC URL；其它不兼容变化仍应失败。
- 已有加密的 HKDF / AAD 派生域、Pack manifest hash 域、分享 token 格式及备份版本。改这些值会导致旧凭据、签名或内容摘要失效。
- 本地状态、私钥和服务端默认数据目录：`~/.local/share/skillhub`、`~/.config/skillhub`、`~/.local/share/skillhub-server`。本阶段继续复用原目录，避免生成新密钥或拆成两套安装账本。服务端可显式使用 `--data-dir`。
- SQLite 约束/trigger/index 名、安装 stage 标记、OIDC 临时 cookie 和 Web 的非敏感 `skillhub.theme` / `skillhub.locale` 偏好 key。
- `SKILLHUB_*` 环境变量继续兼容；新增 `COGNATE_*` 对应变量优先，旧变量为 fallback。
- 历史验收记录、原始记忆标题及检索关键字保留当时名称，不改写已经执行过的命令证据。

不需要为了改名重新安装 skill、搬动 canonical `~/.agents/skills`、删除数据库或重建主密钥。尚未交付的功能不得因为改名被标记完成。
