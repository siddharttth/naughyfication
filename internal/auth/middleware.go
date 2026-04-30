package auth

import (
	"context"
	"net/http"

	"naughtyfication/internal/domain"
	"naughtyfication/internal/repository"
)

type Middleware struct {
	users repository.UserRepository
}

func NewMiddleware(users repository.UserRepository) *Middleware {
	return &Middleware{users: users}
}

func (m *Middleware) RequireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			writeUnauthorized(w)
			return
		}

		user, err := m.users.GetUserByAPIKey(r.Context(), apiKey)
		if err != nil {
			writeUnauthorized(w)
			return
		}

		ctx := domain.ContextWithUser(r.Context(), user)
		ctx = domain.ContextWithAPIKey(ctx, apiKey)
		next(w, r.WithContext(ctx))
	}
}

func UserFromContext(ctx context.Context) (domain.User, bool) {
	return domain.UserFromContext(ctx)
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid API key"}`))
}
