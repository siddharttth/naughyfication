package domain

import "time"

type NotificationType string

const (
	NotificationTypeEmail   NotificationType = "email"
	NotificationTypeSMS     NotificationType = "sms"
	NotificationTypeWebhook NotificationType = "webhook"
)

type NotificationStatus string

const (
	StatusPending  NotificationStatus = "pending"
	StatusRetrying NotificationStatus = "retrying"
	StatusSent     NotificationStatus = "sent"
	StatusFailed   NotificationStatus = "failed"
)

type User struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	WebhookURL string    `json:"webhook_url,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type APIKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
}

type Template struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type IdempotencyStatus string

const (
	IdempotencyInProgress IdempotencyStatus = "in_progress"
	IdempotencyCompleted  IdempotencyStatus = "completed"
)

type IdempotencyRecord struct {
	ID             string            `json:"id"`
	UserID         string            `json:"user_id"`
	Endpoint       string            `json:"endpoint"`
	Key            string            `json:"key"`
	RequestHash    string            `json:"request_hash"`
	ResponseBody   []byte            `json:"-"`
	ResponseStatus int               `json:"response_status"`
	Status         IdempotencyStatus `json:"status"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type DeadLetterJob struct {
	ID             string         `json:"id"`
	NotificationID string         `json:"notification_id"`
	UserID         string         `json:"user_id"`
	Payload        map[string]any `json:"payload"`
	ErrorMessage   string         `json:"error_message"`
	CreatedAt      time.Time      `json:"created_at"`
	RetriedAt      *time.Time     `json:"retried_at,omitempty"`
}

type Notification struct {
	ID          string             `json:"id"`
	UserID      string             `json:"user_id"`
	Type        NotificationType   `json:"type"`
	To          string             `json:"to"`
	Subject     string             `json:"subject,omitempty"`
	Template    string             `json:"template,omitempty"`
	Data        map[string]any     `json:"data,omitempty"`
	Status      NotificationStatus `json:"status"`
	Payload     map[string]any     `json:"payload,omitempty"`
	Attempts    int                `json:"attempts"`
	MaxAttempts int                `json:"max_attempts"`
	LastError   string             `json:"last_error,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type DeliveryLog struct {
	ID             string             `json:"id"`
	NotificationID string             `json:"notification_id"`
	Attempt        int                `json:"attempt"`
	Status         NotificationStatus `json:"status"`
	ErrorMessage   string             `json:"error_message,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}
