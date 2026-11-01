# 完整备份保留与回收：实施授权（2026-11-01）

本节是主线程在完整F4目标内的技术裁定，不是用户逐项原话、实现完成、独审ACCEPT或GA放行。
批准下面v3完整方案、50路径和两个接缝补足；原文“未获授权”保留冻结时历史，由本节取代。
实际实施固定基线为 `c8885b0345753af7222971acacfee41ecb065c51`（已含自动备份与T4）；
原58f9+auto调查、旧失败/UNKNOWN和原v1/v2不重写，不为仅后继文档反复重建。

## 精确输入与写集

- HANDOFF.md SHA256 `6bb3a750fc863df150a6a2cdce5a175eb6ed42e5b16d27f7d36a078bb51cfaf7`。
- INTERFACES.md SHA256 `3277d67b14945fd5ebcf1d20f53fd3606f7ecad872dc0c6c3a4f01cb4021a31b`。
- WRITESET.tsv SHA256 `3a110e1633c304cf417c24ce45a999f7c402f1dd55f9f8ce3d64f7e6cf76a759`。
- VERIFICATION.md SHA256 `a6d05106b717e755ae0be5eec40d07556b0b4de80de4f5c2a09b5075a22ac30a`。
- IMPLEMENTATION.md SHA256 `755d14dbd1947735f42878773f81239372ad6b761a34a14d9b4fb174ba216f1c`。
- REPORT.md SHA256 `9a02ed98be806ff59e60953659b4c6b8ab5e752b5f1f6fc22dc75959f324fd7d`。
精确清单见 `2026-11-01-backup-retention-writeset.tsv`，50唯一路径：
24修改、22新增、3生成、1对R3依赖修改。原43之外的七路径理由已核对。
需要第51路径或改变源协议/容器/权限边界时先列准确名字与必要性，不隐含扩写。

## 主裁定

1. 完整真实R3演练/实际App业务读取/身份门/所有资源终态是D1前提；冷reader产生的opaque
   proof之后仍须同源Source/Candidate完整重验与全ObjectRef匹配。最新至少两份只是附加保护；
   没有真proof、OIDC identity pending、原CloseUnknown或D7未明，成功备份零删除。
2. 批准typed reference的 `CheckRuntimeOwner` / `CheckOfflineOwner` 和被动expectation两个
   读取入口。它们不签proof、不返回key/FD；共享status.go只复用同一认证读取逻辑。
   不能比较公开facts就冒充same-owner，不能将管理员bool或配置hash当受证演练。
3. 冷开/关必须existing-only、同control.lock及同锁pin，不初始化/修复/写fence；原EX下不新开
   SH/EX、不持lease/control mutex等Source、callback、Wait或Close。active冷Close返回Busy，
   外层先收束再关；在线原fence语义不放宽。
4. 原演练资源Unknown与最终attestation写后Unknown分开；可见MAC不等ready。晚期Sync/物理
   Close成功但报错加真实EMFILE，仍由既有durable gate/retained owner挡冷reader；禁止事后
   dup/重建Unknown兜底。producer资格归R3，不让本片补签或清通用fence。
5. 批准完整生命周期：success退役、可信终结orphan、受证本地crash清理、同总盘cap下一任务、
   单次条件删除与持久intent、Unknown只读零重删、owned调度/关闭、管理员与最终built Web。
   原幂等历史和1000/16MiB界限保留；live archives GC和无限历史仍是独立未完项。
6. 所有文件/worker/FD终结后才退reservation；普通旧T1不因T4 ACCEPT获得same-OFD资格。
   D7 Unknown全额charge，不按前缀认领旧垃圾，不用snapshot guard或重启证明所有reader结束。
   仅远端删除Unknown和关键资源Unknown分别验证App可释放/必须保留SH的真实错误链。
7. 可立即顺序实施backup/state/library内核和负控，不互等R3最终源码。restore/status及最终server
   联调只消费Popper真实冻结依赖并分hunk合并；无依赖时不加生产stub/fake proof凑编译。
   正向验收必须实际生产CLI签出完整proof后消费；内核绿不替代它或完整F4。
