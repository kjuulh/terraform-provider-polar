## 0.2.5 (Unreleased)

ENHANCEMENTS:

- **resource/polar_product:** Add `metadata_json` for typed scalar metadata
  without converting integers to strings or losing int64 precision. It is
  mutually exclusive with the existing `metadata` string map. Imports populate
  `metadata_json`; use that attribute when adopting products with numeric metadata.
- **resource/polar_product:** Add `prices.tax_behavior` and
  `recurring_interval_count`. Unchanged active prices retain their IDs on product
  updates, including visibility changes. Archived prices are excluded from state
  and are never reused.
- **resource/polar_discount:** Add `max_redemptions_per_customer` for create,
  update, refresh, and import. Removing the attribute clears the per-customer
  limit. Unrestricted product scope remains unchanged.

VERIFICATION:

- Offline Terraform acceptance scenarios adopt existing products and nested
  prices, meters, discounts, and webhook endpoints through fake HTTP servers,
  including typed metadata, caps, explicit tax behavior, and future webhook events.
- CI adds an offline acceptance job without Polar credentials, alongside the
  unchanged secret-gated sandbox acceptance job. The sandbox suite remains
  serialized and runs when its secret is available. No live compatibility or
  catalog migration is claimed by the fake-server suite.

## 0.2.3 (2026-10-13)

BUG FIXES:

- **resource/polar_webhook_endpoint:** Import and refresh preserve webhook
  events not yet listed in polar-go, including `subscription.cycled`. Updates
  retain these event names instead of failing to decode the API response.

## 0.2.2 (2026-09-20)

ENHANCEMENTS:

- **resource/polar_organization:** Add `default_presentment_currency`. Polar
  requires an organization's default presentment currency to appear among a
  product's prices, so an organization selling a product priced only in EUR
  has to be set to `eur` — previously unreachable from Terraform, leaving
  `polar_product` creation failing with "The organization's default
  presentment currency must be present in the prices." The field is optional
  and computed, so omitting it leaves the organization's current value alone.

## 0.1.0 (Unreleased)

FEATURES:

- **New Resource:** `polar_webhook_endpoint` — Manage webhook endpoints with support for raw, Discord, and Slack formats
- **New Resource:** `polar_meter` — Track usage events with configurable filters and aggregation functions
- **New Resource:** `polar_benefit` — Define benefits including custom, Discord, GitHub repository, downloadables, license keys, and meter credits
- **New Resource:** `polar_product` — Manage products with fixed, free, custom, metered, and seat-based pricing models
- **New Data Source:** `polar_meter` — Fetch an existing meter by ID
- **New Data Source:** `polar_benefit` — Fetch an existing benefit by ID
