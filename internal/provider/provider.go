package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"go.uber.org/zap"

	"naughtyfication/internal/domain"
)

var ErrUnsupportedType = errors.New("unsupported notification type")

type Provider interface {
	Send(ctx context.Context, notification domain.Notification) error
}

type Registry struct {
	providers map[domain.NotificationType]Provider
}

func NewRegistry(providers map[domain.NotificationType]Provider) *Registry {
	return &Registry{providers: providers}
}

func (r *Registry) Send(ctx context.Context, notification domain.Notification) error {
	provider, ok := r.providers[notification.Type]
	if !ok {
		return ErrUnsupportedType
	}
	return provider.Send(ctx, notification)
}

type SMTPProvider struct {
	addr    string
	auth    smtp.Auth
	from    string
	timeout time.Duration
}

func NewSMTPProvider(host string, port int, username, password, from string, timeout time.Duration) *SMTPProvider {
	var auth smtp.Auth
	if username != "" || password != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	return &SMTPProvider{
		addr:    net.JoinHostPort(host, fmt.Sprintf("%d", port)),
		auth:    auth,
		from:    from,
		timeout: timeout,
	}
}

func (p *SMTPProvider) Send(ctx context.Context, notification domain.Notification) error {
	body := renderedBody(notification)
	message := fmt.Sprintf("To: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		notification.To,
		notification.Subject,
		body,
	)

	result := make(chan error, 1)
	go func() {
		result <- smtp.SendMail(p.addr, p.auth, p.from, []string{notification.To}, []byte(message))
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.timeout):
		return errors.New("smtp provider timeout")
	case err := <-result:
		if err != nil {
			return fmt.Errorf("smtp send failed: %w", err)
		}
		return nil
	}
}

type MockProvider struct {
	logger *zap.Logger
}

func NewMockProvider(logger *zap.Logger) *MockProvider {
	return &MockProvider{logger: logger}
}

func (p *MockProvider) Send(_ context.Context, notification domain.Notification) error {
	p.logger.Info("mock notification delivered",
		zap.String("notification_id", notification.ID),
		zap.String("type", string(notification.Type)),
		zap.String("to", notification.To),
		zap.String("subject", notification.Subject),
	)
	return nil
}

type WebhookDispatcher struct {
	client      *http.Client
	logger      *zap.Logger
	baseBackoff time.Duration
	maxRetries  int
}

func NewWebhookDispatcher(timeout, baseBackoff time.Duration, maxRetries int, logger *zap.Logger) *WebhookDispatcher {
	return &WebhookDispatcher{
		client:      &http.Client{Timeout: timeout},
		logger:      logger,
		baseBackoff: baseBackoff,
		maxRetries:  maxRetries,
	}
}

func (d *WebhookDispatcher) Notify(ctx context.Context, user domain.User, notification domain.Notification) {
	if user.WebhookURL == "" {
		return
	}

	payload := map[string]any{
		"id":         notification.ID,
		"status":     statusAlias(notification.Status),
		"type":       notification.Type,
		"to":         notification.To,
		"attempts":   notification.Attempts,
		"last_error": notification.LastError,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		d.logger.Warn("marshal webhook payload failed", zap.Error(err))
		return
	}

	for attempt := 1; attempt <= max(1, d.maxRetries); attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, user.WebhookURL, bytes.NewReader(body))
		if err != nil {
			d.logger.Warn("build webhook request failed", zap.Error(err))
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.client.Do(req)
		if err == nil && resp != nil && resp.StatusCode < 300 {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}

		fields := []zap.Field{
			zap.String("notification_id", notification.ID),
			zap.String("webhook_url", user.WebhookURL),
			zap.Int("attempt", attempt),
		}
		if err != nil {
			fields = append(fields, zap.Error(err))
		} else {
			fields = append(fields, zap.Int("status_code", resp.StatusCode))
		}
		d.logger.Warn("delivery webhook attempt failed", fields...)

		if attempt < max(1, d.maxRetries) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d.retryDelay(attempt)):
			}
		}
	}
}

func (d *WebhookDispatcher) retryDelay(attempt int) time.Duration {
	delay := d.baseBackoff * time.Duration(1<<(attempt-1))
	maxDelay := 30 * time.Second
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func renderedBody(notification domain.Notification) string {
	templateBody, _ := notification.Payload["template_body"].(string)
	body := renderTemplate(templateBody, notification.Data)
	if body == "" {
		body = renderTemplate(notification.Template, notification.Data)
	}
	if body == "" {
		body = fmt.Sprintf("Notification %s delivered by Naughtyfication", notification.ID)
	}
	return body
}

func renderTemplate(template string, data map[string]any) string {
	if template == "" {
		return ""
	}
	body := template
	for key, value := range data {
		body = strings.ReplaceAll(body, "{{"+key+"}}", fmt.Sprint(value))
	}
	return body
}

func statusAlias(status domain.NotificationStatus) string {
	if status == domain.StatusSent {
		return "delivered"
	}
	return string(status)
}
