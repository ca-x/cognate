# S0用户可执行升级产品链：明确申请，尚未实现

MigrationRequired只是一道安全门，不是用户升级功能。此链是C7 B上线的必要依赖，由主协调R3发布/激活实现与独审；未完成前不得更新LPK/Docker并宣称旧数据可直接启用B。不是让用户上线后补测代替交付。

## 1. 精确用户/包装启动点

同一个CGO0 `cognate-server` binary新增离线子命令，普通serve与升级不是同一mode：

```sh
cognate-server schema-upgrade --data-dir /absolute/existing/data \
  --key-file /absolute/existing/keys/master.key --check
cognate-server schema-upgrade --data-dir /absolute/existing/data \
  --key-file /absolute/existing/keys/master.key --apply --review-id <check返回的64hex>
```

普通启动遇S0在任何自动DDL之前返回MigrationRequired，输出安全的目标family/上述命令说明，不进入无限重启/auto-apply。`--check`不改原DB/不DDL/不发布；EX锁/有界私有staging可产生受管临时文件，关闭不明保留fence，不虚称整个文件系统零写。`--apply`必须显式review ID；无apply默认只check，不能因镜像/LPK版本变化自动同意。review绑定root/key公有ID、source DB/WAL/SHM身份+hash、S0/S1 exact catalog、迁移binary/schema身份及计划；apply重新取EX并复核，stale拒绝而不是用新库静默重算同意。

入口文件：`internal/servercli/root.go`注册子命令，`internal/servercli/schema_upgrade.go`完整check/apply输出/确认/stop收据；`cmd/cognate-server/main.go`仅注册独立迁移worker dispatch，不把普通T1变RW。不初始化/覆盖masterkey；缺key、错key、不安全目录、现有live SH/writer全部在原数据写前拒绝。不放宽懒猫旧目录权限，不重新解释用户旧成功问题。

Docker：先停止原service，在**同一现有volume、同运行身份、同key路径**用新image运行该binary的oneshot子命令check→apply，成功后正常启动原service。不得新建空volume/换key、自动清数据或把命令改成另一个source。LPK：包装的离线维护启动点同样执行该binary子命令，固定已成功路径`/lzcapp/var/cognate/data`、`/lzcapp/var/cognate/keys/master.key`；先停正常App，再oneshot，最后正常启动。无需额外shell runtime、新listener或原Web登录；不把密码/key放argv/env。若现有外部LPK launcher未透传子命令，主须先冻结该包装repo的准确launcher delta，不能靠服务启动失败后让用户猜命令。

包装对外可提供“检查升级/执行已审阅升级/启动原服务”三个动作，但不得从“执行镜像更新”推断授权。此设计的产品启动点在Go CLI，不私自改外部lazycat-contrib/cognate-lzcapp freeze。发布门需留正常Docker+实际LPK等价launcher探针及原账号登录/数据保留收据，不先放行再等用户测试。

已只读定位外部包装commit `934ea0599aba1ac5898b3121e1649393c084863d`：真实入口`run.sh`会重建普通serve argv并准备bootstrap/OIDC，不能声称今天已透传schema-upgrade。准确独立申请见`PACKAGING-REQUEST-v2.tsv`：run.sh新增最前面的显式schema-upgrade maintenance分派，拒绝普通serve初始化/密码/OIDC副作用，沿原严格目录检查、最小env调用同binary；tests/startup.py实际旧数据/原密码升级/失败保留；README.md写可执行维护操作。未改该repo/manifest/发布pin，也不使用浮动其HEAD构建C7。

## 2. typed EX→candidate→迁移→验证→发布接口合同

以下是新申请的opaque接口职责，不是当前R3已经有的符号，也不是将SQL helper改名当capability：

1. `SchemaUpgradePlan`：只读safe facts/review ID；不能变异DB、发布、释放EX或取得任意Path/FD。
2. `OfflineUpgradeOwner.WithRoot(ctx, ...)`：R3明确交出的现有运行代EX、schema lock、root/DB/key身份和durable operation intent；不在普通SH Serve内执行，不借LiveStore作migration。
3. `UpgradeCandidate`：仅该operation有删除权的私有candidate；source原DB/sidecars/archives只读借用，live root与candidate身份不同。可调用内部state SQL migration primitive；普通调用者拿*sql.Tx不等于取得此owner。
4. `VerifiedSchemaUpgrade`：独立固定worker在DDL后物理排空/Sync重新inspect S1、旧数据protected projection相等、新policy为空后才生成。one-shot scoped view，不能用一个bool或被动hash代替。
5. `PublishSchemaUpgrade`：仅R3明确的publication adapter接受VerifiedSchemaUpgrade+原owner+intent；精确发布/activation/current/rollback属于R3，不直接rename Path或跳过其durable fence。
6. `SchemaUpgradeReadback`：真实App使用原key/原账号库、正常新schema而非fixture Store打开；受R3授权activation ownership保护。App.Open/Serve/受限实际RPC读回/Close/worker Wait全部join并检查，才可标UpgradeComplete。

