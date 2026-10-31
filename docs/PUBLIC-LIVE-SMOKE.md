# Linux 公开链实测

2026-11-01：限定本机 Linux/amd64 的真实公开 GitHub → 服务端 → 匿名 API → CLI 安装链。
不是完整 F8、UI、私有仓库、OIDC、跨平台或正式版放行。

## 被测版本与隔离

被测 source tree 为 `1c3edd309c581dff2b9ad4a585b52a8205117db6`；与 `2efa940` 和
`9bd0c3d` 仅文档不同。独立代表重新 archive 并逐 tracked blob 核对，重建两个 Linux
产品二进制，SHA256 与实际运行产物相同；Go1.27.3、CGO_ENABLED=0、linux/amd64。

- CLI SHA256：`d1b436de7f734028b83d40531f3abc02499d7f7f3f29fdb7e9510509b71bdbe5`。
- server SHA256：`5847b291aa2f40cfefbe705a7ce92710ad1759fd7792e17c826bc68b2a634b5a`。

每次新建随机私有 fixture，账号/密码/key/token 均为本次合成；不读取真实 HOME、用户密钥
或私有 GitHub 凭据。server 监听 `127.0.0.1:0`，由其 PID 的 socket inode 与 LISTEN 表
交集取得端口，每个 RPC 前复核，不连接既有实例。CLI 使用另一空 HOME。
独立代表在私有 PID/IPC/UTS/user 沙箱、清环境与只读依赖中复跑；共享网络只为公开 HTTPS。
未声称域名级防火墙或完整抓包。password/token 不写入 argv、环境、日志或报告。

## 实际八步

1. 启动实际 server，程序内生成首次 master key，健康检查 200。
2. 注册、登录两个全新站内 owner；不是登录两个真实 GitHub 账号。
3. 无上游凭据采集 `samber/cc-skills-golang` 的真实整仓归档，本次观察 46 个 skills。
4. 默认匿名列表为空；另一 owner 读取仓库 404，自己的分类为空。
5. owner 建分类并关联一个 skill，经完整内容确认显式公开；匿名分类列表仅该条，详情可读。
6. 真 CLI 初始化本机 key，无 token 执行 `install-public`；canonical 下仅一个目录，SKILL.md 非空。
7. 取消公开后匿名详情 404、列表为空、CLI 再装退出 1；此前本地 SKILL.md 字节不变，不远程删除。
8. 两个 Logout RPC 成功，server 正常 SIGTERM/Wait 退出 0，删除本次自建 fixture。

独立代表的真实链 exit0 / 八步 PASS；另以仅返回安装成功、实际不安装的 fakeCLI 原断言
复跑，exit1，准确停在安装文件检查，不用网络失败或 CLI 非预期退出冒充行为 RED。
主线程的 `search golang --limit 5` 另有真实 skills.sh 五条结果、零本地状态写入；独立
`browse samber/cc-skills-golang` 观察 46 条扫描结果且临时 clone 清空，不含安装授权。

## 收据与边界

独审报告：`/tmp/cognate-public-smoke-independent-uCJ2Jc/REPORT.md`，SHA256
`b8db19f6d17f29176c411a533f40541f26e400b2f91830f0593da306ac89792b`，限定 ACCEPT。
主烟测目录 `/tmp/cognate-live-public-install-OdqDE3`；公开搜索/浏览目录
`/tmp/cognate-public-search-smoke-OfrxJr`。这些是本机历史收据位置，不假称其它机器已有这些文件。
源码/协议及步骤随仓库保留，换机器须重新构建、建立临时实例并按同范围复验，不能把本文当新运行。

原 Node socket 型输出捕获导致 `client-key init` 退出 1，符合已有
`CLIENT-SUBSCRIPTIONS.md` 的 FIFO/TTY 合同。改为 Go os/exec 的有界 pipe 捕获后通过；
没有放宽产品 I/O 约束或修改产品代码，原失败保留，不列为本轮产品缺陷。

仅安装一个 skill，不是 46 个全装；安装断言不是独立重算整棵目录 hash 或逐文件二进制
核对。上游默认 HEAD 可变且本次未记录其 commit，46 只代表该次观察。Logout 未额外重放
旧 token；也没有浏览器/动画/键盘证据。更广内容完整性、权限、两 Pack 去重及各平台运行
仍由对应门禁验证，不能从本次 smoke 外推。
