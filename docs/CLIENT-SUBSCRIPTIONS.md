# 客户端 Pack 订阅与本地应用

本页描述已独立验收的 Linux 客户端订阅、显式/持续同步与 MyGo 生命周期接线，以及服务端公开来源定时更新的衔接。不代表私有来源、配置附件/应用、完整跨平台安装/恢复或整个 F5、M2/M4、首发已验收。

## CLI

```sh
cognate client-key init
cognate pack subscribe --server https://your-cognate.example --token-stdin
cognate pack list
cognate pack sync
cognate pack watch --interval 5m --attempt-timeout 2m
cognate pack remove SUBSCRIPTION_ID
cognate uninstall-local example
```

分享凭据仅从标准输入读取，不能通过命令行参数传入；可将密码管理器输出经管道交给命令。不要将令牌写入 shell 命令或脚本。服务端必须是 HTTPS origin，开发环境仅显式 loopback 允许 HTTP；不跟随重定向，不将凭据发往 GitHub。

`client-key init` 在执行命令前检查标准输出，限制也覆盖 `--help`：原生输出须为具备所需 deadline 能力、可独立重开的 Linux 终端或 pipe/FIFO；普通文件及 socket 型捕获不受支持。需保存提示时可使用 `set -o pipefail` 后运行 `cognate client-key init | tee init-status.txt`，检查 CLI 自身退出状态，不要只检查管道最后一个命令。该限制不改变下述 Watch 的普通文件输出合同；初始化后的输出失败也不代表密钥写入已撤销，应核实后显式重试。

`subscribe` 先验证 manifest 并保存订阅，然后同步全部订阅；失败会明确提示订阅已经保存，不能误认为回滚。`remove` 先保存待移除状态，再统一清理；若其他订阅失权或本地内容变化，清理可能暂缓，原有状态仍在。`list` 只显示标识、名称、来源和版本，不显示凭据；尚无状态时不会创建目录。

本轮安装/更新/卸载仅支持 Linux 非 WSL。macOS、Windows、WSL 的原生运行及 copy/junction 适配仍未完成，交叉编译不代表这些平台可用。

## 持续同步

`pack watch` 在前台运行，立即同步一次，之后从每轮完成时起等待 `--interval`（默认 5 分钟，范围 1 秒–24 小时）。每轮受 `--attempt-timeout` 限制（默认 2 分钟，范围 1 秒–10 分钟）；不会重叠执行或补跑积压的定时任务。Ctrl-C 取消在途操作并退出，不创建隐式系统服务或自动启动项。

每轮都重新打开并关闭订阅管理器；进入等待或输出结果前已释放安装锁，其他手动安装命令可以在空闲期运行。同步过程中仍使用原有跨进程锁，不绕过 journal、备份、去重或本地修改检查。

失败后从 `min(30 秒, interval)` 开始退避，连续失败加倍，上限为 interval；成功后重置。循环只输出固定失败提示、连续次数和下次延迟，不打印原始上游错误、分享令牌或部分成功计数。可停止 watch 后运行 `pack sync`、`install-status` 检查；有待恢复操作时仍保守拒绝，不因重试自动清理现场。共享调度器要求同步回调响应 context，不能强制打断任意不合作的内核调用。

Linux 原生 stdout 管道/终端使用独立重开的非阻塞描述符：单次写入最多等待 5 秒，取消会唤醒在途写入并等待取消回调收尾，不遗弃写入 goroutine。caller 的描述符、flags、deadline 不被改动或关闭。普通文件保持原 offset 与所有权，只在同步写前后检查 context；**不保证打断不可中断的内核磁盘 I/O**，取消或短写也不表示没有字节写出。输出超时、短写或失败会以固定错误停止 watch，不回显底层错误。

嵌入 CLI 时，输出仅接受原生文件、标准内存 buffer/builder，或显式实现且遵守 `WriteContext(context.Context, []byte) (int, error)` 的合作式 writer。未知的纯 `io.Writer` 会在访问客户端存储前被拒绝，不能靠私有字段包装假定其可取消；共享调度器的 report 回调也须响应传入 context。原生输出依赖 Linux `/proc/self/fd` 和实际 deadline 能力，能力缺失时明确拒绝，不静默退化为不可取消的管道写入。

watch 会获取服务端已经提供的新 Pack manifest，但**不负责让服务端重新扫描源仓库**。源替换使用独立的手动刷新或服务端周期 worker，见 `REPOSITORY-LIBRARY.md`、`SOURCE-REFRESH.md`；MyGo 原生启停及关闭/换号排空见 `DESKTOP.md`。OS 后台服务、自启动和自动恢复仍未交付。

