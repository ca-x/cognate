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

## Effective ModelInfo v2 私有 codec 候选

`Native0166EffectiveManifest()` 与旧 v1 accessor 分开；v1 ID、四项摘要、模板/golden 与原准入语义保留。v2 ID 为 `codex-0.166.0-linux-amd64-full-responses-effective-v2`，ProfileDigest 域分离绑定固定 source.json 原字节，TemplateDigest 仍为原显式目录模板摘要。完整 pinned metadata/prompt 和 Apache+NOTICE 原文位于 `internal/codexconfig/testdata/native-0.166.0/`，不依赖运行时下载或调用者提供模型能力。

v2 仅从同一 Check 捕获/最终目录快照解析实际所选模型：无有效目录指针时用固定 bundled longest-prefix/受限单 namespace 匹配，显式或保留目录只 exact-match，miss 沿 native fallback。effective Lite 在返回 plan/读取所选秘密之前拒绝；已有 retained ownership/legacy false 冲突不放宽。七个合法资产 root effort 值均原样保留/传递，不按 advertised levels 再设人为 fence；根缺省沿 native default，不填目录、不把 model/list 的 none 投影写回根。

这是纯 codec 候选，不是新生产 profile 登记、D/C2/CLI 应用闭环或 Binary redistribution READY。后继 D preflight2 与 C2 admission helper/test2 各须具名授权/独审，消费同一获批 v2 tuple 并拒绝混合身份。真实 consumer 的40 POST/逐字默认指令/presence 资格仍须另经实际运行与非作者核验。含这些上游数据的公开二进制、OCI、desktop 分发须先完成完整可读 license/notice 的实际打包验收；仅内嵌或 source.json 标签不构成该验收。

### 2026-10-31：v2 codec 与限定消费者证据已验收

上述候选阶段的消费者待办已由新源码绑定的 42 次实际 POST 补足，并经非作者限定核验；不继承旧 40 次运行的证书。源码包 SHA256 为 `4ccccfb5a833ccef8b4c5aa7a6c94268b7406f94464fe35502e831a781081c48`，独审报告 SHA256 为 `7af47f82252df888aa4f59023736846b4330e46abe752f6fe920857494539a62`。selected Full 行必须具备可用的 canonical 或 legacy 指令字符串；空串合法、canonical 优先，双缺失在选取秘密前拒绝且无 plan，不填默认文本。两个合法空串案例的实际请求都不发出 base-prefix item，而非发出空文本、null 或替代模板；其它 developer 上下文仍可存在。

资格仅限既定 v2 tuple、固定 Linux/amd64 ELF、受控隔离的 Missing/API/fresh synthetic OAuth 与 loopback TLS。42 次完成均由 collector 确认，随后主动 kill 并 Wait(-9)，不是 native 优雅退出 0；独审核验这些新执行证据，没有冒称自己重跑全部 42 次。旧 REJECT 和失败探针保留。v1 accessor/摘要/语义不变；D/C2 helper 迁移、生产登记、官方账号刷新、实际业务端点、其它平台、完整客户端与首发资格仍未完成。默认资格表继续为空，不能仅凭本节启用应用；对应提交的实际打包与 CI 结果须单独核对。

固定上游 `prompt.md` 第 151 行的末尾空格属于已核验原始字节，随源摘要保留，不做自动排版或 trim；它不是本项目手写 Go 的格式约定。

### 2026-10-31：D2/C2 helper 迁移已验收，登记仍关闭

新生产者和 C2 projection 采用完整 effective-v2 tuple；历史全零、observed-v1 与
observed-v2 分别读取，不给旧记录重标身份或增发准入权。合法恢复保留原身份、
ownership、managed-model 信息和原 auth 字节；跨 profile 承接仍要求 completed、
current selection、精确 After/target 及已验证版本。局部审阅读取输入 source
operation 的 recorded profile，不把新 restore plan ID 当作历史来源。

独审重新执行五个真实固定 native/TLS 场景与实际 CLI/PTY/App/SQLite 组合，
没有继承旧 42 次运行；这是 helper 迁移证据，不是登记资格。默认
`codexQualifiedProfiles` 继续为空，普通 Codex Apply 仍拒绝；首次公开启用还需
正常登记候选的完整消费者矩阵、分发许可及当前组合验收。

升级注意：将来显式启用后，槽和最终确认即可产生 v2 durable operation，即使
还未 Authorize、未收到业务结果或未修改目标文件，也不能假定可透明回退。
固定旧版 reader 能读取原合法全零/v1，但遇 v2 库存会 fail closed。
升级前保留库存与目标的一致备份以及支持 v2 的新版 binary；结果不明时用新版
的合法 status/recover/restore 等流程核查。不要删 anchor、改 profile 标签、
删除个别 v2 行或覆盖单个角色来绕过拒绝；Restore 也不是全库降级工具。
