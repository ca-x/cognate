# Client Key Entrypoints

## 1. Assumptions、目标与依赖

2026-10-29；本规格定义 capability map 的 `client-key-entrypoints` 模块，覆盖 CLI 和 MyGo 原生入口，不是 Go grant 或实施计划。只授权本文档；具体源码路径、分批交接和测试须在独立设计验收后的 plan 中另授。

固定依据为 docs commit `d40503e2dde3c0874a3e417904c3cc302fbc78c4`：

- `2026-10-29-client-key-capability-map.md`，SHA256 `a54a9543e920bce1da15627a0635eb2994cf4cad7e87760fdae86f2e426881fd`。
- `2026-10-29-client-key-core.md`，SHA256 `4f78964fb5b7e9200cef059d16b0d7db26b3165eec0630dbbefe47268fa7276d`。
- core 正在独立实现；只能依赖上述 API/合同及未来独立签收的精确 source identity，不能以 moving Go 补写或更改本规格。

假设与明确决定：

1. 默认受管 home 仍使用历史 `.config/skillhub/client.key` 和 `.local/share/skillhub` 路径，不因 Cognate 改名迁移路径。home 为明确、干净、绝对的可信目录；产品从原有 home 入口解析一次，不新增 RPC path、自定义 key/root flags 或 XDG 回退策略。
2. 全新用户必须主动执行 `cognate client-key init`，或点击 MyGo 账号页“本机设置”里的初始化按钮。命令/点击本身就是本地初始化意图；无需登录、服务地址、网络、token、TTY 中的秘密或远端许可。普通产品动作不能替用户初始化。
3. 产品默认根下任何可能打开 Store/Session/manager 的本地操作均先准入，包含原本不使用加密 key、但会建立同一 DB 的旧安装入口。这不是删除旧命令：初始化后其安装/备份/卸载/恢复功能及数据格式仍保留。否则先运行旧安装会产生 residue，使稍后首次初始化被正确拒绝而把新用户困住。
4. 不打开状态的纯搜索、浏览、认证、Magpie 远端预览不要求本机 key；已有“状态不存在则直接空列表”的非创建检查可以保留。不能为了全局 preload key 而阻断这些能力或在启动/渲染时制造 marker。
5. core 的 Linux 实现与非 Linux unsupported 合同不变；桌面三平台可构建不等于三平台初始化/安装可运行。C1 仍 existing-key-only，不由本文提前改动。

成功标准：零订阅不再是生成权限；key 准入失败时没有产品 DB/Session/manager/agent 目录副作用；CLI 与原生初始化真正共用 core，所有 key 消费入口均有可追溯覆盖，没有第二份 keypolicy。

## 2. 自包含接口与顺序

唯一密钥策略 API 来自共享 core：

```go
clientkey.LoadExisting(ctx context.Context, home string) ([]byte, error)
clientkey.Initialize(ctx context.Context, home string) error
```

LoadExisting 返回调用方拥有的新 32B slice；任何错误返回 nil。Initialize 不返回 key。初始化需要的目录/锁/marker、安全文件验证、残留判定、durability、释放失败和错误分类一律由 core 实现；入口不得复制它们，也不自行解析 marker、计算 subscription 数量、生成随机 key 或修复权限。

CLI 命令构造候选为 `newClientKeyCommand() *cobra.Command`。现有 `loadClientKey(home, allowCreate)` 必须删除或最终替换成无 bool、带 context 的薄代理 `loadClientKey(ctx, home)`，只调用 LoadExisting；不保留“暂时没人传 true”的生成分支。原生删除 `clientKey(home, create)`；`Service` 直接调用共享 core，并为原生 Backend 增加：

```go
InitializeClientKey(ctx context.Context) error
```

`(*Service).InitializeClientKey` 只以 `service.Home` 调用 Initialize，不接受 origin/session/root/key，不调用登录、HTTP、prepareHome 或 Pack manager。对应 `(*Model).InitializeClientKey()` 只发起异步 worker 并发布安全结果，返回值/闭包不携带 key。当前实际服务边界是 `desktop.Backend`/`Service`；后续若被称为 nativeBackend，含义相同，不另造平行服务。

