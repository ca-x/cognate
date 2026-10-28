import { t } from "./i18n"
import { useTranslation } from "react-i18next"
import { Code, ConnectError } from "@connectrpc/connect"
import { CircleAlert } from "lucide-react"
import { Button } from "./components/ui/button"

export function catalogErrorMessage(error: unknown) {
  switch (ConnectError.from(error).code) {
    case Code.Unimplemented: return t("此服务尚未提供目录或分类接口。请待服务端接通后重新读取。")
    case Code.NotFound: return t("内容不存在、已取消公开，或你没有访问权限。请重新读取最新状态。")
    case Code.Unauthenticated: return t("服务端拒绝了访问。管理操作需要重新登录；公开目录不应要求登录。")
    case Code.PermissionDenied: return t("服务端拒绝了此操作，请检查当前账号是否拥有这些内容。")
    case Code.InvalidArgument: return t("请求格式无效，请检查名称、搜索词或分类后重试。")
    case Code.AlreadyExists: return t("分类名称已存在，请使用其他名称。")
    case Code.Aborted: return t("内容已在其他会话中更改。请关闭对话框并重新读取，再核对并提交。")
    case Code.FailedPrecondition: return t("内容当前不可用，或发布确认未满足要求。请重新读取后检查。")
    case Code.ResourceExhausted: return t("已达到请求或容量限制，请稍后重试。")
    case Code.DataLoss: return t("技能内容缺失或校验失败，暂时不能使用。请联系此服务的维护者。")
    case Code.DeadlineExceeded: return t("等待服务端响应超时。写入可能已经完成，请重新读取状态后再操作。")
    case Code.Unavailable: return t("无法连接目录服务。写入结果尚未确认，请检查连接并重新读取状态。")
    case Code.Canceled: return t("请求已取消；如已提交更改，请重新读取状态。")
    default: return t("服务端未能确认此操作。请重新读取状态，不要将此视为保存或发布成功。")
  }
}

export function CatalogError({ error, retry }: { error: unknown; retry?: () => void }) {
  useTranslation()
  return <div className="error-notice flex-wrap" role="alert">
    <CircleAlert size={18} className="shrink-0" aria-hidden="true" />
    <p className="min-w-0 flex-1 basis-40 break-words">{catalogErrorMessage(error)}</p>
    {retry && <Button variant="outline" onClick={retry}>{t("重新读取")}</Button>}
  </div>
}
