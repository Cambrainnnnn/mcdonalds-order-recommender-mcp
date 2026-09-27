package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	offers "meal-recommendation/internal/application/offers"
	"meal-recommendation/internal/domain/identity"
	"meal-recommendation/internal/domain/product"
)

// Catalog is a portable business snapshot, not a physical database backup.
// Version metadata is kept separately because it need not equal draft metadata.
type Catalog struct {
	FormatVersion int                `json:"formatVersion"`
	Products      []*product.Product `json:"products"`
	Offers        []*offers.Offer    `json:"offers"`
	Versions      []CatalogVersion   `json:"offerVersions"`
}

type CatalogVersion struct {
	ID          string                 `json:"id"`
	OfferID     string                 `json:"offerId"`
	Version     int                    `json:"version"`
	Snapshot    offers.Offer           `json:"snapshot"`
	PublishedAt time.Time              `json:"publishedAt"`
	ValidFrom   *time.Time             `json:"validFrom,omitempty"`
	ValidUntil  *time.Time             `json:"validUntil,omitempty"`
	ValidTimes  []offers.ValidTimeRule `json:"validTimes"`
	VerifiedAt  *time.Time             `json:"verifiedAt,omitempty"`
}

func ReadCatalog(path string) (*Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeCatalog(f)
}

func DecodeCatalog(r io.Reader) (*Catalog, error) {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	var c Catalog
	if err := d.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("catalog must contain exactly one JSON object")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) Validate() error {
	if c == nil || c.FormatVersion != 1 {
		return fmt.Errorf("unsupported catalog formatVersion (expected 1)")
	}
	if c.Products == nil || c.Offers == nil || c.Versions == nil {
		return fmt.Errorf("products, offers and offerVersions arrays are required")
	}
	products := map[string]bool{}
	for _, p := range c.Products {
		if p == nil || !identity.Valid(p.ID) {
			return fmt.Errorf("invalid product ID")
		}
		if products[p.ID] {
			return fmt.Errorf("duplicate product %s", p.ID)
		}
		// Validate a copy; importing must not silently rewrite the snapshot.
		copy := *p
		copy.Aliases = append([]string(nil), p.Aliases...)
		copy.SalesScopes = append([]product.SalesScope(nil), p.SalesScopes...)
		if p.Price != nil {
			price := *p.Price
			copy.Price = &price
		}
		if err := copy.NormalizeAndValidate(); err != nil {
			return fmt.Errorf("product %s: %w", p.ID, err)
		}
		if p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
			return fmt.Errorf("invalid timestamps for product %s", p.ID)
		}
		products[p.ID] = true
	}
	validateOffer := func(o *offers.Offer) error {
		if o == nil || !identity.Valid(o.ID) || o.Version < 0 {
			return fmt.Errorf("invalid offer identity")
		}
		if err := offers.ValidateSnapshot(o); err != nil {
			return fmt.Errorf("offer %s: %w", o.ID, err)
		}
		if o.CreatedAt.IsZero() || o.UpdatedAt.Before(o.CreatedAt) {
			return fmt.Errorf("invalid timestamps for offer %s", o.ID)
		}
		switch o.Status {
		case offers.StatusDraft, offers.StatusReviewed, offers.StatusPublished, offers.StatusDisabled:
		default:
			return fmt.Errorf("invalid status for offer %s", o.ID)
		}
		if o.Version > 0 && !identity.Valid(o.VersionID) || o.Version == 0 && o.VersionID != "" {
			return fmt.Errorf("invalid version ID for offer %s", o.ID)
		}
		for _, component := range o.Components {
			if !products[component.CanonicalProductID] {
				return fmt.Errorf("offer %s references missing product %s", o.ID, component.CanonicalProductID)
			}
			if component.ChoiceGroupID != "" && !identity.Valid(component.ChoiceGroupID) {
				return fmt.Errorf("invalid choice group in offer %s", o.ID)
			}
		}
		for _, group := range o.ChoiceGroups {
			if !identity.Valid(group.ID) {
				return fmt.Errorf("invalid choice group in offer %s", o.ID)
			}
		}
		return nil
	}
	current := map[string]*offers.Offer{}
	for _, o := range c.Offers {
		if err := validateOffer(o); err != nil {
			return err
		}
		if current[o.ID] != nil {
			return fmt.Errorf("duplicate offer %s", o.ID)
		}
		current[o.ID] = o
	}
	versions := map[string]bool{}
	numbers := map[string]map[int]string{}
	for _, v := range c.Versions {
		if err := validateOffer(&v.Snapshot); err != nil {
			return err
		}
		o := current[v.OfferID]
		if !identity.Valid(v.ID) || o == nil || v.Version < 1 || v.Version > o.Version || v.PublishedAt.IsZero() || v.Snapshot.ID != v.OfferID || v.Snapshot.VersionID != v.ID || v.Snapshot.Version != v.Version {
			return fmt.Errorf("invalid offer version %s", v.ID)
		}
		if numbers[v.OfferID] == nil {
			numbers[v.OfferID] = map[int]string{}
		}
		if versions[v.ID] || numbers[v.OfferID][v.Version] != "" {
			return fmt.Errorf("duplicate offer version %s", v.ID)
		}
		versions[v.ID] = true
		numbers[v.OfferID][v.Version] = v.ID
	}
	for _, o := range c.Offers {
		if o.Status == offers.StatusPublished && o.Version == 0 {
			return fmt.Errorf("published offer %s has no version", o.ID)
		}
		if len(numbers[o.ID]) != o.Version || o.Version > 0 && numbers[o.ID][o.Version] != o.VersionID {
			return fmt.Errorf("incomplete version history for offer %s", o.ID)
		}
	}
	return nil
}