8. Go1.27.3、显式CGO_ENABLED=0、无race/新依赖/daemon/客户端moejs。先定向RED/GREEN与
   有界相关包，主统一完整组合，不让各线重复全仓/十八构建。完整累计patch/source/evidence
   冻结并非作者验收后才合主；保留原失败与供应商/原生平台边界。

本片私有实现不得写主线、他人冻结或创建branch/worktree/spawn/commit/push。当前只跟踪
授权文档；实际产品进度与验收必须另记，不能因本节用语推断功能可用。

以下完整保留批准的HANDOFF正文；冲突以本节和权威需求为准。

---

# 完整 F4 retention / owned orphan / 跨任务磁盘回收 — 设计 v3

**状态：最终设计包，供主裁定后实施；未获整体实施授权、未实现、非独审ACCEPT或发布资格。** 本稿取代v1/v2作为实施方案，旧稿/旧SHA不覆盖。实施方式为私有archive内顺序完成一个完整可用片，不spawn/branch/worktree/commit/push，不写main/WIP。确切50路径及原43差异见WRITESET与DELTA；任何第51路径先报。

**固定调查基线：** 58f9b45a9d515df176c482e99ef3fbb5ee966c94 archive+已批准auto exact16，复用原私有1537文件只读源；不追HEAD。**固定接缝：** RETENTION-SEAM-v3 SHA d49df2a33ce55423f417155d023b0f76d5fe9eb91901af06280039eeb8af48fc，输入副本随包。主最新输入的T4非作者限定ACCEPT报告SHA96e53049f2e8032b09e01a6a95fcc0e081659f7c2d0cd7333a197d1c49442dfd按输入记录，优先于seam文档里较早的“待独审”进度文字；不改变固定接口，也不将普通T1资格扩大。

**完整目标：** 实际退役过期完整备份、清可信自有终结attempt、回收可证明无使用者的受证crash工作区，下一真实任务在同一总盘预算内运行；管理员能看见/触发/精确只读对账。不是只加预算/列表，也不将受保护任务改成垃圾。完整success正向资格依赖R3真实生产proof，但设计/内核实施可立即按固定API并行，不互等源码freeze。

## 1. 已定裁定与最小方案

主D1明确：**不降低restore_verified原门**。R3完整离线演练、实际App业务读回、身份及全资源终态后签受证收据；retention消费opaque proof，再同源完整重验。没有真实proof，成功备份零DELETE。保留最新K≥2是附加保护，不替代唯一当前有效已演练锚点（在K外也额外保护）。candidate/T4/helper/MAC-only或管理员bool均不是proof。

主认可D2–D6方向：Unknown只读对账零重删；只认可信自有来源；保留全部幂等历史/1000条/16MiB；绑定4MiB版本sidecar；只保护参与同一本地权威的reader，外部/异机须停prune。本文不将方向认可写成50路径授权。

D7保持：普通Prepare/WithVerified的旧T1 RO worker没有runtime同OFD，不写磁盘inventory（摘要流到io.Discard）；正常结果要actual Wait。父死/Wait/FD不明工作区留全额charge、零success删除，不凭Pdeathsig、snapshot guard可得或T4 ACCEPT解锁。它不是业务writer存活证明，也不为所有普通启动增加永久dirty。T4新EX inspector直接复用算法、不嵌套旧child且始于ready Candidate；其资格不倒流包Prepare。

最简单却不足的方案“按List/mtime删旧目录+预算”被拒：没有可靠引用/终态，也无法保留幂等与Unknown。本方案用现ControlStore同锁状态、原S3 Inspect/Delete和原Capture/worker，增加必要操作账本/工作区归属/reader seam；新依赖、daemon、CLI命令、环境变量均0。live archives GC、无限历史、任意外部前缀垃圾不混入本片。

```text
private policy -> 管理员RPC/Web -> Controller owned单活 + 同锁read pins
                                    |                    |
                           authenticated control.log -- ReclaimID --> reclaim.log
                                    |                    |
                         原catalog/ref authority     workspace owner/quota/intent
                                    |                    |
             R3 cold proof -> trusted server bridge -> 原Source/Candidate重验
                                    |                    |
                         原Inspect/If-Match/Delete   原Capture + explicit stage parent
                                                         原R2 guard/Wait（D7另隔离）
offline R3 EX -> existing-only cold control opener -> 同一reference权威，不开SH/新App
```

