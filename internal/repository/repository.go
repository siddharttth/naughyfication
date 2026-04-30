package repository

import (
	"context"
	"time"

	"naughtyfication/internal/domain"
)

type NotificationRepository interface {
	CreateNotification(ctx context.Context, notification domain.Notification) error
	UpdateNotification(ctx context.Context, notification domain.Notification) error
	IncrementAttempts(ctx context.Context, notificationID string, attempts int, updatedAt time.Time) error
	GetNotification(ctx context.Context, userID, notificationID string) (domain.Notification, error)
	GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error)
	ListNotifications(ctx context.Context, userID string) ([]domain.Notification, error)
}

type DeliveryLogRepository interface {
	AppendLog(ctx context.Context, logEntry domain.DeliveryLog) error
	ListLogs(ctx context.Context, notificationID string) ([]domain.DeliveryLog, error)
}

type UserRepository interface {
	GetUserByAPIKey(ctx context.Context, key string) (domain.User, error)
	GetUserByID(ctx context.Context, userID string) (domain.User, error)
	UpsertUserWithAPIKey(ctx context.Context, user domain.User, apiKey string) error
	UpdateWebhookURL(ctx context.Context, userID, webhookURL string) error
}

type TemplateRepository interface {
	GetTemplateByName(ctx context.Context, userID, name string) (domain.Template, error)
}

type IdempotencyCreateResult int

const (
	IdempotencyCreated IdempotencyCreateResult = iota
	IdempotencyExisting
)

type IdempotencyRepository interface {
	CreateOrGet(ctx context.Context, record domain.IdempotencyRecord) (domain.IdempotencyRecord, IdempotencyCreateResult, error)
	MarkCompleted(ctx context.Context, id string, responseStatus int, responseBody []byte) error
}

type DeadLetterRepository interface {
	CreateDeadLetter(ctx context.Context, job domain.DeadLetterJob) error
	ListDeadLetters(ctx context.Context, userID string) ([]domain.DeadLetterJob, error)
	GetDeadLetter(ctx context.Context, userID, id string) (domain.DeadLetterJob, error)
	MarkDeadLetterRetried(ctx context.Context, id string, retriedAt time.Time) error
}
