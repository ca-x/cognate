# 已收录公开来源的自动更新

Cognate 的公开来源 worker 与 GitHub Lists 同步独立：Lists 元数据、水位线没有变化时，仍重新下载已收录的公开归档并扫描内容。手动收录、服务端插件预览后显式收录和仍获授权的 star 来源都参加轮转；插件预览本身不收录、不发布、不安装。此功能不是私有来源 S1，也不代表完整 F5 已完成。

## 启用与节拍

- `server.Config.SourceRefreshInterval *time.Duration`：`nil` 默认 15 分钟，指向 `0` 禁用，正值必须为 1 秒至 24 小时。首次运行等待完整间隔，不在启动时抓取 GitHub。
- 禁用只停止本自动更新 worker；手动 Refresh 和独立 Lists 同步/首次收录仍由各自入口控制。
- 用户入口由主线程的 CLI 切片组合后提供：`cognate-server --source-refresh-interval=15m`；使用 `--source-refresh-interval=0` 禁用。本 worker 冻结切片不包含 CLI 实现，须连同该切片验收，不能仅凭本文假定某个旧二进制已有此 flag。
- 每次最多处理 16 位 owner、每位一个来源，整批最多 2 分钟；抓取复用 library 的 20 秒请求预算和两个 IO slots。批次完成后再等待间隔，默认 15 分钟不是所有来源的刷新 SLA。
- 按 owner ID keyset 分页，每轮结束把来源 slot 在 0–999 轮转；各 owner 按来源 ID 顺序选 `slot % 来源数`。稳定集合有界、公平；失败或拒绝也推进，只推进实际开始尝试的项。游标只在内存，重启重新扫描。
- 同一 worker 不重叠；同数据目录的自动批次复用文件锁协调。锁竞争短暂让出，不积压旧 tick，不承诺多实例 exactly-once。停止会取消并等待批次退出，再关闭依赖的服务、归档缓存和数据库。

## 授权与内容边界

`Manual=true` 包括当前手动和插件显式收录，二者没有独立 provenance schema。`Manual=false` 不能在 star 失权后自动变为手动来源：必须绑定当前 owner、GitHub viewer、凭据 ID/代次/key、选择同步代次/已应用代次/fetch ID，并保持当前选中、可见、新鲜且公开的成员关系。仅使用无认证的公开 codeload 请求；GitHub token 不发送给归档来源或订阅客户端。

worker 只能从数据库领取不透明 target/ticket；不能凭 owner 字符串自报授权。library 在 Fetch 前后校验，state 在每次新事务重试时重新验证原 ticket 和原 `(archive_hash, publication_revision)`，star 凭据条件写锁提供 SQLite 写排序；提交前在该事务内重读授权并用新时钟检查到期/观察期限。普通 Web/MCP 写入没有 source context 时沿用原有授权路径。

更新沿用手动刷新合同：相同路径保留 skill ID 和分类，受影响 Pack 各加一个 revision、重算 manifest hash；新增 skill 不自动进入 Pack。删除/改名影响被引用 skill、alias 冲突或引用完整性异常均整次拒绝。**已发布仓库的任何 archive hash 变化都拒绝**，必须显式 Unpublish → 更新 → 重新确认整目录 Publish；worker 不自动取消公开或扩大已发布目录。

## 失败、缓存与未知结果

上游不可用、私有化、限流、非法归档、授权失效、缓存满或旧 CAS 均保留原数据库快照和 Pack。不会自动 rebase、以旧候选重试新的 tuple，亦不会在新授权代次下重放已抓取内容。下一批只会重新领取当前 ticket 并重新 Fetch。

归档缓存沿用既有预算（默认 512 MiB）和持久化策略，旧 hash 对象继续保留，失败事务也可能留下已写入的候选对象并占用预算。此切片没有垃圾回收、自动驱逐或预算迁移；满额会拒绝新写，不为腾空间删除仍可服务的旧对象。

最终授权检查与驱动实际 COMMIT 不是一个时间原子操作。取消、驱动错误或响应丢失不能证明零提交；应读回 owner-scoped 当前 tuple 确认结果，但不能把读回当成自动 rebase/重放许可。已经领取旧 Pack revision 的下载继续遵守既有 revision/hash 校验；保留缓存不等于重新授权旧下载。

## 可复核验证

`CGO_ENABLED=0 go test ./internal/server/state ./internal/server/library ./internal/server/sourcerefresh ./internal/server` 覆盖真实 SQLite 双 Store 的撤权/换号/轮换/旧 CAS、提交时到期、无 source context 回归、分页/失败/超时/锁/取消和 App 生命周期。`TestPeriodicSourceRefreshTwoPacksConnectAndAutomaticWatch` 使用合成 HTTP 归档及隔离临时客户端目录：manual/plugin/star 各自经周期触发到双 Pack、真实 Connect manifest/download、自动 Watch 单次下载安装；不读真实 HOME，不访问真实 GitHub。
