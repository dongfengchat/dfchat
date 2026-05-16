package auth

import (
	"fmt"
	"html"
)

// Branded HTML pages for the public verification / change-confirm
// landing URLs. Kept inline (no separate template files) so the
// distroless API container doesn't need a mounted templates dir.
//
// Design goals:
//   - matches dfchat.chat marketing site palette (#0b0d11 bg, brand-500
//     blue accent, system fonts);
//   - works without JS, on mobile, behind weird corporate proxies;
//   - inlines a tiny SVG logo so there's no external request leak in
//     incognito tabs.
//
// All four states are rendered through the same shell — the only
// differences are icon, title, message, and accent color.

type pageVariant string

const (
	pageSuccess pageVariant = "success"
	pageError   pageVariant = "error"
)

// htmlPage is the actual implementation — kept simple and explicit.
// We don't bother with html/template because the inputs are well-scoped
// and we want zero room for accidental partial-application bugs.
const pageTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#0b0d11">
<meta name="robots" content="noindex">
<title>%s · DFCHAT</title>
<style>
  *, *::before, *::after { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; }
  body {
    min-height: 100vh;
    background: radial-gradient(ellipse at top, #1a1f2e 0%%, #0b0d11 60%%);
    color: #e7e9ee;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
                 "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    -webkit-font-smoothing: antialiased;
  }
  .card {
    background: #15181f;
    border: 1px solid #252a36;
    border-radius: 16px;
    padding: 40px 32px;
    max-width: 440px;
    width: 100%%;
    text-align: center;
    box-shadow: 0 10px 40px rgba(0,0,0,0.45);
  }
  .brand {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: #a0a7b8;
    font-size: 13px;
    font-weight: 500;
    letter-spacing: 0.02em;
    margin-bottom: 32px;
  }
  .brand .mark {
    width: 22px; height: 22px;
    background: #4869f7;
    border-radius: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: #fff;
  }
  .icon {
    color: %s;
    margin-bottom: 18px;
    display: inline-flex;
  }
  h1 {
    font-size: 22px;
    font-weight: 600;
    margin: 0 0 12px;
    color: #f4f5f8;
    letter-spacing: -0.01em;
  }
  p {
    font-size: 14px;
    line-height: 1.7;
    color: #a0a7b8;
    margin: 0 0 8px;
  }
  .detail {
    margin-top: 16px;
    padding: 10px 14px;
    background: #1d2230;
    border-radius: 8px;
    font-size: 13px;
    color: #c8cdda;
    word-break: break-all;
  }
  .hint {
    margin-top: 28px;
    padding-top: 20px;
    border-top: 1px solid #252a36;
    font-size: 12px;
    color: #6e7588;
  }
  .btn {
    display: inline-block;
    margin-top: 20px;
    padding: 10px 22px;
    background: #4869f7;
    color: #fff;
    text-decoration: none;
    border-radius: 8px;
    font-size: 14px;
    font-weight: 500;
    transition: background 0.15s;
  }
  .btn:hover { background: #5a78ff; }
  .footer {
    margin-top: 24px;
    font-size: 11px;
    color: #525968;
  }
</style>
</head>
<body>
  <div class="card">
    <div class="brand">
      <span class="mark"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>
      <span>东风快信 · DFCHAT</span>
    </div>
    <div class="icon">%s</div>
    <h1>%s</h1>
    <p>%s</p>
    %s
    <div class="hint">
      可以关闭这个标签页，回到 DFCHAT 客户端继续使用。
    </div>
    <a class="btn" href="https://dfchat.chat/">返回官网</a>
    <div class="footer">© 东方信息 · DFCHAT</div>
  </div>
</body>
</html>
`

// Actual entry point used by handlers. Builds the page from the
// template with proper escaping for all dynamic fields.
func htmlPage(variant pageVariant, title, body, detail string) string {
	var iconSVG, accentColor string
	switch variant {
	case pageSuccess:
		accentColor = "#10b981"
		iconSVG = `<svg viewBox="0 0 24 24" width="48" height="48" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>`
	default:
		accentColor = "#ed4245"
		iconSVG = `<svg viewBox="0 0 24 24" width="48" height="48" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>`
	}

	detailHTML := ""
	if detail != "" {
		detailHTML = `<p class="detail">` + html.EscapeString(detail) + `</p>`
	}

	return fmt.Sprintf(pageTemplate,
		html.EscapeString(title), // <title>
		accentColor,              // .icon color
		iconSVG,                  // icon
		html.EscapeString(title), // h1
		html.EscapeString(body),  // primary message
		detailHTML,               // optional detail block (already HTML)
	)
}
