package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	appoffers "meal-recommendation/internal/application/offers"
	apprec "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type offerCacheEntry struct {
	value         []apprec.OfferVersion
	loadedAt      time.Time
	expiresAt     time.Time
	hardExpiresAt time.Time
}

type OfferRepository struct {
	db         *sql.DB
	now        func() time.Time
	staleAfter time.Duration
	cacheTTL   time.Duration
	staleTTL   time.Duration
	cacheMu    sync.RWMutex
	cache      map[string]offerCacheEntry
}

func NewOfferRepository(db *sql.DB, now func() time.Time, staleAfter, cacheTTL, staleTTL time.Duration) *OfferRepository {
	return &OfferRepository{db: db, now: now, staleAfter: staleAfter, cacheTTL: cacheTTL, staleTTL: staleTTL, cache: make(map[string]offerCacheEntry)}
}

func (r *OfferRepository) Create(ctx context.Context, offer *appoffers.Offer) error {
	payload, err := json.Marshal(offer)
	if err != nil {
		return fmt.Errorf("marshal offer: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO offers (id,status,version,draft,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		offer.ID, offer.Status, offer.Version, payload, offer.CreatedAt, offer.UpdatedAt)
	if err == nil {
		r.invalidate()
	}
	return err
}

func (r *OfferRepository) Get(ctx context.Context, id string) (*appoffers.Offer, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT draft FROM offers WHERE id=$1`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, appoffers.ErrOfferNotFound
	}
	if err != nil {
		return nil, err
	}
	var offer appoffers.Offer
	if err := json.Unmarshal(payload, &offer); err != nil {
		return nil, fmt.Errorf("decode offer: %w", err)
	}
	return &offer, nil
}

func (r *OfferRepository) List(ctx context.Context) ([]*appoffers.Offer, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT draft FROM offers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offers := make([]*appoffers.Offer, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var offer appoffers.Offer
		if err := json.Unmarshal(payload, &offer); err != nil {
			return nil, fmt.Errorf("decode offer: %w", err)
		}
		offers = append(offers, &offer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return offers, nil
}

func (r *OfferRepository) Save(ctx context.Context, offer *appoffers.Offer) error {
	payload, err := json.Marshal(offer)
	if err != nil {
		return fmt.Errorf("marshal offer: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE offers SET status=$2,version=$3,draft=$4,updated_at=$5 WHERE id=$1`,
		offer.ID, offer.Status, offer.Version, payload, offer.UpdatedAt)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows == 0 {
		return appoffers.ErrOfferNotFound
	}
	r.invalidate()
	return err
}

func (r *OfferRepository) Publish(ctx context.Context, offer *appoffers.Offer) error {
	payload, err := json.Marshal(offer)
	if err != nil {
		return fmt.Errorf("marshal offer: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE offers SET status=$2,version=$3,draft=$4,updated_at=$5 WHERE id=$1`,
		offer.ID, offer.Status, offer.Version, payload, offer.UpdatedAt)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return appoffers.ErrOfferNotFound
	}
	validTimes, err := json.Marshal(offer.ValidTimes)
	if err != nil {
		return fmt.Errorf("marshal valid time rules: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO offer_versions (id,offer_id,version,snapshot,published_at,valid_from,valid_until,valid_time_rules,verified_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		offer.VersionID, offer.ID, offer.Version, payload, offer.UpdatedAt, offer.ValidFrom, offer.ValidUntil, validTimes, offer.VerifiedAt)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.invalidate()
	return nil
}

func (r *OfferRepository) ListEligible(ctx context.Context, requirement domain.Requirement) ([]apprec.OfferVersion, error) {
	key := offerCacheKey(requirement)
	now := r.now().UTC()
	effectiveAt := now
	if requirement.TargetTime != nil {
		effectiveAt = *requirement.TargetTime
	}
	if cached, ok := r.cached(key, now, false); ok {
		return cached, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT v.id, v.snapshot FROM offers o JOIN offer_versions v ON v.offer_id=o.id AND v.version=o.version WHERE o.status='published' AND (v.valid_from IS NULL OR v.valid_from <= $1) AND (v.valid_until IS NULL OR v.valid_until > $1)`, effectiveAt)
	if err != nil {
		if cached, ok := r.cached(key, now, true); ok {
			return markOffersStale(cached), nil
		}
		return nil, err
	}
	defer rows.Close()
	result := make([]apprec.OfferVersion, 0)
	hardExpiresAt := time.Time{}
	for rows.Next() {
		var payload []byte
		var versionID string
		if err := rows.Scan(&versionID, &payload); err != nil {
			return nil, err
		}
		var offer appoffers.Offer
		if err := json.Unmarshal(payload, &offer); err != nil {
			return nil, fmt.Errorf("decode offer version: %w", err)
		}
		offer.VersionID = versionID
		if offerMatches(&offer, requirement, effectiveAt) {
			result = append(result, toRecommendationOffer(&offer, r.staleAfter, now))
			if requirement.TargetTime == nil && offer.ValidUntil != nil && (hardExpiresAt.IsZero() || offer.ValidUntil.Before(hardExpiresAt)) {
				hardExpiresAt = *offer.ValidUntil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.storeCache(key, result, now, hardExpiresAt)
	return cloneVersions(result), nil
}

func (r *OfferRepository) cached(key string, now time.Time, allowStale bool) ([]apprec.OfferVersion, bool) {
	r.cacheMu.RLock()
	entry, ok := r.cache[key]
	r.cacheMu.RUnlock()
	if !ok || (!entry.hardExpiresAt.IsZero() && !now.Before(entry.hardExpiresAt)) || (!allowStale && !now.Before(entry.expiresAt)) || (allowStale && now.Sub(entry.loadedAt) > r.staleTTL) {
		return nil, false
	}
	return cloneVersions(entry.value), true
}

func (r *OfferRepository) storeCache(key string, value []apprec.OfferVersion, now, hardExpiresAt time.Time) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.cache[key] = offerCacheEntry{value: cloneVersions(value), loadedAt: now, expiresAt: now.Add(r.cacheTTL), hardExpiresAt: hardExpiresAt}
}

func (r *OfferRepository) invalidate() {
	r.cacheMu.Lock()
	r.cache = make(map[string]offerCacheEntry)
	r.cacheMu.Unlock()
}

func offerMatches(offer *appoffers.Offer, requirement domain.Requirement, effectiveAt time.Time) bool {
	if !appoffers.MatchesValidTime(offer.ValidTimes, effectiveAt) {
		return false
	}
	if offer.City != "" && requirement.Location.City != "" && !strings.EqualFold(offer.City, requirement.Location.City) {
		return false
	}
	if len(offer.StoreCodes) > 0 && requirement.Location.StoreCode != "" && !slices.Contains(offer.StoreCodes, requirement.Location.StoreCode) {
		return false
	}
	return requirement.OrderMode == "" || len(offer.OrderModes) == 0 || slices.Contains(offer.OrderModes, requirement.OrderMode)
}

func toRecommendationOffer(offer *appoffers.Offer, staleAfter time.Duration, now time.Time) apprec.OfferVersion {
	components := make([]apprec.OfferComponent, 0, len(offer.Components))
	for _, item := range offer.Components {
		components = append(components, apprec.OfferComponent{CanonicalProductID: item.CanonicalProductID, Quantity: item.Quantity, ChoiceGroupID: item.ChoiceGroupID, ExtraPrice: item.ExtraPrice})
	}
	confidence, quotedAt := domain.PriceVerifiedPast, offer.UpdatedAt
	warnings := []string(nil)
	if offer.VerifiedAt != nil {
		quotedAt = *offer.VerifiedAt
		if staleAfter <= 0 || now.Sub(*offer.VerifiedAt) <= staleAfter {
			confidence = domain.PriceVerifiedToday
		} else {
			warnings = append(warnings, "人工套餐价格已超过复核时限")
		}
	}
	groups := make([]apprec.OfferChoiceGroup, 0, len(offer.ChoiceGroups))
	for _, group := range offer.ChoiceGroups {
		groups = append(groups, apprec.OfferChoiceGroup{ID: group.ID, Name: group.Name, MinSelect: group.MinSelect, MaxSelect: group.MaxSelect})
	}
	if len(offer.OrderModes) > 0 {
		modes := make([]string, len(offer.OrderModes))
		for i, mode := range offer.OrderModes {
			modes[i] = string(mode)
		}
		warnings = append(warnings, "套餐适用取餐方式："+strings.Join(modes, ", "))
	}
	return apprec.OfferVersion{ID: offer.VersionID, Source: offer.Source, Title: offer.Title, Price: domain.PriceQuote{Amount: offer.Price, Confidence: confidence, QuotedAt: quotedAt, Source: offer.Source}, ChoiceGroups: groups, Components: components, Eligibility: domain.EligibilityEligible, PurchaseURL: offer.PurchaseURL, Warnings: warnings}
}

func offerCacheKey(r domain.Requirement) string {
	target := "now"
	if r.TargetTime != nil {
		target = r.TargetTime.Format(time.RFC3339)
	}
	return strings.Join([]string{string(r.OrderMode), r.Location.City, r.Location.StoreCode, target}, "|")
}
func cloneVersions(value []apprec.OfferVersion) []apprec.OfferVersion {
	data, _ := json.Marshal(value)
	var clone []apprec.OfferVersion
	_ = json.Unmarshal(data, &clone)
	return clone
}
func markOffersStale(value []apprec.OfferVersion) []apprec.OfferVersion {
	for i := range value {
		value[i].Warnings = append(value[i].Warnings, "套餐数据库不可用，使用缓存数据")
		value[i].Price.Confidence = domain.PriceVerifiedPast
	}
	return value
}

var _ appoffers.Repository = (*OfferRepository)(nil)
var _ apprec.OfferCatalog = (*OfferRepository)(nil)
