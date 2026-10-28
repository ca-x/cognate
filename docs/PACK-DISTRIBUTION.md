# Pack 管理、分享与资源分发

本说明记录当前开发预览的服务端能力，不代表 F5、M4 或首发全量验收完成。CLI/MyGo 订阅、安装与跨 Pack 去重尚未接线；源仓库仍是收录时的不可变快照，未接入自动更新。插件仅在服务端运行。

## Web 操作

1. 在「仓库」收录内容，在「组合」新建 Pack。切换仓库不清空已选技能，可筛选当前仓库并移除已选项目。
2. 选择 1–256 个技能，为冲突项指定安装别名。别名是最多 80 字符的小写 ASCII 字母、数字、连字符或下划线，拒绝路径、Windows 系统保留名等不可移植名称；留空时服务端根据 skill 名称生成。
3. 同一别名与内容 hash 的多个来源可保留在选择列表中，分发 manifest 只保留一个确定代表；同别名不同 hash 拒绝整个写入，不静默覆盖。不同别名即使内容相同也保留。
4. 编辑和删除使用 revision CAS。其他会话先提交时，本次修改明确失败，网页保留尚未保存的输入；取消并重新读取后再修改。
5. 创建分享时可选 1、7、30 天或长期有效。令牌只在当前界面显示一次，可主动复制/隐藏；不进入地址、浏览器存储或 TanStack Query/mutation 缓存。离开组件或退出后不保留。剪贴板由浏览器/操作系统管理，Cognate 不会自动擦除。
6. 分享列表只有随机 ID、时间与状态，没有 token。撤销和删除均有二次确认；删除 Pack 同事务删除成员与全部分享。客户端已经接收或安装的内容不能追回。

创建分享请求如果已经提交，浏览器随后中断等待，不保证分享未创建。可重新读取列表并撤销无法取回凭据的记录。

## 数据与隔离

- ent 的 Pack 属于一个用户；成员只能引用该用户收录仓库中的技能。读取详情再次核验成员归属与 hash/别名，不只信任提交时检查。
- Pack 写入先取得 SQLite 写锁，再同事务解析全部选择、替换成员和提交 revision；失败回滚名称、成员与 revision。删除和更新必须提交当前 revision。
- 单用户最多 100 个 Pack；每 Pack 最多 100 个有效分享、1000 个总分享。过期/撤销记录暂不自动清理。
- 创建分享先对用户与 Pack 公共 ID 执行写 fence，避免删除/数字 ID 重用竞态。分享授权使用独立的随机公共 ID 和 token hash，不依赖猜测数据库数字 ID。
- 登录 session 和分享是不同凭据域。注销 session 不自动撤销已经创建的分享，必须显式撤销或等待过期。

## 分享凭据 v1

`shp1.` 后是无 padding 的严格 base64url，编码 107 字节：

| 字段 | 长度 |
| --- | --- |
| 版本 `1` | 1 字节 |
| 随机公开分享 ID | 26 字节 ASCII |
| 随机 nonce | 32 字节 |
| 签发时间 Unix 秒，大端 | 8 字节 |
| 过期 Unix 秒，大端；0 表示长期 | 8 字节 |
| 前 75 字节的 HMAC-SHA256 | 32 字节 |

签名使用外部 32 字节主密钥通过 HKDF-SHA256、独立 info `skillhub/pack-share-signing/v1` 派生的 key，与 session 签名分离。服务必须确认 master key 与数据库匹配。验证严格编码、长度、版本、常量时间 MAC、签发时间和过期时间，再用完整 token SHA256 查询当前数据库授权。有效期最长 366 天；签发时间容忍最多 30 秒时钟偏差。

签名有效不等于仍有权限：撤销、过期、Pack 删除、成员移除或 revision 不符仍拒绝分发。重建服务不会清除数据库撤销状态。token 类型的普通/Go 格式化和 slog 表示均脱敏；HTTP 没有请求/响应体或凭据日志。

## Connect / Buf 接口

权威 schema 为 `proto/skillhub/pack/v1/pack.proto`，Go 与 TypeScript 由 Buf 本地固定插件生成。

- `PackService` 使用用户 session 的 Authorization Bearer：Create/Update/List/Get/Delete Pack，Create/List/Revoke Share。
- `DistributionService` 使用分享的 Authorization Bearer，不能拿 session 代替；GetManifest 和 server-streaming DownloadSkill 均不接受文件路径、上游 URL 或服务端路径。
- Create/Update Pack 的 HTTP body 及 Connect 解码上限 64 KiB，容纳 256 项与最长别名；其他请求仍是 4 KiB。响应最多 8 MiB；下载数据块由应用层固定为最多 64 KiB。
- 过期/伪造/撤销分享统一返回 `unauthenticated`；初始下载 revision 过时返回 `aborted`。流进行中授权不再匹配时中断，不发送成功 final。RPC 错误不包含 token、路径或底层数据库诊断。

