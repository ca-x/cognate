# MyGo 三类选择导入完整产品接线

## 授权与范围

主线程按完整 F6/F12 目标授权下列 exact12 私有实现；不是功能验收或正式版放行。
固定基线 `2cd75680cc7d2b1aa33860fb8f64eb1624d32c22`，以 git archive 新建私有树，
不改主线、他人冻结或创建 branch/worktree。精确路径见同名 writeset.tsv；需要其它路径先报具体缺口。

权威需求为 `docs/REQUIREMENTS.md` F6/F7；复用 `IMPORT-SELECTION.md`、
`IMPORT-SELECTION-PROTOCOL.md`、`NPX-SKILLS-IMPORT.md` 及现有 importclient 的完整合同。
本片补齐 MyGo 对已实现三种来源的真实产品流程，不以仅仓库旧入口、渲染截图或 mock service
代替选择、确认、真实提交和回读；不缩减完整 F6 的其它来源、内容迁移、兼容写回或插件义务。

## 共享接口与边界

- 保持普通生产前门 Page=2，导航更新为「导入」，内部分离新版选择导入与显式旧版 Magpie
  仅仓库流程；旧能力继续可达、不得暗改旧 RPC 语义。不要新增顶层页面、第二套导入事务或依赖。
- 生产 Service 复用 importclient.SelectionClient 的 PreviewFile、BeginSelection、CommitFile、
  Status 和 Close。来源为 cc-switch schema24、自包含一致 SQLite 快照，Magpie JSON 与
  npx skills v3 global lock；不得扫描真实用户默认目录、读取运行中 SQLite/WAL 或执行客户端 moejs。
- 仅显式填写/选择导出路径不读取和上传；独立确认「整个文件可能含凭据，授权上传到此 origin」
  后点击预览才读。文件身份、大小、私有 regular-file/路径校验及提交前原 hash 重读沿用共享内核。
- 新 service/model 必须绑定真实 GetSession 所得 origin/owner/进程内会话；不从 UI 文本或
  preview 内容猜身份。可用独立可选 backend 接口，不扩大旧 Backend 对所有测试替身的要求。
- 仅使用 MyGo 原生 ui 组件、现有 native shell/主题/反馈与调度；不用 HTML/WebView、自制基础控件、
  临时明文文件或 Cobra。路径输入可沿用既有显式绝对路径方式，不为此新加文件系统权限。
- 不改 Configuration/MCP/Grok/资产排序模块或其现有接口。本片 model.go/view.go 的入口变化与
  后继 C7 由主线程串行整合，不能取他人活 WIP 为输入。

## 完整用户流程

1. 选择来源和授权文件，明确 origin/上传风险，点击预览；默认三类均不勾选、不提交或安装。
   无效来源、非自包含/变化/过大/不安全文件、过期或错误身份均有安全错误状态。
2. 真实展示仓库、技能及配置候选、READY/需选择/不支持/冲突等状态，排除提示和凭据存在位。
   原始导出、ticket、来源秘密不进入 UI 字符串、模型可格式化图、日志、磁盘或通用缓存；
   View() 的无 ticket 副本不能扩大为原文编辑器。配置草稿和身份通过私有闭包持有。
3. 支持来源已实现的明确配置改名、Claude/Gemini 认证模式与 Gemini key transport，
   不将 native-login、API key、缺失模式自动等同。修改任何候选选项即失效旧预览/确认/选择，
   显式重新预览取得新票据；不修改原文件或伪造不支持配置。
4. 技能绑定必须显式读取当前 owner 已收录的同仓库/ref 内容，复用现有 ListRepositories /
   GetRepository 只读 RPC，选择完整 repository/archive/skill/logicalPath/contentHash 身份。
   不让用户手抄内部 ID、不按名称自动匹配、不把 npx git tree SHA 当 Cognate 内容 hash。
   未收录仓库先显式选择仓库提交，然后重新预览/绑定；读取失败或来源变化保持安全、可返回。
