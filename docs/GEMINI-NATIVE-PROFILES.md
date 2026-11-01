# Gemini provider 本机应用

2026-11-01 主线接入：原 exact65 经非作者两轴审查，其唯一 P1「原审阅期限后仍发布」
已由七文件修复与独立增量复审关闭。下面保留固定 profile、手动授权、双文件非原子、
OAuth 和平台边界；功能进入主线不表示已包含在此前正式发布的二进制中。

本片为固定版本的完整手动应用候选，交付冻结报告及非作者验收决定可合入性；不是完整 F12、十四 agent、GUI 或全平台 GA。服务端资产保存、排序与设备应用是不同操作。后续 MyGo 应复用 `nativeconfig.Client.ApplyGemini/LocalGemini`，不执行 Cobra 或 moejs。

## 固定边界

- 首 profile：`gemini-0.61.0-node26.13.0-linux-amd64-provider-v1`，仅 Linux/amd64；不支持的版本/平台在 target/RPC/秘密访问前关闭。
- 官方 Gemini CLI 0.61.0，commit `bb523741c7429a44d03e964bc124c7c92df59d5f`，tree `c4226f654bb0b35109c4f5ce34535c5addecda47`。使用未经修改的完整 npm 发布包，不仅核对入口。
- Node 26.13.0 ELF SHA256 `0adde40d1b7dfa57c8bc83cbc319758c3ec6ebba21363bb2fa752ea6b267ad1b`；449 文件包 manifest SHA256 `d937b098022f074463d0d26af37c5e7669cfeccbf6966e9795002f03f7db76b6`。固定身份及原始 LICENSE/manifest 位于 `internal/geminiconfig/testdata/native-profile-v0.61.0/`。
- 必须已有安全、归本机用户控制的 `~/.gemini` 根，以及已有可信目录规则覆盖的明确 cwd。Cognate 不创建 workspace trust，不搜索 PATH，不运行 Node/Gemini 做生产预览，不授予任意路径写入权限。
- Go 无执行观察器在预览、批准、材料化和每次发布前复核完整运行时/包、根身份、环境、dotenv 搜索、项目/系统设置与 trust 来源。源码身份不是远程 attestation；调用者不能自报 witness 或有效层事实。
- 仅管理 `.env` 与 `settings.json`。各文件最多256KiB、合计384KiB；`.env` 及新文件/stage为0600，原 `.env` 也须0600。缺少根、未信任/不可判定来源、迁移、外层认证/路由、插值、代理、符号/硬链接、不安全权限/ACL等明确 Unsupported；不做有损降级。

## 精确批准

唯一六字段为 `GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL`、`GEMINI_API_KEY_AUTH_MECHANISM`、`GEMINI_MODEL`、`/model/name`、`/security/auth/selectedType`。dotenv 与 JSONC 分别解析；未知的已证明无影响 sibling、数值词素、注释/CRLF、顺序与其它字节保留。其它未知有效来源不假称可安全应用。

服务端 moejs 只接收声明/凭据存在位；Go 宿主独立核验，事务内复核 owner、asset revision 与捕获的全部可选字段。沿用原六个 native-purpose RPC，追加 target3、slot4、tag41 typed variant，旧 Claude/Codex 字节、签名域、wire、AAD、记录和 auth Keep 不迁移。

预览不取 key、不写 target/selection。第一次审阅精确六字段的 Absent/Managed/Foreign，foreign 必须逐字段 takeover，删除必须逐字段 clear；不因字段名就认领。之后生成不同的最终 plan ID，另行批准 `gemini.api-key` 槽和最终确认。Enter/空输入默认拒绝，stdin session 不代表批准；不持 target/install 锁等待用户或网络。完整工作保持同一次最多2分钟批准预算，慢观察不延长期限。

API-key 支持 native-default 与 bearer 两种 transport。`native-login` 仅写 selector，并在明确清理批准后移除本操作所管 key/endpoint/bearer；不读取、复制、迁移或管理 OAuth、ADC、keyring，也不声称用户已登录。认证必要错误、静态 marker 或保留文件都不是实际账户请求的资格证据。

## 使用

先按既有客户端 key 初始化流程建立 key；不会隐式生成/重置已有 key。示例中的所有资产、版本、owner与路径都必须明确来自本次用户选择：

```sh
cognate auth run --server https://your-origin -- config apply \
  --target gemini.settings --asset ASSET --revision REVISION --profile PROFILE \
  --gemini-node /absolute/node --gemini-package /absolute/gemini/package \
  --gemini-cwd /absolute/trusted-workspace --apply
```

