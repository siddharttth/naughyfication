package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"naughtyfication/internal/domain"
	"naughtyfication/internal/repository"
)

var ErrNotFound = errors.New("record not found")

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateNotification(ctx context.Context, notification domain.Notification) error {
	payload, err := json.Marshal(notification.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO notifications (
			id, user_id, type, status, to_address, subject, template, payload, attempts, max_attempts, last_error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`,
		notification.ID,
		notification.UserID,
		notification.Type,
		notification.Status,
		notification.To,
		notification.Subject,
		notification.Template,
		payload,
		notification.Attempts,
		notification.MaxAttempts,
		notification.LastError,
		notification.CreatedAt,
		notification.UpdatedAt,
	)
	return err
}

func (r *Repository) UpdateNotification(ctx context.Context, notification domain.Notification) error {
	payload, err := json.Marshal(notification.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	commandTag, err := r.pool.Exec(ctx, `
		UPDATE notifications
		SET status = $2,
			subject = $3,
			template = $4,
			payload = $5,
			attempts = $6,
			max_attempts = $7,
			last_error = $8,
			updated_at = $9
		WHERE id = $1
	`,
		notification.ID,
		notification.Status,
		notification.Subject,
		notification.Template,
		payload,
		notification.Attempts,
		notification.MaxAttempts,
		notification.LastError,
		notification.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) IncrementAttempts(ctx context.Context, notificationID string, attempts int, updatedAt time.Time) error {
	commandTag, err := r.pool.Exec(ctx, `
		UPDATE notifications
		SET attempts = $2,
			updated_at = $3
		WHERE id = $1
	`, notificationID, attempts, updatedAt)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) GetNotification(ctx context.Context, userID, notificationID string) (domain.Notification, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, type, status, to_address, subject, template, payload, attempts, max_attempts, last_error, created_at, updated_at
		FROM notifications
		WHERE id = $1 AND user_id = $2
	`, notificationID, userID)
	return scanNotification(row)
}

func (r *Repository) GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, type, status, to_address, subject, template, payload, attempts, max_attempts, last_error, created_at, updated_at
		FROM notifications
		WHERE id = $1
	`, notificationID)
	return scanNotification(row)
}

func (r *Repository) ListNotifications(ctx context.Context, userID string) ([]domain.Notification, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, type, status, to_address, subject, template, payload, attempts, max_attempts, last_error, created_at, updated_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []domain.Notification
	for rows.Next() {
		notification, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, notification)
	}
	return notifications, rows.Err()
}

func (r *Repository) AppendLog(ctx context.Context, logEntry domain.DeliveryLog) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO delivery_logs (id, notification_id, attempt, status, error_message, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, logEntry.ID, logEntry.NotificationID, logEntry.Attempt, logEntry.Status, logEntry.ErrorMessage, logEntry.CreatedAt)
	return err
}

func (r *Repository) ListLogs(ctx context.Context, notificationID string) ([]domain.DeliveryLog, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, notification_id, attempt, status, error_message, created_at
		FROM delivery_logs
		WHERE notification_id = $1
		ORDER BY created_at ASC
	`, notificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []domain.DeliveryLog
	for rows.Next() {
		var logEntry domain.DeliveryLog
		if err := rows.Scan(
			&logEntry.ID,
			&logEntry.NotificationID,
			&logEntry.Attempt,
			&logEntry.Status,
			&logEntry.ErrorMessage,
			&logEntry.CreatedAt,
		); err != nil {
			return nil, err
		}
		logs = append(logs, logEntry)
	}
	return logs, rows.Err()
}

func (r *Repository) GetUserByAPIKey(ctx context.Context, key string) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT u.id, u.email, COALESCE(u.webhook_url, ''), u.created_at
		FROM api_keys ak
		JOIN users u ON u.id = ak.user_id
		WHERE ak.key = $1
	`, key)
	return scanUser(row)
}

func (r *Repository) GetUserByID(ctx context.Context, userID string) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, email, COALESCE(webhook_url, ''), created_at
		FROM users
		WHERE id = $1
	`, userID)
	return scanUser(row)
}

