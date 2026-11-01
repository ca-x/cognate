# MyGo 三来源选择导入

普通桌面导航「导入」默认进入选择导入；「旧版 Magpie（仅仓库）」保留原 RPC 和原有操作。
选择导入保存服务器私有资产和内容引用，不安装技能、不应用设备配置，也不迁移来源凭据。

## 文件与身份

- 显式提供绝对导出路径，再独立授权「整个文件上传到此 origin」，点击预览才读取和上传。
  填路径、选来源或登录都不表示上传或提交批准。不扫描默认目录，不执行客户端插件。
- cc-switch 仅接受 schema24、一致自包含的 SQLite 导出，上限16 MiB；不能直接上传运行中的
  SQLite/WAL。Magpie library.json 和 npx skills v3 global lock 上限1 MiB。
- Unix 本地文件须为无 group/other 权限的 regular file；这是 service 的路径/权限前置检查，
  不是同一 FD 上的原子权限证明。无符号链接组件、源大小、原文 SHA256 和提交前原 hash
  重读由共享 importclient 负责。私有稳定目录、合作文件系统仍为前提，Windows ACL 未获运行资格。
- 服务以真实 GetSession 绑定规范 origin、owner 和当前进程会话；预览、内容元数据读取、提交
  及状态请求均重新核实身份。原始导出、票据、bearer 和配置草稿不进入通用缓存或格式化日志；
  票据和草稿由私有状态/闭包持有，取消或身份变更时清理并排空客户端。

## 选择、选项与审阅

三类候选默认全部不选。READY 才能选择；需选择、不支持、冲突与排除项都明确显示。
支持已实现来源的显式配置改名、Claude 认证模式、Gemini 认证模式与 key transport。
「保留来源解释」不是自动 native-login，也不把缺失凭据当成 API key 已就绪。
任何选项变化都会关闭旧票据、清除选择和确认，需重新预览。源文件不被修改。

技能必须先「读取同仓库内容」，通过当前 owner 的 ListRepositories/GetRepository 获取完整
repository/ref、repository ID、archive SHA256、skill ID、logicalPath 和 content SHA256，
再明确核对并绑定；不手抄 ID、不按名称猜测、不把 Git tree SHA 当内容 hash。
cc-switch/npx 的显式绑定需要重新预览。Magpie 沿用共享内核对来源固定目录的解析，只能明确
核对当前服务器返回的同一个完整 pin，不伪造共享 API 尚不支持的任意路径重绑选项。
未收录仓库时，先只选择仓库提交，再重新授权预览、读取并选择技能。

所有 required notices 必须逐项确认。最终审阅列出 origin/owner、来源、原文件 SHA256、
期限、三类选择、完整技能身份、配置目标/模式及排除项。审阅后另勾选明确提交确认，才调用
共享 BeginSelection/CommitFile；Model 与 service 都保留门，不依赖控件 Disabled 防止重放。

## 回执与取消

结果保留 operation ID、Observation、Phase、revision 与三类计数，不把 Partial、Failed、
InProgress、Unknown 或 NotObserved 简化成成功或回滚。仓库计数是仓库回执数，不是成功数。
配置凭据存在位只说明来源是否含值，导入后的凭据仍需通过相应配置资产功能明确补充。

写入开始前即保留单写屏障。丢回复、取消或离开后只可显式查询原 operation ID，不自动再次
Commit、新建 ID 或重新预览绕过屏障。非终态合法回执仍保留屏障；只有相同 origin/owner/ID
的终态回执才解除。不明操作只保留安全身份和 ID，旧票据/草稿被清理。
共享内核明确报告发送前本地拒绝、无 phase 且不是 ErrSelectionUnknown 时，可撤销本地预留；
这不是拿 NotObserved 当作从未发送或未来不会完成的证明。
NotObserved 不证明仍在途的请求未来不会完成。可粘贴保存的非秘密 ID 做只读查询，不读源文件。

屏障随当前 Model 的进程内生命周期保留，换页、退出登录、重新登录、换 origin 后返回不会
解除同一身份的屏障；其它 owner 不因此全局停用。退出前保存 ID。进程重启不提供持久屏障或
“未知已回滚”证明，用户仍须明确查询原操作；不能把新 client 当成服务器幂等性已解决。
原有「取消任务」会清除本地会话，查询时需重新登录。取消返回与 worker 实际排空分开，
排空前不准新的源任务，不在持锁或自身 callback 内同步等待 Close。

## 证据边界

作者冻结附真实 TLS/Auth/SQLite/完整 App.Serve、服务端 moejs、三来源独立/混合选择、
未知与取消屏障、过期票据、stale metadata、无源凭据迁移及 UI 控件测试；没有新生产依赖。
普通未修改 binary 的合成账号登录、Page2 前门及授权预览与 ui.NewTester 的完整提交证据分开，
不用 direct-entry overlay 代替普通入口。精确命令、历史失败、ELF hash、六目标构建及截图以
冻结 REPORT/manifest 为准。EN locale 测试是布局/键盘环境覆盖，不宣称现有中文文案已翻译。

Linux 软件渲染窗口不等于真人/GPU或 macOS/Windows 运行资格。三来源接线不开放其它 provider、
本地内容上传、通用目录扫描、npx lock 写回、客户端 moejs 或插件；完整 F6/F7/F12、Pack 自动应用、
平台与发布门禁保持原要求。本实现已完成非作者限定审查及主线组合验证；来源支持范围、
普通窗口完整提交矩阵与跨平台运行仍按各自证据验收，不外推完整 F6/F7/F12 或 GA。
