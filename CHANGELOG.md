## 0.2.2 (Unreleased)

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
