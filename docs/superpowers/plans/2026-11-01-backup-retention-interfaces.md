# 冻结的实施接口与所有权

固定输入`inputs/RETENTION-SEAM-v3.md` SHA d49df2a33ce55423f417155d023b0f76d5fe9eb91901af06280039eeb8af48fc中的DrillSourceBinding/Versions/Expectation/Facts/VerifiedRestoreDrill，以及两个ReadVerifiedRestoreDrill入口、WithSourceFacts和§4所有pin API，逐字沿用；不再列“等待Popper”。下列为本片具体落点及WRITESET-DELTA中明确请求批准的非破坏性补足。

## 1. Runtime注入与reference

- `server.openServerBackup`在原四实参后增加借用`*runtimegen.Runtime`参数；唯一app.go调用点传`app.runtime.lease`。serverBackup不Close这个owner，不复制另一Runtime、不OpenRuntime。原off路径允许nil以兼容独立内核；retention启用或消费proof须非nil真实owner，nil拒绝。
- ControllerOptions增加同一个Runtime、RetentionOptions、ArtifactStore及AnchorVerifier。controlOptionsHash不纳入这些指针/策略；真实目标generation沿原显式hash。NewController在短Runtime.WithWorkerFiles借用中逐级pin stable-root/backup/control并对比本ControlStore身份，之后释放lease mutex；不只比较路径字符串。
- `RetainedBackupRead`、`RetainedBackupFacts`及WithRetainedBackup/OpenOfflineReferenceStore/WithOfflineRetainedBackup/WithFacts/MatchCatalog严格使用v3签名，在backup/retention.go及control_store.go落地。opaque state绑定physical ControlStore指针、scope nonce、job snapshot、owner指针与根身份、context/closing epoch；没有公开构造或从facts反升cap的函数。
- 增加`CheckRuntimeOwner(ctx context.Context, runtime *runtimegen.Runtime) error`和`CheckOfflineOwner(ctx context.Context, offline *runtimegen.OfflineRoot) error`两个reference方法。必须指针相同且模式相同，零值/复制到其他owner/错根/过期/取消拒绝；短owner admit前后和control named identity都复核。它们不返回key/FD或签发proof。R3 status两个reader及proof.WithSourceFacts复核分别调用，不以公开I/B/A相等代替owner相等。
- MatchCatalog验证每一个ObjectRef的I/B/A/kind/ordinal/size/SHA、唯一性/总数及两头；临时map只在当前scope、上限Seal.MaxObjects+2及现control catalog预算内，结束释放，非持久第二索引。不同顺序允许，缺/额外/重复/末项变异拒绝。输入只是被动资料，方法不会签proof。

## 2. Cold opener：不能重用普通OpenControlStore/Close

准确入口仍为v3 `OpenOfflineReferenceStore(ctx, offline, originalMasterKey) (*ControlStore,error)`；返回的是cold/readOnly模式，不能NewController、persist、初始化、自动恢复或升级。

1. caller已持同一OfflineRoot，在R3外层WithOperation逻辑生命周期内调用；不处于任何root/worker FD callback。short `offline.WithRetainedWorkerFiles`中只做FD建立/清理/命名复核，不执行用户callback、SQL、Source或Wait。它是已接受T4的既有owning-root错误保留入口，不改kernel，不宣称普通T1继承它。
2. 从借入stable root FD以openat/NOFOLLOW打开既有backup/control，每级0700/同UID/同dev，control文件0600/常规/nlink1；逐级命名与FD身份一致。内部如需os.Root桥接，只能从自己已pin的FD构造，并复核同一inode，不能接受caller path或dev/ino自证。平台FD/目录转换helper与existing-only锁均落control_store_unix.go，other对称Unsupported；generic control_store.go不直接依赖unix符号。
3. 调新增existing-only control lock helper：与在线同一control.lock、同一LOCK_EX|LOCK_NB，去O_CREAT；dup的是该control lock自己的同OFD，不是重锁runtime。缺lock、missing历史、Busy、额外文件、control.fence、任一next或错误sidecar直接拒绝，零mkdir/rename/remove/repair。不能调用旧opener后“补回文件”。
4. 有界解密现有control v1/v2及绑定sidecar；v1无sidecar可只读，不能建立ReclaimID。v2缺/错/不明sidecar、已有retirement/Unknown保护按同一规则加载。Decode只是内存状态，不将running job改phase、不自动Cancel/Reconcile。枚举必须覆盖完整allowlist且超限拒绝。
5. 返回前释放root FD loan并完成后置admit，再进入用户reference callback。coldStore记录typed OfflineRoot供每次短复核；既有R3 WithOperation负责外层EX寿命。不得把它的WithWorkerFiles当长期锁scope罩住Source或再调用proof reader。
6. Cold Close独立分支：先cancel其scope；若尚有reference返回ErrBusy、不释放任何依赖，caller须在callback外收束再Close，避免无ctx Close自等待。无pin后，在短retained owner scope只关闭自有FD/root/key，禁普通Close的armRelease/clearRelease/Sync写control.fence。Close不明映射ErrControlUncertain并让现owning EX保留机制接管，原R3操作不得产生completedDrill；不靠事后dup（EMFILE）补锁。确定Close才清自有key副本。borrowed original master不由store清零。

