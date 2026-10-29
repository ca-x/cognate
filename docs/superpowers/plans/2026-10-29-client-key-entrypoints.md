# Client Key Entrypoints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 本轮仅编写计划；不授权 Go、启动 agent、stage、commit 或 push。源码实施须主线程另授 exact 写集，非作者逐片验收。

**Goal:** 让 CLI 与 MyGo 原生客户端只在明确初始化动作生成默认根 key，所有默认根本地状态打开先经同一已签 core 准入，缺 key 不再以零订阅自动重建。

**Architecture:** 直接复用 `internal/clientkey`；CLI 是无 bool 薄代理加显式命令，原生是现有 Backend/Service/Model/账号页按钮。core 的双锁在返回前释放；其后旧订阅 manager 的网络/安装 Session 生命周期不重构。原生文件操作留在既有 worker/slots 内，新增实际任务排空见证而不把 Busy 当锁释放。

**Tech Stack:** Go1.27.3、CGO_ENABLED=0、现有 Cobra、MyGo v0.7.1 `ui`、clientkey/install/subscription/configapply/nativeconfig；无新依赖、WebView、JS 客户端或新 keypolicy。

**Spec:** `docs/superpowers/specs/2026-10-29-client-key-entrypoints.md`，SHA256 `4bf561538e1ddc46a546c7d99652df5f4336cee5ec61e57706b919f9b0806d3a`；Gauss fix1 scoped closure Spec/Quality design ACCEPT，review SHA256 `ca83989e60446e046fb9b0afd95ee4957bec5ff3fb7fc8578fe59d46697f88be`。原拒签历史保留，不能只读报告开头误判状态。

## Global Constraints

- 文档身份更新为已精确提交/推读回 `3c1348a5e0af77d12c296f8b374619680c9d53a4`；生产事实基线仍 `b6a906f6910dc188faa2c88e665261c44015c5dc`。已只读确认 b6→3c 的 `internal/{cli,desktop,clientkey}`、两客户端 cmd 无变化，不能纳入其它 moving WIP。
- K core 已交付 `5637945e0e8a8921cf720dc0ea5288adaa2a8d81`；map SHA `a54a9543e920bce1da15627a0635eb2994cf4cad7e87760fdae86f2e426881fd`、core spec SHA `4f78964fb5b7e9200cef059d16b0d7db26b3165eec0630dbbefe47268fa7276d`、已签 core plan SHA `013c630eb42c61ec3d850363f1fdc0449dd1f3c639ee53e91b70836dbfe8ecd5` 不变。不改 core 四文件/算法/错误分类/锁序/marker。
- C1 **已REJECT、待原作者修复/重新完整签收**：历史candidate base `848104bde551781e8a914b6efa9b3420bf7ab903`，tree `feb621b2baf1a4fc5f2b1a6ddc3772df2e03651e`，正式 handoff `/tmp/cognate-native-C1-final.EGWj4rPg`。下文实际读取此 immutable tree，仅保留接缝出处，不把旧candidate当已接受/已入 shared。源码开工前必须等主提供 successor C1 ACCEPT、精确提交身份及 `root.go`/CLI helper 串交；先核修复差异更新接缝，不凭旧 HEAD 实施。C1源码finding原作者修复优先。
- 本 plan 独立 design ACCEPT、C1 接受/串交及主线程新源码授权三门齐备才可实施。fix1候选未来33路径（原32+唯一CLI Watch adapter）是授权请求，不是开放目录权限。A8 保持 freeze；不碰 K/C1作者在途源码、B Engine、server/state/proto/module 或其它 agent。
- 默认路径保持 `home/.config/skillhub/client.key`、`home/.local/share/skillhub`。home 由 CLI `localHome()`/Service.Home 明确提供；不加 XDG/custom-root/key flags，不从 RPC/插件取路径。只读非创建校验可先行，key admission 必须早于 DB/Session/manager/agent mkdir、安装下载或 native Authorize/Consume。
- 唯一生成入口是 `clientkey.Initialize(ctx, home) error`；`LoadExisting(ctx, home) ([]byte, error)` 错误必须返回 nil。只调用/分类 core，不复制随机生成、marker、安全文件或残留判定。key slice 的所有权与 clear 见 Task1/2；string/库内部副本不承诺彻底清零。
- Initialize/LoadExisting 不在已持安装 Session 时调用；返回时全部准入资源已释放。旧 subscription.Open→Close 持业务 Session 并执行 Manifest/Download 的合同保留；**不**借此豁免 C1 的网络/人工批准必须在安装锁外的独立合同。
- 普通浏览/认证/搜索/远端 Magpie preview/apply 不用 key；既有不存在状态就空列表的非创建分支保留。Linux 实跑；非 Linux 固定 unsupported、FS 不变；cross-build 不冒充 runtime。
- 无真实 HOME/账号/密钥/用户安装器，测试仅 TempDir+合成 HTTP/TLS/SQLite；不启 CGO1/race。新 Go 按完整 modern-go list（本轮已读取1.27全表），`errors.Is`/具名变量、`WaitGroup.Go`、`t.Context`、typed atomic；源码授权后再跑完整 list。