func (r *Repository) UpsertUserWithAPIKey(ctx context.Context, user domain.User, apiKey string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, email, webhook_url, created_at)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		ON CONFLICT (id) DO UPDATE
		SET email = EXCLUDED.email
	`, user.ID, user.Email, user.WebhookURL, user.CreatedAt)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO api_keys (id, user_id, key, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE
		SET user_id = EXCLUDED.user_id
	`, domain.NewID("key"), user.ID, apiKey, time.Now().UTC())
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) UpdateWebhookURL(ctx context.Context, userID, webhookURL string) error {
	commandTag, err := r.pool.Exec(ctx, `
		UPDATE users
		SET webhook_url = NULLIF($2, '')
		WHERE id = $1
	`, userID, webhookURL)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) GetTemplateByName(ctx context.Context, userID, name string) (domain.Template, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, name, content, created_at
		FROM templates
		WHERE user_id = $1 AND name = $2
	`, userID, name)
	var template domain.Template
	if err := row.Scan(&template.ID, &template.UserID, &template.Name, &template.Content, &template.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Template{}, ErrNotFound
		}
		return domain.Template{}, err
	}
	return template, nil
}

func (r *Repository) CreateOrGet(ctx context.Context, record domain.IdempotencyRecord) (domain.IdempotencyRecord, repository.IdempotencyCreateResult, error) {
	commandTag, err := r.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (
			id, user_id, endpoint, key, request_hash, response_body, response_status, status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (user_id, endpoint, key) DO NOTHING
	`,
		record.ID,
		record.UserID,
		record.Endpoint,
		record.Key,
		record.RequestHash,
		record.ResponseBody,
		record.ResponseStatus,
		record.Status,
		record.CreatedAt,
		record.UpdatedAt,
	)
	if err != nil {
		return domain.IdempotencyRecord{}, repository.IdempotencyExisting, err
	}
	if commandTag.RowsAffected() == 1 {
		return record, repository.IdempotencyCreated, nil
	}

	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, endpoint, key, request_hash, response_body, response_status, status, created_at, updated_at
		FROM idempotency_keys
		WHERE user_id = $1 AND endpoint = $2 AND key = $3
	`, record.UserID, record.Endpoint, record.Key)
	var existing domain.IdempotencyRecord
	if err := row.Scan(
		&existing.ID,
		&existing.UserID,
		&existing.Endpoint,
		&existing.Key,
		&existing.RequestHash,
		&existing.ResponseBody,
		&existing.ResponseStatus,
		&existing.Status,
		&existing.CreatedAt,
		&existing.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.IdempotencyRecord{}, repository.IdempotencyExisting, ErrNotFound
		}
		return domain.IdempotencyRecord{}, repository.IdempotencyExisting, err
	}
	return existing, repository.IdempotencyExisting, nil
}

func (r *Repository) MarkCompleted(ctx context.Context, id string, responseStatus int, responseBody []byte) error {
	commandTag, err := r.pool.Exec(ctx, `
		UPDATE idempotency_keys
		SET response_status = $2,
			response_body = $3,
			status = $4,
			updated_at = $5
		WHERE id = $1
	`, id, responseStatus, responseBody, domain.IdempotencyCompleted, time.Now().UTC())
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) CreateDeadLetter(ctx context.Context, job domain.DeadLetterJob) error {
	payload, err := json.Marshal(job.Payload)
	if err != nil {
		return fmt.Errorf("marshal dead letter payload: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO dead_letter_jobs (id, notification_id, user_id, payload, error_message, created_at, retried_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, job.ID, job.NotificationID, job.UserID, payload, job.ErrorMessage, job.CreatedAt, job.RetriedAt)
	return err
}

func (r *Repository) ListDeadLetters(ctx context.Context, userID string) ([]domain.DeadLetterJob, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, notification_id, user_id, payload, error_message, created_at, retried_at
		FROM dead_letter_jobs
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []domain.DeadLetterJob
	for rows.Next() {
		job, err := scanDeadLetter(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *Repository) GetDeadLetter(ctx context.Context, userID, id string) (domain.DeadLetterJob, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, notification_id, user_id, payload, error_message, created_at, retried_at
		FROM dead_letter_jobs
		WHERE id = $1 AND user_id = $2
	`, id, userID)
	return scanDeadLetter(row)
}

func (r *Repository) MarkDeadLetterRetried(ctx context.Context, id string, retriedAt time.Time) error {
	commandTag, err := r.pool.Exec(ctx, `
		UPDATE dead_letter_jobs
		SET retried_at = $2
		WHERE id = $1
	`, id, retriedAt)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNotification(row scanner) (domain.Notification, error) {
	var notification domain.Notification
	var payload []byte
	if err := row.Scan(
		&notification.ID,
		&notification.UserID,
		&notification.Type,
		&notification.Status,
		&notification.To,
		&notification.Subject,
		&notification.Template,
		&payload,
		&notification.Attempts,
		&notification.MaxAttempts,
		&notification.LastError,
		&notification.CreatedAt,
		&notification.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Notification{}, ErrNotFound
		}
		return domain.Notification{}, err
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &notification.Payload); err != nil {
			return domain.Notification{}, err
		}
	} else {
		notification.Payload = map[string]any{}
	}
	return notification, nil
}

func scanUser(row scanner) (domain.User, error) {
	var user domain.User
	if err := row.Scan(&user.ID, &user.Email, &user.WebhookURL, &user.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, ErrNotFound
		}
		return domain.User{}, err
	}
	return user, nil
}

func scanDeadLetter(row scanner) (domain.DeadLetterJob, error) {
	var job domain.DeadLetterJob
	var payload []byte
	if err := row.Scan(&job.ID, &job.NotificationID, &job.UserID, &payload, &job.ErrorMessage, &job.CreatedAt, &job.RetriedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.DeadLetterJob{}, ErrNotFound
		}
		return domain.DeadLetterJob{}, err
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &job.Payload); err != nil {
			return domain.DeadLetterJob{}, err
		}
	}
	if job.Payload == nil {
		job.Payload = map[string]any{}
	}
	return job, nil
}
