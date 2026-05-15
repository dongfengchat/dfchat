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
	SRSAPIBaseURL  string // server-side health probe URL, e.g. http://srs:1985/api/v1/summaries

	// coturn TURN server (WebRTC fallback for symmetric NAT).
	TurnSecret     string // shared --static-auth-secret with coturn
	TurnHost       string // public host clients reach, e.g. dfchat.chat
	TurnTLSEnabled bool   // turns:5349 enabled?
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

		LiveRTMPURL:   getEnv("LIVE_RTMP_URL", "rtmp://localhost:1935/live"),
		LiveHLSURL:    getEnv("LIVE_HLS_URL", "http://localhost:8088/live"),
		LiveSRSSecret: getEnv("LIVE_SRS_SECRET", ""),
		SRSAPIBaseURL: getEnv("SRS_API_BASE_URL", ""),

		TurnSecret:     getEnv("TURN_SECRET", ""),
		TurnHost:       getEnv("TURN_HOST", ""),
		TurnTLSEnabled: getEnv("TURN_TLS_ENABLED", "false") == "true",
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
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
