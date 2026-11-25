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

最小结构（详见 [session.example.json](session.example.json)）：

```json
{
  "access_token": "eyJhbGciOi...",
  "account_id": "18bb1260-83ea-4f52-aea7-bd93683eeca3",
  "cookies": { "cf_clearance": "可选" },
  "build": "可选",
  "email": "you@example.com",
  "plan": "free"
}
```

只有 `access_token` 是必填的。`account_id`、邮箱、套餐和过期时间会自动从 JWT 里解析；`cookies` 通常也不需要，后端使用浏览器 TLS 指纹访问 ChatGPT。

获取 `access_token`：在已登录的浏览器里打开 `chatgpt.com`，按 F12 打开控制台执行：

```js
fetch('/api/auth/session').then(r => r.json()).then(s => console.log(JSON.stringify({
  access_token: s.accessToken,
  account_id: s.account?.id,
  email: s.user?.email,
  plan: s.account?.planType
}, null, 2)))
```

access token 通常有效期约 10 天。过期后直接替换 session 文件里的 `access_token` 即可，服务端会检测文件修改时间并自动重新读取，不需要重启。

> session 文件等同于账号密码，请勿提交到仓库或发给别人。`sessions/` 已加入 `.gitignore`。

## 功能

- 用户登录 / 退出，Cookie 会话，按 IP 限制连续登录失败次数。
- 按用户授权展示可用的 ChatGPT 账号标签。
- 拉取 ChatGPT 会话列表，支持搜索、加载更多、删除。
- 查看历史会话并继续对话。
- 新建会话、流式输出、停止生成、Markdown 渲染、代码块一键复制。
- 模型固定使用 `auto`，由 ChatGPT 自动选择，不提供选择器。
- 移动端侧栏抽屉。

## API

所有接口都在 `/api` 下，除 `POST /api/login` 和 `GET /api/health` 外都需要登录 Cookie。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/login` | 登录，body `{username,password}` |
| POST | `/api/logout` | 退出登录 |
| GET | `/api/me` | 当前用户和可用标签 |
| GET | `/api/tokens` | 账号标签及 session 就绪状态 |
| GET | `/api/conversations?tag=&offset=&limit=` | 会话列表 |
| GET | `/api/conversations/{id}?tag=` | 会话详情（已归一化为 user/assistant 消息） |
| DELETE | `/api/conversations/{id}?tag=` | 删除 ChatGPT 会话 |
| POST | `/api/chat` | 发送消息，返回 SSE 流 |

`POST /api/chat` 请求体：

```json
{
  "tag": "default",
  "conversation_id": "可选，新会话不传",
  "parent_message_id": "可选，续聊时传上一条消息 id",
  "message": "你好"
}
```

返回的 SSE 事件：

```text
data: {"type":"start"}
data: {"type":"delta","text":"你"}
data: {"type":"delta","text":"好"}
data: {"type":"done","conversation_id":"...","message_id":"...","text":"你好"}
```

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

- 只支持文本对话，暂不支持图片上传、语音、文件附件。
- 不支持编辑/重新生成消息。
- 会话标题由 ChatGPT 生成，刚建的新会话可能短暂显示为「新对话」。
- ChatGPT 网页接口和 PoW 算法随时可能调整；如果突然全部 403，需要重新对照网页端请求更新实现。
- 删除会话会同时从 ChatGPT 账号里删除，无法恢复。
