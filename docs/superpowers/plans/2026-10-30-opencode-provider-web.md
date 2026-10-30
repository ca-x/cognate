# OpenCode Provider Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: use executing-plans for inline execution. No subagent spawn, stage, commit or push. All implementation waits for main's explicit source grant after independent design review; checkboxes below are future work, not results.

**Goal:** 在现有配置页实现OpenCode V1/V2专属结构化服务端资产管理，不开放native应用、不冒称R1排序完成。

**Architecture:** 沿用现有七RPC及React会话/请求门；新增OpenCode纯状态转换、分格式字段组件、递归值和slot面板。只扩展既有页面union/创建/编辑/复制入口及字典；测试通过真实TLS/SQLite/auth和构建后Web执行，不修改生产backend/模块/生成文件。

**Tech Stack:** 固定base的React/TanStack/Radix/shadcn/i18next/Buf/Connect，Go1.27.3 CGO0、Node26.12.0、现有agent-browser0.39.1；无新依赖或测试框架。

**Spec:** `docs/superpowers/specs/2026-10-30-opencode-provider-web.md`（随本包交付）。产品base `1b037cde2dbb8a7ec5ea654d5d3bbde017d7733c`。

## Global Constraints

- 设计阶段只写本spec/plan与私有报告。源码阶段也仅main批准的下列12路径；不得由计划自授权。
- 不改state/schema、configuration.proto/GoTS生成、import/native/App启用、moejs、package/module/lock/CI、旧三家字段文件或UI primitives。
- 仅synthetic HOME/XDG/TempDir/credentials及loopback；normal SumDB、只读缓存/offline模块；无CGO1/race/真实配置/keyring。
- 保留native深度12/2048nodes/64slots、map vs array和完整预算；没有rawJSON编辑/literal逃逸/自动初始化/SDK下载。
- R1 owner/order-revision后续backend独立片，当前无资产排序/应用按钮。

## Review Focus

1. optional父message关闭/展开后，false/0/empty wrapper与V1 thinking/cost及V2 reasoningField省略不丢不补默认（T2/T3 minimal/full fixture）。
2. 同slot复用及最后ref移除必须同CAS CLEAR；locale改变清新秘密但仍要求重新确认CLEAR，不能悄悄提交错误树（T4）。
3. map特殊key/prototype与V1 variants map、V2 variants/cost arrays顺序不互换，最大合法native深度不被proto wrapper折半（T2/T3）。
4. commit已发生但回执丢失/locale/owner改变：固定Unknown/block、不重试、不弹迟到成功、不把取消当回滚（T4/T5）。
5. HasPreservedExtras只读不等于typed extensions只读；COPY不得带任何slot值，列表QueryCache不得容纳Draft或新秘密（T2/T5）。

## 精确拟实施写集：12 source paths（9新增、3修改）

| # | 操作/路径 | 单一职责 |
|---|---|---|
|01|A `web/src/configuration-opencode-state.ts`|表单DTO、完整typed转换/clone、预算/ref/response守卫；不含秘密值|
|02|A `web/src/configuration-opencode-form.tsx`|总表单、格式固定、dirty/提交/清理与summary|
|03|A `web/src/configuration-opencode-v1-fields.tsx`|V1 provider/options/models/modalities/thinking/cost/variant map|
|04|A `web/src/configuration-opencode-v2-fields.tsx`|V2 settings/models/compatibility/capabilities/cost tiers/variant arrays|
|05|A `web/src/configuration-opencode-value-fields.tsx`|无literal的typed递归value及object/map行、深度/节点反馈|
|06|A `web/src/configuration-opencode-common-fields.tsx`|可选string/number/boolean、header→ref、ordered strings、limit/compaction/cost小组|
|07|A `web/src/configuration-opencode-secret-fields.tsx`|asset-local slot输入/确认与ref-only秘密生命周期hook|
|08|M `web/src/configuration-state.ts`|新增OpenCode discriminated union/require dispatch；复用而不重写请求门|
|09|M `web/src/configuration.tsx`|OpenCode create/edit/copy support、strict写回校验、四家范围文案；metadata列表不解密|
|10|M `web/src/locales/en.ts`|新增中英完整keys（中文以现i18n资源方式生成），不改其它功能文案|
|11|A `internal/server/httpapi/configuration_opencode_browser_test.go`|仅测试的TLS/SQLite/auth fixture、fault receipts、自检/七RPC，绝无生产fixture路由|
|12|A `scripts/verify-opencode-web.mjs`|Node内置assert/process/fs驱动agent-browser、进程终态/证据/退化校验，无新npm依赖|

