package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"naughtyfication/internal/auth"
	"naughtyfication/internal/domain"
	"naughtyfication/internal/postgres"
	"naughtyfication/internal/provider"
	"naughtyfication/internal/queue"
	"naughtyfication/internal/repository"
	"naughtyfication/internal/service"
)

type testRepo struct {
	notifications map[string]domain.Notification
	logs          []domain.DeliveryLog
	user          domain.User
	idempotency   map[string]domain.IdempotencyRecord
}

func (r *testRepo) CreateNotification(_ context.Context, notification domain.Notification) error {
	if r.notifications == nil {
		r.notifications = map[string]domain.Notification{}
	}
	r.notifications[notification.ID] = notification
	return nil
}

func (r *testRepo) UpdateNotification(_ context.Context, notification domain.Notification) error {
	r.notifications[notification.ID] = notification
	return nil
}

func (r *testRepo) IncrementAttempts(_ context.Context, notificationID string, attempts int, _ time.Time) error {
	item := r.notifications[notificationID]
	item.Attempts = attempts
	r.notifications[notificationID] = item
	return nil
}

func (r *testRepo) GetNotification(_ context.Context, userID, notificationID string) (domain.Notification, error) {
	n, ok := r.notifications[notificationID]
	if !ok || n.UserID != userID {
		return domain.Notification{}, postgres.ErrNotFound
	}
	return n, nil
}

func (r *testRepo) GetNotificationByID(_ context.Context, notificationID string) (domain.Notification, error) {
	n, ok := r.notifications[notificationID]
	if !ok {
		return domain.Notification{}, postgres.ErrNotFound
	}
	return n, nil
}

func (r *testRepo) ListNotifications(_ context.Context, userID string) ([]domain.Notification, error) {
	var items []domain.Notification
	for _, item := range r.notifications {
		if item.UserID == userID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *testRepo) AppendLog(_ context.Context, logEntry domain.DeliveryLog) error {
	r.logs = append(r.logs, logEntry)
	return nil
}

func (r *testRepo) ListLogs(_ context.Context, notificationID string) ([]domain.DeliveryLog, error) {
	var items []domain.DeliveryLog
	for _, item := range r.logs {
		if item.NotificationID == notificationID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *testRepo) GetUserByAPIKey(_ context.Context, key string) (domain.User, error) {
	if key != "dev-secret-key" {
		return domain.User{}, postgres.ErrNotFound
	}
	return r.user, nil
}

func (r *testRepo) GetUserByID(_ context.Context, userID string) (domain.User, error) {
	if userID != r.user.ID {
		return domain.User{}, postgres.ErrNotFound
	}
	return r.user, nil
}

func (r *testRepo) UpsertUserWithAPIKey(_ context.Context, _ domain.User, _ string) error {
	return nil
}

func (r *testRepo) UpdateWebhookURL(_ context.Context, userID, webhookURL string) error {
	if userID != r.user.ID {
		return postgres.ErrNotFound
	}
	r.user.WebhookURL = webhookURL
	return nil
}

func (r *testRepo) GetTemplateByName(_ context.Context, _, _ string) (domain.Template, error) {
	return domain.Template{}, postgres.ErrNotFound
}

func (r *testRepo) CreateOrGet(_ context.Context, record domain.IdempotencyRecord) (domain.IdempotencyRecord, repository.IdempotencyCreateResult, error) {
	if r.idempotency == nil {
		r.idempotency = map[string]domain.IdempotencyRecord{}
	}
	key := record.UserID + ":" + record.Endpoint + ":" + record.Key
	if existing, ok := r.idempotency[key]; ok {
		return existing, repository.IdempotencyExisting, nil
	}
	r.idempotency[key] = record
	return record, repository.IdempotencyCreated, nil
}

func (r *testRepo) MarkCompleted(_ context.Context, id string, responseStatus int, responseBody []byte) error {
	for key, record := range r.idempotency {
		if record.ID == id {
			record.ResponseStatus = responseStatus
			record.ResponseBody = responseBody
			record.Status = domain.IdempotencyCompleted
			r.idempotency[key] = record
			return nil
		}
	}
	return postgres.ErrNotFound
}

type integrationQueue struct{}

func (integrationQueue) Enqueue(_ context.Context, _ queue.JobPayload, _ time.Duration) error {
	return nil
}

type noOpCallback struct{}

func (noOpCallback) Notify(_ context.Context, _ domain.User, _ domain.Notification) {}

type deadLetterTestRepo struct{}

func (deadLetterTestRepo) CreateDeadLetter(_ context.Context, _ domain.DeadLetterJob) error {
	return nil
}

func (deadLetterTestRepo) ListDeadLetters(_ context.Context, _ string) ([]domain.DeadLetterJob, error) {
	return nil, nil
}

func (deadLetterTestRepo) GetDeadLetter(_ context.Context, _, _ string) (domain.DeadLetterJob, error) {
	return domain.DeadLetterJob{}, postgres.ErrNotFound
}

func (deadLetterTestRepo) MarkDeadLetterRetried(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func TestNotifyEndpointAccepted(t *testing.T) {
	t.Parallel()

	repo := &testRepo{
		user: domain.User{ID: "user_1", Email: "user@example.com"},
	}
	svc := service.New(
		repo,
		repo,
		repo,
		repo,
		deadLetterTestRepo{},
		integrationQueue{},
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: provider.NewMockProvider(zap.NewNop()),
		}),
		noOpCallback{},
		nil,
		3,
		time.Second,
		8*time.Second,
	)

	api := New(auth.NewMiddleware(repo), NewMiddleware(repo, nil, zap.NewNop(), 100, 100), func(http.ResponseWriter, *http.Request) {}, svc)
	server := httptest.NewServer(api.Router())
	defer server.Close()

	body, _ := json.Marshal(map[string]any{
		"type":          "email",
		"to":            "user@example.com",
		"subject":       "Welcome",
		"template_body": "Hi {{name}}",
		"data": map[string]any{
			"name": "Siddharth",
		},
	})

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/notify", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "dev-secret-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", resp.StatusCode)
	}
}

