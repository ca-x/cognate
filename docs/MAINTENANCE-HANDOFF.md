# 维护交接

2026-11-01 用户决定停止本轮自动开发，后续自行维护。不再自动推进功能、合入私有工作
或发布版本。完整需求仍以 `REQUIREMENTS.md` 为准，未完成清单见 `TODO.md`。

## 已交付

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
