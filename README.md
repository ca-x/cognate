# Cognate

原名 SkillHub；仓库已迁移至 `ca-x/cognate`。命名与已有数据/协议的兼容策略见 `docs/COGNATE-RENAME.md`。

Cognate 用于聚合、分发和同步 coding agent skills。当前是开发预览：服务端已接通本地账号、OIDC、GitHub 设备授权、Lists 定时同步/增量、获授权的公开与私有 GitHub 仓库收录及来源更新，并提供 Web 面板与 Pack 分享/流式分发。Linux CLI 与 MyGo 客户端已有 Pack 订阅、跨组合去重、显式/持续同步与备份卸载；Web 支持 Claude 与 Codex 私有 provider 资产管理，Claude 另有 Linux 手动 CLI 预览、批准应用和恢复，Codex 尚不能本机应用。匿名公开目录/分类、免 token 安装、MCP token 管理及 Web 主题/中英文切换已通过各自限定范围的独立验收；整合验证方法与边界见 `docs/VERIFICATION.md`。完整配置导入、配置随 Pack 分发和自动应用、MyGo 配置界面及非 Linux 实际安装仍在开发，尚未发布首个正式版本。

## CLI

需要 Go 1.27.3 和 Git。构建和运行：

```sh
CGO_ENABLED=0 go build -o cognate ./cmd/cognate
./cognate scan /path/to/repository
./cognate browse vercel-labs/skills
./cognate browse owner/repository --branch main
./cognate search "react" --limit 20
```

`browse` 需要 Git 和网络，克隆到临时目录，扫描完成后删除；支持 `owner/repo` 和 GitHub HTTPS 仓库 URL。`scan` 扫描本地目录。扫描包含隐藏目录，但跳过 `.git`。缺失的 name 或 description 使用 skill 目录名。

`search` 直接查询公开的 `skills.sh` 搜索索引，查询词会发送给该服务；结果仅供发现，不自动下载或安装。独立浏览模式与现有 Pack 订阅/显式同步分开，Pack 客户端仍须通过 Cognate 服务端获取内容。

第三方导入插件仅在服务端执行；客户端插件入口已撤下。服务端已有 cc-switch 与源格式验证后的 Magpie 内部只读预览，完整授权上传/持久化/配置导入仍在开发，见 `docs/PLUGIN-IMPORT.md` 和 `docs/MAGPIE-IMPORTER.md`。

首次使用本地安装、Pack 同步或配置应用前，先显式运行 `./cognate client-key init`；可用 `--timeout 30s` 设置初始化期限（大于零且不超过两分钟）。该命令只初始化或核实本机加密密钥，不登录服务端、不访问网络。MyGo 对应入口在“账号 → 本机设置”。已有密钥会原样保留；缺失且存在初始化标记或客户端数据时必须恢复原密钥，不会自动生成替代。密钥默认位于 `~/.config/skillhub/client.key`，请单独安全备份，勿上传或写入日志。`scan`、`browse`、`search` 不需要初始化；匿名安装虽然不需要服务端 token，仍需要本机密钥准入。当前本机初始化仅支持 Linux，不能以交叉构建成功代替其他平台支持。

`client-key init`（含 `--help`）的输出仅支持具备所需 deadline 能力、可独立重开的 Linux 终端或 pipe/FIFO，不支持直接 `>普通文件` 或 socket 型捕获。需保存提示时可经受支持管道交给 `cat`/`tee`，并用 `pipefail` 检查 CLI 自身退出状态；这不同于 `pack watch` 的普通文件输出支持。

Linux 开发环境可用 `./cognate install-local /path/to/skill --slug example` 实验性安装本地 skill。该命令只向已存在的 agent skills 目录创建链接，状态保存在 `~/.local/share/skillhub/`；重复安装时，只有已记录的内容和全部目标核验一致才会跳过。`./cognate uninstall-local example` 先完成备份再保守卸载已记录且未变化的目标。可用 `./cognate install-status` 查看未完成操作，或用 `./cognate install-status --reconcile` 取消未开始的操作并将已中断操作标记为需关注；该命令不会删除现场。自动恢复/续跑和人工处理界面仍未实现，请勿用于无法人工处理的环境。WSL 的 copy 安装及其他平台尚未完成。安全边界见 `docs/INSTALL-RECOVERY.md`。

Pack 客户端命令为 `pack subscribe --server ORIGIN --token-stdin`、`pack list`、`pack sync`、`pack watch --interval 5m --attempt-timeout 2m` 和 `pack remove SUBSCRIPTION_ID`。watch 在前台串行同步，失败有界退避、空闲时释放安装锁，Ctrl-C 停止，不自动创建后台服务。分享原文仅从标准输入读取，使用独立私有密钥加密落库；同技能跨组合只安装一份，更新和最后一次取消订阅会先备份。完整顺序、密钥保管、Linux 限制及未完成恢复能力见 `docs/CLIENT-SUBSCRIPTIONS.md`。这不等于 F5 或 M2/M4 全部验收完成。

