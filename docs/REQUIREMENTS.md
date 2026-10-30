【跨机器交付】本文档全文即权威需求来源，随记忆同步到任何机器。此前记忆 c723f5c3 里的本地文件路径仅对作者 Alma 所在机器有效，已作废。Codex 开工第一步：把本文完整写入项目 docs/REQUIREMENTS.md，不要截断。

# Cognate 需求文档（原名 SkillHub，交接 Codex 开发）

> 生成：2026-10-27。本文档是唯一权威需求来源；记忆（nmem）为背景资料，与本文档冲突时**以本文档为准**。
> 技术栈中**已定项不许更改**，**待定项由你（Codex）与用户商定后补进本文档 §3**。

---

## 1. 产品定位

产品命名补充（用户确认，2026-10-28）：软件统一命名 **Cognate**，GitHub 仓库与 Go module 相应迁移至 `ca-x/cognate` / `github.com/ca-x/cognate`。历史记忆中的 SkillHub 指同一产品，不另起项目；完整原始需求及后续补充继续保留。对外品牌、命令和构建产物使用新名称；已有 RPC namespace、加密派生域、持久化格式及旧数据路径不得因机械替换失效，兼容策略见 `COGNATE-RENAME.md`。

一个 skill（Claude Code / 各类 coding agent 的 SKILL.md 技能包）的**聚合、分发、同步**系统：

- **服务端**：HTTP 服务。同步用户 GitHub star 的 skill 仓库、收纳手动添加的仓库、定时更新源、托管分组（pack）、为客户端提供中转下载加速、用户账号体系、云端备份。
- **客户端**：CLI 与桌面 GUI（GUI 框架用 egoist/mygo）。从服务端拉取/安装/更新 skill 到本地各 agent 目录，自动去重。
- **插件系统**：基于 MoeForge/moejs（纯 Go JS 运行时）的 JS 插件，用于导入第三方来源（cc-switch、magpie、npx skills 等已有skill 数据）。

一句话：**别人的 skill 仓库 → 我的服务端聚合/打包 → 我的所有 agent 本地目录，全程自动同步更新。**

---

## 2. 已定技术决策（不要更改）

| 项 | 决策 | 备注 |
|---|---|---|
| 语言 | **Go** | 服务端/CLI/GUI 内核统一 Go，单二进制交叉编译 |
| ORM | **ent** + **github.com/lib-x/entsqlite** | entsqlite 让 ent 接 modernc.org/sqlite（纯 Go 无 cgo，保交叉编译）。连接串用 `_pragma=name(value)` 格式：`_pragma=foreign_keys(1)`、`_pragma=journal_mode(WAL)`、`_pragma=busy_timeout(10000)`、`_pragma=synchronous(NORMAL)` |
| 插件运行时 | **github.com/MoeForge/moejs** | 纯 Go JS runtime，支持 ESM/async，1.4µs 起 runtime、~81KiB/实例，可每请求一实例隔离 |
| 客户端 GUI | **github.com/egoist/mygo** | 使用 MyGo 原生 UI 组件（`ui` 包），以 Go 实现桌面界面，不使用 Web 模式；与 CLI 共用内核 |
| skill 获取模式 | **整仓拉取 → 递归扫 SKILL.md** | `gh repo clone` / tar.gz / archive zip 均可；不要把 GitHub REST API 当主数据源（三款同类工具 cc-switch/magpie/npx skills 源码验证的一致结论） |
| skill 元数据 | SKILL.md YAML frontmatter，**name + description 必填**，缺失用目录名兜底 | 扫描深度建议 3~4 层，注意 `.claude/skills/` 等隐藏目录 |
| 搜索索引 | 接 `GET https://skills.sh/api/search?q=...&limit=N` | 现成的聚合索引，不自建 |
| 本地安装模型 | canonical 目录 **`~/.agents/skills/`** + 向各 agent 目录 symlink 投影 | Windows 用 junction；失败回退 copy；WSL 强制 copy。`~/.agents/skills` 是事实标准（~30 个 agent 直读） |

---

## 3. 待定技术选型（由 Codex + 用户商定，不要自行锁死）

> 以下此前讨论过但**未锁定**，仅作参考方向，欢迎提出更优方案：

- **文件存储抽象（已确认）**：使用 **github.com/spf13/afero** 及其适用的中间件；本地文件系统、测试文件系统与将来的远端文件存储通过适当的 Afero 实现接入。SQLite 仍使用 §2 指定的 ent + entsqlite。云端备份的具体后端仍待确认。
  - 参考已有项目 `nowledge-mem-snap`：S3 使用 `github.com/fclairamb/afero-s3`，WebDAV 使用 `github.com/lib-x/aferodav`；具体后端及依赖待选型确认后再接入和验证。
  - 适配器需验证文件系统接口契约、远端根路径隔离和资源关闭；不得假设远端对象存储具有本地文件系统的全部语义。
