# 批准后的单片实施顺序

本文件是未来执行计划，不是已执行记录。采用本人顺序实施、不spawn/commit；以下六步是一个完整可用片的内部顺序，不是六次发布。开工条件为主批准50路径及DELTA两项接线补足；不互等R3源码freeze做设计或backup内核实施。

## A. 控制权威/冷开/read-pin

目标：control_store.go、control_store_unix/other、retention.go、controller.go、reclaim_store与其测试、retention_reference_linux_test。

- 先建真实现有control v1/v2、同UID目录、两个真实根与typed Runtime/EX fixture。先写TestRetentionColdOpenNoMutation、TestRetentionColdMissingOrNextRejectsWithoutRepair、TestRetentionColdOwnerAndSharedLock、TestRetentionPinTombstoneAtomic、TestRetentionColdCloseActiveIsBusy。
- 行为RED分别用旧ordinary opener/Close、有O_CREAT的锁helper、删除同锁pin门或owner身份门的可编译overlay触发；缺符号只记entry RED。
- 实现INTERFACES冷模式/readonly Close和同锁scope；冷读禁止persist/构造Controller，错owner早拒。ControlStore未知与物理Close链保持；R3外层operation持EX，不在root callback内运行consumer。
- 实现ReclaimID绑定迁移及1000/16MiB/4MiB容量、strict目录/版本。以旧v1和原generation/options编码golden证明旧字节不变；sidecar错/丢/next/容量/发布故障拒绝。

## B. Source观测与R3受信bridge

目标：新增restore/retention_source.go/_linux_test.go，共享restore/status.go hunk，backup_retention_runtime.go，app.go唯一实参及backup_runtime.go构造/错误保留链。

- 不复制R3活实现。按固定v3 types/reader签名编写消费；status共享hunk只接owner校验和共用私有metadata reader。R3签发、late-ready持久权威仍由其既有operation/records/publish/lease负责。
- 测试TestRetainedSourceFullClosureAndTailClose、TestRetainedSourceHeadAndCatalogBinding、TestDrillExpectationIsNotProof、TestRetainedDrillOwnerPinAndLateReady；ref缺/额外/重复/末项、complete不同、callback成功但Source尾Close失败均拒。Source wrapper必须复用原s3CandidateSource，不能另造crypto遍历。
- 生产bridge先pin/expectation/opaque proof，再真实S3/Prepare/WithVerified/全refs/全Close/post-admit，最后返回；nil proof源或手填facts拒。正向fixture暂时没有真实R3时就是未完成正向，不允许return nil假替代。
- controller只接自己的App Runtime；backup_runtime借用而不Close，app.go不写其他R3/T3/Serve/Close hunk。注入真实ErrCandidateUncertain，证明经Controller poison→serverBackup→App而非仅UI错误码。

## C. 工作区所有权/配额/原快照接线

目标：artifact_store*、capture/seal、library/content_snapshot、state/local_snapshot/workspace*、controller_worker、backup_runtime。

- 写TestArtifactCrashGuardAndQuota、TestArtifactUnknownRetainsFullReservation、TestArtifactLegacyAndSubstitutionRefused、TestRetentionD7UncertainNoSuccessDelete、TestLocalSnapshotWorkspaceRealWorker。
- 再实现先owner intent再Mkdir/payload、At API到原闭包、局部engine、prototype空workspace、跨任务reservation和FD相对两遍GC。不要改R2/T4/R3 worker协议与同OFD kernel。
- 真实父死受证capture残留先interrupted保护，显式Cancel后仍须实际旧child/guard退役；下一job成功。D7旧T1另一条Unknown全额charge，即使snapshot guard可得也零success DELETE。

## D. 策略/持久删除状态机/owned调度

目标：retention_worker/retention/automatic/controller、backup_config及所有对应测试。

- TestRetentionRealProofProtectedAnchor、TestRetentionDeletesCompleteFirst、TestRetentionOrphanExactReferences、TestRetentionDeleteUnknownNoReplay、TestRetentionSchedulerOwnsRegisteredWork、TestRetentionClosePreservesRuntime。
- 保留最新K和唯一当前已演练anchor；没有真实proof不删success。持久tombstone/每对象intent后单次原Inspect/If-Match/DeleteExact，Missing才推进；Unknown跨重启只读对账，不自动重发/新UUID。
- 固定300秒timer，retention-only、busy skip、due优先、不catchup；Stop producer不撤销已注册GC。Run RPC只注册owned scope；Close锁外cancel/join，真实FD不明保留App SH。

## E. 管理/Web与真实联合消费

目标：httpapi、backup proto+三生成物、Web三源、新state脚本、原backup browser fixture增量和server集成测试。

- 先正常npm ci，Buf仅backup proto→typecheck/build。旧六RPC保护retirement，两个新RPC严格admin先鉴权，A/B均可管理实例actor，不放宽CAS/Unknown。
- 真实生产cognate-server restore plan/activate/status经原私有config/operation/confirm参数完成离线演练与全终态；冷reader独立取得proof，再真实TLS/Candidate同源重验和物理退役，确认至少最新2及受证anchor仍可读。
- built页完成策略配置显示而非健康灯、手动清理确认、真实503→Unknown→只读对账零重DELETE、orphan清理/下个job，EN/中文/375/键盘焦点/无秘密。test-only浏览器overlay在私有probes外，不新增产品写集或覆盖旧fixture收据；显式stop/Serve-worker Wait/App.Close及真实EX释放。

## F. 精确冻结/非作者审

- 仅有界相关测试/vet+一份Linux实际CLI binary；保留所有历史失败，核全源manifest、50路径/生成物、共享hunk与各自基线。每次Go改前Modern，Go1.27.3/CGO0/no-race。
- 交patch/fullsource/SHA/安全证据；R3依赖manifest与本片patch分别绑定，不把其producer路径暗收为本片实现。请求非作者复跑关键减门与final-built生命周期；主另做当前组合。未实现/未联调/未独审不能称F4/GA。

无全仓、18build、WebMCP重跑；无主线写或旧freeze覆盖。第51路径、原crypto/catalog/协议/kernel/权限/Unknown放宽需求，先具体报再批。
