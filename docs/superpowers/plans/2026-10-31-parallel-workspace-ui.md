# Workspace UI Parallel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 并行交付登录/账号、仓库/分类及 Pack/分享三个可独立验收的 UI 切片，缩短首发 UI 门禁的关键路径。

**Architecture:** 保留 React、TanStack Router/Query、现有 Radix/shadcn 风格组件封装。各切片使用自己的 CSS 和 i18n 注册模块，不改共享路由、API、样式或英文总表；主线程负责来源页候选集成与最终同组合验证。

**Tech Stack:** React 19、TanStack Query/Router、Radix、Lucide、现有 Go Echo/Connect/SQLite 测试 fixture。

**Spec:** `docs/REQUIREMENTS.md`、`docs/UI-DESIGN.md` 首发逐页验收账本。

## Global Constraints

- UI 使用成熟框架、框架的组件；复用既有 Button/Input/Checkbox/Select/Dialog，不增加依赖或自研基础控件。
- GUI 仍为 MyGo 原生；本计划只改变 Web，不以 Web 证据代替原生验收。
- 深浅/跟随系统、中英、不丢未提交草稿、375px 与 320px/200% 文字、真实 loading/empty/error、键盘与焦点恢复、ReduceMotion。
- 动效只服务定位、直接反馈与状态关系，不加持续背景装饰、假统计、假进度或输入延迟。
- 破坏性与含密钥动作保持现有授权/确认语义；没有实现的能力明确标记不可用。
- 所有 Go 命令显式 `CGO_ENABLED=0`；不使用 race/cgo。使用系统 Go 1.27.3 与新版语法。
- token/密钥不进入日志、截图、提交；只用 synthetic 凭据，不读取真实配置或账号。
- 只使用 apply_patch 修改源码。各 worker 在私有 archive 副本工作；不创建分支、不改主线、不提交或推送。
- 不改 `web/src/styles.css`、`web/src/main.tsx`、`web/src/i18n.ts`、`web/src/locales/en.ts`、生产 Go/API、Buf schema 或其它 worker 文件。

## Review Focus

- 后台查询失败而仍有缓存时，明确显示未知/错误，不能显示成已停用、空列表或操作成功。
- 语言/主题切换、查询刷新、窄屏换行不丢草稿、选择或键盘焦点。
- 长英文在 320px/200% 下整词可读，重要动作可以真实 Tab/滚动揭示并触发。
- 分享 token/密码只在原授权路径显示，截图前必须清空或遮挡；撤销不能承诺收回已安装内容。
- mutation 的冲突、失败或丢回执不自动重放，局部 UI 打磨不扩张功能或授权范围。

## Main Integration

- [x] 固定来源 v2 工件的 11 文件身份；核对相对 main `59da539` 可应用。
- [x] 使用 apply_patch 集成，逐文件 SHA256 一致；Web build、来源六阶段回归和 100 轮 RPC-only 通过。
- [x] 接收原独审 Hilbert 的 N1 修后 Spec/Quality ACCEPT，不以作者绿色替代独审。
- [x] 最终冻结新组合真实来源浏览器、备份回归、7919 PASS 全仓及十八目标 CGO0 构建；具体证据见 `VERIFICATION.md`。
- [ ] 通过后按阶段提交并 SSH push，读回远端，记录 Nowledge 进度。

## Task 1: 登录与账号

**Files:** 修改 `web/src/auth.tsx`、`web/src/account.tsx`；新增 `web/src/auth-ui.css`、`web/src/auth-ui-i18n.ts`、`internal/server/httpapi/auth_ui_browser_fixture_test.go`、`scripts/verify-auth-ui.mjs`、`docs/AUTH-UI.md`。

**Interfaces:** 保留 `AuthScreen`、`AccountPage` 导出及现有 auth RPC；新 i18n 模块由本切片组件自行导入。