## 2. 公开面、策略与兼容

既有私有S3 JSON仅增加四个整数：keep_last=0关闭、开启2..1000；min_age/grace默认86400秒、60..31536000；max_artifact_bytes=0推导、正值至少一次合法最大需求且≤1TiB。字段全名为retention_keep_last、retention_min_age_seconds、orphan_grace_seconds、max_artifact_bytes。strict拒未知/重复/null/float/overflow；不得通过任意URL或path为普通owner授删除权。

四项在generation编码前全清零并omitzero；保留原automatic interval排除逻辑、原generation/options golden、principal+request绑定。目标/key/内容预算/版本变动仍拒跨generation对账。policy不通过新增fingerprint伪造新任务。目标prune只在显式开启策略时授予既有target；不放宽S3/权限/Close规则。

首次未启用且无sidecar绑定时off保持原v1/路径/行为，后三项不暗启tracking。已有绑定后off只停自动删除，仍加载tombstone/所有权/charge和新scratch tracking；只读对账允许，不能重建无归属临时数据。策略开启和auto一样须至少一个有效管理员。

keep顺序使用原Jobs追加顺序，不用UUID/mtime。current generation/options remote_committed且超min_age才候选；保护最新K及真实受证anchor。failed/canceled、CompleteAttempted=false且超grace的可信catalog才是远端orphan候选，先精确确认complete Missing；发现complete有效/冲突/不明先交原业务对账，不能把success重命名为orphan绕D1。

任一generation的pending/capturing/uploading/committing、actual active、interrupted、commit_unknown都不政策清理；未决业务/reclaim Unknown阻自动备份/GC。缺proof只是anchor_unverified，不伪装成资源Unknown；成功删除停，但已确定终结orphan/本地残留治理不因此假装完整success验收已完成。1000条/16MiB满后仍拒新job；数据回收不删除或增加历史登记次数。

新wire：BackupJob tags9..11=retention_state/retention_code/reclaimed_ciphertext_bytes；Status tags5..11=retention_enabled/retention_blocked/retention_active_job_id/artifact_bytes/artifact_limit/retention_running/retention_code。仅新增RunBackupRetention空request返回Status、ReconcileBackupRetention(job_id)返回Job envelope；不复用tag，restore_available仍false。workspace-only有running而不伪造jobID。

鉴权先于读取配置/key/目标，原管理员A/B均可管实例actor；匿名/普通/share/F10 MCP token拒绝。Cancel不是删除授权；旧Reconcile先查retirement不得复活pruned；重复Start同request返回原job并装饰回收状态。输出只固定码/计数/opaque ID，无bucket/prefix/ETag/catalog/raw SDK error或凭据。

## 3. 同锁权威与R3 seam

完整接口/锁序/冷开关闭的实施合同见INTERFACES.md，是本HANDOFF组成部分。原v3签名不改；请主一并批准其明确两点非破坏性补足：typed reference owner校验方法、被动expectation读取（都不是新proof构造）。准确共享status.go hunk列入第50路径，不藏进原43。

Cold opener不得调用会创建control.log/lock、丢弃next和写release fence的普通OpenControlStore/Close。只在已有typed EX下短借FD，existing-only相对打开/认证原backup/control，同control.lock非阻塞，v1只读兼容、v2严格绑定；零mkdir/repair/升级/phase写。缺控制历史拒D1参与，不阻止R3本来允许的独立本地恢复，但该恢复不获得此control权威的prune保护/消费资格。

pin/tombstone在同一controlState.mutex原子互斥，pin覆盖proof/Source/Candidate/workspace全清理及tombstone提交。在线借已有Runtime；离线在R3 WithOperation外层EX生命周期中工作，不new App、再开SH/EX、在FD callback内递归reader。ControlStore的cold Close有独立只读分支，active时ErrBusy而不自等；外层先收束read，再关闭store，再离开WithOperation，最后由R3关原EX。

