# 离线归档缓存维护

当前主线提供 Linux legacy 数据布局的单归档校验和显式修复。校验已包含在
`server-v0.2.0`；修复是后继主线功能，不会改变旧发布标签。非 Linux 平台拒绝执行。

## 校验

停止服务后，明确指定数据目录和归档压缩字节的小写 SHA256：

```sh
cognate-server cache verify --data-dir /absolute/private/data --sha256 <sha256>
```

命令持 runtime 独占锁。在线服务尚未退出时等待，可用 Ctrl-C 取消。它核对私有文件权限、
完整 SHA256 和归档语义，不读取主密钥、不改数据库或归档。成功输出 `archive verified`。
不要把语义校验当成技能代码的安全审计。

## 显式修复

从可信备份等途径准备正确的本机归档；必须是当前用户所有、无组/其他用户权限的普通文件，
只有一个硬链接，最大 32 MiB，压缩字节的 SHA256 必须准确匹配原缓存对象。
命令不下载替代内容，不接收任意 HTTP 路径，也不会把另一版本技能伪装为原快照。

```sh
cognate-server cache repair \
  --data-dir /absolute/private/data \
  --sha256 <sha256> \
  --archive-file /absolute/private/replacement.tar.gz
```

- 先持 runtime 独占锁，再持实际缓存目录的原 `.cache.lock`。原锁必须存在、私有且只有一个硬链接，
  预检、实际取得的句柄和最终名字绑定同一 inode；缺锁时拒绝，不创建同名协调文件绕过旧锁。
  命令等待合作读写者退出，不绕过在线服务。
- 除 runtime 协调文件外不初始化缓存；必须已有准确格式的私有 `.cache-budget`。不提高或重写预算。
- 所有普通文件均计费，包括损坏目标、保留原件、有效和不完整 stage；仅排除 budget/lock。
  目录中的链接、特殊文件、子目录或不安全权限导致拒绝，不按“垃圾”处理。
- 目标正确时只报告 `archive already verified`，不交换或分配 stage；目标缺失时用 NOREPLACE 补回。
- 损坏目标以 Linux 原子 EXCHANGE 交换；旧 inode 和原字节保留在
  `archives/.repair-<sha256>-<repair-id>.tar.gz`。不支持原子操作时拒绝，不降级覆盖。
- 新 stage 以独占创建、0600 权限写入，完成文件 Sync/Close、内容/身份复查和目录 Sync 后发布；
  发布后再次校验、Sync 和身份复查，全部关闭成功才输出 `archive repaired`。
- `retained-artifact=true` 仅表示该 ID 对应文件当前存在；目标已经健康时，它不是历史提交收据，
  也不保证该文件来自哪次修复。命令不输出归档内容、token 或主密钥。

## 中断与显式续接

省略 `--repair-id` 时随机生成 32 位小写 hex，在任何归档写入前输出
`archive repair started; repair-id=...`。请保存 ID；开始标识不是成功收据。
后继检查或写入失败时，命令不会自动删除 stage、旧原件或自动交换回去。
出现 `archive repair not confirmed`，先 `cache verify`，不要自动重发。
实际修复完成后若最终状态输出失败，也返回该 uncertain 标记并保留原输出错误；
开始行输出失败则不进入修复。不要从终端没有显示成功推断归档未发布。

显式续接示例：

```sh
cognate-server cache repair \
  --data-dir /absolute/private/data \
  --sha256 <sha256> \
  --archive-file /absolute/private/replacement.tar.gz \
  --repair-id <original-32-lowercase-hex-id>
```

同 ID 的已有 stage 必须完整通过 SHA256 和语义检查，仍会重新 Sync/Close、预算与身份核验后发布；
“内容正确”不证明上次 Sync 已完成。目标已经健康时仅报告当前状态，不重放历史操作。
不完整 stage 会保留并拒绝；可用正确输入和新 ID 发起新操作，但旧残留仍计入预算。
空间不足时不会自动回收、删除原件或提高预算。

真实 SIGKILL 回归覆盖三个明确屏障：stage 写到一半、完整 stage 持久化后且交换前、实际交换后。
前者留下不完整 stage 并拒绝同 ID 续接；完整 stage 可续接；交换后目标正确且旧 inode 保留。
这是进程中断证明，不是断电、失效磁盘、任意时刻中断或恶意同 UID 写者的保证。

## 仍未提供

- 运行代、restore 或 intent 布局支持；检测到相关痕迹即拒绝，避免操作错误目录。
- 完整数据库恢复、对象引用重建、自动回收或保留期清理。不要按年龄删除归档。
- 远程修复 API、在线静默替换或用户技能代码的安全背书。