`internal/server/restore/schema_upgrade{,_linux,_other}.go`负责前四项coordinator，`schema_upgrade_publication.go`是**R3-owner adapter申请**；真实R3冻结后必须逐项绑定其实际opaque publication/activation API，不能在这里伪造一个开放文件路径发布后说已完成。`internal/server/state/schema_upgrade{,_worker,_worker_linux,_worker_other}.go`负责固定SQL/worker，匿名pipe key/结果，不含发布能力。测试路径在WRITESET-v2/GUPGRADE列明。

实际App读回由servercli/主R3的App activation接点执行，restore package不直接import上层server造成cycle，也不接受任意callback返回nil就生成Complete。需要R3真实opaque activation/runtime owner绑定同一operation/root/schema和App实际读回/排空结果；`COORDINATION-v2.tsv`明确App/app_runtime接点申请，不借当前不存在的接口或伪Runtime。

初期仅Linux/amd64真实资格；other显式Unsupported，保持全CGO0可构建。worker安装现有成熟资源/网络限制再SQLite，DB/row/staging/CPU/时间/FD上界先证明；DDL需要的syscall由独立精确sandbox测试，不放宽T1/T4既有sandbox。

## 3. 保留原业务，不把升级当灾难恢复

**Upgrade != DisasterRestore。** schema-upgrade不调用T4九DML，不清sessions/native旧短期行、不撤share/MCP、不改密码/hash/UUID/OIDC/GitHub状态，不重新签发key、不改业务revision。S1新增policy表必须空；旧native只增加NULL新列/相同session parent值。所有旧密文、AAD身份、public/internal ID、账号/密码材料、原序列高水位、业务计数与archive refs逐项canonical投影不变。

源若有WAL/SHM：在EX下先固定所有原输入身份/hash/预算，只对私有完整副本进行SQLite crash recovery/规范化；不能为省事在live旧库checkpoint、RW recovery或VACUUM。逻辑S0在private恢复后再独立验，原DB/WAL/SHM字节前后不变。source sidecar不可信/不完整就拒，不能忽略已提交WAL交易。迁移只复制DB所需输入，不复制整档案库；归档只借受限验证视图。

T4真正灾难恢复仍按SCHEMA-RESTORE-v2九DML删除所有持久policy/device及旧会话，防旧证书复活。两种operation_kind写进R3 intent/投影/发布审阅，不能让一个mode偷走另一个的失权或保留语义。

## 4. 发布、实际App读回与失败保旧

- 发布前任一步失败：旧运行代/原key/DB及sidecars/archives、当前指针均不改；candidate依唯一删除owner清理，清理/Close不明保留fence，不返回成功、不重放同一DDL事务。
- verified之后才R3发布。旧代留存为受保护rollback来源，发布intent记录前后schema/DB/root身份，source facts不被新hash覆盖。
- 实际App读回不能在持EX时再递归取SH自锁；必须使用R3真实的EX→activation Runtime所有权交接，并阻止其它普通writer在读回资格确定前入场。C7不发明flock“原子升级”或伪Runtime；若该R3接口未交付，此项仍阻塞产品升级。
- 读回期间禁止自动source refresh/backup等业务后台先写；实际新的App打开S1、核原key、账号/password材料/资产/Pack/序列和正常handler数据，Close/Serve/log/worker全部排空。隔离fixture随后用**原密码正常登录并实际读回**，不以schema metadata代替App。真实用户密码不作为CLI输入或取证要求。
- 发布后读回失败/commit或最后close不明：保留两代与intent并拒绝普通writer，不假称“从未发布/旧库一定仍active”。仅走R3已资格化的显式rollback/reconcile；不得自动copy旧DB盖回，也不能再以旧review ID apply。结果Unknown/NeedsAttention，产品诊断指向同一operation的只读状态。
- `schema-upgrade --status <operation-id>`为只读对账入口，不重跑migration或恢复材料；Complete只在原intent/已发布schema+App读回证据都相符。新重试须解决旧intent、重check/新review。

## 5. 产品依赖与接受条件

明确申请GUPGRADE路径及R3 publication/activation adapter，不碰R3/T4冻结、不自行改kernel。主提供真实R3冻结接口后再串行编写；未有它不是产品可用，不能以SQL helper/安全MigrationRequired关闭缺口。

实际验证：old S0 fresh-key→原账号/密码/合成agent密文/非空WAL数据→CLI check→显式apply→真实worker/EX/migration/S1独验→R3发布→真实App/原密码登录/数据读回；源完整投影+序列不变。真live第二server/错误key/unsafe path/中断/各边界commit-Close-Wait-Sync/发布前失败/发布后读回失败/rollback分别实测，去掉scope/投影/独验App读回负控必须行为RED。全部当前未执行，未给产品Ready。
