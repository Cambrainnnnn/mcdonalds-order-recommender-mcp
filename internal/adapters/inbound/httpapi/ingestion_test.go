package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	offers "meal-recommendation/internal/application/offers"
	"meal-recommendation/internal/infrastructure/ids"
)

func TestOfferIngestionHTTP(t *testing.T) {
	o := memory.NewOfferRepository(time.Now, time.Hour)
	p := memory.NewProductRepository()
	service := offers.NewService(o, ids.Default, time.Now, offers.WithProductCatalog(p), offers.WithIngestionStore(memory.NewIngestionStore(o, p)))
	h := NewHandler(sessionUseCaseStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithOfferAdmin(service)).Routes()
	body := `{"source":"meituan","externalId":"sku-1","title":"4选2","price":{"amount":2290,"currency":"CNY"},"choiceGroups":[{"id":"123","minSelect":2,"maxSelect":2,"allowRepeat":true}],"components":[{"quantity":2,"choiceGroupId":"123","extraPrice":100,"extraPriceMode":"per_unit","maxRepeat":2}],"productBindings":[{"componentIndex":0,"product":{"brand":"mcd","name":"薯条","specification":"中份"}}]}`
	call := func(method, path, payload, key string) *httptest.ResponseRecorder {
		r := newJSONRequest(method, path, strings.NewReader(payload))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := call(http.MethodPost, "/v1/admin/offers", body, "screenshot-1")
	if first.Code != 201 {
		t.Fatalf("create %d %s", first.Code, first.Body.String())
	}
	var result offers.IngestionResult
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Offer.Status != offers.StatusDraft || !result.Products[0].Created {
		t.Fatal("bad ingestion response")
	}
	retry := call(http.MethodPost, "/v1/admin/offers", body, "screenshot-1")
	if retry.Code != 201 || retry.Body.String() != first.Body.String() {
		t.Fatalf("replay %d %s", retry.Code, retry.Body.String())
	}
	mismatch := call(http.MethodPost, "/v1/admin/offers", strings.Replace(body, "2290", "2390", 1), "screenshot-1")
	if mismatch.Code != 409 || !strings.Contains(mismatch.Body.String(), "idempotency_conflict") {
		t.Fatalf("mismatch %d %s", mismatch.Code, mismatch.Body.String())
	}
	duplicate := call(http.MethodPost, "/v1/admin/offers", body, "other-key")
	if duplicate.Code != 409 || !strings.Contains(duplicate.Body.String(), "offer_conflict") {
		t.Fatalf("duplicate %d %s", duplicate.Code, duplicate.Body.String())
	}
	update := call(http.MethodPut, "/v1/admin/offers/"+result.Offer.ID+"/draft", strings.Replace(body, "2290", "2390", 1), "edit-1")
	if update.Code != 200 {
		t.Fatalf("update %d %s", update.Code, update.Body.String())
	}
	unknown := call(http.MethodPost, "/v1/admin/offers", strings.Replace(body, `"brand":"mcd"`, `"guess":true,"brand":"mcd"`, 1), "unknown")
	if unknown.Code != 400 {
		t.Fatalf("unknown nested field: %d", unknown.Code)
	}
	invalid := call(http.MethodPost, "/v1/admin/offers", strings.Replace(body, `"componentIndex":0`, `"componentIndex":99`, 1), "bad-binding")
	if invalid.Code != 400 {
		t.Fatalf("invalid binding: %d", invalid.Code)
	}
	for _, action := range []string{"review", "publish"} {
		w := call(http.MethodPost, "/v1/admin/offers/"+result.Offer.ID+"/"+action, "", "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	replay := call(http.MethodPost, "/v1/admin/offers", body, "screenshot-1")
	if replay.Code != 201 || replay.Body.String() != first.Body.String() {
		t.Fatal("published state changed original replay")
	}
}
