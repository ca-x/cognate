# C7 v2实施验收门（全部未执行）

先Modern完整list，显式Go1.27.3/CGO_ENABLED=0/no-race；不用fixture切换profile/更换assertion当GREEN。新API编译缺失不是安全RED。每组保留原输入/旧行为、最小移除gate负控、新结果和actual exit/SHA。按Spec/Quality分轴非作者审，下面不等于作者自验PASS。

## 权限/状态RED→GREEN

1. owner Issue→真实设备Apply→owner正常Logout/24h交互session结束→fresh Pack child仍成功；普通CRUD/元数据Get/任意owner会话仍不能用device key/Pack bearer。删session不会偷偷删consent，撤consent/device才拒。
2. recipient设备登记/操作必须fresh普通账号+device PoP；伪DeviceID、跨origin/账号、仅child bearer、未知purpose、session与policy双parent/均缺、SQL0替NULL、跨row_kind/self-cycle都拒绝；原普通native旧字节仍过。
3. 原未到期ticket→recipient远程撤device→owner Issue并发：同一原事务锚点rev/终态拒绝。只“撤当前consent rows”的负控应真实放行，保留RED。
4. Prepare（含已有prepare幂等返回）/Authorize/Consume各自SQL barrier后删Pack/换owner/share撤销/到期、改附件/asset版本/row权限、撤grant/device/epoch或disable policy；均应失败且无secret/evidence。改asset但snapshot SHA不变也拒。
5. 零秘密/native-login Prepare→在Authorize后撤权→terminal Consume拒；去掉零槽Consume负控应错报成功。旧无Pack零槽断言原字节保持。
6. owner撤单条/同device自己的所有grant、recipient撤整device、全Pack双epoch撤销；issuer不能撤其它owner的grant或读跨owner设备diff/恢复；新版本新consent/local审批，不改旧pins。

## D5真实进程与隐私

7. App/TLS/SQLite+隔离HOME/实际CLI/MyGo设备进程，recipient Web命令→outbound poll→真实engine plan→typed脱敏diff→设备可信批准→Apply/reported result；无opt-in/未确认请求只能NeedsDeviceApproval，不因Web按钮材料化。
8. Web秘密批准绕行/伪server enabled/伪device report Applied不能取得native材料或越过本机baseline；报告标reported非attestation。issuer看recipient设备任一List/Get/Page/Command/Restore都拒。
9. 已知credential/sentinel secrets、旧本机秘密、raw未知节点、绝对HOME/username/env/argv分别注入：SQLite可解密业务范围、HTTP/日志/error/浏览器storage/报告序列化/复制中不出现；secret仅在设备消费内存/原受管文件。不能用“密文里搜不到”单独证明隐私。
10. 完整16附件×512refs/长合法ID、diff128跨页/长单项chunk、缺页/混页/重复ordinal/digest/过期/cross-report/ref集合投影不一致；不截断合法集合，不把1m隐式cap当公开MaxRecords。
11. pending command多tabCAS/并发Issue/revoke/poll/report，stop_generation抢先、离线/重启、ReportPage丢包只重读不重执行；server业务提交未知禁止自动重发命令。
12. recipient WebRequestRestore→设备新真实恢复计划+确认→实际部分状态/完整结果。grant已撤仍可恢复自己的受管备份，device已撤不接受远端控制；旧backup ID不能当任意路径/别账号record入口。

## 本机/关闭/故障

13. skills Manager持client.db.install.lock时配置不得嵌套Open；Close后才进engine。目标独占多Pack在首写前冲突、共存只组合已验证disjoint变更；skills成功/配置失败不混为全Pack成功。
14. 本地外改/late conflict/过期observation/tuple变化/失key/未知journal/共享锁释放不明拒绝；删除journal不重置资格；准确After no-op不重新消费。
15. 每网络write前durable Attempted/fsync；Consume真提交失回执、Kill/reopen、remoteCommit/local失败、部分publish、Status不得重吐secret或自动replay。Unknown旧job未解决不得新自动job绕过target。
16. cancel/identity switch/Web关窗/CLI SIGTERM/MyGo Close/server Close：stop必须明确received；long-poll waiters取消join，worker/proc Wait和App.Close返回实际检查，所有pipeline/日志/输出关闭错误保留；不靠unchecked defer。明确远端已禁用不等于设备已排空。

