# GptWeb

一个前后端分离的 ChatGPT 网页客户端：Go 后端负责登录、账号配额和 ChatGPT 接口代理，React + Vite 前端提供类似 ChatGPT 的聊天界面。

- Go 后端只暴露 `/api`，其余路径都用于提供前端静态文件。
- 用户账号、可用 token 标签都写在 `config.json` 里。
- 每个 ChatGPT 登录态单独保存为一个 session 文件，`config.json` 只记录文件路径。
- 会话列表、历史消息、发消息、新建会话、删除会话都直接调用 ChatGPT 官方网页接口。
- 后端会处理 ChatGPT 的 PoW / sentinel 校验，并按浏览器 TLS 指纹发起请求。

> 本项目复用的是 ChatGPT 网页版私有接口，不是官方 OpenAI API。接口可能随时变化，也可能带来账号风控风险，请仅用于个人学习与自用。

## 快速开始

需要 Go 1.27+、Node 20+、Yarn 1.x。

```powershell
cd C:\Users\Administrator\file\go\gptweb\web
yarn install
yarn build
```

```powershell
cd C:\Users\Administrator\file\go\gptweb
go run . -config config.json
```

打开 `http://127.0.0.1:8080`，默认账号是 `admin` / `admin123`（**请先修改 `config.json`**）。

> Windows 下重新编译前请先停止正在运行的 `gptweb.exe`，否则 Go 可能把旧程序备份成 `gptweb.exe~`。

开发前端时可以开两个终端：

```powershell
# 终端 1：后端
go run . -config config.json
```

```powershell
# 终端 2：前端，Vite 会把 /api 代理到 8080
cd web
yarn dev
```

## 配置

`config.json`：

```json
{
  "listen": "127.0.0.1:8080",
  "web_dir": "./web/dist",
  "session_secret": "换成一段足够长的随机字符串",
  "session_hours": 168,
  "request_timeout_seconds": 300,
  "users": [
    { "username": "admin", "password": "admin123", "token_tags": ["default"] }
  ],
  "tokens": [
    { "tag": "default", "name": "默认账号", "session_file": "sessions/default.json" }
  ]
}
```

| 字段 | 说明 |
| --- | --- |
| `listen` | 监听地址，例如 `127.0.0.1:8080` 或 `0.0.0.0:8080` |
| `web_dir` | 前端构建产物目录，相对路径按配置文件所在目录解析 |
| `session_secret` | 登录 Cookie 的签名密钥，部署前务必修改 |
| `session_hours` | 登录有效期（小时） |
| `request_timeout_seconds` | 单次对话请求超时 |
| `users[].token_tags` | 该用户能使用的 token 标签 |
| `tokens[].tag` | 标签，用户配置引用它 |
| `tokens[].session_file` | ChatGPT 登录态文件路径 |

同一个用户可以配置多个标签，网页右上角/侧栏可以切换。不同用户看不到彼此没有权限的标签。

## session 文件

session 文件的格式就是 `https://chatgpt.com/api/auth/session` 的返回内容，**原样整段复制**进来即可（详见 [session.example.json](session.example.json)）：

```json
{
  "user": {
    "id": "user-xxxxxxxxxxxxxxxx",
    "name": "你的名字",
    "email": "you@example.com"
  },
  "expires": "2027-02-23T16:18:03.327Z",
  "accessToken": "eyJhbGciOi...",
  "authProvider": "google",
  "account": {
    "id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "planType": "free"
  }
}
```

必填只有 `accessToken`；`account.id`、`user.email`、套餐和过期时间会自动读取，缺失时也会从 JWT 里解析。文件必须是这个原始格式，不接受自定义的 snake_case 字段。

获取方法：登录 `chatgpt.com`，按 F12 打开控制台执行：

```js
fetch('/api/auth/session').then(r => r.json()).then(s => console.log(JSON.stringify(s, null, 2)))
```

把控制台输出的完整 JSON 保存为 `sessions/xxx.json` 即可。access token 通常有效期约 10 天；过期后替换文件里的 `accessToken`，服务端会检测文件修改时间并自动重新读取，不需要重启。

> session 文件等同于账号密码，请勿提交到仓库或发给别人。`sessions/` 已加入 `.gitignore`。

## 功能

- 用户登录 / 退出，Cookie 会话。
- 按用户授权展示可用的 ChatGPT 账号标签，支持多个 session 文件。
- 拉取 ChatGPT 会话列表，支持搜索、加载更多、删除。
- 查看历史会话并继续对话，历史里的图片和文件附件会一并展示。
- 新建会话、流式输出、停止生成、Markdown 渲染、数学公式（KaTeX）、代码块一键复制。
- content.parts 里的 GenUI 组件按类型渲染：柱状图/折线图/饼图/散点图（Recharts）、代码块、异步图片、链接、写作块；地图和其他未实现类型显示「未实现」。Mermaid 和其他代码块一样显示源码与语言名，不渲染图表。
- 支持图片和文件：点击回形针选择、拖拽到窗口、或直接粘贴图片；发送前可预览和移除。
- 单文件最大 20MB，一次最多 8 个附件；历史会话里的图片可查看，文件可下载。
- 模型固定使用 `auto`，由 ChatGPT 自动选择，不提供选择器。
- 移动端侧栏抽屉。

## API