- **服务端 Web 管理面板前端框架（已确认）**：React 19 + TypeScript + Vite + Tailwind v4 + shadcn/ui + TanStack Query/Router；产物需能 `go:embed` 进二进制。桌面客户端使用 MyGo 原生 UI，不再要求与服务端面板共用 SPA。
  - 用户补充确认（2026-10-28）：前端使用成熟框架和组件，不自造基础组件轮子。Web 优先采用 shadcn/ui 及其成熟的无障碍组件基础，复用 TanStack 的路由/服务端状态能力；产品特有的业务编排可自行实现。桌面端继续使用 MyGo 原生组件，不因此改成 Web 容器。
- **HTTP 框架（已确认）**：使用 Echo。
- **客户端与服务端协议（已确认）**：使用 Connect RPC 及 Protobuf；二进制 skill 等资源可通过流式消息分块传输，服务端经 Echo 承载 Connect handler。传输时限制消息与资源大小、校验内容 hash，并支持取消；客户端仍只访问服务端，不直连 GitHub。具体传输接口在服务端里程碑设计并验证。
- **Protobuf 管理（已确认）**：使用 Buf 管理 schema、生成代码、lint 和兼容性检查；在引入首个协议后加入 CI 校验。
- **CLI 框架（已确认）**：Cobra；需要交互式多选时引入 Bubble Tea。
- **云端备份存储后端（已确认）**：S3 兼容存储起步，使用 Afero 适配器；WebDAV 可后续扩展。
- **密码哈希 / 会话（实现决策，按“具体库你定”授权）**：使用 `golang.org/x/crypto/argon2` 的 Argon2id（19 MiB、2 轮、独立随机盐）。本地会话为随机 nonce + HMAC-SHA256 签名，服务端只持久化 token hash、用户归属与有效期，默认 24 小时；签名 key 从外部主密钥通过 HKDF 派生。OIDC 使用 `coreos/go-oidc/v3` 验证签名 ID token，并与本地账号共享该会话体系；不按邮箱静默合并。
- **插件 manifest 具体格式**、分享 token 编码细节：你设计后在 §4 下方补记。

---

## 4. 功能需求

### F1 star 同步（服务端）
1. 授权两条路：
   - **GitHub device flow**（OAuth App，仅需 client_id，无 callback）：`POST https://github.com/login/device/code`（body: client_id+scope）→ 用户打开 `github.com/login/device` 输 user_code → 按 interval 轮询 `POST https://github.com/login/oauth/access_token`（grant_type=`urn:ietf:params:oauth:grant-type:device_code`）。`authorization_pending`=继续等，`slow_down`=间隔+5s，`expires_in`≈900s。scope：读公开 list 留空即可；读私有 list 需 `user`。
   - **手动粘贴已授权 token**（granted token）。
   - 个人开发期捷径：本机已 `gh auth login`，`gh auth token` 可直接取（仅本地模式）。
2. 拉 list：GraphQL（REST 不支持 lists，别试）
   ```graphql
   query($cursor:String){ viewer { lists(first:50, after:$cursor){
     pageInfo{ endCursor hasNextPage }
     nodes{ name slug isPrivate updatedAt lastAddedAt
       items(first:100){ totalCount pageInfo{ endCursor hasNextPage }
         nodes{ ... on Repository { nameWithOwner databaseId } } } } } } }
   ```
   - `UserListItems` 目前仅 Repository；item 上**没有 starredAt**。
   - 写入 mutation 用 `createUserList`/`updateUserListsForItem`（前端内部的 `addUserListItems` 在公开 API 不存在，勿用）。
3. 用户**勾选指定 list（collection）订阅**（如 skills 分类）；每个订阅 list 建同步任务。
4. 增量策略：`UserList.updatedAt/lastAddedAt` 做水位线，未变跳过，变了整 list 重拉；定时 worker（间隔可配）。
5. 已知限制：每用户最多 **32 个 list**；token 只存服务端（加密落盘），客户端永远不接触 GitHub token。

### F2 手动仓库（服务端）
- 支持 `owner/repo`、完整 github URL、可选 branch；入库前**先拉一次归档验证**能扫到至少一个 SKILL.md 再入库。
- 来源标记 `manual`，与 star 来源可交叉（同一仓库去重）。
- 实现补记（2026-10-28）：已接入公开归档收录和 Web 技能浏览；owner/name 忽略大小写，空分支与 `HEAD` 归一。每次最多 32 MiB 压缩内容、64 MiB 普通文件、10000 个归档条目、512 个技能，单个 SKILL.md 最多 1 MiB；不跟随链接。归档缓存默认总预算 512 MiB（首次可用 `--archive-cache-bytes` 指定，同目录配置必须一致，暂不支持在线修改），计入中断残留并跨进程协调；单用户最多 1000 个收录记录。归档先持久化，再事务写入仓库和技能，重复收录不更新已有快照。Lists 调度已接通公开来源自动收录，并以凭据/选择代次栅栏防止失权后写回；私有内容获取和已有快照源替换仍待完成，真实用户 GitHub 账号验收另见 §7。
- 后续实现（2026-10-29，取代上条当日的私有来源待办状态）：具有相应私有仓库读取权限的服务端 GitHub 凭据现可用于手动收录、所选 Lists 收录、显式刷新及周期更新；仍为整仓归档扫描，客户端只从服务端安装和更新，不接触 GitHub 凭据。认证失败不降级匿名，已观察到的私有仓库按所有者与规范仓库名保留单调拒绝历史；公开观察、换 viewer 或重启不能抹除该拒绝证据，历史本身不授予访问权限。范围经合成 GitHub TLS 上游、真实 SQLite、双 Pack→Connect→隔离 HOME Watch 独立验收并提交；真实 GitHub 账号授权尚未实测，不改变 §7 的证据边界。详见 `SOURCE-REFRESH.md`。