另拟tracked设计docs为spec/plan2路径；不包含在12-source grant里。`PATHS.tsv`列同一12集合。需要第13源文件、primitive/API/shared hook或后端修复时先报具体缺口，不凑写集。

## 共同接口（在T2先定型，T3/T4消费）

- `supportsOpenCode(asset: AssetMetadata): boolean`：恰opencode/provider/schema1。
- `OpenCodeView = { mode: "editable"|"readonly"; asset: AssetMetadata; provider: OpenCodeProvider; slots: {slotId:string;present:boolean}[]; readiness: CredentialReadiness }`，provider为深clone。
- `OptionalInput<T> = {enabled:boolean;value:T}`；`MapRow<T> = {rowId:string;key:string;value:T}`；`OrderedRow<T> = {rowId:string;value:T}`。`ValueFields`恰为choose/null/boolean/number/array/object/reference tagged union，number是string，reference是slotID。choose仅用于未完成表单，不能编码。
- `OpenCodeFields = {name:string;providerId:string;format?:"v1"|"v2";v1:OpenCodeV1Fields;v2:OpenCodeV2Fields}`。两个具体Fields按spec§3各字段一一命名：所有optional用OptionalInput；map为MapRow数组，原生array为OrderedRow数组；message可选不折叠，required nested字段显式选择。不得靠宽泛any/Record<string,unknown>/JSON串替代具体接口。
- `openCodeFields(provider?:OpenCodeProvider,name?:string): OpenCodeFields`；`validateOpenCode(fields:OpenCodeFields): Record<string,string>`只返回路径→i18n key；`openCodeDraft(fields): ConfigurationDraft`仅opencodeProvider oneof；`requireOpenCodeView(view?:DraftView): OpenCodeView`完整metadata/format/slots/readiness/unknown守卫。
- `openCodeRequiredSlots(provider): string[]`及`openCodeNativeBudget(provider): {providerBytes,desiredBytes,nodes,depth,headers,variants,extensions}`：遍历全部分支，去重排序slot，不对array排序。native budget序列化镜像Go结构JSON，而不是wire wrapper JSON；desired按同一provider加按slot排序的`{slot,present:false}`计保守上界（false比true多1byte），不读取秘密长度。固定Go fixture提供边界预期。各层extensions也用OptionalInput<MapRow<ValueFields>[]>保留wrapper presence。
- `ConfigurationOpenCodeForm` props与已有GeminiForm一致，`original?:OpenCodeView`；V1/V2/Common/Value组件仅收本地fields/onChange/path/disabled，不能访问client、QueryCache、storage或密码。
- `useOpenCodeSecrets({original,requiredSlots,language})`在secret-fields内部提供slot动作/确认状态、`buildMutations():SecretChanges`、`clearReplacements():void`；密码与owned mutation对象只在ref中。`OpenCodeSecretFields`呈现该controller的slot面板，所有清理/dirty通知由父form协调，无隐藏全局状态。
- 页面沿现有 `ConfigurationDraft`/`SecretChanges` 的callback发既有RPC；严格写回比较用 `equals(OpenCodeProviderSchema,...)`，metadata和slot后状态单独校验，不以请求体缓存作为提交成功证据。

## T1 — 先建立真实fixture与浏览器RED入口

