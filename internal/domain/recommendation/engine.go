package recommendation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const OfficialChannel = "official"

var ErrIncompleteRequirement = errors.New("requirement is incomplete")

// CatalogOption is the normalized input understood by the deterministic
// recommendation engine. Adapters are responsible for converting external
// offer/menu data into this representation.
type CatalogOption struct {
	ID             string
	Channel        string
	OfferVersionID string
	Products       []CandidateProduct
	Price          PriceQuote
	Eligibility    EligibilityStatus
	PurchaseURL    string
	Warnings       []string
}

type EngineInput struct {
	Requirement Requirement
	Options     []CatalogOption
}

type ScoringPolicy struct {
	Version string
	Weights ObjectiveWeights
}

type Engine struct{}

func NewEngine() Engine { return Engine{} }

// Generate builds the bounded MVP search space: a third-party offer by itself,
// a third-party offer plus one official supplement leg, and an official cart
// containing all must-have products. Official nice-to-have variants are also
// generated one at a time. No candidate has more than two purchase legs.
func (Engine) Generate(input EngineInput) ([]Candidate, error) {
	if missing := input.Requirement.MissingFields(); len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrIncompleteRequirement, strings.Join(missing, ", "))
	}

	official := make([]CatalogOption, 0)
	thirdParty := make([]CatalogOption, 0)
	for _, option := range input.Options {
		if option.Eligibility == EligibilityIneligible || len(option.Products) == 0 {
			continue
		}
		if option.Channel == OfficialChannel {
			official = append(official, option)
		} else {
			thirdParty = append(thirdParty, option)
		}
	}

	candidates := make([]Candidate, 0, len(thirdParty)*2+2)
	seen := make(map[string]struct{})
	add := func(legs ...PurchaseLeg) {
		candidate := newCandidate(legs, input.Requirement.Budget)
		if _, exists := seen[candidate.ID]; exists {
			return
		}
		seen[candidate.ID] = struct{}{}
		candidates = append(candidates, candidate)
	}

	for _, offer := range thirdParty {
		if !overlapsRequests(offer.Products, input.Requirement.MustHave) {
			continue
		}
		offerLeg := optionLeg(offer)
		add(offerLeg)

		missing := missingProducts(input.Requirement.MustHave, offer.Products)
		if supplement, ok := officialLegForRequests(missing, official); ok && len(supplement.Products) > 0 {
			add(offerLeg, supplement)
		}
	}

	if officialLeg, ok := officialLegForRequests(input.Requirement.MustHave, official); ok {
		add(officialLeg)
		for _, preferred := range input.Requirement.NiceToHave {
			variantRequests := append(slices.Clone(input.Requirement.MustHave), preferred)
			if variant, found := officialLegForRequests(variantRequests, official); found {
				add(variant)
			}
		}
	}

	return candidates, nil
}

