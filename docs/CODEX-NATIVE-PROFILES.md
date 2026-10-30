# Codex native profile 能力映射

状态：纯转换 codec 的逐 profile 合同；不启用生产 NativeProfile，不代表本机应用或完整 Codex 验收通过。普通配置资产继续无损保存全部声明，包括 optional false、absence 与模型顺序。

## 并行工具调用

固定官方 `codex-cli 0.166.0`（源 commit `6ff1e1ca3b30fc8b914720681fcf5fe62e95f856`）的真实隔离 HTTPS consumer 观察表明：Full Responses 不读取模型目录的 `supports_parallel_tool_calls` 来控制请求，false/true/absence 都会发送 `parallel_tool_calls:true`。`use_responses_lite=true` 可以发送 false，但同时改变工具、header 与 reasoning 协议，不能冒充等价字段映射。旧反例保留，不能通过打开 profile bool 或删除服务端声明洗绿。

内部 profile 改为封闭能力模式，而不是“支持并行字段”的布尔值：

| 模式 | 合同 |
| --- | --- |
| 未验证或未知值 | 拒绝，零 plan/候选 |
| Catalog | 仅用于已证模型目录字段确有该语义的 profile；保留原字面映射。现有 synthetic fixture 属此模式，不构成真实 consumer 资格 |
| AlwaysOn | Full Responses 固定允许请求并行；声明里任一模型 explicit false 都在秘密材料消费前拒绝整包。true 表示允许并行，不保证模型实际并发；absence 保持未声明，不能合成为 true/false。不能新增一个 consumer 不读取的目录字段冒充控制 |

AlwaysOn 的模板不允许预置该 legacy 字段。已存在目录里的 legacy false 或错误类型拒绝，不删除、不覆盖，也不因该模型将被移除而忽略；已有 legacy true 按原字节保全，但不当作有效控制。完整声明、presence、顺序及能力模式仍进入本地 plan 绑定，改变模式必须改变 profile digest。原 Catalog golden、服务端资产格式与 C2 协议不改。

AlwaysOn 遇有效本地 Lite 模式必须拒绝；未知启动参数、其它配置层、特性覆盖与实际 consumer 的完整资格仍由后继 NativeProfile/D 观察器验证，不由本 codec 的 caller facts 或 digest 代替。实际 Lite provider 兼容与可表达 false 的其它版本仍是后续能力，不能声称本合同已覆盖。

## 验收

- 全模型而非只检查主模型：false 拒绝；true/absence 可转换且不制造 legacy 字段；错误零 plan/候选。
- 模板、已有目录 legacy false/错误类型、Lite 与未知模式拒绝；无关字节保全。
- 模式与完整声明/presence/order 绑定；不修改输入，不读本机登录或补密钥；取消使用原 context sentinel。
- 原 synthetic Catalog golden 与旧 codec 测试保持；真实 consumer 证据只证明上述有限观察，不签 keyring/Auto、所有字段/平台、安装 launcher、D 或完整首发。
- 已提交基线的生产资格表为空；Q2 私有候选新增固定 0.166.0 codec manifest，不等于生产登记或资格签收。engine 的 `codexQualifiedProfiles` 仍为空；公开 `CheckProvider` 的 codec manifest 不能代替 engine 的启动观察与资格门。

## Q2 私有候选边界（未验收）

仅 Linux 的固定 ELF/source manifest、直接 `app-server`、受控清空环境及具体 cwd 可进入观察。观察器复用 B 的 TOML/JSON 解析，核真实 executable 物理身份与 SHA、实际 native config/read 的 user/system 层，以及所有受支持 source 的缺失物理见证；任何其它层、override、未知 File auth 或需要 refresh 的状态均拒绝。fresh OAuth 的语法与时间窗口分类不是账户真实性或 refresh 成功证明。

`ObserveCodex` 产生 engine-owned opaque 观察；`PreviewCodexObserved` 绑定 owner/origin/selection、三 role 基线和期限。原 `PreviewCodex` 不会因新增 API 自动放行。auth 仍 Keep，true/absence 与全模型 explicit false 的 AlwaysOn 合同不变。descriptor 仅进入既有加密认证 journal；restart 必须重新观察，不能把持久化 hash 当新鲜授权。非 role closure 与可合法交换的 managed Before/After 分开。

当前私有候选的 materialization、publisher 入口、每次 `before-publish-N` 之后交换前、最终 Completed/Restored 持久化前及 restart 均重新复核。原真实晚变更 compiled RED 保留；获批 exact17 扩展仅增加 publisher 文件，并复用同一 fresh validator。若某 role 已实际落盘才发现 source 冲突，保留实际部分发布或恢复状态，不虚构回滚或完成；后续恢复仍需 fresh 观察和显式确认。最终新鲜复核也不延长原 operation 安全期限。

固定 native loader 会将相对 catalog 路径规范化；观察器只对 B 已支持的固定相对 `cognate-model-catalog.json` 按具体 user config 目录生成期望值，逐层语义比对仍拒绝其它路径或未解析结果，不另造配置合并。候选 template 的新增固定值须绑定 manifest 独立核证；Q1 的旧 native 证据不自动证明候选所有新值。独立真实规格验收前不得登记生产 profile、称 Applied 资格已完成或把私有 fixture 当真实端到端交付。

非 Linux 仍 failclosed；跨编译不代表运行支持。CLI/MyGo 后续应用、C2、完整 D 与全部十四 agent 首发范围不因本片改变。