## Review Focus

1. 零订阅但存在 SQLite/WAL/native vault/marker 等残留：不能生新 key；Task1/2 的 `Residue` 用真实 FS/SQLite 证明。
2. 旧 install/backup/status 分支绕过 Pack：即使不消费加密 key，OpenSession 前也要准入；Task1 `DefaultRootMatrix` 逐入口，缺 key 的下载/DB/agent 副作用为零。
3. C1 已有三条 `config` 子命令不在旧 HEAD：Task1 `C1ExistingOnly` 核实 apply/status/restore 均迁移，绝不动 flow/Engine/授权协议。
4. Cancel 后 Busy=false 但真实旧 worker 未结束：Task2/3 `CanceledDrain` 必须阻止新本地任务准入，并实际 join，不能只校验旧结果未显示。
5. durable init 已完成、旧 generation 被取消/账号清空，或 main 在 RunE 前失败且 stderr 饱和：Task3/1 分别验证不回填、不删 key、有限退出及 caller OFD flags 不变。

## 1. 固定源码接缝与未来写集

### 1.1 已核实际 API（不是新公共工厂）

| 来源 | 现有事实 / 本片唯一接法 |
| --- | --- |
| K `internal/clientkey/key_linux.go` / `key_other.go` | `LoadExisting(context.Context,string)([]byte,error)`、`Initialize(context.Context,string)error`；四个 sentinel：`ErrInitializationRequired`、`ErrRecoveryRequired`、`ErrUnavailable`、`ErrUnsupported`。Linux错误可能同时带 ctx sentinel；取消优先分类。不解析 err.Error() |
| b6 CLI `pack.go` | `prepareClientHome(ctx)(home,directory string,err error)` 当前会 mkdir；`openSubscriptionManager(ctx)(*subscription.Manager,error)` 当前先 state.Open/count再 `loadClientKey(home,len(records)==0)`，必须根除 |
| C1 exact `native_configuration.go` | `newNativeConfigurationCommand` 注册 `config apply/restore/status`；三者共享 `localHome→loadClientKey(home,false)→prepareStateDirectory→nativeconfig.New`。不是假设不存在 configcommands |
| C1 exact I/O | `prepareNativeOutput(ctx,io.Writer)(io.WriteCloser,error)` 允许受控 bytes.Buffer 或私有重开 os.File；`nativeOutput.remaining` 当前64KiB。`prepareNativeFileOutput`/`nativeFile.operation` 每次 write≤5s、ctx取消唤醒并join；`nativeReopen` 不借 dup 改 caller OFD。`nativeInvocation`/`nativeExitDiagnostic` 当前只保护 config 子树 |
| b6 `desktop/model.go` | 实际接口名 **Backend**，实现 **Service**，不是新建 actualBackend/nativeBackend。`Model.job` 总限60s，`slots`容量1，`workers`、generation、life/cancel；`Cancel`立即Busy=false但不是排空证明 |
| b6 `desktop/watch.go` | `publish`/`cut` 串行发布取消；`watchLocksLocal() bool` 判 watch指针非nil，直到实际watch结束的回调才清；`StopWatch`保留stopping；`safeTaskError`当前不识别key分类 |
| 原生退出 / 组件 | `cmd/cognate-desktop/main.go` 以 `Window.Update` dispatch，OnQuit/OnClosed先 Close，App.Run返回后 Model.Wait。`view.go.accountView` 是账号页；现用 `ui.Column/Text/Button/PrimaryButton.Disabled`。headless真接口为 `ui.NewTester`、Click/Key/Frame/HasText/Focused |

### 1.2 候选 exact 写集：CLI 20 + MyGo 13，共33源码/测试路径

所有路径都须后续显式授权；没有此列表以外的“必要时顺手改”。若 C1 最终变化导致再多一个路径，先列证据申请。cmd/main现有调用可复用，不在写集。

