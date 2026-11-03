import i18n from "./i18n"

const english = {
  "已关联的 OIDC 身份": "Linked OIDC identities",
  "无法读取关联身份，请重试。": "Could not load linked identities. Try again.",
  "取消 OIDC 关联": "Unlink OIDC identity",
  "输入当前账号密码以取消关联": "Enter your current account password to unlink",
  "取消关联后保留当前会话，退出其他会话。之后仍可使用原用户名和密码登录。": "Unlinking keeps this session and signs out other sessions. Your original username and password will still work.",
  "此账号没有本地密码，不能移除唯一登录方式。": "This account has no local password. Its sign-in method cannot be removed.",
  "尚未关联 OIDC 身份。": "No OIDC identity is linked.",
  "取消关联失败，请检查当前账号密码并重试。": "Could not unlink. Check your current account password and try again.",
  "OIDC 关联已取消。": "OIDC identity unlinked.",
  "工作空间访问": "Workspace access",
  "账号已创建，正在完成登录。": "Account created. Completing sign-in…",
  "其他登录方式": "Other sign-in methods",
  "无法读取登录方式，请重试。本地账号仍可登录。": "Could not read sign-in methods. Try again. Local accounts can still sign in.",
  "重试读取登录方式": "Retry sign-in methods",
  "刷新登录方式": "Refresh sign-in methods",
  "当前会话身份": "Current session identity",
  "账号显示名称": "Account display name",
  "用户标识": "User ID",
  "仅显示当前会话信息，不代表已绑定的身份清单。": "This is the current session, not a list of linked identities.",
  "身份绑定": "Identity linking",
  "状态未知": "Status unknown",
  "正在检查": "Checking",
  "可用": "Available",
  "未启用": "Not enabled",
  "绑定需要你前往身份提供方明确授权；此页不会显示密码或身份令牌。": "Linking requires your explicit authorization with the identity provider. Passwords and identity tokens are never displayed here.",
}

i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
