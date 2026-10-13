# 开发计划（接手先看这个）

写于 2026-10-13。状态：**方案已定，尚未动手**。每完成一项，把下面对应的 `[ ]` 改成 `[x]`，并在“进度记录”里补一行。

## 0. 先看这几条

- 用户是非开发者、初学者，说话要短，回复用中文。
- **不要自动 `git commit` / `git push`**，每次提交、推送、部署服务器前先问用户。
- 不要在对话、命令、文件里出现密钥、令牌、密码、sessionKey。
- 服务器 `config.yaml` 和 `data/` 是线上数据，不能覆盖或删除。部署前先备份。
- 外层工作区的 `accounts.txt`、`开发说明.md` 等不属于本仓库，不要放进来。详见外层 `开发说明.md`（怎么编译、测试、连服务器、部署）。
- 每做完一项：本地 `go build ./...`、`go vet ./...`、`go test ./... -count=1 -skip SDKToolCall`，前端有逻辑则跑 `node web/test/*.test.js`；然后向用户汇报，等他同意再提交。

## 1. 总目标（用户提出的 7 件事）

1. 整理“刷新”按钮，替换暴力的一键刷新（温和排队）
2. 额度实时刷新（不用 F5）
3. 乱葬岗（记录封号/失效/手动删除的账号）
4. 额度均衡选号（5 小时窗口剩余明显偏少的账号先不分配）
5. 界面美化（圆角卡片、悬浮效果）
6. 记住每页条数（账号页、日志页，F5 后不回到 10）
7. 乱葬岗“手动删除”也要记录（用户确认：他手动删的通常都是阵亡的）

## 2. 做的顺序

A 刷新按钮整理 → B 额度实时推送 → C 额度均衡选号 → D 乱葬岗 → E 记住每页条数 → F 美化

（美化放最后，免得页面结构变动后反复改。）

## 3. 现有代码要点（已核实）

- 账号表 `accounts`：`internal/repository/account.go`，字段 Email / OrgUUID / Cookies(含 sessionKey，绝不外泄) / Status(active、expired、error) / Quota(JSON) / CreatedAt / UpdatedAt。
- 迁移：`internal/repository/db.go` 的 `AutoMigrate(&Account{}, &APIKey{}, &APILog{}, &Grave{})`，新增表在这里加。`repository.MarkAccountExpired` 是统一的「置失效」入口（先记乱葬岗再置 expired）。
- 账号失效处理：`internal/service/account.go` 的 `RefreshAccount`。会话失效时，若配置 `RemoveInvalidAccount` 为真则直接 `DeleteAccount`，否则把 Status 置为 `expired`。每 6 小时（`StatusCheckIntervalSeconds`，最小 21600）巡检一次。
- 删除入口：`repository.DeleteAccount`、`repository.DeleteAccountsByStatus`（handler：`AdminDeleteAccount`、`AdminDeleteExpired`，在 `internal/handler/admin.go`）。另有 `internal/service/dispatcher.go` 里换号重试时也会删失效账号（约第 195 行）。
- 额度落库：对话时 `dispatcher.go` 里 `client.OnQuota` 调 `repository.UpdateAccountQuota`；额度刷新逻辑在 `internal/service/usage.go`、`quota.go`；批量额度刷新 `AdminRefreshAllQuota` 已是 SSE + 逐个排队。
- 选号：`dispatcher.go` 的 `pickAPIAccount`，轮询 `apiIndex`，用 `skipExhausted` 跳过已耗尽账号（全部耗尽则原样放行）。
- 路由：`internal/router/router.go`，管理接口在 `/api`（`middleware.AdminAuth()`）。
- 前端：`web/index.html`（页面用 `data-page` 切换，`switchPage` 在 `web/app.js` 约 892 行）、`web/app.js`、`web/quota.js`、`web/style.css`。分页变量 `pageSize`（账号）、`logsPageSize`（日志）。localStorage 已有先例：`VIEW_KEY`。
- 现状问题：账号页“刷新”只重读列表；“一键刷新”用 `Promise.allSettled` **同时**给所有账号发请求，之后还会再跑一遍额度刷新（两轮请求），伤号。

## 4. 各项方案

### A. 刷新按钮整理

