# R3 完整恢复实施授权与技术裁定（2026-11-01）

本节是主线程在完整 F4 目标内的技术裁定，不是用户逐项原话，也不是代码或运行签收。
下方保留作者 HANDOFF-v2 原文（SHA256 `681717ba55781b3ac393045d0187973b63872884568b88065ad356ff1c5a5350`）；
原文的“尚未授权”是冻结时状态，由本节后继授权取代。精确56路径见同目录
`2026-11-01-runtime-generation-restore-writeset.tsv`，原件SHA256
`86bc31a80a643fd2f3b1f63de64ab0fe6565a64799fcf33e27c0051d97d44a0d`。
实施使用独立私有源码，不写主线或覆盖既有冻结；主线程另做组合、独审、提交和推送。

## 已批准的六项技术边界

1. **typed Resolve / v3**：批准稳定根与实际代目录分离的opaque证明，以及仅为祖先链追加的512字节内部v3和FD12/13。
   保留legacy/v2分支的原身份门、正常双SH及SQLite/WAL恢复；不得删除root==parent等式冒充跨代支持。
   新v3、EX与probe资格必须实测，不继承R2的限定证书。
2. **T4所有权**：消费T4完整sanitize及其私有take seam。在首次候选树rename前移走唯一files/key/删除权，
   旧Candidate/Sanitized Close永远不能删除已移交树；必须等整个WithCandidate和root后置admit成功才ready。
   不在callback中重入EX；T4接口须与作者明确绑定，未冻结前可推进无依赖部分，但不能伪造cap或擅自改T4。
3. **认证记录 / legacy收编**：批准原master经独立HKDF域认证current/intent/qualification的小型版本化格式，
   先持久inflight再副作用、原子单一选择及旧代保留。恢复不换key，旧不合作binary须停用。
   接管同身份实例必须明确原实例永久停用；不以MAC或advisory lock宣称解决split-brain。
4. **两种真实probe**：切换前在独立DB副本做实际登录/各owner HTTP/API/下载/解密，
   archives只读借、Dprobe/Jprobe完整计账，副本不复制回源；切换后selected实际App/Serve严格RO，
   禁DDL/迁移/回填/WAL模式转换/会话DML，真实领域读取并核全部projection/sqlite_sequence及raw DB不变。
   selected不伪造HTTP登录、无普通模式auth bypass；不能为绿测删sequence投影或省前置登录。
   OIDC-only同样保留全部身份映射，不暗建本地管理员；完全断网不能证明实时provider登录。
   无可用本地验收身份时必须区分离线数据/映射已验与identity-verification-pending，不能签restore_verified；
   后继显式恢复OIDC环境并经原有真实认证路径验收才可关闭身份门。不得把只支持本地账号当完整F3/F4完成，
   也不得用合成provider冒称真实外部账号资格。若需要新增命令或第57路径，先给具体接口修订。
5. **reconcile / fence**：批准原请求、原密文、全闭包及只读后置重新认证后的显式收敛，
   同副本绝不重放非幂等T4。restore key不授权删除通用runtime close-unknown fence；
   status只读诊断，不mkdir、改phase或暗开worker。不能以新UUID绕过未决操作。
6. **接受 / 后台 / 回退**：发布与cleanup的确定性分开，accepted不等于最后Close必成功。
   旧同步及自动备份默认mask关闭，只有停服EX下明确批准才启用；回退重新完整T4及全部验收，
   已有新代写入时单独确认数据损失，不直接改current回旧目录、不复活旧bearer。
   Linux/amd64是本片实跑目标，其它平台交编不冒充运行资格，原全平台首发门禁仍开放。

## 交付与验收

必须交付本地原密文断网Source、完整activate/status/reconcile/rollback/后台启用CLI、真实生产App读回、
逐发布/flush/关闭/进程死亡不明矩阵及旧bearer拒绝；不能以SQL helper、手工mv或内部API代替。
所有Go明确CGO_ENABLED=0，无race/新依赖；保留历史失败和新鲜RED→GREEN。T4与本片独立冻结后
主线组合验收，未完成以上及原F4/F12/UI/平台要求前不打正式标签。

以下是冻结设计原文；发生冲突时以本节后继技术裁定与权威需求为准。

---

# R3 完整离线恢复 / 运行代 / CLI Implementation Plan — v2

> For agentic workers: 采用 executing-plans 顺序实施并保留真实 RED→GREEN；本稿仅设计，未授权实现、提交或派生 agent。不得把任何中间内核片称作 R3/F4 完成。

**Goal:** 将既有认证密文、T2 Candidate、T4 Sanitized 接成生产离线 CLI：停服取得 EX，完整撤销，单一代选择，实际受限启动/业务读取，显式开放；失败可在同等撤销策略下显式回退。
**Architecture:** 稳定根锁与 typed data-parent 分离；用原 master 派生认证的小型 current/intent 记录，而不是 DB/archives 两次独立替换或备份日志冒充事务。未知是持久的安全边界，不通过重放撤销、自动选 previous 或新 operation 隐藏。
**Tech:** Go1.27.3、CGO_ENABLED=0/no-race、现有 SQLite/Afero/SealEngine/Cobra；不加依赖、RPC、proto、Web 或恢复包格式。
**Spec:** `docs/superpowers/plans/2026-11-01-offline-server-restore.md` §3–5/§7；`docs/REQUIREMENTS.md:98` F4；`docs/superpowers/specs/2026-10-28-server-backup.md:158` §8。原计划的 T3/T4/T5 产品闭环归入本 R3，撤销实现仍消费独立 T4 coordinator。

