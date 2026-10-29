# Darwin Filesystem Primitives Implementation Plan

**Goal:** 为 Darwin 增加可独立验证的安全文件原语，不开放安装支持。
**Base:** `06eedca921a7da666b37464082d3d151c1294a5a`。
**Spec:** ignored `.superpowers/sdd/2026-10-28-release/client-platform-gaps.md` 的 Darwin 8 文件建议；`docs/INSTALL-RECOVERY.md` 的可信稳定路径、句柄辅助证据及保守恢复边界。
**Execution:** 本会话按 UB / modern-go / go-best-practices、tests-first 执行；用户另行安排独立验收。禁止 spawn、branch、stage、commit、push。

## 约束与写集

Go **1.27.3**，`CGO_ENABLED=0`，无 race/cgo、新依赖；fixture、HOME、配置、凭据均隔离至临时目录。其他 agent WIP 不修改。

只写以下 11 个 tracked 路径：

- 本计划。
- `internal/objectid/identity.go`、`identity_other.go`、新 `identity_darwin.go`、现 `identity_test.go`。
- `internal/install/trust_other.go`、新 `trust_darwin.go`。
- `internal/install/projection_handle_other.go`、新 `projection_handle_darwin.go`。
- 新 `internal/install/platform_darwin_test.go`。
- 新 `.github/workflows/platform-primitives.yml`。

Linux 行为及身份字节不变；Darwin 使用 `darwin-dev-ino-v1:<uint32 dev bits>:<nonzero uint64 inode>`，严格 canonical 十进制，不重解释 Linux 记录、不接受 Windows 格式。所有 CLI/MyGo/core/stage/backup/uninstall/restore 非 Linux guard 不动；Windows 原语继续 unsupported。现有 `publish_darwin.go` 只测不改。

## 实施与验证

- [x] 身份：先扩展 `TestValidate`，在 Linux 观察 Darwin canonical parser RED，再最小实现 GREEN；以 TempDir 测本机身份域、rename、替换与错误类型拒绝，保留 Linux 字节回归。
- [x] Darwin 信任：先写 `TestDarwinTrustPrimitives`，覆盖实际当前 UID、目录私密性、group/world 写位、symlink/FIFO、`.git` 范围和取消，另以元数据 fixture 验证异主拒绝；实现逐字保留 Linux 的当前用户/root 规则，缩窄 other build tags。原生运行待 CI。
- [x] Darwin 链接：先写 `TestDarwinProjectionLinkPrimitives`，覆盖真实链接句柄 fstat（含悬空及 root 外目标）、链接文本、同文本替换、父目录替换、非法名称及普通/特殊文件拒绝；实现 `os.Root.OpenFile` + `O_SYMLINK|O_NOFOLLOW|O_NONBLOCK`、fstat/Lstat 一致性检查，禁止 O_PATH/fallback。原生运行待 CI。
- [x] 发布：写 `TestDarwinRenameNoReplacePrimitives`，验证既有 `RENAME_EXCL` 对目录/文件/悬空链接均不覆盖，成功移动保留身份；全部使用 TempDir。原生运行待 CI。
- [x] CI：新增只读、无 publish 的 Go1.27.3/CGO0 三 OS workflow；macOS 实跑明确命名 TestDarwin*，另跑 common parser 和已有 portable scan/slug/Connect smoke。解析 JSON 核对每个必需测试 pass、拒绝 skip/零匹配；验证器本地负控通过。Windows smoke 使用内存扫描和 loopback Connect 元数据，避开既有可执行位归档的 POSIX 前提。
- [x] 本地 gate：Linux common tests、限定原语/portable tests 和 vet 通过；Darwin amd64/arm64 test 二进制交叉编译、Darwin 静态 vet、Windows common/portable test 二进制交叉编译通过。静态 gate 确认三 OS 文件选择、CGO0、无新依赖、Linux/trust 规则未变及 TestDarwin 确实编入二进制。
- [x] 冻结：ignored `.superpowers/sdd/2026-10-28-release/darwin-primitives-report.md`（≤1 页）记录 base、逐文件 SHA256 manifest、只含写集的候选 tree、验证日志及原生 CI 待验状态；用无 index 的 Git tree plumbing 排除其他 WIP，不 stage。并行提交导致共享 HEAD 前移时，核对本片路径无交叉修改，仍固定上述 base。

Darwin 测试先于实现编写；Linux 不能运行 Darwin RED/GREEN，交叉编译只证明构建。真实 OS 正确性和原生测试结果必须等后续推送 CI，不能宣称 Mac 绿或完整 Darwin 安装支持。若 API 不支持或安全实现超写集，带精确证据停止。

## 真实 CI RED 的窄修

修复基线 `5b3f80bca245706ef8804bd5c61c8a71a531145a`；保留真实 run `40785264971`（completed/failure），Mac job `123156749045` step 7 的 RED：13 run / 11 pass / 2 fail / 0 test skip。Trust fixture 把 TempDir 当作 0700；Projection 使用的 Root.OpenFile 强制加入 O_NOFOLLOW，Darwin 对最终 symlink 返回 ELOOP，Root 随后解析链接产生 escapes/ENOENT/ELOOP。

- [x] 仅修改本计划、`internal/install/platform_darwin_test.go` 的私密目录 fixture 初始化、`internal/install/projection_handle_darwin.go`；静态 gate 确认全部原测试名称、用例及断言逐字保留，trust/身份/入口 guard 不改。
- [x] 从 `root.Open(".")` 固定父目录句柄，对已限制为单组件的名称调用 Darwin `unix.Openat`，主实现使用 `O_SYMLINK|O_NONBLOCK|O_CLOEXEC`。O_SYMLINK 本身打开链接而不跟随目标；不再附加会拒绝 symlink 的 O_NOFOLLOW，不走按路径重开或 unsupported fallback。保留 fstat 类型、Lstat 身份及既有父目录/链接文本核验。
- [x] 旧 CI 日志及实际错误已保存于 `/tmp/cognate-darwin-fix.lsRFPG/`；Linux objectid/skill/packclient 全 tests、5 个原语 tests、Linux/Darwin 静态 vet、静态边界检查和 Darwin 两架构 test 交叉编译均通过；ignored 报告及修复 manifest 同处交接。仅主线程后续提交推送后的真实 CI 可证明原生修复通过，不 watch 或用交叉编译代替原生 PASS。
