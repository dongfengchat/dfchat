# 把 DFCHAT 推到 GitHub

本地 git 已 init + commit。剩下的只需要在 GitHub 创建空仓库 + 推送。

---

## 1. 在 GitHub 创建空仓库

1. 浏览器打开 https://github.com/new
2. **Owner**: 你的用户名（或组织）
3. **Repository name**: `dfchat`（或你想要的名字）
4. **Description**: `Desktop chat + live streaming app · Electron + Go`
5. **Public**（关键 —— SignPath OSS 免费签名要求公开）
6. **DON'T** check "Add a README / .gitignore / license" — 我们本地已经有了，否则会有 merge conflict
7. 点 Create repository

复制 GitHub 给你的 URL（HTTPS 或 SSH）：
- HTTPS: `https://github.com/<your-name>/dfchat.git`
- SSH: `git@github.com:<your-name>/dfchat.git`

---

## 2. 本地添加 remote + 推送

终端在 `/Users/york/Documents/DFCHAT` 目录跑：

```bash
# HTTPS 方式（首次会提示登录，用 Personal Access Token 当密码）
git remote add origin https://github.com/<your-name>/dfchat.git
git branch -M main
git push -u origin main

# 或者 SSH 方式（前提：你机器上已配 SSH key 到 GitHub）
git remote add origin git@github.com:<your-name>/dfchat.git
git branch -M main
git push -u origin main
```

如果用 HTTPS 需要 token：GitHub 右上头像 → **Settings → Developer settings → Personal access tokens → Tokens (classic) → Generate**，勾 `repo` scope，复制 token 当作密码贴。

---

## 3. 上 GitHub 后立即做这几件

### 3a. 验证密钥没泄露

GitHub 右上 **secret scanning** 会自动扫码库里的 token / key / 密码。等几分钟看：
- Repo → Security → Secret scanning alerts
- 应该是空的（我们已经把 `vzpJCZ57` 等敏感字符串从所有 `deploy/*.sh` 移除，强制走 env var）

如果你之前 push 过含密码的 commit 想"完全洗白历史"，需要 `git filter-repo` 或 BFG，但当前是首次 push 所以历史是干净的。

### 3b. 添加 GitHub topics + 描述

Repo 主页 → 右上 ⚙️ → 添加：
- **About**: `Desktop chat + live streaming · Electron + Go · Self-hostable Discord/Twitch alternative`
- **Topics**: `electron` `react` `golang` `live-streaming` `webrtc` `hls` `srs` `chat` `desktop-app` `self-hosted`
- **Website**: `https://dfchat.chat`

### 3c. 设默认分支保护

Settings → Branches → Add rule for `main`:
- ✅ Require pull request before merging
- ✅ Require status checks（先不勾，等 CI 跑起来再加）

### 3d. CI Actions 启用

Push 后 Actions tab 应该自动发现 `.github/workflows/release.yml`。它配的是：tag `v*.*.*` 时触发，自动 build Mac/Win/Linux 三平台 + 上传到 GitHub Release。

测一下：
```bash
git tag v0.1.14 -m "Bootstrap release on GitHub"
git push origin v0.1.14
```

Actions 跑完会创建 GitHub Release 并附 3 个安装包（dmg + exe + AppImage）。

---

## 4. 接 SignPath 拿 Windows 免费签名

GitHub 公开后，按 `docs/signpath-setup.md` 申请。审核约 1–2 周，通过后：
- 每次 push tag → Actions 跑 build → 自动 POST 到 SignPath → SignPath 签好 .exe → 写回 Release 资源
- **Windows 用户下载新版后不再有 SmartScreen 蓝框警告**

---

## 5. 后续日常发版

仓库就位后两条路二选一：

**A. 继续本地 release（不依赖 GitHub Actions）**
```bash
bash deploy/release.sh 0.1.15
```
本地 build mac + 远程 build win/linux → 上传到自家服务器 + 刷 latest.json。GitHub repo 只用来：开源展示 + 接 SignPath 签名 + 别人贡献 PR。

**B. 改用 GitHub Actions 三平台 build**
```bash
git tag v0.1.15 -m "..."
git push origin v0.1.15
```
完全用 CI 出包。优点：不依赖本地 macOS 机器，PR 触发也能 build。

两种可以并存——A 用于日常迭代，B 用于正式 release。

---

## 6. 小提醒

- **Production secrets** 永远在 `deploy/.env.prod`（已被 `.gitignore`），git 看不到
- **SSH 密码** 已从所有脚本里去掉，跑脚本前要 `export DFCHAT_PASSWORD=xxx`
- **server.rtf**（含原始账号）已被 `.gitignore` 排除
- `.claude/` 也被排除，不会泄露 Claude Code 历史命令
- 任何时候想验证当前会 push 什么：`git ls-files | grep -i "secret\|password\|env\|token"` 应该空
