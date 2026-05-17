package auth

import (
	"fmt"
	"html"
	"strings"
)

// HTML email templates for verification / password reset / email change.
//
// Design notes:
//   - Inline styles. Gmail / Outlook / QQ Mail strip <style> blocks
//     unpredictably; inline is the lowest common denominator.
//   - Table-based layout. Outlook on Windows still uses Word's renderer
//     and doesn't understand flexbox / grid.
//   - Max width 560px centered; mobile clients fluid below that.
//   - Brand colour #4869f7 (matches the in-app brand-500) with a
//     darker background card on a near-black wrapper — matches the
//     landing pages so users see one consistent look across mail +
//     web + Electron.
//   - All dynamic content is HTML-escaped via html.EscapeString.

type emailKind string

const (
	emailVerify       emailKind = "verify"
	emailPasswordReset emailKind = "reset"
	emailEmailChange  emailKind = "change"
)

// renderEmailHTML produces the HTML body for one of the three mail
// types. ctaURL is the click-through link; expiryHint is the human
// "1 小时内有效" / "24 小时内有效" string we want to show next to the
// button.
func renderEmailHTML(kind emailKind, ctaURL, expiryHint string) string {
	var headline, bodyCopy, ctaText, footnote string
	switch kind {
	case emailVerify:
		headline = "验证你的 DFCHAT 邮箱"
		bodyCopy = "感谢注册 DFCHAT。点击下方按钮验证这个邮箱，以便后续接收找回密码 / 邮箱变更等重要通知。"
		ctaText = "立即验证邮箱"
		footnote = "如果不是你本人操作，请忽略此邮件 — 没有人能在没有验证链接的情况下使用你的邮箱注册账号。"
	case emailPasswordReset:
		headline = "重置 DFCHAT 密码"
		bodyCopy = "收到了重置你 DFCHAT 账号密码的请求。点击下方按钮设置新密码。"
		ctaText = "重置密码"
		footnote = "如果不是你本人操作，请忽略此邮件 — 你的当前密码不会被改变。"
	case emailEmailChange:
		headline = "确认你的 DFCHAT 新邮箱"
		bodyCopy = "收到了把 DFCHAT 账号的注册邮箱改成这个地址的请求。点击下方按钮确认变更。"
		ctaText = "确认变更"
		footnote = "如果不是你本人操作，请忽略此邮件 — 账号当前邮箱不会被改变。"
	}

	return fmt.Sprintf(emailShell,
		html.EscapeString(headline),                 // <title>
		html.EscapeString(headline),                 // h1
		html.EscapeString(bodyCopy),                 // body paragraph
		html.EscapeString(ctaURL),                   // a[href]
		html.EscapeString(ctaText),                  // button label
		html.EscapeString(expiryHint),               // hint under button
		html.EscapeString(ctaURL),                   // fallback link href
		html.EscapeString(ctaURL),                   // fallback link text
		html.EscapeString(footnote),                 // safety footer
	)
}

// renderEmailText is the matched plain-text fallback. Same content
// but stripped of styling, plus the URL on its own line so any
// terminal client renders it as a clickable hyperlink.
func renderEmailText(kind emailKind, ctaURL, expiryHint string) string {
	var headline, body, footnote string
	switch kind {
	case emailVerify:
		headline = "验证你的 DFCHAT 邮箱"
		body = "感谢注册 DFCHAT。点击下方链接验证这个邮箱"
		footnote = "如果不是你本人操作，请忽略此邮件。"
	case emailPasswordReset:
		headline = "重置 DFCHAT 密码"
		body = "收到了重置你 DFCHAT 账号密码的请求。点击下方链接设置新密码"
		footnote = "如果不是你本人操作，请忽略此邮件 — 你的当前密码不会被改变。"
	case emailEmailChange:
		headline = "确认你的 DFCHAT 新邮箱"
		body = "收到了把 DFCHAT 账号的注册邮箱改成这个地址的请求。点击下方链接确认变更"
		footnote = "如果不是你本人操作，请忽略此邮件 — 账号当前邮箱不会被改变。"
	}
	var b strings.Builder
	b.WriteString(headline)
	b.WriteString("\n\n")
	b.WriteString(body)
	b.WriteString("（")
	b.WriteString(expiryHint)
	b.WriteString("）：\n\n")
	b.WriteString(ctaURL)
	b.WriteString("\n\n")
	b.WriteString(footnote)
	b.WriteString("\n\n— DFCHAT · 东方信息\n")
	return b.String()
}

