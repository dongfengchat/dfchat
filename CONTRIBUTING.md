# Contributing to DFCHAT

感谢你考虑为 DFCHAT 贡献代码！本指南帮你快速上手 + 顺利通过 review。

## 行为准则

参与本项目即同意遵守 [Code of Conduct](CODE_OF_CONDUCT.md)。简而言之：互相尊重、就事论事。

## 你能贡献什么

| 类型 | 怎么开始 |
|---|---|
| 🐛 Bug 报告 | 走 [Issues](https://github.com/dongfengchat/dfchat/issues/new/choose) → "Bug report" 模板 |
| 💡 功能建议 | "Feature request" 模板，**先开 issue 讨论**再写代码 |
| 📝 文档修复 | 直接 PR，无需开 issue |
| 🌐 翻译 / i18n | 当前全中文 hardcoded，i18n 改造欢迎 PR |
| 🧪 测试用例 | 后端 Go 测试 + 客户端 e2e 都很缺 |
| 🎨 UI 优化 | 截图 → 开 issue 讨论 → PR |

## 本地开发环境

需要：
- **Go 1.26+**
- **Node 20+**
- **Docker**（起 Postgres / Redis / MinIO / NATS / SRS）

```bash
# 一次性 setup
git clone https://github.com/dongfengchat/dfchat.git
cd dfchat
cp .env.example .env
cp server/.env.example server/.env
cp client/.env.example client/.env

# 起本地依赖
docker compose up -d

# 跑迁移
make migrate-up

# 起后端（终端 1）
cd server && go run ./cmd/api

# 起客户端（终端 2）
cd client && npm install && npm run dev
```

打开 Electron 窗口 → 注册账号 → 看到登录页面 = 一切就绪。

## 项目布局

```
client/             Electron + React + TS + Vite
  electron/         主进程 + preload
  src/              渲染进程
    pages/          路由页面（Home / Login / Live / Admin / Settings ...）
    components/     可复用组件
    store/          Zustand store
    api/            HTTP / WS 客户端
    notify/         桌面通知 + 应用内 toast + 音效

server/             Go 单体后端
  cmd/api/          HTTP + WebSocket 入口
  internal/         业务域
    auth/           注册 / 登录 / 邮箱验证 / 找回密码
    user/           用户资料 / 会话
    friend/         好友请求
    group/          群组 / 角色 / 通知偏好
    channel/        群内频道
    message/        消息 CRUD + reaction + pin + read
    file/           MinIO 直传 + 群文件中心
    live/           直播房间 / 弹幕 / 关注 / 录制
    realtime/       WebSocket 网关
    admin/          管理员后台
    turn/           coturn 凭证签发
  pkg/              基础设施
    auth/           JWT + bcrypt
    config/         env loading
    db/             pgx pool
    storage/        MinIO client
    mailer/         SMTP (SMTP_HOST 空 = dev log fallback)
    health/         深度健康检查
    audit/          审计日志
    middleware/     CORS + RequireAuth + RateLimit
    wsbus/          per-user WebSocket fan-out

migrations/         golang-migrate SQL (000001-000015)
deploy/             生产部署：docker-compose + nginx + SRS + cron 脚本
web/                营销站 dfchat.chat（纯静态 HTML/CSS）
docs/               开发文档（SignPath 接入指南等）
```

## 提交规范

### Commit message

用 [Conventional Commits](https://www.conventionalcommits.org/) 格式：

```
type(scope): short subject

Optional body explaining "why", wrapping at 72 chars.

Refs #123
```

常用 type：
- `feat` 新功能
- `fix` Bug 修复
- `refactor` 重构（不改行为）
- `perf` 性能优化
- `docs` 文档
- `test` 测试
- `ci` CI/CD 配置
- `chore` 杂项（依赖升级、版本 bump 等）

例：
```
feat(live): support 1080p quality with bandwidth consent
fix(notify): mention badge not clearing after switching conv
```

### PR 流程

1. **先开 issue 讨论**（除非是文档 / 小修复）
2. Fork → 新建 branch（**不要直接动 main**）
3. 跑通本地：
   ```bash
   cd server && go vet ./... && go build ./...
   cd client  && npm run build
   ```
4. PR 标题用同样的 Conventional Commits 风格
5. 描述写清楚：解决什么问题 / 怎么验证 / 截图（如有 UI 改动）
6. 链接相关 issue：`Closes #N` / `Refs #N`

## 代码风格

- **Go**：`gofmt` + `go vet` 默认严格，强烈建议本地装 `golangci-lint`
- **TypeScript**：tsconfig 已严格，禁止 `any` 泄露（除非有充分理由）
- **SQL migration**：每个 `up` 必须有对应 `down`；不要修改已发布的 migration
- **API endpoint**：参考 `server/internal/*/handler.go` 的错误码风格（`code: 80012` + 中文 message）
- **WS event**：`type` 用 dot-notation（`chat.recv` / `live.host.golive`）

## 安全相关贡献

请走 [SECURITY.md](SECURITY.md) 的私下报告渠道，**不要在公开 issue 中披露漏洞**。

## License

提 PR 默认以 [MIT](LICENSE) 授权给本仓库。
