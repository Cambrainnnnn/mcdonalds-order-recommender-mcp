package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

const maxRequestBodyBytes = 1 << 20

type SessionUseCase interface {
	StartSession(ctx context.Context, command app.StartSessionCommand) (*domain.Session, error)
	GetSession(ctx context.Context, id string) (*domain.Session, error)
	UpdateRequirement(ctx context.Context, id string, requirement domain.Requirement) (*domain.Session, error)
	GenerateRecommendations(ctx context.Context, id string) (*app.RecommendationResponse, error)
}

type Handler struct {
	sessions SessionUseCase
	logger   *slog.Logger
}

func NewHandler(sessions SessionUseCase, logger *slog.Logger) *Handler {
	if sessions == nil {
		panic("httpapi: session use case is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{sessions: sessions, logger: logger}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.health)
	mux.HandleFunc("POST /v1/recommendation-sessions", h.startSession)
	mux.HandleFunc("GET /v1/recommendation-sessions/{sessionId}", h.getSession)
	mux.HandleFunc("PUT /v1/recommendation-sessions/{sessionId}/requirement", h.updateRequirement)
	mux.HandleFunc("POST /v1/recommendation-sessions/{sessionId}/recommendations", h.generateRecommendations)
	return h.recoverPanic(h.requestLog(mux))
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type startSessionRequest struct {
	UserID       string           `json:"userId"`
	OrderMode    domain.OrderMode `json:"orderMode"`
	City         string           `json:"city"`
	StoreCode    string           `json:"storeCode"`
	StoreKeyword string           `json:"storeKeyword"`
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) {
	var request startSessionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	session, err := h.sessions.StartSession(r.Context(), app.StartSessionCommand{
		UserID:       request.UserID,
		OrderMode:    request.OrderMode,
		City:         request.City,
		StoreCode:    request.StoreCode,
		StoreKeyword: request.StoreKeyword,
	})
	if err != nil {
		if errors.Is(err, domain.ErrInvalidOrderMode) {
			writeError(w, http.StatusBadRequest, "invalid_order_mode", err.Error())
			return
		}
		h.logger.Error("start session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create recommendation session")
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.sessions.GetSession(r.Context(), r.PathValue("sessionId"))
	if err != nil {
		if errors.Is(err, app.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "session_not_found", err.Error())
			return
		}
		h.logger.Error("get session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to get recommendation session")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) updateRequirement(w http.ResponseWriter, r *http.Request) {
	var requirement domain.Requirement
	if err := decodeJSON(w, r, &requirement); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	session, err := h.sessions.UpdateRequirement(r.Context(), r.PathValue("sessionId"), requirement)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrSessionNotFound):
			writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, app.ErrSessionExpired):
			writeError(w, http.StatusGone, "session_expired", err.Error())
		case errors.Is(err, domain.ErrInvalidOrderMode), errors.Is(err, domain.ErrInvalidRequirement):
			writeError(w, http.StatusBadRequest, "invalid_requirement", err.Error())
		default:
			h.logger.Error("update requirement failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to update recommendation requirement")
		}
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) generateRecommendations(w http.ResponseWriter, r *http.Request) {
	response, err := h.sessions.GenerateRecommendations(r.Context(), r.PathValue("sessionId"))
	if err != nil {
		var incomplete *app.IncompleteRequirementError
		switch {
		case errors.Is(err, app.ErrSessionNotFound):
			writeError(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, app.ErrSessionExpired):
			writeError(w, http.StatusGone, "session_expired", err.Error())
		case errors.As(err, &incomplete):
			writeError(w, http.StatusUnprocessableEntity, "incomplete_requirement", err.Error())
		case errors.Is(err, app.ErrRecommendationUnavailable):
			writeError(w, http.StatusServiceUnavailable, "recommendation_unavailable", err.Error())
		case errors.Is(err, app.ErrNoCandidates):
			writeError(w, http.StatusUnprocessableEntity, "no_candidates", err.Error())
		default:
			h.logger.Error("generate recommendations failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to generate recommendations")
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
