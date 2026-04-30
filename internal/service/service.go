package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"naughtyfication/internal/domain"
	"naughtyfication/internal/provider"
	"naughtyfication/internal/queue"
	"naughtyfication/internal/repository"
)

var (
	ErrUnsupportedType = errors.New("unsupported notification type")
	ErrUserNotFound    = errors.New("user not found")
)

type DeliveryRequest struct {
	UserID       string
	Type         domain.NotificationType `json:"type"`
	To           string                  `json:"to"`
	Subject      string                  `json:"subject"`
	Template     string                  `json:"template"`
	TemplateBody string                  `json:"template_body"`
	Data         map[string]any          `json:"data"`
}

type CallbackDispatcher interface {
	Notify(ctx context.Context, user domain.User, notification domain.Notification)
}

type Service struct {
	notifications repository.NotificationRepository
	logs          repository.DeliveryLogRepository
	users         repository.UserRepository
	templates     repository.TemplateRepository
	dlq           repository.DeadLetterRepository
	queue         queue.Enqueuer
	providers     *provider.Registry
	callbacks     CallbackDispatcher
	metrics       Metrics
	maxRetries    int
	baseBackoff   time.Duration
	maxBackoff    time.Duration
}

type Metrics interface {
	IncTotalNotifications()
	IncSuccess()
	IncFailure()
	IncRetry()
}

func New(
	notifications repository.NotificationRepository,
	logs repository.DeliveryLogRepository,
	users repository.UserRepository,
	templates repository.TemplateRepository,
	dlq repository.DeadLetterRepository,
	queue queue.Enqueuer,
	providers *provider.Registry,
	callbacks CallbackDispatcher,
	metrics Metrics,
	maxRetries int,
	baseBackoff, maxBackoff time.Duration,
) *Service {
	return &Service{
		notifications: notifications,
		logs:          logs,
		users:         users,
		templates:     templates,
		dlq:           dlq,
		queue:         queue,
		providers:     providers,
		callbacks:     callbacks,
		metrics:       metrics,
		maxRetries:    maxRetries,
		baseBackoff:   baseBackoff,
		maxBackoff:    maxBackoff,
	}
}

