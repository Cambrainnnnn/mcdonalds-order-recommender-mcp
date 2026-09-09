package products_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	app "meal-recommendation/internal/application/products"
	domain "meal-recommendation/internal/domain/product"
	"meal-recommendation/internal/infrastructure/ids"
)

func TestSearchBatch(t *testing.T) {
	productRepositories(t, func(t *testing.T, repo app.Repository) {
		ctx := context.Background()
		service := app.NewService(repo, ids.Default, time.Now)
		products := make([]*domain.Product, 0)
		for _, fields := range []app.Fields{
			{Brand: "mcd", Name: "豆浆", Specification: "小杯", Aliases: []string{"豆奶"}},
			{Brand: "mcd", Name: "豆浆", Specification: "大杯"},
			{Brand: "other", Name: "豆浆", Specification: "小杯"},
			{Brand: "mcd", Name: "豆浆", Specification: "中杯", Status: domain.StatusDisabled},
			{Brand: "mcd", Name: "薯条", Specification: "小份"},
			{Brand: "mcd", Name: "Coffee", Specification: "Small"},
		} {
			p, err := service.Create(ctx, app.CreateCommand{Fields: fields})
			if err != nil {
				t.Fatal(err)
			}
			products = append(products, p)
		}
		queries := []app.SearchQuery{
			{Name: " 豆浆 ", Specification: " 小杯 "},
			{Name: "豆浆"},
			{Name: "豆浆", Specification: "小份"}, // Must not combine different products' fields.
			{Name: "豆浆", Specification: "中杯"}, // Disabled.
			{Name: "豆"},  // No substring matching.
			{Name: "豆奶"}, // No alias expansion.
			{Name: "豆浆", Specification: "小"},
			{Name: " COFFEE ", Specification: " SMALL "},
			{Name: "豆浆", Specification: "小杯"},  // Preserve duplicates.
			{Name: "薯条", Specification: "   "}, // Blank specification is unconstrained.
		}
		response, err := service.Search(ctx, app.SearchRequest{Brand: " MCD ", Queries: queries})
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{products[0].ID}, {products[1].ID, products[0].ID}, {}, {}, {}, {}, {}, {products[5].ID}, {products[0].ID}, {products[4].ID}}
		if len(response.Results) != len(want) {
			t.Fatalf("results=%+v", response.Results)
		}
		for i, result := range response.Results {
			if result.Query != queries[i] || result.Candidates == nil {
				t.Fatalf("result %d lost original query or empty array: %+v", i, result)
			}
			got := make([]string, 0)
			for _, p := range result.Candidates {
				got = append(got, p.ID)
			}
			if !reflect.DeepEqual(got, want[i]) {
				t.Fatalf("result %d: candidates=%v want=%v", i, got, want[i])
			}
			status, resolved := app.SearchNotFound, ""
			if len(want[i]) == 1 {
				status, resolved = app.SearchExact, want[i][0]
			} else if len(want[i]) > 1 {
				status = app.SearchAmbiguous
			}
			if result.Status != status || result.ResolvedProductID != resolved {
				t.Fatalf("result %d: %+v", i, result)
			}
		}
		response.Results[0].Candidates[0].Aliases[0] = "changed"
		if response.Results[8].Candidates[0].Aliases[0] != "豆奶" {
			t.Fatal("duplicate results share mutable candidates")
		}
		allBrands, err := service.Search(ctx, app.SearchRequest{Queries: []app.SearchQuery{{Name: "豆浆", Specification: "小杯"}}})
		if err != nil || allBrands.Results[0].Status != app.SearchAmbiguous || len(allBrands.Results[0].Candidates) != 2 || allBrands.Results[0].Candidates[0].ID != products[0].ID || allBrands.Results[0].Candidates[1].ID != products[2].ID {
			t.Fatalf("cross-brand ambiguity: %+v err=%v", allBrands, err)
		}
	})
}

type searchRepositorySpy struct {
	app.Repository
	calls int
	err   error
}

func (r *searchRepositorySpy) List(ctx context.Context) ([]*domain.Product, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.Repository.List(ctx)
}

func TestSearchLoadsOnceAndDoesNotTruncate(t *testing.T) {
	repo := &searchRepositorySpy{Repository: memory.NewProductRepository()}
	service := app.NewService(repo, ids.Default, time.Now)
	for i := 124; i >= 0; i-- {
		_, err := service.Create(context.Background(), app.CreateCommand{Fields: app.Fields{Brand: "mcd", Name: "豆浆", Specification: fmt.Sprintf("规格%03d", i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	queries := make([]app.SearchQuery, app.MaxSearchQueries)
	for i := range queries {
		queries[i] = app.SearchQuery{Name: "豆浆"}
	}
	response, err := service.Search(context.Background(), app.SearchRequest{Queries: queries})
	if err != nil || repo.calls != 1 {
		t.Fatalf("calls=%d err=%v", repo.calls, err)
	}
	for _, result := range response.Results {
		if result.Status != app.SearchAmbiguous || len(result.Candidates) != 125 || result.Candidates[0].Specification != "规格000" || result.Candidates[124].Specification != "规格124" {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
}

func TestSearchValidationAndRepositoryErrors(t *testing.T) {
	repo := &searchRepositorySpy{Repository: memory.NewProductRepository()}
	service := app.NewService(repo, ids.Default, time.Now)
	valid := []app.SearchQuery{{Name: "豆浆"}}
	for _, request := range []app.SearchRequest{
		{}, {Queries: []app.SearchQuery{}}, {Queries: make([]app.SearchQuery, app.MaxSearchQueries+1)},
		{Queries: []app.SearchQuery{{Specification: "小杯"}}},
		{Queries: []app.SearchQuery{{Name: " \t "}}},
		{Queries: []app.SearchQuery{{Name: strings.Repeat("豆", 201)}}},
		{Queries: []app.SearchQuery{{Name: "豆浆\x00"}}},
		{Queries: []app.SearchQuery{{Name: "豆浆", Specification: strings.Repeat("小", 101)}}},
		{Queries: []app.SearchQuery{{Name: "豆浆", Specification: "小杯\x00"}}},
		{Brand: strings.Repeat("品", 65), Queries: valid},
		{Brand: "mcd\x00", Queries: valid},
		{Queries: []app.SearchQuery{{Name: "豆浆"}, {Name: ""}}},
	} {
		if _, err := service.Search(context.Background(), request); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid batch loaded catalog")
	}
	empty, err := service.Search(context.Background(), app.SearchRequest{Queries: valid})
	if err != nil || empty.Results[0].Status != app.SearchNotFound || empty.Results[0].Candidates == nil {
		t.Fatalf("empty catalog: %+v err=%v", empty, err)
	}
	repo.err = errors.New("storage unavailable")
	if result, err := service.Search(context.Background(), app.SearchRequest{Queries: valid}); result != nil || !errors.Is(err, repo.err) {
		t.Fatalf("storage failure became search result: %+v err=%v", result, err)
	}
	repo.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Search(ctx, app.SearchRequest{Queries: valid}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search: %v", err)
	}
}
