import { clone } from "@bufbuild/protobuf"
import { Code, ConnectError } from "@connectrpc/connect"
import { GitHubProxySettingsSchema, type GitHubProxySettings } from "./gen/skillhub/management/v1/github_proxy_pb"

function proxyAddress(value: string) {
  if (value !== value.trim() || /[\s\\\p{Cc}]/u.test(value)) return undefined
  try {
    const address = new URL(value)
    return ["http:", "https:", "socks5:", "socks5h:"].includes(address.protocol) && address.hostname && (!address.port || /^\d+$/.test(address.port) && Number(address.port) > 0 && Number(address.port) <= 65535) && !address.search && !address.hash && ["", "/"].includes(address.pathname) && !value.includes("?") && !value.includes("#") ? address : undefined
  } catch { return undefined }
}

export function validGitHubProxyInput(value: string) {
  return value === "" || new TextEncoder().encode(value).length <= 4096 && !!proxyAddress(value)
}

export function acceptGitHubProxySettings(value?: GitHubProxySettings, previousRevision?: bigint) {
  const address = value?.configured ? proxyAddress(value.endpoint) : undefined
  if (!value || value.$unknown?.length || value.revision <= 0n || previousRevision !== undefined && value.revision <= previousRevision || (value.configured ? !address || address.username || address.password || value.endpoint.includes("@") : value.endpoint !== "")) throw new ConnectError("Invalid GitHub proxy settings response", Code.DataLoss)
  return clone(GitHubProxySettingsSchema, value)
}
