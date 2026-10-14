# 开发计划 2（接手先看这个）

写于 2026-10-14。状态：**方案已定，尚未动手**。上一份计划 `docs/PLAN.md` 的 A~F 已全部完成。

## 0. 规则（和 PLAN.md 一致，再强调）

- 用户是非开发者、初学者，说话要短，回复用中文。
- **不要自动 commit / push / 部署**，每次先问用户。
- 不要在对话、命令、文件里出现密钥、令牌、密码、sessionKey、代理账号密码。
- 服务器 `config.yaml` 和 `data/` 是线上数据，不能覆盖或删除。部署前先备份。
- 提交时不要带 `.agents/`。
- 编译测试：见外层 `开发说明.md`。go.exe 在 `_work/tools/go/bin`，Git Bash 里要把它加进 PATH。
- **实时更新本文件**：每完成一步，立刻把第 6 节的 `[ ]` 改成 `[x]`，并补一行“日期、做了什么、测试结果、是否提交/部署、遗留问题”。不要等全部做完再写。

## 1. 当前状态（2026-10-14 已核实）

- 本地、GitHub、服务器 git 记录都在 `a490a1b`。服务器已对齐（备份 `/root/claude2api-backup-2026-10-14-0540`），容器正常，端口 `127.0.0.1:8788`。
- 服务器时区是 **UTC**，北京时间 = UTC+8。
- 服务器 `config.yaml` 的 `proxy` 为空：所有账号直接用服务器 IP 访问 Claude。

## 2. 本轮要做的功能

1. 定时刷新账号（用户想法：早上 6 点提前刷新，让 5 小时窗口提前开始计时，10 点上班、11 点附近窗口重置，体验更顺）
2. 乱葬岗曲线图（每日阵亡数）

顺序：先 1 后 2。

## 3. 功能 1：定时刷新

### 需求
- 设置页：开关（默认关）+ 每天几点（**用户填北京时间**，如 06:00；程序内部用 UTC+8 固定时区换算，不依赖服务器时区）。
- 到点后逐个排队检查账号，复用现有 `service.CheckAccount` / `RefreshAllQuotas`，每个账号只发一轮请求。
- **间隔比手动检查更长**：账号之间随机 8~20 秒（手动是 3~8 秒）。用户明确说可以更慢更平滑。
- 每天只跑一次。

### 设计
- 配置项（`internal/config/config.go`，沿用 `CONFIG_FIELDS` 机制）：`schedule_enabled`(bool)、`schedule_time`(字符串 "HH:MM"，校验格式，非法则回退 "06:00")。
- 新文件 `internal/service/schedule.go`：
  - `StartScheduler()`，在 `cmd/server/main.go` 里 `StartAccountStatusMonitor()` 之后调用。
  - 后台循环每 30 秒醒一次，**只比较本地时间，不发任何请求**。条件：已开启 且 北京时间现在 >= 今日设定时间 且 今日未跑过 且 没有别的批次在跑（`TryBeginQuotaBatch` 失败就下一轮再试）。
  - 重启补跑：若到点时服务正好不在，开机后只在设定时间后 **2 小时内**补跑一次，超过就跳过当天（过晚补跑失去意义）。
  - 间隔用独立变量 `ScheduleGapMin/Max`（测试里设 0），不要改手动检查的 `CheckGapMin/Max`。
  - 账号被判失效时，走 `repository.MarkAccountExpired`（会入乱葬岗），不要绕开。
- 状态持久化：`data/schedule_state.json`（用 `config.DataDir()`），记录 `last_run_date`(北京日期)、`last_run_at`、`ok/failed/skipped`。原子写（先写临时文件再改名）。文件损坏则当作没跑过。
- 接口：`GET /api/schedule/status` 返回开关、设定时间、下次运行时间、上次结果；运行中的进度和结果通过已有的实时推送（`internal/service/events.go`）广播，页面不用 F5。
- 前端：系统管理页加输入；账号页顶部显示“上次定时刷新：时间 · 成功 N / 失败 M”和“下次：明天 06:00”。文案注明“会向 Claude 发请求，每个账号 1 次”。
- 连续被限流 2 个账号会自动停止（`RefreshAllQuotas` 已有逻辑），保留。

