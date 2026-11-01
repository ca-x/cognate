# 开发预览验证

## 2026-11-01：Linux 默认关闭的周期自动备份

原 exact16 patch SHA256 `dd0dc3546c3d14daad984d04c8f891bfd17ec0ecd6ce7dfa1571734a6cc6cb5a`，
非作者 Spec/Quality 限定 ACCEPT 报告
`/tmp/cognate-auto-independent-6c22784-ss6w4SgR/REPORT.md`，SHA256
`b5caa740b1410883bd993b2d2adda77d3db3c2f1c92b8ea069c25d2e10b0aeba`。
默认关闭，私有 `automatic_interval_seconds` 仅0或60–86400；首次及每轮检查后等待完整间隔，
无停机补跑。自动任务沿原 Controller/Capture/worker/Seal/S3 路径，使用实例 actor，
当前管理员可管理；所有 generation 未决历史、单活及原容量上限继续阻断新登记。
调度间隔不进入旧 target generation HMAC，策略已开启不等于执行健康。

58f9b45 + exact16 隔离组合 `/tmp/cognate-auto-main-58f9b45-6twofBM3` 的真实
Web build、91 PASS事件/0 SKIP/0 FAIL及 App.Serve 完整60秒周期均实际 exit0。
当前真实内嵌 Web 的非作者增量报告
`/tmp/cognate-auto-browser-current-58f9b45-s7i0OhCK/REPORT.md`，SHA256
`b5fd9d607c23a71f92058dfa87bec3e9af93bd254071d09e65812bca736d3b05`：
45条浏览器命令、自动远端提交、第二周期真实503、两次精确对账零重传和显式取消通过；
受保护 stop204、真实测试进程 Wait0、两个 App.Close=nil及独立 EX 释放均确认。
它直调 StartAutomatic，不冒充 App.Serve 接线资格；Serve60由前述单独用例证明。
后端是本机 TLS S3-like，不是外部供应商资格。

canonical `verify-backup-web-state.mjs` 原来把合法 auto=true 一概拒绝，原脚本在
自动候选上实际 exit1；第17路径 test-only 增量保留其余断言并增加策略正负控。
patch SHA256 `4b761dac2c020d1dc7146fe752229b48d8e74d89cfe93e35d56f09fab2e10ca0`，
非作者报告 `/tmp/cognate-auto-web-state-independent-lWv8yx/REPORT.md`，SHA256
`96535acfc82df9745eacb4c26c4c18803f2ef9542903a85c55cd86eefa0ed234`。
独立原 RED→67 checks GREEN及非法bool、disabled+auto、restore三类减门 RED通过。

主线应用后，16路径与58f9隔离候选逐字相同，脚本SHA与独审原件相同；
`/tmp/cognate-auto-main-integration-58f9b45-Ib4sxeUF` 新执行三包定向91 PASS事件、
0 SKIP/FAIL，canonical67 checks、正常 Web build及三包 vet均 exit0。
均 Go1.27.3、CGO_ENABLED=0；本自动单片没有借后继 C6 联合全仓结果作自己的全仓资格。
原作者最后浏览器 fixture 未stop超时失败和旧忽略 Close 的成功边界全部保留，
不以本次干净退出抹去。保留/孤儿治理、完整恢复、R3/T4及 GA 仍待完成；
既有150ms EX探针 UNKNOWN不由本片关闭。

## 2026-11-01：Linux 同步快照 worker 同 OFD 根 lease

R2 snapshot 子片累计 exact14：八生产源、六测试源，原四 lease kernel 和
`app_runtime.go` 不变。App 将受管 Runtime 借给 Store，DB 写前绑定 legacy parent；
Start、取消回调、实际 Wait 和物理关闭位于同步 root scope 内。子进程保留原 fd3–9，
追加同 OFD lock10/root11，严格 v2 request 在 SQL 前验证身份，直到进程退出才释放。
响应/EOF 不当作 Wait，未知 Wait/Close 经 state/library/App 保留 SH，不引入全局 dirty。

原 exact13 干净主线组合全仓实际 exit1：9010 PASS、173 SKIP、35 FAIL 事件，
唯一失败包的十六 valid 叶及父层都来自现存实际二进制测试仍发送旧 v1 mode/magic/version。
fix1 仅迁移该测试三处输入，保留全部原断言，增加旧 frame/mode 拒绝及 invalid 零 bootstrap；
所有生产源逐字未改，不通过兼容旧请求来放宽门。原失败完整保留于
`/tmp/cognate-worker-main-6f1-4cLo03JU/evidence/02-full-go.*`。

fix1 patch SHA256 `6b6032e2d6bed3066bfabdbfd26ea128f7e2e7ec05b52983255d684aab43caf2`。
非作者 Spec/Quality 限定 ACCEPT：
`/tmp/cognate-r2-worker-review-6c22784-XBFZHnsn/REPORT.md`，SHA256
`a181570cccbd97d10702bb0f93e4e6f8b841c8e4314cb4686369ff7dac160103`。
独立实跑真实父死/排空/取消/Unknown 强引用、双 SH、WAL 重启及减门 RED；
两实际二进制57 PASS事件/38叶、0 SKIP/FAIL，重新放行旧 frame/mode 的两反控均行为 RED。

主线程新干净组合（6f1 基线加 exact14；后继 c2bf 仅改 workflow/文档）全仓
`go test -json ./... -count=1 -timeout=10m` 实际 exit0：9049 PASS事件、173 SKIP、
0 FAIL、51包通过；全仓 vet exit0。十八个 server/CLI/MyGo CGO0 目标重新构建并核对
Go/OS/架构元数据，且与仅测试修订前的十八产物逐字相同。真实公开 GitHub→分类/公开→
匿名 CLI 安装→取消公开→登出/干净退出八步再次通过，观察46 skills，只断言一个安装目录。
原烟测一次误把 mise shim 当 Node 的 setup exit1 保留，之后用实际 Node 运行，不算产品重试。
收据位于 `/tmp/cognate-worker-main-fix1-YCcOadOL/evidence/01-*`、`03-*` 至 `06-*`。

独审首次重复有一次 reap、Store/Runtime Close均nil且pid ESRCH后的正向 EX 150ms 超时。
探针还执行身份检查及三个 Sync，原失败没有阶段 trace，不能归因为调度、Sync或锁泄漏；
后继30次仅观测诊断和原源码10轮通过不关闭 UNKNOWN。没有观测活 worker 时 EX 提前通过，
故审查判为非阻塞时序观察，仍留 TODO；不承诺150ms可用性，也不删除或放宽原测试。
本片不代表非 Linux 运行资格、恢复 inspector EX、完整 R2/R3、完整恢复或 GA。

## 2026-11-01：Darwin 清理收据一致性

`.github/workflows/platform-primitives.yml` 仅补齐已挂载卷 detach 非零、缺 exit 或 timeout
的失败判定，2 插入、1 删除；原 strict seal、schema 和其它 workflow 字节不变。
不加入重试、force、延迟或原始 stderr。原 run `41252902613` 的 detach16 仍属未知
OS 原因，本修订不将它改写成成功，也不声称已修复 native detach。

原单文件 patch SHA256 `2a4dc862cdd956eda30197e62ffa5ea415e22d9f1d4babfecf627a99341d281d`。
非作者 Spec/Quality 限定 ACCEPT：
`/tmp/cognate-cleanup-independent-harvey-6f1e716-3EySPruA/REPORT.md`，SHA256
`99ff38865bef80010cb56360609781917307380fb8c99d0d3298b3710047e43e`。
独立完整 cleanup harness 旧版 24 PASS/11 FAIL、候选 35 PASS；六次完整 seal 拒绝回放、
三份旧 artifact 一致性和 actionlint 通过。模拟 OS 边界不是实际 Darwin 调用资格。
主线程另跑原作者 23 案 harness，旧源 exit1、新源 exit0及显式 shellcheck/actionlint exit0；
收据位于 `/tmp/cognate-worker-main-6f1-4cLo03JU/evidence/05-*` 至 `07-*`。
后续新提交的 native workflow 仍须现场观察；本段不授予 Darwin 安装或正式版资格。

## 2026-11-01：S3 条件请求 HTTP 412 协议修复

真实 MinIO 后继诊断两次复现：第二次条件 PUT 已通过本地 bind/Stat/size/hash，服务层
一次明确为 HTTP412 但无 smithy.APIError；旧代码只识别 API 码，未进入完整回读便返回
Unavailable，外层 sealError 又映射为 Invalid。诊断报告
`/tmp/cognate-minio-invalid-diag-38c-lggG94vR/REPORT.md`，SHA256
`0e15e8a2d60c7fda0c76910eedfdfc43f3984e378d9d934e5e6fd249351f4533`。
这定位了两次新复现的转换链，不给原291行事件补造同一原因，也未证明 MinIO 为什么缺 APIError。

exact2 只改 S3Target 和新增测试，patch SHA256
`6f90bff35583c2292bf150460676d8f28a6634789888c1641c2570f5c32fd86b`，source tree
`7c6feeb702176a849ac298cae4e7554757caa47a`。共用 helper 保留原 typed PreconditionFailed，
兼认准确 HTTP412；PUT 仍须原完整长度/hash/read/close/health 回读才 Existing，DELETE
同型分支只 Conflict、不算已删除。不增加重试、无条件写/删、SDK、时限或错误正文日志。

新 TLS malformedXML 测试实际确证相同 SDK 错误形状，旧源码行为 RED；同长度异密文、
missing/denied、409/500、If-None-Match/If-Match 和单次 mutation 均有断言。一次新测试
误要求原 PutConflict 必须带 error，修正为原 Conflict+nil 合同，保留历史失败；不是产品修复。
主 clean-export backup/restore 两包完整 809 PASS、0 FAIL/SKIP及两包 vet，六目标 server
CGO0 构建和 metadata 全通过；收据 `s3-conditional-main/02-*`、`04-*` 至 `07-*`。

非作者 Spec/Quality 限定 ACCEPT：`/tmp/cognate-s3-412-review-2706-4weVVbW7/REPORT.md`，
SHA256 `56a4d82a6d30da5ff3c7f31abcd51c72312e4fb3c4de97de4dbc25968d7e2c77`。
独立新增11 PASS、相关25 PASS、vet 通过；旧源码、跳过回读、放宽状态、伪报已删除四组
行为 RED 均命中。不改其它 sealError 映射，不能宣称所有 Invalid 表象消失。

修后固定源真实 MinIO 五新进程均通过，前两轮原样 QA、后三轮仅固定布尔观测；
实际 Capture→7 密文对象条件上传→Source→Candidate 及正确/错误/匿名凭据、错误原 master
路径通过。21 次观测412均带 APIError，完整 verify 后才 Existing；本次没有再命中新 fallback，
不能将其冒称真后端无 APIError 分支的直接证据。报告
`/tmp/cognate-s3-fixed-real-minio-2706-lZ43pEGw/REPORT.md`，SHA256
`3c97baa952436525895eff636980d0408f4db714c710fcaa716cc439411289cc`。
DELETE 真后端、原291具体来源及缺 APIError 的更深机制仍未证明；不是 AWS/IAM、完整
生产恢复、无限稳定性或 GA 资格。原受限 MinIO 历史段继续保留。

## 2026-11-01：GitHub 明确重新授权与 T3S/R4 组合

GitHub exact5 patch SHA256
`575655a115c35191f03be75b1b7e30718faf4020808aa655308ce64859abef02`，三生产源和两新增
测试按冻结字节合入。非作者 Spec/Quality ACCEPT：
`/tmp/cognate-r4-independent-38c246b-Oj8SQI/REPORT.md`，SHA256
`2f0fda8c0e39df4144b10693c07d0b2126465fcc7287ffa3f174543656516816`。
独立实跑 112 PASS 事件及 5 个自写探针、三包 vet；旧四入口与单独旧 device 接点的
行为 RED 均复现。覆盖 Ready 旧行为、Needs 两连接单赢家、原 AAD/损坏密文拒绝、旧代
工作失效，以及真实 Connect→TLS device-code/poll/GraphQL→事务新代发布，不用 Complete
mock 代替。被动身份 getter 不 SELECT 密文或解密，普通 Credential 仍 ready-only。

主干净导出将 T3S exact12、撤销 exact2 与重新授权 exact5 合成 source tree
`61e267232439960a0c9a4d4c8da3ab001eb843c6`（与后继提交仅文档不同），复用正常构建的
未变 Web embed：server/state/httpapi/githubauth 四包完整测试实际 2782 PASS 事件、
18 SKIP、0 FAIL及四包 vet exit0，收据 `runtime-r4-main/01-four-packages.*`、`02-vet.*`。
此前 T3S 全仓 8838 PASS 不冒称本 nineteen-source 组合的单次全仓运行。
该组合六目标 server CGO0 构建及实际 Go/OS/架构元数据检查全部通过，见
`runtime-r4-main/03-*`。更正：最初的客户端依赖探针误把“无任何 internal/server”当作
现状，实际 exit1；客户端联合依赖清单包含 server/state/ent，直接引用来自 CLI 主入口的
既有 SnapshotWorkerDispatch，不能声称完全无服务端包或 SQL。收据 `04-client-deps.*`
保留；窄检查确认不含 moejs、server/backup 或 AWS SDK，见 `05-server-only-deps.*`。
不为此夹带已有依赖图重构；补跑最新组合客户端 12 目标构建及元数据检查全通过，
加上该组合 server 六目标，共 18 个当前源 CGO0 构建，见 `06-*`。这是两个批次，
不是把前一组 T3S 的 18 构建移记到当前源。
最新产物真实公开安装链八步再次 exit0、46 skills、自建 fixture 清理，见
`runtime-r4-main/07-public-live.*`；新旧独立代表及此主线程复跑的范围保持区分。
没有新 wire/UI/依赖；不是用户真实 GitHub/OIDC 账号、完整恢复或正式发布资格。

## 2026-11-01：真实 MinIO 有限 QA，上传稳定性仍开放

官方两个预编译校验端点返回 410 后，主线程批准从官方源码固定 tag 构建私有测试工具：
`RELEASE.2025-09-07T16-13-09Z`，peeled commit
`07c3a429bfed433e49018cb0f78a52145d4bedeb`。Go1.27.3/CGO0 构建及 metadata/静态 ELF
核验通过，不是官方预编译发布包，也不加入产品依赖。报告
`/tmp/cognate-real-s3-src-38c-XfJWKRUG/REPORT.md`，SHA256
`8aaadd2782318d37006cf8285f99579b39b67cfe4afccbde3ae70d825a64b9e0`。

固定 38c 产品代码、私有 HOME/数据、字面回环与合成可信 TLS/凭据；真实 Capture 密文
经 MinIO 的条件上传和精确回读、Source→Candidate，以及错误/匿名凭据、错误原 master
拒绝均实际运行。最终五个新进程 25 叶次通过，但保留一次 Capture.WithFiles/上传阶段
ErrInvalid 尚未定位，不能用后继绿次关闭。首次建桶的精确 503/XMinioServerNotInitialized
属于已定位的 fixture readiness；只在 fixture 等待，不为产品增加重试。
错误凭据的 Source 公开类实际为 uncertain，不改写成干净 denied。上传失败继续有界取证，
本段不是稳定性验收、当前 App 恢复闭环、AWS/IAM/其它供应商或 GA 资格。

## 2026-11-01：恢复凭据撤销事务内核

新增 `state/revokeRestoreCredentialsTx` 的内部借事务实现及测试，exact2 patch SHA256
`173de73a031a368b28389506fd69d3fc83501bf81c309afe053f6f07d603307a`。
非作者在 38c 提交版 archive 组合限定 ACCEPT，报告
`/tmp/cognate-r4-review-38c246b-3C8XpU/REPORT.md`，SHA256
`853a7ae5c8a10e39dd3965471c33bbafd3b64300337233c5434e479d4b5ac166`。
独立实跑 114 PASS 事件（106 行为叶）、39 个相关回归 PASS、4 个新增探针 PASS及 vet；
FK、revision 写前溢出、取消遮盖 unknown、AAD 破坏四个减门对照均行为 RED。
主线程在 T3S 与该两源组合实跑同组 114 PASS、0 FAIL/SKIP，见 `t4-main/01-revoke.*`。

helper 要求 caller 已验真的隔离可写事务及 FK=1，不开/提交/回滚/关闭该事务；依次清除
五类会话能力、统一撤销 share/MCP 并推进 MCP revision，将 GitHub 标为需重授权且清理
在途标记。全部业务、配置、原密文/AAD/代次与私有历史保留，并检查后置条件。
函数非幂等：固定撤销时刻不是重放许可，提交不明不得再次调用。它没有生产 coordinator、
候选发布或恢复 CLI，测试侧 commit-unknown 不作为产品交付；完整 F4/R4 仍开放。

## 2026-11-01：T3S Linux legacy 启动参与与排空

exact10 从 `2efa940` 独立构建，patch SHA256
`d251986d21df151405fff4f169805bcf54f84a7105fbd3110896fbab4b8ea3d2`，fullsource SHA256
`7cafb11bf88a27e9415130036fdcc0ff3c9c1f1e7d01c4732bf71cf20b00bc02`。
非作者 Spec/Quality 限定 ACCEPT，报告
`/tmp/cognate-t3s-independent-2efa940-GBQrgrG9/REPORT.md`，SHA256
`28b7e791f2cd9acdf6651333b8d529f863d71cfa7f273e594a9835493ea443b3`。
仅三份既有源修改、七份新增；kernel 四源与原接受字节一致，未放宽目录/key 校验。

普通及 backup-enabled App 均先准备最小根、取得 SH、检查 legacy 保留痕迹，再读/初始化
key 和构造 Store/cache/backup/logs。原 InitializeMasterKey 仍要求真正空目录；新 typed
Runtime 入口只在两件具体协调 inode 被当前 live lease 证明、完整目录扫描且 fence 为空时
允许首次 key 发布。未知 hidden/business/history/current/intent 不作为可忽略点文件；
已有 key 不覆盖，已有数据丢 key 拒绝替代。Legacy gate 只读拒绝，不解析或自动挑选运行代。

App 自取得 SH 起有 partial owner；所有 owning 构造结果立即接管。Close 一次性停止准入并
取消依赖，先退役控制器，再等 HTTP handler、Shutdown callback、Serve 尾部与最终日志，
最后关闭 library/cache、Store、logs、lease。未知资源不重试关闭，强引用保留相关依赖及 SH；
包括未能证明清理的 nil 构造与 key 错误的保守情况。listener once-wrapper 防真实 Close
已经发生后报错又被 net/http/Serve 重复调用。最终 lease 自身晚期错误沿用 kernel 边界，
不能将所有 uncertain 都说成物理锁仍持有。

独审实际执行 220 个定向行为叶、19 个独立边界、八组编译成功行为 RED 与恢复 GREEN，
四包 vet 通过。两个真实普通 server 可同时健康并阻止第三进程 EX，关闭一个仍阻止，
全部正常退出后 EX 可取；backup on/off × key init on/off 均在 EX 下禁止业务/key-parent 写入。
handler/Serve tail/library/cache/Store/log 屏障、并发 Close/GC、nil 构造与实际关闭后 EIO
均以真 EX 进程检查 authority，不拿 registry 长度或 helper noop 当证据。

额外懒猫同形测试仅在独立 namespace UID0、合成 `/lzcapp/var/cognate/{data,keys}` 进行：
fresh 自动生成 32-byte/0600 key、正常重启保持 inode/字节；无 sticky 的 0777 合成祖先
严格拒绝且不 chmod。不是新 LPK/盒子资格，也不重开用户已确认成功的旧权限问题。

主线程已将原十源合入最新 Source5 之后的组合，四包定向 244 PASS 事件、0 FAIL/SKIP；
正常 npm ci/build 均 exit0。完整隔离 fullGo/vet、真实公开链及交叉编译终态另记，
不把独审的基线结果冒称最新组合。主证据根新增 `t3s-main/`。

首次组合 fullGo 实际 exit1，vet exit0：source-refresh 白盒 App 没有新生命周期，另两项
import 测试在 Close 已正确关闭监听之后继续网络 POST。原三组失败保留，不用定向通过
抵销。fix1 累计 exact12，生产六源及 lease 四源不变，仅三个测试文件调整合同：白盒显式
生命周期；保留原慢上传/排空断言并检查监听可重绑定；新增真实 handler 关闭前 401、
关闭后 503 和 runtime 零 delegate 检查。新测试首次 httptest 默认 Host 被既有 guard
以 403 拒绝，修正为字面回环 Host；这是夹具失败而非安全 RED。四个定向顶层用例修后
6 PASS 事件、0 FAIL/SKIP。非作者 fix1 增量限定 ACCEPT，原三项合同冲突 CLOSED；
原字节三 FAIL 独立复现、完整认证 handler/零 body 读取及分层减门行为 RED 均保留。
报告 `/tmp/cognate-t3s-fix1-independent-3dfs0bc1/REPORT.md`，SHA256
`a827b44ff9eab1a86aab03f77e74f9ef44e4e71f52df3df7ca351f3c35acc8a4`。
freeze tree `5a1860a4c1b58869a748465caa0d40b94994dfcb`，patch SHA256
`d70bb98e6e4bf1f0573c37dbd017b1677ae770df84175ee0ecac528986a7f7ca`。

fix1 干净导出正常 Web 构建、三产品×三 OS×双架构 18 个 CGO0 构建及元数据核验通过，
新 binary 真实公开链八步 exit0，见 `PUBLIC-LIVE-SMOKE.md`。第一次 fix1 fullGo 因主选择
240s 包时限，在未改的 configapply durable-crash 矩阵超时（240.011s）；其余 50 包通过，
7991 PASS 事件、170 SKIP，只有该包级 FAIL，不能计为全仓成功。全 vet exit0。
保留 `fix1/05-full-go.*`，改以标准 10m 包时限重跑完整命令 `fix1/10-full-go.*`，
实际 exit0：51 个测试包、8838 PASS 事件、170 SKIP、0 FAIL；configapply 完整结束
耗时 281.138s。没有修改产品时限、测试正文或断言，也未据此猜测具体慢因。
以上是固定 T3S fix1 tree 的完整组合；之后的凭据撤销/重新授权组合另记，不混称一个输入。
前两阶段 Build `41250331711`（3802231）、`41250449473`（38c246b）本轮均已核实 success。

重要残余不变：SH 保留只对进程存活期有效；worker 父死/FD 交接、进程退出后的 durable
业务故障 fence 未交付。非参与旧 binary/supervisor 须停用；稳定命名是主线程沿原稳定根
计划的技术裁定，不保证跨 inode 全局 authority。尚无 Resolve、authenticated current、
运行代收编/旧 binary 拒读占位、撤销/发布/回退或生产恢复 CLI，不能据此放行 R2/R3/F4/GA。

## 2026-11-01：真实公开链 smoke

源码与两 Linux 二进制经独立重建/哈希绑定；真实无凭据 GitHub 整仓采集、默认私有及
owner 隔离、分类/显式公开、匿名 API 与实际 CLI 安装、取消公开拒绝再次安装、正常
退出清理的八步在独立清环境沙箱复跑 exit0。无实际安装却返回成功的 fakeCLI 在文件
断言处真实 RED；不把单纯退出 0 当安装成功。限定 ACCEPT，报告及完整范围见
`PUBLIC-LIVE-SMOKE.md`。这不等于所有文件 hash/二进制、UI、私有仓库、OIDC、跨平台或 GA。

## 2026-11-01：可信 S3 CandidateSource 桥接

`S3CandidateSource` 是冷函数，借 S3 options、私有 parent FD 和恢复 key 原 slice；调用期间
保持这些借入参数稳定。每次调用自有 target、key 副本、engine/session/bundle 和一个密文
输入 inode，所有正常错误/取消/关闭结果进入返回值；unknown/cleanup 优先，借入参数不清零。
只读 GET/HEAD，不写远端、不发布候选。WithRestoreHead 内仅 BeginOpen，session 使用外层
恢复 ctx；头 scope 全部成功关闭后才 NextRef/CopyExact/AcceptFile/Finish/consumer，不能
把整个恢复放进头的 120 秒期限或占用槽。单密文文件复用 engine 已预留的 InputStagingBytes，
不重复计费；每对象 Verified+nil、Sync、只读 FD/身份/长度检查之后才进原 AEAD 门。

作者 exact4 patch SHA256 `12b18d75f03585afcc3491ae6f50904774375e7be50e8fdc85bb6d798267c308`。
独审 `/tmp/cognate-source-s3-review-9bd0c3d-n9uhrq/REPORT.md`，SHA256
`f6fad9df988dc5c4b674b71e97d6f06537d0f333f7889e942fdda5979eac6f53`，合同内 Spec/Quality
限定 ACCEPT。旧 f674+单列依赖与真实 9bd 组合分别重建验证，不能借旧 overlay 冒称新组合。
独审新组合整个 restore 48 顶层/238 PASS、backup 11/48、独立真实 CaptureSnapshot 原密文
经 TLS 到 Prepare/WithVerified 的 3 顶层/15 PASS 及五个当前源码行为 RED 均实际执行。

将独审 317 行探针提升为永久 `source_s3_candidate_linux_test.go`，仅改四个名称（五处引用），
原 reviewer 逐字等价确认 ACCEPT；新增测试 SHA256
`9ffd8d8affec4714ed31e4c43914f3d6b1fc3d5131798ece82399db41edb7a32`，原 exact4 字节不变。
覆盖空业务与双 owner 共享 hidden/binary 原始归档、Pack/分类/配置附件；源全部明确关闭并
移走后，原 Capture 的 6/7 个完整密文对象经真实本机 TLS 恢复。晚期 target unknown、
bundle EIO+取消、consumer error、头 cleanup/unknown、末密文损坏、错业务 key 不放出 ready。
同时核对 cap 过期/one-shot、原 key/options 可重用和非 Verified 结果不得进入 consumer。

主线程 9bd+五源隔离组合完整 restore 为 **51 顶层/253 PASS、0 FAIL/SKIP**，restore/backup
vet exit0，证据主根 `source-s3-main/05-promoted-probes-combination.*` 与 `06-promoted-vet.*`。
另有忽略 Source 最终错误的编译成功行为 RED、Darwin/Windows 包级 CGO0 构建；对应
`07-promoted-source-error-red.*` 为预期 exit1，`08-package-*` 全 exit0，不算目标平台运行资格。
实际 staged-tree 五源 SHA 与已审冻结一致：restore 51 顶层/253 PASS、backup 定向
26 顶层/168 PASS，共 77 顶层/421 PASS 事件、0 FAIL/SKIP，两包 vet exit0。收据为
`09-staged-restore.*`、`10-staged-backup.*`、`11-staged-vet.*`，全为 Go1.27.3/CGO0。
前四源码阶段及文档 `9bd0c3d` 的远端 Build 现均 completed/success，不替代本片新 CI 状态。
早期主线程重新封装明文的原型测试 01/02/03 只保留为探索历史，不是这份永久原密文测试。

剩余边界明确：普通 consumer panic 和输入 Sync panic 可正常 unwind；cleanup 自身的私有
parent Sync hook 若 panic，Source parent 副本仍可能未显式关闭。独审已复现，未放出 ready，
但不计作“任意 cleanup panic 安全”的 PASS，也无远程输入触发默认 os.File.Sync panic 的证据。
本片不升级该更强保证，不重试未知 Close、不承诺全局预算或断电完成证书。真实 S3 IAM/
供应商、生产离线 CLI、startup/Resolve、撤销/发布/回退仍未通过；不关闭完整 F4/GA。

## 2026-11-01：T2 Candidate 核心与源关闭屏障

Core7 fix1 累计 patch SHA256
`a1cba77eb29b5404f2a646a5fa8d624e442cbb554b9ab9f9d3e9acbd546f4bbd`。
原独审发现 staging 中途 panic 跳过 callback 完成通知，外层 Prepare 永久自等待，
P2 BLOCK 与原 RED 保留。主线程修复后原 Socrates 增量复审 P2 CLOSED、限定 ACCEPT：
`/tmp/cognate-candidate-fix1-review-f6744b3-zoG2YE/REPORT.md`，SHA256
`29160f0c70199a44f3b7ddb9894345552c0fd29be81c5715eb2079b51e4af602`。

