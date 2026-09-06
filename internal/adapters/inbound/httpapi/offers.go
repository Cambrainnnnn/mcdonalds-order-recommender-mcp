package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	appoffers "meal-recommendation/internal/application/offers"
	domain "meal-recommendation/internal/domain/recommendation"
)

type offerRequest struct {
	Source       string                    `json:"source"`
	Title        string                    `json:"title"`
	Price        domain.Money              `json:"price"`
	PurchaseURL  string                    `json:"purchaseUrl"`
	City         string                    `json:"city"`
	StoreCodes   []string                  `json:"storeCodes"`
	OrderModes   []domain.OrderMode        `json:"orderModes"`
	ValidFrom    *time.Time                `json:"validFrom"`
	ValidUntil   *time.Time                `json:"validUntil"`
	ValidTimes   []appoffers.ValidTimeRule `json:"validTimes"`
	ChoiceGroups []appoffers.ChoiceGroup   `json:"choiceGroups"`
	Components   []appoffers.Component     `json:"components"`
}

func (r offerRequest) command() appoffers.CreateCommand {
	return appoffers.CreateCommand{Source: r.Source, Title: r.Title, Price: r.Price, PurchaseURL: r.PurchaseURL, City: r.City, StoreCodes: r.StoreCodes, OrderModes: r.OrderModes, ValidFrom: r.ValidFrom, ValidUntil: r.ValidUntil, ValidTimes: r.ValidTimes, ChoiceGroups: r.ChoiceGroups, Components: r.Components}
}

func (h *Handler) registerOfferAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admin/offers", h.listOffers)
	// 创建套餐接口
	mux.HandleFunc("POST /v1/admin/offers", h.createOffer)
	mux.HandleFunc("PUT /v1/admin/offers/{offerId}/draft", h.updateOfferDraft)
	mux.HandleFunc("POST /v1/admin/offers/imports", h.importOffers)
	mux.HandleFunc("POST /v1/admin/offers/{offerId}/review", h.offerAction(h.offers.Review))
	mux.HandleFunc("POST /v1/admin/offers/{offerId}/publish", h.offerAction(h.offers.Publish))
	mux.HandleFunc("POST /v1/admin/offers/{offerId}/verify", h.offerAction(h.offers.Verify))
	mux.HandleFunc("POST /v1/admin/offers/{offerId}/disable", h.offerAction(h.offers.Disable))
	mux.HandleFunc("/v1/admin/offers", h.methodNotAllowed("GET, POST"))
	mux.HandleFunc("/v1/admin/offers/imports", h.methodNotAllowed("POST"))
	mux.HandleFunc("/v1/admin/offers/{offerId}/draft", h.methodNotAllowed("PUT"))
	for _, action := range []string{"review", "publish", "verify", "disable"} {
		mux.HandleFunc("/v1/admin/offers/{offerId}/"+action, h.methodNotAllowed("POST"))
	}
}

func (h *Handler) listOffers(w http.ResponseWriter, r *http.Request) {
	offers, err := h.offers.List(r.Context())
	if err != nil {
		h.writeOfferError(w, "list offers", err)
		return
	}
	writeJSON(w, http.StatusOK, offers)
}

func (h *Handler) createOffer(w http.ResponseWriter, r *http.Request) {
	var request offerRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeRequestError(w, err)
		return
	}
	offer, err := h.offers.Create(r.Context(), request.command())
	if err != nil {
		h.writeOfferError(w, "create offer", err)
		return
	}
	writeJSON(w, http.StatusCreated, offer)
}

func (h *Handler) updateOfferDraft(w http.ResponseWriter, r *http.Request) {
	var request offerRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeRequestError(w, err)
		return
	}
	offer, err := h.offers.UpdateDraft(r.Context(), r.PathValue("offerId"), request.command())
	if err != nil {
		h.writeOfferError(w, "update offer", err)
		return
	}
	writeJSON(w, http.StatusOK, offer)
}

