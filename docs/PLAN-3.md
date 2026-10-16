# 开发计划 3：额度均衡选号改造（接手先看这个）

写于 2026-10-16。状态：**方案已定，正在动手**。上一份计划见 `docs/PLAN-2.md`（第 8、9 节之后无新内容），均衡选号的原始设计见 `docs/PLAN.md` 的 C 节。

## 0. 规则（和 PLAN-2.md 一致，再强调）

- 用户是非开发者、初学者，说话要短，回复用中文。
- **不要自动 commit / push / 部署**，每次先问用户；推送前先给用户看改了哪些文件。
- 不要在对话、命令、文件里出现密钥、令牌、密码、sessionKey、代理账号密码。
- 服务器 `config.yaml` 和 `data/` 是线上数据，不能覆盖或删除。部署前先备份。
- 提交时不要带 `.agents/`。
- 提交信息结尾加：`Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`
- 不得新增任何对 Claude 的请求（不主动刷新额度、不轮询）。
- **实时更新本文件**：每完成一步，立刻把第 6 节的 `[ ]` 改成 `[x]`，并补一行“日期、做了什么、测试结果、是否提交/部署、遗留问题”。不要等全部做完再写。
- 编译测试：见外层 `开发说明.md`。go.exe 在 `_work/tools/go/bin`，Git Bash 里要把它加进 PATH。（本次是在新克隆的目录里工作，若找不到 go，先确认 go 的路径再跑。）
- 换行符：仓库是 LF，本机 `core.autocrlf=true`。提交前 `git diff --cached --stat`，若出现“整个文件都改了”，先停下排查。

## 1. 用户的问题

用户反馈：账号额度均衡使用不稳定，有的号被榨干，有的号一直是满的、毫发无伤。

## 2. 已核实的现状（2026-10-16 读代码得出）

选号入口：`internal/service/dispatcher.go` 的 `pickAPIAccount`，流程是：
`AccountUsable` 过滤 → `skipExhausted`（跳过 5h/7d 已耗尽）→ `balanceByFiveHour`（剩余比最大者低超过 gap 的跳过）→ `usable[apiIndex % len(usable)]` 轮询，`apiIndex++`。

根因（按影响排序）：

1. **快照 10 分钟过期当“未知”（最关键）**。`fiveHourRemain` 里用 `balanceFreshWindow = 10 分钟`：`UpdatedAt` 超过 10 分钟就返回 `known=false`。额度快照只在“该号完成对话”或“检查/刷新账号”（含每日定时检查）时更新，所以闲置的号 10 分钟后变“未知”，**既不被跳过，也不参与求最大值**。满的闲置号看不见，被榨干的号每次对话都有新快照。
   - 注意：面板后台显示的额度（`web/quota.js` 的 `windowState`）**没有过期概念**，只看 `resets_at`：重置时间已过 → 按满额 100%；否则按 `utilization` 算。所以用户在面板看到的“剩余多少”和选号用的“剩余多少”口径不一致。新方案要让选号和面板口径一致。
2. **只是过滤，不是优先选剩余多的**。过滤后剩下的号仍是简单轮询，差距 30 个百分点以内的号平分流量，均衡几乎不起作用。
3. **全局计数器 `apiIndex` 配合每次都在变的候选列表**，`apiIndex % len(usable)` 落点跳动，个别号可能连续被选中或长时间轮不到。

用户已确认：
- 不看 7 天窗口（“不重要”）。`skipExhausted` 里已有的 7 天耗尽跳过**保持不动**。
- 不能为了刷新额度而向 Claude 发请求。
- 账号池里的号只给本项目用，不会在网页/手机上另外使用，所以“旧快照在重置前一直有效”的假设成立。
- 同意按“差 5 个百分点”的新规则改。

## 3. 新方案

### 3.1 剩余额度计算 `fiveHourRemain`

- **去掉 10 分钟过期**（删除 `balanceFreshWindow`，或不再用于判断）。
- 规则：
  - 没有快照（`UpdatedAt` 为零）或没有 5 小时用量数据 → 视为**满额 100%**（让新号先被用起来，产生第一份快照）。
  - 有 `ResetsAt` 且 `now >= ResetsAt` → 窗口已重置，剩余 100%。
  - 否则剩余 = `1 - utilization`，夹到 [0,1]。