**v2修订依据：** 主线程技术裁定，而非用户原始需求改写。v1把真实Login/Logout放到selected唯一sanitized DB后，又要求全部ProtectedProjection不变，存在真实矛盾：`internal/server/ent/schema/session.go:10`使用默认int ID，Login会推进sqlite_sequence，Logout不将sequence还原；T4明确保护全部sqlite_sequence。v2废止这条selected session DML路线，严格按原backup spec§8.8在切换前专用副本完成真实登录/各owner HTTP/API/下载/配置解密；selected后的实际受限App/Serve只读现存DB/archives，验证health与真实领域服务读取，不伪造selected HTTP登录。v1及其SHA原封不动保留，只有本v2可作为后续裁定输入；仍未获产品实施授权。

## 0. 固定依据与资格

- 私有 archive 基线 `c2bf74808acab55033186c04f071a4e4c836392d`；`base.tar` SHA256 `381ad3c3a37dd83913154cdf53e1d027b610c7c394ef5b86b495d052f32b3359`。`source/` 保持原样。
- `inputs/R2-exact14.patch` SHA256 `6b6032e2d6bed3066bfabdbfd26ea128f7e2e7ec05b52983255d684aab43caf2`；只应用在 `r2-reference/` 供定位，不是本片实现。主线程后报58f9b45按同字节合入，R2独审限定 ACCEPT；全Go/vet/18build及150ms正向EX探针UNKNOWN均仅引用主线程收据，未在此重跑。R2资格限Linux同步snapshot/借用，不覆盖T4 EX inspector/RW或本R3。
- T4设计 `inputs/T4-COORDINATOR.md` SHA256 `578bce2cff29315d523782aef74d7156fa08304bd48d829af8168f2ed25e34f4`；计划副本 `inputs/restore-sanitize-main-untracked.md` SHA256 `77686fd607e20b6b941ea24e07cdeb65adaebccbeddf54a61b745a593aea4e82`。后者读取时未跟踪，主线程后报已随58f9b45跟踪；不覆盖副本、不追新HEAD。T4私有实现/独审是前置，不称已交付。
- 采用 writing-plans、DDIA、ub；Modern Go完整1.27.3列表已读。一次有界Nowledge thread查询无命中；依据实际源码及上述合同。仅阅读、archive和私有设计，不跑构建/测试、不访问真实key、不动auto/R4/R2/T4冻结或main。

## 1. 首要跨片冲突：禁止删除 identity 检查当修复

以下行号中 **B** 指 `source/` 固定基线，**R2** 指只读依赖视图 `r2-reference/`：

| 实际接点 | 现有事实 / R3必须改变的契约 |
|---|---|
| R2 `internal/server/state/store.go:41`、`:90` | Options.Runtime借root，强制其inode等于DB parent。generation DB的parent不是stable root，当前会正确拒绝，不能只删等式。 |
| R2 `internal/server/state/local_snapshot_worker_linux.go:31`、`:46`、`:247` | parent发送前检查root==source.parent，child再次要求request.root==request.parent。两端都必须消费新的目录链证明。 |
| R2 `internal/server/state/local_snapshot_worker.go:16`、`:78` | v2固定448-byte frame，尾16字节仅root dev/ino；缺generation/ancestor/current绑定，不能把root字段偷偷改成data-parent。 |
| R2 `internal/server/runtimegen/worker_linux.go:12`、`:26` | SH/EX共用原root/lock FD，同一lease mutex前后admit；保留原API给T4，不能重入它或关borrowedFD。 |
| B `internal/server/app_runtime.go:57`、B `internal/server/app.go:172`、`:199`、`:243` | 现在先CheckLegacy，再读key，再以DataDirectory拼DB/cache。须改为lease→既有私有key只读→Resolve→同一typed data。 |
| B `internal/server/state/masterkey.go:17`、`:33`、`:99`、`:143` | 复用私有文件/身份/Close检查；有current/intent/历史数据缺key不生成。已有key可只读加载；仅严格fresh lease可生成新key。 |
| B `internal/server/backup_runtime.go:68`、`:90`；B `internal/server/app.go:285`、`:324` | backup/control、artifacts、refresh锁与logs留stable root；DB/archives才随代切换。绝不整体替换Config.DataDirectory。 |
| B `internal/server/state/store.go:159`、`:167` | Store会Schema.Create和兼容/历史初始化；恢复probe/代首次开库必须走typed exact-schema opening policy，不能先迁移再验真。 |
| B `internal/server/restore/candidate_source.go:13`、B `candidate.go:221`、`:331` | PrepareCandidate须等Source清理成功，原Candidate是one-shot且有unlink权；只读Evidence不是发布权。 |
| T4合同§2–3 | WithCandidate重验并借EX；takeForPublication在首次候选rename前转移唯一删除权。其接口/后置admit结果必须随T4冻结。 |

**推荐不变量：** stable root/lock永远指原生命周期权威；data-parent只能由Resolve在同一lease下、认证current后、逐级openat/NOFOLLOW得到。Store和worker不接受用路径字符串、同设备、布尔“resolved”、caller dev/ino或key hash替代这个opaque证明。legacy仍保留原root==parent检查。