Stage 只材料化私有 DB/inventory/archive 文件，不产生 ready；Prepare 仅在可信 Source
恰好一次 callback 及外层全部 cleanup 成功后开放 WithVerified。后者再次真实 T1、绑定
DB bytes/hash/schema/key、核对 canonical 尾、完整文件集合/cache 正文，以及每个共享仓库
全部技能目录 path/contentHash；短命 reader/worker 关闭后才借 one-shot opaque capability。
没有公共 path/FD、激活或 rename API；不能证明任意恶意 Go Source 是否谎报 cleanup。

callback completion、scoped cancel/AfterFunc stop 立即 defer；Stage 先接管 files 再初始化，
fence FD 立即 deferred Close，不明后仍关闭自有目录但不重复 Sync。永久八点 Sync panic
矩阵先 9 FAIL 自等待，再仅通知修复的 4 PASS/5 FAIL 暴露 FD 泄漏，最终全绿；借 key/parent
不变、原 panic 可 recover、fence 保留且仅借入 parent FD 留存。独审原三探针、吞 panic/
Goexit 追加探针及两个减门行为 RED 均实际执行，restore 39 顶层/184 PASS、vet exit0。
最终 fence unlink 在全部自有 FD 明确关闭之后；不保证断电后没有保守残留，未知 Close
不重试、不删 foreign；整根非合作同 UID/root 置换不在稳定命名合同内。

本次同时组合已独审的辅助关系 gate 与 CopyFile 二分。真实 Capture/Open fixture 覆盖
空业务库、两 owner 共享隐藏/二进制归档、Pack/分类/配置及附件；源目录移走、源 scope
全部关闭后仍可验真。篡改实际 SQLite 并保持 T1/inventory 自洽、但最后共享仓库技能
contentHash 错误的反例拒绝，不能只查首仓库。主线七源 SHA 与 fix1 冻结一致；隔离组合
source tree `1c3edd309c581dff2b9ad4a585b52a8205117db6` 的完整 restore/state/backup 三包为
184/1265/545 PASS 事件，共 1994 PASS、3 SKIP、0 FAIL，三包 vet exit0。三个 SKIP 是
父进程中的具名 subprocess helper，不把它们算作行为 PASS。证据为主证据根
`candidate-fix1/24-full-three-package-combined.*`、`25-three-package-vet.*`；Go1.27.3/CGO0。

同一导出树的首次全仓命令因测试编排遗漏 Web embed 构建、并将临时 GIT_INDEX_FILE
泄入 fixture 子进程而 exit1，记录 `18-full-go-combined.*` 保留，不作为产品回归或绿色证据。
随后清除该变量，正常 npm ci/build 均 exit0，重新全仓运行的独立收据为
`20-combined-npm-ci.*`、`21-combined-web-build.*`、`22-full-go-combined-corrected.*`、
`23-full-vet-combined-corrected.*`：最终 **8627 PASS / 170 SKIP / 0 FAIL**，51 个测试包
通过，77 个无测试包；全仓 tests/vet 均 exit0。三产品 CLI/server/MyGo × Linux/Darwin/
Windows × amd64/arm64 的 18 项 CGO0 build 全 exit0，实际二进制的 Go1.27.3、CGO_ENABLED=0
及 GOOS/GOARCH 元数据均逐项匹配，见 `26-cross-builds.*`、`27-build-metadata.*`。
被测 source tree 与已推 `2efa940` 的 Go/Web/module 源码完全相同，仅后继文档有差异。
交叉构建不等于目标平台安装/运行，也不把 scoped 独审或本机全仓推成完整 F4/GA。

四阶段均已 SSH 推送读回：CopyFile `82b212d`、lease `7b58bf5`、辅助关系 `a9bc97c`、
Candidate `2efa940`。前两 Build `41247661708` / `41247819147` 已 completed/success；
后两 Build `41247945841` / `41248349185` 目前 in_progress，未称成功。后两对应 Platform
filesystem primitives `41247945745` / `41248349194` 已 completed/success；不能用它们
替代整个 Build 资格。后继启动准备设计见 `superpowers/plans/2026-11-01-restore-startup.md`，
只批准私有隔离实施，未把设计写成启动接线已交付。

可信 S3 Source bridge、startup/运行代 Resolve、凭据撤销、发布/回退、离线 CLI 和真实
业务重启仍未交付。Candidate 核心不是用户可运行的恢复产品；不发布正式标签或分发产物。

## 2026-11-01：恢复辅助配置关系准入

在同一个只读、受限 T1 worker 中增加配置排序及 Pack 配置附件 gate；不用 canonical
inventory 未包含的字段冒充完整验证。检查 owner/分区、完整成员集、连续 ordinal、
数量上限及正 ID；合法空排序、无排序的 legacy fallback、stale/future revision pin、
Unsupported 的读取状态继续保留，不要求所有附件 pin 等于当前资产 revision。

原 exact3 独审发现新增 helper 将公开 T1 MaxRecords 的 `(0, MaxInt64]` 错误收窄为
一百万，P2 BLOCK 保留。fix1 沿用原有效域；uint64 内求 remaining+1、饱和到 MaxInt64
后再转 SQL int64 LIMIT，防负溢出且不静默夹断逻辑配额。Candidate 自己的一百万政策
不能倒灌旧 API。饱和完整性依赖同 worker 已验证的实际 DB 大小、catalog、integrity/FK，
不是对任意虚拟 SQL driver 无限流的保证；既有 heap/AS/CPU/timeout 限制不变。

累计 exact3 patch SHA256 `c92525ef3e4a3f531bd5ce5e6067699e2aa4b7274d52c31d01623e7a2039d963`。
原独审增量复审 `/tmp/cognate-assets-independent-f6744b3-yy08IM/fix1-review/REPORT.md`，
SHA256 `a593bb0cb9858095e2a581d7d4fff2becc6cee2106626fac56d0f4d5cb9a14bb`，P2 CLOSED、
限定 ACCEPT。同一原 probe 在旧候选 exit1、fix1 exit0；仅放宽 guard 的负控实际产生
四个负 LIMIT，永久参数测试将其检出。fix1 辅助套件 11 顶层/88 PASS、原独审探针
3 顶层/9 PASS、state vet 通过；原 BLOCK/RED 未改写。主线三源 SHA 与冻结一致；
排除 Candidate WIP 的隔离 staged-tree 为 state 28 顶层/180 PASS、backup 2 顶层/
2 PASS 事件、0 FAIL/SKIP，两包 vet exit0。主证据根 `candidate-fix1/16-assets-staged.*`
及 `17-assets-staged-vet.*`，均 Go1.27.3/CGO0。本片不单独关闭 Candidate、完整 T2/C4/F4 或 GA。

## 2026-11-01：T3 Linux 运行根 SH/EX lease 内核

新增 runtimegen 四源，尚未接 App startup、key 初始化或运行代 Resolve。OpenRuntime 为 SH，
LockOffline 为 EX；根必须既存私有目录。部署前提是参与者维持 root/lock/fence-dir 命名稳定，
停用不合作旧 binary/supervisor；这是主线程沿原计划“稳定根永不 rename”的技术裁定，
不是用户原话，也不是全路径跨 inode 全局互斥。原 whole-root 双 authority 反例继续有效。

每个 Close 先持久化自身 pending marker，再关闭目录、根和最后锁 FD；全部成功后才按
身份删除 own marker，之后无新 Close。不明结果隔离，不重试，也不删除 peer/foreign fence。
最终 unlink 已生效却报错时不能声称仍锁住；marker 无法持久化时保留实际 authority，
不声称存储拒写再断电仍能生成持久 fence。已获得的 lease 不随准入 ctx 取消暗中释放。

exact4 patch SHA256 `9e88ac29144549a4dcd9d0396ec1d51d61049e5bfe498770a59e26503d83cdfa`；
独审 `/tmp/cognate-t3-independent-f6744b3-i7GnynYB/REPORT.md`，SHA256
`d208a76333fe857c20a359702f9efd677228d209c33c5cd1918698b50e33a76f`，Spec/Quality 限定 ACCEPT。
独立执行原 54、新 18 行为叶及关键重复，真实 SH/EX 进程、16 SH 并发关闭、未知 FD 关闭后
新进程拒入和两个破坏 fence 的 RED 负控均有收据；包 vet 与 Darwin/Windows 包编译通过。
这些不是 App 接线、非 Linux 运行资格或完整 T3/C4/F4/GA。主线四源 SHA 与冻结一致；
排除并行 Candidate WIP 的隔离 staged-tree：runtimegen 19 顶层/61 PASS 事件，
maintenance 20 顶层/29 PASS 事件，0 FAIL/SKIP，两包 vet 和 Darwin/Windows amd64
包级 CGO0 build 全 exit0。证据为主证据根 `candidate-fix1/11-lease-staged.*`、
`12-lease-staged-vet.*`、`13-lease-*.{log,exit}`；不发布新分发版本。

## 2026-11-01：恢复 CopyFile 目录查找根修

T2d `f6744b3` 的 Build `41243767350` 已现场确认 completed/success。
本片仅将已完成认证 bundle 的 CopyFile 目录定位从逐次全扫改为 lower_bound 加匹配范围，
不建立新索引、不改 wire/AAD/AEAD、extent 校验或 Close/取消语义。完整排序与连续 extent
前提由既有 Open/Finish 管线证明，未将任意目录提升为可信输入。

exact3 patch SHA256 `318c5af2236ac9e89316da3c69e6ce51851982fce93501f810e280777626c5cb`；
独审报告 `/tmp/cognate-copy-exact3-review-bpgY5yhB/REPORT.md`，SHA256
`73355862140a38e40ebb6484e8a82aaa6df9acc5b3a7981d2dd39a712bed58bb`，限定 ACCEPT。
独审实际执行新 35 叶、旧 85 叶、独立 26 叶、drain 重复 30 叶及旧实现兼容 35 叶，
全部通过；恢复旧线性算法的读数断言真实 RED 保留。100000 条真实磁盘 catalog 的定位
读取为 16–17 次，旧全扫为 100000；复杂度 O(log N+E)，不是端到端倍数声明。

主线三源 SHA 与冻结一致；隔离 staged-tree 的真实 crypto/CopyFile/restore database
组合为 10 顶层/43 PASS 事件、0 FAIL/SKIP，backup vet exit0；均为 Go1.27.3/CGO0。
证据在 `/tmp/cognate-npx-selection.TB9Vd2tL/candidate-fix1/08-copy-staged.*` 与
`09-copy-staged-vet.*`。并行 Candidate 与辅助资产 gate 不计入此片证据。
本片不关闭完整 T2、C4/F4 或 GA 门禁，不发布标签或分发产物。

## 2026-11-01：T2d 有界归档语义检查

T2b `1ba9896` 的 Build `41241972010`、T2c `720287a` 的 Build `41242531269`
现已现场读回 **completed/success**；T1 `74c7f45` 的 Build `41240209775` 同样成功。
本次仍是源码阶段，不发布正式标签、Docker 或 LPK。

T2d exact2 新增 `restore/archive.go` / `archive_test.go`，基线为 `1ba9896` 加冻结
T2c 三源只读依赖；patch SHA256
`e503e58273fc5388c829bd0ab588b62c65cfb9cb6a4d4081c475adc3f767357e`。
独立 Spec/Quality 限定 ACCEPT，报告 `/tmp/cognate-t2d-review-1ba9896-EcvOgu/REPORT.md`，
SHA256 `b4f294917dc4d8999426e9619b48eb1973a7bdccd3af7a16567c9142dd666805`。

内部 `verifyArchive` 只借用 bytes 与被动仓库期望，不修改/清零/保留输入，无文件、网络、
数据库或 goroutine。显式压缩 bytes 上限沿用 `repository.MaxArchiveBytes` 32MiB，单次
合计 references 上限 1000000：每个 repo 加其每项技能都计费，共享归档不省略工作量。
先验证预算、精确长度和整体 hash，再复用 `repository.InspectArchive` 的 Afero 安全解包与
扫描；对每一个共享仓库核对完整技能 count、map cardinality、目录 path 和内容 hash。
根技能 path 是 `.`，不自行补 SKILL.md 或规范化；内容 hash 沿用 producer，包含目录内容、
执行位及空目录语义，不仅是文档正文。错误只返回 bare 类别/context，不透传 parser/cause。

独审实际重跑 T2d 8 顶层/45 PASS 事件、T2c 9/60、repository 定向 5/10 和 vet；追加
6 顶层/17 PASS 事件，八个独立 overlay 安全门变异均产生预期行为 RED。新增覆盖真正
压缩 bytes 恰好 32MiB 的合法归档、512/513 技能边界、根/嵌套目录内容向量和共享引用计费。
链接沿用 producer 的安全忽略行为，不跟随，也不 blanket 拒绝所有含链接的合法归档。
实际 1000000 引用正控不是进程硬 RSS/CPU 证明；取消在有界阶段/循环检查点观察。

主线两源与冻结 SHA 一致，导出仅 exact2 的 staged tree，在 Go 1.27.3/CGO0 下运行：
`t2d-staged-restore.jsonl` 为 17 顶层/105 PASS 事件；`t2d-staged-repository.jsonl`
为 5 顶层/10 PASS 事件；共 22 顶层/115 PASS 事件、0 FAIL/SKIP，restore/repository vet、
gofmt、diff check 均通过。证据根仍为 `/tmp/cognate-npx-selection.TB9Vd2tL`，非全仓复跑。

expected 不认证来源；单次预算不能替代全 Candidate 累计预算。后继必须从真实 T1/canonical
引用构造期望，绑定实际 DB、inventory 与归档文件集合，管理私有暂存/FD/清理及源关闭顺序。
该片没有 Candidate/激活 capability、恢复 CLI、运行代切换或完整 C4/F4/GA 资格。

## 2026-11-01：T2c 纯 inventory 与内容闭包摘要检查

T2b `1ba9896c4d845a9871eeee2d1fd57f707612479a` 已提交 SSH 推送并读回一致。后继 T2c
新增 `internal/server/restore/` 三文件，冻结基线 `74c7f45`；exact3 patch SHA256
`6ee87a4c1a7e07c356e86f122bc2cb548dedc4e39b47a9e5389fcd9a28c844d8`。独立 Spec/Quality
限定 ACCEPT，报告 `/tmp/cognate-t2c-review-74c7f45-RHwp1I/REPORT.md`，SHA256
`5b81b63217366116f9766cde89760171d13d02abf1c3b576ca154762aed737d8`。

该内部同步检查只借 reader，至多读取声明 inventory 长度+1；64MiB inventory、100000
archives 与 1TiB 内容预算均显式封顶，archive JSON 在逐条分配前受 metadata count 限制。
检查完整字节长度/hash、严格 v1 必填头/CutID、T1 canonical references 的精确尾长度/hash/
记录数、repository+pack owner 并集、仓库与头的归档集合双向相等/唯一升序、正长度及剩余
预算无溢出累加、既有实际 NUL 域/原始摘要/BE64 closure。错误脱敏，不 Close 借入 reader、
不启动 goroutine、无磁盘/网络/激活副作用；reader 必须合作返回，取消不是 CPU 即时抢占。

本片接收的 references 三元组只是被动期望，不是 T1 capability。后继 Candidate 仍须将
metadata.Database bytes/hash、schema、key 等绑定到真实 T1 evidence，并验证实际归档
文件集合、安全扫描与每个相关仓库的技能；本片不因为自洽摘要而授予候选发布资格。

独审实际执行原 9 顶层/60 PASS 事件与 vet；新增 8 顶层/54 PASS 事件及两次预期失败的
临时变异。追加包括五个头成员缺失/null/重名、pack-only owner、等长非投影引用变造、独立
多字节 BE64 向量、七种 closure 负控、短读/EOF/错误/取消检查点，以及 DB 被动绑定边界。
64MiB 输入与 100000 archive 实际解码正控通过；1TiB 只是声明长度预算算术，不是实际文件
压力测试，输入量上限也不是 RSS/Go heap 硬限制。

作者证据保留 API 编译 RED、可编译占位的实际行为 RED、archive count 预算 RED 与 GREEN；
两次生产者 fixture setup 失败（字段误用、dispatcher 需完整 os.Args）不计作安全行为 RED。
最终真实生产者覆盖空业务+多余 user，以及双 owner 共享归档、Pack/分类；源目录移走后
复制的数据库通过 T1，inventory 检查与真实 DB/reference 摘要相符，不靠投影 golden 替代。

主线三文件 SHA 仍等于冻结；排除其它 WIP 的 staged-tree 新组合在 Go 1.27.3/CGO0 验证：
`t2c-staged-tests.jsonl` 为 9 顶层/60 PASS 事件；`t2c-staged-backup-link.jsonl` 为 T1、
头→BeginOpen 与 CopyExact 四顶层/6 PASS 事件；共 13 顶层/66 PASS 事件、0 FAIL/SKIP，
restore/backup vet exit 0。证据仍在 `/tmp/cognate-npx-selection.TB9Vd2tL`，没有重跑全仓。

完整 T2 材料化/归档验真与 T3–T5、调度保留和原 GA 门禁继续开放；没有恢复产品入口或
正式 tag/Docker/LPK。T2d 归档语义叶片另在私有目录实施，不计入本次已验收范围。

## 2026-11-01：T2b S3 恢复头借用 scope

T1 阶段 `74c7f4596e88d865997202906682a5abafbab9b0` 已 SSH 推送读回；Build
`41240209775` 现场读回 **completed/success**。T2b 作者基线 `db2b235` 的 exact3 经独审
Spec/Quality 限定 ACCEPT：`s3_restore_head.go`、其测试与仅新增十行的 `s3_transport.go`。
报告 `/tmp/cognate-t2b-independent-db2b235-W5fsJP/REPORT.md`，SHA256
`343bbcda05b29eb4007eb3885706a9b0ef739c7f045a44eaa485cb2e7f9f2337`；patch SHA256
`dcc2ff638f364b4f60be43364c6eccd8b7f4050974fb012855970ffb2bd85233`。

`WithRestoreHead(ctx, parent, expected, consume)` 显式借入私有 parent FD，固定 canonical
attempt 的 bootstrap/complete，只经 Afero 与既有 transport 顺序执行 HEAD/范围 GET 各一次，
最多四个请求。bootstrap 恰 156 bytes，complete 为 1..16384 bytes；不 List/写入/重定向/
重试，Afero 坏流自动重开被本 mode 的健康门阻止。两个头及全部物理响应 Close 成功、私有
文件 Sync/RW Close/只读重开和身份检查后才调用 consume；借入 parent 不关闭。

consume 必须合作返回，不能关闭、复制或逃逸借 FD，也不能在回调内激活。target Close
取消并等待回调及清理排空；回调后的 RO Close、unlink、目录 Sync/discard 也决定最终结果。
本地清理不明 retain/quarantine，不重试 Close 或继续准入；错误优先级与脱敏保持。此 API
只返回 error，不提供认证或激活能力；头仍须交 BeginOpen，全部源 scope 成功后才能推进候选。

独审本次实际运行 87 新叶、127 既有叶、150 并发叶次；另六个对抗叶及十轮重复，backup vet
通过。追加覆盖旧 CopyExact/VerifyExact/InspectForDelete 严格 HEAD、不明清理取消另一个
活跃 scope 的 FD 排空、物理 Close+取消+本地 Close 叠加优先级，没有放宽旧 mode。

主线三文件 SHA 与冻结一致；为排除并行 T2c WIP，实际导出仅 exact3 的 staged tree，在
Go 1.27.3/CGO0 下验证，证据仍在 `/tmp/cognate-npx-selection.TB9Vd2tL`：

- `t2b-staged-backup.jsonl`：新头 scope、CopyExact、T1 密文→DB 和旧离线密文，共 28 顶层、
  170 PASS 事件/155 叶，0 FAIL/SKIP。
- `t2b-staged-old-s3.jsonl`：另 22 个既有 S3 顶层、68 PASS 事件/62 叶，0 FAIL/SKIP；
  精确 regex 来自两份既有测试文件，未把 `^TestS3` 误称为所有旧 S3 测试。
- `t2b-staged-vet.log`：隔离组合 backup vet exit 0；共 50 顶层、238 PASS 事件，不是全仓。

T2b 不关闭候选 DB/inventory/archives 闭包、文件材料化/发布、停服锁、撤销、运行代切换、
恢复产品入口或正式版门禁；T2c 纯 inventory 检查另待独审，仍无新正式 tag、Docker 或 LPK。

## 2026-11-01：T1 只读候选数据库验真与非正 ID 根修合入

集成基线 `db2b235a68196527a88338e7a7282bc1838160e5`，其 Build `41238804826`
已现场读回 **completed/success**。本节是后继源码阶段，不发布正式 tag、Docker 或 LPK。

T1 fix1 累计 exact12（9 个新增文件与 3 个 dispatcher 接点），累计 patch SHA256
`4ca0354a1270e45f1affde3141a04e53fd2c6b7007d1d922e84979b9c4f99a00`。
原候选的 P1 BLOCK 和七个实际错误放行保留；后继独立增量复审将该 P1 关闭，Spec/Quality
限定 ACCEPT。报告 `/tmp/cognate-t1-independent-7942388-3Wf81L/fix1-review/REPORT.md`
SHA256 `2a0ce86e096b7435c35c029b74a1fd495321dcf6ed38ce288a331d8710c01cfd`。
旧候选同七例再次 exit 1，fix1 拒绝，category=0 相邻负控保持；独立关联域、稀疏 MaxInt64
正控及内部 ent_types 域隔离通过。没有以原绿色套件覆盖反例，也未批准额外 catalog。

`state.InspectRestoreDatabase` 借入私有候选目录与固定 `server.db` 的只读 FD；Linux worker
在 SQL 前安装既有资源限制，只读检查精确 schema、integrity/FK、canonical 引用与分类、原
master key/policy proof 及全部 retained GitHub/configuration 密文。所有正域分页之前共同
检查 9 表/18 列，不只在已过滤循环内拒绝。原 key 走独立匿名管道，不进入 JSON/argv/env；
不迁移、补 proof 或修改候选。错误返回零 evidence，借入 FD 不关闭，取消排空与清理不明
优先级保留。其它平台普通受控拒绝；opaque evidence 不是候选发布或激活 capability。

主线以 apply_patch 合入，12 项源码与独审冻结逐字节一致；Go 1.27.3、显式 CGO0，无 overlay，
实际 server worker 使用现有真实 Web embed。证据根 `/tmp/cognate-npx-selection.TB9Vd2tL`：

- `t1-main-state.jsonl`：17 顶层、92 PASS 事件，0 FAIL/SKIP。
- `t1-main-backup-copyexact.jsonl`：2 个 T1 加 11 个 CopyExact 顶层、75 PASS 事件，0 FAIL/SKIP。
- `t1-main-server-worker.jsonl`：真实生产 main/匿名 FD/拒绝与取消，1 顶层、7 PASS 事件。
- `t1-main-snapshot-regression.jsonl`、`t1-main-crypto-regression.jsonl`：既有 snapshot dispatcher、
  catalog 与真实密文离线回读共 4 顶层、11 PASS 事件，0 FAIL/SKIP。
- 三个受影响包 vet、exact12 gofmt、diff check 均 exit 0。没有重跑全仓或十八目标矩阵；
  父子 PASS 事件不冒充独立用例，原作者与独审旧日志不计作此次主线新执行。

T1 只证明隔离候选数据库的只读准入，不证明 live 停服、inventory/归档双向闭包、撤销、
运行代切换、恢复 CLI 或重启业务验收。T2b 头引导与完整 T2–T5、自动调度/保留及其它 GA
门禁仍开放；下一节的 P1 BLOCK 是被本次后继关闭的历史候选状态，不是当前 fix1 结论。

## 2026-11-01：恢复读取 T2a 与数据库验真 T1 的独立门禁

Web 后继 `699969f630d134ee01f358f88988b570c23c3a42` 的 Build `41237215863` 已现场读回
**completed/success**；与此前 `3a8ca47`、`7942388` 的两次成功分别对应，不表示 GA。

T2a 基线 `7942388`，作者 exact2 新建 `s3_restore.go` / `s3_restore_test.go`，独审限定
Spec/Quality ACCEPT。报告 `/tmp/cognate-copyexact-independent-7942388-KwQ59D/REPORT.md`
SHA256 `4b636d3b4539534e5430da3d4ea39c132eab956dc20c6e035ef9b53e83245644`。
独立核对 patch/source/evidence、其余 1474 文件字节与类型/可执行位不变；实际 65 新叶子、
62 既有回归叶子、4 组重复取消/并发共120叶子执行、另9个追加对抗叶子及其90次重复执行通过，
受影响 vet 通过。作者占位行为 RED、非法 writer 计数 panic RED、hash/Close 两项负控保留，
没有将作者旧证据计作本次新执行。主线 apply_patch 集成后两项源码 SHA 与冻结清单一致，
新增实际 TLS 例与 backup vet 再次 exit 0，证据在主根 `copyexact-main-*`。

读取通过 Afero 与固定有限 transport，显式对象/ciphertext hash/size 和全部物理响应关闭均成功后
才返回 Verified。已有输出可能部分或完整但仍不可信，writer 不由库关闭，不能承诺失败回滚；
任意 writer 必须从 Write 返回才能完成取消/排空。无头文件引导、产品恢复入口或外部供应商资格。

T1 exact12 则为 **P1 BLOCK**，从未合入主线。独审完整复现作者 18 顶层/80 PASS 和 vet，
另七个真实数据库反例证明 `after=0` 分页漏掉非正 ID，导致损坏 GitHub/config 密文、非法 Pack/
分类仍有 Valid evidence。报告 `/tmp/cognate-t1-independent-7942388-3Wf81L/REPORT.md`
SHA256 `fbeac7b4e0d01a9fdb13df510caac86a8be20482008abdfc1067b66d8012c919`。
原 BLOCK/失败证据保留，已退回作者做全域准入根修，不以原绿色套件、正确 key 或 FK/integrity
通过覆盖漏验。独审修后闭合前不得引入该候选；完整 C4/F4 与原正式版剩余门禁不变。

## 2026-11-01：npx 导入、Pack 附件与配置工作空间收口

基线为已 SSH 推送的 `75d570ea1c4a0fbdab47ac11bfecdcc31b1f0faa`；本轮现场读回其 Build `41226952624` 为 completed/success，已有 CLI/GUI Release 仍为 prerelease。以下是新源码阶段证据，**尚不构成 GA、正式 tag 或 Docker/LPK 发布**。完整恢复、调度保留、十四 agent/设备应用与跨平台门禁不变。

主线程证据 `/tmp/cognate-npx-selection.TB9Vd2tL`，Go 1.27.3、所有 Go 命令显式 `CGO_ENABLED=0`：

- npx 固定上游 v3 global lock → 只读投影 → 服务端 moejs → owner/origin/source/options 绑定票据 → 选择回执；新增枚举和密封 source profile 3，旧 1/2 与 290-byte 格式保持。CLI 与现代 Web 提供来源，legacy 仍只有 cc-switch/Magpie。树 SHA 不作为内容哈希，真实扫描路径归一为目录，必须显式绑定 owner 已收录内容。
- domain、profile、HTTP source mapping、单批上限和内容提示计数先有实际 RED；后来完整 importer 回归发现 host golden 失效，并补上原清单遗漏的新 parser。独立 Node 长度分帧重算 host/三源绑定摘要，保留旧两模块摘要不变。旧预览需重新预览，不把安全拒绝写成兼容完成。
- 修后完整 `importer` + `state` 两包为 **1288 PASS / 3 SKIP / 0 FAIL**（`importer-state-final.jsonl`）；旧 **1287 / 3 / 1 FAIL** 保留。真实 TLS Connect 的 JSON/proto、目录/file 四条主链和十六项 schema/auth 反例通过，owner 拒绝和幂等/status 不多下载。首次 core 独审发现永久 enum ledger 未更新；主复现 RED，只追加来源 3 的固定账本，Descriptor/Enum/NPXHTTP 组合 GREEN，不删除或放宽旧断言。
- `httpapi` + `importclient` + `cli` 完整三包另为 **1677 PASS / 33 SKIP / 0 FAIL**（`http-cli-regression.jsonl`）；五个相关包 vet 通过。这不是全仓重跑，两个组计数不叠加它们之外的定向命令。CLI 六个 OS/arch、Linux server 两架构共八个 no-cgo 构建及 Go1.27.3 metadata 检查通过，本机两个 help 执行通过；不是对应 OS 的安装运行资格，也不是最终发布产物。
- Buf 1.75.0 lint、对 HEAD breaking、Go/TS 双模板重新生成逐目录 parity 通过，无新增依赖。最初一次主 vet 因并发 Vite 清理 dist 导致 embed 输入消失，真实 exit1 保留在 `core-vet.log`；停止共享 dist 并发写后 `combined-vet.log` exit0，不为构建竞争修改产品逻辑。