| 片 / 路径 | 责任 |
| --- | --- |
| E1 新 `internal/cli/client_key.go` | 无bool代理、只准入helper、显式init、固定分类与有限输出/最终出口helper |
| E1 删除 `internal/cli/client_key_linux.go`、`internal/cli/client_key_other.go` | 删除旧生成/平台重复策略；共同代理直接让core决定unsupported |
| E1 改 `internal/cli/client_key_linux_test.go`；新 `internal/cli/client_key_test.go`、`internal/cli/client_key_entrypoints_linux_test.go`、`internal/cli/client_key_entrypoints_other_test.go` | 迁移安全fixture；新入口矩阵/真main/故障与非Linux真实runner用例 |
| E1 改 `internal/cli/{root.go,pack.go,install.go,public.go,backup.go,restore.go}` | 注册/最终出口及全部默认根准入，不重做低层安装 |
| E1 fix1唯一新增候选 `internal/cli/pack_watch.go` | 串行cycle→observer的core错误终止接缝及Watch返回后的取消优先级；不改subscription.Watch或新增一份调度器，需主另授此第33路径 |
| E1 串交后改 `internal/cli/{native_configuration.go,native_configuration_test.go}` | 消费 C1已接受的existing-only入口，迁移helper调用/错误分类及真实config回归；不改 native_configuration_io_* |
| E1 改 `internal/cli/{root_test.go,pack_test.go,pack_watch_test.go,pack_watch_output_linux_test.go}` | 成功本地fixture显式Initialize；最后一个原等待DB出现的真实full-pipe测试不能漏迁移或改成提前缺key失败 |
| E2 改 `internal/desktop/{service.go,model.go,view.go,watch.go}` | Backend/Service直接core、Model异步与排空、账号页按钮/错误映射、StartWatch互斥 |
| E2 改 `internal/desktop/{storage_linux.go,storage_other.go,storage_linux_test.go}` | 删除clientKey(create)、仅为其服务的privateKey/rand逻辑和旧stub；保留trustedDirectory/prepareHome安装目录用途 |
| E2 改 `internal/desktop/{integration_test.go,watch_integration_test.go,watch_service_test.go,service_other_test.go}` | 成功安装/Pack/watch显式初始化fixture、保既有真实HTTP/取消/锁释放与非Linux诚实错误 |
| E2 新 `internal/desktop/{client_key_entrypoints_test.go,client_key_entrypoints_linux_test.go}` | 真MyGo事件、生命周期和真实Service/core锁等待/故障组合；不使用新UI测试框架 |

E1 同一 owner 串行拥有 root/helper/C1迁移，不能与 C1作者并写。E2服务→Model→View也是同一owner串行；E1/E2仅在三门齐备且主分配不相交写集后可并行。二者只共享已签core合同；单片通过不代表整个entrypoints完成。用户文档如需在落地后更新，另列明确文件交主授权，不藏作第34路径。

## 2. 全部默认根准入 caller 表

这是源码实施检查清单；行内空状态例外只允许已存在的非创建检查，不新增“空订阅”例外。新found caller必须先补表/申请路径，不能只靠grep结果为零声称覆盖。

| 实际公开入口 / caller | 检查→准入→后续Open；本片落点 |
| --- | --- |
| CLI install-local / `install.go:newInstallLocalCommand` | ctx/平台/slug/只读输入能力→admitClientHome→现有state/canonical目录→OpenSession；不新增key到安装格式 |
| CLI install-public / `public.go` | ctx/平台/origin/id非网络校验→prepareClientHome准入→GetSkill/Download→OpenSession；缺key时不能先下载或mkdir |
| CLI install-status / `install.go` | 现有state或DB不存在可直接空列表；存在则只读验证→admitClientHome→OpenSession（包括--reconcile） |
| CLI uninstall-local / `pack.go` | slug/ctx/平台→prepareClientHome准入→OpenSession.UninstallLocal |
| CLI backup-local、backup-list / `backup.go` | 参数/路径只读检查；list既有不存在状态空返回可保留；所有会OpenSession的路径先admitClientHome；备份与Close失败语义保留 |
| CLI restore-local / `restore.go` | ctx/平台/输入、既有DB与parent只读验证→admitClientHome→原警告/Session.RestoreLocal；没有先mkdir目标再准入 |
| CLI pack subscribe/list/sync/remove / `pack.go` | flag/origin/凭据有界非创建校验→openSubscriptionManager内准入→准备目录→subscription.Open。list只有既有state根不存在的空返回保留；不读取订阅数授权生成 |
| CLI pack watch / `pack_watch.go` | 原选项/输出能力失败与启动前已取消的非创建返回保留；每轮fresh openSubscriptionManager重新准入、真实Close。四种core sentinel必须经§2.1的adapter返回root，不能被失败report/等待重试或Ctrl-C→nil吞掉；普通网络/业务失败保原退避 |
| C1 config apply/restore/status / exact native_configuration.go | 所有子命令都 `loadClientKey(ctx,home)`，且返回原core固定分类，不降成泛化nativeconfig.ErrInvalid；在state目录、nativeconfig.New及engine前。apply含token读取/网络/授权且未知只Status；restore仍preview-confirm，status仍真实读durable状态。现有flow实现不动 |
| MyGo Service.Install / `service.go` | ctx/平台/selected/origin只读检查→LoadExisting并clear（旧安装不需要key）→prepareHome/Download/OpenSession；不靠按钮是否点击过 |
| MyGo Service.Packs、WatchCycle / service.openPackManager | 非创建参数检查→LoadExisting→prepareHome→subscription.Open（其复制key后清入口副本）；去掉state.Open计数。watch_service.go继续只经该入口fresh open/Close，无idle manager |
| MyGo InitializeClientKey | 直接core.Initialize(service.Home)，没有prepareHome、origin/session、Pack、HTTP；不是业务预授权缓存 |
| CLI scan/search/browse/远端import；MyGo Login/Logout/Search/Details/Preview/Apply | 不开默认客户端状态，无key gate、不初始化。MyGo Apply是Magpie远端收录，不是C1本机apply；不能混淆命名。纯remote回归在无key合成home执行 |

