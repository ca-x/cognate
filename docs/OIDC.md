# OpenID Connect 登录与显式绑定

本设计参考 cat-led 的可选 OIDC 入口及签名会话模式。本项目以标准 discovery 和签名 ID token 验证身份，保留本地账号登录；不接受未经配置的网关身份头，不按邮箱或用户名合并账号。

## 身份与会话

- 外部身份为精确、区分大小写的 `(issuer, subject)`，数据库唯一约束及不可变用户外键保证一项身份只属于一个内部用户。
- 首次 OIDC 登录创建独立内部用户；已登录用户点击“绑定 OpenID Connect 账号”才执行绑定。回调时再次验证原本地会话仍有效；已属于另一用户的身份不转移、不合并。
- ID token 校验签名、issuer、audience、有效期、nonce 和授权方；使用授权码流程和 S256 PKCE。Provider 地址只来自管理员配置，HTTPS 为默认，HTTP 仅允许 loopback 开发服务。
- OIDC 与本地登录共用 HMAC 签名的随机会话及数据库 token hash；OIDC access/ID token 不落盘，客户端不接触 provider token。

## 浏览器流程

1. Connect `StartOIDCLogin` 返回授权 URL，并设置 HttpOnly、SameSite=Lax、host-only 的短期事务 cookie。绑定模式要求现有 Bearer 会话。
2. 浏览器跳到 provider；回调为固定 `PUBLIC_ORIGIN/auth/oidc/callback`，不从 Host 或请求参数拼接重定向目标。
3. state 与独立浏览器 cookie 匹配后，一次性消费事务，交换授权码并验证 ID token。错误只返回固定的登录失败标记，不输出 provider 原始消息。
4. 服务端创建本地会话，将领取凭证放在另一枚短期 HttpOnly cookie 中，重定向回 `/?oidc=complete`；URL 中不放会话 token 或领取凭证。
5. 前端调用 `CompleteOIDCLogin` 一次性领取会话，清除领取 cookie 与 URL 标记；会话仍只保存在当前页面内存。

进行中的事务和待领取结果均有有效期与容量上限，服务端重启后需重新登录。收到取消或关闭时取消并等待正在进行的工作。反向代理应隐藏 callback query 和 Cookie/Authorization 日志；应用本身不记录这些内容。

## 管理配置

使用 `--oidc-issuer`、`--oidc-client-id`、可选的 `--oidc-client-secret-file`，以及必须明确设置的 `--public-origin`。也支持相应 `SKILLHUB_OIDC_*` 环境变量。client secret 由私有文件读取，不写入项目配置或日志；公开客户端可仅使用 PKCE。这些配置不允许通过用户 RPC 修改。

验收需涵盖签名伪造、错误 nonce/issuer/audience、错误浏览器 cookie、重放、过期、显式绑定冲突、原会话撤销、用户隔离与不泄露 provider token；真实 provider 部署另行验证。

## 当前验证与边界

Go 测试使用真实 ES256 签名与 JWKS，验证 nonce、audience、issuer、expiry、signature、azp、PKCE、浏览器绑定、单次领取及撤销后拒绝绑定。绑定事务再次查询原会话 hash、归属与有效期；事务读出后从第二连接撤销，也不能提交新身份或会话。有效期检查发生在提交调用之前，不保证提交瞬间的绝对时间边界。

OIDC 网络请求均受大小限制、10 秒 HTTP 超时与服务生命周期控制。SDK 自行发起的后台 JWKS 请求也由取消传输层跟踪，关闭时先取消并等待，随后关闭状态库。

已在浏览器使用本地签名 provider 验证：本地账号注册 → 显式绑定 → cookie 领取 → 退出 → OIDC 登录，内部用户保持不变；最终 URL 无领取凭证及会话 token，390px 布局无水平溢出，来源页 axe WCAG A/AA 无违规。这是协议测试环境，不代表真实 provider 已部署。

可重新运行浏览器夹具：先构建前端，再设置 `SKILLHUB_OIDC_BROWSER_FIXTURE=127.0.0.1:18882`，执行 `go test ./internal/server/oidclogin -run '^TestOIDCBrowserFixture$' -v -count=1`。夹具只监听 loopback，使用临时数据库和测试身份；仅用于自动化测试。完成后 POST `/__test/finish`，否则三分钟超时并清理。