### 界面独审与修复边界

- npx 作者六文件：真实生产 Web/TLS/SQLite **35 条证据、16 布局**，fixture `0/null`，原入口缺失 RED 保留于 `/tmp/cognate-npx-web-evidence`。核心与 Web 的非作者最终验收另行记录；不能把作者测试当作独审。
- Pack 作者八文件：28 业务收据、36 布局与有界终止自测；独立审核仍因 **PC-F1/P2** 阻断。143/156 的读取完成、移到边界、移除当前引用落 BODY；独立 14 项协议/隔离与 4 项几何绿色不覆盖此反例。原报告 `/tmp/cognate-pack-configuration-independent.kgyP2Dpb/work/REVIEW.md` SHA256 `252db0853721959729a78e48076eda6e953e0ae789a284a0a37fd19c4851053c`。
- 主先加永久 `--phase focus`，旧作者二进制 156 实际 RED；再仅按实际拥有焦点的同步草稿动作回可用逻辑目标，读取用 aria-disabled 保持焦点及显式 guard，无 timer/全局监听/业务重放。新生产构建和 fixture 下，**143/156 各 10 收据，fixture 均 0/null**；add/clear/discard 同形操作及外部焦点保留也覆盖。该 narrow GREEN 仍待非作者 PC-F1 闭合，不冒称完整矩阵重跑。
- 配置总览作者八文件：最终字节 80 布局、18 业务、20 键盘通过，旧精修前 960 布局不能转记为最终重跑。独审复现两个 P2：pending 保存和取消重新读取回 BODY；原 BLOCK 报告 `/tmp/newton-exact8-review-Jy2tGoJj/REVIEW.md` SHA256 `402a429c9d756fbed701a1522d8d1b45895b00fca23f3041b6a6278185a0cd8f`。作者正在原隔离副本根修，未把未验候选合入主线。

本轮状态是并行补齐功能、独立发现与修复发布阻断，不是预览改标签。未发正式版；最终合成构建、独审闭合、提交/SSH push 与远端 CI 需分别留据。

### 后继闭合与主线集成

- Pack 的 PC-F1 已由非作者独立新构建在 143/156 各 10 项焦点收据关闭，限定 Spec/Quality ACCEPT；报告 `REVIEW-PC-F1.md` SHA256 `255b8423c86328d23f3f669b514ff3e3042ecc28037dfdbc90cb453547f8e9d4`，位于上述独审目录。exact8 与 TODO 已提交并 SSH 推送 `f0e194baf0836a93889f68ed0e67abe9720de26a`，远端读回一致；现场查询 Build `41233172751` 为 **completed/success**。原 BLOCK 保留，非阻断同步只读去重 P3 另跟踪，不冒称严格单飞。
- npx core21 逐 SHA 与冻结交付一致，独立增量 26 PASS 关闭 enum ledger 缺失，限定 ACCEPT。报告 `/tmp/cognate-npx-independent.pBpipKng/evidence/v3/CORE-CLOSURE.md` SHA256 `ff9f5f48e61e4c2ebe52228fea963a53d4d130bba51a339dd8344cec8e0f19e3`。这不替代 Web、写侧兼容或完整 F6 验收。
- npx Web 初审 W1/P2 为未上传文件的来源弃稿取消/确认后落 BODY。主先以永久回归在旧 binary 复现真实 RED，再根修稳定来源控件的焦点交接；新 143 行为 19 项、156 完整 35 项/16 布局/26 图，fixture 均 `0/null`、已回收。严格终态门禁另有 39 个自测，不以零收据或仅 Go PASS 当成功。新 exact6 独立增量复审限定 Spec/Quality ACCEPT、W1 CLOSED；独立原反例/扩展探针、完整 35/16/26 与 CI exact2 检查通过。报告 `/tmp/cognate-npx-independent.pBpipKng/evidence/web-v2/FINAL-REVIEW.md` SHA256 `472c4067953b240975c198847e01c2a6612c7ff28a625c79db804ad4f64eeb37`。CI 实际仍是 Node `26`，仅本地固定 26.12.0；完整 actionlint 与基线相同的两项 SC2016 和一项 SC2034 不称全绿。
- 配置总览 focus-fix1 的 F1/F2 已由 Laplace 独立旧/新构建在 143/156 复现并闭合：旧各 15 FAIL、新各 31 PASS；原探针双版本、156 的 80 smoke 与额外外部焦点/Unknown 回归通过，终态 7/7。报告 `/tmp/newton-exact8-review-Jy2tGoJj/FOCUS-FIX1-REVIEW.md` SHA256 `b31bac8bac0915847d3c3dd1fa9e2d49375c21f774a7f0c100902ab2e9405182`，限定 exact8 ACCEPT。主线用累计 patch 集成，八文件初次逐 SHA 全匹配；没有把旧 960 布局转记为新组合证据。OpenCode provider 表单旧排序文案 P3 仍未修，不扩大十四 agent 或设备应用资格。

主线组合 Web build、CGO0 fixture 编译通过；同一新 binary 的配置 focus-regression 在 156 为 31 PASS，npx 在 143 为 19 项行为收据，均正常终止；38 条定向 Go 测试事件、两包退出 0。组合未重跑全部布局或全仓，也未将此前完整五包回归转记为新组合全仓证据。三份 runner 的终态自测分别通过（配置 7、npx 39、Pack 8 负收据与 5 异常子进程）。相关证据为主线程根下的 `combined-*`。

npx 的恰 32 路径阶段已提交并 SSH 推送 `3a8ca4723121fbaa8fa8917b352bce23c5596fbd`，远端读回一致。提交前将 staged tree `ecad0c1a3f9c6c05dfc8f8f1206bd9f4cfc8a609` 独立导出至 `/tmp/cognate-npx-staged.FHU81CXj`，排除配置界面 WIP 后重建 Web、定向 HTTP/CLI **36 PASS / 0 SKIP / 0 FAIL**、五包 vet 全部 exit 0，证据 `npx-staged-*`。该提交 Build `41235578933` 当前 **in_progress**，尚未称远端成功。配置界面的七项生产/runner/fixture hash 仍与独审相同，闭合文档单列增量；七案终态 self-test 接入 CI，新增命令 shellcheck 与禁 shellcheck 的 workflow 检查 exit 0，不改记完整 shellcheck 基线全绿。

上述片段不关闭完整恢复、备份调度/保留、完整来源/agent 配置导入、Pack 配置消费和自动应用、客户端 OIDC、跨平台实际安装/恢复或签名门禁。阶段提交另记；当前没有新增正式 tag、Docker 或 LPK。

### 两阶段推送后与 P3 后继

npx `3a8ca47` 的 Build `41235578933`、配置工作空间 `79423887903393d01ef17b8738234cc21b535d38` 的 Build `41235706942` 后继均已现场读回 **completed/success**；上面的 in_progress 是首次查询状态，不是最终结论。两次 SSH push 的远端 SHA 均与本地相同。配置阶段恰 12 路径（原 exact8、CI 一行与三份账本），不是签收其余未实现能力。

Pack PC-P3 / PC-N1 已由非作者限定 Spec/Quality ACCEPT：独立旧/新构建在156复现两组读取 RED→一组读取 GREEN，七个读取计数、失败释放、Unknown 零重放与原十项焦点通过。报告 `/tmp/pack-pc-p3-independent-iiX9My02/REVIEW-PC-P3.md` SHA256 `cd6ebe03e065523eb3421b1b2f88b571e7b5b9d3891177f5937c371cc941edbe`。主用 apply_patch 集成 exact3，初次三项 SHA 全匹配；新组合 binary 的143 `--phase reads` **9/9**、七次读均 `1/1/0`，fixture `0/null`。只限当前组件的显式读取，不推广到所有查询。

此组合再次完成 Web build、CGO0 fixture 编译，以及 Linux amd64/arm64 server 两个 CGO0 build 与 Go1.27.3 metadata 核验，证据 `final-combined-*`。它包含后继 OpenCode 排序文案候选，不是发布工件或逐 OS 运行资格。文案旧版本在真实页面以新永久回归 RED，新构建在两语言、320px/200%/深色/reduced-motion 下通过四条收据与两项几何，读提示没有触发配置写入；最终 runner 与文案的独立复核另行留据，不将其冒充已独审。首个完整正式版目标仍在推进。

该文案 P3 后继也由非作者限定 Spec/Quality ACCEPT / CLOSED：独立156旧/新构建、最终 runner 的中英提示/两项几何/关闭后排序按钮存在且启用，以及五类 mutation 计数不增通过，七案终态自测通过。报告 `/tmp/opencode-ordering-copy-review-2YCpKrJ8/REVIEW-OPENCODE-COPY-P3.md` SHA256 `a3888e8436e5944cd8cc8d0b16e58998a2e3d08061b90ce6b283264f7d156d03`；主核对 exact3 全部 SHA 一致。文案没有修改提交、凭据或 native 能力。最终两个 runner syntax/自测、两包 vet 与 diff whitespace 检查通过。旧 P2、PC-F1 原报告保留；此增量只关闭两个明确的 UI P3，不关闭完整 GA。

## 2026-11-01：MCP 与日志管理页候选集成及 F1 焦点修复

主线程把 Newton 的恰八文件候选应用至 `e2003855c69e2e72e05962d011faa8f788abd733`，逐文件 SHA 与交付清单一致，未改生产 Go 或协议。累计 patch SHA256 `29d47abd5ab902f2fca0fd691b85adfee770c1b3f333828d0a4cc204a193cfd8`，八文件清单 SHA256 `61caabfae412dde9dbb34f7a9ff72b97e1a2feeb90f7bf8d2020442271139902`。首次非作者审查为 Spec/Quality BLOCK，唯一 P2 是下面的 F1；不能用作者或主线组合 GREEN 覆盖这个独立反例。

主线程证据目录 `/tmp/cognate-management-main.jmCjuLim`，Go 1.27.3、Node 26.12.0，所有 Go 命令显式 `CGO_ENABLED=0`：

- `npm --prefix web run build`、真实 fixture 编译、七案终止契约 self-test、MCP/日志授权专项和 logging 包通过。
- `go test -count=1 -json ./internal/server/httpapi ./internal/server/logging` 为 **1223 PASS / 10 SKIP / 0 FAIL**，两个包终态 PASS；组合 JSON SHA256 `1b4f06937a0a0fd04bc42654fa157975297efad6d1dee3224c1a893b9c2b238a`。这不是再次全仓测试，不能把上阶段的 7919 直接转记成本候选结果。
- 两个相关包 vet、嵌入本次生产 Web 的 Linux server build 与 CGO0 metadata 检查通过。53 项 dist 在主线程组合后复核一致，清单 SHA256 `0b74b284c558d418f9ef5c50f8bad3707e725973093726e3dc3b2a66888673c0`；没有借此声称十八平台已重新运行或本机 GUI 验收。
- 真实 TLS/Connect/SQLite 浏览器为 **179 条收据、160 组矩阵、14 张安全图**，fixture 终态 `code=0 / signal=null`、无 failure。覆盖 token 创建/编辑/轮换/撤销/过期及逐调用授权、CAS/Unknown、防秘密持久化；日志管理员/普通用户、草稿/清理确认、CAS/Unknown、真实降级计数及失败健康未知。截图门禁拒绝一次显示区或非空密码框，不用系统剪贴板模拟成功复制。收据 SHA256 `8e300870271bb446a274042108c962cdbfc921565b49b0ce0f252be873d7db62`。
- 终止 self-test 对正常、非零退出、signal、缺 stop、缺 PASS、收据后 FAIL 及超时分别取期望结果；JSON SHA256 `7119a90caac4c04aa7ce15351dd9531331e981f82318bc77455e036ea8ac88ae`。主线程另目视安全截图的宽屏 MCP 及英文深色 375px/200% 日志页。

矩阵含中文/English、浅/深/跟随系统、正常/ReduceMotion，1440/375/320 CSS px 及 375/320px 的真实 200% 根字号、两个页面。实际键盘导航/Tab/Space/Enter/Escape与焦点检查不等于覆盖所有共享指针导航；作者旧轮 200% 共享指针导航异常仍未归因，最终矩阵采用真实键盘路由。外部剪贴板成功复制、真人读屏/触摸、其它浏览器及 MyGo 仍未验收，完整功能门禁不变，见 `MANAGEMENT-UI.md`。

上阶段 `e200385` 已 SSH push，origin main 读回一致；其 Build `41223330061` 与 Platform `41223330122` 均为 completed/success。它们只证明该提交，不是本候选的远端 CI。尚无正式 tag、Docker/LPK 更新或 GA 放行。

### F1：成功撤销后的返回焦点不能依赖 disabled 的浏览器时序

首次独审报告 `/tmp/cognate-task4-independent.MGt1w9CC/work/REVIEW.md`，SHA256 `293d3bbae5f00fd458d805804dda9326d3a7a024bb0f1a62a920df5d839f8192`。同一真实撤销在 Chromium 143.0.7499.4 中，列表刷新把触发器禁用后浏览器先原生 blur 到 BODY，layout effect 已看不到原 disabled activeElement；普通/慢列表三轮均失败。Chrome 156.0.8078.79 的三轮返回 H1，仅是版本对照，不能据此排除 143。其余缓存错误、CAS、已提交 Unknown 不重放、秘密/身份隔离及终态契约的独立检查通过；报告原 BLOCK 保留。

主线程先强化持久脚本，再用旧二进制在 143 得到同一焦点断言 RED。生产只在明确收到成功撤销通知时清空旧返回触发器，现有 Radix 关闭路径直接回稳定 H1，不再先回即将禁用的按钮；取消保留原触发器，不增加 timer、全局 focus 监听、RPC 重放或其它状态恢复。新脚本实测 Escape 取消零写入且回原可用触发器，随后慢 ListCredentials 下真实撤销、刷新完成回 H1、精确一次 mutation。

| 受控对照与修后实际门禁 | 结果与收据 SHA256 |
| --- | --- |
| 旧生产二进制 + 新永久脚本 / Chromium 143 | exit1，仅预期的稳定 H1 断言失败；fixture 0/null；`3d71cecbd3ca3354a48db32e008aaae022e776d8bc433906f9e3703efee3c3b5` |
| 修后生产二进制 + 同脚本 / Chromium 143 | exit0，10 条收据，fixture 0/null；`f217c22a1b522cf0b778e1605e0ae112f23a4ed38a4f2ab0e5154dd6b9c71789` |
| 修后生产二进制 + 完整脚本 / Chrome 156 | exit0，180 条收据、160 组矩阵、14 安全图，fixture 0/null；`c881729c6313d217e4b417872147e7d7bb8682de20a4ec9ac7bd80a023aa1ec5` |

证据仍在同一主线程目录的 `f1-*` 子项；最终 53 项 dist 清单 SHA256 `a33d9efa955ec639f989e717e6304bef763ac967477104bbcaf22d4fff2e6300`，两文件增量 SHA256 `71def6e7ad4cf25e0f80f254071a8c352f3caa671a280c0d9d19fda8e2f358d6`。修后 Web build、fixture 编译、server 嵌入 build/CGO0 metadata 和七案终态 self-test 均重新通过，源码与 dist 再核一致；不把修前的 1223 项 Go 结果记为重新运行。Build workflow 增加一行管理页 self-test，仅覆盖终态门禁，不冒称运行完整浏览器矩阵。默认 mise actionlint shim 未配置版本的编排失败保留；直接使用固定 1.7.7 二进制后结构检查 exit0，不声称完整 ShellCheck 全绿。

**F1 已由原非作者 reviewer 独立复核 CLOSED，限定 Spec/Quality ACCEPT。** 其独立构建的旧143加强探针仅三项 F1 RED；修后143/156各 12/12 PASS，包含取消/普通关闭零写入、后台焦点保持及成功撤销精确一次写入，fixture 均 0/null。报告 `/tmp/cognate-task4-independent.MGt1w9CC/work/REVIEW-F1.md`，SHA256 `3702e502bcdfae16567d5aea5d9627a4d301dd77a46e65f45b07cb15be7adf80`。新增 CI 单行另获限定 ACCEPT，附记 `/tmp/cognate-task4-independent.MGt1w9CC/work/REVIEW-F1-CI.md`，SHA256 `69840801af9f449a487b29a9f0fb0eb2e0df5b16eab930a14b9b4e3931634af5`。没有重复全仓/十八构建或借此签署 GA；原 BLOCK 与未验边界不改写。

## 2026-11-01：四组工作空间 UI 与 SQLite 修复的最终组合

本阶段基于 `59da539509f6d7aa10c28f652b72c879157be63c`，接收来源、登录/账号、仓库/分类、Pack/分享四组 UI，以及 GitHub 写事务、备份源文件锁和 Linux Secret Service 测试就绪修复。以下证据属于最终冻结组合，不覆盖后文旧候选、失败或拒绝记录。MCP/日志页面另有作者候选，尚未包含于本节。

证据目录 `/tmp/cognate-integrated-release.JWDjDE0I`。1319 项非文档/非 workflow 输入与隔离副本 `frozen-stage` 一致，52 项 Web dist 在运行门禁前后逐项相同；三组件构建后仅修改测试就绪文件，生产输入重新核验未变。主线提交前也重新核验源码清单；主线忽略的旧 dist 先未匹配冻结产物，重新生产 build 后单独核验，不能把第一次不匹配写成通过。

| 最终门禁 | 实际结果 |
| --- | --- |
| 隔离环境、Go 1.27.3、`CGO_ENABLED=0 go test -p 4 -count=1 -json ./...` | **7919 PASS / 166 SKIP / 0 FAIL**；49 测试包通过，77 包级 SKIP；`repaired.status` 为 `full_go=0` |
| 全仓 vet、24 个实际 MyGo 依赖包 vet、两个 MyGo 目录 vet | exit0；不是 macOS/Windows 真实运行 |
| module verify、tidy-diff、Buf lint/breaking、Go/Web 双生成 parity | 全部 exit0，见 `protocol.status` |
| server、CLI、GUI × Linux/Darwin/Windows × amd64/arm64 | **18 个 CGO0 build 和 metadata 检查通过**，见 `binaries.status`；不等于相应平台安装资格 |
| 三个 Build CI 门禁的本地自测 | 来源 59、备份锁 10、Auth 4 案通过，见 `ci-gates.status`；不声称远端 CI 已运行完整浏览器矩阵 |
| workflow 检查 | 结构 actionlint exit0；完整 ShellCheck 的两个 SC2016、一个 SC2034 与基线相同，仍保留，不宣称全部 lint 通过 |

最终生产 Web → 真实 TLS/Connect/SQLite 浏览器结果如下。全部 fixture 正常退出、无 signal；安全截图不包含合成秘密原文。矩阵及操作细节见各专项文档，不外推真人读屏、外部账号、其它浏览器或平台。

| 切片 | 最终结果 |
| --- | --- |
| 来源 | 395 条证据、121 组几何/完整词检查、140 张安全截图 |
| 登录/账号 | 8 个业务场景、32 组布局、49 张安全截图 |
| 仓库/分类 | 65 条证据、24 组布局、55 张安全截图，包含删除丢回执与触发器卸载后的永久 F1/F2 |
| Pack/分享 | 132 条证据、56 张安全截图；另 6 个焦点场景、5 项焦点/恢复观察，写入计数不变 |
| 备份手动管理 | 88 条证据、43 张安全截图；原专项另有 56 组布局/焦点检查 |

同组合的三组 fresh fixture × 10 seed × 21 次备份共 **630 次真实 TLS/SQLite/S3-like 备份通过**，没有恢复性 retry。GitHub 六阶段、跨连接及 N1 四格取消回归各十轮通过。GitHub 修复**确实包含写锁准入轮询**：仅专用池中尚未开始的 `BEGIN IMMEDIATE` 精确 SQLITE_BUSY，以 5ms 间隔、最多 10 秒且可取消；不重试 517、CAS、业务、提交或网络，普通连接池 pragma 不变。

备份 8714 修复由确定性子进程证明普通 DB/SHM FD close 会释放进程 POSIX 锁，并在 DMS 锁下用受控 3-byte 截短触发同类映射故障。父进程改用 O_PATH inode pin 与 metadata 检查，受限 worker 保留完整有界内容检查；不取消预算、cleanup 或结果不明 fence。该受控反例不是暂停真实 worker syscall，也不表示历史 v7 已全部归因，见 `BACKUP-SQLITE-LOCKS.md`。

**原全仓失败保留。** `final-full-go.json` 为 7910 PASS / 166 SKIP / 1 FAIL，失败在 `TestValidReplyDoesNotBypassWaitAndCancellation` 的 `fixture-connect`，不是旧等待断言或 `fixture-open-session`。真实 daemon 的 listen 入口延迟 200ms，旧 fixture 出现 bind 成功但 listen 未完成即 connect/ECONNREFUSED；改为有界 PID 管道就绪握手后同一测试通过。只改测试，不加 connect/RPC retry、sleep 或预算，不弱化等待/取消/隐私断言。专项 30 轮、正确 package cwd 整包 5 轮及上表修后全仓通过。独审独立编译新旧二进制复现相同 RED→GREEN；原当轮缺 syscall trace，旧历史原因仍 UNKNOWN，见 `SECRET-SERVICE-FIXTURE-READINESS.md`。

非作者审查：来源 N1、备份锁、Auth I1/M1、仓库 F1/F2、Pack F1 及测试就绪分别给出限定 Spec/Quality ACCEPT；CSS/i18n、三行 CI 接线和覆盖账本的联合检查为 LIMITED ACCEPT、无新增集成阻断。不把作者矩阵或上述局部结论写成完整 GA 放行。

| 冻结文件或独审报告 | SHA256 |
| --- | --- |
| `repaired-full-go.json` | `69dce0e6db103d8938b4a7c88425752094a8b5e6e36af9294ae8efe64cefedd2` |
| `repaired-source.sha256` | `c45ceb4e0a1294540d57e91c81f3a66ff7bf24e779d8c1bb3bc334f523963aa2` |
| `final-dist.sha256` | `04c5f8bc60e38f3e6b7928f9af288fc1f36fdc1cf4227b32da0f1a3d12c09898` |
| `FINAL-SUMMARY.json` | `bd2935ecde9bbd78290f816d182c2455c18762d332d0535327a75eec153a1e6e` |
| 来源 N1 `/tmp/cognate-sources-review-v2-YHluhh/work/REVIEW-N1.md` | `c9771a94d050805195a5487dad75ac372ca9c7a1615746dcac738b14a8bcfd67` |
| 备份 `/tmp/skillhub-backup-independent.3XqsMFhR/delivery/REVIEW.md` | `a66f668b7a4504f915f7b91af9c88539244e689811d44ec7aaf3672749649636` |
| Auth `/tmp/cognate-task1-auth-recheck.H09Suta7/REVIEW.md` | `824ac25d2b7fcd36875115ecba3b77fbfdcb8fcb6ead14f0d9ecbaacf9acdd73` |
| 仓库 `/tmp/skillhub-task2-f1f2-independent-q0GuUDjo/REVIEW.md` | `80956fee87614d0c277e83322bab913cd34a91c7c18e8cb8c9571dbc9af9fcd8` |
| Pack `/tmp/cognate-task3-f1-rereview.WrN3BQV5/delivery/REVIEW.md` | `4011b2f308bdc6f8e2d343701d4b332e1dd108ad37059dc4c274ac1ea81ac489` |
| 集成 `/tmp/cognate-integration-successor-independent.xyULNvSg/INTEGRATION-REVIEW-ADDENDUM.md` | `d02cbe63cf3efefc869bd8732d12fe1c76321bef9e326111b1c542ffe240d197` |
| 测试就绪 `/tmp/cognate-ss-readiness-review.hHqqVROq/work/REVIEW.md` | `45206faa97ea887bd56e5501628256f28f117b41adfcada770d674fefef48113` |

本节是阶段交付而非正式版：完整恢复/代切换、调度保留、十四 agent 与选择导入、Pack 配置附件消费、客户端 MCP/OIDC 及真实跨平台门禁继续按 `TODO.md` 和 `FORMAL-RELEASE-AUDIT.md` 推进，不能用测试数字替代未实现的功能。没有新正式 tag、Docker 或 LPK。

## 2026-10-31：GitHub 来源候选组合与并行开发

来源候选作者基线为 `c120214210c8fb790c84b9c1d30217dc72e74849`，累计十一文件补丁 SHA256 `f232865023d953de8cf861763343b51d5f8ec8edb8cc8220a1f4f8a75184df1c`。主线程将其应用至 `59da539509f6d7aa10c28f652b72c879157be63c`，逐文件核验冻结清单一致；本节旧组合数字属于该候选，不能覆盖随后 N1 增量。原独审对原 P1/P2 的 Spec ACCEPT、Quality 因写锁取消等待 N1 仍 BLOCK；主线程四格 RED→GREEN 和相关五包通过，独立准入池增量及复审边界见 `SOURCES-UI.md`。没有新 tag、Docker、LPK 或正式版发布。

沿用 React/TanStack、项目 Button/Input 与 Radix Checkbox/DeviceFlow。生产改变是来源 UI/CSS/英语文案，以及 GitHub 六处事务在读取前通过零增量 UPDATE 取得 SQLite 写锁；不改 generation、序列值、凭据密文或网络请求协议。六阶段确定性回归覆盖 catalog/credential/begin/claim/commit/subscription 的旧快照升级，独立连接池并发回归不依赖进程 mutex。作者 RED/对照证据与修复边界见 `SOURCES-UI.md`，不将此故障归因扩张到所有历史 Internal。

主线程证据目录 `/tmp/cognate-sources-main.cxZAL9iu`：

- Web 生产 build exit0 后编译真实 TLS/Connect/SQLite fixture；六阶段专项 `-count=10` 通过。
- `sources-rpc` 无 UI 的 100 轮 RPC-only exit0，无恢复性重试；门禁 self-test 59 项通过，并接入 Build workflow。
- `sources-browser` 真实浏览器 exit0：395 条证据、121 组几何/完整词检查，140 张安全原图；`receipts.json` SHA256 `e33cbc630f5895580b81168ab18ba67a9b3dc78673a62339c2890210872f238c`。范围包括中英/深浅、320/375/1440 CSS px、200% 根字号、实际键盘、ReduceMotion、授权/过期/generation/订阅/同步和未知状态，不是外部 GitHub 真账号或其它浏览器验收。真实入口为 `/`，没有新增 `/sources` 路由。
- fresh `env -i`、隔离 HOME/XDG/TMP/runtime、Go 1.27.3、离线 readonly、`CGO_ENABLED=0 go test -p 4 -count=1 -json ./...` 为 **7894 PASS / 163 SKIP / 0 FAIL**，49 通过测试包、另 77 包级 SKIP；`full-go.json` SHA256 `9d0709d2128f4ca411cb4f2690652afae006691aa9984eabfdc4730ae8953514`。
- 全仓 vet、24 个实际 MyGo 依赖包 vet、modules/tidy-diff、Buf lint/breaking 和 Go/Web 双生成 parity 均 exit0；三组件 × 三 OS × 两 arch 共 18 个 CGO0 build/metadata 通过，不等于对应平台实际安装运行。
- 1308 项非文档输入与 48 项 Web dist 在上述运行门禁前后完全一致。输入清单 SHA256 `de077078ba11c6fb020173e71a1afd1412f8b3907d3ff8a71f1aaa3638156ed9`，dist 清单 SHA256 `7f97f4166fc4e9b1bebac63b01419d3116809db08f48a12fb1b6fe9daea9432f`。此后只增加 CI 的 self-test 接线和文档；运行源码再次核对未变。
- workflow 结构 actionlint exit0；带 ShellCheck 的完整检查 exit1，两个 SC2016 与一个 SC2034 在 `59da539` 的原始 workflow 也逐项存在，保留基线与候选日志，没有以禁用 ShellCheck 冒充全 lint 通过。初次工具 shim 无版本的错误也未当作产品失败。