### 测试
- 用可注入的时钟：到点触发、未到点不触发、今日已跑不重复、补跑窗口内/外、批次忙时延后、配置非法时间回退、状态文件损坏、关闭开关不跑。
- handler/router：status 接口需要 AdminAuth（无凭据 401）。
- 前端纯函数（下次运行时间文案）写进 `web/test/`。

### 风险
- 不得引入并发请求，不得缩短间隔到 8 秒以下。
- 北京时间不要用 `time.Local`，用 `time.FixedZone("CST", 8*3600)`。

## 4. 功能 2：乱葬岗曲线图

- 后端：`GET /api/graveyard/daily?days=7|30`（默认 7，其它值夹到 7 或 30）。返回 `[{date:"2026-10-14", count:N}]`，**按北京日期分组并补零**（Grave.DiedAt 存 UTC，在 Go 里换算分组，不要用 SQL 日期函数）。含手动删除（用户已确认计入阵亡）。
- 前端：`web/grave.js` 加纯函数生成 SVG 折线图（无依赖、用 createElementNS，不拼 HTML 字符串），切换“近 7 天 / 近 30 天”。空数据显示“这段时间没有账号阵亡”。需有深浅色可读、键盘焦点可见、`prefers-reduced-motion` 下不做动画；每个点有 `<title>` 提示“日期：N 个”。
- 测试：repository（补零、跨午夜北京日期、手动删除计入）、handler（401、参数夹取）、`web/test/grave.test.js`（点坐标、空数据、最大值为 0 时不除零）。

## 5. 已解答的问题（用户问：请求中途额度用完，能否无缝换号？）

结论：**只在“还没开始输出”时无缝，开始输出后不能。**

- `internal/service/dispatcher.go` 里有 `emitted` 标记：只要已经向客户端输出过任何内容，就**不再换号**，因为换号会让客户端收到重复片段。
- 还没输出就失败（建会话失败、被拒绝、空回复、`stop_reason=refusal`）：按 `retry_count` 自动换号重试，客户端无感。
- 已经在流式输出时中断（`流式输出中断`）：直接返回错误，不会把剩下的活转给别的账号。
- 一般来说，一个已经开始的请求，上游通常会让它写完，额度是在这条消息之后才显示用光，所以“请求跑到一半被掐断”并不常见（此点未实测）。更常见的是：额度已空的账号在**下一个**请求开始时被拒绝，这种会换号重试。
- 已有的缓解：选号时跳过额度耗尽的账号、5 小时剩余明显偏少的账号本轮不分配（`balanceByFiveHour`，阈值 `quota_balance_gap` 默认 0.30）。
- 若以后想做“中途断了也能续”：需要把已输出的内容作为上下文喂给新账号继续生成，复杂且会产生拼接痕迹和额外消耗，**目前不做**，用户要求时再评估。

## 6. 进度记录（实时更新）