显式raw-key/custom-root的install/subscription/configapply/nativeconfig低层API保持caller-managed；不加隐藏准入、不声称覆盖历史任意副本。C1 `internal/nativeconfig/flow.go` 的withEngine与锁外交互已从candidate核实，本计划不改。

### 2.1 fix1：Watch的最小错误传播接缝（ENTRYPOINTS-PLAN-01）

固定3c的subscription.Watch不在WatchReport中携error；它把cycle错误转失败report并重试，且父ctx取消时可在observer前返回。CLI adapter现又把context.Canceled吞为nil。仅改manager或root分类不能闭合入口。修复只在候选pack_watch.go内保留一次invocation局部 `admissionError error`：cycle与observer同步串行，不新增共享全局/锁/goroutine或导出hook。

- 每轮cycle开始先清该变量；openSubscriptionManager错误若clientKeyFailure（errors.Is四个core sentinel，包括包裹错误）才保存并返回；普通网络/业务/Sync错误不保存、不改其原退避。manager未取得不Close；取得后仍真Sync+Close，不能先返回遗漏释放。
- observer先检查本轮admissionError，非nil直接返回它，**不打印泛化重试report/不等待下一轮**；输出成功/普通失败仍走原输出/计数/退避。所有轮次fresh准入，没有首轮一次preflight或全局preload替代。
- subscription.Watch返回后、原有context.Canceled→nil分支**之前**再检查admissionError；有则作为终止错误返回，保errors.Is及core的ctx分类，覆盖父取消抢在observer前的情况。只有没有已观测core准入错误的普通前台取消才保原exit0；原启动前已取消仍不调用core。输出Close仍defer执行并保失败，不把core错误伪造为正常取消。
- root按Task1固定有限出口处理该terminal error；不在adapter打印原错误/token/路径，不改共享WatchReport/proto/订阅库、不把全部失败改成永久停止。原生同类report遗漏在已列watch.go内补**安全分类提示**，见Task2；此处不额外决定原生普通失败停止策略。

## Task 0: 身份、串交与可执行前置（所有source之前）

**Files:** 当前仅本plan；未来源码写集见§1.2。**Consumes:** 已签entry spec/core、C1最终ACCEPT身份；**Produces:** 主明确交付的源码base+33路径授权/两片owner。

- [ ] 收 Wegener 非作者design审与主裁定；核spec/review哈希及core563。收C1非作者完整ACCEPT+主精确commit，逐项对照candidate的source.blobs八项，特别root/helper/I/O与三个config子命令。不能把candidate作者测试当C1签收。
- [ ] 在固定base clean export只叠successor已接受C1；保存base/tree/33路径初始blob+mode，正常构建web/dist如fullGo需要。记录A8/K其它WIP不在export；不操作真实index或HOME。如最终身份只变化而接缝相同，仅更新证据身份；有API变化先暂停相应任务并申请窄修plan。
- [ ] Go1.27.3/CGO0跑完整modern-go list并读；重读ui-ux-pro-max/apple-design/emil-design-eng/animate相关规则。此处采用即时反馈、明确焦点、稳定布局和**不新增动画**；不采纳native-feel技能中与已签MyGo ui禁止Web冲突的WebView/Node架构。不引新UI/动画库。

## Task 1 / E1: CLI显式初始化及所有默认入口迁移

**Files:** §1.2 E1精确20候选路径，包含仍需主另授的pack_watch.go。**Consumes:** core两API+sentinel；successor签收C1 I/O helper。**Produces:** 以下package-private接口，供本包caller统一调用（不新增exported key服务）：

```go
func loadClientKey(ctx context.Context, home string) ([]byte, error)
func admitClientHome(ctx context.Context) (string, error)
func newClientKeyCommand() *cobra.Command
func clientKeyInvocation(arguments []string) bool
func clientKeyFailure(err error) bool
func clientKeyErrorMessage(err error) string
func prepareClientKeyOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error)
func executeClientKey(ctx context.Context, command *cobra.Command) int
```

`loadClientKey`只代理LoadExisting；`admitClientHome`解析localHome、LoadExisting、立即clear，成功返回home。`pack.go`保留原 `prepareClientHome(ctx)(string,string,error)` 外部接缝，内部先admit；另抽私有 `prepareAdmittedClientHome(ctx,home)(directory string,err error)` 仅复用原目录准备。openSubscriptionManager自己load一次，再调用后者；避免重复准入/拿到Session后再Load。此helper没有公开授权身份含义，逐caller表/负控约束其使用。

