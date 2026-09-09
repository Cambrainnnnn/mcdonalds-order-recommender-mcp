package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	app "meal-recommendation/internal/application/products"
	domain "meal-recommendation/internal/domain/product"
)

type ProductAdminUseCase interface {
	Create(context.Context, app.CreateCommand) (*domain.Product, error)
	Update(context.Context, string, app.Fields) (*domain.Product, error)
	Get(context.Context, string) (*domain.Product, error)
	List(context.Context, app.Filter) ([]*domain.Product, error)
	Search(context.Context, app.SearchRequest) (*app.SearchResponse, error)
}

func WithProductAdmin(products ProductAdminUseCase) HandlerOption {
	return func(h *Handler) { h.products = products }
}

func (h *Handler) registerProductRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/admin/products", h.createProduct)
	mux.HandleFunc("GET /v1/admin/products", h.listProducts(false))
	mux.HandleFunc("GET /v1/admin/products/{productId}", h.getProduct)
	mux.HandleFunc("PUT /v1/admin/products/{productId}", h.updateProduct)
	mux.HandleFunc("GET /v1/products", h.listProducts(true))
	mux.HandleFunc("POST /v1/products/search", h.searchProducts)
	mux.HandleFunc("/v1/products/search", h.methodNotAllowed("POST"))
	mux.HandleFunc("/v1/admin/products", h.methodNotAllowed("GET, HEAD, POST"))
	mux.HandleFunc("/v1/admin/products/{productId}", h.methodNotAllowed("GET, HEAD, PUT"))
	mux.HandleFunc("/v1/products", h.methodNotAllowed("GET, HEAD"))
}

func (h *Handler) searchProducts(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_product", "search options must be supplied in the request body")
		return
	}
	var request app.SearchRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeRequestError(w, err)
		return
	}
	result, err := h.products.Search(r.Context(), request)
	if err != nil {
		h.writeProductError(w, "search products", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) createProduct(w http.ResponseWriter, r *http.Request) {
	var command app.CreateCommand
	if err := decodeJSON(w, r, &command); err != nil {
		writeRequestError(w, err)
		return
	}
	p, err := h.products.Create(r.Context(), command)
	if err != nil {
		h.writeProductError(w, "create product", err)
		return
	}
	w.Header().Set("Location", "/v1/admin/products/"+p.ID)
	writeJSON(w, http.StatusCreated, p)
}

func (h *Handler) updateProduct(w http.ResponseWriter, r *http.Request) {
	var fields app.Fields
	if err := decodeJSON(w, r, &fields); err != nil {
		writeRequestError(w, err)
		return
	}
	p, err := h.products.Update(r.Context(), r.PathValue("productId"), fields)
	if err != nil {
		h.writeProductError(w, "update product", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) getProduct(w http.ResponseWriter, r *http.Request) {
	p, err := h.products.Get(r.Context(), r.PathValue("productId"))
	if err != nil {
		h.writeProductError(w, "get product", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) listProducts(activeOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		// Reject unsupported options instead of silently ignoring pagination or typos.
		for key, values := range query {
			if (key != "brand" && key != "category" && key != "q" && key != "status") || len(values) != 1 {
				writeError(w, http.StatusBadRequest, "invalid_product", "unsupported or repeated query parameter: "+key)
				return
			}
		}
		filter := app.Filter{Brand: query.Get("brand"), Category: query.Get("category"), Query: query.Get("q"), Status: domain.Status(strings.TrimSpace(query.Get("status")))}
		if activeOnly {
			if filter.Status != "" && filter.Status != domain.StatusActive {
				writeError(w, http.StatusBadRequest, "invalid_product", "this catalog only returns active products; use /v1/admin/products for other statuses")
				return
			}
			filter.Status = domain.StatusActive
		}
		products, err := h.products.List(r.Context(), filter)
		if err != nil {
			h.writeProductError(w, "list products", err)
			return
		}
		writeJSON(w, http.StatusOK, products)
	}
}

func (h *Handler) writeProductError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, app.ErrNotFound):
		writeError(w, http.StatusNotFound, "product_not_found", err.Error())
	case errors.Is(err, app.ErrConflict):
		writeError(w, http.StatusConflict, "product_conflict", err.Error())
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_product", err.Error())
	default:
		h.logger.Error(operation+" failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
