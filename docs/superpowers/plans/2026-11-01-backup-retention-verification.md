# 必须验收（全部为未来门，当前未实测）

每条须记录完整argv/环境/终态exit与失败日志；不能继承主T4/main矩阵或作者旧PASS。行为RED只能是可编译减门后断言失败，setup/缺符号/缺dist/挂超时不算安全RED。正向R3 proof必须来自生产CLI全链，不用bool/MAC-only/内部helper捷径。

| 门 | fixture/关键断言 | 必要减门RED |
| --- | --- | --- |
| cold opener真实只读 | typed EX下cold open/read/Close，文件集合/bytes/mode/关键inode不变；缺lock/log/sidecar、next/fence零repair，审计create/rename/unlink/Sync写路径计数0 | 改回普通opener/普通Close或O_CREAT helper，观察next被删/缺文件被建/写fence路径，必须失败 |
| cold同锁/owner/排空 | 同control.lock第二进程非阻塞Busy；wrong Runtime/EX/克隆日志根/过期scope拒；active cold Close=Busy而非自等；源操作退出后正常Close | 去typed owner或named+FD门能错放；关掉pin/tombstone同锁使held reader被retire，必须失败 |
| 无嵌套EX/SH | 真R3已有EX及operation逻辑scope，cold callback中调Offline proof reader/Source后全返回；零OpenRuntime/LockOffline调用，有限deadline内成功 | 故意再开SH/EX或把consumer放进lease FD callback，隔离child必须失败/被外层有界拒绝；不能把挂超时记正常成功 |
| 原v1/历史/HMAC | 原v1 golden、generation/options golden、同request原job、1000/16MiB、record字节及catalog不变；v2 off仍读tombstone | 去ReclaimID或删sidecar后放行为行为RED；不通过删历史腾空间 |
| 真Source全集合 | 真实Capture→worker→Seal密文，经TLS head/CopyExact/Finish/Stage；observed两头+root/page/extents与catalog全等，最后一项也覆盖 | 删末项/加ref/重复/错complete或callback成功但尾Close失败不得公开facts；删MatchCatalog门使变异放行则RED |
| 真实D1正向 | 生产离线CLI完整T4→实际副本登录/各owner HTTP/API/下载/解密→selected RO App/Serve→全Close/Wait/原EX结束→attestation→独立冷读→同源TLS/Candidate | 只跑Candidate/只任一probe/被动MAC/facts/metadata入口不得授权；去proof门再跑负控必须RED |
| 原CloseUnknown | 原App/worker/EX真实终结不明，accepted历史可存在但无completedDrill；重启/进程消失也不能补造 | 以accepted/response ok代替真实终态使proof可得，RED；零新success DELETE |
| attestation尾Unknown | 原演练全已知但最终写/Sync/Close/返回未知，finalizing/next/fence或缺图冷读拒；仅R3显式reconcile能认证原完整证据 | 删late-ready/admit门后冷读得到proof则RED；retention Reconcile不能替R3解门 |
| late Close+EMFILE | 真实FD物理Close成功但hook返回错误，随后真实耗尽FD；晚期ready发布点和最终release/fence点逐一注入，独立新冷reader仍拒proof/零success dispatch | 清gate后靠新dup/事后写Unknown的变体必须失败；不以“指针还在”证明锁仍持有 |
| 身份/版本/lineage | OIDC-only identity pending、错I/B/A/complete/closure/policy/versions/owner/代、rollback旧proof拒；真实后续业务DB/WAL写不抹历史演练 | initial-provenance当complete-drill、source原DB被sanitized摘要替换或回滚复用proof均RED |
| 远端实际退役 | 至少4真实发布，keep2+有效已演练anchor，complete DELETE先并确认Missing后payload；剩余锚点可完整Candidate读；failed/canceled无complete orphan实删，额外/外部/别实例对象不动 | 去If-Match/完整Inspect/complete顺序，替换对象与412必须失败；List遗漏不得证Missing |
| Unknown零重传 | DELETE已删但回复丢、仍存在503、intent后发送前kill、cursor后kill重开；同ref第二次DELETE=0；Reconcile仅Missing推进下一未尝试对象 | 去durable intent或把Unknown当success/新UUID重发必须RED；保持原FAIL日志 |
| 新本地crash回收 | 真R2/当前generation snapshot child、明文DB/归档/密文；父死窗口Pdeathsig0仅测试，interrupted零删除，Cancel后guard仍阻，实际child Wait后回收，下一job同cap成功 | 去guard/Close确认/身份门或提前退quota必须RED；旧无证/link/device/换inode/未知项原字节不动 |
| D7保守边界 | 普通旧T1 RO inspector可能存活/Wait或Close未知；全额reservation、未知目录不删，snapshot guard可得仍不放行；自动及手动success DELETE=0，普通App启动/WAL恢复/双SH仍可 | 将signal/PID/guard可得/T4 ACCEPT当终态，或找新anchor/新UUID绕过，必须RED |
| 调度/Close错误链 | retention-only真Serve、synctest300s首次/固定延迟/无catchup、busy无队列、due不饿死；scheduler cancel不撤registered scope；真实FD不明一路App留SH/EX拒，纯remote Unknown可Close=nil | 去owned ctx/Stop join、吞Candidate/FD Close或取消scheduler连带撤GC均RED |
| built管理闭环 | admin A/B、普通/匿名/share/MCP拒；旧Reconcile不复活；实际browser publish/proof/retire/orphan/下job、Unknown只读零重DELETE、EN/中文/375/焦点/无secret | 无真实backend状态或未显式stop/App.Close/Serve-worker Wait/EX释放判失败，不能用页面成功灯替代 |