**未通过的组合回归必须保留。** 初次把 `httpapi.test` 传给备份脚本，该二进制无目标测试，日志明确 `no tests to run`；这是主线程编排错误，不是产品启动失败。随后用正确 `go test -c ./internal/server` 二进制运行 `backup-browser-correct-package`，在 seed 返回 HTTP500，安全账本为 `operation=wait` / `code=internal`、25 次 complete，存储 fixture 的 violation=false；此轮 exit1，不能称整组合全部绿色。

主线程进一步在隔离的旧 `59da539` 副本只增加安全诊断，不修改生产行为：`SessionUser` 仅记录 SQLite 数字码、取消/超时 bool，fixture 仅记录允许列表中的错误边界。三个完整浏览器场景通过后，第四个在 `backup-diagnostic-4` 再现 seed500：`boundary=authentication`、`operation=wait` / `code=internal`，SQLite **8714 / SQLITE_IOERR_IN_PAGE**，非取消或超时。该数字码由当前依赖 `modernc.org/sqlite@v1.60.1` 的 `lib/sqlite.go` 和 `lib/seh.go` 核对为 WAL 映射内存故障保护；触发原因仍待确定性验证，与来源页的 517 不同。没有将诊断日志加入生产，没有添加 retry，也不能据此反推历史 v7 每次失败已归因。

用户要求加速后，登录/账号、仓库/分类、Pack/分享分别在隔离副本按不重叠写集并行实现；第四条服务端线接上述映射故障的根因及最小修复，主线程负责集成。任务与边界见 `superpowers/plans/2026-10-31-parallel-workspace-ui.md`；未接收、未独审切片保持 OPEN。备份完整恢复、十四 agent、本机应用、Pack 附件与真实平台等正式门禁不缩减。

续接检索使用 nmem CLI，default Space、normal 输入/服务端 `fast_bm25_vector`、无标签过滤、上限 1：query `Cognate 手动备份Web已推送59da539 来源UI独审BLOCK修复中`，rank 1 为 `82fe4846-eda2-4814-a9ea-9da47344aed0`，同名阶段事件、score 0.99。正式范围另 query `Cognate 正式版 全部范围 十四agent 完整备份 恢复 门禁`，rank 1 为 `308322e5-56a6-46fb-8844-0dba76b862d7`，标题“Cognate阶段完成：原生UI与同切点密文管线已推送，7768测试通过”，score 0.8768186479356365；只作既有范围续接，不替代需求。CLI 无已验证的同身份/Space 浏览器图谱会话，未开启图谱。

## 2026-10-31：成熟组件的管理员手动备份 Web

基线 `1fb8ee6827dce691b84a972f2ad837fda3688d3a`，仅增加既有备份 RPC 的 Web 客户端、独立中英 namespace、管理页/布局、边界与 intent 回归、真实 App 浏览器 fixture 和生产 deep link。没有改控制内核、上传/恢复协议、数据库或依赖。React/TanStack 沿用现有版本，Button 沿用项目封装，Dialog/Checkbox 使用 Radix；未自研基础控件或新建动效系统。

主线新组合证据 `/tmp/cognate-backup-web-final.e5wE4AWO`。Go 1.27.3，先 Web 生产 build 后编译 embed；全仓测试为 `env -i`、隔离 HOME/XDG/TMP/runtime、离线 readonly、显式 `CGO_ENABLED=0 go test -count=1 -timeout=600s -json ./...`：**7885 PASS / 162 SKIP / 0 FAIL**、49 测试包。full-go JSON SHA256 `a1c0d772e1e50a814c5679211c6f348b34922bb94d18198b37aeaf7b955d36e9`。fixture 只在显式环境下启动，新增一个预期 SKIP；不代表其它平台运行资格。

全仓 vet、实际依赖的 24 MyGo 包 vet、tidy-diff/module verify、Buf lint/breaking 和 Go/Web 独立输出双生成逐文件 parity 全 exit0；备份 state **60 checks**、原 Grok 编辑/导入 state 也通过。server/CLI/GUI × Linux/Darwin/Windows × amd64/arm64 **18 CGO0 build 与 metadata** 均过；CLI/GUI 仍不依赖 server backup/moejs，server help 三备份参数存在。1303 项非文档输入与 Web dist 的前后 hash 一致，清单 SHA256 分别 `2788e2c5b21f74d0228a84096fb9227777c645bcff578d1fad18d8b4c8b874ea` 和 `53b7c8ab0bf6cd317cd25cb445411b1b8afea3001a1a011652dd83180a1589d4`。未运行 cgo 或 race。

生产 Web → 真正 App、TLS Connect、SQLite 与 synthetic TLS S3-like 浏览器 v8：**88 条收据、56 组布局/焦点检查、exit0**，正常停止 fixture。目录 `/tmp/cognate-backup-web-browser-v8`；`receipts.json` SHA256 `851c6fd341e6149683c107c2b16ae7993a16145ad5b45ab0cb67c7a12178fb77`，`layouts.json` SHA256 `70dd1d0758f07ac769097ae55f2b072062356f367377e5c5f1cb4b6a0fda695e`。包含实际管理员/普通用户拒绝、独立 disabled App、错误/非法 DTO、实际 27 任务分页、后续页 error/invalid/empty 可回最新页；丢/坏 Start 回执后重载、同 ID 两次请求只登记一任务/一发布、记录中途丢失与 storage 写拒绝均不发新请求；Forget 不取消，held upload 实际取消，无 PUT 的精确只读对账；旧 owner 持有的 Start 回复在换身份后被 abort、无旧数据泄露。26 次 complete-last、157 个已封装对象，未把远端发布当恢复。

中英/深浅 × 320/375 CSS px × 200% 根字号验证文本和控件无横向越界；实际 Tab/Space/Enter 触发并滚动揭示确认/详情按钮，完整矩形在弹层视口内。Escape 恢复触发器，确认后回稳定历史标题；ReduceMotion 无 dialog 入场动画。200% 根字号不是浏览器 zoom 或真实 OS 字体设置；未测 Safari/Firefox、真人读屏或外部 S3/完整恢复。

复现：

```sh
export PATH=/home/czyt/.local/share/mise/installs/node/26.12.0/bin:/home/czyt/.local/share/mise/installs/go/1.27.3/bin:$PATH
npm --prefix web run build
node scripts/verify-backup-web-state.mjs
CGO_ENABLED=0 go test -c ./internal/server -o /tmp/cognate-backup-web.test
node scripts/verify-backup-web.mjs \
  --fixture-bin /tmp/cognate-backup-web.test \
  --browser-bin /home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/.bin/agent-browser \
  --chrome-bin /opt/google/chrome/chrome --output /tmp/cognate-backup-web-fresh
```

**真实失败与局部资格：**

- v1 截图真实 404，生产嵌入白名单漏了 `/server-backup`；永久 `TestBackupDeepLink` 先 GET 404 RED，补精确 GET/HEAD 路由后全 Web 测试 GREEN，未知子路径仍拒绝。没有在 fixture 中放宽生产 fallback。
- v2/v3 的 malformed Start 注入没有生效，因为 recorder 的真实 gzip 响应未先解压；安全元数据确认 decode 未成功，修复 test-only 有界解压/Content-Length 后，原 ID 保留与同 ID 继续真实通过。不把这两轮当产品 DataLoss 证据。
- v5 在 200% 详情异步加载后，自动聚焦的页底按钮落到弹层视口外；v6 使用 Radix 公开 initial-focus 事件聚焦稳定标题，再以真实 Tab/Enter 揭示并激活动作通过。不是加等待、强制点击、缩小字体或遮住溢出。
- 原独审副本 `/tmp/cognate-backup-web-review.mnJLmFkj/REVIEW.md`：I1 后续页失败/非法/空时全部分页消失，M1 Continue/Forget 的触发器消失导致焦点回 body；主线修为独立返回导航/页级空文案和 Radix 确认后稳定落点。M2 同 owner client 更替依赖现有 ApiProvider `setSession` 先清 query cache 的补偿仍保留，不声称组件自身具有额外 client-ID 隔离。
- v7 在 21 任务种子阶段 HTTP500，`/tmp/cognate-backup-web-browser-v7` 保留失败；当轮没有安全 stage/code，具体原因 **UNKNOWN**，不能由之后通过反推根因或宣称修复。新增 safe operation/Connect code/job phase/code 诊断后，v8 完整浏览器通过；另 fresh RPC-only 三批 21 个、共 63 次真实备份全部通过，证据 `/tmp/cognate-backup-seed-probe.JX9VOu/receipts.json` SHA256 `2fe03cef1f92b3781e361e51eaee228b51f824f08e61243f7fbade0b5efb4273`。没有 retry/sleep 掩盖旧失败。

相同源码/二进制的第二个独立 fixture 完整轮 v9 也是 exit0、88/56，receipt hash 与 v8 相同。为尝试获取旧 seed 失败的阶段分类，另以 fail-first 的 fresh RPC-only probe 有界执行 20 批 × 21 次，420 次均实际发布，未出现可分类失败；目录 `/tmp/cognate-backup-seed-probe.UPMckc`。这只界定本次取证没有复现，并未关闭旧 UNKNOWN；后继追踪保留在 `TODO.md`，不追加无界重试来取得绿色。

**非作者后继局部 ACCEPT。** Dewey 在新隔离副本仅覆盖冻结十二输入，独立重跑 31 个真实 React/TanStack/Radix 组件探针与 12 项真实键盘观察，I1 三种坏后续页实际返回原第一页、held 旧页 abort 后不能覆盖；M1 Continue/Forget 后回稳定 heading、Escape 回触发器、异步详情不推移初始焦点、320px/200% 操作完整揭示均通过。独立 state/build/Web route/既有 Grok 回归也通过；再只读解析最终 fullgate/v8/63 seed 的工件与输入 hash，移除等待这些门禁的条件。报告 `/tmp/cognate-backup-web-recheck.rxGGaZ1x/REVIEW.md` SHA256 `65da1f1823bd98ecc13b67f64287f6ee4543c5b8ed513178245b2927868b2f76`。原 REJECT 与中间 conditional 报告均保留；最终没有新 finding。M2 是主线程保留的生产清缓存补偿，不是人类用户另行确认，也不构成 client-only 局部隔离的新实现。

阶段提交/推送身份紧随本次交付记录；未发布新 tag/Docker/LPK，未放行完整 F4 或首个正式版。来源页另有独审 BLOCK，作者交付未合入本次候选：无 UI RPC-only 可复现重新授权 GetCatalog/Sync Internal，且需补全 unexpected Internal 失败门禁，详见 `TODO.md`。上个后端提交 `1fb8ee6` 的 Build `41199035600` 已新查询 completed/success，精确 head 相同；它不是本轮 Web 的 CI 资格。

本次 nmem CLI 定向查询 `Cognate C3手动管理员备份控制平面已推送1fb8ee6`，默认检索、server `search_mode=fast_bm25_vector`、default Space、上限1，无其它过滤；rank1 为 `b76d67c7-28eb-4900-981a-dbaf91b94fbb`、标题「Cognate阶段完成：C3手动管理员备份控制平面已推送1fb8ee6，来源页UI继续」、score0.99。CLI 无已证实同身份/Space 的浏览器图谱会话，未自动打开图谱。

## 2026-10-31：备份控制内核三项修复后的新组合门禁

本段是初次 REJECT 候选的后继，不删除或改记下方原始失败。作者新冻结七文件见 `/tmp/cognate-control-fix.amkyrSXL/kernel-freeze.sha256`，清单 SHA256 `b94ed8d6dae8a3e7d4018be233367871e77a690a93e72a32de2fec61525725d1`。最后锁 close 前先持久化 private `control.fence`；不明时隔离旧能力，不能将返回错误等同于物理 flock 仍持有。确认物理释放后旧能力永久 closed，再删除/Sync 标记；此后的清理错误若删除已经可见，新进程可能进入，但旧能力不能再写或附着控制器。日志登记/catalog 发布预留全部后继记账，clean budget 拒绝不 poison；Reconcile 落盘重新合并当前取消。旧满额日志不自动迁移。这些修复仍需非作者的真实反例复核，不由下面绿色门禁代签。

主线专项在隔离环境中执行 `go test -count=1 -timeout=180s -run '^Test(ServerBackup|BackupManagement|BackupFlagsRemain|Control)' -json ./internal/server/backup ./internal/server ./internal/server/httpapi ./internal/servercli`，**115 PASS / 2 SKIP / 0 FAIL**；四包 vet exit0。证据 `/tmp/cognate-backup-fixed-product.gZgY9xoG`。与 App 独审的统计范围不同，不混加。

新组合使用 Go 1.27.3、`env -i`、隔离 HOME/XDG（含 runtime）/TMP，`GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local CGO_ENABLED=0`；先重新生产构建 Web，再编译 Go 嵌入。`go test -count=1 -timeout=600s -json ./...` 为 **7884 PASS / 161 SKIP / 0 FAIL**、49 测试包。SKIP 包含显式 fixture helper 和平台相关边界，不是未运行平台的资格证明；未运行 race 或 cgo。证据 `/tmp/cognate-control-fixed-final.RgJD7BHZ/full-go.json`，SHA256 `dfd7da110d4329bdb24eb4e54c1a4f89f1e63b06de0c13238540748b4e8928da`。

同一冻结组合下，全仓 `go vet ./...`、24 个实际使用 MyGo 包的 vet、`go mod tidy -diff`、`go mod verify` 通过；`buf lint`、相对 `HEAD` 的 `buf breaking`、Go/Web 两模板在独立输出目录重新生成并逐文件 parity 全通过。不是 MyGo 未使用 audio 包的 wildcard vet 资格。三组件 `cognate-server` / `cognate` / `cognate-desktop` × Linux/Darwin/Windows × amd64/arm64 **18 项 `go build -trimpath`** 及 `go version -m` 的 CGO0/OS/arch 校验通过；CLI/GUI 依赖闭包不含 server backup 或 moejs，实际 server help 包含三项备份参数。Web build、Grok 编辑/导入 state 与公开目录验收脚本语法通过。全部日志、状态和 metadata 在同一目录；非文档源码输入及 `web/dist` 的前后 SHA256 逐项一致，源码清单 SHA256 `21fd3f6b5f6537316c915509170eb7825ef6455f4ca3090504341f55397974dd`。

**非作者后继复核：三项 finding 范围内 ACCEPT。** Aquinas 在 `git archive 8a701c5` 只覆盖冻结七文件的独立副本中，以原生 syscall-exit 注入“实际 close 已完成但返回 EIO”，真实新进程被先持久化的 marker 拒开；另验证 partial marker、正常释放和确认释放后清理不明的精确分界。独立预算构造分别覆盖零余量干净拒绝、256 字节余量下 catalog 失败仍可持久 failed/budget 并重启/取消/幂等、2048 字节余量成功提交。并发取消、已取消终态、取消持久化不明优先、complete-unknown 和最终精确验真例外均通过，无 PUT 重发。原“恰满 pending 必须批准”旧断言的预期失败单独保留，不冒作 GREEN。十项独立探针三轮 **30 PASS**，九项必要回归三轮 **27 PASS**，均零 FAIL/SKIP；独立 vet/build exit0。报告 `/tmp/cognate-kernel-recheck.KZcIAz/REVIEW.md` 记录完整命令、证据与冻结身份，审前/后七文件一致。这个局部 ACCEPT 不重做先前 App/RPC 审查，也不替代主线组合门禁。

本轮不发布 tag、Docker 或 LPK；不代表真实外部 S3、硬件断电、完整恢复、全产品 UI 或正式版放行。残留 fence 与旧满额日志没有自动清理、迁移或恢复资格。阶段代码提交/推送与持续功能开发另行记录，不把本切片等同 GA。

提交前默认 `git diff --cached --check` 曾 exit2，唯一输出是 `backup_pb.ts` 的生成器末尾空行。为保持 Buf 重新生成逐字节一致，不手改生成物；其它文件保留默认空白检查，只有这一个 TypeScript 生成文件使用 `git -c core.whitespace=-blank-at-eof diff --cached --check -- web/src/gen/skillhub/management/v1/backup_pb.ts` 检查其余空白规则，不修改仓库 Git 配置或新增忽略规则。

## 2026-10-31：公开目录精致化与实例备份控制平面

基线为 `8a701c5`。UI 仍是首发重要目标，本轮采用现有 React/TanStack/Radix/Lucide 与系统字体，不增加依赖或改变安装、权限及插件边界。目录改为标题优先的索引，搜索/分类/已提交条件和两套分页分层；详情将来源身份与匿名命令分区，明确“公开不等于安全审查”和“仅复制、未执行”。方向反馈仅精细指针 hover 的 150ms/2px 箭头，键盘与 ReduceMotion 静态。实图、永久浏览器入口与边界见 `PUBLIC-CATALOG-UI.md`。

**备份实现：** Linux 显式 opt-in 的真实 App 将受管 Store/cache、同切点内容 Capture、既有密文容器与 S3 条件发布相连。独立外置 KEK 与私有 S3 JSON 不进入 RPC/日志；控制日志为私有、有界、AES-GCM/HKDF 与原子发布，最多一个活动任务，不无限排队。管理员六个 Connect RPC 支持状态、幂等手动登记、查询分页、取消及仅精确回读的对账。全部 ObjectRef 目录与 complete 发送意图先持久化再发生副作用；PUT unknown 不自动重发，generation 不匹配不探测旧目标。正常 Close 先排空任务，关键不明保留 App、根能力与实例锁。细节见 `SERVER-BACKUP.md` 与 `superpowers/plans/2026-10-31-backup-control.md`。

**真实产品链路：** 独立子进程以合成 `SSL_CERT_FILE` 信任本次测试 CA，不给生产 target 增加 TLS 绕过。真实 App/SQLite、站内 bootstrap 管理员、普通账号及六个 TLS RPC 验证授权先于配置探测、输入/错误脱敏与 no-store；HTTP 请求取消后已登记任务仍完成，complete 最后，正常 Close 后重启同 request ID 不重发；终态 Cancel 不假称回滚，Reconcile 仅回读。另覆盖默认关闭无备份目录、managed 配对、同 master/坏 KEK/坏配置拒绝、损坏日志不重建、普通初始化错误可回收。前两轮 fixture 失败分别是漏接 AWS SDK 的 `x-id` 操作 query、将 `page` 命名误写为 `manifest`，均修测试协议而非放宽生产边界；日志保留在 `/tmp/cognate-backup-control-main.ijoiukHl`。

**独立 App/RPC 审查：** Herschel 发现构造返回关键不明时，原 cleanup 可能以成功 Close 消去不明并释放依赖。补丁在清理前固化关键错误，保留 runtime 与 App。初版同进程 Store probe 不能证明物理锁，因为 maintenance registry 允许共享；旧日志保留但撤回其锁证据推断，没有据此增加第二个生产补丁。永久测试改为真实子进程，必须因 `maintenance.ErrLock` 被拒；原补丁移除的 overlay 普通行为 RED，修复后三轮 GREEN。独立 reviewer 另验证正常活 App 拒绝/正常 Close 允许的正控，局部 ACCEPT；独立三包专项 50 PASS、2 个预期 worker SKIP、0 FAIL，证据 `/home/czyt/.cache/backup-app-review.EjugbcnY/independent-physical-red-green.json`。主线程三包专项为 47 PASS、2 SKIP、0 FAIL，统计范围不同，不混加。

**作者控制内核证据：** 初次冻结七文件在 `/tmp/cognate-control-evidence/kernel-freeze.sha256`，清单 SHA256 `e05d56e525f444182db38a63ede318ee79f59a2b2ab1d057c48121c61907a8dd`。三轮控制用例 63 个顶层 PASS，backup 单包 97 个顶层 PASS；vet/build 均 exit0。包含持久化屏障、catalog/意图顺序、未知结果重启对账、取消、source 屏障在网络前释放、Close 与 journal Sync 竞争。作者没有穷尽最后 guard-FD/os.Root Close 的故障注入，不外推硬件断电或外部供应商，不以作者绿色替代非作者验收。

**控制内核首次独审：REJECT。** Aquinas 独立原单包 97 顶层 PASS、vet/build通过，但另外真实反例稳定复现三项：最后 guard close 实际完成却报告 EIO 时，root 保留不代表物理 flock 仍持有，新 opener 可进入；16MiB 边界没有为后继状态预留记账空间，干净容量拒绝会 poison 并卡住重启；Reconcile 回写能把已 canceled 或对账期间显式取消的任务覆盖成 interrupted。三类独立三轮证据及原生 syscall-exit 注入在 `/tmp/cognate-kernel-review.thd6PQ/REVIEW.md`。该冻结候选未提交；修复与新独审尚在收口，下面的全仓绿色不能抵消这些反例，也不能转记为修后签收。

**公开目录最后集成：** Godel 的公开目录、200% 几何与复制完整词三轮局部 ACCEPT 已闭合全部发现。主线干净 `git archive 8a701c5` 只覆盖七文件，重建 Web 与 Go 嵌入后，真实 TLS/SQLite `--checks-only` 浏览器回归 pass=true、82 项证据/71 PNG、fixture exit0；15 项定向 Go 测试零 skip/fail、vet、TS/生产 build 均通过。独立范围未包含任何备份 WIP，见 `/tmp/cognate-catalog-standalone.raeuY6AR` 和 `PUBLIC-CATALOG-UI.md`。UI 提交 `753f6a0`、逐页验收账本 `c120214`，SSH 推送并读回；全产品视觉与真人跨平台门禁仍 OPEN。

**第一轮组合门禁：** Go 1.27.3、隔离 HOME/XDG/TMP、离线 readonly、显式 CGO0：`go test -count=1 -timeout=600s -json ./...` 为 **7865 PASS / 161 SKIP / 0 FAIL**、49 测试包。161 包含仅供子进程/浏览器启动的显式 fixture helper，不代表平台资格。全仓 vet、24 个实际使用的 MyGo 包 vet、tidy-diff、module verify 通过；三组件 × 三 OS × 两架构十八次 CGO0 build 及实际 metadata 校验通过，CLI/GUI 不依赖 backup/moejs；实际 server help 包含三项备份参数，Web 生产 build、Grok 两套 state 回归与新脚本语法通过。证据 `/tmp/cognate-control-catalog-final.hkEAUHrk`，1295 项非文档输入清单，full Go JSON SHA256 `e8a8f3cdcaf7091b6ff14194a9fab03511eb48fb695614c0818402cfe92f0242`。本轮额外尝试 MyGo 全模块 wildcard vet，因未使用的音频包缺少 go.sum 条目而退出1；保留 `mygo-vet.log`，没有为此添加无关依赖，不宣称 wildcard 全绿。最终 UI 嵌入组合以最后收口记录为准。

没有新 GA tag、没有移动预览标签，也不把远端密文 `remote_committed` 称为恢复验证。自动调度、管理 Web 页、retention/孤儿清扫、完整离线恢复/运行代切换、全产品 UI 与十四 agent/Pack 附件及真实平台门禁仍保持打开。

## 2026-10-31：原生共享外壳与同切点密文内容管线

本轮以 `88d3a99` 为基线，交付 MyGo 原生共享外壳和 F4 内部内容管线，未开放 App/S3 备份任务或生产恢复入口。UI 仍是首发重要门禁，不把本轮共享组件验收扩张为全产品或真人跨平台验收。

**原生 UI：** 深浅分层材质、稳定选中导航、身份区和紧凑反馈栏；仅真实任务活动标签使用 160ms MyGo 原生颜色过渡，尊重 high contrast / ReduceMotion，不给按键和导航增加延迟。独立代表发现 Pack 在短窗/125%字号下停止操作被遮挡；原错误快照的独立反例为 10 FAIL / 2 PASS，修后 12/12 PASS；永久回归独立重放 16/16 RED → 16/16 GREEN，点击或 Return 确实取消 watch context。最大列表 End 导航、主题/焦点/草稿保护也通过。作者完整桌面包 295 PASS / 1 SKIP / 0 FAIL；真实 cage/Wayland 软件绘制窗口、Tab/Return 和前后截图见 `NATIVE-UI-DESIGN.md`，不等于硬件 GPU、IME 或其它平台运行证据。

**内容源与闭包：** 全 owner 数据库、repository/skill/Pack/item 引用与去重归档在真实 B2 屏障内读取；分类随同切点数据库复制。缺失/损坏和 path/hash/count 不符拒绝，不回源补齐。source reader 覆盖最终关闭并 pin 确切 B2 operation，关闭不明保留原文件、根能力和实例所有权。独审的逃逸复制、末次生命期、inventory 取消与错误降级四项发现均有行为反例及永久回归；最终只读复核 ACCEPT，证据在 `/tmp/skillhub-f4-source-verify.nN1xTr` 和 `/tmp/skillhub-f4-inventory-verify.vR5zyV`。这不是全流程 RSS/磁盘资源数值证书。

**密文连接：** `SealExtentSource` 同步 producer 复用既有认证格式，无新增明文 stage；`CaptureSnapshot` 逐文件切块，只有所有源端外层成功后才封 page/root/complete。验证真实源关闭移走后的数据库、inventory、归档认证回读，真实 B2 cleanup、源 Close 和组合密文清理故障的完成屏障及错误优先级，64MiB+1 实际密文分块与总体 hash，关闭消费者排空。作者 backup 包 267 PASS / 0 FAIL / 0 SKIP，不含主线程空安装永久 case；主线程独立验证空实例，并以临时 overlay 产生 127 个唯一归档、129 extent 和两页 manifest，经现有 BeginOpen/AcceptFile/Finish 回读通过。独立专项见 `/tmp/cognate-capture-main-review.l3G4b9V1`，作者具名命令与边界见 `/tmp/cognate-capture.C16jXQ68/README.md`。巨大 SQLite 端到端、生产恢复/代切换仍未验收。

**最终组合：** 隔离 HOME/XDG/TMP，Go 1.27.3，离线 readonly，所有测试/构建显式 `CGO_ENABLED=0`，未运行 race。`go test -json -count=1 -timeout=240s ./...` 为 **7768 PASS / 158 SKIP / 0 FAIL**；全仓 vet、modules verify、两个 MyGo vet，以及 server/CLI/desktop × Linux/Darwin/Windows × amd64/arm64 的 **18 项构建**全部通过。1140 个 Go/module 输入在测试前后逐项 SHA256 一致，证据 `/tmp/cognate-content-ui-final.7nyeLNwG`；`full-go.json` 的 SHA256 为 `32886ee0aec4cdf4e575b93b437026af87814179edeae5911d91029c1c5d7a1d`。

首个串行验证脚本在已记录 full-go/vet PASS 后以 143 退出，终止原因 UNKNOWN；其 module 输出不能代替退出码收据。保留 `gate-interruption.log`，modules 随后独立重跑 exit 0，构建与 MyGo vet 另批 exit 0，未把中断描述为通过或归因代码/负载。`crapper`、`dryer`、`mutator` 本机未安装，未声称运行；未新增工具依赖。原始编译 RED、独立行为 RED、测试驱动错误与最终 GREEN 各自保留，不互相覆盖。

本轮无 Web 源码变更，不冒称重跑 Web 浏览器矩阵。没有新 GA tag、没有移动预览标签。完整 F4 的管理入口/S3/任务/保留/恢复、十四 agent、本机配置应用、Pack 附件和真实平台门禁仍见 `FORMAL-RELEASE-AUDIT.md` 与 `TODO.md`。