省略 `--apply` 只预览。也可沿用已有 `--server/--owner/--token-stdin` 原生入口；会话不得放命令行或日志。`auth run` 仍在退出时清理其父会话，不因为本功能保留登录权限。

```sh
cognate config status --target gemini.settings --server https://your-origin \
  --owner OWNER --profile PROFILE --operation OPERATION
```

`status --check-remote` 是额外显式的只读 RPC。重启票据只含原签名 Status 请求与作用域受限的 native bearer，加密保存，不保存签名私钥、grant token 或旧批准。父会话登出/撤销、expiry 或 owner/origin 不匹配仍拒绝；`auth run` 已清理父会话后不能用票据复活远端授权。普通 Unknown 路径不自动调用 Status。

`recover`、`restore --source-operation`、`abandon`、`retire-unpublished` 均须明确 `--target gemini.settings --server --owner --profile`、原 operation、新的 Node/package/cwd 观察，以及新审阅/`--apply`。其中 recover/abandon/retire 使用 `--operation`。恢复/还原有 key 写回时，独立槽批准标记来源为 durable-candidate/local-backup，不是新 server 消费；新计划不能恢复丢失的明文。

## 发布与恢复真相

请先关闭正在运行或自动热加载的 Gemini。两个 rename **不是 pair 原子**；Cognate 锁只协调自身 writer，不阻止外部编辑器或 Gemini 看到窗口。env先、settings selector后，各 role发布前 durable intent、再核验 CAS、交换、fsync并核对实际 image；两 role及已置换前像都验证后才提交 active selection。取消或错误不等于回滚，Consumed不等于Applied。

AuthorizeAttempted/ConsumeAttempted 在远端调用前持久化。丢收据保留真实 Unknown，禁止自动重发 Authorize/Consume或换 operation伪装重试。readonly Status/Reconcile不发布第二文件、不获取新 grant。完整认证 candidate 的剩余本地发布必须新 observation/plan/槽/最终确认；重启不恢复旧审批。

Restore是新 operation，CAS当前 After，从加密原件恢复原 presence、精确字节及安全的原 selector mode，选择状态最后提交。原 `.env` 权限不放宽。其它 owner/origin 的新应用需要独立会话、全新绑定及精确 foreign takeover；旧 owner 的恢复不能覆盖新的实际文件。

最多32个操作、256个选择、64KiB加密元数据和96MiB vault；不删除未知文件来制造容量。未知未索引 reservation/stage 保留并计入有限预算，不凭文件名认领/删除。显式恢复最多四次 stage epoch；损坏、外部更改或无法证明拥有的已置换 image为NeedsAttention。未发布退役与消费前放弃不同，保留真实 Consumed/Unknown、备份和配额；不是远端撤销或通用清理工具。两次发布间的部分态、selector恢复mode窗口和关闭错误均不能说成已回滚或已清理。

## 资格与升级

固定 profile 的普通 CLI 资格必须同时具备：未修改官方完整 consumer/TLS实际请求、控制 PTY、双 owner/版本 CAS、Unknown无重放、SIGKILL+Wait/重开/新确认、恢复原 presence/字节/mode、五类减门反例，以及旧 Claude/Codex普通 C6和0/v1/v2交叉读取。开发 fixture overlay或单独 consumer prototype不是最终生产 tuple 验收。最终状态以本片冻结 REPORT及非作者结论为准，不借用其它切片资格。

本片作者执行证据包括普通 CLI 的两种交叉 transport A→B→Restore-A、独立模型/相同模型/env优先/无模型官方auto、六字段foreign接管及已证明无影响的CRLF/未知sibling。无模型测试按固定源的数值分类器响应并核对实际路由与最终请求，不通过强制model改变产品默认行为。native-login仅有未登录官方消费者的auth-required/零请求边界；合成OAuth marker另以inotify核实Cognate不打开、不读、不改，不能据此声称真实账户请求成功。原始失败与后续修正收据均随交付保留；这些作者证据不是非作者ACCEPT。

升级 Node、Gemini 包、codec、有效来源观察合同或 native tuple 必须重新资格；不能把新版本动态映射为旧 profile。旧记录保留原身份、AAD及过期真相，不静默升级。回退二进制必须先核实本地待处理操作，不重放远端消费，不把不认识的新记录当空库存。分发许可包须包含未经修改的 Gemini LICENSE和包内 THIRD_PARTY_NOTICES；本片不重新分发 Node或Gemini执行文件，使用者显式提供固定官方输入。
