---
name: meal-recommendation
description: Recommend meal combinations from the server-maintained meal-recommendation catalog and published offers.
---

# Server catalog meal recommendations

Use the meal-recommendation MCP for meal and combo recommendations. Current scope is the server's maintained products, prices and published offers. City, store, store preparation, inventory and official live pricing are future extensions. Do not ask for city, store or location. Do not claim store availability or official quotes.

## Workflow

1. Resolve named products using `search_product_catalog` (complete name, optional specification). Use `list_product_catalog` only when browsing or identifying the complete name is needed. Both include disabled products: status does not prevent a product from being selected within a published offer.
2. Only an `exact` result supplies `resolvedProductId`. If specifications are ambiguous, ask which the user wants; do not default to a large drink. Use `allowedProductIds` only when the user has accepted those alternatives. Never copy IDs from old conversations or this skill.
3. Start a session, then replace its requirement using `update_meal_requirement`. Include `mustHave` entries with `query`, `quantity` and resolved IDs. Reuse the default `objectives` returned by the session. Location may be omitted. `orderMode` is optional: only include it if the user specified a preference, otherwise omit it without asking.
4. Once at least one must-have item is present, the session can be `ready`. Generate recommendations and report server-maintained prices and applicable restrictions. Published offers retain their maintained time rules; use `targetTime` if the user specifies a meal time.

Standalone products are eligible only when active, marked `standalone`, and priced. A disabled product or one without a standalone price can still be included in an eligible published offer. A catalog match establishes identity, not purchasability.

If no candidates are returned, say the maintained data and supplied requirements did not yield a combination. Do not infer that a store lacks stock. Do not probe random cities, stores or order modes. An expired session can be recreated with the same user requirements.