### F3 用户系统（服务端）
- 账号密码注册/登录，密码安全哈希存储，会话 token 下发。
- Web 管理面板与客户端 API 共用该账号体系。
- 支持 OIDC 登录，参考 `lazycat-contrib/cat-led` 的 OIDC 集成及安全处理；保留本地账号登录。身份以 issuer + subject 唯一标识，账号绑定需明确授权，不按未经验证的邮箱静默合并；会话应签名并校验，网关身份头仅在明确配置的可信部署边界使用。具体登录与绑定流程在 M3 实现并验证。

### F4 云端备份（服务端，可选开关）
- 备份对象：收录的 skill 内容 + 仓库配置 + pack 配置。
- 自动/手动触发，本地 SQLite 快照 + 远端对象存储（后端见 §3）。
- 支持恢复。
- 实现补记（2026-10-31）：已加入内部 S3 兼容密文对象传输层，Afero 用于受限读取校验，窄 SDK 接口负责单次条件上传和条件删除；有界并发、取消排空、响应关闭错误和不确定结果均有真实 TLS 回归。该层尚未接入 App、备份调度或管理入口；本地一致快照、内容闭包、认证加密格式与离线恢复仍待完成，不能据此宣称 F4 已可用。CLI/GUI 不引入此服务端依赖。
- 快照前置补记（2026-10-31）：内部源观测增加有界前缀读取、文件身份与关闭检查，以及必要的格式、算术溢出和预算拒绝。成功仅返回 `NeedsCertificate`，不是可用快照或源访问授权；尚未接入 Store/App，不授权 SQLite 连接或备份调用，不能代替一致性屏障、完整资源上界证明和恢复验收。
- 加密容器补记（2026-10-31）：内部备份层加入有界认证加密分片、分页清单和完成标记，以及独立解封校验；清理失败与关闭不确定性优先于普通取消，失败不返回可用结果、不自动重试或退还未确认资源。该层已通过独立代码与行为验收，但尚未接入一致快照、完整内容闭包、App/调度或运行代恢复；不能据此宣称完整备份/恢复可用，也不代表进程总 RSS 上界或外部密码学审计。

### F5 pack 分组分享 + 中转加速 + 自动更新（核心差异化功能）
1. **打包**：用户从自己添加/同步的仓库里选 skill 组合成 pack（例：仓库A的 a1、a2 + 仓库B的 b1、b2）。
2. **分享 token**：每个 pack 生成分享 token（签名、可撤销、可设过期）。客户端填入 token 即可订阅该 pack。
3. **服务端中转加速**：服务端定时拉源仓库更新；下载产物（tarball/快照）按 commit sha 或内容 hash 缓存；**客户端只访问服务端，不直连 GitHub**。
4. **客户端自动更新**：客户端本地记录已装 skill 的内容 hash，与服务端 manifest 比对，增量下载变化的 skill。
5. **去重**：跨 pack / 跨仓库重合 skill 按 `slug + content hash` 判重，同一 skill 只装一份、只更新一次。

- 实现补记（2026-10-28）：已接入用户隔离的 Pack CRUD、revision CAS、跨仓库选择与安装别名，以及 Web 管理、分享创建/列表/撤销和服务端 Connect manifest/二进制流。单用户最多 100 个 Pack，每 Pack 1–256 个技能选择；同别名不同内容拒绝保存，manifest 对同别名相同内容稳定去重。Linux 客户端订阅/安装/跨 Pack 去重、显式同步及备份卸载的限定范围已通过独立复验，见 `CLIENT-SUBSCRIPTIONS.md`；源自动更新、持续客户端调度与客户端配置附件仍待完成，不算 F5 或前端全部完成。
- 实现补记（2026-10-30，仅配置附件存储层）：已通过独立验收的 owner-only Store 支持每 Pack 最多 16 条有序配置版本引用、Pack/附件组双 revision CAS、旧技能更新保留附件，以及被引用配置的事务内删除保护；配置更新后引用明确变为 stale，不自动追随新版本。读取只返回引用元数据，不复制配置内容或密钥，MCP 上下文不能访问。全部已登记目标仍为 NativeUnavailable；本片不包含附件 RPC/Web/MyGo、配置消费或授权自动应用，也不代表 F5/F12 或正式发布完成。
- 分享编码决策：`shp1.` 加严格规范化的 base64url；二进制包含版本、随机分享 ID、32 字节随机 nonce、签发/过期秒数与 HMAC-SHA256。签名 key 通过外部主密钥的独立 HKDF 域派生，服务端只存完整 token 的 SHA256 和有效期/撤销状态。token 仅创建时返回一次，不进入 URL、日志或浏览器持久存储；可长期有效或最长 366 天。分享读取当前 Pack，下载绑定当时的 revision/成员/content hash，并在每块发送前重新核验数据库授权。已发送字节无法收回；检查与实际发送间仍有竞态窗口。详细协议见 `PACK-DISTRIBUTION.md`。

