# Native desktop Pack watch implementation plan

**Goal:** 将已签收的 `subscription.Watch` 接入 MyGo 原生生命周期，显式启停并保守复用现有安装/订阅内核。

**Architecture:** UI 线程独占 Model，单后台 worker 复用 Watch 的串行调度和有界重试；每轮通过现有 worker 槽打开独立 Manager，完成/失败后关闭，再等待周期。窗口回调不等待 worker，入口已有的 Close→Wait 在事件循环外排空；取消的报告及旧 origin/owner 回填被丢弃。

**Spec:** `docs/REQUIREMENTS.md` F5/F7、`docs/DESKTOP.md`、已提交 `internal/subscription/watch.go`。仅 MyGo `ui` 原生组件，无 JS/Web/新依赖/CGO1。

## Scope and policy

- 写集：`internal/desktop/model.go`、`view.go`、`service.go`、新增 `watch*.go` / watch 相关 tests、`docs/DESKTOP.md` 和本文。2B 六文件、subscription/CLI/server/source/entry/module/workflows 不改；不 stage/commit。
- 默认关闭、仅本次进程显式启用；立即首轮，默认每 5 分钟、单轮 2 分钟；失败使用 Watch 的 30s 起有界退避，最长正常周期。同步全部本地已保存订阅（可能多个 origin），不是仅文本框对应的 Pack；界面明确提示。
- 使用现有 Sync preflight：本地修改/冲突/待恢复时拒绝覆盖，清单移除经内核备份，不因远端撤权自动清空本地内容。失败可能已有部分持久化，取消不等于事务回滚。
- 手动安装、Pack 操作在后台运行/停止排空期间禁用，方法层同样拒绝；已有槽保证真正互斥。浏览可继续，不隐式关闭 Watch。
- 明确停止、取消、换 origin、退出账号、关闭窗口均 cancel；停止中显示排空状态，不提前允许重启或本地写入。后台不等 UI 回调，排空不阻塞 UI。
- 固定安全状态摘要，不显示 raw error/token。当前安装内核仅 Linux，其他平台明确不可用，不冒称跨 OS 安装已通过。

## Implementation / verification

- [x] Backend TDD：新 WatchCycle 复用每轮 fresh Manager+Close；两个真实 Pack 共用技能，首轮/更新去重、撤权保留、本地修改保留、实际锁释放、取消。
- [x] Model/UI TDD：真实 MyGo Tester 点击启停、状态/禁用控件、延迟 worker、旧 queued report、origin/logout/Close/Cancel 排空；不在 UI 线程 Wait，不接受停止后旧结果。
- [x] 整合：保留原 headless/HTTP tests，CGO0 Go1.27.3 下 tests/vet/MyGo vet；Linux 真原生窗口 smoke（可用显示才运行，不冒称无显示为 native smoke）。负控必须真实行为 RED，隔离 HOME/DB/keys，不触真实 skills。
- [x] 精确冻结：只列本写集的 SHA256，以记录的 base 加选中路径计算 candidate Git tree（不操作 index）；报告命令/结果/边界并交独立验收。未具备证据的平台/产品门禁继续 pending。

实施在当前已授权共享 worktree，只写上述不重叠路径；最终隔离验证不包含并行 server/source/2B WIP。

实施起点 `02339276afe4e2ca71a8ecd4f42c515bb687d362`；交接采用已提交 base `88610c9c63db3274bbf5def2fbafada71eda732f` 加本文及 desktop 精确十路径。Git tree/逐文件 SHA 与执行证据在 `/tmp/cognate-desktop-watch-evidence/identity.json`、`source.sha256`、`REPORT.md`；这些实施者结果不是独立产品签收。
