import i18n from "./i18n"

const english = {
  "GitHub 仓库": "GitHub repository",
  "已选": "Selected",
  "服务端未能确认结果。收录可能已完成，请重新读取状态；不会自动重试或覆盖快照。": "The server could not confirm the result. Collection may have completed. Read the current state; no automatic retry or snapshot replacement occurs.",
  "无法获取仓库归档，请检查仓库、分支及 GitHub 授权。私有来源需要服务端凭据具有对应读取权限。": "The repository archive is unavailable. Check the repository, branch and GitHub authorization. Private sources require server credentials with the appropriate read access.",
  "私有来源需要服务端 GitHub 授权。收录默认不公开；只有另行确认发布，才允许匿名下载。": "Private sources require GitHub authorization on the server. Collection is private by default; anonymous downloads require a separate, explicit publication confirmation.",
  "快照状态未知；下面保留上次读取的内容与筛选，不代表当前校验成功。": "Snapshot status is unknown. The last read contents and filters remain below; this does not confirm current integrity.",
  "上次读取的快照": "Last read snapshot",
  "归档 SHA-256": "Archive SHA-256",
  "添加第一个技能仓库，或从下方 Lists 候选中明确选择并收录。": "Add your first skill repository, or explicitly select and collect a candidate from GitHub Lists below.",
  "没有匹配的仓库。当前详情仍保留所选快照。": "No matching repositories. The selected snapshot remains open in the detail panel.",
  "本次提交不会自动重试。请关闭对话框并重新读取，核对结果后再开始新的操作。": "This submission will not retry automatically. Close this dialog and read the current state before starting another operation.",
  "分类状态未知；暂存列表不代表最新计数。未提交的编辑仍保留。": "Category status is unknown. The cached list does not confirm current counts. Unsaved edits are retained.",
  "上次读取的分类；计数未知": "Last read category; current count unknown",
}

i18n.addResourceBundle("en", "translation", english, true, false)
i18n.addResourceBundle("zh-CN", "translation", Object.fromEntries(Object.keys(english).map((key) => [key, key])), true, false)