- 实现补记（2026-10-30，仅配置附件所有者 RPC）：已通过独立验收的独立 Connect 服务支持读取及整组替换附件元数据，绑定 Pack/配置组/资产版本并保留顺序，支持 stale 状态与配置被引用时的有限 typed 删除错误；普通用户会话仅能操作自己的 Pack。请求鉴权、限界、取消及服务端关闭排空纳入真实 TLS/App/SQLite 验证；分享与公开分发仍仅包含 skills，不返回配置内容或密钥。全部目标仍为 NativeUnavailable；Web/MyGo 附件管理、配置消费及授权自动应用尚未由本片交付。

### F6 插件导入系统（moejs）
- 实现补记（2026-10-28）：Magpie 已接通 Web 选择授权 JSON 导出、服务端有界 moejs 预览和显式勾选逐仓收录，默认不勾选，预览不入库，收录复用 F2 的权限/私有默认值/缓存验证/去重。仅处理公开仓库候选，不等于迁移本地 skill 内容或配置；cc-switch 上传、完整 manifest/第三方插件、客户端配置附件与自动应用仍待完成。细节见 `MAGPIE-IMPORTER.md`。
- **运行位置（用户明确确认）**：插件及内置适配器只在服务端执行；CLI 与 MyGo 客户端不加载 moejs、不执行插件。客户端负责选择与上传明确授权的导出数据，以及从服务端获取并应用 skill 和附带配置。
- 用户补充（2026-10-28）：评估将不同 AI agent（如 Claude Code、Codex、Gemini、OpenCode）的技能目标元数据、配置解析与配置渲染抽象为同一套服务端 moejs 适配插件。来源导入适配器与目标 agent 适配器分工明确；插件只输出经宿主校验的声明式结果，不将任意脚本、命令或绝对文件路径下发给客户端。客户端保留原生的权限、受控路径、预览确认、原子写入、备份和回滚内核；不改变服务端插件边界。设计与实现状态见 `AGENT-ADAPTERS.md`。
- 服务端仅处理用户上传的隔离导入数据，或管理员显式配置的服务端来源；RPC 请求不能指定任意服务端文件系统或数据库路径。文中 `~` 数据源路径仅供服务端受控读取管理员授权的本机来源，不能解释为远端客户端路径。
- 插件 = JS 模块 + manifest（你设计格式）。钩子建议：`detect()`、`listRepos()`、`listInstalledSkills()`、`importRepos()`（映射为内部 repo 记录）。
- 宿主 API 白名单：`fetch`（域白名单）、`storage`（kv）、`log`；每插件一个 runtime 实例隔离。
- 基于 moejs 的 cc-switch 和 magpie 适配器支持用户分别选择导入 skill、仓库和客户端配置；实际支持的配置种类以源数据和兼容性验证为准，不把所有配置默认视为可安全迁移。
- 客户端配置属于辅助功能：用户组建 skill pack 后，可额外勾选导入的客户端配置并随 pack 提供给客户端；skill 仍为产品核心。
- 导入前应可预览选择及冲突；敏感配置不得明文写入日志，保存和传输须按密钥要求保护。插件只通过受控宿主能力读取所需数据，不直接获得无限制文件系统或数据库权限。
- **内置适配器与第三方插件同机制**，首批四个：
  | 适配器 | 数据源 | 实测备注 |
  |---|---|---|
  | cc-switch | `~/.cc-switch/cc-switch.db`（SQLite）表 `skill_repos(owner, name, branch, enabled)` | 开发机装有 cc-switch 则直接联调（作者机器实测有 10 条真实数据），否则按该表 schema 构造 fixture 测试；**branch 可能是 `HEAD`**（vercel-labs/skills 那条），需回退解析 |
  | magpie | 探测 `~/.local/share/magpie/library/library.json` 等多路径 + CLI `magpie library skill list` 回退 | 本机**当前无 library.json**（用户没用 magpie 装过 skill），适配器必须静默跳过不报错 |
  | npx skills | `~/.agents/.skill-lock.json`，只认 `source_type == "github"` 条目 | 顺便实现写侧兼容（将来互操作） |
  | 通用 | 扫 `~/.agents/skills/` 目录 | 兜底 |

