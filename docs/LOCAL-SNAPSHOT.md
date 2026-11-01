# 服务端本地快照与加密内容闭包内核

受管 Store 提供数据库快照和引用清单，library 将同切点的 skill 归档接到
既有加密容器；这些是内部能力，不是已经可用的完整备份入口。
后继已接通显式启用的 Linux App 受管模式、自动/手动任务、云端上传和 Web 管理；
完整恢复仍待接线，产品参数与边界见 `SERVER-BACKUP.md`。
默认服务端不会因本片自动启用备份或改变既有多进程模式。

## 后继恢复状态

T1 只读验真、认证 Source/Candidate 及 Linux T4 隔离候选净化已单独交付。
T4 使用同 EX 租约的 inspector，真实提交凭据撤销并关闭、等待、Sync，独立复核完整
保留数据后才交付一次性 SanitizedCandidate；原 inventory/归档及源摘要不重标。
普通 PrepareCandidate 的旧 T1 只读检查未因此取得运行根同 OFD 资格，不能混用收据。
这仍不是运行代发布、持久 current/intent、回退、离线恢复 CLI 或真实 App 恢复演练。
保留清理不能用 candidate_verified 代替 restore_verified；完整 F4 仍打开。

## 接口与生命周期

可信进程内协调器可调用 `Store.WithLocalSnapshot(ctx, limits, consume)`。
调用者不能传入数据库路径、DSN、SQLite 连接、目标文件或维护租约。
只有与数据库物理身份绑定的受管 Store 可以进入；普通 RPC、插件和用户输入
不能直接指定快照来源或执行回调。

快照持有 Store operation pin 和本实例排他维护屏障，等待合作 writer 排空。
关闭 Store 会拒绝新操作并等待相关操作退出，不提前释放实例所有权。
回调内的 `LocalSnapshotView` 仅提供有界元数据、数据库流和引用分页/流；
回调返回后失效，不导出可逃逸的文件路径、SQL 连接或读取句柄。

只有外层返回 nil error、全部资源与临时产物明确收尾后，summary 才有效。
回调成功不是发布成功；失败、取消或关闭结果不明均不能发布候选。

最后一个维护句柄退出时，先关闭目录能力，成功后才关闭实例物理锁。
目录清理返回错误时不尝试解锁，注册表保留失败条目，同进程拒绝重新加入；
不能先解锁再用内存中的强引用冒充跨进程互斥。物理锁本身关闭报错时，
仍保留失败条目，但其内核状态不明，不保证另一进程仍被阻挡，也不自动重试。
正常关闭后移除条目，允许后继进程重新获取锁。

释放顺序的回归测试使用真实锁与独立子进程，覆盖目录清理进行中、清理错误
后的拒入，以及正常释放后的获取。目录关闭错误由局部 `io.Closer` 注入，
不宣称已诱发操作系统的真实 `os.Root.Close` 失败；当前 Go Unix 实现不会
将底层 close 错误返回给调用者，因此也不能从 nil 推断内核关闭无任何异常。
这项修复不启用 App 备份入口或改变完整 F4 的交付范围。

## 无 cgo 的受限复制

源前置检查允许 SQLite 正常关闭后只读重开产生的空 WAL-index：只有物理 WAL
帧数与 SHM 已提交帧数同时为零时，SHM 页大小为零才合法。非零错误页大小、
存在未提交物理帧或残留已提交帧仍拒绝；双 header、校验和、salt、容量及
backfill 检查不放宽。不通过 checkpoint、写入预热或删除 sidecar 绕过检查。
前置成功仍仅为 `NeedsCertificate`，不是完整快照或恢复资格。

2026-11-01 在 Go 1.27.3、`CGO_ENABLED=0` 的 Linux 上验证：真实 SQLite
512/4096/8192/65536 四种页大小只读重开成功，检查前后 DB/WAL/SHM 摘要不变；
旧 helper 覆盖下这些正例失败，删除物理帧 guard 的负控被未提交帧用例捕获。
独立复核接受这两文件修复；本地主线程以下回归与 vet 均 exit 0：

```sh
go test -mod=readonly -count=1 -timeout=10m ./internal/server/state ./internal/server/library ./internal/server
go vet ./internal/server/...
```

Linux worker 使用既定 modernc SQLite 在线 Backup API，按页复制受控候选。
CLI 与 server 都保留真实 worker dispatch，普通命令不自动启动快照。
它不复制活动 main/WAL/shm 文件，不调用外部 sqlite，不使用全库 Serialize，
不修改 live 源的 journal 模式或在检查时运行迁移。

worker 及宿主复核绑定身份、大小/页数预算、内容引用、完整性与关闭结果。
源、候选均必须匹配固定可信 schema：完整扫描全部 92 条 catalog 记录，
包括 90 条应用记录及两个完整内部元组；名称前缀不授予内部对象豁免。
真实 SQL NULL 与非法/超长值投影出的 NULL 明确区分。合法迁移需要另行更新
并验收可信 schema，不能从正在检查的数据库自动生成新的准入指纹。

