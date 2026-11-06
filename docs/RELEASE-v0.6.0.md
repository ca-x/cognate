# Cognate OpenCode 导入更新

目标组件版本：Server/LazyCat 0.6.0、CLI 0.3.0、MyGo GUI 0.1.4。
发布完成与产物回执另行记录，本文不把计划版本号当作已发布证据。

- 服务端新增 cc-switch schema23/24 的受控 OpenCode v1 provider 选择导入：
  provider ID、npm/name、端点、header 引用、模型、上下文/输出上限和 modalities。
- 密钥和所有 header 原值不随来源迁移；只显示存在位和稳定凭据槽，保存为所有者
  私有资产后，需另行在配置编辑器补充凭据。不会执行 agent 或修改设备文件。
- Web 和 CLI 完整审阅；MyGo 候选与最终确认显示同样的全部安全字段及 presence，
  仍需独立最终批准。三类默认不选、未知字段整项拒绝、CAS/票据及 Unknown 不重放不变。
- 客户端支持严格的字符串键配置映射解码，拒绝重复 map key、未知字段和模糊标量。

## 升级

服务器与客户端需要配套升级：旧 CLI/GUI 无法解析 READY OpenCode 候选，可能拒绝
包含它的整个预览，而不只是该配置。没有能力协商或自动降级。服务端 Web 自动随包更新。
原数据库、主密钥、OIDC、管理员和部署目录沿用；无数据库或 RPC schema 变更。
升级前保留数据和主密钥备份；旧导入票据需重新预览，不能自动重放提交。

目标镜像 `ghcr.io/ca-x/cognate:server-v0.6.0`，Linux amd64/arm64，不更新 `latest`。
懒猫继续使用原 Exec 包装和设置向导，仅喵喵商店，官方商店关闭。
CLI/GUI 按独立 tag 构建，不扩大既有 macOS/Windows 运行资格或签名状态。

## 未完成范围

OpenCode v2、扩展/高级模型字段、其它 agent、MCP/prompt 来源导入、本机配置应用、
Pack 配置自动消费、完整备份恢复及完整产品/平台验收仍见 `TODO.md`。
本版是已验证能力的组件更新，不是全部 backlog 或完整 cc-switch 功能对等的完成声明。