- [ ] **RED tests**：`TestClientKeyEntrypointsCLI`（NoImplicitCreate、ExplicitInit、IdempotentRestart、LegacyEnrollment、Residue、DefaultRootMatrix、RemoteNoKey、C1ExistingOnly）。真实init前普通动作拒绝，db/canonical/agent/下载/Authorize/Consume计数为零；只允许core自身目录/锁。Residue用真实空订阅DB、WAL-only、vault/backup、marker-only、未知文件、旧安装DB；删key+state仍有marker拒绝。坏权限/链接/截断保原文件；不靠len==0。
- [ ] **记录真实RED**：先跑命中上述case；缺新命令的首个compile/unknown-command失败与“旧自动生成/先DB”的行为RED分开保存。不能因漏init使原成功业务测试变成提前缺key的假绿。
- [ ] **最小实现**：新命令只有 `client-key init`，无位置参数；timeout默认30s、>0且≤2m，非法args/flags/help在core前处理；无stdin/TTY/server/token/网络或force/rotate/repair。以command.Context派生期限，调用core.Initialize；仅成功输出“本机客户端密钥已就绪（未初始化服务端会话）”。重复init不声称刚生成。
- [ ] **出口落实**：root.New只注册子树；root.Execute在C1 config分支之外、RunE前识别client-key子树并走executeClientKey，覆盖unknown子命令/flags/args/help。prepareClientKeyOutput复用C1已签prepareNativeOutput/私有reopen，收紧remaining为累计4KiB（stdout+stderr共用剩余额度），每次等待≤5s；不动C1原64KiB契约。业务没有输出前的失败可写一次固定分类；输出已失败就放弃再次诊断并非0，不串两次饱和写延长收尾。不能退到root普通Fprintln、任意writer goroutine、dup更改OFD或读取stdin。Close/释放失败非0且不宣布成功；main仍用现有signal ctx，必须真正Wait回收子进程。
- [ ] **分类与迁移**：优先ctx取消/期限→“操作未确认完成；写入可能已经发生，请核实后显式重试”，InitializationRequired→“请先显式初始化本机客户端密钥”，RecoveryRequired→“请恢复既有本机客户端密钥，勿重新生成”，Unavailable→固定安全不可用提示，Unsupported→固定Linux限制；不含路径/key/长度/fingerprint/marker/error文本。clientKeyFailure仅用errors.Is识别四个core sentinel；root普通Pack/local命令遇这些错误也走一次有限固定分类诊断、exit1，不能原样打印英文core错误或包裹的业务文字，非key错误不在本片泛化改造。C1 nativeErrorMessage增加同一分类转发，保留原unknown/conflict/平台语义。删除两个旧平台loader及全部bool/随机生成分支；按§2改每条业务准入。
- [ ] **key所有权**：旧安装只admit后clear；openSubscriptionManager用defer clear覆盖所有失败，Open复制后即返回时清理；C1 nativeconfig.New复制key后立即clear入口副本，同时defer兜底，最终client.Close继续清其owned副本。零值/错误key不传业务；不存Cobra flags/Model/日志，不声称不可变session字符串已可靠清零。
- [ ] **GREEN / 真实路径**：同一合成home运行真main init（闭合stdin、网络计数0）→Pack subscribe/list/sync/remove/watch→local install/status/backup/list/uninstall/restore；重启init后同key，真实Store仍解密凭据。保原业务断言、8s/3s等已签watch预算；pack_watch_output_linux_test先显式init再等DB/满pipe，不能放宽时间或改断言。C1三子命令真实无key拒绝且无网络授权、initialized场景保原preview/unknown/restore语义。
- [ ] **Watch真实错误出口**：在现有候选CLI测试文件加 `TestClientKeyEntrypointsWatchAdmission`，Linux上New().ExecuteContext与真main两层覆盖Pristine（InitializationRequired）、Residue/LateKeyLoss（RecoveryRequired）、真实core故障overlay（Unavailable，含包裹/ctx错误）。可写输出必须固定提示、返回相同core类别、main非0且不待重试/Ctrl-C；key/原文件不变，只有core允许元数据，无DB/agent/下载。饱和/关闭stderr仍≤5s收尾非0；恢复吞sentinel的adapter负控要由精确返回/退出断言失败，不靠全局timeout。非Linux真实runner以New+memory/cooperative输出到达core.Unsupported；真main保留更早的prepareWatchFileOutput平台拒绝并验证FS不变，不伪称它到达了core（!linux文件输出原本不支持）。
- [ ] **Watch后续轮/取消顺序**：LateKeyLoss必须先真实成功一轮、manager已Close，再在observer释放前删除合成key后放行下一轮。New.Command优先用现有watchContextWriter.WriteContext在首轮成功report设ctx可取消握手；按真实顺序cycle已Sync+Close，独立OpenSession取得锁作释放正控后丢key/放行。真main则用真实IPC及仅/tmp、同pack_watch.go测试overlay在该observer阶段建立等价握手（main不改、无shipping/global hook、无新source路径），不靠pipe调度猜测。沿原合法1s interval/attempt选项和8s/3s/1s测试预算，不改产品默认。新增 `LateCancelAfterAdmission` 用同范围测试overlay在core错误已被保存、observer前取消父ctx，仍须返回该core错误而非nil。`OrdinaryFailureThenRecovery`/`InitializedCancel`保普通网络退避→成功/正常Ctrl-C exit0；原已取消前置不建状态。吞错误mutant出现泛化失败report时fixture可依明确事件发SIGINT收尾，随后精确exit0断言RED，不以挂死充数。
- [ ] **真main failure tests**：`TestClientKeyEntrypointsMainOutput`（FullStderr、ClosedStderr、Writable、PreRunErrors、ClosedStdin、InvalidTimeout、SignalWhileLocked、OutputFailureAfterDurable）。真二进制及helper关闭FD；饱和/关闭stderr≤5s收尾非0并Wait，记录caller OFD flags/offset前后，写正控必有固定消息。CLI init锁取消真实drain，未确认完成不删durable key。再跑E1全局部回归/vet后精确freeze；等待独立验收，不自行提交。