同源承诺用原I/B/A+Complete ordinal0原密文字节SHA256与bytes，认证链连接bootstrap/root/pages/extents。全认证ObjectRef集合含两头与原catalog逐项全等（含计数/重复/末项），不新造catalog canonicalization、不把ETag当hash。原closure/DB/inventory/schema/app/format/suite/policy与R3 proof一致；新sanitized DB digest永不顶替原DB。当前target generation/options另由backup权威核对。

server bridge只在原v3冷reader返回非零opaque proof后作同源Source/Candidate完整重验。Source是同步函数不是可Close对象；全部defer成功才公开观测refs。Bridge与后置admit全部返回后，Controller才形成内部anchor终态；不在proof/Source callback里提前tombstone/DELETE。真实元数据发现入口解决expected来源，不接受“我已验证”布尔或操作员乱填hash。

## 4. 两种Unknown与晚期ready gate

| 原因 | 可否获得D1 proof/删除success | 谁能收敛 |
| --- | --- | --- |
| 原App/worker/关键FD/原EX Close或Wait未知 | 没有completedDrill，零；不能从MAC/重启/进程消失补造观察 | R3保留原操作边界；无法证明就保持Unknown，不重跑同副本T4 |
| 原演练全部已知，最终attestation写/Sync/Close/响应不明 | 自动消费仍零；visible .drill MAC不等ready | R3显式reconcile只能认证原来确实存在的完整终态图，不能变造原Close |
| 仅终端输出丢失，磁盘/Sync/Close均实际已知完成 | 是否可读取由冷reader实际图验证，不按输出猜 | 独立冷读，禁止基于退出文本替代证据 |
| 纯远端DELETE结果Unknown、资源均已确认Close | D2持久阻mutation，零重删；不必强留OS SH | 本片只读Reconcile仅Missing推进 |
| 本片Candidate/资源Close/Sync或cold owner不明 | critical错误链保留SH/EX/charge，零success | 原scope/owner保留，禁止事后补dup或force |

R3 producer只在完整原Source/T4/probes/资源/原EX全部终结后，独立短EX metadata scope验证未换代并封存1024-byte complete-drill；initial-provenance不能代替。最终attestation证据的资源与原演练资源分账，不声称签名前连签名文件也关闭。

**冷consumer必须验证整张ready图：** selection accepted/drill finalizing/drill ready严格区分；inflight/next/缺成员/未支持role或版本/任意runtime fence/不确定尾部均拒。ready发布晚期Sync/Close失败，即使物理Close实际成功却返回错误且紧接EMFILE，预先存在的同操作durable gate/retained-release权威仍须挡冷读；不得先清gate再靠错误后新分配FD补Unknown。ReadVerified reader及proof.WithSourceFacts前后owner/pin/lineage admit都不能略掉。

这项producer安全义务属于固定seam的R3已批准operation/status/records/publish/lease路径，本片不改kernel来补它。实现若不能通过真实晚期门负控，就不具备可消费proof，success-prune永远0；不是设计再互等或放宽reader。Rollback新代/新operation使旧live proof失效，历史事实保留；普通后续DB/WAL业务写不凭mutable rawhash误抹历史演练。OIDC data/mappings-only或identity-pending无proof，不暗造管理员或假provider。

## 5. 绑定回收账本与远端实际删除

control旧容器magic/HKDF/AAD、原Job历史语义不变。payload v2新增canonical128bit hex ReclaimID；v1零值省略保留旧序列化。Job新展示字段json排除、装饰副本，不改原phase/catalog/request/CompleteAttempted。

reclaim.log≤4MiB，同一control物理锁；独立域cognate/server-backup/reclaim/v1、magic CGBKRC01，AAD绑定instance/ReclaimID/version。strict版本/字段/计数；max1000 victim、2048未释放workspace。只存操作摘要/归属/cursor/当前intent，不复制第二内容索引。先认证空sidecar持久落盘，再control v2绑定确认，最后才有GC副作用；v2缺失/错绑定/篡改拒绝，off仍加载。old binary拒新版本/文件，不能删sidecar降级。