5. 分别勾选任意非空 READY 候选组合，并逐项确认所有 required notices；不支持候选不能选。
   最终审阅同时列出 origin/owner、来源、文件 SHA256/期限、三类选择、配置目标/模式及明确
   排除项，说明保存服务器私有资产/内容引用，不安装或应用设备、不迁移来源秘密。
   在这个审阅基础上另一次明确确认才 Begin/Commit；令牌输入和预览本身不表示批准。
6. 结果展示完整 SelectionResult 的三类计数、操作 ID、Observation/Phase，区分 Completed、
   Partial、Failed、InProgress、Unknown 和 NotObserved；不把取消/非零/缺回执说成回滚。
   不能只显示「成功」而抹去部分收录。客户端秘密补充、设备应用与 Pack 自动应用仍走各自功能。
7. mutation 发出后丢回复、取消或窗口离开，不自动再 Commit、换 ID 或重新预览绕开 Unknown；
   当前 origin/owner 的不明屏障保留到明确状态处理，提供显式只读查询原 operation ID。
   支持用户粘贴已保存的非秘密 operation ID 做状态查询，不上传原文件。NotObserved 不证明
   并发请求未来不会完成。不能把页面重载、重新登录或新 client 当服务器幂等已解决。

## 生命周期与并发

所有后台工作使用既有 generation、UI dispatcher 和有界 worker 机制；只有当前身份的结果
能发布。取消、换 origin/账号、Logout、Close 与页面切换应分别清理敏感 draft/preview/op/client，
并实际排空相关 worker；不要在持模型 mutex 或自身 callback 内同步 Close/Wait 导致自等待。
未知操作仅保留安全 origin/owner/ID 与状态；明确退出需提示保存 ID，不持久保存 bearer/ticket。
文件/来源/选择变更与已有工作取消及只读状态查询的关系要有单一、可测试的状态机，不能靠
控件 Disabled 作为唯一写门。Double activation 在 service/model 层也只发一次。

## 实施顺序与必需证据

1. 写失败的 service/model 测试，证明现有普通入口缺失、三类默认不选、未授权零文件读取/上传，
   再接真实共享 client。Go 使用系统1.27.3、CGO_ENABLED=0、modern-go 指引；无 race/新依赖。
2. 完成全部选项、同仓绑定、逐 notice/最终确认、回执与 Unknown 只读恢复及身份/取消排空，
   每个步骤保存 RED→GREEN 与真实请求计数；现有旧 Magpie 流程和其它 desktop 测试必须回归。
3. 新真实 App/TLS/SQLite/server-only moejs 集成覆盖三来源、仓库/技能/配置独立及混合选择、
   缺仓重预览、无源密钥迁移、改名/认证选项、stale/文件变化、过期、跨 owner、取消和提交后
   丢回执单写/状态只读。不得以 stub receipt 或绕过真实服务端声明成功。
4. ui.NewTester 验真实控件/默认关闭/键盘/滚动/错误及草稿；普通新编 MyGo binary 从前门真实
   导航至本页并捕获 Linux 窗口，不能用 direct-entry overlay 当普通前门。认证后 native 交互
   与 tester/合成 session 的证据明确分开；不拿无人窗口冒称真人或跨平台运行。
5. 定向测试/vet、六个 MyGo CGO0 构建及 metadata；正常来源闭包不能包含 server/moejs。
   不重复跑全仓或18构建，主线程统一最终组合。新旧失败、SKIP/setup/product 区分保留。
6. 冻结完整累计 patch/fullsource/evidence、精确写集、可复现命令及最终 REPORT/SHA，
   经非作者验收后主线合入；只凭作者 GREEN、截图、parser 或路径存在不算验收。

## 发布边界

本片是 MyGo 对现有三类选择导入的完整接线，不开放其它 provider/MCP/instructions 导入、
本地 skill 内容上传、npx 锁写回或客户端插件。已有服务端拒绝继续明确显示，不伪装迁移完成。
完整 F6/F12、平台/真实账号、首发 UI 全产品和正式分发门禁保持原要求。