**Files:**11/12。用独立OpenCode测试fixture，不改或复用Gemini专属业务逻辑；可借鉴其能力目录/权限/计数/drain实现。`configuration.Store`外层test-only wrapper在真实物理Store成功返回后记录commit，只有受控fault模式改变回执，不替换CRUD。

- [ ] 新增 `TestOpenCodeBrowserHarnessGuardsAndDrain`：TempDir0700/ready0600、控制capability、错误origin/unknowncommand/body超限拒绝；hold-before/release/cancel/drain、drop-after-commit的actualcommits/active统计；跨owner拒绝；synthetic HOME/client原生文件tripwire不变。全部普通断言，无依赖浏览器的skip。
- [ ] 新增 `TestOpenCodeBrowserFixtureRPC`：real auth/Store/ConfigurationService/NewWithOptions、生成Connect client、TLS，两codec每家V1/V2最小及full vector；实际七RPC(List/Get/ReadDraft/Create/Update/Copy/Delete)、CAS、owner、slot/树保真与私密 marker不得在receipt/log/DB明文出现。server运行构建后的 `web.Handler()`，不是Vite假API。
- [ ] 新增显式入口 `TestOpenCodeAssetsBrowserFixture`，env `COGNATE_OPENCODE_BROWSER_DIR`指向私有目录；无env时仅该入口可明确helper skip，其它两个机械test必须实跑。fixture内部总deadline240s，Go进程timeout270s，不能无限等待浏览器。
- [ ] 控制面只loopback、独立port，私有capability header、no-store，拒Origin/Sec-Fetch及非allowlisted命令；`ready.json`0600仅含synthetic账号/fixture capability，public-ready仅URL。`GET /state`返回计数/ID/revision/treeEqual/slotbool/sideEffectsZero，无明文/长度/hash；`POST /command`只有hold-before/drop-after-commit/release/external-update/seed-preserved-extras/stop及所需assetID。真实domain/Store操作，不向主业务路由加后门。
- [ ] runner接口固定：`node scripts/verify-opencode-web.mjs --fixture-bin FILE --browser-bin FILE --chrome-bin FILE --suite preflight|forms|privacy|lifecycle|regression|all --output DIR`。runner自己创建0700 fixture目录并启动测试binary，读取ready文件；所有子进程有限timeout/finally join，失败也先释放gate/stop、关闭自有browser session；等待stopped receipt active0/closeOK/sideEffectsZero及fixture exit0。不能仅kill进程当正常cleanup PASS。
- [ ] runner以唯一自有agent-browser session、synthetic HOME/profile、`--executable-path`指向已安装Chrome、`--allowed-domains 127.0.0.1`和仅该合成TLS场景的`--ignore-https-errors`运行，拒外网；不使用现有浏览器/profile/state/用户keyring、不安装浏览器。用真实表单按钮/accessible label/role操作；不由evaluate直接调用controller/RPC代替UI保存。只能为了synthetic密码填入使用private stdin、不输出值；普通字段采用CLI fill/select。不得把credential/capability放argv、打印ready私有文件或在填写时截图。默认不保存HAR/body。
- [ ] `preflight`要求七RPC自检和readiness/drain全实跑；`forms`在旧UI必须因缺OpenCode入口/控件普通断言RED（不是找不到binary/npm未build等setup错误），保留日志。再进入T2/T3，不将作者backend绿当Web证据。

## T2 — 纯状态与完整双格式字段

**Files:**01/03/04/05/06；03–06暴露具名组件 `OpenCodeV1Fields`/`OpenCodeV2Fields`/`OpenCodeValueFields`/通用字段组件，具体Fields类型在01定义。DTO→wire builder把map行验证后装入null-prototype对象；禁止先转object而吞重复键。只导出当前需要的接口，不新增通用schema-renderer/runtime。