- [ ] 读取现有登录、注册和 OIDC 绑定协议及共享组件；记录实际基线问题并写先失败的针对性验收。
- [ ] 打磨信息层级、材质、表单和反馈；登录方式查询失败提供明确重试，不伪装 OIDC 未启用。
- [ ] 保留本地登录、register→login→GetSession、显式绑定、不按邮箱合并与密码不回显边界。
- [ ] 真实生产 build→TLS Go/Connect/SQLite fixture 覆盖登录/注册、loading/error、OIDC 未启用/启用/失败、账号绑定提示和身份。
- [ ] 覆盖全局视觉/键盘矩阵，保存安全前后截图和复现命令，冻结限定写集补丁与 SHA256。

## Task 2: 仓库与分类

**Files:** 修改 `web/src/repositories.tsx`、`web/src/categories.tsx`；新增 `web/src/repositories-ui.css`、`web/src/repositories-ui-i18n.ts`、`internal/server/httpapi/repositories_ui_browser_fixture_test.go`、`scripts/verify-repositories-ui.mjs`、`docs/REPOSITORIES-UI.md`。

**Interfaces:** 保留 `RepositoriesPage`、`CategoriesPage` 导出、既有 collection/category RPC、PublicationControl/SkillCategories/RepositoryRefreshControl；只读其它子组件。

- [ ] 先用真实 fixture 验证收录/技能详情/分类的加载、错误、空和长内容基线，保留可复现 RED。
- [ ] 打磨仓库选择、分支与快照层级、筛选与分类编辑/删除；不能把来源列表误称已收录内容。
- [ ] 保留公开 opt-in、私有源授权、刷新/完整性失败及分类所有者隔离边界，不添加假公开状态或计数。
- [ ] 真实生产 Web/Connect/SQLite 验证真实选择/收录/筛选/分类 CRUD、错误重试与取消，不用 RPC mock 代替业务成功。
- [ ] 覆盖全局视觉/键盘矩阵，冻结限定写集、截图及证据，局限明确记录。

## Task 3: Pack 与分享

**Files:** 修改 `web/src/packs.tsx`、`web/src/pack-shares.tsx`；新增 `web/src/packs-ui.css`、`web/src/packs-ui-i18n.ts`、`internal/server/httpapi/packs_ui_browser_fixture_test.go`、`scripts/verify-packs-ui.mjs`、`docs/PACKS-UI.md`。

**Interfaces:** 保留 `PacksPage` 和 `PackShares` 导出与既有 Pack/share RPC，不实现或暗示缺失的配置附件自动应用。

- [ ] 记录当前列表/编辑/重合技能/分享/删除的真实基线，写针对性失败验收。
- [ ] 打磨选择、修订信息、编辑表单、分享范围与危险区域，复用成熟组件；不改变 CAS 或 mutation 次序。
- [ ] 明确过期、撤销、一次显示及已分发内容无法收回；保留权限、容量与结果不明边界。
- [ ] 真实生产 Web/Connect/SQLite 验证创建/编辑/删除/分享/撤销与失败恢复；回执不明不自动重放。
- [ ] 覆盖全局视觉/键盘矩阵，保存无秘密截图，冻结限定写集与可复现证据。

## Acceptance And Delivery

每个任务先做针对性测试和 Web build，不重复全仓测试或全平台构建。主线程接收冻结工件后做组合验证；独立 reviewer 做 spec compliance 与 quality 双结论。未通过或未验收的切片继续 OPEN，不阻塞其它已通过切片的小阶段提交，也不能被计为完整 UI/正式版完成。

## Task 4: MCP Token 与管理员日志页面

本任务在独立副本开发，不并入尚未冻结的前三个切片；沿用全部 Global Constraints。目标是推进逐页账本，不增加客户端 MCP 配置能力或改变全局日志授权。

**Files:** 修改 `web/src/mcp-settings.tsx`、`web/src/mcp-settings-i18n.ts`、`web/src/server-logging.tsx`、`web/src/server-logging-i18n.ts`；新增 `web/src/management-ui.css`、`internal/server/httpapi/management_ui_browser_fixture_test.go`、`scripts/verify-management-ui.mjs`、`docs/MANAGEMENT-UI.md`，恰八文件。

