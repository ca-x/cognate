# Claude Provider Patch Implementation Plan

> **For agentic workers:** Use subagent-driven-development or executing-plans, with ub-mode red → green → independent acceptance. Steps use checkbox tracking. This plan only authorizes the server-side patch capability, not native writes or secret transfer.

**Goal:** 真实服务端 moejs 将完整 Claude provider desired-state 转成七个受控操作，补齐四键 additive renderer 无法删除旧认证/映射模型的问题。

**Architecture:** 共享 `internal/agentconfig` 只有声明式 DTO。服务端 registry 执行 JS，再由 Go 宿主逐项验证输出等价于输入。旧设置、原始秘密、路径、网络和客户端文件系统均不进入该接口。客户端未来消费受控操作，不运行 JS。

**Tech Stack:** 系统 Go 1.27.3、encoding/json/v2、现有 moejs runtime；CGO_ENABLED=0，无新依赖。

**Spec:** `../specs/2026-10-28-configuration-native-apply.md` §§2–3；完整 native apply spec 的传输/发布待定项不由本片决定。

## Global Constraints

- 不缩减完整十四 agent/F12 首发目标；本片无公开 RPC、选择、Pack 附件、native preview/apply/restore 或秘密导出。
- `claude.provider-switch.v1` 管理精确七键：API_KEY、AUTH_TOKEN、BASE_URL、MODEL、DEFAULT_HAIKU_MODEL、DEFAULT_SONNET_MODEL、DEFAULT_OPUS_MODEL，均为 ANTHROPIC_ 前缀。
- native-login 清除两认证且不接受 endpoint；api-key/auth-token 只能声明相应 present、不得另一凭据残留；缺必要凭据返回明确 unavailable/invalid，不产可应用结果。
- endpoint/models 使用 typed domain 完整语义：optional nil 对应remove，不是KEEP；present空串invalid，不归一化为absence。endpoint最多2048 UTF-8 bytes，仅绝对HTTP(S)、合法非空host，禁止userinfo/query/fragment/控制字符；native-login不接受endpoint。各模型present为1–256 UTF-8 bytes且无控制字符，不trim、不改大小写。秘密仅以固定slot引用表达，绝无secret string/bytes输入。
- builtin.claude 升为1.1.0并保留四键 additive capability，同时加入新 switch capability。旧精确版本/digest请求拒绝，不自动fallback；新版本调用旧 additive能力仍按原合同。
- 新patch API独立于旧Render，不接收Base或旧env。registry必须显式具有相应capability，不能仅凭target许可。
- 输出精确七项固定顺序slot操作，每项恰一 `set-literal` / `set-selected-secret` / `remove`；未知/重复/遗漏/额外值/path/command拒绝并返回零结果。`set-literal`只允许五个非认证slot且必须有literal、没有reference；`set-selected-secret`只允许API_KEY引用`claude.api-key`、AUTH_TOKEN引用`claude.auth-token`且没有literal；`remove`不带literal或reference。额外成员即使空/null也拒绝。实际asset/revision归属由未来materialization合同绑定，不能声称本片已验证存储授权。
- 构建/测试从不读取真实HOME配置，不打印非法输入或原始DTO；文档明确这只是声明式转换，不是“provider已切换”。

## Review Focus

- 缺失模型/endpoint是否真正产生remove，而非悄悄保留旧值。
- 模块恶意更换literal/secret引用、删一项或添加一项时宿主是否拒绝。
- 宿主根据调用前原始输入的深拷贝独立复核，不拿插件修改后的输入作比较基准；switch-only manifest调用旧Render必须拒绝，不能仅检查capability非空。
- 模块是否只得到typed非秘密数据，不能接触旧env或原始秘密。
- 新capability是否绕过旧Render gate、版本/digest或失败回退。
- 请求与结果的可变slice/pointer是否被模块或调用者修改，超限/取消是否零结果。

## Task 1: Declarative DTO and Actual moejs Capability

**Files:** 修改 `internal/agentconfig/types.go`、`internal/server/agentadapter/{builtins.go,builtins.json,claude.mjs,registry.go,render.go}`；新增 `internal/server/agentadapter/provider_patch.go`、`provider_patch_test.go`；必要时调整原registry/render tests及 `docs/AGENT-ADAPTERS.md`。不改configuration domain/API/Web/native内核。

**Interfaces:** 新 DTO表达auth mode、optional endpoint/default/haiku/sonnet/opus和两个secret present标记，无Base/旧env/secret原文；request同样绑定API/version/digest/target/capability。`Registry.PlanClaudeProvider(ctx, request)` 返回七项声明式patch或零结果+固定错误。最终命名可沿已有风格，不扩大语义。

- [ ] 先写行为测试并保留旧capability不足/native-login/角色模型未映射的red，不用编译失败代替red。
- [ ] 实现宿主输入校验、真实moejs转换、输出严格解码及独立语义等价复核；复用现有runtime deadline/取消，不引入第二JS引擎。
- [ ] 认证三模式、optional模型presence全组合、精确七项顺序、limits、版本/digest、capability错配均覆盖。
- [ ] 恶意真实JS mutation覆盖缺项/重复/额外slot、任意path、错literal/secret引用及未知成员，确保拒绝且输入不变；去宿主复核overlay必须出现行为red。
- [ ] CGO0 focused重复/vet、旧adapter回归、CLI依赖无server/moejs检查，冻结写集和报告，不自行stage/commit。
- [ ] 主线程独立导出全量验证，非作者验收exact tree后单独提交SSH推送；原生文件/秘密传输另案实现。
