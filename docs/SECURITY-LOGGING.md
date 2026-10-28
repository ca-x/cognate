# Sensitive data and logging

Cognate 的 token、密钥、replacement、原始上传、配置全文和敏感 diff 不得进入日志、错误详情、URL、提交信息或浏览器持久化。本文是开发边界，不是全仓信息流分析或“已证明任何路径都不泄漏”的声明。

## 只记录明确的安全状态

- 日志只接收单独构造的白名单状态：独立随机事件 ID、固定阶段/结果码、受控枚举、布尔 presence、计数与耗时。身份凭据、proof、commitment、token hash、含秘密文件 hash 和任意用户输入都不能因为“是 metadata”而自动进入白名单。
- 不将请求/响应、domain 对象、service/client/model、错误链或数据库行整体传给 fmt、slog、body tracing、调试 dump 或 crash reporter。用户名称、endpoint、仓库 URL、MCP 参数及导入文件也可能含秘密。
- 对外错误使用固定分类和文案，不原样回显上游 body、SDK 错误、含 query 的 URL 或原生配置。取消或超限不能切换到打印原始输入的诊断分支。
- 日志字段不得借显式 Reveal、CopyBytes、字符串插值、预先 fmt 或 JSON 序列化绕过限制。允许授权传输/加密/持久化代码显式取得必要 bytes，不等于允许日志记录。

## Generated DTO 与既有 wrapper

原始 Protobuf/Connect DTO 的 String/JSON/slog 不保证脱敏，`debug_redact` 不代替调用方约束。普通配置编辑 API 必须继续使用 metadata、presence 与显式安全视图；原始 replacement 或未来 materialization DTO 不进入通用日志。

2026-10-29，Go 1.27.3 下对以下五种既有 wrapper 的合成 probe 确认了有限边界：

- `auth.SessionToken`
- `packs.ShareToken`
- `mcpaccess.Secret`
- `github.Token`
- `configuration.Secret`

这些类型的常用直接字符串格式与既有直接日志测试成立，但非字符串 fmt 动词、私有字段外层及真实 TextHandler 可暴露合成材料或可逆 byte 编码。私有字段被 JSON 忽略也不是递归脱敏；先 fmt 再交给 JSON logger 仍可能带入秘密。**它们不能作为通用 fmt/slog 保密边界，不得据 Stringer/GoStringer/LogValuer 的存在批准整体对象日志。**

该只读侧查没有证实当前生产调用已把真实 token 写入日志，也没有证明全仓无此路径。五类型的完整格式化 hardening 尚未实施；未来若要依赖更强 wrapper 合同，必须先实现并独立验证，不能把本政策当作已经修复。发现实际 sink 时仍须修复和补回归，不得仅增加免责声明。

## 新增敏感类型与验证

新增敏感类型不得只复制旧 Stringer 惯例。若声明默认格式化安全，验收必须覆盖所有受支持动词、flags/width/precision、值/指针、私有多层容器及实际 slog Text/JSON handler，并检查可逆编码，而不只搜索明文。零值和显式 copy 的语义也须验证。codec 已签收的强合同不因上节 legacy 限制而降低；主动 unsafe/反射取得秘密或显式输出原始 bytes 不属于默认格式化保护。

本轮 Claude codec 的两次拒签已说明只增加 Formatter 或单层指针并不足够。最终表示及独立退化负例见 `VERIFICATION.md`；其它类型不能自动继承其 PASS。

服务端与客户端验收使用合成 sentinel、隔离 HOME/数据库和真实成功/失败/超限/取消路径，捕获 stdlog、slog、HTTP ErrorLog 及需要的浏览器 console。故意加入打印或错误回显的负例必须触发隐私断言。测试本身只报告布尔或固定错误，不能为证明泄漏而把真实秘密写入证据；定向测试通过不等于通用污点分析。