// Rank applies hard constraints, calculates explainable score components,
// removes Pareto-dominated candidates, then selects distinct lowest-price,
// best-match and best-value results.
func (Engine) Rank(requirement Requirement, candidates []Candidate, policy ScoringPolicy) ([]Result, error) {
	weights := policy.Weights
	if weights == (ObjectiveWeights{}) {
		weights = requirement.Objectives
	}
	if weights == (ObjectiveWeights{}) {
		weights = DefaultObjectives()
	}

	eligible := make([]Candidate, 0, len(candidates))
	for _, original := range candidates {
		candidate := cloneCandidate(original)
		candidate.TotalPrice = totalPrice(candidate.PurchaseLegs, requirement.Budget)
		if !passesHardConstraints(requirement, &candidate) {
			continue
		}
		candidate.Scores = scoreCandidate(requirement, candidate)
		candidate.UnwantedProductIDs = unwantedProductIDs(requirement, candidate)
		candidate.Reasons = candidateReasons(requirement, candidate)
		candidate.Warnings = candidateWarnings(candidate)
		eligible = append(eligible, candidate)
	}

	frontier := paretoFrontier(eligible)
	if len(frontier) == 0 {
		return nil, nil
	}

	lowest := slices.Clone(frontier)
	sort.SliceStable(lowest, func(i, j int) bool {
		if lowest[i].TotalPrice.Amount != lowest[j].TotalPrice.Amount {
			return lowest[i].TotalPrice.Amount < lowest[j].TotalPrice.Amount
		}
		return lowest[i].Scores.PreferenceMatch > lowest[j].Scores.PreferenceMatch
	})

	bestMatch := slices.Clone(frontier)
	sort.SliceStable(bestMatch, func(i, j int) bool {
		if bestMatch[i].Scores.PreferenceMatch != bestMatch[j].Scores.PreferenceMatch {
			return bestMatch[i].Scores.PreferenceMatch > bestMatch[j].Scores.PreferenceMatch
		}
		if bestMatch[i].Scores.Waste != bestMatch[j].Scores.Waste {
			return bestMatch[i].Scores.Waste < bestMatch[j].Scores.Waste
		}
		return bestMatch[i].TotalPrice.Amount < bestMatch[j].TotalPrice.Amount
	})

	bestValue := slices.Clone(frontier)
	maxPrice := int64(1)
	for _, candidate := range frontier {
		maxPrice = max(maxPrice, candidate.TotalPrice.Amount)
	}
	sort.SliceStable(bestValue, func(i, j int) bool {
		left := valueScore(bestValue[i], weights, maxPrice)
		right := valueScore(bestValue[j], weights, maxPrice)
		if left != right {
			return left > right
		}
		return bestValue[i].TotalPrice.Amount < bestValue[j].TotalPrice.Amount
	})

	selections := []Result{
		{Label: LabelLowestPrice, Candidate: lowest[0]},
		{Label: LabelBestMatch, Candidate: bestMatch[0]},
		{Label: LabelBestValue, Candidate: bestValue[0]},
	}
	results := make([]Result, 0, len(selections))
	selected := make(map[string]struct{})
	for _, selection := range selections {
		if _, exists := selected[selection.Candidate.ID]; exists {
			continue
		}
		selected[selection.Candidate.ID] = struct{}{}
		results = append(results, selection)
	}
	return results, nil
}

func optionLeg(option CatalogOption) PurchaseLeg {
	codes := make([]string, 0, len(option.Products))
	for _, product := range option.Products {
		if product.ProductCode != "" {
			codes = append(codes, product.ProductCode)
		}
	}
	return PurchaseLeg{
		Channel:        option.Channel,
		OfferVersionID: option.OfferVersionID,
		Products:       slices.Clone(option.Products),
		ProductCodes:   codes,
		Price:          option.Price,
		Eligibility:    option.Eligibility,
		PurchaseURL:    option.PurchaseURL,
		Warnings:       slices.Clone(option.Warnings),
	}
}

func officialLegForRequests(requests []ProductRequest, official []CatalogOption) (PurchaseLeg, bool) {
	if len(requests) == 0 {
		return PurchaseLeg{}, true
	}
	leg := PurchaseLeg{Channel: OfficialChannel, Eligibility: EligibilityEligible}
	confidence := PriceLive
	for _, request := range requests {
		quantity := requestQuantity(request)
		matched := false
		for _, option := range official {
			if option.Eligibility == EligibilityIneligible || !productsMatchRequest(option.Products, request) {
				continue
			}
			for _, product := range option.Products {
				if productMatchesRequest(product, request) {
					product.Quantity = quantity
					leg.Products = appendOrIncreaseProduct(leg.Products, product)
					if product.ProductCode != "" {
						leg.ProductCodes = append(leg.ProductCodes, product.ProductCode)
					}
					break
				}
			}
			leg.Warnings = append(leg.Warnings, option.Warnings...)
			leg.Price.Amount.Amount += option.Price.Amount.Amount * int64(quantity)
			if leg.Price.Amount.Currency == "" {
				leg.Price.Amount.Currency = option.Price.Amount.Currency
			}
			if option.Price.QuotedAt.After(leg.Price.QuotedAt) {
				leg.Price.QuotedAt = option.Price.QuotedAt
			}
			if option.Eligibility == EligibilityUnknown {
				leg.Eligibility = EligibilityUnknown
			}
			confidence = leastConfidence(confidence, option.Price.Confidence)
			matched = true
			break
		}
		if !matched {
			return PurchaseLeg{}, false
		}
	}
	leg.Price.Confidence = confidence
	leg.Price.Source = OfficialChannel
	return leg, true
}

