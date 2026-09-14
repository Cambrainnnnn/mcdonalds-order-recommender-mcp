package offers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

func digest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BusinessKey excludes operational metadata and ordering. Group labels and IDs
// do not change the offer's contents; membership and all selection rules do.
func BusinessKey(o *Offer) string {
	source := strings.ToLower(strings.TrimSpace(o.Source))
	if o.ExternalID != "" {
		return "external:" + digest([]string{source, strings.TrimSpace(o.ExternalID)})
	}
	normalizeComponents := func(items []Component) []string {
		result := make([]string, 0, len(items))
		for _, c := range items {
			c.ChoiceGroupID = ""
			if c.ExtraPriceMode == "per_selection" {
				c.ExtraPriceMode = ""
			}
			result = append(result, digest(c))
		}
		sort.Strings(result)
		return result
	}
	fixed := []Component{}
	grouped := map[string][]Component{}
	for _, c := range o.Components {
		if len(o.ChoiceGroups) == 0 || c.ChoiceGroupID == "" {
			if len(o.ChoiceGroups) == 0 && c.ChoiceGroupID != "" {
				c.ExtraPrice = 0
			}
			fixed = append(fixed, c)
		} else {
			grouped[c.ChoiceGroupID] = append(grouped[c.ChoiceGroupID], c)
		}
	}
	groups := []string{}
	for _, g := range o.ChoiceGroups {
		members := normalizeComponents(grouped[g.ID])
		g.ID = ""
		g.Name = ""
		groups = append(groups, digest(struct {
			Rule    ChoiceGroup
			Members []string
		}{g, members}))
	}
	sort.Strings(groups)
	stores := append([]string{}, o.StoreCodes...)
	sort.Strings(stores)
	modes := append([]string{}, func() []string {
		v := []string{}
		for _, m := range o.OrderModes {
			v = append(v, string(m))
		}
		return v
	}()...)
	sort.Strings(modes)
	times := []string{}
	for _, v := range o.ValidTimes {
		v.Weekdays = append([]int{}, v.Weekdays...)
		sort.Ints(v.Weekdays)
		times = append(times, digest(v))
	}
	sort.Strings(times)
	from, until := "", ""
	if o.ValidFrom != nil {
		from = o.ValidFrom.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if o.ValidUntil != nil {
		until = o.ValidUntil.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return "content:" + digest([]any{source, strings.ToLower(strings.Join(strings.Fields(o.Title), " ")), o.Price, strings.ToLower(strings.TrimSpace(o.City)), stores, modes, from, until, times, normalizeComponents(fixed), groups})
}
