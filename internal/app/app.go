package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"naughtyfication/internal/auth"
	"naughtyfication/internal/config"
	"naughtyfication/internal/domain"
	"naughtyfication/internal/httpapi"
	"naughtyfication/internal/metrics"
	"naughtyfication/internal/postgres"
	"naughtyfication/internal/provider"
	"naughtyfication/internal/queue"
	"naughtyfication/internal/service"
)

type App struct {
	router http.Handler
	queue  *queue.Server
	client *queue.Client
	redis  *redis.Client
	db     *pgxpool.Pool
	logger *zap.Logger
}

func New(cfg config.Config) (*App, error) {
	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := postgres.RunMigrations(ctx, db); err != nil {
		return nil, err
	}

	repo := postgres.NewRepository(db)
	if err := repo.UpsertUserWithAPIKey(ctx, domain.User{
		ID:        "user_bootstrap",
		Email:     cfg.BootstrapUserEmail,
		CreatedAt: timeNowUTC(),
	}, cfg.BootstrapAPIKey); err != nil {
		return nil, fmt.Errorf("bootstrap api key: %w", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddress,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	var emailProvider provider.Provider
	if cfg.SMTPHost == "" && cfg.AllowMockDelivery {
		emailProvider = provider.NewMockProvider(logger)
	} else {
		emailProvider = provider.NewSMTPProvider(
			cfg.SMTPHost,
			cfg.SMTPPort,
			cfg.SMTPUsername,
			cfg.SMTPPassword,
			cfg.SMTPFrom,
			cfg.ProviderTimeout,
		)
	}

	providers := provider.NewRegistry(map[domain.NotificationType]provider.Provider{
		domain.NotificationTypeEmail: emailProvider,
	})
	client := queue.NewClient(cfg, logger)
	collector := metrics.NewCollector(func() (int64, error) {
		return client.QueueDepth()
	})
	webhooks := provider.NewWebhookDispatcher(cfg.WebhookTimeout, cfg.BaseBackoff, cfg.WebhookMaxRetries, logger)
	svc := service.New(repo, repo, repo, repo, repo, client, providers, webhooks, collector, cfg.MaxRetries, cfg.BaseBackoff, cfg.MaxBackoff)
	worker := queue.NewServer(cfg, svc, logger)
	if err := worker.Start(); err != nil {
		return nil, err
	}

	httpMiddleware := httpapi.NewMiddleware(repo, redisClient, logger, cfg.RateLimitRPS, cfg.RateLimitBurst)
	api := httpapi.New(auth.NewMiddleware(repo), httpMiddleware, collector.Handler(), svc)
	return &App{
		router: api.Router(),
		queue:  worker,
		client: client,
		redis:  redisClient,
		db:     db,
		logger: logger,
	}, nil
}

func (a *App) Router() http.Handler {
	return a.router
}

func (a *App) Close() {
	a.queue.Shutdown()
	_ = a.client.Close()
	_ = a.redis.Close()
	a.db.Close()
	_ = a.logger.Sync()
}
