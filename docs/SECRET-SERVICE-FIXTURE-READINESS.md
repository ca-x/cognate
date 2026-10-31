# Secret Service 私有 broker 就绪握手

## 本次失败与确定性证据

最终组合的首次隔离全仓测试为 **7910 PASS / 166 SKIP / 1 FAIL**；失败是 `TestValidReplyDoesNotBypassWaitAndCancellation` 的 `fixture-connect`，不是旧等待断言或 `fixture-open-session`。记录 `/tmp/cognate-integrated-release.JWDjDE0I/final-full-go.json`，保持原失败，不用后继绿色覆盖。

原 fixture 只等待 Unix socket 路径出现，随后立即连接。真实 `dbus-daemon` 的 `bind` 先创建路径，`listen` 尚未完成时，这个检查就可能放行。为验证机制，用 strace 仅将真实子进程的 `listen` 入口延迟 200ms，不修改 syscall 结果、业务逻辑或连接预算。旧二进制的同一测试稳定失败于 `fixture-connect`，trace 顺序为 `bind=0` → 尚未完成的 `listen` → `connect=-1 ECONNREFUSED`。

这是受控真实 syscall 时序反例，不是模拟连接错误；初次全仓日志没有 errno，因此不宣称它或所有历史故障均有完整 syscall 归因。旧 CI 的 `fixture-open-session`、其它等待断言仍保留各自 UNKNOWN 边界。

## 最小修复

仅改变 Linux 测试 fixture，不改产品 Secret Service、认证、密钥、CLI/GUI 行为或生产依赖：

- broker 使用 `--print-pid=1`，stdout 接继承的私有匿名管道；stderr 继续丢弃，GNOME keyring 的 stdout 仍丢弃。
- 保留 3 秒就绪预算，等当前子进程准确 PID 的完整一行，再验证 socket 类型。最多读取 32 字节，不记录帧或错误原文。
- 取消时关闭管道并等待关闭回调结束；缺失、错误、截断、超长帧与取消均拒绝。管道和子进程组继续由测试清理，不接触用户会话 bus。
- 不重试 connect、D-Bus RPC 或业务写入，不增加 sleep、不延长预算、不弱化等待/取消/隐私断言。

系统随附 `/usr/share/man/man1/dbus-daemon.1.gz` 的 `--ready-event-handle` 章节明确说明 Unix 可等待 `--print-address`/`--print-pid` 的继承 FD 输出来取得同类就绪通知。官方页面也已实际下载为证据目录中的 `dbus-daemon-official.html`，SHA256 `d775257e6eb51ccfed185532280af6d4a86cf9d26accbd664ff9f28afd29ce5b`。先前聊天中未成功取得的网页引用已撤回，不作为证据。

## 验证与复现

Go 1.27.3，所有 Go/test binary 命令显式 `CGO_ENABLED=0`。证据目录为 `/tmp/cognate-integrated-release.JWDjDE0I`。

| 验证 | 实际结果 |
| --- | --- |
| 原测试、旧二进制、真实 listen 延迟 | exit1，`secretservice-listen-red.log/.trace` |
| 同测试、修后二进制、相同 listen 延迟 | exit0，`secretservice-listen-green.log/.trace` |
| 持久 bound-but-not-listening、帧和取消回归 | `-test.count=30` exit0，`secretservice-readiness-unit.log` |
| 从正确 package 工作目录、隔离 HOME/XDG/TMP、只读离线 module cache 的整包测试 | `-test.count=5` exit0，`secretservice-package-corrected.log` |

初次直接从仓库根执行整包 test binary，使其相对路径的 shipping build 找不到正确 module，并且没有交接私有 module cache，产生 `fixture-build-failed`。这是编排错误，保留 `secretservice-package.log`，不称产品 RED；修正 cwd/缓存后才取得上述整包结果。

```sh
export CGO_ENABLED=0
go test -c ./internal/secretservice -o /tmp/secretservice.test
strace -f -e trace=bind,listen,connect -e inject=listen:delay_enter=200000 \
  -o /tmp/secretservice-readiness.trace /tmp/secretservice.test \
  -test.run '^TestValidReplyDoesNotBypassWaitAndCancellation$' -test.v -test.timeout=30s
CGO_ENABLED=0 go test ./internal/secretservice -run '^TestFixtureBrokerReadiness' -count=30
CGO_ENABLED=0 go test ./internal/secretservice -count=5
```

受控 trace 只收 bind/listen/connect，不收密码、响应、环境或消息正文。全部凭据由私有 fixture 合成。修后最终组合全仓为 7919 PASS / 166 SKIP / 0 FAIL，详情见 `VERIFICATION.md`。

非作者 Hilbert 在新的基线 archive 中独立编译新旧二进制，以相同的真实 listen 延迟分别取得 RED/GREEN，并验证分片完整帧无需 EOF、取消关闭读端、边界长度和错误 socket 类型，给出局部 Spec ACCEPT / Quality ACCEPT、无新增阻断。报告 `/tmp/cognate-ss-readiness-review.hHqqVROq/work/REVIEW.md`，SHA256 `45206faa97ea887bd56e5501628256f28f117b41adfcada770d674fefef48113`。独审没有重复全仓，也没有宣称原当轮或其它历史故障已全部归因；本修复不关闭正式版或其它平台验收。
