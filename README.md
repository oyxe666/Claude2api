<h1 align="center">Claude2API</h1>
<p align="center">把 Claude.ai 账号池转成标准 API 的网关（Go）</p>

Claude2API 统一管理多个 Claude.ai 账号，自动选号、失败换号，并以 **Anthropic Messages**、**OpenAI Chat Completions**、**OpenAI Responses** 三种标准接口对外提供服务。可接入 Claude Code、Codex CLI、各类 SDK 和支持自定义 Base URL 的客户端。自带管理后台：账号、密钥、调用日志、账号存活、用量统计、在线对话测试。

> [!WARNING]
> 免责声明：
>
> 本项目涉及对 Claude.ai 官网相关能力的逆向研究，仅供个人学习、技术研究与非商业性技术交流使用。
>
> - 严禁用于任何商业用途、盈利性使用、批量操作、自动化滥用或规模化调用。
> - 严禁用于破坏市场秩序、恶意竞争、套利倒卖、二次售卖相关服务，以及任何违反 Anthropic 服务条款或当地法律法规的行为。
> - 严禁用于生成、传播或协助生成违法、暴力、色情、未成年人相关内容，或用于诈骗、欺诈、骚扰等非法或不当用途。
> - 使用者应自行承担全部风险，包括但不限于账号受限、临时封禁、永久封禁以及因违规使用导致的法律责任。
> - 本项目依赖 Claude.ai 上游接口，上游接口、风控策略及页面结构的变化都可能导致部分功能失效。
> - 使用本项目即视为你已充分理解并同意本免责声明；请勿使用重要账号、常用账号或高价值账号进行测试。

## 来源说明