## schema/T4/R3

17. S0→S1真实SQLite DDL迁移，完整旧投影/序列/密文不变，新表空、exact fresh/upgrade catalog同一family；普通SH不DDL，未知schema/中断/Commit/Close/Wait/Sync不明不发布。
18. S1 backup/candidate→九DML sanitize→独立后inspect，旧receipt/device key/ticket/child不能复活，即便masterkey/Pack不变。删新增清空/后验负控必须真实RED；保留原八DML收据边界。
19. 全相关ID/nullable/foreign域先证明、category/ent_types合法边界不误拒、MaxRecords(0,MaxInt64]/remaining+1/128分页、不明close优先cancel、worker资源先SQL；失败zero evidence。

## 真实CLI/GUI/consumer资格

20. 中英、深浅、dirty/focus、keyboard、375px、真正CDP浏览器200% zoom、reduced-motion、切主题语言草稿保持、身份切换清secret/审批。MyGo真实窗口不以headless model替所有交互；Web真实built正常npm锁安装/build，不借作者dist。
21. 逐十四agent/kind/OS/GUI cell取得真实native消费者/恢复和Pack后台receipt；未开放保Unavailable，不翻registry bool、不调用真实OpenAI模型或真实用户HOME。Gemini要等其独立freeze再组合，原无Pack六RPC40/41 golden bytes回归。

22. 所有公共旧proof APIs（含native_proof.go knownTree/StrictUnmarshalNativeProof/validDecodedProof和Codex/Gemini公开helper）对每个新known Pack字段/双parent/空message/optional0/false/nested mix实际拒绝；分别从公开API直调与严格wire调用，不借HTTP兜底。保留原合法old canonical；remove legacy allowlist应产生行为RED，不能因新字段known而假绿。
23. 用户offline schema-upgrade真实CLI/包装oneshot：非空WAL、原账号/密码/密文/序列→check→显式review apply→真实EX/worker→独立S1→R3发布/实际App/原密码登录读回；升级不跑T4撤权。发布前失败原数据不变，发布后读回失败按真实intent保留两代/阻断writer，Status不重跑。SQL helper PASS不关闭产品路径。
24. Enroll实际原ordinary Begin→Bind→fresh child→同parent普通session+device/child双PoP；server challenge只在Bind单次消费，Enroll的child binding在新表唯一消费。并发两Enroll/换key重放/used challenge复活/客户端nonce冒serverfresh/Pack parent假引导/另一session/已Prepare child/过期/Logout/失包拒绝；去掉唯一消费gate/索引出现行为RED，原Begin/Bind字节/TTL不变。

定向命令形状（批准实施后，以实际测试名为准，不空匹配冒PASS）：

```sh
export PATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/usr/bin:/bin
export CGO_ENABLED=0
go test -count=1 -timeout=10m ./internal/packattachments ./internal/desktop
go test -count=1 -timeout=10m -run 'Test.*Pack|Test.*Native|Test.*Policy|Test.*Schema|Test.*Restore' \
  ./internal/nativesessionauth ./internal/server/state ./internal/server/auth \
  ./internal/server/configuration ./internal/server/nativeconsumer ./internal/server/httpapi ./internal/server
go test -count=1 -timeout=10m -run 'Test.*Pack|Test.*Native|Test.*Codex|Test.*Device' \
  ./internal/configclient ./internal/configapply ./internal/nativeconfig ./internal/subscription ./internal/cli
go vet ./internal/packattachments ./internal/nativesessionauth ./internal/server/state \
  ./internal/server/auth ./internal/server/configuration ./internal/server/nativeconsumer ./internal/server/httpapi \
  ./internal/configclient ./internal/configapply ./internal/nativeconfig ./internal/subscription ./internal/desktop ./internal/cli
```

Ent/Buf生成先用固定版本工具；审实际diff与精确写集，Buf lint/breaking用私有基线proto导出；不得替换Gemini/Codex既有字段。Web用既有锁/依赖和新纯状态tests，实际App/浏览器/PTY/固定ELF另存收据。全仓/18build后继由主组合，不在本轮设计中重跑。
