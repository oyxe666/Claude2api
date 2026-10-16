# 开发计划 4：账号启用/停用开关（接手先看这个）

写于 2026-10-16。状态：**方案已定，正在动手**。上一份计划见 `docs/PLAN-3.md`（均衡选号改造，代码已改完、测试全绿，**尚未提交、未推送、未部署**）。

## 0. 规则（和 PLAN-3.md 一致）

- 用户是非开发者、初学者，说话要短，回复用中文。
- **不要自动 commit / push / 部署**，每次先问用户；推送前先给用户看改了哪些文件。
- 不要在对话、命令、文件里出现密钥、令牌、密码、sessionKey、代理账号密码。
- 服务器 `config.yaml` 和 `data/` 是线上数据，不能覆盖或删除。部署前先备份。
- 提交时不要带 `.agents/`。提交信息结尾加：`Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`
- 不得新增任何对 Claude 的请求。
- **实时更新本文件**：每完成一步，立刻把第 5 节的 `[ ]` 改成 `[x]`，并补一行“日期、做了什么、测试结果、是否提交/部署、遗留问题”。
- 编译测试：Git Bash 里先 `export PATH="/c/Program Files/Go/bin:$PATH"`；Go 测试用 `go test ./... -skip SDK`（SDK 集成测试需要本地 8787 服务）；前端测试 `node web/test/*.test.js`。
- 换行符：仓库是 LF，本机 `core.autocrlf=true`。提交前 `git diff --numstat`，若出现“整个文件都改了”，先停下排查。

## 1. 需求

用户只有一个人用。希望能**选择哪些账号参与调用**，没被选中的账号“待着”：不接请求，但不删除、不变失效、额度信息照常保留。

调研结论（开源同类项目）：sub2api 用“分组 + API Key 绑定分组”（多人用，不要）和“临时不可调度”（自动冷却）；claude-relay-service 有自动冷却和“重置状态”；没从公开页面确认到手动启停开关的字段名。我们取最简方案：**每个账号一个“参与调度”开关**。

用户已决定：**所有账号都被停用时，直接报错“没有可用账号”**（不要忽略开关）。

## 2. 设计

### 2.1 数据
- `repository.Account` 新增字段 `Disabled bool`，json `disabled`，gorm `not null;default:false`。用“停用”而不是“启用”，这样老库 AutoMigrate 补列后老账号默认都是参与调度，不需要数据迁移。
- `UpsertAccount` 的 `DoUpdates` **不包含** `disabled`：重新导入同邮箱不会把停用状态冲掉。
- 新函数 `repository.SetAccountDisabled(email string, disabled bool) bool`：只更新 `disabled` 列（用 `db.Model(&Account{}).Where("email = ?", email).Update("disabled", disabled)`），账号不存在返回 false。注意 `UpdateAccount` 用 `Save`，会写全部列，别用它改这个字段以免和额度快照等并发写冲突。
- 提供批量版 `SetAllAccountsDisabled(disabled bool) int`（可选，用于“全部停用/全部启用”）。

### 2.2 选号
- `dispatcher.go` 的 `pickAPIAccount`：在 `AccountUsable` 之外再排除 `Disabled`。**不要改 `AccountUsable`**（它还被号池页、额度批量目标、镜像站选账号等用到，改了会让停用号的额度检查也停掉）。新增 `AccountSchedulable(a) bool = AccountUsable(a) && !a.Disabled`，只在选号处用。
- 全部被停用（候选为空）时 `pickAPIAccount` 返回 nil，`Complete` 已有“号池中没有可用账号”错误，错误文案改成能区分：若存在可用但全被停用的账号，写“所有账号都已停用，请在账号页启用至少一个”。
- `skipExhausted` / `balanceByFiveHour` 逻辑不变（停用号已在上游被过滤，不参与求最大值）。
- `lastPicked` 清理：现在按 `active`（可用）清理；停用号保留 lastPicked 没问题，但为简单起见，`active` 仍以 `AccountUsable` 为准（停用号只是暂时不被选，重新启用后轮流顺序应延续）。`apiClients` 同理保持，不要因为停用就销毁客户端。