## 2. 能力图与拟定API（不是现有API）

```text
private original master -> Runtime(SH) -> authenticated Resolve -> Resolved -> Store + cache + snapshot child
private input + explicit I/B/A/op -> OfflineRoot(EX) + operation lifetime barrier
  -> existing Source/Seal/Open -> ready Candidate -> complete T4 -> Sanitized
  -> scoped take + root post-admit -> publicationOwnedFiles -> authenticated intent/current
  -> restricted production App/Serve + business reads -> accepted; background remains off
explicit rollback -> EX -> isolated previous copy -> fresh complete T4 -> same publication/verification
```

```go
type Resolved struct { state *resolvedState }
type RuntimePaths struct { Stable, Data, Database, Archives string }
type DataFiles struct { state *dataFilesState }
type SnapshotFiles struct { state *snapshotFilesState }
type PrepublishProbeData struct { state *prepublishProbeState }
type SelectedReadonlyData struct { state *selectedReadonlyState }
func (runtime *Runtime) Resolve(ctx context.Context, originalMasterKey []byte) (*Resolved, error)
func (data *Resolved) Paths(ctx context.Context) (RuntimePaths, error)
func (data *Resolved) WithDataFiles(ctx context.Context, use func(context.Context, DataFiles) error) error
func (data *Resolved) WithSnapshotFiles(ctx context.Context, use func(context.Context, SnapshotFiles) error) error
func (root *OfflineRoot) WithOperation(ctx context.Context, operation [16]byte, use func(context.Context) error) error

type Input struct { state *inputState }
func OpenInput(ctx context.Context, privateConfigFile string) (*Input, error)
func (input *Input) Close() error
func Activate(ctx context.Context, root *runtimegen.OfflineRoot, input *Input, request ActivateRequest) (Result, error)
func Status(ctx context.Context, stableRoot string, originalMasterKey []byte, operation [16]byte) (Result, error)
func Reconcile(ctx context.Context, root *runtimegen.OfflineRoot, input *Input, request ReconcileRequest) (Result, error)
func Rollback(ctx context.Context, root *runtimegen.OfflineRoot, input *Input, request RollbackRequest) (Result, error)
func EnableBackground(ctx context.Context, root *runtimegen.OfflineRoot, request BackgroundRequest) (Result, error)
```

- 四种request都严格包含非零Operation、ExpectedCurrent与ConfirmDigest。Activate另含Replace/ExpectEmpty且互斥；Rollback另含FromOperation与AcknowledgeDataLoss；Reconcile另含ObservedDigest及精确Decision枚举；Background另含显式允许项mask。Backup I/B/A、key ID和源预算固定在Input并进入request摘要。Result的Phase与LastConfirmedPhase使用封闭枚举，SelectionCertainty和CleanupCertainty分开，zero Result不表示成功。
- v2明确替换通用RunRestoreProbe/ProbeOptions，不同时保留有歧义的role bool。准确拟定接口为`server.RunRestorePrepublishProbe(ctx context.Context, data runtimegen.PrepublishProbeData, options PrepublishProbeOptions) (PrepublishProbeReceipt, error)`与`server.RunRestoreSelectedReadonly(ctx context.Context, data runtimegen.SelectedReadonlyData, options SelectedReadonlyOptions) (SelectedReadonlyReceipt, error)`。两种opaque data无公开构造，均由同EX owner为已pin的副本或selected分别签发，不互相转换；普通Runtime/Store不能拿它绕锁。server只依赖runtimegen/state/library等，不import restore，避免包环。
- PrepublishProbeOptions绑定Operation、源/新DB/protected摘要、limits，以及独立原key/明确现有登录身份密码的借用pipe；SelectedReadonlyOptions只有Operation、预期selected记录/DB/完整projection身份、readonly limits和原key pipe，**没有session/token/password/owner-auth override字段**，不能把前置副本session带过去。Receipt类型也分开，只给比较事实，不签发activation cap；真实App/Serve/worker/自身资源全终结、root后置admit成功才能交付，coordinator分别要求两个不同角色的receipt。
- state新增窄入口分别为`OpenRestoreProbeCopy(ctx context.Context, data runtimegen.PrepublishProbeData, key []byte, options Options) (*Store, error)`及`OpenRestoreSelectedReadonly(ctx context.Context, data runtimegen.SelectedReadonlyData, key []byte, options Options) (*Store, error)`；共用现有构造/验证核心但opening policy由typed data确定，不新增普通配置bool。前者只在副本中允许正常登录/会话SQL，后者从打开开始即mode=ro/query_only，无prepare/create/schema/normalization/backfill/WAL-mode转换或session写入口。Linux实现及other Unsupported均在既定restore_runtime两文件内。
- Resolved无公开构造/字段，不保存原key；绑定Runtime指针、lease epoch、当前记录摘要、generation UUID和逐级FD身份，一次Resolve固定到App关闭，不在请求中追current。Runtime.Close在所有consumer结束后关闭这些自有FD，最后关root/lock；Close不明走现有fence/quarantine。借FD不关、不逃逸、不在自己的callback内Close owner。
- State.Options保留R2的Runtime（旧legacy消费者语义不变），新增DataParent `*runtimegen.Resolved`；同时出现时须属于同一Runtime。App总是传Resolve结果。没有DataParent的Runtime只能通过原legacy等式；generation必拒。不把raw evidence加到Options当cap。
- Store的generation/probe路径用pin住的parent FD构造SQLite地址`file:/proc/self/fd/<parent>/server.db`，不是`/proc/self/fd/<DB>`，sidecars仍在正确parent。generation必须existing DB且mode=rw而非rwc，缺DB/archives拒绝，不自动新建空库/缓存；只读角色用ro。文件创建/检查、snapshot source保留都复用同一parent的openat与named+FD identity；不先EvalSymlinks重开另一个parent。正常legacy/fresh路径保留。
- SnapshotFiles是scoped opaque loan，借stable root、同OFD lock、data parent；generation额外借generations目录与只读current FD。每次前后验证命名链/current摘要且原post-admit成功后才能交结果。T4旧WithWorkerFiles保持原合同。
- WithOperation只为EX owner增加整段逻辑生命周期barrier，不长期占lease mutex：Close先cancel，有界等外层operation结束后再物理close（外部Close默认10s排空，超时ErrUncertain且不释放仍被依赖的自有guard）；T4与发布各自短借同一EX，不嵌套loan。排空不明保留自有guard/资源，不假定Close错误还持锁。普通SH不增加crash-dirty标记，也不互斥。
- Input是本片构造的冷输入owner：固定local或S3、I/B/A、原key/recovery ID和预算，禁止caller塞任意CandidateSource callback伪绑定backup身份。仅调用已有可信S3 Source或本片local Source，两者BeginOpen都用同一个expected Attempt。源scope全部清理退出后才运行T4。Result只含operation、phase、last-confirmed phase、selection/cleanup certainty、摘要/计数和bare code；err不表示未切换。

