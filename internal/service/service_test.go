package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"naughtyfication/internal/domain"
	"naughtyfication/internal/postgres"
	"naughtyfication/internal/provider"
	"naughtyfication/internal/queue"
)

type notificationRepoStub struct {
	notifications map[string]domain.Notification
}

func (r *notificationRepoStub) CreateNotification(_ context.Context, notification domain.Notification) error {
	if r.notifications == nil {
		r.notifications = map[string]domain.Notification{}
	}
	r.notifications[notification.ID] = notification
	return nil
}

func (r *notificationRepoStub) UpdateNotification(_ context.Context, notification domain.Notification) error {
	r.notifications[notification.ID] = notification
	return nil
}

func (r *notificationRepoStub) IncrementAttempts(_ context.Context, notificationID string, attempts int, _ time.Time) error {
	n := r.notifications[notificationID]
	n.Attempts = attempts
	r.notifications[notificationID] = n
	return nil
}

func (r *notificationRepoStub) GetNotification(_ context.Context, userID, notificationID string) (domain.Notification, error) {
	n, ok := r.notifications[notificationID]
	if !ok || n.UserID != userID {
		return domain.Notification{}, postgres.ErrNotFound
	}
	return n, nil
}

func (r *notificationRepoStub) GetNotificationByID(_ context.Context, notificationID string) (domain.Notification, error) {
	n, ok := r.notifications[notificationID]
	if !ok {
		return domain.Notification{}, postgres.ErrNotFound
	}
	return n, nil
}

func (r *notificationRepoStub) ListNotifications(_ context.Context, userID string) ([]domain.Notification, error) {
	var items []domain.Notification
	for _, n := range r.notifications {
		if n.UserID == userID {
			items = append(items, n)
		}
	}
	return items, nil
}

type logRepoStub struct {
	items []domain.DeliveryLog
}

func (r *logRepoStub) AppendLog(_ context.Context, logEntry domain.DeliveryLog) error {
	r.items = append(r.items, logEntry)
	return nil
}

func (r *logRepoStub) ListLogs(_ context.Context, notificationID string) ([]domain.DeliveryLog, error) {
	var items []domain.DeliveryLog
	for _, item := range r.items {
		if item.NotificationID == notificationID {
			items = append(items, item)
		}
	}
	return items, nil
}

type userRepoStub struct {
	users map[string]domain.User
}

func (r *userRepoStub) GetUserByAPIKey(_ context.Context, _ string) (domain.User, error) {
	return domain.User{}, postgres.ErrNotFound
}

func (r *userRepoStub) GetUserByID(_ context.Context, userID string) (domain.User, error) {
	user, ok := r.users[userID]
	if !ok {
		return domain.User{}, postgres.ErrNotFound
	}
	return user, nil
}

func (r *userRepoStub) UpsertUserWithAPIKey(_ context.Context, _ domain.User, _ string) error {
	return nil
}

func (r *userRepoStub) UpdateWebhookURL(_ context.Context, userID, webhookURL string) error {
	user := r.users[userID]
	user.WebhookURL = webhookURL
	r.users[userID] = user
	return nil
}

type templateRepoStub struct{}

func (templateRepoStub) GetTemplateByName(_ context.Context, _, _ string) (domain.Template, error) {
	return domain.Template{}, postgres.ErrNotFound
}

type deadLetterRepoStub struct {
	items []domain.DeadLetterJob
}

func (r *deadLetterRepoStub) CreateDeadLetter(_ context.Context, job domain.DeadLetterJob) error {
	r.items = append(r.items, job)
	return nil
}

func (r *deadLetterRepoStub) ListDeadLetters(_ context.Context, userID string) ([]domain.DeadLetterJob, error) {
	var items []domain.DeadLetterJob
	for _, item := range r.items {
		if item.UserID == userID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *deadLetterRepoStub) GetDeadLetter(_ context.Context, userID, id string) (domain.DeadLetterJob, error) {
	for _, item := range r.items {
		if item.UserID == userID && item.ID == id {
			return item, nil
		}
	}
	return domain.DeadLetterJob{}, postgres.ErrNotFound
}

func (r *deadLetterRepoStub) MarkDeadLetterRetried(_ context.Context, id string, retriedAt time.Time) error {
	for index, item := range r.items {
		if item.ID == id {
			r.items[index].RetriedAt = &retriedAt
			return nil
		}
	}
	return postgres.ErrNotFound
}

type queueStub struct {
	jobs []queue.JobPayload
	err  error
}

func (q *queueStub) Enqueue(_ context.Context, payload queue.JobPayload, _ time.Duration) error {
	if q.err != nil {
		return q.err
	}
	q.jobs = append(q.jobs, payload)
	return nil
}

type providerStub struct {
	err error
}

func (p providerStub) Send(_ context.Context, _ domain.Notification) error {
	return p.err
}

type callbackStub struct {
	notifications []domain.Notification
}

func (c *callbackStub) Notify(_ context.Context, _ domain.User, notification domain.Notification) {
	c.notifications = append(c.notifications, notification)
}

func TestSubmitAndProcessEmailNotification(t *testing.T) {
	t.Parallel()

	notifications := &notificationRepoStub{}
	logs := &logRepoStub{}
	users := &userRepoStub{users: map[string]domain.User{
		"user_123": {ID: "user_123", Email: "user@example.com"},
	}}
	queueStub := &queueStub{}
	callbacks := &callbackStub{}
	dlq := &deadLetterRepoStub{}
	svc := New(
		notifications,
		logs,
		users,
		templateRepoStub{},
		dlq,
		queueStub,
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: providerStub{},
		}),
		callbacks,
		nil,
		3,
		time.Second,
		8*time.Second,
	)

	notification, err := svc.Submit(context.Background(), DeliveryRequest{
		UserID:       "user_123",
		Type:         domain.NotificationTypeEmail,
		To:           "user@example.com",
		Subject:      "Welcome",
		TemplateBody: "Hi {{name}}, your code is {{otp}}.",
		Data: map[string]any{
			"name": "Siddharth",
			"otp":  "123456",
		},
	})
	if err != nil {
		t.Fatalf("submit notification: %v", err)
	}
	if len(queueStub.jobs) != 1 {
		t.Fatalf("expected one enqueued job, got %d", len(queueStub.jobs))
	}

	if err := svc.Process(context.Background(), queue.JobPayload{NotificationID: notification.ID, Attempt: 1}); err != nil {
		t.Fatalf("process notification: %v", err)
	}

	stored, deliveryLogs, err := svc.Get(context.Background(), "user_123", notification.ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if stored.Status != domain.StatusSent {
		t.Fatalf("expected sent status, got %q", stored.Status)
	}
	if len(deliveryLogs) != 2 {
		t.Fatalf("expected 2 delivery logs, got %d", len(deliveryLogs))
	}
}

