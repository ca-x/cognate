# cc-switch OpenCode 选择导入

本轮贯通 schema23/24 的 OpenCode v1 provider：来源预览、独立选择、完整审阅、
服务端原子保存和所有者读回。沿用现有导入票据、单次提交、Unknown 只读核对和
三类默认不选，不新增数据库或 RPC schema，不执行 OpenCode 或修改设备文件。

支持明确的 v1 来源（空 meta 为上游旧版 v1，或仅有 `opencodeConfigFormat: v1`）：
provider ID、npm/name、options 的 baseURL/apiKey/headers，以及 models 的 name、
limit（context/output）和 modalities（input/output）。缺失与明确空对象保留；
数字以精确十进制文本传输。API key 和全部 header 值仅投影存在位与宿主生成的
凭据槽，不迁移来源原文；保存后由用户在配置编辑器独立补充凭据。

v2、扩展字段、模型 options/thinking/cost/variants、额外 metadata/endpoint 仍明确
标为不支持，不静默丢弃，也不把它们混作 v1。该来源 profile 是开发顺序，不替代
F6/F12 的完整格式、agent、MCP、prompt、Pack 与本机应用目标。

实施与验证顺序：来源和加密信封回归 RED→GREEN；票据绑定与 Connect/SQLite
提交读回；共享客户端严格校验与一次提交/丢响应；Web 完整审阅、主题与键盘；
全量检查与组件发布。只有完成当前链路验收后才发布新版；未通过项保留在 TODO。

## 客户端兼容

含 READY OpenCode 配置的快照需要新版客户端：CLI 0.3.0、MyGo 0.1.4 或对应源码
构建。旧版严格解码器会拒绝新候选，即使只想导入同一快照中的其他项目；没有能力
协商或透明降级。请一并更新导入客户端。服务端 Web 使用随服务器发布的新版本。

## 交互约束与本地验证

| Before | After | 原因 |
| --- | --- | --- |
| OpenCode 只显示不支持的元数据 | 受控 v1 可独立选择并保存私有资产 | 不混同来源支持与本机应用 |
| 原生审阅缺少 OpenCode 定义 | 候选与最终审阅共用完整字段和 presence 摘要 | 用户能核对实际批准的内容 |
| Web 没有对应安全字段展示 | 渐进展开和确认页显示 provider、模型、精确数值、凭据槽 | 不靠存在位或资产名称代替完整审阅 |

沿用已有组件和短反馈，不新增动画或基础控件。沿用导入安全边界：切换语言清理
来源与预览，须重新授权上传；切换主题不重新读取文件或提交。测试矩阵明确区分两者。

本地 Go 1.27.3 / CGO0 全量测试、vet、Web build 和状态回归通过。真实 TLS/SQLite
覆盖 schema23/24、普通/丢回执、字段精确读回与所有凭据缺失；CLI 审阅、MyGo
模型/原生组件的完整审阅和独立批准提交通过。浏览器八组 375/1440px、中英、
深浅布局，原生 details 展开、键盘、确认取消/再次审阅和一次提交通过。
本机证据在 `/tmp/cognate-opencode-import`，不是远端备份或全部平台运行资格。

原生审阅 P2 先由用户代表指出，新增真实集成用例复现后修复并经限定复审关闭。
导入宿主增加第十三个受绑定源码；独立长度前缀编码重算固定摘要，仍保持模块摘要、
封闭路径集合、独立编译字节验证和混合 agent 拒绝，未删除原身份门禁。