- [ ] 定义上述具体类型/完整转换，逐项覆盖spec§3表；node/byte/depth统计覆盖所有typed值分支。providerID/npm/endpoint等known字段按backend位置约束；metadata.name沿现既有trim；其余字符串/数字不trim/重写。
- [ ] `requireOpenCodeView`检查正revision/asset身份/name、oneof、typed树/required slots全集/唯一presence/readiness；known HasPreservedExtras仅mode readonly；literal/错误format/丢wrapper视为固定DataLoss，未知metadata保持FailedPrecondition，不落回Claude。
- [ ] 构造fieldset/label、presence开关、typed树编辑器与重复键校验；typestr null/boolfalse区别、timeout false sentinel、V1 limit.input无控件、V2 capabilities/cost/tier required字段按spec；array上下移，map无排序操作。rowID不传wire，折叠不丢字段。
- [ ] 与T3的真实 `forms` suite共同证明：V1/V2 optional全省略合法（特含thinking/cost/reasoningField），full table逐字段输入→保存→刷新→重开语义相等；empty wrapper/false/0/null、map__proto__/constructor、嵌套array次序、V1variant map/V2variant array、cost single/ordered tiers、原文数字与Unicode全部命中。不得只从fixture预填再按一次保存代表全表单可输入。
- [ ] boundary cases：native depth12通过/13在发送前固定拒绝；64models/128variants/64headers/256extensions/2048nodes/64unique refs及provider64KiB的边界由Go合法fixture作expected，N+1普通断言拒绝，预算不截断。可大向量经测试fixture提供初始tree再用真实UI修改边界项；完整field入力场景仍必须实际DOM编辑。V1apiKey/header/reasoningField只有slot picker，无literal字符串出口。

## T3 — 页面纵切、readonly与响应守卫

**Files:**02/08/09/10；引用T2类型，不更改旧3provider form/state源。

- [ ] 新建目标增加OpenCode，选择后固定；format同样显式固定；只在支持metadata的Edit/Copy时ReadDraft，列表不解密。扩union及copy guarded preload，不全局改旧分支/codec。
- [ ] 接入form和i18n所有visible labels/help/errors/confirmation，显示readonly提示及服务端资产边界；只有明确草稿操作增删models/variants，没有资产“设为当前/应用/排序”。metadata卡片与payload editor分离。
- [ ] SAVE守卫：新rev1/编辑同ID rev+1、agent/kind/schema/name、full typed provider相等；slots=新R且按旧presence+REPLACE/CLEAR正确计算、readiness吻合，readonly写回拒绝。发送前按对应Create/Update request schema的`toJsonString`默认序列化检查UTF8≤96KiB（包含owned mutations），短暂文本finally去引用、不打印/缓存；保持base JSON transport，fixture记录真实Content-Type。native64KiB与wire96KiB为两个独立门。status不接收服务器低层error文本。
- [ ] COPY先验证源id/revision/typed branch/非extras；副本新id/rev1/name、完整provider相等、全部slotfalse，R空Complete否则NeedsCredential。DELETE保留原metadataCAS和确认，不因readonly拒绝metadata删除。
- [ ] `forms`恢复GREEN并存真实RPC统计/DB身份/revision及无明文receipt；`regression`验证同providerID可两asset共存、unknownagent只metadata、typed extensions可编辑但storedextras只读/copydisabled/CASdelete可用；四provider新建/edit/copy/delete旧入口仍可达。未知树不偷偷渲染rawJSON。

## T4 — 秘密、dirty、语言与在途生命周期

**Files:**02/07/09/10，复用原request hook，不改它的取消/unknown语义。