### 2.3 其它入口（需核实，不确定的先读代码再定）
- 号池页 / 镜像站 `PoolList`、`PoolSelect`（internal/handler/pool.go）：是用户手动选账号进入 claude.ai 镜像的，**停用不影响**，保持现状（用户可能想手动进某个停用号看看）。
- 额度批量检查、定时检查（`QuotaBatchTargets`、`usage.go` 约 600 行用 `AccountUsable`）：**停用号照常检查**（用户确认只是不被选去对话）。
- 乱葬岗/存活统计：不受影响。

### 2.4 接口
- `POST /api/accounts/disable`，body `{"email": "...", "disabled": true|false}`，AdminAuth；账号不存在 404；成功返回 `{"account": PublicAccountView}`，并触发实时推送（沿用 `repository.OnQuotaChanged` 回调不合适，它只在额度变化时调；需要在 handler 里手动调用 `service.PublishQuotaChanged(email)`，先读 `internal/service/events.go` 确认可用的发布函数名）。
- `POST /api/accounts/disable-all`，body `{"disabled": true|false}`，批量。
- 路由挂在 `internal/router/router.go`，在 `admin.POST("/accounts/refresh", ...)` 附近。
- `PublicAccount` 增加 `Disabled bool json:"disabled"`，`PublicAccountView` 填上。

### 2.5 前端
- `web/quota.js` `buildCard`：卡片加开关按钮（`data-act="toggle-disabled"`，文案“停用”/“启用”），停用的卡片加类 `is-disabled` 并显示徽标“已停用”。`accountView` 增加 `disabled` 字段。保持纯 DOM（createElement + textContent），不要拼 innerHTML。
- `web/app.js`：卡片点击事件里（约 1664 行 `button[data-act]` 的 switch）加 `case "toggle-disabled"`，调接口后用 `mergeAccount` + 重绘；表格视图（约 301-304 行）同样加一列/按钮。
- 顶部加“全部停用 / 全部启用”按钮（`web/index.html` 账号页工具栏），点“全部停用”要二次确认（提示“停用后所有请求会报错”）。
- `web/style.css`：`.quota-card.is-disabled` 灰化（降低不透明度、虚线边框），沿用现有配色变量；保持 `prefers-reduced-motion` 不增加动画。
- 概览里“可用账号 X/Y”的口径：把停用号从“可用”里排除（`summarize`），否则会误导。先读 `summarize`，停用号 `usable` 保持 true 但不算 `available`，另计 `disabled` 数。

### 2.6 测试
- repository：`SetAccountDisabled`（开/关/账号不存在）、老库补列后默认 false（参考 `TestAutoMigrateKeepsOldData`）、`UpsertAccount` 重复导入不重置 disabled。
- service：`pickAPIAccount` 跳过停用号；全部停用返回 nil；停用号的额度仍被 `QuotaBatchTargets` 包含；`PublicAccountView` 带 disabled。
- handler/router：接口无凭据 401；账号不存在 404；成功后 `PublicAccountView.disabled` 正确。
- 前端 `web/test/quota.test.js`：停用卡片有 `is-disabled` 与徽标、按钮文案随状态变化、`summarize` 不把停用号算可用、恶意邮箱不进 innerHTML。

## 3. 不改的东西
- 不改 `AccountUsable`；不改检查/刷新/定时检查的行为与间隔；不新增对 Claude 的请求。
- 不做分组、不做按 API Key 绑定账号（多人用方案，用户明确不要）。
- 不做自动冷却（之后用户要再评估）。

## 4. 风险与注意
- 全部停用后所有 API 请求都会报错，这是用户选择的行为（方案 A）；前端“全部停用”需要二次确认。
- 新增 DB 列依赖 `AutoMigrate`，部署时旧数据不受影响；回滚到旧版本代码时多出的列会被忽略。
- 改 `app.js` 文件很大，用精确替换，别整体重写。

