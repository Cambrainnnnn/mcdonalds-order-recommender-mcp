package mcdmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type Catalog struct {
	client           *Client
	now              func() time.Time
	ttl, timeToStale time.Duration
	maxDetails       int
	mu               sync.RWMutex
	cache            map[string]supplyCacheEntry
}
type supplyCacheEntry struct {
	supply   app.OfficialSupply
	loadedAt time.Time
}

func NewCatalog(client *Client, now func() time.Time, ttl, timeToStale time.Duration, maxDetails int) *Catalog {
	return &Catalog{client: client, now: now, ttl: ttl, timeToStale: timeToStale, maxDetails: maxDetails, cache: make(map[string]supplyCacheEntry)}
}

func (c *Catalog) LoadSupply(ctx context.Context, requirement domain.Requirement) (app.OfficialSupply, error) {
	key := supplyKey(requirement)
	now := c.now().UTC()
	if value, ok := c.cached(key, now, false); ok {
		return value, nil
	}
	supply, err := c.load(ctx, requirement, now)
	if err == nil {
		c.mu.Lock()
		c.cache[key] = supplyCacheEntry{supply: cloneSupply(supply), loadedAt: now}
		c.mu.Unlock()
		return supply, nil
	}
	if value, ok := c.cached(key, now, true); ok {
		markSupplyStale(&value, "麦当劳 MCP 暂不可用，使用缓存菜单")
		return value, nil
	}
	// Manual offers can still be recommended when the official channel has no cache.
	return app.OfficialSupply{StoreCode: requirement.Location.StoreCode, LoadedAt: now}, nil
}

func (c *Catalog) load(ctx context.Context, requirement domain.Requirement, now time.Time) (app.OfficialSupply, error) {
	storeCode, beCode := requirement.Location.StoreCode, ""
	beType, orderType := orderParameters(requirement.OrderMode)
	if storeCode == "" {
		var stores mcdResponse[[]storeData]
		err := c.client.CallTool(ctx, "query-nearby-stores", map[string]any{"searchType": 2, "beType": beType, "city": requirement.Location.City, "keyword": requirement.Location.StoreKeyword}, &stores)
		if err != nil {
			return app.OfficialSupply{}, err
		}
		if !stores.Success || len(stores.Data) == 0 {
			return app.OfficialSupply{}, errors.New("no McDonald's store matched the requirement")
		}
		storeCode, beCode = stores.Data[0].StoreCode, stores.Data[0].BECode
	}
	args := map[string]any{"storeCode": storeCode, "beCode": beCode, "orderType": orderType, "beType": beType}
	if requirement.TargetTime != nil {
		args["reservationDate"] = requirement.TargetTime.Format("2006-01-02 15:04")
	}
	var menu mcdResponse[menuData]
	if err := c.client.CallTool(ctx, "query-meals", args, &menu); err != nil {
		return app.OfficialSupply{}, err
	}
	if !menu.Success {
		return app.OfficialSupply{}, fmt.Errorf("query meals: %s", menu.Message)
	}
	products := menuProducts(menu.Data)
	loadedDetails := 0
	for index := range products {
		if loadedDetails >= c.maxDetails || !relevantProduct(products[index], requirement) {
			continue
		}
		loadedDetails++
		detailArgs := copyMap(args)
		detailArgs["code"] = products[index].Code
		var detail mcdResponse[mealDetail]
		if err := c.client.CallTool(ctx, "query-meal-detail", detailArgs, &detail); err == nil && detail.Success {
			products[index].ChoiceGroups = choiceGroups(detail.Data)
		} else if err != nil {
			products[index].Warnings = append(products[index].Warnings, "套餐详情暂不可用")
		}
	}
	var couponResponse mcdResponse[[]couponData]
	coupons := []app.OfficialCoupon(nil)
	if err := c.client.CallTool(ctx, "query-store-coupons", args, &couponResponse); err == nil && couponResponse.Success {
		for _, coupon := range couponResponse.Data {
			codes := make([]string, 0, len(coupon.Products))
			for _, product := range coupon.Products {
				codes = append(codes, product.ProductCode)
			}
			coupons = append(coupons, app.OfficialCoupon{ID: coupon.CouponID, Code: coupon.CouponCode, ProductCodes: codes})
		}
	}
	return app.OfficialSupply{StoreCode: storeCode, BECode: beCode, Products: products, Coupons: coupons, LoadedAt: now}, nil
}