原control与新sidecar都按next写→Sync→Close→Rename→目录Sync确认；目录枚举需覆盖完整6项allowlist，不能保留ReadDir(5)漏检。v1未绑定空认证sidecar只在显式启用时完成绑定，非空/坏/多余next不自动重建。原16MiB近满不能放binding时干净拒功能，不删历史腾空间。

每victim状态：retiring→deleting→all_remote_absent/local_pending→pruned或orphan_reclaimed；另外read_unknown/delete_unknown/delete_conflict。持久计划稳定绑定job+generation/options/catalog摘要，只建一次；保留原上传历史和幂等结果。副作用前encode预留最坏intent/unknown/完成记录空间，账本满不能删后才发现记不下。

删除顺序：真实D1锚点及pins全收束（orphan无需D1但须终态/complete Missing）→同锁持久victim tombstone→成功victim的complete先Inspect完整回读→持久当前ref/index/attempt intent→原同target permit一次If-Match DELETE→仅AbsentVerified+nil推进cursor→再catalog payload。Missing可记确切缺失；Conflict/Unknown停。两头/数据不能重排到complete之后仍可发现为完整备份。

intent后任一点崩溃均按未知，不重发同ref；Inspect Unknown也不得进入mutation。Reconcile只VerifyExact当前intent对象，typed Missing才推进，Present/Conflict/Unknown继续挡，不重mint permit、不清tombstone、不换UUID、无retry/force。该RPC本身不启动下一DELETE；后继显式Run/已开timer才处理下一未尝试项。List遗漏、普通404、mtime不构成absence/ownership。已确认全对象缺失且本地归属完全释放才称pruned和累计确认回收字节。

## 6. 本地crash与跨任务磁盘生命周期

新workspace为backup/artifacts/ws-<128bit>，role job/runtime-prototype/anchor-verify。先sidecar allocated归属/预算，再Mkdir/pin父根dev+inode并持久active；active前零payload/worker。证书含instance/nonce/job或role/generation及reservation。仅allocated且确切空的初始化残留可收敛，意外数据拒绝。不能按crypto/.snapshot前缀认领旧垃圾。

原App prototype在其受证runtime workspace建空engine，不接payload；每job在独立workspace内NewWorkspaceEngine（沿同key/limits）、plain cap通过CaptureOptions/WithContentSnapshotAt/WithLocalSnapshotAt走原闭包，R2 v2/未来R3 v3的frame/FD/Wait均不改。CapturedSnapshot、engine、cap/FD/目录所有Close都确认后释放workspace。API借用有live scope与排空，不让目录FD逃逸。

受证残留仅owner已终结/过grace/无pin才GC；interrupted、commit_unknown及active禁止。对snapshot残留还需原Quiesce→AcquireSnapshotGuard，证明旧child确已退役；父死释放controller lock不是证明。library新薄转发及Store scope登记让Store.Close等GC结束，控制锁外取guard、关guard后才Release token；不持锁等worker。

**D7：** 调Prepare前持久inspector_may_live，覆盖Stage及后续普通WithVerified；任何父死/Wait/Close未知保留resource_unknown及全额原reservation，即使root已缺、snapshot guard可得也不退quota。自动backup/GC及手动success-prune都拒绝，不找另一个anchor逃避它，不用新UUID掩盖。普通App/SQLite crash recovery可照常启动；不是全App脏标记。T4/R3最终proof不自动追踪这些旧T1。现片不force清除，也不以一次EX探针称其解决。

本地删除两遍有界预检、openat/no-follow与逐项身份复核，0700/0600、同UID/nlink1/no设备/无跨dev；仅允许已核producer grammar（snapshot六文件、crypto规范名、Source head/input、Candidate固定布局）。未知项拒整个workspace，不忽略点文件，不RemoveAll。分页128、深度≤5、每workspace项数≤2*Seal.MaxObjects+MaxArchives+128、root≤4096项、workspace≤2048；超限或扫描不完拒新分配。旧未证crypto/.snapshot/意外文件只计账/隔离且字节不动，不动DB/WAL/journal/live archives或别实例。