普通本地动作顺序固定：

1. 检查 ctx、平台、非创建的 args/flags/slug/origin/输入能力；允许安全的只读路径检查，不可先 mkdir agent 或打开 DB。
2. 解析明确 home → LoadExisting → 成功且 core 所有 release 已完成；失败立即停止，释放临时材料，零网络授权/秘密消费。
3. 此后才执行原有目录准备、Store/Session/subscription/configapply Open 与业务。key 不再被 subscription 数量或 DB 是否存在授权。不存在“先开 Session 再 LoadExisting”的例外。
4. Initialize/LoadExisting 自身的 core/安装准入双锁内不得执行业务网络或人类确认；所有 release 必须在后续 Store/Session/manager Open、业务网络或确认前完成，不在已持安装 Session 时调用这两个 API。传给需要 key 的 manager/engine 后按其 ownership 合同清理入口副本；不需要 key 的旧安装入口在准入后立即 clear。失败/取消同样清理，不把 key 存入 Model、任务状态、日志或账号字段。
5. 原业务 Close/drain 失败仍必须反馈；不因已加载 key 或 init 完成而将后续未知状态误报成功。LoadExisting 不是证明任意旧 ciphertext 可解密，后续各域仍执行真实认证/CAS。

上述锁外要求只约束 key 准入本身。准入完成后，既有订阅 manager 从 Open 到 Close 持有业务安装 Session，并在其中执行 Manifest/Download 的生命周期及其有界/取消合同保留；本片不重构该锁架构，也不声称所有业务 I/O 已锁外。此保留不豁免另签的 C1 新 native config 合同：其网络与人类确认仍须在不持安装锁时执行。

## 3. CLI 用户合同与最终错误出口

```sh
cognate client-key init
cognate client-key init --timeout 30s
cognate client-key init --help
```

仅 `init`，无位置参数；`--timeout` 默认30s、必须>0且≤2m。无 `--force`、rotate、repair、delete、raw-key、custom-root、server/token/password/自动 yes 入口。缺/错参数先失败、help 不调用 core。命令不读取 stdin、控制 TTY 或网络；输入可以为闭合 pipe，不能等待 EOF。ctx 贯穿 core 锁等待，Ctrl-C/超时取消并真实 drain 后返回。

成功 exit0，只显示“本机客户端密钥已就绪（未初始化服务端会话）”，不显示 key、fingerprint、长度、绝对路径、marker 内容，且不声称这次一定新建了 key。重复 init 保持相同 key；已有 valid key 的 legacy enrollment 也走共享 core，而非直接成功。无 key 的普通动作 exit1，安全提示“请先显式初始化本机客户端密钥”；已有残留/坏文件则提示恢复既有 key，不建议重新生成。按 core 已冻结的结构化分类映射，不能匹配原始 error 文本猜类别；若 core 签收 API 未暴露所需类别，先申请合同澄清，不自行增 API。

释放/同步失败或取消可能已经留下 durable key/marker，固定提示操作未确认完成、需检查/显式重试，不伪造回滚。正常可写输出给固定分类；无法安全输出可丢诊断但仍有界非0退出。输出累计≤4KiB、每次等待≤5s；禁止借 dup 改 caller OFD/flags/deadline、无界 stderr Fprintln 或遗弃 goroutine。未知子命令/flags/args 的 RunE 前错误同样覆盖，不能只测 ExecuteContext。

此处 bounded 退出/输出是 entrypoints 的验收需求，不是新通用 I/O 框架：C1 I/O seam 尚须源码实现及独立验收。它是未来接入前置，不能消费移动实现或自行扩其 root 子树权限。无法复用时先申请精确窄 seam，不交付无限等待，也不降低显式 CLI init 能力。

## 4. MyGo 本机设置与生命周期

