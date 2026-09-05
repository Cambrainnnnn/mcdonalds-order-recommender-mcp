package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type sessionUseCaseStub struct {
	session      *domain.Session
	result       *app.RecommendationResponse
	startErr     error
	getErr       error
	updateErr    error
	recommendErr error
	panicStart   bool
	onStart      func(app.StartSessionCommand)
	onUpdate     func(domain.Requirement)
}

func (s sessionUseCaseStub) StartSession(_ context.Context, command app.StartSessionCommand) (*domain.Session, error) {
	if s.panicStart {
		panic("test panic")
	}
	if s.onStart != nil {
		s.onStart(command)
	}
	return s.session, s.startErr
}

func (s sessionUseCaseStub) GetSession(_ context.Context, _ string) (*domain.Session, error) {
	return s.session, s.getErr
}

func (s sessionUseCaseStub) UpdateRequirement(_ context.Context, _ string, requirement domain.Requirement) (*domain.Session, error) {
	if s.onUpdate != nil {
		s.onUpdate(requirement)
	}
	return s.session, s.updateErr
}

func (s sessionUseCaseStub) GenerateRecommendations(_ context.Context, _ string) (*app.RecommendationResponse, error) {
	return s.result, s.recommendErr
}

func newJSONRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestStartSession(t *testing.T) {
	now := time.Date(2026, time.August, 16, 1, 0, 0, 0, time.UTC)
	session := &domain.Session{ID: "session-1", State: domain.SessionCollecting, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var captured app.StartSessionCommand
	handler := NewHandler(sessionUseCaseStub{session: session, onStart: func(command app.StartSessionCommand) {
		captured = command
	}}, logger).Routes()

	body := bytes.NewBufferString(`{"orderMode":"pickup","city":"上海市"}`)
	request := newJSONRequest(http.MethodPost, "/v1/recommendation-sessions", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var decoded domain.Session
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.ID != session.ID {
		t.Fatalf("session ID = %q, want %q", decoded.ID, session.ID)
	}
	if captured.OrderMode != domain.OrderModePickup || captured.City != "上海市" {
		t.Fatalf("captured command = %#v", captured)
	}
}

func TestStartSessionRejectsUnknownFields(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(sessionUseCaseStub{}, logger).Routes()

	request := newJSONRequest(http.MethodPost, "/v1/recommendation-sessions", bytes.NewBufferString(`{"unexpected":true}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestJSONRequestProtocolErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(sessionUseCaseStub{}, logger).Routes()

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{name: "missing content type", body: `{}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_media_type"},
		{name: "wrong content type", contentType: "text/plain", body: `{}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_media_type"},
		{name: "empty body", contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "multiple objects", contentType: "application/json", body: `{} {}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "too large", contentType: "application/json", body: `{"userId":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}`, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request_too_large"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/recommendation-sessions", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), test.wantCode)
		})
	}
}

func TestUseCaseErrorsMapToHTTPResponses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name       string
		stub       sessionUseCaseStub
		method     string
		path       string
		body       io.Reader
		wantStatus int
		wantCode   string
	}{
		{name: "start invalid order mode", stub: sessionUseCaseStub{startErr: domain.ErrInvalidOrderMode}, method: http.MethodPost, path: "/v1/recommendation-sessions", body: strings.NewReader(`{}`), wantStatus: http.StatusBadRequest, wantCode: "invalid_order_mode"},
		{name: "session not found", stub: sessionUseCaseStub{getErr: app.ErrSessionNotFound}, method: http.MethodGet, path: "/v1/recommendation-sessions/missing", wantStatus: http.StatusNotFound, wantCode: "session_not_found"},
		{name: "expired requirement", stub: sessionUseCaseStub{updateErr: app.ErrSessionExpired}, method: http.MethodPut, path: "/v1/recommendation-sessions/session-1/requirement", body: strings.NewReader(`{}`), wantStatus: http.StatusGone, wantCode: "session_expired"},
		{name: "invalid requirement", stub: sessionUseCaseStub{updateErr: domain.ErrInvalidRequirement}, method: http.MethodPut, path: "/v1/recommendation-sessions/session-1/requirement", body: strings.NewReader(`{}`), wantStatus: http.StatusBadRequest, wantCode: "invalid_requirement"},
		{name: "incomplete requirement", stub: sessionUseCaseStub{recommendErr: &app.IncompleteRequirementError{Fields: []string{"mustHave"}}}, method: http.MethodPost, path: "/v1/recommendation-sessions/session-1/recommendations", wantStatus: http.StatusUnprocessableEntity, wantCode: "incomplete_requirement"},
		{name: "dependencies unavailable", stub: sessionUseCaseStub{recommendErr: app.ErrRecommendationUnavailable}, method: http.MethodPost, path: "/v1/recommendation-sessions/session-1/recommendations", wantStatus: http.StatusServiceUnavailable, wantCode: "recommendation_unavailable"},
		{name: "no candidates", stub: sessionUseCaseStub{recommendErr: app.ErrNoCandidates}, method: http.MethodPost, path: "/v1/recommendation-sessions/session-1/recommendations", wantStatus: http.StatusUnprocessableEntity, wantCode: "no_candidates"},
		{name: "unknown error", stub: sessionUseCaseStub{getErr: errors.New("database unavailable")}, method: http.MethodGet, path: "/v1/recommendation-sessions/session-1", wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(test.stub, logger).Routes()
			request := httptest.NewRequest(test.method, test.path, test.body)
			if test.body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			assertErrorCode(t, response.Body.Bytes(), test.wantCode)
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("security headers = %#v", response.Header())
			}
		})
	}
}

