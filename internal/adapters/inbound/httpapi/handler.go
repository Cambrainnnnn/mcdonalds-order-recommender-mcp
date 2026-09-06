package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	appoffers "meal-recommendation/internal/application/offers"
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
	offers   OfferAdminUseCase
	products ProductAdminUseCase
	logger   *slog.Logger
}

type OfferAdminUseCase interface {
	Create(context.Context, appoffers.CreateCommand) (*appoffers.Offer, error)
	List(context.Context) ([]*appoffers.Offer, error)
	UpdateDraft(context.Context, string, appoffers.UpdateCommand) (*appoffers.Offer, error)
	Import(context.Context, []appoffers.CreateCommand) appoffers.ImportResult
	Review(context.Context, string) (*appoffers.Offer, error)
	Publish(context.Context, string) (*appoffers.Offer, error)
	Verify(context.Context, string) (*appoffers.Offer, error)
	Disable(context.Context, string) (*appoffers.Offer, error)
}

type HandlerOption func(*Handler)

func WithOfferAdmin(offers OfferAdminUseCase) HandlerOption {
	return func(handler *Handler) { handler.offers = offers }
}

func NewHandler(sessions SessionUseCase, logger *slog.Logger, options ...HandlerOption) *Handler {
	if sessions == nil {
		panic("httpapi: session use case is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	handler := &Handler{sessions: sessions, logger: logger}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.health)
	mux.HandleFunc("POST /v1/recommendation-sessions", h.startSession)
	mux.HandleFunc("GET /v1/recommendation-sessions/{sessionId}", h.getSession)
	mux.HandleFunc("PUT /v1/recommendation-sessions/{sessionId}/requirement", h.updateRequirement)
	mux.HandleFunc("POST /v1/recommendation-sessions/{sessionId}/recommendations", h.generateRecommendations)
	if h.offers != nil {
		h.registerOfferAdminRoutes(mux)
	}
	if h.products != nil {
		h.registerProductRoutes(mux)
	}
	mux.HandleFunc("/healthz", h.methodNotAllowed("GET, HEAD"))
	mux.HandleFunc("/readyz", h.methodNotAllowed("GET, HEAD"))
	mux.HandleFunc("/v1/recommendation-sessions", h.methodNotAllowed("POST"))
	mux.HandleFunc("/v1/recommendation-sessions/{sessionId}", h.methodNotAllowed("GET, HEAD"))
	mux.HandleFunc("/v1/recommendation-sessions/{sessionId}/requirement", h.methodNotAllowed("PUT"))
	mux.HandleFunc("/v1/recommendation-sessions/{sessionId}/recommendations", h.methodNotAllowed("POST"))
	mux.HandleFunc("/", h.notFound)
	return h.requestLog(h.recoverPanic(h.responseHeaders(mux)))
}

func (h *Handler) notFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "route_not_found", "route not found")
}

func (h *Handler) methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
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
		writeRequestError(w, err)
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
		h.writeUseCaseError(w, "start session", err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.sessions.GetSession(r.Context(), r.PathValue("sessionId"))
	if err != nil {
		h.writeUseCaseError(w, "get session", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) updateRequirement(w http.ResponseWriter, r *http.Request) {
	var requirement domain.Requirement
	if err := decodeJSON(w, r, &requirement); err != nil {
		writeRequestError(w, err)
		return
	}
	session, err := h.sessions.UpdateRequirement(r.Context(), r.PathValue("sessionId"), requirement)
	if err != nil {
		h.writeUseCaseError(w, "update requirement", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) generateRecommendations(w http.ResponseWriter, r *http.Request) {
	response, err := h.sessions.GenerateRecommendations(r.Context(), r.PathValue("sessionId"))
	if err != nil {
		h.writeUseCaseError(w, "generate recommendations", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type requestError struct {
	status  int
	code    string
	message string
}

func (e *requestError) Error() string { return e.message }

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	contentType := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "application/json" && !(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json"))) {
		return &requestError{
			status:  http.StatusUnsupportedMediaType,
			code:    "unsupported_media_type",
			message: "Content-Type must be application/json",
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &requestError{
				status:  http.StatusRequestEntityTooLarge,
				code:    "request_too_large",
				message: fmt.Sprintf("request body must not exceed %d bytes", maxRequestBodyBytes),
			}
		}
		if errors.Is(err, io.EOF) {
			return &requestError{status: http.StatusBadRequest, code: "invalid_request", message: "request body is required"}
		}
		return &requestError{status: http.StatusBadRequest, code: "invalid_request", message: err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &requestError{
			status:  http.StatusBadRequest,
			code:    "invalid_request",
			message: "request body must contain a single JSON object",
		}
	}
	return nil
}

func writeRequestError(w http.ResponseWriter, err error) {
	var requestErr *requestError
	if errors.As(err, &requestErr) {
		writeError(w, requestErr.status, requestErr.code, requestErr.message)
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "invalid request")
}

func (h *Handler) writeUseCaseError(w http.ResponseWriter, operation string, err error) {
	var incomplete *app.IncompleteRequirementError
	switch {
	case errors.Is(err, app.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "session_not_found", err.Error())
	case errors.Is(err, app.ErrSessionExpired):
		writeError(w, http.StatusGone, "session_expired", err.Error())
	case errors.Is(err, domain.ErrInvalidOrderMode):
		writeError(w, http.StatusBadRequest, "invalid_order_mode", err.Error())
	case errors.Is(err, domain.ErrInvalidRequirement):
		writeError(w, http.StatusBadRequest, "invalid_requirement", err.Error())
	case errors.As(err, &incomplete):
		writeError(w, http.StatusUnprocessableEntity, "incomplete_requirement", err.Error())
	case errors.Is(err, app.ErrRecommendationUnavailable):
		writeError(w, http.StatusServiceUnavailable, "recommendation_unavailable", err.Error())
	case errors.Is(err, app.ErrNoCandidates):
		writeError(w, http.StatusUnprocessableEntity, "no_candidates", err.Error())
	default:
		h.logger.Error(operation+" failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
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
