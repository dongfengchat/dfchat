package sync

import (
	"net/http"

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
	g := rg.Group("/sync")
	g.Use(middleware.RequireAuth(h.issuer))
	g.GET("/conversations", h.conversations)
}

type convSummary struct {
	ID            string `json:"id"`
	Type          int16  `json:"type"`
	HeadSeq       int64  `json:"headSeq"`
	LastMessageAt string `json:"lastMessageAt,omitempty"`
	Muted         bool   `json:"muted"`
}

// conversations returns every conversation the caller is a member of,
// together with the current head seq. The client compares this against
// its local lastSeq per-conv to figure out what to pull.
func (h *Handler) conversations(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT c.id, c.type,
		       COALESCE(cs.last_seq, 0) AS head_seq,
		       COALESCE(c.last_message_at::text, ''),
		       cm.muted
		FROM conversation_members cm
		JOIN conversations c   ON c.id  = cm.conversation_id
		LEFT JOIN conversation_seq cs ON cs.conversation_id = cm.conversation_id
		WHERE cm.user_id = $1
		ORDER BY c.last_message_at DESC NULLS LAST, c.id`, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	defer rows.Close()

	out := make([]convSummary, 0)
	for rows.Next() {
		var s convSummary
		if err := rows.Scan(&s.ID, &s.Type, &s.HeadSeq, &s.LastMessageAt, &s.Muted); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversations": out})
}

