import i18n from "./i18n"

const en = {
  destination: "S3 destination and recovery key",
  title: "Backup settings", intro: "Manage this instance's backup destination and schedule here. Save to apply without restarting the server.",
  enable: "Enable server backups", enableHelp: "Allows encrypted backups to the S3 destination below. Disabling backups does not delete existing remote copies.",
  automatic: "Back up automatically", interval: "Backup interval (seconds)", intervalHelp: "60–86,400 seconds. Each interval starts after the previous task finishes. Disabled server backups never run automatically.",
  endpoint: "S3 endpoint (HTTPS)", region: "Region", bucket: "Bucket", prefix: "Object prefix (optional)", recoveryKeyId: "Recovery key ID (UUID)", recoveryKeyFile: "Recovery key file on the server",
  keyHelp: "An existing private file containing exactly 32 raw bytes, independent of the server master key. Enter its absolute server path, not a browser or computer path. Cognate validates it but never overwrites it. Keep a separate offline copy; full restore is not available yet.",
  credentials: "S3 credentials", keep: "Keep existing credentials", replace: "Replace credentials", enter: "Enter credentials", sessionToken: "Session token (optional)",
  credentialsHelp: "Saved credentials are encrypted and never returned to this page. Replace submits a complete new set; an empty session token removes the old one. Inputs clear on submission.",
  applyHelp: "Changes apply after you save. While a backup is running or its result needs reconciliation, finish or reconcile it before changing settings. No service restart or automatic retry is performed.",
  save: "Save and apply", saving: "Applying…", reload: "Reload saved settings", loading: "Reading settings…", saved: "Last saved settings applied without a restart. Unsaved edits are not active.",
  unknown: "The server did not confirm the change. It may have applied. Reload saved settings before trying again; no automatic retry was made.",
  conflict: "Another administrator changed these settings. Your draft is unchanged. Reload saved settings before saving again.",
  busy: "Settings cannot change while a backup is running or requires reconciliation. Check task history and the recovery key, then try again.",
  denied: "Your account no longer has permission to change backup settings.", invalid: "Settings were not accepted. Check the S3 destination, credentials, interval and private recovery-key file. Enter replacement credentials again before saving.",
  unsupported: "This server does not support online backup settings. Upgrade the server to configure them here.", loadError: "Saved settings could not be read. No change has been made. Reload before editing.",
}
const zh: Record<keyof typeof en, string> = {
  destination: "S3 备份目标与恢复密钥",
  title: "备份设置", intro: "在这里管理本实例的备份目标与周期。保存后在线生效，无需重启服务端。",
  enable: "启用服务端备份", enableHelp: "允许将加密备份写入下方 S3 目标。关闭不会删除已有的远端备份。",
  automatic: "自动备份", interval: "备份间隔（秒）", intervalHelp: "范围 60～86,400 秒，从上一个任务结束后开始计时。关闭服务端备份后不会自动执行。",
  endpoint: "S3 服务地址（HTTPS）", region: "区域", bucket: "存储桶", prefix: "对象前缀（可选）", recoveryKeyId: "恢复密钥标识（UUID）", recoveryKeyFile: "服务端恢复密钥文件",
  keyHelp: "填写服务端已有的私有文件绝对路径，不是浏览器所在电脑的路径。文件必须是独立于主密钥的 32 字节原始密钥；程序会校验，不会覆盖。请另存离线副本，完整恢复功能尚未交付。",
  credentials: "S3 访问凭据", keep: "保留已保存的凭据", replace: "替换凭据", enter: "填写凭据", sessionToken: "会话令牌（可选）",
  credentialsHelp: "凭据加密保存，不回显已有值。替换时提交完整的新凭据；会话令牌留空会清除旧令牌。输入框在提交时清空。",
  applyHelp: "修改后点击保存即可在线生效。已有备份运行中或结果待核对时，请先等待结束或核对任务，再修改设置。不会重启服务，也不会自动重试保存。",
  save: "保存并生效", saving: "正在应用…", reload: "重新读取已保存设置", loading: "正在读取设置…", saved: "上次保存的设置已生效，无需重启。尚未保存的修改不会应用。",
  unknown: "未收到服务端确认，设置可能已生效。请先重新读取已保存设置，再决定是否重试；程序没有自动重试。",
  conflict: "其他管理员已修改设置，当前草稿保留。请重新读取已保存设置后再保存。",
  busy: "当前有备份正在运行或需要核对，暂不能修改设置。请检查任务历史与恢复密钥后重试。",
  denied: "当前账号已无权修改服务端备份设置。", invalid: "设置未被接受。请检查 S3 目标、凭据、周期及私有恢复密钥文件；替换凭据需要重新填写。",
  unsupported: "此服务端不支持在线备份设置，请升级服务端后在这里配置。", loadError: "无法读取已保存设置，尚未进行更改。请重新读取后再编辑。",
}
i18n.addResourceBundle("en", "backupSettings", en, true, false)
i18n.addResourceBundle("zh-CN", "backupSettings", zh, true, false)
