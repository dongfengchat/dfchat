<!--
感谢提 PR！请填写下面的清单——空着也没关系，但完整的描述能加速 review。
-->

## 这个 PR 做了什么

<!-- 1-3 句话讲清楚改动 -->

## 为什么需要这个改动

<!-- 关联 issue 用 "Closes #123" 或 "Refs #123" -->

## 怎么验证

<!-- 例：
1. cd client && npm run dev
2. 登录 → 进入 "测试群"
3. 发送 5 条消息
4. 观察 sidebar 应该出现红色 5
-->

## 截图（如果有 UI 改动）

<!-- 拖拽图片到这里 -->

## 自检清单

- [ ] 本地通过 `go vet ./...` + `go build ./...`（如有 server 改动）
- [ ] 本地通过 `npm run build`（如有 client 改动）
- [ ] 新增 migration 都有对应的 `down.sql`
- [ ] 没有提交 `.env` / 凭据 / `node_modules` / 构建产物
- [ ] commit message 用 Conventional Commits 风格（`feat:` / `fix:` / `docs:` 等）
- [ ] 大改动已先开 issue 讨论
