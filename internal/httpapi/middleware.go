package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"naughtyfication/internal/auth"
	"naughtyfication/internal/domain"
	"naughtyfication/internal/postgres"
	"naughtyfication/internal/repository"
)

type Middleware struct {
	idempotency repository.IdempotencyRepository
	redis       redis.UniversalClient
	logger      *zap.Logger
	rps         int
	burst       int
}

func NewMiddleware(idempotency repository.IdempotencyRepository, redis redis.UniversalClient, logger *zap.Logger, rps, burst int) *Middleware {
	return &Middleware{
		idempotency: idempotency,
		redis:       redis,
		logger:      logger,
		rps:         rps,
		burst:       burst,
	}
}

func (m *Middleware) RateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.redis == nil {
			next(w, r)
			return
		}
		apiKey, ok := domain.APIKeyFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		allowed, err := m.allowRequest(r.Context(), apiKey)
		if err != nil {
			m.logger.Warn("rate limiter degraded", zap.Error(err))
			next(w, r)
			return
		}
		if !allowed {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}

func (m *Middleware) Idempotency(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, r)
			return
		}

		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))

		hash := sha256.Sum256(body)
		record, result, err := m.idempotency.CreateOrGet(r.Context(), domain.IdempotencyRecord{
			ID:           domain.NewID("idem"),
			UserID:       user.ID,
			Endpoint:     endpoint,
			Key:          key,
			RequestHash:  hex.EncodeToString(hash[:]),
			ResponseBody: []byte{},
			Status:       domain.IdempotencyInProgress,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to register idempotency key")
			return
		}

		if result == repository.IdempotencyExisting {
			switch {
			case record.RequestHash != hex.EncodeToString(hash[:]):
				writeError(w, http.StatusConflict, "idempotency key reused with different payload")
				return
			case record.Status == domain.IdempotencyInProgress:
				writeError(w, http.StatusConflict, "request with this idempotency key is already in progress")
				return
			case record.Status == domain.IdempotencyCompleted:
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(record.ResponseStatus)
				_, _ = w.Write(record.ResponseBody)
				return
			}
		}

		recorder := newResponseRecorder(w)
		next(recorder, r)
		if err := m.idempotency.MarkCompleted(r.Context(), record.ID, recorder.statusCode, recorder.body.Bytes()); err != nil && !errors.Is(err, postgres.ErrNotFound) {
			m.logger.Warn("persist idempotency response failed", zap.Error(err), zap.String("idempotency_id", record.ID))
		}
	}
}

func (m *Middleware) allowRequest(ctx context.Context, apiKey string) (bool, error) {
	pipe := m.redis.TxPipeline()
	now := time.Now().UnixMilli()
	windowStart := now - 1000
	key := "ratelimit:" + apiKey
	member := domain.NewID("req")
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: member})
	countCmd := pipe.ZCard(ctx, key)
	pipe.Expire(ctx, key, 2*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	if m.burst <= 0 {
		m.burst = m.rps
	}
	return countCmd.Val() <= int64(m.burst), nil
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	r.body.Write(body)
	return r.ResponseWriter.Write(body)
}
