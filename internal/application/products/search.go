package products

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	domain "meal-recommendation/internal/domain/product"
)

const MaxSearchQueries = 100

type SearchQuery struct {
	Name          string `json:"name" jsonschema:"Required complete product name; matched case-insensitively after trimming surrounding whitespace."`
	Specification string `json:"specification,omitempty" jsonschema:"Optional complete specification. Omit to search all specifications of the name."`
}

type SearchRequest struct {
	Brand   string        `json:"brand,omitempty" jsonschema:"Optional exact brand filter for the entire batch."`
	Queries []SearchQuery `json:"queries" jsonschema:"Between 1 and 100 product queries. Results preserve input order, including duplicates."`
}

type SearchStatus string

const (
	SearchExact     SearchStatus = "exact"
	SearchAmbiguous SearchStatus = "ambiguous"
	SearchNotFound  SearchStatus = "not_found"
)

type SearchResult struct {
	Query             SearchQuery       `json:"query"`
	Status            SearchStatus      `json:"status"`
	ResolvedProductID string            `json:"resolvedProductId,omitempty"`
	Candidates        []*domain.Product `json:"candidates"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// Search resolves a batch against one complete catalog snapshot. Explicit name and
// specification constraints must match the same product's complete fields.
// All matches have equal relevance, so List's brand/name/specification/ID order
// breaks ties. Never infer a unique match by truncating the candidate set.
func (s *Service) Search(ctx context.Context, request SearchRequest) (*SearchResponse, error) {
	if len(request.Queries) == 0 || len(request.Queries) > MaxSearchQueries {
		return nil, fmt.Errorf("%w: queries must contain between 1 and %d items", domain.ErrInvalid, MaxSearchQueries)
	}
	brand := strings.TrimSpace(request.Brand)
	if utf8.RuneCountInString(brand) > 64 || strings.ContainsRune(brand, '\x00') {
		return nil, fmt.Errorf("%w: brand must not exceed 64 characters or contain null characters", domain.ErrInvalid)
	}
	queries := make([]SearchQuery, len(request.Queries))
	for i, query := range request.Queries {
		query.Name = strings.TrimSpace(query.Name)
		query.Specification = strings.TrimSpace(query.Specification)
		if query.Name == "" || utf8.RuneCountInString(query.Name) > 200 || strings.ContainsRune(query.Name, '\x00') {
			return nil, fmt.Errorf("%w: queries[%d].name is required, must not exceed 200 characters or contain null characters", domain.ErrInvalid, i)
		}
		if utf8.RuneCountInString(query.Specification) > 100 || strings.ContainsRune(query.Specification, '\x00') {
			return nil, fmt.Errorf("%w: queries[%d].specification must not exceed 100 characters or contain null characters", domain.ErrInvalid, i)
		}
		queries[i] = query
	}

	catalog, err := s.List(ctx, Filter{Brand: brand})
	if err != nil {
		return nil, err
	}
	response := &SearchResponse{Results: make([]SearchResult, len(queries))}
	for i, query := range queries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := SearchResult{Query: request.Queries[i], Status: SearchNotFound, Candidates: make([]*domain.Product, 0)}
		for _, p := range catalog {
			if !strings.EqualFold(query.Name, p.Name) || (query.Specification != "" && !strings.EqualFold(query.Specification, p.Specification)) {
				continue
			}
			result.Candidates = append(result.Candidates, p.Clone())
		}
		switch len(result.Candidates) {
		case 0:
		case 1:
			result.Status = SearchExact
			result.ResolvedProductID = result.Candidates[0].ID
		default:
			result.Status = SearchAmbiguous
		}
		response.Results[i] = result
	}
	return response, nil
}