所有接口都在 `/api` 下，除 `POST /api/login` 和 `GET /api/health` 外都需要登录 Cookie。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/login` | 登录，body `{username,password}` |
| POST | `/api/logout` | 退出登录 |
| GET | `/api/me` | 当前用户和可用标签 |
| GET | `/api/conversations?tag=&offset=&limit=` | 会话列表 |
| GET | `/api/conversations/{id}?tag=` | 会话详情（已归一化为 user/assistant 消息） |
| DELETE | `/api/conversations/{id}?tag=` | 删除 ChatGPT 会话 |
| POST | `/api/upload` | 上传图片/文件，multipart 字段 `tag`、`file` |
| GET | `/api/files/{id}?tag=` | 读取/下载 ChatGPT 文件，可带 `name`、`kind`、`conversation_id` |
| POST | `/api/chat` | 发送消息，返回 SSE 流 |

`POST /api/upload` 返回的附件对象可以直接放进聊天请求：

```json
{
  "id": "file_00000000...",
  "name": "test.png",
  "mime_type": "image/png",
  "size": 2367,
  "width": 320,
  "height": 180,
  "kind": "image"
}
```

`POST /api/chat` 请求体：

```json
{
  "tag": "default",
  "conversation_id": "可选，新会话不传",
  "parent_message_id": "可选，续聊时传上一条消息 id",
  "message": "这张图片里有什么？",
  "attachments": [
    {
      "id": "file_00000000...",
      "name": "test.png",
      "mime_type": "image/png",
      "size": 2367,
      "width": 320,
      "height": 180,
      "kind": "image"
    }
  ]
}
```

只发附件、不写文字也可以（`message` 为空、`attachments` 非空）。

返回的 SSE 事件：

```text
data: {"type":"start"}
data: {"type":"delta","text":"你"}
data: {"type":"delta","text":"好"}
data: {"type":"genui","components":[...]}
data: {"type":"replace","text":"完整替换文本（content.parts 被覆盖时会用到）"}
data: {"type":"done","conversation_id":"...","message_id":"...","text":"你好","genui":[...]}
```

`genui` 事件是流式快照：上游 `genui_components` 出现或 `end_index` 变化时会更新组件，可能暂时没有完整 data。`done` 仍然带最终完整数据。

`genui` 数组与历史接口的 `message.genui` 相同，每项包含：

```json
{
  "type": "charts_widget_v2",
  "start_index": 20,
  "end_index": 248,
  "data": {
    "chartType": "bar",
    "xKey": "month",
    "series": [{"dataKey": "sales", "label": "销售额", "valuePrefix": "¥"}],
    "data": [{"month": "1月", "sales": 120}],
    "description": "示例数据"
  }
}
```

`text` 始终是原封不动的 content.parts。`start_index` / `end_index` 也原样透传（上游是 Unicode 码点偏移）；前端删除该区间并插入组件，JS 切片前会先转换成 UTF-16 下标。后端只发送组件渲染需要的最小 `data`，不再发送 `safe_urls`、完整 `resolved` 等冗余字段：

- `code_block`：`{ language, code }`
- `writing_block`：`{ subject, variant, body }`
- `async_image`：`{ query, aspectRatio, width, maxWidth, images: [{ thumbnail_url, content_url, url, title }] }`
- `link`：`{ url, title }`
- `charts_widget_v2`：`{ chartType, data, xKey/nameKey/valueKey, series, description }`
- `map_widget_v2`：不带 data，前端显示「未实现」
- DIL 交互块（`{@body ...}`、`{#if}` 或包含交互标签的顶层 `box/row/grid`）由后端识别为整块 `dil_block`，前端整块显示「未实现」；块内嵌套的顶层 GenUI 也会一起隐藏

前端按类型选择实现，未实现的类型显示「未实现」。

出错时返回 `{"type":"error","message":"..."}`。

## 目录结构

```text
gptweb/
├── main.go                 入口
├── config.json             运行配置（含用户和 token 标签）
├── session.example.json    session 文件示例
├── sessions/               为每个 token 保存 ChatGPT 登录态（不要提交）
├── internal/
│   ├── config/             配置加载与校验
│   ├── chatgpt/            ChatGPT 接口、PoW、sentinel、SSE、会话归一化
│   └── server/             登录、Cookie 会话、API 与静态文件服务
└── web/                    React + Vite + TypeScript 前端
```

## 安全建议

- 修改默认密码和 `session_secret`。
- 不要把服务直接暴露在公网；需要远程访问时放在 HTTPS 反向代理后面，并限制来源。
- 密码以明文写在配置文件里，注意文件权限。
- session 文件包含 access token，泄露等同于账号泄露。

## 已知限制

- 支持图片和常见文档，不支持语音、视频和实时通话。
- 当前实现的 GenUI 类型：`charts_widget_v2`（bar/line/pie/scatter）、`code_block`、`async_image`、`link`、`writing_block`。`map_widget_v2`、`entity`、`async_image_group` 及未知类型显示「未实现」；卡片布局、交互控件、动态数据和 AppBlock 只保留文本。Mermaid 不渲染为图，只按普通代码块显示源码和语言名。
- 单文件上限 20MB，一次最多 8 个附件。
- 不支持编辑/重新生成消息。
- 会话标题由 ChatGPT 生成，刚建的新会话可能短暂显示为「新对话」。
- ChatGPT 网页接口和 PoW 算法随时可能调整；如果突然全部 403，需要重新对照网页端请求更新实现。
- 删除会话会同时从 ChatGPT 账号里删除，无法恢复。