### F7 客户端安装适配（CLI/GUI 共用内核）
- 安装：下载 → 放入 canonical `~/.agents/skills/<slug>/` → 为检测到的各 agent 建 symlink：`~/.claude/skills`、`~/.codex/skills`、`~/.gemini/skills`、`~/.config/opencode/skills`、`~/.cursor/skills`、`~/.codeium/windsurf/skills` 等（完整表可参考 vercel-labs/skills `src/agents.ts` ~79 条或 magpie `internal/library/targets.go`）。
- 客户端支持导入和应用 skill；订阅或应用 pack 时，自动应用用户选择附带的客户端配置。配置应用应保留无关已有配置，处理冲突并记录自身变更，以支持恢复或回滚；具体格式及冲突规则在实现前验证。
- 目录不存在的 agent 跳过，不制造垃圾目录；卸载只回收自己写的东西（记录 Applied 映射）。
- 更新检测：内容 SHA-256；卸载先备份。
- 平台实现补记（2026-10-31，预览源码）：密钥核心已复用同一受控实现面向 Linux/Darwin 构建；Darwin 运行资格仍须由对应提交在真实 macOS arm64、两种 APFS 卷上的安全与隔离测试证明，交叉编译不能替代。CLI 与 MyGo 调用入口仍明确限制 Linux，尚未解锁 Darwin 本地安装、配置应用或 Pack 生命周期；不得将密钥核心变更称为完整 macOS 支持。
- 平台验收补记（2026-10-31，仅密钥核心）：`f422bb1` 在实际 macOS 15.8.2 arm64、Go 1.27.3、CGO0 及两个独立 APFS 卷上通过 48 项共用安全测试和 3 项调用隔离测试，并经独立 artifact 验收；五项产品操作仍为预期的普通失败，CLI/MyGo/安装/配置/Pack 尚未取得 macOS 运行资格。精确提交、环境、证据 hash 和旧失败历史见 `DARWIN-CLIENT-KEY-CORE.md`，不代表其它平台或完整首发完成。

### F8 公开技能目录与匿名安装（用户补充，2026-10-28）
- 后台允许用户将自己收录/维护的 skill 仓库显式勾选公开；默认不公开。GitHub 仓库本身公开不等于自动发布到 SkillHub。
- 提供类似 skills.sh 的免登录公开目录，可浏览、搜索、按分类筛选技能及查看来源、描述和安装信息；保留 §2 的 skills.sh 外部搜索接入，不以本地目录替代它。
- CLI 与 MyGo 客户端可以从 SkillHub 服务端匿名安装公开技能，不要求分享 token、MCP token 或登录；仍遵循 canonical、hash 验证和去重模型，不直连 GitHub。
- 公开元数据和下载使用明确的匿名接口，不让空 token 绕过原有私有 Pack 鉴权。取消公开后禁止新的浏览/详情/下载授权；流式下载继续逐块核验，已发送数据无法收回。
- 只公开明确发布的 skill 内容与必要元数据；不公开 GitHub 凭据、私有 Pack、导入的客户端配置、账号敏感信息或服务端文件路径。发布前提示 skill 全目录（含二进制/隐藏文件）将可被匿名下载。

### F9 技能分类（用户补充，2026-10-28）
- 用户可维护自己的分类，并为自己的 skill 选择多个分类；分类是管理元数据，不修改源 SKILL.md 或内容 hash。
- 后台支持分类管理、分配和筛选，公开目录只暴露已公开 skill 使用的分类与计数。未分类技能仍可浏览和安装。
- 分类与关联必须按用户隔离；不能通过猜测 ID 修改其他用户分类或将私有技能带入公开结果。删除分类仅移除关联，不删除 skill、仓库或本地安装。

### F10 MCP 管理入口与用户 token（用户补充，2026-10-28）
- 服务端提供 MCP，让 AI 通过受控工具维护用户自己的仓库、skill 分类、Pack 等已实现能力；采用成熟的官方 MCP SDK，不自行重写协议。
- Web 后台可创建、命名、查看元数据、轮换和撤销自己的 MCP token，并选择明确的权限范围和有效期；原文只在创建/轮换时显示一次。后续列表不返回原文，只持久化校验摘要及授权/过期/撤销元数据。
- MCP token 与 Web 会话、GitHub token、Pack 分享 token 分离，只经 Authorization Bearer 发送，不放入 URL、日志或浏览器持久存储；不允许通过 MCP 自行扩权或创建管理 token。
- 每次工具调用重新核验 token、用户归属、权限、有效期和撤销状态；所有查询和修改复用现有领域校验，不能越权维护其他用户数据、读取密钥、执行任意 JS/命令或访问任意服务端路径。
- 读与写权限分开，发布/取消公开需要明确的发布权限；审计仅记录必要的 token 标识、操作、资源标识与结果，不记录敏感输入。限制请求大小、并发、调用时间与生命周期，关闭可取消并等待在途操作。

