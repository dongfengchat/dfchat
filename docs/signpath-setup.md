# 通过 SignPath.io 给 Windows 安装器免费代码签名

[SignPath.io](https://signpath.io) 对**公开 GitHub 项目**提供免费的 OV 级 Windows 代码签名服务（"Open Source Plan"）。签完的 `.exe` 在 Windows 上**完全不再弹 SmartScreen 警告**。

整个流程一次配置，之后每次 push tag 自动签名。预计审核 + 接入需要 **1–2 周**。

---

## 前置条件

- [x] GitHub 仓库已公开
- [x] 仓库根目录有 `LICENSE`（MIT/Apache-2.0/GPL 等 OSI 认证开源协议）
- [x] 仓库根目录有 `README.md` 描述项目
- [x] `.github/workflows/release.yml` 已能产生 `.exe` 产物（已配置）

---

## 第 1 步：注册 SignPath 账号

1. 访问 https://signpath.io/signup
2. 用 **GitHub 账号登录**
3. 同意服务条款

## 第 2 步：申请 Open Source Plan

1. 进入 Dashboard → 顶部菜单选 **"Subscribe to a Plan"**
2. 选 **"Open Source Plan"**（免费）
3. 填写申请表：
   - **Project name**: `DFCHAT`
   - **Repository URL**: `https://github.com/<你的用户名>/dfchat`
   - **Description**: "Desktop chat + live streaming app for teams. Electron client + Go backend."
   - **License**: MIT
   - **Why is this open source?**: 简要说明（一两句）
4. 等审核 — 通常 3–7 个工作日，邮件通知

## 第 3 步：创建 Organization 和 Project

审核通过后：

1. Dashboard → **Create Organization** → 命名 `dongfang`（或任意 slug）
2. 进入 organization → **Projects → Add Project**
   - **Slug**: `dfchat`
   - **Name**: DFCHAT
   - 连接到上一步申请通过的 OSS Plan
3. 进入 project → **Artifact Configurations → Add**
   - **Slug**: `dfchat-exe`
   - **Type**: `Windows Application`
   - 上传一个示例 `.exe`（用 GitHub Actions 跑出来的随便一个）
4. 进入 project → **Signing Policies → Add**
   - **Slug**: `release-signing`
   - **Allowed artifact configurations**: 勾选 `dfchat-exe`
   - **Approval mode**: `Automatic` (Open Source Plan 自动批准)

## 第 4 步：在 GitHub 创建 Action 接入 Token

1. SignPath → User Profile → **API Tokens → Create**
   - **Name**: `github-actions`
   - **Scope**: 选择刚才创建的 organization
   - **Lifetime**: 1 年（到期前提醒续期）
2. 复制生成的 token（只显示一次！）

## 第 5 步：在 GitHub 仓库配置 Secrets

GitHub 仓库 → **Settings → Secrets and variables → Actions → New repository secret**：

| Secret 名 | 值 |
|---|---|
| `SIGNPATH_API_TOKEN` | 第 4 步复制的 token |
| `SIGNPATH_ORGANIZATION_ID` | SignPath organization 的 UUID（在 organization Settings 页能看到） |
| `SIGNPATH_PROJECT_SLUG` | `dfchat`（第 3 步设的） |
| `SIGNPATH_SIGNING_POLICY_SLUG` | `release-signing`（第 3 步设的） |

## 第 6 步：启用 release.yml 里的签名 job

打开 `.github/workflows/release.yml`，找到底部 `# === Optional: Sign Windows .exe ===` 部分，**去掉所有 `#` 注释**，提交。

第一次手动跑 workflow（GitHub Actions → Run workflow）验证。成功后：
- 后续推 `v1.0.0` 这种 tag 自动触发
- 整条链路：CI build → 上传 unsigned exe 到 release → 提交给 SignPath → SignPath 签名 → 覆盖回 release

签名完成后下载 `.exe`，右键 → 属性 → 数字签名标签页应能看到 "Open Source Developer, <你的姓名>"。

---

## 验证签名是否生效

PowerShell：
```powershell
Get-AuthenticodeSignature .\DFCHAT-Setup-1.0.0.exe
```

应输出：
```
SignerCertificate     :  [Subject]   CN=Open Source Developer, <name> ...
Status                : Valid
```

在干净 Windows 测试机（最好不是开发机）下载并双击 — SmartScreen 蓝框应该**直接不出现**。

---

## 故障排查

**Q: 申请被拒**
A: 检查 LICENSE 是 OSI 认证的（MIT/Apache/GPL/BSD/MPL 等都行）。如果项目核心代码 < 1000 行或明显是商业产品 fork，可能被认为不符合 OSS。补充说明再申请。

**Q: 签名 action 报 "artifact not found"**
A: workflow 里 `github-artifact-id` 需要在 build job 里上传到 GitHub Actions Artifacts（不是 Release），再传 ID 给 SignPath。详见 [SignPath GitHub Action 文档](https://github.com/SignPath/github-action-submit-signing-request)。

**Q: 签了之后 SmartScreen 还报警告**
A: SmartScreen 有"声誉"系统 — 新签名证书首次下载量小的时候仍会警告。一般几百次下载后才"信誉建立"。商业 EV 证书无此问题（但贵）。

---

## macOS 签名（参考）

Apple 没有针对开源项目的免费方案。要消除 macOS Gatekeeper 警告需要 **Apple Developer Program**（$99/年），然后用 `xcrun notarytool` 公证。流程见 [Apple 官方文档](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)。

当前仓库已做 **ad-hoc 签名**（`client/build/afterPack.cjs`），保证 Apple Silicon kernel 不拒绝执行，但首次启动 Gatekeeper 仍会弹一次 "无法验证开发者" — 用户右键 → 打开即可。