func (s *Service) Submit(ctx context.Context, request DeliveryRequest) (domain.Notification, error) {
	if request.Type != domain.NotificationTypeEmail {
		return domain.Notification{}, ErrUnsupportedType
	}
	if request.Data == nil {
		request.Data = map[string]any{}
	}

	now := time.Now().UTC()
	notification := domain.Notification{
		ID:          domain.NewID("notif"),
		UserID:      request.UserID,
		Type:        request.Type,
		To:          request.To,
		Subject:     request.Subject,
		Template:    request.Template,
		Data:        request.Data,
		Status:      domain.StatusPending,
		Payload:     map[string]any{"template_body": request.TemplateBody},
		MaxAttempts: s.maxRetries + 1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if request.Template != "" && request.TemplateBody == "" {
		template, err := s.templates.GetTemplateByName(ctx, request.UserID, request.Template)
		if err == nil {
			notification.Payload["template_body"] = template.Content
		}
	}

	if err := s.notifications.CreateNotification(ctx, notification); err != nil {
		return domain.Notification{}, err
	}

	if err := s.logs.AppendLog(ctx, domain.DeliveryLog{
		ID:             domain.NewID("log"),
		NotificationID: notification.ID,
		Attempt:        0,
		Status:         domain.StatusPending,
		CreatedAt:      now,
	}); err != nil {
		return domain.Notification{}, err
	}

	if err := s.queue.Enqueue(ctx, queue.JobPayload{NotificationID: notification.ID, Attempt: 1}, 0); err != nil {
		return domain.Notification{}, err
	}
	if s.metrics != nil {
		s.metrics.IncTotalNotifications()
	}

	return notification, nil
}

func (s *Service) Process(ctx context.Context, payload queue.JobPayload) error {
	notification, err := s.notifications.GetNotificationByID(ctx, payload.NotificationID)
	if err != nil {
		return err
	}
	if notification.Status == domain.StatusSent || notification.Status == domain.StatusFailed {
		return nil
	}

	notification.Attempts++
	notification.UpdatedAt = time.Now().UTC()
	if err := s.notifications.IncrementAttempts(ctx, notification.ID, notification.Attempts, notification.UpdatedAt); err != nil {
		return err
	}

	err = s.providers.Send(ctx, notification)

	logEntry := domain.DeliveryLog{
		ID:             domain.NewID("log"),
		NotificationID: notification.ID,
		Attempt:        notification.Attempts,
		CreatedAt:      time.Now().UTC(),
	}

	if err != nil {
		notification.LastError = err.Error()
		if notification.Attempts >= notification.MaxAttempts {
			notification.Status = domain.StatusFailed
			logEntry.Status = domain.StatusFailed
			logEntry.ErrorMessage = err.Error()
			if s.dlq != nil {
				_ = s.dlq.CreateDeadLetter(ctx, domain.DeadLetterJob{
					ID:             domain.NewID("dlq"),
					NotificationID: notification.ID,
					UserID:         notification.UserID,
					Payload: map[string]any{
						"notification_id": notification.ID,
						"attempt":         notification.Attempts,
						"type":            notification.Type,
						"to":              notification.To,
						"subject":         notification.Subject,
					},
					ErrorMessage: err.Error(),
					CreatedAt:    time.Now().UTC(),
				})
			}
			if s.metrics != nil {
				s.metrics.IncFailure()
			}
		} else {
			notification.Status = domain.StatusRetrying
			logEntry.Status = domain.StatusRetrying
			logEntry.ErrorMessage = err.Error()
			if enqueueErr := s.queue.Enqueue(ctx, queue.JobPayload{
				NotificationID: notification.ID,
				Attempt:        notification.Attempts + 1,
			}, s.retryDelay(notification.Attempts)); enqueueErr != nil {
				return fmt.Errorf("schedule retry: %w", enqueueErr)
			}
			if s.metrics != nil {
				s.metrics.IncRetry()
			}
		}
	} else {
		notification.Status = domain.StatusSent
		notification.LastError = ""
		logEntry.Status = domain.StatusSent
		if s.metrics != nil {
			s.metrics.IncSuccess()
		}
	}

	if updateErr := s.notifications.UpdateNotification(ctx, notification); updateErr != nil {
		return updateErr
	}
	if logErr := s.logs.AppendLog(ctx, logEntry); logErr != nil {
		return logErr
	}

	if user, userErr := s.users.GetUserByID(ctx, notification.UserID); userErr == nil {
		go s.callbacks.Notify(context.Background(), user, notification)
	}

	return err
}

func (s *Service) Get(ctx context.Context, userID, notificationID string) (domain.Notification, []domain.DeliveryLog, error) {
	notification, err := s.notifications.GetNotification(ctx, userID, notificationID)
	if err != nil {
		return domain.Notification{}, nil, err
	}
	logs, err := s.logs.ListLogs(ctx, notificationID)
	if err != nil {
		return domain.Notification{}, nil, err
	}
	return notification, logs, nil
}

func (s *Service) List(ctx context.Context, userID string) ([]domain.Notification, error) {
	return s.notifications.ListNotifications(ctx, userID)
}

func (s *Service) UpdateWebhook(ctx context.Context, userID, webhookURL string) error {
	return s.users.UpdateWebhookURL(ctx, userID, webhookURL)
}

func (s *Service) ListDeadLetters(ctx context.Context, userID string) ([]domain.DeadLetterJob, error) {
	return s.dlq.ListDeadLetters(ctx, userID)
}

func (s *Service) RetryDeadLetter(ctx context.Context, userID, deadLetterID string) error {
	job, err := s.dlq.GetDeadLetter(ctx, userID, deadLetterID)
	if err != nil {
		return err
	}
	notification, err := s.notifications.GetNotification(ctx, userID, job.NotificationID)
	if err != nil {
		return err
	}
	notification.Status = domain.StatusPending
	notification.LastError = ""
	notification.UpdatedAt = time.Now().UTC()
	if err := s.notifications.UpdateNotification(ctx, notification); err != nil {
		return err
	}
	if err := s.dlq.MarkDeadLetterRetried(ctx, deadLetterID, time.Now().UTC()); err != nil {
		return err
	}
	return s.queue.Enqueue(ctx, queue.JobPayload{NotificationID: notification.ID, Attempt: notification.Attempts + 1}, 0)
}

func (s *Service) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := s.baseBackoff * time.Duration(1<<(attempt-1))
	if delay > s.maxBackoff {
		return s.maxBackoff
	}
	return delay
}
