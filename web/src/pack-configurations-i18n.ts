import i18n from "./i18n"

const english: Record<string, string> = {
  "所有者元数据": "OWNER METADATA",
  "配置附件": "Configuration attachments",
  "仅维护私有引用，不读取配置正文或密钥，不授予分享访问，也不自动应用到设备。": "Private references only. No configuration bodies or secrets are read, no share access is granted, and nothing is automatically applied to devices.",
  "组合版本": "Pack revision", "配置组版本": "Configuration revision", "资产版本": "Asset revision", "格式版本": "Schema",
  "正在读取附件与资产元数据…": "Reading attachment and asset metadata…",
  "读取失败或组合版本未核实。保留上次元数据和草稿，禁止写入。": "Read failed or Pack revision is unverified. Keeping the last metadata and draft; writes are disabled.",
  "读取附件元数据": "Reload attachment metadata", "只读核对当前状态": "Reconcile current state (read only)",
  "编辑配置附件": "Edit configuration attachments", "放弃旧审阅，重新编辑": "Discard old review and edit anew",
  "没有配置附件。空列表只清空附件，不删除资产或技能。": "No configuration attachments. An empty list clears attachments only; it does not delete assets or skills.",
  "资产元数据缺失，引用保留": "Asset metadata missing; reference retained", "不支持的元数据": "Unsupported metadata", "旧资产版本，不自动追随": "Stale asset revision; not followed automatically", "当前元数据": "Current metadata",
  "待提交的有序引用": "Proposed ordered references",
  "从空选择开始添加。旧引用保留原版本；更新、移除和清空都需要明确审阅。": "No asset is preselected. Existing references keep their pinned revisions; updating, removing and clearing require explicit review.",
  "可附加的所有者资产": "Eligible owner assets", "明确选择一个资产": "Select an asset explicitly", "添加引用": "Add reference",
  "只列出 schema 1 provider 的十四类元数据目标；服务端校验为准，不代表原生支持。": "Only schema 1 provider metadata for fourteen targets is listed. Server validation is authoritative; this is not native support.",
  "上移": "Move up", "下移": "Move down", "上移第 {{number}} 项": "Move item {{number}} up", "下移第 {{number}} 项": "Move item {{number}} down", "移除第 {{number}} 项": "Remove item {{number}}", "移除引用": "Remove reference", "使用当前资产版本": "Use current asset revision",
  "清空待提交引用": "Clear proposed references", "审阅替换": "Review replacement", "放弃草稿": "Discard draft",
  "确认整组替换附件元数据": "Review complete attachment replacement",
  "审阅不是服务端预验证。确认后重验所有者与完整快照，只发送一次双 CAS 替换。": "Review is not server prevalidation. Confirmation rechecks the owner and exact snapshot before one dual-CAS replacement.",
  "服务来源": "Server origin", "当前所有者": "Current owner", "组合标识": "Pack ID", "组合版本 / 配置组版本": "Pack / configuration revision",
  "旧的有序引用": "Previous ordered references", "新的有序引用": "Proposed ordered references",
  "我已审阅顺序、版本和清空后果，只替换元数据。": "I reviewed order, revisions and the consequences of clearing; replace metadata only.",
  "取消审阅": "Cancel review", "停止等待并关闭": "Stop waiting and close", "确认替换一次": "Confirm one replacement", "正在核验或提交…": "Checking or submitting…",
  "已取消审阅；未发送替换。": "Review cancelled; no replacement was sent.", "已放弃草稿；未发送替换。": "Draft discarded; no replacement was sent.",
  "附件元数据已替换。未应用设备配置，技能与分享保持不变。": "Attachment metadata replaced. No device configuration was applied; skills and shares are unchanged.",
  "审阅已失效或读取失败；未发送替换。请只读刷新后重新编辑。": "Review expired or a read failed; no replacement was sent. Reload read-only, then edit anew.",
  "审阅基线已变化。草稿保留；请重新编辑，不重用旧确认。": "Review baseline changed. Draft retained; edit anew without reusing the old confirmation.",
  "附件或资产版本冲突。请只读刷新，再重新编辑和审阅。": "Attachment or asset revision conflict. Reload read-only, then edit and review anew.",
  "当前所有者无权访问此组合或资产，或资源已删除。": "The current owner cannot access this Pack or asset, or it was deleted.",
  "服务端拒绝了引用、目标或数量。请只读刷新并重新审阅。": "The server refused the references, targets or count. Reload read-only and review anew.",
  "提交结果不明（Unknown）。不会重放；匹配的当前状态也不能证明哪次提交成功。": "Submission is Unknown. It will not be replayed; matching current state cannot prove which attempt committed.",
  "只读核对完成；旧草稿仅供检查。后续替换必须重新编辑和审阅。": "Read-only reconciliation completed. The old draft is for inspection only; a later replacement needs a new edit and review.",
  "已重新读取元数据。不会覆盖草稿或提交替换。": "Metadata reloaded. The draft is not overwritten and no replacement is submitted.",
}

i18n.addResourceBundle("en", "translation", english, true, true)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map(key => [key, key])), true, true)
