// Package turn issues ephemeral TURN credentials per the coturn REST API
// spec (https://datatracker.ietf.org/doc/html/draft-uberti-behave-turn-rest-00).
//
// Flow: client calls GET /api/v1/turn/credentials (authed), we hand back
// { username, credential, urls } that's valid for 30 minutes. Client passes
// it straight to RTCPeerConnection iceServers. coturn validates by
// re-running the HMAC with its --static-auth-secret.
//
// We never expose the static secret to the client — that's the whole point.
package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dongfang/dfchat/server/pkg/middleware"
	pkgauth "github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	issuer    *pkgauth.Issuer
	secret    string   // coturn --static-auth-secret
	host      string   // public DNS, e.g. dfchat.chat
	tcpEnabled bool    // whether 5349 TLS is configured
}

// Config bundles deployment-time settings.
type Config struct {
	Secret     string
	Host       string
	TCPEnabled bool
}

func NewHandler(issuer *pkgauth.Issuer, cfg Config) *Handler {
	return &Handler{
		issuer:     issuer,
		secret:     cfg.Secret,
		host:       cfg.Host,
		tcpEnabled: cfg.TCPEnabled,
	}
}

func (h *Handler) Register(rg *gin.RouterGroup) {
	g := rg.Group("/turn")
	g.Use(middleware.RequireAuth(h.issuer))
	g.GET("/credentials", h.credentials)
}

// credentials returns iceServers suitable for an RTCPeerConnection.
//
// Response shape mirrors what twilio / metered.ca return so client code can
// keep iceServers in a single field:
//
//   {
//     "iceServers": [
//       { "urls": ["stun:stun.dfchat.chat:3478"] },
//       { "urls": ["turn:turn.dfchat.chat:3478?transport=udp", ...],
//         "username": "<expires-unix>:<user>",
//         "credential": "<base64-hmac>" }
//     ],
//     "ttl": 1800
//   }
func (h *Handler) credentials(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(int64)

	if h.secret == "" || h.host == "" {
		// TURN not configured — return only public STUN so calls still work
		// behind cone NATs. Symmetric-NAT users will fail until TURN is set.
		c.JSON(http.StatusOK, gin.H{
			"iceServers": []gin.H{
				{"urls": []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
				}},
			},
			"ttl": 0,
		})
		return
	}

	ttl := 30 * time.Minute
	expires := time.Now().Add(ttl).Unix()
	username := fmt.Sprintf("%d:%d", expires, uid)

	mac := hmac.New(sha1.New, []byte(h.secret))
	mac.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	urls := []string{
		"turn:" + h.host + ":3478?transport=udp",
		"turn:" + h.host + ":3478?transport=tcp",
	}
	if h.tcpEnabled {
		urls = append(urls, "turns:"+h.host+":5349?transport=tcp")
	}

	c.JSON(http.StatusOK, gin.H{
		"iceServers": []gin.H{
			{"urls": []string{"stun:" + h.host + ":3478"}},
			{
				"urls":       urls,
				"username":   username,
				"credential": credential,
			},
		},
		"ttl":     int(ttl.Seconds()),
		"expires": strconv.FormatInt(expires, 10),
	})
}