- [ ] 实现引用位置列表/slot面板，new秘密用password DOM/ref；create无KEEP，update省略/KEEP/CLEAR/REPLACE只有一条每slot，所有wrapper都不收秘密字符串。lastref移除时同时确认CLEAR并保留R外pending CLEAR，sharedref剩余时不清；不自动重编号。
- [ ] CLEAR逐slot确认（取消保持、确认仅记录本CAS，未SAVE不动DB）；移ref同确认，readonly全部变更disabled。locale变化清replacement/owned发送对象/CLEAR确认但保非秘密草稿；未确认移除ref导致保存阻断直到重新确认，不误发孤儿slot。
- [ ] dirty取消/确认、ESC/导航/close、owner/logout/origin/lang/unmount按spec清理；异步finally也释放新秘密，无late-success。写过但结果未知不能自动重试；Aborted明确保非秘密草稿并要求显式重读；不把cancel视为rollback。
- [ ] `privacy` suite：使用Go生成synthetic sentinel检测表单预览、所有UI错误/通知、List/Get/ReadDraft、URL/storage/console和persisted DB/WAL无明文；server receipt确认KEEP后same secret（仅bool），REPLACE生效、CLEAR缺值、copy全缺。秘密值不出浏览器测试输出。不要记录明文截图/HAR；本地新输入值不能借测试hook入日志。
- [ ] `lifecycle` suite：hold-before时切语言/关闭/换owner及late-response，drop-after-commit时actualcommits=1/UIunknown/blocked/连续SAVE零新增写，恢复后明确重读无自动重放；关闭fixture后active0/worker join/Store Close成功。当前已有request gate若不能满足本新增分支，先报具体反例申请scope，不暗改其它路径。

## T5 — 实际浏览器门禁、退化控制与冻结

**Files:**11/12完善，相关01–10仅按既定spec修正。不是再造原backend审查。

- [ ] 主业务browser run必须用正常构建、无observer的shipping代码；每suite记录匹配的fixture/原生文件tripwire/RPC counters/DB receipts。1440×900和375×812，中英、light/dark、reduced-motion、200%zoom及纯键盘：焦点圈闭/错误摘要链接/嵌套CLEAR取消和关闭trigger恢复；无关键横向溢出/颜色唯一状态。截图只取无新秘密状态。
- [ ] QueryCache核验另用**私有临时copy的 `configuration.tsx` test-only observer overlay**，只暴露缓存shape和sentinel-free bool给私有runner，不返回内容/值，不进shipping。核metadata投影无Draft/view/slot/new秘密，生命周期后旧owner数据不可见。observer diff/hash与独立run单列，不能用带observer的pass替代无observer主业务run；申请授权时连同此test-only接缝明确列出。
- [ ] compiled负控至少三项，在私有copy仅退化已授文件：①builder吞掉optional false或重排V2variant，保存后与用户输入的exact typed读回断言RED；②使用R非空的真实copy，test-only fixture提交后将回执slots全部置present=true、readiness一致置Complete（结构合法、仅违反Copy无key合同），正源须固定DataLoss而不能success；前端忽略copy slot守卫的退化须被该UI断言RED，DB真实副本仍全false；③禁用language清replacement，切换后password为空/重新输入才可提交的断言RED。每项normal typecheck/build0后跑对应真实browser普通断言exit1，不接受compile0后因server没启动/超时/selector过时失败。第二项是响应完整性负控，不伪造domain复制秘密的产品缺陷。
- [ ] 原source恢复、正常build后all suite重新GREEN；全Go/vet/mod/old3HTTP/golden回归，保显式fixturehelper skip与实际browserrun1pass1分开。所有handles终态，freeze before/after源身份/exact12 manifest/tar/report/compiledcontrols及真实限制；main独立组合与非作者实现审后才可声称UI交付。不stage/commit/push。

## 完整执行命令（源码授权后，当前未执行）

在隔离归档目录 `$S`（本次对应source），用已有toolchain/cache；不得复制真实HOME。`$E`必须是新证据目录，下列run只允许显式环境变量，经env-i对npm/Go/Node/runner执行。npm cache不足不能disable认证/乱改lock；按正常认证下载获准或报告前置缺失。

