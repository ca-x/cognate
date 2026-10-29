# 已收录来源的自动更新与私有来源

Cognate 的来源 worker 与 GitHub Lists 同步独立：Lists 元数据、水位线没有变化时，仍重新下载已收录的归档并扫描内容。手动收录、服务端插件预览后显式收录和仍获授权的 star 来源都参加轮转；插件预览本身不收录、不发布、不安装。私有来源通过服务器当前 owner 凭据获取，客户端只访问 Cognate；这不代表完整 F5、备份一致性或非 Linux 安装支持。

## 启用与节拍

- `server.Config.SourceRefreshInterval *time.Duration`：`nil` 默认 15 分钟，指向 `0` 禁用，正值必须为 1 秒至 24 小时。首次运行等待完整间隔，不在启动时抓取 GitHub。
- 禁用只停止本自动更新 worker；手动 Refresh 和独立 Lists 同步/首次收录仍由各自入口控制。
- 用户入口由主线程的 CLI 切片组合后提供：`cognate-server --source-refresh-interval=15m`；使用 `--source-refresh-interval=0` 禁用。本 worker 冻结切片不包含 CLI 实现，须连同该切片验收，不能仅凭本文假定某个旧二进制已有此 flag。
- 每次最多处理 16 位 owner、每位一个来源，整批最多 2 分钟；抓取复用 library 的 20 秒请求预算和两个 IO slots。批次完成后再等待间隔，默认 15 分钟不是所有来源的刷新 SLA。
- 按 owner ID keyset 分页，每轮结束把来源 slot 在 0–999 轮转；各 owner 按来源 ID 顺序选 `slot % 来源数`。稳定集合有界、公平；失败或拒绝也推进，只推进实际开始尝试的项。游标只在内存，重启重新扫描。
- 同一 worker 不重叠；同数据目录的自动批次复用文件锁协调。锁竞争短暂让出，不积压旧 tick，不承诺多实例 exactly-once。停止会取消并等待批次退出，再关闭依赖的服务、归档缓存和数据库。

## 授权与内容边界

`Manual=true` 包括当前手动和插件显式收录，二者没有独立 provenance schema，也不表示公开。manual 有 GitHub 连接（包括失效连接）或 owner 的已知 private 历史时必须 API-first；失权、缺凭据或认证失败直接拒绝。只有无连接且无已知 private 历史才沿用公共 Fetch，不尝试公共请求后再认证，也不在认证失败后回退。跨 viewer 的同 owner private 历史只禁止降级，不授予访问；缓存 hash 和重复收录同样不是授权。

已知 private 历史独立保存于 `RepositoryPrivateHistory`，唯一键为 owner ID 与规范小写 `name_with_owner`，不绑定 viewer，不保存凭据或上游 URL。已授权的 private Lists 观察和 private metadata receipt 在原事务内幂等追加；当前 `GitHubRepository.Private` 仍忠实反映新观察，不粘住旧 private。public 观察、改名、换 viewer、断连和重启不会删除旧 canonical key 的拒绝历史；其他 owner、仅有 public 历史的仓库不因此被拒绝。历史只能禁止匿名降级，不能替代当前凭据、selection 或 metadata 授予访问，旧 ticket 的当前 private 位变化仍会使其失效。

启动在数据库既有私有 schema 锁内、密钥身份验证后分页回填现存且可证实的 private 记录，规范化并去重，重启幂等；失败整次回滚。旧数据已经覆写为 public 且无其他证据时，无法重建丢失事实；迁移不猜测、不把所有历史 public 都拒绝，也不宣称恢复已丢失的私有历史。

`Manual=false` 不能在 star 失权后自动变为手动来源：绑定当前 owner、GitHub viewer、凭据 ID/代次/key、选择同步代次/已应用代次/fetch ID，保持当前选中、可见、新鲜且 private 位一致的成员关系。public 成员沿用无认证公共 Fetch，private 成员必须持 Store-issued 私有 proof；private 位变化使旧 public ticket 失效，必须重新取得当前授权。Lists 首次收录与独立自动刷新都走此边界，不能只实现下载 helper。

认证路径在同一个 20 秒预算及凭据到期限制内先 GET 固定 GitHub API metadata，再请求归档 API，人工验证唯一 302 到精确 codeload HTTPS host。Bearer 仅发送给 API；第二跳新建请求，无 Authorization/Cookie/Referer，不接受后续重定向。上游签名 query 作为本次短期能力 URL 原样保留，仅活在服务器内存，不写 DB/cache key/日志/错误/RPC/客户端。metadata 只保留有界 node ID、规范仓库名和 private；manual metadata 与首次收录/刷新同事务保存，不伪造 Lists/membership，私有历史另按上述模型单调保留。凭据需要上游实际 repo/Contents 权限，既有 Device flow 不自动获得该权限，也不自动扩 scope。

