import i18n from "./i18n"

const english: Record<string, string> = {
  "组合工作台": "PACK WORKSPACE",
  "仅分发技能。配置附件自动应用尚未提供；此页不表示设备已同步。": "Skills only. Automatic application of configuration attachments is not available; this page does not indicate device sync status.",
  "组合列表状态未知；保留上次读取内容。": "Pack list status is unknown. Showing the last read content.",
  "组合内容状态未知；保留上次读取内容。": "Pack content status is unknown. Keeping the last read content and your draft.",
  "当前服务端修订": "SERVER REVISION",
  "更新于 {{time}}": "Updated {{time}}",
  "版本": "Revision",
  "危险区域": "Danger zone",
  "编辑基于版本 {{revision}}；刷新不会覆盖草稿。": "Editing revision {{revision}}. Refreshing will not replace your draft.",
  "本次保存未确认或版本已冲突。输入已保留；请取消并刷新核对，不会自动重放。": "Save is unconfirmed or the revision conflicts. Your input is preserved. Cancel and refresh to reconcile; the write will not be replayed automatically.",
  "删除未确认。请关闭确认框并刷新列表核对；不会自动重放。": "Deletion is unconfirmed. Close this dialog and refresh the list to reconcile; it will not be replayed automatically.",
  "分享范围：仅技能，不含配置内容或密钥。撤销只阻止后续读取，不能收回已分发或安装的内容。": "Scope: skills only, without configuration content or secrets. Revocation blocks future reads; it cannot retrieve content already distributed or installed.",
  "重新读取分享状态": "Reload share status",
  "创建结果未确认，不会自动重放。请重新读取分享状态；若已创建但未收到令牌，可撤销该记录。": "Creation is unconfirmed and will not be replayed automatically. Reload share status; if a share was created without a token receipt, you can revoke that record.",
  "分享状态未知，不能据此判断已过期或撤销。": "Share status is unknown. This does not mean shares have expired or been revoked.",
  "撤销结果未确认。请重新读取分享状态核对，不会自动重放。": "Revocation is unconfirmed. Reload share status to reconcile; it will not be replayed automatically.",
  "撤销只阻止后续读取，已经发送或安装的内容无法收回。": "Revocation blocks future reads. Content already sent or installed cannot be retrieved.",
  "正在撤销…": "Revoking…",
  "重新读取组合状态": "Reload pack status",
}

i18n.addResourceBundle("en", "translation", english, true, true)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, true)