## 2026-10-31：首发 UI 目标、Web 精致化与 Grok 原生入口

用户明确 UI 精致度、科技感和用途动效为首个正式版本重要目标，已写回需求与 `FORMAL-RELEASE-AUDIT.md`，不以本次切片代替完整首发签收。Web 阶段 `74c8583` 统一材质/层级、导航、共享弹层和按压/进入反馈，将配置研发说明收为可展开边界；成熟框架、主题、多语言与关键安全语义保留。具体前后实图、失败原因、复验矩阵与独立审查见 `UI-DESIGN.md`。主线程 build、两套 state 与原仓库浅深主题真实浏览器流程均通过。

Grok 新增生产 MyGo 页面与内存编辑接口，覆盖七模型字段、env absent、精确整数、密钥独立授权、审阅/CAS/Unknown、复制/删除/排序及身份生命周期。独立代表发现排序提交误清脏草稿和空闲导航误报取消；两项均先由回归重现，修复后再次独立复核通过，未用首轮绿色覆盖发现。此阶段仅管理服务端资产，没有本机应用或客户端插件。

最终组合在隔离 HOME/XDG/TMP、Go 1.27.3、离线 readonly、显式 `CGO_ENABLED=0` 下执行 `go test -json -count=1 ./...`：**7571 PASS / 158 SKIP / 0 FAIL**；全仓 vet、module verify 与 Linux/Darwin/Windows × amd64/arm64 六目标桌面构建通过。证据 `/tmp/cognate-ui-native-combined.xhw6k5op/final-full-go.json`，SHA256 `cdb8c44929deb6419a6bb364297415047b009564b6c326143d16677d9683b8e5`；对应变更源码摘要在 `final-source.sha256`。worker 的 desktop 最终 221 PASS / 1 SKIP / 0 FAIL，独立六函数及子场景复核通过。

真实 MyGo 窗口在独立 headless Wayland/pixman 中启动并以 Tab/Return 进入 Grok 未登录页；不是 mock 控件树，也不把 Linux 软件绘制截图外推为已登录完整流程、用户显示器或跨平台真人安装验收。完整 F4、十四 agent、本机配置应用、Pack 附件授权与正式分发等仍按审计推进，整体正式版目标未完成，不发布 GA 标签。

## 2026-10-31：MyGo Grok service/model 底座

并行 worker 新增 `grok_service.go`、`grok_model.go` 与六项真实 Connect/SQLite 测试；不注册生产 UI，Create/Update fail-closed。覆盖 owner/origin、读取安全摘要与最大整数、复制不带源凭据、删除/排序、精确确认、审阅取消、提交 revision CAS、已提交丢响应及 Unknown 禁自动重放、未知字段只读、忙时拒绝和关闭排空。worker 的定向十轮、desktop/grokclient 包测试及 vet/MyGo vet 通过；三文件 SHA256 与 `/tmp/cognate-grok-gui-boundary.YbiQrn/source.sha256` 一致。

独立代表只读审查接受该底座，另用内存 overlay 验证真实双所有者隔离与 pending review 的关闭/重复关闭；未修改源文件。本结论不扩大为完整 GUI、七字段编辑、密钥三态、本机应用或跨实例持久幂等。独立补充检测与正式无 cgo 构建证据分开，不以其替代生产 `CGO_ENABLED=0` 门禁。

主线程对最终组合重新以隔离 HOME/XDG/TMP、Go 1.27.3、离线 readonly、`CGO_ENABLED=0` 执行 `go test -json -count=1 ./...`：**7523 PASS / 158 SKIP / 0 FAIL**。证据 `/tmp/cognate-grok-import.SIpZg5z4/combined-full.json`，SHA256 `3eb70e03e8b5e5bb2e3b53413abda6643388c861cb198d79d1640e29f0a5fd3b`；desktop/MyGo vet 与 Linux/Darwin/Windows × amd64/arm64 桌面构建通过。构建不算窗口运行、签名或真人平台验收。后续先补 grokclient 内存输入/typed 脱敏读取，再接原生表单与主页面；关闭窗口、登出或切换身份时须显式关闭会话。

## 2026-10-31：Grok Build 来源导入阶段

支持 cc-switch schema24 的显式单模型配置，经安全 Go 投影、固定服务端 moejs adapter、审阅票据和真实 SQLite 加密资产写入；Connect 客户端回读全部字段并验证省略源密钥。丢响应场景只查状态，第二次提交拒绝，配置单独导入不拉取仓库。Web 复用现有审阅组件和 Grok 字段验证，不新增 UI 框架或动画；最大窗口 `9007199254740991` 以整数/十进制字符串在各层传输。

边界测试覆盖未知字段、多模型、默认引用错误、空配置、错误类型与窗口溢出、HTTP endpoint、env absent/empty、密钥 absent/empty/present、跨 agent 混合与选项拒绝。票据 transcript 单独验证模式、模型存在位、全部模型字段和 env 存在位/内容变化均改变摘要。初次相关包回归的三项失败来自旧十文件宿主指纹和“空 Grok 仍为 unsupported-agent”的旧断言；保留原失败日志，以独立 Node 长度编码计算十一文件宿主和 adapter 绑定新摘要，固定模块摘要不变；空 Grok 改为 unsupported-field，未放宽拒绝边界。

隔离 HOME/XDG/TMP、Go 1.27.3、`CGO_ENABLED=0`、离线 readonly 模块环境下，全仓 `go test -json ./...` 为 **7506 PASS / 158 SKIP / 0 FAIL**；后加 Grok 的 `TestPrepareImportedDraftParity` 单独通过。全仓 vet、modules verify、六平台 CLI/服务端构建（Linux/Darwin/Windows × amd64/arm64）通过；构建不是跨平台运行验收。Web 类型检查/生产构建、Grok 导入 state 向量、已有 Grok Web state 回归和 actionlint 通过。证据 `/tmp/cognate-grok-import.SIpZg5z4`，全仓日志 SHA256 `4c7178af016afeb9ea75f1c4303b22c5cd7b3d99455a4c726e6f0c6b55fb63d2`。

独立验收另发现两个真实反例：TOML struct 解码的 `DisallowUnknownFields()` 仍接受大小写别名甚至同现覆盖；公共客户端只接受 80 字节/JS code units 的名称，与服务端 128 UTF-8 字节不符。五个大小写反例和 81/128 字节名称反例先 RED；解析改为单次精确键 map 解码、逐层白名单及类型校验，Go/Web 名称与回执统一 128 UTF-8 字节并拒绝非法 Unicode/控制字符后定向 GREEN。保留首轮通过与独立反例，不以首轮全仓绿色覆盖该发现；修复后最终验证另记。

修复后在排除 desktop 并行 WIP 的源快照完整验证：**7517 PASS / 158 SKIP / 0 FAIL**，全仓 vet 通过；日志 `final-full.json` 的 SHA256 为 `7470d77f69ca65ac062717f51ddcb431d75b8071682bd7d63a37388b0c1ff4ae`。快照与待提交代码逐文件 cmp 一致；Web 重新构建与两组 state 向量、最新 embed 包测试及六平台 CGO0 CLI/服务端构建通过。独立代表复验 P1/P2、五相关包定向测试、Web 额外长名称预览/回执向量及宿主绑定后解除阻塞；接受范围仅为本片，不含 desktop 或发布产物。当前宿主摘要 `34c9962a2611cdfee49351132e09b229d161357319adbf9bb8690250bd664e7d`，固定 moejs 模块摘要保持不变。

本片没有 Grok 本机应用、MyGo 编辑器、Magpie 配置或完整十四 agent 导入；Web 本次只做状态与构建验证，没有新浏览器/真机验收。用户已重新允许并行推进，独立验收与 MyGo 后续实现分别隔离分工；不更新现有发布 tag。

## 2026-10-31：懒猫安装成功反馈

用户在本线程明确确认“懒猫安装成功了”，原 master-key 启动故障据此闭环，恢复剩余功能开发。此前最新交付为 rc.7，但用户没有单独确认实测版本号；不把这句话扩展为平台 OIDC、升级数据保留、文件选择器或最终 Release 下载探针验收通过。此前失败记录保留。

## 2026-10-31：懒猫 rc.4 发布与设备失败反馈

服务端 `server-v0.1.0-rc.3` 固定源码 `577ed79bacce981c474fe434094115780885efa1`，release run `41166206452` completed/success，amd64/arm64 容器构建、提升与部署验证均通过。懒猫 rc.3 run `41166452977` 在 SDK 原始模板解码阶段失败，没有上传包或商店；旧 tag 保留。整项 printf 模板改为普通环境标量加 b64enc，使用与 CI 相同的 Action v1.3.7 得到旧模板普通 RED、新模板预检查 PASS，未渲染虚构参数。密码直接解码到私有文件，真实测试覆盖特殊字符与尾部换行，子服务环境不含原始或编码密码。

包装源码 `760d225dbda3016d73416b093a4c7831edf16acd`、rc.4 run `41166888272` completed/success。喵喵响应接受 app 670/version 3753，68,021,248 字节 LPK 的 SHA256 `3b1ed55b8d1bbe319aef82d2de3e330bdc8fcca4ac89eacebdae75745338f2e8` 与 GitHub Asset digest 一致；公开 prerelease、非 latest、官方商店关闭。通过 gh 下载最终 Release 文件的 90 秒探针超时，未把该探针称作下载通过；本地提取测试与 CI/商店字节回执分开记录。

用户在 04:06Z 实测 rc.4 仍报 master-key 校验拒绝；这推翻「本地别名修复已足够解决设备问题」的任何外推，设备根因仍 UNKNOWN。暂停配置导入等新功能，包装后继 rc.5 仅添加 UID、mode、硬链接数、字节数与逐级目录元数据，不记录 key 内容或参数值、不放宽校验、不修改平台祖先权限。真实本地 LPK 提取测试覆盖原 key 保留、缺失自动生成、health 200，以及 key 合法但祖先 0777 无 sticky/硬链接数 2 的两种拒绝与诊断；不将构造场景归因于用户设备。

rc.5 run `41167603186` 已核实 completed/success，喵喵接受 app 670/version 3754，68,022,272 字节 LPK 的 SHA256 `6eda03732a17f27592ac76c9ddc0f3f0e53e1c0b393e443a73fca0b3066a5626` 与 GitHub Asset digest 一致。包装 tag 固定 `a5e554f64cfaa565f26993defb7671d7cf657476`；公开 prerelease/no-latest 的后处理也通过。已请求新启动日志的 metadata 行，不要求进入容器或提供 key 内容；尚无这些设备证据，故障不能标记解决。

## 2026-10-31：首次管理员初始化与懒猫启动候选

主线程实现并自审启动专用 bootstrap，默认关闭，无初始化 RPC 和密码 argv。空实例的账号/hash/AEAD 身份绑定同事务提交，注入元数据写失败后 User 和 LocalAccount 均为零；重开数据库保留原 UUID/hash。损坏或删除绑定、改变用户名、非空实例、缺失/过短/过大/公开权限/符号链接密码文件均拒绝。真实 App 的本地 Login 与日志管理授权在两次启动中通过；输入文件移除后仍用原密码登录。现有账户、主密钥与所有权检查未放宽。

本片最终组合全 Go 7,444 PASS、158 SKIP、0 FAIL，受影响四包 vet、module verify 和 Linux/Darwin/Windows × amd64/arm64 六项 CGO0 服务端构建退出 0。证据目录由 `/tmp/cognate-bootstrap.latest` 指向，包含 focused/full-go/vet/modules/cross-build/startup 日志。此处是主线程自测，不称独立验收。

包装仓库的真实服务测试复现旧脚本在祖先挂载别名下的 master-key 拒绝；新脚本规范路径后 health 200，原 key 保留。初始化管理员真实登录及全局日志授权通过，密码未进入 argv、child env 或捕获日志；两次重启（更换输入密码、移除输入文件）均保留原密码和 key。错误大小/公开权限/symlink key 与非法 UUID 均拒绝且不重建 key。shell、actionlint 和 `lzc-cli project lint` 通过；安装模板尚待新包构建验证，用户懒猫真机没有元数据，不能据此声称设备问题已完全解决。

## 2026-10-31：Grok provider Web

主线程实现与自审，不新增独立验收。合法 Grok 回执经既有解码入口取得普通断言 RED 后补齐入口；独立 state 向量、OpenCode 回归、类型检查与 Web 构建通过。真实 Chromium/TLS/Auth/SQLite 的 dark/light 英文 375px 场景覆盖创建、全部模型字段、精确最大整数、密钥持久化布尔、无凭据复制、env absent、Replace/Clear、切语言/模式清密钥、键盘/无横向溢出、CAS、未知字段只读和伪成功回执；提交后丢响应恰好一次写入，Unknown 不重放。两张截图人工查看，无新动画或依赖。

本片组合全 Go 7,432 PASS、158 SKIP、0 FAIL，vet 与 module verify 退出 0。证据目录 `/tmp/cognate-grok-web.vrgTBI`；full-go JSONL SHA256 `a9bf9caf288c64154bae45694a6cede2213db344f78e6e683558ae5e4891f64e`，dark/light 回执 SHA256 分别 `a1879be9c4c50bf795c7449f5f091c53a464107fb3ee9e25565a3e8692bab263`、`0ba5c9273b2f322c230dee0ef65911eda4e51fcae6e4faad2e6e6617e139be37`。前两轮浏览器脚本定位错误原样保留，不称产品 RED。该结果不包含后续管理员初始化改动，不签本机应用或完整正式版。

既有诊断提交 `b4361dc` 的 Build `41163047723` 已重新查询为 completed/success；原 Secret Service fixture-open-session 失败未复现，原因仍 UNKNOWN。

## 2026-10-31：Codex effective-v2 D2/C2 迁移

精确四实现与四测试叶匹配 source8 SHA256 `4d1f339f1434490bfe1c45d8f97f6b0b4bd9b67c233f9112d7b77566c75d30e5`。独审报告 SHA256 `1895380f079ba5fab98c9f6e6e0ebfe4e60f5857b86a466fae62103588c9a3bd`，Spec/Quality 接受；D2、C2、历史 reader 与实际 CLI/PTY/TLS/SQLite 具名门通过，四个可编译变异普通 RED 后恢复。另有五次新 native/TLS POST，完成 turn 后主动 kill+Wait(-9)，全部 worker join；不是 native 正常退出或旧 42 次重签。作者 INVALID_LAUNCHER_RECEIPT 原样保留，不用后续绿色洗掉。

主线程在当前 `365a497`（已含 B2 worker 与 watch 窄修）上组合精确 source8。Codex 三包定向 222 PASS、126 显式条件 SKIP；全仓 7,304 个测试 PASS、49 个测试包 PASS、158 显式用例 SKIP，另 77 个无测试包，零 FAIL。全仓 vet、module verify、十二项 Go1.27.3/CGO0 CLI/server 构建退出 0；复用 Web/branding 输入未变的精确既有 embed，无新 Web/browser 验收。跳过的条件/native 门不算运行通过，当前主组合不把独审五次 POST 外推为公开资格。

默认登记表仍为空，未修改 codec/tuple/平台准入、协议、模块或旧 v1 getter。完整身份与 auth/ownership 的历史保全、旧 binary 拒 v2、首次确认可能已写 v2 及不可透明降级的操作说明见 `CODEX-NATIVE-PROFILES.md`；生产登记与完整正常 CLI 消费矩阵仍是后继门。

## 2026-10-31：watch 晚期密钥丢失测试阶段边界

仅修改 `client_key_entrypoints_linux_test.go`，源码 SHA256 `9102060ac7a9942c7ca7f61f23157261d8ab84825fd7c6b3497a4dbd7df2099f`；生产代码、interval/attempt 1 秒、outer 4 秒、取锁 300 毫秒均不变。现在允许第一次成功前的合法重试历史，仍要求恰好一次成功、真实安装锁已关闭后删 key、下一次准入返回 RecoveryRequired，且成功边界后没有失败/完成报告或额外重试提示。

独立报告 SHA256 `3cbb9ae0890b8181eca69fcd6bbdee03e294ccf35f9f7165c3bcf2016ab36905`：Spec/Quality 接受，14 PASS、零 SKIP/FAIL；真实首次 attempt 到期令原断言普通 RED，新断言通过，吞掉 report 准入错误的可编译生产变异仍被新断言普通拒绝，恢复通过。全部 170 项证据经主线程校验。原两份自然失败没有 buffer，精确历史触发仍 UNKNOWN；本片不归因负载、不回写旧失败，也不签 Darwin 或 auth 候选。

主线程在当前 `b0bc3d2` 的独立源码目录以精确新测试 overlay 验证三项相邻测试各三次：9 PASS、零 SKIP/FAIL，相关 vet 和格式检查通过。复用此前正常构建的精确 Web embed，受影响提交间 Web/branding 字节未变；无新浏览器或全仓结果声明。待后续功能组合重新执行全仓门禁；`b0bc3d2` 自身 Build `41154469309` 已实际 completed/success，九个 jobs 均通过，不借该旧测试绿色代替此次修复证据。

## 2026-10-31：本地 SQLite 快照 Core27 闭合 catalog 后继

签收 source27 SHA256 `18a1e22305c59643af5b0ef849d32d9bc2be6bf40d40b449c7a465059eac2336`。相对前版仅 worker 与测试两叶根修，完整读取 92 条 schema 记录并核对两个内部完整元组，不再按 sqlite 名称前缀豁免；原应用 90 条的双指纹保留，真实 SQL NULL 与非法投影 NULL 分开处理。独立报告 SHA256 `bbf007e664bac5fc2225e8ad0a743796c8b00cdbced0e290383653eae2c17698`：Spec/Quality 接受，145 项具名用例与恢复后的 63 项通过，三个可编译控制均普通 RED；只关闭新后继的 B2-CORE-01/02，原两版 REJECT 与三个反例字节不变，不重新签资源数值资格。

主线程在 `33b68f2` 上组合精确 27 叶，核对全部 SHA256、七个前置与二十个新增路径无冲突。当前组合具名测试 141 PASS、零 SKIP/FAIL，包含 CLI/server 两种真实 binary、八种页大小、非空 WAL 及独立恢复 SQL；全仓第一次运行 7,295 个测试 PASS、49 个测试包 PASS、131 个显式用例 SKIP，另 77 个无测试包，零 FAIL。定向 vet、module verify、十二项 Go1.27.3/CGO0 二进制构建及 CLI 生产依赖检查通过。CLI 现在合法包含 state/maintenance/Ent 数据层，不能再声称零 server 前缀；生产闭包仍不包含 moejs/agentadapter/importer/httpapi 或 server App/bootstrap。

使用独立空环境与合成 HOME/XDG/TMP，无真实账号、密钥或外部业务端点；正常 npm ci/build 仅供应本次 embed，不是新浏览器验收。仅将既有 modernc/libc 从间接提升为直接依赖，版本与 go.sum 不变。数据库候选不是完整备份：App 受管接线、skill 内容闭包、任务入口、S3 发布与完整恢复尚未交付，见 `LOCAL-SNAPSHOT.md`。

该全仓绿属于本次新 B2 组合，不回写先前 Darwin 候选及 auth 候选的旧 watch 断言失败。独立诊断已用真实首次 attempt 到期证明旧断言会误拒成功前合法重试；两次原失败缺少 buffer，精确触发仍 UNKNOWN。测试阶段边界窄修另行独审，本片未修改原 watch 测试，不将偶然通过当作该问题已修复。

## 2026-10-31：Grok 所有者客户端库 G1

精确七个新库/测试叶经独立 Spec/Quality 接受，报告 SHA256 `bed19f33a4f6540dd1c1d0c0a0a19721d5a6110ce20abb3117051e5aa51f2f45`，源码包 SHA256 `c458549fea898ac661d682a9b5f7e1f386b147485e78bc801ea20aa828501354`。独立真实 App/TLS、第二 SQLite、九 RPC、凭据快照、Unknown 与关闭生命周期探针通过；五项可编译负控均普通 RED，恢复同一源码通过。仅接受库，不接受 CLI/TTY、GUI 或 native 应用。

主线程从 `289dc98` 导出完整组合，七叶逐 SHA256 匹配；原 1,291 叶字节/可执行位不变。使用独立 HOME/XDG、合成账号/数据库及验证 CA 的回环 TLS，正常 npm ci/build 后，定向 62 个用例 PASS，零 SKIP/FAIL；全仓 7,129 个用例 PASS、48 个测试包 PASS、130 个显式用例 SKIP，另 78 个无测试包，零 FAIL。全仓 vet、module verify、库六目标构建、CLI/server 十二项 Go1.27.3/CGO0 构建及元数据检查均退出 0；库的生产依赖不含 server/moejs/importer/agentadapter。无 race、非 Linux 运行或浏览器验收声明。此后 `21bd8a0` 与 `deb8473` 只改发布 workflow/文档，Go/Web/运行资源未变；本次库源码保持签收原字节。

首个本地启动器把 XDG 从旧 HOME 展开，已终止并保留为无效 setup，不计入上述结果；修正为先创建独立 HOME 再派生 XDG，才取得上述完整终态。未修改产品源码来冲洗失败。CLI 命令及真实终端确认仍待 G2，库保存不代表本机应用或完整 F12。

## 2026-10-31：公开服务端 Docker 预览

`server-v0.1.0-rc.1` 指向 `289dc98`。原发布 run `41149414526` 的共享 Go/Web/Buf 检查、amd64/arm64 完整容器 smoke 成功，OCI 原样上传；但第二次按同一 index digest 拉不同架构触发 Docker classic store `cannot overwrite digest`，原 run 保持 failure。`21bd8a0` 将检查拆为无发布写权限的独立 workflow，按 index 内各架构 child digest 分别拉取，禁止用重发镜像来重试验证。

唯一只读 dispatch `41150799480` 在精确 `21bd8a0` completed/success：tag 与预期 index、双 child digest 绑定，真实 pull/architecture inspect/双 server `--help` 及无凭据访问均成功。主线程另独立匿名读回 index 字节，SHA256 为 `ac5c5721b5ad8eaa9469c14f0607ce0e7bb2b7c6c7aadf26e06fe715865f36fc`，只有 linux/amd64 和 linux/arm64。未重建、重推、移动 tag 或更新 latest；这只是公开 server preview，不是完整产品正式版、CLI/GUI 发布或新平台配置应用资格。拉取地址与不可变引用见 `RELEASING.md`。

## 2026-10-31：Codex effective ModelInfo v2 codec

独立接受精确 source11 后继，源码包 SHA256 `4ccccfb5a833ccef8b4c5aa7a6c94268b7406f94464fe35502e831a781081c48`，报告 SHA256 `7af47f82252df888aa4f59023736846b4330e46abe752f6fe920857494539a62`。原 selected-instructions finding 仅在该后继的限定范围关闭；原 REJECT、旧 40 次与原反例保留。独审实际执行 codec 212 PASS、零 FAIL/SKIP，并核验新冻结的 42 个真实 POST/completed、主动 kill+Wait(-9)、TLS worker join 与 source/tuple/ELF 绑定，没有冒称重跑 42 次。两个合法空串补充案例通过存在性、完整文本和精确 item ID 证明 base-prefix 不发出，不与 absent/null 或默认文本混淆。

主线程从当前主线组合已签收的 Codex CLI 与 v2 codec，代码/数据十叶（包括先前已交付且不重复修改的 LICENSE）与接受输入逐 SHA256 一致；profile 文档保留原文后仅追加限定验收状态。完整组合全仓测试退出 0：7,114 个含包及子用例 PASS、47 个测试包、130 个显式条件用例 SKIP，另 78 个无测试包，零 FAIL。全仓 vet、module verify 与六平台乘 CLI/server 的十二项 CGO0 Go1.27.3 构建及元数据检查通过；当前主树所有实现逐叶与该组合相同，仅需求和阶段文档有增补。独立 codec/消费者证据不替代 D/C2 helper 迁移或生产 profile 登记，默认门仍关闭。

主线程最初在 Git-free 审查目录尝试导出主线，因错误 cwd 未生成源码；该 setup 失败保留，随后显式从固定主仓提交导出，未计为产品 RED 或通过结果。完整测试使用复用的精确 Web embed 产物，无新 Web/browser 运行声明；后继提交实际 CI 和产物仍须各自读回。

暂存后的 whitespace 检查仅报告固定上游 `testdata/native-0.166.0/prompt.md:151` 的原始末尾空格；保留其已签收 SHA256，不以 trim 改写消费者模板证据。排除该单一原始数据叶后，其余暂存差异检查通过，不新增通用忽略规则。

## 2026-10-31：Codex CLI 显式编排与原操作确认

原 Q4 独审发现恢复的同终端复核遗漏 source operation，保持原 REJECT 和普通 RED；后继仅修改原五路径内的三叶，明确显示原操作与新计划并维持最终确认绑定新计划。原字节不变的真实 main/App/TLS/SQLite/native/PTY 探针转绿；独立回退旧 renderer 或错误确认原操作的编译负控均普通 RED，恢复原后继通过。修复独审报告 SHA256 `160a38cb554471aff2360c4a57a6e1c96b0c3f432a1892e4494ad5091ef40e6e`，仅关闭该后继的同终端身份显示问题，不改旧失败历史。

四个 CLI 实现/测试叶逐字节等于签收后继；主线既有配置文档完整保留后，追加原 CLI 说明。独立最终组合报告 SHA256 `1b71ae02889edd2b4616a77fd8503c6cd61ac6d693f98d133d12d765999422ca`，确认精确五叶组合 tree `4deaebafe324242f755c9441b77adcbfafb8c969`，其它 1,279 叶 mode/bytes 不变。主线程实际 stage 后计算得到相同 tree，再单独添加本段与需求进度说明；无额外实现漂移。独立默认构建/renderer 窄门 8 PASS、零 FAIL/SKIP，普通实际 CLI 拒绝资格并不创建 HOME 或发放能力；生产依赖不含 server/moejs，登记表仍为空。

主线程固定最终实现组合中 cli/configapply/configclient 三包测试退出 0：1,760 个含包及子用例 PASS、103 个显式条件 SKIP、零 FAIL；六目标 CLI CGO0 构建、相关 vet 和 module verify 均退出 0。没有重跑旧完整 native 批次，不将条件跳过或交编称为新消费者/平台运行资格；尚不开放普通 Codex/v2 应用、MyGo 接线或十四 agent/F12。

## 2026-10-31：Grok Build 类型化 Connect API

精确八个手写路径和两个正常 Buf 生成路径已获非作者 Spec/Quality 独立签收；报告 SHA256 `b031c4a5b6ed3108436b50f12a138284073875c76547660b466064230dec9f18`，输入源码包 SHA256 `fc609b234cca605f6a61a38fc36835dc6d291314b4ab50e8dad1d1fb0e95a680`。独立真实 App/TLS、第二物理 SQLite、protobuf/protoJSON 验证覆盖所有者/Origin 隔离、凭据及未来字段、CAS、复制、完整旧 descriptor 保全与关闭；四个可编译负控均普通断言失败，原源码恢复通过。独立冻结片测试 66 PASS、邻接 219 PASS、独立探针 16 PASS，均零 FAIL/SKIP；Buf 双模板全部 35 生成叶一致、仅两个生成差异，lint/breaking、Web build、vet/module verify 退出 0。

主线程在不可变最终实现组合中全仓测试退出 0：7,044 个含包及子用例 PASS，47 个测试包、110 个显式用例/helper/platform SKIP，另 78 个无测试包；零 FAIL。全仓 vet、module verify、六平台乘 CLI/server 的十二项 CGO0 Go1.27.3 构建及元数据检查通过。该固定组合与合入主树的实现逐叶一致，仅 Docker、workflow 和阶段文档不同；主树十个接口叶 SHA256 与独立签收一致，生成文件由正常 Buf 再生，不手工编辑。

原旧 schema 的创建请求 400、`envKey:null` 被接受为 200 和 Gemini 当前 union 个数断言的普通 RED 均保留；后两者分别以既有 strict codec 的 Grok 分支和新增字段标签断言修复，不放宽预算或旧字段语义。所有者接口不代表 Grok Web/CLI 编辑器、导入、本机 TOML 写入、消费者资格或完整十四 agent/F12。Docker 实跑按各提交的新 CI 另核，旧失败不追溯改绿。