在现有“账号”页保留登录表单，另设明确的“本机设置”区：说明客户端 key 是本机加密材料、与服务端登录无关；按钮“初始化本机客户端密钥”。服务地址空/无效、未登录、离线都可点击。Linux 按钮只有明确用户事件能触发 Initialize；View/构造 Model、进入页面、login/logout、换 origin、Pack install/sync、watch 启动均不得自动触发。平台 unsupported 时显示原因并禁用动作，服务方法自身仍须安全拒绝绕过 UI 的调用。

复用 MyGo 成熟的 Column/Text/Button/禁用与状态控件、原有键盘焦点与 light/dark/narrow 布局；不加 WebView、通用 JSON 编辑器、key 文本框或动画库。采用 ui-ux-pro-max 的异步可见反馈/禁重复提交、Emil 的稳定布局与 Apple 的即时反馈原则：点击后稳定显示“正在初始化…”，布局不跳动、按钮不可重复提交，提供原有取消入口。简单初始化无新增动画需求；不得用动画/延迟掩盖阻塞，也不把状态反馈只放在瞬时 toast。

沿用现有 worker/slots、generation、life context、cancel/drain 和 UI dispatch：

- worker 执行文件操作；UI/event线程不得等待锁、worker 或依赖UI线程的 callback。初始化与本机手动写/WatchCycle 不并行准入；watch 活跃/排空时按钮明确禁用，请先显式停止。没有自动重启 watch。
- 任务使用30s上限，捕获明确 home，不捕获账号密码或token。取消后旧 worker未排空前不可启动替代本地写；Busy取消只是界面状态，不代表底层锁已释放，slots及实际 drain 才是依据。
- 换 origin/账号、logout、Cancel、Close 取消任务并使旧 generation UI 回填无效；不能阻塞账号敏感字段清理或关闭窗口。window/event loop释放后由既有退出路径 Wait/join，不在 View 或同步发布回调中 Wait。跨页不触发新初始化。
- 取消/Close 可以发生在 durable写入后：不删 key/marker，不显示“已经撤销”。不回填旧账号 session/成功状态；显式再次 init 可通过 core 核实并重试同一 key。UI 的“完成”不是下一次业务授权，后续动作必须再 LoadExisting。
- 不记录敏感 form snapshot/HAR；独立 headless/event 验收只记录布尔断言、安全固定状态和资源计数。Core unsupported 不应被通用“请初始化”提示误分类。

## 5. 覆盖与迁移候选（非源码授权）

| 当前入口/候选路径 | 必须迁移的行为 |
| --- | --- |
| `internal/cli/pack.go` | 删除 openSubscriptionManager 的临时 state.Open→Subscriptions→len(records)==0 检查；LoadExisting 必须早于 prepareClientHome 的目录创建和 subscription.Open。prepareClientHome 的所有 Session caller 需纳入准入，不能只修 Pack sync |
| `internal/cli/client_key_linux.go`、`client_key_other.go` | 删除随机生成/allowCreate 与重复文件安全策略；可删除实现或保留上述无 bool 薄代理，other 也走同一 core unsupported 分类，不留旧后门 |
| 新 `internal/cli/client_key.go`、新/迁移 CLI tests、`root.go` | 显式 client-key/init 注册与有限退出；CLI helper 及 root 必须与 C1 串行交接，见§6 |
| `internal/cli/install.go`、`public.go`、`backup.go`、`restore.go`、`pack.go` | 保留 install-local/install-public/install-status/uninstall-local/backup-local/backup-list/restore-local 能力；所有默认根 Session Open 前先 key admission，包含未走 prepareClientHome 的分支。public 安装在下载/目录创建前准入；不新增 key 到旧业务格式 |
| `internal/cli/pack_watch*.go` 及 Pack tests | subscribe/list/sync/remove/watch 共用新顺序；每轮 fresh manager 重新准入；无状态空列表/非法选项/已取消保持非创建返回，不因迁移强行创建 DB |
| `internal/desktop/service.go`、`watch_service.go` | openPackManager 删除先开DB计数/自动生成；Install 在 prepareHome/download/OpenSession 前准入；WatchCycle 经同一新 manager 入口。服务不依赖 UI 是否已初始化 |
| `internal/desktop/storage_linux.go`、`storage_other.go` 及 storage tests | 删除 clientKey(create) 和仅供它使用的 privateKey/crypto/rand 逻辑；保留安装目录 trustedDirectory/prepareHome 的现有用途，不能借机重构全部平台文件 |
| `internal/desktop/model.go`、`view.go`、service/lifecycle/view/integration/watch tests | 扩展 Backend、Service、Model与账号页本机设置；全部真实/伪 Backend 测试契约同步；不能仅新增按钮而不接共享 core |
| 已冻结或后续 C1 `native_configuration.go` / tests | 在单独批准的串行 migration 中将 existing-key-only load 改接 core；本 spec 不授 C1 扩写。不动 B Engine/Select/journal/renderer；raw-key lower API仍由 caller 管理 |

