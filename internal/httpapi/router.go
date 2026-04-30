package httpapi

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"naughtyfication/internal/auth"
	"naughtyfication/internal/domain"
	"naughtyfication/internal/postgres"
	"naughtyfication/internal/service"
)

type API struct {
	auth    *auth.Middleware
	mw      *Middleware
	metrics http.HandlerFunc
	service *service.Service
}

//go:embed static/*
var staticFS embed.FS

func New(auth *auth.Middleware, mw *Middleware, metrics http.HandlerFunc, service *service.Service) *API {
	return &API{
		auth:    auth,
		mw:      mw,
		metrics: metrics,
		service: service,
	}
}

func (a *API) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /metrics", a.metrics)
	mux.HandleFunc("GET /docs", a.handleDocs)
	mux.HandleFunc("GET /openapi.yaml", a.handleOpenAPI)
	mux.HandleFunc("POST /v1/notify", a.auth.RequireAPIKey(a.mw.RateLimit(a.mw.Idempotency("/v1/notify", a.handleNotify))))
	mux.HandleFunc("GET /v1/notifications", a.auth.RequireAPIKey(a.mw.RateLimit(a.handleListNotifications)))
	mux.HandleFunc("GET /v1/notifications/", a.auth.RequireAPIKey(a.mw.RateLimit(a.handleGetNotification)))
	mux.HandleFunc("PUT /v1/webhook", a.auth.RequireAPIKey(a.mw.RateLimit(a.handleWebhook)))
	mux.HandleFunc("GET /v1/dlq", a.auth.RequireAPIKey(a.mw.RateLimit(a.handleDLQList)))
	mux.HandleFunc("POST /v1/dlq/", a.auth.RequireAPIKey(a.mw.RateLimit(a.handleDLQRetry)))
	return mux
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) handleNotify(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	var request struct {
		Type         domain.NotificationType `json:"type"`
		To           string                  `json:"to"`
		Subject      string                  `json:"subject"`
		Template     string                  `json:"template"`
		TemplateBody string                  `json:"template_body"`
		Data         map[string]any          `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	if request.To == "" || request.Type == "" {
		writeError(w, http.StatusBadRequest, "`type` and `to` are required")
		return
	}

	notification, err := a.service.Submit(r.Context(), service.DeliveryRequest{
		UserID:       user.ID,
		Type:         request.Type,
		To:           request.To,
		Subject:      request.Subject,
		Template:     request.Template,
		TemplateBody: request.TemplateBody,
		Data:         request.Data,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUnsupportedType):
			writeError(w, http.StatusBadRequest, "only email notifications are enabled in this MVP")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusAccepted, notification)
}

func (a *API) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	notifications, err := a.service.List(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": notifications})
}

func (a *API) handleGetNotification(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/v1/notifications/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "notification id is required")
		return
	}

	notification, logs, err := a.service.Get(r.Context(), user.ID, id)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrNotFound):
			writeError(w, http.StatusNotFound, "notification not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"notification": notification,
		"logs":         logs,
	})
}

func (a *API) handleWebhook(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	var request struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	if err := a.service.UpdateWebhook(r.Context(), user.ID, request.URL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (a *API) handleDLQList(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	items, err := a.service.ListDeadLetters(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) handleDLQRetry(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/retry") {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/dlq/"), "/retry")
	if id == "" {
		writeError(w, http.StatusBadRequest, "dead letter id is required")
		return
	}
	if err := a.service.RetryDeadLetter(r.Context(), user.ID, id); err != nil {
		switch {
		case errors.Is(err, postgres.ErrNotFound):
			writeError(w, http.StatusNotFound, "dead letter job not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "requeued"})
}

func (a *API) handleDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<html><body><h1>Naughyfication API Docs</h1><p><a href="/openapi.yaml">OpenAPI Spec</a></p></body></html>`)
}

func (a *API) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	body, err := staticFS.ReadFile("static/openapi.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load openapi spec")
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