func TestPanicRecoveryReturnsJSONAndLogsStatus(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := NewHandler(sessionUseCaseStub{panicStart: true}, logger).Routes()
	request := newJSONRequest(http.MethodPost, "/v1/recommendation-sessions", strings.NewReader(`{}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	assertErrorCode(t, response.Body.Bytes(), "internal_error")
	if !strings.Contains(logs.String(), `"status":500`) || !strings.Contains(logs.String(), `"bytes":`) {
		t.Fatalf("request log does not include response metrics: %s", logs.String())
	}
}

func assertErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, body)
	}
	if decoded.Error.Code != want {
		t.Fatalf("error code = %q, want %q; body=%s", decoded.Error.Code, want, body)
	}
}

func TestGenerateRecommendations(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result := &app.RecommendationResponse{
		SessionID: "session-1", Status: "completed",
		Recommendations: []domain.Result{{
			Label:     domain.LabelLowestPrice,
			Candidate: domain.Candidate{ID: "candidate-1", TotalPrice: domain.Money{Amount: 3000, Currency: "CNY"}},
		}},
	}
	handler := NewHandler(sessionUseCaseStub{result: result}, logger).Routes()

	request := httptest.NewRequest(http.MethodPost, "/v1/recommendation-sessions/session-1/recommendations", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var decoded app.RecommendationResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Recommendations) != 1 || decoded.Recommendations[0].Candidate.ID != "candidate-1" {
		t.Fatalf("response = %#v", decoded)
	}
}

func TestUpdateRequirementMapsJSONToDomain(t *testing.T) {
	session := &domain.Session{ID: "session-1", State: domain.SessionReady}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var captured domain.Requirement
	handler := NewHandler(sessionUseCaseStub{session: session, onUpdate: func(requirement domain.Requirement) {
		captured = requirement
	}}, logger).Routes()
	body := `{
		"people":2,
		"orderMode":"pickup",
		"location":{"storeCode":"store-1"},
		"mustHave":[{"resolvedProductId":"BIG_MAC","quantity":2}],
		"budget":{"amount":5000,"currency":"CNY"}
	}`
	request := newJSONRequest(http.MethodPut, "/v1/recommendation-sessions/session-1/requirement", strings.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if captured.People != 2 || captured.OrderMode != domain.OrderModePickup ||
		len(captured.MustHave) != 1 || captured.MustHave[0].ResolvedProductID != "BIG_MAC" {
		t.Fatalf("captured requirement = %#v", captured)
	}
}

func TestUnknownRouteReturnsJSON404(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(sessionUseCaseStub{}, logger).Routes()
	request := httptest.NewRequest(http.MethodGet, "/v1/unknown", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	assertErrorCode(t, response.Body.Bytes(), "route_not_found")
}

func TestKnownRouteWithWrongMethodReturnsJSON405(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(sessionUseCaseStub{}, logger).Routes()
	request := httptest.NewRequest(http.MethodDelete, "/v1/recommendation-sessions/session-1", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", response.Header().Get("Allow"), "GET, HEAD")
	}
	assertErrorCode(t, response.Body.Bytes(), "method_not_allowed")
}