## 2026-10-31：Grok Build 加密资产域

独立签收精确八个领域层路径，沿用既有 Store 加密/AAD、单快照读取、所有者隔离、revision CAS、总配额和排序事务，无 schema/module/native 登记变更。非作者独审实际执行领域/adapter 回归、独立 SQLite/moejs 探针和四个可编译负控，Spec/Quality 均接受，零未关闭 finding；报告 SHA256 为 `40c4ddc9d8187c8a1c0af3224a3a46c506aea082432e4926f34906e082642305`。

主线程精确核对领域八叶 SHA256，并在包含后续私有 RPC 候选的固定组合执行 configuration/agentadapter/state/httpapi 四包测试：3,037 个含包及子用例 PASS、五个显式 helper/browser SKIP、零 FAIL，退出 0。该命令开始后另补的 binary-only API 测试不计入此次数量；八个领域源文件始终不变，新增 binary-only 测试另行通过。这里只交付加密资产域，不借用未签收 RPC 候选的断言作其独立验收，不代表 Grok 编辑器、CLI/native 应用或完整十四 agent/F12 完成。

提交前主树仅集成精确领域八叶、没有 RPC 候选时，主线程新鲜执行 configuration/agentadapter 两包：944 个含包及子用例 PASS、零 FAIL/SKIP；对应 vet 退出 0，八叶 SHA256 再次全部匹配。

## 2026-10-31：可读许可打包与 Docker 实跑跟踪

源码 `489bcf2` 的 Native desktop `41137504523` 已完成三系统双架构打包、Go/VCS 身份和实际安装包许可检查；`publish=false`，发布 job 跳过，没有创建正式 Release。Windows、Darwin、Linux 三个实际上传 artifact 的 ZIP SHA256 均与 GitHub 记录相符。此处是 unsigned/ad-hoc 预览产物及打包验证，不是安装运行、完整客户端或消费者资格。

源码 `45b172e` 的 Build `41137697438` 中，全仓测试和六组二进制通过，两个 Docker 架构均在容器运行检查 exit1。旧失败原样保留；缺少命令级诊断，原因尚未确认，不猜测为权限或环境。新增预检仅检查最终镜像的固定公共 LICENSE 路径元数据和 UID10001 可读/完整 hash，不挂载数据或密钥，原默认用户、启动、私有数据、错误密钥拒绝、重启和清理断言不变。后续新源码 CI 负责给出实际定位证据，不通过重跑旧任务将其改绿。

后继诊断源码 `ef9e49f` 的 Build `41140357215` 已终态失败；全仓测试与六组二进制成功，两架构最终镜像均在 `stat /usr/share/licenses/cognate` 失败。实际日志显示 `/usr/share/licenses` 为 `0644`，缺少目录搜索权限；即便 UID0，在 cap-drop ALL 下也不能越过该目录。UID10001 检查与原业务 smoke 被跳过，不记作通过；这不追溯认定旧 run 的首个失败命令。修复仅在 LICENSE COPY 后对三个精确公共祖先目录设置 `0755`，文件继续 `0644`，不递归改权限，不改变 `/data` 的 `0700`、默认用户或任何 CI 断言。非作者静审通过，actionlint 通过；本机无 Docker/Podman，实际修复结果仍须后续精确提交的双架构 CI 验证。

实际闭环：精确修复提交 `bd8cc64b78cafb14f228b14ef97d498b5b168df8` 的 Build `41141720391` 已 completed/success，九个 jobs 全成功；amd64/arm64 两个最终镜像的原预检和完整业务 smoke 均成功，未跳过。主线程读取两架构原始 job 日志，三个公共目录实际均 `0755`、LICENSE `0644`，独立 UID10001 与业务容器各自的完整 hash 检查均 OK；默认用户、私有数据、错误密钥拒绝和重启仍由原 mandatory smoke 检查。后继领域提交 `a5f201d` 的 Build `41141882783` 亦 completed/success。这不创建正式 Release、不改变旧 run 的失败历史，也不将 CI 容器结果说成本机 Docker 执行。

另对先前 `489bcf2`/Native desktop `41137504523` 下载产物做静态审计：三个 ZIP 再次匹配 GitHub artifact digest，安全路径/重复成员检查通过；六份架构级 LICENSE 均为完整 11,558 字节且 SHA256 匹配，六个桌面可执行文件的静态 Go 元数据均为 Go1.27.3、CGO0、正确 OS/arch、精确 VCS revision 且无 dirty 标记。本机未执行安装器或桌面程序，亦未在 Linux 上打开 DMG/NSIS 内部；安装包内许可检查仍属于对应平台的原 CI，不以 ZIP staging 证明实际安装或 GUI 可用。

## 2026-10-31：Native HTTP 配额测试改用被动观测

Grok Build 候选全仓运行实际失败于 `TestCodexNativeTransportLifetime`：八个慢 body 未达到测试探针期待的配额状态，原失败保留。原探针通过 `service.Call(no-op)` 自身占槽，可能使同时入场的第八个真实请求被拒；受控交错已在未改生产服务的情况下复现同一断言失败，不推断原运行的精确调度或归因为机器负载。

现在仅在测试中被动记录八个真实 body 的首次成功读取；仍验证第九个真实 HTTP 请求返回 429/no-store、Close 取消排空和停止后拒绝新工作。服务容量 8、20 秒期限、原 5 秒测试边界均不变。双入口重复 25 轮通过；将生产容量临时编译为 9 时，两条原第九请求断言均普通失败，恢复后通过，独立验收亦通过。包含该测试修复、Grok 候选、已验收客户端编排及许可数据的固定组合全仓为 6,835 PASS、110 显式 SKIP、47 个测试包、零 FAIL，vet/module verify 通过；跳过不代表消费者或平台资格。旧 logging/Browse 历史失败仍保留其各自未明原因，不由此改绿。

## 2026-10-31：Darwin 密钥核心限定运行验收

`f422bb1` 的实际双 APFS 安全套件与调用隔离合计 51 项通过，实际清理及两个封存 artifact 已经独立核验。旧 `646fe4d` 的控制器前置检查失败保留，不追溯改绿。CLI、Desktop、安装和 Pack 的五项产品失败也仍保留；本结果只授予该提交与原生环境的密钥核心资格。完整身份、测试范围和 artifact 摘要见 [Darwin 密钥核心记录](DARWIN-CLIENT-KEY-CORE.md)。

本记录自 2026-10-28 开发预览阶段起按提交保留验收与失败历史，各节描述对应时间点，不是首版发布或 M2 至 M7 全部完成的声明。最新 MyGo Claude 原生配置交付见末节。权威功能范围见 `REQUIREMENTS.md`；持续进度见 `DEVELOPMENT.md`。

## 2026-10-30：配置事务取消测试的清理锁竞争

Build `40985001579`（源码 `671db64`）实际失败于 Gemini 日志隐私测试内的 `DROP TRIGGER` 清理步骤，二进制与 Docker jobs 因此跳过；原失败保留，不用重跑替代诊断。

在 Go 1.27.3、`CGO_ENABLED=0`、隔离 HOME 下，单核调度使原 Gemini 事务取消测试重复 500 轮全部在清理处失败，1000 个 proto/JSON 子例均观察到 SQLite `SQLITE_BUSY(5)`；原 Codex 同类测试重复 10 轮的 20 个子例也在清理处失败。较高并发度下原测试可以通过，不能以一次绿色证明无竞态。

`database/sql` 的取消回滚可能先将事务标为 done，再完成物理回滚；此时显式 `Rollback` 返回不证明数据库写锁已经释放。测试用于建删 trigger 的独立连接原先没有 busy timeout，可能立即与仍在回滚的连接冲突。修复仅让这两个测试连接通过 file URL 的连接级 DSN 使用 `busy_timeout(10000)`，与 Store 一致；不改生产事务、HTTP 取消、零持久化断言或隐私断言，也不忽略清理错误。

同一单核条件下，候选 Gemini/Codex 事务测试各重复 100 轮通过，完整 Gemini 失败日志隐私测试重复 20 轮通过。候选完整仓库 `go test -mod=readonly ./... -count=1 -timeout=600s` 与 `go vet -mod=readonly ./...` 实际 exit0，保持默认包并行。Build 工作流新增单核定向回归（三轮），并保留原全仓测试，防止仅靠默认调度掩盖回归。以上是本地结果；后继提交的 Actions 状态须按其自身 SHA 核查，不借用旧绿色。

## 独立验收范围

- Linux CLI Pack 订阅、显式同步、跨 Pack 去重与保守卸载；匿名公开 skill 安装复用内容校验，但不隐式加入订阅，不需要 token 或 client.key。
- 服务端 Lists 定时增量/公开来源收录、公开目录与分类、默认私有及取消公开后的匿名拒绝；分类变更不修改 skill 内容 hash。
- MCP 用户级 token 生命周期、权限/有效期校验、取消与关闭行为，以及 Web 管理和一次性显示。后续 `e707dc0` 已接通分类与发布 tools/scopes，详见末尾阶段记录；不能把该增量追溯为早期切片已具备。
- Web 匿名和管理界面的 light/dark/system、zh-CN/en；保留主题/语言切换前的未提交表单。独立代表保留并重放了 OIDC 失败提示后切换语言导致 MCP 草稿丢失的回归，修复后双向通过，且登录会话切换的清理边界未被削弱。
- cc-switch 在后续 `f429da9` 已接通自包含只读 SQLite snapshot 授权上传及显式选择仓库收录；Magpie 同样支持授权上传预览与仓库选择。下节保留早期切片及实际失败历史；这不等于 skill 内容或客户端配置迁移已完成。CLI 和 MyGo 不执行插件。

验收不是仅凭构建结果：公开目录使用实际浏览器与受控或公开归档来源；MCP 管理使用真实嵌入式服务端和浏览器；后端争用/过期/取消及客户端安装另有保留回归测试。截图只在一次性凭据已清除后采集；不保留账号、密钥、数据库、会话导出或含凭据 HAR。

2026-10-28 最终阶段整合审查结论为 Spec PASS / Quality PASS，限本阶段整合接缝读审与冻结输入、既有验收证据核对；新增确认 P1/P2 均为 0，另有三项非阻断文档建议。该审查未重新执行全套测试或浏览器验收，不代表 M2–M7 全量完成、首发验收或远端 CI 成功。

## 可复现检查

### Magpie 上传与仓库选择收录

2026-10-28 本轮独立代表最终结论为 Spec PASS / Quality PASS，确认 P1/P2 为 0。代表在冻结副本独立执行全部 Go tests/vet、Buf lint/生成/breaking 和定向真实 TCP 测试；另以可控 RPC 的真实 React 界面独立验证取消、大小限制、延迟旧账号 401 回填和容量/未知错误停止，最终嵌入式服务上验证真实授权上传、HEAD/head、键盘、偏好与清理。以下另行记录实现者/主线程证据，不混称为代表亲自重复全部交叉构建或真实 GitHub 收录。

- Go 1.27.3、`CGO_ENABLED=0`：编译可运行的行为 red 曾观察到缺失路由、旧 4096 字节上限和慢连接拒绝不及时；实现后 HTTP/App 上传预览、准入、大小/格式/压缩、重鉴权、跨 owner、HEAD/head、脱敏、真实 TCP deadline 与 Close 排空测试通过。
- 既有 Collect 的选中仓库、重复/并发、部分失败、缓存损坏、取消/响应丢失收敛测试直接 green，作为回归证据，不伪造它们的 red。全量 `go test -mod=readonly ./... -count=1 -timeout=240s`、`go vet -mod=readonly ./...`、`go mod tidy -diff`、`go mod verify` 均通过。
- Web `npm run typecheck --prefix web`、build、生产依赖 audit（0 vulnerabilities）通过。额外从暂存区完整导出，执行干净 `npm ci`/build、与本地 dist 逐字比较、全部 Go tests/vet 和两份 Buf 模板生成比较均通过。
- `buf lint`、对本阶段父提交的 breaking 检查、Go/Connect/TS 逐字生成一致通过；CLI 依赖树无服务端、moejs 或 importer。Linux/macOS/Windows × amd64/arm64 的 CLI 与服务端共 12 项 `CGO_ENABLED=0` 交叉构建通过，不代表这些平台的原生运行验收。
- `protoc-gen-es` 生成的 TS 与既有生成文件一样带末尾空行，保持生成原样以通过逐字比较。非生成物使用默认 whitespace check；生成物仅豁免 `blank-at-eof`，其他 whitespace 检查不变。
- 最终生产 bundle 重新嵌入本地服务端后，通过具名隔离浏览器会话复验：选择文件 0 请求；显式预览 2 候选、默认 0 选择/0 Collect；逆序勾选后切换语言/主题保留选择。真实缺失仓库失败后继续收录 `obra/superpowers`，两个请求串行、库仅 1 条，详情 15 个技能均有 SHA-256；重试后库仍 1 条。
- 最终 bundle 在 390px 无文档级横向溢出；三个候选中已有记录仍验证缓存，第二项在途取消后为成功/待确认/未尝试，只有 2 次新 Collect。退出清候选和待确认状态，新 owner 资料库为空、偏好保留；localStorage 只有 theme/locale，sessionStorage 为空，配置秘密哨兵未显示。
- 浏览器账号使用页面内生成的临时随机密码，不保留凭据、账号库、截图、accessibility snapshot 或 HAR。源码中 synthetic fixture 不是用户真实导出；浏览器证据不能替代外部 OIDC/GitHub 授权验收、MyGo 原生 GUI 打包或容器运行。

此切片只新增 Magpie JSON 的仓库候选上传及选择收录，F6/M6、首版均未完成。远端 CI 需按实际推送 SHA 单独核查，不从本地测试推断成功。

交付跟进：源码提交 `bf556fd` 已通过 SSH 推送且远端 SHA 读回一致。Actions `40667951091` 首次尝试在既有 `TestRepresentativeDistributionConnectDeadlineUnblocksSocketWrite` 第 170 行失败，观察到 `context.Canceled` 而非断言要求的 `DeadlineExceeded`；本轮 Magpie/App 测试通过，后续二进制/OCI 因依赖测试失败而跳过。当前提交定向重复 40 次也观察到 1 次同样失败；该分发实现和测试本轮均未修改。保留失败证据，仅请求一次 failed-jobs 重跑，不删除、skip、放宽断言或修改无关生产路径；重跑成功也不等于这个既有不稳定点已修复。

独立代表随后在未修改的父提交 `dda1dd9` 导出副本重跑：40 次均通过，额外 100 次有 95 次通过、5 次同位置失败；这确认既有基线即可复现，次数不是 CI 失败概率估计。相关分发实现/测试、packs 及依赖与本轮源码提交逐字未变。代码因果分析表明 socket 写 deadline 与父 context 计时器没有保证先后顺序；写失败返回后的下载清理可能先取消子 context，使 Err 为 Canceled，而原断言要求唯一的 DeadlineExceeded。未插桩捕获该次 CI 内部事件，也未修复这项无关竞态。保留为后续独立事项。

同一源码 SHA 的 `40667951091` attempt 2 已实际 `completed/success`：test、六组 CLI/服务端二进制、amd64/arm64 OCI 构建全部通过，独立代表也单独查询确认。没有重写源码或断言来得到此结果；容器实际运行、非 Linux 实机与 MyGo GUI 仍未验收。本段后续纯文档提交的 CI 应按其自身 SHA 查询，不能借用源码提交的结果。

### 全项目命令

工具链：Go 1.27.3、Node.js 26、Buf 1.75.0，所有 Go 命令设置 `CGO_ENABLED=0`。从暂存区导出到独立临时目录验证，避免未暂存工作掩盖缺失文件；不得复制运行数据或密钥。

```sh
npm ci --prefix web
npm run build --prefix web
npm audit --omit=dev --prefix web
CGO_ENABLED=0 go test -mod=readonly ./... -count=1 -timeout=240s
CGO_ENABLED=0 go vet -mod=readonly ./...
CGO_ENABLED=0 go mod tidy -diff
CGO_ENABLED=0 go mod verify
buf lint
generated="$(mktemp -d)"
buf generate -o "$generated"
diff -ru gen "$generated/gen"
buf generate --template buf.gen.web.yaml -o "$generated"
diff -ru web/src/gen "$generated/web/src/gen"
```

另对 `./cmd/skillhub`、`./cmd/skillhub-server` 执行 `CGO_ENABLED=0 GOOS=… GOARCH=… go build -trimpath`，目标为 Linux/macOS/Windows × amd64/arm64；用 `go list -deps ./cmd/skillhub` 检查客户端不依赖服务端、moejs 或服务端 MCP SDK。Web 必须先构建，才能验证最终嵌入资源。

2026-10-28 从暂存源码独立导出后执行上述检查：npm 干净安装、Web 类型/生产构建、生产依赖审计（0 漏洞）、全部 Go tests/vet、模块 tidy diff/verify、Buf lint 和生成逐字比对、客户端依赖隔离及十二项交叉编译均退出 0。该结果不包含远端 Actions，也不证明依赖没有未知漏洞。

## 首轮真实 CI 失败与 fixture 修正

2026-10-28，提交 `91bcb261` 的 Actions 运行 `40657038713` 首轮真实 CI 失败：`TestPrepareUninstallRejectsChangedInstallation/symlink` 报 `uninstall_test.go:130: accepted symlink change`。保留日志位于 `.superpowers/sdd/2026-10-28-client-subscriptions/ci-first-failure.log:68`；日志没有记录替换前后的 inode，不能据此声称已实测 CI 发生 inode 复用。

原 fixture unlink 旧 symlink 后创建同 target 的新 symlink，而生产辅助身份 `linux-dev-ino-v1` 仅包含设备号和 inode；安装完成已关闭旧 symlink 的句柄。旧对象释放后 inode 可复用，因此该 fixture 不能保证身份一定改变。源码成功路径和 CI 症状高度支持这一归因，但 CI 上的具体复用仍属推断，不是新增的实测结果。

修正仅将旧 symlink rename 到保留路径，再创建同 target 的新 symlink，并用 `Lstat` / `os.SameFile` 显式断言前后为不同对象；仍要求 Prepare 拒绝且不产生 backup 或 pending operation，没有 skip、重试或生产身份算法变更。此次本机 Btrfs / Go 1.27.3 / `CGO_ENABLED=0` 验证：原 symlink case 50 轮通过，修正后的完整 changed-installation 测试 100 轮通过，`./internal/install` tests（`-count=1`）和 vet 均退出 0。日志在同目录 `ci-fixture-fix-evidence/`，归因与命令见 `ci-fixture-fix-report.md`。本次验证使用当前工作树，没有重新暂存导出，也没有重跑全仓或远端 CI。

这只消除了测试对 inode 永不复用的依赖，**没有完成 inode 复用下的历史连续归属保障**。`INSTALL-RECOVERY.md` 和原 `uninstall-review.md` 的 trusted, stable-path model 仍适用；持久化 dev/ino、generation 和 hash 不证明历史连续所有权。本次修正不扩大删除授权，不宣称能识别所有已释放后复用 inode 的替换，也不代表远端 CI 已恢复成功。

### 后续整合与实际 CI

修正经独立代表复核，三个变更分支各 100 轮与相邻成功/故障准备各 10 轮通过；主线程另从 index 加精确二文件补丁独立导出，全仓 Go tests/vet 退出 0。提交 `0924a5b6636982463ef6be7912e379a6491a1447` 经 SSH 非强推至 `main` 并以 SSH 读回一致。对应 Actions `40659619868` 最终 `completed / success`：test、六组 CLI/服务端二进制任务和 amd64/arm64 OCI 构建任务全部通过。该结果补充前述修正时尚未重跑 CI 的时间点，不改写首轮失败或 inode 根因仍属推断的事实。

首轮 CI 实际上传的 Web artifact 已下载，与本机独立导出的生产 Web 构建逐字一致。二进制任务中的客户端是 CLI，不是 MyGo 原生 GUI；OCI 构建成功也不是容器运行验收或镜像仓库发布。所有结果均对应上述明确提交，不预先承诺后续提交的 CI 状态。

## 构建与未覆盖项

`.github/workflows/build.yml` 对 push/PR 执行检查，生成 CLI/服务端六平台二进制、Web 和双架构 OCI 构建产物。检查具体提交的 Actions 结果，不能把本机测试视为远端 CI 成功。构建产物不等于 GitHub Release；MyGo 原生客户端及其打包仍待实现。

本机没有 Docker，尚无本机容器运行验收；交叉编译不证明非 Linux 原生运行，Windows 服务端、WSL copy、断电恢复、外部真实 GitHub/OIDC、恶意插件操作系统级隔离仍不在已验证范围。无 cgo 测试也不替代 race detector。私有源、源快照自动替换、持续客户端自动同步、S3、完整导入、MyGo GUI 和首发验收仍待后续阶段。

## 2026-10-28 仓库候选导入与 Cognate 改名

导入切片提交 `f429da936d5bd317c3f8db6a22c1d6f42a3457da`，对应独立签收的30文件 tree `3684568b4dee166d8ab70832650dda89196c07e7`，Spec/Quality PASS、未关闭P1/P2为0；SSH推送至已更名的仓库并读回一致。Actions `40681924445` 首次 completed/success，测试、六组CLI/服务端二进制及双架构OCI全部成功。该切片仅为服务端cc-switch一致SQLite上传预览、Magpie CLI消费及Web显式选择/逐仓收录，不是完整配置迁移或M6完成。独立发现的Deserialize重建pager覆盖cache_size问题保留red，再在Deserialize后、任何查询前恢复策略并独立复验green。写入阻塞、尾flush、HTTP/2和同连接复用的增量验证及原始失败证据均保留。

改名提交 `c96b4ae3baae03ecf36bd629eee88748c4ed7cfb` 精确匹配独立签收 tree `baa17d9e4514e2c83acc1bd78608eb75e2c0613c`，基线为前述导入提交。主线程从干净index导出，执行npm ci/typecheck/build/audit（0漏洞）、Go1.27.3/CGO0全量tests/vet/tidy-diff/module verify、Buf lint和Go/TS生成逐字比对，以及CLI+server六目标共12次CGO0交叉构建，全部退出0。独立代表另行导出并核对616个tracked blob，复验Spec/Quality PASS、P1/P2为0，没有纳入GUI、MCP、agent插件、配置资产或旧deadline fixture WIP。

Go module及go_package按用户要求迁移，原始Buf FILE检查明确报7项go_package source API breaking，未隐藏为原样兼容。CI仅在临时历史基准中规范化精确的旧Go module前缀，仍运行全部FILE规则；normalized检查通过，另一个go_package与wire package变更负例仍拒绝。Connect namespace、持久化目录、加密与hash域等按 `COGNATE-RENAME.md` 有意保持旧标识。

独立代表实际运行旧、新服务端，同DB/key/端口重启后旧session、账号、repository/skill/hash及公开metadata均可读；fixture使用旧版Store/ArchiveCache API明确构造，不冒充真实GitHub收录，首次504及缺cache的fixture失败亦记录。独立新binary Chromium验证Cognate标题、匿名安装命令、旧非敏感偏好键与中文/English切换，以及实际 `skillhub.*` RPC。没有复用旧服务页面充当新binary验收，没有输出真实密钥或token。

改名提交经SSH推送并读回一致；对应Actions `40685515081` 首次 completed/success，测试、六组CLI/服务端二进制及双架构OCI全部成功。仓库现为 `ca-x/cognate`，尚无本阶段tag/Release，OCI成功仍不等于容器实跑或镜像发布。完整F1–F12/M1–M7首发门禁继续有效。

用户提供的cc-switch本地checkout只读对照基线为 `6d6b7162f122ac0ce1356a14688c8c4f712db3c8`，交互矩阵见 `CCSWITCH-CONFIGURATION-MATRIX.md`。源码注册14种工具，独占/共存/特殊默认、MCP、prompt和OpenClaw配置必须分别验证；没有启动upstream或读取用户真实配置。是否首发纳入代理/协议转换/failover/模型路由已异步询问用户，不能静默决定或宣称完成；其余已明确配置开发继续进行。

## MCP、原生桌面与配置存储增量

2026-10-28，以下阶段均 SSH 推送并读回 main SHA，提交 tree 与各自独立报告完全相符，不含当时其它 WIP：

| 阶段 | Commit / 独立签收 tree | 已证明范围 |
| --- | --- | --- |
| MCP 分类与发布 | `e707dc0d22150576f89cc7233cc400a4e8d2cff0` / `a7e9b2b0bb858a0335ea4d45367ca1f4098266f9` | 七 scopes、17 个 typed tools、全部十个业务写工具事务前/提交前复核权限、旧 token 不扩权；独立 SDK、真实旧凭据/新 binary 浏览器、失权负例通过 |
| MyGo 桌面首片 | `611f2a069acd5c6dbed40a86dd99a1636b3aa7be` / `6f189c637734959ac2245dca7db6baa83c1bd059` | 真实 Linux 原生窗口、headless 原生组件+HTTP/共用内核流程、六 CGO0 构建、Linux 包内容；不等于跨系统安装或完整 M7 |
| 两个 HTTP fixtures | `4c668b28858fd0bfc241837276a461ae46cc5d47` / `e1531dd3e5ce7a34530f5cd9901bafc9cb032767` | request body 每次重建、分发 socket timeout 与连接错误传播排序；独立确定性耗尽 body red/green、默认/单核压力与缺失 WriteDeadline 负例有效；没有生产代码修改 |
| 配置资产内部存储 | `94668b7ad70949d50808fb16a8386c521b30300d` / `2e3029ecdce370bfb4d6e3d254d3811b867a664d` | ent+独立 HKDF/AES-GCM、owner/AAD/修订绑定、metadata/payload 隔离、CAS/容量竞争、单 SELECT 同版本、旧 schema 迁移；独立深审与整合测试通过，不等于配置产品功能 |

Native desktop Actions `40691777676` 精确绑定 `611f2a0`，首次 completed/success：verify、Linux/Windows/macOS runner 的双架构包装全部 success，publish=false 按设计 skipped。Windows 两架构 NSIS Setup、macOS 两架构 DMG、Linux 两架构 deb/tar 均有实际构建检查，构建日志包含 Linux 原生实窗捕获。这是 unsigned/ad-hoc artifact，不是 Developer ID/公证、Authenticode、实际跨系统安装或正式 Release。

不能将该桌面 workflow 的绿色替代普通 Build：MCP Actions `40689163056` 在 `mcpserver/lifecycle_test.go` 复用 body 上失败；修复后普通 Build `40691768850`、`40692998730`、配置存储提交对应 `40694979772` 又在既存 `catalog_lifecycle_test.go:238` 的 DeadlineExceeded/Canceled 排序上失败。原始远程失败日志保留，未靠 rerun 隐藏；catalog fixture 的定向修复另行冻结独立验收。配置存储的 clean-export 全仓测试/vet/Web build/Buf 已通过，但不将本机通过记成这些远端 run 成功。

后续 catalog fixture 经独立原始失败复现、固定版本100轮、close/disconnect对照与缺失生产WriteDeadline负例验收后，按 tree `af4266c5ee05f96dbc4109c98089df3e5c1010ad` 提交并 SSH 推送 `fb45608bfe951497591e081b35352f88d20f842a`。对应普通 Build Actions `40698837803` completed/success，实际 test、六组 CLI/server binaries 和 amd64/arm64 两 OCI jobs 全部 success。该真实后续绿色不覆盖此前失败历史，也不把 OCI artifact 当运行中的容器。

整合代表另外确认了既存 MCP 非 UTC 时区授权问题：SQLite 时间文本带不同 offset 时不能依赖 SQL 字符串排序表达真实时间。该问题在旧基线同样存在，不由配置存储引入；最终独立修复记录见下节，不借存储签收隐式关闭。MCP 发布归档缓存和审计 append 仍不与领域写事务构成跨资源原子提交。

