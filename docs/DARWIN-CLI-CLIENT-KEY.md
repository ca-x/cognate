# Darwin CLI 客户端密钥：限定运行预览

## 当前状态

**原生运行 NOT_RUN；`DARWIN-CLI-PTY-01` OPEN。** 本文记录待验收预览，不代表完整 Darwin CLI、安装、Pack、Desktop 或原生配置支持。当前没有新的运行 ID、artifact 身份或运行通过结论。

CLI caller8 冻结包 SHA256 为 `56a48d03729d7487c94d0fb7b3932f146fba656f7acef7e1ad867e7047dca321`。它的非作者源码审报告 SHA256 为 `f5e69429a8ae32d1362f320a949b55ee488f693f389ec35199c2ea462ca0540c`，结论仅为 SCOPED_ACCEPT；Linux 机制测试和 Darwin 交编不等于 Mac 运行资格。工作流/文档组合以 `a6b7638660f0d1eefffe4c01140c7c65c4b53888` 为观察基线；真正预览必须另外绑定最终提交、树、工作流修订和具名 run/attempt，不能使用 caller8 原作者旧树冒充组合身份。

[既有密钥核心验收](DARWIN-CLIENT-KEY-CORE.md) 保留其原提交、run41121382674、core51 和当时五项产品 RED；本预览不改写那份历史记录。

## 预览要证明的边界

- 仅实际 Darwin arm64、Go1.27.3、CGO0 和本次实际创建/挂载/识别的两个独立 APFS 卷。
- 显式 `client-key init` 复用核心初始化及幂等语义；真实调用者的匹配 devfs PTY 或已有内存测试输出；不开放 pipe/FIFO/socket/普通文件重定向，也不借控制 `/dev/tty` 替代调用者的输出。
- `install-local` / `install-public` 先作真实密钥准入，但即使密钥有效仍返回 Unsupported；无自动初始化、源文件业务消费、网络、客户端数据库或 agent 发布。有效 legacy key 的登记和原锁元数据不是“完全只读 HOME”的承诺。
- 正常/部分/背压写、固定5秒上限、无 deadline 的取消、Close 排空、断开和 FD 重用均须有实际 Darwin 测试。取消/输出失败不能回滚已发生的初始化，也不能自动重试生成密钥。

## 具名运行的必需证据

`.github/workflows/platform-primitives.yml` 保留原五个正向产品 oracle 的字节，仅允许 `TestDarwinExecutionCLIRealPTY` 新变为 PASS0。CLIAdmission 仍因 Pack 关闭而普通 RED1；LocalInstall、DesktopServiceInstall、PackLifecycle 也必须保留原断言/标记的普通 RED1。不同错误、超时、skip 或其它产品意外成功都不能代替该合同。

core 角色仍为两个 APFS 卷各24项共用安全测试，加 CLI 保留产品边界1项和未改的 Desktop guards2项，共51个 RUN/PASS 事件。新 CLI guard 有新的保留产品语义，不能称其旧初始化隔离断言未变。

caller 专属角色在每个 APFS 的独立 TMPDIR 中启动新测试进程：实际 `TestDarwinClientKeyPTYIO` 可行性先于任何密钥操作；随后显式初始化/九类准入/SQLite 不变/物理锁取消、输出9项、原 CLIRealPTY 的真实主程序成功与幂等、`TestDarwinClientKeyPTYActualMainErrors` 的真实主程序错误路径。预期每卷25个 RUN/PASS 事件，总50项，含9个准入子测试；这与 core51 分开计数。部分写/取消等输出组件证据不冒充所有主程序信号/重定向组合。

成功运行必须同时提供旧 baseline、core 和新增 `darwin-cli-caller-security-RUN-ATTEMPT` 三份封闭 artifact。新增 caller artifact 为17个 payload 加 manifest，绑定实际 source/run/workflow、构建前后代码二进制身份、逐卷文件系统/临时目录身份、全部测试回执、真实 detach 与清理。未知/遗漏/重复/跳过/泄露输出、未 join、身份变化或清理不确定时只能封存固定 refusal/status，不能上传原始终端、密钥、密钥摘要、账号/路径或磁盘工具原文。

有限 synthetic sealer 正反控和 actionlint/AST 仅验证工作流机制，不算 native 通过。主线程另行批准一个准确 push-run 或 dispatch，二择一；不得因结果迟到自动重复运行。

## 尚未签收

2026-11-01 主线已选择在 `0d6c59d` 后接入候选，并以该阶段一次 push 自动触发新验证；
不另外 dispatch 或重跑旧 run。当前组合保留既有 Web embed 前置及 detach 失败清理门，
Go/交编和工作流有限验证见 [验证记录](VERIFICATION.md)。本段不是实际 Mac 运行收据，
本文开头的 NOT_RUN/OPEN 在取得并独立核实新 run 的全部证据前不变。

当前真实 F_GETPATH/devfs/独立重开、借用 TTY flags/termios/可用性、两卷真实 core/caller/主程序、所有取消/Close/清理及 artifact 独立验收均待具名运行。源码/工作流静审不关闭这些门。

只有非作者核对实际 API run/job/attempt、源码与 artifact digest/成员/回执以及清理后，主线程才能更新本文的限定交付记录。其它 OS/架构/文件系统、真实安装/Pack/Desktop/native configuration、完整14-agent/Release 和断电恢复义务保持不变。