- [x] 3 定时刷新：后端 schedule.go + 配置项（2026-10-14 完成：配置项 schedule_enabled / schedule_time 在 internal/config/config.go；调度器 internal/service/schedule.go（北京时间固定时区、间隔 8~20 秒、每天一次、重启补跑窗口 2 小时、状态文件 data/schedule_state.json）；main.go 已接入 StartScheduler；schedule_test.go 5 个测试通过；未提交、未部署）
- [x] 3 定时刷新：状态文件与 status 接口（2026-10-14 完成：状态文件 data/schedule_state.json 已在 schedule.go；接口 GET /api/schedule/status 在 internal/handler/schedule.go，已挂路由并有 401 鉴权测试；未做实时推送，设置页打开时读取一次状态，如需实时再加）
- [x] 3 定时刷新：前端设置（2026-10-14 完成：设置页新增“定时检查账号”开关与时间输入，下方显示上次/下次时间；web/index.html、web/app.js；账号页顶部的状态显示未做；浏览器点击未实测）
- [x] 3 定时刷新：测试 + build/vet/test 全绿（2026-10-14：go build/vet/test 全绿，node 测试 4 个文件全过；未提交、未部署）
- [x] 4 曲线图：后端 daily 接口 + 测试（2026-10-14 完成：GET /api/graveyard/daily?days=7|30，repository.GraveDailyCounts 按北京日期分组补零，含手动删除；有跨午夜测试和 401 鉴权测试；未提交、未部署）
- [x] 4 曲线图：前端 SVG + 测试（2026-10-14 完成：web/grave.js buildChart/chartPoints，乱葬岗页顶部折线图可切近 7/30 天；node 测试 13 个通过；go build/vet/test 全绿；浏览器点击未实测）
- [x] 5 设置页“预期窗口刷新时间”（2026-10-14 已完成：web/live.js 新增 windowEstimate，web/app.js 新增 updateScheduleEstimate 并在 loadConfig 与时间输入事件里调用，web/index.html 新增 #schedule-estimate 并把定时检查说明改为如实文案；web/test/live.test.js 新增 2 个测试，live 共 11 个通过，node 测试 4 个文件全过，go build/vet/test 全绿；未提交、未部署；浏览器点击未实测。以下为当时的方案记录）：
  - 用户决定：**只在设置页**加一句预期，不读账号数据，不显示在账号页。例如定时 07:00 → “预期：每天 07:00 检查，约 12:00 起 5 小时窗口刷新”，直接把时间往后推 5 小时；跨午夜显示“次日”。纯前端计算，随时间输入实时变化，不发请求。不需要加“以实际对话为准”的提醒（原因见下）。
  - 事实核实（用户曾质疑“定时检查是否向 Claude 发请求”）：检查账号先调 `/usage` 接口（`internal/service/usage.go` 的 RefreshAccountQuota，只读，不是对话）；若窗口全为 null（免费账号），回退 `probeQuotaByChat`：给 `claude-haiku-4-5-20251001` 发 `hi`，用完即删会话，从 message_limit 事件读额度，约消耗 1 条消息。付费账号只读接口、不发对话，**未实测**；只读接口不会开启 5 小时窗口，免费号的探测对话才会开启窗口。上一位 AI 说“没找到额度接口”不准确。
  - 文案要如实：设置页“定时检查账号”说明改为“免费账号会发一条最短对话（约消耗 1 条消息）”，不要再写“每个账号发 1 次请求”。
  - 步骤：[ ] 改 web/index.html 说明文案 + 预期句占位；[ ] web/live.js 或 web/prefs.js 加纯函数 `windowEstimate(hhmm)`（返回 {start, end, nextDay}）；[ ] web/app.js 绑定输入事件实时更新；[ ] web/test/ 加测试（普通、跨午夜、非法输入）；[ ] node 测试 + go build/vet/test 全绿；[ ] 回填本条为 [x]。
- [x] 3 定时刷新：账号页顶部状态行（2026-10-14 完成：web/index.html 新增 #schedule-line；web/live.js 新增 scheduleLine（未开启 / 尚未运行 / 进行中 / 上次成功失败数 + 下次今天·明天·日期）；web/app.js 新增 loadScheduleLine，进入页面、回到前台、收到 SSE schedule 事件时重读 /api/schedule/status。后端：service/events.go 新增 SubscribeScheduleEvents / PublishScheduleChanged，handler/events.go 推 `event: schedule`，RunScheduledCheck 开始与结束各广播一次；不新增任何对 Claude 的请求，不改检查行为和间隔。测试：新增 live.test.js 5 个（共 16）、schedule_events_test.go 1 个；go build/vet/test 全绿，node 测试 4 个文件全过，node --check 通过；未提交、未部署；浏览器点击未实测）
- [ ] 提交、推送 GitHub、服务器备份并部署（每步先问用户）

（每项完成后在此补：日期、提交哈希、是否已部署、遗留问题。）

## 7. 出口 IP / 代理（给用户的说明，非开发项）

- 配置项 `proxy`，后台“系统管理”里有输入框；留空则直连。全局一个代理，所有账号共用，不能按账号分配。
- 想“每个账号一个出口 IP”是另一个功能，用户尚未要求。
- 代理地址含账号密码，**不要让用户发到对话里**，让他自己填进后台。