### 最小、明确版本化的snapshot扩展

保留v2 parser/dispatch与无Runtime/legacy负控，新增生产runtime-bound v3，**理由仅是v2无法表达分离的ancestor证明**，不是备份密文新协议：固定512 bytes，旧448布局外增加generation UUID16、generations dev/ino16、current SHA25632；magic/version/mode严格区分，reserved必须零，exact length/EOF。legacy-v3这64字节全零且不继承额外FD；generation-v3必须全部齐备。

原FD3..11位置不改；generation追加FD12=generations、FD13=current（只读）。child在SQL之前校验所有类型/owner/mode/nlink/方向、自身image/request nonce；root下`.runtime.lock`仍匹配FD10且同OFD来自typed loan，绝不LOCK_UN/重open取得替代锁。逐级Fstatat(root,"generations")、Fstatat(generations,canonical UUID)必须匹配FD12与data-parent FD6；current FD须匹配root命名项、大小/严格record形状与frame摘要，body generation也相同。child无master，不声称独立MAC认证；MAC权威来自已Resolve的受信parent，frame仅比较事实，实际目录链/FD必须独立证明。保留源DB、stage、snapshot lock、sidecar、AS/CPU/heap/FD/限流/Wait/Close全部旧门；拒绝未知/多余协议继承FD、错分支和v2跨代伪装，不把Go运行时自行打开的poller FD当协议FD误拒。不能把150ms EX探针UNKNOWN改写成该新链路合格。

## 3. 布局、原key认证与durable事实

```text
stable/.runtime.lock + .runtime-fences/        existing lifecycle authority
stable/runtime.current                       regular0600, authenticated single selector
stable/restore.intent                        regular0600, last/current operation authority
stable/restore/operations/<op>.record         bounded terminal receipts, no secret/body
stable/generations/<uuid>/{server.db,archives/,inventory.jsonl,qualification}
stable/.restore-<op>/candidate/               existing T4 production namespace
stable/{logs/,backup/, .source-refresh.lock}  destination-local operational state
```

- current512 bytes、intent1024 bytes、qualification1024 bytes：固定有界binary v1，末32-byte HMAC-SHA256，原key经HKDF-SHA256三个独立domain派生，typed fields/长度/枚举/零reserved严格解析；不存任意路径。记录schema/minimum reader/instance、op、generation/previous、request/expected-current摘要、源I/B/A/closure、原与sanitized DB身份、撤销策略/R、policy与phase/sequence；qualification绑定发布来源，记录不是重新认证的CGBK。复用已有原子发布/秘密清零模式，不借ControlStore或复用其加密格式。固定小控制格式是跨重启认证所必需的新资产；密文格式/suite/page/complete保持原样。
- 每次变更先写唯一O_EXCL/NOFOLLOW0600临时项→精确写→file Sync→Close→rename→parent Sync；目录均0700/private/no links，绑定逻辑instance与key ID，进程内另pin dev/ino。不要将可随重挂变化的st_dev当持久实例ID。一个active intent，最多1000个terminal receipt且总控制预算有界，满则拒绝，无自动prune。
- intent是唯一active authority；每个副作用之前持久化其inflight phase。terminal receipt成功持久后才可替换成下一operation；同op同request只返回原结果，不重跑，异参数conflict。无time/mtime/“最新目录”推断。乱序、部分/错MAC/未知版本、缺current引用、未知`.restore*`痕迹均NeedsAttention。
- Resolve在任何SQL/cache/log/后台构造之前认证current、intent与qualification的选择关系。legacy只在完全没有恢复痕迹时接纳；fresh key初始化仍只能在原WithFreshRoot证明的严格空根。已有key只读加载，恢复绝不Initialize/rotate key。当前/intent结果不明不启动、不自动previous、不自动replay。
- **正常运行后DB可变。** sanitized DB raw hash只证明发布来源；accepted代正常重启不要求DB字节仍等于它，不用immutable忽略WAL、不拒正常SQLite恢复。Store继续原key/policy校验，恢复代精确schema门拒绝新/未知schema且跳过迁移/兼容修正；正常双SH、合法热WAL和SIGKILL后重启为强正控。
- stable不rename。首个generation布局在EX中有独立layout-inflight：旧库先由窄worker正常SQLite恢复/checkpoint并确认关闭（不Store迁移、不复制活动WAL/sidecar、不手删未知sidecar），再把完整旧DB/archives收编previous。两次旧数据移动期间intent挡所有新startup；root/server.db放非regular拒读占位，旧binary必须在转换前由supervisor禁启。不承诺advisory lock能约束不合作旧binary。
- legacy checkpoint与rollback snapshot不能借普通Store打开inactive/live路径。新probe worker仅有固定`legacy-checkpoint`、`rollback-snapshot`、`prepublish-copy-probe`、`selected-readonly/postinspect`角色：typed EX data证明限定stable legacy、已认证previous、专用副本或selected；只有前两种允许对应有界恢复/快照，第三种允许副本登录SQL，第四种绝不拥有RW打开权限。复用既有snapshot/T1 SQL核心与资源控制，不复制一套SQL或让旧T1变RW。新角色各自须真实子进程验收；不继承R2 snapshot-SH资格。

