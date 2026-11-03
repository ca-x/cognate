import i18n from "./i18n"

const english = {
  "无法预览此导出，请检查 Magpie 导出文件和密码。": "Could not preview this export. Check the Magpie file and password.",
  "导出超过大小或条目限制，或预览请求较多，请检查后稍后重试。": "Export size or item limit exceeded, or preview is busy. Check the export and try again later.",
  "Magpie 导出密码": "Magpie export password",
  "Magpie 界面加密导出 · 64 MiB（兼容 library.json · 1 MiB）": "Magpie encrypted app export · 64 MiB (library.json · 1 MiB also supported)",
  "cc-switch schema23/24 SQLite · 64 MiB": "cc-switch schema23/24 SQLite · 64 MiB",
  "请选择非空 Magpie 导出，最大 64 MiB。": "Select a non-empty Magpie export up to 64 MiB.",
  "请选择非空、自包含的 SQLite 快照，最大 64 MiB。": "Select a non-empty, self-contained SQLite snapshot up to 64 MiB.",
  "密码仅在浏览器内解密，不发送到服务端。仅导入技能来源；备份中的文件内容与凭据不会迁移。明文 library.json 可留空。": "Decrypted in your browser; the password is never sent to the server. Only skill sources are imported, not bundled files or credentials. Leave blank for plain library.json.",
}
i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
