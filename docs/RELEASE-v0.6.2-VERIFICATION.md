# 0.6.2 维护验证与发布记录

## 范围与固定源码

本轮仅发布 Web 交互维护对应的 Server 与懒猫 Exec 包，不重发未修改的 CLI 0.3.0
和 MyGo 0.1.4，不移动既有 tag，官方商店保持关闭。

Server 不可变 `server-v0.6.2` 固定源码
`643394746424f792a84724323b55cc8706a40ee7`。
懒猫不可变 `v0.6.2` 固定包装
`664e2d2b32db9976026d2a92b6f16530ea210476`，准确 pin 上述 Server tag 和完整源码。
后续文档提交不改变发布构件源码。

## 开发与发布门禁

Web 构建、状态检查、真实浏览器和截图记录见
`UI-INTERACTION-POLISH-VERIFICATION.md`：auth 10 项/52 张截图/33 种布局、sources
398 项、公开目录 108 条证据、备份设置 28 项和 Magpie 60 项场景通过。
覆盖中英、深浅主题、320/375px、200% 文本、键盘与 reduced-motion；触摸/笔仅为
PointerEvent 注入与移动 viewport，不冒充实机硬件测试。

本机首次针对 Web/server/httpapi 的 Go 测试中，既有
`TestConfigurationOwnerLifecycleAndCAS/json` 报 `1 success, 0 conflicts`；原失败
保留，未修改 Go 后端、该测试或断言。随后隔离五次和完整 httpapi 包复测通过。
不能据后来通过宣称根治该历史时序问题。

准确源码的 Build `42240548849` completed/success，全部 Go/Web/Buf 检查、六平台
交叉构建与双架构 Docker 均通过。Server release `42243330417` completed/success，
再次通过共享检查、原 OCI 构件提升与按 child digest 拉取执行的双架构验证；不重建
已测试构件，不更新 Docker latest，不另外创建 GitHub Server Release。
Platform `42243330335` completed/success，不据此关闭历史 Darwin 失败或扩大客户端
原生运行资格。

包装候选使用 Go 1.27.3、Node 26.12.0、CGO0 从固定干净源码构建；shell 语法、
actionlint、diff 检查与完整隔离 root 启动 suite 通过。首次管理员、升级原账号/主密钥、
卷链接、只读卷与不安全祖先拒绝均保持。新增匿名 HTTP 请求核对完整 React Bits
许可，构建另核对并附带同一许可文件，不仅检查源码暂存目录。

本机证据保存在 `/tmp/cognate-release-0.6.2`；截图、日志和真实用户数据不入 git。
本机 actionlint/shellcheck 初次经 mise shim 未配置版本而无法执行，改用已安装的准确
工具路径后通过；未为此修改全局配置或放宽断言。

懒猫 run `42246065093` attempt 1 长时间停留在安装 bubblewrap 的 `apt-get update`，
日志显示 Ubuntu Azure 镜像请求 `Ign`；尚未执行构建、测试或任何发布步骤。
取消后核实 `lazycat` job 为 skipped，保留完整启动 job 日志
`lazycat-attempt1-startup-cancelled.log`，只允许同一源码的一次基础设施重跑。
这不是测试通过，也不据后续结果宣称镜像网络问题已经根治。

## Server 最终镜像

镜像为 `ghcr.io/ca-x/cognate:server-v0.6.2`：

- Index：`sha256:f1ddc3e2dfec3ac0b5a3513f47108596d5bf0e4617bbe295bc08cd53fbf993bc`
- Linux amd64：`sha256:2dc6be120553f062818a4a0ddbcd4a1adef62ff2d9066ff44a0d153c08b2bfd9`
- Linux arm64：`sha256:c6cb1b95cf30d623f9de475d3b06b850d81eca1c144f79a5e36b166c009df911`

本机独立匿名读取 index、两个 child manifest 和 config，逐字 SHA256 与响应 digest
一致；架构恰为 Linux amd64/arm64，运行身份仍为 `10001:10001`。
完整下载 amd64 最终载荷层并核对其 digest，提取实际服务端 ELF，SHA256 为
`e0590371fcf2e785f71f8bd251c2f7f88a1676f2ba300bc261e8876558fe299a`。
对该最终二进制另跑完整隔离 root 启动 suite，首装、升级和不安全路径拒绝全部通过；
实际匿名 HTTP 返回完整 React Bits 原文许可，非仅源码或暂存目录检查。
本机隔离执行不代替容器检查；发布 CI 另外按两个准确 child digest 拉取并执行镜像。
证据为 `docker-verification.json` 和 `docker-final-startup.log`。

本机大文件串行下载缓慢，保留原日志后改用有界八路 Range 下载；完整拼接后仍校验
全部字节、大小与 SHA256，不以若干片段或响应头代替最终产物验证。