## 4. 完整顺序、唯一删除权与显式回退

| 顺序 | 必须持久/实际完成的边界 | 失败语义 |
|---|---|---|
| 1 | CLI明确I/B/A/op、预期current或严格empty、目标替换确认；外置原master/recovery key只读；停服并EX；持久preparing intent | 拒绝不等于开始；intent已写则真实interrupted，不伪装无操作 |
| 2 | 完全断网local或只读S3→原Seal/Open→Source完整退出→Candidate；same EX下完整T4一次撤销/真实Commit/Wait/Close/Sync/后验 | T4 unknown不重跑同副本，无publication cap，current不变 |
| 3 | T4 WithCandidate callback中仅takeForPublication，转移files+key的唯一删除权；等整个callback/root后置admit成功，publisher owner才ready | 不在callback里重入root；失败保留/清理按真实ownership，旧两个Close永不unlink已take树 |
| 4 | 从sanitized DB复制专用private DB；切换前在该副本运行完整真实App/Serve登录、各owner HTTP/API、下载、配置解密；archives只读借、不复制，原sanitized DB/inventory/archives/hash/全部projection始终不变；副本完整Close/Wait后退役、从不复制回去 | 显式预留Dprobe+Jprobe，不足先拒绝；副本sequence可变不是源sequence变化，不省登录验收 |
| 5 | 持久layout/publish-inflight，完成必要legacy收编，sync/rename候选目录入generations，写qualification；再写switch-inflight | 此前已take；第一次候选目录rename后任何旧owner cleanup不得删除现场 |
| 6 | 持久current选择目标→父目录Sync；写activated_unverified；此时普通serve仍拒绝 | rename可见点≠durable确认；晚期Sync/Close/取消/响应不明保留真实inflight/selected事实，无自动回滚 |
| 7 | 同EX，用SelectedReadonlyData启动实际受限App/Serve；现存DB+archives只读，private listener仅health，不登录、不迁移、不回填、不DDL、不转换WAL模式、不创建/注销session；以真实领域服务直接读取所选代业务；真实停止并join | 失败记activation_failed；unknown保留选择与owner，不能写“旧代已恢复”或伪造selected HTTP登录 |
| 8 | 完整关闭后独立T1+T4只读后置/全部ProtectedProjection校验；selected原raw DB hash、sqlite_sequence、文件集合与projection前后全等，原归档闭包不变 | 不通过忽略sequence/重新签一份hash/二次撤销/清理session补救；任一变化、Close/Wait未知均不accepted |
| 9 | current mode与intent一致地持久为accepted，terminal receipt持久；背景任务mask仍关闭；CLI完整关闭Input/publisher/EX并汇总cleanup结果 | response丢失status返回已持久事实；Close不明不把accepted改成failed/rolled_back |

控制记录自身的原子rename自然先于T4；“第一次rename前take”严格指首次候选数据目录的移动，不能误读成先移动数据、后补cap。旧布局数据移动前也已完成take并持久layout intent。

**两种真实受限启动不是普通server加公开bool。** 新隐藏probe进程仅由同exe父级以EX同OFD guard+严格FD/frame+nonce调用；不同typed data、opcode、options、receipt区分可写副本与selected只读。复用生产App/Serve及领域服务，无新RPC/普通模式auth bypass。原key、前置阶段登录密码各用独立只读pipe，stdio无正文；seccomp/no_new_privs拒外联socket/connect，仅允许已继承private Unix listener必要accept，Rlimits先于SQL。两阶段均exact schema、无DDL/迁移/回填，不初始化OIDC discovery/device/refresh/Lists/backup/native/plugin或任何落在selected里的日志/cache policy/lock写入。所有App.Close、Serve terminal、listener/transport/pipe、worker Wait和scope后置必须真实成功，不能忽略defer Close返回值。