// emailShell template. 9 sprintf args (see renderEmailHTML for order).
// All Sprintf placeholders use %s; callers MUST escape user-controlled
// strings before insertion.
const emailShell = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
</head>
<body style="margin:0; padding:0; background:#0b0d11; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'PingFang SC','Hiragino Sans GB','Microsoft YaHei',sans-serif;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%%" style="background:#0b0d11; padding:32px 16px;">
  <tr>
    <td align="center">
      <table role="presentation" cellpadding="0" cellspacing="0" border="0" width="560" style="max-width:560px; background:#15181f; border:1px solid #252a36; border-radius:16px; overflow:hidden;">
        <!-- Brand bar -->
        <tr>
          <td style="padding:24px 32px 12px 32px;">
            <table role="presentation" cellpadding="0" cellspacing="0" border="0">
              <tr>
                <td style="background:#4869f7; width:32px; height:32px; border-radius:8px; text-align:center; vertical-align:middle; font-size:16px; color:#fff; font-weight:600;">东</td>
                <td style="padding-left:10px; color:#cbd0d8; font-size:14px; font-weight:600; letter-spacing:0.02em;">DFCHAT · 东风快信</td>
              </tr>
            </table>
          </td>
        </tr>
        <!-- Headline + body -->
        <tr>
          <td style="padding:8px 32px 24px 32px;">
            <h1 style="margin:0 0 14px 0; color:#f4f5f8; font-size:22px; font-weight:600; letter-spacing:-0.01em;">%s</h1>
            <p style="margin:0; color:#a0a7b8; font-size:14px; line-height:1.7;">%s</p>
          </td>
        </tr>
        <!-- CTA button + expiry hint -->
        <tr>
          <td align="center" style="padding:8px 32px 8px 32px;">
            <table role="presentation" cellpadding="0" cellspacing="0" border="0">
              <tr>
                <td style="background:#4869f7; border-radius:10px;">
                  <a href="%s" target="_blank" rel="noopener" style="display:inline-block; padding:13px 32px; color:#ffffff; font-size:15px; font-weight:600; text-decoration:none; letter-spacing:0.02em;">%s</a>
                </td>
              </tr>
            </table>
            <p style="margin:14px 0 0 0; color:#6e7588; font-size:12px;">%s</p>
          </td>
        </tr>
        <!-- Fallback plain link (some clients block buttons / users want to copy) -->
        <tr>
          <td style="padding:20px 32px 8px 32px;">
            <p style="margin:0 0 6px 0; color:#6e7588; font-size:12px;">如果按钮无法点击，复制下方链接到浏览器：</p>
            <p style="margin:0; word-break:break-all; font-size:12px;"><a href="%s" target="_blank" rel="noopener" style="color:#7a93ff; text-decoration:underline;">%s</a></p>
          </td>
        </tr>
        <!-- Safety footer -->
        <tr>
          <td style="padding:24px 32px; border-top:1px solid #252a36;">
            <p style="margin:0; color:#6e7588; font-size:12px; line-height:1.6;">%s</p>
          </td>
        </tr>
        <tr>
          <td style="padding:16px 32px; background:#0f1115; text-align:center;">
            <p style="margin:0; color:#525968; font-size:11px;">© 东方信息 · DFCHAT · 自动发送，请勿回复</p>
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>
</body>
</html>`