## Task 2 / E2a: 原生服务与实际任务排空合同

**Files:** §1.2 E2中除view.go外12路径；view接Task3，同一owner，阶段freeze不作模块完整签收。**Consumes:** core；原Backend/Model worker；**Produces:** Backend新增 `InitializeClientKey(context.Context) error`，Service同名方法，Model新增 `InitializeClientKey()`。

- [ ] **RED tests**：`TestClientKeyEntrypointsService` 覆盖 OfflineInit、InvalidOriginInit、NoImplicitCreate、DefaultRootInstall/Packs/Watch、Residue、RemoteNoKey、LockedCancel、CloseFailure。真Service(core)，自定义RoundTripper遇调用立即失败/计数；Init不接服务端，未初始化本地Install/Packs/Watch的下载/DB/agent均0，初始化后真实fixtures成功。以真实install.OpenSession子进程持锁证实init等同一物理锁，取消后真实join/再取锁，不用仅mock顺序。
- [ ] **最小服务实现**：Service.InitializeClientKey仅 `clientkey.Initialize(ctx,service.Home)`。Service.Install先校验ctx/平台/selected/origin再LoadExisting+clear然后prepareHome；openPackManager先LoadExisting再prepareHome/subscription.Open，删state.Open/count及clientKey。保fresh WatchCycle/真实manager.Close，不改subscription库。storage_linux/other移除旧key实现，不删除安装用途的trustedDirectory/prepareHome。
- [ ] **任务接口**：Model中新增 `pendingJobs atomic.Int32`、`localWorkBlocked() bool`、`jobWithTimeout(label string, limit time.Duration, work func(context.Context)(func(),error))`。原job是60s调用代理；init用30s（含slot准入），只捕获Backend调用，不捕获密码/session/share token。Backend/Service内均不依赖UI是否显示过成功。
- [ ] **实际drain**：pendingJobs在提交worker前递增，真实work返回并释放slot/资源后递减（不等待UI回调）；覆盖取消的slot候补。localWorkBlocked判pendingJobs>0或watchLocksLocal；InitializeClientKey/Install/Packs/StartWatch的入口与控件采用此闸，防Busy取消后抢排第二个本地写。Busy仍是展示而非授权。取消无普通结果发布时可经publish(model.life,空状态变更回调)仅唤醒重绘，禁止旧generation owner/status内容；Model关闭不再派发。保原generation/life/cut/publish与Close/Wait语义，不增加阻塞UI的Wait或废弃goroutine。
- [ ] **安全错误**：watch.go.safeTaskError委托/复用本包core固定分类规则（不跨依赖CLI）；ctx优先，不打印raw error。Model.StartWatch的cycle额外保存本轮core错误的**固定提示string**，observer把该安全快照显示为初始化/恢复/不可用/Linux限制并提示“请显式停止后台同步并等待排空”；不能只改safeTaskError却仍在report分支显示泛化重试。保持现有原生failure/recovery/退避与停止控件，不擅自令所有普通错误终止；无raw error进UI回调，旧generation仍被拒绝。新 `TestClientKeyEntrypointsNativeWatchKeyHint` 用真Service/core+ui.NewTester观察该report与停止→drain→初始化按钮，不以直接WatchCycle错误代替。Linux初始化完成只显示就绪与非登录说明；非Linux服务直接core.ErrUnsupported，UI明确不可用，不以stub成功替代。
- [ ] **GREEN**：成功fixture显式Initialize（只在确实需要本地状态的测试setup）；integration/Watch fixtures和watch_service_test迁移，remote-only负控仍用无keyhome。storage安全链接/权限测试改由core调用，不删原用途的prepareHome安全断言。现有blockedBackend/blockedLogin/watchBackend使用嵌入Backend，不新增平行backend体系；新增测试fake明确实现新增method，生产始终真Service。

