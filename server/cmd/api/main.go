package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dongfang/dfchat/server/internal/admin"
	"github.com/dongfang/dfchat/server/internal/auth"
	"github.com/dongfang/dfchat/server/internal/channel"
	"github.com/dongfang/dfchat/server/internal/file"
	"github.com/dongfang/dfchat/server/internal/friend"
	"github.com/dongfang/dfchat/server/internal/group"
	"github.com/dongfang/dfchat/server/internal/live"
	"github.com/dongfang/dfchat/server/internal/message"
	"github.com/dongfang/dfchat/server/internal/moderation"
	"github.com/dongfang/dfchat/server/internal/realtime"
	"github.com/dongfang/dfchat/server/internal/search"
	"github.com/dongfang/dfchat/server/internal/sync"
	"github.com/dongfang/dfchat/server/internal/turn"
	"github.com/dongfang/dfchat/server/internal/user"
	pkgaudit "github.com/dongfang/dfchat/server/pkg/audit"
	pkgauth "github.com/dongfang/dfchat/server/pkg/auth"
	"github.com/dongfang/dfchat/server/pkg/config"
	"github.com/dongfang/dfchat/server/pkg/db"
	"github.com/dongfang/dfchat/server/pkg/health"
	"github.com/dongfang/dfchat/server/pkg/logger"
	"github.com/dongfang/dfchat/server/pkg/mailer"
	"github.com/dongfang/dfchat/server/pkg/middleware"
	"github.com/dongfang/dfchat/server/pkg/storage"
	"github.com/dongfang/dfchat/server/pkg/wsbus"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	log := logger.New(cfg.AppEnv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("connect postgres failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	log.Info("connected to postgres")

	issuer := pkgauth.NewIssuer(cfg.JWTSecret, cfg.JWTAccessTTLHours)
	refreshStore := auth.NewRefreshStore(pool, time.Duration(cfg.JWTRefreshHours)*time.Hour)
	bus := wsbus.New()
	auditor := pkgaudit.New(pool, log)

	storageClient, err := storage.New(ctx, storage.Config{
		Endpoint:  cfg.MinioEndpoint,
		AccessKey: cfg.MinioAccessKey,
		SecretKey: cfg.MinioSecretKey,
		Bucket:    cfg.MinioBucket,
		UseSSL:    cfg.MinioUseSSL,
		PublicURL: cfg.MinioPublicURL,
	})
	if err != nil {
		log.Error("init minio failed", "err", err)
		os.Exit(1)
	}
	log.Info("connected to minio", "bucket", cfg.MinioBucket)

	userRepo := user.NewRepo(pool)
	friendRepo := friend.NewRepo(pool)
	groupRepo := group.NewRepo(pool)
	channelRepo := channel.NewRepo(pool)
	messageRepo := message.NewRepo(pool)
	fileRepo := file.NewRepo(pool)
	liveRepo := live.NewRepo(pool)

	mail := mailer.New(mailer.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		UseTLS:   cfg.SMTPUseTLS,
	}, log)

	authSvc := auth.NewService(userRepo, issuer, refreshStore)
	authHandler := auth.NewHandler(authSvc, issuer, refreshStore, mail, pool, cfg.PublicBaseURL)
	userHandler := user.NewHandler(userRepo, issuer, refreshStore, groupRepo)
	friendHandler := friend.NewHandler(friendRepo, issuer, bus)
	groupHandler := group.NewHandler(groupRepo, issuer)
	channelHandler := channel.NewHandler(channelRepo, groupRepo, issuer)
	messageHandler := message.NewHandler(messageRepo, friendRepo, groupRepo, channelRepo, issuer, bus)
	syncHandler := sync.NewHandler(pool, issuer)
	fileHandler := file.NewHandler(fileRepo, storageClient, issuer)
	// liveHandler declared below — admin needs a reference to it for
	// shared teardown on force-end / ban. Forward-declared at zero
	// value here, populated immediately after live.NewHandler.
	var adminHandler *admin.Handler
	searchHandler := search.NewHandler(pool, issuer)
	liveHandler := live.NewHandler(liveRepo, issuer, bus, cfg.LiveRTMPURL, cfg.LiveHLSURL, cfg.LiveSRSSecret, cfg.SRSInternalHTTP)
	adminHandler = admin.NewHandler(pool, issuer, auditor, liveRepo, liveHandler, cfg.LiveHLSURL)
	// relayAdapter implements realtime.RelayBackend: a relay (WebRTC
	// signaling, typing) is allowed iff sender and recipient are friends
	// or share at least one group. This keeps strangers from initiating
	// calls or pinging "X is typing" at arbitrary user ids.
	relayAdapter := relayBackend{friends: friendRepo, groups: groupRepo}
	realtimeHandler := realtime.NewHandler(issuer, bus, log, liveRepo, relayAdapter, cfg.CORSAllowOrigins)
	// Realtime owns the room subscriber set, live needs to query it for
	// /viewers — wire the back-reference now that both exist.
	liveHandler.AttachViewerSource(realtimeHandler)
	// Background goroutine pushes reminders 0-10 min before scheduled streams.
	liveHandler.RunScheduledReminderLoop(ctx)
	// Background sweeper rotates stream keys for rooms that have been
	// ended for >5 min without an explicit owner stop. on_unpublish
	// itself no longer rotates (so brief OBS network blips don't lose
	// the key) — this loop enforces "key gets rotated eventually" so
	// a leaked HLS URL can't take over a future broadcast.
	liveHandler.RunKeyRotateSweeper(ctx, log, 5*time.Minute)
	// AI moderation worker. Multi-provider; configured via env
	// (MODERATION_ENABLED / MODERATION_PROVIDERS / each provider's
	// own ANTHROPIC_API_KEY / OPENAI_API_KEY / etc). Disabled by
	// default — operator opts in.
	liveHandler.RunModerationLoop(ctx, moderation.Config{
		Enabled:                 cfg.ModerationEnabled,
		Providers:               cfg.ModerationProviders,
		IntervalSeconds:         cfg.ModerationInterval,
		Threshold:               cfg.ModerationThreshold,
		AnthropicKey:            cfg.ModerationAnthropicKey,
		AnthropicModel:          cfg.ModerationAnthropicModel,
		OpenAIKey:               cfg.ModerationOpenAIKey,
		OpenAIModel:             cfg.ModerationOpenAIModel,
		OpenAIBaseURL:           cfg.ModerationOpenAIBaseURL,
		GeminiKey:               cfg.ModerationGeminiKey,
		GeminiModel:             cfg.ModerationGeminiModel,
		LocalEndpoint:           cfg.ModerationLocalEndpoint,
		LocalModel:              cfg.ModerationLocalModel,
		LMStudioEndpoint:        cfg.ModerationLMStudioEndpoint,
		LMStudioModel:           cfg.ModerationLMStudioModel,
	}, log)
	// Sweep AI verdict rows older than 7 days every 6 hours. Pinned
	// rows survive — admin pins anything they want to keep for
	// investigations / training-set export. Files on disk are
	// unlinked alongside DB rows.
	liveHandler.RunVerdictCleanup(ctx, 7*24*time.Hour, log)
	// Background reconciler: polls SRS /api/v1/streams every 60 s and
	// marks any DB row "live" that SRS doesn't actually have a publisher
	// for as ended. Catches the "OBS crashed mid-stream + on_unpublish
	// callback was lost" + "SRS container restarted" zombie cases.
	go live.RunSRSReconcileLoop(ctx, liveRepo, cfg.SRSAPIBaseURL, log)
	// Background sweeper: drops unverified accounts > 14 days old and GCs
	// expired email-verify / password-reset tokens / draw selections.
	// Runs hourly.
	go auth.RunCleanupLoop(ctx, pool, log)
	// Background sweeper for the message retention window. Deletes
	// messages older than message.RetentionWindow (30 days) except
	// pinned ones. After deletion the server can no longer recall /
	// edit / delete those messages — the client's local archive (if
	// any) is the only remaining copy. See internal/message/cleanup.go.
	go message.RunRetentionLoop(ctx, pool, log)

	// Account-number pool: populate any open segment that's missing
	// rows. Idempotent — on warm boots this is a no-op. On the first
	// boot after migration 000019 it generates ~10k rows in < 1s.
	if err := auth.EnsureSegmentPools(ctx, pool, log); err != nil {
		log.Warn("account-no: pool init failed", "err", err.Error())
	}
	turnHandler := turn.NewHandler(issuer, turn.Config{
		Secret:     cfg.TurnSecret,
		Host:       cfg.TurnHost,
		TCPEnabled: cfg.TurnTLSEnabled,
	})
	healthChecker := health.New(pool, storageClient, cfg.SRSAPIBaseURL)

	if cfg.AppEnv != "development" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	// CRITICAL: by default Gin trusts every proxy, which means
	// c.ClientIP() honors X-Forwarded-For from any source. Anyone could
	// then forge their IP to bypass our rate limiters, IP-pinned account
	// draws, login-log auditing, and per-IP caps. In prod we sit behind
	// exactly one nginx (in the docker bridge network), so trust only
	// that network. In dev (no proxy), pass an empty slice → no proxy
	// trusted → ClientIP returns the connection's RemoteAddr directly.
	if cfg.AppEnv == "development" {
		_ = r.SetTrustedProxies(nil)
	} else {
		// docker default bridge subnet — covers both deploy_default and
		// any custom compose network we might be on.
		_ = r.SetTrustedProxies([]string{"172.16.0.0/12", "10.0.0.0/8", "127.0.0.1"})
	}
	r.Use(gin.Recovery())
	r.Use(middleware.CORS(cfg.CORSAllowOrigins))
	r.Use(requestLogger(log))
	// Global rate limit: 30 r/s steady, burst 60, per client IP.
	// Loose enough for real users (typing, scroll, prefetch) but cuts off
	// scripted abuse hard.
	r.Use(middleware.RateLimit(30, 60))

	r.GET("/healthz", healthChecker.Handler())
	// Prometheus scrape target. nginx restricts this to internal IPs.
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	v1 := r.Group("/api/v1")
	authHandler.Register(v1)
	userHandler.Register(v1)
	friendHandler.Register(v1)
	groupHandler.Register(v1)
	channelHandler.Register(v1)
	messageHandler.Register(v1)
	syncHandler.Register(v1)
	fileHandler.Register(v1)
	adminHandler.Register(v1)
	searchHandler.Register(v1)
	liveHandler.Register(v1)
	turnHandler.Register(v1)
	realtimeHandler.Register(r)

	srv := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("api server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down, draining WS + HTTP")

	// Give the HTTP server (and any in-flight requests + open WS loops) up
	// to 15 s to wind down. WS read loops will close naturally when the
	// listener stops accepting.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("bye")
}

// relayBackend implements realtime.RelayBackend by stitching friend +
// group repos together. The realtime handler stays decoupled from those
// domain packages this way.
type relayBackend struct {
	friends *friend.Repo
	groups  *group.Repo
}

func (r relayBackend) CanRelay(ctx context.Context, from, to int64) (bool, error) {
	if from == to {
		return false, nil
	}
	if ok, _ := r.friends.AreFriends(ctx, from, to); ok {
		return true, nil
	}
	return r.groups.SharesGroupWith(ctx, from, to)
}

func requestLogger(log interface{ Info(msg string, args ...any) }) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}