**切换前完整资格：** 新Dprobe文件先精确复制/hash比对sanitized DB，再在此副本开应用登录路径；真实HTTP/各owner API、Pack/download hash和配置解密均在该副本完整执行。只读archives loan仍指sanitized archive closure，不复制、不改policy。副本可新增并注销session、推进自己的sqlite_sequence，这些变化只属于待丢弃副本；不得作为新sanitized DB替换源。独立MaxProbeDBBytes与MaxProbeJournalBytes加入总staging溢出安全预算；私有副本采用已确认DELETE/FULL，无WAL/SHM，Jprobe保守上界2*DprobeMax+64KiB，FSIZE/max_page_count/SQL/CPU/AS/时间同受限。没有足够预算不能降为只测SQL或跳过真实登录。

**selected后零写资格：** OpenRestoreSelectedReadonly必须在文件层O_RDONLY、SQL mode=ro/query_only、内存temp/mmap关闭打开**现存**DB；不可走prepareDatabaseFile、Schema.Create、proof创建、兼容转换、backfill、journal_mode设值、session DML，archives也无policy/lock/evict写。private listener只开放health；内部验证器调用真实library/packs/configuration读取方法遍历各owner与完整闭包，不包装成已认证HTTP用户，不接受caller owner override，不把副本session或伪token拿到selected上“登录”。普通HTTP鉴权代码完全不加后门。selected rawhash/全projection/sqlite_sequence/文件集合前后保持，真实服务读取只证明所选代可读；HTTP登录资格只来自前一阶段，报告必须分栏，不能合并冒称selected HTTP登录成功。

OIDC-only或无可用现有本地密码身份时，断网成功登录不能伪造；§7待裁定前不能签完整登录资格，不新建账号/复活旧token。该限制不是删除原spec§8.8切换前真实登录义务；两owner本地密码全链仍同片交付。

**显式回退：** 不是改current指针到旧目录。先停并join新服务、EX、绑定原operation/expected selected digest及新的rollback operation；旧代原件保留，用既有受控snapshot产生完整私有DB副本（含合法已提交WAL，不复制sidecar）并物化所需archives闭包；完整验真→同一EX内重新完整T4撤销→相同take/probe/publish/受限启动/accepted链。rollback自己的intent先持久，失败/unknown保持停服，不自动再切回；无previous、新旧key/外部配置不足或磁盘不够拒绝。有正式写入后回退必须确认丢弃新代写入；不宣称合并/RPO0。未决原operation须先显式核对收敛，不能靠新UUID掩盖。

**late cleanup精确边界：** selection/intent不明必须拒启；已确认accepted后仅响应/最后物理close未知不能虚称未切换。保留原强引用及现有.runtime-fences规则：close报错不证明锁仍持有；若标记删除已可见后才报晚期错误，不能再承诺所有新进程必被旧锁挡住，原能力永久quarantine。status分别报告已认证phase与cleanup uncertainty，不能为“收敛”重试close或回写已失权目录。

## 5. 生产CLI与断网Source（同片必须交付）

- `cognate-server restore plan --config-file <private-file> --operation <uuid>`：只读解析/认证输入、显示非秘密request摘要和预期目标，不开App、不创建key、不取代最后EX重验。
- `restore activate --config-file ... --operation ... --confirm <request-digest>`：一个进程跑§4完整链；明确backup+attempt+instance来自private配置，非空target须有replace和expected-current确认。无静默latest/默认备份/环境原文key。
- `restore status --config-file ... --operation ...`：仅打开已存在private文件并精确读取/认证，不mkdir/创建锁/改phase/恢复worker；并发变化读两次不一致报Unknown。fence存在也能给只读诊断，不绕过它取得写权。
- status只加载root及原master的private引用，不要求恢复KEK/S3凭据/原Source仍在线；OpenInput为冷解析，秘密按具体子命令才加载。reconcile需重新验原密文时才要求对应local对象集与恢复key，不能借只读status暗中重开engine/worker。
- `restore reconcile --operation ... --decision acknowledge-selected|retire-unpublished --confirm <observed-digest>`：EX+原请求+原密文完整重认证；selected还须重新pin全闭包、T1/key/schema、T4只读撤销后置及从原源重算的ProtectedProjection（MCP恰+1规范化），不把落盘qualification/MAC摘要单独当重启cap。只对能证明的选择/终态显式确认，不执行第二次T4/rename；无法证明即NeedsAttention。不能自动删通用runtime fence；残留fence的恢复权限须依现有独立释放合同裁定，不拿restore key绕过。
- `restore rollback ... --from-operation ... --operation <new-uuid> --expected-current ... --confirm ... [--acknowledge-data-loss]`：完整上段回退，不留给用户手工mv/SQL。`restore enable-background --operation ... --confirm ...`在停服EX下持久允许配置的后台项，默认仍off，不因为重启/配置中旧interval而自动恢复。
- 配置严格private有界（<=64KiB），只放key/密码/可选S3凭据的private文件引用及预算，不接受原文key argv/env/stdin混用，不记录原始配置/parser错误/路径正文。local模式不得读取任何S3配置/凭据，不构造S3 client。
- local对象目录保持原`<root>/<instancehex>/backups/<backuphex>/<attempthex>/{bootstrap,complete,page,data}/%08x`对象键布局，不再打一个新明文tar/JSON索引。新增backup只读窄helper `WithLocalRestoreHead(ctx, objects *os.File, prefix string, expected Attempt, consume func(context.Context,*os.File,*os.File)error) error`与`CopyLocalExact(ctx, objects *os.File, prefix string, ref ObjectRef, output io.Writer)(VerifyResult,error)`，在backup包内复用已有private Attempt/ObjectRef和objectKey，避免为跨包访问发明第二种ref结构。
- restore.LocalCandidateSource复用原engine.BeginOpen→NextRef→受控单原密文临时文件→AcceptFile→Finish→bundle callback；零ObjectRef,nil为完成，不是EOF。head仅短scope；session用outer ctx。完整头、所有pages/extents、密文hash+AEAD由现有engine证明，不能信被动complete。local/helper逐级NOFOLLOW、regular0600/private/nlink1、命名+FD+exact length/hash、受限完整枚举缺/额外对象、count/bytes/时间cap；不执行文件。借目录/writer不关闭；自己全部Close/Sync/取消排空错误汇总，失败无ready Candidate。InputStagingBytes已预留的一个窗口不重复charge。