### Manifest v1

包含 format version、Pack ID、名称、描述、revision、manifest hash 与技能列表。元数据来自同一个短读事务快照，避免把不同 revision 的成员拼到一起。

成员按 slug、content hash、skill ID 稳定排序，再对相邻的 slug/hash 去重，选择 skill ID 排序最小的代表。Manifest hash 是域名、Pack 字段、revision 与排序后的公共成员字段的 SHA256；字符串以大端 64 位长度界定，revision 为大端 64 位整数。不包含生成时间或分享 token。同一 revision 的重复读取得到相同结果。

分享始终读取 Pack 当前内容，不固定创建分享时的 revision。客户端下载请求必须同时带 skill ID、content hash 与 Pack revision；过时的 manifest 需要重新读取，不能把不同 revision 的片段拼接使用。

### 二进制归档流

1. 验证分享并读取完整 Pack 快照，检查指定 revision 和成员。
2. 读取已缓存的整仓压缩 bytes，先核验其 archive SHA256，才开始解压；不能把「边拷贝最后再校验」的内容直接接入网络。
3. 复用完整有界归档检查，重新计算选中技能子树的逻辑 content hash，匹配后才产生发送 bytes。只导出精确子树，使用固定根 `skill/`，跳过 `.git` 目录和非普通链接，保留二进制、隐藏文件、空目录与执行位；不包含前缀相似的兄弟目录。固定时间、owner 和归档元数据，生成确定性 tar.gz。
4. 同步写入最多 64 KiB 的块，网络发送阻塞会自然形成背压，无 io.Pipe/后台生产者 goroutine。每块发送前用新的短数据库查询检查分享当前有效、revision 和成员/hash；不持读事务等待网络。
5. 每块携带连续 offset 与 data。成功 final 没有 data，offset 是总字节数，并携带 `transfer_sha256` 与逻辑 `content_hash`。必须同时收到正确 final、连续 offset、完整传输 SHA256 和正常 RPC 结束，才能视为成功。

**两个 hash 不可混用**：逻辑 content hash 包含相对路径、目录、文件内容和执行位；transfer SHA256 是本次 tar.gz 压缩字节的摘要。未来客户端必须再安全解包并核验逻辑 hash，不能只校验压缩文件摘要。

沿用整仓限制：32 MiB 压缩、64 MiB 普通文件、80 MiB tar 展开、10000 个条目、单 SKILL.md 最多 1 MiB；单技能输出压缩流再限制 81 MiB。下载服务和仓库服务各有两个 fail-fast 重 IO 槽，独立 HTTP 请求有 20 秒生命周期。HTTP 读/写 deadline 随超时/关闭取消提前触发，使真正阻塞的网络发送和未完成的请求体读取能够退出；不支持 deadline 的传输拒绝开启分发。Connect interceptor 将协议解析后的实际 RPC context 期限绑定到传输层，较短的客户端超时同样生效，监听覆盖整个响应与 trailers。

正常协议完成时，Connect 会主动取消自己的子 context，这不等于客户端取消或服务关闭。传输层集中协调 deadline：真实到期/外层取消立即中止读写；正常子 context 取消恢复外层期限，避免误伤 keep-alive。回调串行重核外层状态，全部取消并等待后才重置 deadline。关闭取消已接受请求并等待清理后再清空派生 key。

授权查询与实际网络发送无法形成数据库/网络原子事务。撤销与查询交错时，已经发送的字节及当前检查后正在发送的一块无法追回；保证后续块重新核验，而非承诺即时追回数据。

## 已验证与剩余范围

单元和真实 HTTP 测试覆盖所有者隔离、CAS、失败回滚、slug 冲突/去重、撤销/有效期、服务重建、分块 offset/摘要、二进制/隐藏路径/执行位及确定性归档。独立代表回归覆盖查询执行期间过期、HTTP/1 真阻塞写的 200ms Connect 期限、半包读取下关闭、正常完成后的 keep-alive，以及实际 HTTP/2 窗口背压下到期/关闭；保留在三个 `representative_*_test.go` 中。实际浏览器从 GitHub 收录两个分支快照，验证三个选择去重为两个 manifest 项、下载 gzip/传输 SHA256、编辑 alias 后分享跟随版本、旧 revision 无数据失败、并发编辑保留输入、撤销和删除失效。320/390px 无横向溢出，axe A/AA 无违规或待人工项；测试过程不输出分享凭据。

尚不代表完成：客户端消费/订阅安装、跨 Pack 去重、自动源更新、客户端配置附件/应用、完整服务端插件导入、S3、MyGo 原生 GUI/打包、Docker/远端 CI 及真实跨平台运行。独立用户代表验收结果和后续修复证据记录在实现计划与 `DEVELOPMENT.md`。
