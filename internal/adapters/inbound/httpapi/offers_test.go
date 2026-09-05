package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appoffers "meal-recommendation/internal/application/offers"
)

type offerAdminStub struct {
	offer    *appoffers.Offer
	err      error
	created  appoffers.CreateCommand
	imported []appoffers.CreateCommand
}

func (s *offerAdminStub) Create(_ context.Context, command appoffers.CreateCommand) (*appoffers.Offer, error) {
	s.created = command
	return s.offer, s.err
}
func (s *offerAdminStub) UpdateDraft(context.Context, string, appoffers.UpdateCommand) (*appoffers.Offer, error) {
	return s.offer, s.err
}
func (s *offerAdminStub) Import(_ context.Context, commands []appoffers.CreateCommand) appoffers.ImportResult {
	s.imported = commands
	return appoffers.ImportResult{Created: []*appoffers.Offer{s.offer}}
}
func (s *offerAdminStub) Review(context.Context, string) (*appoffers.Offer, error) {
	return s.offer, s.err
}
func (s *offerAdminStub) Publish(context.Context, string) (*appoffers.Offer, error) {
	return s.offer, s.err
}
func (s *offerAdminStub) Verify(context.Context, string) (*appoffers.Offer, error) {
	return s.offer, s.err
}
func (s *offerAdminStub) Disable(context.Context, string) (*appoffers.Offer, error) {
	return s.offer, s.err
}

func TestCreateOfferMapsRequest(t *testing.T) {
	admin := &offerAdminStub{offer: &appoffers.Offer{ID: "offer-1", Status: appoffers.StatusDraft}}
	handler := NewHandler(sessionUseCaseStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithOfferAdmin(admin)).Routes()
	body := `{"source":"douyin","title":"巨无霸套餐","price":{"amount":2990,"currency":"CNY"},"orderModes":["pickup"],"components":[{"canonicalProductId":"BIG_MAC","quantity":1}]}`
	request := newJSONRequest(http.MethodPost, "/v1/admin/offers", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if admin.created.Source != "douyin" || admin.created.Price.Amount != 2990 || len(admin.created.Components) != 1 {
		t.Fatalf("command=%#v", admin.created)
	}
}

func TestImportOffersParsesCSV(t *testing.T) {
	admin := &offerAdminStub{offer: &appoffers.Offer{ID: "offer-1"}}
	handler := NewHandler(sessionUseCaseStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithOfferAdmin(admin)).Routes()
	csvBody := "source,title,price_amount,currency,purchase_url,city,store_codes,order_modes,valid_from,valid_until,components_json\n" +
		"douyin,巨无霸套餐,2990,CNY,https://example.test,上海,S1|S2,pickup,2026-09-01T00:00:00Z,2026-10-01T00:00:00Z,\"[{\"\"canonicalProductId\"\":\"\"BIG_MAC\"\",\"\"quantity\"\":1}]\"\n"
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/offers/imports", strings.NewReader(csvBody))
	request.Header.Set("Content-Type", "text/csv; charset=utf-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(admin.imported) != 1 || len(admin.imported[0].StoreCodes) != 2 || admin.imported[0].Components[0].CanonicalProductID != "BIG_MAC" {
		t.Fatalf("imported=%#v", admin.imported)
	}
}

func TestOfferTransitionErrorMapping(t *testing.T) {
	admin := &offerAdminStub{err: appoffers.ErrInvalidTransition}
	handler := NewHandler(sessionUseCaseStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithOfferAdmin(admin)).Routes()
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/offers/offer-1/publish", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response.Body.Bytes(), "invalid_offer_transition")
}