## 5. 进度记录（实时更新）

- [ ] 0 先把 PLAN-3 的改动处理掉？（用户未决定是否先提交；本计划的改动会和 PLAN-3 的改动一起留在工作区，提交时要告知用户两个功能混在一起，或先问用户是否先提交 PLAN-3）
- [x] 1 repository：`Disabled` 字段、`SetAccountDisabled`、`SetAllAccountsDisabled`（2026-10-16 完成：internal/repository/account.go；`UpsertAccount` 的 DoUpdates 本来就不含 disabled，不会冲掉；尚未编译、未测试）
- [x] 2 service（2026-10-16 完成：account.go 新增 `AccountSchedulable`、`PublicAccount.Disabled`；events.go 的 `EventAccount` 也带 disabled；dispatcher.go 的 `pickAPIAccount` 过滤停用号（active 仍按可用算，保留 lastPicked/apiClients），新增 `hasOnlyDisabledAccounts` 与“所有账号都已停用”错误文案；尚未编译、未测试；注意 `AccountSchedulable` 目前未被调用，可在收尾时删掉或保留）
- [x] 3 handler + 路由（2026-10-16 完成：internal/handler/admin.go 新增 AdminSetAccountDisabled（400 缺邮箱 / 404 账号不存在 / 成功后 PublishQuotaChanged 推送）与 AdminSetAllAccountsDisabled；internal/router/router.go 挂 POST /api/accounts/disable 与 /api/accounts/disable-all（AdminAuth）；service 里补上了 PublicAccountView 的 Disabled 与“所有账号都已停用”错误文案；go build、go vet 通过；测试尚未写；前端待做）
- [x] 4 前端（2026-10-16 完成：quota.js 增加 disabled 字段、summarize 不把停用号算可用并计 disabled、卡片 is-disabled 样式/“已停用”徽标/“停用·启用”按钮；app.js 增加 toggleDisabled、setAllDisabled 与事件绑定；index.html 加“全部停用/全部启用”按钮；style.css 加 is-disabled 样式；表格视图未加停用按钮（遗留）；尚未跑测试，live.js 的 mergeEventAccount 是否合并 disabled 字段待核实）
- [x] 5 测试（2026-10-16 完成：repository `TestSetAccountDisabled`（默认启用、切换、不存在返回 false、重复导入与写额度不冲掉停用、批量启用）；service `TestPickAPIAccountSkipsDisabled`（停用号不被选、仍在额度批量目标里、全部停用返回 nil 并识别、公开视图带 disabled、启用后恢复）；handler 新文件 `disable_test.go`（400 缺邮箱 / 404 不存在 / 成功 / 不泄露 sessionKey / 批量启用）；router `TestDisableRoutesRequireAdminAuth`（无凭据 401）；前端 quota.test.js 新增停用卡片与 summarize 两个测试，并修正按钮列表断言）
- [x] 6 `go build`、`go vet`、`go test ./... -skip SDK`、node 测试全绿（2026-10-16：Go 全部包 ok；node 4 个文件全过：grave 18、live 16、prefs 10、quota 40；未提交、未部署；浏览器点击未实测；遗留：表格视图没有停用按钮、概览未单独显示“已停用 N 个”）
- [x] 6b 补表格视图与概览（2026-10-16 完成：web/app.js 表格行新增“停用/启用”按钮（act-toggle）和“已停用”徽标，并绑定点击；web/quota.js 概览“可用账号”后追加“（已停用 N）”；node --check 通过，node 测试 4 个文件全过，go build、go test 全绿；未提交、未部署；浏览器点击未实测）
- [ ] 7 给用户看改动，等确认后再提交、推送（用户已决定：均衡选号与停用开关分两次提交；先提交 PLAN-3 相关文件，再提交 PLAN-4 相关文件；推送前仍需用户确认）
- [ ] 8 （用户决定）部署：先备份服务器

（每项完成后在此补：日期、提交哈希、是否已部署、遗留问题。）