完整首发继续未达成：typed domain/API/编辑器、完整十四 agent 与导入、配置选择/Pack 附加、原生受控应用/恢复、源自动替换、持续客户端同步、S3、私有源和真实外部授权/跨平台运行等仍按原需求逐项推进，不以阶段 PASS 缩减范围。

## 时区、moejs 目标适配与 Typed Configuration

以下三个阶段均从精确冻结 tree 导出验证、由非作者独立验收，再单独提交并通过 SSH 推送与读回。它们不包含当时未冻结的 Web 编辑器或其它 WIP。

| 阶段 | Commit / 签收 tree | 已证明范围 |
| --- | --- | --- |
| MCP 时间兼容 | `f3cae917cece5c1367131d487bd75f4b39fe76d7` / `c38d470fd61e51a18f47c51531fda08f3f791e92` | 查询 credential/owner 后 Go instant 校验；新 credential/audit 时间 UTC；启动锁内有界兼容旧 numeric zone/monotonic 文本，保留授权与审计身份 |
| 服务端目标适配器 | `538c466b8e499ad0c656a192131c2dbe29e4344a` / `02d6b4d5e063e241247e77f46309b6f20c8fa4b8` | 真实 moejs registry、精确版本/digest/capability、Claude 四键 additive merge；未选旧 env/secret 留 Go 宿主；Codex/Gemini 仅 metadata，不是 provider switching |
| Claude typed domain/API | `8a7eebe960a1270ac1ef24b98e0c77eb08e06c94` / `01f694d9cb0e91ca309914c5d0b47a240b1dc80d` | owner/CAS CRUD、同版编辑读取、keep/replace/clear、不带秘密复制、严格有界 Connect、真实日志/响应隐私；未知 agent 仅合法 metadata 浏览/删除，保存不等于应用 |

MCP 旧 lexical 比较与真实 Kathmandu numeric zone 旧库分别保留 red。仅 credential 修复的旧候选没有被当作最终签收：audit 同样存在旧行无法读取及本地时区新写不可读，最终七文件候选把两者一并修复。代表用真正旧生产 writer、真实进程 TZ、MCP SDK endpoint/restart、审计 append/读取和撤销复验；错误 key、后批坏时间、幂等、owner/scope、transaction/commit 时间 gate 另有独立负例。每批128条、每时间输入256字节，坏批回滚，无法恢复的值固定错误停止启动。升级前停止旧 writer，不承诺新旧 writer 长期混用。

Typed API 首个默认打印合同 P2 已在当时所测直接格式范围独立关闭，但不是给 generated DTO 或任意 domain wrapper 新增通用日志安全承诺：只允许明确构造的无秘密状态 DTO 进入 logger，原始 Protobuf/Connect request 的 String/JSON/slog 能包含 replacement，严禁用于日志/持久化/error details。`debug_redact` 注解保留，当前 Go runtime 不因此自动安全。真实 proto/JSON 成功、失败、超限请求捕获标准 log/slog/HTTP ErrorLog 和响应；三项临时 mutation（stdlog、slog、错误回显）均触发实际隐私断言 red，去除后 green。窄 AST gate 不是全程序污点分析。2026-10-29 对五种既有 wrapper 的补充 probe 发现非字符串动词/私有容器边界，未证实现有生产 sink；精确限制与后续开发约束见 `SECURITY-LOGGING.md`，不将历史有限 PASS 扩大为所有格式安全。

主线程对上述各冻结 tree 重新执行 clean-export 全 Go tests/vet、Web build；最终 API 另有 domain count20、HTTP/App count10、Buf Go/TS 双模板完整生成目录 parity、六系统/架构组合 CLI/server 共十二次无 cgo 构建。独立代表执行自己的 package/protocol/日志负例，不借作者通过替代验收。完整日志索引在 `.superpowers/sdd/2026-10-28-release/`，关键行为合同已随本文件和各 spec 提交，临时合成数据库/凭据不提交。

Build Actions `40709123040` 精确绑定 MCP 提交、`40709433501` 精确绑定 adapter 提交、`40709705439` 精确绑定 typed API 提交，均 completed/success；逐 job 核对 test、六组 binaries、amd64/arm64 两组 OCI 构建全绿。前述历史失败继续保留。没有新 tag/Release；OCI 构建不是容器运行，七键 provider switch、秘密原生消费、选择/Pack 附件与真实本机 apply/restore 尚未由本节交付。

## 容器实跑与七键声明式变更 checkpoint

2026-10-29 续接核对：下列两个阶段已独立验收，按精确 tree 提交并 SSH 推送；没有包含仍在验收的 Web 编辑器、桌面身份门禁或本地 JSON codec。

| 阶段 | Commit / 签收 tree | 已证明范围 |
| --- | --- | --- |
| 双架构容器实跑 | `9cf109edcbcebe1a342db72391b891c096fe3bf8` / `fdd10f58d4cd291d84411e34f04bd8ab49ea5f4d` | CI 实际加载镜像，以 UID 10001、只读根目录、禁网络及私有持久卷启动；health、内嵌 Web、CLI help、SIGTERM exit 0、错误主密钥 exit 1 且精确错误、原密钥重启通过 |
| Claude 七键声明式变更 | `059bf928afbb73e869d8eed8112f667b35555f60` / `7c96c64b208c36f5cf704c04a8e42ce2849dbe2a` | 服务端 moejs `claude.provider-switch.v1`，七个受管 env slot 的 set/remove；凭据只使用 typed 引用，真实秘密和旧 env 不进入 JS；Go 独立验证输出，无未知能力 fallback |

Build Actions `40719035614` 精确绑定容器提交，`40722843461` 精确绑定七键提交，均 completed/success，test、六组 binaries、两架构 docker jobs 全绿。容器提交两 job 的真实 smoke step 均 success，并输出固定完成标记；这不是仅构建 OCI 或命令替身的结果。arm64 在 QEMU 环境，不宣称原生 ARM 硬件运行。本机没有 Docker/Podman 的历史事实不变。

七键能力保留旧四键 additive renderer，新增能力明确区分两种认证 slot 与五个非认证 slot，空值/非法联合/额外成员均拒绝。独立 reviewer 复验真实 moejs 和输出篡改负例；主线程对最终 clean export 执行 Web build、全 Go tests/vet、runtime 重复测试、模块校验与客户端依赖边界检查。它仅产生声明式 patch，不提供秘密传输、设备授权或本地文件应用，不能标记完整 provider 切换体验已完成。

当前 Web 编辑器曾被独立验收拒签：保存成功响应与离开确认并发可能留下阻塞对话框；本地 JSON codec 也曾因非常规 fmt 动词泄漏秘密的可逆字节被拒签。修复与新树复验另行记录，不能用原树的全量测试通过覆盖这些行为缺陷。没有 tag/Release，完整首发目标仍未完成。

## Claude 私有资产 Web 编辑首片

2026-10-29，按 tree `569372e78883a36f3ea39b76df93ae22a41ef467` 独立整合验收，提交并 SSH 推送 `f659706ddb0c81cc0ac4e20835dd14b2039c03d2`，远端 main 读回一致。八文件只含六个 Web 消费文件及 embed 路由/测试，不带当时的本地 codec 或其它 WIP。

- `/configuration` 提供当前账号 Claude provider 资产列表、新建、typed 编辑、非秘密复制、revision CAS 删除；secret 为 keep/replace/clear，未知合法 agent/schema 仅 metadata 浏览/按版本删除。保存不切换本机 provider。
- dirty 草稿有关闭/导航放弃确认；主题切换保留表单，locale 切换清除新凭据并取消在途操作，账号/会话切换直接清理敏感状态。凭据不进入缓存、URL 或浏览器持久化。GET/HEAD 直达与相邻路径 404/CSP 回归通过。
- 原保存成功与导航 resolver 的竞态 P2 在最终代码中关闭：不再使用提交时捕获的旧 resolver；独立浏览器 held-response Create/Update/Copy、同时响应、继续/放弃、CAS 失败、locale/auth/unmount 等回归得到 86 项 boolean 观察全真，另有真实未知 schema Store、刷新和隐私检查。旧拒签记录保留。
- 独立 reviewer 对最终 tree 核全部 695 blobs/mode，八文件与已实测修复候选相同；fresh 构建的完整 Web dist 与 server executable 也逐字相同，将此前真实浏览器证据绑定最终运行时，而不是声称又跑了一次新 origin。最终 fresh Web/server/HTTP configuration seams 通过；主线程另跑 clean-export Web build、全 Go tests/vet、相关包 count10、模块校验全部通过。

对应 Build Actions `40727877287` 精确绑定该 commit，completed/success；实际 test、六组 binaries、两架构 docker jobs 全绿，不由本地通过推断。独立最终报告为 `configuration-ui-final-review.md`，旧竞态拒签与两次修复复验均保留。该首片不是十四类 agent 完整编辑器、来源配置导入、选择/Pack 附件或 CLI/MyGo 原生 apply/restore，也不关闭本地 JSON codec 的独立 finding。用户操作说明见 `CONFIGURATION.md`。

## 桌面打包身份门禁结果

桌面修复 `39013dacfc01b2b50d6633cee3b2c324f09bf945` 的 Native Actions `40726579335` 已 completed/success，verify 与三系统双架构实际包装全部成功，publish=false/skipped。六 payload 在各 runner 的真实 metadata 检查全部验证 Go/CGO/目标架构、精确源码 revision 与 clean 标记；旧 Windows dirty 的因果复现和拒绝历史继续保留。普通 Build `40726569509` 也 completed/success。已核实际 job 结论与六条执行输出，不将后续 artifact 下载、安装或跨系统运行自动算作完成；完整限制见 `DESKTOP.md`。

2026-10-29 下载独立复核：该 run 三个 unsigned ZIP 的大小/摘要与实时 artifact API 匹配，28 个条目与落地 bytes 一致；六个 payload 的 Go 1.27.3、CGO0、目标架构、精确 `39013dac` 与 modified=false 均通过，八个包装存在且非空，14 个 metadata 负控被拒绝。没有执行或安装 payload、挂载 DMG、验证可信签名或证明包装内部同一性；下载摘要和 embedded VCS 信息不等于源码到二进制的密码学证明。三个实际 ZIP SHA-256 及限制见 `DESKTOP.md`，独立报告为 `desktop-downloaded-payload-review.md`。此项不签收 native auth/state、备份或完整首发。

## 本地 JSON codec 与 Native schema

2026-10-29 续接，两个切片分别独立验收、提交；它们不包含原生秘密消费、文件发布、备份恢复或完整 F12。

| 阶段 | Commit / 签收 tree | 已证明范围 |
| --- | --- | --- |
| Claude 纯内存 JSON codec | `d856f5d4216b9cad21c1275e4aa182fef253ec03` / `b0a33ec4ee651ddb87abb240b22001de84f5628f` | 七键 patch 与精确 credential slot 独立校验；严格 jsontext、有界输入/结果；局部修改保留未知 token/大整数；无磁盘、HTTP、CAS 或已应用状态 |
| Native schema / Web 生成隔离 | `356a2d4` / `df3c8797cc2bed8cae85cf6270d22139ed14c758` | 独立 native-purpose proto 包与 Go 生成物；Web template 排除该目录，保留所有普通 TS 和完整 parity；CI 明确检查 Go 正向与 Web 负向，没有注册服务端 endpoint |

Codec 两条隐私 P2 均在最终 tree 独立关闭：初版缺 Formatter，非字符串动词可输出可逆 bytes；仅加 Formatter 后，外层私有字段可绕过方法派发，实际 fmt/slog 仍泄漏。最终 wrapper 采用双层不透明间接表示并保留值接收者 Formatter，不批准“只允许公开容器”的豁免。原 30 项隐私 probe 100 轮通过，新增 23 动词、私有嵌套容器与真实 slog 双 handler 通过；退化为直接 bytes 的原 10 项、退化为单层指针的 `%t` 均重新 red。任意主动 unsafe/反射取出秘密或显式 CopyBytes 不属于默认日志保护承诺。

Codec 独立完整 scoped 100 轮、fresh fuzz/vet/deps 通过；主线程最终 clean export Web build、全 Go tests/vet、codec 100 轮、客户端禁止 server/moejs 依赖检查与模块校验通过。对应 Build `40733265755` 精确绑定 codec commit，completed/success，test、六 binaries、两 docker jobs 全绿。旧拒签记录没有被删除或以原树全量通过覆盖。

Native schema 的独立 review 核 23 messages、6 enums、6 RPC 的全部 token 与已签协议一致，包含 reserved/options；实际 Buf 1.75.0 全 Go/TS parity、breaking against `a168eb5`、lint/format、CGO0 generated 包 compile/vet、Web build 与 actionlint 通过。未过滤模板真实产生 native TS 并使 boundary/parity red；过滤后普通 auth/configuration TS 保留而 native TS 缺席。CI exact gate 的 11 项负例、删除新 tag 的 breaking 负例均有效。静态 source literal gate 只是开发回归检查，不是防恶意同权限客户端的身份认证或 attestation。

主线程对 schema 最终 clean export 另跑全仓 Go tests/vet、Web build、完整双模板生成 parity 与兼容性、模块校验全部通过。原生 proof、父/子 session 事务、一次性 grant、敏感 transport、平台安全 publisher 和真实 apply/restore 仍分别待验收；schema 可编译不能等同 native capability 可用。后续 CI 结果另按精确提交核对，不从本地通过推断。

## Native Pure-Proof Checkpoint

2026-10-29，纯 proof 切片按 exact tree `f30019c466d20baedbba207a3cd347dd51f77b0f` 独立 Spec/Quality PASS，四文件提交 `b5e2e0c426761d219a0068048104b676e1f2ed49` 已 SSH 推送并读回。五 purpose 的 canonical transcript、Preparation/Apply/Patch hashes、Ed25519 sign/verify、safe patch 验证和 strict raw protobuf decoder 已实现。decoder 在 merge 前拒绝重复 singular/oneof 与 nested unknown，失败不改变目标；12-type allowlist 明确排除三种秘密响应。Transcript/Signature 使用双层 opaque 表示；不承诺原始 DTO、显式 bytes/hash 或私钥适合日志。

原 tree `69c6c4d8c4395ab6ee3ec501dee59a0f6b403759` 虽主全量通过，独立发现 auto-policy ID 错限任意 32 bytes：合法短 ASCII 被拒绝、32 个 NUL 被接受。该 P2 没有通过改协议豁免；最终复用已签 1–64 bytes ASCII opaque ID 规则，typed/raw 原反例 100 轮通过，独立字符边界通过，退回旧实现的 overlay 重新 red。原拒签、16 个独立 known-answer vectors、strict-wire/格式化隐私及四项有效 mutation 证据保留于 `native-proof-final-review.md`；作者六个 Python/OpenSSL fixture 的 parity 不替代独立 oracle。

最终 clean export 的 Web build、Buf lint、完整 Go/Web 双模板 parity、全仓 Go tests/vet、proof 100 轮与模块校验通过，均 Go 1.27.3 / CGO0；没有运行需要 cgo 的 race detector。Build `40745938862` 已登记但本 checkpoint 未核完成，不声明远端 CI 通过。本片无 HTTP/磁盘/秘密消费，不证明 owner MAC、重放/授权、数据库时间与撤销、grant、客户端发布或完整配置应用；后续 native state/auth 及 watch WIP 不在本次签收内。

## Selection Read And Initial Lists Publication Checkpoint

2026-10-29 后续交付，以下两片已分别通过非作者独立 Spec/Quality 验收（开放 P1/P2 为 0）、提交、SSH 推送和远端 SHA 读回。本节补充后续证据，不追认早期 checkpoint 或其未核 CI 已经通过。

| 切片 | 精确提交 / Build | 已证明范围 |
| --- | --- | --- |
| C0-S 当前选择读取 | `04ad076529b032082dfa3b8ef1d36d601fba67a1` / `40832542043` completed/success | 每次在真实安装锁内重新认证已有 root 与完整 inventory；返回三个 revision、AutoApply、DeviceID；验证后不存在才 absent，取消/释放/损坏错误返回全零；重启、Restore 后暂停与跨进程新鲜读取有行为验证 |
| B1c 首次 Lists 内容发布 | `34c22a1070e3bdc907d177f89e1bdc426b342e58` / `40833359423` completed/success | 共享物理维护配对验证；首次 cache.Put 到 CollectListRepository 真实事务完成和清理的外层 writer scope，传播 leasedCtx；网络准备在外；真实子进程、Close/Quiesce、提交回执丢失与失权回归有证据 |

C0-S 独立 reviewer 额外验证认证全局库存后才认 absent、root 移除/恢复、真实 pending、uint64 极值；五类独立编译后行为退化均产生断言 RED，恢复精确源码 GREEN。B1c 独立 reviewer 在真实归档发布/清理与 COMMIT 尾部设屏障，以第二 Store readback 验证真实提交、Close/Exclusive 等待及过期 leasedCtx；去外 scope、丢 leasedCtx、重置 helper context 的三项负控各三次真实断言 RED。编译失败、panic、整体超时与父进程空 helper 不算行为证明。

组合 tree `b25f02bb46342d445179940201984ff2c37869d6` 恰为基线 `de2886e` 加 C0-S 两路径与 B1c 五路径，独立机械 ratify 逐项 blob/mode 及完整导出一致。主线程对该不可变 clean export 执行 npm ci/build、全仓 Go tests/vet、go mod verify、Buf lint、Go/Web 双模板生成逐字比较，以及 CLI/服务端/MyGo 的 Linux amd64、Windows amd64、Darwin arm64 九项 CGO0 编译，全部 exit0。交叉编译不代表这些平台的实际安装或恢复验收；主线程全量不冒称独立 reviewer 重跑全仓。

这两片依赖已交付的 Linux/Claude 本地加密 journal、备份、确认/CAS 发布及恢复内核（`0839b62`），但仍不是 CLI/MyGo 配置应用的完整产品闭环。CurrentSelection 返回的快照不是授权、reservation 或防恶意历史回放证明；网络撤销与本地落盘不是跨机器原子事务。B1c 也不是 App 已启用 managed 模式、全局备份协调器或 S3 恢复完成。

显式客户端密钥准入的 capability map、core spec 与 plan 已在 `d40503e2dde3c0874a3e417904c3cc302fbc78c4` 提交推送，Build `40834848617` completed/success；这是设计文档交付，不是 key 初始化实现验收。旧 CLI/native 自动创建入口关闭、真实人类逐槽批准与安全 TTY、MyGo 配置 UI、自动/Pack 附件、其它 agent/平台、完整 F12 和首个正式 Release 仍分别待完成；不能因以上 CI 全绿勾掉这些门禁。

## Explicit Client-Key Core And Contention Checkpoint

2026-10-29 后续交付；下列四项均按独立验收的精确写集提交、SSH 推送并读回。此节更新后续事实，不把上一节当时尚未实现的功能追认为已完成。

| 阶段 | 精确提交 / Build | 范围 |
| --- | --- | --- |
| 配置争用测试 | `bcbed01477120e6e46e97743b2f09bba8015e583` / `40842683164` completed/success | 保留真实并发下限次重试的 Conflict/Unavailable 分类，增加 KEEP-first、Replace-first 两个同版本快照后真实提交顺序；确定性失者必须 Conflict，不能重读重试或重合并秘密；不改生产逻辑 |
| Codex 声明设计 | `a8a6ad8cece9744fa2bac2fa6c5aca375986ea2e` / `40843367375` completed/success | capability map、typed 声明 spec 与 plan；独立设计审全部 finding 已闭；不是 Codex provider 实现或本机安装 |
| 显式客户端密钥核心 | `5637945e0e8a8921cf720dc0ea5288adaa2a8d81` / `40843635870` completed/success | Linux 的 LoadExisting/Initialize；只有显式初始化生成，先持久化 key 与必要父目录再持久化严格 marker，retry 重做同步；合法 legacy key 原样登记，缺 key 且有 marker/state 残留拒绝；尚未接入旧 CLI/MyGo 入口 |
| 初始 Lists 子进程预算 | `ef004f2fbfc9c7fd81daf3525c8af4d9866b2c38` / `40844151112` 本节仅观察 in_progress | 预期成功的完整 SQLite 初始化独立限 3s；预期被维护锁阻塞仍限 250ms，且要求 nil Store、DeadlineExceeded 与 maintenance.ErrLock；不改生产锁等待语义 |

密钥核心独立验收包含真实双向 Session/初始化锁争用、严格 marker 与残留、legacy 原 key、取消/释放错误 withholding、真实 fsync/父目录/解锁 EIO，以及八项编译后行为负控和精确源码恢复。故障注入不是物理断电证明；部分 lost-receipt 模拟与真实资源调用失败分别标注。非 Linux 仅编译，返回 unsupported，不能以九项构建宣称这些平台可初始化或安装。

两个测试修复也分别经过非作者完整 Spec/Quality 验收。配置测试有真实 SQLite COMMIT 后屏障、取消及四项编译后负控；scheduler 使用真实受控慢读取和物理锁验证，回退成功预算、弱化锁证明均产生断言 RED，恢复 GREEN。原 `dff2546` 的 Build `40837382467` 确实失败，其日志与调查保留；没有通过重新运行原提交抹除失败，也不宣称已测得远端当时的具体物理延迟。

主线程对六个代码路径的组合 tree `3b2ea6c4bf506d0f493940a9bbdcc9a909bc5c78` 做不可变 clean export，npm ci/build、全仓 Go tests/vet、模块校验、Buf lint/Go+Web 双模板 parity 和 CLI/服务端/MyGo 三目标平台九项 CGO0 编译均 exit0。最终 `ef004f2` tree `3a2f40063353c4716308e6fa3d5503f5645100d0` 相对该已测试树只增加上述独立设计验收的三个 Codex 文档，六代码路径 blob/mode 不变；密钥实际交付树还单独 fresh 跑过 key 包测试与 vet。完整主线程验证不冒称由独立 reviewer 重跑。

CLI 真实逐槽确认/应用/恢复、旧入口安全准入与原生初始化 UI、MyGo 配置编辑、Codex 及其他 agent 的完整适配、导入/Pack 配置附件、自动应用、完整备份/S3 与正式 Release 仍分别待完成。本节不关闭这些首发门禁。

## Codex Provider Planning Checkpoint

2026-10-29，服务端 Codex 声明式 provider planner 已以 `c7d8ab60c2f7ae6d2c4b18b1163b72fe65d59dc8` 提交、SSH 推送并读回；对应 Build `40853241980` completed/success。此前的客户端密钥入口与本地快照两份规格以 `3c1348a5e0af77d12c296f8b374619680c9d53a4` 提交，对应 Build `40849671013` completed/success；规格交付不等于这些功能已经实现。

Codex 实现的精确八路径候选 tree 为 `25fb32052ce748b34e6468029743f407d02430db`，经非作者完整 Spec/Quality 验收，未关闭 finding 为 0。独立验证包含真实 moejs、provider/catalog presence 组合与隐藏成员、恶意输出、取消与 clone 隔离；恢复回归 716 个含子用例、shuffle 4600 个含子用例通过，三项先编译成功的行为负控分别断言失败，恢复后通过。实际交付 tree 为 `5d97c8c6a85534fdc97b05c6759ab4882728152a`。

该能力只生成单一 `codex.provider-key` 槽位、固定 provider/Responses 协议及尚未解析的 `preserve-local-login-v1` 策略声明。宿主基于独立输入快照重算完整 JSON 树，并返回自身持有的副本；整个 DTO 仍敏感，不得整体日志。真实运行时会省略 JavaScript `undefined` 成员，因此验证边界是归一化后的 JSON 树，不是 JavaScript own-key 反射；真正 JSON 中的未知成员、null 或错误值仍被拒绝。此交付不包含 Codex 本地 codec、登录解析、客户端应用或完整 F12。

主线程从不可变 A-only tree `d8227ddcc5b5369dc007e8de5f71371a07a80bb2` 导出，完成 npm、全仓 Go tests/vet、模块校验、Buf Go/Web 双模板生成比对、客户端无服务端/moejs 依赖检查，以及 CLI/服务端/MyGo 九项 CGO0 交叉构建，均退出 0。最终交付相对该验证树只增加上述两份已接受的规格文档，不冒称独立 reviewer 重跑了主线程全量验证，也不以交叉编译证明非 Linux 原生运行。

Claude/Linux 手动 CLI 配置工作流 C1 的独立候选 tree `feb621b2baf1a4fc5f2b1a6ddc3772df2e03651e` 尚未签收或交付。非作者完整验收发现两个 P2：未知子命令打印帮助后 exit0；满 stdout/stderr 时，1s 命令 deadline 未传到底层嵌套写入，约 10.06s 才退出。已有 focused 52/52 通过不能覆盖这两个真实进程反例；原候选与失败证据保留，须修复并复验。主线程曾验证的 A+C1 组合 tree `1ddbe8f66dc0ecf3db315206cbc222a09f33f38b` 全量通过，也不能替代此独立验收或为后续修订源码提供通过证明。

显式密钥入口迁移、MyGo 配置 UI、完整十四 agent、配置导入/Pack 附件/自动应用、加密备份/S3/恢复、跨平台实际运行及首个正式 Release 仍分别未完成。

## Claude Native CLI Apply And Restore Checkpoint

2026-10-29，上一节未签收的 C1 已由原作者修复，两个独立反例分别闭合；原拒签、原 RED 与原样独立 oracle 均保留。接受的 successor tree 为 `a4b79217d59dbfa415c760329f38cacde4a70c7c`，仍为原八路径；独立 scoped 复验连同原完整实现审签收该精确范围，非作者重新执行 focused 63 个含子用例全部通过、零 fail/skip，且分别撤销两处修复的 overlay 均先编译成功再由原断言产生 RED。满输出的真实 1s 命令约 6.02s 自行退出，遵守原有命令预算加独立 5s 固定诊断尾预算，未放宽原 6.5s oracle，也不声称整个进程仅耗时 1s。

该八路径以 `3b0345ead6bc27b906e89e9fb55bed8a8f081c72` 提交、SSH 推送并读回，交付 tree 为 `3ef3bbf250cfc50569f6e2ef2f253b96c396b3ad`。能力限 Linux/Claude 手动 CLI：`config apply` 默认预览，明确应用需要真实控制 TTY 的逐 secret slot 批准与另一次最终计划确认；`config restore` 独立预览、释放锁、确认、重开并校验 CAS；`config status` 为本地状态查询。网络与人类等待不持本地安装/目标锁；Unknown 只允许原 flow 内最多一次 Status，不重放秘密。已有客户端密钥必需，不创建新 key，不等于后续旧入口迁移或 MyGo 配置应用已经完成。

主线程从 base `65d8c113fe9596947c3ab5bcb558dbb5c84ca42a` 加精确 successor 八路径构造 tree `a8593d2f1de1a6afc5938413ac0c81f289d45bc5`，fresh npm、全仓 Go tests/vet、模块校验、Buf 双模板生成比对、客户端依赖门禁与九项 CGO0 构建均退出 0。非作者另核全部 924 个 tracked blob/mode、八路径来源及主验证日志归属；不是重新执行主全量。最终交付相对该测试树只增加已独立接受的 B2 plan 与原生配置 UI spec 两份文档，无其他源码变化。交叉构建不证明非 Linux 实际运行，CLI 正确处理不证明 GUI 或完整 F12。

实际交付的 Build `40866724425` completed/failure：全仓测试、amd64 OCI 与其余五组二进制成功，但 Linux arm64 二进制及 arm64 OCI 在 `internal/configapply/native_publish_linux.go:35` 编译失败。该架构的 `unix.Stat_t.Nlink` 为 uint32，而受控文件身份的 Links 为 uint64；此前九项主线程构建未包含 Linux arm64，不能拿其绿色声称六目标全覆盖。主线程在原不可变导出中以 Linux arm64 真正复现相同编译错误，保留原 CI 与本地 RED；需要单独修复显式类型转换并验证，而不是重跑原提交掩盖失败。

已接受的密钥入口实施计划支持后续 CLI 与 MyGo 两个不相交写集并行；本地备份快照实施计划也已接受。此处只记录设计与后续实施边界，不提前签收入口迁移、原生配置 UI、完整备份/S3/恢复或首个正式 Release。