## Task 3 / E2b: 真MyGo按钮、取消/身份变化与故障

**Files:** 同E2 13路径，重点view.go与两个新entrypoints测试；不改cmd。**Consumes:** Task2精确Backend/Model接口；**Produces:** 完整可点本机设置而非CLI代用品。

- [ ] **RED真事件**：`TestClientKeyEntrypointsNativeEvents` 用ui.NewTester(model.View,640,600)，Page=账号；有无origin、错误origin、未登录与离线都能点“初始化本机客户端密钥”。初render/切页/login/logout/Pack操作绝不隐式init。mouse Click与Key(Tab/Enter)都要实际命中按钮、检查Focused与结果；不能只调用Model方法算事件验收。
- [ ] **实现view**：保账号登录表单，增ui.Column本机设置区+Text说明“本机加密材料，与服务端登录无关”+原生成熟Button。key为稳定 `client-key-init`；按钮仅明确事件调用Model.InitializeClientKey，不调ready。Disabled条件包含非Linux、Busy/localWorkBlocked；活动/停止中watch显示“请先停止后台同步并等待排空”，沿用现有停止/取消入口，不自动重启watch。点击立即显示“正在初始化…”；固定状态区可见、稳定布局，不toast-only、不加动效/新组件/secret框。
- [ ] **异步/互斥实证**：`TestClientKeyEntrypointsNativeCanceledDrain` 真实core锁等待时UI仍能焦点/取消；另用明确可阻塞返回的Backend witness证明取消到实际return前pendingJobs仍1、按钮禁用、重复点击/直接Model本地入口不排新任务。停watch→stopping禁用→真实worker结束→init→手动安装/同步；复用同slots，不在event线程等barrier。
- [ ] **身份/关闭实证**：`TestClientKeyEntrypointsNativeGeneration` 以barrier精确制造core已durable、Backend未返回，再分别Cancel/换origin/改账号/logout/Close；账号密码/session/share字段立即清，不被旧成功回填，key/marker不删。worker结束后无旧owner/status回调生效，无UI线程Wait；App退出后沿既有Model.Wait真实join。允许显式再次init核实同一key，不称rollback或key已授权下次操作。
- [ ] **外观/secret/平台**：light/dark×narrow/normal真layout，字/按钮可见、Tab焦点可达，初始化说明不被登录校验遮挡。输出/标准log+slog串行捕获并cleanup，合成key/session/private路径不得出现；只保存布尔/类别与无敏感截图。`TestClientKeyEntrypointsUnsupported`在Darwin/Windows各自runner实跑服务拒绝/FS不变与按钮disabled原因；本机Linux不能替它签。
- [ ] **故障组合**：借用已签core故障overlay（仅/tmp，不改core生产）把fsync/release失败接到真实CLI/Service，断言nil loaded key、init不宣成功、durable文件原样、不重生。入口无法拿到已签可复用故障seam时先申请最小测试路径，不引新的公开key工厂。实际Go test结束前drain所有worker/子进程/FD。

## Task 4: 编译后负控、精确导出与完整门禁

**Files:** 无新生产路径；日志/manifest/报告仅ignored release或/tmp，源码授权时同时确认这些证据路径。**Consumes:** E1/E2最终frozen trees；**Produces:** 精确33候选路径增删manifest、模块级非作者验收包。

- [ ] 各片先RED→GREEN；再用/tmp overlay每个先 `go test -overlay=... -run '^$'` 编译成功，然后命中确定测试得到行为断言RED：①普通动作调用Initialize/零订阅生成→CLI/Service Residue失败；②准入放prepare/DB后→DefaultRootMatrix副作用失败；③跳过core/吞Load错误→已用状态缺key仍尝试业务失败；④取消时提前清pendingJobs或释放slot→NativeCanceledDrain失败；⑤移除generation gate→NativeGeneration旧状态回填失败；⑥client-key root退回普通Fprintln→满stderr真main≤5s收尾断言失败。不得以整体test timeout、编译失败、panic或未命中测试充数。
- [ ] fix1补第⑦项：恢复CLI Watch吞core sentinel/父取消先转nil两种独立adapter变体，分别先compile0，再由WatchAdmission pristine/late-key-loss和LateCancelAfterAdmission精确类别/退出断言RED；恢复原生泛化hint变体由NativeWatchKeyHint真实事件断言RED。普通业务退避/成功/取消正控必须仍GREEN，不修改subscription.Watch。
- [ ] 分别保存每项源码diff/SHA/selector/command/exit/诊断（无raw secret），恢复exact source后反例GREEN；真实core故障负控与生命周期fake witness分别标明，不混称所有都真实磁盘故障。普通源码没有测试专用后门。
- [ ] 从主提供的新固定base只叠E1/E2精确source，diff-tree核明确获授的33候选路径/模式/增删，正常npm构建dist、全Go测试/vet/module及successor C1/core/install/watch回归。CI/运行门禁失败保留原RED，不靠retry/加timeout掩盖；无相关失败不修其它WIP。
- [ ] 交base/tree、逐文件blob/mode/SHA256、干净重构命令、实际exit/运行/skip统计、正负控和≤1页作者报告；停止写集，交Wegener/主安排非作者完整Spec+Quality。主精确提交另授；entrypoints双片齐备也不宣称C1/MyGo配置apply、平台安装、完整14-agent/F12或Release完成。