在线/离线均只使用physical controlState.mutex那一份reference表；跨进程依靠同一control.lock而不是第二索引。offline lock只非阻塞，绝不持runtime mutex等待control lock。正常在线writer的原Close/fence语义不因cold分支放宽。

## 3. pin准入与锁序

同锁检查job/canonical catalog/current绑定、未retired/Unknown/closed→登记pin→解锁；tombstone在同锁复核全部pins及保护集合。pin持至Source整个函数返回、Candidate.Close和workspace清理及所有后置admit。callback结束使所有reference副本失效；active methods排空后才能释放scope，不允许逃逸借FD或key。

在线wrapper将read scope登记到Controller的owned ctx/Close drain；离线wrapper登记到coldStore并使用R3 operation context。Controller/ColdStore没有互相再开的reference registry。retention自己已占active时走内部同锁anchor pin，不递归调用公共Run/WithRetainedBackup。

锁序：lease mutex只作短borrow/admit；automatic.mutex只管producer句柄；control mutex只作状态/AEAD持久化与pin原子性。它们均不罩住网络、Source/proof callback、Quiesce/guard、App.Close、worker Wait或done等待。Cold开/关的FD操作可在短lease loan内完成，用户操作必须在loan全返回后进行。root Close由R3 operation外调用；Controller Close在其callback外调用。无EX→新SH、EX重取EX、mutex内等待自己的scope。

## 4. 被动expectation发现：明确来源，不新增operator bool/hash配置

新`restore/retention_source.go`提供两个internal-package导出入口：

`ReadRetainedDrillExpectation(ctx context.Context, runtime *runtimegen.Runtime, reference backup.RetainedBackupRead, originalMasterKey []byte) (DrillExpectation,error)`

`ReadRetainedDrillExpectationOffline(ctx context.Context, offline *runtimegen.OfflineRoot, reference backup.RetainedBackupRead, originalMasterKey []byte) (DrillExpectation,error)`

先检查typed reference owner；使用status.go同一个私有metadata reader，读取当前认证lineage对应的operation/attestation资料（固定记录/原总operations上限1000，不扫任意路径）。比较reference的原I/B/A/complete bytes/hash；原source字段来自受证记录，不从未认证CLI参数拼装。Versions/Policy还须是当前compiled reader支持的生产版本/实际规则，不支持即拒绝。只返回被动DrillExpectation，全部自有FD Close确认后返回。

这不是proof reader的别名，更不是成功许可：即使返回expected，必须再调用未改签名的ReadVerifiedRestoreDrill/Offline并通过identity、ready图、Unknown/fence与当前owner/pin检查。两个读取之间current变更/关owner导致后者拒绝，不自动换operation。status.go只机械抽取原私有读取核心供共用，不新增MAC解析/签名域、公开signer或第三套qualification。metadata入口的任何资料均不能反向构造VerifiedRestoreDrill。

## 5. 真实Source全ref观测adapter

同一新文件提供：

