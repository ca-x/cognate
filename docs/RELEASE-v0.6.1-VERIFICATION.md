# 0.6.1 维护验证与发布记录

## 范围

本轮仅发布 Server 与懒猫 Exec 包，修复导入可用性、大库预览、OIDC 会话名称，
补充 GitHub token 说明及管理员在线备份设置。CLI 0.3.0 与 MyGo 0.1.4 不重发。
Server 与懒猫发布流水线均成功，喵喵接受新版本；不可把分发等同于真机验收。

功能源码固定 `b993ed59f594a935be056a5194579e528d468262`，不可变
`server-v0.6.1` 与远端相同。懒猫包装不可变 `v0.6.1` 固定
`f5a32533deaafb149a4fb14229e5467a8594f367`，准确 pin 上述源码。均已 SSH 推送，
未移动任何旧 tag；后续验证记录提交不改变发布构件源码。

## 开发验证

- Go 1.27.3、CGO0 全仓测试与 vet、Web TypeScript/Vite build 通过；最终源码已再次完整复跑，
  `go-release-final.log`、`vet-release-final.log`、`web-release-final.log` 均成功。
- Buf lint、相对原 HEAD 的 breaking 检查与全新目录 Go/Connect/TS 生成比对通过。
  唯一精确排除为手写 `backup_redaction.go`，其它生成代码仍须逐字一致。
- CGO0 Linux arm64、Windows amd64、Darwin arm64 交叉构建通过，不扩大原有运行资格。
- 导入真实浏览器验证包括 Magpie 60 项、cc-switch/OpenCode 中英、深浅主题和窄宽屏。
  取消重勾通知仍须显式完整确认，按原始预览顺序提交；缺少任何确认均拒绝提交。
- 获用户授权，只读验证其本机 cc-switch schema24 数据库：75 仓库、81 技能、18 配置
  完整 HTTP 预览成功。零提交、零状态查询、零上游请求；原数据库及 sidecar 的内容、
  时间与存在性不变。私有临时快照及 runtime 已删除；真实数据、日志与凭据不入 git。
  其中 2 项配置类型、16 项配置字段仍不支持，不能宣称全部配置均可导入。
- 备份在线设置 13 项真实浏览器检查通过：从未启用状态热启用、修改自动周期、关闭、
  保留凭据重新启用、8 组主题/语言/375 与 1440 布局及普通用户拒绝访问，全程不重启。
- 完整备份页面另通过 88 项行为与 56 项布局检查，包括 320/375 宽、200% 字号、中英、
  深浅主题、焦点、确认、任务与错误状态。修复长确认文案撑宽，不以禁用布局断言替代修复。
- 专项覆盖加密文件原子发布、revision CAS、活动/未明确任务拒绝重配、失败隔离、
  自动调度 revision、单数据目录所有权及关闭后可重新接管。独立复核无确定 P1/P2。
- 最终 clean 源码另通过懒猫 root 首次安装、升级保留原账号和主密钥、卷链接、只读卷及
  祖先权限完整启动 suite；不修改原 manifest、wrapper、安装向导或密钥安全校验。

源码 Build `42185643203` attempt 1 的全仓测试失败：既有
`TestS3RestoreHeadCancellationStages/stage=3/deadline` 未到达测试 barrier。
该测试及其 250ms deadline 本轮未修改；本机普通与 `GOMAXPROCS=1` 各连续 20 次通过，
原始失败日志保留为 `source-build-ci-failed.log`。仅对同一源码发起一次失败 job 重跑，
不修改或跳过断言、不增加自动重试；不能据后来成功断言该历史时序问题已根治。
attempt 2 最终 completed/success，含完整 Go/Web/Buf 测试、六平台交叉编译和两个
Docker 架构。按此成功门禁创建上述发布标签。

本机证据目录为 `/tmp/cognate-import-patch` 与 `/tmp/cognate-import-review-20261106`；
日志、截图及回执不随源码分发。早期失败保留，最终完整备份页面证据为
`backup-web-accessibility-final`，导入为 `magpie-post-final`、`opencode-post-final`。

## Server

Server run `42189151917` completed/success，含共享 Go/Web/Buf、双架构容器和原 OCI
构件提升，以及最终按 child digest 拉取和执行的验证。未更新 Docker latest，未另外创建
GitHub Server Release；CLI/GUI 不重发。

镜像 `ghcr.io/ca-x/cognate:server-v0.6.1`：

- Index：`sha256:c64687e4e6c73fcdfd3efab709431f6467bcd6061592bc19353ea605574a4710`
- Linux amd64：`sha256:5c8dfba7ab420f13b18dda5273fee508c3ff5cc1e0def704f8dcc483d9d20555`
- Linux arm64：`sha256:96b76e007559bb2ce48050d28096cd8ca68b98c3c7ad3c900b6bfee87cc37e1f`

本机另独立匿名读取三个 manifest，逐字 SHA256 与响应 digest 一致，index 恰含两个
Linux 架构。Platform run `42189151683` completed/success；不据此关闭历史 Darwin
失败记录或扩大原生客户端运行资格。不移动旧标签，官方商店保持关闭。

## LazyCat Exec

懒猫 run `42189169910` completed/success，含发布前 root 首次安装、升级与权限启动门禁。
公开非 draft、非 prerelease 的 GitHub Release 为 `v0.6.1`。

最终资产 `community.lazycat.app.cognate-v0.6.1.lpk`：**78,883,328 字节**，SHA256
`36bf5f1888c5d677bea29c6bca8485919c781d3bcad2f05d56d245ee90e89e78`。
完整下载后与 GitHub API digest 一致，独立核对外层五项、content 七项白名单、root 属主、
部署参数、manifest、图标、wrapper、全部许可和 SOURCE_COMMIT。最终 ELF SHA256
`c8e71420bdf5702583c504792af23499bd3aee3b8e29d61b3362325c75edc89b`；
Go 1.27.3、Linux amd64、CGO0、准确 source revision 与 clean VCS metadata 均通过。
对最终下载解包的 binary/wrapper 再次执行完整隔离 root 启动 suite，全部通过；日志为
`lpk-final-startup.log`，不以候选二进制代替最终实包验证。

喵喵接受 app `670` / version `4261`，published=true、skipped=false；准确引用上述
最终资产 URL 和 SHA256，已与本机完整下载逐字核对。回执 `onlineVersion=0.6.0`，
不能声称店面已经切换到 0.6.1。仅发布喵喵商店，官方关闭，真机升级仍待用户测试。

## 未完成边界

支持在线备份的 Linux 同一数据目录现在仅允许一个服务端，即使备份关闭也是如此；
不同数据目录独立。独立恢复密钥仍须部署者提供已有的私有 32 raw-byte 文件，不能用
Web 保存成功代替远端 S3 授权或完整恢复成功。完整恢复、外部供应商、懒猫真机升级
与平台 OIDC 待独立验证；完整产品 backlog 不因该维护版本勾选完成。