### 未来验证命令（当前未执行，均在隔离export）

先由fixture设置TempDir HOME/XDG/TMP、合成凭据与loopback fixtures；main子进程Env只继承工具链必要项+这些合成路径，不能直接用操作者HOME。以下Go selector须实际命中预定case，非Linuxrunner不得因Linuxbuild tags只得到no-tests假绿。

```sh
export CGO_ENABLED=0 GOTOOLCHAIN=local
go version                                      # 必须Go1.27.3
go env CGO_ENABLED GOTOOLCHAIN
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go test -mod=readonly ./internal/cli ./internal/desktop -run '^TestClientKeyEntrypoints' -count=1 -v
go test -mod=readonly ./internal/cli ./internal/desktop -run '^TestClientKeyEntrypoints' -shuffle=on -count=3 -json
go test -mod=readonly ./internal/clientkey ./internal/cli ./internal/desktop ./internal/install ./internal/subscription ./internal/nativeconfig ./internal/configclient -count=1
go vet -mod=readonly ./internal/clientkey ./internal/cli ./internal/desktop
go mod verify
(cd web && npm ci && npm run build)               # 仅隔离导出，非共享web/dist
go test -mod=readonly ./... -count=1
go vet -mod=readonly ./...
EVIDENCE_DIR="$(mktemp -d /tmp/cognate-key-entrypoints.XXXXXXXX)"
go build -mod=readonly -o "$EVIDENCE_DIR/cognate" ./cmd/cognate
go build -mod=readonly -o "$EVIDENCE_DIR/cognate-desktop" ./cmd/cognate-desktop
GOOS=darwin GOARCH=arm64 go build -mod=readonly -o "$EVIDENCE_DIR/cognate-desktop-darwin-arm64" ./cmd/cognate-desktop
GOOS=windows GOARCH=amd64 go build -mod=readonly -o "$EVIDENCE_DIR/cognate-desktop-windows-amd64.exe" ./cmd/cognate-desktop
env -u DISPLAY -u WAYLAND_DISPLAY go test -mod=readonly ./internal/desktop ./cmd/cognate-desktop -count=1
HOME="$(mktemp -d /tmp/cognate-key-smoke.XXXXXXXX)" xvfb-run -a "$EVIDENCE_DIR/cognate-desktop" -smoke=2s -capture="$EVIDENCE_DIR/native.png"
```

原有实窗机制只证明真实MyGo窗口/捕获/退出、不自动点初始化；按钮验收由真实headless事件组单独证明，有display条件再人工原生按钮操作并记录。缺GTK/Xvfb/display如实pending，不能用Web模式补过。Darwin/Windows各自runner跑 `go test -mod=readonly ./internal/cli ./internal/desktop -run '^TestClientKeyEntrypointsUnsupported$' -count=1 -v`，另保原unsupported安装测试；cross-build结果独立标compile-only。不在本片改workflow/自动打包配置。

## 自审覆盖 / 交接

| spec | plan覆盖 |
| --- | --- |
| §§1–2 唯一core、最早准入、key清理与锁边界 | Global、§1/2 caller表、Task1/2；保留旧manager网络锁生命周期，不把core准入/Load包装成业务授权 |
| §3 CLI显式init/最终有限出口 | Task1真main/I/O复用/前置错误与失败不回滚；删除旧bool代码而非留兼容壳 |
| §4 MyGo异步/实际drain/账号事件 | Task2 pendingJobs与slots、Task3原生按钮/布局/五类取消及真Service；非CLI替代 |
| §§5–6 所有调用与C1串交 | 33候选exact路径、§2每个Open及§2.1 Watch错误出口、C1exact三config分支；Task0严格待successor接受/源码另授 |
| §§7–8 真路径/故障/独立负控/平台命令 | Task1–4、七类compile0行为RED及恢复GREEN（含Watch两个终止变体/原生hint）、实际预算/平台pending区分 |

类型/方法/文件名均对照固定源码；`actualBackend`仅指真实Backend/Service边界，没有这个新类型。未决定新的key policy/锁/schema/协议；没有未定义公共wrapper或伪装已运行的PASS。原freeze `c2746861…` / review `8804127f…` 保留；本次仅ENTRYPOINTS-PLAN-01的Watch传播/测试/唯一候选扩集和C1拒签前置更新。计划已作者自审，**fix1待Wegener scoped独立design复审；所有实施checkbox未执行，当前绝无Go授权。**