`ObservedRetainedS3Source(options backup.S3Options, parent *os.File, expected backup.Attempt, recoveryID [16]byte, recoveryMaterial []byte, limits backup.SealLimits) (CandidateSource,*RetainedSourceObservation)`

`(*RetainedSourceObservation).WithFacts(ctx context.Context, consume func(RetainedSourceFacts) error) error`，以及无FD/无key的`Dispose()`。

RetainedSourceFacts只含被动Source绑定、source应用/schema/格式/suite、完整`[]backup.ObjectRef`。它不是R3 proof，也不是engine/Candidate/DELETE cap。observation没有public setter/constructor/decoder；Source单次执行，出错/取消/Dispose/未全返回时WithFacts拒绝。

实现复用原私有s3CandidateSource/defaultSourceS3Ops，以target wrapper观察而不改变算法：

- WithRestoreHead只在借到两只只读head FD时用SectionReader有界hash/length（bootstrap156、complete≤16KiB），构造被动两头ref；不改变FD offset、不关闭borrowed FD。
- 每次CopyExact记录传入的ref，仍原样转发ctx/borrowed writer和结果；暂存资料不当认证。重复/超限立即拒绝；不重发、不吞response Close错误。
- 既有engine完整Finish后，在包住consume的callback读取原bundle Metadata（不是sanitized DB）；只有整个Source，包括所有defer关闭及caller的Stage都nil后，observation才能公开资料。头SHA在此刻才属于同一完成认证链。callback成功不提前标ready。
- parent engine/session/bundle/target/input关闭、secret擦除、one-shot、取消规则保持原函数；不改source_s3.go/s3 target/CGBK/catalog布局。key只借用本次调用，adapter不持久化raw key。Source失败包括现ErrCandidateUncertain的保守映射必须原样传播。

## 6. 唯一trusted server bridge

backup.ControllerOptions的AnchorVerifier是server固定接入的内部函数，参数为ctx及真实reference，返回error，不接受管理员bool、proof文件路径或已验标记。nil在启用时拒绝。生产只能赋值本文件的固定bridge；正向测试禁止用return nil函数替代真实R3 producer。任何in-process恶意改源码不是本合同授权模型，公开RPC/config不能注入该函数。

`backup_retention_runtime.go`中固定顺序：

1. caller先在同锁取得anchor pin；读取strict原master/recovery文件并核构造时generation/key commitment，不长期保存openApp传入的slice。
2. 读取被动expectation→原v3冷reader全返回opaque proof→WithSourceFacts比较全部绑定/versions/policy；两者均须与本reference匹配，自动启用不能扩大版本支持。
3. 分配受证workspace并在Prepare前持久inspector_may_live；observed Source→PrepareCandidate→普通WithVerified，认证原全部闭包/DB/inventory/archives。D7普通T1不借T4资格。
4. Source已全返回nil后，observation.WithFacts逐项对比proof原source字段、原complete bytes/hash及原source版本；reference.MatchCatalog比较全ref集合含两头，不能只比较complete。Candidate明确Close、observation Dispose、所有workspace FD/目录/guard确认结束。
5. proof.WithSourceFacts末次校验当前lineage/owner/pin；reference后置admit；bridge整个函数返回nil（含defer），才由Controller形成内部anchor-terminal、复核同锁保护集合并持久victim tombstone。anchor pin覆盖这一提交，之后释放。proof本身不直接传进DeleteExact。

ErrCandidateUncertain/ErrCandidateCleanup、runtimegen.ErrUncertain及资源Close/Sync不明必须join backup.ErrControlUncertain：Controller poison→serverBackup.Close失败/retain→App现有错误链保留SH。纯远端DeleteUnknown且资源已确定关闭是持久业务未知，可以App.Close=nil；仍挡自动/零重删。真实测试分别证明两条链，不用“callback承诺”或PASS文字替代。

无匹配完整proof/identity pending/仅metadata或Candidate成功时，success-prune=0；返回固定anchor_unverified/unknown分类，不生成新drill/backup UUID、不自动恢复、不暗开EX。至少最新K≥2外再保护唯一当前有效已演练anchor；原历史收据在rollback后仍保存，但不能作新lineage锚点。