worker 只能从数据库领取不透明 target/ticket；不能凭 owner 字符串自报授权。library 在 metadata/归档每跳前后校验，state 在每次新事务重试时重新验证原 ticket 和原 `(archive_hash, publication_revision)`，凭据条件写锁提供 SQLite 写排序；提交前在该事务内重读授权并用新时钟检查到期/观察期限。writer admission 先于 token/Lists gate 和 SQLite，租约覆盖重试、rollback、OnCommit 及完成，HTTP 不持长写租约。public 无凭据时以不匹配任何记录的 SQLite UPDATE 取得写排序，再重读无连接/无 private 历史条件；不创建伪凭据。MCP `catalog:write`/撤销闸仍叠加，不能用 GitHub proof 替代；没有 source context 的普通写入沿用原有路径。此切片不新增 cache.Put→DB 的外层内容租约，不改默认 Cache 行为。

更新沿用手动刷新合同：相同路径保留 skill ID 和分类，受影响 Pack 各加一个 revision、重算 manifest hash；新增 skill 不自动进入 Pack。删除/改名影响被引用 skill、alias 冲突或引用完整性异常均整次拒绝。**已发布仓库的任何 archive hash 变化都拒绝**，必须显式 Unpublish → 更新 → 重新确认整目录 Publish；worker 不自动取消公开或扩大已发布目录。

## 失败、缓存与未知结果

上游不可用、可见性变化、限流、非法归档、授权失效、缓存满或旧 CAS 均保留原数据库快照和 Pack。不会自动 rebase、以旧候选重试新的 tuple，亦不会在新授权代次下重放已抓取内容。下一批只会重新领取当前 ticket 并重新 Fetch。

归档缓存沿用既有预算（默认 512 MiB）和持久化策略，旧 hash 对象继续保留，失败事务也可能留下已写入的候选对象并占用预算。此切片没有垃圾回收、自动驱逐或预算迁移；满额会拒绝新写，不为腾空间删除仍可服务的旧对象。

最终授权检查与驱动实际 COMMIT 不是一个时间原子操作。取消、驱动错误或响应丢失不能证明零提交；应读回 owner-scoped 当前 tuple 确认结果，但不能把读回当成自动 rebase/重放许可。已经领取旧 Pack revision 的下载继续遵守既有 revision/hash 校验；保留缓存不等于重新授权旧下载。

## 可复核验证

`CGO_ENABLED=0 go test ./internal/server/state ./internal/server/library ./internal/server/sourcerefresh ./internal/server` 覆盖真实 SQLite 双 Store 的撤权/换号/轮换/旧 CAS、提交时到期、无 source context 回归、分页/失败/超时/锁/取消和 App 生命周期。`TestPeriodicSourceRefreshTwoPacksConnectAndAutomaticWatch` 使用合成 HTTP 归档及隔离临时客户端目录：manual/plugin/star 各自经周期触发到双 Pack、真实 Connect manifest/download、自动 Watch 单次下载安装；不读真实 HOME，不访问真实 GitHub。

`TestPrivateSourceTwoPacksConnectAndAutomaticWatch` 在 Linux 对 private manual/selected Lists 分别实际首收录，经过真实合成 metadata→API 302→codeload、独立 RefreshAutomatic、双 Pack、真实 Connect 和隔离 HOME Watch；校验单次共享下载安装、canonical/symlink、隐藏二进制/执行位。401/403/404/429 和跳间凭据轮换不得 fallback 或改变旧 DB/Pack/安装。`TestPrivateSource*` 的 Store 测试另覆盖跨 owner/Store/proof 替换、private 位/选择/凭据/过期/旧 CAS、提交到期 metadata 原子回滚和 MCP 撤销；全部 Go1.27.3、CGO_ENABLED=0，无真实账号或凭据验收。

`TestIndependentS1HistorySurvivesPublicListsObservation` 保留独立发现的 private manual → public Lists → 断连前后重启反例；历史用例另核更名旧 key、owner/viewer 隔离、纯 public 允许、匿名旧票据失效、旧 schema 分页回填、事务回滚与实际 COMMIT 后丢回执不得重放。历史表不改变非 Linux 安装 guards 或平台支持声明。