## LazyCat Exec 最终包

LazyCat run `42246065093` attempt 2 completed/success，准确包装源码与 tag 不变，
完整 startup-smoke 和构建/分发 job 成功。公开 GitHub Release `v0.6.2` 非 draft、
非 prerelease；最终资产 `community.lazycat.app.cognate-v0.6.2.lpk` 为
**78,898,688 字节**，SHA256
`e1363b85e71c05d7f2e70064acfedde19a27eceb936834f6c523dab3caa82403`。

最终 LPK 已匿名完整下载，整体哈希与 GitHub API digest、喵喵回执一致。独立核对
外层五项、content 八项白名单、root 属主、文件权限、部署参数、manifest、图标、
原 wrapper、SOURCE_COMMIT 和全部许可。最终 ELF SHA256 为
`95bc90221457d8fae4f2db85e249a70c2136d8af424bbfb66f1a36a1c6df8917`，与本机固定
源码候选逐字相同，Go 1.27.3/Linux amd64/CGO0、准确 revision 与 clean VCS metadata
均通过。额外许可文件与匿名 HTTP 原文均保留完整 MIT + Commons Clause。

对最终下载解包的 binary/wrapper 再跑完整隔离 root suite，首装、升级原账号/主密钥、
不安全/丢失密钥拒绝、卷链接、只读卷及祖先权限均通过，不以候选二进制代替实包。
证据为 `lpk-verification.json`、`lpk-anonymous-download.log` 与 `lpk-final-startup.log`。

喵喵回执 app `670` / version `4295`，published=true、skipped=false，准确引用上述
最终资产 URL 与 SHA256；`onlineVersion=0.6.1`，不能声称店面已切换至 0.6.2。
只发布喵喵商店，官方关闭；没有懒猫盒子真机升级或平台 OIDC 验收。

## 发布检查表

| 分发表面 | 证据与范围 |
| --- | --- |
| 审查基线 | Source `server-v0.6.1..server-v0.6.2`；包装 `v0.6.1..v0.6.2`，发布源码固定，不含后续文档提交 |
| 工作区 | 起始两仓库均干净；包装六文件改动逐项提交，后继仅更新两仓库的本轮发布记录，无未知用户修改 |
| 远端 | 两仓库经 SSH 推送，发布 tag 已用 GitHub API 回读完整 SHA；最后文档同步另核对 main |
| 版本字段 | 组件独立 tag；包装 `package.yml` 0.6.2、`upstream.env` Server tag/完整 SHA 与发布一致，无需更改 Web 私有包版本 |
| 发布通道 | Server stable 双架构 Docker、LazyCat stable Exec/喵喵；不更新 latest、不发官方商店、不重发 CLI/GUI |
| 运行依赖 | 无新增产品运行时依赖；打包与启动校验只用既有 Go/Node、Python 标准库和 bubblewrap |
| 生成资源 | 固定源码的 Vite 输出嵌入服务端；构建和启动校验完整第三方许可，不改 Buf/Go 生成协议 |
| 归档内容 | Docker 最终层完整核验；LPK 匿名完整下载及八项 content 白名单通过 |
| 隔离运行 | 准确源码候选、最终 Docker ELF、最终 LPK 均跑完整首装/升级/权限 suite，包含匿名许可 HTTP 核对 |
| 资产与注册表 | Server 匿名 digest 回读通过；完整 LPK 与 GitHub/喵喵哈希一致，商店接受不等于线上切换 |
| CI | Source Build、Server release、Platform 成功；LazyCat attempt 1 取消，单次基础设施重跑 attempt 2 成功，保留原日志 |
| Issue/PR | 源码仓库无打开的 PR，本轮没有关联 Issue/PR，不做评论、关闭、合并或 Release reaction |

通用 `release_gate.py` 已运行：源码当时只有两份已知未跟踪发布文档，tracking ref 与
HEAD 相同，但它不识别本项目组件前缀 tag，也不识别包装 YAML 版本；这些 N/A 项由
本表的准确 tag、SHA 和字段核对补足，不拿通用脚本的未识别当作版本门禁通过。

如需回退，应先备份状态并使用上一版已发布分发构件，不重新生成主密钥、不重置账号，
也不移动或覆盖任何已发布 tag。

## 未完成边界

仅完成本轮 Web 维护与组件分发，不关闭产品完整 backlog。完整恢复、外部供应商、
平台 OIDC、懒猫真机升级和原生 GUI 仍按原资格独立验收。React Bits 完整许可为
MIT + Commons Clause，SHA256
`4bdd1fa59a6b7d5954bc3b213dbc34e455c635a94bbcecf9293553797d0fad02`，共 2,060 字节；
不将其表述为纯 MIT。