## 6. 提议精确写集：56路径（尚未获实现授权）

完整机器清单见 `WRITESET.tsv`；无通配/“必要其它文件”。M为修改现有，N为新建，均是**未来R3差分**，不含R2/T4依赖源。现阶段这些产品文件一个未写。

| 路径组 | 精确责任 |
|---|---|
| runtimegen 10 | Resolve/typed loan、认证小记录、publication/状态读取、分离PrepublishProbeData/SelectedReadonlyData、lease资源终结；不改R2 worker借用API |
| state 12 | Store typed parent+exact restore policy、FD source复用、v3帧父/子严格验证、fresh key兼容；restore_runtime两实现分别开副本RW与selected现存RO，不再共享session写策略 |
| library 3 | 两阶段都用typed只读archive view，不创建policy/lock、不写/evict，不放松正常cache |
| server 6 | App解析代路径/稳定root拆分、两个窄API/receipt、前置副本真实登录HTTP及selected只读实际Serve/health/领域读取；无普通模式auth bypass |
| restore 8 | Input/operation状态机、Dprobe+Jprobe预算、完整激活/回退、两receipt独立门、status/reconcile/local Source及真实反例 |
| backup 4 | 只读local heads/exact copy；不改seal/manifest/容器/ControlStore/传输内核 |
| servercli 4 | 命令/私有配置/确认/结果分层，不调用正常RunE绕到listener |
| cmd 3 | 新probe dispatch、v3实际exe验收、完整离线产品演练 |
| docs 6 | 记录已批准布局/权限/CLI/真实资格，改原spec未落实条而不冒称F4/GA |

v2路径成员仍是56（21M/35N），见WRITESET-v2.tsv及WRITESET-v2-NOTES.md；两个API与角色在既定resolve/restore_runtime/restore_probe/activate及测试文件实现，无需第57路径，不新增session/schema/auth生产文件。T4的sanitize/candidate文件不在本写集；唯一删除权seam须由其已批准实现提供。`cmd/cognate-server/main.go`与T4有dispatch接点，必须在T4 freeze后按hunk串行组合；App/store/worker与已收R2同理。不得回写旧app.go/旧v1 fixture或顺手改Auto。若seam不满足§7或实施时确需第57路径，先报具体原因再授权，不隐含扩写。

## 7. 必要技术裁定 / 真实阻碍（不是用户原话）

1. **typed bridge + v3**：批准本稿目录链证明及512-byte内部版本扩展，不能仅放宽R2等式。R2的限定ACCEPT不涵盖此新组合。
2. **T4私有seam须先冻结**：take返回自有publicationOwnedFiles、移走unlink/key责任，且能在整个WithCandidate/root post-admit成功后才ready。若T4只给callback内使用、禁止移出owned owner，则当前设计不能安全跨loan发布，须先由主裁定seam；不在R3暗改T4 scope。
3. **认证小控制格式/legacy收编/最小reader**：同意fixed current/intent/qualification及原key HKDF域；明确旧binary supervisor停用、非regular哨兵和同实例原key约束。首版不同key的覆盖灾备仍拒绝；同身份异机接管须显式确认旧实例已永久停用，MAC不能证明split-brain已消失。
4. **probe opening已获本轮主线程技术裁定，登录特殊前提仍待定**：完整真实登录/各owner HTTP/API/下载/解密必须在切换前专用DB副本，archives只读借、不复制并严格计Dprobe+Jprobe；selected后实际App/Serve仅RO现存DB/archives，零DDL/迁移/回填/WAL-mode/session写；health与领域服务读取验证选代，不伪造selected HTTP登录。OIDC-only/无本地密码账号的断网登录仍不得造假，需明确接受的验收前提后才实施该分支；不将主裁定偷换成取消登录义务或普通模式auth bypass。
5. **reconcile/fence边界**：恢复操作不能清通用runtime close-unknown fence。若要求CLI也修复所有历史释放不明，需另给fence关联/证据授权合同，不能把legacy8-byte marker当已认证restore intent；本片先只读诊断并保持拒启，不把人工mv当正常activate/rollback。
6. **accepted后的背景策略与数据丢失**：明确默认mask掉旧sync/自动备份，显式EX enable；回退可能丢accepted之后写入需新确认。首发平台运行资格Linux amd64，其它平台编译且Unsupported；T4/probe沙箱/文件系统fsync资格必须独立，不借R2收据。

