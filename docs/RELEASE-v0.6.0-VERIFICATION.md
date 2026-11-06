# 0.6.0 组件验证与发布记录

## 范围与源码

2026-11-06 用户恢复剩余开发并授权发布新版。本轮交付受控 cc-switch
schema23/24 OpenCode v1 provider 选择导入，不代表完整 F6/F12 或全部 backlog 完成。
行为及客户端兼容边界见 `OPENCODE-IMPORT.md` 与 `RELEASE-v0.6.0.md`。

功能提交 `bece83eb7ae7ed398a294d9208165e04071c83ca`，随后仅将 MyGo 包版本
同步到 0.1.4。三个不可变组件 tag 均固定最终源码
`ed00481e84fb29fce6d5a50a2140875939911823`：Server 0.6.0、CLI 0.3.0、GUI 0.1.4。
均通过 SSH 推送并从远端读取相同 SHA；未移动任何旧 tag。

## 开发验证

- Go 1.27.3、CGO0 全仓测试、vet、Web TypeScript/Vite build 与导入状态回归通过。
  最终版本源码再次通过 importer/configuration/httpapi/importclient/cli/desktop 六包回归。
- 真实 Connect/TLS/SQLite 覆盖 schema23/24、普通提交及丢响应只读核对，安全字段精确
  读回且没有来源凭据。使用合成数据，未将真实用户数据库或导出提交到仓库。
- 浏览器八组 375/1440、中英、深浅主题通过，检查可见审阅字段、原生 details、Escape、
  再次确认与一次提交。切换语言仍清理预览并要求重新授权，不为测试放宽安全边界。
- 用户代表只读审查发现 MyGo 最终审阅遗漏 OpenCode 详情；新增真实回归先复现失败，
  修复共用摘要后由代表独立针对性复验通过。原生渲染和真实控件确认不代替各平台真人安装。
- 新增来源解析文件进入第十三个受绑定宿主源码集合；固定摘要经独立长度前缀重算，
  模块摘要、封闭源码集合与编译字节检查保留。
- 源码 Build `42163691961` 与最终源码 Build `42164271683` 均 completed/success；
  包含共享 Go/Web/Buf、六平台交叉编译与两个 Docker 架构。

本机日志、截图、回执和完整下载位于 `/tmp/cognate-opencode-import`，不是远端备份。

## Server

Release run `42165466938` completed/success。共享测试、双架构容器、原 OCI 产物
提升和最终按 child digest 拉取/执行验证通过，未更新 `latest`。

镜像：`ghcr.io/ca-x/cognate:server-v0.6.0`。

- Index：`sha256:c1868f18e28c099c440c474dafa6df6f8fc9257923479675cdbcb83e8f125b19`
- Linux amd64：`sha256:a6c7ae998cad1c139f9fb35cbc67c8ed3da9f804e99693ba9dc9197bfa2ba284`
- Linux arm64：`sha256:16c17b331113d8d78e0c556a50894437d548e12d4db666ec690e95eaffa9c62f`

本机独立匿名读取以上三个 manifest，逐字 SHA256 与响应 digest 相同，index 恰含两个
Linux 架构。服务端按既有约定没有额外创建 GitHub Server Release。

## CLI 与原生 GUI

CLI run `42165466314`、GUI run `42165467497` completed/success。CLI 六份归档与
GUI 八份安装包全部完整下载，分别与 API digest 和随包 `SHA256SUMS` 一致。

CLI 六份归档均独立检查准确许可字节、唯一成员、Go 1.27.3、CGO0、平台架构、最终
source revision 与 clean VCS metadata；下载的 Linux amd64 CLI 在隔离 HOME 执行
`--help` 退出 0。GUI Linux tar/DEB 均独立核对许可和 source/CGO0/架构 metadata；
各 runner 另外检查实际 DMG/NSIS/tar/DEB 许可、原生验证及 package-check，未跳过打包。

完成校验并替换准确功能/限制说明后，公开两个非 draft、非 prerelease 的组件 Release：

- CLI：<https://github.com/ca-x/cognate/releases/tag/cli-v0.3.0>
- MyGo：<https://github.com/ca-x/cognate/releases/tag/gui-v0.1.4>

macOS 仍仅 ad-hoc 且未公证，Windows 安装包未签名；这些是组件分发，不扩大平台运行
资格。旧客户端无法解析 READY OpenCode 候选，可能拒绝整个预览，需配套升级；本版
没有能力协商或自动降级，也没有 OpenCode 本机应用。

独立 Platform `42163691960`、`42165466802`、`42165467427` success；但 CLI tag 的
Platform `42165466313` **failure**：三平台基础原语通过，Darwin baseline 的 core/caller、
cleanup、seal 步骤失败。保留该失败，不用同源码其它成功 run 覆盖，不宣称历史问题闭合。
它不属于 Linux Server/Exec 发布门禁，也不据此开放 macOS 安装或配置应用。

## LazyCat Exec

包装不可变 `v0.6.0` 固定 `6c35f383c363f84ac8a042aa61609c66227747cb`，准确 pin 上述
source tag/commit。run `42167459006` completed/success，保留 runner 的 root 首次安装、
升级及主密钥安全启动 suite；原 manifest、wrapper、设置向导、权限边界不变。

公开正式 Release `436126984`：
<https://github.com/lazycat-contrib/cognate-lzcapp/releases/tag/v0.6.0>。

最终资产 `community.lazycat.app.cognate-v0.6.0.lpk`：**78,698,496 字节**，SHA256
`c3c05c6a11b1370e7809f7ed711a0563d20a6685084f3195e0c10167f2c7f0b9`。
完整下载的大小和整体 hash 与 API digest、喵喵回执完全一致。独立检查外层五项、content
七项白名单、属主、部署参数、manifest、图标、wrapper、全部许可与 SOURCE_COMMIT；
最终 ELF SHA256 `f58eaca29b8c0636a84f840ef9ba2532510940a189e2e971f991e1e1c51cb671`，
Go 1.27.3、Linux amd64、CGO0、准确 revision 与 clean metadata 均通过。
对最终下载解包的 binary/wrapper 再次执行完整隔离 root 启动 suite，首次管理员、升级
保留原账号/密码/密钥、缺失或不安全密钥拒绝、卷链接、只读卷和祖先权限边界均通过。
日志为 `final-lpk-startup.log`；本地 suite 不代替懒猫真机升级。

喵喵接受 app `670` / version `4253`，published=true、skipped=false；回执准确引用
上述最终 URL 和 hash，但 `onlineVersion=0.5.0`，不能声称店面已切换到 0.6.0。
官方商店保持关闭；用户盒子升级、平台 OIDC 与店面切换仍待真实环境验证。

## 未完成

OpenCode v2/高级字段、完整 agent/provider/MCP/prompt 导入、MyGo 本机配置应用、Pack
配置自动消费、备份恢复、完整 UI/平台验收等仍在 `TODO.md`，未因本次组件发布勾选。