每unlink/rmdir先记当前条目身份intent；不明后只读ENOENT才前进，存在/换inode不自动重删。所有FD Close、目录空/rmdir、父Sync确认后才退reservation；否则全额charge/强引用。仅确认释放的workspace归属记录可compact，business history和victim unknown永不删除。

预算checked int64：Rboot=1MiB；Rcapture=DB.MaxStagingBytes+Seal.StagingBytes+1MiB；Candidate.MaxStagingBytes=MaxContentBytes+64MiB+256，ArchiveCacheBytes用实际配置（0取512MiB）；Rverify=Seal.StagingBytes+**Seal.InputStagingBytes**+Candidate.MaxStagingBytes+1MiB（Source独立输入不可漏算）。默认global=2*max(Rcapture,Rverify)，显式最小Rboot+max(...)且≤1TiB。used=活reservation+残留max(未确认reservation,实测logical bytes)+legacy bytes，按workspace去重。

先治理已证本地残留，再终结orphan，再success retention；不要为了释放配额先分配anchor导致无路可清。无空间在副作用前拒任务，不在ENOSPC后删保护备份自救。control/reclaim双份next元数据另有16MiB/4MiB界限；scratch cap不冒称整个目录物理blocks/RSS/安全擦除保证。

## 7. producer、停机、管理UI

原StartAutomatic/StopAutomatic签名不变，一个owned goroutine内backup timer与固定300秒retention timer；retention-only也启动。各自首次/每检查后完整延迟，无catchup/jitter、busy跳过无队列，到期retention优先于下一自动backup准入。RunRetention管理员只注册owned工作并立即返回，不把大备份/GC挂短RPC。已注册scope用Controller ctx/JobTimeout；scheduler cancel不取消它，Controller.Close才cancel/join。

controlActive区分backup/reconcile/retention/read，id只放真实job。Controller.Close先closing→Stop producer并join→cancel→锁外等active/read/workspace→关target/engine/key/control/borrowed。资源不明沿现serverBackup/App错误链保留；成功Close nil才最终root释放。callback不得关闭并等待自己owner；cold Close active返回Busy防自等。

Web保留原CAS/session/严格schema/无凭据渲染；显示配置≠健康，真实running/blocked/retired分开。不可逆手动“按当前策略清理”独立确认，Cancel不删。已退役remote_committed仅历史发布事实，未知只给只读对账，无重试/force。原six RPC和幂等历史仍可查，不能伪装恢复可用。新验证脚本独立，不覆盖主canonical fix1 helper；normal npm build后的实际embed浏览器须完整publish→proof→retire/orphan→下个job，并显式stop/Wait/App.Close/真实EX释放。

## 8. 开工、联调与交付边界

主批50路径及DELTA两项补足后即可按IMPLEMENTATION和VERIFICATION实施，无需等R3源码freeze做设计；backup/control/pin/cold/quota/GC及负控可先并行编写/定向跑。restore/status及最终server编译需要真实R3类型/reader，使用其声明接口接入，不复制活WIP、不加生产stub或return nil假proof。不因正向fixture暂缺而降低success门。

真正正向联调需固定R3生产导出与已接受T4；只应用明确patch，分hunk合共享文件。最终以真实生产CLI产生proof再消费，完整实现/物理回收/所有负控及非作者独审一起交付；原FAIL保留，主另做当前组合。Go1.27.3/CGO0/no-race，Go每次改前Modern；有界相关包/单Linux实测binary，不全仓/18build或重复WebMCP。

迁移前off可撤回且无删除；绑定后off仍读tombstone，旧binary拒新版。发生物理删除后不能删sidecar/回滚binary伪装存在；恢复/rollback是另一个显式R3操作。非Linux cold/GC受控拒绝，无不安全fallback。外部/异机不参与reader须停prune；现S3条件删除供应商能力仍需真实后端资格，不冒称MinIO/AWS全供应商。

live archives GC仍是独立未完项；完整历史归档/无限运行、旧无证垃圾强制认领、跨generation旧目标删、外部reader仲裁不在本片完成。完整F4/GA继续依赖真实R3及其他既有发布门；不以此设计或主T4矩阵虚关。