func (c *Catalog) cached(key string, now time.Time, allowStale bool) (app.OfficialSupply, bool) {
	c.mu.RLock()
	entry, ok := c.cache[key]
	c.mu.RUnlock()
	if !ok {
		return app.OfficialSupply{}, false
	}
	age := now.Sub(entry.loadedAt)
	if (!allowStale && age > c.ttl) || (allowStale && age > c.timeToStale) {
		return app.OfficialSupply{}, false
	}
	return cloneSupply(entry.supply), true
}

type Pricing struct {
	client           *Client
	now              func() time.Time
	ttl, timeToStale time.Duration
	mu               sync.RWMutex
	cache            map[string]priceCacheEntry
}
type priceCacheEntry struct {
	quote    domain.PriceQuote
	loadedAt time.Time
}

func NewPricing(client *Client, now func() time.Time, ttl, timeToStale time.Duration) *Pricing {
	return &Pricing{client: client, now: now, ttl: ttl, timeToStale: timeToStale, cache: make(map[string]priceCacheEntry)}
}
func (p *Pricing) Calculate(ctx context.Context, request app.OfficialPriceRequest) (domain.PriceQuote, error) {
	keyBytes, _ := json.Marshal(request)
	key := string(keyBytes)
	now := p.now().UTC()
	if quote, ok := p.cached(key, now, false); ok {
		return quote, nil
	}
	beType, orderType := orderParameters(request.OrderMode)
	items := make([]map[string]any, 0, len(request.Items))
	for _, item := range request.Items {
		value := map[string]any{"productCode": item.ProductCode, "quantity": item.Quantity}
		if item.CouponID != "" {
			value["couponId"] = item.CouponID
		}
		if item.CouponCode != "" {
			value["couponCode"] = item.CouponCode
		}
		if len(item.Selections) > 0 {
			value["selections"] = item.Selections
		}
		items = append(items, value)
	}
	args := map[string]any{"storeCode": request.StoreCode, "beCode": request.BECode, "orderType": orderType, "beType": beType, "items": items}
	if request.ReservationDate != "" {
		args["reservationDate"] = request.ReservationDate
	}
	var response mcdResponse[priceData]
	err := p.client.CallTool(ctx, "calculate-price", args, &response)
	if err == nil && !response.Success {
		err = fmt.Errorf("calculate price: %s", response.Message)
	}
	if err != nil {
		if quote, ok := p.cached(key, now, true); ok {
			quote.Confidence = domain.PriceVerifiedPast
			quote.Source = "mcd_mcp_cache"
			return quote, nil
		}
		return domain.PriceQuote{}, err
	}
	quote := domain.PriceQuote{Amount: domain.Money{Amount: response.Data.Price, Currency: "CNY"}, Confidence: domain.PriceLive, QuotedAt: now, Source: "mcd_mcp"}
	p.mu.Lock()
	p.cache[key] = priceCacheEntry{quote: quote, loadedAt: now}
	p.mu.Unlock()
	return quote, nil
}
func (p *Pricing) cached(key string, now time.Time, stale bool) (domain.PriceQuote, bool) {
	p.mu.RLock()
	entry, ok := p.cache[key]
	p.mu.RUnlock()
	if !ok {
		return domain.PriceQuote{}, false
	}
	age := now.Sub(entry.loadedAt)
	if (!stale && age > p.ttl) || (stale && age > p.timeToStale) {
		return domain.PriceQuote{}, false
	}
	return entry.quote, true
}

