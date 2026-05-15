# DFCHAT

> 一个面向团队的桌面端聊天 + 直播应用 · Electron + Go 单体后端

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey.svg)](https://dfchat.chat/#download)
[![Backend: Go](https://img.shields.io/badge/backend-Go%201.26-00ADD8.svg)](server/)
[![Frontend: Electron](https://img.shields.io/badge/frontend-Electron%2032-47848F.svg)](client/)

DFCHAT 把日常协作里的「聊天 + 语音视频 + 文件 + 直播」做在一个桌面客户端里。后端是单一 Go 二进制 + 主流开源依赖（Postgres / Redis / MinIO / SRS），方便自部署。

**🔗 官网 / 下载**：https://dfchat.chat

## 功能

| 模块 | 说明 |
|---|---|
| 💬 即时通讯 | 私聊 / 群组 / 频道 · 消息编辑 / 撤回 / 引用 / @ 提及 / 表情反应 / 置顶 / 已读 |
| 👥 好友与群组 | 好友请求 · 拉黑 · 群组邀请码 · 群权限 · 在线状态 |
| 📞 语音视频 | WebRTC 1v1 通话 · 屏幕共享（`replaceTrack`） |
| 📎 文件传输 | 直传 MinIO（presigned URL） · 缩略图 · 断点续传 |
| 🎥 直播 | OBS → RTMP 推流 → HLS 播放 · 实时弹幕（WebSocket） · DVR 录制 |
| 🔍 搜索 | 全局消息历史搜索（⌘K / Ctrl+K） |
| 🛡 账号安全 | 多设备会话管理 · 刷新令牌轮转 · 修改密码 · 注销账号 |
| 🎨 桌面体验 | 系统托盘 · 桌面通知 · 自动更新（electron-updater） · 暗色主题 |

## 项目结构

```
.
├── client/              # Electron + Vite + React + TS + Tailwind 客户端
│   ├── src/             # 渲染进程
│   ├── electron/        # 主进程 + preload
│   └── electron-builder.yml
├── server/              # Go 单体后端
│   ├── cmd/api/         # HTTP + WebSocket 入口
│   ├── internal/        # 业务域（user / chat / friends / live / ...）
│   └── pkg/             # 通用基础设施（auth / config / storage / ...）
├── migrations/          # golang-migrate SQL
├── deploy/              # docker-compose.prod.yml + nginx + SRS + 运维脚本
├── web/                 # 营销站（dfchat.chat 静态页）
└── docker-compose.yml   # 本地依赖：Postgres / Redis / MinIO / NATS
```

## 技术栈

- **客户端**：Electron 32 · React 18 · TypeScript · Vite · Zustand · Tailwind · HLS.js · lucide-react
- **服务端**：Go 1.26 · Gin · pgx/v5 · gorilla/websocket · minio-go · bcrypt
- **基础设施**：PostgreSQL 16 · Redis 7 · MinIO · NATS 2 · SRS 6（直播） · nginx · Let's Encrypt
- **运行时**：Docker Compose · distroless 镜像 · UFW

## 本地开发

需要：Go 1.26+ · Node 20+ · Docker

```bash
# 1. 起依赖
cp .env.example .env
docker compose up -d

# 2. 跑迁移
make migrate-up

# 3. 起后端（终端 1）
cd server && cp .env.example .env && go run ./cmd/api

# 4. 起客户端（终端 2）
cd client && npm install && npm run dev
```

打开 Electron 窗口 → 注册账号 → 登录。

## 部署到生产

```bash
# 在你自己的服务器上
bash deploy/setup-website.sh   # nginx + 静态站
bash deploy/setup-https.sh     # Let's Encrypt
bash deploy/setup-live.sh      # SRS 直播
bash deploy/setup-ops.sh       # 每日备份 + 证书自动续期
```

具体见 `deploy/` 下各脚本顶部注释。

## 打包客户端

```bash
cd client
npm run dist:mac     # macOS dmg（arm64 + x64，需要 mac 主机）
npm run dist:win     # Windows NSIS（需要 wine 容器或 Windows 主机）
npm run dist:linux   # Linux AppImage
```

详细说明见 [`docs/build.md`](docs/build.md)（待补）。

## 路线图

- [x] 阶段 1：用户 / 好友 / 群组 / 频道 / 私聊
- [x] 阶段 2：媒体（文件 / 图片 / 语音视频通话）
- [x] 阶段 3：直播（RTMP / HLS / 弹幕 / DVR）
- [ ] 阶段 4：群语音视频 · TURN 服务器 · 端到端加密
- [ ] 阶段 5：Windows 代码签名（SignPath.io）· macOS 公证

## 贡献

欢迎 issue 与 PR。提 PR 前请：
1. 确保 `go vet ./...` 和 `npm run build` 通过
2. 不要提交 `.env` / `server.rtf` / 其他凭据类文件（`.gitignore` 已配）
3. 大改动请先开 issue 讨论

## License

[MIT](LICENSE) © 2026 东方信息（Dongfang Information）
