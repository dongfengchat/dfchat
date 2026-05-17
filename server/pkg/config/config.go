package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv  string
	AppPort string

	DatabaseURL string

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	JWTSecret         string
	JWTAccessTTLHours int
	JWTRefreshHours   int

	CORSAllowOrigins []string

	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioPublicURL string
	MinioUseSSL    bool

	// Live broadcasting (SRS).
	LiveRTMPURL    string // shown to streamer, e.g. rtmp://dfchat.chat/live
	LiveHLSURL     string // shown to viewer,   e.g. https://dfchat.chat/hls
	LiveSRSSecret  string // shared with SRS HTTP callbacks
	SRSAPIBaseURL   string // server-side health probe URL, e.g. http://srs:1985/api/v1/summaries
	SRSInternalHTTP string // internal SRS HTTP server (HLS files), e.g. http://srs:8080 — used by api to proxy + rewrite m3u8 for signed playback

	// coturn TURN server (WebRTC fallback for symmetric NAT).
	TurnSecret     string // shared --static-auth-secret with coturn
	TurnHost       string // public host clients reach, e.g. dfchat.chat
	TurnTLSEnabled bool   // turns:5349 enabled?

	// Outbound SMTP for email verification / password reset.
	// Empty SMTPHost → mailer logs to stdout instead of sending (dev mode).
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string // From: header, e.g. "DFCHAT <no-reply@dfchat.chat>"
	SMTPUseTLS   bool   // true for implicit TLS (port 465), false for STARTTLS

	// Public base URL used to construct email links (verification, reset).
	// e.g. "https://dfchat.chat" so the link looks like
	//   https://dfchat.chat/verify-email?token=xxxxx
	PublicBaseURL string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:            getEnv("APP_ENV", "development"),
		AppPort:           getEnv("APP_PORT", "8080"),
		DatabaseURL:       getEnv("DATABASE_URL", ""),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisDB:           getEnvInt("REDIS_DB", 0),
		JWTSecret:         getEnv("JWT_SECRET", ""),
		JWTAccessTTLHours: getEnvInt("JWT_ACCESS_TTL_HOURS", 2),
		JWTRefreshHours:   getEnvInt("JWT_REFRESH_TTL_HOURS", 720),
		CORSAllowOrigins:  splitCSV(getEnv("CORS_ALLOW_ORIGINS", "http://localhost:5173")),

		MinioEndpoint:  getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinioAccessKey: getEnv("MINIO_ACCESS_KEY", ""),
		MinioSecretKey: getEnv("MINIO_SECRET_KEY", ""),
		MinioBucket:    getEnv("MINIO_BUCKET", "dfchat"),
		MinioPublicURL: getEnv("MINIO_PUBLIC_URL", "http://localhost:9000"),
		MinioUseSSL:    getEnv("MINIO_USE_SSL", "false") == "true",

		LiveRTMPURL:     getEnv("LIVE_RTMP_URL", "rtmp://localhost:1935/live"),
		LiveHLSURL:      getEnv("LIVE_HLS_URL", "http://localhost:8088/live"),
		LiveSRSSecret:   getEnv("LIVE_SRS_SECRET", ""),
		SRSAPIBaseURL:   getEnv("SRS_API_BASE_URL", ""),
		SRSInternalHTTP: getEnv("SRS_INTERNAL_HTTP", "http://srs:8080"),

		TurnSecret:     getEnv("TURN_SECRET", ""),
		TurnHost:       getEnv("TURN_HOST", ""),
		TurnTLSEnabled: getEnv("TURN_TLS_ENABLED", "false") == "true",

		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getEnvInt("SMTP_PORT", 587),
		SMTPUser:      getEnv("SMTP_USER", ""),
		SMTPPassword:  getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:      getEnv("SMTP_FROM", ""),
		SMTPUseTLS:    getEnv("SMTP_USE_TLS", "false") == "true",
		PublicBaseURL: getEnv("PUBLIC_BASE_URL", "https://dfchat.chat"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	// SRS callback authenticator: SRS hits /api/v1/live/srs-hook/<secret>
	// with the secret in the URL path. An empty default would silently
	// fail-open (both sides empty → equal → authorised), letting anyone
	// on the internet spoof on_publish / on_unpublish / on_dvr hooks
	// and remote-kill live streams. Refuse to boot on weak config.
	if len(cfg.LiveSRSSecret) < 32 {
		return nil, fmt.Errorf("LIVE_SRS_SECRET must be at least 32 chars (got %d) — SRS callback auth is fail-open otherwise", len(cfg.LiveSRSSecret))
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
