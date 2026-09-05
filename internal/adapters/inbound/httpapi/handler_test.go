package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type sessionUseCaseStub struct {
	session *domain.Session
	result  *app.RecommendationResponse
}

func (s sessionUseCaseStub) StartSession(_ context.Context, _ app.StartSessionCommand) (*domain.Session, error) {
	return s.session, nil
}

func (s sessionUseCaseStub) GetSession(_ context.Context, _ string) (*domain.Session, error) {
	return s.session, nil
}

func (s sessionUseCaseStub) UpdateRequirement(_ context.Context, _ string, _ domain.Requirement) (*domain.Session, error) {
	return s.session, nil
}

func (s sessionUseCaseStub) GenerateRecommendations(_ context.Context, _ string) (*app.RecommendationResponse, error) {
	return s.result, nil
}

func TestStartSession(t *testing.T) {
	now := time.Date(2026, time.August, 16, 1, 0, 0, 0, time.UTC)
	session := &domain.Session{ID: "session-1", State: domain.SessionCollecting, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(sessionUseCaseStub{session: session}, logger).Routes()

	body := bytes.NewBufferString(`{"orderMode":"pickup","city":"上海市"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/recommendation-sessions", body)
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
}

func TestStartSessionRejectsUnknownFields(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(sessionUseCaseStub{}, logger).Routes()

	request := httptest.NewRequest(http.MethodPost, "/v1/recommendation-sessions", bytes.NewBufferString(`{"unexpected":true}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
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