账号页只保留：
- **刷新列表**：只重读本地数据，**不会给 Claude 发任何请求**。按钮旁/悬浮提示必须明确写这句。
- **检查账号**（替换“一键刷新”）：逐个排队，每个账号之间随机间隔 3~8 秒；每个账号只发一轮请求（状态检查 + 额度合并成一次，不再先状态再额度两轮）；进度条照常；可随时点“停止”。提示写明：“会逐个向 Claude 发请求，约需 N 分钟”。
- 卡片上的单个账号刷新保留，提示写“会向 Claude 发 1 次请求”。
- 号池页、日志页的“刷新”只改文案，注明是否发请求（它们都只读本地数据）。

实现：后端新增 SSE 接口 `POST /api/accounts/check-all`（可参考 `AdminRefreshAllQuota` 的写法，复用 `TryBeginQuotaBatch` 防并发），支持客户端断开即停止（用 `c.Request.Context()`）。前端删掉 `refreshAllAccounts` 的并发逻辑。间隔做成可配置常量，测试里设为 0。

### B. 额度实时推送

- 新增 `GET /api/events`（SSE，AdminAuth 保护），服务端内存里维护订阅者列表（加锁）。
- `UpdateAccountQuota` 成功后广播事件 `{email, account: PublicAccountView}`（只含公开字段，不含 sessionKey）。注意避免 repository 反向依赖 service：用一个回调变量（如 `repository.OnQuotaChanged`）在启动时注入。
- 前端用 `EventSource` 订阅，收到后 `mergeAccount` + 局部重绘。断线自动重连。页面隐藏时可以不处理，回到前台时重读列表一次。
- 反向代理 nginx 要关缓冲：响应头 `X-Accel-Buffering: no`（批量额度接口已这样做）。服务器 nginx 若有超时设置，需确认 `proxy_read_timeout`；服务端每 25 秒发一条注释心跳防断。
- 不增加任何对 Claude 的请求。

### C. 额度均衡选号（用户新提）

目标：5 小时窗口剩余明显少于其他账号的，暂不分配，以此均衡。

设计：
- 在 `pickAPIAccount` 里，`skipExhausted` 之后再加一层 `balanceByFiveHour`。
- 对每个候选账号算 5 小时剩余 `remain = 1 - utilization`（utilization 是 0~1 小数；快照缺失或已过期的账号视为未知）。
- 只比较有**新鲜**快照（`UpdatedAt` 距今 < 10 分钟）的账号；新鲜快照里 `ResetsAt` 已过 → 窗口已重置，剩余按 100% 算；快照缺失/过期/没有用量 → 未知，不参与比较也不跳过。差距「恰好等于阈值」不跳过，只有「低超过」才跳。配置页输入留空/非法保持原值，填 0 才关闭；`config.example.yaml` 不改，默认值（0.30）在代码里。
- 取候选里剩余最大值 `maxRemain`；剩余比 `maxRemain` 低超过阈值 `balanceGap`（默认 0.30，即低 30 个百分点以上）的账号，本轮跳过。
- 未知快照的账号**不跳过**（宁可让它被用，也不要误杀）。
- 保护：过滤后若没有账号可选，原样返回全部候选（和 `skipExhausted` 一致）；至少保留 1 个。
- 阈值做成配置项（`quota_balance_gap`，0 表示关闭），在设置页可改；默认开启 0.30。需要在 `internal/config/config.go` 加字段和更新逻辑，并在 `web/index.html` 的系统管理页加输入框（沿用 `CONFIG_FIELDS` 机制）。
- 额度只在对话后更新，快照可能略旧，所以阈值取得宽松，不追求精确均衡。
- 单元测试：参考 `internal/service/dispatcher_pick_test.go`，覆盖：差距超阈值被跳过、未知快照不跳过、全部过滤时放行、窗口已重置按满额算、关闭开关。
- 已知取舍：均衡会让“剩余多”的账号先被用，剩余少的账号恢复到接近后再进入轮询，总体耗尽时间更接近。

### D. 乱葬岗

新表 `graveyard`（模型 `Grave`，加进 `AutoMigrate`）：
- ID、Email（唯一，一个邮箱只记一次）、JoinedAt（账号 CreatedAt）、DiedAt、LifespanSec、Reason（`session_invalid` 会话失效 / `auto_removed` 自动清理 / `manual_delete` 手动删除）、ChatCount（死前累计对话次数，可从 `api_logs` 按账号统计）、LastQuota（死前最后额度快照 JSON）。
- **绝不存** sessionKey、Cookies、OrgUUID。
- 记录时机（都在删除/置失效**之前**读取账号信息）：
  1. `RefreshAccount` 判 `expired`（含 `RemoveInvalidAccount` 直接删除分支）
  2. `dispatcher.go` 换号重试时删除失效账号分支
  3. `AdminDeleteAccount`、`AdminDeleteExpired` / `DeleteAccountsByStatus`（手动，Reason=`manual_delete`）