本项目基于 [basketikun/claude2api](https://github.com/basketikun/claude2api)（MIT 许可证）修改，与原作者无关联。感谢原项目。

## 功能

接口
- 三种协议：`POST /v1/messages`（Anthropic）、`POST /v1/chat/completions`、`POST /v1/responses`（OpenAI），另有 `GET /v1/models`。
- 流式与非流式输出、多轮对话、System Prompt。
- 工具调用：Anthropic `tools / tool_use`、OpenAI `tools / tool_calls`、Responses `function_call`。网页端只能输出纯文本，所以网关用标签化协议（tagged tool call）让模型输出 `<tool_calls>` / `<final_answer>`，再解析成各协议的工具调用格式。
- 图片输入：支持 base64（Data URI）和 http(s) 链接，格式 jpeg / png / webp / gif，单张不超过 20 MB，自动上传到 Claude.ai。
- 扩展思考：模型名加 `-thinking` 后缀。
- 长上下文：提示词超过 `max_history_length` 时自动转为文本附件。
- `stop_sequences`：不再转发给上游，由网关在本地截断，并返回 `stop_reason=stop_sequence`。这修复了 Claude Code auto mode 分类器请求返回 400 的问题。
- WebSearch（实验性）：识别 Anthropic 服务端工具 `web_search_20250305` 的请求，输出 `server_tool_use` / `web_search_tool_result` 内容块；上游没有结构化结果时回退为文字。原生搜索块这部分尚未用真实上游数据验证。
- 上游拒绝（`stop_reason=refusal`）会返回明确错误，并按重试次数换号重试；上游空回复视为失败并换号重试。

号池
- 批量导入 `sessionKey`，自动获取邮箱和组织信息；账号状态查看、手动刷新、失效清理。
- 账号停用 / 启用开关：停用的号不参与选号，额度照常检查。
- 额度显示：记录每个账号的 5 小时 / 7 天额度，后台有总览条、进度条和重置倒计时，可单个或全部刷新，变化实时推送到页面。
- 额度均衡选号：优先分配 5 小时剩余额度多的账号（与最大剩余差距在阈值内的轮流用），剩余明显偏少的暂不分配。额度快照不会过期，只看重置时间。阈值 `quota_balance_gap` 默认 0.05（5 个百分点），可在后台调整，0 为关闭。
- 跳过额度已耗尽的账号；失败时按 `retry_count` 换号重试。
- 检查账号逐个排队执行，间隔随机，带进度条，可随时停止。
- 定时检查：每天在设定的北京时间自动检查，可提前开启 5 小时窗口。
- 官网镜像：号池选择页可随机或指定账号进入 claude.ai 镜像站。

管理后台
- 账号管理（卡片 / 表格视图）、密钥管理、API 文档、对话测试、调用日志、账号存活、用量统计、系统管理。
- 账号存活：存活率、存活曲线、每日导入 / 阵亡图，以及「乱葬岗」（失效、被清理、手动删除的账号及存活时长）。
- 用量统计：近 7 / 30 天每日输入 / 输出 token 曲线，按模型的调用数、成功率和占比。数据来自本地调用日志，不会向 Claude 发请求。
- 费用折算：系统管理里有「模型单价表」（默认填 Anthropic 官方 API 价格，可自行修改），用量页据此折算费用。订阅账号实际不按 token 计费，折算值仅供参考；token 为本地估算值。
- 调用日志：时间、接口、模型、账号、状态、输入 / 输出 token、总耗时、首字延迟、TPS；可选保存完整请求与响应（`detailed_api_log`），支持详情、批量删除、只保留最近 N 条。
- 手机端适配：顶栏可滑动，表格卡片化，弹窗为底部抽屉。

## 快速部署

Docker Compose 本地构建（不会拉取原作者的镜像）：

```bash
git clone https://github.com/oyxe666/Claude2api.git
cd Claude2api
cp config.example.yaml config.yaml
# 编辑 config.yaml，至少修改 admin_password
docker compose up -d --build
```

- `docker-compose.yml` 与 `docker-compose.local.yml` 内容相同，都用本仓库代码构建。
- 服务默认只绑定 `127.0.0.1:8788`（容器内端口 8787），外网不能直连，适合前面放 nginx 等反向代理。要直接对外开放，把 `ports` 改成 `"8787:8787"`，并务必改掉管理密码、设置好 API 密钥。
- `./data` 挂载到容器 `/app/data`，数据库为 `data/app.db`；`./config.yaml` 挂载为配置文件。
- 打开 `http://127.0.0.1:8788/`，用 `admin_password` 登录后台。

## 配置

`config.yaml`（模板见 `config.example.yaml`）：

| 配置项 | 默认 | 说明 |
| --- | --- | --- |
| `proxy` | 空 | 出口代理 |
| `web_host` | `0.0.0.0` | 监听地址 |
| `web_uc_host` | `localhost` | Artifacts 沙箱 Host，用反代时需要一个与主域名不同的域名 |
| `admin_password` | `claude2api` | 管理后台密码，请务必修改 |
| `status_check_seconds` | `21600` | 账号状态监测间隔（秒） |
| `retry_count` | `0` | 失败换号重试次数 |
| `delete_chat` | `true` | 请求完成后删除 Claude.ai 上游会话 |
| `max_history_length` | `12000` | 提示词超过该长度时转为文本附件 |
| `remove_invalid_account` | `false` | 自动移除会话失效的账号 |
| `detailed_api_log` | `true` | 记录完整 API 请求与输出 |
| `quota_balance_gap` | `0.05` | 额度均衡阈值，0 关闭（示例文件未列出，后台可调） |
| `schedule_enabled` / `schedule_time` | 关 / `06:00` | 定时检查账号及北京时间（后台可调） |
| `model_prices` | 官方价格 | 模型单价表，美元 / 百万 token（后台可调） |

服务端口固定为 8787。大部分配置也可在后台「系统管理」修改，修改会写回 `config.yaml`。

## 使用

先在后台「密钥管理」创建 API 密钥。以下示例中 `<api-key>` 为占位符，`localhost:8788` 对应上面的默认端口。

`/v1/*` 接口用 `Authorization: Bearer <api-key>` 或 `x-api-key: <api-key>` 鉴权。

### 支持的模型

每个基础模型都另有 `-thinking` 版本：

- `claude-sonnet-4-6`（默认）
- `claude-haiku-4-5-20251001`
- `claude-sonnet-5`
- `claude-sonnet-5-5`
- `claude-haiku-5-5`

实际可用性取决于账号权限和上游状态，以 `GET /v1/models` 的返回为准。

```bash
curl http://localhost:8788/v1/models -H "Authorization: Bearer <api-key>"
```

### Anthropic Messages

```bash
curl http://localhost:8788/v1/messages \
  -H "x-api-key: <api-key>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 1024,
    "system": "回答尽量简短",
    "messages": [
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "描述这张图片"},
          {
            "type": "image",
            "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo..."}
          }
        ]
      }
    ],
    "stream": true
  }'
```

WebSearch（实验性）：在 `tools` 里带上服务端工具即可。

```json
"tools": [{"type": "web_search_20250305", "name": "web_search"}]
```

### OpenAI Chat Completions

```bash
curl http://localhost:8788/v1/chat/completions \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {"role": "system", "content": "回答尽量简短"},
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "描述这张图片"},
          {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo..."}}
        ]
      }
    ],
    "stream": true
  }'
```

### OpenAI Responses

`input` 可以是字符串或消息数组：

```bash
curl http://localhost:8788/v1/responses \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "instructions": "回答尽量简短",
    "input": [
      {
        "role": "user",
        "content": [
          {"type": "input_text", "text": "描述这张图片"},
          {"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgo..."}
        ]
      }
    ],
    "stream": false
  }'
```

### 客户端接入

- Claude Code：Anthropic Base URL 设为本服务地址（`/v1/messages`），密钥填后台创建的 API 密钥。
- Codex CLI：OpenAI Base URL 设为本服务的 `/v1`（使用 `/v1/responses`），密钥同上。

### 账号导入

后台「账号管理」→「批量导入」，每行一个 `sessionKey`：

```text
sk-ant-sid01-xxxxxxxx
sk-ant-sid01-yyyyyyyy
```

导入在后台执行并自动查询账号信息。`sessionKey` 等同账号登录凭据，请妥善保管。

## 已知限制

- 额度查询使用 claude.ai 未公开的内部接口，随时可能变化；付费账号上未实测。免费账号该接口无数据，刷新额度时会额外发一条最短对话（约消耗 1 条消息）。
- 额度只在对话后更新，页面不会自动刷新额度，需按 F5。
- 「跳过耗尽账号」和「空回复重试」目前只有单元测试，没有线上真实触发验证。
- WebSearch 原生搜索块为实验性，未用真实上游数据验证。
- 用量统计的 token 和费用折算均为本地估算，仅供参考。
- 账号额度用完时，上游会返回限流（rate-limited）错误，等额度重置后恢复。

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=oyxe666/Claude2api&type=Date)](https://www.star-history.com/#oyxe666/Claude2api&Date)

## 社区支持

学 AI，上 L 站：[Linux.do](https://linux.do)