func (h *Handler) offerAction(action func(context.Context, string) (*appoffers.Offer, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		offer, err := action(r.Context(), r.PathValue("offerId"))
		if err != nil {
			h.writeOfferError(w, "change offer status", err)
			return
		}
		writeJSON(w, http.StatusOK, offer)
	}
}

func (h *Handler) importOffers(w http.ResponseWriter, r *http.Request) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "text/csv" && mediaType != "application/csv" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be text/csv")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	commands, err := decodeOfferCSV(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("request body must not exceed %d bytes", maxRequestBodyBytes))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_csv", err.Error())
		return
	}
	result := h.offers.Import(r.Context(), commands)
	status := http.StatusCreated
	if len(result.Errors) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func (h *Handler) writeOfferError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, appoffers.ErrOfferNotFound):
		writeError(w, http.StatusNotFound, "offer_not_found", err.Error())
	case errors.Is(err, appoffers.ErrInvalidOffer):
		writeError(w, http.StatusBadRequest, "invalid_offer", err.Error())
	case errors.Is(err, appoffers.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid_offer_transition", err.Error())
	default:
		h.logger.Error(operation+" failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func decodeOfferCSV(reader io.Reader) ([]appoffers.CreateCommand, error) {
	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true
	csvReader.ReuseRecord = false
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, errors.New("CSV requires a header and at least one data row")
	}
	header := make(map[string]int)
	for index, name := range records[0] {
		header[strings.TrimSpace(name)] = index
	}
	required := []string{"source", "title", "price_amount", "currency", "components_json"}
	for _, name := range required {
		if _, ok := header[name]; !ok {
			return nil, fmt.Errorf("missing CSV column %q", name)
		}
	}
	commands := make([]appoffers.CreateCommand, 0, len(records)-1)
	for rowIndex, row := range records[1:] {
		get := func(name string) string {
			index, ok := header[name]
			if !ok || index >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[index])
		}
		amount, err := strconv.ParseInt(get("price_amount"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("row %d invalid price_amount: %w", rowIndex+2, err)
		}
		var components []appoffers.Component
		if err := json.Unmarshal([]byte(get("components_json")), &components); err != nil {
			return nil, fmt.Errorf("row %d invalid components_json: %w", rowIndex+2, err)
		}
		var choiceGroups []appoffers.ChoiceGroup
		if value := get("choice_groups_json"); value != "" {
			if err := json.Unmarshal([]byte(value), &choiceGroups); err != nil {
				return nil, fmt.Errorf("row %d invalid choice_groups_json: %w", rowIndex+2, err)
			}
		}
		var validTimes []appoffers.ValidTimeRule
		if value := get("valid_time_rules_json"); value != "" {
			if err := json.Unmarshal([]byte(value), &validTimes); err != nil {
				return nil, fmt.Errorf("row %d invalid valid_time_rules_json: %w", rowIndex+2, err)
			}
		}
		validFrom, err := optionalTime(get("valid_from"))
		if err != nil {
			return nil, fmt.Errorf("row %d invalid valid_from: %w", rowIndex+2, err)
		}
		validUntil, err := optionalTime(get("valid_until"))
		if err != nil {
			return nil, fmt.Errorf("row %d invalid valid_until: %w", rowIndex+2, err)
		}
		modes := make([]domain.OrderMode, 0)
		for _, mode := range splitPipe(get("order_modes")) {
			modes = append(modes, domain.OrderMode(mode))
		}
		commands = append(commands, appoffers.CreateCommand{Source: get("source"), Title: get("title"), Price: domain.Money{Amount: amount, Currency: get("currency")}, PurchaseURL: get("purchase_url"), City: get("city"), StoreCodes: splitPipe(get("store_codes")), OrderModes: modes, ValidFrom: validFrom, ValidUntil: validUntil, ValidTimes: validTimes, ChoiceGroups: choiceGroups, Components: components})
	}
	return commands, nil
}

func splitPipe(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return slices.DeleteFunc(parts, func(v string) bool { return v == "" })
}
func optionalTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
