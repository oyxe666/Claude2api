<h1 align="center">Claude2API</h1>

<p align="center">Claude2API 是一个基于 Go 开发的 Claude.ai API 兼容网关、账号池与网页镜像服务。项目通过统一管理多个 Claude.ai 账号并自动轮询可用账号，将 Claude 模型能力以 OpenAI API 和 Anthropic API 兼容接口的形式提供给第三方客户端、自动化脚本与开发工具。Claude2API 支持 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 接口，兼容 Claude Code、Codex CLI 以及支持自定义 Base URL 的应用；同时提供流式输出、多轮对话、System Prompt、多模态图片输入、扩展思考（Thinking）、Function Calling / Tool Use、长上下文处理、会话清理、API Key 鉴权、调用日志和可视化后台管理。项目支持 Docker Compose 一键部署，适合个人学习、接口适配、客户端联调和 Claude API 集成测试。</p>

> [!WARNING]
> 免责声明：
>
> 本项目涉及对 Claude.ai 官网相关能力的逆向研究，仅供个人学习、技术研究与非商业性技术交流使用。
>
> - 严禁将本项目用于任何商业用途、盈利性使用、批量操作、自动化滥用或规模化调用。
> - 严禁将本项目用于破坏市场秩序、恶意竞争、套利倒卖、二次售卖相关服务，以及任何违反 Anthropic 服务条款或当地法律法规的行为。
> - 严禁将本项目用于生成、传播或协助生成违法、暴力、色情、未成年人相关内容，或用于诈骗、欺诈、骚扰等非法或不当用途。
> - 使用者应自行承担全部风险，包括但不限于账号受限、临时封禁、永久封禁以及因违规使用导致的法律责任。
> - 本项目依赖 Claude.ai 上游接口，上游接口、风控策略及页面结构的变化都可能导致部分功能失效。
> - 使用本项目即视为你已充分理解并同意本免责声明；请勿使用重要账号、常用账号或高价值账号进行测试。

## 本改版说明

