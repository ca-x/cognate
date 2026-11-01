# Cognate Server 0.3.0 发布核验

更新：2026-11-01。本次仅更新服务端及懒猫 Exec 包，CLI 保持 `0.2.0`，
Desktop 保持 `0.1.3`；不移动已发布标签，不包含未合入的 MyGo、Pack 或恢复改动。

## 服务端

`server-v0.3.0` 固定源码 `20c7a193a9582cd14cf9d29cbbbdcfbf414e54ec`，
annotated tag 对象为 `886ec95bba7dcde2ed5bd1f58e70197c04fd8eac`，SSH 推送后已读回。

| 检查 | 证据 | 结果 |
| --- | --- | --- |
| Main Build | `41299790509`，准确源码 `20c7a19` | success |
| Main Platform filesystem primitives | `41299790609`，准确源码 `20c7a19` | success，不覆盖历史 Darwin 失败 |
| 标签发布 | `41303485978`，共享测试、Web/Buf、双架构 Docker、镜像提升 | success |
| 最终镜像验证 | 同一 run 的 `verify-server / verify` | 双架构实际拉取和受限容器 `--help`、匿名访问均通过 |
| 本轮定向回归 | `CGO_ENABLED=0` 的 filelock/library/servercli tests、vet | 全部 exit 0 |
| 本轮懒猫预发布启动 | 六场景 bubblewrap 隔离检查 | 新装、原账号/密钥保留、升级、卷别名、只读卷及不安全祖先检查通过 |
| 本轮 Linux ELF 修复 | 合成归档先拒绝，修复后校验，再核对保留 inode/原字节和同 ID 重查 | exit 0，不读取主密钥 |

镜像为 `ghcr.io/ca-x/cognate:server-v0.3.0`，index digest：

```text
sha256:aa92a0e3091888e52cf80ffa97575c70c4adc0e85cba77481e1aca096e6b0623
```

主线程另取匿名 GHCR pull grant，下载实际 index 与两个 child manifest，重新计算 SHA256
并逐个比较 registry header 与工作流预期；准确包含 Linux amd64/arm64。
没有更新 `latest`。初次人工选取了日志中 binfmt 的 digest，校验器按预期拒绝；随后从
`IMAGE_DIGEST` 唯一值取准确预期并完成核验，该输入错误不作为产品失败或通过证据。

## 独立范围审查

本轮非作者审查限定 ACCEPT，无新 P1/P2。cache 九源逐字匹配此前两项 P2 修复后的
已接受快照；懒猫仅四个发布文件变化，官方商店关闭，启动参数、升级行为和许可打包门未变。
该审查不代替最终资产、商店或真机验证。

报告 SHA256：`fcae080834640cf9fd9908840bc616347fde430748137395532c5ed747d23fc1`。

## 懒猫

包装标签 `v0.3.0` 固定 `ca422c63a559f5b520c9e621d5ec59eb3497c992`，
annotated tag 对象为 `d4eb4b136b922f428f613ef1ce157980abfa99fc`。
`upstream.env` 固定上文服务端 tag 和完整源码提交；package 版本 `0.3.0`，
官方商店关闭，仅喵喵发布。工作流 `41304471910` completed/success，包含固定源码
构建、隔离启动、LPK 打包、GitHub Release 和喵喵发布；官方商店步骤跳过。

GitHub Release `430556181` 公开且非 draft/prerelease，主线程已匿名 API 读回。
最终资产 `community.lazycat.app.cognate-v0.3.0.lpk` 完整下载，大小 `78537728` 字节，
实际 SHA256 与 GitHub asset digest 和商店回执一致：

```text
2ca687ca2d1eb451955cbc17572560734cc268f9a4516d6a5c40ad6a9206d5c9
```

严格核对外层五文件和内层七文件白名单、类型、权限、owner，manifest、安装参数和图标
均与包装源匹配；服务端 LICENSE、Codex 许可及 Gemini 两份许可声明逐字匹配源码。
最终 ELF 的 Go metadata 为 Go `1.27.3`、Linux amd64、`CGO_ENABLED=0`、准确源码
revision、`vcs.modified=false`；随包 `SOURCE_COMMIT` 同样匹配。该 ELF SHA256 为
`23adc907e362d40af6671fca1124598e32fd6eac6ede9cd7d453bc0f2dcd118f`。

实际从最终 LPK 提取的 ELF 和启动脚本重新通过六场景隔离启动检查，包含原管理员密码、
原主密钥保留、重启、丢失/不安全密钥拒绝；同一最终 ELF 的合成归档修复、校验、保留
损坏 inode/原字节及同 ID 健康重查也全部 exit 0。验证器使用既有隔离环境 PyYAML `6.0.3`；
默认 Python 缺少依赖的首次启动错误不计作产品行为结果。

喵喵回执：`published=true`、`appId=670`、`versionId=3880`、`version=0.3.0`，
但 `onlineVersion=0.2.0`。因此仅确认商店接受，尚未确认店面切换或真机升级。

## 验收边界

离线修复仅支持 Linux legacy 布局，保留原件且不自动回收，不等于数据库完整恢复。
最终 LPK 内容及隔离启动通过仍不代替懒猫真机升级测试。
此前 Darwin APFS 清理失败仍保留，不通过新 Build 或 Platform success 改写历史结论。
本次不新增 GUI、其它 agent/profile 或 macOS/Windows 运行资格；完整未完成项继续见 `TODO.md`。
