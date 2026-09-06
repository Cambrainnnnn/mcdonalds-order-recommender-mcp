package recommendation

import "time"

type PriceConfidence string

const (
	PriceLive          PriceConfidence = "live"
	PriceVerifiedToday PriceConfidence = "verified_today"
	PriceVerifiedPast  PriceConfidence = "verified_past"
	PriceUnverified    PriceConfidence = "unverified"
)

type EligibilityStatus string

const (
	EligibilityEligible   EligibilityStatus = "eligible"
	EligibilityIneligible EligibilityStatus = "ineligible"
	EligibilityUnknown    EligibilityStatus = "unknown"
)

type PriceQuote struct {
	Amount     Money           `json:"amount"`
	Confidence PriceConfidence `json:"confidence"`
	QuotedAt   time.Time       `json:"quotedAt"`
	Source     string          `json:"source"`
}

// CandidateProduct keeps the canonical product used by recommendation rules
// separate from the channel-specific product code used for purchasing/pricing.
type CandidateProduct struct {
	CanonicalProductID string `json:"canonicalProductId"`
	ProductCode        string `json:"productCode,omitempty"`
	Quantity           int    `json:"quantity"`
	ChoiceGroupID      string `json:"choiceGroupId,omitempty"`
	ExtraPrice         int64  `json:"extraPrice,omitempty"`
}

type PurchaseLeg struct {
	Channel        string             `json:"channel"`
	OfferVersionID string             `json:"offerVersionId,omitempty"`
	Products       []CandidateProduct `json:"products"`
	ProductCodes   []string           `json:"productCodes,omitempty"`
	Price          PriceQuote         `json:"price"`
	Eligibility    EligibilityStatus  `json:"eligibility"`
	PurchaseURL    string             `json:"purchaseUrl,omitempty"`
	Warnings       []string           `json:"warnings,omitempty"`
}

type ScoreComponents struct {
	PreferenceMatch float64 `json:"preferenceMatch"`
	Waste           float64 `json:"waste"`
	Convenience     float64 `json:"convenience"`
	FreshnessRisk   float64 `json:"freshnessRisk"`
	EligibilityRisk float64 `json:"eligibilityRisk"`
}

type Candidate struct {
	ID                    string          `json:"id"`
	PurchaseLegs          []PurchaseLeg   `json:"purchaseLegs"`
	TotalPrice            Money           `json:"totalPrice"`
	CoveredRequirementIDs []string        `json:"coveredRequirementIds"`
	UnwantedProductIDs    []string        `json:"unwantedProductIds,omitempty"`
	Scores                ScoreComponents `json:"scores"`
	Reasons               []string        `json:"reasons,omitempty"`
	Warnings              []string        `json:"warnings,omitempty"`
}

type RecommendationLabel string

const (
	LabelLowestPrice RecommendationLabel = "lowest_price"
	LabelBestMatch   RecommendationLabel = "best_match"
	LabelBestValue   RecommendationLabel = "best_value"
)

type Result struct {
	Label     RecommendationLabel `json:"label"`
	Candidate Candidate           `json:"candidate"`
}