后续 plan 必须逐 grep caller 建表，冻结 exact base/blob/写集，不把上表当开放目录权限。迁移 fixture 必须用合成 home 显式 Initialize 或已有 legacy key fixture；旧 helper 的“true 成功生 key”用例改为新 init 真实行为，不简单删除安全回归。链接、模式、截断、replacement、取消与 Close失败等核心测试归 core，入口保留实际连接证明而不复制一套策略。

## 6. 交付依赖与串行交接

先独立接受 core 精确四路径，再接受本模块设计与实施 plan。可以分为“CLI准入+显式init”“MyGo服务+事件生命周期”两个可验片，但模块完整签收必须两者均通过；不能为避冲突省略原生初始化。旧写入入口迁移必须在各片签收范围明列，不由按钮覆盖推断。

C1 已按独立计划授八路径、root 仅 config 子树 bounded 固定出口；其 existing-key-only 行为保持，不抢改。entrypoints 的 root 注册、client-key 子树错误路由和 CLI helper 迁移等待 C1 作者冻结、非作者验收及主精确串行授权。若 entrypoints 先获实现机会，主必须先重新裁定 C1 dependency/base 及 helper caller 合同；任一作者不得并发写 root 或以旧 bool API 的兼容壳暗保生成能力。C1 I/O helper 的复用须依据最终签收 identity，不能为本文提前扩 C1 grant。

不修改 core 源码、install/state 的锁/schema、RPC/proto/module/server 或 B 公共 API；不新增通用 key 服务、遥测、远端 registry、rotation、导入任意 key、自定义根初始化或自动恢复。低层显式 raw-key/custom-root OpenSession/manager/Engine 调用方继续自管，不声称覆盖任意磁盘副本/历史；所有受支持的默认产品根必须覆盖。丢 key、未知残留和升级旧二进制并行问题沿 core 恢复合同，禁止以“零订阅”豁免。

## 7. 真行为验收与负控

所有用例仅 TempDir 合成 home/credentials、loopback fixture；不读取真实 HOME、真实配置或运行真实用户安装器。以下是未来要求，当前无运行证据。