公开目录位于 `/explore`，不需要登录。仓库默认不发布；只有所有者确认公开完整 skill 目录后，游客才能浏览、搜索、分类筛选和安装。Linux 客户端使用 `./cognate install-public --server https://your-cognate.example --skill SKILL_ID`，只访问 Cognate，验证二进制传输和逻辑 hash 后安装到 canonical，不需要或生成 token。取消公开会拒绝新的下载授权，不会删除此前独立安装的本地文件。不要公开含敏感文件的 skill；隐藏文件和二进制也属于发布内容。

Web 登录页、匿名目录和管理页均提供浅色/深色/跟随系统主题与简体中文/English 切换；只持久化主题和语言偏好，网页登录会话仍仅存在内存。MCP 的用户凭据/权限和真实官方 SDK 协议见 `docs/MCP.md`；MCP token 与网页会话、GitHub 凭据及 Pack 分享相互独立。

实验性 `./cognate backup-local example` 可在私有状态目录中创建已安装 skill 的本地快照，包含 `.git` 和普通权限位；快照未加密，可能包含敏感文件。`./cognate backup-list` 列出记录，`./cognate restore-local BACKUP_ID EXISTING_PARENT NEW_NAME` 将快照恢复为此前不存在的新目录，不覆盖原文件或建立安装映射；恢复会保留根目录权限，可能允许其他用户读取或写入。列表不验证快照是否仍可恢复；中断后的自动恢复尚未实现，快照本身不构成删除授权。

## 自动构建

GitHub Actions 构建 Web 面板，打包 Windows、macOS、Linux 的 amd64/arm64 CLI 与服务端二进制，以及 Linux amd64/arm64 Docker OCI 镜像 artifact。服务端目前在 Unix 上验证数据目录安全，Windows 服务端暂不能启动；能交叉编译不代表平台原生运行已通过。MyGo 原生桌面客户端及独立打包流程见 `docs/DESKTOP.md`。

CI 对 Protobuf 执行 Buf lint、Go/TypeScript 生成结果比对，并在基准提交已有 schema 时检查兼容性。改名阶段的完整构建已在 Actions 验证；桌面阶段也已实际生成三系统双架构安装产物，但仍为未正式发布的 unsigned/ad-hoc preview，不能视为各平台安装与完整产品验收通过。逐提交结果及后续测试 fixture 问题见 `docs/VERIFICATION.md`；OCI 构建不等于容器实跑。

```sh
docker build -t cognate .
docker run --rm --entrypoint cognate cognate browse vercel-labs/skills
```

服务端和客户端原始 Logo 分别位于 `assets/branding/server.png` 和 `assets/branding/client.png`。面板使用 128px 衍生图 `assets/branding/server-web.png`，原始图保留供后续平台图标使用。

## 服务端与管理面板

需要 Node.js 26、npm、Go 1.27.3。构建时将 React 面板嵌入服务端二进制：

```sh
npm ci --prefix web
npm run build --prefix web
CGO_ENABLED=0 go build -o cognate-server ./cmd/cognate-server
```

为主密钥准备仅当前用户可访问的目录与 32 字节原始文件；保管好该文件，丢失后不能解密既有凭据：

```sh
install -d -m 700 "$HOME/.config/skillhub"
(umask 077; head -c 32 /dev/urandom > "$HOME/.config/skillhub/master.key")
./cognate-server --key-file "$HOME/.config/skillhub/master.key"
```

首次创建密钥只执行一次，已有服务不要覆盖密钥。浏览器打开 `http://127.0.0.1:8080`，创建本地账号后可在“仓库”页直接添加公开 GitHub 仓库，填写 `owner/repo` 或 HTTPS 地址及可选分支。服务端下载整仓归档，扫描到至少一个有效技能后保存内容和元数据；可浏览名称、描述、文件路径和内容 hash。留空分支或填写 `HEAD` 均使用默认分支；重复收录保持原快照，不表示已更新源。

“组合”页可跨仓库挑选技能、设置安装别名、编辑或删除 Pack，并创建可过期/撤销的分享。分享令牌只在创建时显示一次，隐藏后无法重新查看；请仅交给获授权的订阅者。分享跟随组合的最新版本，客户端仅通过服务端的 Connect 分发接口获取 manifest 和选中技能归档，不接触 GitHub token。Linux CLI 已接通订阅、显式同步及前台 `pack watch`；MyGo 提供订阅管理和明确启停的后台同步，不会自动应用 AI 配置。协议与当前边界见 `docs/PACK-DISTRIBUTION.md`、`docs/CLIENT-SUBSCRIPTIONS.md` 和 `docs/DESKTOP.md`。

