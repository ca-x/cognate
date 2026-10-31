# Backup Content Pipeline Implementation Plan

**Goal:** 把真实数据库同切点的完整归档内容接到既有加密容器，随后推进 App/S3/恢复，不把内部接口当成可用完整备份。

**Architecture:** 复用受管 Store 的 `WithLocalSnapshot` 屏障。先对确切 Store/cache 建立独立生命周期 pin，在其回调内读取所有 repository/skill/Pack 引用并验证归档；内容直接送入现有密文 extent，不再新增可恢复的明文中间包格式。只有快照外层返回成功，才能封装最终完成标记或发布对象。

**Tech Stack:** Go 1.27.3、ent/entsqlite、Afero、现有 sio 密文容器；全部测试/构建 `CGO_ENABLED=0`。

**Spec:** `docs/REQUIREMENTS.md` F4、`docs/LOCAL-SNAPSHOT.md`、`docs/superpowers/specs/2026-10-29-server-snapshot-archives.md`。

## 与旧候选方案的关系

2026-10-29 B3 是未实现的候选，不是新的用户需求。本后继保留其中真实的同切点、完整引用、归档验真、cache pin、关闭排空和隐私约束；以直接加密流替代“另建独立 sealed 明文候选再加密”的中间表示。这样没有新明文产物的重启消费/清理协议，也不需要为该中间表示额外保持实例所有权。既有加密引擎仍持有自己的输出能力及资源所有权。候选里针对独立明文 attempt 的验收不用于声称本方案有该产物；恢复、完整内存/磁盘预算、真实 App 和远端发布仍各自验收。

## 写集与分工

- 主线程：`internal/server/library/cache_snapshot*.go`、`cache_maintenance.go`、`content_snapshot*.go` 及直接测试；`internal/repository/archive.go` 的同算法检查薄封装及直接测试。`state/local_snapshot.go` 增加确切切点的 reader pin，`state/maintenance_scope.go` 拒绝 nil/零值 Store，`library/service.go` 抽出按操作期限进入的方法。仅新增内核能力，不改变业务 writer/数据库 schema。
- 密文 worker：`internal/server/backup/seal_source.go`、`capture.go` 与直接测试；沿用现有格式与资源预算，把同步 producer 写入受限加密 writer，不生成明文临时文件。`CaptureSnapshot` 在同切点依次消费数据库、规范 inventory 和排序去重的归档，外层全部成功后才封 manifest page、root 与 complete；返回 opaque 密文文件集合。
- UI worker：仅原生 shell 的 disjoint 文件，与备份不交叉。
- 审阅者：实现稳定后只读验证，不能以作者绿色替代独立反例。

## 数据与生命周期合同

- `ArchiveCache.WithSnapshotSource(ctx, store, consume)` 只收 managed 配对，Quiesce 前完成短 writer 验证并释放。独立 pin 不持 writer lease，Close 等待 pin 和已接纳子复制完成后才关闭 root。
- `SnapshotArchiveSource.CopyVerified(ctx, hash, writer)` 只接受该 source scope 内的真实、确切 Store live snapshot context；固定 hash 文件名、读取前后物理身份与私有属性、长度/哈希/关闭错误全部验证。过期/取消/错误不返回成功摘要。
- `repository.InspectArchive` 仅调用既有 parser/hash，不下载、不修复、不修改输入。
- 完整引用按有界分页读取；归档去重，但不忽略 private/unpublished/not-in-Pack 或其他 owner。每个引用的路径、内容 hash 与技能数必须匹配同一归档扫描。
- 回调成功只是候选；B2 外层失败/取消/清理不明优先，不得封最终完成标记。无新 App、RPC、S3 或恢复能力时不更新为完整 F4 已完成。
- 已接纳归档复制必须登记到真实 B2 operation，覆盖末次文件关闭；单独的 cache pin 不能替代切点 pin。离开回调时等待逃逸复制排空，真实生命期失效后不得通过 `WithoutCancel` 取得成功摘要。
- 与既有容器保持相同 extent 顺序与 64MiB 非末块规则；单次分块内存有界并清零，不能通过限制整个数据库为 64MiB 掩盖分块问题。

## 进度与复核记录

- 2026-10-31：归档源独审发现逃逸复制未登记 B2 reader，以及文件关闭末尾生命期复核缺失；永久回归与修复后，独立三轮 GREEN，并用外部驱动证明 B2 未返回、writer 仍拒绝进入后才放行 child。原行为 RED 保留，不能用旧驱动自死锁冒充新缺陷。初始缺符号编译失败只记为编译 RED。
- 主线程源三包验证：`internal/server/library`、`internal/server/state`、`internal/repository` 的独立 CGO0 test/vet 通过；`/tmp/cognate-content-pipeline.LUUzXUZv/source-packages.json` 为 1334 PASS / 4 SKIP / 0 FAIL。该收据不覆盖后继 Capture、原生 UI 或全仓最终组合。
- 内容闭包独审另发现两处 P2：inventory 末次真实生命期缺失，以及第二次普通错误覆盖先前清理故障。主线程先复现原三格行为 RED，再新增永久四格回归，末次 life 检查和 sticky failure 都以优先级合并；独立复核三轮 66 PASS / 0 FAIL / 0 SKIP，原 drain 正控保留。最终范围 ACCEPT，源摘要与收据在 `/tmp/skillhub-f4-inventory-verify.vR5zyV`。
- Capture 作者验证真实源移走后的密文离线认证回读、64MiB+1 分块、外层 B2/源关闭/组合密文清理故障及消费者关闭排空；最终 backup 包 267 PASS / 0 FAIL / 0 SKIP（不含主线程空安装永久测试）。见 `/tmp/cognate-capture.C16jXQ68/README.md`；生产连接的失败零 bundle，外层成功前无 page/root/complete。
- 主线程独立审查 producer/Capture 的长度与 hash、过期 writer、内存清零、顺序、完成门和资源归属，另验证空安装永久 case；临时 overlay 用 127 个唯一归档产生 129 个 extent、两页 manifest，经现有 BeginOpen/AcceptFile/Finish 认证回读通过。见 `/tmp/cognate-capture-main-review.l3G4b9V1/pages-final.json`。首轮空实例 JSON tag 漏写和分页 fixture 提前关闭均是测试驱动错误，保留原记录，不作为生产缺陷 RED。

## 验收步骤

- [x] Cache source：验证 managed 配对、错误 Store/ctx、过期 source、Close/drain、读写/短写/关闭故障、symlink/hardlink/替换与哈希错误；独审缺陷先保留行为 RED 再修复。
- [x] 真 snapshot callback 能复制合法归档；source pin 不自阻塞 Quiesce；writer 在复制与末次消费完成前不能穿透。
- [x] 内容引用闭包：跨 owner/shared archive、多页、private/not-in-Pack、DB path/content hash/count 不符全部拒绝；源不存在不回源下载。
- [x] 密文流 producer：同格式真实解密、精确长度/hash、预算、取消/错误/关闭与过期 writer；无新增明文 stage。
- [x] 连接内容源与加密容器，真实离线解密回读；之后另行接 App/S3/恢复和产品入口。
- [x] 独立审查和最终组合回归：7768 PASS / 158 SKIP / 0 FAIL，vet/modules/MyGo vet 与十八目标 CGO0 构建通过。阶段提交、SSH push/readback 和记忆收据在实际交付时另行记录；完整正式版目标继续 active。