## 建议有界命令

在主批准后的独立组合源执行；本轮未执行。R3/T4真实导出须单列manifest，不能用生产stub假装符号和资格齐全。所有HOME/TMPDIR/output私有、合成key/auth只在内存/childenv/0600输入，日志禁secret/raw SDK响应。

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.13.0/bin:$PATH
export CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off GOFLAGS=-mod=readonly
npm --prefix web ci --ignore-scripts --no-audit --no-fund
buf generate --template buf.gen.yaml --path proto/skillhub/management/v1/backup.proto
buf generate --template buf.gen.web.yaml --path proto/skillhub/management/v1/backup.proto
npm --prefix web run typecheck
npm --prefix web run build
node scripts/verify-backup-retention-state.mjs
go test ./internal/server/backup -run '^(TestRetention|TestReclaim|TestArtifact|TestAutomatic|TestController|TestScopedListAndConditionalDelete|TestDeleteConflictAndUncertainOutcomes|TestListOmissionDoesNotProveMissing)' -count=1 -timeout=4m
go test ./internal/server/state ./internal/server/library ./internal/server/restore -run '^(TestLocalSnapshotWorkspace|TestSnapshotArtifactGuard|TestRetained|TestDrillExpectation|TestS3Candidate|TestCandidate)' -count=1 -timeout=4m
go build -o "$PRIVATE_OUTPUT/cognate-server" ./cmd/cognate-server
go test ./internal/server ./internal/server/httpapi -run '^(TestServerBackupRetention|TestBackupRetention|TestServerBackupSettings|TestServerBackupAutomatic)' -count=1 -timeout=4m
go vet ./internal/server/backup ./internal/server/state ./internal/server/library ./internal/server/restore ./internal/server ./internal/server/httpapi
go test -c -o "$PRIVATE_OUTPUT/retention-browser.test" ./internal/server
```

PRIVATE_OUTPUT是运行器创建的私有绝对路径，不是新增产品env。测试fixture通过显式私有输入接收该单份CLI binary；使用R3既有`restore plan/activate/status --config-file <0600-file> --operation <uuid>`及activate的`--confirm <request-digest>`，不另开后门或命令。确认摘要与原key/identity输入只在受控fixture内，终端仅固定phase结果，不输出secret配置。

新server测试负责调用真实binary并消费收据，避免same-package server测试import servercli形成环。Browser runner可复用现verify-backup-web的私有test-only副本扩本片场景，随evidence交可复跑overlay/patch，不修改已审canonical脚本或占第51源路径。真正final dist先build、自己的fixture再编译；不能复用作者binary、无dist编译或旧PASS。

每次修改Go前执行Modern指南完整list；最多有界相关包和一份Linux CLI构建，不重复全仓/18build、Serve60已完矩阵或WebMCP。任何信号/timeout/缺stop/未Wait/Close非nil必须保留FAIL，不自动重跑覆盖日志。正向EX须实际成功，短超时只能Unknown，不能沿用旧150ms结果当释放证明。
