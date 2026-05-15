# Security Policy

## 支持的版本

DFCHAT 还在 0.1.x 早期阶段，安全修复只针对**最新发布的版本**。

| 版本 | 安全更新 |
|---|---|
| 最新 0.1.x | ✅ |
| 更早 0.1.x | ❌ |

## 报告漏洞

**请不要在公开 GitHub Issue 里披露漏洞** —— 这会让攻击者在补丁发布前抢先利用。

发邮件到 **dongfengchat@gmail.com**，邮件标题以 `[security]` 开头。请附：

1. 漏洞类型（XSS / SQLi / RCE / 鉴权绕过 / WebSocket 风险 / 等）
2. 受影响组件（客户端 / 服务端 / 哪个 endpoint）
3. 复现步骤 + 必要时 PoC
4. 你认为可能的影响范围
5. 你希望被致谢的方式（GitHub handle / 真名 / 匿名）

## 响应承诺

| 阶段 | 时限 |
|---|---|
| 收到回执 | 48 小时内 |
| 初步影响评估 | 5 个工作日内 |
| 修复 + 发版（高危） | 14 天内 |
| 修复 + 发版（中低危） | 30 天内 |
| 公开披露 | 修复发布 + 30 天后 |

## 安全相关基础设施

DFCHAT 已实施的安全控制（可帮助报告者快速定位是否已缓解）：

- ✅ **HTTPS only** —— 所有流量走 TLS，HSTS `max-age=63072000; preload`
- ✅ **HTTP 安全头** —— X-Frame-Options / X-Content-Type-Options / Referrer-Policy / Permissions-Policy
- ✅ **JWT access + 刷新令牌** —— Access TTL 2 小时，refresh 30 天，DB 持久化可强制下线
- ✅ **Bcrypt cost 12** —— 密码哈希
- ✅ **per-IP 限流** —— 30 r/s 稳态 / 60 burst
- ✅ **审计日志** —— Admin 操作 / 直播管理 / 用户状态变更全部入 `audit_logs`
- ✅ **WebSocket 心跳** —— 30s ping，60s 无 pong 剔除
- ✅ **MinIO 内网化** —— 仅 nginx 反代暴露，9000 端口已关
- ✅ **TURN 凭证 HMAC-SHA1** —— 不在客户端暴露 static-auth-secret
- ✅ **WS 弹幕禁言检查** —— 服务端持久化禁言列表
- ✅ **邮箱重置令牌** —— 60 分钟 TTL + 单次使用 + 重置后失效所有 refresh tokens
- ❌ **端到端加密** —— **未实现**，消息在服务端可被管理员读取
- ❌ **2FA / TOTP** —— **未实现**
- ❌ **签名安装包**：
  - Windows：等 SignPath OSS 审核
  - macOS：仅 ad-hoc 签名（无 Apple Developer ID）

## 范围

**在 scope 内**：
- 主仓库 `dongfengchat/dfchat` 内的代码
- https://dfchat.chat 和 https://app.dfchat.chat 部署
- 客户端二进制（dmg / exe / AppImage）

**不在 scope 内**：
- 第三方依赖的漏洞（请直接报给上游）
- 物理访问 / 社工 / DDoS
- 仅在你完全控制的环境下复现的问题

## 致谢

经过验证的报告者会被列入 `SECURITY-HALL-OF-FAME.md`（当文件存在时）。
