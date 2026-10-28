# 客户端 Pack 订阅与本地应用

本页描述当前实现和边界。服务端/Web Pack 已通过上一轮独立验收；本轮客户端整合及下载器补充修复仍在独立复验，不代表整个 F5、M2/M4 或首发已验收。

## CLI

```sh
skillhub pack subscribe --server https://your-skillhub.example --token-stdin
skillhub pack list
skillhub pack sync
skillhub pack remove SUBSCRIPTION_ID
skillhub uninstall-local example
```

分享凭据仅从标准输入读取，不能通过命令行参数传入；可将密码管理器输出经管道交给命令。不要将令牌写入 shell 命令或脚本。服务端必须是 HTTPS origin，开发环境仅显式 loopback 允许 HTTP；不跟随重定向，不将凭据发往 GitHub。

`subscribe` 先验证 manifest 并保存订阅，然后同步全部订阅；失败会明确提示订阅已经保存，不能误认为回滚。`remove` 先保存待移除状态，再统一清理；若其他订阅失权或本地内容变化，清理可能暂缓，原有状态仍在。`list` 只显示标识、名称、来源和版本，不显示凭据；尚无状态时不会创建目录。

本轮安装/更新/卸载仅支持 Linux 非 WSL。macOS、Windows、WSL 的原生运行及 copy/junction 适配仍未完成，交叉编译不代表这些平台可用。

## 数据与密钥

- SQLite 位于 `~/.local/share/skillhub/client.db`，与已有安装 journal、安装映射和备份记录同库。
- 独立 32 字节根密钥默认位于 `~/.config/skillhub/client.key`，首次订阅自动创建，仅当前用户可读写，不接受软/硬链接。它不写入 SQLite，也不输出到日志。
- 分享原文经独立 HKDF 域派生的 AES-GCM 加密，AAD 绑定订阅 ID、服务器 origin 和 Pack ID；SQLite/WAL 不保存原文。公开状态 DTO 不包含密文或原文。
- 已有订阅时密钥丢失不自动生成替代；必须恢复原密钥。密钥损坏、权限过宽、归属不符或解密不匹配会拒绝操作，不能假装空数据库继续。
- 复制数据库用于续接时，需单独安全保管和恢复密钥；只备份数据库不能解密订阅。不要把根密钥放进公开仓库、分享内容或普通日志。

## 同步顺序

1. 持有与本地安装一致的跨进程 session 锁，先拒绝未恢复的 journal 操作。
2. 通过配置的 SkillHub 服务端读取所有启用订阅的 manifest；完整核验 metadata/hash，核对 Pack ID，并构造所有订阅的 desired 并集。
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
- 当前客户端同步是显式命令；持续后台自动同步、源仓库内容替换、配置附件/应用、MyGo 原生界面等尚需后续接线与验收。独立匿名公开安装已接 `install-public --server ORIGIN --skill ID` 并在单独整合验收；它不使用或放宽本节 Pack 凭据，也不被认领为订阅管理的安装。