上述是设计待主线程技术批准的具体前提，不自称已有授权。本稿完整列出不可省的产品范围；阻碍不等于将activation、OIDC验收或释放修复悄悄留成手工步骤并报完成。

## 8. 实施顺序与红绿验收（最后一项前不称R3完成）

### A. typed Resolve / Store / worker全链
- [ ] 永久真实反例先RED：legacy正控；认证generation+DBparent!=stable在旧R2真实Store/snapshot exe拒绝；不能只编译失败。最小桥GREEN后加入wrongroot/forgedcap/expired loan/掉ancestorFD/换gen/current inode/同设备foreignparent/extra/truncated frame/旧v1v2分支负控、末页密文与记录预算不退化。
- [ ] 同一正常数据库两个SH服务、真实WAL未checkpoint提交、SIGKILL重启，均能业务读取；EX仍受父/worker同OFD阻挡。正常restart无全局dirty/fence写入。key缺/错/31/33字节、policy/schema/min-reader/current/intent MAC/partial/unknown拒绝，hook证明SQL/cache/log/后台未启动。

### B. local Source与控制记录 / ownership
- [ ] 从原Seal生成含binary资源/共享archive/两个owner/加密资产的真实对象集，禁网络且移走S3凭据，完整Source→Candidate→T4。错误complete/page链/最后extent/缺多对象、额外项/links/traversal、大小/count/Input窗口/取消/Close未知均无cap；借FD/原key slice不变、所有自有资源实际join，日志秘密negative scan。
- [ ] 真实落盘current/intent/qualification及current选择点逐步RED→GREEN；丢/改/重排/旧reader/错domain全部failclosed。真实rename后调用旧Candidate.Close/Sanitized.Close内容仍存；去掉take的可编译mutation必须导致删除反例红。持久inflight遇进程死不自动重跑T4或选previous。

### C. 完整生产activate/rollback与cleanup
- [ ] 实际CGO0 `cognate-server`二进制：停服务→离线CLI明确backup/op/确认→完整T4→专用DB副本实际App/Serve完整登录/各owner HTTP/API/下载/配置解密→副本终结→publish→selected只读实际App/Serve health+真实领域读取→检查Close/Wait/源rawhash及全projection不变→accepted→普通真实server启动/正常登录与业务读。两阶段资格分列；旧cookie/share/MCP/native/refresh/device ticket拒绝，新PAT/device重授权按既有R4有效；无外网/插件/自动任务。
- [ ] 永久 `TestR3SelectedReadonlyPreservesSequenceAndRawHash`：真实SQLite session int ID正控证明专用副本Login/Logout确实推进自己的sqlite_sequence，但源sanitized DB/projection保持；减门把同一真实Login/Logout接到selected唯一DB时，rawhash与包含完整sqlite_sequence的projection都必须检测失败，不允许只检查sessions最终为空。selected正确路径零session DML、零DDL/回填/迁移/journal_mode设值，无新DB/sidecar；副本receipt不能替代selected receipt，selected没有密码/session输入仍能完成health/领域读。`TestR3PrepublishProbeBudgetAndOwnership`覆盖Dprobe/J溢出/不足/清理未知及副本绝不copyback。以上是拟验收，当前未执行。
- [ ] current rename前后、fsync、file/dir Close、EX最后close、response丢失、App/worker排空、**仅副本**login/logout及selected readonly postinspect每一边界故障注入+新进程status读真实phase；不把mock成功/进程消失当join。取消不覆盖unknown；exact same op不重放，new UUID不能绕active unknown。
- [ ] 当前激活启动失败、业务读失败、无previous、磁盘不足、schema更新、原key错误、篡改previous均有真实反例；停并join后CLI rollback重新完整T4，重启旧业务代但所有原bearer仍拒绝。包括旧代有热WAL、新代已接受写入后的明确data-loss确认、rollback rename未知不假回退。
- [ ] 技术资格命令固定Go1.27.3且每条CGO_ENABLED=0：定向runtimegen/state/library/server/restore/servercli、`cmd/cognate-server`实际exe链与受影响vet；用明确TestR3前缀及既有受影响回归，检查非零测试匹配。只构建一次真实server用于进程矩阵，不跑全仓/18build/race。当前仅设计，以上未执行，不给假PASS数。

### Review Focus（与测试一一对应）
1. generation换parent被误当合法：A的real Store/worker/目录置换负控。
2. 正常WAL/双SH被恢复fence误伤：A的crash与双实例正控。
3. T4借用重入/旧owner rename后删除：B的后置admit、Close竞态、take mutation。
4. late Sync/Close未知被伪装成rollback：C的实际落盘/新进程逐边界status。
5. 回退复活旧权限或probe偷偷联网/迁移/改sequence：C的旧bearer/网络sandbox/schema、真实副本登录→selected RO的rawhash/全部projection减门反例及全链业务验收。

**v2自查结论：** 已移除selected session DML与“删除session即可保持projection”的错误前提；真实完整登录义务只在切换前副本完成，selected实际Serve严格RO。56路径成员未变，API/责任修订明确；没有放松T4完整sqlite_sequence投影、改旧schema、删登录义务、加普通auth bypass或手工activation。其余裁定待主逐项批准；产品源码未改，以上拟测试未跑，不是实施/独审/完整F4声明。
