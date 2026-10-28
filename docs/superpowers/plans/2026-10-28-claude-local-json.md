# Claude Local JSON Implementation Plan

> **For agentic workers:** Use ub-mode and executing-plans: behavior red → minimal implementation → negative controls → independent review. This authorizes only an in-memory codec, not native file writes or sensitive transport.

**Goal:** 以纯Go将完整七键声明式patch合并到已授权读取的Claude settings bytes，保留无关原始token，补齐后续native内核的格式处理依赖。

**Architecture:** 新 `internal/configapply` 只依赖共享 `agentconfig` 和stdlib；用 `encoding/json/jsontext` 严格解析、记录member/value spans并局部修改，不自行发明JSON语法，不以map/float64重编码整个文件。没有磁盘、网络、server包、moejs或公开RPC。

**Tech Stack:** Go1.27.3、CGO0、jsontext/json-v2，无新依赖。

**Spec:** `../specs/2026-10-28-configuration-native-apply.md` §2.2和§3的本地合并边界；§10文件发布/传输接缝仍未锁定，本片不实现它们。

## Contract

- 写集：新建 `internal/configapply/claude_json.go`、`claude_json_test.go`、必要的同目录secret wrapper文件/tests；不得改共享DTO、server、Web、install/state或依赖/index。
- `RewriteClaudeSettings(base []byte, patch agentconfig.ClaudeProviderPatchV1, credentials map[agentconfig.ClaudeSecretSlot]SecretMaterial) (SettingsImage, error)`；错误固定分类，不回显输入。失败返回零SettingsImage。
- `SecretMaterial` 和 `SettingsImage` 私有bytes，默认fmt（含%#v/指针）、JSON/Text/slog均脱敏；constructor/copy accessor复制bytes，不保留调用者可变引用。SettingsImage的显式CopyBytes仅供未来受控持久化/加密调用，不是普通view/日志字段。Secret constructor要求1–8192 UTF-8 bytes、无NUL/CR/LF，不trim。
- 独立验证target、精确七slot顺序、无遗漏/重复、action联合、nil与present空值；API_KEY仅引用claude.api-key，AUTH_TOKEN仅引用claude.auth-token，literal只给其它五slot，remove无literal/reference。五literal遵循server plan的endpoint/models完整校验。认证最多一个selected-secret，两者remove时endpoint必须remove。credentials恰含所需引用，缺失/多余/非法slot均拒绝，绝不执行JS。
- base必须是非空严格UTF-8 JSON object；不猜测empty为absent，也不支持JSONC。所有层级重复成员/尾随数据拒绝；env若存在必须object，受管旧值必须string。非受管env可为任意合法JSON原样保留。escaped-key按解码身份识别，不能绕过duplicate/slot定位。
- 原始与结果文件各最多256KiB，顶层最多512成员，env最多128成员/编码64KiB；输入及最终预算均检查。每env string值解码后最多8KiB；其它非受管env值raw最多8KiB。不限制/转换未知数值为float64，不裁剪未知字段以求通过。
- 修改仅受管叶节点，保留其余token bytes、成员顺序、转义及换行；只允许为合法JSON必需的相邻逗号/空白变化。删除最后一个受管键保留原有env object；原无env且七项全remove不新增env（整个base逐字不变）。新增成员使用固定slot顺序，不调整已有非受管顺序。
- 对原始base、patch指针、credential map及返回CopyBytes的后续修改不得改变已返回SettingsImage；无原始秘密或含秘密hash/长度的正常输出。不存在CAS、文件身份/权限、备份或“已应用”成功状态。

## Tests And Gate

- [ ] 先真实行为red：七键切换/remove、native-login、unknown大整数/object env/escaped keys与字节保留；不把缺符号编译失败计作red。
- [ ] 校验非法JSON/重复/深层尾随、union/认证互斥、缺/多secret、nil-empty、输入/最终512与128/64KiB/256KiB边界；失败零结果和零输入变更。
- [ ] 默认wrapper全部打印/JSON/Text/slog脱敏、显式copy隔离；测试错误只报告布尔/固定分类，不能dump含秘密output。
- [ ] 破坏删除/未知字段保留/独立联合校验的overlay负例得到指定行为red，去overlay green。可加bounded fuzz和b.Loop benchmark验证解析与分配，不宣称无测量性能提升。
- [ ] CGO0 focused重复/vet、无server/moejs依赖，冻结写集/作者报告；不stage/commit。主线程clean-export全量与非作者exact-tree验收后再提交SSH推送。
