// Package search provides cross-conversation message search.
//
// MVP uses ILIKE on the JSONB text field — works on Chinese without an extra
// tokenizer (pg_jieba) and is fast enough up to a few hundred thousand rows.
// Migrate to Postgres FTS or Meilisearch when message volume crosses ~1M.
package search

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool   *pgxpool.Pool
	issuer *auth.Issuer
}

func NewHandler(pool *pgxpool.Pool, issuer *auth.Issuer) *Handler {
	return &Handler{pool: pool, issuer: issuer}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/search")
	g.Use(middleware.RequireAuth(h.issuer))
	g.GET("/messages", h.searchMessages)
}

type hit struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	SenderID       string `json:"senderId"`
	Type           string `json:"type"`
	Text           string `json:"text"`
	Seq            int64  `json:"seq"`
	CreatedAt      string `json:"createdAt"`
}

func (h *Handler) searchMessages(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"hits": []hit{}})
		return
	}
	if len(q) > 80 {
		q = q[:80]
	}
	convID := c.Query("conversationId") // optional scope

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	// pgx will escape the param; ILIKE pattern wraps it with wildcards.
	// We restrict to text messages whose content.text contains the query,
	// joined against the caller's conversation_members so we only return
	// hits from conversations they belong to.
	args := []any{uid, "%" + q + "%", limit}
	var convFilter string
	if convID != "" {
		args = append(args, convID)
		convFilter = " AND m.conversation_id = $4"
	}

	query := `
		SELECT m.id, m.conversation_id, m.sender_id, m.type,
		       COALESCE(m.content->>'text', '') AS text,
		       m.seq, m.created_at::text
		FROM messages m
		JOIN conversation_members cm ON cm.conversation_id = m.conversation_id
		WHERE cm.user_id = $1
		  AND m.is_recalled = FALSE
		  AND m.type = 'text'
		  AND m.content->>'text' ILIKE $2` + convFilter + `
		ORDER BY m.created_at DESC
		LIMIT $3`

	rows, err := h.pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()

	out := make([]hit, 0)
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.ID, &h.ConversationID, &h.SenderID, &h.Type, &h.Text, &h.Seq, &h.CreatedAt); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		out = append(out, h)
	}
	c.JSON(http.StatusOK, gin.H{"hits": out, "query": q})
}
