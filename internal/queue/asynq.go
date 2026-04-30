package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"naughtyfication/internal/config"
)

const TypeNotificationDelivery = "notification:deliver"

type JobPayload struct {
	NotificationID string `json:"notification_id"`
	Attempt        int    `json:"attempt"`
}

type Enqueuer interface {
	Enqueue(ctx context.Context, payload JobPayload, delay time.Duration) error
}

type Processor interface {
	Process(ctx context.Context, payload JobPayload) error
}

type Client struct {
	client    *asynq.Client
	inspector *asynq.Inspector
	logger    *zap.Logger
}

func NewClient(cfg config.Config, logger *zap.Logger) *Client {
	opt := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddress,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	return &Client{
		client:    asynq.NewClient(opt),
		inspector: asynq.NewInspector(opt),
		logger:    logger,
	}
}

func (c *Client) Close() error {
	if err := c.inspector.Close(); err != nil {
		_ = c.client.Close()
		return err
	}
	return c.client.Close()
}

func (c *Client) Enqueue(ctx context.Context, payload JobPayload, delay time.Duration) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal job payload: %w", err)
	}

	task := asynq.NewTask(TypeNotificationDelivery, body)
	options := []asynq.Option{
		asynq.MaxRetry(0),
		asynq.Queue("notifications"),
		asynq.TaskID(fmt.Sprintf("%s-%d", payload.NotificationID, payload.Attempt)),
	}
	if delay > 0 {
		options = append(options, asynq.ProcessIn(delay))
	}

	info, err := c.client.EnqueueContext(ctx, task, options...)
	if err != nil {
		return fmt.Errorf("enqueue task: %w", err)
	}
	c.logger.Debug("job enqueued",
		zap.String("task_id", info.ID),
		zap.String("notification_id", payload.NotificationID),
		zap.Int("attempt", payload.Attempt),
		zap.Duration("delay", delay),
	)
	return nil
}

func (c *Client) QueueDepth() (int64, error) {
	info, err := c.inspector.GetQueueInfo("notifications")
	if err != nil {
		return 0, err
	}
	return int64(info.Size), nil
}

type Server struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	logger *zap.Logger
}

func NewServer(cfg config.Config, processor Processor, logger *zap.Logger) *Server {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeNotificationDelivery, func(ctx context.Context, task *asynq.Task) error {
		var payload JobPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return fmt.Errorf("decode task payload: %w", err)
		}
		return processor.Process(ctx, payload)
	})

	return &Server{
		server: asynq.NewServer(
			asynq.RedisClientOpt{
				Addr:     cfg.RedisAddress,
				Password: cfg.RedisPassword,
				DB:       cfg.RedisDB,
			},
			asynq.Config{
				Concurrency: cfg.WorkerCount,
				Queues: map[string]int{
					"notifications": 1,
				},
				Logger: asynqLogger{logger: logger},
			},
		),
		mux:    mux,
		logger: logger,
	}
}

func (s *Server) Start() error {
	go func() {
		if err := s.server.Run(s.mux); err != nil {
			s.logger.Error("asynq server stopped", zap.Error(err))
		}
	}()
	return nil
}

func (s *Server) Shutdown() {
	s.server.Shutdown()
}

type asynqLogger struct {
	logger *zap.Logger
}

func (l asynqLogger) Debug(args ...any) {
	l.logger.Sugar().Debug(args...)
}

func (l asynqLogger) Info(args ...any) {
	l.logger.Sugar().Info(args...)
}

func (l asynqLogger) Warn(args ...any) {
	l.logger.Sugar().Warn(args...)
}

func (l asynqLogger) Error(args ...any) {
	l.logger.Sugar().Error(args...)
}

func (l asynqLogger) Fatal(args ...any) {
	l.logger.Sugar().Fatal(args...)
}