也可先连接 GitHub、勾选 Star Lists，再从“仓库”页展开“从 GitHub Lists 收录仓库”。服务端已有 Lists 定时同步、水位线跳过、获授权的私有来源收录，以及独立的手动/周期来源刷新；私有内容由服务端当前 owner 凭据获取，不向客户端传递 GitHub 凭据。重复收录本身不更新旧快照，Lists 元数据同步也不代替来源刷新；已发布仓库的内容变化会被拒绝，须显式取消公开、更新后重新确认发布。权限要求、更新边界和合成闭环证据见 `docs/SOURCE-REFRESH.md`；真实 GitHub 账号权限验收仍待完成。管理员配置 `--github-client-id`（或 `SKILLHUB_GITHUB_CLIENT_ID`）后，可通过 GitHub 设备授权连接，无需粘贴 token；该 OAuth App 必须启用 device flow，但不会自动获得私有仓库权限。也支持手动 token。网页登录态仅保留在内存，刷新后重新登录。

单次归档上限 32 MiB、普通文件合计 64 MiB、10000 个归档条目和 512 个技能；单个 SKILL.md 上限 1 MiB，不跟随符号/硬链接。归档缓存位于数据目录的 `archives/`，默认对象预算 512 MiB，首次初始化可用 `--archive-cache-bytes` 指定，预算计入中断残留；每用户最多 1000 个收录记录。预算保存在私有 `.cache-budget`，共享目录的所有实例必须配置一致，不支持在线改预算，也不要在运行中编辑该策略文件。当前尚无自动回收或归档修复：失败事务/进程中断可能保留无引用内容，但不能无限突破缓存预算。不要直接删除缓存对象；详情浏览与重复收录会重新核验内容完整性，发现丢失或损坏时明确报错，不静默覆盖旧快照。

默认数据保存在 `~/.local/share/skillhub-server`，通过 `--data-dir` 更改；主密钥路径可通过 `COGNATE_MASTER_KEY_FILE` 设置。所有服务端 `COGNATE_*` 配置变量优先于对应 `SKILLHUB_*` 旧名，显式设置为空时不回退；旧名仍兼容。`/healthz` 提供存活检查。非 loopback 监听需要 `--tls-cert`/`--tls-key`，或在明确可信的私有网络中使用 `--allow-http`。TLS 私钥同样需要当前用户私有文件与私有目录。自定义域名或反向代理场景必须配置固定的 `--public-origin https://你的域名`；未配置时只接受 loopback Host。程序不信任代理身份头。

收到停止信号时取消请求并最多等待 10 秒，再关闭未结束的连接。慢请求导致停机超过期限时返回错误；这不表示数据库损坏。

Docker 默认在容器内监听 HTTP，部署时应仅映射到本机或可信私有网络，并在外部配置 TLS。密钥和数据卷必须由运行 UID 拥有且保持私有；默认 UID 为 10001。镜像内保留 `cognate` CLI，可通过 `--entrypoint cognate` 使用。

沿用前述私有密钥目录，以当前 UID 运行的本机开发示例（Actions 已通过双架构 OCI 构建；本机 Docker 运行仍待实际验证）：

```sh
install -d -m 700 "$HOME/.local/share/skillhub-server"
docker run --rm --user "$(id -u):$(id -g)" \
  -p 127.0.0.1:8080:8080 \
  -v "$HOME/.config/skillhub:/config:ro" \
  -v "$HOME/.local/share/skillhub-server:/data" \
  cognate --data-dir /data --listen 0.0.0.0:8080 --allow-http --key-file /config/master.key
```

前端开发可执行 `npm run dev --prefix web`，将服务端设为 `--public-origin http://localhost:5173`（如 Vite 使用不同端口或主机名，需与实际 origin 一致）。Go 和 TypeScript 协议分别通过 `buf generate` 和 `buf generate --template buf.gen.web.yaml` 更新。

## OpenID Connect

可选启用标准 OIDC discovery。服务端需要固定公开 origin、issuer 和 client ID；保密客户端另提供私有 secret 文件，公开客户端仅使用 PKCE：

```sh
./cognate-server --key-file "$HOME/.config/skillhub/master.key" \
  --public-origin https://skills.example.com --listen 127.0.0.1:8080 \
  --oidc-issuer https://identity.example.com --oidc-client-id skillhub \
  --oidc-client-secret-file "$HOME/.config/skillhub/oidc-secret"
```

在 provider 注册回调 `https://skills.example.com/auth/oidc/callback`，并配置 HTTPS 反向代理。也可使用 `SKILLHUB_OIDC_ISSUER`、`SKILLHUB_OIDC_CLIENT_ID`、`SKILLHUB_OIDC_CLIENT_SECRET_FILE`。远端 provider 必须使用 HTTPS；仅 loopback 开发服务允许 HTTP。

登录页可选择 OpenID Connect；已登录用户在“账号”页主动绑定身份。不同 issuer 或 subject 不自动合并，邮箱相同也不合并；已属于其他账号的身份不会转移。provider token 不存入客户端，URL 不携带本地会话 token；浏览器通过短期 HttpOnly cookie 一次性领取会话，刷新后仍需重新登录。详见 `docs/OIDC.md`。当前已用签名的本地测试 provider 验证完整网页流程，真实 provider 仍需部署验证。

需求、技术决策和后续里程碑见 `docs/REQUIREMENTS.md`。