- [ ] 核对当前真实空/加载/错误、Token 创建/轮换/撤销/过期、日志管理员/非管理员与 CAS/Unknown 协议，先保留针对性 RED。
- [ ] 改善信息层级、长权限说明、一次显示区、危险操作确认及日志保留参数/健康状态；使用现有 Radix 与框架组件，禁止自造基础控件、虚构统计或模糊管理 Token 与 agent MCP 配置的区别。
- [ ] Token 原文仅创建/轮换成功时在内存出现，拒绝秘密截图及日志，刷新/离页不回显；权限/有效期与逐调用撤销保持真实服务端语义， mutation 不自动重放。
- [ ] 日志设置只有显式管理员可改；错误/冲突/结果不明保留草稿并阻止重放，显式核对不虚构保存成功，日志路径不可自选。
- [ ] 生产 Web → 真实 TLS/Connect/SQLite fixture 验证业务与失败；中英/深浅/系统、320/375px/200% 根字号、真实键盘/焦点、ReduceMotion；异步查询失败和触发器卸载后有稳定焦点。
- [ ] 浏览器脚本必须拒绝 fixture 非零退出、signal、超时及缺失收据；保存针对性正常/异常自测、真实无秘密截图、八文件补丁/源码摘要与未验边界，交非作者独审。

## Parallel Server Repair

本次组合的真实备份浏览器在 seed 的 GetBackupJob 认证阶段失败。主线程已在旧 `59da539` 的隔离副本仅增加安全数码诊断，第四个有界完整场景捕获 `SessionUser` SQLite 8714 / SQLITE_IOERR_IN_PAGE；此前三个绿轮保留，不能冒充已修。与 GitHub 的 517 是不同故障，也不能据此宣称历史 v7 已全部归因。

服务端实现线负责 `local_snapshot_source*` / `local_snapshot_worker*` 及必要维护接缝和测试、既有备份浏览器 fixture 的安全诊断、新 `docs/BACKUP-SQLITE-LOCKS.md`。先确定性验证源文件描述符 open/close 是否释放活跃 SQLite 的 POSIX 锁，再决定最小修复；本段只记录待验证假设，不作为根因结论。不能移除预算/安全路径/能力 pin，不能以 retry、全局 mutex、切换 journal_mode 或降级备份掩盖。需要扩张生产接口或其它写集时先回报主线程。

## Integration Ledger

- 来源 v2 已集成；N1 取消等待四格修复已集成，限定独审 Spec/Quality ACCEPT。原 BLOCK 报告不重写。
- Task 1：fix round 1/5 完成，I1/M1 均由原非作者 reviewer 复审 CLOSED，Spec/Quality ACCEPT；最终组合完整专项通过。
- Task 2：fix round 1/5 完成，F1/F2 均由原非作者 reviewer 复审 CLOSED，Spec/Quality ACCEPT；最终组合完整专项通过。
- Task 3：fix round 1/5 完成，后台失败焦点 F1 由原非作者 reviewer 复审 CLOSED，Spec/Quality ACCEPT；最终组合 132 条证据和独立五项焦点观察通过。
- 备份：六文件身份核验并集成，非作者独审 ACCEPT。子进程锁观测及 3-byte 受控截短已证实机制；新组合 630 次备份和真实管理页通过，历史 v7 仍未归因。
- 最终全仓首次在 broker fixture-connect 失败；主线程用真实 syscall 延迟证明错误就绪机制，新增测试就绪握手，修后全仓 7919 PASS / 166 SKIP / 0 FAIL；Hilbert 独立编译及真实时序对照后限定 Spec/Quality ACCEPT。原失败保留，不混同旧 OpenSession 或等待断言。
- Task 4：作者恰八文件冻结候选已交付，Hilbert 在隔离 archive 做非作者独审；未集成、未签收，不并入前三组的绿色证据。
- 集成限定独审对稳定 CSS/i18n、CI 门禁及覆盖账本给出 Spec/Quality LIMITED ACCEPT，新增集成阻断 0；最终 runtime 1319 项与 Web dist 52 项前后 hash 一致，十八目标构建/metadata 通过。
- 当前尚无阶段提交、push、tag 或新的 Docker/LPK；审核和组合门禁通过后再交付，不发布伪 GA。

本线与三个 UI 写集互不重叠。主线程继续来源页阶段集成和进度交付，待此故障修复并独审后重新做受影响组合门禁；目前不发正式标签。