### F11 Web 主题与多语言（用户补充，2026-10-28）
- Web 提供浅色、深色和跟随系统三种主题，默认跟随系统；系统配色变更时仅在跟随系统模式下更新。
- 首批语言为简体中文和 English；使用成熟的国际化库统一管理界面文案，覆盖登录/注册、匿名公开目录及所有管理页面的操作、空状态、校验、错误提示和无障碍标签。用户自己的 skill 名称、描述及源内容不自动翻译。
- 主题和语言切换无需重新登录，不清空当前未提交的表单草稿，并在刷新、重新打开页面后保留偏好；浏览器持久存储仅保存这些非敏感偏好，不保存 token。保持正确的文档 lang 和表单可访问性。
- 两种主题均须保证可读性、键盘焦点和对比度；尊重 prefers-reduced-motion，不以主题初始化为由放宽 CSP 的脚本策略。

### F12 AI 配置管理与应用（用户补充，2026-10-28）
- 参照 cc-switch 的实际交互完善 AI 配置管理，不将需求缩减为仓库导入或仅保存原始 JSON。建立来源版本可追溯的能力矩阵，覆盖各 agent 支持的 provider 新建/编辑/删除/复制/切换、端点与模型、结构化表单与原始配置编辑、MCP 和提示词等配置类型；仅对经过格式验证的能力开放应用，不静默丢弃不支持字段。
- cc-switch / Magpie 导入须独立选择 skills、仓库和客户端配置；客户端配置可单独维护，并可显式附加到以 skill 为主的 Pack。GitHub 凭据与程序自身的会话、MCP 管理 token 不作为 agent 配置自动导出。
- Web 管理和 MyGo 原生客户端应提供能力一致的配置选择、预览/差异、冲突处理、应用状态及恢复入口；Connect RPC 可以增量扩展，但不复用已有字段标签或破坏旧客户端。自动应用只针对用户明确启用的配置及目标 agent，不因登录、导入预览、浏览公开目录或安装公开 skill 而写入 agent 配置。
- 配置敏感字段加密落盘，只向明确授权的所有者及配置消费方返回；公开技能目录和默认的 Pack 分享不能泄露配置密钥。配置写入保留无关字段，使用 revision / 内容摘要检测并发本地修改，先备份、失败可恢复，卸载只处理自身变更。

- 实现补记（2026-10-30，仅 OpenCode Web 配置资产）：已通过独立验收的专属结构化编辑器覆盖 V1/V2 字段、递归值与有序集合、凭据引用的 KEEP/REPLACE/CLEAR、最后引用移除时同次 CAS 确认清除、无密钥复制、未知字段只读以及冲突和失包后的禁止重放。主题、语言、键盘与移动布局纳入真实 TLS/SQLite 浏览器验证；仅保存服务端私有资产，不安装 SDK、不选择 provider、不应用到设备。跨资产持久排序、其它 agent 及完整 Web/MyGo/native 能力一致性仍待完成，不代表 F12 或正式发布完成。
- 实现补记（2026-10-31，仅 MCP 配置资产服务端与 Linux CLI）：独立 Connect 服务与 `cognate mcp-assets` 提供列出、元数据读取、脱敏草稿读取、逻辑预览、新增、更新、无密钥复制、删除八项所有者操作；这些是私有客户端 MCP 配置资产，不是 F10 的站点 MCP 管理 token。可编辑格式为既有 Claude schema v1，未知格式仅提供元数据；插件规划仍只在服务端执行。
- MCP CLI 会话仅从有界非 TTY 标准输入读取，凭据变更采用独立私有文件；默认不提交变更，`--apply` 需真实控制终端完整审阅并明确确认。revision/CAS、旧引用的显式 CLEAR、无密钥复制和不确定结果禁止自动重放均经过真实 TLS/App/SQLite 验证；不导出已有密钥、不启动 MCP 进程、不应用本机配置。Web/MyGo 的 MCP 配置编辑、原生应用和完整跨平台客户端能力仍待完成。
- 实现补记（2026-10-31，仅 Codex 固定版本原生观察与应用引擎）：新增 Linux 上固定 0.166.0 可执行文件身份、受控启动参数和环境、真实 config/read 配置层及来源的 engine-owned 观察，绑定三份本地文件基线、选择与原始有效期；预览、确认、材料化、每次发布交换及恢复均重新核验。慢观察不得延长原期限，过期在材料接纳前拒绝；晚期冲突保留真实部分落盘状态，不虚构完成或回滚。该源码范围已经独立验收，固定新模板的完整消费者资格仍须单独核证；生产 profile 登记表仍为空，不代表 CLI/MyGo 生产应用、其它平台或完整 F12 已交付。
- 生命周期补记（2026-10-31，仅 Codex 应用核心与私有传输）：确认记录持久绑定操作、密钥槽、原始 revision 与毫秒期限；授权及消费前先记录 durable intent，未确认远端结果不得自动重放。明确区分消费前放弃和未发布操作退役，重启不恢复旧授权；旧记录不静默升级。已认证的生命周期记录跟踪实际 After-owned 模型，首次目录从缺失恢复时使用受限发布顺序，晚期失败保留部分状态供显式恢复。该范围已通过独立验收，但生产 profile 登记仍为空；新 effective-model profile、CLI/MyGo 接线与完整 agent 能力仍需分别验收，不能据此宣称 F12 完成。
- Pack CLI 补记（2026-10-31，仅所有者配置附件元数据）：`pack configurations list|replace` 通过当前所有者会话管理有序引用与双 revision CAS，默认只读预览；实际替换必须在同一控制终端完整复核并精确确认，`[]` 明确清空附件。未确认的提交结果阻止重放，只允许显式只读核对；本地解码或大小错误不能冒充服务端已拒绝。实际 CLI 对阻塞/关闭输出、帮助输出失败和取消安全退出，诊断不回显原始参数或会话。该片已通过独立验收，所有目标仍为 `NativeUnavailable`，不读取配置正文、不授予分享访问、不进行客户端应用，完整 F5/F12 仍待后续闭环；使用限制见 `PACK-CONFIGURATION-CLI.md`。
- 编排补记（2026-10-31，仅 Codex 共用客户端内核）：新增类型化应用、状态、恢复、还原、消费前放弃与未发布退役入口，复用既有观察/授权/消费及单引擎生命周期，不执行客户端插件。人机审批接收安全副本，准确批准原凭据槽并最终确认；Unknown 不重放，消费成功后本地失败不冒称未消费。状态可收敛真实本地日志；恢复等动作需要新计划和新审批。该范围已通过真实隔离消费者与 App/TLS/SQLite 的独立验收，但生产 profile 仍未登记，普通应用在任何路径或 RPC 操作前拒绝；尚不代表 CLI 控制终端、MyGo 接线、完整消费者资格或 F12 已完成。