物理快照不是净化操作，可能保留空闲页及删除历史，必须按全实例敏感数据处理。
文件流可在回调内交给加密容器内核，但这不意味着远端发布或完整恢复已完成。
完整备份必须继续保护同一切点的 skill 内容，不能先释放屏障再读取可能变化的
live cache。

## 全实例内容闭包

`library.Service.WithContentSnapshot` 在同一个受管屏障内分页枚举所有 owner 的
repository、skill、Pack 和 Pack item，不能只导出公开资源或某个 Pack。
归档按实际 SHA-256 去重，检查其 SKILL.md 路径、内容 hash 和技能数与数据库
一致；缺失、损坏或不匹配时直接失败，不访问网络补齐。分类和其关联随同一份
数据库复制；内容 inventory 还包含规范版本头、切点身份和排序后的归档身份。

`ArchiveCache.WithSnapshotSource` 独立持有 cache pin，不持 writer lease，避免
自己阻塞 Quiesce。每次归档复制另外登记到确切 Store 的真实 B2 operation，
覆盖读取、目的端写入和源文件最终关闭。逃逸复制没有完成前不能释放切点。
只读 Afero 源在复制前后复核物理身份、长度、私有权限、所有者与链接数；
最终关闭后再次检查真实生命期，不能用 `context.WithoutCancel` 绕过失效。

归档源关闭结果不明时返回 `ErrSnapshotArchiveUncertain`，保留文件、根能力和
实例所有权，cache 后续关闭同样报告不明。不能通过重试 close 或提前释放锁
将不明结果包装成成功。返回错误及默认 fmt/JSON/slog 表示不带真实路径、
归档正文、凭据或密钥；可信消费者只能在有效 scope 内显式读取所需内容。

## 直接连接密文容器

`backup.SealEngine.SealExtentSource` 接收同步 producer，直接写入既有认证加密
格式，检查精确明文长度与 SHA-256。回调退出后 writer 失效，不能关闭底层
密文流；写入错误、短写、取消或校验失败不能成为成功对象，也不能继续封装
该失败 attempt。

`CaptureSnapshot` 在内容闭包回调中依次消费数据库、inventory 与排序归档。
单次明文缓冲最多 64MiB，非末 extent 保持既有 64MiB 规则，消费后清零；
不新增明文临时包或第二种重启消费格式。数据库本身仍使用 B2 已有受控候选，
不把“没有新增明文 stage”解释为整个流程从来没有数据库明文候选。

只有 `WithContentSnapshot` 所有外层成功并明确清理后，才封装 manifest pages、
root 和 complete。任一源端或密文端清理不明均优先保留错误类别，不能被取消
或普通回调错误掩盖。成功返回的 opaque `CapturedSnapshot` 可按协议顺序消费
密文文件；Close 取消并等待消费者排空，再释放文件，不能在消费中途删产物。

离线认证回读数据库、inventory 和归档，只证明加密内容自足和完整性，不是
生产恢复入口。schema/引用校验、隔离候选代、运行代切换、凭据撤销和失败回退
仍须独立完成；App、S3 任务、保留策略和管理界面也未因此自动开启。

## 2026-11-01：隔离恢复数据库的只读准入 T1

`state.InspectRestoreDatabase(ctx, parent, database, originalMasterKey, limits)` 只接受
隔离私有 parent 与固定 `server.db` 的只读 FD；caller 负责没有 writer，不接受活动数据库。
Linux 隐藏 worker 先安装资源限制，再只读核验完整 catalog、integrity/FK、canonical 引用、
分类、原 key/policy proof 和全部保留密文；借入 FD 不关闭，不迁移或补 proof。key 经独立
匿名管道传递，不进入控制 JSON/argv/env。其它平台受控拒绝，不凭交叉编译声明运行资格。

首次候选曾因 `after=0` 分页漏验非正 ID 获 P1 BLOCK，原证据保留。fix1 在共同入口补齐
9 表/18 列全域证明，原七个真实漏验反例经独立复审 CLOSED 后才合主线。主线 T1、旧
snapshot dispatcher、真实密文回读、实际生产 worker 与 S3 CopyExact 的有限组合通过；
命令、计数与冻结报告见 `VERIFICATION.md` 首节。

成功返回的 opaque evidence 不是发布能力，不能跨候选写入或替换复用，也不证明 inventory/
归档闭包、全实例停服、凭据撤销、运行代切换或完整 F4。T2–T5 仍须单独完成。

## 验收范围

独立验收覆盖闭合 schema 拒绝、真实连接/WAL、全部所有者的 SQL 还原、
两种实际 binary 和 512 至 65536 的八种页大小，以及错误、取消和资源归属。
旧两版 schema finding/REJECT 保留，新后继由原反例、独立正门和编译负控闭合。
当前运行证据限 Linux amd64 的具名场景；交叉编译不证明其他平台可用，
也不新增全流程资源数值证书、完整 F4 或 GUI 资格。后继内容管线的实现与
独立复核进度见 `docs/superpowers/plans/2026-10-31-backup-content-pipeline.md`，
最终组合命令和结果见 `docs/VERIFICATION.md`；历史内核签收不自动覆盖后继。
