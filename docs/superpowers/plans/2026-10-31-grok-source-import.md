# Grok Build 来源导入

范围：cc-switch schema24、参考源码 `6d6b7162f122ac0ce1356a14688c8c4f712db3c8` 的显式单模型 TOML。仅导入服务端配置资产，不启用本机应用。Magpie 配置仍按原边界拒绝。

- 严格解析 `[models].default` 和唯一 `[model.<profile>]`，保留七字段、env_key 存在位与 int64 窗口；空配置、多模型、未知字段、错误引用与类型一律 unsupported，不按上游 UI fallback 补值。
- api_key 仅投影 slot 存在位，不导入源凭据；后续通过已有配置密钥独立授权入口设置。
- 接通投影、固定 moejs adapter、审阅票据逐字段绑定、提交与 Grok envelope，禁止类型混合或误编码为 Claude。
- 接通 Connect 映射、CLI/Web 预览验证和回执；完整展示安全字段与密钥省略警告。
- 先失败测试，再实现；验证 parser 边界、最大整数、票据字段变化、真实 SQLite commit/read、协议与客户端审阅。
- 更新验收文档、TODO 和跨机器记忆；测试后独立提交、SSH 推送及读回，不自动变更现有发布标签。
