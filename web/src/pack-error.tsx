import { t } from "./i18n"
import { useTranslation } from "react-i18next"
import { Code, ConnectError } from "@connectrpc/connect"
import { errorMessage } from "./api"
import { Button } from "./components/ui/button"

export function PackError({ error, retry }: { error: unknown; retry?: () => void }) {
  useTranslation()
  const code = ConnectError.from(error).code
  const message = code === Code.Aborted ? t("组合已在其他会话中更改。当前输入已保留；请取消编辑，重新读取后再修改。") : code === Code.InvalidArgument ? t("请检查名称、描述和安装别名。同名但内容不同的技能必须设置不同别名；别名不支持路径或系统保留名。") : code === Code.FailedPrecondition ? t("选中的技能已不可用，请重新读取仓库并选择。") : code === Code.DataLoss ? t("保存的技能内容缺失或损坏，暂时不能分发。") : errorMessage(error)
  return <div role="alert" className="error-notice flex-wrap"><span>{message}</span>{retry && <Button variant="outline" onClick={retry}>{t("重试")}</Button>}</div>
}