// ExportCatalog reads all tables in one consistent, read-only snapshot.
func ExportCatalog(ctx context.Context, db *sql.DB) (*Catalog, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := readCatalog(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = c.Validate(); err != nil {
		return nil, fmt.Errorf("database catalog cannot be exported: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}

func readCatalog(ctx context.Context, tx *sql.Tx) (*Catalog, error) {
	c := &Catalog{FormatVersion: 1, Versions: []CatalogVersion{}}
	var err error
	c.Products, err = (&ProductRepository{db: tx}).List(ctx)
	if err != nil {
		return nil, err
	}
	c.Offers, err = (&OfferRepository{tx: tx}).List(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,offer_id,version,snapshot,published_at,valid_from,valid_until,valid_time_rules,verified_at FROM offer_versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v CatalogVersion
		var snapshot, rules []byte
		if err = rows.Scan(&v.ID, &v.OfferID, &v.Version, &snapshot, &v.PublishedAt, &v.ValidFrom, &v.ValidUntil, &rules, &v.VerifiedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(snapshot, &v.Snapshot); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(rules, &v.ValidTimes); err != nil {
			return nil, err
		}
		c.Versions = append(c.Versions, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	c.canonicalize()
	return c, nil
}

func utc(t *time.Time) {
	if t != nil {
		*t = t.UTC()
	}
}
func normalizeOffer(o *offers.Offer) {
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	utc(o.ValidFrom)
	utc(o.ValidUntil)
	utc(o.VerifiedAt)
}
func (c *Catalog) canonicalize() {
	for _, p := range c.Products {
		p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	}
	for _, o := range c.Offers {
		normalizeOffer(o)
	}
	for i := range c.Versions {
		v := &c.Versions[i]
		normalizeOffer(&v.Snapshot)
		v.PublishedAt = v.PublishedAt.UTC()
		utc(v.ValidFrom)
		utc(v.ValidUntil)
		utc(v.VerifiedAt)
	}
	sort.Slice(c.Products, func(i, j int) bool { return c.Products[i].ID < c.Products[j].ID })
	sort.Slice(c.Offers, func(i, j int) bool { return c.Offers[i].ID < c.Offers[j].ID })
	sort.Slice(c.Versions, func(i, j int) bool {
		a, b := c.Versions[i], c.Versions[j]
		if a.OfferID != b.OfferID {
			return a.OfferID < b.OfferID
		}
		return a.Version < b.Version
	})
}

func (c *Catalog) Write(w io.Writer) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c.canonicalize()
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	e.SetEscapeHTML(false)
	return e.Encode(c)
}

func same(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }

// ImportCatalog never modifies immutable versions or deletes absent records.
// The bool is false only when if-empty skips an existing business catalog.
func ImportCatalog(ctx context.Context, db *sql.DB, c *Catalog, mode string) (bool, error) {
	if mode != "merge" && mode != "if-empty" {
		return false, fmt.Errorf("invalid catalog import mode %q", mode)
	}
	if err := c.Validate(); err != nil {
		return false, err
	}
	c.canonicalize()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Same writer lock order as aggregate ingestion; readers remain available.
	if _, err = tx.ExecContext(ctx, `LOCK TABLE offers, products, offer_ingestion_requests, offer_versions, offer_business_keys IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return false, err
	}
	old, err := readCatalog(ctx, tx)
	if err != nil {
		return false, err
	}
	if mode == "if-empty" && (len(old.Products) > 0 || len(old.Offers) > 0 || len(old.Versions) > 0) {
		return false, tx.Commit()
	}
	products := map[string]*product.Product{}
	for _, p := range old.Products {
		products[p.ID] = p
	}
	repo := &ProductRepository{db: tx}
	for _, p := range c.Products {
		if previous := products[p.ID]; previous != nil {
			if same(previous, p) {
				continue
			}
			if !previous.CreatedAt.Equal(p.CreatedAt) || !p.UpdatedAt.After(previous.UpdatedAt) {
				return false, fmt.Errorf("product %s conflicts with database (creation time differs or database is equally/newer updated)", p.ID)
			}
			err = repo.Save(ctx, p)
		} else {
			err = repo.Create(ctx, p)
		}
		if err != nil {
			return false, fmt.Errorf("import product %s: %w", p.ID, err)
		}
	}
	current := map[string]*offers.Offer{}
	oldKeys := map[string][]string{}
	for _, o := range old.Offers {
		current[o.ID] = o
		key := offers.BusinessKey(o)
		oldKeys[key] = append(oldKeys[key], o.ID)
	}
	for _, o := range c.Offers {
		key := offers.BusinessKey(o)
		previous := current[o.ID]
		if previous == nil || offers.BusinessKey(previous) != key {
			for _, otherID := range oldKeys[key] {
				if otherID != o.ID {
					return false, fmt.Errorf("offer %s has the same business identity as database offer %s", o.ID, otherID)
				}
			}
		}
		if previous := current[o.ID]; previous != nil {
			if same(previous, o) {
				continue
			}
			if !previous.CreatedAt.Equal(o.CreatedAt) || previous.Version > o.Version || !o.UpdatedAt.After(previous.UpdatedAt) {
				return false, fmt.Errorf("offer %s conflicts with database (creation time differs or database has equal/newer data)", o.ID)
			}
		}
		payload, e := json.Marshal(o)
		if e != nil {
			return false, e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO offers (id,status,version,draft,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status,version=EXCLUDED.version,draft=EXCLUDED.draft,updated_at=EXCLUDED.updated_at`, o.ID, o.Status, o.Version, payload, o.CreatedAt, o.UpdatedAt)
		if err != nil {
			return false, fmt.Errorf("import offer %s: %w", o.ID, err)
		}
	}
	versions := map[string]CatalogVersion{}
	for _, v := range old.Versions {
		versions[v.ID] = v
	}
	for _, v := range c.Versions {
		if previous, ok := versions[v.ID]; ok {
			if !same(previous, v) {
				return false, fmt.Errorf("immutable offer version %s conflicts with database", v.ID)
			}
			continue
		}
		payload, e := json.Marshal(v.Snapshot)
		if e != nil {
			return false, e
		}
		rules, e := json.Marshal(v.ValidTimes)
		if e != nil {
			return false, e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO offer_versions (id,offer_id,version,snapshot,published_at,valid_from,valid_until,valid_time_rules,verified_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, v.ID, v.OfferID, v.Version, payload, v.PublishedAt, v.ValidFrom, v.ValidUntil, rules, v.VerifiedAt)
		if err != nil {
			return false, fmt.Errorf("import version %s: %w", v.ID, err)
		}
	}
	// Preserve legacy duplicate identities using the same oldest-owner rule as migration.
	if _, err = tx.ExecContext(ctx, `DELETE FROM offer_business_keys`); err != nil {
		return false, err
	}
	if err = backfillOfferKeys(ctx, tx); err != nil {
		return false, err
	}
	// Imported IDs include nested choice groups. Never move the durable clock backwards.
	var maxID int64
	track := func(id string) {
		n, _ := strconv.ParseInt(id, 10, 64)
		if n > maxID {
			maxID = n
		}
	}
	for _, p := range c.Products {
		track(p.ID)
	}
	trackOffer := func(o *offers.Offer) {
		track(o.ID)
		track(o.VersionID)
		for _, g := range o.ChoiceGroups {
			track(g.ID)
		}
		for _, p := range o.Components {
			track(p.CanonicalProductID)
			track(p.ChoiceGroupID)
		}
	}
	for _, o := range c.Offers {
		trackOffer(o)
	}
	for _, v := range c.Versions {
		track(v.ID)
		trackOffer(&v.Snapshot)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE snowflake_state SET last_ms=$1, sequence=4095 WHERE singleton AND last_ms <= $1`, maxID>>22); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