## MyGo Explicit Client-Key Entrypoints Checkpoint

2026-10-29，MyGo 密钥入口迁移已按精确十三路径独立 Spec/Quality PASS，零开放 finding，并以 `3fc1326ec84a170e095f30c1c85602d8441752b1` 提交、SSH 推送及远端读回。交付 tree 为 `1f1c694f2d7c0b128b4b0c5e9cf5a3281f6c24ea`；相对文档基线 `c24710d056f484d872e650f183ffde502f322e3e` 的十三项 blob/mode 均等于已签候选，其余沿用基线。客户端显式初始化持久密钥；已有密钥加载不再隐式创建，缺失或不确定状态须明确恢复。异步准入、取消后真实排空、generation 以及关闭错误保持原权限和生命周期边界；这不是 MyGo 配置编辑或应用界面的完成声明。

非作者独立执行 desktop/clientkey 测试、vet、自建 late-key-loss、真实 drain、排队 generation、异步隐私及初始化释放失败 oracle；五项负控均编译成功后产生有限断言 RED，恢复后 GREEN。另以隔离 HOME/runtime 的私有 Cage/headless Wayland 实际启动生产 MyGo 窗口、捕获并正常退出；账号初始化的鼠标/键盘与截图仍是 `ui.NewTester` 证据，不冒称物理桌面或该原生窗口的账号输入验收。

主线程在新鲜组合导出中执行 npm ci/build、全仓 Go tests/vet、模块校验、六平台乘 CLI/server/MyGo 共十八项 CGO0 构建，全部退出 0；另核 Buf 双模板生成一致、客户端依赖不含服务端或 moejs。非作者机械复核全部 931 项 tracked blob/mode 及十八个产物的 Go1.27.3/CGO0/平台元数据；这不是独立重跑主线程全量测试。交付的 Build `40888776765` 已实际观察 completed/success，九个 job 全部成功，包括双架构 OCI；交叉构建不证明非 Linux 实际运行。

前述 Linux arm64 类型错误另以 `656c211d01a9d7d2632c5cee12b49f6fb3229ca9` 的显式 uint64 转换修复，Build `40869447717` completed/success；原失败 `40866724425` 保留，不能用新提交绿色改写旧结果。CLI 密钥入口另片、MyGo 原生配置 UI、完整十四 agent、导入/Pack 配置附件/自动应用、备份/S3/恢复及跨平台运行门禁仍需各自关闭；目前无正式 tag/Release。

## Codex typed provider 资产（2026-10-30）

提交 `4d0e6c0cac5399dc29f2fd36b7a28bc71f90c2fe`，tree `5d0905bf12b5277d53a4f7e26bc2675f64d3a4b4`，精确 23 路径，已 SSH 推送并远端读回。域、HTTP、Web 及最终组合分别经非作者 Spec/Quality 验收，范围内零未关闭 finding；该提交不包含仍未签收的 MyGo 配置界面。

已支持 owner-scoped 加密 Codex provider-key 资产 CRUD、无凭据复制、revision CAS、严格 Connect JSON/protobuf 映射和 Web 专属表单。optional false/none、目录字段 presence、空白和有序列表保留；未来字段只读、不允许保存或复制。缺 key 可保存草稿，结构完整不等于本机可应用或真实端点已验证。

主线程在不可变精确源码导出中执行 30 项门禁全部退出 0：npm 安装/构建、全仓 Go tests/vet、模块校验、Buf lint/breaking、Go/Web 正常生成与两次 parity、六平台乘三入口的十八项 CGO0 构建及客户端依赖隔离。独立最终审核核对 947 项 tracked blob/mode、组件来源和十八个产物元数据；不冒称由 reviewer 重跑全部门禁。

真实 SQLite/HTTP/Chromium 验证覆盖创建/重开/keep/clear/复制补 key、CAS 显式重读、丢回执 Unknown 禁重放、目录及嵌套列表实际排序保存后恢复、成功响应与离开确认交错、提交后切语言清新凭据并保非秘密草稿、焦点恢复、owner/origin 隔离以及 375px 深色与更宽浅色界面。最终新鲜运行十项 PASS、八条存储收据对应五次预期写入/提交，服务和 browser 均正常停止。另有独立真实服务 Web 回归及旧版 BODY 焦点断言负控；原 domain 拒签、原焦点 RED 和失败自动化尝试均保留，不以新运行改写。

Codex 官方 ChatGPT/OAuth、本机 provider 选择/文件写入/登录保留、原生授权消费及应用、其它 agent、来源导入/Pack 配置附件/自动应用、完整备份恢复及正式 Release 仍未由本次交付完成。六平台交叉构建不代表非 Linux 实际运行。

原提交的远端 Build `40930852848` completed/failure：`TestRepresentativeTimeoutDoesNotStarveNextRepository` 的第二次成功工作在 200ms 测试预算内超时，二进制和 Docker job 被跳过。后继 test-only 提交 `43fa42e555b0f3453a2b820386b247b3e1d097fb` 已 SSH 推送读回，保留首轮 200ms 真实超时及重启后顺序/收录断言，仅将成功工作预算分离为 3s；可取消的 250ms 成功 fixture 确定性复现旧预算不足，移除 cursor 推进的负控仍编译成功后断言失败。非作者独立复验签收，精确 one-file 候选的完整 30 项门禁全部退出 0；不修改生产期限或以本地绿色抹去原 CI 失败，后继远端 CI 仍须另行读回。

## MyGo Claude 原生配置交付（2026-10-30）

提交 **`bd00bc03411f3d1979e237da8e03abc5f0ca16fb`** 已精确交付十三个 desktop 路径、SSH 推送及读回，tree **`7fbfb9a537c426fcd638593802ba106a37f319c5`**。基线 `e373db83edd411950c5cb31db2dd8ae306b50e13` 的 tree 为 `3afc079373b9f8ed3de43b997363bc9ddc8b1f8a`；其 Darwin8 与已签计划保持不变。MyGo 支持 Claude provider/schema1 原生资产编辑、服务端保存与本机应用分离、无秘密复制、预览、逐 secret slot 加最终批准的应用，以及本机 Status/Restore；本机操作仅限 Linux。Codex 等其它资产在 MyGo 仅 metadata，不代表支持本机应用，详细操作与缺口见 `CONFIGURATION.md`。

非作者完整实施审及 safe-focus successor 复验接受 tree `55a09520db96790ccaf2d856fcb9e284a0536789`。原四个独立 probe 文件未改：六项独立测试三轮共 18 PASS，包含 Apply/Restore/Delete/dirty-discard 初次 Enter、opaque 隐私、真实 TLS/SQLite 七槽转换和零网络离线恢复。只回退旧 view 的 overlay 先编译成功，再由四个原行为断言全部 RED，恢复后四项 GREEN；deliberate Tab/Enter、slot 与最终批准门禁、焦点包含/返回及身份失效测试二十轮共 340 个含子用例 PASS，隐私原 oracle 一百轮 PASS。独立 fresh desktop 为 186 个含子用例 PASS、一个既有 fault-overlay 条件 skip，真实请求 deadline 用例运行 20.12s，vet/build 通过；不把该 skip 说成已测故障分支。

`NATIVE-UI-01` 在新候选关闭：三个 Modal 的安全控件优先，拒绝位于凭据 checkbox 与最终批准之前，零槽也不能默认 Enter 应用/恢复。没有禁用正常键盘确认或自造 focus trap。密码、session、分享 token 与新凭据保持 opaque，无 raw 兼容影子；非常规 fmt、私有容器、可逆 hex、JSON/stdlog/slog 检查通过，不承诺主动取出秘密后的日志安全或物理擦除。组件独立报告 `native-configuration-ui-focus-fix1-independent-review.md` SHA256 为 `00a8673821a098d81c72f42c2fd6aa7511851670ef61d9cff4d473fc2b3f68d9`；旧隐私/焦点 REJECT、四个原 RED 与旧基线并行 state deadline RED 全部保留，不用主线程绿色抹除。

主线程对上述精确最终 tree 的隔离导出执行 **40 项门禁，全部实际 exit0，终态 FINAL_EXIT=0**：

| 主线程实际门禁 | 项数 / 结果 |
| --- | --- |
| npm ci/build | 2 / exit0 |
| 默认包并行全仓 Go tests、vet、模块校验 | 3 / exit0；未使用 `-p=1`，包输出没有 cached 行 |
| Buf lint/breaking、Go/TS 正常生成及双 parity | 6 / exit0 |
| 六 OS/architecture 组合 × CLI/server/MyGo | 18 / Go1.27.3、CGO0 build exit0 |
| CLI/MyGo 生产依赖边界 | 1 / 无 server/moejs |
| Darwin amd64/arm64 × cli/clientkey/desktop/install/subscription | 10 / test-compile exit0，不是执行 |

对应导出 `/tmp/cognate-native-focus-final.AQe6mf/source`，主线程 `verification.log` SHA256 为 `37135c038434f363cfd773ee0a0e83635408fa914ffe454fb0cac5072acd3cc9`。非作者 whole 机械 ratify 核对 961 项 tracked bytes/mode、精确十三路径的 base/new blob 与完整 binary diff、基线文档/module 保全，以及 28 个产物实际 Go1.27.3/CGO0/目标平台与包路径；两份生成目录另逐项比对。新 Darwin desktop 测试与 opaque Model 的兼容性由两个架构实际 test-compile 验证，不是只读推断。独立最终报告 `native-configuration-ui-focus-final-combined-review.md` SHA256 为 `4ed4810b9ca9c1dd1b1c949529c8eca526b141773030c2f882d0524ddf6589aa`。这部分核验实际主日志和产物，**没有冒称 reviewer 重跑 40 门**。

没有为本次 safe-focus 修复重新打开原生窗口。既有 Linux Xvfb/llvmpipe 截图、鼠标/键盘与真实服务闭环保留原 `5cf14557` 候选归属；新焦点证明来自 MyGo headless 真事件及真实 HTTP/SQLite/文件，不将旧截图记作新窗口焦点 PASS，更不证明 Darwin/Windows 配置应用或 accessibility。

另保留基线 `e373db83` 的 platform Actions **`40936595059` actual completed/failure**：三 OS primitives jobs 成功；Darwin baseline 编译成功，但 APFS fixture 创建/挂载失败，在 capability 与六项 ordinary 行为 probes 之前停止，随后 cleanup/sealer fail-closed，只封存拒绝收据。原日志 SHA256 为 `e24e299e708b7a17c90565ba777b289c1281676faff8863db24b52fba281e0c4`。**这不是六项 native 普通行为 RED 已取得，也不是安装能力 PASS**；不能将基础设施失败当作预期产品 RED，根因与后继修复须单独验证。

本次 native 交付 checkpoint 起初仅观察到后继 CI pending/in_progress；随后主线程实际等待并 fresh 核对 Build **`40937407082`** 为 completed/success，精确 head 为 `bd00bc03411f3d1979e237da8e03abc5f0ca16fb`，九个 jobs 全部成功。platform **`40937406971`** 则实际 completed/failure，同样在 APFS fixture RuntimeError 处、capability 与六项 ordinary probes 之前停止，cleanup/sealer 也未取得完整能力收据；原失败日志 SHA256 为 `4374e2be86896a46023ab658c74c541a5da0e74dd95b9cea372652fec1999006`。本次没有该 run 其它三 OS primitives 的完整 fresh 核验，不借用 e373 的成绩称新 run 全部成功。Build 绿色不覆盖 platform 失败，更不等于六项 native 普通行为 RED、Darwin 安装/配置应用或新窗口 PASS。

完整历史、值级/文件 diff、其它 agent 本机支持、来源配置导入、Pack 配置附件、自动应用、非 Linux 配置应用和完整 F12/M7/正式 Release 仍未由该十三路径交付。

## Linux 认证 CLI 与首批组件预览收尾（2026-10-31）

主线程在 `793f086` 上合入已有认证 source10 及 AUTH-ORIGIN-01 的两文件后继，保留现有 Grok 文档段落；没有新派子 agent。原独立源审及限定 origin 复验是历史证据，本轮主线程自审与组合验证不称独立验收。明文 HTTP 仅允许字面量回环地址，用户名和密码从真实控制终端隐藏读取，会话仅以匿名 pipe 传给一次白名单子命令；B2 worker dispatch 与 `/proc/self/exe` 认证子命令共存。

Go 1.27.3、CGO0、readonly modules、env-i 与隔离 HOME/XDG/TMP 下，实际主程序构建及 `go test ./internal/cli -run '^TestAuthRun' -count=1 -timeout=180s -v` 通过，56 个含子用例 PASS、零 skip/fail。包含 12.5 秒延迟输入后才启动业务命令、真实 TLS/App/SQLite/PTY、确认写入、提交后失包、Logout 失败、取消与秘密隔离。全仓 `go test ./... -count=1 -timeout=600s -json` 为 7360 个含子用例 PASS、158 skip、49 package PASS、77 无测试包、零 fail；skip 不计运行资格。全仓 vet、模块校验、六 OS/架构 CLI CGO0 构建及 metadata 读取通过，格式和 diff 检查通过；交叉编译不是其它平台运行支持。

主 full 验证日志 SHA256：`63115309327dfeff2aa20b550e6a1cacbe309d3ce292033abcff8201ae0bba5c`；认证专项日志 SHA256：`3c97599ced9c497159a2ddd7d618022271dd9201be4ab0f5f94110b547f435fc`。发布仍须以对应 tag 的实际 CI、安装包和远端镜像结果为准；本地绿色不代替发布完成。未完成工作集中于 `TODO.md`，本片不开放 Codex 生产资格或补齐 F3/F4/F12/M7。

## 维护锁释放顺序（2026-10-31）

主线程在 `1fd7564` 上修正最后一个维护句柄的清理顺序：先关闭目录能力，
只有成功后才关闭物理锁。错误路径继续保留注册表失败条目，不尝试重新获取
或重放关闭。锁本身关闭失败时不声称物理互斥仍然成立。

先抽出沿用旧顺序的清理函数，加入真实第二进程回归测试，两个清理子用例
均以普通断言失败：期望 `BLOCKED`，实际 `ACQUIRED`，测试命令 exit 1。
调整顺序后，维护包全测通过，专项再执行 20 轮通过；测试覆盖清理中拒入、
注入清理错误后的拒入、正常释放后的获取及锁关闭错误传播。目录能力实际
关闭，错误由局部 closer 注入；不冒称操作系统真实 close 故障已经复现。
本轮是主线程自审，不是新增独立验收。

Go 1.27.3、CGO0、禁网模块解析、env-i 与隔离 HOME/XDG/TMP 下，全仓
`go test -json ./... -count=1` exit 0：7365 个含子用例 PASS、158 skip、
49 package PASS、77 无测试包、零 fail；全仓 `go vet ./...` 和
`go mod verify` 均 exit 0。全测试日志 SHA256：
`b959c611bdf3c0c9e8a37911abe10e45d629d3154d683087a4adae0570947dc0`。
原 RED 日志 SHA256：
`ae568cb03f66cd163f974ba9a73b12b6143060c1ab23a868ab1ea68b840190df`。
维护包测试二进制另外通过 Linux/macOS/Windows × amd64/arm64 六组 CGO0
交叉编译；不将编译通过记为非 Linux 的运行资格。格式与 diff 检查通过。
本片不启用完整备份、恢复、云上传或自动调度；预览发布标签保持不变。

该提交 `73ddf19` 的远端 Platform filesystem primitives run `41161084843`
实际 completed/success；Build `41161084838` 实际 completed/failure，唯一
测试包失败为 Secret Service 的 `TestProcessExitReclaimsLiveWorker`，在
`process_linux_test.go:299` 的私有 fixture 初始化中报 `fixture-open-session`。
失败发生在业务断言前，具体根因仍待排查，不归因 CPU、负载或本次锁修复，
也不以本地通过改写远端失败。原失败已加入 `TODO.md`。

## Grok 资产 CLI 与账号入口（2026-10-31）

主线程在 `73ddf19` 上组合既有 G2 候选四个新增文件，root 只合并 Grok
分派与注册两处 delta，保留账号入口及原命令。候选归档 SHA256 为
`273bf3a1b0877834feda4bd29805f767547a320f5400f4de9cc68515b5833cf0`。
另外接通账号白名单与真实 Login → Grok 子进程 → Logout 联调，不修改 G1、
服务端、protobuf、模块或生产 native 资格表。本轮是主线程自审和组合验证，
不称新增独立验收。

新增路由测试先取得普通断言 RED：九种 Grok 路由全部被旧白名单拒绝。
组合后专项 58 个含子用例 PASS、零 skip/fail；其中真实 CLI/PTy/TLS/App/
SQLite 覆盖九 RPC、完整同终端审阅、密钥独立确认、错误确认拒绝、CAS、
排序、提交后失包及输出失败的 Unknown。新增账号组合还覆盖默认预览、
确认写入、错误最终批准、已提交响应丢失及 Logout 失败，检查准确提交次数、
持久配置的七项字段与密钥、一次登录/登出、原会话清理和真实子进程秘密隔离。
账号专项额外重复三轮通过。

新增联调的早期失败原样保留：最初误以为所有 GetSession 均由 wrapper
发起，实际 G1 在准备及提交前还会重验会话；测试增加首次身份检查的子进程
观察，并准确要求总次数 2 或 3，未删除客户端重验。随后读取数据库内部
整型 id 而不是 public_id 的测试错误也已更正。两者都是新测试设置错误，
不是产品行为 RED；不能替代旧白名单的真实拒绝反例。

Go 1.27.3、CGO0、readonly modules、禁网模块解析及隔离 HOME/XDG/TMP 下，
全仓 `go test -json ./... -count=1 -timeout=600s` exit 0：7422 个含子用例
PASS、158 skip、49 package PASS、77 无测试包、零 fail；其中 Secret Service
23 个用例本地通过，但不覆盖上述远端失败。全仓 vet、模块校验、格式和 diff
检查均通过。六 OS/架构 CLI 与测试二进制交叉编译通过，逐个记录 Go/CGO/平台
元数据；它们来自开发树，不是重新发布的正式产物，也不是非 Linux 运行资格。

依赖检查确认 G1 不依赖 ent、server 或 JS runtime，CLI 库不依赖 server/moejs，
命令入口不引入插件运行时。早期两次依赖探针误将既有本地 ent 状态、入口的
快照 worker server/state 依赖归为新增越界，均保留 exit 1；核对既有入口及依赖
图后，更正检查范围，未为通过检查删除快照 worker 或放开客户端执行插件。

全仓测试日志 SHA256：
`d9ace1ac879c852accd0fdf320e063b35fe542e36c9537553679259ca8165a41`；
专项日志 SHA256：
`a72d61ad541f3734cd1590c3b0074b16433b078b2b06fb77566e1e6c4b9e9e13`。
不改变首批预览 tag；Grok Web/MyGo、来源导入与本机应用仍未完成。

## Secret Service 私有 fixture 诊断（2026-10-31）

在 `beafeca` 上针对旧 Build `41161084838` 的 `fixture-open-session` 失败
执行有界复现：仅给原失败点增加脱敏分类后，隔离 HOME/XDG/TMP、Go 1.27.3、
CGO0、readonly modules 和离线模块解析下运行原
`TestProcessExitReclaimsLiveWorker` 200 次，实际 200 PASS、零 skip/fail。
原失败未在本机复现，原因仍 UNKNOWN；不将它归为端点注册竞态、CPU 或负载，
不添加 OpenSession 重试、改变现有限时或放宽业务断言。

新增诊断仅允许八个固定 D-Bus 错误名，以及 deadline/canceled/other 分类；
另记录 Go 错误类型与上下文取消布尔值，不输出任意错误消息、D-Bus body、
未知错误名或秘密。新增测试覆盖全部允许分类、嵌套错误、上下文及未知名称/
敏感 body 隐藏。完整 Secret Service 包 32 个含子用例 PASS、零 skip/fail，
包 vet、格式及 diff 检查通过。测试使用真实私有 D-Bus/keyring，不访问用户
实际 keyring；这次仅增强 fixture 取证，不修改生产 Secret Service 行为。

200 次观察日志 SHA256：
`e1a5baab54a9719021f9d67f1b0668862eb28d066d333c968bd77528fb9c9f3d`。
包测试日志 SHA256：
`2fa28fb8a2398fbc47741f13f0aa3716c0c69e8e6d6341e0b9b5402792061947`。
本轮未重跑全仓；之前的 7422 PASS 属于 `beafeca` 组合，不能改记到本片。
`beafeca` 的 Platform run `41162492364` 已实际 success；Build `41162492271`
观察时仍在 setup-go 步骤运行，不重启、不冒称全绿，也不关闭旧失败待办。

## 服务端程序内主密钥初始化（2026-10-31）

新增显式 `--init-key-if-missing`，默认关闭，仅空实例生成。状态包测试覆盖首次生成的大小和权限、并发初始化、保留原 key、已有数据库丢 key 拒绝、错误大小/公开权限/符号链接/硬链接拒绝及取消。服务端集成测试实际两次打开并关闭数据库，再删除测试 key 验证不会为已有数据生成替代 key。

状态、服务端及 servercli 三包完整测试通过；初始化与懒猫祖先权限定向用例连续 20 轮通过，三个包 vet 通过。实际构建的服务端二进制在隔离测试目录首次启动和原 key 重启均 health 200，SIGTERM 正常退出；随后删除测试 key，进程失败且提示恢复原 key，不产生替代文件。Linux/Darwin/Windows × amd64/arm64 六项 `CGO_ENABLED=0` 编译通过，交叉编译不代表异机运行验收。本轮未跑全仓测试，不复用历史全仓计数。

用户 rc.5 设备日志显示安全的 32 字节密钥及私有叶目录，但第二级祖先为无 sticky bit 的 `0777`。本地同条件回归证明程序继续因可写祖先拒绝，而非密钥生成方式错误；错误保留具体非敏感原因，不输出内容。没有更改平台目录权限或降低核心检查，也没有发布新的 LPK；包装仍固定 rc.3 服务端，切换程序内初始化须与持久卷安全适配一起交付。

续接检索使用 nmem CLI，查询 `Cognate 懒猫 rc.5 诊断`，默认模式/default Space、结果上限 1，首位记忆 `db2cde48-d389-4105-b620-7eda7215c4bd`，标题“Cognate 懒猫 rc.5 诊断包已提交喵喵：缺失自动生 key，设备根因仍 UNKNOWN”，score 0.99。此处以新设备证据更新旧 UNKNOWN 判断；CLI 无可信浏览器会话，未自动生成图谱。

## 懒猫 root 私有卷适配及 rc.6（2026-10-31）

用户明确要求修复 root 运行的懒猫适配并显式传递数据目录。`root` 不被核心校验拒绝，真正触发点为 rc.5 日志中无 sticky bit 的 `0777` 祖先。核对本地官方 SDK 源码 `gitee.com/linakesi/lzc-sdk`，commit `f5bd0470167da6df10f5ebb396525d9a12706d7c` 的 `docs/app-runtime.md` 与 `docs/other.md`：`/lzcapp/var` 是应用自己的备份数据，目录约定为应用所属用户与 `0700`。没有把共享的平台路径当作应用私有卷。

包装源码 `9381130fd60140081d00ee2bb30fbe0a47e109d7`，版本 `v0.1.0-rc.6`，仅在使用 `/lzcapp/var` 下状态目录时，写入前验证 `/` 与 `/lzcapp` 的类型、属主和非可写权限，验证固定卷为当前用户拥有的真实目录，然后仅收紧该卷到 `0700` 并复查；不 chown、不递归 chmod、不修改其他祖先。显式传递数据目录、key 路径及 `--init-key-if-missing`，删除原 shell 随机生成逻辑。固定上游 `server-v0.1.0-rc.4` / `a576aeb5c8adf4bda98c1431bcd6072c642d3660`，无 Go 安全校验放宽。

新增持久回归 `cognate-lzcapp/tests/startup.py`。先在旧包装上观察 health 门禁 RED，再用新包装与实际二进制通过；另直接在 namespace UID 0、`0777` 卷、`0700` 叶目录及合规已有 key 上验证具体的 ancestor-writable 拒绝。独立 user/mount/network namespace 的根目录为本次创建的 tmpfs，数据卷为临时目录绑定，未修改开发机器的 `/lzcapp`。测试验证新安装、原 key inode 与字节保留、管理员原密码和日志授权、真实 `/proc` 中的显式路径参数及秘密清除、卷权限再次回到 `0777` 后重启；只读/符号链接卷、不可信平台父目录、自定义外部可写祖先、公开/多硬链接 key、已有数据丢 key 均拒绝且不生成替代 key。CI 增加同一 root 启动门禁，成功后才运行 reusable LPK 发布任务。

本地完整源构建、Web typecheck/build、Shell 语法、actionlint、同版 Action v1.3.7 原始 Manifest 预检与真实 LPK 构建通过。从实际 LPK 解出二进制和包装脚本，与构建输入逐字节比较一致，再以这两份解包产物运行全部 namespace 用例通过。该本地 LPK 为 68,036,608 字节，SHA256 `96793d6f0a768df7a9e5b446bebf571ce8014f6b365524e2df965ce885544e2f`；这只是本地产物身份，不替代 CI 最终资产校验。本轮没有更改 Go 代码；上游 `a576aeb` 的 Build `41168903301` 与 Platform `41168903181` 已 success，新增 server tag 的发布另行核验。

影响范围检查：严格 `ReadMasterKey` 和所有 Unix 文件安全检查保持不变；标准 Docker 镜像原有 UID 10001/`0700 /data` 已显式配置，不属于本次错误；只有单 Exec 懒猫应用卷进入收紧分支，自定义外部祖先仍拒绝。没有新增 token 日志，也没有触碰用户现有 key 或数据库。不将本地通过称为真机安装或 OIDC 验收。

rc.6 CI `41171336131` 在 namespace 挂载 runner checkout 中的测试脚本时报告 `Permission denied`，应用启动门禁未执行，后续 LPK 与商店发布均 skipped；保留该 tag 与失败。rc.7 源码 `58256406a2bf003fe97248cc2bbc6d1a64b044a0` 将测试脚本复制到测试用户拥有的私有临时目录后挂载，应用脚本与上游代码不变，不修改 runner home 权限、不绕过测试。rc.7 的本地 LPK 解包一致性与完整 namespace 启动回归也通过；本地产物 68,036,608 字节，SHA256 `205ef24ed2883a5856a038310abffb1b3fcc8fde304016a1a071abf7ea512e93`，与 CI 最终资产分别记录。

rc.7 Actions `41171750570` completed/success：真实 root `startup-smoke`、LPK 构建与喵喵提交、预览标记全部成功。GitHub API 核验公开且非 draft 的 prerelease，最终 `community.lazycat.app.cognate-v0.1.0-rc.7.lpk` 为 68,036,096 字节，digest `sha256:047ea3a9d71029470a086fd91316eed39f531dbae0399b8b5d7ecdfb114b29c1`。喵喵回执 published=true、skipped=false，app `670` / version `3757`，使用相同版本化 GitHub Asset URL 和 SHA256；官方商店仍关闭。该回执不是商店前台可见或设备安装成功的证明。

服务端 `server-v0.1.0-rc.4` 的发布 run `41170966158` completed/success，包括完整测试、amd64/arm64 Docker、镜像提升和实际镜像验证；不更新 latest。代码与包装 tag 均 SSH push 并读回一致，不移动 rc.6 tag。

最终 GitHub Asset 下载探针限时 90 秒，实际 exit 124，只有部分字节；不宣称下载成功、SHA256 复算通过或已启动 CI 最终 LPK。上述最终资产身份来自 GitHub Asset digest 与喵喵回执交叉核对；本地解包实跑与 CI 构建后二进制的 root 启动回归分别有证据，不能替代最终下载验收。

本次续接以 nmem CLI 查询 `Cognate rc.5 777 祖先 程序内`，默认模式/default Space、上限 1，首位 `c8ded42f-836e-48e3-9ed1-423338bb4ad1`，score 0.99；未扩展图谱，CLI 无可信浏览器会话。以新适配与真实回归更新其中“LPK 适配未完成”的阶段状态。