```bash
S=/tmp/your-author-root/source
E=$(mktemp -d /tmp/cognate-opencode-web-gates.XXXXXXXX)
mkdir -p "$E"/{home,tmp,config,data,cache,state}
TOOLPATH=/home/czyt/.local/share/mise/installs/go/1.27.3/bin:/home/czyt/.local/share/mise/installs/node/26.12.0/bin:/usr/bin:/bin
run() {
  env -i PATH="$TOOLPATH" HOME="$E/home" TMPDIR="$E/tmp" TZ=UTC LANG=C.UTF-8 \
    XDG_CONFIG_HOME="$E/config" XDG_DATA_HOME="$E/data" XDG_CACHE_HOME="$E/cache" XDG_STATE_HOME="$E/state" \
    GOENV=off GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0 GOMAXPROCS=4 \
    GOCACHE=/home/czyt/.cache/go-build GOMODCACHE=/home/czyt/go/pkg/mod GOPROXY=off GOSUMDB=sum.golang.org \
    npm_config_cache=/home/czyt/.npm npm_config_logs_dir="$E/npm-logs" npm_config_update_notifier=false "$@"
}
cd "$S"
GUIDELINES_VERSION=$(cat /home/czyt/.codex/skills/use-modern-go/scripts/VERSION)
mkdir -p "$E/cache/go-modern-guidelines/$GUIDELINES_VERSION"
cp "/home/czyt/.cache/go-modern-guidelines/$GUIDELINES_VERSION/go-modern-guidelines" "$E/cache/go-modern-guidelines/$GUIDELINES_VERSION/"
run sh /home/czyt/.codex/skills/use-modern-go/scripts/run-tool.sh list --go-version 1.27
(cd web && run timeout 240s npm ci --offline --no-audit --no-fund)
(cd web && run timeout 120s npm run typecheck && run timeout 180s npm run build)
run timeout 120s go test -mod=readonly ./internal/server/httpapi -run '^TestOpenCodeBrowser(HarnessGuardsAndDrain|FixtureRPC)$' -count=3 -json > "$E/fixture.jsonl"
run timeout 120s go test -mod=readonly -c -o "$E/httpapi.test" ./internal/server/httpapi
BROWSER_BIN=/home/czyt/.local/share/mise/installs/npm-agent-browser/0.39.1/node_modules/agent-browser/bin/agent-browser-linux-x64
CHROME_BIN=/opt/google/chrome/chrome
test -x "$BROWSER_BIN" && test -x "$CHROME_BIN"
run timeout 30s "$BROWSER_BIN" skills get core > "$E/browser-core.md"
run timeout 1800s node scripts/verify-opencode-web.mjs --fixture-bin "$E/httpapi.test" --browser-bin "$BROWSER_BIN" --chrome-bin "$CHROME_BIN" --suite all --output "$E/browser"
run timeout 900s go test -mod=readonly ./... -count=1 -json > "$E/fullgo.jsonl"
run timeout 300s go vet -mod=readonly ./...
run timeout 120s go mod verify
```

每条命令单存raw stdout/stderr/exit及实际工具版本，不靠shell最后一条exit冒全部绿；runner每次RPC/CLI/等待≤30s，fixture每实例240s，总all≤1800s，慢于期限即失败并join。`--suite forms/privacy/lifecycle/regression`与相同参数可单独复现相应RED→GREEN，每suite单独fixture，all逐一消费且验全部case出现/无skip。

期望：npm三个门/Go/vet/mod terminal0；自检count3至少两个top-level各3PASS零skip；显式fixture入口在runner实际run1/pass1/no-skip，而默认fullGo只允许其未开启时的说明性helper skip。无其它产品skip豁免。browser all要求具名cases逐项pass及正常cleanup receipt，非“进程没崩”或截图有界面。

## 设计自检、未决与交接

spec§1/2→T3/T5，§3/4→T2/T3，§5→T4/T5，§6→T2/T3/T5，§7→T1/T5；五项Review Focus均已落test步骤。当前没有未定字段shape/format/slot数/错误恢复策略。尚待主批准：**此独立Web合同、exact12源码集合及所列私有observer接缝**；R1另backend未完成；浏览器可执行程序/缓存Chromium是实施环境前置，不授权安装全局包。初次类型/构建若显示12路径外真实必要接缝，停在明确反例申请扩集，不静默增加。下一阶段必须非作者设计审通过和源码grant，本文不授生产实现。
