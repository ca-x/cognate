# GitHub Lists 同步（开发中）

`internal/github` 提供服务端使用的 GitHub device flow 和 Lists 读取基础；`internal/server/state` 提供加密凭据与同步事务保护。`cognate-server` 已接入本地账号和 Connect GitHub API，支持 device flow、手动 token 校验保存、Lists 发现、订阅选择与手动同步；Web 面板调用相同 API。定时任务、增量水位线跳过和实际仓库拉取入库未完成，F1/M3 尚未完成。客户端不应直接使用该包访问 GitHub；生产 token 只属于服务端。

## 设备授权接线

管理员先为 Cognate 创建 GitHub OAuth App 并启用 device flow，再通过 `--github-client-id` 或 `SKILLHUB_GITHUB_CLIENT_ID` 配置公开 client ID；未配置时隐藏设备授权按钮，仍可使用手动 token。不可借用其他应用的 client ID。

`StartDeviceAuthorization` 返回随机授权 ID、用户验证码、GitHub 官方验证地址与到期时间，不返回 OAuth `device_code` 或 token。`GetDeviceAuthorization` 和 `CancelDeviceAuthorization` 按已登录账号隔离；空 ID 查询或取消本人当前任务，以便页面刷新后恢复。每账号同时只允许一个进行中的任务，全局最多保存 64 个任务；容量紧张时驱逐终态，不驱逐活跃任务。结果最长保留约 20 分钟，重启后丢失，客户端需重新发起。

服务端按 GitHub 提供的 interval 等待和轮询，处理 pending、slow_down、拒绝及过期；启动请求最多 10 秒，整个任务最多 15 分钟。网页每 2 秒查询本服务端任务进度，不驱动 GitHub 轮询。完成后再验证 Lists viewer，凭据按发起时的 expected generation 条件保存，迟到授权不能覆盖较新的手工或设备授权凭据。过期凭据的 catalog 只返回重新授权所需的非秘密状态，不返回旧列表和仓库。

任务启动的凭据读取、上游请求和后台轮询均纳入服务端生命周期；关闭服务会取消并等待。取消只停止本服务端流程，不表示撤销 GitHub 上已经授予的应用权限。真实 OAuth App 与真实用户 device flow 尚未验证；当前证据来自模拟上游的行为测试、HTTP 隔离测试与网页响应夹具。

Device flow 默认不申请 scope，调用方明确选择时才申请 `user`。该 scope 包含个人资料写入等权限，不能仅因为选择了它就认定私有列表或私有仓库一定可见；需要向用户展示实际授权范围，并在真实账户上验证。设备码、access token 和 refresh token 不写日志；会话与 token 的常见格式化输出已脱敏。内核可在内存中保留可过期凭据并发起 device flow 令牌刷新；刷新会使旧凭据失效，发送后即使响应丢失也不能盲目重试旧 refresh token。实验性状态库可以加密持久化凭据，但持久凭据的刷新前后尚无跨进程协调和完整恢复；不得绕过该边界直接刷新，也不得将凭据明文写入配置、数据库或调试输出。

同一内存 token 的副本共享请求 gate：刷新前等待已进入 gate 的 GitHub 请求关闭响应，随后标记旧 access token 不可再用于内核请求；等待可被 context 取消，也以相应凭据的过期时间为上限，未发送请求时不会消耗 refresh grant。刷新明确返回空 scope 时记录为空，返回非空 scope 时使用返回值；字段缺失时保留已知 scope，明确的 null 被拒绝。该协调只覆盖使用同一 token 状态的进程内调用，不撤销已经交给其他代码的原始字符串，也不覆盖跨进程或持久化副本。

Lists 读取使用 `viewer.lists` 分页获取元数据，再按列表节点 ID 分别查询 `node(id:)` 的 items 分页。列表和每个仓库都保留 GitHub 全局节点 ID；数字 `databaseId` 可为空。每一页检查 viewer、列表身份、元数据、数量、cursor 和仓库类型，HTTP 或 GraphQL 错误、权限变化、空的非空字段、重复或异常分页都返回错误，不返回部分结果。为限制资源消耗，单次读取最多 1024 个列表、总计 100000 个仓库条目，每次响应最多 2 MiB。

读取所有项目后再次获取列表元数据并比较，可检测部分并发变更，但 GitHub 不保证跨页快照一致；`updatedAt`、`lastAddedAt` 和项目数量不能证明所有删除或权限变化均可见。后续持久化同步必须隔离用户身份，只有完整读取成功后才原子应用结果，并对不可见对象保守处理，不能将失去访问权限直接等同于删除。真实账户、权限变化、token 刷新及速率限制尚未端到端验证。

`CommitSnapshot` 为进程内使用同一 token 状态的同步提交提供 gate：结果绑定原凭据和同步代次，刷新、凭据过期或后一次同步开始后不调用旧结果的提交回调；即使后一次同步失败，旧结果也保守失效。回调与刷新串行，回调 context 不晚于凭据过期时间。回调负责自己的原子数据库事务和 context 处理，不得重入使用同一 token 的 GitHub 请求；忽略 context 的回调不能保证在到期前停止。实验性 `internal/server/state` 进一步使用持久凭据代次、读取票据和 SQLite 事务条件更新协调同库进程；见 `docs/SERVER-STATE.md`。这不是 GitHub 数据的一致性证明，也不能代替服务端的订阅、授权或删除策略。