- 记录时机补充：`markExpired`（额度查询/检查账号判失效）与 `handler/proxy.go` 镜像站二次确认失效两处，也走 `repository.MarkAccountExpired` 记录。`ChatCount` 只数 `success=true` 的 api_logs。
- 已知取舍：一个邮箱只记一次；重新导入后再死不再记；曾 expired 后恢复 active 的账号，墓碑仍在。
- 去重：先记 `expired`、之后又被手动删，不再重复记；但可把原因保持为最早的那条。**手动删除也计入阵亡统计数**（用户 2026-10-13 确认：他手动删的都是阵亡的）。列表里原因仍显示“手动删除”以便区分。
- 接口：`GET /api/graveyard`（列表，分页，倒序）、`GET /api/graveyard/stats`（累计阵亡数、平均寿命、近 7 天阵亡数）。
- 页面：导航加“乱葬岗”（`data-page="graveyard"`），墓碑卡片（邮箱、阵亡时间、存活时长、原因、对话次数、死前额度）+ 顶部统计。文案有点幽默但克制。空状态写“还没有账号阵亡”。
- 只从上线那天起记；之前删掉的账号没有记录。
- 测试：repository 层（去重、不含敏感字段）、handler 层、各触发点。

### E. 记住每页条数

- 账号页 `#page-size`、日志页 `#logs-page-size` 变化时写 localStorage（键如 `claude2api_acc_pagesize`、`claude2api_logs_pagesize`），读取时校验值必须在下拉选项里，否则用默认。页面加载时同步到变量和下拉框。
- localStorage 读写加 try/catch。

### F. 美化

- 只用 CSS（`web/style.css`）：卡片统一圆角、悬浮轻微上浮 + 阴影变化、进度条宽度平滑过渡、按钮悬浮反馈。
- 必须加 `@media (prefers-reduced-motion: reduce)` 关闭动画。键盘焦点要可见。
- 沿用现有配色，不推翻重做；乱葬岗页面可以略有个性，其余克制。
- 不要给每个元素都加动效，避免花哨。

## 5. 风险与注意

- 所有“检查/刷新”类请求都会消耗账号寿命，新增功能不得引入并发请求或自动轮询 Claude。
- 改 `app.js` 时注意文件很大（约 1600 行），用精确替换，别整体重写。
- 换行符：仓库是 LF，本机 `core.autocrlf=true`。提交前 `git diff --cached --stat`，若出现“整个文件都改了”，先停下排查。
- 新增 SSE 接口要考虑 nginx 缓冲与超时。
- 部署：本地提交推送 → 服务器备份 → `git fetch origin && git reset --hard origin/main` → `docker compose up -d --build`（端口 `127.0.0.1:8788`）。改动涉及数据库新表，`AutoMigrate` 会自动建，旧数据不受影响。

## 6. 进度记录

- [x] A 刷新按钮整理（2026-10-13 完成，未提交、未部署；build/vet/test、node 测试通过，评审 APPROVED；浏览器点击未实测）
- [x] B 额度实时推送（同上；新文件 internal/handler/events.go、internal/service/events.go、web/live.js；实施记录与评审见 .agents/tasks/ab-plan.md、ab-review.md，提交时不要带 .agents/）
- [x] C 额度均衡选号（2026-10-13 完成，未提交、未部署；主要改 internal/config/config.go、internal/service/dispatcher.go、web/index.html、web/app.js）
- [x] D 乱葬岗（2026-10-13 完成，未提交、未部署；新文件 internal/repository/graveyard.go、internal/handler/graveyard.go、web/grave.js 及测试；浏览器点击未实测）
- [x] E 记住每页条数（2026-10-13 完成，未提交、未部署；新文件 web/prefs.js；浏览器点击未实测。实施记录见 .agents/tasks/cde-plan.md，提交时不要带 .agents/）
- [x] F 美化（2026-10-13 完成，未提交、服务器尚未更新到本次改动；含“已失效/额度用光”状态视觉区分；实拍：账号页卡片/表格、乱葬岗，号池页未实拍；记录见 .agents/tasks/f-plan.md）

（每项完成后在此补：日期、提交哈希、是否已部署服务器、遗留问题。）