- 返回值不再需要 `known`（所有号都有剩余值）；如果保留函数签名，则恒为 true。以实现时的简洁为准，但要同步改测试。

### 3.2 选号方式 `balanceByFiveHour` / `pickAPIAccount`

- 找出候选里**剩余最多**的值 `maxRemain`。
- 候选集 = 剩余 `>= maxRemain - gap` 的账号（gap 默认改为 **0.05**，即 5 个百分点以内算“差不多”）。
- 在候选集内轮流选择。
- `gap <= 0` 表示关闭均衡：原样返回全部，仍按原轮询（保持旧行为，测试里要覆盖）。
- 保护：候选集为空时原样返回全部（理论上最大者必留）。
- 保持候选的原有相对顺序。

### 3.3 轮询计数器稳定性（第 3 点）

- 现在 `apiIndex % len(usable)` 随候选变化会跳。改法：在候选集里选**最近最少被选中**的账号（记录每个邮箱最后一次被选中的时间或序号，候选里挑最旧的）。
- 做法：在 dispatcher.go 增加一个受 `apiIndexLock` 保护的 `lastPicked = map[string]uint64`，每次选中时写入递增序号；候选里选序号最小（从未选过视为 0）者；并列时按候选原顺序。
- 号被删除时，已有的 `pickAPIAccount` 清理 `apiClients` 的逻辑旁边，同步清理 `lastPicked` 里不在 `active` 的邮箱。
- `apiIndex` 变量如果不再使用就删掉（注意先 Grep 确认没有别处引用）。

### 3.4 配置

- `internal/config/config.go`：`QuotaBalanceGap` 默认值 0.30 → **0.05**（第 119 行附近的默认 `Settings{...}`）；字段注释改成“剩余在最大值该范围内的账号轮流分配；0 关闭”。
- 注意：**已有的服务器 `config.yaml` 如果写过 `quota_balance_gap: 0.3`，部署后仍会是 0.3**。部署时要告诉用户：去后台“系统管理”把均衡阈值改成 0.05（或确认服务器配置里有没有这个键）。`config.example.yaml` 若有这个键也要同步改（先 Grep 确认）。
- `web/index.html`（约第 599 行）“额度均衡阈值（0 = 关闭）”的说明文案、`web/app.js`（约第 71 行）默认值 `quota_balance_gap: 0.3` 同步改成 0.05。
- `README.md` 第 25 行和 `docs/PLAN.md` C 节的描述已过时，改完后在 README 里改一句话；PLAN.md 旧计划不改，只在本文件记录。
- `config_test.go` 里涉及 gap 默认值的测试要核对（`TestUpdateQuotaBalanceGap`、`TestSettingsJSONHasQuotaBalanceGap`）。

### 3.5 不改的东西

- 7 天窗口（不参与均衡）。
- `skipExhausted` 及 `windowBlocked`。
- 任何向 Claude 发请求的代码，包括定时检查、手动刷新。
- 面板前端的额度展示逻辑（`web/quota.js`）。

## 4. 要改的文件

- `internal/service/dispatcher.go`：`fiveHourRemain`、`balanceByFiveHour`、`pickAPIAccount`、`lastPicked`、删 `balanceFreshWindow` 和 `apiIndex`（若无其他引用）。
- `internal/service/dispatcher_pick_test.go`：旧测试按旧规则写，需要重写。逐个处理：
  - `TestBalanceSkipsLowRemain`：改成 gap=0.05 的新规则。
  - `TestBalanceUnknownNotSkipped`：**作废重写**，改成“没快照/过期快照按满额算且不再被当成未知”。
  - `TestBalanceAllFilteredPassThrough`、`TestBalanceWindowResetCountsFull`、`TestBalanceDisabled`、`TestBalanceBoundaryNotSkipped`、`TestBalancePreservesOrder`：按新语义调整期望。
  - `TestPickAPIAccountUsesBalance`：改成验证“剩余最多的号被优先、差距在 gap 内的轮流、gap=0 时全部出现”。
  - 新增：快照 3 小时前、窗口未重置，仍按旧用量参与（不过期）；没有快照的号按满额被优先选；最近最少被选中的轮流顺序稳定。
- `internal/config/config.go`、`internal/config/config_test.go`
- `web/index.html`、`web/app.js`（默认值和文案）
- `README.md`（均衡选号一句话）
- `config.example.yaml`（仅当里面有这个键）

## 5. 风险与注意