func TestNotifyEndpointIdempotentReplay(t *testing.T) {
	t.Parallel()

	repo := &testRepo{
		user: domain.User{ID: "user_1", Email: "user@example.com"},
	}
	svc := service.New(
		repo,
		repo,
		repo,
		repo,
		deadLetterTestRepo{},
		integrationQueue{},
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: provider.NewMockProvider(zap.NewNop()),
		}),
		noOpCallback{},
		nil,
		3,
		time.Second,
		8*time.Second,
	)

	api := New(auth.NewMiddleware(repo), NewMiddleware(repo, nil, zap.NewNop(), 100, 100), func(http.ResponseWriter, *http.Request) {}, svc)
	server := httptest.NewServer(api.Router())
	defer server.Close()

	body, _ := json.Marshal(map[string]any{
		"type":          "email",
		"to":            "user@example.com",
		"subject":       "Welcome",
		"template_body": "Hi {{name}}",
		"data": map[string]any{
			"name": "Siddharth",
		},
	})

	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/notify", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "dev-secret-key")
		req.Header.Set("Idempotency-Key", "idem-123")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("execute request: %v", err)
		}
		return resp
	}

	first := request()
	defer first.Body.Close()
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("expected first request 202, got %d", first.StatusCode)
	}

	second := request()
	defer second.Body.Close()
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("expected replay request 202, got %d", second.StatusCode)
	}
	if second.Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatal("expected replay header")
	}
}

func TestNotifyEndpointIdempotencyConflictOnDifferentPayload(t *testing.T) {
	t.Parallel()

	repo := &testRepo{
		user: domain.User{ID: "user_1", Email: "user@example.com"},
	}
	svc := service.New(
		repo,
		repo,
		repo,
		repo,
		deadLetterTestRepo{},
		integrationQueue{},
		provider.NewRegistry(map[domain.NotificationType]provider.Provider{
			domain.NotificationTypeEmail: provider.NewMockProvider(zap.NewNop()),
		}),
		noOpCallback{},
		nil,
		3,
		time.Second,
		8*time.Second,
	)

	api := New(auth.NewMiddleware(repo), NewMiddleware(repo, nil, zap.NewNop(), 100, 100), func(http.ResponseWriter, *http.Request) {}, svc)
	server := httptest.NewServer(api.Router())
	defer server.Close()

	request := func(payload map[string]any) *http.Response {
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/notify", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "dev-secret-key")
		req.Header.Set("Idempotency-Key", "idem-123")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("execute request: %v", err)
		}
		return resp
	}

	first := request(map[string]any{"type": "email", "to": "user@example.com"})
	defer first.Body.Close()
	second := request(map[string]any{"type": "email", "to": "another@example.com"})
	defer second.Body.Close()

	if second.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d", second.StatusCode)
	}
}
