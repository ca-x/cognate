# npx skills 选择导入

## 支持范围

接入现有 `PreviewImportSelection` / `CommitImportSelection` / `GetImportCommitStatus`，不增加第二套迁移事务。服务端使用独立 moejs 模块；CLI 和浏览器不加载 JavaScript 插件。新版来源枚举为 `NPX_SKILLS_LOCK=3`，旧 cc-switch=1 / Magpie=2 保持不变。

格式依据固定的 `vercel-labs/skills` commit `92a9a34ac3bf88e695e468b90c7bf32f163d32c8` 中 `src/skill-lock.ts`，不是对未来版本的兼容承诺：

- 用户明确选择并授权上传 v3 global `.skill-lock.json`，不是项目级锁文件。不会自动寻找、打开或上传真实用户目录；不接受服务端文件路径。
- 文件最大 1 MiB，最多检查 1000 个来源条目。沿用现有单批 128 个技能候选、16 个仓库上限；超过明确拒绝，不截断、不默默漏掉技能。未知版本、重复 JSON 字段或不完整输入拒绝。
- 仅 `github` 创建候选。接受当前 `sourceType` 与需求文档中的 `source_type`，两者同时存在必须相同。其它 provider 只输出排除计数，不回传本机路径。
- 仓库、ref、GitHub HTTPS 来源 URL 和相对路径严格校验。来源 URL 不携带 userinfo、query 或 fragment，不用锁中的任意 URL 发起请求。
- `skillPath` 可为目录或 `SKILL.md`；统一转为 Cognate 扫描的逻辑目录，根目录为 `.`。没有路径不能靠名称自动绑定。
- Git tree SHA 不是 Cognate 内容哈希。树哈希、安装时间、agent 偏好等不作为已验证内容或设备安装记录；metadata / 其它 provider / 未知根字段分别提示排除。

## 操作

Web 仓库页的新版选择导入提供 npx skills 来源，仍保留旧 cc-switch/Magpie 的仅仓库兼容入口。界面使用步骤见 [Web 验证与边界](NPX-SKILLS-IMPORT-WEB.md)。

CLI 的只读预览示例（会话通过非 TTY 标准输入交接，不能放入参数或日志）：

```sh
cognate import select --server https://cognate.example \
  --source npx-skills --file /private/export/.skill-lock.json --token-stdin
```

默认不选择、不提交任何对象。预览不收录仓库、不创建配置，也不安装本机技能。需要实际导入时，沿用 `import select --apply`：固定预览原文 `--expected-sha256`，显式给出 1-based `--repositories` / `--skills` 与逐项 `--ack-notice`，再在控制终端确认。令牌输入不表示批准。npx 不产生 `--configurations` 候选。

技能必须显式绑定本账号已收录的仓库、ref、归档、逻辑目录、skill ID 和内容哈希。未收录时先选择收录仓库，再重新预览并绑定；不是自动依据锁文件宣称内容一致。确认“来源内容未比较”不等于验证源应用的本地修改。

提交使用五分钟、owner/origin/source/options 绑定的加密票据，以及同一 operation 的幂等回执。断网、取消或丢失提交回执后，只使用 `cognate import status` 查询，不自动重放提交；NotObserved 不证明尚在途的提交不会完成。原始文件/字节/票据不进入浏览器存储、URL、查询缓存或日志。文件可能包含敏感信息，只能上传到信任的服务端。

此升级把 npx parser 纳入编译后宿主源码身份，既有两种来源的 JS 模块摘要不变，但绑定宿主的适配器身份改变。升级前尚未提交的预览必须重新预览；不能带旧票据执行新语义。已提交操作的状态仍由原 operation 回执查询。

## 不包含

- 不迁移其它 provider、本地修改、凭据或客户端配置；不直接安装或自动应用。
- 不改用户真实锁文件，不包含兼容写回、canonical 目录通用导入或 MyGo 选择导入界面。
- 不代表完整 F6、十四 agent 配置、跨平台本机应用或正式版验收完成。

相关契约：[选择导入](IMPORT-SELECTION.md)、[协议](IMPORT-SELECTION-PROTOCOL.md)、[插件](PLUGIN-IMPORT.md)。逐阶段验证记录与独立审查结论以 [验证账本](VERIFICATION.md) 为准。
