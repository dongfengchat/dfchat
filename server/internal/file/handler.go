package file

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/dongfang/dfchat/server/pkg/storage"
	"github.com/gin-gonic/gin"
)

const (
	maxImageBytes = 20 * 1024 * 1024  // 20 MB
	maxFileBytes  = 100 * 1024 * 1024 // 100 MB (MVP cap; design doc allows 2GB later)
	presignTTL    = 10 * time.Minute
)

type Handler struct {
	repo    *Repo
	storage *storage.Client
	issuer  *auth.Issuer
}

func NewHandler(repo *Repo, st *storage.Client, issuer *auth.Issuer) *Handler {
	return &Handler{repo: repo, storage: st, issuer: issuer}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/files")
	g.Use(middleware.RequireAuth(h.issuer))
	g.POST("/upload-token", h.uploadToken)
	g.POST("/confirm", h.confirm)
	g.GET("/:id", h.detail)
	// Group / channel "files center" — list every image/file attachment in
	// the conversation, newest first. Caller must be a conversation member.
	g.GET("/by-conversation/:convId", h.byConversation)
}

type convFileItem struct {
	ID         int64  `json:"id,string"`
	SenderID   int64  `json:"senderId,string"`
	Type       string `json:"type"`           // "image" | "file"
	Name       string `json:"name"`
	URL        string `json:"url"`
	Size       int64  `json:"size,omitempty"`
	MimeType   string `json:"mime,omitempty"`
	Thumbnail  string `json:"thumbnail,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

// byConversation returns recent image+file messages of a conversation.
// We read from the messages table directly to avoid mixing in side
// tables — messages.content is JSONB with the file metadata.
func (h *Handler) byConversation(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	convID := c.Param("convId")
	if convID == "" {
		fail(c, 400, 60040, "convId required")
		return
	}
	// Authorization: caller must be in conversation_members.
	var isMember bool
	if err := h.repo.Pool().QueryRow(c.Request.Context(),
		`SELECT EXISTS (SELECT 1 FROM conversation_members WHERE conversation_id = $1 AND user_id = $2)`,
		convID, uid).Scan(&isMember); err != nil || !isMember {
		fail(c, 403, 60041, "not a conversation member")
		return
	}
	rows, err := h.repo.Pool().Query(c.Request.Context(),
		`SELECT id, sender_id, type,
		        COALESCE(content->>'name', ''),
		        COALESCE(content->>'url', ''),
		        COALESCE((content->>'size')::bigint, 0),
		        COALESCE(content->>'mime', ''),
		        COALESCE(content->>'thumbnail', ''),
		        created_at
		 FROM messages
		 WHERE conversation_id = $1
		   AND type IN ('image', 'file')
		   AND is_recalled = false
		 ORDER BY created_at DESC LIMIT 200`, convID)
	if err != nil {
		fail(c, 500, 50001, "internal error")
		return
	}
	defer rows.Close()
	out := make([]convFileItem, 0)
	for rows.Next() {
		var it convFileItem
		var t time.Time
		if err := rows.Scan(&it.ID, &it.SenderID, &it.Type, &it.Name, &it.URL,
			&it.Size, &it.MimeType, &it.Thumbnail, &t); err != nil {
			continue
		}
		it.CreatedAt = t.UTC().Format(time.RFC3339)
		out = append(out, it)
	}
	c.JSON(200, gin.H{"files": out})
}

type tokenReq struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	Kind string `json:"kind"` // "image" | "file"
}

type tokenRes struct {
	FileID     string `json:"fileId"`
	UploadURL  string `json:"uploadUrl"`
	PublicURL  string `json:"publicUrl"`
	ExpiresIn  int    `json:"expiresIn"`
	StorageKey string `json:"storageKey"`
}

func (h *Handler) uploadToken(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req tokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 40010, "invalid body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		fail(c, http.StatusBadRequest, 40011, "name required")
		return
	}
	if req.Size <= 0 {
		fail(c, http.StatusBadRequest, 40012, "size required")
		return
	}
	switch req.Kind {
	case "image":
		if req.Size > maxImageBytes {
			fail(c, http.StatusRequestEntityTooLarge, 40013, "image too large (max 20MB)")
			return
		}
	case "file", "":
		if req.Size > maxFileBytes {
			fail(c, http.StatusRequestEntityTooLarge, 40014, "file too large (max 100MB)")
			return
		}
	default:
		fail(c, http.StatusBadRequest, 40015, "unknown kind")
		return
	}

	rnd := make([]byte, 8)
	if _, err := rand.Read(rnd); err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	key := time.Now().UTC().Format("2006/01/02") + "/" + hex.EncodeToString(rnd) + ext

	publicURL := h.storage.PublicURL(key)
	f, err := h.repo.CreatePending(c.Request.Context(), CreatePendingParams{
		UserID:     uid,
		Name:       name,
		Mime:       req.Mime,
		StorageKey: key,
		URL:        publicURL,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	uploadURL, err := h.storage.PresignPut(c.Request.Context(), key, presignTTL)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, tokenRes{
		FileID:     strconv.FormatInt(f.ID, 10),
		UploadURL:  uploadURL,
		PublicURL:  publicURL,
		StorageKey: key,
		ExpiresIn:  int(presignTTL.Seconds()),
	})
}

type confirmReq struct {
	FileID string `json:"fileId"`
}

func (h *Handler) confirm(c *gin.Context) {
	uid := c.MustGet("userID").(int64)
	var req confirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 40020, "invalid body")
		return
	}
	id, err := strconv.ParseInt(req.FileID, 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, 40021, "invalid fileId")
		return
	}
	pending, err := h.repo.FindByID(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		fail(c, http.StatusNotFound, 40022, "file not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	if pending.UserID != uid {
		fail(c, http.StatusForbidden, 40023, "not your file")
		return
	}

	size, mime, err := h.storage.StatObject(c.Request.Context(), pending.StorageKey)
	if err != nil {
		fail(c, http.StatusBadRequest, 40024, "object not uploaded yet")
		return
	}
	confirmed, err := h.repo.Confirm(c.Request.Context(), id, uid, size, mime)
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"file": confirmed})
}

func (h *Handler) detail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, 40030, "invalid id")
		return
	}
	f, err := h.repo.FindByID(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		fail(c, http.StatusNotFound, 40031, "file not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, 50001, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"file": f})
}

func fail(c *gin.Context, status, code int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"code": code, "message": msg})
}