type mcdResponse[T any] struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}
type storeData struct {
	StoreCode string `json:"storeCode"`
	BECode    string `json:"beCode"`
}
type menuData struct {
	Meals map[string]struct {
		Name               string `json:"name"`
		CurrentPrice       string `json:"currentPrice"`
		CanonicalProductID string `json:"canonicalProductId"`
	} `json:"meals"`
}
type mealDetail struct {
	Code   string        `json:"code"`
	Rounds []detailRound `json:"rounds"`
}
type detailRound struct {
	ID          any    `json:"id"`
	Name        string `json:"name"`
	MinQuantity int    `json:"minQuantity"`
	MaxQuantity int    `json:"maxQuantity"`
	Choices     []struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		Quantity int    `json:"quantity"`
		Price    int64  `json:"price"`
	} `json:"choices"`
}
type couponData struct {
	CouponID   string `json:"couponId"`
	CouponCode string `json:"couponCode"`
	Products   []struct {
		ProductCode string `json:"productCode"`
	} `json:"products"`
}
type priceData struct {
	Price         int64 `json:"price"`
	ProductPrice  int64 `json:"productPrice"`
	DeliveryPrice int64 `json:"deliveryPrice"`
	Discount      int64 `json:"discount"`
}

func menuProducts(menu menuData) []app.OfficialProduct {
	result := make([]app.OfficialProduct, 0, len(menu.Meals))
	for code, meal := range menu.Meals {
		id := meal.CanonicalProductID
		if id == "" {
			id = strings.ToLower(strings.TrimSpace(meal.Name))
		}
		product := app.OfficialProduct{Code: code, Name: meal.Name, CanonicalProductID: id, Available: true}
		if cents, err := yuanToCents(meal.CurrentPrice); err == nil {
			money := domain.Money{Amount: cents, Currency: "CNY"}
			product.Price = &money
		}
		result = append(result, product)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}
func yuanToCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid price")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	fraction := int64(0)
	if len(parts) == 2 {
		digits := parts[1]
		if len(digits) == 1 {
			digits += "0"
		}
		if len(digits) > 2 {
			digits = digits[:2]
		}
		fraction, err = strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	return whole*100 + fraction, nil
}
func relevantProduct(product app.OfficialProduct, r domain.Requirement) bool {
	for _, req := range append(append([]domain.ProductRequest{}, r.MustHave...), r.NiceToHave...) {
		candidates := append([]string{req.Query, req.ResolvedProductID}, req.AllowedProductIDs...)
		for _, candidate := range candidates {
			if strings.EqualFold(strings.TrimSpace(candidate), product.Name) || strings.EqualFold(strings.TrimSpace(candidate), product.CanonicalProductID) || strings.EqualFold(strings.TrimSpace(candidate), product.Code) {
				return true
			}
		}
	}
	return false
}
func choiceGroups(detail mealDetail) []app.OfficialChoiceGroup {
	groups := make([]app.OfficialChoiceGroup, 0, len(detail.Rounds))
	for _, round := range detail.Rounds {
		group := app.OfficialChoiceGroup{ID: fmt.Sprint(round.ID), Name: round.Name, MinQuantity: round.MinQuantity, MaxQuantity: round.MaxQuantity}
		for _, choice := range round.Choices {
			group.Choices = append(group.Choices, app.OfficialChoice{Code: choice.Code, Name: choice.Name, Quantity: choice.Quantity, Price: choice.Price})
		}
		groups = append(groups, group)
	}
	return groups
}
func orderParameters(mode domain.OrderMode) (int, int) {
	switch mode {
	case domain.OrderModeDelivery:
		return 2, 2
	case domain.OrderModeDriveThru:
		return 5, 1
	default:
		return 1, 1
	}
}
func supplyKey(r domain.Requirement) string {
	target := ""
	if r.TargetTime != nil {
		target = r.TargetTime.Format("2006-01-02 15:04")
	}
	return strings.Join([]string{string(r.OrderMode), r.Location.City, r.Location.StoreCode, r.Location.StoreKeyword, target}, "|")
}
func copyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func cloneSupply(value app.OfficialSupply) app.OfficialSupply {
	data, _ := json.Marshal(value)
	var clone app.OfficialSupply
	_ = json.Unmarshal(data, &clone)
	return clone
}
func markSupplyStale(value *app.OfficialSupply, warning string) {
	for index := range value.Products {
		value.Products[index].Warnings = append(value.Products[index].Warnings, warning)
	}
}

var _ app.OfficialCatalog = (*Catalog)(nil)
var _ app.OfficialPricing = (*Pricing)(nil)