func TestRetrySchedulesDelayedJob(t *testing.T) {
	t.Parallel()

	notifications := &notificationRepoStub{
		notifications: map[string]domain.Notification{
			"notif_1": {
				ID:          "notif_1",
				UserID:      "user_123",
				Type:        domain.NotificationTypeEmail,
				To:          "user@example.com",
				Status:      domain.StatusPending,
				Payload:     map[string]any{"template_body": "hi"},
				MaxAttempts: 3,
			},
		},
	}
	logs := &logRepoStub{}
	users := &userRepoStub{users: map[string]domain.User{
		"user_123": {ID: "user_123", Email: "user@example.com"},
	}}
	queueStub := &queueStub{}
	dlq := &deadLetterRepoStub{}
	svc := New(
		notifications,
		logs,
		users,
		templateRepoStub{},
		dlq,
		queueStub,
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: providerStub{err: errors.New("boom")},
		}),
		&callbackStub{},
		nil,
		2,
		time.Second,
		4*time.Second,
	)

	err := svc.Process(context.Background(), queue.JobPayload{NotificationID: "notif_1", Attempt: 1})
	if err == nil {
		t.Fatal("expected process error")
	}
	if len(queueStub.jobs) != 1 {
		t.Fatalf("expected one retry job, got %d", len(queueStub.jobs))
	}
	if notifications.notifications["notif_1"].Status != domain.StatusRetrying {
		t.Fatalf("expected retrying status, got %q", notifications.notifications["notif_1"].Status)
	}
}

func TestRetryExhaustionMovesToDLQ(t *testing.T) {
	t.Parallel()

	notifications := &notificationRepoStub{
		notifications: map[string]domain.Notification{
			"notif_1": {
				ID:          "notif_1",
				UserID:      "user_123",
				Type:        domain.NotificationTypeEmail,
				To:          "user@example.com",
				Status:      domain.StatusRetrying,
				Payload:     map[string]any{"template_body": "hi"},
				Attempts:    1,
				MaxAttempts: 2,
			},
		},
	}
	dlq := &deadLetterRepoStub{}
	svc := New(
		notifications,
		&logRepoStub{},
		&userRepoStub{users: map[string]domain.User{"user_123": {ID: "user_123", Email: "user@example.com"}}},
		templateRepoStub{},
		dlq,
		&queueStub{},
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: providerStub{err: errors.New("boom")},
		}),
		&callbackStub{},
		nil,
		1,
		time.Second,
		4*time.Second,
	)

	err := svc.Process(context.Background(), queue.JobPayload{NotificationID: "notif_1", Attempt: 2})
	if err == nil {
		t.Fatal("expected process error")
	}
	if notifications.notifications["notif_1"].Status != domain.StatusFailed {
		t.Fatalf("expected failed status, got %q", notifications.notifications["notif_1"].Status)
	}
	if len(dlq.items) != 1 {
		t.Fatalf("expected one dead letter job, got %d", len(dlq.items))
	}
}

func TestSubmitReturnsQueueFailure(t *testing.T) {
	t.Parallel()

	svc := New(
		&notificationRepoStub{},
		&logRepoStub{},
		&userRepoStub{users: map[string]domain.User{"user_123": {ID: "user_123", Email: "user@example.com"}}},
		templateRepoStub{},
		&deadLetterRepoStub{},
		&queueStub{err: errors.New("redis unavailable")},
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: providerStub{},
		}),
		&callbackStub{},
		nil,
		2,
		time.Second,
		4*time.Second,
	)

	_, err := svc.Submit(context.Background(), DeliveryRequest{
		UserID: "user_123",
		Type:   domain.NotificationTypeEmail,
		To:     "user@example.com",
	})
	if err == nil {
		t.Fatal("expected queue failure")
	}
}