- 实现补记（2026-10-30，仅 Claude MCP 连接配置领域层）：已通过独立验收的服务端私有配置资产支持 stdio、HTTP、SSE 三类完整有序声明、显式启停、凭据引用和加密存储、所有者隔离、版本 CAS、无密钥复制及只读预览。服务端 moejs 仅接收声明与凭据存在状态，其完整结果由 Go 独立校验；删除旧引用须在同次 CAS 中明确确认清除。有效但含 query 的远程 URL 整项返回 Unsupported，不删参数或放行。此处管理的是 agent 使用的 MCP 连接配置，不是 F10 的网站 MCP 管理 token；本片尚不提供连接配置 RPC/Web/MyGo 管理、外部 MCP 连接或设备应用，不代表 F12 或正式发布完成。

- 实现补记（2026-10-30，仅配置资产持久排序 Store 与所有者 RPC）：按 agent 和配置类型维护完整资产分区及独立排序 revision，支持完整顺序读取和全排列 CAS 替换；创建、复制、导入和删除在同一事务中维护成员关系，内容更新不改变顺序、内容 revision 或时间戳，空分区保留 revision 防止 ABA。顺序操作只读取元数据，不解密配置内容，不改变 Pack 附件的内容版本引用。两个新增精确 RPC 路径沿用配置请求的 128 KiB 上限，可传输完整 250 项分区；其余路径上限不放宽。此片已经过独立验收，尚不包含 Web/MyGo 排序控件或设备应用，不代表 F12 或正式发布完成。

- 实现补记（2026-10-30，仅 Web 配置资产分组排序）：已通过独立验收的 Web 排列入口读取完整 agent/类型分区元数据，通过上移、下移与独立排序 revision CAS 保存顺序；未知格式也可排列，不读取配置正文或凭据。冲突和未确认结果阻止继续写入，须显式读取当前顺序，不自动重放业务写入；保存排序不改变内容版本、Pack 引用或设备配置。中英、主题、移动布局、键盘位置播报与焦点恢复、所有者切换均纳入真实 TLS/App/SQLite 浏览器验收。此片不包含 MyGo 排序、provider 切换或设备应用，也不代表完整 F12 或正式发布完成。

### F13 服务端文件日志与后台设置（用户补充，2026-10-29）
- 服务端完善结构化运行日志，写入服务端私有数据目录下的日志文件；按文件大小轮转，并按保留数量、保留天数自动清理，避免无限占用磁盘。
- Web 后台提供日志级别、单文件大小、备份数量和保留天数设置，保存后持久化并生效；普通用户、匿名访问者和 MCP token 不能修改全局日志设置。管理员须由部署者明确授权，不能因首次注册自动获得管理权限。
- 日志不得包含 token、密钥、密码、请求/响应正文、AI 配置内容、上游临时下载地址或任意错误原文；后台不能指定任意服务器路径。日志故障须可观察，但不能通过日志失败泄露敏感信息或改变业务授权。
- 详细默认值、并发/保留边界与实现验收见 `SERVER-LOGGING.md`；新增需求不代表已经完成。

---

## 5. 非功能需求
- 全链路并发拉取（goroutine + 限速，尊重 GitHub 归档下载的 429）。
- 单二进制交叉编译（GOOS/GOARCH 全覆盖 Windows/macOS/Linux）；一切选型不得引入 cgo。
- 配置文件 + 日志；服务端数据目录与客户端缓存目录分开。
- 密钥（GitHub token、分享签名 key）加密落盘，**任何日志/回复中不得明文出现**。
- 自动构建 CLI 客户端的跨平台二进制和 Docker 镜像；桌面客户端实现后也纳入自动打包。构建全程禁用 cgo，构建与发布分开；用户已授权独立验收后发布首个可用版本，真人测试留到首发后。
- 前端开发遵循 `ui-ux-pro-max`、`emil-design-eng`、`apple-design`、`animate`；数据库设计遵循 `ddia-principles`；异步和并发设计、性能验证遵循 `chao-go-sync` 和 `chao-go-perf`。
- Go 使用开发机已安装的 Go 1.27.3 与 `use-modern-go` 对应的现代语法和实践。

