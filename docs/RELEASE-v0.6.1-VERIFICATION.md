# 0.6.1 维护验证与发布记录

## 范围

本轮仅发布 Server 与懒猫 Exec 包，修复导入可用性、大库预览、OIDC 会话名称，
补充 GitHub token 说明及管理员在线备份设置。CLI 0.3.0 与 MyGo 0.1.4 不重发。
发布尚未完成；不可把本地验证等同于远端分发或真机验收。

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

本机证据目录为 `/tmp/cognate-import-patch` 与 `/tmp/cognate-import-review-20261106`；
日志、截图及回执不随源码分发。早期失败保留，最终完整备份页面证据为
`backup-web-accessibility-final`，导入为 `magpie-post-final`、`opencode-post-final`。

## 发布门禁与边界

源码提交、远端 CI、Docker digest、LPK 完整下载及商店回执待发布后补录。
不移动旧标签、不更新 Docker latest，官方商店保持关闭。

支持在线备份的 Linux 同一数据目录现在仅允许一个服务端，即使备份关闭也是如此；
不同数据目录独立。独立恢复密钥仍须部署者提供已有的私有 32 raw-byte 文件，不能用
Web 保存成功代替远端 S3 授权或完整恢复成功。完整恢复、外部供应商、懒猫真机升级
与平台 OIDC 待独立验证；完整产品 backlog 不因该维护版本勾选完成。
