# Cognate 0.2.0 组件分发核验

更新：2026-11-01。服务端/CLI `0.2.0`、GUI `0.1.3` 的源码标签均固定
`71afc524d35efeb3253160dce41a26d12a3c9a4b`，不移动此前标签。
本记录不宣称完整产品、完整恢复或跨平台运行验收完成。

## 上游构建与产物

| 范围 | 实际证据 | 当前状态 |
| --- | --- | --- |
| Main Build | `41293207375`，共享 Go/Web/Buf、交叉构建及 Docker 检查 | success |
| 服务端发布 | `41293226218`，双架构 OCI 推送及独立 published-image verifier | success，镜像已发布 |
| CLI 发布构建 | `41293225774`，六归档及实际最终归档许可门 | success，正式公开 Release `430482348` |
| GUI 发布构建 | `41293225980`，verify、三 OS package、package-check、publish | success，正式公开 Release `430483241` |

服务端 index digest 为
`sha256:16802797eae73d3f16a1a9801d8cfabcdfca65926eb99a712567367193168eb8`。
主线程另经匿名 GHCR pull grant 下载实际 index 和两个 child manifest，重新计算的 SHA256
与 registry header、工作流预期一致，精确包含 Linux amd64/arm64；没有更新 `latest`。

六份 CLI 归档及八份 GUI 安装产物均已通过官方 GitHub asset API 完整下载，逐份核对
API digest、实际字节数和发布 `SHA256SUMS`，不以远端 hash 或部分字节代替下载验收。
CLI 六归档核对四个准确 regular 成员、Codex 与 Gemini 两份原文许可、Go1.27.3、
CGO0、源提交/clean VCS 和目标身份；下载的 Linux amd64 CLI 在隔离 HOME 下执行 help。
GUI 下载包核对全部八个外层 checksum，另从 Linux tar/deb 回读三份准确许可文本，
两个 tar 的 binary 元数据也已核对。macOS/Windows 不以 Linux 主机推断运行资格。

已取得 GUI 三项实际 package job 日志并核对六条架构许可收据：从真实 Linux tar/deb、
挂载 DMG 或解出的 NSIS 检查 Codex 与 Gemini 原文，不只验证资源暂存目录。
这些是安装包载荷证据，不是实际用户安装、Developer ID、公证或完整原生体验证明。

本地 verifier 的两个有效负例分别移除 CLI/GUI 的 Gemini notice，并重新计算有效外层
checksum，仍被内部载荷检查拒绝；错误源码 revision 也被 binary 元数据检查拒绝。
原下载内容没有改动。证据根为 `/tmp/cognate-v020-delivery-L7sii128`，含完整产物、
API metadata、下载日志、源码身份与许可结果及三个负例日志。

CLI/GUI 在全部完整下载检查通过后才公开，均回读为 draft=false、prerelease=false。
又以无认证 GitHub API 核对 7/9 个资产的 ID、名字、size、digest 与预公开版本完全一致；
GUI `0.1.3` 为仓库 latest，CLI 独立分发不修改该 latest 指向。

## 未通过的 Darwin 观察，不能由其它绿色运行覆盖

CLI-tag 独立 Platform run `41293225710` 在 Darwin baseline observation 的 fixture
cleanup 失败，随后有限证据 sealer 拒绝 caller/baseline 证据；原始 job 与失败日志保留，
没有重跑。其 artifact `13078775790` 实际 ZIP SHA256 为
`e8030c5bf49153cad58459abcfd98d85befc731d0179e7d20a9e730337a71a96`，CRC 检查通过。
认证收据显示 `cleanup.failed:true`：insensitive detach_exit=0，sensitive detach_exit=16；
两者均 still_mounted=false。后者不能抵消非零 detach、不能证明清理完整或解释根因。

同源 server/gui 的 Platform runs `41293226157`、`41293226057` 成功，但不用于关闭该失败。
非作者独立核对原 ZIP 后确认：不自动阻断已声明 Linux 优先范围的组件分发，但不能把
本次 Darwin baseline/caller 记为通过，也不新增或确认 Darwin 运行资格；若发现生产安全
影响须重新评估。发布说明显式列出此限制，故障继续保留在 TODO。

## 懒猫 Exec 0.2.0

包装仓库 `lazycat-contrib/cognate-lzcapp` 的 tag `v0.2.0` 固定
`0cf739c32e972f04678e5785336682634b49f989`，准确引用上述服务端 tag/完整源提交。
官方商店关闭，仅喵喵商店。Workflow `41294390302` completed/success，startup-smoke
与 lazycat 发布成功；mark-preview 按正式版本请求跳过。本地与最终资产分别核验如下。

本地固定源码实际构建、manifest lint、actionlint、shell syntax 通过；真实本地 LPK
78,484,480 字节，SHA256 `28a591d52f63fccc5acf61e3cdc0b8dc099b5611ca91bec17ce20baf491eb7db`。
解包检查五个外层文件、七个 content 文件、权限、源 identity、模板和准确许可文本；
binary 为 Linux amd64/Go1.27.3/CGO0/clean VCS。对实际解包的 binary 与 wrapper 运行
完整 namespace 启动 suite，fresh、upgrade、原管理员/主密钥保留、不安全或丢失密钥拒绝、
卷链接、只读卷和不安全祖先场景通过。以 unprivileged user namespace 内的 UID0 执行，
不冒称宿主 sudo 或用户盒子的实际升级/OIDC 已通过。

初次本地 package 因私有副本未创建输出目录而失败；创建受控 `.lazycat-build` 后通过，
原日志保留，没有改生产 wrapper 或放宽数据安全门。

最终公开 Release `430487306` 为 non-draft、non-prerelease；资产
`community.lazycat.app.cognate-v0.2.0.lpk`（ID `697917315`）78,483,968 字节，SHA256 为
`2a2d354837c2bb1f441b5a9d21923c68f1de4091fff3a64e03782296c3911ec4`。
该 CI 资产已完整下载并重新计算整体 hash，解包核对外层 metadata、七个 content 成员、
原 wrapper、模板、许可、固定源提交与 CGO0/平台身份；实际最终 binary 逐字等于本地
固定源码构建，SHA256 `f7458fdc9539d65536a2687e1efbbba95f54ebc5684237e45bb842e56de65902`。
最终下载解包内容又通过完整 namespace 启动 suite，非仅本地 staging 的重复断言。

喵喵回执为 app `670`、version `3851`、published=true、skipped=false，准确版本 `0.2.0`、
versioned GitHub Asset URL 与上述最终 SHA256 一致；官方发布 outcome=skipped。
最终实包与本地包的外层 size/hash 不同，分别保留，不拿本地包 hash 冒称发布 hash。
商店接受不是实际用户安装、商店可见性或平台 OIDC/升级成功证明，这些仍待真人测试。