## 6. 数据模型草案（ent schema 起点，可调整）
`User` / `GithubToken(加密)` / `Repo(owner,name,branch,source:star|manual|plugin,enabled,published)` / `Skill(slug,name,description,path,frontmatter,contentHash,repoID)` / `Category(user,name)` / `SkillCategory(skill,category)` / `Pack` / `PackItem(pack,skill)` / `ShareToken(tokenHash,packID,expiresAt,revoked)` / `MCPCredential(user,tokenHash,scopes,expiresAt,revoked)` / `SyncState(listSlug,watermark,updatedAt)` / `BackupJob` / `Plugin`。

## 7. 验收要点
1. F1：能从真实 GitHub 账号拉到 lists 并按勾选同步出仓库列表（用用户的真实 GitHub 账号走 device flow 验证）。
2. F5：两个 pack 含重合 skill 时，客户端只装一份；源仓库更新后，客户端在下轮同步拿到新版本。
3. F6：cc-switch 适配器能列出 skill_repos 且 branch=HEAD 条目不崩；开发机无 cc-switch 环境时用 fixture（表结构见 F6）。
4. F7：安装后 `~/.claude/skills/<slug>` 为有效链接，卸载后无残留。
5. F8/F9：默认私有的仓库不出现在匿名目录；显式公开后游客可搜索/分类筛选/查看并免 token 安装，二进制与 hash 验证通过；取消公开立即阻止新的授权，私有数据不泄露，分类删除不删 skill。
6. F10：用户可在后台管理自己的 MCP token；只读 token 不能写、过期/撤销/其他用户 token 不能越权；AI 能通过真实 MCP 客户端维护获准的业务资源，token 不出现在日志和后续列表中。
7. F11：未登录与已登录页面均可切换主题和中文/English；刷新保留偏好，跟随系统实时响应系统配色，键盘操作及窄屏可用，无未翻译的界面 key，持久存储不含凭据。
8. F12：按 cc-switch 能力矩阵逐项验收实际支持的 agent 配置交互；导入、编辑、切换、Pack 附加、客户端差异预览/自动应用/冲突拒绝/恢复形成闭环；敏感字段不进入日志、公开目录或浏览器持久存储。不同 agent 的插件渲染结果必须经过宿主验证，客户端不加载 moejs。
9. F13：真实服务端请求产生日志文件；管理员可在中英文后台修改参数并跨重启保留，未授权修改被拒；轮转、数量/期限清理、并发写、磁盘故障与停止清理有行为测试，恶意请求中的合成密钥不进入文件或 stderr。

## 8. 里程碑建议
M1 仓库拉取+扫描+CLI 浏览 → M2 本地安装适配+去重 → M3 服务端 API+用户+star 同步 → M4 pack+分享 token+中转 → M5 备份 → M6 插件系统 → M7 GUI。

M1 的 `browse` 是独立开发与浏览模式，直接拉取 GitHub 仓库；未来 pack 订阅和同步客户端必须遵循 F5，只访问 SkillHub 服务端。cc-switch / magpie 客户端配置导入和 pack 附加配置属于后续里程碑，不算 M1 已交付。

---

## 9. 背景记忆检索指南（nmem）

本机记忆系统命令为 `nmem`（已在 PATH）。检索：

```bash
nmem m search "关键词"          # 语义+bm25 混合检索
nmem m search "关键词" --json   # 结构化输出
```

与本项目相关的记忆（可直接用 ID 或关键词命中）：

| 记忆 ID | 内容 | 推荐关键词 |
|---|---|---|
| `d6c6a3f9` | cc-switch/magpie/npx-skills 三家源码逆向结论（获取/安装/适配差异） | `skill 安装`、`skills.sh`、`SKILL.md 扫描` |
| `064ab8a1` | GitHub star Lists GraphQL 同步结论（含 REST 不支持、32 上限、水位线） | `star lists`、`UserList GraphQL` |
| `89d6bd03` | device flow 申请 token 全流程要点 | `GitHub token`、`device flow` |
| `c35f77cf` | SkillHub 完整方案（功能拆解、插件导入、中转加速设计） | `SkillHub`、`分组分享`、`插件导入` |
| `ccfed0c3` | 技术栈讨论记录 ⚠️ 其中**前端选型部分已解锁**，以本文档 §3 为准 | `SkillHub 技术栈`、`moejs`、`mygo` |

检索技巧：用专有名词（`skills.sh`、`UserList`、`device flow`）命中率最高；泛词（`symlink`、`token`、`GitHub` 单独用）会命中无关记忆。
