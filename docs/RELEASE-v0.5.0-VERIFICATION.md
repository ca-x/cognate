# 0.5.0 验证与发布记录

## 本地验证

- 本轮范围：相对于 `218e570f183735cb6c8d6a0263abeab817753836` 的 Web 交互与布局更新，不恢复已停止的完整产品 backlog。
- Go 1.27.3、Node 26.12.0，`CGO_ENABLED=0 GOTOOLCHAIN=local`；Web build、全量 `go test ./...`、`go vet ./...` 退出 0。
- 真实 TLS/SQLite 仓库浏览器回归：74 条证据、32 组布局，通过默认私有、发布/取消公开、CAS、Unknown 禁重放、归档损坏、分类和跨用户隔离。新增导航意图只加载代码、弹层草稿保留、关闭焦点恢复及窄屏 200% 检查。
- 登录浏览器回归：8 检查、49 张截图、32 组布局通过。
- Grok 配置浅/深色真实浏览器回归通过，新增名称/Agent 筛选与清除、375/1440 布局；深色补充请求记录断言筛选不读取草稿、不写资产。原有凭据、复制、CAS 与提交后丢回执覆盖保留。
- Magpie 加密导出真实浏览器回归通过：指针与键盘展开、错误密码不上传、正确密码得到真实服务预览、密码/排除内容不传输，375/1440 布局通过。
- npx skills 完整真实浏览器回归：35 条证据、16 组布局，fixture 正常退出；保留私有 canary、显式绑定、确认、Unknown、跨 owner 与清理检查。
- 所有夹具使用临时合成数据，不读取或提交用户真实数据库、导出及凭据。原始日志和截图在本机 `/tmp/cognate-ui-050`，该目录不是远端备份。
- 用户代表 agent 对冻结变更与上述证据只读审查，未发现有证据支持的 P1/P2 阻断；未重跑测试，不代替完整产品或发布验收。

## 发现与定位

1. 仓库及 npx 截图脚本原先禁止任何密码输入框出现，0.4.0 新增空 Magpie 密码框后会误失败。现在仍拒绝任何非空密码框；不会清空真实输入来让截图断言通过，私有 canary 检查保留。
2. npx 测试的旧 Magpie 来源文案不再对应 0.4.0 格式，更新为加密备份与兼容 library.json 的准确标签。
3. 浏览器工具对折叠 summary 的高级 click 会先展开再点击，从而关闭它；低级真实指针 down/up 与键盘 Enter 均能正常展开。测试保留实际指针及键盘检查，不用 DOM click 或 force-click 绕过。
4. Grok 原测试在默认小视口点击长弹层里的 label 时命中遮罩，旧二进制同样复现。改为通过 label.control 定位关联控件并正常滚入视图；未修改生产遮罩、未 force-click 或增加固定等待。

## 发布状态

不可变 `server-v0.5.0` 指向 `66c8d76215f8696180a992e5cb5a1f0f5c7eb3b8`。
源码 Build `41814073679` completed/success，包含六平台/架构交叉编译。
服务端 release `41814175849` completed/success，含全量 Go/Web/Buf、两架构容器及最终镜像验证。
`ghcr.io/ca-x/cognate:server-v0.5.0` index：
`sha256:f03d0e4503fa71c93cdcb67b6130793c27ebbe643a731cf1765175e60342b917`。
本机独立匿名读取 index 与 Linux amd64/arm64 两个 child manifest，逐字 SHA256 与响应 digest 一致；
远端最终门禁另外拉取并执行两个架构二进制。不更新 `latest`，不重发 CLI 或 GUI。

独立 Platform filesystem primitives `41814175840` 的 Linux/Windows/macOS 三组原语通过；
Darwin baseline observation 的原生能力/产品基线步骤通过，但 cleanup 与 evidence seal 失败。
本轮未修改这些原语或观测脚本，不把该 run 报成全部通过，也不扩大 Darwin 客户端安装资格。
这不是 Linux 服务端/Docker/Exec LPK 的发布门禁；未为本次 Web 更新修改或放宽它。

懒猫包装 `c712b3a48f0d9e69754519349ac7c2a4b002ef3c` 固定上述源码。已通过本地 LPK lint、
CGO0 build、七项 content 逐字核验和隔离 root 首次/升级/密钥/卷权限启动 suite。
本地包不代替最终 CI 资产。不可变 `v0.5.0` 指向上述包装 commit；LazyCat run `41815749938`
completed/success，包含 runner root 启动门。公开正式 Release `433794212` 非 draft/prerelease。
最终资产 `community.lazycat.app.cognate-v0.5.0.lpk` 的 API 元数据为 **78,657,024 字节**，
SHA256 `4e8914fccf12d008c575e3e45c6ac39263f850f2d5eb8f90c67982849ea05ea0`。
完整下载的字节数及整体 SHA256 与上述 API 元数据一致。对最终包核验外层五项、content 七项
白名单、属主、权限、全部许可、原 wrapper、SOURCE_COMMIT，并逐字比对本地固定源码构建。
最终 ELF SHA256 为 `bdff551dad3a0963bf176372bfaac11535df4b180fa188ed8f40d84f249e3a57`，
Go 1.27.3、CGO0、Linux amd64、准确 VCS revision 与 clean metadata 均通过。
针对最终下载解包内容再次运行完整隔离 root 启动 suite，首次管理员、升级保留原账号/密码/密钥、
丢失或不安全密钥拒绝、卷链接、只读卷与祖先权限边界均通过。
最终下载位于 `/tmp/cognate-ui-050/final-download/verified-complete.lpk`，启动证据为
`/tmp/cognate-ui-050/final-lpk-startup.log`；本地路径不是远端备份。

喵喵接受 app `670` / version `4128`，published=true、skipped=false；回执中的准确最终资产和
SHA256 与上述 API 元数据一致。回执 `onlineVersion=0.4.0`，不代表店面已切到 0.5.0。
官方商店保持关闭；用户盒子的真机升级与平台 OIDC 仍待用户验证。

本轮 GitHub status 曾报告 Minor Service Outage；SSH Git 传输公钥认证失败，HTTPS push 亦有
remote Internal Server Error。使用现有 gh 凭据 helper、读取远端后重试完成源码推送，未修改密钥或持久上游。
包装 tag 的 Git push 连续失败后，经 API 创建指向同一已推送 commit 的轻量 ref；没有移动已有 tag。

最终资产下载通道约 50 KiB/s，第一次尺寸检查在下载结束前执行并拒绝继续；没有将部分文件冒充最终包。
随后使用官方资产的 8 个有界 Range 补齐其余字节，逐段校验 Content-Range 和长度，再对完整文件做
整体 SHA256 校验；通过后才解包、比对和执行隔离启动检查。