## 数据与密钥

- SQLite 位于 `~/.local/share/skillhub/client.db`，与已有安装 journal、安装映射和备份记录同库。
- 独立 32 字节根密钥默认位于 `~/.config/skillhub/client.key`，只由显式 `cognate client-key init` 或 MyGo“账号 → 本机设置”的初始化动作创建，仅当前用户可读写，不接受软/硬链接。初始化不访问网络、不登录服务端，也不创建业务数据库。首次订阅不再自动创建密钥；订阅和同步每次重新加载已有密钥，且必须先通过准入才会打开业务数据库或请求服务端。密钥不写入 SQLite，也不输出到日志。
- 分享原文经独立 HKDF 域派生的 AES-GCM 加密，AAD 绑定订阅 ID、服务器 origin 和 Pack ID；SQLite/WAL 不保存原文。公开状态 DTO 不包含密文或原文。
- 没有任何既有状态时，缺少密钥会提示先显式初始化；存在初始化标记或客户端数据而密钥丢失时须恢复原密钥，不会生成替代，即使没有保存订阅也是如此。密钥损坏、权限过宽、归属不符或解密不匹配会拒绝操作，不能假装空数据库继续。再次显式初始化会核实并保留同一密钥，不会轮换它。
- 复制数据库用于续接时，需单独安全保管和恢复密钥；只备份数据库不能解密订阅。不要把根密钥放进公开仓库、分享内容或普通日志。

## 同步顺序

1. 持有与本地安装一致的跨进程 session 锁，先拒绝未恢复的 journal 操作。
2. 通过配置的 Cognate 服务端读取所有启用订阅的 manifest；完整核验 metadata/hash，核对 Pack ID，并构造所有订阅的 desired 并集。
3. 同 slug/hash 仅保留一个下载/安装目标；同 slug 不同 hash 在任何文件变更前失败。最多 32 个订阅、每个 256 项、合并最多 1024 个安装目标。
4. 对本地记录、实际 canonical 内容、对象身份和全部投影进行预检；未记录的目标或非订阅安装的不同内容不能被覆盖。
5. 事务/CAS 保存 desired revision/hash 与成员，保留此前 applied 状态。下载失败或中途只成功一部分不推进 applied revision。
6. 逐项下载至私有临时目录，要求正常流结束、连续 offset、成功 final、传输 SHA256、安全单 gzip/tar 与逻辑 hash 全部匹配。已完整匹配的安装不重新下载。
7. 新安装复用现有 journal，并在同一个完成事务中记录订阅管理归属；更新先完成并验证旧版本备份/卸载，再安装已核验的新快照。下载失败不会先移除旧版本。
8. 对不再被任何 desired 订阅引用、且确实由订阅创建的安装备份卸载；复用的既有本地安装不被“认领”，取消订阅不删除它。
9. 最后再次核验所有 desired 实际安装，在同一事务中推进 applied 版本并清理已移除订阅。失败保留 desired/applied 差异，可重试；完整安装成功前不报应用完成。

## 恢复与限制

- 多技能同步不是文件系统级原子事务：网络或磁盘失败可能只完成一部分，但每个新安装的归属与 journal 持久化，重试复用已完成部分，不重复下载。
- 更新的旧版本备份会保留；若卸载后安装失败，下一次可重试或显式恢复备份。自动回滚/自动续跑中断删除尚未实现，不能把保守 `needs_attention` 当作恢复完成。
- `install-status --reconcile` 不删除现场，只取消未开始操作或标记中断记录。不要直接手工删除 journal/锁/数据库来掩盖未完成操作。
- 本地内容或投影被用户修改、替换或权限不可信时拒绝覆盖/卸载；最终路径操作仍依赖文档中的可信稳定路径假设，不承诺对同 UID 恶意并发改名者提供内核级条件 unlink。
- CLI 支持显式同步和前台持续同步；MyGo 默认关闭后台同步，显式启用后立即首轮，每轮完成后等 5 分钟再同步全部已保存订阅。停止/换地址/退出/关闭均取消任务并由后台实际排空，UI 不等待 worker，排空前不允许重启或手动本地写入。当前实际安装内核仍仅 Linux；私有来源、配置附件/应用及完整恢复仍需后续交付。独立匿名公开安装已接 `install-public --server ORIGIN --skill ID` 并在单独整合验收；它不使用或放宽本节 Pack 凭据，也不被认领为订阅管理的安装。
