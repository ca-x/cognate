# 维护交接

随后用户额外授权 Web 界面与交互维护并发布 **Server/懒猫 0.5.0**：仓库内容优先、添加弹层、
导入渐进展开、配置筛选和导航代码预热；不恢复完整 backlog，不包含原生 GUI 更新。
变更见 `RELEASE-v0.5.0.md`，最终 Docker、LPK 完整下载/隔离启动及喵喵接受见
`RELEASE-v0.5.0-VERIFICATION.md`。喵喵回执 onlineVersion 仍 0.4.0，真机与店面切换待验证。
独立 Darwin 观测 cleanup/seal 失败另记，不把 Linux 服务端成功扩大为客户端安装资格。

2026-11-04 用户额外授权修复 OIDC 关联/解绑、Magpie 加密界面导出和 cc-switch
数据库兼容性，并要求发布新版。本次为有限维护发布，不恢复下述原功能 backlog。
Server/懒猫 0.4.0 的变更与验证见 `RELEASE-v0.4.0.md` 和发布验证记录；
CLI 0.2.0、GUI 0.1.3 不重发。

2026-11-01 用户决定停止本轮自动开发，后续自行维护。不再自动推进功能、合入私有工作
或发布版本。完整需求仍以 `REQUIREMENTS.md` 为准，未完成清单见 `TODO.md`。

## 已交付

- UI 维护发布（2026-11-04）：Server `server-v0.5.0` 固定 `66c8d76215f8696180a992e5cb5a1f0f5c7eb3b8`；release `41814175849`、源码 Build `41814073679` success。LazyCat `v0.5.0` 固定包装 `c712b3a48f0d9e69754519349ac7c2a4b002ef3c`，run `41815749938` success，公开 Release `433794212` 与最终包完整核验通过；喵喵接受 version `4128`，官方关闭。CLI 0.2.0、GUI 0.1.3 不重发。

- 有限维护发布（2026-11-04）：Server `server-v0.4.0` 固定 `bb81f39b77c9776e42fba34566095c772cd2623a`，release `41679165153` success，双架构 Docker 与匿名访问校验通过；LazyCat `v0.4.0` 固定包装 `8720054f99a79b41d9737170fa51e765cf092ec6`，run `41681049194` success，最终 LPK 完整下载、许可/metadata/隔离启动通过。喵喵接受 version `4074`，回执 onlineVersion 仍 `0.3.0`，不等于店面已切换；官方关闭。详见 `RELEASE-v0.4.0-VERIFICATION.md`。下列旧版交付记录保留为历史，不恢复原 backlog。

- 服务端 `server-v0.3.0`：源码 `20c7a193a9582cd14cf9d29cbbbdcfbf414e54ec`，
  Linux amd64/arm64 Docker 已发布且通过最终镜像校验，不更新 `latest`。
- 懒猫 Exec `0.3.0`：包装仓库 `lazycat-contrib/cognate-lzcapp`，
  标签源码 `ca422c63a559f5b520c9e621d5ec59eb3497c992`，公开 LPK 已完整下载核验，
  喵喵商店接受；回执在线版本仍为 `0.2.0`，店面切换及真机升级待确认。
- CLI 保持 `cli-v0.2.0`，GUI 保持 `gui-v0.1.3`，不混入未验收改动。
- 发布证据、准确 digest、升级限制见 `RELEASE-v0.3.0-VERIFICATION.md`；
  使用缓存维护前阅读 `CACHE-MAINTENANCE.md`，不要将单归档修复视为完整恢复。

## 尚未合入

以下工作保留在本机私有临时目录，不属于主线或任何公开版本；临时路径不是远端备份，
如需继续请先自行保存并核对来源。没有为了结束开发而勾选其验收项或合入半成品。

| 工作 | 本机位置 | 边界 |
| --- | --- | --- |
| MyGo Codex/Gemini 本机应用组合树 | `/tmp/cognate-mygo-main-20c7a19-nDCEsREd` | 可见字段标签修复已验证；取消后 Unknown 安全证据交付仍有 P2，新增回归尚未完成验证，不能直接发布 |
| MyGo 作者冻结 | `/tmp/cognate-mygo-native-29b172d-AwnbrQUO` | 原 exact10 冻结及作者报告，不等于组合树独审通过 |
| C7 S1 | `/tmp/cognate-c7-s1-implementation-bd39df7-PVijVEfv` | 未收完整最终冻结；包含额外 legacy S0 测试夹具的第 78 路径授权，不代表生产自动升级或 Pack 消费已启用 |
| R3 恢复 | `/tmp/cognate-r3-7244ced-nUndUL` | 私有实施，不等于完整恢复交付或验收 |

完整恢复、归档回收及引用闭包、Pack 配置消费与自动应用、其它 agent/profile、完整导入、
CLI 跨平台鉴权、平台 OIDC、签名与真人安装等未完成项继续以 `TODO.md` 为准。
历史 Darwin APFS 清理失败仍未闭合，不以其它成功运行覆盖。

## 后续维护底线

- 固定栈及需求决策不变；无 cgo，组件使用各自 tag，不移动已发布标签。
- 不生成替代主密钥解密旧数据，不记录 token、密码或密钥。
- 私有工作先做来源核验、针对性回归及独立审查，再考虑合入；不要直接复制活工作目录。
- 本轮结束不代表完整需求完成，也不代表全部平台运行资格已验证。