- 没快照的新号按满额处理，会被优先选中，直到它产生快照；这是有意为之。
- 如果上游 `/usage` 或对话事件长期不给 `utilization`，该号会一直按满额，被偏爱。出现这种情况再单独评估。
- 快照只在对话后和检查后更新；同一号高并发时快照滞后，可能短时间内多请求打到同一个号，属已知取舍。
- 不得引入并发请求，不得改检查间隔。

## 6. 进度记录（实时更新）

- [x] 0 安装 Go（2026-10-16：winget install GoLang.Go 成功，go1.27.0，路径 `/c/Program Files/Go/bin`，Git Bash 里要先 `export PATH="/c/Program Files/Go/bin:$PATH"`；改动前基线：go build 通过，go test ./... 除 `internal/adapter` 的 SDK 集成测试外全绿，该测试需要本地 127.0.0.1:8787 服务在跑，不是代码问题，用 `go test ./internal/adapter -skip SDK` 跳过后 adapter 也通过）
- [x] 1 读代码确认没有别处引用 `apiIndex`、`balanceFreshWindow`、`fiveHourRemain`（2026-10-16 已核实：`apiIndex` 除 dispatcher.go 外，`internal/service/usage_test.go:331-333` 也在用 `apiIndexLock`，删 `apiIndex` 时要一并看这里；`balanceFreshWindow`、`fiveHourRemain` 只在 dispatcher.go；`config.example.yaml` 里没有 `quota_balance_gap` 键，不用改；默认值相关位置：config.go 第 119 行、config_test.go 第 25/57 行、web/app.js 第 71 行、web/index.html 第 601 行）
- [x] 2 改 `dispatcher.go`：剩余额度计算（2026-10-16 完成：删 `balanceFreshWindow`，`fiveHourRemain` 改为只返回剩余比例，无快照/无用量按满额；尚未编译，旧测试待重写）
- [x] 3 改 `dispatcher.go`：均衡改成“剩余最多 ± gap 内” + 最近最少选中（2026-10-16 完成：删 `apiIndex`，新增 `lastPicked`/`pickSeq`/`leastRecentlyPicked`，`pickAPIAccount` 清理失效账号的 lastPicked；尚未编译；下一步第 4 步改 config 默认值）
- [x] 4 改 config 默认值与注释（2026-10-16 完成：config.go 默认 0.30 → 0.05，注释已改；config_test.go 用显式值，不用改）
- [x] 5 改 `web/index.html`、`web/app.js` 默认值与文案；README（2026-10-16 完成：app.js 默认 0.05；index.html 占位符与说明文案已改；README 已改；config.example.yaml 无此键；尚未编译、未测试）
- [x] 6 重写/新增 `dispatcher_pick_test.go` 测试（2026-10-16 完成：重写旧均衡测试，新增 TestFiveHourRemain、TestBalanceStaleSnapshotStillCounts、TestBalanceNoSnapshotCountsFull、TestLeastRecentlyPicked，TestPickAPIAccountUsesBalance 改为验证低剩余号不出现且其余号 6:6 均匀轮流；go vet 通过，go test ./internal/service ./internal/config 通过）
- [x] 7 `go build`、`go vet`、`go test ./...` 全绿，node 测试全绿（2026-10-16 完成：go build 通过；go vet 通过；go test ./... -skip SDK 全部包 ok（SDK 集成测试需本地 127.0.0.1:8787 服务，不属本次改动）；node 测试 4 个文件全过：grave 18、live 16、prefs 10、quota 38；未提交、未部署；浏览器点击未实测）
- [ ] 8 给用户看改动（`git diff --stat` + 要点），等用户确认后再提交、推送
- [ ] 9 （用户决定）部署：先备份服务器，部署后提醒用户在后台把“额度均衡阈值”改成 0.05

（每项完成后在此补：日期、提交哈希、是否已部署、遗留问题。）

## 7. 已回答的用户问题（给接手的人参考）

- “后台能看到剩多少，所以不知道会过期”：后台展示逻辑（`web/quota.js`）本来就没有 10 分钟过期，过期只存在于后端选号（`fiveHourRemain`）。两边口径不一致正是问题之一。
- “能不能先刷新额度再选”：不能，刷新就是向 Claude 发请求。
- “如何更智能”：用重置时间代替固定的 10 分钟过期，因为没人动的号，额度在重置前不会变。
