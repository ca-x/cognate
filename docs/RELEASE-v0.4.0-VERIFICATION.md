# Server / LazyCat 0.4.0 发布验证

## 范围与本地验证（2026-11-04）

- 仅用户授权的 OIDC 账号关联/解绑、Magpie 界面加密导出及 cc-switch 数据库兼容修复；不恢复旧功能 backlog。CLI/GUI 不重发。
- `CGO_ENABLED=0 GOTOOLCHAIN=local`，Go 1.27.3；`go test ./...`、`go vet ./...` 均退出 0。
- `buf lint`、对修复前 HEAD 的 `buf breaking`、Go/TS 重新生成与已检入生成物比对、`git diff --check` 通过。
- Web build 与 Magpie 独立 Node 加密/WebCrypto 解密测试通过；密码错误、篡改、KDF 边界、BOM、取消及凭据排除覆盖。
- Magpie 真实 TLS/SQLite/moejs 浏览器测试通过：密码错误不上传，正确密码得到候选；抓取合成请求确认无密码和文件/配置 canary；375/1440 布局通过。
- Auth 真实浏览器回归通过：8 检查、49 张截图、32 布局。原脚本截图前的密码清空断言曾失败；保留该断言，并增加第二次 GetSession 请求及表单结束的明确等待。生产登录页未因此改变。
- 新签名 OIDC/TLS Connect 测试验证带会话、无绑定 flag 时归属不变；解绑密码错误不登出、正确解绑后密码可登录；跨用户隔离、其它会话撤销及 OIDC-only 防锁死均通过。
- 额外真实浏览器检查通过：注册本地密码账号，经本地签名 provider 绑定，内部用户 ID 保持不变；误输解绑密码仍留在原账号，正确解绑后身份列表清空且当前会话保留。额外探针首次因误用按钮译文、第二次因抢在 OIDC 导航完成前跳转而失败；修正探针定位和导航等待后通过，未修改生产实现。
- 用户授权 `~/.cc-switch/cc-switch.db` 仅以只读方式读取，在内存通过实际 ProjectSelection、moejs 和 SelectionService 预览；schema23、约 45 MiB，无真实数据库、行值或凭据提交。
- 本地原始证据位于 `/tmp/cognate-login-import-fixes-1lOiqoBs`，不是远端备份；测试夹具只使用临时合成数据。
- `actionlint -shellcheck=` 结构检查通过；完整 actionlint 的九条既存 ShellCheck 提示与修复前 HEAD 一致（忽略新增测试引起的行号偏移），未修改无关脚本。

## 发布状态

- 不可变 `server-v0.4.0` 源码为 `bb81f39b77c9776e42fba34566095c772cd2623a`。
- 源码 Build `41676917605` completed/success，含全量 Go、Web、Buf、六平台/架构交叉编译及两个 OCI 架构构建；平台原语 `41676917558` success，不扩大原有 Darwin 运行资格。
- 懒猫候选包装 `8720054f99a79b41d9737170fa51e765cf092ec6` 固定上述源码。发布前 LPK lint 无警告，真实本地打包、content 白名单/许可/CGO0/source metadata 和六场景隔离启动通过。最终 CI 资产仍须独立下载校验，不能使用本地包替代。

服务端 release `41679165153` completed/success，准确源码为上述不可变 tag。
`ghcr.io/ca-x/cognate:server-v0.4.0` 的 index digest：
`sha256:ee3616a4532af4d7950953e0ad10177d06a9476f1f941345ef6e6a9c6b37744f`。
最终镜像门禁拉取 Linux amd64/arm64 并执行二进制；本机独立匿名读取 index 和两个
child manifest、校验各自哈希通过。不更新 `latest`。

懒猫不可变 `v0.4.0` 固定包装 `8720054f99a79b41d9737170fa51e765cf092ec6`；
上游服务端门禁通过后才推送该 tag。LazyCat workflow `41681049194` completed/success，
包含 runner root 启动门和发布。公开正式 Release `432994285` 非 draft、非 prerelease。

最终资产 `community.lazycat.app.cognate-v0.4.0.lpk` 为 **78,648,320 字节**，
完整 API 下载 SHA256：
`204c29043268236d3219ad3946e152d2571d460b0af3b27f086475dde4c4b41d`。
逐字核对原 wrapper、三份上游许可/声明、本项目许可、SOURCE_COMMIT；白名单、权限、
Go 1.27.3、CGO0、Linux amd64 和 clean VCS metadata 通过。最终 ELF 与本地固定源码
构建逐字相同，SHA256 为
`6be1cd55d6fc48d8d053940d01585a214da446c67084508349614f63f972c443`。
对最终下载解包内容再次运行六场景隔离启动通过，覆盖首次管理员、升级保留原账号和
主密钥、不安全/丢失密钥拒绝、卷链接、只读卷及祖先权限边界。

喵喵回执：`published=true`、`skipped=false`、app `670`、version `4074`，
准确下载 URL 和 SHA256 与上述最终资产一致；回执 `onlineVersion=0.3.0`，
不将接受提交等同店面已切换 0.4.0。官方商店关闭。真实懒猫 provider、店面切换和
真机升级数据保留仍由用户验证；历史独立 OIDC 账号不自动合并，Magpie 不是完整备份恢复。