1. **CLI显式首次使用：** 真二进制普通 Pack/local命令无 key 拒绝，key/DB/agent目录均未产生（core允许的锁/目录除外）；`client-key init` 无 origin/token/网络、stdin闭合也完成，只留下 core允许文件。再 Pack订阅/同步、旧本地安装/状态/备份/卸载/恢复实际成功；重启 init 不改 key，真实 Store验证订阅凭据仍可解密。
2. **零订阅不得重建：** 真实SQLite空订阅DB、WAL-only、native vault/备份、marker-only、未知文件、无key但旧安装DB分别覆盖。CLI普通入口与MyGo服务失败，明确恢复分类且 key/原文不变；初始化后删key及state保marker仍拒绝。坏key/链接/unsafe权限拒绝不修复。legacy valid key+已有数据可 enroll，后续业务仍验证真实域认证。
3. **最早准入：** 按§5逐公开入口真实触发，断言无 key/坏 key时无 DB Open/schema、Session、manager、agent mkdir及安装下载/Authorize/Consume。用实际FS树/SQLite标识/HTTP计数验证，不仅mock调用顺序。先准备状态再load、忽略Load错误、恢复len==0生成的编译成功变体必须行为RED。
4. **MyGo显式事件：** 真Service+core+ui.NewTester，服务地址空/无效、登出、网络RoundTripper禁止调用，点击账号页按钮异步初始化后普通安装/Pack实际可用；只 render/切页/login/logout 不生 key。light/dark/narrow、键盘与鼠标事件、焦点/状态可见性和重复点击单任务均验；不以直接调用Model方法代替按钮验收。
5. **实际锁与生命周期：** 子进程持真实安装锁时初始化worker等待，UI线程可交互；取消/超时/换账号/origin/Close后真实锁等待终止、join/FD/worker无泄漏；取消后尚未drain不能抢开第二本地写。明确barrier制造durable完成后旧generation回填，断言账号/敏感字段已清且UI不被污染、key不被删、不声称回滚。stop watch→实际drain→init→手动操作路径真实运行。
6. **输出与失败：** 真main子进程满stderr/关闭stderr/可写正控、错误args/flags和锁等待取消，≤5s收尾并非0、真实Wait/join、FD flags不变；core release失败不报告成功、不交付key。可用core已验故障overlay接入口，必要额外文件先申请；不用整体test timeout/构建失败替代行为RED。init取消后可见完整key也不得自动删或重新生成。
7. **平台与回归：** Linux执行上述真行为；Darwin/Windows在各自runner实测初始化固定unsupported、FS不变，UI显示限制，旧安装unsupported不被假成功覆盖。三平台cross-build单独标 compile-only；core非Linux能力未来另授，不用Linuxstub/打包成功代runtime支持。public搜索/登录/远端导入不被缺key阻断；raw-key/custom-root低层原能力保持。

至少对“自动初始化、晚准入、跳过core、取消未drain、旧generation回填、root退回无界打印”各做先编译成功再确定断言RED的独立负控；保存patch/SHA/raw命令/退出码，恢复exact source GREEN。完整模块需非作者Spec/Quality及真实事件/子进程审；不是全14agent配置、C1/MyGo配置应用、平台安装或首发整体PASS。

## 8. 完整命令与证据要求（实施后才运行）

```sh
export PATH="$(mise where go@1.27.3)/bin:$PATH"
export CGO_ENABLED=0 GOTOOLCHAIN=local
go version
go env CGO_ENABLED GOTOOLCHAIN
sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
go test -mod=readonly -run '^TestClientKeyEntrypoints' -count=1 -v ./internal/cli ./internal/desktop
go test -mod=readonly -run '^TestClientKeyEntrypoints' -shuffle=on -count=3 -json ./internal/cli ./internal/desktop
go test -mod=readonly -count=1 ./internal/clientkey ./internal/cli ./internal/desktop ./internal/install ./internal/subscription
go vet -mod=readonly ./internal/clientkey ./internal/cli ./internal/desktop
go mod verify
EVIDENCE_DIR="$(mktemp -d /tmp/cognate-client-key-entrypoints.XXXXXXXX)"
go build -mod=readonly -o "$EVIDENCE_DIR/cognate" ./cmd/cognate
go build -mod=readonly -o "$EVIDENCE_DIR/cognate-desktop" ./cmd/cognate-desktop
GOOS=darwin GOARCH=arm64 go build -mod=readonly -o "$EVIDENCE_DIR/cognate-desktop-darwin-arm64" ./cmd/cognate-desktop
GOOS=windows GOARCH=amd64 go build -mod=readonly -o "$EVIDENCE_DIR/cognate-desktop-windows-amd64.exe" ./cmd/cognate-desktop
```

未来 plan 将§7各用例固定到 `TestClientKeyEntrypoints*` top-level/subtest与精确命令，真实main/PTY等子进程由合成fixture执行且回收。证据须区分 Go1.27.3/CGO0、本机runtime、三OS runner与仅编译结果，逐层统计run/pass/skip/fail；不启CGO1/race，不记录秘密snapshot/HAR。上述命令不是当前已运行记录，也不批准下载工具链/改依赖/使用真实HOME。
