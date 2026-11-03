# Server / LazyCat 0.4.0 发布验证

## 范围与本地验证（2026-11-04）

- 仅用户授权的 OIDC 账号关联/解绑、Magpie 界面加密导出及 cc-switch 数据库兼容修复；不恢复旧功能 backlog。CLI/GUI 不重发。
- `CGO_ENABLED=0 GOTOOLCHAIN=local`，Go 1.27.3；`go test ./...`、`go vet ./...` 均退出 0。
- `buf lint`、对修复前 HEAD 的 `buf breaking`、Go/TS 重新生成与已检入生成物比对、`git diff --check` 通过。
- Web build 与 Magpie 独立 Node 加密/WebCrypto 解密测试通过；密码错误、篡改、KDF 边界、BOM、取消及凭据排除覆盖。
- Magpie 真实 TLS/SQLite/moejs 浏览器测试通过：密码错误不上传，正确密码得到候选；抓取合成请求确认无密码和文件/配置 canary；375/1440 布局通过。
- Auth 真实浏览器回归通过：8 检查、49 张截图、32 布局。原脚本截图前的密码清空断言曾失败；保留该断言，并增加第二次 GetSession 请求及表单结束的明确等待。生产登录页未因此改变。
- 新签名 OIDC/TLS Connect 测试验证带会话、无绑定 flag 时归属不变；解绑密码错误不登出、正确解绑后密码可登录；跨用户隔离、其它会话撤销及 OIDC-only 防锁死均通过。
- 用户授权 `~/.cc-switch/cc-switch.db` 仅以只读方式读取，在内存通过实际 ProjectSelection、moejs 和 SelectionService 预览；schema23、约 45 MiB，无真实数据库、行值或凭据提交。
- 本地原始证据位于 `/tmp/cognate-login-import-fixes-1lOiqoBs`，不是远端备份；测试夹具只使用临时合成数据。
- `actionlint -shellcheck=` 结构检查通过；完整 actionlint 的九条既存 ShellCheck 提示与修复前 HEAD 一致（忽略新增测试引起的行号偏移），未修改无关脚本。

## 发布状态

服务端双架构镜像、最终镜像门禁、懒猫最终 LPK 校验与喵喵回执尚待发布后补记；
本地通过不能代替发布成功。官方商店保持关闭；真实懒猫 provider 和升级验收仍由用户进行。