func newCandidate(legs []PurchaseLeg, budget *Money) Candidate {
	candidate := Candidate{PurchaseLegs: slices.Clone(legs)}
	candidate.TotalPrice = totalPrice(candidate.PurchaseLegs, budget)
	hash := sha256.Sum256([]byte(candidateKey(candidate)))
	candidate.ID = "candidate-" + hex.EncodeToString(hash[:8])
	return candidate
}

func candidateKey(candidate Candidate) string {
	parts := make([]string, 0)
	for _, leg := range candidate.PurchaseLegs {
		products := slices.Clone(leg.Products)
		sort.Slice(products, func(i, j int) bool {
			return products[i].CanonicalProductID+products[i].ProductCode < products[j].CanonicalProductID+products[j].ProductCode
		})
		legParts := []string{leg.Channel, leg.OfferVersionID}
		for _, product := range products {
			legParts = append(legParts, fmt.Sprintf("%s:%s:%d", product.CanonicalProductID, product.ProductCode, product.Quantity))
		}
		parts = append(parts, strings.Join(legParts, ":"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func missingProducts(requests []ProductRequest, products []CandidateProduct) []ProductRequest {
	available := productQuantities(products)
	missing := make([]ProductRequest, 0)
	for _, request := range requests {
		uncovered := consumeRequestQuantity(request, available)
		if uncovered == 0 {
			continue
		}
		copy := request
		copy.Quantity = uncovered
		missing = append(missing, copy)
	}
	return missing
}

func overlapsRequests(products []CandidateProduct, requests []ProductRequest) bool {
	for _, request := range requests {
		if productsMatchRequest(products, request) {
			return true
		}
	}
	return false
}

func productsMatchRequest(products []CandidateProduct, request ProductRequest) bool {
	for _, product := range products {
		if productMatchesRequest(product, request) {
			return true
		}
	}
	return false
}

func productMatchesRequest(product CandidateProduct, request ProductRequest) bool {
	productID := normalizeProductID(product.CanonicalProductID)
	for _, accepted := range requestProductIDs(request) {
		if productID == accepted {
			return true
		}
	}
	return false
}

func requestProductIDs(request ProductRequest) []string {
	ids := make([]string, 0, len(request.AllowedProductIDs)+2)
	if id := normalizeProductID(request.ResolvedProductID); id != "" {
		ids = append(ids, id)
	}
	for _, allowed := range request.AllowedProductIDs {
		if id := normalizeProductID(allowed); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		if query := normalizeProductID(request.Query); query != "" {
			ids = append(ids, query)
		}
	}
	return ids
}

func normalizeProductID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func requestQuantity(request ProductRequest) int {
	if request.Quantity <= 0 {
		return 1
	}
	return request.Quantity
}

func appendOrIncreaseProduct(products []CandidateProduct, product CandidateProduct) []CandidateProduct {
	for index := range products {
		if products[index].CanonicalProductID == product.CanonicalProductID && products[index].ProductCode == product.ProductCode {
			products[index].Quantity += product.Quantity
			return products
		}
	}
	return append(products, product)
}

func productQuantities(products []CandidateProduct) map[string]int {
	quantities := make(map[string]int)
	for _, product := range products {
		quantities[normalizeProductID(product.CanonicalProductID)] += max(1, product.Quantity)
	}
	return quantities
}

func candidateProducts(candidate Candidate) []CandidateProduct {
	products := make([]CandidateProduct, 0)
	for _, leg := range candidate.PurchaseLegs {
		products = append(products, leg.Products...)
	}
	return products
}

func matchingQuantity(request ProductRequest, quantities map[string]int) int {
	total := 0
	for _, id := range requestProductIDs(request) {
		total += quantities[id]
	}
	return total
}

func totalPrice(legs []PurchaseLeg, budget *Money) Money {
	result := Money{}
	if budget != nil {
		result.Currency = budget.Currency
	}
	for _, leg := range legs {
		result.Amount += leg.Price.Amount.Amount
		if result.Currency == "" && leg.Price.Amount.Currency != "" {
			result.Currency = leg.Price.Amount.Currency
		}
	}
	return result
}

func passesHardConstraints(requirement Requirement, candidate *Candidate) bool {
	currency := ""
	for _, leg := range candidate.PurchaseLegs {
		if leg.Eligibility == EligibilityIneligible {
			return false
		}
		if leg.Price.Amount.Amount < 0 {
			return false
		}
		if leg.Price.Amount.Currency != "" {
			if currency != "" && !strings.EqualFold(currency, leg.Price.Amount.Currency) {
				return false
			}
			currency = leg.Price.Amount.Currency
		}
	}
	quantities := productQuantities(candidateProducts(*candidate))
	available := cloneQuantities(quantities)
	covered := make([]string, 0, len(requirement.MustHave))
	for _, request := range requirement.MustHave {
		if consumeRequestQuantity(request, available) > 0 {
			return false
		}
		covered = append(covered, displayRequestID(request))
	}
	for _, excluded := range requirement.Exclude {
		if quantities[normalizeProductID(excluded)] > 0 {
			return false
		}
	}
	if requirement.Budget != nil {
		if candidate.TotalPrice.Amount > requirement.Budget.Amount {
			return false
		}
		if candidate.TotalPrice.Currency != "" && requirement.Budget.Currency != "" &&
			!strings.EqualFold(candidate.TotalPrice.Currency, requirement.Budget.Currency) {
			return false
		}
	}
	candidate.CoveredRequirementIDs = covered
	return true
}

func consumeRequestQuantity(request ProductRequest, available map[string]int) int {
	remaining := requestQuantity(request)
	for _, id := range requestProductIDs(request) {
		used := min(remaining, available[id])
		available[id] -= used
		remaining -= used
		if remaining == 0 {
			break
		}
	}
	return remaining
}

func cloneQuantities(source map[string]int) map[string]int {
	clone := make(map[string]int, len(source))
	for id, quantity := range source {
		clone[id] = quantity
	}
	return clone
}

func scoreCandidate(requirement Requirement, candidate Candidate) ScoreComponents {
	quantities := productQuantities(candidateProducts(candidate))
	mustTotal, mustCovered := requestCoverage(requirement.MustHave, quantities)
	niceTotal, niceCovered := requestCoverage(requirement.NiceToHave, quantities)
	preference := ratio(mustCovered, mustTotal)
	if niceTotal > 0 {
		preference = 0.7*preference + 0.3*ratio(niceCovered, niceTotal)
	}

	desired := make(map[string]struct{})
	for _, request := range append(slices.Clone(requirement.MustHave), requirement.NiceToHave...) {
		for _, id := range requestProductIDs(request) {
			desired[id] = struct{}{}
		}
	}
	totalItems, unwanted := 0, 0
	for id, quantity := range quantities {
		totalItems += quantity
		if _, wanted := desired[id]; !wanted {
			unwanted += quantity
		}
	}

	unknown := 0
	freshness := 0.0
	for _, leg := range candidate.PurchaseLegs {
		if leg.Eligibility == EligibilityUnknown {
			unknown++
		}
		freshness += confidenceRisk(leg.Price.Confidence)
	}
	legCount := max(1, len(candidate.PurchaseLegs))
	return ScoreComponents{
		PreferenceMatch: preference,
		Waste:           ratio(unwanted, totalItems),
		Convenience:     1 / float64(legCount),
		FreshnessRisk:   freshness / float64(legCount),
		EligibilityRisk: float64(unknown) / float64(legCount),
	}
}

func requestCoverage(requests []ProductRequest, quantities map[string]int) (int, int) {
	total, covered := 0, 0
	for _, request := range requests {
		quantity := requestQuantity(request)
		total += quantity
		covered += min(quantity, matchingQuantity(request, quantities))
	}
	return total, covered
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func confidenceRisk(confidence PriceConfidence) float64 {
	switch confidence {
	case PriceLive:
		return 0
	case PriceVerifiedToday:
		return 0.1
	case PriceVerifiedPast:
		return 0.4
	default:
		return 0.8
	}
}

func leastConfidence(left, right PriceConfidence) PriceConfidence {
	if confidenceRisk(right) > confidenceRisk(left) {
		return right
	}
	return left
}

func candidateReasons(requirement Requirement, candidate Candidate) []string {
	reasons := []string{"满足全部必选餐品"}
	if len(candidate.PurchaseLegs) == 1 {
		reasons = append(reasons, "仅需一次购买")
	}
	if requirement.Budget != nil {
		reasons = append(reasons, fmt.Sprintf("总价未超过预算 %d %s", requirement.Budget.Amount, requirement.Budget.Currency))
	}
	if len(requirement.NiceToHave) > 0 && candidate.Scores.PreferenceMatch > 0.99 {
		reasons = append(reasons, "同时覆盖偏好餐品")
	}
	return reasons
}

func unwantedProductIDs(requirement Requirement, candidate Candidate) []string {
	desired := make(map[string]struct{})
	for _, request := range append(slices.Clone(requirement.MustHave), requirement.NiceToHave...) {
		for _, id := range requestProductIDs(request) {
			desired[id] = struct{}{}
		}
	}
	unwanted := make([]string, 0)
	for id := range productQuantities(candidateProducts(candidate)) {
		if _, wanted := desired[id]; !wanted {
			unwanted = append(unwanted, id)
		}
	}
	sort.Strings(unwanted)
	return unwanted
}

func candidateWarnings(candidate Candidate) []string {
	warnings := slices.Clone(candidate.Warnings)
	for _, leg := range candidate.PurchaseLegs {
		warnings = append(warnings, leg.Warnings...)
		if leg.Eligibility == EligibilityUnknown {
			warnings = append(warnings, "部分购买资格尚未验证")
		}
		if confidenceRisk(leg.Price.Confidence) >= confidenceRisk(PriceVerifiedPast) {
			warnings = append(warnings, "价格并非实时核验")
		}
	}
	sort.Strings(warnings)
	return slices.Compact(warnings)
}

func paretoFrontier(candidates []Candidate) []Candidate {
	frontier := make([]Candidate, 0, len(candidates))
	for index, candidate := range candidates {
		dominated := false
		for otherIndex, other := range candidates {
			if index == otherIndex {
				continue
			}
			noWorse := other.TotalPrice.Amount <= candidate.TotalPrice.Amount &&
				other.Scores.PreferenceMatch >= candidate.Scores.PreferenceMatch &&
				other.Scores.Convenience >= candidate.Scores.Convenience
			strictlyBetter := other.TotalPrice.Amount < candidate.TotalPrice.Amount ||
				other.Scores.PreferenceMatch > candidate.Scores.PreferenceMatch ||
				other.Scores.Convenience > candidate.Scores.Convenience
			if noWorse && strictlyBetter {
				dominated = true
				break
			}
		}
		if !dominated {
			frontier = append(frontier, candidate)
		}
	}
	return frontier
}

func valueScore(candidate Candidate, weights ObjectiveWeights, maxPrice int64) float64 {
	priceScore := 1 - float64(candidate.TotalPrice.Amount)/float64(maxPrice)
	return weights.Price*priceScore +
		weights.PreferenceMatch*candidate.Scores.PreferenceMatch +
		weights.AvoidWaste*(1-candidate.Scores.Waste) +
		weights.Convenience*candidate.Scores.Convenience -
		candidate.Scores.FreshnessRisk - candidate.Scores.EligibilityRisk
}

func displayRequestID(request ProductRequest) string {
	if request.ResolvedProductID != "" {
		return request.ResolvedProductID
	}
	if len(request.AllowedProductIDs) > 0 {
		return request.AllowedProductIDs[0]
	}
	return request.Query
}

func cloneCandidate(candidate Candidate) Candidate {
	clone := candidate
	clone.PurchaseLegs = slices.Clone(candidate.PurchaseLegs)
	for index := range clone.PurchaseLegs {
		clone.PurchaseLegs[index].Products = slices.Clone(candidate.PurchaseLegs[index].Products)
		clone.PurchaseLegs[index].ProductCodes = slices.Clone(candidate.PurchaseLegs[index].ProductCodes)
		clone.PurchaseLegs[index].Warnings = slices.Clone(candidate.PurchaseLegs[index].Warnings)
	}
	clone.CoveredRequirementIDs = slices.Clone(candidate.CoveredRequirementIDs)
	clone.UnwantedProductIDs = slices.Clone(candidate.UnwantedProductIDs)
	clone.Reasons = slices.Clone(candidate.Reasons)
	clone.Warnings = slices.Clone(candidate.Warnings)
	return clone
}
