# MyGo 配置资产分区排序

在原生客户端登录后进入“资产排序”，选择“重新读取 / 核实排序”。页面从所有者完整资产元数据发现 agent / 配置类型分区，再读取所选分区的完整顺序与独立排序 revision；不分页截断 250 项分区。配置类型包括 provider、MCP、prompt、agent-settings 和 workspace，资产 schema 未被本机编辑器支持也可排序。

在原生列表中选择资产，用“上移所选资产”“下移所选资产”调整。列表支持键盘方向键、Home/End、Tab 与按钮确认，所选资产随移动保留；未保存时可放弃本地排序。已有未保存内容草稿的离页确认仍然有效，排序成功、冲突或 Unknown 不清除 Claude/Grok 表单 Dirty。

“审阅完整排序”显示 origin、owner、agent/类型、独立排序 revision、全部资产 ID 和前后位置。输入本次精确确认后只提交一次完整全排列 CAS；排序不批准内容 revision，也不保存正文、切换 provider、改变 Pack 引用或应用到设备。审阅不会读取正文、旧秘密或环境变量值，不调用客户端插件或 Cobra。

冲突或 Unknown 后本次审阅已消耗，禁止自动重放、自动读取后重提或沿用旧确认。必须显式重新读取，核实实际顺序，再移动并生成新审阅。重读会替换未确认的本地排列；此时不能推断先前操作已回滚。正在审阅时分区选择和移动锁定，取消审阅或 Escape 不提交。

当前分区最后一个资产被其它操作删除后，显式重读仍保留该空分区及服务端 revision，防止空分区 ABA。没有资产的账号不产生写操作。新建/复制/导入/删除改变成员关系时，旧排序 revision 会被 CAS 拒绝；内容编辑使用独立内容 revision。

会话沿用既有 configurationIdentity 与 Service。提交前重新核对当前 owner；请求由现有跟踪 worker 执行，经 Window.Update/dispatch 与 generation 发布。取消、切 origin/owner、登出、关闭取消在途请求；关闭时由现有 Wait 排空，旧 callback 不回填。账号会话不写磁盘、不进入审阅摘要。

该片限定服务端资产排序；不是其它 agent 的配置编辑或本机应用，也不是完整 F12/GA。ui.NewTester 是 headless native 组件验证，私有 cage/Wayland smoke 如有证据也只代表相应 Linux 无人值守环境，不代表真人或 macOS/Windows 验收。
