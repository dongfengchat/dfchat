# 通过 SignPath 给 Windows 安装器免费代码签名

[SignPath Foundation](https://signpath.org)（非营利基金会）通过 [SignPath.io](https://signpath.io)（商业产品）对**符合条件的开源项目**提供免费 OV 级 Windows 代码签名。签完的 `.exe` 在 Windows 上**完全不再弹 SmartScreen 警告**。

> ⚠️ **重要更正**：之前我让你去 `signpath.io/signup` 用 GitHub 登录是**错的**——
> - SignPath**没有公开自助注册**，更**没有 GitHub OAuth 登录**
> - 申请必须先填 https://signpath.org/apply 上的**表单**（HubSpot 表单，邮箱提交）
> - 基金会审核通过后，他们**人工**给你开一个 signpath.io 账号
> - **登录方式：Google ✅ / Microsoft ✅ / Okta 用户名密码 ✅，但不支持 GitHub**
> - 你之前看到"不支持 GitHub"是正常的——SignPath 用 Okta 做身份认证，集成的是 Google + Microsoft，没集成 GitHub

> 💡 **关键技巧**：你**用哪个登录方式**由你申请时填的邮箱决定：
> - 填 `@gmail.com` / Google Workspace → 后续用 **Google 登录** ✅（最省事）
> - 填 `@outlook.com` / `@hotmail.com` / 公司 Azure AD → 用 **Microsoft 登录** ✅
> - 填普通邮箱（如 `@163.com`、`@qq.com`） → 用 Okta 设的密码登录
>
> **推荐**：用 Gmail 申请。审核通过后直接 Google 登录，不用额外注册 Microsoft 账号。

预计审核 + 接入 **2–4 周**（基金会人工审核较慢）。

---

## 前置条件

- [x] GitHub 仓库已公开（`https://github.com/yorkfangyx/dfchat`）
- [x] 仓库根目录有 `LICENSE` —— **必须是 OSI 认证的开源协议**（MIT/Apache-2.0/GPL/BSD/MPL 等）
- [x] 仓库根目录有 `README.md` 描述项目
- [x] 项目有"实际维护"的证据（commit 历史 + 至少一个 release）
- [x] 没有"双重许可"（不能既 OSS 又卖商业版）
- [x] 软件不是渗透测试/安全攻击工具（明文禁止）
- [x] `.github/workflows/release.yml` 已能产生 `.exe` 产物（已配置）

---

## 第 1 步：提交申请表（核心步骤）

1. 访问 https://signpath.org/apply
2. 填写 HubSpot 表单：
   - **Email**：你的工作邮箱
   - **Project name**：`DFCHAT`
   - **Project URL**：`https://github.com/yorkfangyx/dfchat`
   - **License**：MIT（或你实际用的协议）
   - **Description**：建议填英文，例如：
     ```
     DFCHAT is a self-hosted desktop chat + live streaming application
     for small teams, combining Discord-like text/voice channels with
     Twitch-like RTMP→HLS streaming. Client is Electron + React, backend
     is Go + Postgres. We need Windows code signing because end users see
     SmartScreen warnings on the installer and many won't proceed.
     ```
   - **Why open source?**：一两句说明
3. **提交** → 等基金会邮件
4. 通常 1–3 周收到第一封回复。可能要求你补充材料（项目活跃度、团队规模、商业关系等）

> 💡 提高通过率：
> - 在 README 顶部加 badge：commit 频率、release 版本、star 数（一开始 star 少也可以，关键是维护活跃）
> - README 写清楚是"自托管开源软件"，没有商业版本
> - 在 GitHub About 里写明 license

---

## 第 2 步：基金会审核通过后

收到 "Welcome to SignPath Foundation" 邮件后：

1. 邮件里有一个 https://app.signpath.io 的邀请链接
2. 点击 → 登录页面会显示三个选项：
   - **Continue with Google**（如果你的申请邮箱是 Gmail / Google Workspace，点这个）
   - **Continue with Microsoft**（如果是 outlook / hotmail / 企业 Azure AD）
   - **Sign in with username/password**（Okta 自带的本地账号，普通邮箱用这个）
3. 登录方式必须与申请时填的邮箱**匹配**，否则进不去这个邀请——
   例：申请填 gmail，结果点 Microsoft 登录就会进入另一个无关账号
4. 首次登录后进入基金会预先为你创建的 organization

## 第 3 步：配置 Project 和 Signing Policy

进入 organization 后：

1. **Projects → Add Project**
   - **Slug**: `dfchat`
   - **Name**: DFCHAT
2. 进入 project → **Artifact Configurations → Add**
   - **Slug**: `dfchat-installer`
   - **Type**: `Windows Application`
   - 上传一个示例 `.exe`（从 GitHub Actions release 下载一个）
3. 进入 project → **Signing Policies → Add**
   - **Slug**: `release-signing`
   - **Allowed artifact configurations**: 勾选 `dfchat-installer`
   - **Approval mode**: `Automatic`（OSS Plan 通常允许自动批准；如不允许就选 Manual，每次发版手动点一下）

## 第 4 步：创建 GitHub Actions 接入 Token

1. SignPath → 右上角头像 → **User Profile → API Tokens → Create**
   - **Name**: `github-actions-dfchat`
   - **Scope**: 限定到你的 organization
   - **Lifetime**: 1 年（到期前会邮件提醒）
2. 复制生成的 token（**只显示一次**）

## 第 5 步：配置 GitHub Secrets

GitHub 仓库 → **Settings → Secrets and variables → Actions → New repository secret**：

| Secret 名 | 值 | 在哪查 |
|---|---|---|
| `SIGNPATH_API_TOKEN` | 第 4 步复制的 token | 创建时显示 |
| `SIGNPATH_ORGANIZATION_ID` | organization 的 UUID | SignPath → Organization Settings → 顶部 ID 字段 |
| `SIGNPATH_PROJECT_SLUG` | `dfchat` | 你在第 3 步设的 |
| `SIGNPATH_SIGNING_POLICY_SLUG` | `release-signing` | 你在第 3 步设的 |

## 第 6 步：启用 release.yml 中的签名 job

打开 `.github/workflows/release.yml`，找到底部注释掉的 `Sign Windows .exe` 段：

```yaml
# === Optional: Sign Windows .exe via SignPath ===
# - name: Submit Windows installer to SignPath
#   uses: signpath/github-action-submit-signing-request@v1
#   with:
#     api-token: ${{ secrets.SIGNPATH_API_TOKEN }}
#     organization-id: ${{ secrets.SIGNPATH_ORGANIZATION_ID }}
#     project-slug: ${{ secrets.SIGNPATH_PROJECT_SLUG }}
#     signing-policy-slug: ${{ secrets.SIGNPATH_SIGNING_POLICY_SLUG }}
#     github-artifact-id: ${{ steps.upload-windows.outputs.artifact-id }}
#     wait-for-completion: true
#     output-artifact-directory: dist-signed/
```

去掉所有 `#`，提交。

> ⚠️ build job 要确保 unsigned `.exe` 上传到 **GitHub Actions Artifacts**（不是直接传 release），这样 SignPath action 才能下载。如果你的 workflow 当前是直接传 release，需要先改成 actions/upload-artifact，等签完再 actions/upload-release-asset。

## 第 7 步：手动跑一次验证

GitHub → Actions → 选 release workflow → **Run workflow**

成功后下载产物 `.exe`，PowerShell 验证：

```powershell
Get-AuthenticodeSignature .\DFCHAT-Setup-1.0.0.exe
```

应输出：
```
SignerCertificate : [Subject] CN=Open Source Developer, York Fang ...
Status            : Valid
```

在干净 Windows 测试机下载 + 双击 — SmartScreen 蓝框应**直接不出现**。

---

## 常见问题

### Q: 申请要等多久？
A: 基金会人少（基本是志愿者+几个员工）。我看过的项目数据：最快 5 天，最慢 6 周。**催促没用**，发了表就耐心等邮件。

### Q: 申请被拒怎么办？
常见拒因和对策：
- **License 不是 OSI 认证** → 换成 MIT 或 Apache-2.0
- **项目太小 / 太新** → 等积累 50+ star 和 10+ commits 再申请
- **包含商业组件** → 把任何非开源依赖移除
- **可被识别为"安全工具"** → 描述里强调"通信"，避免 monitor/sniff/intercept 等词
- **maintainer 信息不全** → README 里加上你的真名 + 邮箱 + GitHub

### Q: 不想等基金会，有更快的方案吗？
有，但都要花钱：
| 方案 | 价格 | 时长 |
|---|---|---|
| **SignPath Trial Plan** | $0（30 天试用） | 立即 |
| **Azure Trusted Signing** | ~$120/月 | 1–3 天 |
| **DigiCert OV** 商业证书 | ~$300/年 | 1–2 周 |
| **DigiCert EV** 商业证书（无 SmartScreen 警告） | ~$700/年 | 2–3 周 + 硬件 token 寄送 |

如果你急用，建议先开 SignPath Trial（30 天免费），跑通流程，期间等 Foundation 审核结果。

### Q: 签了之后 SmartScreen 还报警告？
A: SmartScreen 有"声誉"系统 — 新签名证书首次下载量小的时候仍会有黄框警告（"Windows protected your PC"，但用户能点"更多信息 → 仍要运行"）。一般要积累几百次下载 + 几周时间才"信誉建立"。商业 EV 证书无此问题（但贵 10 倍）。

---

## macOS 签名（参考）

Apple **没有针对开源项目的免费方案**。要消除 macOS Gatekeeper 警告需要 **Apple Developer Program**（$99/年），然后用 `xcrun notarytool` 公证。流程见 [Apple 官方文档](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)。

当前仓库已做 **ad-hoc 签名**（`client/build/afterPack.cjs`），保证 Apple Silicon kernel 不拒绝执行，但首次启动 Gatekeeper 仍会弹一次 "无法验证开发者" — 用户右键 → 打开即可。

---

## Linux 签名（参考）

AppImage 通常**不需要签名**就能跑。如果未来上 Snap Store 或 Flathub，他们各自有审核流程，不是代码签名。