本项目基于 [basketikun/claude2api](https://github.com/basketikun/claude2api)（MIT 许可证）修改，与原作者无关联。相比原项目的差异：

- **适配 Sonnet 5.5**：去掉会触发上游拦截的 `<think>` 指引，新增 `claude-sonnet-5-5`、`claude-haiku-5-5`。
- **上游拒绝不再静默**：识别 `stop_reason=refusal` 并返回明确错误，按重试次数换号重试。
- **额度显示与刷新**：记录每个账号的 5 小时 / 7 天额度，后台提供总览条、进度条、重置倒计时、单个/全部刷新。
- **号池选号优化**：跳过额度已耗尽的账号；上游空回复视为失败并换号重试。
- **额度均衡选号**：优先分配 5 小时剩余额度多的账号（差距在阈值内的账号轮流用），剩余明显偏少的账号暂不分配；额度快照不设过期，只看重置时间，阈值（默认 5 个百分点）可在后台调整。
- **检查账号逐个排队**：额度检查按队列逐个执行、间隔随机，带进度条并可随时停止，额度变化实时推送到页面。
- **定时检查账号**：每天在设定的北京时间自动检查，可提前开启 5 小时窗口，设置页显示预计耗时和运行状态。
- **账号存活页**：新增「账号存活」页，含存活率、存活曲线、每日导入 / 阵亡图，以及「乱葬岗」（记录失效、被清理、手动删除的账号和存活时长）。
- **手机端适配**：顶栏改为可滑动菜单，账号 / 密钥表格改为卡片，弹窗改为底部抽屉，对话页和 API 文档页适配手机屏幕。
- **界面优化**：账号页卡片 / 表格视图切换、每页条数记忆、状态区分（失效、额度用完）、调用日志详情页改版。
- **用量统计页**：新增「用量统计」页，近 7 / 30 天每日输入 / 输出 token 曲线、按模型的调用数、成功率和占比。数据全部来自本地调用日志，不会向 Claude 发请求。
- **费用折算**：设置页新增「模型单价表」（默认填 Anthropic 官方 API 价格，可自行修改），用量页据此折算费用。订阅账号实际不按 token 计费，折算值仅供参考；token 为本地估算值。
- 新增对应的单元测试。

已知限制：

- 额度查询用的是 claude.ai 未公开的内部接口，随时可能变化；付费账号上未实测。免费账号该接口无数据，刷新额度时会额外发一条最短对话（约消耗 1 条消息）。
- “跳过耗尽账号”和“空回复重试”目前只有单元测试，没有线上真实触发验证。
- 额度只在对话后更新，页面不会自动刷新，需按 F5。

## 快速开始

Docker Compose 本地构建部署（用本仓库的代码构建镜像）：

```bash
git clone https://github.com/oyxe666/Claude2api.git
cd Claude2api
cp config.example.yaml config.yaml
docker compose up -d --build
```

> 本仓库的 `docker-compose.yml` 和 `docker-compose.local.yml` 都是用本仓库代码本地构建，不会拉取原作者的镜像。
>
> 两个 compose 文件默认都把服务绑定在 **`127.0.0.1:8788`**（容器内端口仍是 8787），只允许本机访问，外网不能直连，适合前面放 nginx 等反向代理。如果需要对外直接开放，请把 `ports` 改成 `"8787:8787"`（此时务必设置好 API 密钥与后台密码）。下文示例里的 `localhost:8788` 对应这个默认端口。

## 核心功能

- **官网镜像**：提供 Claude.ai 官网镜像和账号池选择页面，可随机或指定可用账号进入镜像站，并自动维护访问所需的 Cookie、浏览器指纹与会话信息。
- **号池管理**：支持批量导入 Claude.ai 账号，自动获取邮箱和组织信息，并提供账号状态查看、手动刷新、失效清理与定期巡检。
- **账号轮询**：API 请求自动轮询可用账号，请求失败时可按配置换号重试，避免单个账号异常影响服务。
- **OpenAI 兼容**：支持 `POST /v1/chat/completions` 和 `POST /v1/responses` 接口，可接入 OpenAI SDK 及支持自定义 Base URL 的客户端。
- **Anthropic 兼容**：支持 `POST /v1/messages` 接口，可接入 Anthropic SDK、Claude Code 等兼容客户端。
- **开发工具接入**：支持配置 Codex CLI、Claude Code 使用本服务的兼容 API。
- **模型列表**：提供 `GET /v1/models` 接口，统一返回当前支持的基础模型及 Thinking 模型。
- **流式响应**：OpenAI 与 Anthropic 接口均支持流式和非流式输出。
- **多轮对话**：支持 System Prompt、user / assistant 历史消息以及多轮上下文拼接。
- **多模态输入**：支持 OpenAI 与 Anthropic 格式的 Base64 图片输入，并自动上传至 Claude.ai。
- **扩展思考**：模型名添加 `-thinking` 后缀即可启用扩展思考模式。
- **工具调用**：支持 OpenAI `tools / tool_calls`、Responses `function_call` 和 Anthropic `tools / tool_use`，兼容流式、非流式及工具结果回传。
- **长上下文处理**：提示词超过配置阈值时自动转换为文本附件，减少超长上下文直接提交造成的问题。
- **会话清理**：支持请求完成后自动删除 Claude.ai 上游会话。
- **密钥管理**：可在后台创建、查看和删除多个 API 密钥，用于号池、镜像和 API 接口鉴权。
- **在线测试**：后台内置多轮对话测试页面，支持选择模型、流式输出和图片上传。
- **调用日志**：记录调用时间、接口、模型、使用账号、响应状态、输入输出 Token、总耗时、首字延迟和 TPS。
- **日志详情**：可选保存完整请求与响应内容，支持查看详情、批量删除及仅保留最近指定数量的日志。
- **运行时配置**：可在后台调整出口代理、重试次数、长上下文阈值、会话清理、账号巡检和详细日志等设置。

## 效果展示

<table width="100%">
  <tr>
    <td width="50%"><img src="https://i.ibb.co/3yMvBm0R/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/zhpt12gG/image.png" alt="image" border="0"></td>
  </tr>
  <tr>
    <td width="50%"><img src="https://i.ibb.co/XZ4G85yp/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/LhxVLkYN/image.png" alt="image" border="0"></td>
  </tr>
  <tr>
    <td width="50%"><img src="https://i.ibb.co/99v19T70/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/ZpYzPkb9/image.png" alt="image" border="0"></td>
  </tr>
  <tr>
    <td width="50%"><img src="https://i.ibb.co/1Y0C0Nyq/image.png" alt="image" border="0"></td>
    <td width="50%"><img src="https://i.ibb.co/rK773GJR/image.png" alt="image" border="0"></td>
  </tr>
</table>

## 账号导入

进入管理后台的「账号管理」，点击「批量导入」，每行填写一个 `sessionKey`：

```text
sk-ant-sid01-xxxxxxxx
sk-ant-sid01-yyyyyyyy
```

导入任务会在后台执行，并自动查询账号信息。`sessionKey` 等同于账号登录凭据，请妥善保管。

### 支持的模型

当前服务暴露以下基础模型，并同时提供对应的 `-thinking` 版本：

- `claude-sonnet-4-6`
- `claude-haiku-4-5-20251001`
- `claude-sonnet-5`

实际可用性取决于账号权限和 Claude.ai 上游状态，请以 `GET /v1/models` 的返回结果为准。

<details>
<summary><code>GET /v1/models</code></summary>
<br>

获取当前服务暴露的模型列表：

```bash
curl http://localhost:8788/v1/models \
  -H "Authorization: Bearer <api-key>"
```

```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-sonnet-4-6",
      "object": "model",
      "created": 1750000000,
      "owned_by": "anthropic"
    }
  ]
}
```

</details>

<details>
<summary><code>POST /v1/chat/completions</code></summary>
<br>

OpenAI Chat Completions 兼容接口，支持多轮对话、图片输入以及流式输出。图片使用 Data URI：

```bash
curl http://localhost:8788/v1/chat/completions \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {
        "role": "system",
        "content": "回答尽量简短"
      },
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "描述这张图片"},
          {
            "type": "image_url",
            "image_url": {"url": "data:image/png;base64,iVBORw0KGgo..."}
          }
        ]
      }
    ],
    "stream": true
  }'
```

</details>

<details>
<summary><code>POST /v1/responses</code></summary>
<br>

OpenAI Responses API 兼容接口，`input` 支持字符串或消息数组：

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
          {
            "type": "input_image",
            "image_url": "data:image/png;base64,iVBORw0KGgo..."
          }
        ]
      }
    ],
    "stream": false
  }'
```

</details>

<details>
<summary><code>POST /v1/messages</code></summary>
<br>

Anthropic Messages 兼容接口，可用于接入支持自定义 Anthropic Base URL 的客户端：

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
            "source": {
              "type": "base64",
              "media_type": "image/png",
              "data": "iVBORw0KGgo..."
            }
          }
        ]
      }
    ],
    "stream": true
  }'
```

</details>

### 客户端接入

Codex CLI 可通过自定义 OpenAI Base URL 使用 `/v1/responses`，Claude Code 可通过自定义 Anthropic Base URL 使用 `/v1/messages`；两者均填写本服务地址和后台创建的 API Key 即可。

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=oyxe666/Claude2api&type=Date)](https://www.star-history.com/#oyxe666/Claude2api&Date)

## 社区支持

学 AI，上 L 站：[Linux.do](https://linux.do)

交流群：点击链接加入群聊【开源无限画布(2群)】：https://qm.qq.com/q/Shgvco7XEu
